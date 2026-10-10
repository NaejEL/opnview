package collect

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The collector half of specs/SPEC-step-5a-live-corrections.md: the gateway absences
// (AC15), the late lookups (AC16), the host-name retry across a restart (AC14, H6), and
// the two pairing fields read from the filter log (AC7, AC9).

// gatewayReadings counts the stored gateway readings, and those whose value is 0.
func gatewayReadings(t *testing.T, database *store.Store) (int, int) {
	t.Helper()
	return scalarCount(t, database, "SELECT count(*) FROM measurement_sample WHERE subject_kind = 'gateway'"),
		scalarCount(t, database, "SELECT count(*) FROM measurement_sample WHERE subject_kind = 'gateway' AND value = 0")
}

// TestEachGatewayAbsenceIsNamedOnItsOwnAndWritesNoReading is AC15's second and third
// points: each of the five absences yields its own wording and no row, and "~" never
// yields 0.
func TestEachGatewayAbsenceIsNamedOnItsOwnAndWritesNoReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(fake *fakeFirewall)
		wording string
	}{
		{"authentication failed", func(fake *fakeFirewall) {
			fake.answer(opnsense.GatewayStatus, http.StatusUnauthorized,
				[]byte(`{"status":401,"message":"Authentication Failed"}`))
		}, "gateway latency and loss (authentication failed (HTTP 401): the firewall did not accept the API key and secret)"},
		{"denied", func(fake *fakeFirewall) {
			fake.answer(opnsense.GatewayStatus, http.StatusForbidden, []byte(`{"status":403,"message":"Forbidden"}`))
		}, "gateway latency and loss (denied, HTTP 403: the API key's user needs the privilege \"System: Gateways\""},
		{"not found", func(fake *fakeFirewall) {
			fake.answer(opnsense.GatewayStatus, http.StatusNotFound, []byte(`{"errorMessage":"Endpoint not found"}`))
		}, "gateway latency and loss (not found, HTTP 404"},
		{"failed", func(fake *fakeFirewall) {
			fake.answerJSON(opnsense.GatewayStatus, map[string]any{"items": []any{}, "status": "failed"})
		}, "gateway latency and loss (the endpoint answered \"status\": \"failed\" with no item, which it does both when no gateway is configured"},
		{"no gateway", func(fake *fakeFirewall) {
			fake.answerJSON(opnsense.GatewayStatus, map[string]any{"items": []any{}, "status": "ok"})
		}, "gateway latency and loss (the endpoint answered \"status\": \"ok\" and listed no gateway)"},
		{"no figure", func(fake *fakeFirewall) {
			fake.answerFixture(opnsense.GatewayStatus, "gateway_status_unmonitored.json")
		}, "gateway latency and loss (2 of 2 gateways listed with no figure: dpinger.inc reports \"~\""},
	}
	seen := map[string]string{}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			harness := arrangeMeasurementCollection(t)
			one.arrange(harness.fake)
			if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
				t.Fatalf("sampling: %v", err)
			}
			detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
			if !strings.Contains(detail, one.wording) {
				t.Errorf("the detail is %q, which does not name the absence as %q", detail, one.wording)
			}
			for other, wording := range seen {
				if strings.Contains(detail, wording) {
					t.Errorf("the %s absence reads like the %s one", one.name, other)
				}
			}
			seen[one.name] = one.wording
			if rows, zeroes := gatewayReadings(t, harness.store); rows != 0 || zeroes != 0 {
				t.Errorf("%d gateway readings, %d of them 0, were written for an absence", rows, zeroes)
			}
		})
	}
}

// TestTheGatewayMeasuresOfTheLiveShapeAreStoredPerGatewayWithFigures is AC15's first
// point: a gateway with figures gets its three measures, a gateway listed with "~" gets
// none and no zero, and the absence of the second is named while the first is stored.
func TestTheGatewayMeasuresOfTheLiveShapeAreStoredPerGatewayWithFigures(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if measured := scalarCount(t, harness.store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'gateway' AND subject_key = 'EXAMPLE_GW_A'`); measured != 3 {
		t.Errorf("the gateway with figures has %d readings, not its three measures", measured)
	}
	if unmeasured := scalarCount(t, harness.store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'gateway' AND subject_key = 'EXAMPLE_GW_B'`); unmeasured != 0 {
		t.Errorf("the gateway listed with ~ has %d readings", unmeasured)
	}
	if _, zeroes := gatewayReadings(t, harness.store); zeroes != 0 {
		t.Errorf("%d gateway readings are 0", zeroes)
	}
	if detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight); !strings.Contains(detail,
		"1 of 2 gateways listed with no figure") {
		t.Errorf("the detail %q does not name the gateway listed with no figure", detail)
	}
}

