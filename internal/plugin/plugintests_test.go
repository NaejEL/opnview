package plugin

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryPlugins is the built-in plugin tree on disk, found from this package's directory.
func repositoryPlugins(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestEveryPluginsOwnTestsPass is the one Go test that runs the plugins' tests: every built-in
// plugin's tests/*.test.js, through the same harness `opnview plugin test` runs a third party's with.
func TestEveryPluginsOwnTestsPass(t *testing.T) {
	t.Parallel()
	root := repositoryPlugins(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			results, err := RunTests(context.Background(), directory)
			if err != nil {
				t.Fatalf("running the tests of %s: %v", entry.Name(), err)
			}
			if len(results) == 0 {
				t.Errorf("the plugin %s carries no test", entry.Name())
			}
			for _, result := range results {
				if !result.Passed {
					t.Errorf("%s: %s: %s", result.File, result.Name, strings.Join(result.Failures, "; "))
				}
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no built-in plugin was found")
	}
}

// TestNoPluginDirectoryHoldsGo is the maintainer's rule of 10 October 2026: a plugin directory
// holds no Go file at all, so nobody writing a plugin writes, builds or runs Go.
func TestNoPluginDirectoryHoldsGo(t *testing.T) {
	t.Parallel()
	root := repositoryPlugins(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			t.Errorf("%s is a Go file inside the plugin tree", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
