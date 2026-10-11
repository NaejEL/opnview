package widgets

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/widget"
)

// host.datasets.latest: a widget reads another plugin's dataset by its data-catalogue name, without
// SQL, and a dataset that is not there is a named absence (the "after the gap build" decision of
// specs/SPEC-backend-completion-plugins.md; docs/plugins.md, "host.datasets").

// latestWorld is an installation whose widget service answers from the data catalogue of the
// built-in plugins and those of a data directory, every plugin table created in its database.
func latestWorld(t *testing.T, dataDir string) (*fixture, *widget.Service) {
	t.Helper()
	database, err := store.Open(context.Background(), storetest.Database(t))
	check(t, err)
	t.Cleanup(func() { _ = database.Close() })
	f := &fixture{db: database, now: fixtureNow}
	f.allReachable(t)
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	check(t, err)
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("a plugin was refused: %+v", refusals)
	}
	catalogue, err := dataset.NewCatalogue(loaded.Datasets())
	check(t, err)
	service, err := widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(f.now, 0) },
		Plugins: loaded.WidgetSources(), Datasets: catalogue})
	check(t, err)
	if refused := service.Refusals(); len(refused) != 0 {
		t.Fatalf("a widget was refused: %+v", refused)
	}
	f.service = service
	return f, service
}

// answerOf asks a service for one widget and returns its data, decoded.
func answerOf(t *testing.T, service *widget.Service, widgetType string, parameters map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(parameters)
	check(t, err)
	response, err := service.Respond(context.Background(), widgetType, string(encoded))
	check(t, err)
	return data(t, response)
}

// pfStateRow is one row of pf_states.rate.
type pfStateRow struct {
	at, span                int64
	device, direction       string
	protocol                string
	port                    any
	forward, reverse, total float64
	states                  int64
}

// insertPfStates writes rows into the pf_states plugin's rate table.
func insertPfStates(t *testing.T, f *fixture, rows ...pfStateRow) {
	t.Helper()
	for _, row := range rows {
		f.exec(t, "INSERT INTO "+store.PluginTableSQLName("pf_states", "rate")+" (sampled_at, span_seconds, "+
			"interface_device, direction, protocol, port, forward_bits_per_second, reverse_bits_per_second, "+
			"bits_per_second, states) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", row.at, row.span, row.device,
			row.direction, row.protocol, row.port, row.forward, row.reverse, row.total, row.states)
	}
}

// currentRatesOf is a traffic_composition answer's current_rates.
func currentRatesOf(t *testing.T, answer map[string]any) map[string]any {
	t.Helper()
	rates, ok := answer["current_rates"].(map[string]any)
	if !ok {
		t.Fatalf("the answer carries no current_rates: %v", answer)
	}
	return rates
}

// memberFigures reads by_member as key -> [forward, reverse, bits, states, sampled_at, span, state].
func memberFigures(t *testing.T, rates map[string]any) (keys []string, figures map[string][]any) {
	t.Helper()
	figures = map[string][]any{}
	members, _ := rates["by_member"].([]any)
	for _, member := range members {
		entry := member.(map[string]any)
		rate := entry["rate"].(map[string]any)
		key := entry["key"].(string)
		keys = append(keys, key)
		figures[key] = []any{rate["forward_bits_per_second"], rate["reverse_bits_per_second"],
			rate["bits_per_second"], rate["states"], rate["sampled_at"], rate["span_seconds"], rate["state"],
			rate["measure"], rate["lower_bound"]}
	}
	return keys, figures
}

