package sqldb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// The read connections: what every widget query, plugin read query and catalogue query runs on
// (P7 of specs/SPEC-backend-completion-plugins.md).
//
// THEY LOG IN AS A ROLE THAT CAN ONLY READ, AND CANNOT READ A SECRET. The role is the database's
// own (<database>_reader), created by the store at every start with a password generated then and
// held in memory only. It holds USAGE on the core schema and SELECT on its tables and views, except
// account, session, encrypted_credential and plugin_state, on which it holds nothing; it is a
// member of no role, so no SELECT can raise its own privileges, and opnview's functions are all
// SECURITY INVOKER. Its sessions default to read-only transactions under a statement timeout. A
// write attempted on these connections fails twice over: by privilege, and by the transaction's
// read-only mode. This replaces SQLite's query_only connections and the EXPLAIN-opcode scan; the
// load-time refusal of a query naming a credential table (internal/store/reads/queryreads.go) stays
// as the first layer.
//
// AND EVERY TEXT IS PREPARED ONCE PER CONNECTION, by the driver's statement cache (driver.go);
// texts records each text the connections ran, for the test that every text is a planned one
// (AC17) and the one that a second call of a read function adds nothing (AC31, restated).

// readStatementTimeout bounds every statement on the read connections.
const readStatementTimeout = "10s"

// readConnectionLimit is how many read connections the API may hold open: a canvas issues one
// request per widget, a dozen at most.
const readConnectionLimit = 8

// The objects the read role holds no privilege on.
var unreadableObjects = []string{"account", "session", "encrypted_credential", "plugin_state"}

// ReadConnections is the read role's pool and the texts it has run.
type ReadConnections struct {
	DB   *sql.DB
	role string

	mu    sync.Mutex
	texts map[string]struct{}
	// statements counts every statement run, and waited the time spent until each answered.
	statements atomic.Int64
	waited     atomic.Int64
}

// ReadRoleName is the read role of a database: its name and _reader, quoted where used.
func ReadRoleName(database string) string { return database + "_reader" }

// OpenReaders creates or refreshes the read role and opens its pool.
func OpenReaders(ctx context.Context, s *Handle) (*ReadConnections, error) {
	var database string
	if err := s.SQL.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return nil, fmt.Errorf("store: reading the database name: %w", err)
	}
	role := ReadRoleName(database)
	password, err := randomPassword()
	if err != nil {
		return nil, err
	}
	if err := s.grantReadRole(ctx, role, password); err != nil {
		return nil, err
	}
	config, err := pgx.ParseConfig(s.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("store: reading the database connection parameter: %w", err)
	}
	config.User = role
	config.Password = password
	db, err := openPoolWithConfig(config)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(readConnectionLimit)
	db.SetMaxIdleConns(readConnectionLimit)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: reaching the database as the read role: %w", err)
	}
	return &ReadConnections{DB: db, role: role, texts: map[string]struct{}{}}, nil
}

// grantReadRole creates the read role when it is absent and gives it exactly what it may read.
func (s *Handle) grantReadRole(ctx context.Context, role, password string) error {
	quoted := pgx.Identifier{role}.Sanitize()
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the read role's grants: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", role).
		Scan(&exists); err != nil {
		return fmt.Errorf("store: looking up the read role: %w", err)
	}
	statements := []string{}
	if !exists {
		statements = append(statements, "CREATE ROLE "+quoted+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
	}
	statements = append(statements,
		"ALTER ROLE "+quoted+" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD "+
			quoteLiteral(password),
		"ALTER ROLE "+quoted+" SET default_transaction_read_only = on",
		"ALTER ROLE "+quoted+" SET statement_timeout = "+quoteLiteral(readStatementTimeout),
		"ALTER ROLE "+quoted+" SET search_path = "+CoreSchema+", public",
		"REVOKE ALL ON SCHEMA public FROM "+quoted,
		"GRANT USAGE ON SCHEMA public TO "+quoted,
		"GRANT USAGE ON SCHEMA "+CoreSchema+" TO "+quoted,
		"GRANT SELECT ON ALL TABLES IN SCHEMA "+CoreSchema+" TO "+quoted,
	)
	for _, object := range unreadableObjects {
		statements = append(statements, "REVOKE ALL ON "+CoreSchema+"."+object+" FROM "+quoted)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: granting the read role: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing the read role's grants: %w", err)
	}
	return nil
}

// GrantReadOnSchema gives the read role SELECT on every table of a schema: a plugin's own, or the
// mirror's. The aside schema is never granted.
func (s *Handle) GrantReadOnSchema(ctx context.Context, schema string) error {
	if s.Reader == nil {
		return nil
	}
	quotedRole := pgx.Identifier{s.Reader.role}.Sanitize()
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT SELECT ON ALL TABLES IN SCHEMA " + quotedSchema + " TO " + quotedRole,
	} {
		if _, err := s.SQL.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: granting the read role on %s: %w", schema, err)
		}
	}
	return nil
}

// GrantReadOnTable gives the read role SELECT on one table of a schema, and USAGE on the schema: the
// mirror grants each data table as it creates it, and none of its metadata (A15).
func (s *Handle) GrantReadOnTable(ctx context.Context, schema, table string) error {
	if s.Reader == nil {
		return nil
	}
	quotedRole := pgx.Identifier{s.Reader.role}.Sanitize()
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
		"GRANT SELECT ON " + pgx.Identifier{schema, table}.Sanitize() + " TO " + quotedRole,
	} {
		if _, err := s.SQL.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: granting the read role on %s.%s: %w", schema, table, err)
		}
	}
	return nil
}

// randomPassword is a password for the read role, generated at every start and never stored.
func randomPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: generating the read role's password: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// quoteLiteral quotes a string as a SQL literal.
func quoteLiteral(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }

// Note records a text the read connections ran.
func (r *ReadConnections) Note(query string) {
	r.mu.Lock()
	r.texts[query] = struct{}{}
	r.mu.Unlock()
}

// RunCount reports how many distinct texts the read connections have run.
func (r *ReadConnections) RunCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.texts)
}

// RunTexts returns every distinct text the read connections have run.
func (r *ReadConnections) RunTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	texts := make([]string, 0, len(r.texts))
	for query := range r.texts {
		texts = append(texts, query)
	}
	return texts
}

func (r *ReadConnections) Close() error { return r.DB.Close() }

// ExecContext is refused: the connections read. It exists so they are a querier the shared
// helpers accept; a write that reached them would be refused by the role anyway, and refusing here
// names the mistake instead.
func (r *ReadConnections) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("store: the read connections do not write")
}

// QueryContext runs a query on the read connections.
func (r *ReadConnections) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.Note(query)
	started := time.Now()
	defer r.counted(started)
	return r.DB.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a one-row query on the read connections.
func (r *ReadConnections) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.Note(query)
	started := time.Now()
	defer r.counted(started)
	return r.DB.QueryRowContext(ctx, query, args...)
}

// counted counts one statement and the time it took to answer.
func (r *ReadConnections) counted(started time.Time) {
	r.statements.Add(1)
	r.waited.Add(int64(time.Since(started)))
}

// Executed reports how many statements the read connections have run, and the time spent until
// each answered, since they were opened: what a response costs in statements, which
// ci/widgetbudget reports beside each response's time, and a test holds to a bound.
func (r *ReadConnections) Executed() (int64, time.Duration) {
	return r.statements.Load(), time.Duration(r.waited.Load())
}
