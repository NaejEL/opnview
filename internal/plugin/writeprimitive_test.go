package plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The write primitive (ADR 0018): what PostgreSQL refuses a plugin's SQL, the loader refuses by name when
// the plugin loads, and a plugin whose requirement is missing, refused or circular is refused naming it.

// writePlugin writes one plugin directory into a data directory: its manifest, and the files given.
func writePlugin(t *testing.T, dataDir, name, manifest string, files map[string]string) {
	t.Helper()
	directory := filepath.Join(dataDir, plugin.PluginsDirectory, name)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, contract.ManifestFile), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	for file, text := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, file)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, file), []byte(text), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// refusalOf is the refusal of one plugin, or nil when it loaded.
func refusalOf(loaded *plugin.Loaded, name string) *contract.Refusal {
	for _, refusal := range loaded.Refusals() {
		if refusal.Plugin == name {
			copied := refusal
			return &copied
		}
	}
	return nil
}

// meterManifest is a plugin with a table, a push entry (the provider a derivation needs), and the statement
// and derivation given.
func meterManifest(name, statementsFile, derivation string) string {
	return `{"api_version": 3, "name": "` + name + `", "version": "1.0.0", "display_name": "Meter",
	  "tables": [{"name": "readings", "columns": [{"name": "logged_at", "type": "instant"}, {"name": "value", "type": "integer"}],
	              "retention": {"column": "logged_at"}},
	             {"name": "totals", "columns": [{"name": "meter", "type": "text"}, {"name": "total", "type": "integer"}],
	              "primary_key": ["meter"]}],
	  "kinds": [{"kind": "` + name + `", "push": true}],
	  "providers": [{"kind": "` + name + `", "display_name": "Meter", "source": "push"}],
	  "push": [{"name": "readings", "protocol": "syslog", "filter": {"program": ["meter"]},
	            "pattern": "^reading (?P<value>[0-9]+)", "output_table": "readings"}],
	  "statements": ["` + statementsFile + `"],
	  "derivations": [` + derivation + `]}`
}

func TestAStatementWritingOutsideThePluginSchemaRefusesThePluginByName(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "meter_out", meterManifest("meter_out", "w.statements.sql",
		`{"name": "leak", "cadence_seconds": 60, "statements": ["leak"], "outputs": {}}`),
		map[string]string{"w.statements.sql": "-- statement: leak\nDELETE FROM interface WHERE id = :now"})
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "meter_out")
	if refusal == nil || refusal.Code != contract.RefusalStatementRefused || !strings.Contains(refusal.Detail, "leak") {
		t.Fatalf("a statement deleting from a core table loaded, or was refused otherwise: %+v", refusal)
	}
}

func TestAStatementReadingACredentialTableRefusesThePlugin(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "meter_peek", meterManifest("meter_peek", "w.statements.sql",
		`{"name": "peek", "cadence_seconds": 60, "statements": ["peek"], "outputs": {"tables": ["totals"]}}`),
		map[string]string{"w.statements.sql": "-- statement: peek\nINSERT INTO plugin_meter_peek.totals (meter, total) " +
			"SELECT login, count(*) FROM account GROUP BY login"})
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "meter_peek")
	if refusal == nil || refusal.Code != contract.RefusalStatementRefused {
		t.Fatalf("a statement reading account loaded, or was refused otherwise: %+v", refusal)
	}
}

