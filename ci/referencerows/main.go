// Command referencerows compares one query's PostgreSQL result with the result the same query
// gave on SQLite before the store moved (P-AC6 of specs/SPEC-backend-completion-plugins.md, the
// sql/queries half). sql/testdata/sqlite_reference_queries.json holds, under "screen: <Name>" and
// "diagnostic: <Name>", the rows each query of sql/queries returned on the 100 000-row seed,
// recorded with sqlite3's JSON mode; sql/schema-checks.sh runs the same query on the same seed in
// PostgreSQL, one row_to_json object per line, and hands both here.
//
//	referencerows -reference <file> -key "screen: Overview" -actual <file> -order exact|multiset
//
// The rows are compared TYPED: the same column names; numbers equal numerically, an integer and
// the same value written as a decimal being equal, and two floating values equal within a
// relative 1e-9; text equal byte for byte; null equal null and nothing else. A PostgreSQL
// boolean is compared with SQLite's 0 or 1, which is how SQLite stores one, and the difference
// of spelling is reported as a note, never silently. With -order exact the rows are compared in
// order; with -order multiset, for a query whose ORDER BY does not make the order total, each
// row must be matched once, in any order. A column named with -list-columns holds a list SQLite
// built with group_concat, whose element order SQLite leaves undefined: its elements are compared
// as a multiset, each byte for byte, and a different order is reported as a note. The exit status is 0 when they are equal, 1 when they
// differ -- the first differing row printed -- and 2 when the input cannot be read.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strings"
)

func main() {
	reference := flag.String("reference", "", "the SQLite reference file")
	key := flag.String("key", "", "the query's key in the reference file")
	actual := flag.String("actual", "", "the PostgreSQL rows, one JSON object per line")
	order := flag.String("order", "", "exact or multiset")
	lists := flag.String("list-columns", "", "comma-separated columns holding a group_concat list whose "+
		"element order SQLite left undefined; their elements are compared as a multiset")
	flag.Parse()
	for _, column := range strings.Split(*lists, ",") {
		if column = strings.TrimSpace(column); column != "" {
			listColumns[column] = true
		}
	}
	if *reference == "" || *key == "" || *actual == "" || (*order != "exact" && *order != "multiset") {
		fmt.Fprintln(os.Stderr, "referencerows: -reference, -key, -actual and -order exact|multiset are required")
		os.Exit(2)
	}
	expected, err := readReference(*reference, *key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "referencerows:", err)
		os.Exit(2)
	}
	got, err := readActual(*actual)
	if err != nil {
		fmt.Fprintln(os.Stderr, "referencerows:", err)
		os.Exit(2)
	}
	notes := map[string]bool{}
	var difference string
	if *order == "exact" {
		difference = compareExact(expected, got, notes)
	} else {
		difference = compareMultiset(expected, got, notes)
	}
	for _, note := range sortedKeys(notes) {
		fmt.Println("note:", note)
	}
	if difference != "" {
		fmt.Println(difference)
		os.Exit(1)
	}
}

// row is one result row, its values decoded with json.Number so no number is rounded on reading.
type row map[string]any

func decode(raw []byte) (row, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value row
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func readReference(path, key string) ([]row, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var queries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &queries); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	entry, found := queries[key]
	if !found {
		return nil, fmt.Errorf("%s holds no %q", path, key)
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(entry, &raws); err != nil {
		return nil, fmt.Errorf("reading %q: %w", key, err)
	}
	rows := make([]row, 0, len(raws))
	for _, item := range raws {
		value, err := decode(item)
		if err != nil {
			return nil, fmt.Errorf("reading a row of %q: %w", key, err)
		}
		rows = append(rows, value)
	}
	return rows, nil
}

func readActual(path string) ([]row, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	var rows []row
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		value, err := decode(line)
		if err != nil {
			return nil, fmt.Errorf("reading a PostgreSQL row: %w", err)
		}
		rows = append(rows, value)
	}
	return rows, scanner.Err()
}

