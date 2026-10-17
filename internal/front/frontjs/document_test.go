package frontjs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The document module's reading and writing (F7-36, F7-AC34 of specs/SPEC-step-7-frontend.md): the
// restricted YAML parser and both serialisers, the line of every path, the migration ladder and the
// coercion of scalars to the type a manifest declares. Run in the interpreter on the front's own
// front/core/document.js.
//
// FIXTURE PROVENANCE. The worked example is read from docs/dashboard-format.md and the round-trip
// documents from testdata/dashboards/conformance (synthesised; see its README). The short documents
// below are invented: slugs and labels that name nothing real.

// repositoryFile reads a file of the repository, from this file's own location.
func repositoryFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(append([]string{filepath.Dir(here), "..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// documentCall calls a function of opnview.document with JSON-encoded arguments and decodes its
// answer.
func documentCall(t *testing.T, page *frontjs.Runtime, script string, arguments ...any) map[string]any {
	t.Helper()
	encoded := make([]string, len(arguments))
	for index, argument := range arguments {
		raw, err := json.Marshal(argument)
		if err != nil {
			t.Fatal(err)
		}
		encoded[index] = string(raw)
	}
	text := page.Run(fmt.Sprintf(`JSON.stringify((%s).apply(null, [%s]))`, script, strings.Join(encoded, ","))).String()
	var answer map[string]any
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("%s answered %s: %v", script, text, err)
	}
	return answer
}

// fencedBlock is the first fenced block of a language in a Markdown text.
func fencedBlock(t *testing.T, markdown, language string) string {
	t.Helper()
	found := regexp.MustCompile("(?s)```" + language + "\n(.*?)```").FindStringSubmatch(markdown)
	if found == nil {
		t.Fatalf("no %s block", language)
	}
	return found[1]
}

func TestTheWorkedExampleReadsTheSameInYAMLAndInJSON(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	markdown := string(repositoryFile(t, "docs", "dashboard-format.md"))
	parse := `function (text, syntax) { return opnview.document.parse(text, syntax); }`
	fromJSON := documentCall(t, page, parse, fencedBlock(t, markdown, "json"), "json")
	fromYAML := documentCall(t, page, parse, fencedBlock(t, markdown, "yaml"), "yaml")
	if fromJSON["error"] != nil || fromYAML["error"] != nil {
		t.Fatalf("the worked example does not parse: JSON %v, YAML %v", fromJSON["error"], fromYAML["error"])
	}
	if !reflect.DeepEqual(fromJSON["document"], fromYAML["document"]) {
		t.Errorf("the YAML example reads as\n%v\nand the JSON example as\n%v", fromYAML["document"], fromJSON["document"])
	}
	// The format's note: `period: 7d` is the string 7d and `columns: 12` the integer 12. Compared as
	// JavaScript sees them, so a number decoded by Go cannot hide a string.
	types := page.Run(`(function (text) {
  var document = opnview.document.parse(text, "yaml").document;
  var canvas = document.canvases[0];
  return typeof canvas.widgets[0].parameters.period + ":" + canvas.widgets[0].parameters.period + " " +
    typeof canvas.grid.columns + ":" + canvas.grid.columns;
})(` + jsString(t, fencedBlock(t, markdown, "yaml")) + `)`).String()
	if types != "string:7d number:12" {
		t.Errorf("the YAML example's period and columns read as %q", types)
	}
	if fromYAML["comments"] != false {
		t.Errorf("the YAML example holds no comment and was read as holding one")
	}
}

// jsString is a Go string as a JavaScript literal.
func jsString(t *testing.T, text string) string {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEveryConformanceFixtureRoundTripsInBothSyntaxesWithItsLines(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	var cases []struct {
		Name string  `json:"name"`
		File string  `json:"file"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(repositoryFile(t, "testdata", "dashboards", "conformance", "cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, entry := range cases {
		if entry.Text != nil {
			texts[entry.Name] = *entry.Text
		} else {
			texts[entry.Name] = string(repositoryFile(t, "testdata", "dashboards", "conformance", "documents", entry.File))
		}
	}
	answer := page.Run(`(function (texts) {
  var d = opnview.document, failures = [], tried = 0;
  Object.keys(texts).sort().forEach(function (name) {
    var model;
    try { model = JSON.parse(texts[name]); } catch (e) { return; }
    tried += 1;
    var direct = d.parse(texts[name], "json");
    if (direct.error || JSON.stringify(direct.document) !== JSON.stringify(model)) {
      failures.push(name + ": the JSON reader and JSON.parse disagree"); return;
    }
    ["json", "yaml"].forEach(function (syntax) {
      var written = d.serialise(model, syntax);
      if (syntax === "json" && written.text !== JSON.stringify(model, null, 2)) {
        failures.push(name + ": the JSON text is not JSON.stringify's");
      }
      var read = d.parse(written.text, syntax);
      if (read.error) { failures.push(name + " (" + syntax + "): " + JSON.stringify(read.error)); return; }
      if (JSON.stringify(read.document) !== JSON.stringify(model)) {
        failures.push(name + " (" + syntax + "): does not round-trip"); return;
      }
      Object.keys(read.paths).forEach(function (path) {
        if (d.lineOf(written.lines, path) !== read.paths[path]) {
          failures.push(name + " (" + syntax + "): " + path + " is on line " + read.paths[path] +
            " and the serialiser says " + d.lineOf(written.lines, path));
        }
      });
    });
  });
  return JSON.stringify({ failures: failures, tried: tried });
})(` + jsonOf(t, texts) + `)`).String()
	var result struct {
		Failures []string `json:"failures"`
		Tried    int      `json:"tried"`
	}
	if err := json.Unmarshal([]byte(answer), &result); err != nil {
		t.Fatal(err)
	}
	if result.Tried < 10 {
		t.Errorf("only %d conformance documents were round-tripped", result.Tried)
	}
	for _, failure := range result.Failures {
		t.Error(failure)
	}
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(buffer.String())
}

func TestTheYAMLSubsetRefusesWhatItCannotHoldNamingTheLine(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	head := "format_version: 1\ncanvases:\n  - id: main\n    title: Main\n"
	for _, entry := range []struct {
		name, text, condition string
		line                  float64
	}{
		{"an anchor", head + "    grid: &layout\n      columns: 12\n", "yaml_anchor", 5},
		{"an alias", head + "    grid: *layout\n", "yaml_alias", 5},
		{"a tag", head + "    title_again: !!str Main\n", "yaml_tag", 5},
		{"a second document", head + "---\nformat_version: 1\n", "yaml_multiple_documents", 5},
		{"an end-of-document marker", head + "...\n", "yaml_multiple_documents", 5},
		{"a complex key", head + "    ? grid\n    : columns\n", "yaml_non_string_key", 5},
		{"a flow key", head + "    [a, b]: c\n", "yaml_non_string_key", 5},
		{"a number as a key", head + "    12: twelve\n", "yaml_non_string_key", 5},
		{"a boolean as a key", head + "    true: yes\n", "yaml_non_string_key", 5},
		{"a tab in the indentation", head + "\t\tgrid: {}\n", "yaml_tab", 5},
		{"a duplicate key", head + "    title: Again\n", "yaml_duplicate_key", 5},
		{"an unterminated string", head + "    description: \"open\n", "yaml_unterminated_string", 5},
		{"a block scalar", head + "    description: |\n      text\n", "yaml_syntax", 5},
		{"a stray indentation", head + "      widgets: []\n", "yaml_indentation", 5},
		{"an empty document", "# only a comment\n\n", "document_empty", 0},
	} {
		answer := documentCall(t, page, `function (text) { return opnview.document.parse(text, "yaml"); }`, entry.text)
		refused, _ := answer["error"].(map[string]any)
		if refused == nil {
			t.Errorf("%s: read as %v", entry.name, answer["document"])
			continue
		}
		line, _ := refused["line"].(float64)
		if refused["condition"] != entry.condition || line != entry.line {
			t.Errorf("%s: refused %v on line %v, want %s on line %v", entry.name, refused["condition"], refused["line"],
				entry.condition, entry.line)
		}
	}
	// The conditions are a closed list the editor translates.
	listed := page.Run(`JSON.stringify(opnview.document.PARSE_CONDITIONS)`).String()
	for _, condition := range []string{"yaml_anchor", "yaml_alias", "yaml_tag", "yaml_multiple_documents",
		"yaml_non_string_key", "value_not_coercible", "json_syntax", "document_empty"} {
		if !strings.Contains(listed, `"`+condition+`"`) {
			t.Errorf("PARSE_CONDITIONS lacks %s", condition)
		}
	}
	// What the subset holds: comments are reported, a colon inside a value is the value's.
	answer := documentCall(t, page, `function (text) { return opnview.document.parse(text); }`,
		"# a dashboard\nformat_version: 1 # the format\ngenerated_by: opnview 0.1\ndescription: see https://example.org/a#b\n"+
			"canvases:\n- id: main\n  title: 'It''s main'\n  widgets: [ ]\n")
	if answer["error"] != nil || answer["comments"] != true {
		t.Fatalf("a commented document: %v", answer)
	}
	document := answer["document"].(map[string]any)
	if document["generated_by"] != "opnview 0.1" || document["description"] != "see https://example.org/a#b" ||
		document["canvases"].([]any)[0].(map[string]any)["title"] != "It's main" {
		t.Errorf("the commented document reads as %v", document)
	}
}

func TestScalarsAreCoercedToTheTypeTheManifestDeclares(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	rows := `function (type) {
  return type === "ranking" ? { parameters: [{ key: "period", kind: "enum" }, { key: "limit", kind: "integer" },
    { key: "include_unlabelled", kind: "boolean" }, { key: "min_share", kind: "number" },
    { key: "series", kind: "enum_set" }] } : null;
}`
	load := `function (text) { return opnview.document.load(text, ` + rows + `); }`
	yaml := "format_version: 1\ncanvases:\n  - id: main\n    title: Main\n    grid: { columns: \"12\" }\n    widgets:\n" +
		"      - id: ranked\n        type: ranking\n        placement: { x: 0, y: 0, w: 6, h: 4 }\n        parameters:\n" +
		"          period: 7d\n          limit: \"10\"\n          include_unlabelled: \"true\"\n          min_share: \"0.5\"\n" +
		"          series: [allowed, 7]\n          kept_key: \"10\"\n      - id: other\n        type: unknown_here\n" +
		"        placement: { x: 6, y: 0, w: 6, h: 4 }\n        parameters: { limit: \"10\" }\n"
	answer := documentCall(t, page, load, yaml)
	if answer["error"] != nil {
		t.Fatalf("refused: %v", answer["error"])
	}
	canvas := answer["document"].(map[string]any)["canvases"].([]any)[0].(map[string]any)
	widgets := canvas["widgets"].([]any)
	got := widgets[0].(map[string]any)["parameters"]
	want := map[string]any{"period": "7d", "limit": float64(10), "include_unlabelled": true, "min_share": 0.5,
		"series": []any{"allowed", "7"}, "kept_key": "10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the parameters are coerced to %v, want %v", got, want)
	}
	if canvas["grid"].(map[string]any)["columns"] != float64(12) {
		t.Errorf("the grid's columns are %v", canvas["grid"])
	}
	if !reflect.DeepEqual(widgets[1].(map[string]any)["parameters"], map[string]any{"limit": "10"}) {
		t.Errorf("an unknown type's parameters were touched: %v", widgets[1])
	}
	// A value that cannot be coerced is refused, naming its line, in both syntaxes.
	refusedYAML := documentCall(t, page, load, strings.Replace(yaml, `limit: "10"`, "limit: ten", 1))["error"]
	wantYAML := map[string]any{"condition": "value_not_coercible", "line": float64(12), "canvas_index": float64(0),
		"widget_index": float64(0), "field": "limit", "path": "canvases[0].widgets[0].parameters.limit", "value": "ten"}
	if !reflect.DeepEqual(refusedYAML, wantYAML) {
		t.Errorf("an uncoercible YAML value: %v, want %v", refusedYAML, wantYAML)
	}
	refusedJSON := documentCall(t, page, load, "{\n  \"format_version\": 1,\n  \"canvases\": [{\"id\": \"main\", \"title\": \"Main\",\n"+
		"    \"widgets\": [{\"id\": \"ranked\", \"type\": \"ranking\",\n      \"placement\": {\"x\": 0, \"y\": 0, \"w\": 6, \"h\": 4},\n"+
		"      \"parameters\": {\n        \"limit\": 1.5}}]}]\n}")["error"].(map[string]any)
	if refusedJSON["condition"] != "value_not_coercible" || refusedJSON["line"] != float64(7) {
		t.Errorf("an uncoercible JSON value: %v", refusedJSON)
	}
}

func TestTheLadderAndTheStructureRefuseNamingTheirLine(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	migrated := documentCall(t, page, `function (document) { return opnview.document.migrate(document); }`,
		map[string]any{"format_version": 2, "canvases": []any{}})
	wantNewer := map[string]any{"condition": "format_version_too_new", "canvas_index": nil, "widget_index": nil,
		"field": "format_version", "path": "format_version", "file_version": float64(2), "current": float64(1),
		"minimum": float64(1)}
	if !reflect.DeepEqual(migrated["refusal"], wantNewer) {
		t.Errorf("a version 2 document: %v", migrated)
	}
	current := documentCall(t, page, `function (document) { return opnview.document.migrate(document); }`,
		map[string]any{"format_version": 1, "canvases": []any{}})
	if current["from"] != float64(1) || current["to"] != float64(1) || current["refusal"] != nil {
		t.Errorf("a version 1 document: %v", current)
	}
	load := `function (text) { return opnview.document.load(text, null); }`
	older := documentCall(t, page, load, "format_version: 0\ncanvases: []\n")["error"].(map[string]any)
	if older["condition"] != "format_version_too_old" || older["file_version"] != float64(0) || older["line"] != float64(1) {
		t.Errorf("a version 0 document: %v", older)
	}
	// The widget lacking its placement starts on line 6 of the YAML and line 4 of the JSON.
	lines := map[string]float64{"yaml": 6, "json": 4}
	for syntax, text := range map[string]string{
		"yaml": "format_version: 1\ncanvases:\n  - id: main\n    title: Main\n    widgets:\n      - id: lonely\n        type: top_sites\n",
		"json": "{\"format_version\": 1,\n \"canvases\": [\n  {\"id\": \"main\", \"title\": \"Main\", \"widgets\": [\n   {\"id\": \"lonely\",\n    \"type\": \"top_sites\"}]}]}",
	} {
		refused := documentCall(t, page, load, text)["error"].(map[string]any)
		if refused["condition"] != "placement_missing" || refused["line"] != lines[syntax] || refused["widget_index"] != float64(0) ||
			refused["field"] != "placement" {
			t.Errorf("%s: a widget with no placement: %v", syntax, refused)
		}
	}
	tooLarge := documentCall(t, page, load, "format_version: 1\n"+strings.Repeat("#", 1048577))["error"].(map[string]any)
	if tooLarge["condition"] != "document_too_large" {
		t.Errorf("a document above the bound: %v", tooLarge)
	}
	broken := documentCall(t, page, load, "{\n  \"format_version\": 1,\n  \"canvases\": [,]\n}")["error"].(map[string]any)
	if broken["condition"] != "json_syntax" || broken["line"] != float64(3) {
		t.Errorf("a JSON syntax error: %v", broken)
	}
	if page.Run(`opnview.document.hasComments("a: 1 # note") && !opnview.document.hasComments("a: \"1 # not\"") && `+
		`!opnview.document.hasComments("{\"a\": \"# no\"}")`).ToBoolean() != true {
		t.Error("hasComments does not tell a comment from a hash inside a value")
	}
}

// TestAMarkedOrDeeplyNestedFileIsReadOrRefusedByName (F7-36): a byte-order mark an editor wrote
// first is not part of the document, in either syntax; a text nested past the parsers' bound is
// refused by name, with its line where it has one, in both syntaxes and in a flow collection; and
// the import path answers a refusal for it, never a thrown error.
func TestAMarkedOrDeeplyNestedFileIsReadOrRefusedByName(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	parse := `function (text, syntax) { return opnview.document.parse(text, syntax); }`
	marked := map[string]string{
		"json": "\uFEFF" + `{"format_version": 1, "canvases": [{"id": "a", "title": "A", "widgets": []}]}`,
		"yaml": "\uFEFF" + "format_version: 1\ncanvases:\n  - id: a\n    title: A\n    widgets: []\n",
	}
	for syntax, text := range marked {
		for _, given := range []any{syntax, nil} {
			answer := documentCall(t, page, parse, text, given)
			document, _ := answer["document"].(map[string]any)
			if answer["error"] != nil || document == nil || document["format_version"] != float64(1) {
				t.Errorf("%s with a byte-order mark, syntax %v, reads as %v", syntax, given, answer)
			}
		}
		loaded := documentCall(t, page, `function (text) { return opnview.editing.read(text); }`, text)
		if loaded["error"] != nil {
			t.Errorf("%s with a byte-order mark is refused on import: %v", syntax, loaded["error"])
		}
	}

	depth := 2000
	deepJSON := strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)
	var deepYAML strings.Builder
	for level := 0; level < depth; level++ {
		deepYAML.WriteString(strings.Repeat(" ", level) + "a:\n")
	}
	deepYAML.WriteString(strings.Repeat(" ", depth) + "b: 1\n")
	deepFlow := "format_version: 1\nx: " + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "\n"
	bound := int(page.Run(`opnview.document.MAX_PARSE_DEPTH`).ToInteger())
	for _, deep := range []struct {
		name, text, syntax string
		line               float64
	}{
		{"JSON", deepJSON, "json", 1},
		{"block YAML", deepYAML.String(), "yaml", float64(bound + 1)},
		{"flow YAML", deepFlow, "yaml", 2},
	} {
		answer := documentCall(t, page, parse, deep.text, deep.syntax)
		refused, _ := answer["error"].(map[string]any)
		if refused == nil || refused["condition"] != "document_too_deep" || refused["line"] != deep.line {
			t.Errorf("%s nested %d deep reads as %v, not document_too_deep on line %v", deep.name, depth, answer["error"],
				deep.line)
		}
		loaded := documentCall(t, page, `function (text) { return opnview.editing.read(text); }`, deep.text)
		if refusal, _ := loaded["error"].(map[string]any); refusal == nil || refusal["condition"] != "document_too_deep" {
			t.Errorf("the import of %s nested %d deep answers %v", deep.name, depth, loaded)
		}
	}
	// Nested to the bound and no deeper, a text is read.
	shallow := strings.Repeat(`{"a":`, bound) + "1" + strings.Repeat("}", bound)
	if answer := documentCall(t, page, parse, shallow, "json"); answer["error"] != nil {
		t.Errorf("JSON nested to the bound, %d, is refused: %v", bound, answer["error"])
	}
}
