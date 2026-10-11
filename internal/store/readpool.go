package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// The read connections and their statement cache: decision 10 and B8d of
// specs/SPEC-step-5b-widget-api.md.
//
// WHY A SECOND HANDLE. The database holds ONE read-write connection (store.go), and
// every collection write and every derivation runs on it. A widget read issued on that
// same connection would queue behind a derivation -- ReclassifyEverything above all --
// and a long read would hold the collection back in turn. The widget API therefore
// reads through a small pool of its own connections. Each is opened with
// PRAGMA query_only, so nothing that reaches it can write: a statement that tries is
// refused by SQLite itself, whatever the code above it does. WAL is what makes this
// sound (https://www.sqlite.org/wal.html, "Concurrency"): a reader sees the last
// committed transaction and never blocks the writer, and the writer never blocks it.
//
// WHY A CACHE. Each read function used to fetch its statement text and run it
// unprepared, so the SQLite driver parsed it again on every call; the read statements
// are long and a canvas issues one request per widget. Every statement the read paths
// run is prepared once on the pool and reused: a *sql.Stmt prepared on a *sql.DB is
// safe for concurrent use and is re-prepared by database/sql on each pool connection
// the first time that connection runs it, which is what makes one cache compatible
// with several connections.

// readConnections is how many read-only connections the API may hold open. Small on
// purpose: SQLite serves one reader per connection, and a canvas issues one request
// per widget, a dozen at most, which a handful of connections drains in a few
// round-trips without letting the readers crowd out the writer's checkpoints. It is a
// constant rather than a setting because no measurement yet says an installation needs
// a different value.
const readConnections = 4

// readPool is the read-only handle and its prepared statements.
type readPool struct {
	db *sql.DB

	mu       sync.Mutex
	prepared map[string]*sql.Stmt
	// prepares counts the statements this pool prepared, so a test can observe that a
	// second call of a read function prepares nothing (AC31).
	prepares int
}

// openReadPool opens the read-only connections on the database at source, a URI the
// writer already opened and applied the schema to.
func openReadPool(ctx context.Context, path string) (*readPool, error) {
	source := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return nil, fmt.Errorf("store: opening the read connections: %w", err)
	}
	db.SetMaxOpenConns(readConnections)
	db.SetMaxIdleConns(readConnections)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: reaching the database for reading: %w", err)
	}
	return &readPool{db: db, prepared: map[string]*sql.Stmt{}}, nil
}

// statement returns the cached preparation of a text, preparing it the first time.
func (p *readPool) statement(ctx context.Context, query string) (*sql.Stmt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if statement, found := p.prepared[query]; found {
		return statement, nil
	}
	statement, err := p.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	p.prepared[query] = statement
	p.prepares++
	return statement, nil
}

// preparedCount reports how many statements the pool has prepared.
func (p *readPool) preparedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prepares
}

// preparedTexts returns the text of every statement the pool has prepared.
func (p *readPool) preparedTexts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	texts := make([]string, 0, len(p.prepared))
	for query := range p.prepared {
		texts = append(texts, query)
	}
	return texts
}

// close releases every prepared statement and the connections.
func (p *readPool) close() error {
	p.mu.Lock()
	for query, statement := range p.prepared {
		_ = statement.Close()
		delete(p.prepared, query)
	}
	p.mu.Unlock()
	return p.db.Close()
}

// ExecContext is refused: the pool reads. It exists so the pool is a querier the
// shared helpers accept, and a write that reached it would be refused by query_only
// anyway; refusing here names the mistake instead.
func (p *readPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("store: the read connections do not write")
}

// QueryContext runs a query through its cached preparation.
func (p *readPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	statement, err := p.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return statement.QueryContext(ctx, args...)
}

// QueryRowContext runs a one-row query through its cached preparation. A text that
// cannot be prepared is run unprepared instead, which reports the same error through
// the row it returns.
func (p *readPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	statement, err := p.statement(ctx, query)
	if err != nil {
		return p.db.QueryRowContext(ctx, query, args...)
	}
	return statement.QueryRowContext(ctx, args...)
}
