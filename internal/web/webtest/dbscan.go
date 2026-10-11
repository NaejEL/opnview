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
	for _, table := range databaseTables(t, harness.Store.DB()) {
		for _, column := range table.columns {
			var hits int
			query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE strpos(CAST(%s AS TEXT), ?) > 0`,
				table.name, quoteIdentifier(column.name))
			if column.dataType == "bytea" {
				query = fmt.Sprintf(`SELECT count(*) FROM %s WHERE position(convert_to(?, 'UTF8') IN %s) > 0`,
					table.name, quoteIdentifier(column.name))
			}
			if err := harness.Store.DB().QueryRow(query, value).Scan(&hits); err != nil {
				t.Fatalf("scanning %s.%s: %v", table.name, column.name, err)
			}
			if hits > 0 {
				return table.name + "." + column.name
			}
		}
	}
	return ""
}

// DatabaseTables lists the tables of every schema but PostgreSQL's and TimescaleDB's own, each as
// schema.table; views are excluded: a view holds no rows of its own, so scanning one would report
// the table beneath it twice.
func DatabaseTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var names []string
	for _, table := range databaseTables(t, db) {
		names = append(names, table.name)
	}
	return names
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
		for _, column := range table.columns {
			expression := "CAST(" + quoteIdentifier(column.name) + " AS TEXT)"
			if column.dataType == "bytea" {
				expression = "encode(" + quoteIdentifier(column.name) + ", 'escape')"
			}
			rows, err := harness.Store.DB().Query("SELECT " + expression + " FROM " + table.name)
			if err != nil {
				t.Fatalf("rendering %s.%s: %v", table.name, column.name, err)
			}
			for rows.Next() {
				var value sql.NullString
				if err := rows.Scan(&value); err != nil {
					t.Fatalf("rendering %s.%s: %v", table.name, column.name, err)
				}
				if value.Valid {
					builder.WriteString(value.String)
					builder.WriteByte('\n')
				}
			}
			_ = rows.Close()
		}
	}
	return builder.String()
}

// quoteIdentifier quotes a column name.
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
