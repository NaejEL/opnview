package frontend_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// F7-AC38: the four worked examples of docs/front-plugins.md -- a theme, a language, a front-only
// widget on the data catalogue, and a renderer carried by a plugin widget -- exist as files under
// testdata/front-plugins/. Each is copied into a scratch data directory exactly where the document
// says it goes, a server is started over it, and each part is reached: the theme in /api/front, the
// language in /api/strings/{code} and in the language picker's options, the front-only widget in
// /api/widgets with its renderer drawing from a dataset answer, the plugin widget's renderer served
// and drawing. No file of the repository changes.
//
// FIXTURE PROVENANCE. The four examples are written for the document and carry no value of any
// network: the probe targets below are RFC 2606 names, and the theme's colours are invented.

// workedExamples are the example files, by their path below testdata/front-plugins/, and where the
// document tells an operator to put them, below the data directory. Each destination is checked to
// be written in docs/front-plugins.md, so the test copies what the document says.
var workedExamples = []struct{ source, destination string }{
	{"themes/example-sepia.json", "themes/example-sepia.json"},
	{"languages/en-GB.json", "languages/en-GB.json"},
	{"widgets/example_lookups_by_hour", "widgets/example_lookups_by_hour"},
	{"plugins/example_latency", "plugins/example_latency"},
}