func compareExact(expected, got []row, notes map[string]bool) string {
	for index := 0; index < len(expected) && index < len(got); index++ {
		if reason := rowsDiffer(expected[index], got[index], notes); reason != "" {
			return fmt.Sprintf("row %d differs (%s):\n  sqlite:     %s\n  postgresql: %s", index+1, reason,
				show(expected[index]), show(got[index]))
		}
	}
	if len(expected) != len(got) {
		return fmt.Sprintf("sqlite returned %d rows, postgresql %d", len(expected), len(got))
	}
	return ""
}

func compareMultiset(expected, got []row, notes map[string]bool) string {
	if len(expected) != len(got) {
		return fmt.Sprintf("sqlite returned %d rows, postgresql %d", len(expected), len(got))
	}
	used := make([]bool, len(got))
	for index, want := range expected {
		matched := false
		for candidate, have := range got {
			if used[candidate] {
				continue
			}
			if rowsDiffer(want, have, notes) == "" {
				used[candidate], matched = true, true
				break
			}
		}
		if !matched {
			closest := ""
			for candidate, have := range got {
				if !used[candidate] {
					closest = fmt.Sprintf("\n  nearest unmatched postgresql row: %s (%s)", show(have),
						rowsDiffer(want, have, map[string]bool{}))
					break
				}
			}
			return fmt.Sprintf("sqlite row %d has no equal postgresql row:\n  sqlite:     %s%s", index+1, show(want),
				closest)
		}
	}
	return ""
}

// rowsDiffer says how two rows differ, or "" when they are equal.
func rowsDiffer(want, have row, notes map[string]bool) string {
	if columns := sortedKeys(want); strings.Join(columns, ",") != strings.Join(sortedKeys(have), ",") {
		return fmt.Sprintf("columns %v against %v", columns, sortedKeys(have))
	}
	for _, column := range sortedKeys(want) {
		if !valuesEqual(want[column], have[column], column, notes) {
			return "column " + column
		}
	}
	return ""
}

func valuesEqual(want, have any, column string, notes map[string]bool) bool {
	switch w := want.(type) {
	case nil:
		return have == nil
	case string:
		h, isString := have.(string)
		if !isString {
			return false
		}
		if h != w && listColumns[column] && sameElements(w, h) {
			notes[fmt.Sprintf("column %s holds the same elements in another order: SQLite's group_concat "+
				"left the order undefined", column)] = true
			return true
		}
		return h == w
	case json.Number:
		switch h := have.(type) {
		case json.Number:
			return numbersEqual(w, h)
		case bool:
			equal := (h && w.String() == "1") || (!h && w.String() == "0")
			if equal {
				notes[fmt.Sprintf("column %s is a boolean in PostgreSQL and 0 or 1 in SQLite", column)] = true
			}
			return equal
		}
		return false
	case bool:
		h, isBool := have.(bool)
		return isBool && h == w
	}
	return false
}

// listColumns are the columns given with -list-columns.
var listColumns = map[string]bool{}

// sameElements says whether two comma-separated lists hold the same elements, each as often,
// in any order. Every element is compared byte for byte.
func sameElements(want, have string) bool {
	wantElements, haveElements := strings.Split(want, ","), strings.Split(have, ",")
	sort.Strings(wantElements)
	sort.Strings(haveElements)
	return strings.Join(wantElements, ",") == strings.Join(haveElements, ",")
}

// numbersEqual compares two JSON numbers: exactly when both are integers, within a relative 1e-9
// otherwise.
func numbersEqual(want, have json.Number) bool {
	wantInt, wantIsInt := new(big.Int).SetString(want.String(), 10)
	haveInt, haveIsInt := new(big.Int).SetString(have.String(), 10)
	if wantIsInt && haveIsInt {
		return wantInt.Cmp(haveInt) == 0
	}
	wantFloat, wantErr := want.Float64()
	haveFloat, haveErr := have.Float64()
	if wantErr != nil || haveErr != nil {
		return false
	}
	if wantFloat == haveFloat {
		return true
	}
	scale := math.Max(math.Abs(wantFloat), math.Abs(haveFloat))
	return math.Abs(wantFloat-haveFloat) <= 1e-9*scale
}

func show(value row) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