// TestTheProbeRoundKeepsWhatTheLastSamplingPassCouldNotRead: the cause found for the
// live firewall's empty detail. The probe round rewrote the measurement provider's
// availability from its own probe, which reads no reading, and erased the record of the
// gateways that had no figure.
func TestTheProbeRoundKeepsWhatTheLastSamplingPassCouldNotRead(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	harness.fake.answerFixture(opnsense.GatewayStatus, "gateway_status_unmonitored.json")
	ctx := context.Background()
	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if err := harness.collector.probeMeasurement(ctx); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight); !strings.Contains(detail,
		"gateways listed with no figure") {
		t.Errorf("after the probe round the detail is %q, which has lost the gateways with no figure", detail)
	}
}

// lookupRow is one row of the resolver's query report, in its own field names.
func lookupRow(client, domain string, at int64) map[string]any {
	return map[string]any{"client": client, "domain": domain, "time": at, "action": "Pass",
		"source": "Recursion", "rcode": "NOERROR", "dnssec_status": "", "blocklist": "", "uuid": nil}
}

// answerLookups makes the query report return these rows.
func answerLookups(fake *fakeFirewall, rows ...map[string]any) {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	fake.answerJSON(opnsense.SearchQueries, map[string]any{"total": len(rows), "rowCount": len(rows),
		"current": 1, "rows": list})
}

// TestALateLookupAttributesAFlowWithNoFurtherFlowPass is item 5.1 and AC16's first point:
// a flow stored and derived with no lookup is attributed by a later lookup pass alone,
// and a still later lookup of a second domain in the window takes the attribution away.
func TestALateLookupAttributesAFlowWithNoFurtherFlowPass(t *testing.T) {
	t.Parallel()
	harness := arrangeResolverCollection(t)
	harness.fake.answerFixture(opnsense.FirewallLog, "firewall_log.json")
	ctx := context.Background()
	if err := harness.collector.probeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	answerLookups(harness.fake)
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting the filter log: %v", err)
	}
	source, destination := "198.51.100.10", "203.0.113.20"
	var flowID, observedAt int64
	if err := harness.store.DB().QueryRow(`SELECT id, observed_at FROM flow
		WHERE src_address = ? AND dst_address = ?`, source, destination).Scan(&flowID, &observedAt); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	attributed := func() int {
		return scalarCount(t, harness.store, "SELECT count(*) FROM domain_attribution WHERE flow_id = ?", flowID)
	}
	if attributed() != 0 {
		t.Fatal("the flow is attributed before any lookup was stored")
	}

	answerLookups(harness.fake, lookupRow(source, "example-late.example.invalid", observedAt-2))
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting the lookups: %v", err)
	}
	if attributed() != 1 {
		t.Fatal("the lookup stored after the flow did not attribute it")
	}

	answerLookups(harness.fake, lookupRow(source, "example-late.example.invalid", observedAt-2),
		lookupRow(source, "example-other.example.invalid", observedAt-1))
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting the lookups: %v", err)
	}
	if attributed() != 0 {
		t.Error("a second domain looked up in the window left the attribution in place")
	}
}

