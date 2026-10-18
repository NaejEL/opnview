package collect_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
)

// TestAThirdPartyLeaseAndLookupPluginStoreTheirRecords: a dhcp_lease plugin and a dns_lookup plugin
// added by their directories alone -- example_leases is docs/plugins.md's worked example, as it is
// written there -- are probed, activated and read by the collector, and their records reach the
// database under their own provider keys. dhcp_lease.backend and dns_resolution.resolver hold the
// provider key, and no closed list of the built-in keys may refuse a third party's.
func TestAThirdPartyLeaseAndLookupPluginStoreTheirRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	for _, name := range []string{"example_leases", "example_lookups"} {
		copyDirectory(t, filepath.Join("testdata", name), filepath.Join(dataDir, plugin.PluginsDirectory, name))
	}
	loaded, err := plugin.Load(ctx, plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Errorf("refused: %+v", refusal)
	}

	fake := providertest.NewFirewall(t)
	database := collecttest.NewTestStore(t)
	clock := collecttest.NewFixedClock(collecttest.ReferenceInstant())
	collector := collecttest.NewTestCollectorWithSources(t, providertest.NewClient(t, fake), database, clock,
		loaded.Set)
	harness := &collecttest.ProbeHarness{Fake: fake, Store: database, Collector: collector, Clock: clock}
	collecttest.ArrangeDiscoverableFirewall(t, fake, collector)
	now := collecttest.ReferenceEpoch()

	// The built-in implementations answer nothing (the fake's 404): only the two plugins are there.
	// The neighbour tables the lease pass reads beside the lease table, as the lease harness answers them.
	fake.AnswerFixture(collecttest.Declared("neighbours", "arp"), "get_arp.json")
	fake.AnswerFixture(collecttest.Declared("neighbours", "ndp"), "get_ndp.json")
	running := map[string]any{"status": "running"}
	fake.AnswerJSON(opnsense.Endpoint{Path: "/api/exampledhcp/service/status", Method: "GET"}, running)
	fake.AnswerJSON(opnsense.Endpoint{Path: "/api/exampledhcp/leases/search", Method: "POST"},
		map[string]any{"total": 1, "rowCount": 1, "current": 1, "rows": []any{
			map[string]any{"address": "192.0.2.10", "hwaddr": "AA:BB:CC:00:11:22", "hostname": "example-host",
				"expire": now + 3600},
		}})
	fake.AnswerJSON(opnsense.Endpoint{Path: "/api/exampledns/service/status", Method: "GET"}, running)
	fake.AnswerJSON(opnsense.Endpoint{Path: "/api/exampledns/queries/search", Method: "GET"},
		map[string]any{"rows": []any{
			map[string]any{"id": "example-lookup-1", "client": "192.0.2.10", "name": "example-site.example.invalid",
				"time": now - 60},
		}})

	if err := collector.EnsureProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if err := collector.ProbeKind(ctx, "dhcp_lease"); err != nil {
		t.Fatalf("probing the lease kind: %v", err)
	}
	if err := collector.ProbeKind(ctx, "dns_lookup"); err != nil {
		t.Fatalf("probing the lookup kind: %v", err)
	}
	if active := harness.ActiveKeysOf(t, "dhcp_lease"); len(active) != 1 || active[0] != "example_leases" {
		t.Fatalf("the lease kind activated %v, want the third-party plugin alone", active)
	}
	if active := harness.ActiveKeyOf(t, "dns_lookup"); active != "example_lookups" {
		t.Fatalf("the lookup kind activated %q, want the third-party plugin", active)
	}

	if err := collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if err := collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("the lookup pass: %v", err)
	}
	if leases := collecttest.ScalarCount(t, database,
		"SELECT count(*) FROM dhcp_lease WHERE backend = $1 AND mac = $2", "example_leases",
		"aa:bb:cc:00:11:22"); leases != 1 {
		t.Errorf("the third-party lease plugin stored %d leases under its key, want 1", leases)
	}
	if lookups := collecttest.ScalarCount(t, database,
		"SELECT count(*) FROM dns_resolution WHERE resolver = $1 AND domain = $2", "example_lookups",
		"example-site.example.invalid"); lookups != 1 {
		t.Errorf("the third-party lookup plugin stored %d lookups under its key, want 1", lookups)
	}
}
