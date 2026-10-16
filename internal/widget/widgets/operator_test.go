package widgets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The modularity proof of the widget seam (b): a brand-new widget is created ONLY by writing
// files -- a manifest and a query -- into the operator widgets directory, and is then served
// over real HTTP with the semantics of its shape. Nothing in the repository is edited, no Go is
// compiled, no list is touched.
//
// The widget most of these tests write is the worked example of docs/extending.md: the test
// reads the example out of the document, so the documentation cannot drift from what is tested.

// docExample returns the fenced block that follows the marker `<!-- example: NAME -->` in a
// document.
func docExample(t *testing.T, document, name string) string {
	t.Helper()
	raw, err := os.ReadFile(document)
	check(t, err)
	marker := "<!-- example: " + name + " -->"
	text := string(raw)
	at := strings.Index(text, marker)
	if at < 0 {
		t.Fatalf("%s carries no %s marker", document, marker)
	}
	block := regexp.MustCompile("(?s)```[a-z]*\n(.*?)```").FindStringSubmatch(text[at+len(marker):])
	if block == nil {
		t.Fatalf("%s: no fenced block after %s", document, marker)
	}
	return block[1]
}

// extendingDoc is the document whose worked example is tested.
const extendingDoc = "../../../docs/extending.md"

// writeWidget writes one operator widget directory: a manifest and a query.
func writeWidget(t *testing.T, directory, name, manifest, query string) {
	t.Helper()
	folder := filepath.Join(directory, name)
	check(t, os.MkdirAll(folder, 0o755))
	check(t, os.WriteFile(filepath.Join(folder, "manifest.json"), []byte(manifest), 0o644))
	if query != "" {
		check(t, os.WriteFile(filepath.Join(folder, "query.sql"), []byte(query), 0o644))
	}
}

// operatorService builds a second service over the fixture's database, reading the operator
// widgets of a directory.
func (f *fixture) operatorService(t *testing.T, directory string) *widget.Service {
	t.Helper()
	service, err := widget.New(f.db, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) },
		SuffixLists: exampleSuffixList(t), OperatorDir: directory})
	check(t, err)
	return service
}

// serve answers one request over a real HTTP server whose routes are built from the service's
// types, and decodes the envelope.
func serve(t *testing.T, service *widget.Service, widgetType string, parameters any) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	for _, served := range service.Types() {
		served := served
		mux.HandleFunc("GET "+widget.PathPrefix+served, func(writer http.ResponseWriter, request *http.Request) {
			service.ServeType(writer, request, served)
		})
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	target := server.URL + widget.PathPrefix + widgetType
	if parameters != nil {
		target += "?" + query(t, parameters)
	}
	answer, err := http.Get(target)
	check(t, err)
	defer func() { _ = answer.Body.Close() }()
	var decoded map[string]any
	check(t, json.NewDecoder(answer.Body).Decode(&decoded))
	return answer.StatusCode, decoded
}

// object reads a member of a decoded document as an object.
func object(t *testing.T, document map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := document[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is %v, not an object, in %v", key, document[key], document)
	}
	return value
}

// list reads a member of a decoded document as an array.
func list(t *testing.T, document map[string]any, key string) []any {
	t.Helper()
	value, ok := document[key].([]any)
	if !ok {
		t.Fatalf("%q is %v, not an array, in %v", key, document[key], document)
	}
	return value
}

// figureValue reads the `value` of a measured figure.
func figureValue(t *testing.T, figure any) float64 {
	t.Helper()
	measured, ok := figure.(map[string]any)
	if !ok {
		t.Fatalf("%v is not a measured figure", figure)
	}
	value, ok := measured["value"].(float64)
	if !ok {
		t.Fatalf("%v carries no value", measured)
	}
	return value
}

// exampleWidgetType is the type of the documentation's worked example.
const exampleWidgetType = "example_bytes_by_protocol"

// writeDocumentedWidget writes the worked example of the documentation into a directory.
func writeDocumentedWidget(t *testing.T, directory string) {
	t.Helper()
	writeWidget(t, directory, exampleWidgetType,
		docExample(t, extendingDoc, "manifest.json"), docExample(t, extendingDoc, "query.sql"))
}

