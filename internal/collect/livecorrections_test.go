package collect_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The collector half of specs/SPEC-step-5a-live-corrections.md: the gateway absences
// (AC15), the late lookups (AC16), the host-name retry across a restart (AC14, H6), and
// the two pairing fields read from the filter log (AC7, AC9).

// gatewayReadings counts the stored gateway readings, and those whose value is 0.
func gatewayReadings(t *testing.T, database *store.Store) (int, int) {
	t.Helper()
	return collecttest.ScalarCount(t, database, "SELECT count(*) FROM measurement_sample WHERE subject_kind = 'gateway'"),
		collecttest.ScalarCount(t, database, "SELECT count(*) FROM measurement_sample WHERE subject_kind = 'gateway' AND value = 0")
}

// TestEachGatewayAbsenceIsNamedOnItsOwnAndWritesNoReading is AC15's second and third
// points: each of the five absences yields its own wording and no row, and "~" never
// yields 0.
func TestEachGatewayAbsenceIsNamedOnItsOwnAndWritesNoReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(fake *providertest.Firewall)
		wording string
	}{
		{"authentication failed", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.GatewayStatus, http.StatusUnauthorized,
				[]byte(`{"status":401,"message":"Authentication Failed"}`))
		}, "gateway latency and loss (authentication failed (HTTP 401): the firewall did not accept the API key and secret)"},
		{"denied", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.GatewayStatus, http.StatusForbidden, []byte(`{"status":403,"message":"Forbidden"}`))
		}, "gateway latency and loss (denied, HTTP 403: the API key's user needs the privilege \"System: Gateways\""},
		{"not found", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.GatewayStatus, http.StatusNotFound, []byte(`{"errorMessage":"Endpoint not found"}`))
		}, "gateway latency and loss (not found, HTTP 404"},
		{"failed", func(fake *providertest.Firewall) {
			fake.AnswerJSON(opnsense.GatewayStatus, map[string]any{"items": []any{}, "status": "failed"})
		}, "gateway latency and loss (the endpoint answered \"status\": \"failed\" with no item, which it does both when no gateway is configured"},
		{"no gateway", func(fake *providertest.Firewall) {
			fake.AnswerJSON(opnsense.GatewayStatus, map[string]any{"items": []any{}, "status": "ok"})
		}, "gateway latency and loss (the endpoint answered \"status\": \"ok\" and listed no gateway)"},
		{"no figure", func(fake *providertest.Firewall) {
			fake.AnswerFixture(opnsense.GatewayStatus, "gateway_status_unmonitored.json")
		}, "gateway latency and loss (2 of 2 gateways listed with no figure: dpinger.inc reports \"~\""},
	}
	// Every absence gets a harness of its own, so the absences run as parallel subtests, each with
	// its own database; each detail is compared with the wordings of the absences before it once
	// every absence has run.
	details := make([]string, len(cases))
	t.Run("absences", func(t *testing.T) {
		for index, one := range cases {
			t.Run(one.name, func(t *testing.T) {
				t.Parallel()
				harness := arrangeMeasurementCollection(t)
				one.arrange(harness.Fake)
				if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
					t.Fatalf("sampling: %v", err)
				}
				detail := harness.DetailOf(t, core.KindMeasurementSample, collecttest.ProviderInsight)
				if !strings.Contains(detail, one.wording) {
					t.Errorf("the detail is %q, which does not name the absence as %q", detail, one.wording)
				}
				details[index] = detail
				if rows, zeroes := gatewayReadings(t, harness.Store); rows != 0 || zeroes != 0 {
					t.Errorf("%d gateway readings, %d of them 0, were written for an absence", rows, zeroes)
				}
			})
		}
	})
	for index, one := range cases {
		for _, other := range cases[:index] {
			if strings.Contains(details[index], other.wording) {
				t.Errorf("the %s absence reads like the %s one", one.name, other.name)
			}
		}
	}
}

