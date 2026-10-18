package store_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The shared fragment (the maintainer's decision of 11 October 2026, generalised by PA5 of the plugin-API
// amendment): the per-connection counting over connection_measure is connection_measure_parts, read
// through flow_measures, in schema.sql, and nothing else of the tree -- the store's statements, the core
// widgets', the widget plugins' -- holds a copy of it. These read files only, and touch no database.

// statementFiles are every SQL file of the tree but schema.sql, by path from the repository root.
func statementFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	files := map[string]string{}
	for _, dir := range []string{"internal", "plugins"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".sql") || entry.Name() == "schema.sql" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			files[filepath.ToSlash(relative)] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no statement file was found")
	}
	return files
}

// TestNoStatementCopiesThePerConnectionCounting is the grep the decision asks for: no statement file
// holds a LATERAL subquery over connection_measure, or derives a coverage class from its states -- the
// two halves of the block every statement once carried -- and every `_measured` statement reads a
// measure's figures through flow_measures or the aggregates' measure_parts.
func TestNoStatementCopiesThePerConnectionCounting(t *testing.T) {
	t.Parallel()
	copies := []*regexp.Regexp{
		regexp.MustCompile(`(?is)LATERAL\s*\(\s*SELECT\b[^;]*?\bFROM\s+connection_measure\b`),
		regexp.MustCompile(`(?i)FILTER\s*\(\s*WHERE\s+\w+\.state\s*=\s*'measured'\s*\)`),
		regexp.MustCompile(`(?i)\bn\.coverage\b`),
	}
	twins := 0
	for path, text := range statementFiles(t) {
		for _, pattern := range copies {
			if location := pattern.FindStringIndex(text); location != nil {
				t.Errorf("%s copies the per-connection counting of connection_measure_parts: %q", path,
					text[location[0]:location[1]])
			}
		}
		if !strings.HasSuffix(path, ".statements.sql") {
			continue
		}
		parsed, err := sqldb.ParseStatements(text)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for name, statement := range parsed {
			if !strings.HasSuffix(name, "_measured") {
				continue
			}
			twins++
			if !strings.Contains(statement, "flow_measures(") && !strings.Contains(statement, "measure_parts") {
				t.Errorf("%s: %s reads a measure's figures through neither flow_measures nor measure_parts",
					path, name)
			}
		}
	}
	if twins == 0 {
		t.Fatal("no _measured statement was found: the test reads nothing")
	}
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, function := range []string{"flow_measures", "flow_measure", "connection_measure_parts"} {
		if !strings.Contains(string(schema), "CREATE OR REPLACE FUNCTION "+function+"(") {
			t.Errorf("schema.sql defines no %s", function)
		}
	}
}