// TestABrandNewWidgetCreatedOnlyByWritingFilesIsServedOverHTTP is the proof (b): the worked
// example of docs/extending.md, written as two files into an empty operator directory, is
// loaded, routed, served over HTTP and answers with the semantics of its shape -- and the
// built-in widgets beside it are unaffected.
func TestABrandNewWidgetCreatedOnlyByWritingFilesIsServedOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	directory := t.TempDir()
	writeDocumentedWidget(t, directory)

	service := f.operatorService(t, directory)
	if refusals := service.Refusals(); len(refusals) != 0 {
		t.Fatalf("the documented widget was refused: %v", refusals)
	}
	if got := service.OperatorTypes(); len(got) != 1 || got[0] != exampleWidgetType {
		t.Fatalf("the operator widgets loaded are %v", got)
	}
	if !params.Contains(service.Types(), exampleWidgetType) {
		t.Fatalf("the new type is not routed: %v", service.Types())
	}
	if len(service.Types()) != len(f.service.Types())+1 {
		t.Errorf("the new widget changed the built-in set: %d types against %d", len(service.Types()),
			len(f.service.Types()))
	}

	status, answer := serve(t, service, exampleWidgetType, map[string]any{"limit": 1})
	if status != http.StatusOK {
		t.Fatalf("the new widget answered %d", status)
	}
	if answer["type"] != exampleWidgetType || answer["condition"] != envelope.ConditionReachable {
		t.Errorf("the envelope is %v", answer)
	}
	if answer["window"] == nil || len(list(t, answer, "availability")) == 0 {
		t.Errorf("the envelope carries no window or availability: %v", answer)
	}

	// The shape's semantics: the bars plus the remainder are the total, and the total is the
	// bytes the database holds in the window.
	data := object(t, answer, "data")
	bars := list(t, data, "bars")
	if len(bars) != 1 {
		t.Fatalf("limit 1 gave %d bars", len(bars))
	}
	remainder := object(t, data, "remainder")
	total := figureValue(t, data["total"])
	shown := figureValue(t, bars[0].(map[string]any)["value"])
	if shown+figureValue(t, remainder["value"]) != total {
		t.Errorf("the bar %v plus the remainder %v is not the total %v", shown, remainder["value"], total)
	}
	if int(remainder["members"].(float64)) < 1 {
		t.Errorf("a limit of 1 over several protocols dropped no member: %v", remainder)
	}
	want := f.scalar(t, `SELECT sum(packet_bytes) FROM flow WHERE observed_at >= ? AND observed_at < ?`,
		f.now-86400, f.now)
	if int64(total) != want {
		t.Errorf("the total is %v and the database holds %d bytes in the window", total, want)
	}
	measured := bars[0].(map[string]any)["value"].(map[string]any)
	if measured["lower_bound"] != true || measured["measure"] != "logged" {
		t.Errorf("a byte figure must say it is a lower bound of its measure: %v", measured)
	}

	// A built-in is untouched, over the same routes.
	status, builtin := serve(t, service, "source_availability", nil)
	if status != http.StatusOK || builtin["type"] != "source_availability" {
		t.Errorf("a built-in widget answered %d %v", status, builtin["type"])
	}
}

// TestADeclarativeWidgetObeysItsParametersAndTheEnvelope: the parameters are applied as the
// manifest declares them, an unknown key is reported and ignored, and an invalid value takes
// its default -- the same envelope rules as every built-in.
func TestADeclarativeWidgetObeysItsParametersAndTheEnvelope(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	directory := t.TempDir()
	writeDocumentedWidget(t, directory)
	service := f.operatorService(t, directory)

	_, blocked := serve(t, service, exampleWidgetType, map[string]any{"action": "block", "limit": 50})
	want := f.scalar(t, `SELECT coalesce(sum(packet_bytes), 0) FROM flow WHERE observed_at >= ? AND observed_at < ?
		AND action = 'block'`, f.now-86400, f.now)
	if got := int64(figureValue(t, object(t, blocked, "data")["total"])); got != want {
		t.Errorf("action block totals %d bytes and the database holds %d", got, want)
	}

	_, odd := serve(t, service, exampleWidgetType, map[string]any{"action": "nonsense", "bogus": 1, "limit": 0})
	if len(list(t, odd, "invalid_parameters")) != 2 {
		t.Errorf("an out-of-vocabulary action and an out-of-range limit are both reported: %v", odd["invalid_parameters"])
	}
	if _, reported := object(t, odd, "unknown_parameters")["bogus"]; !reported {
		t.Errorf("an unknown key is reported: %v", odd["unknown_parameters"])
	}
	effective := object(t, odd, "parameters")
	if effective["action"] != "any" || effective["limit"] != float64(5) {
		t.Errorf("invalid values take their declared defaults, not %v", effective)
	}

	status, malformed := serve(t, service, exampleWidgetType, "[1,2]")
	if status != http.StatusBadRequest || malformed["condition"] != envelope.ConditionMalformedParameters {
		t.Errorf("a non-object parameters payload answered %d %v", status, malformed)
	}
}

