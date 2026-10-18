package storetest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// StatementCounter counts the statements a store's read-write pool runs, by the first word of each:
// what PA-AC25 of specs/SPEC-backend-completion-plugins.md compares between a pass of ten rows and a
// pass of a thousand. It counts and changes nothing: every statement reaches PostgreSQL as it was
// handed.
type StatementCounter struct {
	mutex  sync.Mutex
	counts map[string]int
}

// CountStatements replaces a store's read-write pool by one that counts every statement it runs, for
// the rest of the test.
func CountStatements(t testing.TB, s *store.Store) *StatementCounter {
	t.Helper()
	// The connection is opened as the store opens its own (internal/store/sqldb, driver.go).
	config, err := pgx.ParseConfig(s.ConnectionString)
	if err != nil {
		t.Fatal(err)
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	config.RuntimeParams["search_path"] = sqldb.SearchPath()
	inner := stdlib.GetConnector(*config)
	counter := &StatementCounter{counts: map[string]int{}}
	pool := sql.OpenDB(countingConnector{inner: inner, counter: counter})
	previous := s.Handle.SQL
	s.Handle.SQL = pool
	t.Cleanup(func() {
		s.Handle.SQL = previous
		_ = pool.Close()
	})
	return counter
}

// Reset forgets every statement counted so far.
func (c *StatementCounter) Reset() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.counts = map[string]int{}
}

// Writes is how many INSERT, UPDATE, DELETE and MERGE statements ran, a WITH counted as what it ends
// in when it writes.
func (c *StatementCounter) Writes() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.counts["INSERT"] + c.counts["UPDATE"] + c.counts["DELETE"] + c.counts["MERGE"] + c.counts["WITH-WRITE"]
}

// Total is how many statements ran.
func (c *StatementCounter) Total() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	total := 0
	for _, count := range c.counts {
		total += count
	}
	return total
}

func (c *StatementCounter) note(query string) {
	fields := strings.Fields(strings.ToUpper(query))
	kind := ""
	if len(fields) > 0 {
		kind = fields[0]
	}
	if kind == "WITH" {
		upper := strings.ToUpper(query)
		for _, word := range []string{"INSERT ", "UPDATE ", "DELETE "} {
			if strings.Contains(upper, word) {
				kind = "WITH-WRITE"
			}
		}
	}
	c.mutex.Lock()
	c.counts[kind]++
	c.mutex.Unlock()
}

type countingConnector struct {
	inner   driver.Connector
	counter *StatementCounter
}

func (c countingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, counter: c.counter}, nil
}

func (c countingConnector) Driver() driver.Driver { return c.inner.Driver() }

// countingConn is one connection of the counting pool: every call reaches the pgx connection beneath.
type countingConn struct {
	driver.Conn
	counter *StatementCounter
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.counter.note(query)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.counter.note(query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *countingConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c *countingConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *countingConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *countingConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *countingConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}
