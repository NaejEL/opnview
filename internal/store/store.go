// Package store opens the SQLite database, applies the embedded schema, and
// holds the typed write paths the collectors use.
//
// The schema is one file, applied in place and applied on every start. There is
// no migration runner and no schema_version table: nothing is deployed and
// nobody has data, so numbered files would be machinery for a problem that does
// not exist. Every statement in schema.sql is idempotent, which is what makes
// applying it on every start safe. Migrations begin the day the product runs
// somewhere with data worth keeping — schema.sql becomes the baseline and the
// first real migration is the one after it.
//
// The driver is modernc.org/sqlite, pure Go, so CGO_ENABLED stays 0 and the
// deployment target needs no compiler.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	// The SQLite driver. Pure Go, so nothing here needs cgo.
	_ "modernc.org/sqlite"
)

// The schema and the purge, embedded. They live in this directory rather than
// under sql/ because Go's embed directive cannot reach outside its own package,
// and a second copy under sql/ would be a second truth. sql/schema-checks.sh
// reads these same two files.
//
//go:embed schema.sql purge.sql
var sqlFiles embed.FS

// DatabaseFilename is the name of the database inside the data directory.
const DatabaseFilename = "opnview.db"

// Store is an open database.
type Store struct {
	db *sql.DB
}

// Open opens, or creates, the database in dataDir and applies the schema.
//
// dataDir has no default anywhere in opnview: it is a required flag. A program
// that silently writes a database somewhere plausible is worse than one that
// refuses to start, and a default here would pre-empt the directory layout
// step 8 chooses for the LXC.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("store: no data directory was given")
	}
	// The directory is created rather than required. A first start against a
	// fresh installation has nothing there yet, and refusing to create the one
	// directory the operator just named on the command line would be refusing to
	// start for no reason. 0700 because the database and the key file beside it
	// are the two things in this product that must not be world-readable.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: creating the data directory %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, DatabaseFilename)

	// WAL so a reader never blocks the writer; a busy timeout so a checkpoint
	// never turns into an immediate failure; foreign keys on, because every
	// "not found is a state" guarantee in the model rests on them.
	//
	// secure_delete is FAST, and the guarantee it gives is the weaker one. It
	// zeroes what is freed INSIDE A PAGE THAT IS BEING REWRITTEN, and nothing
	// else: a page that goes back to the freelist whole is unlinked and left with
	// its bytes in the file until something reuses it. The strong form zeroes
	// every freed page of every table on every delete and update, and the purge
	// loops delete continuously, which is a write amplification paid on all the
	// measurement tables to protect the two credential rows.
	//
	// WHAT IT STILL MAKES TRUE, which is the motive: a credential row is
	// REPLACED IN PLACE — one row per name, ON CONFLICT DO UPDATE — so the prior
	// ciphertext is overwritten inside its own page rather than freed, and FAST
	// zeroes exactly that. What it does not make true is a guarantee about the
	// whole file: a credential row that was deleted, or one moved by a page
	// split, can leave its old bytes in a freed page.
	//
	// AND THE WRITE-AHEAD LOG IS A DIFFERENT MATTER AGAIN. While the service
	// runs, <database>-wal holds the prior ciphertext as well as the current one,
	// and secure_delete says nothing about the WAL in either setting. This is
	// recorded rather than dressed up: the residue is AES-GCM under the same key
	// file, so it discloses nothing to anyone who does not hold that file, and
	// only a SUPERSEDED secret to anyone who holds both. Making the guarantee
	// true of the file would mean a wal_checkpoint(TRUNCATE) after every
	// credential write; that was weighed and not taken. See the README's
	// limitations, and AC16 of specs/SPEC-accounts-and-settings.md.
	source := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=secure_delete(FAST)"

	db, err := sql.Open("sqlite", source)
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}

	// One connection. SQLite takes a single writer anyway, and one connection is
	// also what makes Close deterministic: SQLite removes the -wal and -shm
	// companions when the last connection to a database closes, and a hot
	// journal left behind is exactly what step 8's restart story must not meet.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: reaching %s: %w", path, err)
	}

	store := &Store{db: db}
	if err := store.applySchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// DB exposes the handle for the read paths later cycles add. It is deliberately
// not used to write: every write goes through a typed method on Store, so the
// idempotence and canonical-ordering guarantees cannot be bypassed by accident.
func (s *Store) DB() *sql.DB { return s.db }

// Close checkpoints the write-ahead log and closes the database.
//
// The checkpoint is the point: it folds the -wal file back into the database so
// that a clean stop leaves one file and no hot journal, and PRAGMA
// integrity_check on the result is `ok`.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	// A failed checkpoint must not stop the close, or a stuck reader would keep
	// the database open for ever; it is reported alongside instead.
	_, checkpointErr := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	closeErr := s.db.Close()
	s.db = nil
	switch {
	case closeErr != nil:
		return fmt.Errorf("store: closing the database: %w", closeErr)
	case checkpointErr != nil:
		return fmt.Errorf("store: checkpointing the write-ahead log: %w", checkpointErr)
	}
	return nil
}

// applySchema applies schema.sql. It is idempotent, so this runs on every start.
func (s *Store) applySchema(ctx context.Context) error {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("store: reading the embedded schema: %w", err)
	}
	for _, statement := range SplitStatements(string(schema)) {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: applying the schema: %w\nstatement: %s", err, firstLine(statement))
		}
	}
	return nil
}

