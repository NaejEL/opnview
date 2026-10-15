package widgets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The maintainer's decisions of 11 October 2026, after the PS22 build: the aggregates count
// connections per direction state, so best_available judges "covered" exactly as PS22 states it; the
// allowed, blocked and unknown netflow parts carry their own packets and coverage; a state's detail is
// served in state_details; a widget whose netflow twin binds other parameters is refused at load.

// TestBestAvailableTreatsEveryCoveredDirectionStateAsCovered is PS22's "covered": a direction
// in_progress, awaiting_export or missing is covered at the Observation Point, so best_available
// answers the netflow measure -- with no state -- and the coverage says which states were met.
func TestBestAvailableTreatsEveryCoveredDirectionStateAsCovered(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		state          string
		bytes, packets int64
		member         string
	}{
		{"in_progress", 700, 2, "in_progress"},
		{"awaiting_export", 0, 0, "awaiting_export"},
		{"missing", 0, 0, "missing"},
	} {
		f := newMeasureWorld(t)
		f.configureNetflow(t, "example_if_a")
		f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
		f.measured(t, measureFlowA, "reverse", "measured", 9000, 12)
		f.measured(t, measureFlowB, "forward", "measured", 1500, 3)
		f.measured(t, measureFlowB, "reverse", test.state, test.bytes, test.packets)
		answer := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
		total, _ := data(t, answer)["total"].(map[string]any)
		coverage, _ := total["coverage"].(map[string]any)
		states, _ := coverage["direction_states"].(map[string]any)
		if total["measure"] != envelope.MeasureNetflow || len(answer.States) != 0 || answer.StateDetails != nil {
			t.Errorf("%s: best_available answered %s", test.state, encoded(t, answer))
			continue
		}
		if total["bytes"] != float64(14500+test.bytes) || coverage["measured"] != float64(1) ||
			states["measured"] != float64(2) || states[test.member] != float64(1) || states["not_captured"] != float64(0) {
			t.Errorf("%s: the netflow total is %v", test.state, total)
		}
	}
}

// TestBestAvailableFallsBackOnEveryUncoveredDirectionState is PS22's "not covered": a direction
// not_captured, export_gap or not_exported_when_refused, or a connection with no measured volume at all
// (no single Observation Point at its start), makes best_available answer logged with
// netflow_not_covering_window.
func TestBestAvailableFallsBackOnEveryUncoveredDirectionState(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"not_captured", "export_gap", "not_exported_when_refused", "no_row"} {
		f := newMeasureWorld(t)
		f.configureNetflow(t, "example_if_a")
		f.measured(t, measureFlowA, "forward", "measured", 4000, 10)
		f.measured(t, measureFlowA, "reverse", "measured", 9000, 12)
		if state != "no_row" {
			f.measured(t, measureFlowB, "forward", "measured", 1500, 3)
			f.measured(t, measureFlowB, "reverse", state, 0, 0)
		}
		best := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
		logged := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h", "volume_measure": "logged"})
		if !params.Contains(best.States, request.StateNetflowNotCoveringWindow) ||
			string(withoutMeasureDifferences(t, best, request.StateNetflowNotCoveringWindow)) !=
				string(withoutMeasureDifferences(t, logged, request.StateNetflowNotCoveringWindow)) {
			t.Errorf("%s: best_available is not logged's answer with the state\nlogged: %s\ngot:    %s", state,
				encoded(t, logged), encoded(t, best))
		}
		explicit := f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h", "volume_measure": "netflow"})
		total, _ := data(t, explicit)["total"].(map[string]any)
		coverage, _ := total["coverage"].(map[string]any)
		states, _ := coverage["direction_states"].(map[string]any)
		member, count := state, float64(1)
		if state == "no_row" {
			member, count = "not_joinable", 1
			if coverage["not_joinable"] != count {
				t.Errorf("%s: the explicit netflow coverage is %v", state, coverage)
			}
		} else if states[member] != count {
			t.Errorf("%s: the explicit netflow direction states are %v", state, states)
		}
	}
}

// measureBlockedFlow is a third connection of the measure world, refused: logged on the inside
// interface with 100 bytes, its forward direction measured at 600 bytes and 2 packets, its reverse
// missing.
const measureFlowBlocked = int64(3)

