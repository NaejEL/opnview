package modularity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// kindListScope are the directories PA-AC2 of the plugin-API amendment names: no Go map, slice or switch
// listing kind names may remain in them, because a kind is a file (internal/collect/provider/kinds) and
// its attributes, cadences and pages are data.
var kindListScope = []string{"internal/widget/params", "internal/plugin", "internal/store/vocab", "internal/collect",
	"internal/sizing", "internal/config", "internal/web/surfaces"}

// kindNameAllowed are the files of the scope that may write a kind's name, how many times, and why: each
// names one kind -- the coordinator of that kind naming its own, or a word that is not a kind -- and never
// lists several. The list may only shrink.
var kindNameAllowed = map[string]struct {
	count  int
	reason string
}{
	"internal/collect/kinds/filterlog/firewalllog.go": {1, "the firewall_log coordinator's own kind constant"},
	"internal/collect/kinds/neighbours/neighbour.go":  {1, "the neighbour coordinator's own kind constant"},
	"internal/collect/services/services.go": {3, "the service coordinator's two kinds, one constant each, and the " +
		"grouping record's registrable_domain field, which is a field name and not a kind"},
	"internal/collect/provider/pushed_firewall_log.go": {1, "the firewall_log kind's own push rule, in its own file"},
	"internal/widget/params/series.go": {1, "SourceSecurityEvent names the core dataset security_event, a catalogue " +
		"name, not a kind"},
}

// TestNoGoListNamesTheKinds is the grep of PA-AC2: across the non-test Go files of the scope, no string
// literal equals a core kind's name, except in the files allowed above, each its number of times. A
// map, slice or switch listing kind names is therefore impossible without editing this test.
func TestNoGoListNamesTheKinds(t *testing.T) {
	root := repositoryRoot(t)
	kinds := map[string]bool{}
	for _, kind := range provider.CoreKinds() {
		kinds[kind] = true
	}
	if len(kinds) == 0 {
		t.Fatalf("no kind file loaded: %v", provider.KindsError())
	}
	found := map[string]int{}
	var where []string
	for _, scope := range kindListScope {
		err := filepath.WalkDir(filepath.Join(root, scope), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
			ast.Inspect(file, func(node ast.Node) bool {
				literal, isLiteral := node.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					return true
				}
				if text, err := strconv.Unquote(literal.Value); err == nil && kinds[text] {
					found[relative]++
					where = append(where, relative+": "+text)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(where)
	for file, count := range found {
		if allowed, listed := kindNameAllowed[file]; !listed || count > allowed.count {
			t.Errorf("%s names a kind %d times, which only a kind file may: %v", file, count, where)
		}
	}
	for file, allowed := range kindNameAllowed {
		if found[file] < allowed.count {
			t.Errorf("%s is allowed %d kind names (%s) and holds %d: shrink the allowance", file, allowed.count,
				allowed.reason, found[file])
		}
	}
}
