package widgets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The widget measure (PS22 and PS-AC37 of the NetFlow join amendment to
// specs/SPEC-backend-completion-plugins.md, with the maintainer's decision (b) of 11 October 2026):
// every type serving a Volume or a byte Count takes volume_measure -- logged, netflow or
// best_available -- and the core alone decides, from what is stored, which measure an answer is
// given in.

// The measure world: one inside interface carrying one client, a second interface, two outbound
// connections of the last hour -- 300 and 200 logged bytes -- and, when NetFlow is configured, an
// active provider of flow_record whose capture configuration names the interfaces given.
const (
	measureFlowA, measureFlowB = int64(1), int64(2)
	measureLoggedBytes         = int64(500)
	measureClientMAC           = "00:00:5e:00:53:10"
)

// newMeasureWorld is the measure world with NetFlow not configured.
func newMeasureWorld(t *testing.T) *fixture {
	t.Helper()
	database := storetest.Open(t)
	f := &fixture{db: database, now: fixtureNow}
	at := f.now - 7200
	f.exec(t, `INSERT INTO interface (id, identifier, device, description, user_label, link_type, link_kind,
		first_seen_at, last_seen_at) VALUES
		(1, 'example_if_a', 'exdev_a', 'Example A', NULL, 'example-link', 'vlan', $1, $2),
		(2, 'example_if_b', 'exdev_b', 'Example B', NULL, 'example-link', 'vlan', $3, $4)`, at, at, at, at)
	f.exec(t, `INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address,
		first_seen_at, last_seen_at) VALUES (10, 'mac', $1, 1, $2, '192.0.2.10', $3, $4)`,
		measureClientMAC, measureClientMAC, at, at)
	for _, flow := range []struct {
		id          int64
		destination string
		bytes       int64
	}{{measureFlowA, "203.0.113.7", 300}, {measureFlowB, "203.0.113.8", 200}} {
		f.exec(t, `INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device,
			interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol,
			ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES
			($1, $2, $3, $4, 'exdev_a', 'resolved', 1, 10, '192.0.2.10', $5, 'tcp', 4, 'pass', 'in', $6, 'pending',
			 'north_south')`, flow.id, "measure-"+flow.destination, f.now-700+flow.id*100, f.now-700+flow.id*100,
			flow.destination, flow.bytes)
	}
	service, err := widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) }})
	check(t, err)
	f.service = service
	return f
}

// configureNetflow makes a provider of flow_record active, with one readable capture configuration in
// force since before the window, capturing both directions on the interfaces named.
func (f *fixture) configureNetflow(t *testing.T, interfaces ...string) {
	t.Helper()
	f.exec(t, `INSERT INTO provider (id, kind, provider_key, display_name, is_active, registered_at)
		VALUES (900, 'flow_record', 'example_exporter', 'Example exporter', true, 0)`)
	f.exec(t, `INSERT INTO capture_configuration (id, provider_id, first_read_at, last_read_at, fingerprint,
		outcome, detail, enabled, export_version, active_timeout_seconds, inactive_timeout_seconds)
		VALUES (1, 900, $1, $2, 'example', NULL, NULL, true, '9', 60, 15)`, f.now-7200, f.now)
	for _, identifier := range interfaces {
		f.exec(t, `INSERT INTO capture_interface (capture_configuration_id, identifier, ingress, egress, exported)
			VALUES (1, $1, true, true, 'yes')`, identifier)
	}
}

// measured records one direction of a connection's measured volume.
func (f *fixture) measured(t *testing.T, flowID int64, direction, state string, bytes, packets int64) {
	t.Helper()
	f.measuredCopy(t, flowID, direction, state, bytes, packets, false)
}

// measuredCopy records one direction of a connection's measured volume, counted from translated copies
// when counted says so.
func (f *fixture) measuredCopy(t *testing.T, flowID int64, direction, state string, bytes, packets int64,
	counted bool) {
	t.Helper()
	records := int64(0)
	if bytes > 0 {
		records = 1
	}
	f.exec(t, `INSERT INTO connection_measured_volume (connection_flow_id, direction, connection_start_at, state,
		bytes, packets, records, counted_copy, final, due_at, computed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, NULL, $9)`,
		flowID, direction, f.now-700+flowID*100, state, bytes, packets, records, counted, f.now)
}

// measureEverything measures both directions of both connections: 17000 bytes in all.
func (f *fixture) measureEverything(t *testing.T) {
	t.Helper()
	f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
	f.measured(t, measureFlowA, "reverse", "measured", 9000, 12)
	f.measured(t, measureFlowB, "forward", "measured", 1500, 3)
	f.measured(t, measureFlowB, "reverse", "measured", 2500, 4)
}

