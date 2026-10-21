package plugin

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// loadOne loads one built-in-shaped plugin directory from its manifest alone.
func loadOne(t *testing.T, manifest string) *Loaded {
	t.Helper()
	loaded, err := Load(context.Background(), Options{Builtins: fstest.MapFS{
		"example_pages/plugin.json":             {Data: []byte(manifest)},
		"example_pages/widgets/example/dir.txt": {Data: []byte("")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// TestASettingsPageIsValidatedAtLoad is the load half of PA8: a page names settings the manifest declares,
// each on one page at most, its heading and help keys in the plugin's own catalogue; and a provider's
// OPNsense enable page is a /ui/ path named in the plugin's catalogue.
func TestASettingsPageIsValidatedAtLoad(t *testing.T) {
	t.Parallel()
	manifest := func(pages, enable string) string {
		return `{"api_version": 2, "name": "example_pages", "version": "0.1.0", "display_name": "Example pages",
			"widgets": ["example"],
			"settings": [{"key": "first", "type": "integer", "default": 1}, {"key": "second", "type": "text"}],
			"pages": ` + pages + enable + `}`
	}
	page := func(key, settings string) string {
		return `{"key": "` + key + `", "label": "plugin.example_pages.page.` + key + `", "help": "plugin.example_pages.page.` +
			key + `_help", "settings": ` + settings + `}`
	}
	if refusals := loadOne(t, manifest(`[`+page("one", `["first", "second"]`)+`]`, "")).Refusals(); len(refusals) != 0 {
		t.Fatalf("a valid page was refused: %+v", refusals)
	}
	for name, pages := range map[string]string{
		"a setting the manifest does not declare": `[` + page("one", `["third"]`) + `]`,
		"a setting on two pages":                  `[` + page("one", `["first"]`) + `,` + page("two", `["first"]`) + `]`,
		"a page holding nothing":                  `[` + page("one", `[]`) + `]`,
		"a page key used twice":                   `[` + page("one", `["first"]`) + `,` + page("one", `["second"]`) + `]`,
		"a label outside the plugin's catalogue": `[{"key": "one", "label": "collection.title", "help": ` +
			`"plugin.example_pages.page.one_help", "settings": ["first"]}]`,
	} {
		refusals := loadOne(t, manifest(pages, "")).Refusals()
		if len(refusals) != 1 || refusals[0].Code != contract.RefusalManifestInvalid {
			t.Errorf("%s: %+v", name, refusals)
		}
	}
}

// TestAnEnablePageCitesOPNsense: a provider's OPNsense enable page is refused unless it is a path of the
// firewall's web interface named in the plugin's catalogue. A built-in's citation of OPNsense's menu
// definition at the pinned version is held by the Sources surface's test.
func TestAnEnablePageCitesOPNsense(t *testing.T) {
	t.Parallel()
	manifest := func(path, label, menu string) string {
		return `{"api_version": 2, "name": "example_pages", "version": "0.1.0", "display_name": "Example pages",
			"widgets": ["example"],
			"tables": [{"name": "things", "columns": [{"name": "seen_at", "type": "instant"}], "retention": {"column": "seen_at"}}],
			"kinds": [{"kind": "example_pages"}],
			"providers": [{"kind": "example_pages", "display_name": "Example", "cadence_seconds": 600,
				"enable_pages": [{"path": "` + path + `", "label": "` + label + `", "menu_url": "` + menu + `"}]}]}`
	}
	menu := "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/IDS/Menu/Menu.xml"
	for name, refused := range map[string]string{
		"a path outside the web interface": manifest("/api/ids/service/status", "plugin.example_pages.page", menu),
		"a label outside its catalogue":    manifest("/ui/ids", "opnsense_page.ids", menu),
	} {
		refusals := loadOne(t, refused).Refusals()
		if len(refusals) != 1 || !strings.Contains(refusals[0].Detail, "enable page") {
			t.Errorf("%s: %+v", name, refusals)
		}
	}
}
