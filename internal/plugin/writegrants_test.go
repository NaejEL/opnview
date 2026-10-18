package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// Granted writes (ADR 0019): a table's owner names who may write it and on which columns; PostgreSQL
// enforces it, and the loader reports it by name when a granted plugin's statement steps outside.

// ownerManifest is a plugin owning a table of readings whose two placement columns another plugin may
// update, and nothing else.
func ownerManifest(grantee string) string {
	return `{"api_version": 3, "name": "grant_owner", "version": "1.0.0", "display_name": "Owner",
	  "tables": [{"name": "readings",
	              "columns": [{"name": "logged_at", "type": "instant"}, {"name": "value", "type": "integer"},
	                          {"name": "placed_at", "type": "integer", "nullable": true},
	                          {"name": "placement", "type": "text", "nullable": true}],
	              "retention": {"column": "logged_at"},
	              "writable_by": [{"plugin": "` + grantee + `", "columns": ["placed_at", "placement"]}]}],
	  "kinds": [{"kind": "grant_owner", "push": true}],
	  "providers": [{"kind": "grant_owner", "display_name": "Owner", "source": "push"}],
	  "push": [{"name": "readings", "protocol": "syslog", "filter": {"program": ["meter"]},
	            "pattern": "^reading (?P<value>[0-9]+)", "output_table": "readings"}]}`
}

// granteeManifest is a plugin requiring the owner and running one statement on its table.
func granteeManifest(name, statement string) string {
	return `{"api_version": 3, "name": "` + name + `", "version": "1.0.0", "display_name": "Grantee",
	  "requires": ["grant_owner"],
	  "tables": [{"name": "runs", "columns": [{"name": "ran_at", "type": "instant"}], "retention": {"column": "ran_at"}}],
	  "kinds": [{"kind": "` + name + `", "push": true}],
	  "providers": [{"kind": "` + name + `", "display_name": "Grantee", "source": "push"}],
	  "push": [{"name": "ticks", "protocol": "syslog", "filter": {"program": ["tick"]},
	            "pattern": "^tick", "output_table": "runs"}],
	  "statements": ["w.statements.sql"],
	  "derivations": [{"name": "place", "cadence_seconds": 60, "statements": ["place"], "outputs": {}}]}`
}

func TestAGrantedPluginUpdatesTheGrantedColumnsAndNoOther(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "grant_owner", ownerManifest("grant_placer"), nil)
	writePlugin(t, dataDir, "grant_placer", granteeManifest("grant_placer", ""), map[string]string{
		"w.statements.sql": "-- statement: place\nUPDATE plugin_grant_owner.readings SET placed_at = :now, placement = 'inside' " +
			"WHERE placed_at IS NULL"})
	writePlugin(t, dataDir, "grant_wider", granteeManifest("grant_wider", ""), map[string]string{
		"w.statements.sql": "-- statement: place\nUPDATE plugin_grant_owner.readings SET value = 0 WHERE placed_at IS NULL"})
	writePlugin(t, dataDir, "grant_stranger", granteeManifest("grant_stranger", ""), map[string]string{
		"w.statements.sql": "-- statement: place\nUPDATE plugin_grant_owner.readings SET placed_at = :now WHERE placed_at IS NULL"})
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	if refusal := refusalOf(loaded, "grant_owner"); refusal != nil {
		t.Fatalf("the owner was refused: %v", refusal)
	}
	if refusal := refusalOf(loaded, "grant_placer"); refusal != nil {
		t.Fatalf("the plugin granted the two columns was refused: %v", refusal)
	}
	for _, name := range []string{"grant_wider", "grant_stranger"} {
		refusal := refusalOf(loaded, name)
		if refusal == nil || refusal.Code != contract.RefusalStatementRefused || !strings.Contains(refusal.Detail, "place") {
			t.Errorf("%s, which writes what it was not granted, loaded or was refused otherwise: %+v", name, refusal)
		}
	}
}

func TestAGrantNamesAColumnTheTableDeclares(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	manifest := strings.Replace(ownerManifest("grant_placer"), `"columns": ["placed_at", "placement"]`, `"columns": ["colour"]`, 1)
	writePlugin(t, dataDir, "grant_owner", manifest, nil)
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	refusal := refusalOf(loaded, "grant_owner")
	if refusal == nil || refusal.Code != contract.RefusalTableInvalid || !strings.Contains(refusal.Detail, "colour") {
		t.Fatalf("a grant on an undeclared column loaded, or was refused otherwise: %+v", refusal)
	}
}
