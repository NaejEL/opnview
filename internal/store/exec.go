package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// querier is what both *sql.DB and *sql.Tx offer. The derivations run inside one
// transaction each, and the database holds one connection, so a helper that went
// back to s.db in the middle of a transaction would wait for itself for ever;
// every helper the derivations share therefore takes the querier it runs on.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// named turns a parameter map into the named arguments database/sql binds,
// keeping only those the statement names: an argument a statement does not use
// is refused by the driver, and one parameter map serves several statements.
func named(text string, parameters map[string]any) []any {
	arguments := make([]any, 0, len(parameters))
	for name, value := range parameters {
		if !usesParameter(text, name) {
			continue
		}
		arguments = append(arguments, sql.Named(name, value))
	}
	return arguments
}

// execNamed runs one named statement and returns how many rows it changed.
func execNamed(ctx context.Context, q querier, name string, parameters map[string]any) (int64, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, err
	}
	return execText(ctx, q, name, text, parameters)
}

// execText runs a statement text and returns how many rows it changed.
func execText(ctx context.Context, q querier, name, text string, parameters map[string]any) (int64, error) {
	result, err := q.ExecContext(ctx, text, named(text, parameters)...)
	if err != nil {
		return 0, fmt.Errorf("store: %s: %w", name, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %s: %w", name, err)
	}
	return affected, nil
}

// queryInts runs a named statement whose rows are one integer column and returns
// them; a NULL is skipped.
func queryInts(ctx context.Context, q querier, name string, parameters map[string]any) ([]int64, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text, named(text, parameters)...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var values []int64
	for rows.Next() {
		var value sql.NullInt64
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		if value.Valid {
			values = append(values, value.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return values, nil
}

// queryOptionalInt runs a named statement returning at most one integer, and
// reports whether it returned a non-NULL one.
func queryOptionalInt(ctx context.Context, q querier, name string, parameters map[string]any) (
	int64, bool, error) {
	text, err := Statement(name)
	if err != nil {
		return 0, false, err
	}
	return queryOptionalIntText(ctx, q, name, text, parameters)
}

// queryOptionalIntText is queryOptionalInt over a statement text already
// expanded.
func queryOptionalIntText(ctx context.Context, q querier, name, text string,
	parameters map[string]any) (int64, bool, error) {
	var value sql.NullInt64
	err := q.QueryRowContext(ctx, text, named(text, parameters)...).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: %s: %w", name, err)
	}
	return value.Int64, value.Valid, nil
}

// optional renders a nullable id as a bindable value.
func optional(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// usesParameter reports whether a statement names :name, as a whole parameter
// rather than as the start of a longer one.
func usesParameter(text, name string) bool {
	token := ":" + name
	for offset := 0; ; {
		index := strings.Index(text[offset:], token)
		if index < 0 {
			return false
		}
		after := offset + index + len(token)
		if after >= len(text) || !isParameterCharacter(text[after]) {
			return true
		}
		offset = after
	}
}

// isParameterCharacter reports whether a byte can continue a parameter name.
func isParameterCharacter(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')
}

// preparedTx is a transaction that prepares each statement text the first time it
// runs one and reuses the prepared statement for the rest of the transaction.
//
// IT EXISTS FOR THE DERIVATIONS THAT RUN THE SAME STATEMENTS ONCE PER ITEM -- the
// classification once per address, the attribution once per flow, the pairing once
// per record. Measured on 7 October 2026, preparing statements again was about three
// quarters of the CPU the store's tests spent: the SQLite driver parses the
// statement text on every prepare, and these statements are long. A statement
// prepared on the transaction belongs to it and is closed when it commits or rolls
// back, so nothing outlives the transaction and nothing waits for the single
// connection the transaction holds.
//
// It is used by one goroutine at a time, as the transaction it wraps is.
type preparedTx struct {
	tx       *sql.Tx
	prepared map[string]*sql.Stmt
}

// newPreparedTx wraps a transaction.
func newPreparedTx(tx *sql.Tx) *preparedTx {
	return &preparedTx{tx: tx, prepared: map[string]*sql.Stmt{}}
}

// statement returns the transaction's prepared statement for a text, preparing it
// the first time.
func (p *preparedTx) statement(ctx context.Context, query string) (*sql.Stmt, error) {
	if statement, found := p.prepared[query]; found {
		return statement, nil
	}
	statement, err := p.tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	p.prepared[query] = statement
	return statement, nil
}

// ExecContext runs a statement through the transaction's prepared copy of it.
func (p *preparedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	statement, err := p.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return statement.ExecContext(ctx, args...)
}

// QueryContext runs a query through the transaction's prepared copy of it.
func (p *preparedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	statement, err := p.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return statement.QueryContext(ctx, args...)
}

// QueryRowContext runs a one-row query through the transaction's prepared copy of
// it. A text that cannot be prepared is run unprepared instead, which reports the
// same error through the row it returns.
func (p *preparedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	statement, err := p.statement(ctx, query)
	if err != nil {
		return p.tx.QueryRowContext(ctx, query, args...)
	}
	return statement.QueryRowContext(ctx, args...)
}

// statementCache prepares statement texts on the database, for an operation that
// runs the same statements in many short transactions -- the refresh, one
// transaction per slot -- and so cannot keep a transaction's own prepared copies
// from one to the next.
//
// THE DATABASE HOLDS ONE CONNECTION, so a text is never prepared on it while a
// transaction holds that connection: that would wait for itself for ever. A
// transaction that meets a text the cache does not hold runs it unprepared and
// records it, and warm, which its caller runs between transactions, prepares what
// was recorded. A text is therefore parsed at most twice per operation rather than
// once per slot. Close releases every statement at the end of the operation.
type statementCache struct {
	db       *sql.DB
	prepared map[string]*sql.Stmt
	missed   map[string]struct{}
}

// newStatementCache returns an empty cache over the database.
func newStatementCache(db *sql.DB) *statementCache {
	return &statementCache{db: db, prepared: map[string]*sql.Stmt{}, missed: map[string]struct{}{}}
}

// warm prepares every text a transaction met and the cache did not hold. Its
// caller holds no transaction.
func (c *statementCache) warm(ctx context.Context) error {
	for query := range c.missed {
		statement, err := c.db.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("store: preparing a statement: %w", err)
		}
		c.prepared[query] = statement
		delete(c.missed, query)
	}
	return nil
}

// close releases every prepared statement.
func (c *statementCache) close() {
	for query, statement := range c.prepared {
		_ = statement.Close()
		delete(c.prepared, query)
	}
}

// in returns a querier that runs a transaction's statements through the cache.
func (c *statementCache) in(tx *sql.Tx) querier {
	return cachedTx{tx: tx, cache: c}
}

// cachedTx is a transaction whose statements come from a statementCache.
type cachedTx struct {
	tx    *sql.Tx
	cache *statementCache
}

// statement returns the transaction's copy of the cached statement for a text, or
// nil after recording the text for the next warm.
func (c cachedTx) statement(ctx context.Context, query string) *sql.Stmt {
	if statement, found := c.cache.prepared[query]; found {
		return c.tx.StmtContext(ctx, statement)
	}
	c.cache.missed[query] = struct{}{}
	return nil
}

// ExecContext runs a statement, prepared when the cache holds it.
func (c cachedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if statement := c.statement(ctx, query); statement != nil {
		return statement.ExecContext(ctx, args...)
	}
	return c.tx.ExecContext(ctx, query, args...)
}

// QueryContext runs a query, prepared when the cache holds it.
func (c cachedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if statement := c.statement(ctx, query); statement != nil {
		return statement.QueryContext(ctx, args...)
	}
	return c.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a one-row query, prepared when the cache holds it.
func (c cachedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if statement := c.statement(ctx, query); statement != nil {
		return statement.QueryRowContext(ctx, args...)
	}
	return c.tx.QueryRowContext(ctx, query, args...)
}

// direct returns a querier for statements its caller runs outside any
// transaction: a text the cache does not hold is prepared on the spot, which waits
// for nothing because no transaction of the caller's holds the connection.
func (c *statementCache) direct() querier {
	return cachedDB{cache: c}
}

// cachedDB runs statements on the database through a statementCache.
type cachedDB struct {
	cache *statementCache
}

// statement returns the cached statement for a text, preparing it the first time.
func (c cachedDB) statement(ctx context.Context, query string) (*sql.Stmt, error) {
	if statement, found := c.cache.prepared[query]; found {
		return statement, nil
	}
	statement, err := c.cache.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	c.cache.prepared[query] = statement
	delete(c.cache.missed, query)
	return statement, nil
}

// ExecContext runs a statement through its cached preparation.
func (c cachedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	statement, err := c.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return statement.ExecContext(ctx, args...)
}

// QueryContext runs a query through its cached preparation.
func (c cachedDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	statement, err := c.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return statement.QueryContext(ctx, args...)
}

// QueryRowContext runs a one-row query through its cached preparation. A text that
// cannot be prepared is run unprepared instead, which reports the same error
// through the row it returns.
func (c cachedDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	statement, err := c.statement(ctx, query)
	if err != nil {
		return c.cache.db.QueryRowContext(ctx, query, args...)
	}
	return statement.QueryRowContext(ctx, args...)
}