// IntegrityCheck runs PRAGMA integrity_check and returns what it said. A healthy
// database answers `ok`.
func (s *Store) IntegrityCheck(ctx context.Context) (string, error) {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return "", fmt.Errorf("store: integrity check: %w", err)
	}
	return result, nil
}

// Setting returns the value of one setting row, and whether it exists. It is the
// whole of what internal/config needs from storage.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = ?", key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("store: reading setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting writes one setting row.
func (s *Store) SetSetting(ctx context.Context, key, value string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO setting (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now)
	if err != nil {
		return fmt.Errorf("store: writing setting %s: %w", key, err)
	}
	return nil
}

// Purge applies purge.sql, which reads the horizon from
// setting.retention_seconds. A horizon of 0 means unlimited and deletes nothing:
// the scalar subquery yields NULL, so every comparison is NULL.
//
// The file's own transaction control is stripped and the statements run inside
// one Go transaction instead, so a purge is all-or-nothing whichever way it is
// invoked. The DELETE statements themselves are the file's, unchanged, because
// sql/schema-checks.sh asserts against that text and two copies would drift.
//
// ORDERING IS THE CALLER'S. A running service purges through collect.Collector.Purge,
// which never lets this run between a pass storing its rows and the end of that
// pass's derivation (docs/data-model.md, "Purge"). Whatever the ordering, the hours
// this writes a purged part for are selected by the next refresh (dirty_hours).
func (s *Store) Purge(ctx context.Context, now int64) error {
	purge, err := sqlFiles.ReadFile("purge.sql")
	if err != nil {
		return fmt.Errorf("store: reading the embedded purge: %w", err)
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the purge transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	for _, statement := range SplitStatements(string(purge)) {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if strings.HasPrefix(upper, "PRAGMA") || strings.HasPrefix(upper, "BEGIN") ||
			strings.HasPrefix(upper, "COMMIT") {
			continue
		}
		if _, err := transaction.ExecContext(ctx, statement, sql.Named("now", now)); err != nil {
			return fmt.Errorf("store: purging: %w\nstatement: %s", err, firstLine(statement))
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the purge: %w", err)
	}
	return nil
}

// PurgeDueAddresses returns, sorted, the address of every unplaced end of a flow a
// purge at now would remove. They are what the collector places before it purges, so
// the purged part records each flow where the evidence held puts it rather than as it
// was stored. With an unlimited retention nothing is due and the list is empty.
func (s *Store) PurgeDueAddresses(ctx context.Context, now int64) ([]string, error) {
	const name = "purge_due_addresses"
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, map[string]any{"now": now})...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var addresses []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		addresses = append(addresses, address)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	sort.Strings(addresses)
	return addresses, nil
}

// SplitStatements splits a SQL file into statements on the semicolons that end
// one.
//
// It has to understand three things, and getting any of them wrong corrupts a
// statement rather than failing loudly. A semicolon inside a single-quoted string
// does not end a statement. A quote inside a `--` comment is not the start of a
// string — both files this package embeds are heavily commented and several of
// those comments carry an apostrophe, which is exactly the case a naive splitter
// gets wrong. And a quote doubled inside a string is an escaped quote, which
// leaves the string open; toggling on each of the pair does the right thing
// because two toggles cancel.
//
// It is exported so a test can exercise it on its own rather than only through a
// database.
func SplitStatements(text string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(text); i++ {
		character := text[i]

		switch {
		case inLineComment:
			if character == '\n' {
				inLineComment = false
			}
			current.WriteByte(character)
			continue
		case inBlockComment:
			if character == '*' && i+1 < len(text) && text[i+1] == '/' {
				inBlockComment = false
				current.WriteString("*/")
				i++
				continue
			}
			current.WriteByte(character)
			continue
		case !inString && character == '-' && i+1 < len(text) && text[i+1] == '-':
			inLineComment = true
			current.WriteString("--")
			i++
			continue
		case !inString && character == '/' && i+1 < len(text) && text[i+1] == '*':
			inBlockComment = true
			current.WriteString("/*")
			i++
			continue
		}

		switch {
		case character == '\'':
			inString = !inString
			current.WriteByte(character)
		case character == ';' && !inString:
			statements = append(statements, current.String())
			current.Reset()
		default:
			current.WriteByte(character)
		}
	}
	statements = append(statements, current.String())

	kept := make([]string, 0, len(statements))
	for _, statement := range statements {
		if !blankOrComment(statement) {
			kept = append(kept, statement)
		}
	}
	return kept
}

// blankOrComment reports whether a fragment holds nothing a database would run.
func blankOrComment(statement string) bool {
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return false
		}
	}
	return true
}

// firstLine returns the first line of a statement that is not a comment, so an
// error names the statement without reprinting its documentation.
func firstLine(statement string) string {
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return trimmed
		}
	}
	return strings.TrimSpace(statement)
}

// PurgedBefore returns the furthest horizon the retention purge has applied, and whether
// a purge with a finite retention has run at all. Every flow and lookup observed before it
// may have been purged. InsertFlow and InsertDNSResolution refuse such a record inside
// their own insert statements, which is what makes the refusal hold against a purge
// committing at any moment; this read serves the tests and the screens.
func (s *Store) PurgedBefore(ctx context.Context) (int64, bool, error) {
	var before int64
	err := s.db.QueryRowContext(ctx, "SELECT purged_before FROM retention_purge WHERE id = 1").Scan(&before)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: reading how far back the purge has purged: %w", err)
	}
	return before, true, nil
}