// TestADeclarativeWidgetTakesItsConditionFromItsProviderAndNeverInventsAZero: with the bound
// provider unavailable, the widget says so and an empty answer is no data at all, not a zero;
// with it healthy, an empty answer is the measured zero of reachable_no_rows.
func TestADeclarativeWidgetTakesItsConditionFromItsProviderAndNeverInventsAZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	directory := t.TempDir()
	empty := strings.Replace(docExample(t, extendingDoc, "query.sql"), "WHERE ", "WHERE 1 = 0 AND ", 1)
	writeWidget(t, directory, exampleWidgetType, docExample(t, extendingDoc, "manifest.json"), empty)
	service := f.operatorService(t, directory)

	_, healthy := serve(t, service, exampleWidgetType, nil)
	if healthy["condition"] != envelope.ConditionReachableNoRows {
		t.Fatalf("a healthy source with no rows is %v", healthy["condition"])
	}
	if got := figureValue(t, object(t, healthy, "data")["total"]); got != 0 {
		t.Errorf("a measured empty window totals %v", got)
	}

	f.setAvailability(t, "firewall_log", "pf", vocab.StateUnavailable, vocab.OutcomeNotFound, "example probe")
	_, unavailable := serve(t, service, exampleWidgetType, nil)
	if unavailable["condition"] != envelope.ConditionUnavailable {
		t.Errorf("an unavailable provider gave the condition %v", unavailable["condition"])
	}
	if unavailable["data"] != nil {
		t.Errorf("an unavailable source with nothing stored must have no data, not %v", unavailable["data"])
	}

	f.setAvailability(t, "firewall_log", "pf", vocab.StatePresentButDisabled, vocab.OutcomePresentButDisabled, "example probe")
	_, disabled := serve(t, service, exampleWidgetType, nil)
	if disabled["condition"] != envelope.ConditionPresentButDisabled || disabled["data"] != nil {
		t.Errorf("a disabled source gave %v with data %v", disabled["condition"], disabled["data"])
	}
}

// TestADeclarativeWidgetThatCarriesDomainNamesIsWithheldWholeUnderNoDomains: a manifest that
// declares no_domains "whole" is withheld by the aggregate mode exactly as the built-in
// site widgets are -- no data, the condition says why, and the query never runs.
func TestADeclarativeWidgetThatCarriesDomainNamesIsWithheldWholeUnderNoDomains(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	directory := t.TempDir()
	manifest := strings.Replace(docExample(t, extendingDoc, "manifest.json"), `"no_domains": "none"`,
		`"no_domains": "whole"`, 1)
	if manifest == docExample(t, extendingDoc, "manifest.json") {
		t.Fatal("the documented manifest does not declare no_domains, so this test would prove nothing")
	}
	writeWidget(t, directory, exampleWidgetType, manifest, docExample(t, extendingDoc, "query.sql"))
	service := f.operatorService(t, directory)

	_, full := serve(t, service, exampleWidgetType, nil)
	if full["condition"] != envelope.ConditionReachable {
		t.Fatalf("in full mode the widget is %v", full["condition"])
	}
	check(t, f.db.SetSetting(context.Background(), config.KeyAggregateMode, request.AggregateModeNoDomains, f.now))
	_, withheld := serve(t, service, exampleWidgetType, nil)
	if withheld["condition"] != envelope.ConditionWithheldByAggregateMode || withheld["data"] != nil {
		t.Errorf("under no_domains the widget gave %v with data %v", withheld["condition"], withheld["data"])
	}
	if got := list(t, withheld, "withheld"); len(got) != 1 || got[0] != "data" {
		t.Errorf("the withheld list is %v", got)
	}
}