// TestALookupAtTheNewestStoredInstantIsInsertedWhenItsKeyIsNew is AC16's second point:
// the insert decides, whatever the instant.
func TestALookupAtTheNewestStoredInstantIsInsertedWhenItsKeyIsNew(t *testing.T) {
	t.Parallel()
	harness := arrangeResolverCollection(t)
	ctx := context.Background()
	at := referenceEpoch() - 100
	answerLookups(harness.fake, lookupRow("198.51.100.10", "example-first.example.invalid", at))
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	before := countRows(t, harness.store, "dns_resolution")
	// The same lookup again, and a different one at the same instant: the newest stored.
	answerLookups(harness.fake, lookupRow("198.51.100.10", "example-first.example.invalid", at),
		lookupRow("198.51.100.10", "example-second.example.invalid", at))
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if after := countRows(t, harness.store, "dns_resolution"); after != before+1 {
		t.Errorf("the pass stored %d rows; the new lookup at the newest instant is one, and the known one none",
			after-before)
	}
	if second := scalarCount(t, harness.store, `SELECT count(*) FROM dns_resolution
		WHERE domain = 'example-second.example.invalid'`); second != 1 {
		t.Errorf("the lookup sharing the newest instant is stored %d times, not once", second)
	}
	if first := scalarCount(t, harness.store, `SELECT count(*) FROM dns_resolution
		WHERE domain = 'example-first.example.invalid'`); first != 1 {
		t.Errorf("the known lookup is stored %d times, not once", first)
	}
}

