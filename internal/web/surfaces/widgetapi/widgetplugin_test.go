package widgetapi_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget"
)

// A third party's widget plugin, written as files into the data directory and nothing else: a table
// of its own, a statement over it, and a script computing a figure the declarative engine cannot --
// a percentile of each target's latencies. No file of opnview is edited and nothing is compiled.
var exampleLatencyPlugin = map[string]string{
	"plugin.json": `{
		"api_version": 1, "name": "example_latency", "version": "0.1.0", "display_name": "Example latency",
		"tables": [{"name": "probe", "nature": "sample",
			"columns": [{"name": "measured_at", "type": "integer", "role": "time"},
				{"name": "target", "type": "text"},
				{"name": "latency_ms", "type": "real", "role": "measure", "unit": "millisecond"}],
			"retention": {"column": "measured_at"}}],
		"widgets": ["example_latency_percentile", "example_reads_accounts"]}`,
	"widgets/example_latency_percentile/manifest.json": `{
		"manifest_version": 1, "type": "example_latency_percentile", "shape": "ranked_bars",
		"windowed": true, "lower_bound": false,
		"parameters": [
			{"key": "period", "kind": "enum", "values": ["1h", "24h"], "default": "24h"},
			{"key": "percentile", "kind": "integer", "min": 1, "max": 99, "default": 50},
			{"key": "compact", "kind": "boolean", "default": false, "class": "rendering"}],
		"queries": ["latency.statements.sql"], "script": "latency.js"}`,
	"widgets/example_latency_percentile/latency.statements.sql": `
-- statement: widget_example_latency_samples
SELECT target, latency_ms FROM plugin_example_latency.probe
WHERE measured_at >= :from AND measured_at < :to
ORDER BY target, latency_ms;
`,
	"widgets/example_latency_percentile/latency.js": `
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var rows = host.query.rows("widget_example_latency_samples", { from: window.from, to: window.to });
    var byTarget = {};
    var order = [];
    rows.forEach(function (row) {
      if (!(row.target in byTarget)) { byTarget[row.target] = []; order.push(row.target); }
      byTarget[row.target].push(row.latency_ms);
    });
    var rank = host.params.integer("percentile");
    var bars = order.map(function (target) {
      var sorted = byTarget[target];
      // The nearest-rank percentile: the smallest sample at or above rank per cent of them.
      var index = Math.max(0, Math.ceil(rank / 100 * sorted.length) - 1);
      return { target: target, samples: sorted.length, value: sorted[index] };
    });
    host.envelope.cover(rows.length > 0 ? window : null);
    return { data: { percentile: rank, bars: bars }, has_rows: bars.length > 0 };
  }
};
`,
	"widgets/example_reads_accounts/manifest.json": `{
		"manifest_version": 1, "type": "example_reads_accounts", "shape": "table",
		"queries": ["accounts.statements.sql"], "script": "accounts.js"}`,
	"widgets/example_reads_accounts/accounts.statements.sql": `
-- statement: widget_example_accounts
SELECT count(*) AS accounts FROM account;
`,
	"widgets/example_reads_accounts/accounts.js": `
exports.widget = { serve: function (host) {
  return { data: host.query.row("widget_example_accounts"), has_rows: true };
} };
`,
}

// TestAThirdPartyWidgetPluginIsServedOverHTTPFromFilesAlone is the proof of B3's format (AC B6): the
// widget plugin above, dropped into a data directory, is loaded by the loader every plugin goes
// through and answered at GET /api/widget/{type} with the full envelope -- its parameters parsed,
// defaulted and reported (unknown, invalid) exactly as a built-in's -- and its script's figure; the
// widget of the same plugin that reads the account table is refused by name (AC B7), and the
// service starts without it.
func TestAThirdPartyWidgetPluginIsServedOverHTTPFromFilesAlone(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	for name, content := range exampleLatencyPlugin {
		path := filepath.Join(dataDir, plugin.PluginsDirectory, "example_latency", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	harness := webtest.NewAdjusted(t, dataDir, true, false, func(options *web.Options) {
		loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: options.Store})
		if err != nil {
			t.Fatal(err)
		}
		if refusals := loaded.Refusals(); len(refusals) != 0 {
			t.Fatalf("the plugin was refused: %+v", refusals)
		}
		options.Plugins = loaded
	})
	harness.CompleteSetup()

	refused := harness.Server.Widgets().Refusals()
	if len(refused) != 1 || refused[0].Type != "example_reads_accounts" || refused[0].Code != widget.RefusalQueryReadsCredential {
		t.Errorf("the widget reading the accounts was not refused by name: %+v", refused)
	}
	if answer := harness.Get(widget.PathPrefix + "example_reads_accounts"); answer.StatusCode != http.StatusNotFound {
		t.Errorf("the refused widget answered %d", answer.StatusCode)
	}

	now := harness.Clock.Now().Unix()
	for index, sample := range []struct {
		target  string
		latency float64
	}{{"alpha", 12}, {"alpha", 30}, {"alpha", 18}, {"alpha", 50}, {"beta", 7.5}} {
		if _, err := harness.Store.DB().ExecContext(context.Background(),
			"INSERT INTO plugin_example_latency.probe (measured_at, target, latency_ms) VALUES (?, ?, ?)",
			now-600-int64(index), sample.target, sample.latency); err != nil {
			t.Fatal(err)
		}
	}

	answer := harness.Get(widgetPath(t, "example_latency_percentile",
		map[string]any{"percentile": 75, "period": "1y", "colour": "red"}))
	condition, body := decodeCondition(t, answer, http.StatusOK)
	if condition != widget.ConditionReachable {
		t.Errorf("the condition is %q", condition)
	}
	parameters, _ := body["parameters"].(map[string]any)
	if parameters["percentile"] != float64(75) || parameters["period"] != "24h" || parameters["compact"] != false {
		t.Errorf("the effective parameters are %v", parameters)
	}
	if unknown, _ := body["unknown_parameters"].(map[string]any); unknown["colour"] != "red" {
		t.Errorf("the unknown key was not reported: %v", body["unknown_parameters"])
	}
	invalid, _ := body["invalid_parameters"].([]any)
	if len(invalid) != 1 || invalid[0].(map[string]any)["path"] != "period" {
		t.Errorf("the invalid period was not reported: %v", body["invalid_parameters"])
	}
	window, _ := body["window"].(map[string]any)
	if window == nil || window["coverage"] != widget.CoverageFull {
		t.Errorf("the window is %v", body["window"])
	}
	data, _ := body["data"].(map[string]any)
	bars, _ := data["bars"].([]any)
	if data["percentile"] != float64(75) || len(bars) != 2 {
		t.Fatalf("the data is %v", body["data"])
	}
	alpha, _ := bars[0].(map[string]any)
	beta, _ := bars[1].(map[string]any)
	// alpha's samples are 12, 18, 30 and 50: the 75th percentile by nearest rank is the third.
	if alpha["target"] != "alpha" || alpha["samples"] != float64(4) || alpha["value"] != float64(30) ||
		beta["target"] != "beta" || beta["value"] != 7.5 {
		t.Errorf("the percentiles are %v", bars)
	}

	listed := false
	for _, widgetType := range harness.Server.Widgets().OperatorTypes() {
		listed = listed || widgetType == "example_latency_percentile"
	}
	if !listed {
		t.Errorf("the third party's widget is not listed with the data directory's: %v",
			harness.Server.Widgets().OperatorTypes())
	}
}
