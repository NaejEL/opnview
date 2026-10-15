package layout

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The rule of a readable Go core (Phase D of specs/SPEC-backend-completion-plugins.md), held by a
// test so a package that outgrows it fails here rather than in a reader's head:
//
//   - no directory under cmd/, internal/ or the built-in plugin tree holds more than
//     maxSourceFiles non-test source files, Go and SQL counted together;
//   - every Go package has a doc.go whose package comment says what the package is for;
//   - docs/architecture.md, "Map of the tree", lists every Go package directory of cmd/ and
//     internal/ with its purpose, and nothing else.
//
// Test files are not counted, because Go keeps them beside the package they test; testdata/ is
// not counted, because it holds fixtures and not sources.

// maxSourceFiles is the most non-test .go and .sql files one directory may hold.
const maxSourceFiles = 15

// minCommentLength is the shortest package comment that can say what a package is for: a
// comment shorter than this names the package and little else.
const minCommentLength = 60

// trees are the trees the rule covers, relative to the repository root.
var trees = []string{"cmd", "internal", "plugins"}

// mappedTrees are the trees whose package directories the map lists: the Go core.
var mappedTrees = []string{"cmd", "internal"}

// mapHeading is the heading of the map's section in docs/architecture.md.
const mapHeading = "## Map of the tree"

// mapEntry is one line of the map: "- `path` — what it is for".
var mapEntry = regexp.MustCompile("^- `([^`]+)` — (.+)$")

// repositoryRoot is two directories above this package.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// directory is what the rule reads of one directory.
type directory struct {
	// sources are its non-test .go and .sql files.
	sources []string
	// goFiles are all its .go files, tests included: a directory holding one is a Go package.
	goFiles []string
}

// directories returns every directory of the covered trees under root, testdata/ excluded, by its
// slash-separated path relative to root.
func directories(root string) (map[string]*directory, error) {
	found := map[string]*directory{}
	for _, tree := range trees {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			name := entry.Name()
			if found[relative] == nil {
				found[relative] = &directory{}
			}
			if strings.HasSuffix(name, ".go") {
				found[relative].goFiles = append(found[relative].goFiles, name)
			}
			if (strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".sql")) &&
				!strings.HasSuffix(name, "_test.go") {
				found[relative].sources = append(found[relative].sources, name)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

// packageDirectories returns the directories under root holding a Go package, sorted.
func packageDirectories(root string) ([]string, error) {
	found, err := directories(root)
	if err != nil {
		return nil, err
	}
	var packages []string
	for path, entry := range found {
		if len(entry.goFiles) > 0 {
			packages = append(packages, path)
		}
	}
	sort.Strings(packages)
	return packages, nil
}

// oversized returns a finding per directory holding more than maxSourceFiles source files.
func oversized(root string) ([]string, error) {
	found, err := directories(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for path, entry := range found {
		if len(entry.sources) > maxSourceFiles {
			sort.Strings(entry.sources)
			findings = append(findings, fmt.Sprintf("%s holds %d non-test source files, more than %d: %s -- "+
				"split it by responsibility", path, len(entry.sources), maxSourceFiles, strings.Join(entry.sources, ", ")))
		}
	}
	sort.Strings(findings)
	return findings, nil
}

// undocumented returns a finding per package without a doc.go whose package comment, introduced by
// the package's name, says what the package is for.
func undocumented(root string) ([]string, error) {
	packages, err := packageDirectories(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, path := range packages {
		file := filepath.Join(root, path, "doc.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments|parser.PackageClauseOnly)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s has no doc.go holding its package comment: %v", path, err))
			continue
		}
		comment := strings.TrimSpace(parsed.Doc.Text())
		if len(comment) < minCommentLength {
			findings = append(findings, fmt.Sprintf("%s/doc.go has no package comment saying what the package is for "+
				"(%q)", path, comment))
			continue
		}
		// A package is introduced by its name; a command by the command it is.
		if !strings.HasPrefix(comment, "Package "+parsed.Name.Name+" ") &&
			!(parsed.Name.Name == "main" && strings.HasPrefix(comment, "Command "+filepath.Base(path)+" ")) {
			findings = append(findings, fmt.Sprintf("%s/doc.go: the package comment does not begin with the "+
				"package's name: %q", path, strings.SplitN(comment, "\n", 2)[0]))
		}
	}
	return findings, nil
}

// unmapped returns a finding per difference between the map of the tree in markdown and the
// package directories of the mapped trees under root.
func unmapped(root, markdown string) ([]string, error) {
	var findings []string
	mapped := map[string]bool{}
	inMap, sawMap := false, false
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "## ") {
			inMap = strings.TrimSpace(line) == mapHeading
			sawMap = sawMap || inMap
			continue
		}
		if !inMap || !strings.HasPrefix(line, "- `") {
			continue
		}
		match := mapEntry.FindStringSubmatch(line)
		if match == nil || strings.TrimSpace(match[2]) == "" {
			findings = append(findings, fmt.Sprintf("the map's line %q is not \"- `path` — what it is for\"", line))
			continue
		}
		if mapped[match[1]] {
			findings = append(findings, "the map lists "+match[1]+" twice")
		}
		mapped[match[1]] = true
	}
	if !sawMap {
		return append(findings, "docs/architecture.md has no \""+mapHeading+"\" section"), nil
	}
	packages, err := packageDirectories(root)
	if err != nil {
		return nil, err
	}
	for _, path := range packages {
		if !inMappedTree(path) {
			continue
		}
		if !mapped[path] {
			findings = append(findings, "the map of the tree does not list "+path)
		}
		delete(mapped, path)
	}
	for path := range mapped {
		findings = append(findings, "the map of the tree lists "+path+", which is not a package directory of "+
			strings.Join(mappedTrees, "/ or ")+"/")
	}
	sort.Strings(findings)
	return findings, nil
}

