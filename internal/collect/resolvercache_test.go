package collect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The collector half of specs/SPEC-resolver-cache-attribution.md: the two resolver-record
// kinds (AC4, AC5), the late arrival of their evidence (AC9, AC10), host names resolved
// through local data (AC16), the local data's PTR records (scope D1) and the processor's
// stream (AC18).

// fixtureRows returns the data rows of a record-list fixture.
func fixtureRows(t *testing.T, name string) []map[string]any {
	t.Helper()
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(providertest.FixtureBody(t, name), &body); err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
	return body.Data
}

// answerRecords makes one record endpoint answer status ok with these rows.
func answerRecords(fake *providertest.Firewall, endpoint opnsense.Endpoint, rows []map[string]any) {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	fake.AnswerJSON(endpoint, map[string]any{"status": "ok", "data": list})
}

// observationCount counts the stored resolver records.
func observationCount(t *testing.T, database *store.Store) int {
	t.Helper()
	return collecttest.ScalarCount(t, database, "SELECT count(*) FROM resource_record_observation")
}

// TestADumpOfEveryRRTypeStoresOnlyAAAAAAndCNAME is AC5: on a dump of every rrtype the
// shape admits, multiplied by a count, only A, AAAA and CNAME records are stored; the
// availability detail gives the count of each type and the response's size; a record whose
// value is not valid is skipped and counted; and `type`, the class, is never read as the
// record type. The fixture's message-cache references are the subject of
// TestMessageCacheReferencesAreNamedForWhatTheyAreAndNotCountedAsABadTTL.
func TestADumpOfEveryRRTypeStoresOnlyAAAAAAndCNAME(t *testing.T) {
	t.Parallel()
	for _, copies := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d copies", copies), func(t *testing.T) {
			harness := collecttest.ArrangeRecordCollection(t)
			template := fixtureRows(t, "unbound_dumpcache.json")
			var rows []map[string]any
			for copy := 0; copy < copies; copy++ {
				for _, row := range template {
					renamed := map[string]any{}
					for key, value := range row {
						renamed[key] = value
					}
					renamed["host"] = fmt.Sprintf("copy-%d.%s", copy, row["host"])
					if row["rrtype"] == "CNAME" {
						renamed["value"] = fmt.Sprintf("copy-%d.%s", copy, row["value"])
					}
					rows = append(rows, renamed)
				}
				// The class field carrying a record type's name, on a type that is not
				// stored: if `type` were read as the type, this would be an A record.
				rows = append(rows, map[string]any{"host": fmt.Sprintf("copy-%d.text.example.com.", copy),
					"ttl": "300", "type": "A", "rrtype": "TXT", "value": "192.0.2.99"})
			}
			answerRecords(harness.Fake, opnsense.UnboundDumpCache, rows)
			if err := harness.Collector.CollectResolverCache(context.Background()); err != nil {
				t.Fatalf("reading the cache: %v", err)
			}
			if types := stringColumn(t, harness.Store,
				"SELECT DISTINCT rrtype FROM resource_record_observation ORDER BY rrtype"); strings.Join(types, ",") != "A,AAAA,CNAME" {
				t.Errorf("the stored types are %v, not A, AAAA and CNAME", types)
			}
			// Per copy: the CNAME, the edge's A and AAAA; the IPv6-as-A record is skipped,
			// the message-cache references are not records, and every other type is
			// counted only.
			if stored := observationCount(t, harness.Store); stored != 3*copies {
				t.Errorf("%d records were stored, not %d", stored, 3*copies)
			}
			if text := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM resource_record_observation
				WHERE owner_name LIKE '%text.example.com'`); text != 0 {
				t.Error("a TXT record whose class field read A was stored: `type` was read as the record type")
			}
			detail := harness.DetailOf(t, core.KindResolverCache, collecttest.ProviderUnbound)
			references := 0
			for _, row := range template {
				if row["ttl"] == nil {
					references++
				}
			}
			for _, want := range []string{
				fmt.Sprintf("read %d records", len(rows)-references*copies),
				fmt.Sprintf("A %d", 2*copies), fmt.Sprintf("NS %d", copies), fmt.Sprintf("RRSIG %d", copies),
				fmt.Sprintf("TXT %d", copies), fmt.Sprintf("HTTPS %d", copies), "bytes)",
				fmt.Sprintf("skipped %d message-cache references", references*copies),
				fmt.Sprintf("skipped %d whose value is not valid for their type", copies),
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("the detail %q does not say %q", detail, want)
				}
			}
			if state, _, _ := harness.AvailabilityOf(t, core.KindResolverCache, collecttest.ProviderUnbound); state != vocab.StateReachable {
				t.Errorf("a cache read that answered is %q", state)
			}
		})
	}
}

// TestMessageCacheReferencesAreNamedForWhatTheyAreAndNotCountedAsABadTTL is
// specs/SPEC-resolver-cache-follow-ups.md AC4b. A cache row whose ttl is JSON null is a
// message-cache reference -- the line Unbound's dump_msg_ref prints as name, class, type
// and flags, which wrapper.py lets through with no ttl and the flags as its value -- and is
// counted under its own wording, never stored and never counted as a bad ttl. A ttl that is
// a string of digits, 0 included, is the seconds left; a non-digit string, a negative
// value, a JSON number and an absent ttl are still bad ttls.
func TestMessageCacheReferencesAreNamedForWhatTheyAreAndNotCountedAsABadTTL(t *testing.T) {
	t.Parallel()
	for _, references := range []int{1, 4} {
		t.Run(fmt.Sprintf("%d references per rrtype", references), func(t *testing.T) {
			harness := collecttest.ArrangeRecordCollection(t)
			var rows []map[string]any
			for _, v6 := range []bool{false, true} {
				family, rrtype, address := "v4", "A", "192.0.2.20"
				if v6 {
					family, rrtype, address = "v6", "AAAA", "2001:db8::20"
				}
				alias := "www-" + family + ".example.com."
				edge := "edge-" + family + ".example.net."
				rows = append(rows,
					map[string]any{"host": alias, "ttl": "300", "type": "IN", "rrtype": "CNAME", "value": edge},
					map[string]any{"host": edge, "ttl": "0", "type": "IN", "rrtype": rrtype, "value": address},
					// The bad ttls: not digits, negative, a number rather than a string, absent.
					map[string]any{"host": "letters-" + edge, "ttl": "3OO", "type": "IN", "rrtype": rrtype, "value": address},
					map[string]any{"host": "negative-" + edge, "ttl": "-5", "type": "IN", "rrtype": rrtype, "value": address},
					map[string]any{"host": "number-" + edge, "ttl": 300, "type": "IN", "rrtype": rrtype, "value": address},
					map[string]any{"host": "absent-" + edge, "type": "IN", "rrtype": rrtype, "value": address})
				// The message-cache section, after the records, of the live shape.
				for index := 0; index < references; index++ {
					rows = append(rows,
						map[string]any{"host": alias, "ttl": nil, "type": "IN", "rrtype": "CNAME", "value": "0"},
						map[string]any{"host": edge, "ttl": nil, "type": "IN", "rrtype": rrtype, "value": "0"})
				}
			}
			answerRecords(harness.Fake, opnsense.UnboundDumpCache, rows)
			if err := harness.Collector.CollectResolverCache(context.Background()); err != nil {
				t.Fatalf("reading the cache: %v", err)
			}
			// The CNAME and the address of each family, the address with its ttl of 0.
			if stored := observationCount(t, harness.Store); stored != 4 {
				t.Errorf("%d records were stored, not the 4 whose ttl is a string of digits", stored)
			}
			if zero := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM resource_record_observation
				WHERE rrtype IN ('A', 'AAAA') AND covered_until_at = last_seen_at`); zero != 2 {
				t.Errorf("%d address records were stored with their ttl of 0, not 2", zero)
			}
			detail := harness.DetailOf(t, core.KindResolverCache, collecttest.ProviderUnbound)
			for _, want := range []string{
				"read 12 records",
				fmt.Sprintf("skipped %d message-cache references: lines of the dump's message-cache section",
					4*references),
				"skipped 8 whose ttl is not a whole number of seconds",
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("the detail %q does not say %q", detail, want)
				}
			}
		})
	}
}

