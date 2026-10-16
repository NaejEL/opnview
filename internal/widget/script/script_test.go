package widget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// The widget script host's own tests: what the data writer writes, what the loader refuses, and
// that every area of the host is documented. The script widgets' answers themselves are asserted
// by the widget tests (internal/widget/widgets) and by each plugin's own tests.

// scriptCallFor runs a script's top level in a fresh runtime with a host over an empty request,
// and returns the call and the value of the global `value`.
func scriptCallFor(t *testing.T, source string) (*script.ScriptCall, goja.Value) {
	t.Helper()
	vm, _ := script.NewWidgetRuntime("example")
	request := &request.Request{Preset: request.Preset{Type: "example_widget"}, Response: &envelope.Response{}, Refs: map[string][]params.Resolved{}}
	call := script.NewScriptCall(request, vm, &script.ScriptWidget{Plugin: "example", Statements: map[string]script.WidgetStatement{}})
	if err := vm.Set("host", call.Host()); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(source); err != nil {
		t.Fatalf("the script does not run: %v", err)
	}
	return call, vm.Get("value")
}

// TestTheDataWriterWritesWhatGoWrites is the byte identity at the seam: an object the script
// builds is written as encoding/json writes the Go value it stands for -- a struct's members in
// order, a map's keys sorted (marked, or holding an index-like key that JavaScript would have moved
// to the front in numeric order), numbers as Go writes them, strings escaped as Go escapes them.
func TestTheDataWriterWritesWhatGoWrites(t *testing.T) {
	t.Parallel()
	type member struct {
		Zeta  int64    `json:"zeta"`
		Alpha *float64 `json:"alpha"`
	}
	half := 2.5
	// Computed at run time, as the script computes it: a constant expression is exact in Go.
	tenth, fifth := 0.1, 0.2
	goValue := struct {
		Struct  member             `json:"struct"`
		Indexed map[string]int64   `json:"indexed"`
		Marked  map[string]string  `json:"marked"`
		Numbers []float64          `json:"numbers"`
		Whole   []int64            `json:"whole"`
		Text    string             `json:"text"`
		Nested  map[string][]int64 `json:"nested"`
		Absent  *string            `json:"absent,omitempty"`
		Null    *string            `json:"null"`
	}{
		Struct:  member{Zeta: 3, Alpha: &half},
		Indexed: map[string]int64{"9": 1, "10": 2, "x": 3},
		Marked:  map[string]string{"beta": "b", "alpha": "a"},
		Numbers: []float64{tenth + fifth, 1e21, 1e-7, -0.5},
		Whole:   []int64{0, 7, 9007199254740991},
		Text:    "<a & b>  ",
		Nested:  map[string][]int64{"12": {1}, "3": {2}},
	}
	want, err := json.Marshal(goValue)
	if err != nil {
		t.Fatal(err)
	}
	call, value := scriptCallFor(t, `
		var value = {
			struct: { zeta: 3, alpha: 2.5 },
			indexed: { "x": 3, "10": 2, "9": 1 },
			marked: host.json.map({ beta: "b", alpha: "a" }),
			numbers: [0.1 + 0.2, 1e21, 1e-7, -0.5],
			whole: [0, 14 / 2, 9007199254740991],
			text: "<a & b>  ",
			nested: { "3": [2], "12": [1] },
			absent: undefined,
			null: null
		};`)
	got, err := call.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the script's data is written\n%s\nand Go writes\n%s", got, want)
	}
	text, err := call.VM.RunString(`host.json.encode(value)`)
	if err != nil || text.String() != string(want) {
		t.Errorf("host.json.encode answers %v (%v)", text, err)
	}
}

// TestTheDataWriterRefusesWhatIsNotData: what Go would refuse, or what JSON.stringify would drop
// silently, is refused by name.
func TestTheDataWriterRefusesWhatIsNotData(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"NaN":                 `var value = { x: NaN };`,
		"an infinity":         `var value = [1 / 0];`,
		"a function":          `var value = { f: function () {} };`,
		"a date":              `var value = { at: new Date(0) };`,
		"an undefined member": `var value = [1, undefined];`,
		"a cycle":             `var value = {}; value.self = value;`,
		"a symbol":            `var value = { s: Symbol("x") };`,
	} {
		call, value := scriptCallFor(t, source)
		if encoded, err := call.Encode(value); err == nil {
			t.Errorf("%s was written as %s", name, encoded)
		}
	}
}

// TestAScriptThatCannotServeIsRefusedAtLoad: a script that does not compile, throws at its top
// level or exports no widget.serve is refused when it loads, never at the first request.
func TestAScriptThatCannotServeIsRefusedAtLoad(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"a syntax error":   `exports.widget = { serve: function (host) { return {; } };`,
		"a thrown error":   `throw new Error("broken");`,
		"no serve":         `exports.widget = {};`,
		"serve not a fn":   `exports.widget = { serve: 1 };`,
		"no widget at all": `exports.helpers = {};`,
	} {
		if _, err := script.CompileWidgetScript("example.js", source); err == nil {
			t.Errorf("%s compiled", name)
		}
	}
	if _, err := script.CompileWidgetScript("example.js",
		`exports.widget = { serve: function (host) { return { data: null, has_rows: false }; } };`); err != nil {
		t.Errorf("a valid script was refused: %v", err)
	}
}

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
