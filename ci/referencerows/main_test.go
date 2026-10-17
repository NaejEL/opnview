package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rowsOf decodes JSON objects into rows, failing the test on a malformed one.
func rowsOf(t *testing.T, objects ...string) []row {
	t.Helper()
	rows := make([]row, 0, len(objects))
	for _, object := range objects {
		value, err := decode([]byte(object))
		if err != nil {
			t.Fatalf("decoding %s: %v", object, err)
		}
		rows = append(rows, value)
	}
	return rows
}

// TestAGainedRowIsTakenOutAndTheRestKeepsItsOrder: the named row goes, wherever it is, and the
// rows around it are compared with the reference in their order.
func TestAGainedRowIsTakenOutAndTheRestKeepsItsOrder(t *testing.T) {
	got := rowsOf(t, `{"kind":"a","n":1}`, `{"kind":"gained","n":9}`, `{"kind":"b","n":2}`)
	kept, difference := withoutAdded(got, rowsOf(t, `{"kind":"gained","n":9}`), map[string]bool{})
	if difference != "" {
		t.Fatalf("the gained row was refused: %s", difference)
	}
	if reason := compareExact(rowsOf(t, `{"kind":"a","n":1}`, `{"kind":"b","n":2}`), kept, map[string]bool{}); reason != "" {
		t.Fatalf("the rows left do not equal the reference in order: %s", reason)
	}
}

// TestAGainedRowThatIsNotReturnedFails: naming a row excuses nothing when the query does not
// return it.
func TestAGainedRowThatIsNotReturnedFails(t *testing.T) {
	got := rowsOf(t, `{"kind":"a","n":1}`)
	if _, difference := withoutAdded(got, rowsOf(t, `{"kind":"gained","n":9}`), map[string]bool{}); !strings.Contains(difference, "is not returned") {
		t.Fatalf("a missing gained row passed: %q", difference)
	}
}

// TestAGainedRowReturnedTwiceFails: a named row excuses one row, not two.
func TestAGainedRowReturnedTwiceFails(t *testing.T) {
	got := rowsOf(t, `{"kind":"gained","n":9}`, `{"kind":"gained","n":9}`)
	if _, difference := withoutAdded(got, rowsOf(t, `{"kind":"gained","n":9}`), map[string]bool{}); !strings.Contains(difference, "twice") {
		t.Fatalf("a gained row returned twice passed: %q", difference)
	}
}

// TestARowNobodyNamedStillFails: taking the named rows out leaves an unnamed extra row to fail the
// comparison with the reference.
func TestARowNobodyNamedStillFails(t *testing.T) {
	got := rowsOf(t, `{"kind":"a","n":1}`, `{"kind":"gained","n":9}`, `{"kind":"unnamed","n":5}`)
	kept, difference := withoutAdded(got, rowsOf(t, `{"kind":"gained","n":9}`), map[string]bool{})
	if difference != "" {
		t.Fatalf("the gained row was refused: %s", difference)
	}
	if reason := compareExact(rowsOf(t, `{"kind":"a","n":1}`), kept, map[string]bool{}); reason == "" {
		t.Fatal("an extra row nobody named passed the comparison")
	}
}

// TestAnEntryWithoutTheCycleThatAddedItIsRefused: every gained row says which cycle added it.
func TestAnEntryWithoutTheCycleThatAddedItIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "added.json")
	if err := os.WriteFile(path, []byte(`{"q": {"added_by": "", "rows": [{"n": 1}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAdded(path, "q"); err == nil {
		t.Fatal("an entry naming no cycle was read")
	}
	rows, _, err := readAdded(path, "another query")
	if err != nil || len(rows) != 0 {
		t.Fatalf("a query the file does not name gained %d rows (%v)", len(rows), err)
	}
}

// TestTheGainedRowsFileOfTheRepositoryIsWellFormed: every entry of the committed file names its
// cycle and at least one row, and every row decodes.
func TestTheGainedRowsFileOfTheRepositoryIsWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", "sql", "testdata", "reference_rows_added_since.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"diagnostic: Source availability"`) {
		t.Fatal("the file does not name the source availability's gained row")
	}
	if _, addedBy, err := readAdded(path, "diagnostic: Source availability"); err != nil || addedBy == "" {
		t.Fatalf("the source availability's entry cannot be read: %v", err)
	}
}
