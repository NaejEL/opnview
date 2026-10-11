// Package store opens the PostgreSQL database, applies the embedded schema, and holds the typed
// write and read paths the collectors and the widget API use.
//
// The store is PostgreSQL with the TimescaleDB extension (ROADMAP.md, "Rules that apply to every
// step"; decision 13 of specs/SPEC-backend-completion-plugins.md). One database per installation;
// opnview is its only client. The schema is one file, applied in place on every start, in one
// transaction; there is no migration runner and no schema_version table. Migrations begin with
// step 8.
//
// The driver is pgx, pure Go, so CGO_ENABLED stays 0.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The schema and the purge, embedded. They live in this directory rather than under sql/
// because Go's embed directive cannot reach outside its own package, and a second copy under
// sql/ would be a second truth. sql/schema-checks.sh reads these same two files.
//
//go:embed schema.sql purge.sql
var sqlFiles embed.FS

// CoreSchema is the PostgreSQL schema the core's objects live in. No plugin schema can take
// its name: a plugin's carries the plugin prefix (plugintables.go).
const CoreSchema = "opnview"

// searchPath is every connection's search path: the core's objects, then public, where the
// TimescaleDB extension's functions are.
const searchPath = CoreSchema + ", public"

// ErrSchemaOutdated is returned by Open when the database was created by an earlier build whose
// schema differs from this one in a way the in-place schema cannot absorb: a table that exists
// without a column this build's schema reads. Such a database is not upgraded; it is rebuilt
// and the history collected anew. ROADMAP.md, step 5, "Rebuilding the database", says how.
var ErrSchemaOutdated = errors.New("store: the database was created by an earlier build whose " +
	"schema differs from this one; rebuild it as ROADMAP.md, step 5, \"Rebuilding the database\", describes")

// The refusals to start. Each names what was found, so an operator knows what to fix.
var (
	// ErrNoDatabase is a start without the database connection parameter.
	ErrNoDatabase = errors.New("store: no database connection was given (--database); there is " +
		"deliberately no default")
	// ErrDatabaseUnreachable is a database that did not answer within the start bound.
	ErrDatabaseUnreachable = errors.New("store: the database did not answer")
	// ErrTelemetryOn is a database whose TimescaleDB telemetry is not off: it would make a call
	// opnview's outbound rule forbids.
	ErrTelemetryOn = errors.New("store: TimescaleDB telemetry is not off on this database " +
		"(timescaledb.telemetry_level); opnview refuses to start until it is")
	// ErrCollation is a database whose collation or character classification is not C: opnview
	// orders text by its bytes and folds case on ASCII alone.
	ErrCollation = errors.New("store: the database's collation and character type must both be C")
	// ErrAlreadyRunning is a second opnview against a database another one holds.
	ErrAlreadyRunning = errors.New("store: another opnview is already running against this database")
)

// Store is an open database.
type Store struct {
	db *sql.DB
	// reader is the read connections every read function runs on (readers.go). It is nil only
	// on a Store that failed to open.
	reader *readConnections
	// instance holds the session advisory lock that makes this process the database's only
	// opnview, for as long as the store is open.
	instance *sql.Conn
	// plugins are the tables the loaded plugins own on this database (plugintables.go).
	plugins pluginTableSet
	// dsn is the connection parameter the store was opened with.
	dsn string
}

// Options are how a store is opened.
type Options struct {
	// DSN is the connection to the installation's own database: a PostgreSQL connection string,
	// such as "host=db user=opnview dbname=opnview passfile=/run/opnview/pgpass". Required.
	DSN string
	// Wait bounds how long a start waits for the database to answer; zero is DefaultWait.
	Wait time.Duration
	// Log, when set, is told each attempt to reach the database.
	Log func(message string)
}

// DefaultWait is how long a start waits for the database: long enough for a database container
// started beside opnview to finish its own start, short enough that a misconfigured connection
// is reported within a minute.
const DefaultWait = 60 * time.Second

// instanceLock is the key of the session advisory lock one opnview holds on its database.
const instanceLock = 7_115_118_105_101_119

// Open opens the database the connection parameter names and applies the schema.
func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWith(ctx, Options{DSN: dsn})
}

