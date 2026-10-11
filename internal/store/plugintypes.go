package store

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Values of the plugin type vocabulary (PS4 of the push amendment), written and read.
//
// EVERY VALUE IS BOUND, AS TEXT OR A NUMBER, AND CAST IN THE STATEMENT. A boolean, an address, a MAC
// address and an instant each travel as text and are cast by PostgreSQL to the column's own type
// (CAST(? AS inet), ...), so a value that is not one is refused by the database rather than
// coerced, and the driver's rule that a value is bound as the type its Go value has (driver.go)
// holds for every column.

// placeholder is the parameter of one column in an INSERT.
func (c PluginColumn) placeholder() string {
	switch c.Type {
	case "boolean", "ip", "mac", pluginTypeInstant:
		return "CAST(? AS " + PluginColumnTypes[c.Type] + ")"
	}
	return "?"
}

// pluginArgument turns a row's value into the argument its column's placeholder takes: an int64,
// a float64 or text; nil stays nil. A value of the wrong kind is an error naming the column.
func pluginArgument(column PluginColumn, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	invalid := func() error {
		return fmt.Errorf("the column %s is %s and was given %T", column.Name, column.Type, value)
	}
	switch column.Type {
	case "integer":
		switch typed := value.(type) {
		case int64:
			return typed, nil
		case int:
			return int64(typed), nil
		case float64:
			if typed != math.Trunc(typed) {
				return nil, invalid()
			}
			return int64(typed), nil
		}
	case "real":
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case int64:
			return float64(typed), nil
		case int:
			return float64(typed), nil
		}
	case "text":
		if typed, ok := value.(string); ok {
			return typed, nil
		}
	case "boolean":
		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed), nil
		case string:
			return typed, nil
		}
	case "ip":
		switch typed := value.(type) {
		case netip.Addr:
			return typed.String(), nil
		case string:
			return typed, nil
		}
	case "mac":
		switch typed := value.(type) {
		case net.HardwareAddr:
			return typed.String(), nil
		case string:
			return typed, nil
		}
	case pluginTypeInstant:
		switch typed := value.(type) {
		case time.Time:
			return typed.UTC().Format(time.RFC3339Nano), nil
		case int64:
			return time.Unix(typed, 0).UTC().Format(time.RFC3339Nano), nil
		case float64:
			seconds, fraction := math.Modf(typed)
			return time.Unix(int64(seconds), int64(math.Round(fraction*1e9))).UTC().Format(time.RFC3339Nano), nil
		case string:
			return typed, nil
		}
	}
	return nil, invalid()
}

// maxBoundParameters bounds the parameters of one statement, below PostgreSQL's 65535.
const maxBoundParameters = 60000

// InsertPluginRows writes rows into one plugin table in ONE transaction, as multi-row INSERTs of at
// most maxBoundParameters parameters each: a push batch of n rows is ceil(n * columns / 60000)
// statements. A row whose primary key is already held replaces it, as WritePluginPass's do.
func (s *Store) InsertPluginRows(ctx context.Context, table PluginTable, rows []map[string]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	held, found := s.LookupPluginTable(table.SQLName())
	if !found {
		return 0, fmt.Errorf("store: %s is not a plugin table ensured on this store", table.SQLName())
	}
	table = held
	rows = lastPerKey(table, rows)
	perStatement := max(1, maxBoundParameters/len(table.Columns))
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
	}
	defer func() { _ = transaction.Rollback() }()
	statements := 0
	for start := 0; start < len(rows); start += perStatement {
		chunk := rows[start:min(len(rows), start+perStatement)]
		statement, arguments, err := table.multiRowInsert(chunk)
		if err != nil {
			return 0, err
		}
		if _, err := transaction.ExecContext(ctx, statement, arguments...); err != nil {
			return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
		}
		statements++
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
	}
	return statements, nil
}

// multiRowInsert is one INSERT of several rows, with the upsert of upsertStatement.
func (t PluginTable) multiRowInsert(rows []map[string]any) (string, []any, error) {
	names := make([]string, 0, len(t.Columns))
	for _, column := range t.Columns {
		names = append(names, quoteIdentifier(column.Name))
	}
	tuples := make([]string, 0, len(rows))
	arguments := make([]any, 0, len(rows)*len(t.Columns))
	for _, row := range rows {
		marks := make([]string, 0, len(t.Columns))
		for _, column := range t.Columns {
			argument, err := pluginArgument(column, row[column.Name])
			if err != nil {
				return "", nil, fmt.Errorf("store: a row of %s: %w", t.SQLName(), err)
			}
			arguments = append(arguments, argument)
			marks = append(marks, column.placeholder())
		}
		tuples = append(tuples, "("+strings.Join(marks, ", ")+")")
	}
	statement := "INSERT INTO " + t.quoted() + " (" + strings.Join(names, ", ") + ") VALUES " +
		strings.Join(tuples, ", ")
	upsert := t.upsertStatement()
	if conflict := strings.Index(upsert, " ON CONFLICT "); conflict >= 0 {
		statement += upsert[conflict:]
	}
	return statement, arguments, nil
}

// timeColumnIsInstant reports whether a table's time column is a timestamp, read from the
// catalogue, so the purge compares it with an instant rather than an epoch.
func timeColumnIsInstant(ctx context.Context, q querier, qualified, column string) (bool, error) {
	var typeName string
	if err := q.QueryRowContext(ctx,
		`SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute AS a
		 WHERE a.attrelid = CAST(? AS regclass) AND a.attname = ? AND NOT a.attisdropped`,
		qualified, column).Scan(&typeName); err != nil {
		return false, fmt.Errorf("store: reading the type of %s.%s: %w", qualified, column, err)
	}
	return strings.HasPrefix(typeName, "timestamp"), nil
}

// lastPerKey keeps, of rows sharing a primary key, the last: one INSERT may not touch a row twice,
// and the last row written is the one a sequence of single writes would have kept.
func lastPerKey(table PluginTable, rows []map[string]any) []map[string]any {
	if len(table.PrimaryKey) == 0 {
		return rows
	}
	position := map[string]int{}
	var kept []map[string]any
	for _, row := range rows {
		parts := make([]string, 0, len(table.PrimaryKey))
		for _, name := range table.PrimaryKey {
			parts = append(parts, fmt.Sprintf("%T:%v", row[name], row[name]))
		}
		key := strings.Join(parts, "\x00")
		if index, seen := position[key]; seen {
			kept[index] = row
			continue
		}
		position[key] = len(kept)
		kept = append(kept, row)
	}
	return kept
}
