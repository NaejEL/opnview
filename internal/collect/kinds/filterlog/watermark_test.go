package filterlog_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/kinds/filterlog"
)

// TestAPurgeCommittingDuringAFilterLogPassDoesNotCountTwice replays the interleaving the
// scheduler allows: a filter-log pass reads its page, the retention purge -- an
// independent loop -- commits, and only then does the pass store the page. The records
// the purge removed must not be stored again, so the page counts once.
func TestAPurgeCommittingDuringAFilterLogPassDoesNotCountTwice(t *testing.T) {
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

	// The pass reads its page, as CollectFirewallLog does...
	_, providerID, _, err := harness.Collector.ActiveSourceKey(ctx, filterlog.Kind)
	if err != nil {
		t.Fatalf("reading the active source: %v", err)
	}
	answer, _, err := collecttest.MustFirewallLogSource(t, collecttest.ProviderPf).Implementation.Call(ctx,
		harness.Collector, "records", harness.Collector.PageSize(filterlog.Kind))
	records := filterlog.ReportedFlows(answer)
	if err != nil {
		t.Fatalf("reading the page: %v", err)
	}
	newestStored, hasStored, err := harness.Store.NewestFlowObservedAt(ctx)
	if err != nil {
		t.Fatalf("reading the newest flow: %v", err)
	}
	// ...the purge commits...
	if err := harness.Store.Purge(ctx, collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("purging: %v", err)
	}
	// ...and the pass stores the page it read.
	var stored derive.Derivation
	if err := harness.Collector.StoreFirewallLog(ctx, records, filterlog.PullPlanKeeping(records), harness.Collector.Discovery(),
		collecttest.ReferenceEpoch(), providerID, newestStored, hasStored, &stored); err != nil {
		t.Fatalf("storing the page: %v", err)
	}
	if err := harness.Collector.Derive(ctx, derive.Derivation{}); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	purged := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour")
	present := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow")
	if purged+present != truth {
		t.Errorf("the purged part holds %d bytes and flow %d, the page %d", purged, present, truth)
	}
	if got := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h"); got != truth {
		t.Errorf("the hour slots hold %d bytes, the page %d", got, truth)
	}
	if err := harness.Store.Purge(ctx, collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("purging again: %v", err)
	}
	if purged := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"); purged != truth {
		t.Errorf("after the next purge the purged part holds %d bytes, the page %d", purged, truth)
	}
}