// TestRankingTiesAreBrokenByKeyNeverByLabel: two members with one value are ordered by their
// key. The label is never consulted, so an order never leaks a name the aggregate mode would
// withhold.
func TestRankingTiesAreBrokenByKeyNeverByLabel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	directory := t.TempDir()
	writeWidget(t, directory, "example_ranked", shapeManifest("example_ranked", "ranked_bars", false,
		`{"key":"limit","kind":"integer","min":1,"default":2,"class":"data"}`),
		`SELECT column1 AS key, column2 AS label, column3 AS value
		 FROM (VALUES ('b', 'Alpha', 5), ('a', 'Zulu', 5), ('c', 'Mid', 7), ('d', 'Last', 1), ('e', NULL, NULL))`)
	service := f.operatorService(t, directory)
	if len(service.Refusals()) != 0 {
		t.Fatal(service.Refusals())
	}
	_, answer := serve(t, service, "example_ranked", nil)
	data := object(t, answer, "data")
	var keys []string
	for _, bar := range list(t, data, "bars") {
		keys = append(keys, bar.(map[string]any)["key"].(string))
	}
	if strings.Join(keys, ",") != "c,a" {
		t.Errorf("the two bars are %v: value first, then key (a before b) and not label (Alpha before Zulu)", keys)
	}
	remainder := object(t, data, "remainder")
	if remainder["members"] != float64(2) || figureValue(t, remainder["value"]) != 6 || figureValue(t, data["total"]) != 18 {
		t.Errorf("remainder %v total %v: the shown 7+5, the dropped 5+1 and the total 18 must reconcile",
			remainder, data["total"])
	}
	if data["unmeasured"] != float64(1) {
		t.Errorf("the NULL-valued row is counted unmeasured, not ranked as a zero: %v", data["unmeasured"])
	}
}

// TestATimeSeriesHasNullBucketsAndNeverInventsAZero: a bucket the query returned no row for is
// a gap bucket with a null value; only the bucket the query measured has a number.
func TestATimeSeriesHasNullBucketsAndNeverInventsAZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	directory := t.TempDir()
	writeWidget(t, directory, "example_series", shapeManifest("example_series", "time_series", true, ""),
		`SELECT (:to - :to % 900) - 900 AS bucket, 3 AS value, 'x' AS series`)
	service := f.operatorService(t, directory)
	if len(service.Refusals()) != 0 {
		t.Fatal(service.Refusals())
	}
	_, answer := serve(t, service, "example_series", map[string]any{"period": "24h"})
	data := object(t, answer, "data")
	series := list(t, data, "series")
	if len(series) != 1 {
		t.Fatalf("one series expected, got %v", series)
	}
	buckets := list(t, series[0].(map[string]any), "buckets")
	if int(data["bucket_seconds"].(float64)) != 900 || len(buckets) < 96 {
		t.Fatalf("a 24h window is 15-minute buckets: %v buckets of %v s", len(buckets), data["bucket_seconds"])
	}
	measured, nulls := 0, 0
	for _, entry := range buckets {
		bucket := entry.(map[string]any)
		if bucket["value"] == nil {
			nulls++
			if bucket["gap"] != true {
				t.Errorf("a bucket with no row must be a gap, got %v", bucket)
			}
			continue
		}
		measured++
		if figureValue(t, bucket["value"]) != 3 {
			t.Errorf("the measured bucket carries %v", bucket["value"])
		}
	}
	if measured != 1 || nulls != len(buckets)-1 {
		t.Errorf("%d measured and %d null buckets of %d: only the returned bucket has a value", measured, nulls, len(buckets))
	}
	window := object(t, answer, "window")
	if window["coverage"] == "covered" {
		t.Errorf("one returned bucket is not full coverage of a day: %v", window)
	}
}

