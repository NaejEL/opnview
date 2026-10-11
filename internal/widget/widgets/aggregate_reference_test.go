package widgets

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The engine-equivalence record of the aggregates (P-AC6 of the backend-completion cycle).
//
// testdata/sqlite_reference_aggregates.json.gz holds every row of every aggregate table -- the six
// families at the four periods, and purged_flow_hour -- of the default fixture, written on SQLite
// before the store moved to PostgreSQL. This test asserts the store answers the same rows, value
// for value, whatever engine holds them. To rewrite the record, run it with
// OPNVIEW_WRITE_SQLITE_REFERENCE=1; that happens in no suite.

// referenceAggregateTables are the tables the record covers.
func referenceAggregateTables() []string {
	var tables []string
	for _, family := range []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume",
		"rule_volume"} {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			tables = append(tables, family+"_aggregate_"+period)
		}
	}
	return append(tables, "purged_flow_hour")
}

// dumpTable reads every row of a table as column-named values, rows in a stable order.
func dumpTable(t *testing.T, f *fixture, table string) []string {
	t.Helper()
	rows, err := f.db.DB().QueryContext(context.Background(), "SELECT * FROM "+table)
	check(t, err)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	check(t, err)
	var dumped []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		check(t, rows.Scan(pointers...))
		row := map[string]any{}
		for index, column := range columns {
			if column == "id" {
				// The surrogate key: which number a slot's row was given depends on the order the
				// engine produced the groups in, and means nothing; the slot is its other columns.
				continue
			}
			switch value := values[index].(type) {
			case []byte:
				row[column] = string(value)
			case int64, float64, string, nil, bool:
				row[column] = value
			default:
				row[column] = fmt.Sprint(value)
			}
		}
		encoded, err := json.Marshal(row)
		check(t, err)
		dumped = append(dumped, string(encoded))
	}
	check(t, rows.Err())
	sort.Strings(dumped)
	return dumped
}

// TestTheAggregatesEqualTheSQLiteReference is P-AC6's aggregate half.
func TestTheAggregatesEqualTheSQLiteReference(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	record := map[string][]string{}
	for _, table := range referenceAggregateTables() {
		record[table] = dumpTable(t, f, table)
	}
	path := filepath.Join("testdata", "sqlite_reference_aggregates.json.gz")
	if os.Getenv("OPNVIEW_WRITE_SQLITE_REFERENCE") == "1" {
		encoded, err := json.Marshal(record)
		check(t, err)
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, err = writer.Write(encoded)
		check(t, err)
		check(t, writer.Close())
		check(t, os.WriteFile(path, compressed.Bytes(), 0o644))
		return
	}
	file, err := os.Open(path)
	check(t, err)
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	check(t, err)
	raw, err := io.ReadAll(reader)
	check(t, err)
	var reference map[string][]string
	check(t, json.Unmarshal(raw, &reference))
	total := 0
	for _, table := range referenceAggregateTables() {
		want, got := withoutID(reference[table]), record[table]
		total += len(want)
		if len(want) != len(got) {
			t.Errorf("%s holds %d rows, the reference %d", table, len(got), len(want))
			continue
		}
		for index := range want {
			if !sameRow(want[index], got[index]) {
				t.Errorf("%s differs from the reference:\n got  %s\n want %s", table, got[index], want[index])
				break
			}
		}
	}
	if total == 0 {
		t.Error("the reference holds no aggregate row, so this comparison is vacuous")
	}
}

// sameRow compares two dumped rows as JSON values, so a number is compared as a number.
func sameRow(want, got string) bool {
	var left, right map[string]any
	if json.Unmarshal([]byte(want), &left) != nil || json.Unmarshal([]byte(got), &right) != nil {
		return want == got
	}
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, present := right[key]
		if !present || fmt.Sprint(value) != fmt.Sprint(other) {
			return false
		}
	}
	return true
}

// withoutID drops the surrogate key from recorded rows and sorts them again, as dumpTable renders
// them.
func withoutID(rows []string) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		var decoded map[string]any
		if json.Unmarshal([]byte(row), &decoded) != nil {
			result = append(result, row)
			continue
		}
		delete(decoded, "id")
		encoded, _ := json.Marshal(decoded)
		result = append(result, string(encoded))
	}
	sort.Strings(result)
	return result
}
