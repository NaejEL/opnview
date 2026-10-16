package lookups_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestALeasePassResolvesOnlyTheHostNameLookupsSinceThePreviousOne: the work of resolving
// host names again is bounded to the lookups no lease pass has examined since they were
// ingested (the stored mark of the step-5A live corrections, H6).
// A lookup an earlier pass already tried is not tried again, and one read since -- before
// the lease that names its host, as at start -- is resolved.
func TestALeasePassResolvesOnlyTheHostNameLookupsSinceThePreviousOne(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()
	answer := "Recursion"
	ingest := func(key, logged string, at int64) {
		t.Helper()
		lookup, err := harness.Collector.BuildDNSResolution(ctx, dnslookup.Record{
			DeduplicationKey: key, ClientAddress: logged, Domain: "example-site.example.invalid",
			Action: "pass", AnswerSource: &answer, LookedUpAt: at,
		}, "unbound", harness.Clock.Now().Unix())
		if err != nil {
			t.Fatalf("building the lookup: %v", err)
		}
		if err := harness.Store.InsertDNSResolution(ctx, lookup); err != nil {
			t.Fatalf("writing the lookup: %v", err)
		}
	}
	resolution := func(key string) string {
		t.Helper()
		var value string
		if err := harness.Store.DB().QueryRow("SELECT client_resolution FROM dns_resolution WHERE lookup_key = ?",
			key).Scan(&value); err != nil {
			t.Fatalf("reading %s: %v", key, err)
		}
		return value
	}

	// A lookup naming a host no lease carries when the first lease pass runs, a minute
	// after the lookup was read.
	ingest("example-key-tried", "example-host-later.example.invalid", collecttest.ReferenceEpoch()-60)
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Minute))
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the first lease pass: %v", err)
	}
	if got := resolution("example-key-tried"); got != vocab.ClientResolutionUnknown {
		t.Fatalf("with no lease naming it the lookup is %s", got)
	}

	// Time passes; a lease for that host is now held, and a new lookup is read before the
	// lease pass that would resolve it.
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(10*time.Minute))
	provider, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	hostname := "example-host-later"
	if err := harness.Store.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.13", Hostname: &hostname, LeaseState: "reserved",
		GenerationKey: vocab.GenerationKeyOf(nil, nil, collecttest.ReferenceEpoch()), ObservedAt: collecttest.ReferenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	ingest("example-key-new", "example-host-three.example.invalid", collecttest.ReferenceEpoch()+300)
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the second lease pass: %v", err)
	}
	if got := resolution("example-key-new"); got != vocab.ClientResolutionLeased {
		t.Errorf("the lookup read since the previous pass is %s, not resolved", got)
	}
	if got := resolution("example-key-tried"); got != vocab.ClientResolutionUnknown {
		t.Errorf("the lookup the previous pass already tried was tried again: it is %s", got)
	}
}

