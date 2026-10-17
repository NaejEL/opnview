package surfaces_test

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

	"github.com/NaejEL/opnview/internal/discover"
	"github.com/NaejEL/opnview/internal/web"
)

// The rule, held as tests: a surface is a directory, nothing that ships names the surfaces
// by hand, and adding one edits no shipped file.

// surfaceDirectories reads the directories below this one that hold a surface.json, by the
// name the marker declares.
func surfaceDirectories(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("listing the surfaces: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(entry.Name(), "surface.json"))
		if err != nil {
			continue
		}
		var marker struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(raw, &marker); err != nil {
			t.Fatalf("%s/surface.json is not JSON: %v", entry.Name(), err)
		}
		if marker.Name != entry.Name() {
			t.Errorf("%s/surface.json declares the name %q; a surface is named for its directory",
				entry.Name(), marker.Name)
		}
		if marker.Description == "" {
			t.Errorf("%s/surface.json describes nothing", entry.Name())
		}
		found[entry.Name()] = marker.Name
	}
	return found
}

// TestTheGeneratedLinkFileIsCurrent: the one file that names the surfaces is written by
// `go generate` from the directory tree, and this fails, naming the command, when a
// directory was added or removed without running it or when the file was edited by hand.
func TestTheGeneratedLinkFileIsCurrent(t *testing.T) {
	t.Parallel()
	if err := discover.CheckDirective("surfaces.go"); err != nil {
		t.Error(err)
	}
}

// TestEveryDirectoryRegistersASurfaceAndEverySurfaceHasADirectory: the marker, the directory
// and the registration agree, in both directions.
func TestEveryDirectoryRegistersASurfaceAndEverySurfaceHasADirectory(t *testing.T) {
	t.Parallel()
	directories := surfaceDirectories(t)
	if len(directories) == 0 {
		t.Fatal("no surface directory was found, so this assertion is vacuous")
	}
	registered := map[string]bool{}
	for _, surface := range web.RegisteredSurfaces() {
		registered[surface.Name] = true
		if _, present := directories[surface.Name]; !present {
			t.Errorf("the surface %q is registered and has no directory with a surface.json", surface.Name)
		}
	}
	for name := range directories {
		if !registered[name] {
			t.Errorf("the directory %q holds a surface.json and registers no surface", name)
		}
	}
}

// surfaceNames are the names of the surface directories.
func surfaceNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for name := range surfaceDirectories(t) {
		names[name] = true
	}
	return names
}

// handKeptLists returns the places in one Go source where two or more of the given names
// are written as string literals side by side: a composite literal, or a case clause. That
// is the shape of a hand-kept list of implementations -- a registry file, a switch, a table --
// and it is what the rule forbids in a shipped file.
func handKeptLists(source string, names map[string]bool) []string {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "source.go", source, 0)
	if err != nil {
		return []string{"does not parse: " + err.Error()}
	}
	count := func(expressions []ast.Expr) int {
		seen := map[string]bool{}
		for _, expression := range expressions {
			if pair, isPair := expression.(*ast.KeyValueExpr); isPair {
				expression = pair.Key
			}
			literal, ok := expression.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && names[value] {
				seen[value] = true
			}
		}
		return len(seen)
	}
	var found []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.CompositeLit:
			if count(typed.Elts) >= 2 {
				found = append(found, fset.Position(typed.Pos()).String())
			}
		case *ast.CaseClause:
			if count(typed.List) >= 2 {
				found = append(found, fset.Position(typed.Pos()).String())
			}
		}
		return true
	})
	return found
}

// TestNoShippedFileKeepsAListOfSurfacesByHand: no Go file of the interface, outside tests and
// the generated link file, writes the names of two surfaces side by side.
func TestNoShippedFileKeepsAListOfSurfacesByHand(t *testing.T) {
	t.Parallel()
	names := surfaceNames(t)
	var offenders []string
	scanned := 0
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "webtest" || strings.HasPrefix(entry.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, "_gen.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, place := range handKeptLists(string(raw), names) {
			offenders = append(offenders, path+": "+place)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning the interface: %v", err)
	}
	if scanned < 10 {
		t.Fatalf("only %d files were scanned, so this assertion is close to vacuous", scanned)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these places keep a list of surfaces by hand: %v", offenders)
	}
}

// TestTheHandKeptListScanHasTeeth: the scan finds the lists it is for and passes what it is
// not for.
func TestTheHandKeptListScanHasTeeth(t *testing.T) {
	t.Parallel()
	names := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, source := range []string{
		"package x\nvar all = []string{\"alpha\", \"beta\"}\n",
		"package x\nfunc f(n string) { switch n { case \"beta\", \"gamma\": } }\n",
		"package x\nvar m = map[string]int{\"alpha\": 1, \"gamma\": 2}\n",
	} {
		if len(handKeptLists(source, names)) == 0 {
			t.Errorf("the scan misses a hand-kept list:\n%s", source)
		}
	}
	for _, source := range []string{
		"package x\nvar one = []string{\"alpha\", \"other\"}\n",
		"package x\nfunc f(n string) bool { return n == \"alpha\" || n == \"beta\" }\n",
		"package x\nvar twice = []string{\"alpha\", \"alpha\"}\n",
	} {
		if found := handKeptLists(source, names); len(found) != 0 {
			t.Errorf("the scan reports %v for a source that lists no two surfaces:\n%s", found, source)
		}
	}
}
