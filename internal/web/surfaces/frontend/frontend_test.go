package frontend_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// The frontend surface over a running server: the shell and the assets with their headers (F7-AC3,
// F7-AC4), the front's parts of the data directory with their refusals listed (F7-AC1, F7-AC2),
// the appearance (F7-AC6) and the widget registry (F7-AC11).
//
// FIXTURE PROVENANCE. The parts dropped into the data directory are written here, named example_*,
// and carry no value of any network.

// policy is A3's Content-Security-Policy, verbatim.
const policy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// frontOnly writes a front-only widget: a manifest naming a renderer, and the files given.
func frontOnly(t *testing.T, dataDir, widgetType string, files map[string]string) {
	t.Helper()
	directory := filepath.Join(dataDir, "widgets", widgetType)
	write(t, filepath.Join(directory, "manifest.json"), `{"manifest_version": 1, "type": "`+widgetType+
		`", "shape": "table", "renderer": {"entry": "draw.js"}}`)
	for name, text := range files {
		write(t, filepath.Join(directory, name), text)
	}
}

const classicRenderer = `(function (root) { "use strict"; root.opnview = root.opnview || {}; })(this);`

func getJSON(t *testing.T, harness *webtest.Harness, target string, into any) int {
	t.Helper()
	status, body := webtest.StatusAndBody(t, harness.Get(target))
	if into != nil {
		if err := json.Unmarshal([]byte(body), into); err != nil {
			t.Fatalf("GET %s is not JSON (%d): %v: %.300s", target, status, err, body)
		}
	}
	return status
}

// registry is GET /api/widgets as a test reads it.
type registry struct {
	Widgets []struct {
		Type           string                     `json:"type"`
		Backend        bool                       `json:"backend"`
		Parameters     []params.ManifestParameter `json:"parameters"`
		RendererURL    *string                    `json:"renderer_url"`
		TitleKey       string                     `json:"title_key"`
		MissingStrings []string                   `json:"missing_strings"`
	} `json:"widgets"`
	Refused []struct {
		Part string `json:"part"`
		ID   string `json:"id"`
		Code string `json:"code"`
		Path string `json:"path"`
	} `json:"refused"`
}

type frontAnswer struct {
	Themes []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"themes"`
	Languages []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"languages"`
	Assets struct {
		Scripts     []string `json:"scripts"`
		Stylesheets []string `json:"stylesheets"`
	} `json:"assets"`
}

func TestTheShellAndEveryAssetCarryThePolicyNosniffAndATag(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	var front frontAnswer
	if status := getJSON(t, harness, "/api/front", &front); status != http.StatusOK {
		t.Fatalf("GET /api/front answered %d", status)
	}
	targets := []string{"/", "/settings", "/canvas/1/main", "/front/palettes.css"}
	targets = append(targets, front.Assets.Scripts...)
	targets = append(targets, front.Assets.Stylesheets...)
	for _, theme := range front.Themes {
		targets = append(targets, theme.URL)
	}
	if len(front.Assets.Scripts) < 10 || len(front.Assets.Stylesheets) != 2 {
		t.Fatalf("the core's asset URLs are %v", front.Assets)
	}
	for _, target := range targets {
		answer := harness.Get(target)
		_ = webtest.ReadBody(t, answer)
		_ = answer.Body.Close()
		if answer.StatusCode != http.StatusOK {
			t.Errorf("GET %s answered %d", target, answer.StatusCode)
			continue
		}
		if got := answer.Header.Get("Content-Security-Policy"); got != policy {
			t.Errorf("GET %s carries the policy %q", target, got)
		}
		if answer.Header.Get("X-Content-Type-Options") != "nosniff" || answer.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s carries %v", target, answer.Header)
		}
		tag := answer.Header.Get("ETag")
		if tag == "" {
			t.Errorf("GET %s carries no tag", target)
			continue
		}
		request, err := http.NewRequest(http.MethodGet, harness.HTTP.URL+target, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("If-None-Match", tag)
		again, err := harness.Client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = again.Body.Close()
		if again.StatusCode != http.StatusNotModified {
			t.Errorf("GET %s with its own tag answered %d, not 304", target, again.StatusCode)
		}
	}
}