// volumeTypes are the served types whose manifest declares volumes.
func volumeTypes(t *testing.T, service *widget.Service) []string {
	t.Helper()
	var types []string
	for _, widgetType := range service.Types() {
		if preset, served := service.Preset(widgetType); served && preset.Volumes {
			types = append(types, widgetType)
		}
	}
	sort.Strings(types)
	return types
}

// measureParameters are a type's parameters at its defaults, a window of one hour where it takes one,
// and a volume measure.
func measureParameters(service *widget.Service, widgetType, measure string) map[string]any {
	parameters := map[string]any{}
	if widgetType == "client_traffic_detail" {
		parameters["client"] = map[string]any{"kind": "client", "by": "mac", "value": measureClientMAC}
	}
	if params.Contains(paramKeys(service, widgetType), "period") {
		parameters["period"] = "1h"
	}
	if measure != "" {
		parameters["volume_measure"] = measure
	}
	return parameters
}

// encoded is a response as JSON.
func encoded(t *testing.T, response envelope.Response) []byte {
	t.Helper()
	raw, err := json.Marshal(response)
	check(t, err)
	return raw
}

// withoutMeasureDifferences is an answer with the differences decision (b) and the maintainer's
// decision of 11 October 2026 admit removed: the echoed volume_measure key, one named state, and that
// state's detail in state_details -- admitted only when the state itself is present, so a detail
// without its state, or a detail of another state, still differs.
func withoutMeasureDifferences(t *testing.T, response envelope.Response, state string) []byte {
	t.Helper()
	if params.Contains(response.States, state) && response.StateDetails != nil {
		details := map[string]envelope.StateDetail{}
		for name, detail := range response.StateDetails {
			if name != state {
				details[name] = detail
			}
		}
		if len(details) == 0 {
			details = nil
		}
		response.StateDetails = details
	}
	parameters := map[string]any{}
	for key, value := range response.Parameters {
		if key != params.VolumeMeasureKey {
			parameters[key] = value
		}
	}
	response.Parameters = parameters
	states := []string{}
	for _, held := range response.States {
		if held != state {
			states = append(states, held)
		}
	}
	response.States = states
	return encoded(t, response)
}

// measuresIn collects the volume measures (logged, netflow) the figures of an answer's data carry, and
// the netflow figures whose lower_bound is false, by path.
func measuresIn(t *testing.T, response envelope.Response) (map[string]bool, []string) {
	t.Helper()
	var tree any
	check(t, json.Unmarshal(encoded(t, response), &tree))
	measures := map[string]bool{}
	var exact []string
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch typed := node.(type) {
		case map[string]any:
			if measure, isText := typed["measure"].(string); isText &&
				(measure == envelope.MeasureLogged || measure == envelope.MeasureNetflow) {
				measures[measure] = true
				if bound, isBool := typed["lower_bound"].(bool); isBool && !bound && measure == envelope.MeasureNetflow {
					exact = append(exact, path)
				}
			}
			for key, child := range typed {
				walk(child, path+"."+key)
			}
		case []any:
			for _, child := range typed {
				walk(child, path+"[]")
			}
		}
	}
	walk(tree.(map[string]any)["data"], "data")
	return measures, exact
}

