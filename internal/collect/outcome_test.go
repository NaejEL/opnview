package collect_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The availability outcome codes (SPEC-step-5b-widget-api.md, D1, AD1 and AD2), the move of
// the message-cache rule into the Unbound reader (B5, AC25), the named categories of a
// record whose value is not valid for its type (B6, AC26), and a store failure in the lease
// pass (B8g, AC34).

// TestEveryRecordReadOutcomeWritesItsOwnCode is AD1 and AD2 for the resolver's record reads,
// which name the most absences of any reader: each answer the firewall can give yields its
// own code, 401 and 403 included, and a read that answered yields healthy, or healthy_empty
// when it returned nothing.
func TestEveryRecordReadOutcomeWritesItsOwnCode(t *testing.T) {
	t.Parallel()
	for _, read := range []struct {
		endpoint opnsense.Endpoint
		kind     string
		collect  func(*collect.Collector, context.Context) error
	}{
		{opnsense.UnboundDumpCache, core.KindResolverCache, (*collect.Collector).CollectResolverCache},
		{opnsense.UnboundListLocalData, core.KindResolverLocalData, (*collect.Collector).CollectResolverLocalData},
	} {
		codes := map[vocab.AvailabilityOutcome]string{}
		for _, answer := range []struct {
			name   string
			status int
			body   string
			want   vocab.AvailabilityOutcome
		}{
			{"unauthorised", http.StatusUnauthorized, `{"status":401}`, vocab.OutcomeAuthenticationFailed},
			{"forbidden", http.StatusForbidden, `{"errorMessage":"Forbidden"}`, vocab.OutcomeDenied},
			{"not found", http.StatusNotFound, `{}`, vocab.OutcomeNotFound},
			{"server error", http.StatusInternalServerError, `{}`, vocab.OutcomeServerError},
			{"empty body", http.StatusOK, ``, vocab.OutcomeEmptyBody},
			{"failed result", http.StatusOK, `{"result":"failed"}`, vocab.OutcomeResultFailed},
			{"undecodable", http.StatusOK, `["not", "the", "envelope"]`, vocab.OutcomeUndecodable},
			{"failed status", http.StatusOK, `{"status":"failed"}`, vocab.OutcomeStatusNotOK},
			{"no record", http.StatusOK, `{"status":"ok","data":[]}`, vocab.OutcomeHealthyEmpty},
			{"records", http.StatusOK, `{"status":"ok","data":[{"host":"edge.example.net.","name":` +
				`"edge.example.net.","ttl":"60","type":"IN","rrtype":"A","value":"192.0.2.10"}]}`,
				vocab.OutcomeHealthy},
		} {
			harness := collecttest.ArrangeRecordCollection(t)
			harness.Fake.Answer(read.endpoint, answer.status, []byte(answer.body))
			_ = read.collect(harness.Collector, context.Background())
			got := harness.OutcomeOf(t, read.kind, collecttest.ProviderUnbound)
			if got != answer.want {
				t.Errorf("%s answering %s: the outcome is %q, not %q", read.endpoint.Path, answer.name,
					got, answer.want)
			}
			if other, seen := codes[got]; seen {
				t.Errorf("%s: %s and %s gave the same code %q", read.endpoint.Path, answer.name, other, got)
			}
			codes[got] = answer.name
		}
	}
}