func TestAStatementWritingItsOwnSchemaLoadsAndARelationOutsideItIsRefused(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	own := "-- statement: total\nINSERT INTO plugin_meter_own.totals (meter, total) " +
		"SELECT 'all', coalesce(sum(value), 0) FROM plugin_meter_own.readings " +
		"ON CONFLICT (meter) DO UPDATE SET total = excluded.total"
	writePlugin(t, dataDir, "meter_own", meterManifest("meter_own", "w.statements.sql",
		`{"name": "total", "cadence_seconds": 60, "statements": ["total"], "outputs": {"tables": ["totals"]}}`),
		map[string]string{"w.statements.sql": own})
	manifest := strings.Replace(meterManifest("meter_view", "w.statements.sql",
		`{"name": "total", "cadence_seconds": 60, "statements": ["total"], "outputs": {"tables": ["totals"]}}`),
		`"statements": ["w.statements.sql"],`, `"statements": ["w.statements.sql"], "relations": ["v.relations.sql"],`, 1)
	writePlugin(t, dataDir, "meter_view", manifest, map[string]string{
		"w.statements.sql": strings.ReplaceAll(own, "meter_own", "meter_view"),
		"v.relations.sql":  "CREATE OR REPLACE VIEW opnview.meter_view_totals AS SELECT 1 AS one"})
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	if refusal := refusalOf(loaded, "meter_own"); refusal != nil {
		t.Fatalf("a statement writing the plugin's own schema was refused: %v", refusal)
	}
	refusal := refusalOf(loaded, "meter_view")
	if refusal == nil || refusal.Code != contract.RefusalRelationRefused || !strings.Contains(refusal.Detail, "opnview.meter_view_totals") {
		t.Fatalf("a view created in the core schema loaded, or was refused otherwise: %+v", refusal)
	}
}

func TestRequiresOrdersTheLoadAndAMissingOrCircularRequirementRefuses(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	widget := func(name, requires string) string {
		return `{"api_version": 3, "name": "` + name + `", "version": "1.0.0", "display_name": "W", "widgets": ["` + name + `"],
		  "requires": [` + requires + `]}`
	}
	writeWidget := func(name, requires string) {
		writePlugin(t, dataDir, name, widget(name, requires), nil)
		directory := filepath.Join(dataDir, plugin.PluginsDirectory, name, "widgets", name)
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		manifest := `{"manifest_version": 1, "type": "` + name + `", "shape": "table", "windowed": false, "parameters": [],
		  "queries": ["q.statements.sql"], "script": "w.js"}`
		for file, text := range map[string]string{"manifest.json": manifest,
			"q.statements.sql": "-- statement: widget_" + name + "\nSELECT 1 AS one",
			"w.js":             "exports.widget = {serve: function (host) { return {data: {}, has_rows: false}; }};"} {
			if err := os.WriteFile(filepath.Join(directory, file), []byte(text), 0o640); err != nil {
				t.Fatal(err)
			}
		}
	}
	// a_last requires z_first: z_first loads before it although its name sorts after.
	writeWidget("a_last", `"z_first"`)
	writeWidget("z_first", ``)
	// b_lonely requires a plugin that is not there; c_cycle and d_cycle require each other.
	writeWidget("b_lonely", `"nobody"`)
	writeWidget("c_cycle", `"d_cycle"`)
	writeWidget("d_cycle", `"c_cycle"`)
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, member := range loaded.Plugins() {
		if member.Origin == plugin.OriginDataDirectory {
			names = append(names, member.Name)
		}
	}
	if strings.Join(names, ",") != "z_first,a_last" {
		t.Fatalf("the data directory's plugins loaded as %v, not z_first then a_last", names)
	}
	for _, name := range []string{"b_lonely", "c_cycle", "d_cycle"} {
		refusal := refusalOf(loaded, name)
		if refusal == nil || refusal.Code != contract.RefusalRequiresMissing {
			t.Errorf("%s loaded, or was refused otherwise: %+v", name, refusal)
		}
	}
}

func TestAnAfterDerivationsTriggerNamesADerivationOfARequiredPlugin(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "meter_after", meterManifest("meter_after", "w.statements.sql",
		`{"name": "total", "after_derivations": ["somebody/else"], "statements": ["total"], "outputs": {"tables": ["totals"]}}`),
		map[string]string{"w.statements.sql": "-- statement: total\nSELECT 1"})
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "meter_after")
	if refusal == nil || refusal.Code != contract.RefusalDerivationInvalid || !strings.Contains(refusal.Detail, "somebody/else") {
		t.Fatalf("a derivation following a plugin the manifest does not require loaded, or was refused otherwise: %+v", refusal)
	}
}
