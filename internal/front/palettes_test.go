package front_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/front"
)

// The palettes as a browser draws them, held to the approved mockup (the maintainer's review of 17
// October 2026: "the palettes do not work and are not those of the mockup").
//
// A browser draws a palette from one place: the block of the generated /front/palettes.css whose
// selector is the document's data-theme, "<palette>-<mode>", which the front's appearance module
// writes and nothing else does. So what is checked here is that stylesheet as the server serves it,
// block by block against the mockup's own token blocks (docs/mockups/canvas-mockup.html), and that
// nothing in the core stylesheet can override what a block sets.

// tokenBlocks reads every `:root[data-theme="<id>"] { --<token>: <value>; ... }` block of a stylesheet.
func tokenBlocks(css string) map[string]map[string]string {
	blocks := map[string]map[string]string{}
	block := regexp.MustCompile(`:root\[data-theme="([a-z0-9-]+)"\]\s*\{([^}]*)\}`)
	declaration := regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*([^;]+);`)
	for _, found := range block.FindAllStringSubmatch(css, -1) {
		values := map[string]string{}
		for _, pair := range declaration.FindAllStringSubmatch(found[2], -1) {
			values[pair[1]] = normalisedColour(pair[2])
		}
		blocks[found[1]] = values
	}
	return blocks
}

// normalisedColour is a value as a browser compares it: no spaces, lower case.
func normalisedColour(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

// mockupStyle is the mockup's stylesheet: its first <style> element.
func mockupStyle(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs", "mockups", "canvas-mockup.html"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	end := strings.Index(text, "</style>")
	if end < 0 {
		t.Fatal("the mockup holds no stylesheet")
	}
	return text[:end]
}

// TestEveryPaletteAndModeIsDrawnWithTheMockupsValues: for every palette and mode the mockup draws, the
// served stylesheet holds the block the front's data-theme selects, with exactly the mockup's tokens
// and values; the document before any script has run draws the industrial palette; every palette
// and mode the front offers has its block; and the core stylesheet sets no colour token, so no rule
// can draw over a block.
func TestEveryPaletteAndModeIsDrawnWithTheMockupsValues(t *testing.T) {
	t.Parallel()
	index := build(t, t.TempDir())
	asset, found := index.Asset(front.PalettesPath)
	if !found {
		t.Fatalf("%s is not served", front.PalettesPath)
	}
	served := tokenBlocks(string(asset.Body))
	mockup := tokenBlocks(mockupStyle(t))
	if len(mockup) != 12 {
		t.Fatalf("the mockup holds %d palette blocks, not six palettes in two modes", len(mockup))
	}
	ids := make([]string, 0, len(mockup))
	for id := range mockup {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		block, drawn := served[id]
		if !drawn {
			t.Errorf("%s: the served palettes hold no block for it, so choosing it draws nothing", id)
			continue
		}
		for token, value := range mockup[id] {
			if block[token] != value {
				t.Errorf("%s: --%s is %q, the mockup's is %q", id, token, block[token], value)
			}
		}
		for token := range block {
			if _, inMockup := mockup[id][token]; !inMockup {
				t.Errorf("%s: --%s is set and the mockup does not set it", id, token)
			}
		}
	}

	// Before any script runs, the document has no data-theme: the industrial palette, in the mode
	// the operating system prefers.
	defaults := regexp.MustCompile(`(?s):root:not\(\[data-theme\]\)\s*\{([^}]*)\}`).FindAllStringSubmatch(string(asset.Body), -1)
	if len(defaults) != 2 {
		t.Fatalf("the served palettes hold %d blocks for a document without data-theme, not a light and a dark", len(defaults))
	}
	for index, mode := range []string{"industrial-light", "industrial-dark"} {
		values := tokenBlocks(`:root[data-theme="x"] {` + defaults[index][1] + `}`)["x"]
		for token, value := range mockup[mode] {
			if values[token] != value {
				t.Errorf("without data-theme (%s): --%s is %q, the mockup's is %q", mode, token, values[token], value)
			}
		}
	}

	// Every palette and mode the header offers is a block: the attribute it writes always selects one.
	for _, theme := range index.Themes() {
		for _, mode := range []string{front.ModeLight, front.ModeDark} {
			if _, drawn := served[theme.ID+"-"+mode]; !drawn {
				t.Errorf("the header offers %s in %s and no block draws it", theme.ID, mode)
			}
		}
	}

	// The core stylesheet sets no colour token: nothing it holds can draw over a palette's block.
	raw, err := fs.ReadFile(opnview.BuiltinFront(), "tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var tokens struct {
		Tokens []string `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &tokens); err != nil {
		t.Fatal(err)
	}
	style, err := fs.ReadFile(opnview.BuiltinFront(), "style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens.Tokens {
		if regexp.MustCompile(`--` + regexp.QuoteMeta(token) + `\s*:`).Match(style) {
			t.Errorf("front/style.css sets --%s, which only a palette sets", token)
		}
	}
}