func TestAFileTheIndexDoesNotDeclareIsNeverServed(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	targets := []string{"/front/tokens.json", "/front/shell.html", "/front/locale/en.json", "/front/../go.mod",
		"/front/%2e%2e/go.mod", "/front/core/%2e%2e/%2e%2e/go.mod", "/front/core/..%2f..%2fgo.mod",
		"/front/widgets/top_sites/manifest.json", "/front/widgets/top_sites/top_sites.js"}
	// Every file of every built-in plugin, at its own path and as a widget renderer's path: only a
	// declared renderer entry may answer.
	plugins := opnview.BuiltinPlugins()
	declared := map[string]bool{}
	err := fs.WalkDir(plugins, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		targets = append(targets, "/front/"+file, "/front/plugins/"+file)
		parts := strings.Split(file, "/")
		if len(parts) == 4 && parts[1] == "widgets" {
			targets = append(targets, "/front/widgets/"+parts[2]+"/"+parts[3])
			if parts[3] == "manifest.json" {
				raw, err := fs.ReadFile(plugins, file)
				if err != nil {
					return err
				}
				manifest, err := params.DecodeManifest(raw)
				if err != nil {
					return err
				}
				if manifest.Renderer != nil {
					declared["/front/widgets/"+parts[2]+"/"+manifest.Renderer.Entry] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		request, err := http.NewRequest(http.MethodGet, harness.HTTP.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		// The path is sent as written, encoded traversals included.
		request.URL.Opaque = target
		answer, err := harness.Client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = answer.Body.Close()
		if declared[target] {
			if answer.StatusCode != http.StatusOK {
				t.Errorf("the renderer entry %s answered %d", target, answer.StatusCode)
			}
			continue
		}
		if answer.StatusCode == http.StatusOK && answer.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("GET %s served a file the index does not declare (%s)", target, answer.Header.Get("Content-Type"))
		}
		if answer.StatusCode == http.StatusOK && !strings.HasPrefix(target, "/front/") {
			continue
		}
		if strings.HasPrefix(path.Clean(target), "/front/") && answer.StatusCode != http.StatusNotFound &&
			answer.StatusCode != http.StatusMovedPermanently {
			t.Errorf("GET %s answered %d, not 404", target, answer.StatusCode)
		}
	}
}

func TestAnUnknownAPIPathAnswersJSONAndAnyOtherPathTheShell(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	var refused map[string]string
	if status := getJSON(t, harness, "/api/no-such-route", &refused); status != http.StatusNotFound ||
		refused["condition"] != "unknown_route" {
		t.Errorf("an unknown API path answered %d %v", status, refused)
	}
	status, body := webtest.StatusAndBody(t, harness.Get("/no/such/view"))
	if status != http.StatusOK || !strings.Contains(body, `<div id="app"></div>`) {
		t.Errorf("an unknown view path answered %d without the shell", status)
	}
}

func TestPartsOfTheDataDirectoryAreInstalledAtStartAndEachRefusalIsListedWithItsPath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	first := webtest.NewIn(t, dataDir, true)
	var front frontAnswer
	getJSON(t, first, "/api/front", &front)
	if len(front.Themes) != 6 || len(front.Languages) != 1 || front.Languages[0].Code != "en" {
		t.Fatalf("an empty data directory serves %d themes and %v", len(front.Themes), front.Languages)
	}

	industrial, err := fs.ReadFile(opnview.BuiltinFront(), "themes/industrial.json")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dataDir, "themes", "example-sepia.json"),
		strings.Replace(string(industrial), `"id": "industrial"`, `"id": "example-sepia"`, 1))
	write(t, filepath.Join(dataDir, "themes", "industrial.json"), string(industrial))
	write(t, filepath.Join(dataDir, "themes", "example-broken.json"), `{"format_version": 1,`)
	write(t, filepath.Join(dataDir, "languages", "fr.json"),
		`{"language": "fr", "strings": {"language.name": "Français"}}`)
	write(t, filepath.Join(dataDir, "languages", "en.json"),
		`{"language": "en", "strings": {"language.name": "English"}}`)
	frontOnly(t, dataDir, "example_front_only", map[string]string{"draw.js": classicRenderer,
		"locale/en.json": `{"language": "en", "strings": {"widget.example_front_only.title": "Example"}}`})
	frontOnly(t, dataDir, "example_missing_renderer", nil)
	frontOnly(t, dataDir, "example_module_renderer", map[string]string{"draw.js": `export default function () {}`})
	frontOnly(t, dataDir, "example_foreign_strings", map[string]string{"draw.js": classicRenderer,
		"locale/en.json": `{"language": "en", "strings": {"settings.title": "Taken"}}`})
	write(t, filepath.Join(dataDir, "widgets", "top_sites", "manifest.json"),
		`{"manifest_version": 1, "type": "top_sites", "shape": "table", "queries": ["query.sql"]}`)
	write(t, filepath.Join(dataDir, "widgets", "top_sites", "query.sql"), `SELECT 1 AS label, 1 AS value`)

	// A restart: a second server over the same data directory.
	second := first.Reopen()
	second.CompleteSetup()
	front = frontAnswer{}
	getJSON(t, second, "/api/front", &front)
	var themes, languages []string
	for _, theme := range front.Themes {
		themes = append(themes, theme.ID)
	}
	for _, language := range front.Languages {
		languages = append(languages, language.Code)
	}
	sort.Strings(themes)
	if strings.Join(themes, " ") != "catppuccin dracula example-sepia industrial nord rose-pine tokyo-night" ||
		strings.Join(languages, " ") != "en fr" {
		t.Errorf("after the restart the themes are %v and the languages %v", themes, languages)
	}
	var listed registry
	if status := getJSON(t, second, "/api/widgets", &listed); status != http.StatusOK {
		t.Fatalf("GET /api/widgets answered %d", status)
	}
	want := map[string]string{
		"example-broken":           "theme_malformed",
		"industrial":               "name_collision",
		"en":                       "name_collision",
		"example_missing_renderer": params.RefusalRendererMissing,
		"example_module_renderer":  params.RefusalRendererInvalid,
		"example_foreign_strings":  "string_outside_namespace",
		"top_sites":                "name_collision",
	}
	seen := map[string]bool{}
	for _, refused := range listed.Refused {
		code, expected := want[refused.ID]
		if !expected {
			t.Errorf("%s %s was refused %s, which nothing here expects", refused.Part, refused.ID, refused.Code)
			continue
		}
		seen[refused.ID] = true
		if refused.Code != code {
			t.Errorf("%s was refused %s, want %s", refused.ID, refused.Code, code)
		}
		if !strings.HasPrefix(refused.Path, dataDir) {
			t.Errorf("the refusal of %s names the path %q", refused.ID, refused.Path)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is not listed as refused", id)
		}
	}
	// The front-only widget is listed, its renderer served, and its type answered front_only_widget.
	found := false
	for _, row := range listed.Widgets {
		if row.Type == "example_front_only" {
			found = true
			if row.Backend || row.RendererURL == nil || row.TitleKey != "widget.example_front_only.title" ||
				strings.Join(row.MissingStrings, " ") != "widget.example_front_only.question" {
				t.Errorf("the front-only widget's row is %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("the front-only widget is not listed")
	}
	status, body := webtest.StatusAndBody(t, second.Get("/front/widgets/example_front_only/draw.js"))
	if status != http.StatusOK || body != classicRenderer {
		t.Errorf("the front-only renderer answered %d %q", status, body)
	}
	var refusedType map[string]string
	if status := getJSON(t, second, "/api/widget/example_front_only", &refusedType); status != http.StatusNotFound ||
		refusedType["condition"] != "front_only_widget" {
		t.Errorf("GET /api/widget/example_front_only answered %d %v", status, refusedType)
	}
	// The widget's own strings are in the catalogue; a foreign key of a refused file is not.
	var english struct {
		Strings map[string]string `json:"strings"`
	}
	getJSON(t, second, "/api/strings/en", &english)
	if english.Strings["widget.example_front_only.title"] != "Example" || english.Strings["settings.title"] == "Taken" {
		t.Error("the widget directories' strings are not merged as their rules say")
	}

	// Removing the parts and restarting removes them.
	for _, directory := range []string{"themes", "languages", "widgets"} {
		if err := os.RemoveAll(filepath.Join(dataDir, directory)); err != nil {
			t.Fatal(err)
		}
	}
	third := second.Reopen()
	front = frontAnswer{}
	getJSON(t, third, "/api/front", &front)
	if len(front.Themes) != 6 || len(front.Languages) != 1 {
		t.Errorf("after removing the parts, %d themes and %d languages are served", len(front.Themes), len(front.Languages))
	}
}
