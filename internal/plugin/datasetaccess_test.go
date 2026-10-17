package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The fixture of the dataset tests: a third party's plugin reading, from its probe, what its own `mode`
// table tells it to read, and answering what it read as JSON in the result's detail.
const exampleReaderManifest = `{"api_version": 2, "name": "example_reader", "version": "0.1.0",
	"display_name": "Example reader", "script": "example_reader.js",
	"datasets": ["leg", "interface", "this_firewall_address", "provider_value", "account"],
	"endpoints": [{"name": "readings", "path": "/api/examplereader/readings/get", "method": "GET"}],
	"tables": [
		{"name": "mark", "columns": [{"name": "n", "type": "integer"}], "primary_key": ["n"]},
		{"name": "mode", "columns": [{"name": "n", "type": "integer"}], "primary_key": ["n"]}],
	"providers": [{"kind": "example_reading", "display_name": "Example reading", "cadence_seconds": 600}]}`

const exampleReaderScript = `
function answer(detail) {
  return { result: { probe: "readings", state: "unavailable", outcome: "plugin_not_installed",
    detail: JSON.stringify(detail) } };
}
exports.example_reading = {
  probe: function (host) {
    var mode = host.datasets.rows("example_reader.mode").rows[0].n;
    if (mode > 0) {
      var keys = [];
      for (var i = 1; i <= mode; i++) { keys.push(i); }
      var read = host.datasets.rows("example_reader.mark", { keys: { columns: ["n"], values: keys } });
      return answer({ state: read.state, rows: read.rows.length });
    }
    if (mode === -1) {
      host.datasets.rows(["traf", "fic"].join(""));
    }
    if (mode === -2) {
      var account = host.datasets.rows("account");
      return answer({ state: account.state, rows: account.rows.length });
    }
    if (mode === -3) {
      host.query("SELECT key FROM setting", {});
    }
    var legs = host.datasets.rows("leg", { filters: [{ column: "direction", op: "in", value: ["in", "out"] }] });
    var interfaces = host.datasets.rows("interface");
    var addresses = host.datasets.rows("this_firewall_address", { columns: ["address", "interface"] });
    var values = host.datasets.rows("provider_value", { filters: [{ column: "kind", op: "eq", value: "firewall_log" }] });
    var timeouts = {};
    values.rows.forEach(function (row) { timeouts[row.name] = row.integer_value; });
    return answer({ legs: legs.rows.length, interfaces: interfaces.rows.length, addresses: addresses.rows.length,
      timeouts: timeouts, condition: legs.condition });
  },
  collect: function () { return answer({}); }
};
`