// TestEachRecordReadAbsenceHasItsOwnWordingAndStoresNothing is AC4 for both endpoints:
// authentication failed (401), which names no privilege, denied (403) naming the
// privilege, not found, undecodable, and a status that is not ok -- each its own wording,
// none storing an observation or moving the last poll.
func TestEachRecordReadAbsenceHasItsOwnWordingAndStoresNothing(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []struct {
		endpoint opnsense.Endpoint
		kind     string
		collect  func(*collect.Collector, context.Context) error
	}{
		{opnsense.UnboundDumpCache, core.KindResolverCache, (*collect.Collector).CollectResolverCache},
		{opnsense.UnboundListLocalData, core.KindResolverLocalData, (*collect.Collector).CollectResolverLocalData},
	} {
		details := map[string]string{}
		for _, absence := range []struct {
			name   string
			status int
			body   string
			want   string
		}{
			{"unauthorised", http.StatusUnauthorized, `{"status":401,"message":"Authentication Failed"}`,
				"authentication failed (HTTP 401)"},
			{"forbidden", http.StatusForbidden, `{"errorMessage":"Forbidden"}`, `"Services: Unbound"`},
			{"not found", http.StatusNotFound, `{"errorMessage":"Endpoint not found"}`, "not found (HTTP 404)"},
			{"undecodable", http.StatusOK, `["not", "the", "envelope"]`, "undecodable"},
			{"no data list", http.StatusOK, `{"status":"ok"}`, "no data list"},
			{"failed status", http.StatusOK, `{"status":"failed"}`, `status "failed"`},
		} {
			harness := collecttest.ArrangeRecordCollection(t)
			harness.Fake.Answer(endpoint.endpoint, absence.status, []byte(absence.body))
			if err := endpoint.collect(harness.Collector, context.Background()); err == nil {
				t.Errorf("%s %s: the pass reported no failure", endpoint.endpoint.Path, absence.name)
			}
			detail := harness.DetailOf(t, endpoint.kind, collecttest.ProviderUnbound)
			if !strings.Contains(detail, absence.want) || !strings.Contains(detail, "nothing was stored") {
				t.Errorf("%s %s: the detail %q does not say %q and that nothing was stored",
					endpoint.endpoint.Path, absence.name, detail, absence.want)
			}
			// A 401 is a key or secret the firewall did not accept, which no privilege cures
			// (ApiControllerBase::beforeExecuteRoute answers 403 for a missing page).
			if absence.status == http.StatusUnauthorized && strings.Contains(detail, "privilege") {
				t.Errorf("%s %s: the detail %q blames a privilege for a failed authentication",
					endpoint.endpoint.Path, absence.name, detail)
			}
			if state, _, _ := harness.AvailabilityOf(t, endpoint.kind, collecttest.ProviderUnbound); state != vocab.StateUnavailable {
				t.Errorf("%s %s: the state is %q", endpoint.endpoint.Path, absence.name, state)
			}
			if stored := observationCount(t, harness.Store); stored != 0 {
				t.Errorf("%s %s stored %d observations", endpoint.endpoint.Path, absence.name, stored)
			}
			if polls := collecttest.CountRows(t, harness.Store, "resource_record_read"); polls != 0 {
				t.Errorf("%s %s recorded a successful poll", endpoint.endpoint.Path, absence.name)
			}
			details[absence.name] = detail
		}
		// Own wording: no two absences read alike.
		seen := map[string]string{}
		for name, detail := range details {
			if other, duplicate := seen[detail]; duplicate {
				t.Errorf("%s: %s and %s read alike: %q", endpoint.endpoint.Path, name, other, detail)
			}
			seen[detail] = name
		}
	}
}

