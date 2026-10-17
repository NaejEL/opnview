package widgetapi_test

import (
	"context"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// A third party's widget whose script reads the domain names of the resolver lookups, and does
// nothing about aggregate_mode = no_domains: it even catches whatever a read throws and answers with
// what it has. Its author has nothing to do (maintainer's decision, 12 October 2026): the core
// withholds what carries a domain.
var exampleDomainsPlugin = map[string]string{
	"plugin.json": `{
		"api_version": 1, "name": "example_domains", "version": "0.1.0", "display_name": "Example domains",
		"widgets": ["example_looked_up_names"]}`,
	"widgets/example_looked_up_names/manifest.json": `{
		"manifest_version": 1, "type": "example_looked_up_names", "shape": "table",
		"queries": ["names.statements.sql"], "script": "names.js"}`,
	"widgets/example_looked_up_names/names.statements.sql": `
-- statement: widget_example_looked_up_names
SELECT domain, count(*) AS lookups FROM dns_resolution GROUP BY domain ORDER BY domain;
`,
	"widgets/example_looked_up_names/names.js": `
exports.widget = { serve: function (host) {
  var rows = [];
  try {
    rows = host.query.rows("widget_example_looked_up_names");
  } catch (ignored) {
    rows = [{ domain: "caught", lookups: 0 }];
  }
  return { data: { rows: rows }, has_rows: rows.length > 0 };
} };
`,
}

// TestTheCoreWithholdsAThirdPartyScriptsDomainNamesUnderNoDomains: in full mode the third party's
// widget answers the names it read; under no_domains the same widget, unchanged, is withheld whole
// -- condition withheld_by_aggregate_mode, data null, "data" in withheld -- and no name, not even
// the one its catch block would have written, reaches the answer.
func TestTheCoreWithholdsAThirdPartyScriptsDomainNamesUnderNoDomains(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	for name, content := range exampleDomainsPlugin {
		path := filepath.Join(dataDir, plugin.PluginsDirectory, "example_domains", filepath.FromSlash(name))
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
	ctx := context.Background()
	now := harness.Clock.Now().Unix()
	if _, err := harness.Store.DB().ExecContext(ctx,
		"INSERT INTO dns_resolution (id, lookup_key, client_address, domain, resolver, action, looked_up_at, ingested_at) "+
			"VALUES (1, 'k1', $1, 'example.org', 'unbound', 'pass', $2, $3)", documentationAddress, now-60, now-60); err != nil {
		t.Fatal(err)
	}

	condition, body := decodeCondition(t, harness.Get(widget.PathPrefix+"example_looked_up_names"), http.StatusOK)
	data, _ := body["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	if condition != envelope.ConditionReachable || len(rows) != 1 || rows[0].(map[string]any)["domain"] != "example.org" {
		t.Fatalf("in full mode the widget answered %q with %v", condition, body["data"])
	}

	if err := harness.Store.SetSetting(ctx, config.KeyAggregateMode, request.AggregateModeNoDomains, now); err != nil {
		t.Fatal(err)
	}
	condition, body = decodeCondition(t, harness.Get(widget.PathPrefix+"example_looked_up_names"), http.StatusOK)
	if condition != envelope.ConditionWithheldByAggregateMode || body["data"] != nil {
		t.Errorf("under no_domains the widget answered %q with %v", condition, body["data"])
	}
	withheld, _ := body["withheld"].([]any)
	if len(withheld) != 1 || withheld[0] != "data" {
		t.Errorf("the withheld list is %v", body["withheld"])
	}
}

// documentationAddress is the client of the stored lookup: an address of the IPv4 documentation
// block (RFC 5737), built here rather than written, as the repository's literal check asks.
var documentationAddress = netip.AddrFrom4([4]byte{192, 0, 2, 10}).String()
