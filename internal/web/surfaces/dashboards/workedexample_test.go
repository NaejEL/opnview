package dashboards_test

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// F7-AC39's test: the worked example of docs/dashboard-format.md is the first thing a reader copies,
// so every widget type it names is one GET /api/widgets lists, every parameter key one that type's
// manifest declares with a value it accepts, and every member of every custom_chart series one of
// the vocabulary of docs/widget-api.md, "Series of custom_chart". A key invented in the example
// would come back from the widget API as unknown or invalid on every installation that copied it.

// repositoryFile reads a file of the repository by its path from the root.
func repositoryFile(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// workedExampleJSON is the JSON block of the document's "Worked example".
func workedExampleJSON(t *testing.T) map[string]any {
	t.Helper()
	text := repositoryFile(t, "docs", "dashboard-format.md")
	start := strings.Index(text, "\n## Worked example\n")
	if start < 0 {
		t.Fatal(`docs/dashboard-format.md has no "Worked example" section`)
	}
	section := text[start:]
	open := strings.Index(section, "```json\n")
	if open < 0 {
		t.Fatal("the worked example has no JSON block")
	}
	body := section[open+len("```json\n"):]
	body = body[:strings.Index(body, "\n```")]
	var document map[string]any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatalf("the worked example's JSON does not parse: %v", err)
	}
	return document
}

// seriesVocabulary is docs/widget-api.md's "Series of custom_chart": the members a series takes,
// each closed vocabulary, and per source the metrics or measures it reads.
type seriesVocabulary struct {
	axes, renders, aggregations, referenceKeys map[string]bool
	metrics, measures                          map[string]map[string]bool
}

func backticked(text string) map[string]bool {
	found := map[string]bool{}
	for _, match := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(text, -1) {
		found[match[1]] = true
	}
	return found
}

func readSeriesVocabulary(t *testing.T) seriesVocabulary {
	t.Helper()
	text := repositoryFile(t, "docs", "widget-api.md")
	start := strings.Index(text, "\n## Series of `custom_chart`\n")
	if start < 0 {
		t.Fatal("docs/widget-api.md has no section on the series of custom_chart")
	}
	section := text[start+1:]
	section = section[:strings.Index(section[3:], "\n## ")+3]
	member := func(name string) map[string]bool {
		found := regexp.MustCompile("`" + name + "`\\s*\\(([^)]*)\\)").FindStringSubmatch(section)
		if found == nil {
			t.Fatalf("the series section does not list the values of %s", name)
		}
		return backticked(found[1])
	}
	vocabulary := seriesVocabulary{axes: member("axis"), renders: member("render"),
		aggregations: member("aggregation"), metrics: map[string]map[string]bool{},
		measures: map[string]map[string]bool{}}
	references := regexp.MustCompile("under the key\\s+of its kind: ([^.]*)\\.").FindStringSubmatch(section)
	if references == nil {
		t.Fatal("the series section does not list the reference keys")
	}
	vocabulary.referenceKeys = backticked(references[1])
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\| `(metric|measure)`: ([^|]*) \\|")
	for _, line := range strings.Split(section, "\n") {
		if found := row.FindStringSubmatch(line); found != nil {
			target := vocabulary.measures
			if found[2] == "metric" {
				target = vocabulary.metrics
			}
			target[found[1]] = backticked(found[3])
		}
	}
	if len(vocabulary.metrics)+len(vocabulary.measures) < 5 || len(vocabulary.referenceKeys) < 7 {
		t.Fatalf("the series vocabulary read is too small to check anything: %+v", vocabulary)
	}
	return vocabulary
}

