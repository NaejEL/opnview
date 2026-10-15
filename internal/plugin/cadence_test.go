package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestACadenceOverrideIsASettingNeverBelowItsBound: a provider stating its own cadence offers the
// operator its override among the plugin's settings, listed with the declared cadence as its
// default, written through the same validation, and refused below the declared lower bound.
func TestACadenceOverrideIsASettingNeverBelowItsBound(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	directory := filepath.Join(dataDir, PluginsDirectory, "example_inventory")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"plugin.json": `{"api_version": 1, "name": "example_inventory", "version": "0.1.0",
			"display_name": "Example inventory", "script": "example_inventory.js",
			"endpoints": [{"name": "peers", "path": "/api/exampleinventory/peers/get", "method": "GET"}],
			"providers": [{"kind": "reconciled_state", "display_name": "Example inventory",
				"cadence_seconds": 600, "min_cadence_seconds": 120}]}`,
		"example_inventory.js": `exports.reconciled_state = {
			probe: function () { return { result: { probe: "peers", state: "unavailable", outcome: "plugin_not_installed" } }; },
			snapshot: function () { return { result: { probe: "peers", state: "unavailable", outcome: "plugin_not_installed" } }; } };`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	loaded, err := Load(ctx, Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	plugin, found := loaded.Plugin("example_inventory")
	if !found {
		t.Fatalf("the plugin was refused: %v", loaded.Refusals())
	}
	key := "cadence__reconciled_state__example_inventory"
	values, err := plugin.SettingValues(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Key != key || values[0].Value != float64(600) || values[0].Set ||
		values[0].Min == nil || *values[0].Min != 120 {
		t.Fatalf("the settings are %+v", values)
	}
	var refusal *SettingRefusal
	if err := plugin.WriteSettings(ctx, database, nil, map[string]any{key: 60}, 1790000000); !errors.As(err, &refusal) ||
		refusal.Code != SettingRefusalOutOfRange {
		t.Errorf("an override below the bound gave %v", err)
	}
	if err := plugin.WriteSettings(ctx, database, nil, map[string]any{key: 300}, 1790000000); err != nil {
		t.Fatal(err)
	}
	stored, present, err := database.Setting(ctx, "example_inventory_"+key)
	if err != nil || !present || stored != "300" {
		t.Errorf("the override is stored as %q (present %v, %v)", stored, present, err)
	}
}

// TestATriggerNamesAPublishedChannelOnAProviderWithItsOwnCadence: triggered_by is a provider's that
// states its own cadence, and every name in it is a channel the core publishes, alone or with a
// subject (P7).
func TestATriggerNamesAPublishedChannelOnAProviderWithItsOwnCadence(t *testing.T) {
	t.Parallel()
	declaration := contract.ProviderDeclaration{Kind: "example_kind", CadenceSeconds: 600, MinCadenceSeconds: 60,
		TriggeredBy: []string{sqldb.ChannelCollectionPass + ":dhcp_lease", sqldb.ChannelAggregateRefresh}}
	if refusal := checkTriggers("example", declaration, false); refusal != nil {
		t.Errorf("published channels were refused: %v", refusal)
	}
	for _, name := range []string{"opnview_unknown", sqldb.ChannelPluginPass + ":Not A Name", ""} {
		declaration.TriggeredBy = []string{name}
		if refusal := checkTriggers("example", declaration, false); refusal == nil ||
			refusal.Code != contract.RefusalTriggerInvalid {
			t.Errorf("triggered_by %q gave %v", name, refusal)
		}
	}
	declaration.TriggeredBy = []string{sqldb.ChannelAggregateRefresh}
	if refusal := checkTriggers("example", declaration, true); refusal == nil || refusal.Code != contract.RefusalTriggerInvalid {
		t.Errorf("a provider of a surveyed kind named a trigger: %v", refusal)
	}
}