// copyTree copies a file or a directory.
func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o750)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// treeDigest is a digest of every file below a directory, names included.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var names []string
	contents := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names = append(names, path)
		contents[path] = content
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(contents[name]))
		hash.Write(contents[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// drawn runs a renderer file in a page and draws one answer with it, answering what the drawing
// holds: the text of the rows of its table, the keys of its bars and the samples line of each bar.
func drawn(t *testing.T, renderer string, catalogue map[string]string, row map[string]any, answer any,
	parameters map[string]any) map[string][]string {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{})
	encodedStrings, _ := json.Marshal(catalogue)
	page.Run(fmt.Sprintf(`opnview.strings.install("en", %s, %s);`, encodedStrings, encodedStrings))
	page.Run(renderer)
	encodedRow, _ := json.Marshal(row)
	encodedAnswer, _ := json.Marshal(answer)
	encodedParameters, _ := json.Marshal(parameters)
	result := page.Run(fmt.Sprintf(`(function () {
  var answer = %s;
  var drawing = opnview.widgets.draw({ answer: answer, parameters: %s, row: %s,
    size: { width: 640, height: 320 }, prefix: "example" });
  if (drawing.failed) { return JSON.stringify({ failed: [drawing.failed] }); }
  var host = document.createElement("div");
  document.body.appendChild(host);
  opnview.view.render(host, [drawing.tree]);
  function texts(selector) {
    return Array.prototype.map.call(host.querySelectorAll(selector), function (node) { return node.textContent; });
  }
  function attributes(selector, name) {
    return Array.prototype.map.call(host.querySelectorAll(selector), function (node) { return node.getAttribute(name); });
  }
  return JSON.stringify({ rows: texts("tbody tr"), bars: attributes("[data-bar]", "data-bar"),
    samples: texts(".bar-sub"), missing: opnview.strings.missing() });
})()`, encodedAnswer, encodedParameters, encodedRow)).String()
	var out map[string][]string
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if len(out["failed"]) > 0 {
		t.Fatalf("the renderer failed: %v", out["failed"])
	}
	if len(out["missing"]) > 0 {
		t.Errorf("the drawing asked for keys the catalogue does not hold: %v", out["missing"])
	}
	return out
}

func TestTheFourWorkedExamplesOfTheFrontPluginDocumentAreInstalledFromFilesAlone(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	examples := filepath.Join(repository, "testdata", "front-plugins")
	before := treeDigest(t, examples)
	document, err := os.ReadFile(filepath.Join(repository, "docs", "front-plugins.md"))
	if err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	for _, example := range workedExamples {
		if !strings.Contains(string(document), "<data-dir>/"+example.destination) {
			t.Errorf("docs/front-plugins.md does not tell an operator to put an example at <data-dir>/%s",
				example.destination)
		}
		copyTree(t, filepath.Join(examples, filepath.FromSlash(example.source)),
			filepath.Join(dataDir, filepath.FromSlash(example.destination)))
	}
	harness := webtest.NewAdjusted(t, dataDir, true, false, func(options *web.Options) {
		loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: options.Store})
		if err != nil {
			t.Fatal(err)
		}
		if refusals := loaded.Refusals(); len(refusals) != 0 {
			t.Fatalf("the example plugin was refused: %+v", refusals)
		}
		options.Plugins = loaded
	})
	harness.CompleteSetup()

	// The theme, and the language with its picker's options.
	var answer frontAnswer
	getJSON(t, harness, "/api/front", &answer)
	themes := map[string]string{}
	for _, theme := range answer.Themes {
		themes[theme.ID] = theme.URL
	}
	if themes["example-sepia"] == "" {
		t.Errorf("/api/front does not offer the example theme: %v", answer.Themes)
	}
	languages := map[string]string{}
	for _, language := range answer.Languages {
		languages[language.Code] = language.Name
	}
	if languages["en-GB"] != "English (United Kingdom)" {
		t.Errorf("/api/front does not offer the example language: %v", answer.Languages)
	}
	var appearance struct {
		Languages []struct {
			Code string `json:"code"`
		} `json:"languages"`
	}
	getJSON(t, harness, "/api/appearance", &appearance)
	offered := []string{}
	for _, language := range appearance.Languages {
		offered = append(offered, language.Code)
	}
	if strings.Join(offered, " ") != "en en-GB" {
		t.Errorf("the language picker offers %v", offered)
	}
	var british struct {
		Language string            `json:"language"`
		Strings  map[string]string `json:"strings"`
	}
	if status := getJSON(t, harness, "/api/strings/en-GB", &british); status != http.StatusOK ||
		british.Strings["appearance.palette"] != "Colour palette" {
		t.Errorf("/api/strings/en-GB answered %d with appearance.palette %q", status, british.Strings["appearance.palette"])
	}
	var english struct {
		Strings map[string]string `json:"strings"`
	}
	getJSON(t, harness, "/api/strings/en", &english)

	// The two widgets in the registry, their renderers served.
	var listed struct {
		Widgets []struct {
			Type        string  `json:"type"`
			Shape       string  `json:"shape"`
			Plugin      string  `json:"plugin"`
			Backend     bool    `json:"backend"`
			RendererURL *string `json:"renderer_url"`
		} `json:"widgets"`
		Refused []any `json:"refused"`
	}
	getJSON(t, harness, "/api/widgets", &listed)
	if len(listed.Refused) != 0 {
		t.Errorf("a worked example was refused: %v", listed.Refused)
	}
	rows := map[string]map[string]any{}
	renderers := map[string]string{}
	for _, row := range listed.Widgets {
		if row.Type != "example_lookups_by_hour" && row.Type != "example_latency_targets" {
			continue
		}
		if row.RendererURL == nil {
			t.Errorf("%s is listed with no renderer", row.Type)
			continue
		}
		rows[row.Type] = map[string]any{"type": row.Type, "shape": row.Shape}
		status, body := webtest.StatusAndBody(t, harness.Get(*row.RendererURL))
		if status != http.StatusOK {
			t.Errorf("%s's renderer at %s answered %d", row.Type, *row.RendererURL, status)
		}
		renderers[row.Type] = body
		switch row.Type {
		case "example_lookups_by_hour":
			if row.Backend {
				t.Error("the front-only widget is listed as answered by the server")
			}
		case "example_latency_targets":
			if !row.Backend || row.Plugin != "example_latency" {
				t.Errorf("the plugin widget's row is %+v", row)
			}
		}
	}
	if len(renderers) != 2 {
		t.Fatalf("the registry lists the examples %v", rows)
	}

	// The front-only widget: its renderer names a read of the data catalogue, the core performs it on
	// this server, and the renderer draws the answer -- one row per bucket of the period.
	page := frontjs.New(t, frontjs.Options{})
	page.Run(renderers["example_lookups_by_hour"])
	requested := page.Run(`JSON.stringify(opnview.widgets.renderer("example_lookups_by_hour", null)
		.requests({ parameters: { period: "24h" } }))`).String()
	var paths []string
	if err := json.Unmarshal([]byte(requested), &paths); err != nil || len(paths) != 1 ||
		!strings.HasPrefix(paths[0], "/api/datasets/dns/query?parameters=") {
		t.Fatalf("the front-only widget asks for %s", requested)
	}
	if allowed := page.Run(fmt.Sprintf(`opnview.widgets.allowedRead(%q)`, paths[0])).ToBoolean(); !allowed {
		t.Errorf("the core would refuse the front-only widget's read %s", paths[0])
	}
	status, body := webtest.StatusAndBody(t, harness.Get(paths[0]))
	if status != http.StatusOK {
		t.Fatalf("the dataset read answered %d: %.300s", status, body)
	}
	// This scratch installation has collected nothing, so the server's answer is the dataset's
	// envelope with no data -- a condition the card draws as a panel, never the renderer. The
	// renderer is drawn on an answer of the same envelope with three buckets: two the resolver
	// counted, and one the collection did not cover, which is a dash and never a zero.
	var dataset map[string]any
	if err := json.Unmarshal([]byte(body), &dataset); err != nil || dataset["dataset"] != "dns" {
		t.Fatalf("the dataset read answered %.300s", body)
	}
	hour := int64(3600)
	start := harness.Clock.Now().Unix()/hour*hour - 3*hour
	dataset["condition"] = "reachable"
	dataset["data"] = map[string]any{
		"x": map[string]any{"column": "looked_up_at", "bucket_seconds": hour, "alignment": "epoch"},
		"series": []any{map[string]any{"key": "lookups", "column": "lookups", "aggregation": "sum", "unit": "lookup",
			"split": nil, "buckets": []any{
				map[string]any{"from": start, "to": start + hour, "gap": false, "value": 412},
				map[string]any{"from": start + hour, "to": start + 2*hour, "gap": true, "value": nil},
				map[string]any{"from": start + 2*hour, "to": start + 3*hour, "gap": false, "value": 97},
			}}},
	}
	reads := []any{map[string]any{"path": paths[0], "status": status, "body": dataset}}
	frontOnly := drawn(t, renderers["example_lookups_by_hour"], english.Strings, rows["example_lookups_by_hour"],
		map[string]any{"type": "example_lookups_by_hour", "condition": "reachable", "answers": reads, "data": reads},
		map[string]any{"period": "24h"})
	if got := frontOnly["rows"]; len(got) != 3 || !strings.HasSuffix(got[0], "412") ||
		!strings.HasSuffix(got[1], english.Strings["figure.dash"]) || strings.HasSuffix(got[1], "0") ||
		!strings.HasSuffix(got[2], "97") {
		t.Errorf("the front-only widget drew the rows %q", got)
	}

	// The plugin widget: the server answers it from its own table, and its own renderer draws it.
	now := harness.Clock.Now().Unix()
	for index, sample := range []struct {
		target  string
		latency float64
	}{{"alpha.example.net", 12}, {"alpha.example.net", 48}, {"beta.example.org", 7.5}} {
		if _, err := harness.Store.DB().ExecContext(context.Background(),
			"INSERT INTO plugin_example_latency.probe (measured_at, target, latency_ms) VALUES (?, ?, ?)",
			now-600-int64(index), sample.target, sample.latency); err != nil {
			t.Fatal(err)
		}
	}
	var served map[string]any
	if status := getJSON(t, harness, "/api/widget/example_latency_targets", &served); status != http.StatusOK {
		t.Fatalf("the plugin widget answered %d: %v", status, served)
	}
	for _, show := range []bool{true, false} {
		bars := drawn(t, renderers["example_latency_targets"], english.Strings, rows["example_latency_targets"], served,
			map[string]any{"period": "24h", "show_samples": show})
		if strings.Join(bars["bars"], " ") != "alpha.example.net beta.example.org" {
			t.Errorf("the plugin widget's renderer drew the bars %v", bars["bars"])
		}
		if want := map[bool]string{true: "2 samples|1 sample", false: ""}[show]; strings.Join(bars["samples"], "|") != want {
			t.Errorf("with show_samples %v the bars say %v", show, bars["samples"])
		}
	}

	if after := treeDigest(t, examples); after != before {
		t.Error("the test changed a file of testdata/front-plugins")
	}
}
