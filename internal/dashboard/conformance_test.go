package dashboard_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/dashboard"
)

// The conformance of the two structural validators (F7-AC10 of specs/SPEC-step-7-frontend.md): the
// Go one, authoritative, and the front's document module, run here in goja exactly as the browser
// runs it. Both are held to every case of testdata/dashboards/conformance, and to each other.

// repositoryRoot is the directory holding go.mod, found from this file's own location.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("the test cannot locate its own file")
	}
	return filepath.Join(filepath.Dir(here), "..", "..")
}

// conformanceCase is one case of cases.json.
type conformanceCase struct {
	Name       string          `json:"name"`
	File       string          `json:"file"`
	Text       *string         `json:"text"`
	PadToBytes int             `json:"pad_to_bytes"`
	Expect     json.RawMessage `json:"expect"`
}

// loadCases reads every case with its document's bytes.
func loadCases(t *testing.T) ([]conformanceCase, map[string][]byte) {
	t.Helper()
	directory := filepath.Join(repositoryRoot(t), "testdata", "dashboards", "conformance")
	raw, err := os.ReadFile(filepath.Join(directory, "cases.json"))
	if err != nil {
		t.Fatalf("reading the cases: %v", err)
	}
	var cases []conformanceCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("the cases are not JSON: %v", err)
	}
	documents := map[string][]byte{}
	for _, entry := range cases {
		var document []byte
		switch {
		case entry.File != "" && entry.Text == nil:
			document, err = os.ReadFile(filepath.Join(directory, "documents", entry.File))
			if err != nil {
				t.Fatalf("%s: %v", entry.Name, err)
			}
		case entry.File == "" && entry.Text != nil:
			document = []byte(*entry.Text)
		default:
			t.Fatalf("%s: a case names a file or carries a text, and exactly one of them", entry.Name)
		}
		if entry.PadToBytes > 0 {
			if len(document) > entry.PadToBytes {
				t.Fatalf("%s: the document is already longer than %d bytes", entry.Name, entry.PadToBytes)
			}
			document = append(document, bytes.Repeat([]byte(" "), entry.PadToBytes-len(document))...)
		}
		if _, taken := documents[entry.Name]; taken {
			t.Fatalf("the case name %q is used twice", entry.Name)
		}
		documents[entry.Name] = document
	}
	return cases, documents
}

// generic is a JSON value decoded for comparison; "null" is nil.
func generic(t *testing.T, raw []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("not JSON: %v: %s", err, raw)
	}
	return value
}

// documentModule is the front's document module loaded into a fresh runtime.
func documentModule(t *testing.T) *goja.Runtime {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(repositoryRoot(t), "front", "core", "document.js"))
	if err != nil {
		t.Fatalf("reading the document module: %v", err)
	}
	vm := goja.New()
	if _, err := vm.RunScript("front/core/document.js", string(source)); err != nil {
		t.Fatalf("the document module does not run as a classic script: %v", err)
	}
	return vm
}

// TestBothValidatorsAgreeOnEveryConformanceFixture is F7-AC10.
func TestBothValidatorsAgreeOnEveryConformanceFixture(t *testing.T) {
	t.Parallel()
	cases, documents := loadCases(t)
	vm := documentModule(t)
	validate, ok := goja.AssertFunction(vm.Get("opnview").ToObject(vm).Get("document").ToObject(vm).Get("validateText"))
	if !ok {
		t.Fatal("the document module exports no validateText")
	}
	stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	conditions := map[string]bool{}
	for _, entry := range cases {
		document := documents[entry.Name]
		expected := generic(t, entry.Expect)

		var fromGo any
		if refusal := dashboard.Validate(document); refusal != nil {
			encoded, err := json.Marshal(refusal)
			if err != nil {
				t.Fatal(err)
			}
			fromGo = generic(t, encoded)
			conditions[refusal.Condition] = true
		}

		answer, err := validate(goja.Undefined(), vm.ToValue(string(document)))
		if err != nil {
			t.Fatalf("%s: the document module threw: %v", entry.Name, err)
		}
		var fromJS any
		if !goja.IsNull(answer) {
			encoded, err := stringify(goja.Undefined(), answer)
			if err != nil {
				t.Fatal(err)
			}
			fromJS = generic(t, []byte(encoded.String()))
		}

		if !reflect.DeepEqual(fromGo, expected) {
			t.Errorf("%s: the Go validator answered %v, the fixture expects %v", entry.Name, fromGo, expected)
		}
		if !reflect.DeepEqual(fromJS, expected) {
			t.Errorf("%s: the document module answered %v, the fixture expects %v", entry.Name, fromJS, expected)
		}
	}
	// Every refusal code of A7 that a document can carry has a fixture of its own; unknown_dashboard
	// is a request's, not a document's, and is the surface's test.
	for _, code := range []string{dashboard.ConditionMalformedBody, dashboard.ConditionFormatVersionMissing,
		dashboard.ConditionFormatVersionTooNew, dashboard.ConditionFormatVersionTooOld,
		dashboard.ConditionCanvasesMissing, dashboard.ConditionCanvasesEmpty, dashboard.ConditionCanvasIDMissing,
		dashboard.ConditionCanvasIDMalformed, dashboard.ConditionCanvasTitleMissing, dashboard.ConditionWidgetIDMissing,
		dashboard.ConditionWidgetTypeAndShapeMissing, dashboard.ConditionPlacementMissing,
		dashboard.ConditionPlacementNotInteger, dashboard.ConditionDuplicateID, dashboard.ConditionNestingTooDeep,
		dashboard.ConditionDocumentTooLarge} {
		if !conditions[code] {
			t.Errorf("no conformance fixture produces %s", code)
		}
	}
}

// TestTheDocumentModuleCarriesTheGoFormatVersions holds the two validators' constants together.
func TestTheDocumentModuleCarriesTheGoFormatVersions(t *testing.T) {
	t.Parallel()
	vm := documentModule(t)
	module := vm.Get("opnview").ToObject(vm).Get("document").ToObject(vm)
	for name, want := range map[string]int64{"CURRENT_FORMAT_VERSION": dashboard.CurrentFormatVersion,
		"MINIMUM_FORMAT_VERSION": dashboard.MinimumFormatVersion, "MAX_DOCUMENT_BYTES": dashboard.MaxDocumentBytes} {
		if got := module.Get(name).ToInteger(); got != want {
			t.Errorf("the document module's %s is %d, the Go validator's %d", name, got, want)
		}
	}
}
