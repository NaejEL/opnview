package front_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
)

// What the front's files may not contain, read from the files themselves: F7-AC4's outbound rule
// and shell, F7-AC7's palette values, F7-AC8's colour literals.

// frontText is every text file of the built-in front, by its path below front/.
func frontText(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := fs.WalkDir(opnview.BuiltinFront(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(opnview.BuiltinFront(), path)
		if err != nil {
			return err
		}
		files[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// renderers are the renderer entries of every built-in widget plugin, by path.
func renderers(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	plugins := opnview.BuiltinPlugins()
	manifests, err := fs.Glob(plugins, "*/widgets/*/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, manifest := range manifests {
		raw, err := fs.ReadFile(plugins, manifest)
		if err != nil {
			t.Fatal(err)
		}
		var declared struct {
			Renderer *struct {
				Entry string `json:"entry"`
			} `json:"renderer"`
		}
		if err := json.Unmarshal(raw, &declared); err != nil {
			t.Fatal(err)
		}
		if declared.Renderer == nil {
			continue
		}
		path := filepath.ToSlash(filepath.Join(filepath.Dir(manifest), declared.Renderer.Entry))
		text, err := fs.ReadFile(plugins, path)
		if err != nil {
			t.Fatal(err)
		}
		files["plugins/"+path] = string(text)
	}
	return files
}

// eventSourceOpened is every construction of an EventSource, with its first argument.
var eventSourceOpened = regexp.MustCompile(`new\s+(?:[A-Za-z_$][\w$]*\.)*EventSource\s*\(\s*([^,)]*)`)

var fetchingPosition = regexp.MustCompile(`(?i)(src\s*=|href\s*=|url\(|fetch\(|import\s*\(?|@import)\s*["']?\s*https?://`)

func TestNoFrontFileLoadsAnythingFromAnotherOrigin(t *testing.T) {
	t.Parallel()
	scanned := 0
	for path, text := range mergeMaps(frontText(t), renderers(t)) {
		scanned++
		if match := fetchingPosition.FindString(text); match != "" {
			t.Errorf("%s loads from another origin: %q", path, match)
		}
		for _, forbidden := range []string{"@font-face", "XMLHttpRequest", "WebSocket", "@import"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s holds %s", path, forbidden)
			}
		}
		// The one event stream (D21 as amended): opened by the API module alone, on this server's own
		// /api/events, never on another origin.
		if strings.Contains(text, "EventSource") {
			if path != "core/api.js" {
				t.Errorf("%s holds EventSource: only core/api.js opens the event stream", path)
			}
			for _, opened := range eventSourceOpened.FindAllStringSubmatch(text, -1) {
				if opened[1] != `"/api/events"` {
					t.Errorf("%s opens an EventSource on %s, not on this server's \"/api/events\"", path, opened[1])
				}
			}
			if !eventSourceOpened.MatchString(text) {
				t.Errorf("%s names EventSource but never opens it on a literal path", path)
			}
		}
	}
	if scanned < 10 {
		t.Fatalf("only %d files were scanned", scanned)
	}
}

func mergeMaps(maps ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, entries := range maps {
		for key, value := range entries {
			merged[key] = value
		}
	}
	return merged
}

func TestTheShellHoldsNoInlineScriptAndNoStyleAttribute(t *testing.T) {
	t.Parallel()
	shell := frontText(t)["shell.html"]
	for _, script := range regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`).FindAllStringSubmatch(shell, -1) {
		if !strings.Contains(script[1], `src="/front/`) || strings.TrimSpace(script[2]) != "" {
			t.Errorf("the shell holds an inline script: %q", script[0])
		}
	}
	if strings.Contains(shell, "style=") || strings.Contains(shell, "<style") {
		t.Error("the shell holds a style attribute or element")
	}
}

// colourLiteral is a colour written as a value: a hex, rgb(), rgba(), hsl() or hsla() colour, or a
// named one.
var (
	hexValue     = regexp.MustCompile(`(?i)(^|[\s:,(])#[0-9a-f]{3,8}\b`)
	functionalRe = regexp.MustCompile(`(?i)\b(rgba?|hsla?)\(`)
	namedColour  = regexp.MustCompile(`(?i):\s*[^;{}]*\b(white|black|red|green|blue|yellow|orange|purple|gray|grey|pink|silver|navy|teal|maroon|transparent)\b`)
	cssComment   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func TestNoColourLiteralOutsideTheThemeFiles(t *testing.T) {
	t.Parallel()
	for path, text := range mergeMaps(frontText(t), renderers(t)) {
		if strings.HasPrefix(path, "themes/") || path == "tokens.json" || strings.HasPrefix(path, "locale/") {
			continue
		}
		body := cssComment.ReplaceAllString(text, "")
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if match := hexValue.FindString(line); match != "" {
				t.Errorf("%s writes a colour: %q", path, trimmed)
			}
			if match := functionalRe.FindString(line); match != "" {
				t.Errorf("%s writes a colour: %q", path, trimmed)
			}
			if strings.HasSuffix(path, ".css") {
				if match := namedColour.FindString(line); match != "" {
					t.Errorf("%s writes a named colour: %q", path, trimmed)
				}
			}
		}
	}
	// The core's non-colour tokens: the chamfer and the system font stack (Family C).
	style := frontText(t)["style.css"]
	for _, want := range []string{"--border-radius: 0.25rem;",
		`--font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", "Oxygen", "Ubuntu", "Cantarell", ` +
			`"Fira Sans", "Droid Sans", "Helvetica Neue", Arial, sans-serif;`} {
		if !strings.Contains(style, want) {
			t.Errorf("the stylesheet does not declare %s", want)
		}
	}
}

// The values docs/ui-references.md records, and the theme token each is.
var siteColumns = []string{"bg-app", "bg-surface", "text-primary", "text-strong", "text-secondary", "accent",
	"border-color", "status-error"}

// familyC maps the industrial palette's recorded properties (Family C) to the theme tokens they are.
var familyC = map[string]string{
	"--bg-primary": "bg-app", "--bg-secondary": "bg-raised", "--input-bg": "bg-surface",
	"--text-primary": "text-primary", "--text-secondary": "text-secondary", "--text-muted": "text-muted",
	"--border-color": "border-color", "--input-border": "border-strong", "--button-bg": "accent",
	"--button-hover-bg": "accent-hover", "--link-color": "link", "--link-hover": "link-hover",
	"--on-accent": "on-accent", "--status-success": "status-ok", "--status-warning": "status-warn",
	"--status-error": "status-error", "--notice-info": "status-info", "--log-highlight-bg": "highlight-bg",
	"--shadow-md": "shadow-color",
}

// resolved is a token's value with its var() references followed.
func resolved(values map[string]string, token string) string {
	value := values[token]
	for seen := 0; strings.HasPrefix(value, "var(--") && seen < 8; seen++ {
		value = values[strings.TrimSuffix(strings.TrimPrefix(value, "var(--"), ")")]
	}
	return value
}

func readTheme(t *testing.T, id string) map[string]map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(opnview.BuiltinFront(), "themes/"+id+".json")
	if err != nil {
		t.Fatal(err)
	}
	var theme struct {
		Modes map[string]map[string]string `json:"modes"`
	}
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatal(err)
	}
	return theme.Modes
}

