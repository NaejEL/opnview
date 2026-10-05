package collect

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The step-5A corrections on the collection side: AC6, AC7, AC8 and AC11 of
// specs/SPEC-step-5a-corrections.md.

// interfaceIDOf reads the id of the interface with that identifier.
func interfaceIDOf(t *testing.T, database *store.Store, identifier string) int64 {
	t.Helper()
	var id int64
	if err := database.DB().QueryRow("SELECT id FROM interface WHERE identifier = ?", identifier).Scan(&id); err != nil {
		t.Fatalf("reading interface %s: %v", identifier, err)
	}
	return id
}

// TestAFilterLogPassThatFailsPartWayStillPlacesWhatItStored is AC7: the second record
// of the page cannot be stored, the pass returns that error, and the first record --
// stored before the failure -- is placed, with its scope, and in its slots.
func TestAFilterLogPassThatFailsPartWayStillPlacesWhatItStored(t *testing.T) {
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
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
	harness.fake.answerJSON(opnsense.FirewallLog, page)
	ctx := context.Background()
	if err := harness.collector.probeFirewallLog(ctx); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if err := harness.collector.CollectFirewallLog(ctx); err == nil {
		t.Fatal("the pass stored a record the schema refuses and returned no error")
	}
	if stored := countRows(t, harness.store, "flow"); stored != 1 {
		t.Fatalf("%d flows were stored, not the one before the failure", stored)
	}
	inside := interfaceIDOf(t, harness.store, "example_if_a")
	var (
		srcInterface *int64
		srcClient    *int64
		scope        string
	)
	if err := harness.store.DB().QueryRow(`SELECT src_interface_id, src_client_id, traffic_scope FROM flow`).
		Scan(&srcInterface, &srcClient, &scope); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	if srcInterface == nil || *srcInterface != inside || srcClient == nil {
		t.Errorf("the stored record's source is on %v with client %v, not placed on %d", srcInterface, srcClient, inside)
	}
	if scope != "north_south" {
		t.Errorf("the stored record is %s, not north-south", scope)
	}
	if slots := scalarCount(t, harness.store, "SELECT count(*) FROM volume_aggregate_1h WHERE bytes = 60"); slots != 1 {
		t.Errorf("the stored record is in %d hour slots, not one", slots)
	}
}

// TestDiscoveryStoresTheSecondaryAddressesAndProposesTheirNetworks is AC11, and AC6's first
// part: the entries of ipv4[] and ipv6[] are stored under their own source fields, and
// the networks they cover are proposed as detected, a link-local one never.
func TestDiscoveryStoresTheSecondaryAddressesAndProposesTheirNetworks(t *testing.T) {
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
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
	harness.fake.answerJSON(opnsense.InterfacesInfo, body)
	ctx := context.Background()
	if err := harness.collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("discovering: %v", err)
	}
	inside := interfaceIDOf(t, harness.store, "example_if_a")
	for _, want := range []struct {
		field, address string
		bits           int64
	}{
		{store.SourceFieldIPv4, "198.51.100.1", 24},
		{store.SourceFieldIPv4, "192.0.2.129", 25},
		{store.SourceFieldIPv6, "2001:db8::1", 64},
		{store.SourceFieldIPv6, "fe80::1", 64},
	} {
		if rows := scalarCount(t, harness.store, `SELECT count(*) FROM interface_address
			WHERE interface_id = ? AND source_field = ? AND address = ? AND prefix_length = ?`,
			inside, want.field, want.address, want.bits); rows != 1 {
			t.Errorf("%s %s/%d is stored %d times under its own field", want.field, want.address, want.bits, rows)
		}
	}
	if rows := scalarCount(t, harness.store, `SELECT count(*) FROM interface_address
		WHERE interface_id = ? AND source_field IN ('ipv4', 'ipv6')`, inside); rows != 4 {
		t.Errorf("%d secondary-field rows are stored, not the four entries that parse", rows)
	}
	networks, err := harness.store.InterfaceNetworks(ctx)
	if err != nil {
		t.Fatalf("reading the networks: %v", err)
	}
	proposed := map[string]bool{}
	for _, network := range networks {
		if network.InterfaceID == inside {
			proposed[network.Network.String()] = network.Origin == store.NetworkOriginDetected && network.Counts
		}
	}
	for _, want := range []string{"198.51.100.0/24", "192.0.2.128/25", "2001:db8::/64"} {
		if !proposed[want] {
			t.Errorf("the network %s is not proposed as a detected network that counts: %v", want, proposed)
		}
	}
	for network := range proposed {
		if store.IsLinkLocal(network[:len(network)-3]) {
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
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	target := "203.0.113.20"
	if placed := scalarCount(t, harness.store,
		"SELECT count(*) FROM flow WHERE dst_address = ? AND dst_interface_id IS NOT NULL", target); placed != 0 {
		t.Fatalf("the destination %s is placed before any edit", target)
	}
	flowsToTarget := scalarCount(t, harness.store, "SELECT count(*) FROM flow WHERE dst_address = ?", target)
	if flowsToTarget == 0 {
		t.Fatalf("no flow names %s", target)
	}
	second := interfaceIDOf(t, harness.store, "example_if_b")
	if _, err := harness.store.AddInterfaceNetwork(ctx, second, target+"/32", referenceEpoch()); err != nil {
		t.Fatalf("adding the network: %v", err)
	}
	// The edit alone changes nothing stored; the next derivation does.
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("deriving: %v", err)
	}
	if placed := scalarCount(t, harness.store,
		"SELECT count(*) FROM flow WHERE dst_address = ? AND dst_interface_id = ?", target, second); placed != flowsToTarget {
		t.Errorf("%d of the %d flows to %s are placed on the operator's network", placed, flowsToTarget, target)
	}
	mismatches := scalarCount(t, harness.store, `SELECT count(*) FROM (
		SELECT (observed_at / 3600) * 3600 AS slot, traffic_direction, sum(packet_bytes) AS bytes
		FROM classified_flow GROUP BY 1, 2) AS f
		LEFT JOIN (SELECT period_start_at AS slot, traffic_direction, sum(bytes) AS bytes
		           FROM volume_aggregate_1h GROUP BY 1, 2) AS v
		  ON v.slot = f.slot AND v.traffic_direction = f.traffic_direction
		WHERE v.bytes IS NOT f.bytes`)
	if mismatches != 0 {
		t.Errorf("%d hour slots differ from a direct computation over the reclassified flows", mismatches)
	}
	if eastWest := scalarCount(t, harness.store, `SELECT count(*) FROM volume_aggregate_1h
		WHERE traffic_direction = 'inter_interface'`); eastWest == 0 {
		t.Error("no slot carries the flows the edit moved between interfaces")
	}
}