// shapeManifest builds a manifest for a synthetic shape test: one query file, no provider,
// optional extra parameter JSON, a period when windowed.
func shapeManifest(widgetType, shape string, windowed bool, extra string) string {
	parameters := []string{}
	if windowed {
		parameters = append(parameters, `{"key":"period","kind":"enum","values":["1h","24h","7d","30d"],"default":"24h","class":"data"}`)
	}
	if extra != "" {
		parameters = append(parameters, extra)
	}
	windowedText := "false"
	if windowed {
		windowedText = "true"
	}
	return `{"manifest_version":1,"type":"` + widgetType + `","shape":"` + shape + `","providers":[],` +
		`"windowed":` + windowedText + `,"lower_bound":true,"measure":"example","parameters":[` +
		strings.Join(parameters, ",") + `],"queries":["query.sql"]}`
}

// TestEveryShapeIsDrawnFromItsColumnContract draws all twelve shapes through the engine from
// a literal query, and checks the payload member each shape's contract names.
func TestEveryShapeIsDrawnFromItsColumnContract(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	directory := t.TempDir()
	limit := `{"key":"limit","kind":"integer","min":1,"default":2,"class":"data"}`
	cases := []struct {
		shape, query string
		check        func(t *testing.T, data map[string]any)
	}{
		{"figure", `SELECT 42 AS value, 'flows' AS label, 'count' AS unit, 40 AS previous`,
			func(t *testing.T, data map[string]any) {
				if figureValue(t, data["figure"]) != 42 || figureValue(t, data["previous"]) != 40 || data["unit"] != "count" {
					t.Errorf("figure: %v", data)
				}
			}},
		{"table", `SELECT column1 AS name, column2 AS n FROM (VALUES ('a', 1), ('b', 2), ('c', 3))`,
			func(t *testing.T, data map[string]any) {
				if len(list(t, data, "rows")) != 2 || len(list(t, data, "columns")) != 2 ||
					object(t, data, "remainder")["members"] != float64(1) {
					t.Errorf("table: %v", data)
				}
			}},
		{"donut", `SELECT column1 AS key, column2 AS value FROM (VALUES ('a', 6), ('b', 3), ('c', 1))`,
			func(t *testing.T, data map[string]any) {
				slices := list(t, data, "slices")
				other := object(t, data, "other")
				if len(slices) != 2 || other["members"] != float64(1) || figureValue(t, other["value"]) != 1 ||
					figureValue(t, data["total"]) != 10 || slices[0].(map[string]any)["share"] != 0.6 {
					t.Errorf("donut: %v", data)
				}
			}},
		{"feed", `SELECT column1 AS at, column2 AS text FROM (VALUES (100, 'old'), (300, 'new'), (200, 'mid'))`,
			func(t *testing.T, data map[string]any) {
				entries := list(t, data, "entries")
				if len(entries) != 2 || entries[0].(map[string]any)["text"] != "new" ||
					object(t, data, "remainder")["members"] != float64(1) {
					t.Errorf("feed (newest first, one dropped and counted): %v", data)
				}
			}},
		{"cards", `SELECT column1 AS key, column2 AS label, column3 AS cpu FROM (VALUES ('b', 'Two', 2), ('a', 'One', 1), ('c', 'Three', 3))`,
			func(t *testing.T, data map[string]any) {
				cards := list(t, data, "cards")
				if len(cards) != 2 || cards[0].(map[string]any)["key"] != "a" ||
					object(t, cards[0].(map[string]any), "fields")["cpu"] != float64(1) ||
					object(t, data, "remainder")["members"] != float64(1) {
					t.Errorf("cards: %v", data)
				}
			}},
		{"matrix", `SELECT column1 AS "row", column2 AS "column", column3 AS value FROM (VALUES ('r1', 'c1', 5), ('r1', 'c2', 7), ('r2', 'c1', 9))`,
			func(t *testing.T, data map[string]any) {
				if len(list(t, data, "rows")) != 2 || len(list(t, data, "columns")) != 2 || len(list(t, data, "cells")) != 3 {
					t.Errorf("matrix (r2/c2 was not measured and is absent, not 0): %v", data)
				}
			}},
		{"map", `SELECT column1 AS country, column2 AS value FROM (VALUES ('ZZ', 5), ('YY', 3), ('XX', 1), (NULL, 4))`,
			func(t *testing.T, data map[string]any) {
				unplaced := object(t, data, "unplaced")
				if len(list(t, data, "points")) != 2 || unplaced["members"] != float64(1) || figureValue(t, unplaced["value"]) != 4 ||
					object(t, data, "remainder")["members"] != float64(1) {
					t.Errorf("map (a row with no country is unplaced and counted): %v", data)
				}
			}},
		{"tree", `SELECT column1 AS id, column2 AS parent, column3 AS value FROM (VALUES
			('root', NULL, 10), ('a', 'root', 6), ('b', 'root', 3), ('c', 'root', 1), ('lost', 'missing', 2), ('x', 'y', 1), ('y', 'x', 1))`,
			func(t *testing.T, data map[string]any) {
				if data["orphans"] != float64(1) || data["cyclic"] != float64(2) {
					t.Errorf("tree (one orphan drawn as a root, two nodes in a cycle counted): %v", data)
				}
				roots := list(t, data, "roots")
				if len(roots) != 2 {
					t.Fatalf("tree roots: %v", roots)
				}
				first := roots[0].(map[string]any)
				if first["id"] != "root" || len(list(t, first, "children")) != 2 ||
					object(t, first, "remainder")["members"] != float64(1) {
					t.Errorf("tree root: %v", first)
				}
			}},
		{"sankey", `SELECT column1 AS source, column2 AS target, column3 AS value FROM (VALUES ('a', 'b', 5), ('a', 'c', 3), ('b', 'c', 1))`,
			func(t *testing.T, data map[string]any) {
				other := object(t, data, "other")
				if len(list(t, data, "ribbons")) != 2 || other["members"] != float64(1) || figureValue(t, other["value"]) != 1 ||
					figureValue(t, data["total"]) != 9 {
					t.Errorf("sankey: %v", data)
				}
			}},
		{"graph", `SELECT column1 AS source, column2 AS target, column3 AS value FROM (VALUES ('a', 'b', 5), ('b', 'c', NULL), ('c', 'd', 1))`,
			func(t *testing.T, data map[string]any) {
				edges := list(t, data, "edges")
				if len(edges) != 2 || edges[1].(map[string]any)["value"] != nil ||
					object(t, data, "remainder")["members"] != float64(1) || len(list(t, data, "nodes")) != 3 {
					t.Errorf("graph (an edge with no value has a null one): %v", data)
				}
			}},
		{"ranked_bars", `SELECT column1 AS key, column2 AS value FROM (VALUES ('a', 1), ('b', 2), ('c', 3))`,
			func(t *testing.T, data map[string]any) {
				bars := list(t, data, "bars")
				if len(bars) != 2 || bars[0].(map[string]any)["key"] != "c" || figureValue(t, data["total"]) != 6 {
					t.Errorf("ranked_bars: %v", data)
				}
			}},
		{"time_series", `SELECT (:to - :to % 900) - 900 AS bucket, 1 AS value`,
			func(t *testing.T, data map[string]any) {
				if len(list(t, data, "series")) != 1 {
					t.Errorf("time_series: %v", data)
				}
			}},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.shape] = true
		name := "example_" + c.shape
		writeWidget(t, directory, name, shapeManifest(name, c.shape, c.shape == "time_series", limit), c.query)
	}
	for _, shape := range params.Shapes {
		if !covered[shape] {
			t.Errorf("the shape %s has no case in this test", shape)
		}
	}
	service := f.operatorService(t, directory)
	if refusals := service.Refusals(); len(refusals) != 0 {
		t.Fatalf("a shape's widget was refused: %v", refusals)
	}
	for _, c := range cases {
		c := c
		t.Run(c.shape, func(t *testing.T) {
			status, answer := serve(t, service, "example_"+c.shape, nil)
			if status != http.StatusOK {
				t.Fatalf("answered %d", status)
			}
			if answer["condition"] != envelope.ConditionReachable {
				t.Errorf("condition %v", answer["condition"])
			}
			c.check(t, object(t, answer, "data"))
		})
	}
}

