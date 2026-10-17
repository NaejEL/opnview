package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/vocab"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The step-5A corrections on the collection side: AC6, AC7, AC8 and AC11 of
// specs/SPEC-step-5a-corrections.md.

// interfaceIDOf reads the id of the interface with that identifier.
func interfaceIDOf(t *testing.T, database *store.Store, identifier string) int64 {
	t.Helper()
	var id int64
	if err := database.DB().QueryRow("SELECT id FROM interface WHERE identifier = $1", identifier).Scan(&id); err != nil {
		t.Fatalf("reading interface %s: %v", identifier, err)
	}
	return id
}

// TestAFilterLogPassThatFailsPartWayStillPlacesWhatItStored is AC7: the second record
// of the page cannot be stored, the pass returns that error, and the first record --
// stored before the failure -- is placed, with its scope, and in its slots.
func TestAFilterLogPassThatFailsPartWayStillPlacesWhatItStored(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	page := []any{
		map[string]any{"__digest__": "exampledigest0000000000000000201", "__timestamp__": "2026-09-26T23:50:01",
			"interface": "exdev0", "action": "pass", "dir": "in", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "198.51.100.10", "dst": "203.0.113.77", "srcport": "40000", "dstport": "443",
			"reason": "match"},
		// An instant past what the schema admits, so the insert of this record fails.
		map[string]any{"__digest__": "exampledigest0000000000000000202", "__timestamp__": "2101-01-01T00:00:00",
			"interface": "exdev0", "action": "pass", "dir": "in", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "198.51.100.11", "dst": "203.0.113.78", "srcport": "40001", "dstport": "443",
			"reason": "match"},
	}
	harness.Fake.AnswerJSON(opnsense.FirewallLog, page)
	ctx := context.Background()
	if err := harness.Collector.ProbeKind(ctx, "firewall_log"); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if err := harness.Collector.CollectFirewallLog(ctx); err == nil {
		t.Fatal("the pass stored a record the schema refuses and returned no error")
	}
	if stored := collecttest.CountRows(t, harness.Store, "flow"); stored != 1 {
		t.Fatalf("%d flows were stored, not the one before the failure", stored)
	}
	inside := interfaceIDOf(t, harness.Store, "example_if_a")
	var (
		srcInterface *int64
		srcClient    *int64
		scope        string
	)
	if err := harness.Store.DB().QueryRow(`SELECT src_interface_id, src_client_id, traffic_scope FROM flow`).
		Scan(&srcInterface, &srcClient, &scope); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	if srcInterface == nil || *srcInterface != inside || srcClient == nil {
		t.Errorf("the stored record's source is on %v with client %v, not placed on %d", srcInterface, srcClient, inside)
	}
	if scope != "north_south" {
		t.Errorf("the stored record is %s, not north-south", scope)
	}
	if slots := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM volume_aggregate_1h WHERE bytes = 60"); slots != 1 {
		t.Errorf("the stored record is in %d hour slots, not one", slots)
	}
}

