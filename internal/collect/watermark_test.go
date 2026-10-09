package collect

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// The third iteration of the step-5A corrections, on the collection side.

// setClock moves the harness's clock.
func setClock(harness *probeHarness, at time.Time) {
	harness.clock.mutex.Lock()
	defer harness.clock.mutex.Unlock()
	harness.clock.now = at
}

// TestAPurgeCommittingDuringAFilterLogPassDoesNotCountTwice replays the interleaving the
// scheduler allows: a filter-log pass reads its page, the retention purge -- an
// independent loop -- commits, and only then does the pass store the page. The records
// the purge removed must not be stored again, so the page counts once.
func TestAPurgeCommittingDuringAFilterLogPassDoesNotCountTwice(t *testing.T) {
	t.Parallel()
	harness := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	truth := scalarCount(t, harness.store, "SELECT sum(packet_bytes) FROM flow")
	if err := harness.store.SetSetting(ctx, "retention_seconds", "60", referenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}

	// The pass reads its page, as CollectFirewallLog does...
	_, providerID, _, err := harness.collector.activeSourceKey(ctx, KindFirewallLog)
	if err != nil {
		t.Fatalf("reading the active source: %v", err)
	}
	records, _, err := firewallLogSources[ProviderPf].records(ctx, harness.collector,
		harness.collector.pages().FirewallLog)
	if err != nil {
		t.Fatalf("reading the page: %v", err)
	}
	newestStored, hasStored, err := harness.store.NewestFlowObservedAt(ctx)
	if err != nil {
		t.Fatalf("reading the newest flow: %v", err)
	}
	// ...the purge commits...
	if err := harness.store.Purge(ctx, referenceEpoch()); err != nil {
		t.Fatalf("purging: %v", err)
	}
	// ...and the pass stores the page it read.
	var stored derivation
	if err := harness.collector.storeFirewallLog(ctx, records, harness.collector.Discovery(),
		referenceEpoch(), providerID, newestStored, hasStored, &stored); err != nil {
		t.Fatalf("storing the page: %v", err)
	}
	if err := harness.collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	purged := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour")
	present := scalarCount(t, harness.store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow")
	if purged+present != truth {
		t.Errorf("the purged part holds %d bytes and flow %d, the page %d", purged, present, truth)
	}
	if got := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h"); got != truth {
		t.Errorf("the hour slots hold %d bytes, the page %d", got, truth)
	}
	if err := harness.store.Purge(ctx, referenceEpoch()); err != nil {
		t.Fatalf("purging again: %v", err)
	}
	if purged := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"); purged != truth {
		t.Errorf("after the next purge the purged part holds %d bytes, the page %d", purged, truth)
	}
}

// TestALeasePassResolvesOnlyTheHostNameLookupsSinceThePreviousOne: the work of resolving
// host names again is bounded to the lookups no lease pass has examined since they were
// ingested (the stored mark of the step-5A live corrections, H6).
// A lookup an earlier pass already tried is not tried again, and one read since -- before
// the lease that names its host, as at start -- is resolved.
func TestALeasePassResolvesOnlyTheHostNameLookupsSinceThePreviousOne(t *testing.T) {
	t.Parallel()
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()
	answer := "Recursion"
	ingest := func(key, logged string, at int64) {
		t.Helper()
		lookup, err := harness.collector.buildDNSResolution(ctx, lookupRecord{
			DeduplicationKey: key, ClientAddress: logged, Domain: "example-site.example.invalid",
			Action: "pass", AnswerSource: &answer, LookedUpAt: at,
		}, "unbound", harness.clock.Now().Unix())
		if err != nil {
			t.Fatalf("building the lookup: %v", err)
		}
		if err := harness.store.InsertDNSResolution(ctx, lookup); err != nil {
			t.Fatalf("writing the lookup: %v", err)
		}
	}
	resolution := func(key string) string {
		t.Helper()
		var value string
		if err := harness.store.DB().QueryRow("SELECT client_resolution FROM dns_resolution WHERE lookup_key = ?",
			key).Scan(&value); err != nil {
			t.Fatalf("reading %s: %v", key, err)
		}
		return value
	}

	// A lookup naming a host no lease carries when the first lease pass runs, a minute
	// after the lookup was read.
	ingest("example-key-tried", "example-host-later.example.invalid", referenceEpoch()-60)
	setClock(harness, referenceInstant().Add(time.Minute))
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the first lease pass: %v", err)
	}
	if got := resolution("example-key-tried"); got != store.ClientResolutionUnknown {
		t.Fatalf("with no lease naming it the lookup is %s", got)
	}

	// Time passes; a lease for that host is now held, and a new lookup is read before the
	// lease pass that would resolve it.
	setClock(harness, referenceInstant().Add(10*time.Minute))
	provider, err := harness.store.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	hostname := "example-host-later"
	if err := harness.store.InsertDHCPLease(ctx, provider, store.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.13", Hostname: &hostname, LeaseState: "reserved",
		GenerationKey: store.GenerationKeyOf(nil, nil, referenceEpoch()), ObservedAt: referenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	ingest("example-key-new", "example-host-three.example.invalid", referenceEpoch()+300)
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("the second lease pass: %v", err)
	}
	if got := resolution("example-key-new"); got != store.ClientResolutionLeased {
		t.Errorf("the lookup read since the previous pass is %s, not resolved", got)
	}
	if got := resolution("example-key-tried"); got != store.ClientResolutionUnknown {
		t.Errorf("the lookup the previous pass already tried was tried again: it is %s", got)
	}
}