func (f *fixture) measureBlockedFlow(t *testing.T) {
	t.Helper()
	f.exec(t, `INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device,
		interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol,
		ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES
		(?, 'measure-blocked', ?, ?, 'exdev_a', 'resolved', 1, 10, '192.0.2.10', '203.0.113.9', 'tcp', 4, 'block',
		 'in', 100, 'pending', 'north_south')`, measureFlowBlocked, f.now-400, f.now-400)
	f.exec(t, `INSERT INTO connection_measured_volume (connection_flow_id, direction, connection_start_at, state,
		bytes, packets, records, observation_point_identifier, observation_point_device, measured_at_leg,
		measured_flow_id, final, due_at, computed_at) VALUES
		(?, 'forward', ?, 'measured', 600, 2, 1, 'example_if_a', 'exdev_a', 'first_leg', ?, 1, NULL, ?),
		(?, 'reverse', ?, 'missing', 0, 0, 0, 'example_if_a', 'exdev_a', 'first_leg', ?, 1, NULL, ?)`,
		measureFlowBlocked, f.now-400, measureFlowBlocked, f.now, measureFlowBlocked, f.now-400, measureFlowBlocked, f.now)
}

// TestEachActionPartCarriesItsOwnPacketsAndCoverage is "coverage per action": a netflow Volume
// carries the packets and the coverage of its allowed, blocked and unknown parts beside the whole, and
// a map, which keeps one part, serves that part's own packets and coverage.
func TestEachActionPartCarriesItsOwnPacketsAndCoverage(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	f.configureNetflow(t, "example_if_a")
	f.measureEverything(t)
	f.measureBlockedFlow(t)
	coverageOf := func(volume map[string]any, member string) map[string]any {
		coverage, _ := volume[member].(map[string]any)
		return coverage
	}
	total, _ := data(t, f.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"}))["total"].(map[string]any)
	if total["measure"] != envelope.MeasureNetflow || total["packets"] != float64(31) || total["allowed_packets"] != float64(29) ||
		total["blocked_packets"] != float64(2) || total["unknown_packets"] != float64(0) ||
		coverageOf(total, "coverage")["measured"] != float64(2) || coverageOf(total, "coverage")["partial"] != float64(1) ||
		coverageOf(total, "allowed_coverage")["measured"] != float64(2) ||
		coverageOf(total, "allowed_coverage")["partial"] != float64(0) ||
		coverageOf(total, "blocked_coverage")["partial"] != float64(1) ||
		coverageOf(total, "blocked_coverage")["direction_states"].(map[string]any)["missing"] != float64(1) {
		t.Errorf("the netflow total is %v", total)
	}
	for _, test := range []struct {
		widgetType                 string
		bytes, packets, connection float64
		measured, partial          float64
	}{
		{"passed_traffic_world_map", 17000, 29, 2, 2, 0},
		{"blocked_traffic_world_map", 600, 2, 1, 0, 1},
	} {
		answer := f.respond(t, test.widgetType, map[string]any{"period": "1h"})
		mapTotal, _ := data(t, answer)["total"].(map[string]any)
		coverage := coverageOf(mapTotal, "coverage")
		if mapTotal["measure"] != envelope.MeasureNetflow || mapTotal["bytes"] != test.bytes ||
			mapTotal["packets"] != test.packets || mapTotal["connections"] != test.connection || coverage == nil ||
			coverage["measured"] != test.measured || coverage["partial"] != test.partial {
			t.Errorf("%s: the map's total is %v (states %v)", test.widgetType, mapTotal, answer.States)
		}
	}
}

// exampleTwinPlugin is a third party's widget whose statement declares a netflow twin binding the
// parameters given: the twin is valid only when they are its statement's.
func exampleTwinPlugin(twinParameters string, twinTemplate bool) map[string]string {
	from := "flow"
	if twinTemplate {
		from = "volume_aggregate_@period@"
	}
	return map[string]string{
		"plugin.json": `{"api_version": 1, "name": "example_twin", "version": "0.1.0",
			"display_name": "Example twin", "widgets": ["example_twin_total"]}`,
		"widgets/example_twin_total/manifest.json": `{
			"manifest_version": 1, "type": "example_twin_total", "shape": "figure", "windowed": true,
			"lower_bound": true, "volumes": true,
			"parameters": [{"key": "period", "kind": "enum", "values": ["1h", "24h"], "default": "24h"}],
			"queries": ["twin.statements.sql"], "script": "twin.js"}`,
		"widgets/example_twin_total/twin.statements.sql": `
-- statement: widget_example_twin_rows
SELECT count(*) AS allowed_connections FROM flow WHERE observed_at >= :from AND observed_at < :to;

-- statement: widget_example_twin_rows_netflow
SELECT count(*) AS allowed_connections, 'netflow' AS measure FROM ` + from + ` WHERE ` + twinParameters + `;
`,
		"widgets/example_twin_total/twin.js": `
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var row = host.query.row("widget_example_twin_rows", { from: window.from, to: window.to });
    return { data: { total: host.figures.volume(row) }, has_rows: true };
  }
};
`,
	}
}

// TestAWidgetWhoseNetflowTwinBindsOtherParametersIsRefused is twin validation at load: a statement's
// netflow twin that binds other parameters than the statement, or runs at a period where the
// statement does not, is refused with netflow_twin_mismatch, and nothing else is; a twin binding the
// same parameters loads.
func TestAWidgetWhoseNetflowTwinBindsOtherParametersIsRefused(t *testing.T) {
	t.Parallel()
	f := newMeasureWorld(t)
	for name, test := range map[string]struct {
		parameters string
		template   bool
		refused    bool
	}{
		"same parameters":     {"observed_at >= :from AND observed_at < :to", false, false},
		"a parameter missing": {"observed_at >= :from", false, true},
		"a parameter more":    {"observed_at >= :from AND observed_at < :to AND id <> :extra", false, true},
		"a period template":   {"period_start_at >= :from AND period_start_at < :to", true, true},
	} {
		dataDir := t.TempDir()
		writePlugin(t, dataDir, "example_twin", exampleTwinPlugin(test.parameters, test.template))
		loaded, err := plugin.Load(t.Context(), plugin.Options{DataDir: dataDir, Store: f.db})
		check(t, err)
		if refusals := loaded.Refusals(); len(refusals) != 0 {
			t.Fatalf("%s: the plugin was refused: %+v", name, refusals)
		}
		service, err := widget.New(f.db, widget.Options{Plugins: loaded.WidgetSources()})
		check(t, err)
		refusals := service.Refusals()
		_, served := service.Preset("example_twin_total")
		switch {
		case test.refused && (len(refusals) != 1 || refusals[0].Code != params.RefusalNetflowTwinMismatch ||
			!strings.Contains(refusals[0].Detail, "widget_example_twin_rows_netflow") || served):
			t.Errorf("%s: the refusals are %+v (served %v)", name, refusals, served)
		case !test.refused && (len(refusals) != 0 || !served):
			t.Errorf("%s: a valid twin was refused: %+v", name, refusals)
		}
	}
}

// TestTheRefusalCodeOfATwinIsDocumented keeps the closed vocabulary of refusal codes documented: the
// twin's code is in the widget-authoring table and beside the twins in docs/plugins.md.
func TestTheRefusalCodeOfATwinIsDocumented(t *testing.T) {
	t.Parallel()
	for _, document := range []string{"widget-authoring.md", "plugins.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", document))
		check(t, err)
		if !strings.Contains(string(raw), "`"+params.RefusalNetflowTwinMismatch+"`") {
			t.Errorf("docs/%s does not name the refusal code %s", document, params.RefusalNetflowTwinMismatch)
		}
	}
}

