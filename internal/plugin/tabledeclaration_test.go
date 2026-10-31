package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The table declaration as extended by ADR 0019: an identity column, a generated column, a CHECK, a
// foreign key to a required plugin's table and a partial index, all declared; and the expressions a
// declaration may not carry.

// interfacesManifest is a plugin owning a table of interfaces another plugin references.
const interfacesManifest = `{"api_version": 3, "name": "decl_interfaces", "version": "1.0.0", "display_name": "Interfaces",
  "tables": [{"name": "interface", "columns": [{"name": "id", "type": "integer", "identity": true},
                                               {"name": "device", "type": "text"}],
              "primary_key": ["id"], "indexes": [{"columns": ["device"], "unique": true}]}],
  "widgets": ["decl_interfaces"]}`

// recordsManifest is a plugin whose records table uses every extension, with the check given.
func recordsManifest(check string) string {
	return `{"api_version": 3, "name": "decl_records", "version": "1.0.0", "display_name": "Records",
	  "requires": ["decl_interfaces"],
	  "tables": [{"name": "record",
	              "columns": [{"name": "id", "type": "integer", "identity": true},
	                          {"name": "observed_at", "type": "integer"},
	                          {"name": "interface_id", "type": "integer", "nullable": true,
	                           "references": "decl_interfaces.interface(id)"},
	                          {"name": "action", "type": "text"},
	                          {"name": "bytes", "type": "integer", "role": "measure", "unit": "byte"},
	                          {"name": "refused", "type": "boolean", "generated": "action IN ('block', 'reject')"}],
	              "primary_key": ["id", "observed_at"],
	              "retention": {"column": "observed_at"},
	              "indexes": [{"columns": ["observed_at"], "where": "action IN ('block', 'reject')"}],
	              "checks": [{"name": "action_known", "expression": "` + check + `"}]}],
	  "widgets": ["decl_records"]}`
}

// writeDeclaredPlugin writes a plugin whose manifest declares one widget named as the plugin, with the
// smallest widget there is, so the manifest's shape check passes and the tables are what is tested.
func writeDeclaredPlugin(t *testing.T, dataDir, name, manifest string) {
	t.Helper()
	widget := `{"manifest_version": 1, "type": "` + name + `", "shape": "table", "windowed": false, "parameters": [],
	  "queries": ["q.statements.sql"], "script": "w.js"}`
	writePlugin(t, dataDir, name, manifest, map[string]string{
		"widgets/" + name + "/manifest.json":    widget,
		"widgets/" + name + "/q.statements.sql": "-- statement: widget_" + name + "\nSELECT 1 AS one",
		"widgets/" + name + "/w.js":             "exports.widget = {serve: function (host) { return {data: {}, has_rows: false}; }};",
	})
}

func TestATableDeclaresIdentityGeneratedCheckReferencesAndAPartialIndex(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	writeDeclaredPlugin(t, dataDir, "decl_interfaces", interfacesManifest)
	writeDeclaredPlugin(t, dataDir, "decl_records", recordsManifest("action IN ('pass', 'block', 'reject')"))
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"decl_interfaces", "decl_records"} {
		if refusal := refusalOf(loaded, name); refusal != nil {
			t.Fatalf("%s was refused: %v", name, refusal)
		}
	}
	ctx := context.Background()
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_interfaces.interface (device) VALUES ('igb0')"); err != nil {
		t.Fatalf("inserting an interface: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_records.record (observed_at, interface_id, action, bytes) "+
		"VALUES (100, 1, 'block', 40), (101, NULL, 'pass', 60)"); err != nil {
		t.Fatalf("inserting records: %v", err)
	}
	var refused int
	if err := database.SQL.QueryRowContext(ctx, "SELECT count(*) FROM plugin_decl_records.record WHERE refused").Scan(&refused); err != nil {
		t.Fatal(err)
	}
	if refused != 1 {
		t.Errorf("the generated column marks %d refusals, not 1", refused)
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_records.record (observed_at, action, bytes) "+
		"VALUES (102, 'teleport', 1)"); err == nil || !strings.Contains(err.Error(), "record_action_known") {
		t.Errorf("a row breaking the check was accepted, or refused otherwise: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_records.record (observed_at, interface_id, action, bytes) "+
		"VALUES (103, 99, 'pass', 1)"); err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Errorf("a row naming an unknown interface was accepted, or refused otherwise: %v", err)
	}
	var partial bool
	if err := database.SQL.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'plugin_decl_records' "+
		"AND indexdef LIKE '%WHERE%')").Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Error("no partial index was created")
	}
}

