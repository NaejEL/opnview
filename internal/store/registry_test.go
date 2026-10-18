package store_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// todaysRegistry are the provider rows a fresh database held once the built-ins had loaded before the
// DDL stopped seeding them (PA-AC4): the sixteen rows the DDL seeded, and the three the built-ins'
// declarations added at their first probe round. Written out here, from that seed, so the test reads
// what the rows were rather than what the code now computes.
var todaysRegistry = []string{
	"dhcp_lease|dnsmasq|Dnsmasq DHCP",
	"dhcp_lease|isc|ISC dhcpd",
	"dhcp_lease|kea|Kea DHCP",
	"dns_lookup|dnsmasq|Dnsmasq resolver",
	"dns_lookup|unbound|Unbound",
	"firewall_log|pf|pf filter log",
	"flow_record|netflow|NetFlow export",
	"flow_volume|insight|NetFlow / Insight",
	"ip_geolocation|maxmind_geolite2|MaxMind GeoLite2",
	"measurement_sample|insight|NetFlow / Insight sampling",
	"neighbour|neighbours|Neighbour tables",
	"pf_state_rate|pf_states|pf state table",
	"registrable_domain|public_suffix_list|Public Suffix List",
	"resolver_cache|dnsmasq|Dnsmasq cache",
	"resolver_cache|unbound|Unbound cache",
	"resolver_local_data|dnsmasq|Dnsmasq local data",
	"resolver_local_data|unbound|Unbound local data",
	"security_event|suricata|Suricata",
	"unbound_query_log|unbound|Unbound query log over syslog",
}

// TestTheDDLSeedsNoProductRowAndTheBuiltinsWriteTodaysRegistry is PA-AC4: schema.sql inserts no
// provider and no kind; a fresh database holds none; once the built-ins' declarations are written it
// holds today's rows, each inactive, each with its not-yet-probed availability row.
func TestTheDDLSeedsNoProductRowAndTheBuiltinsWriteTodaysRegistry(t *testing.T) {
	t.Parallel()
	schema, err := store.SQLFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"INSERT INTO provider ", "INSERT INTO provider_kind", "INTO source_availability"} {
		if strings.Contains(string(schema), statement) {
			t.Errorf("schema.sql holds %q", statement)
		}
	}
	ctx := context.Background()
	empty, err := store.NewEmptyTestDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.Drop(context.Background()) })
	database, err := store.Open(ctx, empty.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	read := func() []string {
		t.Helper()
		rows, err := database.DB().QueryContext(ctx, "SELECT p.kind, p.provider_key, p.display_name, p.is_active, "+
			"a.outcome FROM provider AS p LEFT JOIN source_availability AS a ON a.provider_id = p.id")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var found []string
		for rows.Next() {
			var kind, key, name string
			var active bool
			var outcome *string
			if err := rows.Scan(&kind, &key, &name, &active, &outcome); err != nil {
				t.Fatal(err)
			}
			if active || outcome == nil || *outcome != "not_yet_probed" {
				t.Errorf("%s/%s is active=%v with the outcome %v", kind, key, active, outcome)
			}
			found = append(found, kind+"|"+key+"|"+name)
		}
		sort.Strings(found)
		return found
	}
	if rows := read(); len(rows) != 0 {
		t.Fatalf("a fresh database holds provider rows: %v", rows)
	}
	if err := database.RegisterBuiltinDeclarations(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Join(got, "\n") != strings.Join(todaysRegistry, "\n") {
		t.Errorf("the registry is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(todaysRegistry, "\n"))
	}
	// Registering again writes nothing new and changes nothing.
	if err := database.RegisterBuiltinDeclarations(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != len(todaysRegistry) {
		t.Errorf("a second registration left %d rows", len(got))
	}
}