// inMappedTree reports whether a directory is under one of the mapped trees.
func inMappedTree(path string) bool {
	for _, tree := range mappedTrees {
		if path == tree || strings.HasPrefix(path, tree+"/") {
			return true
		}
	}
	return false
}

// TestNoDirectoryHoldsMoreThanFifteenSourceFiles is D1's first clause.
func TestNoDirectoryHoldsMoreThanFifteenSourceFiles(t *testing.T) {
	t.Parallel()
	findings, err := oversized(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestEveryPackageSaysWhatItIsForInItsDocFile is D1's second clause: every Go package has a doc.go,
// and its package comment is there and says something.
func TestEveryPackageSaysWhatItIsForInItsDocFile(t *testing.T) {
	t.Parallel()
	findings, err := undocumented(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestTheMapOfTheTreeListsEveryPackageDirectory is D2: the map in docs/architecture.md and the tree
// list the same package directories, each with its purpose.
func TestTheMapOfTheTreeListsEveryPackageDirectory(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	findings, err := unmapped(root, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestTheLayoutRuleHasTeeth runs the three checks over a tree written to break each of them once,
// so none of the tests above passes because its check finds nothing anywhere.
func TestTheLayoutRuleHasTeeth(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	comment := "// Package example is a package written to break the layout rule once, and only once.\npackage example\n"
	// internal/example: sixteen sources, eight Go and eight SQL, and a test that is not counted.
	write("internal/example/doc.go", comment)
	for i := 1; i < 8; i++ {
		write(fmt.Sprintf("internal/example/file%d.go", i), "package example\n")
	}
	for i := 0; i < 8; i++ {
		write(fmt.Sprintf("internal/example/file%d.statements.sql", i), "-- statement: example\nSELECT 1;\n")
	}
	write("internal/example/example_test.go", "package example\n")
	// internal/terse: a doc.go saying too little; internal/silent: none at all.
	write("internal/terse/doc.go", "// Package terse is terse.\npackage terse\n")
	write("internal/silent/silent.go", "package silent\n")
	// A widget directory of the plugin tree is no Go package, and holds no doc.go.
	write("plugins/example/widgets/example/example.statements.sql", "-- statement: example\nSELECT 1;\n")

	if findings, err := oversized(root); err != nil || len(findings) != 1 ||
		!strings.HasPrefix(findings[0], "internal/example holds 16 ") {
		t.Errorf("the file limit found %v (%v)", findings, err)
	}
	findings, err := undocumented(root)
	if err != nil || len(findings) != 2 || !strings.HasPrefix(findings[0], "internal/silent has no doc.go") ||
		!strings.HasPrefix(findings[1], "internal/terse/doc.go has no package comment") {
		t.Errorf("the package comments check found %v (%v)", findings, err)
	}
	markdown := "# Example\n\n" + mapHeading + "\n\n- `internal/example` — breaks the limit.\n" +
		"- `internal/gone` — no longer in the tree.\n- `internal/terse` badly written.\n\n## After\n\n" +
		"- `internal/silent` — listed outside the map.\n"
	findings, err = unmapped(root, markdown)
	want := []string{
		"the map of the tree does not list internal/silent",
		"the map of the tree does not list internal/terse",
		"the map of the tree lists internal/gone, which is not a package directory of cmd/ or internal/",
		"the map's line \"- `internal/terse` badly written.\" is not \"- `path` — what it is for\"",
	}
	if err != nil || strings.Join(findings, "\n") != strings.Join(want, "\n") {
		t.Errorf("the map check found\n%s\nwant\n%s", strings.Join(findings, "\n"), strings.Join(want, "\n"))
	}
}