// TestAnUnresolvedLookupStoredBeforeARestartIsResolvedByTheFirstLeasePassAfterIt is AC14's
// restart point, H6: the point a lease pass resolved host names from was kept in memory
// and started at the collector's creation, so a lookup stored before a restart was never
// examined again.
func TestAnUnresolvedLookupStoredBeforeARestartIsResolvedByTheFirstLeasePassAfterIt(t *testing.T) {
	t.Parallel()
	harness := arrangeResolverCollection(t)
	ctx := context.Background()
	logged := "example-restart-host.example.invalid"
	answerLookups(harness.fake, lookupRow(logged, "example-site.example.invalid", referenceEpoch()-120))
	if err := harness.collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if unknown := scalarCount(t, harness.store, `SELECT count(*) FROM dns_resolution
		WHERE client_hostname = ? AND client_resolution = 'unknown_hostname'`, logged); unknown != 1 {
		t.Fatalf("the lookup was not stored as an unknown host name (%d)", unknown)
	}

	// The restart: a new collector over the same database, an hour later.
	restarted := New(newFakeClient(t, harness.fake), harness.store,
		newFixedClock(referenceInstant().Add(time.Hour)))
	provider, err := harness.store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	hostname := "example-restart-host"
	expires := referenceEpoch() + 86400
	if err := harness.store.InsertDHCPLease(ctx, provider, store.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.30", Hostname: &hostname, LeaseState: "active",
		GenerationKey: store.GenerationKeyOf(nil, &expires, 0), ExpiresAt: &expires,
		ObservedAt: referenceEpoch() + 3600,
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	// What the first lease pass after the restart derives.
	if err := restarted.derive(ctx, derivation{hostnames: true, hostnamesBefore: referenceEpoch() + 3600}); err != nil {
		t.Fatalf("deriving the lease pass: %v", err)
	}
	if resolved := scalarCount(t, harness.store, `SELECT count(*) FROM dns_resolution
		WHERE client_hostname = ? AND client_resolution = 'lease_hostname'
		  AND client_address = '198.51.100.30'`, logged); resolved != 1 {
		t.Error("the lookup stored before the restart was not resolved by the first lease pass after it")
	}
}

// TestTheFilterLogStoresTheTwoPairingFieldsAndPairsTheTwoLegs: `id` and `seq` are read at
// the positions filterlog writes them, an IPv6 record has no `id`, and a NATed connection
// logged on two interfaces is paired by the collector's own derivation.
func TestTheFilterLogStoresTheTwoPairingFieldsAndPairsTheTwoLegs(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	rows := []any{
		map[string]any{"__digest__": "exampledigestpair0000000000000001", "__timestamp__": "2026-09-26T23:58:01",
			"interface": "exdev3", "action": "pass", "dir": "out", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "203.0.113.2", "dst": "192.0.2.80", "srcport": "61001", "dstport": "443",
			"id": "4242", "seq": "123456789", "rid": "example-rule-label-1", "reason": "match"},
		map[string]any{"__digest__": "exampledigestpair0000000000000002", "__timestamp__": "2026-09-26T23:58:01",
			"interface": "exdev0", "action": "pass", "dir": "in", "ipversion": "4", "protoname": "tcp",
			"length": "60", "src": "198.51.100.10", "dst": "192.0.2.80", "srcport": "40001", "dstport": "443",
			"id": "4242", "seq": "123456789", "rid": "example-rule-label-1", "reason": "match"},
		map[string]any{"__digest__": "exampledigestpair0000000000000003", "__timestamp__": "2026-09-26T23:58:00",
			"interface": "exdev0", "action": "pass", "dir": "in", "ipversion": "6", "protoname": "udp",
			"length": "80", "src": "2001:db8::10", "dst": "2001:db8:ffff::53", "srcport": "40002",
			"dstport": "53", "rid": "example-rule-label-1", "reason": "match"},
	}
	harness.fake.answerJSON(opnsense.FirewallLog, rows)
	ctx := context.Background()
	if err := harness.collector.probeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if stored := scalarCount(t, harness.store, `SELECT count(*) FROM flow
		WHERE ip_id = 4242 AND tcp_seq = 123456789`); stored != 2 {
		t.Errorf("%d records carry the identification and the sequence number, not 2", stored)
	}
	if v6 := scalarCount(t, harness.store, `SELECT count(*) FROM flow
		WHERE ip_version = 6 AND ip_id IS NULL AND tcp_seq IS NULL`); v6 != 1 {
		t.Error("the IPv6 UDP record carries a field it does not have")
	}
	if paired := scalarCount(t, harness.store, `SELECT count(*) FROM flow AS f JOIN flow AS p
		ON p.id = f.paired_flow_id AND p.paired_flow_id = f.id
		WHERE f.pair_outcome = 'second_leg' AND f.src_is_this_firewall = 1 AND p.pair_outcome = 'first_leg'`); paired != 1 {
		t.Error("the two legs of the NATed connection were not paired by the collector's derivation")
	}
}

// interfacesInfoBody is an interfaces_info answer of three interfaces -- one inside, one
// upstream, and a second inside one that reports heldAddress, or no address at all when
// it is empty -- with that last interface listed first or last.
func interfacesInfoBody(heldFirst bool, heldAddress string) map[string]any {
	others := []any{
		map[string]any{"identifier": "example_if_a", "device": "exdev0", "description": "example description A",
			"link_type": "example-link-type", "addr4": "198.51.100.1/24", "addr6": "2001:db8::1/64",
			"gateways": []any{}},
		map[string]any{"identifier": "example_if_d", "device": "exdev3", "description": "example description D",
			"link_type": "example-link-type", "addr4": "203.0.113.2/24", "gateways": []any{"203.0.113.1"}},
	}
	held := map[string]any{"identifier": "example_if_b", "device": "exdev1",
		"description": "example description B", "link_type": "example-link-type", "vlan_tag": 101,
		"gateways": []any{}}
	if heldAddress != "" {
		held["addr4"] = heldAddress + "/24"
	}
	rows := append([]any{}, others...)
	if heldFirst {
		rows = append([]any{held}, others...)
	} else {
		rows = append(rows, held)
	}
	return map[string]any{"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows}
}

// TestAReacquiredAddressIsANewHoldingWhateverTheOrderInterfacesAreListed: an interface's
// only address is read twice, missing from two discoveries, then read again. Through the
// production discovery path, with that interface listed first and then last, the address
// is two holdings and the gap between them is held by neither.
func TestAReacquiredAddressIsANewHoldingWhateverTheOrderInterfacesAreListed(t *testing.T) {
	t.Parallel()
	for _, heldFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "listed first", false: "listed last"}[heldFirst], func(t *testing.T) {
			harness := newProbeHarness(t)
			ctx := context.Background()
			harness.fake.answerFixture(opnsense.InterfaceNames, "get_interface_names.json")
			harness.fake.answerFixture(opnsense.SearchRule, "search_rule.json")
			harness.fake.answerFixture(opnsense.SystemTime, "system_time.json")
			held := "192.0.2.1"
			for index, read := range []bool{true, true, false, false, true} {
				setClock(harness, referenceInstant().Add(time.Duration(index)*5*time.Minute))
				address := ""
				if read {
					address = held
				}
				harness.fake.answerJSON(opnsense.InterfacesInfo, interfacesInfoBody(heldFirst, address))
				if err := harness.collector.RefreshDiscovery(ctx); err != nil {
					t.Fatalf("discovery %d: %v", index, err)
				}
			}
			if holdings := scalarCount(t, harness.store, `SELECT count(*) FROM interface_address
				WHERE address = ? AND source_field = 'addr4'`, held); holdings != 2 {
				t.Errorf("the address released and re-acquired is %d rows, not two holdings", holdings)
			}
			gap := referenceEpoch() + int64((12*time.Minute + 30*time.Second).Seconds())
			if spanned := scalarCount(t, harness.store, `SELECT count(*) FROM this_firewall_address
				WHERE address = ? AND first_seen_at - 300 <= ? AND (is_current = 1 OR last_seen_at + 300 >= ?)`,
				held, gap, gap); spanned != 0 {
				t.Error("a holding of the address spans the gap between its two holdings")
			}
		})
	}
}