// OpenWith opens the database with options.
func OpenWith(ctx context.Context, options Options) (*Store, error) {
	if strings.TrimSpace(options.DSN) == "" {
		return nil, ErrNoDatabase
	}
	db, err := openPool(options.DSN)
	if err != nil {
		return nil, err
	}
	if err := waitForDatabase(ctx, db, options); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db, dsn: options.DSN}
	if err := store.checkDatabase(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	instance, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: holding the instance connection: %w", err)
	}
	var held bool
	if err := instance.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", instanceLock).Scan(&held); err != nil {
		_ = instance.Close()
		_ = db.Close()
		return nil, fmt.Errorf("store: taking the instance lock: %w", err)
	}
	if !held {
		_ = instance.Close()
		_ = db.Close()
		return nil, ErrAlreadyRunning
	}
	store.instance = instance
	if err := store.applySchema(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	reader, err := openReaders(ctx, store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	store.reader = reader
	return store, nil
}

// waitForDatabase pings the database until it answers or the bound passes, logging each
// attempt.
func waitForDatabase(ctx context.Context, db *sql.DB, options Options) error {
	wait := options.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	deadline := time.Now().Add(wait)
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if options.Log != nil {
			options.Log(fmt.Sprintf("waiting for the database (attempt %d): %v", attempt, err))
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("%w within %s: %w", ErrDatabaseUnreachable, wait, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrDatabaseUnreachable, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// checkDatabase refuses a database whose telemetry is on or whose collation is not byte order.
func (s *Store) checkDatabase(ctx context.Context) error {
	// The provider too: an ICU or builtin database reports datcollate C and orders or folds case by
	// its own rules all the same. Only libc's C locale orders by bytes and folds ASCII alone.
	var collate, ctype, provider string
	if err := s.db.QueryRowContext(ctx,
		"SELECT datcollate, datctype, CAST(datlocprovider AS text) FROM pg_database WHERE datname = current_database()").
		Scan(&collate, &ctype, &provider); err != nil {
		return fmt.Errorf("store: reading the database's collation: %w", err)
	}
	if collate != "C" || ctype != "C" || provider != "c" {
		return fmt.Errorf("%w (it is %s / %s, provider %s)", ErrCollation, collate, ctype, provider)
	}
	if _, err := s.db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		return fmt.Errorf("store: loading the TimescaleDB extension: %w", err)
	}
	var telemetry string
	if err := s.db.QueryRowContext(ctx, "SELECT current_setting('timescaledb.telemetry_level')").
		Scan(&telemetry); err != nil {
		return fmt.Errorf("store: reading timescaledb.telemetry_level: %w", err)
	}
	if telemetry != "off" {
		return fmt.Errorf("%w (it is %s)", ErrTelemetryOn, telemetry)
	}
	return nil
}

// DB exposes the read-write handle for the read paths and the tests. It is deliberately not used
// to write by any package but this one: every write goes through a typed method on Store, so the
// idempotence and canonical-ordering guarantees cannot be bypassed by accident.
func (s *Store) DB() *sql.DB { return s.db }

// DSN is the connection parameter the store was opened with, which a test reuses to open the same
// database again.
func (s *Store) DSN() string { return s.dsn }

// Close releases the instance lock and closes every connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	var readerErr, instanceErr error
	if s.reader != nil {
		readerErr = s.reader.close()
		s.reader = nil
	}
	if s.instance != nil {
		instanceErr = s.instance.Close()
		s.instance = nil
	}
	closeErr := s.db.Close()
	s.db = nil
	switch {
	case readerErr != nil:
		return fmt.Errorf("store: closing the read connections: %w", readerErr)
	case instanceErr != nil:
		return fmt.Errorf("store: releasing the instance lock: %w", instanceErr)
	case closeErr != nil:
		return fmt.Errorf("store: closing the database: %w", closeErr)
	}
	return nil
}

// Setting returns the value of one setting row, and whether it exists. It is the whole of what
// internal/config needs from storage.
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

// PurgeDueAddresses returns, sorted, the address of every unplaced end of a flow a purge at now
// would remove. They are what the collector places before it purges, so the purged part records
// each flow where the evidence held puts it rather than as it was stored. With an unlimited
// retention nothing is due and the list is empty.
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

// PurgedBefore returns the furthest horizon the retention purge has applied, and whether a purge
// with a finite retention has run at all. Every flow and lookup observed before it may have been
// purged. InsertFlow and InsertDNSResolution refuse such a record inside their own insert
// statements, which is what makes the refusal hold against a purge committing at any moment;
// this read serves the tests and the screens.
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
