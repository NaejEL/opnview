package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The connection to PostgreSQL, and the two things it does to every statement.
//
// ONE AUTHORING SYNTAX FOR PARAMETERS. Every statement of opnview -- the store's, a widget's, a
// plugin's -- names its parameters as :name, and a few short statements in Go use ?. PostgreSQL
// numbers its parameters ($1, $2 ...). This connection rewrites each statement's parameters to
// PostgreSQL's numbering as it is run, binding each named argument to its name, so a statement is
// written once and a widget author never learns PostgreSQL's numbering. A :: cast, a string, a
// quoted identifier, a comment and a dollar-quoted body are left alone.
//
// VALUES ARE ALWAYS BOUND, NEVER SPLICED INTO THE TEXT. Each non-NULL argument travels as a bound
// parameter cast to the type its Go value has -- an int64 as BIGINT, a float64 as DOUBLE
// PRECISION, a string as TEXT, bytes as BYTEA -- so `coalesce(:limit, 20)` and `:at - :window` type
// as they did when every parameter was a literal; a NULL argument is written as NULL, so
// `:client_id IS NULL` needs no cast either. The statement is then prepared once per connection
// and text, by the driver's statement cache, and executed from it.
//
// ANSWERS KEEP THEIR JSON SHAPE. PostgreSQL answers a comparison with a boolean and a sum of
// integers with a numeric; every reader of this store, and every byte of the widget API, was
// written against integers. A boolean column is therefore read as 0 or 1, and a numeric column as
// an integer when it carries no fractional part and as a double otherwise. A column whose declared
// type is boolean does not exist in the schema, so nothing is lost by this.

// driverName is the database/sql driver this file registers.
const driverName = "opnview-postgres"

var registerDriver sync.Once

// openPool opens a database/sql pool over the connection string.
func openPool(dsn string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: reading the database connection parameter: %w", err)
	}
	return openPoolWithConfig(config)
}

// openPoolWithConfig opens a database/sql pool over a parsed connection.
func openPoolWithConfig(config *pgx.ConnConfig) (*sql.DB, error) {
	config.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = searchPath
	config.RuntimeParams["application_name"] = "opnview"
	return sql.OpenDB(connector{inner: stdlib.GetConnector(*config)}), nil
}

// connector opens rewriting connections.
type connector struct{ inner driver.Connector }

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rewritingConn{inner: conn}, nil
}

func (c connector) Driver() driver.Driver { return rewritingDriver{} }

// rewritingDriver exists because database/sql asks a connector for its driver; opnview opens
// every pool through a connector.
type rewritingDriver struct{}

func (rewritingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("store: open the database through store.Open")
}

func init() {
	registerDriver.Do(func() { sql.Register(driverName, rewritingDriver{}) })
}

// rewritingConn is one connection.
type rewritingConn struct{ inner driver.Conn }

var (
	_ driver.QueryerContext     = (*rewritingConn)(nil)
	_ driver.ExecerContext      = (*rewritingConn)(nil)
	_ driver.ConnPrepareContext = (*rewritingConn)(nil)
	_ driver.ConnBeginTx        = (*rewritingConn)(nil)
	_ driver.NamedValueChecker  = (*rewritingConn)(nil)
	_ driver.SessionResetter    = (*rewritingConn)(nil)
	_ driver.Validator          = (*rewritingConn)(nil)
	_ driver.Pinger             = (*rewritingConn)(nil)
)

func (c *rewritingConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext rewrites a statement once; it is prepared on the server by the driver's statement
// cache the first time a connection runs its bound text, so a second call prepares nothing new on
// that connection.
func (c *rewritingConn) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	rewritten, err := rewriteParameters(query)
	if err != nil {
		return nil, err
	}
	return &rewritingStmt{conn: c, statement: rewritten}, nil
}

func (c *rewritingConn) Close() error { return c.inner.Close() }

func (c *rewritingConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *rewritingConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.inner.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c *rewritingConn) Ping(ctx context.Context) error { return c.inner.(driver.Pinger).Ping(ctx) }

func (c *rewritingConn) ResetSession(ctx context.Context) error {
	return c.inner.(driver.SessionResetter).ResetSession(ctx)
}

