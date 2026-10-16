package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
)

// The second iteration of the step-5A corrections, on the collection side.

// TestARecordStoredAgainAfterItsPurgeIsNotCountedTwice: under a retention shorter than
// the span of one filter-log page, the page brings back records the purge already removed.
// Purging and collecting three times must leave every aggregate at the page's true volume:
// a record the purge has passed is not stored again, so it is never both in `flow` and in
// the purged part.
func TestARecordStoredAgainAfterItsPurgeIsNotCountedTwice(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	truth := collecttest.ScalarCount(t, harness.Store, "SELECT sum(packet_bytes) FROM flow")
	if err := harness.Store.SetSetting(ctx, "retention_seconds", "60", collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	for round := 1; round <= 3; round++ {
		if err := harness.Store.Purge(ctx, collecttest.ReferenceEpoch()); err != nil {
			t.Fatalf("round %d, purging: %v", round, err)
		}
		if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
			t.Fatalf("round %d, collecting: %v", round, err)
		}
		purged := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour")
		present := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow")
		if purged+present != truth {
			t.Errorf("round %d: the purged part holds %d bytes and flow %d, the page %d", round, purged,
				present, truth)
		}
		for _, table := range []string{"volume_aggregate_1h", "volume_aggregate_24h", "volume_aggregate_7d",
			"volume_aggregate_30d", "rule_volume_aggregate_1h"} {
			if got := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM "+table); got != truth {
				t.Errorf("round %d: %s holds %d bytes, the page %d", round, table, got, truth)
			}
		}
	}
}