// TestAStateDetailIsServedOnlyWithItsState is the envelope's state_details: absent from an answer
// holding no state that carries a detail -- NetFlow not configured, or answered -- and present, keyed by
// its state, when one is held.
func TestAStateDetailIsServedOnlyWithItsState(t *testing.T) {
	t.Parallel()
	unconfigured := newMeasureWorld(t)
	answer := unconfigured.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
	if !params.Contains(answer.States, request.StateNetflowNotConfigured) || answer.StateDetails != nil ||
		strings.Contains(string(encoded(t, answer)), "state_details") {
		t.Errorf("NetFlow not configured answered %s", encoded(t, answer))
	}
	measured := newMeasureWorld(t)
	measured.configureNetflow(t, "example_if_a")
	measured.measureEverything(t)
	answer = measured.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
	if answer.StateDetails != nil || strings.Contains(string(encoded(t, answer)), "state_details") {
		t.Errorf("a netflow answer carries %s", encoded(t, answer))
	}
	hidden := newMeasureWorld(t)
	hidden.configureNetflow(t, "example_if_a", "example_if_b", "example_if_c")
	answer = hidden.respond(t, "interface_volume_ranking", map[string]any{"period": "1h"})
	if !capturedInterfacesAre(answer, 3) ||
		!strings.Contains(string(encoded(t, answer)),
			`"state_details":{"observation_point_not_exported":{"captured_interfaces":3}}`) {
		t.Errorf("three captured interfaces answered %s", encoded(t, answer))
	}
}