// TestAWidgetWithNoRowsKeepsItsShapeAndCountsNothing: an empty result is the shape with its
// zero members -- measured, because the source answered -- and reachable_no_rows.
func TestAWidgetWithNoRowsKeepsItsShapeAndCountsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	directory := t.TempDir()
	writeWidget(t, directory, "example_none", shapeManifest("example_none", "figure", false, ""),
		`SELECT NULL AS value`)
	service := f.operatorService(t, directory)
	_, answer := serve(t, service, "example_none", nil)
	if answer["condition"] != envelope.ConditionReachableNoRows {
		t.Errorf("a NULL figure is no figure: %v", answer["condition"])
	}
	if object(t, answer, "data")["figure"] != nil {
		t.Errorf("a NULL value must not become 0: %v", answer["data"])
	}
}

// TestAnOperatorWidgetBindsReferenceAndListParameters: a reference parameter binds the resolved
// row, a list parameter binds JSON for json_each, and an unresolved reference makes the widget
// answer the unresolved_reference condition without running the query.
func TestAnOperatorWidgetBindsReferenceAndListParameters(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	directory := t.TempDir()
	manifest := `{"manifest_version":1,"type":"example_flows_of_interfaces","shape":"ranked_bars",
		"providers":["firewall_log"],"primary":"firewall_log","windowed":true,"lower_bound":true,"measure":"logged",
		"parameters":[
		  {"key":"period","kind":"enum","values":["1h","24h","7d","30d"],"default":"24h","class":"data"},
		  {"key":"interfaces","kind":"reference_list","reference":"interface","default":null,"class":"data"},
		  {"key":"actions","kind":"enum_set","values":["pass","block"],"default":["pass","block"],"class":"data"}],
		"references":["interface"],"queries":["query.sql"]}`
	query := `SELECT f.src_interface_id AS key, count(*) AS value
		FROM flow AS f
		WHERE f.observed_at >= :from AND f.observed_at < :to
		  AND f.src_interface_id IS NOT NULL
		  AND (:interfaces IS NULL OR f.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interfaces AS jsonb)) AS j(value)))
		  AND f.action IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:actions AS jsonb)) AS j(value))
		GROUP BY f.src_interface_id`
	writeWidget(t, directory, "example_flows_of_interfaces", manifest, query)
	service := f.operatorService(t, directory)
	if len(service.Refusals()) != 0 {
		t.Fatal(service.Refusals())
	}
	reference := func(identifier string) map[string]any {
		return map[string]any{"kind": "interface", "by": "identifier", "value": identifier}
	}
	_, all := serve(t, service, "example_flows_of_interfaces", nil)
	_, one := serve(t, service, "example_flows_of_interfaces",
		map[string]any{"interfaces": []any{reference("example_if_0")}})
	if len(list(t, object(t, all, "data"), "bars")) < 2 || len(list(t, object(t, one, "data"), "bars")) != 1 {
		t.Errorf("the interface list did not restrict the query: %v / %v", all["data"], one["data"])
	}
	_, none := serve(t, service, "example_flows_of_interfaces",
		map[string]any{"interfaces": []any{reference("no_such_interface")}})
	if none["condition"] != envelope.ConditionUnresolvedReference || none["data"] != nil {
		t.Errorf("an unresolved reference gave %v with data %v", none["condition"], none["data"])
	}
	_, blockOnly := serve(t, service, "example_flows_of_interfaces", map[string]any{"actions": []any{"block"}})
	blocked := f.scalar(t, `SELECT count(DISTINCT src_interface_id) FROM flow WHERE action = 'block'
		AND src_interface_id IS NOT NULL AND observed_at >= ? AND observed_at < ?`, f.now-86400, f.now)
	if int64(len(list(t, object(t, blockOnly, "data"), "bars"))) != blocked {
		t.Errorf("the enum_set did not reach the query: %v bars, %d interfaces with a block", blockOnly["data"], blocked)
	}
}

// TestTheServiceReadsNoOperatorWidgetsWhenNoDirectoryExists: a missing widgets directory is
// the normal installation, not an error.
func TestTheServiceReadsNoOperatorWidgetsWhenNoDirectoryExists(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureSpec{interfaces: 1, clients: 1, flows: 1, span: 3600})
	service := f.operatorService(t, filepath.Join(t.TempDir(), "absent"))
	if len(service.Refusals()) != 0 || len(service.OperatorTypes()) != 0 {
		t.Errorf("a missing directory loaded %v and refused %v", service.OperatorTypes(), service.Refusals())
	}
	if len(service.Types()) != len(f.service.Types()) {
		t.Error("a missing directory changed the built-in set")
	}
}