// TestEachNamedAbsenceHasItsOwnCode is AD2 for the named absences: a resolver that offers
// no cache read, one that offers no local-data read, a resolver whose lookups have no
// recorded grammar, the end-of-life plugin not installed, and the plugin present and not
// read. Each is its own code, and none is a code an HTTP status also gives.
func TestEachNamedAbsenceHasItsOwnCode(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	ctx := context.Background()
	if err := harness.Collector.ProbeDNSLookup(ctx); err != nil {
		t.Logf("the lookup probe round reported %v, which is acceptable: every state was recorded", err)
	}
	if err := harness.Collector.ProbeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe round reported %v, which is acceptable: every state was recorded", err)
	}
	for _, want := range []struct {
		kind, key string
		outcome   vocab.AvailabilityOutcome
	}{
		{core.KindResolverCache, collecttest.ProviderDnsmasq, vocab.OutcomeNoCacheRead},
		{core.KindResolverLocalData, collecttest.ProviderDnsmasq, vocab.OutcomeNoLocalDataRead},
		{core.KindDNSLookup, collecttest.ProviderDnsmasq, vocab.OutcomeLogGrammarNotRecorded},
		{core.KindDHCPLease, collecttest.ProviderISC, vocab.OutcomePluginNotInstalled},
		{core.KindResolverCache, collecttest.ProviderUnbound, vocab.OutcomeHealthy},
	} {
		if got := harness.OutcomeOf(t, want.kind, want.key); got != want.outcome {
			t.Errorf("%s/%s: the outcome is %q, not %q", want.kind, want.key, got, want.outcome)
		}
	}

	// The plugin answers: present, and not read, which is not the same as absent.
	harness.Fake.AnswerJSON(opnsense.ISCStatus, map[string]any{"status": "running"})
	if err := harness.Collector.ProbeDHCPLease(ctx); err != nil {
		t.Logf("the lease probe round reported %v", err)
	}
	if got := harness.OutcomeOf(t, core.KindDHCPLease, collecttest.ProviderISC); got != vocab.OutcomeReadNotSupported {
		t.Errorf("the plugin answering: the outcome is %q, not %q", got, vocab.OutcomeReadNotSupported)
	}
}

// TestTheProbeRoundsOutcomesFollowWhatTheFirewallAnswered is AD1 for the probes: a stopped
// service, a setting switched off, a body that is not the shape, a source reachable and
// silent, and a firewall answering 404 everywhere.
func TestTheProbeRoundsOutcomesFollowWhatTheFirewallAnswered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	stopped := collecttest.NewProbeHarness(t)
	stopped.Fake.AnswerFixture(opnsense.IDSStatus, "ids_status_stopped.json")
	stopped.Fake.AnswerFixture(opnsense.NetflowIsEnabled, "netflow_is_enabled_export_only.json")
	stopped.Fake.Answer(opnsense.FirewallLog, http.StatusOK, []byte(`{"not":"an array"}`))
	_ = stopped.Collector.ProbeAll(ctx)
	for _, want := range []struct {
		kind, key string
		outcome   vocab.AvailabilityOutcome
	}{
		{core.KindSecurityEvent, collecttest.ProviderSuricata, vocab.OutcomeServiceNotRunning},
		{core.KindMeasurementSample, collecttest.ProviderInsight, vocab.OutcomePresentButDisabled},
		{core.KindFirewallLog, collecttest.ProviderPf, vocab.OutcomeUndecodable},
		{core.KindDHCPLease, collecttest.ProviderKea, vocab.OutcomeNotFound},
	} {
		if got := stopped.OutcomeOf(t, want.kind, want.key); got != want.outcome {
			t.Errorf("%s/%s: the outcome is %q, not %q", want.kind, want.key, got, want.outcome)
		}
	}

	silent := collecttest.NewProbeHarness(t)
	silent.Fake.Answer(opnsense.FirewallLog, http.StatusOK, []byte(`[]`))
	_ = silent.Collector.ProbeAll(ctx)
	if got := silent.OutcomeOf(t, core.KindFirewallLog, collecttest.ProviderPf); got != vocab.OutcomeHealthyEmpty {
		t.Errorf("a filter log answering an empty array: the outcome is %q, not %q", got,
			vocab.OutcomeHealthyEmpty)
	}

	refused := collecttest.NewProbeHarness(t)
	refused.Fake.Answer(opnsense.FirewallLog, http.StatusUnauthorized, []byte(`{}`))
	refused.Fake.Answer(opnsense.IDSStatus, http.StatusForbidden, []byte(`{}`))
	_ = refused.Collector.ProbeAll(ctx)
	if got := refused.OutcomeOf(t, core.KindFirewallLog, collecttest.ProviderPf); got != vocab.OutcomeAuthenticationFailed {
		t.Errorf("a 401: the outcome is %q", got)
	}
	if got := refused.OutcomeOf(t, core.KindSecurityEvent, collecttest.ProviderSuricata); got != vocab.OutcomeDenied {
		t.Errorf("a 403: the outcome is %q", got)
	}
}