func TestADeclarationExpressionMayNotWriteOrSelect(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writeDeclaredPlugin(t, dataDir, "decl_interfaces", interfacesManifest)
	writeDeclaredPlugin(t, dataDir, "decl_records", recordsManifest("(SELECT count(*) FROM account) = 0"))
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "decl_records")
	if refusal == nil || refusal.Code != contract.RefusalTableInvalid || !strings.Contains(refusal.Detail, "action_known") {
		t.Fatalf("a check with a subquery loaded, or was refused otherwise: %+v", refusal)
	}
}

func TestAReferenceToAPluginNotRequiredIsRefused(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	manifest := strings.Replace(recordsManifest("true"), `"requires": ["decl_interfaces"],`, ``, 1)
	writeDeclaredPlugin(t, dataDir, "decl_records", manifest)
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "decl_records")
	if refusal == nil || refusal.Code != contract.RefusalTableInvalid || !strings.Contains(refusal.Detail, "does not require") {
		t.Fatalf("a reference to a plugin not required loaded, or was refused otherwise: %+v", refusal)
	}
}

// TestAForeignKeyMayCascadeOnDelete is ADR 0020's restored keys: on_delete "cascade" deletes the
// referencing rows with the referenced one, the default refuses the deletion, and on_delete is refused
// where it names another value or references nothing.
func TestAForeignKeyMayCascadeOnDelete(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	writeDeclaredPlugin(t, dataDir, "decl_interfaces", interfacesManifest)
	cascading := strings.Replace(recordsManifest("true"), `"references": "decl_interfaces.interface(id)"`,
		`"references": "decl_interfaces.interface(id)", "on_delete": "cascade"`, 1)
	writeDeclaredPlugin(t, dataDir, "decl_records", cascading)
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"decl_interfaces", "decl_records"} {
		if refusal := refusalOf(loaded, name); refusal != nil {
			t.Fatalf("%s was refused: %v", name, refusal)
		}
	}
	ctx := context.Background()
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_interfaces.interface (device) VALUES ('igb0'), ('igb1')"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_records.record (observed_at, interface_id, action, bytes) "+
		"VALUES (100, 1, 'pass', 40), (101, 2, 'pass', 60)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM plugin_decl_interfaces.interface WHERE device = 'igb0'"); err != nil {
		t.Fatalf("deleting a referenced interface under cascade: %v", err)
	}
	var left int
	if err := database.SQL.QueryRowContext(ctx, "SELECT count(*) FROM plugin_decl_records.record").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Errorf("%d records are left after the cascade, not 1", left)
	}
	for name, change := range map[string]string{
		"a value that is not cascade": `"references": "decl_interfaces.interface(id)", "on_delete": "restrict"`,
		"on_delete with no reference": `"on_delete": "cascade"`,
	} {
		wrong := strings.Replace(recordsManifest("true"), `"references": "decl_interfaces.interface(id)"`, change, 1)
		wrongDir := t.TempDir()
		writeDeclaredPlugin(t, wrongDir, "decl_interfaces", interfacesManifest)
		writeDeclaredPlugin(t, wrongDir, "decl_records", wrong)
		refusing, err := plugin.Load(context.Background(), plugin.Options{DataDir: wrongDir})
		if err != nil {
			t.Fatal(err)
		}
		refusal := refusalOf(refusing, "decl_records")
		if refusal == nil || refusal.Code != contract.RefusalTableInvalid || !strings.Contains(refusal.Detail, "on_delete") {
			t.Errorf("%s: loaded, or refused otherwise: %+v", name, refusal)
		}
	}
}
