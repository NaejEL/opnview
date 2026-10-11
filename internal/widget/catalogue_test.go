package widget

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// AC1, AC2 and the parameter table of AC6: the registry against the catalogue, the
// registry as a seam, and the API document against the code.

// catalogueEntry is one preset of docs/widget-catalogue.md: its type and the backticked
// tokens of its Parameters paragraph, parsed the way docs/mockups/check-form-covers-
// catalogue.js parses them.
type catalogueEntry struct {
	widgetType string
	tokens     map[string]bool
}

// parseCatalogue reads the catalogue's presets and its preset index.
func parseCatalogue(t *testing.T) (map[string]catalogueEntry, []string) {
	t.Helper()
	file, err := os.Open("../../docs/widget-catalogue.md")
	check(t, err)
	defer func() { _ = file.Close() }()
	entries := map[string]catalogueEntry{}
	var (
		current   *catalogueEntry
		buffer    strings.Builder
		inParams  bool
		index     []string
		inIndex   bool
		backticks = regexp.MustCompile("`[^`]+`")
		token     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	)
	flush := func() {
		if current != nil && buffer.Len() > 0 {
			for _, match := range backticks.FindAllString(buffer.String(), -1) {
				text := strings.TrimSpace(strings.Trim(match, "`"))
				if token.MatchString(text) {
					current.tokens[text] = true
				}
			}
		}
		buffer.Reset()
		inParams = false
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "## Preset index"):
			inIndex = true
			continue
		case inIndex && strings.HasPrefix(line, "## "):
			inIndex = false
		case inIndex && strings.HasPrefix(line, "| `"):
			index = append(index, strings.Trim(strings.Split(line, "|")[1], " `"))
		}
		if strings.HasPrefix(line, "### ") {
			flush()
			current = &catalogueEntry{tokens: map[string]bool{}}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "**Type**") {
			if match := regexp.MustCompile("`([a-z0-9_]+)`").FindStringSubmatch(line); match != nil {
				current.widgetType = match[1]
				entries[match[1]] = *current
			}
			continue
		}
		if strings.HasPrefix(line, "**Parameters**") {
			inParams = true
			buffer.WriteString(line)
			continue
		}
		if inParams {
			if strings.TrimSpace(line) == "" {
				flush()
				entries[current.widgetType] = *current
				continue
			}
			buffer.WriteString(" " + line)
		}
	}
	flush()
	check(t, scanner.Err())
	return entries, index
}

// stepSixTypes are the alert presets decision 1 leaves to step 6.
var stepSixTypes = []string{"security_alerts_over_time", "alerts_by_signature", "alerts_by_client_and_interface"}

// TestTheRegisteredTypesAreThePresetIndexLessTheAlertPresets is AC1: the served types are
// exactly the catalogue's preset index minus the three alert presets; every key a type
// declares is a backticked token of its catalogue Parameters paragraph; and every token
// there is either one of its keys or a value of one, so no catalogue key is missing.
func TestTheRegisteredTypesAreThePresetIndexLessTheAlertPresets(t *testing.T) {
	t.Parallel()
	entries, index := parseCatalogue(t)
	if len(index) != 29 {
		t.Fatalf("the preset index holds %d types, not the 29 it documents", len(index))
	}
	service, err := New(nil, Options{})
	if err == nil || service != nil {
		t.Fatal("a service with no store was built")
	}
	expected := map[string]bool{}
	for _, widgetType := range index {
		if !contains(stepSixTypes, widgetType) {
			expected[widgetType] = true
		}
	}
	served := map[string]bool{}
	for _, preset := range registry {
		if preset.Type == fakePreset.Type {
			continue
		}
		served[preset.Type] = true
	}
	if len(served) != 26 {
		t.Errorf("%d types are served, not 26", len(served))
	}
	for widgetType := range expected {
		if !served[widgetType] {
			t.Errorf("the catalogue's %s is not served", widgetType)
		}
	}
	for widgetType := range served {
		if !expected[widgetType] {
			t.Errorf("%s is served but is not in the preset index less the alert presets", widgetType)
		}
	}
	for _, preset := range registry {
		if preset.Type == fakePreset.Type {
			continue
		}
		entry, documented := entries[preset.Type]
		if !documented {
			t.Errorf("%s has no catalogue entry", preset.Type)
			continue
		}
		accounted := map[string]bool{}
		for _, param := range preset.Params {
			if !entry.tokens[param.Key] {
				t.Errorf("%s declares %q, which its catalogue Parameters paragraph does not name", preset.Type,
					param.Key)
			}
			accounted[param.Key] = true
			for _, value := range param.Values {
				accounted[value] = true
			}
			if param.Kind == KindSeries {
				for _, key := range seriesEntryKeys() {
					accounted[key] = true
				}
				for _, vocabulary := range [][]string{seriesSources, seriesAxes, seriesRenders, seriesAggregations,
					healthMetrics} {
					for _, value := range vocabulary {
						accounted[value] = true
					}
				}
				for _, measures := range seriesMeasures {
					for _, value := range measures {
						accounted[value] = true
					}
				}
			}
		}
		for token := range entry.tokens {
			if !accounted[token] {
				t.Errorf("%s: the catalogue token %q is neither a key the type accepts nor a value of one",
					preset.Type, token)
			}
		}
		// And every key is accepted without being reported unknown.
		parameters := map[string]json.RawMessage{}
		for _, param := range preset.Params {
			parameters[param.Key] = json.RawMessage("null")
		}
		encoded, _ := json.Marshal(parameters)
		parsed, err := parseParameters(string(encoded), preset.Params)
		check(t, err)
		if len(parsed.unknown) != 0 {
			t.Errorf("%s reported its own keys unknown: %v", preset.Type, parsed.unknown)
		}
	}
}

