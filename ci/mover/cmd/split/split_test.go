package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestTheSplitterMovesAFileAndTheModuleStillBuilds runs the command on the small module of
// testdata/module with testdata/plan.json, which moves one file of package shapes into a new package
// shapes/geometry: the result is the tree of testdata/want, byte for byte, and it builds. The two
// identifiers the move makes cross a package boundary are exported, their uses qualified and their
// doc comments kept naming them.
func TestTheSplitterMovesAFileAndTheModuleStillBuilds(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	binary := filepath.Join(work, "split")
	run(t, ".", "go", "build", "-o", binary, ".")
	module := filepath.Join(work, "module")
	copyTree(t, filepath.Join("testdata", "module"), module)
	plan, err := filepath.Abs(filepath.Join("testdata", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	output := run(t, module, binary, "-root", module, "-plan", plan)
	for _, exported := range []string{"sides -> Sides", "square -> Square"} {
		if !strings.Contains(output, exported) {
			t.Errorf("the export %q was not printed:\n%s", exported, output)
		}
	}
	got, want := treeOf(t, module), treeOf(t, filepath.Join("testdata", "want"))
	if strings.Join(sortedKeys(got), "\n") != strings.Join(sortedKeys(want), "\n") {
		t.Fatalf("the tree is\n%s\nnot\n%s", strings.Join(sortedKeys(got), "\n"), strings.Join(sortedKeys(want), "\n"))
	}
	for path, content := range want {
		if got[path] != content {
			t.Errorf("%s is\n%s\nnot\n%s", path, got[path], content)
		}
	}
	run(t, module, "go", "build", "./...")
}

// run runs a command in a directory and returns its output, failing the test when it fails.
func run(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

// copyTree copies a directory's files into another.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(to, relative), 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, relative), raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// treeOf reads every file under a directory, by its slash-separated relative path.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(relative)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func sortedKeys(tree map[string]string) []string {
	keys := make([]string, 0, len(tree))
	for key := range tree {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
