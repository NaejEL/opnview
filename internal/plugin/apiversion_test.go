package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// TestOnlyAPIVersion2IsReadAndAVersion1PluginIsRefusedNamingBoth is PA-AC1 as amended by ADR 0018: versions 2 and 3 are the
// versions this build reads, a version-1 plugin is refused with unsupported_api_version and a detail
// naming both versions, and every built-in manifest says 2 or 3.
func TestOnlyAPIVersion2IsReadAndAVersion1PluginIsRefusedNamingBoth(t *testing.T) {
	if contract.MinAPIVersion != 2 || contract.MaxAPIVersion != 3 {
		t.Fatalf("this build reads versions %d to %d, not 2 to 3", contract.MinAPIVersion, contract.MaxAPIVersion)
	}
	dataDir := t.TempDir()
	directory := filepath.Join(dataDir, PluginsDirectory, "example_old")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"api_version": 1, "name": "example_old", "version": "0.1.0", "display_name": "Example old",
		"widgets": ["example_old"]}`
	if err := os.WriteFile(filepath.Join(directory, contract.ManifestFile), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(context.Background(), Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := loaded.Plugin("example_old"); found {
		t.Fatal("a version-1 plugin was loaded")
	}
	var refusal *contract.Refusal
	for index := range loaded.Refusals() {
		if loaded.Refusals()[index].Plugin == "example_old" {
			refusal = &loaded.Refusals()[index]
		}
	}
	if refusal == nil || refusal.Code != contract.RefusalUnsupportedAPIVersion {
		t.Fatalf("the refusal is %+v, not %s", refusal, contract.RefusalUnsupportedAPIVersion)
	}
	if !strings.Contains(refusal.Detail, "version 1") || !strings.Contains(refusal.Detail, "versions 2 to 3") {
		t.Errorf("the refusal's detail %q does not name both versions", refusal.Detail)
	}
	builtins := BuiltinsLoaded()
	if len(builtins.Refusals()) != 0 {
		t.Fatalf("built-ins refused: %v", builtins.Refusals())
	}
	for _, member := range builtins.Plugins() {
		if member.APIVersion != 2 && member.APIVersion != 3 {
			t.Errorf("the built-in %s says api_version %d", member.Name, member.APIVersion)
		}
	}
}