// TestDnsmasqReportsItsNamedNoReadStates is AC4's Dnsmasq point: the resolver whose API
// reads neither its cache nor its local data reports the named state, whether the probe
// round or a pass meets it, and stores nothing.
func TestDnsmasqReportsItsNamedNoReadStates(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	ctx := context.Background()
	for _, kind := range []struct {
		kind, want string
		collect    func(context.Context) error
	}{
		{core.KindResolverCache, "this resolver offers no cache read through the API", harness.Collector.CollectResolverCache},
		{core.KindResolverLocalData, "this resolver offers no local-data read through the API", harness.Collector.CollectResolverLocalData},
	} {
		if detail := harness.DetailOf(t, kind.kind, collecttest.ProviderDnsmasq); !strings.Contains(detail, kind.want) {
			t.Errorf("after the probe round the %s Dnsmasq detail is %q", kind.kind, detail)
		}
		// The operator turns it on: the pass meets the refusal and records it.
		if err := harness.Store.SetActiveProviders(ctx, kind.kind, collecttest.ProviderDnsmasq); err != nil {
			t.Fatal(err)
		}
		if err := kind.collect(ctx); err != nil {
			t.Errorf("a resolver read the API does not offer is a state, not a failed pass: %v", err)
		}
		if detail := harness.DetailOf(t, kind.kind, collecttest.ProviderDnsmasq); !strings.Contains(detail, kind.want) {
			t.Errorf("after a pass the %s Dnsmasq detail is %q", kind.kind, detail)
		}
	}
	if stored := observationCount(t, harness.Store); stored != 0 {
		t.Errorf("%d observations were stored from a resolver that offers none", stored)
	}
}