func TestEveryThemeHoldsTheValuesTheReferencesRecord(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs", "ui-references.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	// The five named palettes, from the table of the maintainer's site.
	start := strings.Index(document, "#### The values `opnview` uses")
	end := strings.Index(document[start:], "Two consequences")
	if start < 0 || end < 0 {
		t.Fatal("docs/ui-references.md no longer holds the table of the values opnview uses")
	}
	ids := map[string]string{"Tokyo Night": "tokyo-night", "Dracula": "dracula", "Nord": "nord",
		"Rosé Pine": "rose-pine", "Catppuccin": "catppuccin"}
	compared := 0
	for _, line := range strings.Split(document[start:start+end], "\n") {
		cells := strings.Split(strings.Trim(line, "| "), "|")
		if len(cells) != 10 {
			continue
		}
		id, known := ids[strings.TrimSpace(cells[0])]
		if !known {
			continue
		}
		mode := strings.TrimSpace(cells[1])
		values := readTheme(t, id)[mode]
		for index, token := range siteColumns {
			want := strings.Trim(strings.TrimSpace(cells[2+index]), "`")
			if got := resolved(values, token); got != want {
				t.Errorf("%s %s %s is %s, and the site's value is %s", id, mode, token, got, want)
			}
			compared++
		}
	}
	if compared != 5*2*len(siteColumns) {
		t.Errorf("compared %d values of the site's table, want %d", compared, 5*2*len(siteColumns))
	}
	// The industrial palette, from Family C's own table.
	start = strings.Index(document, "**Colour and effect properties**")
	end = strings.Index(document[start:], "**The semantic status colours**")
	industrial := readTheme(t, "industrial")
	compared = 0
	for _, line := range strings.Split(document[start:start+end], "\n") {
		cells := strings.Split(strings.Trim(line, "| "), "|")
		if len(cells) != 3 {
			continue
		}
		token, mapped := familyC[strings.Trim(strings.TrimSpace(cells[0]), "`")]
		if !mapped {
			continue
		}
		for index, mode := range []string{"light", "dark"} {
			want := strings.Trim(strings.TrimSpace(cells[1+index]), "`")
			if got := resolved(industrial[mode], token); got != want {
				t.Errorf("industrial %s %s is %s, and Family C records %s", mode, token, got, want)
			}
			compared++
		}
	}
	if compared != 2*len(familyC) {
		t.Errorf("compared %d values of Family C, want %d", compared, 2*len(familyC))
	}
}
