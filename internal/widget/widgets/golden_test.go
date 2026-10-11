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
				check(t, json.Compact(&compactWant, goldenChanged(golden[key])))
				check(t, json.Compact(&compactGot, got))
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