// TestAHostNameLoggedAsTheClientAttributesOnlyWhenOneLeaseNamesIt is AC8.
func TestAHostNameLoggedAsTheClientAttributesOnlyWhenOneLeaseNamesIt(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	source, destination := "198.51.100.10", "203.0.113.20"
	var flowID, observedAt int64
	if err := harness.Store.DB().QueryRow(`SELECT id, observed_at FROM flow
		WHERE src_address = ? AND dst_address = ?`, source, destination).Scan(&flowID, &observedAt); err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	provider, err := harness.Store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up a lease provider: %v", err)
	}
	expires := collecttest.ReferenceEpoch() + 3600
	for index, lease := range []struct{ address, hostname string }{
		{source, "example-host"}, {"198.51.100.21", "example-twin"}, {"198.51.100.22", "example-twin"},
	} {
		hostname := lease.hostname
		if err := harness.Store.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
			Backend: "dnsmasq", Address: lease.address, Hostname: &hostname, LeaseState: "active",
			GenerationKey: vocab.GenerationKeyOf(nil, &expires, int64(index)), ExpiresAt: &expires,
			ObservedAt: collecttest.ReferenceEpoch(),
		}); err != nil {
			t.Fatalf("writing a lease: %v", err)
		}
	}
	answer := "Recursion"
	decide := func(logged string) (vocab.DNSResolution, int64) {
		t.Helper()
		record := dnslookup.Record{DeduplicationKey: "example-key-" + logged, ClientAddress: logged,
			Domain: "example-site.example.invalid", Action: "pass", AnswerSource: &answer,
			LookedUpAt: observedAt - 2}
		lookup, err := harness.Collector.BuildDNSResolution(ctx, record, "unbound", collecttest.ReferenceEpoch())
		if err != nil {
			t.Fatalf("building the lookup: %v", err)
		}
		if err := harness.Store.InsertDNSResolution(ctx, lookup); err != nil {
			t.Fatalf("writing the lookup: %v", err)
		}
		if _, err := harness.Store.Reclassify(ctx, []string{lookup.ClientAddress}, collecttest.ReferenceEpoch()); err != nil {
			t.Fatalf("classifying: %v", err)
		}
		if _, err := harness.Store.Attribute(ctx, observedAt-60, observedAt, 5, collecttest.ReferenceEpoch()); err != nil {
			t.Fatalf("attributing: %v", err)
		}
		attributed := collecttest.ScalarCount(t, harness.Store, "SELECT count(*) FROM domain_attribution WHERE flow_id = ?", flowID)
		if _, err := harness.Store.DB().Exec("DELETE FROM dns_resolution WHERE lookup_key = ?",
			record.DeduplicationKey); err != nil {
			t.Fatalf("clearing the lookup: %v", err)
		}
		return lookup, int64(attributed)
	}

	for _, step := range []struct {
		logged, resolution string
		attributes         bool
	}{
		{"example-twin.example.invalid", vocab.ClientResolutionAmbiguous, false},
		{"example-nobody.example.invalid", vocab.ClientResolutionUnknown, false},
		{"example-host.example.invalid", vocab.ClientResolutionLeased, true},
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
	if lookup.ClientResolution != vocab.ClientResolutionLoggedAddress || lookup.ClientHostname != nil {
		t.Errorf("an address lookup was recorded as %s with host name %v", lookup.ClientResolution,
			lookup.ClientHostname)
	}
	if attributed != 1 {
		t.Errorf("an address lookup gives the flow %d attributions", attributed)
	}
}

// TestAHostNameLookupReadBeforeItsLeaseIsResolvedByTheLeasePass: every loop runs at start,
// so a lookup naming its client by host name can be read before the lease that resolves it.
// The lease pass resolves it then, as of the lookup's instant, and places it.
func TestAHostNameLookupReadBeforeItsLeaseIsResolvedByTheLeasePass(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeLeaseCollection(t)
	ctx := context.Background()
	answer := "Recursion"
	logged := "example-host-three.example.invalid"
	lookup, err := harness.Collector.BuildDNSResolution(ctx, dnslookup.Record{
		DeduplicationKey: "example-key-early", ClientAddress: logged,
		Domain: "example-site.example.invalid", Action: "pass", AnswerSource: &answer,
		LookedUpAt: collecttest.ReferenceEpoch() - 60,
	}, "unbound", collecttest.ReferenceEpoch())
	if err != nil {
		t.Fatalf("building the lookup: %v", err)
	}
	if lookup.ClientResolution != vocab.ClientResolutionUnknown {
		t.Fatalf("with no lease stored the lookup is %s, not unknown", lookup.ClientResolution)
	}
	if err := harness.Store.InsertDNSResolution(ctx, lookup); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}

	// The lease pass runs after the lookup was read: a lookup stamped in the very second the
	// lease read begins may postdate the leases read, and waits for the next pass.
	collecttest.SetClock(harness, collecttest.ReferenceInstant().Add(time.Second))
	if err := harness.Collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the leases: %v", err)
	}
	var (
		address, resolution, state string
		hostname                   *string
	)
	if err := harness.Store.DB().QueryRow(`SELECT client_address, client_resolution, client_hostname,
		interface_lookup_state FROM dns_resolution WHERE lookup_key = 'example-key-early'`).
		Scan(&address, &resolution, &hostname, &state); err != nil {
		t.Fatalf("reading the lookup: %v", err)
	}
	if resolution != vocab.ClientResolutionLeased || address != "198.51.100.12" {
		t.Errorf("after the lease pass the lookup is %s at %s, not resolved to the leased address",
			resolution, address)
	}
	if hostname == nil || *hostname != logged {
		t.Errorf("the lookup no longer carries the logged name: %v", hostname)
	}
	if state != "resolved" {
		t.Errorf("the resolved lookup is %s, not placed", state)
	}
}