// TestTheGatewayMeasuresOfTheLiveShapeAreStoredPerGatewayWithFigures is AC15's first
// point: a gateway with figures gets its three measures, a gateway listed with "~" gets
// none and no zero, and the absence of the second is named while the first is stored.
func TestTheGatewayMeasuresOfTheLiveShapeAreStoredPerGatewayWithFigures(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if measured := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'gateway' AND subject_key = 'EXAMPLE_GW_A'`); measured != 3 {
		t.Errorf("the gateway with figures has %d readings, not its three measures", measured)
	}
	if unmeasured := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM measurement_sample
		WHERE subject_kind = 'gateway' AND subject_key = 'EXAMPLE_GW_B'`); unmeasured != 0 {
		t.Errorf("the gateway listed with ~ has %d readings", unmeasured)
	}
	if _, zeroes := gatewayReadings(t, harness.Store); zeroes != 0 {
		t.Errorf("%d gateway readings are 0", zeroes)
	}
	if detail := harness.DetailOf(t, core.KindMeasurementSample, collecttest.ProviderInsight); !strings.Contains(detail,
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
	harness.Fake.AnswerFixture(opnsense.GatewayStatus, "gateway_status_unmonitored.json")
	ctx := context.Background()
	if err := harness.Collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	if err := harness.Collector.ProbeMeasurement(ctx); err != nil {
		t.Fatalf("probing: %v", err)
	}
	if detail := harness.DetailOf(t, core.KindMeasurementSample, collecttest.ProviderInsight); !strings.Contains(detail,
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
func answerLookups(fake *providertest.Firewall, rows ...map[string]any) {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	fake.AnswerJSON(opnsense.SearchQueries, map[string]any{"total": len(rows), "rowCount": len(rows),
		"current": 1, "rows": list})
}

// TestALateLookupAttributesAFlowWithNoFurtherFlowPass is item 5.1 and AC16's first point:
// a flow stored and derived with no lookup is attributed by a later lookup pass alone,
// and a still later lookup of a second domain in the window takes the attribution away.
func TestALateLookupAttributesAFlowWithNoFurtherFlowPass(t *testing.T) {
	t.Parallel()
	harness := arrangeResolverCollection(t)
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	ctx := context.Background()
	if err := harness.Collector.ProbeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	answerLookups(harness.Fake)
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting the filter log: %v", err)
	}
	source, destination := "198.51.100.10", "203.0.113.20"
	var flowID, observedAt int64
	if err := harness.Store.DB().QueryRow(`SELECT id, observed_at FROM flow
		WHERE src_address = $1 AND dst_address = $2`, source, destination).Scan(&flowID, &observedAt); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	attributed := func() int {
		return collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM domain_attribution WHERE flow_id = $1", flowID)
	}
	if attributed() != 0 {
		t.Fatal("the flow is attributed before any lookup was stored")
	}

	answerLookups(harness.Fake, lookupRow(source, "example-late.example.invalid", observedAt-2))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting the lookups: %v", err)
	}
	if attributed() != 1 {
		t.Fatal("the lookup stored after the flow did not attribute it")
	}

	answerLookups(harness.Fake, lookupRow(source, "example-late.example.invalid", observedAt-2),
		lookupRow(source, "example-other.example.invalid", observedAt-1))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
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
	at := collecttest.ReferenceEpoch() - 100
	answerLookups(harness.Fake, lookupRow("198.51.100.10", "example-first.example.invalid", at))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	before := collecttest.CountRows(t, harness.Store, "dns_resolution")
	// The same lookup again, and a different one at the same instant: the newest stored.
	answerLookups(harness.Fake, lookupRow("198.51.100.10", "example-first.example.invalid", at),
		lookupRow("198.51.100.10", "example-second.example.invalid", at))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if after := collecttest.CountRows(t, harness.Store, "dns_resolution"); after != before+1 {
		t.Errorf("the pass stored %d rows; the new lookup at the newest instant is one, and the known one none",
			after-before)
	}
	if second := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM dns_resolution
		WHERE domain = 'example-second.example.invalid'`); second != 1 {
		t.Errorf("the lookup sharing the newest instant is stored %d times, not once", second)
	}
	if first := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM dns_resolution
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
	answerLookups(harness.Fake, lookupRow(logged, "example-site.example.invalid", collecttest.ReferenceEpoch()-120))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if unknown := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM dns_resolution
		WHERE client_hostname = $1 AND client_resolution = 'unknown_hostname'`, logged); unknown != 1 {
		t.Fatalf("the lookup was not stored as an unknown host name (%d)", unknown)
	}

	// The restart: a new collector over the same database, an hour later.
	restarted := collecttest.NewTestCollector(t, providertest.NewClient(t, harness.Fake), harness.Store,
		collecttest.NewFixedClock(collecttest.ReferenceInstant().Add(time.Hour)))
	provider, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	hostname := "example-restart-host"
	expires := collecttest.ReferenceEpoch() + 86400
	if err := harness.Store.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.30", Hostname: &hostname, LeaseState: "active",
		GenerationKey: vocab.GenerationKeyOf(nil, &expires, 0), ExpiresAt: &expires,
		ObservedAt: collecttest.ReferenceEpoch() + 3600,
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	// What the first lease pass after the restart derives.
	if err := restarted.Derive(ctx, derive.Derivation{Hostnames: true, HostnamesBefore: collecttest.ReferenceEpoch() + 3600}); err != nil {
		t.Fatalf("deriving the lease pass: %v", err)
	}
	if resolved := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM dns_resolution
		WHERE client_hostname = $1 AND client_resolution = 'lease_hostname'
		  AND client_address = '198.51.100.30'`, logged); resolved != 1 {
		t.Error("the lookup stored before the restart was not resolved by the first lease pass after it")
	}
}

// TestTheFilterLogStoresTheTwoPairingFieldsAndPairsTheTwoLegs: `id` and `seq` are read at
// the positions filterlog writes them, an IPv6 record has no `id`, and a NATed connection
// logged on two interfaces is paired by the collector's own derivation.
func TestTheFilterLogStoresTheTwoPairingFieldsAndPairsTheTwoLegs(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
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
	harness.Fake.AnswerJSON(opnsense.FirewallLog, rows)
	ctx := context.Background()
	if err := harness.Collector.ProbeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if stored := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM flow
		WHERE ip_id = 4242 AND tcp_seq = 123456789`); stored != 2 {
		t.Errorf("%d records carry the identification and the sequence number, not 2", stored)
	}
	if v6 := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM flow
		WHERE ip_version = 6 AND ip_id IS NULL AND tcp_seq IS NULL`); v6 != 1 {
		t.Error("the IPv6 UDP record carries a field it does not have")
	}
	if paired := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM flow AS f JOIN flow AS p
		ON p.id = f.paired_flow_id AND p.paired_flow_id = f.id
		WHERE f.pair_outcome = 'second_leg' AND f.src_is_this_firewall AND p.pair_outcome = 'first_leg'`); paired != 1 {
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
			t.Parallel()
			harness := collecttest.NewProbeHarness(t)
			ctx := context.Background()
			harness.Fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
			harness.Fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
			harness.Fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
			held := "192.0.2.1"
			for index, read := range []bool{true, true, false, false, true} {
				collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Duration(index)*5*time.Minute))
				address := ""
				if read {
					address = held
				}
				harness.Fake.AnswerJSON(opnsense.InterfacesInfo, interfacesInfoBody(heldFirst, address))
				if err := harness.Collector.RefreshDiscovery(ctx); err != nil {
					t.Fatalf("discovery %d: %v", index, err)
				}
			}
			if holdings := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM interface_address
				WHERE address = $1 AND source_field = 'addr4'`, held); holdings != 2 {
				t.Errorf("the address released and re-acquired is %d rows, not two holdings", holdings)
			}
			gap := collecttest.ReferenceEpoch() + int64((12*time.Minute + 30*time.Second).Seconds())
			if spanned := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM this_firewall_address
				WHERE address = $1 AND first_seen_at - 300 <= $2 AND (is_current = 1 OR last_seen_at + 300 >= $3)`,
				held, gap, gap); spanned != 0 {
				t.Error("a holding of the address spans the gap between its two holdings")
			}
		})
	}
}