// TestTrafficCompositionReadsTheRateByProtocolAndServiceFromPfStatesWhenPresent: with the pf_states
// dataset installed, the protocol and service-port groupings carry the current rate of each member
// from the latest pass alone, the states of the interface a connection enters only (direction in),
// summed over the interfaces; the source names the dataset, its state and its instant.
func TestTrafficCompositionReadsTheRateByProtocolAndServiceFromPfStatesWhenPresent(t *testing.T) {
	t.Parallel()
	f, service := latestWorld(t, "")
	latest := f.now - 30
	insertPfStates(t, f,
		// An older pass, which latest never reads.
		pfStateRow{f.now - 90, 60, "example0", "in", "tcp", 443, 9000, 9000, 18000, 9},
		pfStateRow{latest, 60, "example0", "in", "tcp", 443, 100, 400, 500, 1},
		pfStateRow{latest, 60, "example1", "in", "tcp", 443, 50, 50, 100, 2},
		pfStateRow{latest, 60, "example0", "in", "udp", 53, 10, 20, 30, 1},
		pfStateRow{latest, 60, "example0", "in", "icmp", nil, 1, 1, 2, 1},
		// The same forwarded connection's second state, on the interface it leaves by.
		pfStateRow{latest, 60, "exampleup", "out", "tcp", 443, 100, 400, 500, 1},
	)
	at := float64(latest)
	byProtocol := currentRatesOf(t, answerOf(t, service, "traffic_composition", map[string]any{"group_by": "protocol"}))
	keys, figures := memberFigures(t, byProtocol)
	if !reflect.DeepEqual(keys, []string{"protocol:icmp", "protocol:tcp", "protocol:udp"}) {
		t.Fatalf("the protocol members are %v", keys)
	}
	for key, want := range map[string][]any{
		"protocol:tcp":  {150.0, 450.0, 600.0, 3.0, at, 60.0, "current", "sampled", true},
		"protocol:udp":  {10.0, 20.0, 30.0, 1.0, at, 60.0, "current", "sampled", true},
		"protocol:icmp": {1.0, 1.0, 2.0, 1.0, at, 60.0, "current", "sampled", true},
	} {
		if !reflect.DeepEqual(figures[key], want) {
			t.Errorf("%s: %v, want %v", key, figures[key], want)
		}
	}
	source, _ := byProtocol["by_member_source"].(map[string]any)
	if source["dataset"] != "pf_states.rate" || source["state"] != widget.LatestCurrent || source["at"] != at {
		t.Errorf("the source is %v", source)
	}

	byService := currentRatesOf(t, answerOf(t, service, "traffic_composition",
		map[string]any{"group_by": "service_port"}))
	keys, figures = memberFigures(t, byService)
	if !reflect.DeepEqual(keys, []string{"port:443/tcp", "port:53/udp", "port:not_recorded"}) {
		t.Fatalf("the service members are %v", keys)
	}
	if want := []any{150.0, 450.0, 600.0, 3.0, at, 60.0, "current", "sampled", true}; !reflect.DeepEqual(
		figures["port:443/tcp"], want) {
		t.Errorf("port:443/tcp: %v, want %v", figures["port:443/tcp"], want)
	}

	// The client grouping is not the state table's: its members are traffic/top's, as before.
	byClient := currentRatesOf(t, answerOf(t, service, "traffic_composition", map[string]any{"group_by": "client"}))
	if _, present := byClient["by_member_source"]; present {
		t.Errorf("the client grouping names a pf_states source: %v", byClient)
	}
}

