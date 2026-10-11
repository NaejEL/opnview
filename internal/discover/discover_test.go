package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes files (path -> content) under a fresh repository root with a go.mod.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.test/repo\n\ngo 1.22\n"
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var providerSpec = Spec{
	Root: "kinds", Marker: "provider.json", MinDepth: 2, MaxDepth: 2, RequireGo: true,
	Output: "kinds/imports_gen.go", Package: "kinds", What: "the provider implementations",
}

func TestRenderListsEveryMarkedDirectoryAndOnlyThose(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"kinds/alpha/one/provider.json":  "{}",
		"kinds/alpha/one/one.go":         "package one\n",
		"kinds/alpha/two/provider.json":  "{}",
		"kinds/alpha/two/two.go":         "package two\n",
		"kinds/beta/three/provider.json": "{}",
		"kinds/beta/three/three.go":      "package three\n",
		// No Go file: pure data, nothing to import.
		"kinds/beta/data/provider.json": "{}",
		// Wrong depth.
		"kinds/shallow/provider.json": "{}",
		"kinds/shallow/shallow.go":    "package shallow\n",
		// A test-only directory is not an implementation.
		"kinds/beta/testonly/provider.json": "{}",
		"kinds/beta/testonly/x_test.go":     "package testonly\n",
		// Fixtures are never walked.
		"kinds/beta/three/testdata/deep/provider.json": "{}",
	})
	rendered, err := Render(root, "example.test/repo", providerSpec)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	for _, want := range []string{"kinds/alpha/one", "kinds/alpha/two", "kinds/beta/three"} {
		if !strings.Contains(text, `_ "example.test/repo/`+want+`"`) {
			t.Errorf("the generated file lacks %s:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"beta/data", "shallow", "testonly", "testdata"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the generated file lists %s:\n%s", unwanted, text)
		}
	}
	if !strings.HasPrefix(text, "// Code generated") || !strings.Contains(text, "DO NOT EDIT.") {
		t.Errorf("the generated file is not marked as generated:\n%s", text)
	}
}

func TestCheckFailsWhenADirectoryIsAddedWithoutRegeneratingAndPassesAfter(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"kinds/alpha/one/provider.json": "{}",
		"kinds/alpha/one/one.go":        "package one\n",
	})
	if err := Generate(root, providerSpec); err != nil {
		t.Fatal(err)
	}
	if err := Check(root, providerSpec); err != nil {
		t.Fatalf("a freshly generated file is reported stale: %v", err)
	}
	added := filepath.Join(root, "kinds", "alpha", "two")
	if err := os.MkdirAll(added, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"provider.json": "{}", "two.go": "package two\n"} {
		if err := os.WriteFile(filepath.Join(added, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := Check(root, providerSpec)
	if err == nil || !strings.Contains(err.Error(), "go generate") {
		t.Fatalf("an added directory must make the generated file stale and name the fix, got %v", err)
	}
	if err := Generate(root, providerSpec); err != nil {
		t.Fatal(err)
	}
	if err := Check(root, providerSpec); err != nil {
		t.Fatalf("regenerating did not clear the staleness: %v", err)
	}
}

func TestCheckFailsOnAHandEdit(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"kinds/alpha/one/provider.json": "{}",
		"kinds/alpha/one/one.go":        "package one\n",
	})
	if err := Generate(root, providerSpec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "kinds", "imports_gen.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("\n// a hand edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(root, providerSpec); err == nil {
		t.Fatal("a hand-edited generated file passed the check")
	}
}

func TestTheDirectiveIsTheOnlyCopyOfTheParameters(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"kinds/doc.go": "package kinds\n\n" + DirectivePrefix + " -root kinds -marker provider.json " +
			"-min-depth 2 -max-depth 2 -out kinds/imports_gen.go -package kinds\n",
	})
	spec, err := SpecFromDirective(filepath.Join(root, "kinds", "doc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Root != "kinds" || spec.Marker != "provider.json" || spec.MinDepth != 2 ||
		spec.MaxDepth != 2 || spec.Output != "kinds/imports_gen.go" || spec.Package != "kinds" || !spec.RequireGo {
		t.Errorf("the directive was read as %+v", spec)
	}
	if _, err := SpecFromDirective(filepath.Join(root, "go.mod")); err == nil {
		t.Error("a file with no directive yielded a spec")
	}
}

func TestAQuotedDirectiveArgumentStaysOneArgument(t *testing.T) {
	t.Parallel()
	got := splitArguments(` -root kinds -what "the firewall-source provider implementations" -out x.go`)
	want := []string{"-root", "kinds", "-what", "the firewall-source provider implementations", "-out", "x.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("split into %q, want %q", got, want)
	}
}
