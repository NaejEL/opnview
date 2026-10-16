package widget

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// TestAScriptManifestIsValidatedLikeEveryOther: script names a .js file of the widget's own
// directory.
func TestAScriptManifestIsValidatedLikeEveryOther(t *testing.T) {
	t.Parallel()
	base := `{"manifest_version": 1, "type": "example_script", "shape": "table", "queries": ["q.sql"], `
	for text, accepted := range map[string]bool{
		base + `"script": "example.js"}`:    true,
		base + `"script": "../example.js"}`: false,
		base + `"script": "example.ts"}`:    false,
	} {
		manifest, err := params.DecodeManifest([]byte(text))
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		refusal := params.ValidateManifest(manifest)
		if (refusal == nil) != accepted {
			t.Errorf("%s: accepted %v, refusal %v", text, refusal == nil, refusal)
		}
	}
}

// TestEveryHostAreaIsDocumented: every area of the widget host has its section in docs/plugins.md,
// and the document describes no area the host does not have.
func TestEveryHostAreaIsDocumented(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "plugins.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, match := range regexp.MustCompile("(?m)^#### `host\\.([a-z_]+)`$").FindAllStringSubmatch(string(raw), -1) {
		documented[match[1]] = true
	}
	areas := script.HostAreas()
	if len(areas) == 0 {
		t.Fatal("the host has no area")
	}
	for _, area := range areas {
		if !documented[area] {
			t.Errorf("the host area %s has no section in docs/plugins.md", area)
		}
		delete(documented, area)
	}
	var extra []string
	for area := range documented {
		extra = append(extra, area)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("docs/plugins.md documents host areas that do not exist: %s", strings.Join(extra, ", "))
	}
}
