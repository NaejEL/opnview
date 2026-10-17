package webtest

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The database scan: every table of every schema -- the core's, every plugin's, the aside schema
// and the mirror's -- searched for a value, or rendered whole (P-AC23).

// scannedTable is one table of the scan, with its columns and their types.
type scannedTable struct {
	name    string
	columns []scannedColumn
}

type scannedColumn struct {
	name     string
	dataType string
}

// DatabaseContains returns "table.column" where value was found, or the empty string.
//
// A text column is searched as text, a binary one as bytes, and any other column through its text
// rendering, so a value stored as text, as bytes or in a structure is found whichever it was.
func DatabaseContains(t *testing.T, harness *Harness, value string) string {
	t.Helper()
	if value == "" {
		t.Fatal("scanning the database for the empty string would match everything")
	}
	// One statement per table, a count per column side by side, rather than one per column; the
	// first column holding the value, in the scan's order, is the one named.
	for _, table := range databaseTables(t, harness.Store.DB()) {
		counts := make([]string, 0, len(table.columns))
		// Every condition reads the value as $1.
		arguments := []any{value}
		for _, column := range table.columns {
			condition := fmt.Sprintf("strpos(CAST(%s AS TEXT), $1) > 0", quoteIdentifier(column.name))
			if column.dataType == "bytea" {
				condition = fmt.Sprintf("position(convert_to($1, 'UTF8') IN %s) > 0", quoteIdentifier(column.name))
			}
			counts = append(counts, "count(*) FILTER (WHERE "+condition+")")
		}
		hits := make([]int, len(table.columns))
		destinations := make([]any, len(table.columns))
		for index := range hits {
			destinations[index] = &hits[index]
		}
		if err := harness.Store.DB().QueryRow("SELECT "+strings.Join(counts, ", ")+" FROM "+table.name,
			arguments...).Scan(destinations...); err != nil {
			t.Fatalf("scanning %s: %v", table.name, err)
		}
		for index, found := range hits {
			if found > 0 {
				return table.name + "." + table.columns[index].name
			}
		}
	}
	return ""
}

func databaseTables(t *testing.T, db *sql.DB) []scannedTable {
	t.Helper()
	rows, err := db.Query(`
		SELECT quote_ident(c.table_schema) || '.' || quote_ident(c.table_name), c.column_name, c.data_type
		FROM information_schema.columns AS c
		JOIN information_schema.tables AS r
		  ON r.table_schema = c.table_schema AND r.table_name = c.table_name AND r.table_type = 'BASE TABLE'
		WHERE c.table_schema NOT IN ('pg_catalog', 'information_schema', 'public')
		  AND c.table_schema NOT LIKE '\_timescaledb%'
		  AND c.table_schema NOT LIKE 'timescaledb\_%'
		ORDER BY 1, c.ordinal_position`)
	if err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []scannedTable
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			t.Fatalf("reading a table name: %v", err)
		}
		if len(tables) == 0 || tables[len(tables)-1].name != table {
			tables = append(tables, scannedTable{name: table})
		}
		last := &tables[len(tables)-1]
		last.columns = append(last.columns, scannedColumn{name: column, dataType: dataType})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("the database holds no tables, so this scan proves nothing")
	}
	return tables
}

// TableColumns lists one table's columns; the table is named as DatabaseTables names it, or by its
// own name for a core table.
func TableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	for _, scanned := range databaseTables(t, db) {
		if scanned.name == table || scanned.name == "opnview."+table {
			var columns []string
			for _, column := range scanned.columns {
				columns = append(columns, column.name)
			}
			return columns
		}
	}
	t.Fatalf("no table %s", table)
	return nil
}

// DatabaseBytesExcept renders every value of every table as text, some tables left out: the ones
// a test knows a request moves, such as the session row a signed-in request touches. A binary
// value is rendered as its bytes, so a plaintext stored as bytes is visible in the rendering.
func DatabaseBytesExcept(t *testing.T, harness *Harness, skipped ...string) string {
	t.Helper()
	var builder strings.Builder
	for _, table := range databaseTables(t, harness.Store.DB()) {
		bare := table.name[strings.LastIndexByte(table.name, '.')+1:]
		if slices.Contains(skipped, table.name) || slices.Contains(skipped, bare) {
			continue
		}
		// ONE READ OF THE TABLE, its columns side by side, and the rendering column by column as
		// before: every value of the first column, then every value of the second. One statement
		// per table rather than one per column, which made a scan of the whole database cost about
		// as many statements as the database has columns.
		expressions := make([]string, 0, len(table.columns))
		for _, column := range table.columns {
			expression := "CAST(" + quoteIdentifier(column.name) + " AS TEXT)"
			if column.dataType == "bytea" {
				expression = "encode(" + quoteIdentifier(column.name) + ", 'escape')"
			}
			expressions = append(expressions, expression)
		}
		rows, err := harness.Store.DB().Query("SELECT " + strings.Join(expressions, ", ") + " FROM " + table.name)
		if err != nil {
			t.Fatalf("rendering %s: %v", table.name, err)
		}
		rendered := make([]strings.Builder, len(table.columns))
		values := make([]sql.NullString, len(table.columns))
		destinations := make([]any, len(table.columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		for rows.Next() {
			if err := rows.Scan(destinations...); err != nil {
				t.Fatalf("rendering %s: %v", table.name, err)
			}
			for index, value := range values {
				if value.Valid {
					rendered[index].WriteString(value.String)
					rendered[index].WriteByte('\n')
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rendering %s: %v", table.name, err)
		}
		_ = rows.Close()
		for index := range rendered {
			builder.WriteString(rendered[index].String())
		}
	}
	return builder.String()
}

// quoteIdentifier quotes a column name.
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
