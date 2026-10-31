package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// libraryManifests is a plugin shipping a library, a plugin requiring it and one that does not.
func libraryManifests(t *testing.T, library string) fstest.MapFS {
	t.Helper()
	encode := func(manifest map[string]any) []byte {
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	files := fstest.MapFS{
		"shipper/plugin.json": {Data: encode(map[string]any{"api_version": 3, "name": "shipper", "version": "1",
			"display_name": "Shipper", "libraries": []string{"lib.js"}})},
		"user/plugin.json": {Data: encode(map[string]any{"api_version": 3, "name": "user", "version": "1",
			"display_name": "User", "requires": []string{"shipper"}, "libraries": []string{"own.js"}})},
		"user/own.js": {Data: []byte("exports.own = true;")},
		"stranger/plugin.json": {Data: encode(map[string]any{"api_version": 3, "name": "stranger", "version": "1",
			"display_name": "Stranger", "libraries": []string{"own.js"}})},
		"stranger/own.js": {Data: []byte("exports.own = true;")},
	}
	if library != "" {
		files["shipper/lib.js"] = &fstest.MapFile{Data: []byte(library)}
	}
	return files
}

// A plugin may ship libraries and nothing else; a plugin that requires it may use them beside its
// own, in the order own then required; one that does not require it has its own alone (ADR 0021).
func TestALibraryIsUsableByThePluginsThatRequireItsShipper(t *testing.T) {
	t.Parallel()
	loaded, err := Load(context.Background(), Options{Builtins: libraryManifests(t, "exports.add = function (a, b) { return a + b; };")})
	if err != nil {
		t.Fatal(err)
	}
	if refusals := loaded.Refusals(); len(refusals) != 0 {
		t.Fatalf("refused: %+v", refusals)
	}
	names := func(plugin string) []string {
		member, found := loaded.Plugin(plugin)
		if !found {
			t.Fatalf("%s did not load", plugin)
		}
		var listed []string
		for _, library := range member.Libraries() {
			listed = append(listed, library.Plugin+"/"+library.File)
		}
		return listed
	}
	if got := names("user"); len(got) != 2 || got[0] != "user/own.js" || got[1] != "shipper/lib.js" {
		t.Errorf("the requiring plugin's libraries: %v", got)
	}
	if got := names("stranger"); len(got) != 1 || got[0] != "stranger/own.js" {
		t.Errorf("a plugin not requiring the shipper: %v", got)
	}
}

// A library file that is absent, or that does not compile, refuses its plugin by name.
func TestALibraryThatIsAbsentOrDoesNotCompileRefusesItsPlugin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		library string
		code    string
	}{{"", contract.RefusalLibraryMissing}, {"exports.x = ;", contract.RefusalLibraryDoesNotCompile},
		{"throw new Error('while defining');", contract.RefusalLibraryDoesNotCompile}} {
		loaded, err := Load(context.Background(), Options{Builtins: libraryManifests(t, c.library)})
		if err != nil {
			t.Fatal(err)
		}
		var shipper *contract.Refusal
		for _, refusal := range loaded.Refusals() {
			if refusal.Plugin == "shipper" {
				held := refusal
				shipper = &held
			}
		}
		if shipper == nil || shipper.Code != c.code {
			t.Errorf("library %q: shipper refused with %+v, want %s", c.library, shipper, c.code)
		}
	}
}