// TestTrafficCompositionKeepsTheNamedAbsenceWithoutPfStatesRows: a pass older than twice its span is
// stale_sample, figures beside it; a dataset with no row within its horizon is no_rows; an
// installation without the pf_states dataset is dataset_absent; and in the last two by_member is
// empty, never a rate of 0.
func TestTrafficCompositionKeepsTheNamedAbsenceWithoutPfStatesRows(t *testing.T) {
	t.Parallel()
	f, service := latestWorld(t, "")
	empty := currentRatesOf(t, answerOf(t, service, "traffic_composition", map[string]any{"group_by": "protocol"}))
	if members, _ := empty["by_member"].([]any); len(members) != 0 {
		t.Errorf("an empty dataset gives members %v", members)
	}
	if source, _ := empty["by_member_source"].(map[string]any); source["state"] != widget.LatestNoRows ||
		source["at"] != nil {
		t.Errorf("an empty dataset's source is %v", source)
	}

	insertPfStates(t, f, pfStateRow{f.now - 1000, 60, "example0", "in", "tcp", 443, 100, 400, 500, 1})
	stale := currentRatesOf(t, answerOf(t, service, "traffic_composition", map[string]any{"group_by": "protocol"}))
	_, figures := memberFigures(t, stale)
	if state := figures["protocol:tcp"]; len(state) == 0 || state[6] != "stale_sample" || state[2] != 500.0 {
		t.Errorf("a pass older than twice its span reads %v", state)
	}

	// Beyond the dataset's own retention (pf_states.rate keeps a week), nothing is the latest.
	insertPfStates(t, f, pfStateRow{f.now - 8*86400, 60, "example0", "in", "udp", 53, 1, 1, 2, 1})
	f.exec(t, "DELETE FROM "+store.PluginTableSQLName("pf_states", "rate")+" WHERE sampled_at = ?", f.now-1000)
	horizon := currentRatesOf(t, answerOf(t, service, "traffic_composition", map[string]any{"group_by": "protocol"}))
	if source, _ := horizon["by_member_source"].(map[string]any); source["state"] != widget.LatestNoRows {
		t.Errorf("a row past the retention horizon was read: %v", horizon)
	}

	core := emptyFixture(t)
	absent := currentRatesOf(t, answerOf(t, core.service, "traffic_composition",
		map[string]any{"group_by": "service_port"}))
	if members, _ := absent["by_member"].([]any); len(members) != 0 {
		t.Errorf("without pf_states the members are %v", members)
	}
	if source, _ := absent["by_member_source"].(map[string]any); source["state"] != widget.LatestDatasetAbsent ||
		source["condition"] != nil {
		t.Errorf("without pf_states the source is %v", source)
	}
}

// The third party's two plugins of TestAThirdPartyWidgetReadsAThirdPartyPluginsDatasetByName: one
// declares a table, the other carries a widget reading it by its catalogue name. Neither is a file
// of the product.
var (
	exampleMeterPlugin = map[string]string{
		"plugin.json": `{"api_version": 1, "name": "example_meter", "version": "0.1.0",
			"display_name": "Example meter", "script": "meter.js",
			"providers": [{"kind": "example_meter_reading", "display_name": "Example meter",
				"read_strategy": "full_reread", "cadence_seconds": 60}],
			"tables": [{"name": "reading", "nature": "sample", "columns": [
				{"name": "read_at", "type": "integer", "role": "time"},
				{"name": "meter", "type": "text"},
				{"name": "site", "type": "text", "nullable": true, "carries_domain": true},
				{"name": "watts", "type": "real", "role": "measure", "unit": "watt"}],
				"retention": {"column": "read_at", "seconds": 86400}}]}`,
		"meter.js": `exports.example_meter_reading = {
  probe: function () { return { result: { probe: "none", state: "reachable", outcome: "healthy_empty" } }; },
  collect: function (host, state) { return { result: { probe: "none", state: "reachable", outcome: "healthy_empty" }, rows: {}, state: state || {} }; }
};
`,
	}
	exampleGaugePlugin = map[string]string{
		"plugin.json": `{"api_version": 1, "name": "example_gauge", "version": "0.1.0",
			"display_name": "Example gauge", "widgets": ["example_gauge"]}`,
		"widgets/example_gauge/manifest.json": `{"manifest_version": 1, "type": "example_gauge", "shape": "figure",
			"parameters": [], "script": "gauge.js"}`,
		"widgets/example_gauge/gauge.js": `
exports.widget = {
  serve: function (host) {
    var meters = host.datasets.latest("example_meter.reading", [{ column: "meter", op: "in", value: ["a", "b"] }]);
    var everything = host.datasets.latest("example_meter.reading");
    var missing = host.datasets.latest("example_absent.reading", null);
    var refused = "";
    try {
      host.datasets.latest("example_meter.reading", [{ column: "no_such_column", op: "eq", value: 1 }]);
    } catch (e) {
      refused = e instanceof TypeError ? "TypeError" : "other";
    }
    var bySite = host.datasets.latest("example_meter.reading", [{ column: "site", op: "eq", value: "example.org" }]);
    return { data: { meters: meters, everything: everything, missing: missing, refused: refused,
      by_site: bySite }, has_rows: meters.rows.length > 0 };
  }
};
`,
	}
)

