package harness

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTheDocumentedExamplesRun is A15 of the backend-completion cycle: every worked example of
// docs/plugins.md loads through the loader every plugin goes through and passes its own tests, which
// state the result the document describes. An example is a directory of docs/examples/, which
// docs/plugins.md cites by its path, or a fenced block of the document whose info string names its
// path. And the document asks a third party for no Go, no build and no pull request.
func TestTheDocumentedExamplesRun(t *testing.T) {
	docs := filepath.Join("..", "..", "..", "docs")
	raw, err := os.ReadFile(filepath.Join(docs, "plugins.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	root := t.TempDir()
	examples := map[string]bool{}

	// The example directories, copied as they are.
	directory := filepath.Join(docs, "examples")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.Contains(document, "docs/examples/"+name) {
			t.Errorf("docs/plugins.md does not cite the example docs/examples/%s", name)
		}
		source := filepath.Join(directory, name)
		err := filepath.WalkDir(source, func(path string, file fs.DirEntry, err error) error {
			if err != nil || file.IsDir() {
				return err
			}
			if strings.HasSuffix(path, ".go") {
				t.Errorf("the example %s holds a Go file, %s", name, path)
			}
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			target := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			return os.WriteFile(target, body, 0o640)
		})
		if err != nil {
			t.Fatal(err)
		}
		examples[name] = true
	}

	// The fenced blocks of the document, each written where its info string names.
	for _, block := range regexp.MustCompile("(?s)```[a-z]+ path=([^\\s]+)\n(.*?)\n```").FindAllStringSubmatch(document, -1) {
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
	if len(examples) == 0 {
		t.Fatal("docs/plugins.md cites no example")
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