// TestEveryVolumeTypeAcceptsTheThreeMeasures is PS-AC37's first two bullets on the populated fixture,
// which has no active provider of flow_record: every type serving a Volume or a byte Count accepts
// the three values and echoes the one in force, best_available by default; best_available answers
// byte for byte what logged answers apart from the echoed key and the state netflow_not_configured;
// an explicit netflow carries no figure and that state.
func TestEveryVolumeTypeAcceptsTheThreeMeasures(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	types := volumeTypes(t, f.service)
	if len(types) != len(volumeMeasureTypes) {
		t.Fatalf("the types declaring volumes are %v, not the %d the golden record admits", types,
			len(volumeMeasureTypes))
	}
	for _, widgetType := range types {
		answers := map[string]envelope.Response{}
		for _, measure := range append([]string{""}, request.VolumeMeasures...) {
			parameters := measureParameters(f.service, widgetType, measure)
			for key, value := range defaultParameters(f, widgetType) {
				parameters[key] = value
			}
			response := f.respond(t, widgetType, parameters)
			if len(response.InvalidParameters) != 0 {
				t.Errorf("%s %q: invalid parameters %+v", widgetType, measure, response.InvalidParameters)
			}
			echoed := response.Parameters[params.VolumeMeasureKey]
			if (measure == "" && echoed != params.VolumeMeasureBestAvailable) || (measure != "" && echoed != measure) {
				t.Errorf("%s %q: volume_measure is echoed as %v", widgetType, measure, echoed)
			}
			answers[measure] = response
		}
		logged := encoded(t, answers[envelope.MeasureLogged])
		for _, measure := range []string{"", params.VolumeMeasureBestAvailable} {
			got := answers[measure]
			if !params.Contains(got.States, request.StateNetflowNotConfigured) {
				t.Errorf("%s %q: the states are %v, without netflow_not_configured", widgetType, measure, got.States)
			}
			if string(withoutMeasureDifferences(t, got, request.StateNetflowNotConfigured)) !=
				string(withoutMeasureDifferences(t, answers[envelope.MeasureLogged], request.StateNetflowNotConfigured)) {
				t.Errorf("%s %q: best_available is not logged's answer\nlogged: %s\ngot:    %s", widgetType, measure,
					logged, encoded(t, got))
			}
		}
		explicit := answers[envelope.MeasureNetflow]
		if explicit.Data != nil || !params.Contains(explicit.States, request.StateNetflowNotConfigured) {
			t.Errorf("%s: an explicit netflow that cannot be answered carries %s", widgetType, encoded(t, explicit))
		}
	}
}

// TestBestAvailableAnswersNetflowWhenEveryConnectionIsMeasured is PS-AC37's full coverage and NJ-AC14's
// first clause: a configuration capturing seven interfaces throughout the window and both directions of
// every connection measured, so best_available answers the netflow measure -- the bytes NetFlow
// reported, each connection counted once, with its packets and its coverage -- and every figure of the
// answer carries it.
func TestBestAvailableAnswersNetflowWhenEveryConnectionIsMeasured(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a", "example_if_b", "example_if_c", "example_if_d", "example_if_e",
		"example_if_f", "example_if_g")
	f.measureEverything(t)
	response := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
	total, _ := data(t, response)["total"].(map[string]any)
	coverage, _ := total["coverage"].(map[string]any)
	if total["measure"] != envelope.MeasureNetflow || total["bytes"] != float64(17000) || total["connections"] != float64(2) ||
		total["packets"] != float64(29) || total["lower_bound"] != true || coverage["measured"] != float64(2) ||
		coverage["partial"] != float64(0) || coverage["capture_not_known"] != float64(0) {
		t.Errorf("the total is %v", total)
	}
	if len(response.States) != 0 {
		t.Errorf("a netflow answer carries the states %v", response.States)
	}
	logged := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h", "volume_measure": "logged"})
	if loggedTotal, _ := data(t, logged)["total"].(map[string]any); loggedTotal["measure"] != envelope.MeasureLogged ||
		loggedTotal["bytes"] != float64(measureLoggedBytes) || loggedTotal["coverage"] != nil {
		t.Errorf("the logged total is %v", loggedTotal)
	}
	// Every volume type answers the netflow measure alone over the same window.
	for _, widgetType := range volumeTypes(t, f.service) {
		answer := f.respond(t, widgetType, measureParameters(f.service, widgetType, ""))
		measures, _ := measuresIn(t, answer)
		if measures[envelope.MeasureLogged] {
			t.Errorf("%s: best_available under full coverage carries a logged figure (states %v): %s", widgetType,
				answer.States, encoded(t, answer))
		}
	}
}

