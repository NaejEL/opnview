package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// plainPackage is a fixture package that triggers no clause.
const plainPackage = `package plain

// Run returns one.
func Run() int { return 1 }
`

// TestAFixturePackageBecomesSelectedWhenAGoStatementIsAdded is E7: a package with no trigger is
// excluded, and the same package with a go statement added -- in a test file, which counts -- is
// selected by clause (i).
func TestAFixturePackageBecomesSelectedWhenAGoStatementIsAdded(t *testing.T) {
	t.Parallel()
	sources := map[string][]byte{"plain.go": []byte(plainPackage)}
	decided, err := decideFiles("internal/plain", sources)
	if err != nil {
		t.Fatal(err)
	}
	if decided.clause != "" {
		t.Fatalf("a package with no trigger was selected by %s (%s)", decided.clause, decided.file)
	}
	sources["plain_test.go"] = []byte(`package plain

import "testing"

func TestRun(t *testing.T) {
	done := make(chan int)
	go func() { done <- Run() }()
	if <-done != 1 {
		t.Fatal("not one")
	}
}
`)
	decided, err = decideFiles("internal/plain", sources)
	if err != nil {
		t.Fatal(err)
	}
	if decided.clause != "(i)" || decided.file != "plain_test.go" {
		t.Errorf("a go statement selected the package by %q in %q, not (i) in plain_test.go", decided.clause, decided.file)
	}
}

// TestEachClauseSelectsAPackageByItself: every clause of the rule is enough on its own, and the
// plugin host's directory is matched as a directory, never as a prefix of another name.
func TestEachClauseSelectsAPackageByItself(t *testing.T) {
	t.Parallel()
	for name, testCase := range map[string]struct {
		directory, source, clause string
	}{
		"sync":             {"internal/a", "package a\n\nimport \"sync\"\n\nvar m sync.Mutex\n", "(ii)"},
		"sync/atomic":      {"internal/a", "package a\n\nimport \"sync/atomic\"\n\nvar n atomic.Int64\n", "(ii)"},
		"httptest":         {"internal/a", "package a\n\nimport _ \"net/http/httptest\"\n", "(iii)"},
		"a ServeHTTP":      {"internal/a", "package a\n\ntype h struct{}\n\nfunc (h) ServeHTTP() {}\n", "(iii)"},
		"an http.Handler":  {"internal/a", "package a\n\nimport \"net/http\"\n\nvar h http.Handler\n", "(iii)"},
		"a renamed import": {"internal/a", "package a\n\nimport web \"net/http\"\n\nvar s web.Server\n", "(iii)"},
		"an http client":   {"internal/a", "package a\n\nimport \"net/http\"\n\nvar c http.Client\n", ""},
		"the plugin host":  {"internal/plugin", plainPackage, "(iv)"},
		"under it":         {"internal/plugin/harness", plainPackage, "(iv)"},
		"a mere prefix":    {"internal/pluginfixture", plainPackage, ""},
	} {
		decided, err := decideFiles(testCase.directory, map[string][]byte{"a.go": []byte(testCase.source)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if decided.clause != testCase.clause {
			t.Errorf("%s was decided %q, not %q", name, decided.clause, testCase.clause)
		}
	}
}

// TestEveryPackageOfAModuleIsDecided: run over the small module of testdata/module, the rule decides
// every package go list names, exactly once -- a go statement selects one, the plugin host's
// directory another, and a package with no trigger is excluded.
func TestEveryPackageOfAModuleIsDecided(t *testing.T) {
	t.Parallel()
	root := mustAbsolute(t, filepath.Join("testdata", "module"))
	packages, err := list(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, listed := range packages {
		if _, seen := got[listed.ImportPath]; seen {
			t.Errorf("%s is listed twice", listed.ImportPath)
		}
		decided, err := decide(root, listed)
		if err != nil {
			t.Fatal(err)
		}
		got[listed.ImportPath] = decided.clause
	}
	want := map[string]string{"example.com/racefixture/plain": "", "example.com/racefixture/goer": "(i)",
		"example.com/racefixture/internal/plugin/host": "(iv)"}
	if len(got) != len(want) {
		t.Fatalf("the module's packages were decided as %v, not %v", got, want)
	}
	for path, clause := range want {
		if got[path] != clause {
			t.Errorf("%s was decided %q, not %q", path, got[path], clause)
		}
	}
}

func mustAbsolute(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

// TestAFixturePluginLeavesEveryPackageDecided is E29's -race half: the fixture plugin of the
// repository's testdata/plugins, copied into the plugin tree of a copy of the fixture module as a
// third party adds one, holds no Go file -- a plugin is a manifest, a script and its statements,
// run by the plugin host -- so the rule has no package of it to decide, and every package of the
// module is still decided once, exactly as before. The host that runs it, internal/plugin, is
// selected by clause (iv) whatever plugins are installed.
func TestAFixturePluginLeavesEveryPackageDecided(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "module"), root)
	copyTree(t, filepath.Join("..", "..", "testdata", "plugins", "example_peers"),
		filepath.Join(root, "plugins", "example_peers"))
	packages, err := list(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, listed := range packages {
		decided, err := decide(root, listed)
		if err != nil {
			t.Fatal(err)
		}
		got[listed.ImportPath] = decided.clause
	}
	want := map[string]string{"example.com/racefixture/plain": "", "example.com/racefixture/goer": "(i)",
		"example.com/racefixture/internal/plugin/host": "(iv)"}
	if len(got) != len(want) {
		t.Fatalf("with the plugin added the module's packages were decided as %v, not %v", got, want)
	}
	for path, clause := range want {
		if got[path] != clause {
			t.Errorf("%s was decided %q, not %q", path, got[path], clause)
		}
	}
}

// copyTree copies a directory tree, file by file.
func copyTree(t *testing.T, source, target string) {
	t.Helper()
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
}
