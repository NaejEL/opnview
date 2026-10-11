package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shared fragment (the maintainer's decision of 11 October 2026): the per-leg counting over
// connection_measured_volume is connection_netflow, in schema.sql, and nothing else of the tree --
// the store's statements, the core widgets', the widget plugins' -- holds a copy of it. These read
// files only, and touch no database.

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

// TestNoStatementCopiesThePerLegCounting is the grep the decision asks for: no statement file holds a
// LATERAL subquery over connection_measured_volume, or derives a coverage class from its states --
// the two halves of the block every statement once carried -- and every `_netflow` statement reads
// the netflow figures through connection_netflow or the aggregates' netflow_parts.
func TestNoStatementCopiesThePerLegCounting(t *testing.T) {
	t.Parallel()
	copies := []*regexp.Regexp{
		regexp.MustCompile(`(?is)LATERAL\s*\(\s*SELECT\b[^;]*?\bFROM\s+connection_measured_volume\b`),
		regexp.MustCompile(`(?i)FILTER\s*\(\s*WHERE\s+\w+\.state\s*=\s*'measured'\s*\)`),
		regexp.MustCompile(`(?i)\bn\.coverage\b`),
	}
	twins := 0
	for path, text := range statementFiles(t) {
		for _, pattern := range copies {
			if location := pattern.FindStringIndex(text); location != nil {
				t.Errorf("%s copies the per-leg counting of connection_netflow: %q", path,
					text[location[0]:location[1]])
			}
		}
		if !strings.HasSuffix(path, ".statements.sql") {
			continue
		}
		parsed, err := parseStatements(text)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for name, statement := range parsed {
			if !strings.HasSuffix(name, "_netflow") {
				continue
			}
			twins++
			if !strings.Contains(statement, "connection_netflow(") && !strings.Contains(statement, "netflow_parts") {
				t.Errorf("%s: %s reads the netflow figures through neither connection_netflow nor netflow_parts",
					path, name)
			}
		}
	}
	if twins == 0 {
		t.Fatal("no _netflow statement was found: the test reads nothing")
	}
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema), "CREATE OR REPLACE FUNCTION connection_netflow(") {
		t.Error("schema.sql defines no connection_netflow")
	}
}

// TestNetflowPartsIsTheCompositeTypeOfTheSchema is the one order of the thirty-six figures: the fields
// of the composite type netflow_parts in schema.sql, the columns (netflow_sum(...)).* reads back, are
// NetflowPartsColumns, which every scan and every script member follows.
func TestNetflowPartsIsTheCompositeTypeOfTheSchema(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)CREATE TYPE netflow_parts AS \((.*?)\);`).FindSubmatch(schema)
	if block == nil {
		t.Fatal("schema.sql creates no type netflow_parts")
	}
	var fields []string
	for _, line := range strings.Split(string(block[1]), ",") {
		words := strings.Fields(line)
		if len(words) != 2 || words[1] != "BIGINT" {
			t.Fatalf("netflow_parts holds the field %q, not a BIGINT", strings.TrimSpace(line))
		}
		fields = append(fields, words[0])
	}
	if strings.Join(fields, ",") != strings.Join(NetflowPartsColumns(), ",") {
		t.Errorf("netflow_parts is\n%v\nnot\n%v", fields, NetflowPartsColumns())
	}
	var parts NetflowParts
	if len(parts.Fields()) != len(fields) {
		t.Errorf("NetflowParts holds %d figures, netflow_parts %d", len(parts.Fields()), len(fields))
	}
}