// TestAHostNameLoggedAsTheClientAttributesOnlyWhenOneLeaseNamesIt is AC8.
func TestAHostNameLoggedAsTheClientAttributesOnlyWhenOneLeaseNamesIt(t *testing.T) {
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	source, destination := "198.51.100.10", "203.0.113.20"
	var flowID, observedAt int64
	if err := harness.store.DB().QueryRow(`SELECT id, observed_at FROM flow
		WHERE src_address = ? AND dst_address = ?`, source, destination).Scan(&flowID, &observedAt); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	provider, err := harness.store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	expires := referenceEpoch() + 3600
	for index, lease := range []struct{ address, hostname string }{
		{source, "example-host"}, {"198.51.100.21", "example-twin"}, {"198.51.100.22", "example-twin"},
	} {
		hostname := lease.hostname
		if err := harness.store.InsertDHCPLease(ctx, provider, store.DHCPLease{
			Backend: "dnsmasq", Address: lease.address, Hostname: &hostname, LeaseState: "active",
			GenerationKey: store.GenerationKeyOf(nil, &expires, int64(index)), ExpiresAt: &expires,
			ObservedAt: referenceEpoch(),
		}); err != nil {
			t.Fatalf("writing a lease: %v", err)
		}
	}
	answer := "Recursion"
	decide := func(logged string) (store.DNSResolution, int64) {
		t.Helper()
		record := lookupRecord{DeduplicationKey: "example-key-" + logged, ClientAddress: logged,
			Domain: "example-site.example.invalid", Action: "pass", AnswerSource: &answer,
			LookedUpAt: observedAt - 2}
		lookup, err := harness.collector.buildDNSResolution(ctx, record, "unbound", referenceEpoch())
		if err != nil {
			t.Fatalf("building the lookup: %v", err)
		}
		if err := harness.store.InsertDNSResolution(ctx, lookup); err != nil {
			t.Fatalf("writing the lookup: %v", err)
		}
		if _, err := harness.store.Reclassify(ctx, []string{lookup.ClientAddress}, referenceEpoch()); err != nil {
			t.Fatalf("classifying: %v", err)
		}
		if _, err := harness.store.Attribute(ctx, observedAt-60, observedAt, 5, referenceEpoch()); err != nil {
			t.Fatalf("attributing: %v", err)
		}
		attributed := scalarCount(t, harness.store, "SELECT count(*) FROM domain_attribution WHERE flow_id = ?", flowID)
		if _, err := harness.store.DB().Exec("DELETE FROM dns_resolution WHERE lookup_key = ?",
			record.DeduplicationKey); err != nil {
			t.Fatalf("clearing the lookup: %v", err)
		}
		return lookup, int64(attributed)
	}

	for _, step := range []struct {
		logged, resolution string
		attributes         bool
	}{
		{"example-twin.example.invalid", store.ClientResolutionAmbiguous, false},
		{"example-nobody.example.invalid", store.ClientResolutionUnknown, false},
		{"example-host.example.invalid", store.ClientResolutionLeased, true},
	} {
		lookup, attributed := decide(step.logged)
		if lookup.ClientResolution != step.resolution {
			t.Errorf("%s was recorded as %s, not %s", step.logged, lookup.ClientResolution, step.resolution)
		}
		if lookup.ClientHostname == nil || *lookup.ClientHostname != step.logged {
			t.Errorf("%s did not keep the logged name: %v", step.logged, lookup.ClientHostname)
		}
		if step.attributes != (attributed == 1) {
			t.Errorf("%s gives the flow %d attributions", step.logged, attributed)
		}
		if step.attributes && lookup.ClientAddress != source {
			t.Errorf("%s resolved to %s, not %s", step.logged, lookup.ClientAddress, source)
		}
		if !step.attributes && lookup.ClientID != nil {
			t.Errorf("%s, which names no single machine, was given client %d", step.logged, *lookup.ClientID)
		}
	}

	// A lookup that logged an address is recorded as such.
	lookup, attributed := decide(source)
	if lookup.ClientResolution != store.ClientResolutionLoggedAddress || lookup.ClientHostname != nil {
		t.Errorf("an address lookup was recorded as %s with host name %v", lookup.ClientResolution,
			lookup.ClientHostname)
	}
	if attributed != 1 {
		t.Errorf("an address lookup gives the flow %d attributions", attributed)
	}
}