// TestDiscoveryStoresTheSecondaryAddressesAndProposesTheirNetworks is AC11, and AC6's first
// part: the entries of ipv4[] and ipv6[] are stored under their own source fields, and
// the networks they cover are proposed as detected, a link-local one never.
func TestDiscoveryStoresTheSecondaryAddressesAndProposesTheirNetworks(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	body := interfacesBody(t)
	for _, row := range body["rows"].([]any) {
		object := row.(map[string]any)
		if object["identifier"] != "example_if_a" {
			continue
		}
		object["ipv4"] = []any{
			map[string]any{"ipaddr": "198.51.100.1/24"},
			map[string]any{"ipaddr": "192.0.2.129/25", "vhid": "3", "status": "MASTER"},
		}
		object["ipv6"] = []any{
			map[string]any{"ipaddr": "2001:db8::1/64"},
			map[string]any{"ipaddr": "fe80::1%exdev0/64"},
			map[string]any{"ipaddr": "not an address"},
		}
	}
	harness.Fake.AnswerJSON(opnsense.InterfacesInfo, body)
	ctx := context.Background()
	if err := harness.Collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("discovering: %v", err)
	}
	inside := interfaceIDOf(t, harness.Store, "example_if_a")
	for _, want := range []struct {
		field, address string
		bits           int64
	}{
		{vocab.SourceFieldIPv4, "198.51.100.1", 24},
		{vocab.SourceFieldIPv4, "192.0.2.129", 25},
		{vocab.SourceFieldIPv6, "2001:db8::1", 64},
		{vocab.SourceFieldIPv6, "fe80::1", 64},
	} {
		if rows := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM interface_address
			WHERE interface_id = $1 AND source_field = $2 AND address = $3 AND prefix_length = $4`,
			inside, want.field, want.address, want.bits); rows != 1 {
			t.Errorf("%s %s/%d is stored %d times under its own field", want.field, want.address, want.bits, rows)
		}
	}
	if rows := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM interface_address
		WHERE interface_id = $1 AND source_field IN ('ipv4', 'ipv6')`, inside); rows != 4 {
		t.Errorf("%d secondary-field rows are stored, not the four entries that parse", rows)
	}
	networks, err := harness.Store.InterfaceNetworks(ctx)
	if err != nil {
		t.Fatalf("reading the networks: %v", err)
	}
	proposed := map[string]bool{}
	for _, network := range networks {
		if network.InterfaceID == inside {
			proposed[network.Network.String()] = network.Origin == flows.NetworkOriginDetected && network.Counts
		}
	}
	for _, want := range []string{"198.51.100.0/24", "192.0.2.128/25", "2001:db8::/64"} {
		if !proposed[want] {
			t.Errorf("the network %s is not proposed as a detected network that counts: %v", want, proposed)
		}
	}
	for network := range proposed {
		if flows.IsLinkLocal(network[:len(network)-3]) {
			t.Errorf("the link-local network %s was proposed", network)
		}
	}
	if len(proposed) != 3 {
		t.Errorf("the interface's networks are %v, not the three its addresses cover", proposed)
	}
}

// TestAnOperatorNetworkChangesClassificationAtTheNextDerivation is AC6, its third part,
// through the collector: an operator edit is caught by the next derivation, whichever pass
// runs it, and the slots then equal a direct computation over the reclassified flows.
func TestAnOperatorNetworkChangesClassificationAtTheNextDerivation(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	target := "203.0.113.20"
	if placed := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM flow WHERE dst_address = $1 AND dst_interface_id IS NOT NULL", target); placed != 0 {
		t.Fatalf("the destination %s is placed before any edit", target)
	}
	flowsToTarget := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM flow WHERE dst_address = $1", target)
	if flowsToTarget == 0 {
		t.Fatalf("no flow names %s", target)
	}
	second := interfaceIDOf(t, harness.Store, "example_if_b")
	if _, err := harness.Store.AddInterfaceNetwork(ctx, second, target+"/32", collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("adding the network: %v", err)
	}
	// The edit alone changes nothing stored; the next derivation does.
	if err := harness.Collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("deriving: %v", err)
	}
	if placed := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM flow WHERE dst_address = $1 AND dst_interface_id = $2", target, second); placed != flowsToTarget {
		t.Errorf("%d of the %d flows to %s are placed on the operator's network", placed, flowsToTarget, target)
	}
	mismatches := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM (
		SELECT (observed_at / 3600) * 3600 AS slot, traffic_direction, sum(packet_bytes) AS bytes
		FROM classified_flow GROUP BY 1, 2) AS f
		LEFT JOIN (SELECT period_start_at AS slot, traffic_direction, sum(bytes) AS bytes
		           FROM volume_aggregate_1h GROUP BY 1, 2) AS v
		  ON v.slot = f.slot AND v.traffic_direction = f.traffic_direction
		WHERE v.bytes IS DISTINCT FROM f.bytes`)
	if mismatches != 0 {
		t.Errorf("%d hour slots differ from a direct computation over the reclassified flows", mismatches)
	}
	if eastWest := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM volume_aggregate_1h
		WHERE traffic_direction = 'inter_interface'`); eastWest == 0 {
		t.Error("no slot carries the flows the edit moved between interfaces")
	}
}
