package widgets

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The byte-identity record of the built-in widgets (AC B5 of the backend-completion cycle).
//
// THE RESPONSES ARE PINNED BEFORE THE WIDGETS CHANGE HANDS. testdata/golden_responses.json.gz holds,
// for every built-in type, the exact response body at its defaults, at `24h` and `30d` where it
// is windowed, and at every parameter combination the widget tests exercise
// (testdata/golden_parameters.json), on the default fixture. It was written from the Go hooks
// before they were rewritten as widget plugins, and this test asserts the plugins answer every
// one of those requests byte for byte the same, except the fields a later change alters by name,
// which goldenChanged lists.
//
// To record the parameter combinations again, run the widget tests with
// OPNVIEW_RECORD_WIDGET_PARAMETERS set to a file path; to rewrite the responses, run this test with
// OPNVIEW_WRITE_GOLDEN=1. Neither happens in the suites.

var parameterRecorder sync.Mutex

// recordParameters appends one request a test made to the file the environment names.
func recordParameters(widgetType, raw string) {
	path := os.Getenv("OPNVIEW_RECORD_WIDGET_PARAMETERS")
	if path == "" {
		return
	}
	parameterRecorder.Lock()
	defer parameterRecorder.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	line, _ := json.Marshal([2]string{widgetType, raw})
	_, _ = file.Write(append(line, '\n'))
}

// goldenRequest is one recorded request.
type goldenRequest struct {
	Type       string `json:"type"`
	Parameters string `json:"parameters"`
}

// goldenRequests are the requests the record covers: every type at its defaults and periods, and
// the recorded combinations.
func goldenRequests(t *testing.T, f *fixture) []goldenRequest {
	t.Helper()
	seen := map[goldenRequest]bool{}
	var requests []goldenRequest
	add := func(request goldenRequest) {
		if !seen[request] {
			seen[request] = true
			requests = append(requests, request)
		}
	}
	for _, widgetType := range f.service.Types() {
		add(goldenRequest{Type: widgetType})
		preset, _ := f.service.Preset(widgetType)
		if preset.Windowed {
			add(goldenRequest{Type: widgetType, Parameters: `{"period":"24h"}`})
			add(goldenRequest{Type: widgetType, Parameters: `{"period":"30d"}`})
		}
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "golden_parameters.json"))
	check(t, err)
	var recorded []goldenRequest
	check(t, json.Unmarshal(raw, &recorded))
	for _, request := range recorded {
		if _, served := f.service.Preset(request.Type); served {
			add(request)
		}
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Type != requests[j].Type {
			return requests[i].Type < requests[j].Type
		}
		return requests[i].Parameters < requests[j].Parameters
	})
	return requests
}

// TestEveryBuiltinWidgetAnswersByteForByteAsBefore is AC B5. Each type is a subtest of its own, so
// the record of one widget is checked alone while it changes hands:
//
//	go test -run 'TestEveryBuiltinWidgetAnswersByteForByteAsBefore/^aggregate_freshness$' ./internal/widget/widgets/
func TestEveryBuiltinWidgetAnswersByteForByteAsBefore(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	if target := os.Getenv("OPNVIEW_RECORD_FRONT_ANSWERS"); target != "" {
		recordFrontAnswers(t, f, target)
	}
	path := filepath.Join("testdata", "golden_responses.json.gz")
	if os.Getenv("OPNVIEW_WRITE_GOLDEN") == "1" {
		record := map[string]json.RawMessage{}
		for _, request := range goldenRequests(t, f) {
			response, err := f.service.Respond(context.Background(), request.Type, request.Parameters)
			check(t, err)
			encoded, err := json.Marshal(response)
			check(t, err)
			record[request.Type+" "+request.Parameters] = encoded
		}
		encoded, err := json.Marshal(record)
		check(t, err)
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, err = writer.Write(encoded)
		check(t, err)
		check(t, writer.Close())
		check(t, os.WriteFile(path, compressed.Bytes(), 0o644))
		return
	}
	file, err := os.Open(path)
	check(t, err)
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	check(t, err)
	raw, err := io.ReadAll(reader)
	check(t, err)
	var golden map[string]json.RawMessage
	check(t, json.Unmarshal(raw, &golden))
	byType := map[string][]string{}
	for key := range golden {
		widgetType, _, _ := strings.Cut(key, " ")
		byType[widgetType] = append(byType[widgetType], key)
	}
	types := make([]string, 0, len(byType))
	for widgetType := range byType {
		types = append(types, widgetType)
	}
	sort.Strings(types)
	for _, widgetType := range types {
		keys := byType[widgetType]
		sort.Strings(keys)
		t.Run(widgetType, func(t *testing.T) {
			for _, key := range keys {
				if _, served := f.service.Preset(widgetType); !served {
					t.Errorf("%s: no longer answered", key)
					continue
				}
				response, err := f.service.Respond(context.Background(), widgetType, strings.TrimPrefix(key, widgetType+" "))
				check(t, err)
				got, err := json.Marshal(response)
				check(t, err)
				var compactWant, compactGot bytes.Buffer
				check(t, json.Compact(&compactWant, withoutPhaseCFields(widgetType, goldenChanged(golden[key]))))
				check(t, json.Compact(&compactGot, withoutPhaseCFields(widgetType, got)))
				if !bytes.Equal(compactWant.Bytes(), compactGot.Bytes()) && !sameUpToClientIdentityOrder(compactWant.Bytes(), compactGot.Bytes()) {
					t.Errorf("%s: the response changed\nbefore: %s\nafter:  %s", key, compactWant.String(),
						compactGot.String())
				}
			}
		})
	}
}