// TestBestAvailableFallsBackToLoggedWhenAConnectionIsNotMeasured is PS-AC37's partial coverage and
// NJ-AC14's second and third clauses: F5's reverse direction, ingress_not_captured, makes best_available
// answer logged, byte for byte apart from the admitted differences, with netflow_not_covering_window
// and its detail; an explicit netflow answers the netflow figures, each with its coverage and its NJ5
// counts, and the same state.
func TestBestAvailableFallsBackToLoggedWhenAConnectionIsNotMeasured(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
	f.measured(t, measureFlowA, "reverse", "measured", 9000, 12)
	f.measured(t, measureFlowB, "forward", "measured", 1500, 3)
	f.measured(t, measureFlowB, "reverse", "ingress_not_captured", 0, 0)
	parameters := map[string]any{"period": "1h"}
	best := f.respond(t, "interface_volume_ranking", parameters)
	parameters["volume_measure"] = "logged"
	logged := f.respond(t, "interface_volume_ranking", parameters)
	if !params.Contains(best.States, request.StateNetflowNotCoveringWindow) ||
		string(withoutMeasureDifferences(t, best, request.StateNetflowNotCoveringWindow)) !=
			string(withoutMeasureDifferences(t, logged, request.StateNetflowNotCoveringWindow)) {
		t.Errorf("best_available under partial coverage is not logged's answer with the state\nlogged: %s\ngot:    %s",
			encoded(t, logged), encoded(t, best))
	}
	if detail := best.StateDetails[request.StateNetflowNotCoveringWindow]; len(detail.IntervalStates) != 1 ||
		detail.IntervalStates[0] != netflow.CaptureConfigured {
		t.Errorf("the detail of netflow_not_covering_window is %+v, not the one interval state capture_configured",
			best.StateDetails)
	}
	parameters["volume_measure"] = "netflow"
	explicit := f.respond(t, "interface_volume_ranking", parameters)
	total, _ := data(t, explicit)["total"].(map[string]any)
	coverage, _ := total["coverage"].(map[string]any)
	states, _ := coverage["direction_states"].(map[string]any)
	if total["measure"] != envelope.MeasureNetflow || total["bytes"] != float64(14500) || coverage["measured"] != float64(1) ||
		coverage["partial"] != float64(1) || states["ingress_not_captured"] != float64(1) ||
		!params.Contains(explicit.States, request.StateNetflowNotCoveringWindow) {
		t.Errorf("the explicit netflow total is %v, states %v", total, explicit.States)
	}
}

// TestUnderAnUnknownConfigurationBestAvailableIsLoggedAndNetflowCarriesNoFigure is PS-AC37's addition as
// the amendment "the NetFlow join with every interface captured" keeps it (decided question 5): a window
// whose configuration could not be read throughout makes best_available answer byte for byte what logged
// answers apart from the echoed key and capture_configuration_unknown, which carries no detail; an
// explicit netflow carries no figure and the same state. Every interface captured is no reason of its
// own: TestBestAvailableAnswersNetflowWhenEveryConnectionIsMeasured answers netflow under seven.
func TestUnderAnUnknownConfigurationBestAvailableIsLoggedAndNetflowCarriesNoFigure(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a", "example_if_b")
	f.exec(t, `UPDATE capture_configuration SET fingerprint = NULL, outcome = 'unavailable', enabled = NULL`)
	f.measureEverything(t)
	for _, widgetType := range volumeTypes(t, f.service) {
		best := f.respond(t, widgetType, measureParameters(f.service, widgetType, ""))
		logged := f.respond(t, widgetType, measureParameters(f.service, widgetType, envelope.MeasureLogged))
		if !params.Contains(best.States, request.StateCaptureConfigurationUnknown) ||
			string(withoutMeasureDifferences(t, best, request.StateCaptureConfigurationUnknown)) !=
				string(withoutMeasureDifferences(t, logged, request.StateCaptureConfigurationUnknown)) {
			t.Errorf("%s: best_available under an unknown configuration\nlogged: %s\ngot:    %s", widgetType,
				encoded(t, logged), encoded(t, best))
		}
		explicit := f.respond(t, widgetType, measureParameters(f.service, widgetType, envelope.MeasureNetflow))
		if explicit.Data != nil || !params.Contains(explicit.States, request.StateCaptureConfigurationUnknown) {
			t.Errorf("%s: an explicit netflow under an unknown configuration carries %s", widgetType, encoded(t, explicit))
		}
		for name, answer := range map[string]envelope.Response{"best_available": best, "netflow": explicit,
			"logged": logged} {
			if answer.StateDetails != nil {
				t.Errorf("%s %s: the state details are %+v, want none", widgetType, name, answer.StateDetails)
			}
		}
	}
}

// TestEveryFigureOfAnAnswerCarriesOneMeasure is PS-AC37's "every figure in one answer carries the same
// measure", over every volume type and every value, with NetFlow measuring everything, some of it, or
// nothing.
func TestEveryFigureOfAnAnswerCarriesOneMeasure(t *testing.T) {
	t.Parallel()
	for name, prepare := range map[string]func(*testing.T, *fixture){
		"measured": func(t *testing.T, f *fixture) { f.configureNetflow(t, "example_if_a"); f.measureEverything(t) },
		"partial": func(t *testing.T, f *fixture) {
			f.configureNetflow(t, "example_if_a")
			f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
		},
		"not_configured": func(*testing.T, *fixture) {},
	} {
		f := newMeasureWorld(t)
		prepare(t, f)
		for _, widgetType := range volumeTypes(t, f.service) {
			for _, measure := range request.VolumeMeasures {
				answer := f.respond(t, widgetType, measureParameters(f.service, widgetType, measure))
				measures, _ := measuresIn(t, answer)
				if len(measures) > 1 {
					t.Errorf("%s: %s %s mixes the measures %v: %s", name, widgetType, measure, measures,
						encoded(t, answer))
				}
				if measure == envelope.MeasureLogged && measures[envelope.MeasureNetflow] && widgetType != "custom_chart" {
					t.Errorf("%s: %s answers a netflow figure under logged", name, widgetType)
				}
			}
		}
	}
}