// TestAPluginReadsTheDatasetsItDeclaresSetWiseAndNothingElse is PA-AC5 and the reading half of PA-AC10:
// a script reading a dataset its manifest does not declare is refused with dataset_not_declared -- at load
// when it names it by a literal, at the call when it computes the name; account, session,
// encrypted_credential and setting are reachable by neither host.datasets nor host.query; a set-filtered
// read of 10 keys and one of 1 000 run the same catalogue statements, one text; and the core datasets a
// derivation joining another source needs -- the logged legs, the interfaces, this firewall's addresses --
// and the provider_value dataset, through which another plugin reads pf's state timeouts, answer.
//
// Not parallel: it reads the process's catalogue statement counter, which only the sequential phase of a
// package's tests leaves undisturbed.
func TestAPluginReadsTheDatasetsItDeclaresSetWiseAndNothingElse(t *testing.T) {
	ctx := context.Background()
	network := storetest.NewNetwork(t, 2, 2)
	network.Insert(t, network.FlowsFor(6, 600))
	database := network.DB
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_reader", map[string]string{"plugin.json": exampleReaderManifest,
		"example_reader.js": exampleReaderScript})
	writePlugin(t, dataDir, "example_undeclared", map[string]string{
		"plugin.json": `{"api_version": 2, "name": "example_undeclared", "version": "0.1.0",
			"display_name": "Example undeclared", "widgets": ["example_undeclared"]}`})
	if err := os.MkdirAll(filepath.Join(dataDir, PluginsDirectory, "example_undeclared", "widgets",
		"example_undeclared"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, PluginsDirectory, "example_undeclared", "widgets",
		"example_undeclared", "reader.js"), []byte(`var legs = host.datasets.rows("leg");`), 0o640); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	var undeclared *contract.Refusal
	for index, refusal := range loaded.Refusals() {
		if refusal.Plugin == "example_undeclared" {
			undeclared = &loaded.Refusals()[index]
		}
	}
	if undeclared == nil || undeclared.Code != contract.RefusalDatasetNotDeclared || !strings.Contains(undeclared.Detail, `"leg"`) {
		t.Fatalf("a literal read of an undeclared dataset was refused as %+v, not dataset_not_declared", undeclared)
	}
	registration, found := loaded.Set.Lookup("example_reading", "example_reader")
	if !found {
		t.Fatalf("the reader was refused: %+v", loaded.Refusals())
	}
	if _, err := database.SQL.ExecContext(ctx, `INSERT INTO plugin_example_reader.mark (n) SELECT generate_series(1, 1000)`); err != nil {
		t.Fatal(err)
	}
	// pf's provider active, as the probe round makes it on a firewall with a filter log.
	if _, err := database.SQL.ExecContext(ctx, `UPDATE provider SET is_active = true WHERE kind = 'firewall_log' AND provider_key = 'pf'`); err != nil {
		t.Fatal(err)
	}
	session := provider.NewHost(nil, nil, func() int64 { return network.Now })
	probe := func(mode int) (map[string]any, error) {
		t.Helper()
		if _, err := database.SQL.ExecContext(ctx, `DELETE FROM plugin_example_reader.mode`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.ExecContext(ctx, `INSERT INTO plugin_example_reader.mode (n) VALUES ($1)`, mode); err != nil {
			t.Fatal(err)
		}
		_, result, err := registration.Implementation.Call(ctx, session, "probe")
		if err != nil {
			return nil, err
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(result.Detail), &detail); err != nil {
			t.Fatalf("the probe's detail %q is not the script's JSON: %v", result.Detail, err)
		}
		return detail, nil
	}

	// Set-based: the same statements, and the same text, for 10 keys and 1 000.
	before := reads.CatalogueStatementCount()
	small, err := probe(10)
	if err != nil {
		t.Fatal(err)
	}
	afterSmall := reads.CatalogueStatementCount()
	texts := database.Reader.RunCount()
	large, err := probe(1000)
	if err != nil {
		t.Fatal(err)
	}
	afterLarge := reads.CatalogueStatementCount()
	if small["rows"] != float64(10) || large["rows"] != float64(1000) || small["state"] != "current" {
		t.Fatalf("the keyed reads answered %v and %v, not 10 and 1 000 current rows", small, large)
	}
	if afterSmall-before != afterLarge-afterSmall || afterSmall-before != 2 {
		t.Errorf("10 keys ran %d catalogue statements and 1 000 ran %d: a set read is one statement whatever its size",
			afterSmall-before, afterLarge-afterSmall)
	}
	if database.Reader.RunCount() != texts {
		t.Errorf("the read of 1 000 keys ran a text the read of 10 did not: the keys are not one bound array")
	}

	// A computed name the manifest does not declare: a TypeError carrying the closed code.
	if _, err := probe(-1); err == nil || !strings.Contains(err.Error(), contract.RefusalDatasetNotDeclared) {
		t.Errorf("a computed undeclared read answered %v, not %s", err, contract.RefusalDatasetNotDeclared)
	}
	// The secret tables are no dataset, even declared, and no plugin query reads them.
	account, err := probe(-2)
	if err != nil || account["state"] != "unknown_dataset" || account["rows"] != float64(0) {
		t.Errorf("a declared read of account answered %v, %v: it must be the named absence unknown_dataset", account, err)
	}
	for _, table := range []string{"setting", "account", "session", "encrypted_credential"} {
		script := strings.ReplaceAll(exampleReaderScript, `SELECT key FROM setting`, "SELECT * FROM "+table)
		if table != "setting" {
			reloaded := t.TempDir()
			writePlugin(t, reloaded, "example_reader", map[string]string{"plugin.json": exampleReaderManifest,
				"example_reader.js": script})
			again, err := Load(ctx, Options{DataDir: reloaded, Store: database})
			if err != nil {
				t.Fatal(err)
			}
			registration, _ = again.Set.Lookup("example_reading", "example_reader")
		}
		if _, err := probe(-3); err == nil {
			t.Errorf("a plugin query read %s", table)
		}
	}

	// The core datasets a join reads, and pf's state timeouts through provider_value.
	core, err := probe(-4)
	if err != nil {
		t.Fatal(err)
	}
	if core["legs"] != float64(6) || core["interfaces"] == float64(0) || core["addresses"] == float64(0) {
		t.Errorf("the core datasets answered %v: six legs, the interfaces and this firewall's addresses expected", core)
	}
	timeouts, _ := core["timeouts"].(map[string]any)
	if len(timeouts) != 7 || timeouts["state_timeout_tcp_established_seconds"] == nil {
		t.Errorf("provider_value gave another plugin pf's state timeouts as %v, not the seven pf exports", timeouts)
	}
}