// TestTheCacheAndTheQueryReportKeepTheirOwnAvailability is AC4's last point: a cache
// failure never overwrites the query report's availability, and the reverse.
func TestTheCacheAndTheQueryReportKeepTheirOwnAvailability(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	ctx := context.Background()
	if err := harness.Collector.ProbeDNSLookup(ctx); err != nil {
		t.Fatalf("probing the resolver: %v", err)
	}
	answerLookups(harness.Fake, lookupRow("198.51.100.10", "example-site.example.invalid", collecttest.ReferenceEpoch()-30))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("reading the query report: %v", err)
	}
	harness.Fake.Answer(opnsense.UnboundDumpCache, http.StatusForbidden, []byte(`{}`))
	_ = harness.Collector.CollectResolverCache(ctx)
	if state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); state != vocab.StateReachable {
		t.Errorf("a refused cache read moved the query report to %q", state)
	}
	if state, _, _ := harness.AvailabilityOf(t, core.KindResolverCache, collecttest.ProviderUnbound); state != vocab.StateUnavailable {
		t.Errorf("the refused cache read is %q", state)
	}

	answerRecords(harness.Fake, opnsense.UnboundDumpCache, fixtureRows(t, "unbound_dumpcache.json"))
	if err := harness.Collector.CollectResolverCache(ctx); err != nil {
		t.Fatalf("reading the cache: %v", err)
	}
	harness.Fake.Answer(opnsense.SearchQueries, http.StatusInternalServerError, []byte(`{}`))
	_ = harness.Collector.CollectDNSLookup(ctx)
	if state, _, _ := harness.AvailabilityOf(t, core.KindResolverCache, collecttest.ProviderUnbound); state != vocab.StateReachable {
		t.Errorf("a failed query report moved the cache to %q", state)
	}
	if state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); state != vocab.StateUnavailable {
		t.Errorf("the failed query report is %q", state)
	}
}

