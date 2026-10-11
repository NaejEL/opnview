// Package modularity holds the tests that keep the modular rule of CLAUDE.md true: an
// implementation is a directory, discovered, and never an entry in a list somebody maintains
// by hand in a shipped Go file.
package modularity

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// repositoryRoot is two directories above this package.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// implementationNames returns every name an implementation goes by, from the metadata file in
// its own directory: provider keys (provider.json), widget types (manifest.json) and surface
// names (surface.json), under internal/ and under plugins/, where a widget a plugin carries keeps
// its manifest.json. The names are DISCOVERED from the tree, so this test lists none.
func implementationNames(t *testing.T, root string) map[string]string {
	t.Helper()
	names := map[string]string{}
	fields := map[string]string{"provider.json": "key", "manifest.json": "type", "surface.json": "name"}
	for _, tree := range []string{"internal", "plugins"} {
		implementationNamesIn(t, filepath.Join(root, tree), fields, names)
	}
	return names
}

// implementationNamesIn adds the names one tree's metadata files declare.
func implementationNamesIn(t *testing.T, tree string, fields map[string]string, names map[string]string) {
	t.Helper()
	err := filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		field, known := fields[entry.Name()]
		if !known {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil
		}
		if name, ok := document[field].(string); ok && name != "" {
			names[name] = filepath.ToSlash(filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// handLists returns every place in one Go source where two or more implementation names are
// listed together: a slice, array or map literal holding them, a switch or select case listing
// them, or two or more blank imports of implementation directories in one file. A file that
// states one name, to say which implementation it is or to cite it in a message, is not a list.
func handLists(path, source string, names map[string]string, implementationDirectories []string) []string {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, parser.SkipObjectResolution)
	if err != nil {
		return []string{path + ": does not parse: " + err.Error()}
	}
	var found []string
	listed := func(position token.Pos, expressions []ast.Expr) {
		seen := map[string]bool{}
		for _, expression := range expressions {
			if pair, isPair := expression.(*ast.KeyValueExpr); isPair {
				expression = pair.Key
			}
			literal, isLiteral := expression.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}
			if _, isName := names[value]; isName {
				seen[value] = true
			}
		}
		if len(seen) >= 2 {
			var listedNames []string
			for name := range seen {
				listedNames = append(listedNames, name)
			}
			sort.Strings(listedNames)
			found = append(found, fileSet.Position(position).String()+": lists implementations by hand: "+
				strings.Join(listedNames, ", "))
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			listed(typed.Pos(), typed.Elts)
		case *ast.CaseClause:
			listed(typed.Pos(), typed.List)
		}
		return true
	})
	blank := 0
	for _, imported := range file.Imports {
		if imported.Name == nil || imported.Name.Name != "_" {
			continue
		}
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		for _, directory := range implementationDirectories {
			if strings.HasSuffix(value, "/"+directory) {
				blank++
				break
			}
		}
	}
	if blank >= 2 {
		found = append(found, path+": blank-imports implementation directories by hand")
	}
	return found
}

// scannedSources returns the shipped Go sources: everything under cmd/ and internal/ that is
// neither a test nor generated.
func scannedSources(t *testing.T, root string) []string {
	t.Helper()
	var sources []string
	for _, base := range []string{"cmd", "internal", "ci"} {
		err := filepath.WalkDir(filepath.Join(root, base), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
				strings.HasSuffix(name, "_gen.go") {
				return nil
			}
			sources = append(sources, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return sources
}

// implementationDirectoryNames is the last path element of every implementation directory.
func implementationDirectoryNames(names map[string]string) []string {
	var directories []string
	for _, directory := range names {
		directories = append(directories, filepath.Base(directory))
	}
	return directories
}

// TestNoShippedGoFileListsImplementationsByHand is the modular rule as a test.
//
// Adding a provider, a widget or a surface is adding a directory. If a shipped Go file lists
// two of them in one literal, one switch case or one block of blank imports, then adding the
// next one means editing that file, and the rule is broken however well the directory is
// discovered. The names come from the tree itself, the generated link files are exempt because
// a test already proves them current, and test files are exempt because a fixture may name what
// it needs.
func TestNoShippedGoFileListsImplementationsByHand(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	names := implementationNames(t, root)
	if len(names) < 30 {
		t.Fatalf("only %d implementation names were discovered; the scan is not looking at the tree", len(names))
	}
	directories := implementationDirectoryNames(names)
	var offenders []string
	for _, path := range sourcesOf(t, root) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		offenders = append(offenders, handLists(filepath.ToSlash(path), string(raw), names, directories)...)
	}
	if len(offenders) > 0 {
		t.Errorf("implementations are listed by hand (adding one would mean editing these):\n%s",
			strings.Join(offenders, "\n"))
	}
}

func sourcesOf(t *testing.T, root string) []string { return scannedSources(t, root) }

// TestTheHandListGuardHasTeeth proves the scan sees the three shapes of list it exists for, and
// leaves a file that states one name alone.
func TestTheHandListGuardHasTeeth(t *testing.T) {
	t.Parallel()
	names := map[string]string{"alpha": "x/alpha", "beta": "x/beta", "gamma": "x/gamma"}
	directories := implementationDirectoryNames(names)
	for label, source := range map[string]string{
		"slice":   "package p\nvar all = []string{\"alpha\", \"beta\"}\n",
		"map":     "package p\nvar all = map[string]int{\"gamma\": 1, \"beta\": 2}\n",
		"switch":  "package p\nfunc f(s string) { switch s { case \"alpha\", \"gamma\": } }\n",
		"imports": "package p\nimport (\n\t_ \"m/x/alpha\"\n\t_ \"m/x/beta\"\n)\n",
	} {
		if len(handLists("p.go", source, names, directories)) == 0 {
			t.Errorf("the guard misses a hand list written as a %s", label)
		}
	}
	for label, source := range map[string]string{
		"one name":      "package p\nconst key = \"alpha\"\n",
		"unrelated":     "package p\nvar all = []string{\"x\", \"y\"}\n",
		"single import": "package p\nimport _ \"m/x/alpha\"\n",
	} {
		if found := handLists("p.go", source, names, directories); len(found) != 0 {
			t.Errorf("the guard flags %s: %v", label, found)
		}
	}
}