// accepts says whether a manifest parameter takes a value as the widget API would, without
// replacing it by its default.
func accepts(parameter params.ManifestParameter, value any) bool {
	inValues := func(item any) bool {
		text, ok := item.(string)
		if !ok {
			return false
		}
		for _, allowed := range parameter.Values {
			if allowed == text {
				return true
			}
		}
		return false
	}
	switch parameter.Kind {
	case "enum":
		return inValues(value)
	case "enum_set":
		list, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if !inValues(item) {
				return false
			}
		}
		return true
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer", "number":
		number, ok := value.(float64)
		if !ok || (parameter.Kind == "integer" && number != math.Trunc(number)) {
			return false
		}
		return (parameter.Min == nil || number >= *parameter.Min) && (parameter.Max == nil || number <= *parameter.Max)
	case "reference_list":
		list, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			reference, ok := item.(map[string]any)
			if !ok || reference["kind"] != parameter.Reference {
				return false
			}
		}
		return true
	case "reference":
		reference, ok := value.(map[string]any)
		return ok && reference["kind"] == parameter.Reference
	case "series":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func TestTheWorkedExampleUsesOnlyTypesKeysAndSeriesVocabularyThisBuildServes(t *testing.T) {
	t.Parallel()
	document := workedExampleJSON(t)
	vocabulary := readSeriesVocabulary(t)

	harness := webtest.New(t)
	harness.CompleteSetup()
	answer := harness.Get("/api/widgets")
	status, body := webtest.StatusAndBody(t, answer)
	if status != http.StatusOK {
		t.Fatalf("GET /api/widgets answered %d", status)
	}
	var registry struct {
		Widgets []struct {
			Type       string                     `json:"type"`
			Parameters []params.ManifestParameter `json:"parameters"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(body), &registry); err != nil {
		t.Fatal(err)
	}
	declared := map[string]map[string]params.ManifestParameter{}
	for _, row := range registry.Widgets {
		declared[row.Type] = map[string]params.ManifestParameter{}
		for _, parameter := range row.Parameters {
			declared[row.Type][parameter.Key] = parameter
		}
	}

	checkedSeries := 0
	canvases, _ := document["canvases"].([]any)
	for _, rawCanvas := range canvases {
		canvas, _ := rawCanvas.(map[string]any)
		widgets, _ := canvas["widgets"].([]any)
		for _, rawWidget := range widgets {
			widget, _ := rawWidget.(map[string]any)
			widgetType, _ := widget["type"].(string)
			keys, served := declared[widgetType]
			if !served {
				t.Errorf("the worked example's widget %v names the type %q, which /api/widgets does not list",
					widget["id"], widgetType)
				continue
			}
			parameters, _ := widget["parameters"].(map[string]any)
			for key, value := range parameters {
				parameter, known := keys[key]
				switch {
				case !known:
					t.Errorf("%s: the key %q is not one its manifest declares", widgetType, key)
				case !accepts(parameter, value):
					t.Errorf("%s: %q = %v is not a value its manifest accepts", widgetType, key, value)
				}
			}
			series, _ := parameters["series"].([]any)
			for index, rawSeries := range series {
				checkedSeries++
				entry, _ := rawSeries.(map[string]any)
				source, _ := entry["source"].(string)
				for member, value := range entry {
					text, _ := value.(string)
					var ok bool
					switch member {
					case "label":
						ok = text != ""
					case "source":
						ok = vocabulary.metrics[source] != nil || vocabulary.measures[source] != nil
					case "metric":
						ok = vocabulary.metrics[source][text]
					case "measure":
						ok = vocabulary.measures[source][text]
					case "axis":
						ok = vocabulary.axes[text]
					case "render":
						ok = vocabulary.renders[text]
					case "aggregation":
						ok = vocabulary.aggregations[text]
					default:
						reference, isObject := value.(map[string]any)
						ok = vocabulary.referenceKeys[member] && isObject && reference["kind"] == member
					}
					if !ok {
						t.Errorf("%s: series[%d].%s = %v is not in docs/widget-api.md's series vocabulary",
							widgetType, index, member, value)
					}
				}
			}
		}
	}
	if checkedSeries == 0 {
		t.Error("the worked example carries no series, so its hardest value shape is not checked")
	}
}
