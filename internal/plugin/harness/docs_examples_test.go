package harness

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTheDocumentedExamplesRun is A15 of the backend-completion cycle: every worked example of
// docs/plugins.md -- each file a fenced block whose info string names its path -- is copied as it
// is into a directory, loads through the loader every plugin goes through, and passes its own
// tests, which state the result the document describes. And the document asks a third party for no
// Go, no build and no pull request.
func TestTheDocumentedExamplesRun(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "plugins.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	blocks := regexp.MustCompile("(?s)```[a-z]+ path=([^\\s]+)\n(.*?)\n```").FindAllStringSubmatch(document, -1)
	if len(blocks) == 0 {
		t.Fatal("docs/plugins.md holds no example file")
	}
	root := t.TempDir()
	examples := map[string]bool{}
	for _, block := range blocks {
		relative := filepath.FromSlash(block[1])
		if strings.Contains(block[1], "..") || filepath.IsAbs(relative) {
			t.Fatalf("the example path %q leaves its directory", block[1])
		}
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(block[2]+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		examples[strings.SplitN(block[1], "/", 2)[0]] = true
	}
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		results, err := RunTests(context.Background(), filepath.Join(root, name))
		if err != nil {
			t.Errorf("the example %s does not load: %v", name, err)
			continue
		}
		if len(results) == 0 {
			t.Errorf("the example %s has no test stating its result", name)
		}
		for _, result := range results {
			for _, failure := range result.Failures {
				t.Errorf("the example %s, %s: %s", name, result.Name, failure)
			}
		}
	}
	for _, forbidden := range []string{"go generate", "go build", "pull request to", "recompile"} {
		if strings.Contains(strings.ToLower(document), forbidden) {
			t.Errorf("docs/plugins.md describes a step a third party should never take: %q", forbidden)
		}
	}
}
