package infer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/decode"
)

// testNormaliser reads the fixtures' year-less timestamps against the instant they were captured.
var testNormaliser = decode.Normaliser{Reference: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}

// builtinCorrections reads the corrections the built-in manifests declare, by endpoint path.
func builtinCorrections(t *testing.T) map[string]Corrections {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join("..", "..", "..", "plugins", "*", "plugin.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no built-in manifest found (%v)", err)
	}
	corrections := map[string]Corrections{}
	for _, file := range manifests {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Endpoints []struct {
				Path        string       `json:"path"`
				Corrections *Corrections `json:"corrections"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, endpoint := range manifest.Endpoints {
			if endpoint.Corrections != nil {
				corrections[endpoint.Path] = *endpoint.Corrections
			}
		}
	}
	return corrections
}

// TestTheBuiltinAnswersAreLearnedAsTheSurveyEstablishes is BR-AC7's first part: on the recorded
// answer of every built-in endpoint that has one, after the built-in manifests' corrections, the
// nature, the row set, the key and the cursor equal the committed table, each row of which cites
// the survey section that establishes it.
func TestTheBuiltinAnswersAreLearnedAsTheSurveyEstablishes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "builtin_shapes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table []struct {
		Fixture string   `json:"fixture"`
		Path    string   `json:"path"`
		Nature  string   `json:"nature"`
		Rows    string   `json:"rows"`
		Key     []string `json:"key"`
		Cursor  []string `json:"cursor"`
		Survey  string   `json:"survey"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	survey, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "opnsense-api-survey.md"))
	if err != nil {
		t.Fatal(err)
	}
	corrections := builtinCorrections(t)
	for _, row := range table {
		if !strings.Contains(string(survey), row.Survey) {
			t.Errorf("%s cites %q, which is no section of the survey", row.Fixture, row.Survey)
		}
		fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", filepath.FromSlash(row.Fixture)))
		if err != nil {
			t.Fatal(err)
		}
		var labelled struct {
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(fixture, &labelled); err != nil {
			t.Fatal(err)
		}
		value, err := answer.Parse(labelled.Body)
		if err != nil {
			t.Fatalf("%s: %v", row.Fixture, err)
		}
		shape := Learn(value, corrections[row.Path], nil, testNormaliser)
		key, cursor := shape.Key, shape.Cursor
		if key == nil {
			key = []string{}
		}
		if cursor == nil {
			cursor = []string{}
		}
		if shape.Nature != row.Nature || shape.RowSet != row.Rows || !slices.Equal(key, row.Key) ||
			!slices.Equal(cursor, row.Cursor) {
			t.Errorf("%s is learned as %s, rows %q, key %v, cursor %v; the table says %s, rows %q, key %v, cursor %v",
				row.Fixture, shape.Nature, shape.RowSet, key, cursor, row.Nature, row.Rows, row.Key, row.Cursor)
		}
	}
}

// TestAFieldNullOnEveryRowIsNeverAKeyAndACorrectionOverridesInference is BR-AC7's second and third
// parts.
func TestAFieldNullOnEveryRowIsNeverAKeyAndACorrectionOverridesInference(t *testing.T) {
	t.Parallel()
	value, err := answer.Parse([]byte(`{"rows": [{"uuid": null, "name": "a", "n": 1}, {"uuid": null, "name": "b", "n": 2},
		{"uuid": null, "name": "c", "n": 3}]}`))
	if err != nil {
		t.Fatal(err)
	}
	shape := Learn(value, Corrections{}, nil, testNormaliser)
	if slices.Contains(shape.Key, "uuid") {
		t.Errorf("a field null on every row is the key: %v", shape.Key)
	}
	corrected := Learn(value, Corrections{Nature: NatureRecordStream, Key: []string{"n"}, Cursor: []string{"n"},
		Parameters: map[string]string{"x": RoleIgnored}}, nil, testNormaliser)
	if corrected.Nature != NatureRecordStream || !slices.Equal(corrected.Key, []string{"n"}) ||
		!slices.Equal(corrected.Cursor, []string{"n"}) {
		t.Errorf("the corrections were not applied: %+v", corrected)
	}
}

// TestAnObjectKeyedByValuesIsValueKeyed: an object keyed by interface names, addresses or numbers
// is rows keyed by the key, never one field per key.
func TestAnObjectKeyedByValuesIsValueKeyed(t *testing.T) {
	t.Parallel()
	for body, names := range map[string][]string{
		`{"192.0.2.1": {"a": 1}, "192.0.2.2": {"a": 2}}`:     nil,
		`{"igb0": "LAN", "vlan01": "Guests"}`:                {"igb0", "vlan01"},
		`{"one": {"x": 1, "y": 2}, "two": {"x": 3, "y": 4}}`: nil,
	} {
		value, err := answer.Parse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		vocabulary := func() []string { return names }
		Learn(value, Corrections{}, vocabulary, testNormaliser)
		if !value.ValueKeyed {
			t.Errorf("%s is not value-keyed", body)
		}
	}
	value, _ := answer.Parse([]byte(`{"total": 3, "rowCount": 3}`))
	Learn(value, Corrections{}, nil, testNormaliser)
	if value.ValueKeyed {
		t.Error("an object of field names is value-keyed")
	}
}