// TestAThirdPartyWidgetReadsAThirdPartyPluginsDatasetByName is the seam's modularity proof: a widget
// dropped as a folder in a data directory reads, through host.datasets.latest, the dataset another
// dropped plugin declares, editing no file of the product. The latest rows are those of the most
// recent instant the filter keeps; a filter the catalogue refuses is a TypeError the script catches;
// an absent dataset is dataset_absent; under no_domains a domain column is withheld from every row,
// and a filter on one reads nothing.
func TestAThirdPartyWidgetReadsAThirdPartyPluginsDatasetByName(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_meter", exampleMeterPlugin)
	writePlugin(t, dataDir, "example_gauge", exampleGaugePlugin)
	f, service := latestWorld(t, dataDir)
	table := store.PluginTableSQLName("example_meter", "reading")
	insert := func(at int64, meter, site string, watts float64) {
		f.exec(t, "INSERT INTO "+table+" (read_at, meter, site, watts) VALUES (?, ?, ?, ?)", at, meter, site, watts)
	}
	insert(f.now-600, "a", "example.org", 10)
	insert(f.now-300, "a", "example.org", 20)
	insert(f.now-300, "b", "example.net", 30)
	// The newest instant of all, but of a meter the first filter does not keep.
	insert(f.now-60, "c", "example.org", 40)

	answer := answerOf(t, service, "example_gauge", map[string]any{})
	meters := answer["meters"].(map[string]any)
	wantRows := []any{
		map[string]any{"meter": "a", "read_at": float64(f.now - 300), "site": "example.org", "watts": 20.0},
		map[string]any{"meter": "b", "read_at": float64(f.now - 300), "site": "example.net", "watts": 30.0},
	}
	if meters["state"] != widget.LatestCurrent || meters["at"] != float64(f.now-300) ||
		!reflect.DeepEqual(meters["rows"], wantRows) || meters["truncated"] != false {
		t.Errorf("the latest rows of meters a and b are %v", meters)
	}
	if everything := answer["everything"].(map[string]any); everything["at"] != float64(f.now-60) ||
		len(everything["rows"].([]any)) != 1 {
		t.Errorf("with no filter the latest rows are %v", everything)
	}
	if missing := answer["missing"].(map[string]any); missing["state"] != widget.LatestDatasetAbsent ||
		len(missing["rows"].([]any)) != 0 || missing["at"] != nil || missing["dataset"] != "example_absent.reading" {
		t.Errorf("an absent dataset reads %v", missing)
	}
	if answer["refused"] != "TypeError" {
		t.Errorf("a filter on a column the dataset does not declare raised %v", answer["refused"])
	}

	f.exec(t, "UPDATE setting SET value = 'no_domains' WHERE key = 'aggregate_mode'")
	withheld := answerOf(t, service, "example_gauge", map[string]any{})
	meters = withheld["meters"].(map[string]any)
	for _, row := range meters["rows"].([]any) {
		if _, present := row.(map[string]any)["site"]; present {
			t.Errorf("under no_domains a row carries the domain column: %v", row)
		}
	}
	if !reflect.DeepEqual(meters["withheld"], []any{"site"}) {
		t.Errorf("under no_domains the withheld columns are %v", meters["withheld"])
	}
	if bySite := withheld["by_site"].(map[string]any); bySite["state"] != widget.ConditionWithheldByAggregateMode ||
		len(bySite["rows"].([]any)) != 0 {
		t.Errorf("under no_domains a filter on a domain column read %v", bySite)
	}
}
