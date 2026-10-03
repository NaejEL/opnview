package web

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Scanning the whole database for a value.
//
// WHY IT SCANS EVERY TABLE AND EVERY COLUMN rather than the one it expects. Several
// criteria are of the form "this value is nowhere in the database": the password, the
// setup token, the key file, a plaintext credential, a ciphertext that has been
// replaced. A test that looked only in the column it thought was involved would pass
// on a value that had leaked into a different one — which is exactly the accident
// worth catching, because nobody writes a secret into a column on purpose.

// databaseContains returns "table.column" where value was found, or the empty string.
//
// It compares text and blob columns as text. A value that was stored hex-encoded, or
// stored as bytes, is therefore found either way.
func databaseContains(t *testing.T, harness *harness, value string) string {
	t.Helper()
	if value == "" {
		t.Fatal("scanning the database for the empty string would match everything")
	}
	for _, table := range databaseTables(t, harness.store.DB()) {
		for _, column := range tableColumns(t, harness.store.DB(), table) {
			var hits int
			query := fmt.Sprintf(
				`SELECT count(*) FROM "%s" WHERE instr(CAST("%s" AS TEXT), ?) > 0`,
				table, column)
			if err := harness.store.DB().QueryRow(query, value).Scan(&hits); err != nil {
				// A column whose contents cannot be cast to text holds no string, so it
				// cannot hold this one.
				continue
			}
			if hits > 0 {
				return table + "." + column
			}
		}
	}
	return ""
}

// databaseTables lists the tables, views excluded: a view holds no rows of its own,
// so scanning one would report the table beneath it twice.
func databaseTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`)
	if err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("reading a table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("the database holds no tables, so this scan proves nothing")
	}
	return tables
}

// tableColumns lists one table's columns.
func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, table))
	if err != nil {
		t.Fatalf("listing the columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("reading a column name: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the columns of %s: %v", table, err)
	}
	return columns
}

// databaseBytes returns every text and blob value in the database, concatenated.
//
// It is the coarser instrument: where databaseContains answers "is this string
// anywhere", this answers "what is in there at all", which is what a test asserting
// the ABSENCE of a previous ciphertext needs — the previous value is bytes rather
// than a string somebody chose.
func databaseBytes(t *testing.T, harness *harness) string {
	t.Helper()
	return databaseBytesExcept(t, harness)
}

// databaseBytesExcept is databaseBytes with some tables left out: the ones a test
// knows a request moves, such as the session row a signed-in request touches.
func databaseBytesExcept(t *testing.T, harness *harness, skipped ...string) string {
	t.Helper()
	var builder strings.Builder
	for _, table := range databaseTables(t, harness.store.DB()) {
		if slices.Contains(skipped, table) {
			continue
		}
		for _, column := range tableColumns(t, harness.store.DB(), table) {
			rows, err := harness.store.DB().Query(
				fmt.Sprintf(`SELECT CAST("%s" AS TEXT) FROM "%s"`, column, table))
			if err != nil {
				continue
			}
			for rows.Next() {
				var value sql.NullString
				if err := rows.Scan(&value); err != nil {
					continue
				}
				if value.Valid {
					builder.WriteString(value.String)
					builder.WriteByte('\n')
				}
			}
			_ = rows.Err()
			_ = rows.Close()
		}
	}
	return builder.String()
}
