package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Applying the schema, splitting SQL files, and the retention purge's file.

// applySchema applies schema.sql. It is idempotent, so this runs on every start.
//
// THE WHOLE FILE IS ONE TRANSACTION: PostgreSQL's DDL is transactional, so a start interrupted
// half-way through leaves the schema it found, never half of the new one. The transaction takes a
// transaction-scoped advisory lock first, so two starts against one database -- which the
// instance lock already refuses -- could not interleave their applies either.
func (s *Store) applySchema(ctx context.Context) error {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("store: reading the embedded schema: %w", err)
	}
	// A database that already holds the core's tables was written by some build. If the schema
	// then fails to apply BECAUSE A TABLE OR COLUMN IT NAMES IS MISSING, the cause is that
	// build's schema, which the in-place file does not upgrade, and the error says so. Any other
	// failure -- a refused connection, a cancelled start, a full disk -- is not an outdated
	// schema, and telling the operator to rebuild for it would throw history away for nothing.
	var existingTables int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM pg_tables WHERE schemaname = ?", CoreSchema).Scan(&existingTables); err != nil {
		return fmt.Errorf("store: reading the existing schema: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range SplitStatements(string(schema)) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if existingTables > 0 && isMissingSchemaObject(err) {
				return fmt.Errorf("%w: %w\nstatement: %s", ErrSchemaOutdated, err, firstLine(statement))
			}
			return fmt.Errorf("store: applying the schema: %w\nstatement: %s", err, firstLine(statement))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing the schema: %w", err)
	}
	return nil
}

// isMissingSchemaObject reports whether a schema statement failed because a table or column it
// names does not exist -- the one failure an earlier build's schema causes -- read from
// PostgreSQL's error code, never from its message.
func isMissingSchemaObject(err error) bool {
	var failure *pgconn.PgError
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "42P01", "42703": // undefined_table, undefined_column
		return true
	}
	return false
}

// IsConstraintViolation reports whether an error is PostgreSQL's refusal of a row by a
// constraint: SQLSTATE class 23 (integrity constraint violation), whichever constraint it was.
func IsConstraintViolation(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && strings.HasPrefix(failure.Code, "23")
}

// IsUniqueViolation reports whether an error is a unique constraint's refusal (23505).
func IsUniqueViolation(err error) bool {
	var failure *pgconn.PgError
	return errors.As(err, &failure) && failure.Code == "23505"
}

// Purge applies purge.sql, which reads the horizon from setting.retention_seconds. A horizon of 0
// means unlimited and deletes nothing: the scalar subquery yields NULL, so every comparison is
// NULL.
//
// The statements run inside one transaction, so a purge is all-or-nothing. The DELETE statements
// are the file's, unchanged, because sql/schema-checks.sh asserts against that text and two copies
// would drift. A hypertable's whole chunks past the horizon are dropped first, in the same
// transaction (dropChunksPastHorizon); the file's row deletion then removes the straddling chunk's
// rows to the exact second, so the horizon is exact whatever the chunking.
//
// ORDERING IS THE CALLER'S. A running service purges through collect.Collector.Purge, which never
// lets this run between a pass storing its rows and the end of that pass's derivation
// (docs/data-model.md, "Purge"). Whatever the ordering, the hours this writes a purged part for
// are selected by the next refresh (dirty_hours).
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
		upper := strings.ToUpper(strings.TrimSpace(stripComments(statement)))
		if strings.HasPrefix(upper, "BEGIN") || strings.HasPrefix(upper, "COMMIT") {
			continue
		}
		if _, err := transaction.ExecContext(ctx, statement, sql.Named("now", now)); err != nil {
			return fmt.Errorf("store: purging: %w\nstatement: %s", err, firstLine(statement))
		}
	}
	// The tables plugins own, each by its own declared retention (plugintables.go).
	if err := s.purgePluginTables(ctx, transaction, now); err != nil {
		return err
	}
	// The NetFlow tables, the flow records by their own retention (flowrecord.go).
	if err := purgeNetflow(ctx, transaction, now); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the purge: %w", err)
	}
	return nil
}

// stripComments removes the -- comment lines of a statement.
func stripComments(statement string) string {
	var kept []string
	for _, line := range strings.Split(statement, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// SplitStatements splits a SQL file into statements on the semicolons that end one.
//
// It understands what would otherwise corrupt a statement rather than fail loudly: a semicolon
// inside a single-quoted string, a quoted identifier or a dollar-quoted body (a function's) does
// not end a statement; a quote inside a `--` or a `/* */` comment is not the start of a string --
// both files this package embeds are heavily commented and several comments carry an
// apostrophe -- and a quote doubled inside a string is an escaped quote.
//
// It is exported so a test can exercise it on its own rather than only through a database.
func SplitStatements(text string) []string {
	var statements []string
	var current strings.Builder
	length := len(text)
	for i := 0; i < length; {
		character := text[i]
		switch {
		case character == '-' && i+1 < length && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = length - i
			}
			current.WriteString(text[i : i+end])
			i += end
		case character == '/' && i+1 < length && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			stop := length
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			current.WriteString(text[i:stop])
			i = stop
		case character == '\'' || character == '"':
			end := i + 1
			for end < length {
				if text[end] == character {
					if end+1 < length && text[end+1] == character {
						end += 2
						continue
					}
					break
				}
				end++
			}
			stop := min(end+1, length)
			current.WriteString(text[i:stop])
			i = stop
		case character == '$' && i+1 < length && (text[i+1] == '$' || isIdentifierStart(text[i+1])):
			tagEnd := strings.IndexByte(text[i+1:], '$')
			if tagEnd >= 0 && validDollarTag(text[i:i+1+tagEnd+1]) {
				tag := text[i : i+1+tagEnd+1]
				end := strings.Index(text[i+len(tag):], tag)
				stop := length
				if end >= 0 {
					stop = i + len(tag) + end + len(tag)
				}
				current.WriteString(text[i:stop])
				i = stop
				continue
			}
			current.WriteByte(character)
			i++
		case character == ';':
			statements = append(statements, current.String())
			current.Reset()
			i++
		default:
			current.WriteByte(character)
			i++
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

// firstLine returns the first line of a statement that is not a comment, so an error names the
// statement without reprinting its documentation.
func firstLine(statement string) string {
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return trimmed
		}
	}
	return strings.TrimSpace(statement)
}
