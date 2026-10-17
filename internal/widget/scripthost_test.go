package widget

import (
	"testing"

	"github.com/NaejEL/opnview/internal/widget/params"
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