// TestAPresetAddedByAFileAndARowIsReachedOverHTTP is AC2: the fake preset of
// fake_test.go is a test-only file holding one Preset and one registry row, and nothing
// else changes; the service serves it, and an HTTP route built from Types() reaches it.
func TestAPresetAddedByAFileAndARowIsReachedOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 2, flows: 20, span: 3600})
	if !contains(f.service.Types(), fakePreset.Type) {
		t.Fatal("the registered fake type is not served")
	}
	mux := http.NewServeMux()
	for _, widgetType := range f.service.Types() {
		widgetType := widgetType
		mux.HandleFunc("GET "+PathPrefix+widgetType, func(writer http.ResponseWriter, request *http.Request) {
			f.service.ServeType(writer, request, widgetType)
		})
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	answer, err := http.Get(server.URL + PathPrefix + fakePreset.Type + "?" + query(t, map[string]any{"echo": "x"}))
	check(t, err)
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the fake type answered %d", answer.StatusCode)
	}
	var response Response
	check(t, json.NewDecoder(answer.Body).Decode(&response))
	if response.Type != fakePreset.Type || response.Parameters["echo"] != "x" {
		t.Errorf("the fake type answered %+v", response)
	}

}

// TestTheAPIDocumentsParameterTableMatchesTheCode is AC6's document half: the table of
// docs/widget-api.md lists every served type's every key, with its class -- data or
// rendering -- and its default, exactly as the code declares them.
func TestTheAPIDocumentsParameterTableMatchesTheCode(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../../docs/widget-api.md")
	check(t, err)
	documented := map[string]bool{}
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\| `([a-z_]+)` \\| (data|rendering) \\| (.*) \\|$")
	for _, line := range strings.Split(string(body), "\n") {
		if match := row.FindStringSubmatch(strings.TrimRight(line, "\r")); match != nil {
			documented[strings.Join(match[1:], " ")] = true
		}
	}
	var expected []string
	for _, preset := range registry {
		if preset.Type == fakePreset.Type {
			continue
		}
		for _, param := range preset.Params {
			class := "rendering"
			if param.Data {
				class = "data"
			}
			line := strings.Join([]string{preset.Type, param.Key, class, documentedDefault(param)}, " ")
			expected = append(expected, line)
			if !documented[line] {
				t.Errorf("docs/widget-api.md lacks the row: | `%s` | `%s` | %s | %s |", preset.Type, param.Key,
					class, documentedDefault(param))
			}
			delete(documented, line)
		}
	}
	for line := range documented {
		t.Errorf("docs/widget-api.md documents a row the code does not declare: %s", line)
	}
	sort.Strings(expected)
}

// documentedDefault renders a default the way the API document writes it.
func documentedDefault(param Param) string {
	if param.Required {
		return "required"
	}
	if param.Default == nil {
		return "none"
	}
	encoded, _ := json.Marshal(param.Default)
	return "`" + string(encoded) + "`"
}
