package collect

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// The second iteration of the step-5A corrections, on the collection side.

// TestARecordStoredAgainAfterItsPurgeIsNotCountedTwice: under a retention shorter than
// the span of one filter-log page, the page brings back records the purge already removed.
// Purging and collecting three times must leave every aggregate at the page's true volume:
// a record the purge has passed is not stored again, so it is never both in `flow` and in
// the purged part.
func TestARecordStoredAgainAfterItsPurgeIsNotCountedTwice(t *testing.T) {
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
	for round := 1; round <= 3; round++ {
		if err := harness.store.Purge(ctx, referenceEpoch()); err != nil {
			t.Fatalf("round %d, purging: %v", round, err)
		}
		if err := harness.collector.CollectFirewallLog(ctx); err != nil {
			t.Fatalf("round %d, collecting: %v", round, err)
		}
		purged := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour")
		present := scalarCount(t, harness.store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow")
		if purged+present != truth {
			t.Errorf("round %d: the purged part holds %d bytes and flow %d, the page %d", round, purged,
				present, truth)
		}
		for _, table := range []string{"volume_aggregate_1h", "volume_aggregate_24h", "volume_aggregate_7d",
			"volume_aggregate_30d", "rule_volume_aggregate_1h"} {
			if got := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM "+table); got != truth {
				t.Errorf("round %d: %s holds %d bytes, the page %d", round, table, got, truth)
			}
		}
	}
}

// TestAHostNameLookupReadBeforeItsLeaseIsResolvedByTheLeasePass: every loop runs at start,
// so a lookup naming its client by host name can be read before the lease that resolves it.
// The lease pass resolves it then, as of the lookup's instant, and places it.
func TestAHostNameLookupReadBeforeItsLeaseIsResolvedByTheLeasePass(t *testing.T) {
	t.Parallel()
	harness := arrangeLeaseCollection(t)
	ctx := context.Background()
	answer := "Recursion"
	logged := "example-host-three.example.invalid"
	lookup, err := harness.collector.buildDNSResolution(ctx, lookupRecord{
		DeduplicationKey: "example-key-early", ClientAddress: logged,
		Domain: "example-site.example.invalid", Action: "pass", AnswerSource: &answer,
		LookedUpAt: referenceEpoch() - 60,
	}, "unbound", referenceEpoch())
	if err != nil {
		t.Fatalf("building the lookup: %v", err)
	}
	if lookup.ClientResolution != store.ClientResolutionUnknown {
		t.Fatalf("with no lease stored the lookup is %s, not unknown", lookup.ClientResolution)
	}
	if err := harness.store.InsertDNSResolution(ctx, lookup); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}

	// The lease pass runs after the lookup was read: a lookup stamped in the very second the
	// lease read begins may postdate the leases read, and waits for the next pass.
	setClock(harness, referenceInstant().Add(time.Second))
	if err := harness.collector.CollectDHCPLease(ctx); err != nil {
		t.Fatalf("collecting the leases: %v", err)
	}
	var (
		address, resolution, state string
		hostname                   *string
	)
	if err := harness.store.DB().QueryRow(`SELECT client_address, client_resolution, client_hostname,
		interface_lookup_state FROM dns_resolution WHERE lookup_key = 'example-key-early'`).
		Scan(&address, &resolution, &hostname, &state); err != nil {
		t.Fatalf("reading the lookup: %v", err)
	}
	if resolution != store.ClientResolutionLeased || address != "198.51.100.12" {
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
