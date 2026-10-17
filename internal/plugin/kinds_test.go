package plugin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// writePlugin writes one plugin directory of a data directory.
func writePlugin(t *testing.T, dataDir, name string, files map[string]string) {
	t.Helper()
	directory := filepath.Join(dataDir, PluginsDirectory, name)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(directory, file), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// exampleKindsManifest declares two kinds of the plugin's own: example_gauge concurrent, pushed and not
// bindable, example_level with the defaults.
const exampleKindsManifest = `{"api_version": 2, "name": "example_kinds", "version": "0.1.0",
	"display_name": "Example kinds", "script": "example_kinds.js",
	"tables": [{"name": "readings", "columns": [{"name": "read_at", "type": "instant"},
		{"name": "value", "type": "integer", "role": "measure"}], "retention": {"column": "read_at"}}],
	"kinds": [{"kind": "example_gauge", "concurrent": true, "push": true, "bindable": false}],
	"providers": [
		{"kind": "example_gauge", "key": "example_gauge_one", "display_name": "Gauge one", "source": "push"},
		{"kind": "example_gauge", "key": "example_gauge_two", "display_name": "Gauge two", "cadence_seconds": 60},
		{"kind": "example_level", "key": "example_level_one", "display_name": "Level one", "cadence_seconds": 60},
		{"kind": "example_level", "key": "example_level_two", "display_name": "Level two", "cadence_seconds": 60}],
	"push": [{"name": "readings", "protocol": "syslog", "filter": {"program": ["example-gauge"]},
		"pattern": "^(?P<value>\\d+)$", "output_table": "readings"}]}`

const exampleKindsScript = `exports.example_level = {
	probe: function () { return { result: { probe: "example_kinds.js", state: "unavailable", outcome: "plugin_not_installed" } }; },
	collect: function () { return { result: { probe: "example_kinds.js", state: "unavailable", outcome: "plugin_not_installed" } }; } };
exports.example_gauge = exports.example_level;`

// TestAPluginKindGetsItsAttributesWithNoGoChange is PA-AC2's behaviour half: a plugin declaring kinds
// of its own gets concurrency, push and bindability from its manifest alone -- the database lets two
// providers of its concurrent kind be active and refuses a second of its exclusive one, its pushed kind
// takes `"source": "push"` providers and a kind without push does not, and a widget may bind exactly
// the bindable kinds.
func TestAPluginKindGetsItsAttributesWithNoGoChange(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_kinds", map[string]string{"plugin.json": exampleKindsManifest,
		"example_kinds.js": exampleKindsScript})
	// The same push provider, on a kind that does not declare push, is refused.
	writePlugin(t, dataDir, "example_unpushed", map[string]string{"plugin.json": strings.NewReplacer(
		`"name": "example_kinds"`, `"name": "example_unpushed"`, `"example_kinds.js"`, `"example_unpushed.js"`,
		`example_gauge`, `example_unpushed_gauge`, `example_level`, `example_unpushed_level`,
		`"concurrent": true, "push": true,`, `"concurrent": true,`).Replace(exampleKindsManifest),
		"example_unpushed.js": strings.ReplaceAll(exampleKindsScript, "example_level", "example_unpushed_level")})
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	loaded, err := Load(ctx, Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := loaded.Plugin("example_kinds"); !found {
		t.Fatalf("the plugin was refused: %v", loaded.Refusals())
	}
	refusedUnpushed := false
	for _, refusal := range loaded.Refusals() {
		if refusal.Plugin == "example_unpushed" && refusal.Code == contract.RefusalManifestInvalid {
			refusedUnpushed = true
		}
	}
	if !refusedUnpushed {
		t.Errorf("a push provider of a kind without push loaded: %v", loaded.Refusals())
	}
	if !loaded.Set.Concurrent("example_gauge") || loaded.Set.Concurrent("example_level") ||
		!loaded.Set.Pushable("example_gauge") || loaded.Set.Pushable("example_level") {
		t.Errorf("the set's attributes are %+v and %+v", attributes(loaded.Set, "example_gauge"),
			attributes(loaded.Set, "example_level"))
	}
	if err := database.SetActiveProviders(ctx, "example_gauge", "example_gauge_one", "example_gauge_two"); err != nil {
		t.Errorf("two providers of a concurrent plugin kind could not both be active: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx, "UPDATE provider SET is_active = true WHERE kind = 'example_level'"); err == nil {
		t.Error("the database let two providers of an exclusive plugin kind be active")
	}
	bindable := loaded.Set.BindableKinds()
	for kind, want := range map[string]bool{"example_gauge": false, "example_level": true, "firewall_log": true} {
		manifest, err := params.DecodeManifest([]byte(`{"manifest_version": 1, "type": "example_binding", ` +
			`"shape": "table", "queries": ["query.sql"], "providers": ["` + kind + `"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := params.ValidateManifestWith(manifest, bindable) == nil; got != want {
			t.Errorf("a widget binding %s is accepted=%v, want %v", kind, got, want)
		}
	}
}

// attributes is a kind's attributes, for a message.
func attributes(set *provider.Set, kind string) provider.KindAttributes {
	found, _ := set.Attributes(kind)
	return found
}

// TestTheLoaderWritesWhatTheTemplateDeclarationsWrite: the registry rows a database built with no plugin
// loaded holds (store.RegisterBuiltinDeclarations, from the manifests read directly) are the rows the
// plugin loader writes for the built-ins.
func TestTheLoaderWritesWhatTheTemplateDeclarationsWrite(t *testing.T) {
	t.Parallel()
	loaderKinds, loaderProviders := provider.SetDeclarations(BuiltinsLoaded().Set)
	coreKinds, coreProviders := provider.CoreDeclarations()
	pluginKinds, pluginProviders, err := provider.PluginDeclarations(opnview.BuiltinPlugins())
	if err != nil {
		t.Fatal(err)
	}
	sortKinds := func(kinds []provider.KindAttributes) []provider.KindAttributes {
		sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
		return kinds
	}
	sortProviders := func(manifests []provider.Manifest) []provider.Manifest {
		sort.Slice(manifests, func(i, j int) bool {
			return manifests[i].Kind+"/"+manifests[i].Key < manifests[j].Kind+"/"+manifests[j].Key
		})
		return manifests
	}
	if got, want := sortKinds(loaderKinds), sortKinds(append(coreKinds, pluginKinds...)); !reflect.DeepEqual(got, want) {
		t.Errorf("the loader's kinds are\n%+v\nthe declarations'\n%+v", got, want)
	}
	if got, want := sortProviders(loaderProviders), sortProviders(append(coreProviders, pluginProviders...)); !reflect.DeepEqual(got, want) {
		t.Errorf("the loader's providers are\n%+v\nthe declarations'\n%+v", got, want)
	}
}