// TestAFailedLeasePassDoesNotUseUpALookupsRetry is H6 against a failed lease read: a
// pass that could not read the active backend examines nothing, so the lookup is still
// resolved by the first pass that does read it.
func TestAFailedLeasePassDoesNotUseUpALookupsRetry(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()
	collecttest.UnresolvedLookup(t, harness.Store, "example-key-failed-pass", "example-host-failed.example.invalid",
		collecttest.ReferenceEpoch()-120, collecttest.ReferenceEpoch()-100)

	harness.Fake.Answer(opnsense.DnsmasqLeases, http.StatusInternalServerError, []byte(`{}`))
	if err := harness.Collector.CollectDHCPLease(ctx); err == nil {
		t.Fatal("a lease pass whose backend answered 500 reported no failure")
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-failed-pass"); done {
		t.Fatal("a lease pass that read no lease examined the lookup, using up its retry")
	}

	harness.Fake.AnswerFixture(opnsense.DnsmasqLeases, "dnsmasq_leases.json")
	provider, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	hostname := "example-host-failed"
	if err := harness.Store.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.41", Hostname: &hostname, LeaseState: "reserved",
		GenerationKey: vocab.GenerationKeyOf(nil, nil, collecttest.ReferenceEpoch()), ObservedAt: collecttest.ReferenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(5*time.Minute))
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if done, resolution := collecttest.Examined(t, harness.Store, "example-key-failed-pass"); !done ||
		resolution != vocab.ClientResolutionLeased {
		t.Errorf("the first lease pass that read the backend left the lookup examined %t, %s", done, resolution)
	}
}

// TestALookupIngestedAfterTheLeaseReadBeganWaitsForTheNextPass: a lookup stamped at or
// after the instant the lease read began may postdate the leases read, so that pass does
// not examine it and the next one does.
func TestALookupIngestedAfterTheLeaseReadBeganWaitsForTheNextPass(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()
	collecttest.UnresolvedLookup(t, harness.Store, "example-key-late", "example-host-late.example.invalid",
		collecttest.ReferenceEpoch()-10, collecttest.ReferenceEpoch())
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-late"); done {
		t.Fatal("a lookup ingested as the lease read began was examined against those leases")
	}
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(5*time.Minute))
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the next lease pass: %v", err)
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-late"); !done {
		t.Error("the next lease pass did not examine the lookup")
	}
}