// TestEveryWrittenAvailabilityCarriesACodeInTheClosedSet is AD1's quantifier: after a probe
// round against a firewall answering 404 everywhere, every row the round wrote carries a code
// of the closed set, and none still reads not_yet_probed.
func TestEveryWrittenAvailabilityCarriesACodeInTheClosedSet(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	_ = harness.Collector.ProbeAll(context.Background())
	for _, provider := range everyProvider(t, harness.Store) {
		if provider.kind == core.KindIPGeolocation || provider.kind == core.KindRegistrableDomain || provider.kind == core.KindFlowVolume {
			// Recorded by internal/maxmind and internal/publicsuffix, or (flow_volume) by nothing,
			// none of which this round runs.
			continue
		}
		got := harness.OutcomeOf(t, provider.kind, provider.key)
		if !got.Valid() || got == vocab.OutcomeNotYetProbed {
			t.Errorf("%s/%s carries the outcome %q after the probe round", provider.kind, provider.key, got)
		}
	}
}

// TestNoCodeReadsTheAvailabilityDetail is AD1's grep: no Go or SQL file outside the tests
// decides anything from source_availability.detail. The detail is written, stored, and
// passed through; it is never compared, searched or matched.
func TestNoCodeReadsTheAvailabilityDetail(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	readers := []*regexp.Regexp{
		regexp.MustCompile(`strings\.(Contains|HasPrefix|HasSuffix|Index|EqualFold)\([^)]*\b[dD]etail\b`),
		// Compared with a non-empty literal. Testing a detail for emptiness only decides
		// whether to join two texts, which reads no meaning from it.
		regexp.MustCompile(`\b[dD]etail\s*(==|!=)\s*"[^"]`),
		// Matched in a condition of SQL; an assignment in an UPDATE is a write.
		regexp.MustCompile(`(?i)\b(WHERE|AND|OR|WHEN|ON)\s+[\w.]*detail\s*(LIKE|GLOB|REGEXP|IN\s*\(|=|<>|!=)`),
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == ".git" || name == ".claude" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, "_test.go") ||
			(!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".sql")) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "--") {
				continue
			}
			for _, reader := range readers {
				if reader.MatchString(line) {
					t.Errorf("%s:%d reads the availability detail: %s", path, number+1, trimmed)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// a1e2707DumpDetail is the availability detail the cache read of unbound_dumpcache.json
// recorded at a1e2707, before the message-cache rule moved into the Unbound reader,
// captured by running that read on that commit.
const a1e2707DumpDetail = "read 11 records of the cache (1381 bytes) in 0 ms: A 2, AAAA 1, CNAME 1, " +
	"DS 1, HTTPS 1, NS 1, NSEC 1, NSEC3 1, RRSIG 1, SOA 1; kept 3 of the A, AAAA, CNAME records " +
	"(3 observations started, 0 extended); every other type is counted and not stored; skipped 3 " +
	"message-cache references: lines of the dump's message-cache section naming an rrset of a " +
	"cached answer, with no ttl and no record data, so not records and not counted among them; " +
	"skipped 1 whose value is not valid for their type"

// TestMovingTheMessageCacheRuleLeavesTheDumpDetailByteIdentical is AC25. The detail of the dump
// fixture is a1e2707's byte for byte, except for the one clause B6 extends on purpose: the
// bad-value count now names its category, appended after the count a1e2707 wrote. And
// internal/collect/kinds/resolverrecords/resourcerecord.go, which holds the DNS-generic rules,
// states no Unbound fact.
func TestMovingTheMessageCacheRuleLeavesTheDumpDetailByteIdentical(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeRecordCollection(t)
	answerRecords(harness.Fake, opnsense.UnboundDumpCache, fixtureRows(t, "unbound_dumpcache.json"))
	if err := harness.Collector.CollectResolverCache(context.Background()); err != nil {
		t.Fatalf("reading the cache: %v", err)
	}
	want := a1e2707DumpDetail + " (A whose value is an IPv6 address: 1)"
	if detail := harness.DetailOf(t, core.KindResolverCache, collecttest.ProviderUnbound); detail != want {
		t.Errorf("the dump fixture's detail changed:\n got %q\nwant %q", detail, want)
	}

	source, err := os.ReadFile(filepath.Join("kinds", "resolverrecords", "resourcerecord.go"))
	if err != nil {
		t.Fatalf("reading resourcerecord.go: %v", err)
	}
	for _, fact := range []string{"message-cache", "message cache", "null", "wrapper.py", "dump_msg_ref",
		"Unbound", "unbound"} {
		if strings.Contains(string(source), fact) {
			t.Errorf("resourcerecord.go still states %q, a fact about Unbound rather than about DNS", fact)
		}
	}
}

// TestARecordWhoseValueIsNotValidIsCountedUnderItsNamedCategory is AC26. Every way a record's
// value can fail its type is counted under its own category in the availability detail, for
// the cache and for the local data; a1e2707 wrote one undifferentiated count, which this
// test refuses.
func TestARecordWhoseValueIsNotValidIsCountedUnderItsNamedCategory(t *testing.T) {
	t.Parallel()
	for _, copies := range []int{1, 2} {
		harness := collecttest.ArrangeRecordCollection(t)
		var cache, local []map[string]any
		for copy := 0; copy < copies; copy++ {
			prefix := string(rune('a'+copy)) + "-"
			cache = append(cache,
				map[string]any{"host": prefix + "a-text.example.com.", "ttl": "60", "type": "IN", "rrtype": "A", "value": "not-an-address"},
				map[string]any{"host": prefix + "a-v6.example.com.", "ttl": "60", "type": "IN", "rrtype": "A", "value": "2001:db8::1"},
				map[string]any{"host": prefix + "aaaa-text.example.com.", "ttl": "60", "type": "IN", "rrtype": "AAAA", "value": ""},
				map[string]any{"host": prefix + "aaaa-v4.example.com.", "ttl": "60", "type": "IN", "rrtype": "AAAA", "value": "192.0.2.1"},
				map[string]any{"host": prefix + "alias.example.com.", "ttl": "60", "type": "IN", "rrtype": "CNAME", "value": "."},
				// A valid record of each family, which is stored and counted in no category.
				map[string]any{"host": prefix + "edge.example.net.", "ttl": "60", "type": "IN", "rrtype": "AAAA", "value": "2001:db8::10"})
			local = append(local,
				map[string]any{"name": strconv.Itoa(20+copy) + ".2.0.192.in-addr.arpa.", "ttl": "3600", "type": "IN", "rrtype": "PTR", "value": "."},
				map[string]any{"name": prefix + "host.example.test.", "ttl": "3600", "type": "IN", "rrtype": "PTR", "value": "host.example.test."},
				map[string]any{"name": "." + prefix + "host.example.test.", "ttl": "3600", "type": "IN", "rrtype": "A", "value": "192.0.2.30"})
		}
		answerRecords(harness.Fake, opnsense.UnboundDumpCache, cache)
		answerRecords(harness.Fake, opnsense.UnboundListLocalData, local)
		ctx := context.Background()
		if err := harness.Collector.CollectResolverCache(ctx); err != nil {
			t.Fatalf("reading the cache: %v", err)
		}
		if err := harness.Collector.CollectResolverLocalData(ctx); err != nil {
			t.Fatalf("reading the local data: %v", err)
		}
		n := func(count int) string { return strconv.Itoa(count * copies) }
		cacheDetail := harness.DetailOf(t, core.KindResolverCache, collecttest.ProviderUnbound)
		wantCache := "; skipped " + n(5) + " whose value is not valid for their type (" +
			"A whose value is not an address: " + n(1) + ", A whose value is an IPv6 address: " + n(1) +
			", AAAA whose value is not an address: " + n(1) + ", AAAA whose value is an IPv4 address: " +
			n(1) + ", CNAME with an empty target: " + n(1) + ")"
		if !strings.HasSuffix(cacheDetail, wantCache) {
			t.Errorf("%d copies: the cache detail %q does not end %q", copies, cacheDetail, wantCache)
		}
		localDetail := harness.DetailOf(t, core.KindResolverLocalData, collecttest.ProviderUnbound)
		wantLocal := "; skipped " + n(3) + " whose value is not valid for their type (" +
			"PTR with an empty target: " + n(1) + ", PTR whose owner name encodes no address: " + n(1) +
			", local data whose host name has an empty first label: " + n(1) + ")"
		if !strings.HasSuffix(localDetail, wantLocal) {
			t.Errorf("%d copies: the local-data detail %q does not end %q", copies, localDetail, wantLocal)
		}
		// a1e2707's wording ended at the count; a detail that still does is undifferentiated.
		for _, detail := range []string{cacheDetail, localDetail} {
			if strings.HasSuffix(detail, "whose value is not valid for their type") {
				t.Errorf("the detail %q still ends at the undifferentiated count", detail)
			}
		}
		if stored := observationCount(t, harness.Store); stored != copies {
			t.Errorf("%d copies: %d records were stored, not the valid AAAA of each copy", copies, stored)
		}
	}
}

// TestAStoreFailureInTheLeasePassIsRecordedAndCountsAsAFailedPass is AC34. The Kea backend
// answers, and storing its leases fails (a trigger planted in the test database refuses the
// insert): its availability names the store failure with its code instead of reading
// reachable with the read's detail, and the pass counts as failed for the host-name retry,
// so the pending lookup is not examined against an incomplete lease table.
func TestAStoreFailureInTheLeasePassIsRecordedAndCountsAsAFailedPass(t *testing.T) {
	t.Parallel()
	harness := collecttest.LeaseRetryHarness(t, "example-key-store-failure", "example-host-store-failure")
	ctx := context.Background()
	harness.Fake.AnswerFixture(opnsense.KeaLeases, "kea_leases.json")
	if err := harness.Store.SetActiveProviders(ctx, core.KindDHCPLease, collecttest.ProviderDnsmasq, collecttest.ProviderKea); err != nil {
		t.Fatalf("activating both backends: %v", err)
	}
	kea, err := harness.Store.ProviderID(ctx, core.KindDHCPLease, collecttest.ProviderKea)
	if err != nil {
		t.Fatal(err)
	}
	collecttest.PlantFailure(t, harness.Store, "dhcp_lease", "NEW.provider_id = "+strconv.FormatInt(kea, 10))

	if err := harness.Collector.CollectDHCPLease(ctx); err == nil {
		t.Fatal("a lease pass whose leases could not be stored reported no failure")
	}
	state, _, _ := harness.AvailabilityOf(t, core.KindDHCPLease, collecttest.ProviderKea)
	if state != vocab.StateUnavailable {
		t.Errorf("the backend whose leases could not be stored reads %q", state)
	}
	if got := harness.OutcomeOf(t, core.KindDHCPLease, collecttest.ProviderKea); got != vocab.OutcomeStoreFailure {
		t.Errorf("the outcome is %q, not %q", got, vocab.OutcomeStoreFailure)
	}
	if detail := harness.DetailOf(t, core.KindDHCPLease, collecttest.ProviderKea); !strings.Contains(detail,
		"could not be stored") || !strings.Contains(detail, "lease passes in a row") {
		t.Errorf("the detail %q does not name the store failure and the retry count", detail)
	}
	if done, _ := collecttest.Examined(t, harness.Store, "example-key-store-failure"); done {
		t.Error("the lookup was examined although one backend's leases were not stored")
	}
}

// TestAStoreFailureInTheLookupPassIsRecorded extends B8g to the DNS-lookup pass: the
// resolver's query report answers and storing its lookups fails, so the provider reads
// unavailable with store_failure and a detail naming it, never reachable with the read's
// detail.
func TestAStoreFailureInTheLookupPassIsRecorded(t *testing.T) {
	t.Parallel()
	harness := arrangeResolverCollection(t)
	ctx := context.Background()
	collecttest.PlantInsertFailure(t, harness.Store, "dns_resolution")

	if err := harness.Collector.CollectDNSLookup(ctx); err == nil {
		t.Fatal("a lookup pass whose lookups could not be stored reported no failure")
	}
	state, _, _ := harness.AvailabilityOf(t, core.KindDNSLookup, collecttest.ProviderUnbound)
	if state != vocab.StateUnavailable {
		t.Errorf("the resolver whose lookups could not be stored reads %q", state)
	}
	if got := harness.OutcomeOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); got != vocab.OutcomeStoreFailure {
		t.Errorf("the outcome is %q, not %q", got, vocab.OutcomeStoreFailure)
	}
	if detail := harness.DetailOf(t, core.KindDNSLookup, collecttest.ProviderUnbound); !strings.Contains(detail,
		"could not be stored") || !strings.Contains(detail, "planted store failure") {
		t.Errorf("the detail %q does not name the store failure", detail)
	}
}