// TestACacheReadNamesAFlowThatThreeLookupsCouldNot is AC9's first outcome and AC10's
// second point end to end: a flow and three lookups in its window are stored and derived
// with no attribution, and the cache pass that stores the evidence names it, at that pass.
func TestACacheReadNamesAFlowThatThreeLookupsCouldNot(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	ctx := context.Background()
	if err := harness.Collector.ProbeDNSLookup(ctx); err != nil {
		t.Fatalf("probing the resolver: %v", err)
	}
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
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
		WHERE src_address = ? AND dst_address = ?`, source, destination).Scan(&flowID, &observedAt); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	answerLookups(harness.Fake,
		lookupRow(source, "example-first.example.com", observedAt-3),
		lookupRow(source, "example-second.example.com", observedAt-2),
		lookupRow(source, "example-third.example.com", observedAt-1))
	if err := harness.Collector.CollectDNSLookup(ctx); err != nil {
		t.Fatalf("collecting the lookups: %v", err)
	}
	attributionOf := func() (int, string) {
		var method string
		count := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM domain_attribution WHERE flow_id = ?", flowID)
		if count == 1 {
			if err := harness.Store.DB().QueryRow("SELECT method FROM domain_attribution WHERE flow_id = ?",
				flowID).Scan(&method); err != nil {
				t.Fatal(err)
			}
		}
		return count, method
	}
	if count, _ := attributionOf(); count != 0 {
		t.Fatal("three domains in the window attributed the flow before any cache read")
	}

	// The cache is read a minute before the flow: the first poll's coverage starts at its
	// own instant, so it covers the lookups and the flow.
	collecttest.SetClock(harness, time.Unix(observedAt-60, 0))
	answerRecords(harness.Fake, opnsense.UnboundDumpCache, []map[string]any{
		{"host": "example-second.example.com.", "ttl": "3600", "type": "IN", "rrtype": "CNAME",
			"value": "edge.example.net."},
		{"host": "edge.example.net.", "ttl": "3600", "type": "IN", "rrtype": "A", "value": destination},
	})
	if err := harness.Collector.CollectResolverCache(ctx); err != nil {
		t.Fatalf("reading the cache: %v", err)
	}
	if count, method := attributionOf(); count != 1 || method != attribution.MethodResolverCacheAnswer {
		t.Fatalf("the cache pass left the flow with %d attributions by %q", count, method)
	}
	var site string
	if err := harness.Store.DB().QueryRow("SELECT site_name FROM domain_attribution WHERE flow_id = ?",
		flowID).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if site != "example-second.example.com" {
		t.Errorf("the site name is %q, not the name the client looked up", site)
	}
}

// unresolvedHostLookup makes the query report return one lookup whose client is a host
// name, and stores it.
func unresolvedHostLookup(t *testing.T, harness *collecttest.ProbeHarness, hostname string, at int64) {
	t.Helper()
	answerLookups(harness.Fake, lookupRow(hostname, "example-site.example.com", at))
	if err := harness.Collector.CollectDNSLookup(context.Background()); err != nil {
		t.Fatalf("collecting the lookup: %v", err)
	}
}

// resolutionOf reads how the lookup of one logged host name was resolved.
func resolutionOf(t *testing.T, database *store.Store, hostname string) (string, string) {
	t.Helper()
	var resolution, address string
	if err := database.DB().QueryRow(`SELECT client_resolution, client_address FROM dns_resolution
		WHERE client_hostname = ?`, hostname).Scan(&resolution, &address); err != nil {
		t.Fatalf("reading the lookup of %s: %v", hostname, err)
	}
	return resolution, address
}

// localDataRows are the local data naming one host, in one family.
func localDataRows(hostname, address string) []map[string]any {
	rrtype := "A"
	if strings.Contains(address, ":") {
		rrtype = "AAAA"
	}
	return []map[string]any{
		{"name": hostname + ".", "ttl": "3600", "type": "IN", "rrtype": rrtype, "value": address},
		{"name": "unrelated.example.test.", "ttl": "3600", "type": "IN", "rrtype": "A", "value": "192.0.2.77"},
	}
}

// TestAnUnknownHostNameIsResolvedByLocalDataThatArrivesLater is AC16 through the
// collector, in both families and across a restart: the lookup is stored unresolved, the
// local data naming its host arrives at a later pass, and that pass resolves it to
// local_data_hostname. Two addresses of one family give ambiguous_hostname, and, as AC16
// is amended by group C of specs/SPEC-step-5b-widget-api.md, one A and one AAAA with no
// querying family reported give dual_stack_hostname (the family cases are in
// dualstack_test.go).
func TestAnUnknownHostNameIsResolvedByLocalDataThatArrivesLater(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		addresses []string
		restart   bool
		want      string
	}{
		{"one IPv4 address", []string{"192.0.2.40"}, false, vocab.ClientResolutionLocalData},
		{"one IPv6 address", []string{"2001:db8::40"}, false, vocab.ClientResolutionLocalData},
		{"one address, across a restart", []string{"2001:db8::41"}, true, vocab.ClientResolutionLocalData},
		{"two addresses of one family", []string{"192.0.2.42", "192.0.2.43"}, false,
			vocab.ClientResolutionAmbiguous},
		{"one A and one AAAA, no family reported", []string{"192.0.2.44", "2001:db8::44"}, false,
			vocab.ClientResolutionDualStack},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := collecttest.ArrangeRecordCollection(t)
			ctx := context.Background()
			if err := harness.Collector.ProbeDNSLookup(ctx); err != nil {
				t.Fatalf("probing the resolver: %v", err)
			}
			hostname := "example-host.example.test"
			// A first local-data poll, before the lookup, that does not name the host.
			collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(-5*time.Minute))
			answerRecords(harness.Fake, opnsense.UnboundListLocalData, localDataRows("other-host.example.test", "192.0.2.50"))
			if err := harness.Collector.CollectResolverLocalData(ctx); err != nil {
				t.Fatalf("the first local-data pass: %v", err)
			}
			collecttest.SetClock(harness, collecttest.ReferenceInstant())
			unresolvedHostLookup(t, harness, hostname, collecttest.ReferenceEpoch()-120)
			if resolution, _ := resolutionOf(t, harness.Store, hostname); resolution != vocab.ClientResolutionUnknown {
				t.Fatalf("the lookup was stored %s before any data named its host", resolution)
			}

			collector := harness.Collector
			if testCase.restart {
				collector = collecttest.NewTestCollector(t, providertest.NewClient(t, harness.Fake), harness.Store,
					collecttest.NewFixedClock(collecttest.ReferenceInstant().Add(time.Hour)))
			} else {
				collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(5*time.Minute))
			}
			var rows []map[string]any
			for _, address := range testCase.addresses {
				rows = append(rows, localDataRows(hostname, address)[0])
			}
			answerRecords(harness.Fake, opnsense.UnboundListLocalData, rows)
			if err := collector.CollectResolverLocalData(ctx); err != nil {
				t.Fatalf("the local-data pass: %v", err)
			}
			resolution, address := resolutionOf(t, harness.Store, hostname)
			if resolution != testCase.want {
				t.Errorf("the lookup is %s, not %s", resolution, testCase.want)
			}
			if testCase.want == vocab.ClientResolutionLocalData {
				if canonical, _ := attribution.CanonicalAddress(testCase.addresses[0]); address != canonical.String() {
					t.Errorf("the lookup's client is %q, not the one address the local data named", address)
				}
			}
		})
	}
}

// TestALeasePassWaitsForTheLocalDataBeforeExaminingALookup: with both sources of host
// names in use, a lookup's one examination waits until the local data has been read after
// it was ingested, so a lease pass alone does not spend it.
func TestALeasePassWaitsForTheLocalDataBeforeExaminingALookup(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()
	harness.Fake.AnswerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
	harness.Fake.AnswerFixture(opnsense.UnboundSettings, "unbound_settings.json")
	if err := harness.Collector.ProbeResolverLocalData(ctx); err != nil {
		t.Fatalf("probing the local data: %v", err)
	}
	// The local data's first poll ever, before the lookup: it vouches for no instant
	// before its own, so it is not yet a read the lookup can wait for.
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(-5*time.Minute))
	answerRecords(harness.Fake, opnsense.UnboundListLocalData, localDataRows("other-host.example.test", "192.0.2.61"))
	if err := harness.Collector.CollectResolverLocalData(ctx); err != nil {
		t.Fatalf("the first local-data pass: %v", err)
	}
	collecttest.SetClock(harness, collecttest.ReferenceInstant())
	collecttest.UnresolvedLookup(t, harness.Store, "example-key-waits", "example-host-waits.example.test",
		collecttest.ReferenceEpoch()-120, collecttest.ReferenceEpoch()-100)
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the lease pass: %v", err)
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-waits"); done {
		t.Fatal("a lease pass examined the lookup before the local data had been read after it")
	}
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Minute))
	answerRecords(harness.Fake, opnsense.UnboundListLocalData, localDataRows("example-host-waits.example.test", "192.0.2.60"))
	if err := harness.Collector.CollectResolverLocalData(ctx); err != nil {
		t.Fatalf("the local-data pass: %v", err)
	}
	if done, resolution := collecttest.Examined(t, harness.Store, "example-key-waits"); !done ||
		resolution != vocab.ClientResolutionLocalData {
		t.Errorf("the pass that read both sources left the lookup examined %t, %s; want examined and "+
			"resolved by the local data", done, resolution)
	}
}

// arrangeProcessorStream stands up a measurement harness and answers the processor's
// stream with these events, held open as the firewall's is.
func arrangeProcessorStream(t *testing.T, events ...string) *collecttest.ProbeHarness {
	t.Helper()
	harness := arrangeMeasurementCollection(t)
	payloads := make([][]byte, 0, len(events))
	for _, event := range events {
		payloads = append(payloads, []byte(event))
	}
	harness.Fake.AnswerStream(opnsense.CPUUsageStream, payloads, true)
	return harness
}

// processorReadings reads the stored processor readings.
func processorReadings(t *testing.T, database *store.Store) []float64 {
	t.Helper()
	rows, err := database.DB().Query("SELECT value FROM measurement_sample WHERE measure = ?",
		string(vocab.MeasureCPUUseRatio))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var values []float64
	for rows.Next() {
		var value float64
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}

// TestTheProcessorReadingIsTheDocumentedDerivationOfTheSecondEvent is AC18's second point:
// on the fixture of the verified shape, the reading is total / (total + idle) of the second
// event -- the first being iostat's average since boot.
func TestTheProcessorReadingIsTheDocumentedDerivationOfTheSecondEvent(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	var body struct {
		Events []struct{ Total, Idle float64 } `json:"events"`
	}
	if err := json.Unmarshal(providertest.FixtureBody(t, "cpu_usage_stream.json"), &body); err != nil {
		t.Fatal(err)
	}
	want := body.Events[1].Total / (body.Events[1].Total + body.Events[1].Idle)
	if got := processorReadings(t, harness.Store); len(got) != 1 || got[0] != want {
		t.Errorf("the processor readings are %v, want the second event's %v", got, want)
	}
	if requests := harness.Fake.RequestsTo(opnsense.CPUUsageStream); len(requests) != 1 {
		t.Errorf("the stream was requested %d times", len(requests))
	}
	// getActivity carries no processor figure, so it is not registered at all
	// (specs/SPEC-resolver-cache-closing.md, scope 2): the client refuses any path outside
	// the registry, so no /api/diagnostics/activity/ path can be read for one.
	for _, endpoint := range opnsense.Registry() {
		if strings.HasPrefix(endpoint.Path, "/api/diagnostics/activity/") {
			t.Errorf("the registry holds %s, which carries no processor figure", endpoint.Path)
		}
	}
}

// TestEachProcessorAbsenceIsNamedOnItsOwnAndWritesNoReading is AC18's third point, and its
// fourth for a stream that sends one event and then nothing.
func TestEachProcessorAbsenceIsNamedOnItsOwnAndWritesNoReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(*providertest.Firewall)
		want    string
	}{
		{"authentication failed", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.CPUUsageStream, http.StatusUnauthorized,
				[]byte(`{"status":401,"message":"Authentication Failed"}`))
		}, "authentication failed (HTTP 401)"},
		{"denied", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.CPUUsageStream, http.StatusForbidden, []byte(`{}`))
		}, `privilege "Lobby: Dashboard"`},
		{"not found", func(fake *providertest.Firewall) {
			fake.Answer(opnsense.CPUUsageStream, http.StatusNotFound, []byte(`{}`))
		}, "not found, HTTP 404"},
		{"no event in time", func(fake *providertest.Firewall) {
			fake.AnswerStream(opnsense.CPUUsageStream, nil, true)
		}, "no event within 5 s"},
		{"only the since-boot event", func(fake *providertest.Firewall) {
			fake.AnswerStream(opnsense.CPUUsageStream, [][]byte{[]byte(`{"total":9,"idle":91}`)}, true)
		}, "no event after the first within 5 s"},
		{"an undecodable event", func(fake *providertest.Firewall) {
			fake.AnswerStream(opnsense.CPUUsageStream, [][]byte{[]byte(`{"total":9,"idle":91}`),
				[]byte(`not json`)}, true)
		}, "could not be read as {total, idle}"},
		{"a stream that ends at once", func(fake *providertest.Firewall) {
			fake.AnswerStream(opnsense.CPUUsageStream, nil, false)
		}, "the stream ended before any event"},
	}
	// Each absence has its own wording: no two cases expect the same words.
	wordings := map[string]bool{}
	for _, testCase := range cases {
		wordings[testCase.want] = true
	}
	if len(wordings) != len(cases) {
		t.Errorf("%d absences share %d wordings", len(cases), len(wordings))
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := arrangeMeasurementCollection(t)
			// The stream answer of the arrangement is replaced; a plain answer needs the
			// stream entry gone.
			harness.Fake.RemoveStream(opnsense.CPUUsageStream)
			testCase.arrange(harness.Fake)
			started := time.Now()
			if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
				t.Fatalf("sampling: %v", err)
			}
			if elapsed := time.Since(started); elapsed > processorStreamWithin+5*time.Second {
				t.Errorf("the pass took %v, beyond the stream's bound", elapsed)
			}
			if got := processorReadings(t, harness.Store); len(got) != 0 {
				t.Errorf("an absence wrote the readings %v", got)
			}
			detail := harness.DetailOf(t, core.KindMeasurementSample, collecttest.ProviderInsight)
			if !strings.Contains(detail, testCase.want) {
				t.Errorf("the detail %q does not say %q", detail, testCase.want)
			}
		})
	}
}

// TestTheUnverifiedProcessorPathIsGone is AC18's first point, as the grep it names: no
// unverifiedCPUKeys anywhere in the package, and no fixture whose body carries the
// synthesised `cpu` key. activity.json, which carried it until the resolver-cache cycle,
// went with the getActivity registry entry it was served for
// (specs/SPEC-resolver-cache-closing.md, scope 2), so the check now runs over every
// fixture rather than that one.
func TestTheUnverifiedProcessorPathIsGone(t *testing.T) {
	t.Parallel()
	// The package and its parts (core, derive, the kinds' coordinators...), each a directory below it.
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "unverifiedCPU"+"Keys") {
			t.Errorf("%s still carries the unverified processor keys", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range providertest.FixtureNames(t) {
		// A body that is not an object carries no key, so a decoding failure here is no
		// finding: readFixture already holds every fixture to valid JSON.
		var body map[string]any
		if err := json.Unmarshal(providertest.FixtureBody(t, name), &body); err != nil {
			continue
		}
		if _, present := body["cpu"]; present {
			t.Errorf("%s carries a synthesised `cpu` key", name)
		}
	}
}

// TestLocalDataPTRRecordsMapTheirReverseNameToAnAddressAndTheirTargetToAHost is scope D1's
// PTR path, through the collection of unbound_listlocaldata.json: a PTR record's owner
// name is decoded to the address it encodes -- four decimal labels under in-addr.arpa and
// thirty-two nibble labels under ip6.arpa, least significant first -- and its target's
// first label is the host label; the TXT record is counted and not stored; and a reverse
// name that encodes no address is counted as skipped and not stored.
func TestLocalDataPTRRecordsMapTheirReverseNameToAnAddressAndTheirTargetToAHost(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	rows := fixtureRows(t, "unbound_listlocaldata.json")
	// Reverse names that encode no address, each pointing at a target of its own so a
	// stored one would be seen. The code skips them as values not valid for their type.
	malformed := []string{
		"2.0.192.in-addr.arpa.",                        // three labels
		"1.20.2.0.192.in-addr.arpa.",                   // five labels
		"256.2.0.192.in-addr.arpa.",                    // a label above 255
		"020.2.0.192.in-addr.arpa.",                    // a label with a leading zero
		"x.2.0.192.in-addr.arpa.",                      // a label that is not decimal
		strings.Repeat("0.", 31) + "ip6.arpa.",         // thirty-one nibbles
		strings.Repeat("0.", 33) + "ip6.arpa.",         // thirty-three nibbles
		"g." + strings.Repeat("0.", 31) + "ip6.arpa.",  // a nibble that is not hexadecimal
		"10." + strings.Repeat("0.", 31) + "ip6.arpa.", // a label of two digits
		"host-b.example.test.",                         // not a reverse name at all
	}
	for index, name := range malformed {
		rows = append(rows, map[string]any{"name": name, "ttl": "3600", "type": "IN",
			"rrtype": "PTR", "value": fmt.Sprintf("malformed-%d.example.test.", index)})
	}
	answerRecords(harness.Fake, opnsense.UnboundListLocalData, rows)
	if err := harness.Collector.CollectResolverLocalData(context.Background()); err != nil {
		t.Fatalf("reading the local data: %v", err)
	}

	ptr := stringColumn(t, harness.Store, `SELECT owner_name || ' | ' || value || ' | ' ||
		coalesce(CAST(address AS TEXT), 'NULL') || ' | ' || coalesce(CAST(host_label AS TEXT), 'NULL')
		FROM resource_record_observation WHERE rrtype = 'PTR' ORDER BY address`)
	want := []string{
		"20.2.0.192.in-addr.arpa | host-a.example.test | 192.0.2.20 | host-a",
		"0.2.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa | " +
			"host-a.example.test | 2001:db8::20 | host-a",
	}
	if strings.Join(ptr, "\n") != strings.Join(want, "\n") {
		t.Errorf("the stored PTR records are\n%s\nnot\n%s", strings.Join(ptr, "\n"), strings.Join(want, "\n"))
	}
	if types := stringColumn(t, harness.Store,
		"SELECT DISTINCT rrtype FROM resource_record_observation ORDER BY rrtype"); strings.Join(types, ",") != "A,AAAA,PTR" {
		t.Errorf("the stored types are %v, not A, AAAA and PTR", types)
	}
	if stored := observationCount(t, harness.Store); stored != 4 {
		t.Errorf("%d records were stored, not the A, the AAAA and the two PTR records", stored)
	}
	if text := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM resource_record_observation
		WHERE owner_name = 'example.test'`); text != 0 {
		t.Error("the TXT record was stored")
	}
	if bad := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM resource_record_observation
		WHERE value LIKE 'malformed-%' OR owner_name = 'host-b.example.test'`); bad != 0 {
		t.Errorf("%d PTR records whose owner encodes no address were stored", bad)
	}
	detail := harness.DetailOf(t, core.KindResolverLocalData, collecttest.ProviderUnbound)
	for _, wantDetail := range []string{
		fmt.Sprintf("read %d records of the local data", len(rows)),
		fmt.Sprintf("PTR %d", 2+len(malformed)), "TXT 1", "A 1", "AAAA 1",
		"kept 4 of the A, AAAA, PTR records",
		fmt.Sprintf("skipped %d whose value is not valid for their type", len(malformed)),
	} {
		if !strings.Contains(detail, wantDetail) {
			t.Errorf("the detail %q does not say %q", detail, wantDetail)
		}
	}
}

// processorStreamWithin is the bound the insight plugin reads the processor's stream within: five
// seconds (plugins/insight/insight.js, processorStreamWithin).
const processorStreamWithin = 5 * time.Second