// TestLowerBoundIsFalseOnlyOnAFullyMeasuredRecord is PS-AC37's lower_bound: false on a single
// client_traffic_detail record whose two directions are measured, and true on every aggregate netflow
// figure and on a record a direction of which is not measured. The record carries, per direction, the
// bytes, the packets, the state and whether it was counted from translated copies.
func TestLowerBoundIsFalseOnlyOnAFullyMeasuredRecord(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
	f.measured(t, measureFlowA, "reverse", "measured", 9000, 12)
	f.measured(t, measureFlowB, "forward", "measured", 1500, 3)
	f.measured(t, measureFlowB, "reverse", "in_progress", 700, 2)
	detail := f.respond(t, "client_traffic_detail", measureParameters(f.service, "client_traffic_detail",
		envelope.MeasureNetflow))
	records, _ := data(t, detail)["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("the records are %v", data(t, detail))
	}
	byFlow := map[float64]map[string]any{}
	for _, record := range records {
		row := record.(map[string]any)
		byFlow[row["flow_id"].(float64)] = row
	}
	full, partial := byFlow[float64(measureFlowA)], byFlow[float64(measureFlowB)]
	fullBytes, _ := full["bytes"].(map[string]any)
	partialBytes, _ := partial["bytes"].(map[string]any)
	if fullBytes["measure"] != envelope.MeasureNetflow || fullBytes["value"] != float64(13000) || fullBytes["lower_bound"] != false {
		t.Errorf("the fully measured record's bytes are %v", fullBytes)
	}
	if partialBytes["value"] != float64(2200) || partialBytes["lower_bound"] != true {
		t.Errorf("the partly measured record's bytes are %v", partialBytes)
	}
	directions, _ := partial["directions"].(map[string]any)
	reverse, _ := directions["reverse"].(map[string]any)
	if reverse["bytes"] != float64(700) || reverse["packets"] != float64(2) || reverse["state"] != "in_progress" ||
		reverse["counted_copy"] != false {
		t.Errorf("the record's reverse direction is %v", reverse)
	}
	for _, widgetType := range volumeTypes(t, f.service) {
		answer := f.respond(t, widgetType, measureParameters(f.service, widgetType, envelope.MeasureNetflow))
		_, exact := measuresIn(t, answer)
		for _, path := range exact {
			if widgetType != "client_traffic_detail" || path != "data.records[].bytes" {
				t.Errorf("%s: the netflow figure at %s is not a lower bound", widgetType, path)
			}
		}
	}
	logged := f.respond(t, "client_traffic_detail", measureParameters(f.service, "client_traffic_detail",
		envelope.MeasureLogged))
	for _, record := range data(t, logged)["records"].([]any) {
		row := record.(map[string]any)
		if _, present := row["directions"]; present || row["bytes"].(map[string]any)["lower_bound"] != true {
			t.Errorf("a logged record carries %v", row)
		}
	}
}

// writePlugin writes a third party's plugin into a data directory, file by file.
func writePlugin(t *testing.T, dataDir, name string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		target := filepath.Join(dataDir, plugin.PluginsDirectory, name, filepath.FromSlash(path))
		check(t, os.MkdirAll(filepath.Dir(target), 0o750))
		check(t, os.WriteFile(target, []byte(content), 0o640))
	}
}

// pluginService is a widget service over a database, with the plugins of a data directory.
func pluginService(t *testing.T, f *fixture, dataDir string) *widget.Service {
	t.Helper()
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: f.db})
	check(t, err)
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("the plugin was refused: %+v", refusals)
	}
	service, err := widget.New(f.db, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) },
		Plugins: loaded.WidgetSources()})
	check(t, err)
	if refused := service.Refusals(); len(refused) != 0 {
		t.Fatalf("the widget was refused: %+v", refused)
	}
	return service
}