// goldenChanged applies to a recorded response the changes later cycles made by name: the two
// service kinds were renamed when they became plugin kinds (geo_asn is ip_geolocation and
// public_suffix is registrable_domain, B2 of the backend-completion cycle).
func goldenChanged(recorded json.RawMessage) []byte {
	changed := bytes.ReplaceAll(recorded, []byte(`"kind":"geo_asn"`), []byte(`"kind":"ip_geolocation"`))
	changed = bytes.ReplaceAll(changed, []byte(`"geo_asn"`), []byte(`"ip_geolocation"`))
	return bytes.ReplaceAll(changed, []byte(`"public_suffix"`), []byte(`"registrable_domain"`))
}

// sameUpToClientIdentityOrder reports whether two unresolved_joins responses differ only in the
// order of the records of the client_identity and the geo counters. Both lists were read with no
// ORDER BY, so on SQLite they came in the engine's scan order, which no reader may rely on; they
// are now ordered by client id and by address (backend-completion cycle, Phase P, the one named
// exception of P-AC6).
func sameUpToClientIdentityOrder(want, got []byte) bool {
	canonical := func(raw []byte) (string, bool) {
		var decoded map[string]any
		if json.Unmarshal(raw, &decoded) != nil || decoded["type"] != "unresolved_joins" {
			return "", false
		}
		data, _ := decoded["data"].(map[string]any)
		counters, _ := data["counters"].([]any)
		for _, entry := range counters {
			counter, _ := entry.(map[string]any)
			records, _ := counter["records"].([]any)
			switch counter["counter"] {
			case "client_identity":
				sort.SliceStable(records, func(i, j int) bool {
					left, _ := records[i].(map[string]any)
					right, _ := records[j].(map[string]any)
					a, _ := left["client_id"].(float64)
					b, _ := right["client_id"].(float64)
					return a < b
				})
			case "geo":
				sort.SliceStable(records, func(i, j int) bool {
					left, _ := records[i].(map[string]any)
					right, _ := records[j].(map[string]any)
					a, _ := left["address"].(string)
					b, _ := right["address"].(string)
					return a < b
				})
			}
		}
		encoded, err := json.Marshal(decoded)
		return string(encoded), err == nil
	}
	left, ok := canonical(want)
	right, ok2 := canonical(got)
	return ok && ok2 && left == right
}

// recordFrontAnswers writes the answers the front's renderers are drawn on in its tests
// (internal/front/frontjs/testdata/widget_answers.json.gz, A9 of specs/SPEC-step-7-frontend.md):
// the widget API's answer, as it is served today, to every request of the record above, on the
// default fixture, keyed "<type> <parameters>". Unlike the record, nothing is taken away from them:
// they are what a browser receives. It runs only when OPNVIEW_RECORD_FRONT_ANSWERS names the file,
// which is the generator's explicit invocation:
//
//	docker compose run --rm -e OPNVIEW_RECORD_FRONT_ANSWERS=/workspace/internal/front/frontjs/testdata/widget_answers.json.gz \
//	  dev go test -count=1 -run '^TestEveryBuiltinWidgetAnswersByteForByteAsBefore$' ./internal/widget/widgets/
func recordFrontAnswers(t *testing.T, f *fixture, target string) {
	t.Helper()
	record := map[string]json.RawMessage{}
	// The seed's answers ci/answerdrift recorded into the same file are kept: this fixture cannot
	// give them, and only that program writes them.
	if existing, err := os.Open(target); err == nil {
		reader, err := gzip.NewReader(existing)
		check(t, err)
		raw, err := io.ReadAll(reader)
		check(t, err)
		check(t, existing.Close())
		var previous map[string]json.RawMessage
		check(t, json.Unmarshal(raw, &previous))
		for key, body := range previous {
			if strings.Contains(key, " seed:") {
				record[key] = body
			}
		}
	}
	for _, request := range append(goldenRequests(t, f), defaultDashboardRequests(t, f)...) {
		if _, recorded := record[request.Type+" "+request.Parameters]; recorded {
			continue
		}
		response, err := f.service.Respond(context.Background(), request.Type, request.Parameters)
		check(t, err)
		encoded, err := json.Marshal(response)
		check(t, err)
		record[request.Type+" "+request.Parameters] = encoded
	}
	encoded, err := json.Marshal(record)
	check(t, err)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(encoded)
	check(t, err)
	check(t, writer.Close())
	check(t, os.WriteFile(target, compressed.Bytes(), 0o644))
}

// defaultDashboardRequests are the requests the front makes for the widgets of its default dashboard
// (front/dashboards/default.json), as its request builder makes them with no canvas scope: each
// widget's own parameters, and its canvas's period where the type takes one. Recorded beside the
// others, they are the answers the default canvases draw, so the renderers are tested on them and a
// review instance serves them.
func defaultDashboardRequests(t *testing.T, f *fixture) []goldenRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "front", "dashboards", "default.json"))
	check(t, err)
	var document struct {
		Canvases []struct {
			Period  string `json:"period"`
			Widgets []struct {
				Type       string         `json:"type"`
				Parameters map[string]any `json:"parameters"`
			} `json:"widgets"`
		} `json:"canvases"`
	}
	check(t, json.Unmarshal(raw, &document))
	var requests []goldenRequest
	for _, canvas := range document.Canvases {
		period := canvas.Period
		if period == "" {
			period = "24h"
		}
		for _, widget := range canvas.Widgets {
			preset, served := f.service.Preset(widget.Type)
			if !served {
				continue
			}
			parameters := map[string]any{}
			for key, value := range widget.Parameters {
				parameters[key] = value
			}
			if _, own := parameters["period"]; !own && preset.Windowed {
				parameters["period"] = period
			}
			encoded, err := json.Marshal(parameters)
			check(t, err)
			requests = append(requests, goldenRequest{Type: widget.Type, Parameters: string(encoded)})
		}
	}
	return requests
}
