package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/store/aggregates"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/geo"
	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/purge"
	"github.com/NaejEL/opnview/internal/store/push"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The schema, embedded. It lives in this directory rather than under sql/ because Go's embed
// directive cannot reach outside its own package, and a second copy under sql/ would be a second
// truth. sql/schema-checks.sh reads this same file (and the purge's, internal/store/purge).
//
//go:embed schema.sql
var sqlFiles embed.FS

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

// Store is an open database: the handle every part of the store runs on, and the parts, each a
// package of internal/store named by what it holds (doc.go). A part's methods are the Store's, so
// a caller holds one Store and never names a part.
type Store struct {
	*sqldb.Handle
	*accounts.AccountStore
	*owners.OwnerStore
	*flows.FlowStore
	*attribution.AttributionStore
	*aggregates.AggregateStore
	*samples.SampleStore
	*push.PushStore
	*geo.GeoStore
	*reads.ReadStore
	*plugintables.PluginTableStore
	*purge.PurgeStore
}

// compose is the store whose parts all run on handle.
func compose(handle *sqldb.Handle) *Store {
	return &Store{
		Handle:           handle,
		AccountStore:     accounts.On(handle),
		OwnerStore:       owners.On(handle),
		FlowStore:        flows.On(handle),
		AttributionStore: attribution.On(handle),
		AggregateStore:   aggregates.On(handle),
		SampleStore:      samples.On(handle),
		PushStore:        push.On(handle),
		GeoStore:         geo.On(handle),
		ReadStore:        reads.On(handle),
		PluginTableStore: plugintables.On(handle),
		PurgeStore:       purge.On(handle),
	}
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
	// Timed, when set, is told how long each step of the open took: StepWait, StepSchema (with
	// whether the schema was applied or already recorded) and StepReadRole.
	Timed func(step string, took time.Duration)
}

// The steps of an open, as Options.Timed names them.
const (
	// StepWait is the wait for the database to answer.
	StepWait = "wait"
	// StepSchemaApplied is the apply of the schema to a database that did not record it.
	StepSchemaApplied = "schema applied"
	// StepSchemaRecorded is the check that the database records this schema already, with no apply.
	StepSchemaRecorded = "schema already recorded"
	// StepReadRole is the creation of the read role and its grants.
	StepReadRole = "read role"
)

// timingSteps names each step of an open as the suite's breakdown counts it.
var timingSteps = map[string]string{StepWait: TimingWait, StepSchemaApplied: TimingSchema,
	StepSchemaRecorded: TimingSchema, StepReadRole: TimingRole}

// DefaultWait is how long a start waits for the database: long enough for a database container
// started beside opnview to finish its own start, short enough that a misconfigured connection
// is reported within a minute.
const DefaultWait = 60 * time.Second

// mainIdleConnections is how many connections the main pool keeps open between uses. The default of
// database/sql, two, closed every connection beyond the second as soon as it was handed back, so the
// collector's concurrent passes opened a connection for most statements and prepared every statement
// again on it (the driver prepares each text once per connection, internal/store/sqldb/driver.go).
// Measured on 16 October 2026, keeping eight took the everyday suite's database CPU down by about a
// tenth. The pool stays unbounded in connections it may open; this bounds only those it keeps.
const mainIdleConnections = 8

// instanceLock is the key of the session advisory lock one opnview holds on its database.
const instanceLock = 7_115_118_105_101_119

// Open opens the database the connection parameter names and applies the schema, unless the
// database records it already.
func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWith(ctx, Options{DSN: dsn})
}

// OpenWith opens the database with options.
func OpenWith(ctx context.Context, options Options) (*Store, error) {
	if strings.TrimSpace(options.DSN) == "" {
		return nil, ErrNoDatabase
	}
	// Each step is also recorded for the suite's breakdown, which does nothing unless the CI-only
	// switch is on (internal/testtiming, through Timing).
	timed := func(step string, started time.Time) {
		took := time.Since(started)
		if options.Timed != nil {
			options.Timed(step, took)
		}
		recordTiming(timingSteps[step], took)
	}
	db, err := sqldb.OpenPool(options.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(mainIdleConnections)
	started := time.Now()
	if err := waitForDatabase(ctx, db, options); err != nil {
		_ = db.Close()
		return nil, err
	}
	timed(StepWait, started)
	store := compose(&sqldb.Handle{SQL: db, ConnectionString: options.DSN})
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
	store.Instance = instance
	started = time.Now()
	applied, err := store.applySchema(ctx)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if applied {
		timed(StepSchemaApplied, started)
	} else {
		timed(StepSchemaRecorded, started)
	}
	started = time.Now()
	reader, err := openReaders(ctx, store.Handle)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	timed(StepReadRole, started)
	store.Reader = reader
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
		// A retry is an event, never a silent sleep: the suite's breakdown counts it.
		recordTiming(TimingRetry, time.Second)
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
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT datcollate, datctype, CAST(datlocprovider AS text) FROM pg_database WHERE datname = current_database()").
		Scan(&collate, &ctype, &provider); err != nil {
		return fmt.Errorf("store: reading the database's collation: %w", err)
	}
	if collate != "C" || ctype != "C" || provider != "c" {
		return fmt.Errorf("%w (it is %s / %s, provider %s)", ErrCollation, collate, ctype, provider)
	}
	if _, err := s.SQL.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		return fmt.Errorf("store: loading the TimescaleDB extension: %w", err)
	}
	var telemetry string
	if err := s.SQL.QueryRowContext(ctx, "SELECT current_setting('timescaledb.telemetry_level')").
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
func (s *Store) DB() *sql.DB { return s.SQL }

// DSN is the connection parameter the store was opened with, which a test reuses to open the same
// database again.
func (s *Store) DSN() string { return s.ConnectionString }

// Close releases the instance lock and closes every connection.
func (s *Store) Close() error {
	if s.SQL == nil {
		return nil
	}
	var readerErr, instanceErr error
	if s.Reader != nil {
		readerErr = s.Reader.Close()
		s.Reader = nil
	}
	if s.Instance != nil {
		instanceErr = s.Instance.Close()
		s.Instance = nil
	}
	closeErr := s.SQL.Close()
	s.SQL = nil
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
