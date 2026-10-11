package widgets

import (
	"github.com/NaejEL/opnview/internal/widget"
)

import "testing"

// TestEveryStatementTheAPIRunsIsAPlannedStatement is AC17's "schema-checks plans every
// statement the API runs": every served type is answered -- at its defaults and with every
// reference kind resolved, so the reference lookups run too -- and every statement text the
// read connections prepared on the way is a registered named statement (the *.statements.sql files of the tree, which
// sql/schema-checks.sh finds and plans at both seed sizes). A read that ran SQL
// written inline in Go, as ReadNotRunning did, would be listed here.
func TestEveryStatementTheAPIRunsIsAPlannedStatement(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	for _, widgetType := range f.service.Types() {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			f.respond(t, widgetType, withPeriod(f, widgetType, period))
		}
	}
	for _, reference := range []map[string]any{
		{"kind": "interface", "by": "identifier", "value": "example_if_0"},
		{"kind": "interface", "by": "device", "value": "exdev0"},
		{"kind": "interface", "by": "user_label", "value": "example label"},
		{"kind": "client", "by": "mac", "value": "00:00:5e:00:53:00"},
		{"kind": "rule", "by": "pf_label", "value": "example-pass-label"},
		{"kind": "rule", "by": "description", "value": "example pass rule"},
		{"kind": "provider", "by": "kind_and_key", "value": "dns_lookup/unbound"},
		{"kind": "country", "by": "iso_3166_1_alpha_2", "value": "ZZ"},
		{"kind": "operator", "by": "asn", "value": 64496},
		{"kind": "site", "by": "domain", "value": f.sites[0]},
	} {
		key, _ := reference["kind"].(string)
		f.respond(t, "custom_chart", map[string]any{"series": []any{map[string]any{"source": widget.SourceTraffic,
			key: reference}}})
	}
	f.respond(t, "connection_tree", map[string]any{"owners": []any{map[string]any{"kind": "owner",
		"by": "display_name", "value": "example owner"}}})
	f.respond(t, "client_traffic_detail", map[string]any{"client": map[string]any{"kind": "client",
		"by": "hostname", "value": "example-host"}})

	outside, err := f.db.ReadStatementsOutsideTheFiles()
	check(t, err)
	if len(outside) != 0 {
		t.Errorf("the API ran %d statements no planned statement file holds:\n%q", len(outside), outside)
	}
	if f.db.ReadStatementCount() == 0 {
		t.Fatal("the read connections prepared nothing, so this assertion is vacuous")
	}
}