// exampleVolumePlugin is a third party's widget: a total of the rolling window's volume, read through
// the core and built through host.figures. It names no measure, and declares volumes in its manifest.
var exampleVolumePlugin = map[string]string{
	"plugin.json": `{"api_version": 2, "name": "example_volume", "version": "0.1.0",
		"display_name": "Example volume", "widgets": ["example_volume_total"]}`,
	"widgets/example_volume_total/manifest.json": `{
		"manifest_version": 1, "type": "example_volume_total", "shape": "figure", "windowed": true,
		"lower_bound": true, "volumes": true,
		"parameters": [{"key": "period", "kind": "enum", "values": ["1h", "24h"], "default": "24h"}],
		"queries": [], "script": "total.js"}`,
	"widgets/example_volume_total/total.js": `
exports.widget = {
  serve: function (host) {
    var window = host.volume.window();
    return { data: { total: host.figures.volume(host.volume.total(window.rows)) }, has_rows: window.rows.length > 0 };
  }
};
`,
}

// TestAThirdPartyWidgetGetsTheMeasureFromItsManifestAlone is CLAUDE.md's "Modular at every layer" for
// the measure: a third party's widget, written as files into a data directory and declaring volumes,
// takes volume_measure and is answered in the netflow measure -- with no shipped file edited, no list
// naming it, and no measure named in its script.
func TestAThirdPartyWidgetGetsTheMeasureFromItsManifestAlone(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measureEverything(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_volume", exampleVolumePlugin)
	f.service = pluginService(t, f, dataDir)
	preset, served := f.service.Preset("example_volume_total")
	if !served || !preset.Volumes {
		t.Fatalf("the third party's widget is not served as a volume type: %+v", preset)
	}
	best := f.respond(t, "example_volume_total", map[string]any{"period": "1h"})
	total, _ := data(t, best)["total"].(map[string]any)
	if best.Parameters[params.VolumeMeasureKey] != params.VolumeMeasureBestAvailable ||
		total["measure"] != envelope.MeasureNetflow || total["bytes"] != float64(17000) {
		t.Errorf("best_available answered %s", encoded(t, best))
	}
	logged := f.respond(t, "example_volume_total", map[string]any{"period": "1h", "volume_measure": "logged"})
	if loggedTotal, _ := data(t, logged)["total"].(map[string]any); loggedTotal["measure"] != envelope.MeasureLogged ||
		loggedTotal["bytes"] != float64(measureLoggedBytes) {
		t.Errorf("logged answered %s", encoded(t, logged))
	}
	// The netflow Volume carries its coverage, whole and per part, and its direction states.
	if coverage, _ := total["coverage"].(map[string]any); coverage == nil || total["allowed_coverage"] == nil ||
		coverage["direction_states"].(map[string]any)["measured"] != float64(2) {
		t.Errorf("the third party's netflow total is %v", total)
	}

	// The states reach the third party's widget too: a configuration that could not be read, which carries
	// no detail, and a connection whose reverse direction enters by an interface captured on egress only,
	// whose state carries the interval states met.
	hidden := newMeasureWorld(t)
	hidden.configureNetflow(t, "example_if_a", "example_if_b")
	hidden.exec(t, `UPDATE capture_configuration SET fingerprint = NULL, outcome = 'unavailable', enabled = NULL`)
	hidden.measureEverything(t)
	hidden.service = pluginService(t, hidden, dataDir)
	answer := hidden.respond(t, "example_volume_total", map[string]any{"period": "1h"})
	if !params.Contains(answer.States, request.StateCaptureConfigurationUnknown) || answer.StateDetails != nil {
		t.Errorf("under an unknown configuration the third party's widget answered %s", encoded(t, answer))
	}
	uncovered := newMeasureWorld(t)
	uncovered.configureNetflow(t, "example_if_a")
	uncovered.measured(t, measureFlowA, "forward", "measured", 4000, 10)
	uncovered.measured(t, measureFlowA, "reverse", "ingress_not_captured", 0, 0)
	uncovered.service = pluginService(t, uncovered, dataDir)
	answer = uncovered.respond(t, "example_volume_total", map[string]any{"period": "1h"})
	detail := answer.StateDetails[request.StateNetflowNotCoveringWindow]
	if !params.Contains(answer.States, request.StateNetflowNotCoveringWindow) ||
		strings.Join(detail.IntervalStates, ",") != netflow.CaptureConfigured {
		t.Errorf("under partial coverage the third party's widget answered %s", encoded(t, answer))
	}
}

// exampleMixingPlugin is a third party's widget that adds the logged bytes its own statement reads to
// the figures the core read: under netflow that would be a Volume built from two measures.
var exampleMixingPlugin = map[string]string{
	"plugin.json": `{"api_version": 2, "name": "example_mixing", "version": "0.1.0",
		"display_name": "Example mixing", "widgets": ["example_mixing_total"]}`,
	"widgets/example_mixing_total/manifest.json": `{
		"manifest_version": 1, "type": "example_mixing_total", "shape": "figure", "windowed": true,
		"lower_bound": true, "volumes": true,
		"parameters": [{"key": "period", "kind": "enum", "values": ["1h", "24h"], "default": "24h"}],
		"queries": ["mixing.statements.sql"], "script": "mixing.js"}`,
	"widgets/example_mixing_total/mixing.statements.sql": `
-- statement: widget_example_mixing_logged
SELECT coalesce(sum(packet_bytes), 0) AS bytes, count(*) AS allowed_connections
FROM flow WHERE observed_at >= :from AND observed_at < :to;
`,
	"widgets/example_mixing_total/mixing.js": `
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var core = host.volume.total(host.volume.window().rows);
    var own = host.query.row("widget_example_mixing_logged", { from: window.from, to: window.to });
    return { data: { total: host.figures.volume(host.figures.add(core, own)) }, has_rows: true };
  }
};
`,
}

// TestNoVolumeIsServedBuiltFromTwoMeasures is PS-AC35's "no Volume or Count is built from two
// measures" and PS23's "never logged figures labelled netflow": a widget adding logged bytes of its own
// to netflow figures is never served as netflow. best_available falls back to its logged answer with
// netflow_not_served_by_type, and an explicit netflow carries no figure.
func TestNoVolumeIsServedBuiltFromTwoMeasures(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measureEverything(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_mixing", exampleMixingPlugin)
	f.service = pluginService(t, f, dataDir)
	best := f.respond(t, "example_mixing_total", map[string]any{"period": "1h"})
	total, _ := data(t, best)["total"].(map[string]any)
	if total["measure"] != envelope.MeasureLogged || total["bytes"] != float64(2*measureLoggedBytes) ||
		!params.Contains(best.States, request.StateNetflowNotServedByType) {
		t.Errorf("best_available answered %s", encoded(t, best))
	}
	explicit := f.respond(t, "example_mixing_total", map[string]any{"period": "1h", "volume_measure": "netflow"})
	if explicit.Data != nil || !params.Contains(explicit.States, request.StateNetflowNotServedByType) {
		t.Errorf("an explicit netflow answered %s", encoded(t, explicit))
	}
}

// TestTheWindowStateNamesWhyNetflowCannotAnswer is PS18's interval states, as NJ6 keeps them, as an answer
// names them: a configuration that could not be read, one with the export disabled, and a window starting
// before the first read.
func TestTheWindowStateNamesWhyNetflowCannotAnswer(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		prepare func(*testing.T, *fixture)
		state   string
	}{
		"unreadable": {func(t *testing.T, f *fixture) {
			f.configureNetflow(t, "example_if_a")
			f.exec(t, `UPDATE capture_configuration SET fingerprint = NULL, outcome = 'unavailable', enabled = NULL`)
		}, request.StateCaptureConfigurationUnknown},
		"disabled": {func(t *testing.T, f *fixture) {
			f.configureNetflow(t, "example_if_a")
			f.exec(t, `UPDATE capture_configuration SET enabled = false`)
		}, request.StateNetflowNotConfigured},
		"read_inside_the_window": {func(t *testing.T, f *fixture) {
			f.configureNetflow(t, "example_if_a")
			f.exec(t, `UPDATE capture_configuration SET first_read_at = $1`, f.now-1800)
		}, request.StateNetflowNotCoveringWindow},
	} {
		f := newMeasureWorld(t)
		test.prepare(t, f)
		answer := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
		if !params.Contains(answer.States, test.state) {
			t.Errorf("%s: the states are %v, not %s", name, answer.States, test.state)
		}
		// Only netflow_not_covering_window, of these, carries a detail: the interval states the window
		// meets -- here no configuration read before first_read_at, then a readable, enabled one.
		if test.state == request.StateNetflowNotCoveringWindow {
			detail := answer.StateDetails[test.state]
			if len(answer.StateDetails) != 1 || strings.Join(detail.IntervalStates, ",") !=
				netflow.CaptureUnknown+","+netflow.CaptureConfigured {
				t.Errorf("%s: the state details are %+v", name, answer.StateDetails)
			}
		} else if answer.StateDetails != nil {
			t.Errorf("%s: the state %s carries the details %+v", name, test.state, answer.StateDetails)
		}
		if total, _ := data(t, answer)["total"].(map[string]any); total["measure"] != envelope.MeasureLogged {
			t.Errorf("%s: the total is %v", name, total)
		}
	}
}

// TestCaptureStatesAreReadFromTheStoredConfiguration is PS18's table, as NJ6 keeps it, on the store's
// single definition, which the collector and the widget measure both read: unknown before the first
// read and between two reads that differ, configured over a readable enabled run whatever the number of
// interfaces it captures, and not configured over one capturing nothing.
func TestCaptureStatesAreReadFromTheStoredConfiguration(t *testing.T) {
	t.Parallel()
	one := netflow.CaptureConfiguration{FirstReadAt: 100, LastReadAt: 200, Readable: true, Enabled: true,
		Interfaces: []netflow.CaptureInterface{{Identifier: "example_if_a", Ingress: true, Egress: true}}}
	two := one
	two.FirstReadAt, two.LastReadAt = 300, 400
	two.Interfaces = append(two.Interfaces, netflow.CaptureInterface{Identifier: "example_if_b", Egress: true})
	none := netflow.CaptureConfiguration{FirstReadAt: 500, LastReadAt: 600, Readable: true, Enabled: true}
	configurations := []netflow.CaptureConfiguration{one, two, none}
	for _, check := range []struct {
		at    int64
		state string
	}{
		{50, netflow.CaptureUnknown},
		{110, netflow.CaptureConfigured},
		{250, netflow.CaptureUnknown},
		{350, netflow.CaptureConfigured},
		{550, netflow.CaptureNotConfigured},
		{900, netflow.CaptureNotConfigured},
	} {
		if state, _ := netflow.CaptureStateAt(configurations, check.at); state != check.state {
			t.Errorf("at %d: %s, not %s", check.at, state, check.state)
		}
	}
	points := netflow.CaptureChangePoints(configurations, 0, 1000)
	want := []int64{0, 100, 201, 300, 401, 500, 601}
	if len(points) != len(want) {
		t.Fatalf("the change points are %v, not %v", points, want)
	}
	for index := range want {
		if points[index] != want[index] {
			t.Errorf("the change points are %v, not %v", points, want)
		}
	}
}

// TestACustomChartSeriesOfTheNetflowMeasures is PS22's custom_chart measures: a traffic series of
// netflow_bytes or netflow_packets is the netflow measure whatever volume_measure says, beside a logged
// series that is never summed with it, and carries no figure, with the named state, when NetFlow cannot
// answer the window.
func TestACustomChartSeriesOfTheNetflowMeasures(t *testing.T) {
	t.Parallel()
	series := []any{
		map[string]any{"source": "traffic", "measure": "netflow_bytes"},
		map[string]any{"source": "traffic", "measure": "netflow_packets"},
		map[string]any{"source": "traffic", "measure": "bytes"},
	}
	parameters := map[string]any{"period": "1h", "series": series, "volume_measure": "logged"}
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measureEverything(t)
	lines, _ := data(t, f.respond(t, "custom_chart", parameters))["series"].([]any)
	if len(lines) != 3 {
		t.Fatalf("the series are %v", lines)
	}
	for index, want := range []struct {
		measure string
		total   float64
	}{{envelope.MeasureNetflow, 17000}, {envelope.MeasureNetflow, 29}, {envelope.MeasureLogged, float64(measureLoggedBytes)}} {
		line := lines[index].(map[string]any)
		if line["measure"] != want.measure || line["total"] != want.total {
			t.Errorf("series %d is %s with total %v, not %s with %v", index, line["measure"], line["total"],
				want.measure, want.total)
		}
	}

	hidden := newMeasureWorld(t)
	hidden.configureNetflow(t, "example_if_a", "example_if_b")
	hidden.exec(t, `UPDATE capture_configuration SET fingerprint = NULL, outcome = 'unavailable', enabled = NULL`)
	hidden.measureEverything(t)
	answer := hidden.respond(t, "custom_chart", parameters)
	lines, _ = data(t, answer)["series"].([]any)
	if len(lines) != 3 || !params.Contains(answer.States, request.StateCaptureConfigurationUnknown) ||
		answer.StateDetails != nil {
		t.Fatalf("under an unknown configuration the chart answered %s", encoded(t, answer))
	}
	for index := 0; index < 2; index++ {
		line := lines[index].(map[string]any)
		buckets, _ := line["buckets"].([]any)
		for _, bucket := range buckets {
			if bucket.(map[string]any)["value"] != nil {
				t.Errorf("series %d carries a figure under an unknown configuration: %v", index, bucket)
			}
		}
		if line["total"] != nil {
			t.Errorf("series %d carries the total %v under an unknown configuration", index, line["total"])
		}
	}
}