func (c *rewritingConn) IsValid() bool {
	if validator, ok := c.inner.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

// CheckNamedValue converts an argument as database/sql would for any driver -- a pointer is
// dereferenced, an int is an int64 -- and a boolean to 0 or 1, as every flag column holds it. A
// pgx query mode passes through, for the catalogue's bound queries.
func (c *rewritingConn) CheckNamedValue(value *driver.NamedValue) error {
	switch typed := value.Value.(type) {
	case pgx.QueryExecMode:
		return nil
	case bool:
		if typed {
			value.Value = int64(1)
		} else {
			value.Value = int64(0)
		}
		return nil
	}
	converted, err := driver.DefaultParameterConverter.ConvertValue(value.Value)
	if err != nil {
		return err
	}
	if flag, isBool := converted.(bool); isBool {
		converted = int64(0)
		if flag {
			converted = int64(1)
		}
	}
	value.Value = converted
	return nil
}

func (c *rewritingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rewritten, err := rewriteParameters(query)
	if err != nil {
		return nil, err
	}
	return c.query(ctx, rewritten, args)
}

func (c *rewritingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	rewritten, err := rewriteParameters(query)
	if err != nil {
		return nil, err
	}
	return c.exec(ctx, rewritten, args)
}

func (c *rewritingConn) query(ctx context.Context, statement rewrittenStatement, args []driver.NamedValue) (
	driver.Rows, error) {
	text, ordered, err := statement.bind(args)
	if err != nil {
		return nil, err
	}
	rows, err := c.inner.(driver.QueryerContext).QueryContext(ctx, text, ordered)
	if err != nil {
		return nil, err
	}
	return newShapedRows(rows), nil
}

func (c *rewritingConn) exec(ctx context.Context, statement rewrittenStatement, args []driver.NamedValue) (
	driver.Result, error) {
	text, ordered, err := statement.bind(args)
	if err != nil {
		return nil, err
	}
	return c.inner.(driver.ExecerContext).ExecContext(ctx, text, ordered)
}

// rewritingStmt is a statement rewritten once and bound at every execution.
type rewritingStmt struct {
	conn      *rewritingConn
	statement rewrittenStatement
}

var (
	_ driver.StmtQueryContext  = (*rewritingStmt)(nil)
	_ driver.StmtExecContext   = (*rewritingStmt)(nil)
	_ driver.NamedValueChecker = (*rewritingStmt)(nil)
)

func (s *rewritingStmt) Close() error  { return nil }
func (s *rewritingStmt) NumInput() int { return -1 }

func (s *rewritingStmt) CheckNamedValue(value *driver.NamedValue) error {
	return s.conn.CheckNamedValue(value)
}

func (s *rewritingStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

func (s *rewritingStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

func (s *rewritingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.exec(ctx, s.statement, args)
}

func (s *rewritingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.query(ctx, s.statement, args)
}

func valuesToNamed(values []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(values))
	for index, value := range values {
		named[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return named
}

// rewrittenStatement is a statement in PostgreSQL's numbering, with what each number binds.
type rewrittenStatement struct {
	text string
	// parts and slots are the text around each parameter: parts[k] precedes the parameter
	// bound to names[slots[k]], and the last part follows the last parameter.
	parts []string
	slots []int
	// dollarNumbered is a statement written in PostgreSQL's own numbering, bound as it is.
	dollarNumbered bool
	// names[k] is the name bound to $(k+1), or "" for the k-th positional ? (or $n) parameter.
	names []string
	// positional is how many positional parameters the statement takes.
	positional int
}

// arguments orders the arguments a statement was handed by its numbering. A query mode passes
// through, first.
func (r rewrittenStatement) arguments(args []driver.NamedValue) ([]driver.NamedValue, error) {
	var options []driver.NamedValue
	byName := map[string]driver.Value{}
	var positional []driver.Value
	for _, arg := range args {
		if mode, isMode := arg.Value.(pgx.QueryExecMode); isMode {
			options = append(options, driver.NamedValue{Value: mode})
			continue
		}
		if arg.Name != "" {
			byName[arg.Name] = arg.Value
			continue
		}
		positional = append(positional, arg.Value)
	}
	ordered := make([]driver.NamedValue, 0, len(options)+len(r.names))
	ordered = append(ordered, options...)
	if len(r.names) == 0 {
		for index, value := range positional {
			ordered = append(ordered, driver.NamedValue{Ordinal: len(ordered) + 1, Value: value})
			_ = index
		}
		return renumber(ordered), nil
	}
	next := 0
	for _, name := range r.names {
		if name == "" {
			if next >= len(positional) {
				return nil, fmt.Errorf("store: the statement takes more positional arguments than the %d it was handed",
					len(positional))
			}
			ordered = append(ordered, driver.NamedValue{Value: positional[next]})
			next++
			continue
		}
		value, present := byName[name]
		if !present {
			return nil, fmt.Errorf("store: the statement names :%s and no argument of that name was handed", name)
		}
		ordered = append(ordered, driver.NamedValue{Value: value})
	}
	return renumber(ordered), nil
}

// bind returns the text to run and its arguments: each non-NULL argument bound to its own number,
// cast to the type its Go value has, and each NULL written as NULL, so a statement types its
// parameters as it typed them as literals.
func (r rewrittenStatement) bind(args []driver.NamedValue) (string, []driver.NamedValue, error) {
	ordered, err := r.arguments(args)
	if err != nil {
		return "", nil, err
	}
	if r.dollarNumbered || len(r.names) == 0 {
		return r.text, ordered, nil
	}
	var options []driver.NamedValue
	for len(ordered) > 0 {
		if _, isMode := ordered[0].Value.(pgx.QueryExecMode); !isMode {
			break
		}
		options = append(options, ordered[0])
		ordered = ordered[1:]
	}
	numbers := make([]int, len(r.names))
	bound := make([]driver.NamedValue, 0, len(options)+len(ordered))
	bound = append(bound, options...)
	var text strings.Builder
	for index, slot := range r.slots {
		text.WriteString(r.parts[index])
		value := ordered[slot].Value
		if value == nil {
			text.WriteString("NULL")
			continue
		}
		if numbers[slot] == 0 {
			bound = append(bound, driver.NamedValue{Value: value})
			numbers[slot] = len(bound) - len(options)
		}
		placeholder := "$" + strconv.Itoa(numbers[slot])
		if cast := castOf(value); cast != "" {
			placeholder = "CAST(" + placeholder + " AS " + cast + ")"
		}
		text.WriteString(placeholder)
	}
	text.WriteString(r.parts[len(r.parts)-1])
	return text.String(), renumber(bound), nil
}

// castOf is the PostgreSQL type a Go value is bound as.
func castOf(value driver.Value) string {
	switch value.(type) {
	case int64:
		return "BIGINT"
	case float64:
		return "DOUBLE PRECISION"
	case string:
		return "TEXT"
	case []byte:
		return "BYTEA"
	}
	return ""
}

// renumber gives each argument its ordinal.
func renumber(args []driver.NamedValue) []driver.NamedValue {
	for index := range args {
		args[index].Ordinal = index + 1
		args[index].Name = ""
	}
	return args
}

var rewriteCache sync.Map

// rewriteParameters rewrites a statement's :name and ? parameters to PostgreSQL's $n. A statement
// already in PostgreSQL's numbering, or with no parameter, is returned unchanged.
func rewriteParameters(query string) (rewrittenStatement, error) {
	if cached, found := rewriteCache.Load(query); found {
		return cached.(rewrittenStatement), nil
	}
	rewritten, err := rewrite(query)
	if err != nil {
		return rewrittenStatement{}, err
	}
	rewriteCache.Store(query, rewritten)
	return rewritten, nil
}

func rewrite(query string) (rewrittenStatement, error) {
	var out strings.Builder
	out.Grow(len(query) + 16)
	result := rewrittenStatement{}
	numberOf := map[string]int{}
	dollarNumbered := false
	cut := 0
	slot := func(number int) {
		result.parts = append(result.parts, out.String()[cut:])
		result.slots = append(result.slots, number-1)
		out.WriteString("$" + strconv.Itoa(number))
		cut = out.Len()
	}
	length := len(query)
	for i := 0; i < length; {
		character := query[i]
		switch {
		case character == '\'' || character == '"':
			end := i + 1
			for end < length {
				if query[end] == character {
					if end+1 < length && query[end+1] == character {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= length {
				return rewrittenStatement{}, fmt.Errorf("store: an unterminated quote in the statement")
			}
			out.WriteString(query[i : end+1])
			i = end + 1
		case character == '-' && i+1 < length && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				end = length - i
			}
			out.WriteString(query[i : i+end])
			i += end
		case character == '/' && i+1 < length && query[i+1] == '*':
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return rewrittenStatement{}, fmt.Errorf("store: an unterminated comment in the statement")
			}
			out.WriteString(query[i : i+2+end+2])
			i += 2 + end + 2
		case character == '$' && i+1 < length && (query[i+1] == '$' || isIdentifierStart(query[i+1])):
			// A dollar-quoted body: $$...$$ or $tag$...$tag$.
			tagEnd := strings.IndexByte(query[i+1:], '$')
			if tagEnd < 0 {
				out.WriteByte(character)
				i++
				continue
			}
			tag := query[i : i+1+tagEnd+1]
			if !validDollarTag(tag) {
				out.WriteByte(character)
				i++
				continue
			}
			end := strings.Index(query[i+len(tag):], tag)
			if end < 0 {
				return rewrittenStatement{}, fmt.Errorf("store: an unterminated dollar-quoted body in the statement")
			}
			stop := i + len(tag) + end + len(tag)
			out.WriteString(query[i:stop])
			i = stop
		case character == '$' && i+1 < length && query[i+1] >= '0' && query[i+1] <= '9':
			dollarNumbered = true
			out.WriteByte(character)
			i++
		case character == ':' && i+1 < length && query[i+1] == ':':
			out.WriteString("::")
			i += 2
		case character == ':' && i+1 < length && isIdentifierStart(query[i+1]) && (i == 0 || query[i-1] != ':'):
			end := i + 1
			for end < length && isParameterCharacter(query[end]) {
				end++
			}
			name := query[i+1 : end]
			number, seen := numberOf[name]
			if !seen {
				result.names = append(result.names, name)
				number = len(result.names)
				numberOf[name] = number
			}
			slot(number)
			i = end
		case character == '?':
			result.names = append(result.names, "")
			result.positional++
			slot(len(result.names))
			i++
		default:
			out.WriteByte(character)
			i++
		}
	}
	if dollarNumbered && len(result.names) > 0 {
		return rewrittenStatement{}, fmt.Errorf("store: a statement mixes $n with :name or ? parameters")
	}
	result.text = out.String()
	result.parts = append(result.parts, result.text[cut:])
	result.dollarNumbered = dollarNumbered
	return result, nil
}

func isIdentifierStart(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func validDollarTag(tag string) bool {
	if len(tag) < 2 || tag[0] != '$' || tag[len(tag)-1] != '$' {
		return false
	}
	for index := 1; index < len(tag)-1; index++ {
		if !isParameterCharacter(tag[index]) {
			return false
		}
	}
	return true
}

// shapedRows reads a boolean as 0 or 1 and a numeric as an integer or a double.
type shapedRows struct {
	driver.Rows
	numeric []bool
	boolean []bool
}

func newShapedRows(rows driver.Rows) driver.Rows {
	shaped := &shapedRows{Rows: rows}
	columns := rows.Columns()
	shaped.numeric = make([]bool, len(columns))
	shaped.boolean = make([]bool, len(columns))
	if typed, ok := rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		for index := range columns {
			switch typed.ColumnTypeDatabaseTypeName(index) {
			case "NUMERIC":
				shaped.numeric[index] = true
			case "BOOL":
				shaped.boolean[index] = true
			}
		}
	}
	return shaped
}

func (r *shapedRows) Next(dest []driver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	for index, value := range dest {
		switch {
		case r.boolean[index]:
			if flag, ok := value.(bool); ok {
				dest[index] = int64(0)
				if flag {
					dest[index] = int64(1)
				}
			}
		case r.numeric[index]:
			if text, ok := value.(string); ok {
				dest[index] = numericValue(text)
			}
		}
	}
	return nil
}

// numericValue reads a numeric's text as an integer when it has no fractional part, and as a
// double otherwise.
func numericValue(text string) driver.Value {
	if !strings.ContainsAny(text, ".eE") {
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			return integer
		}
	}
	if real, err := strconv.ParseFloat(text, 64); err == nil {
		return real
	}
	return text
}

// The column type helpers database/sql asks a driver's rows for, passed through.
func (r *shapedRows) ColumnTypeDatabaseTypeName(index int) string {
	if typed, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return typed.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}
