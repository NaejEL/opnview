package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestAFixturePluginsStatementsAreFoundForTheSweep is E29's plan-sweep half: the fixture plugin of
// the repository's testdata/plugins, copied into a temporary plugin tree as a third party adds one,
// has its statements file found by the walk sql/schema-checks.sh plans every statement of -- and a
// statements file under a testdata directory, a test's input, is not.
func TestAFixturePluginsStatementsAreFoundForTheSweep(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join("..", "..", "testdata", "plugins", "example_peers")
	target := filepath.Join(root, "plugins", "example_peers")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o750)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, relative), content, 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, "plugins", "example_peers", "testdata", "input.statements.sql")
	if err := os.MkdirAll(filepath.Dir(ignored), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte(Marker+"a_test_input\nSELECT 1;\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	unmarked := filepath.Join(root, "plugins", "example_peers", "notes.sql")
	if err := os.WriteFile(unmarked, []byte("SELECT 1;\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	found, err := Find(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(target, "widgets", "example_peer_places", "places.statements.sql")}
	if !slices.Equal(found, want) {
		t.Errorf("the walk found %v, not %v", found, want)
	}
}
