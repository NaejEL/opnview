package frontjs_test

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The recorded answers every renderer is drawn on.
//
// PROVENANCE. They are the widget API's own answers as it serves them today, recorded from the real
// handlers over the widget package's synthesised fixture (internal/widget/widgets/fixture_test.go:
// documentation-range addresses, RFC 2606 names, interface and client counts as parameters) by its
// explicitly invoked generator, and committed as testdata/widget_answers.json.gz beside this file:
//
//	docker compose run --rm -e OPNVIEW_RECORD_FRONT_ANSWERS=/workspace/internal/front/frontjs/testdata/widget_answers.json.gz \
//	  dev go test -count=1 -run '^TestEveryBuiltinWidgetAnswersByteForByteAsBefore$' ./internal/widget/widgets/
//
// They cover every request of that package's golden record: every type at its defaults and periods
// and every parameter combination its tests exercise. Unlike the golden record, which pins the answers
// as they were before later cycles added members, nothing is taken out of them. The pre-deployment
// command answers every type again on the 100 000-row seed and fails when a type's member paths
// differ from the ones recorded here (ci/answerdrift).

// recordedAnswer is one recorded answer: its type, the parameters it was asked with, and the answer.
type recordedAnswer struct {
	Type       string
	Parameters string
	Answer     map[string]any
	Raw        []byte
}

var (
	recordedOnce sync.Once
	recorded     map[string][]recordedAnswer
	recordedErr  error
)

// recordedAnswers reads the golden answers once, by type, in a stable order.
func recordedAnswers(t testing.TB) map[string][]recordedAnswer {
	t.Helper()
	recordedOnce.Do(func() {
		_, here, _, _ := runtime.Caller(0)
		file, err := os.Open(filepath.Join(filepath.Dir(here), "testdata", "widget_answers.json.gz"))
		if err != nil {
			recordedErr = err
			return
		}
		defer func() { _ = file.Close() }()
		reader, err := gzip.NewReader(file)
		if err != nil {
			recordedErr = err
			return
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			recordedErr = err
			return
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			recordedErr = err
			return
		}
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		recorded = map[string][]recordedAnswer{}
		for _, key := range keys {
			body := entries[key]
			// An entry is the answer's JSON, or that JSON as a string.
			var text string
			if json.Unmarshal(body, &text) == nil {
				body = json.RawMessage(text)
			}
			var answer map[string]any
			if err := json.Unmarshal(body, &answer); err != nil {
				recordedErr = err
				return
			}
			widgetType, parameters, _ := strings.Cut(key, " ")
			recorded[widgetType] = append(recorded[widgetType], recordedAnswer{Type: widgetType, Parameters: parameters,
				Answer: answer, Raw: body})
		}
	})
	if recordedErr != nil {
		t.Fatalf("reading the recorded answers: %v", recordedErr)
	}
	return recorded
}

// drawable are the recorded answers of a type that carry data to draw.
func drawable(answers []recordedAnswer) []recordedAnswer {
	var out []recordedAnswer
	for _, answer := range answers {
		if answer.Answer["data"] != nil && answer.Answer["condition"] != "reachable_no_rows" {
			out = append(out, answer)
		}
	}
	return out
}

// firstDrawable is a type's reference answer: the one asked with no parameters when it has data,
// else the first that has.
func firstDrawable(t testing.TB, widgetType string) recordedAnswer {
	t.Helper()
	answers := drawable(recordedAnswers(t)[widgetType])
	if len(answers) == 0 {
		t.Fatalf("no recorded answer of %s carries data", widgetType)
	}
	for _, answer := range answers {
		if answer.Parameters == "" {
			return answer
		}
	}
	return answers[0]
}

// manifestParameter is one parameter of a built-in manifest, as the registry lists it.
type manifestParameter struct {
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	Values    []string `json:"values"`
	Default   any      `json:"default"`
	Class     string   `json:"class"`
	Reference string   `json:"reference"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Required  bool     `json:"required,omitempty"`
}

// builtinWidget is one built-in widget's manifest and its renderer's text.
type builtinWidget struct {
	Type       string              `json:"type"`
	Shape      string              `json:"shape"`
	Windowed   bool                `json:"windowed"`
	Volumes    bool                `json:"volumes"`
	Parameters []manifestParameter `json:"parameters"`
	Renderer   *struct {
		Entry string `json:"entry"`
	} `json:"renderer"`
	Script       string `json:"script"`
	RendererText string `json:"-"`
}

var (
	builtinOnce sync.Once
	builtins    []builtinWidget
	builtinErr  error
)

// builtinWidgets reads every built-in widget's manifest, by type: the list is the plugins' own
// directories, never written down.
func builtinWidgets(t testing.TB) []builtinWidget {
	t.Helper()
	builtinOnce.Do(func() {
		plugins := opnview.BuiltinPlugins()
		manifests, err := fs.Glob(plugins, "*/widgets/*/manifest.json")
		if err != nil {
			builtinErr = err
			return
		}
		for _, manifest := range manifests {
			raw, err := fs.ReadFile(plugins, manifest)
			if err != nil {
				builtinErr = err
				return
			}
			var widget builtinWidget
			if err := json.Unmarshal(raw, &widget); err != nil {
				builtinErr = err
				return
			}
			if widget.Renderer != nil {
				text, err := fs.ReadFile(plugins, strings.TrimSuffix(manifest, "manifest.json")+widget.Renderer.Entry)
				if err != nil {
					builtinErr = err
					return
				}
				widget.RendererText = string(text)
			}
			for index := range widget.Parameters {
				if widget.Parameters[index].Class == "" {
					widget.Parameters[index].Class = "data"
				}
			}
			builtins = append(builtins, widget)
		}
		sort.Slice(builtins, func(i, j int) bool { return builtins[i].Type < builtins[j].Type })
	})
	if builtinErr != nil {
		t.Fatalf("reading the built-in manifests: %v", builtinErr)
	}
	return builtins
}

// rowOf is the registry row of a built-in, as GET /api/widgets lists it, in JSON.
func rowOf(t testing.TB, widget builtinWidget) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": widget.Type, "shape": widget.Shape, "windowed": widget.Windowed,
		"parameters": widget.Parameters, "backend": true, "title_key": "", "question_key": ""})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// drawingPage is one page holding every renderer and the English catalogue, for drawing answers.
func drawingPage(t *testing.T) *frontjs.Runtime {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{})
	page.LoadRenderers()
	english, err := json.Marshal(englishCatalogue(t))
	if err != nil {
		t.Fatal(err)
	}
	setJSON(t, page, "__catalogue", string(english))
	page.Run(`opnview.strings.install("en", __catalogue, __catalogue);
var __host = document.createElement("div"); document.body.appendChild(__host);
function __plain(tree) { return JSON.stringify(tree, function (key, value) { return typeof value === "function" ? undefined : value; }); }
function __draw(row, answer, parameters, size) {
  var drawn = opnview.widgets.draw({ answer: answer, parameters: parameters || answer.parameters, row: row,
    size: size || { width: 620, height: 360 }, prefix: "w" });
  if (drawn.failed) { throw new Error("the drawing failed: " + drawn.failed); }
  return drawn;
}
function __render(row, answer, parameters, size) {
  var drawn = __draw(row, answer, parameters, size);
  opnview.view.render(__host, [drawn.tree]);
  return drawn;
}`)
	return page
}

// setJSON gives the page a value decoded from JSON, under a name.
func setJSON(t *testing.T, page *frontjs.Runtime, name, text string) {
	t.Helper()
	page.Run("var " + name + " = " + text + ";")
}