// unresolvedLookup stores one lookup whose logged host name named no lease.
func unresolvedLookup(t *testing.T, database *store.Store, key, hostname string, lookedUpAt, ingestedAt int64) {
	t.Helper()
	name := hostname
	if err := database.InsertDNSResolution(context.Background(), store.DNSResolution{
		LookupKey: key, ClientAddress: name, ClientHostname: &name,
		ClientResolution: store.ClientResolutionUnknown, Domain: "example-site.example.invalid",
		Resolver: "unbound", Action: "pass", LookedUpAt: lookedUpAt, IngestedAt: ingestedAt,
	}); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}
}

// examined reads whether a lease pass has examined one lookup, and its resolution.
func examined(t *testing.T, database *store.Store, key string) (bool, string) {
	t.Helper()
	var (
		at         *int64
		resolution string
	)
	if err := database.DB().QueryRow(`SELECT hostname_examined_at, client_resolution FROM dns_resolution
		WHERE lookup_key = ?`, key).Scan(&at, &resolution); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return at != nil, resolution
}

// TestAFailedLeasePassDoesNotUseUpALookupsRetry is H6 against a failed lease read: a
// pass that could not read the active backend examines nothing, so the lookup is still
// resolved by the first pass that does read it.
func TestAFailedLeasePassDoesNotUseUpALookupsRetry(t *testing.T) {
	t.Parallel()
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()
	unresolvedLookup(t, harness.store, "example-key-failed-pass", "example-host-failed.example.invalid",
		referenceEpoch()-120, referenceEpoch()-100)

	harness.fake.answer(opnsense.DnsmasqLeases, http.StatusInternalServerError, []byte(`{}`))
	if err := harness.collector.CollectDHCPLease(ctx); err == nil {
		t.Fatal("a lease pass whose backend answered 500 reported no failure")
	}
	if done, _ := examined(t, harness.store, "example-key-failed-pass"); done {
		t.Fatal("a lease pass that read no lease examined the lookup, using up its retry")
	}

	harness.fake.answerFixture(opnsense.DnsmasqLeases, "dnsmasq_leases.json")
	provider, err := harness.store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	hostname := "example-host-failed"
	if err := harness.store.InsertDHCPLease(ctx, provider, store.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.41", Hostname: &hostname, LeaseState: "reserved",
		GenerationKey: store.GenerationKeyOf(nil, nil, referenceEpoch()), ObservedAt: referenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	setClock(harness, referenceInstant().Add(5*time.Minute))
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if done, resolution := examined(t, harness.store, "example-key-failed-pass"); !done ||
		resolution != store.ClientResolutionLeased {
		t.Errorf("the first lease pass that read the backend left the lookup examined %t, %s", done, resolution)
	}
}

// TestALookupIngestedAfterTheLeaseReadBeganWaitsForTheNextPass: a lookup stamped at or
// after the instant the lease read began may postdate the leases read, so that pass does
// not examine it and the next one does.
func TestALookupIngestedAfterTheLeaseReadBeganWaitsForTheNextPass(t *testing.T) {
	t.Parallel()
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()
	unresolvedLookup(t, harness.store, "example-key-late", "example-host-late.example.invalid",
		referenceEpoch()-10, referenceEpoch())
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if done, _ := examined(t, harness.store, "example-key-late"); done {
		t.Fatal("a lookup ingested as the lease read began was examined against those leases")
	}
	setClock(harness, referenceInstant().Add(5*time.Minute))
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the next lease pass: %v", err)
	}
	if done, _ := examined(t, harness.store, "example-key-late"); !done {
		t.Error("the next lease pass did not examine the lookup")
	}
}
