package store_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PA-AC23 of specs/SPEC-backend-completion-plugins.md: the SQLite shape of the store is gone.

// TestNoFlagIsAnIntegerCheckedToZeroOrOne: every flag column is a BOOLEAN.
func TestNoFlagIsAnIntegerCheckedToZeroOrOne(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if location := regexp.MustCompile(`IN \(0, ?1\)`).FindIndex(schema); location != nil {
		t.Errorf("schema.sql still checks a column to 0 or 1: %q", schema[max(0, location[0]-80):location[1]])
	}
}

// TestNoGoStatementUsesTheQuestionMarkForm walks every Go file under internal/ and cmd/ and refuses a
// string holding SQL with a ? parameter, and the helper that turned a bool into 0 or 1.
func TestNoGoStatementUsesTheQuestionMarkForm(t *testing.T) {
	t.Parallel()
	placeholder := regexp.MustCompile(`(?i)\b(?:WHERE|AND|OR|SET|VALUES|IN|LIMIT|SELECT)\b[^"` + "`" + `\n]*[=<>(,]\s*\?\s*[,)]?\s*(?:$|["` + "`" + `]|\bAND\b|\bOR\b|\bLIMIT\b)`)
	for _, sample := range []string{"\"SELECT id FROM flow WHERE log_digest = ?\"", "`UPDATE x SET a = ? WHERE b = ?`"} {
		if !placeholder.MatchString(sample) {
			t.Fatalf("the check would not see %s", sample)
		}
	}
	for _, root := range []string{filepath.Join("..", "..", "internal"), filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for number, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "func "+"boolToInt") {
					t.Errorf("%s:%d defines boolToInt", path, number+1)
				}
				if placeholder.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") &&
					!strings.HasSuffix(path, "sqliteshape_test.go") {
					t.Errorf("%s:%d binds a ? parameter: %s", path, number+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
