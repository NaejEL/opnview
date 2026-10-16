package store_test

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The purge's ordering against the derivation, on the store's side: AC1 of
// specs/SPEC-purge-serialisation.md as the verifier's probe at store level, and the
// statement the collector places a purge's rows from.

// assertEveryFamilyEqualsTheFlows checks every slot of every family of every period
// against the sums over flows, and that no slot holds a key no flow produces.
func assertEveryFamilyEqualsTheFlows(t *testing.T, database *store.Store, flows []storedFlow, when string) {
	t.Helper()
	for _, period := range vocab.Periods() {
		for _, family := range allFamilies {
			expected := expectedFamily(flows, period, family)
			stored := storedFamily(t, database, period, family)
			for key, want := range expected {
				if got := stored[key]; got != want {
					t.Errorf("%s the %s slot %s of the %s family holds %+v, every flow ingested sums to %+v",
						when, period.Name, key, family, got, want)
				}
			}
			for key := range stored {
				if _, present := expected[key]; !present {
					t.Errorf("%s the %s family holds %s %s, which no flow produces", when, family,
						period.Name, key)
				}
			}
		}
	}
}

// TestAPurgeBetweenAPassStoreAndItsRefreshLeavesEverySlotWhole is the verifier's probe at
// store level: retention 600 s, a 77-byte flow at now - 1000, inserted as a pass stores it,
// then the purge, then the pass's derivation -- a classification that finds nothing left to
// place, and the refresh from the previous refresh's watermark. The purge moved the flow
// into the purged part before any refresh read it, so only the refresh's own reading of the
// purged part can bring the hour, the day, the week and the month their 77 bytes.
func TestAPurgeBetweenAPassStoreAndItsRefreshLeavesEverySlotWhole(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	if err := network.DB.SetSetting(ctx, "retention_seconds", "600", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	// The previous pass's refresh.
	previous := network.Now - 30
	if _, err := network.DB.RefreshAggregates(ctx, 0, previous, nil); err != nil {
		t.Fatalf("the previous refresh: %v", err)
	}

	// The pass stores its row...
	observed := network.Now - 1000
	addresses := network.Insert(t, []storetest.Flow{
		outboundFlow(network.Clients[0], observed, 77, 1, network.Now-1),
	})
	flows := readFlows(t, network.DB)
	// ...the purge commits...
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if left := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow"); left != 0 {
		t.Fatalf("%d flows survived the purge; the probe needs the purge to remove the one", left)
	}
	if purged := storetest.QueryInt(t, network.DB, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"); purged != 77 {
		t.Fatalf("the purged part holds %d bytes, not the flow's 77", purged)
	}
	// ...and the pass derives: nothing is left to place, so nothing is forced.
	result, err := network.DB.Reclassify(ctx, addresses, network.Now)
	if err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.DB.RefreshAggregatesAt(ctx, previous, network.Now, network.Now,
		result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	for _, period := range vocab.Periods() {
		if got := slotBytes(t, network.DB, "volume", period, observed); got != 77 {
			t.Errorf("the %s slot holding the purged flow holds %d bytes, not 77", period.Name, got)
		}
	}
	assertEveryFamilyEqualsTheFlows(t, network.DB, flows, "after the refresh")

	// The purged part does not keep its slots stale for ever: the refresh after the one
	// that read it -- a purge in the same second as a refresh is stale to it, as an
	// ingestion is -- rests on every slot but the current ones.
	if _, err := network.DB.RefreshAggregatesAt(ctx, network.Now, network.Now+60, network.Now+60,
		nil); err != nil {
		t.Fatalf("the next refresh: %v", err)
	}
	later := network.Now + 120
	refresh, err := network.DB.RefreshAggregatesAt(ctx, network.Now+60, later, later, nil)
	if err != nil {
		t.Fatalf("the refresh after it: %v", err)
	}
	for _, slot := range refresh.Rewritten {
		period, err := vocab.PeriodNamed(slot.Period)
		if err != nil {
			t.Fatalf("naming the period %s: %v", slot.Period, err)
		}
		if slot.Start != period.SlotStart(later) {
			t.Errorf("a refresh with nothing ingested or purged since the previous one rewrote the "+
				"%s slot at %d", slot.Period, slot.Start)
		}
	}
	assertEveryFamilyEqualsTheFlows(t, network.DB, flows, "after the slots rest")
}

// TestThePurgeDueAddressesAreTheUnplacedEndsOfTheFlowsItWillRemove: what the collector
// places before it purges is every unplaced end of a flow older than the horizon, and
// nothing of a flow the purge keeps. With an unlimited retention nothing is due.
func TestThePurgeDueAddressesAreTheUnplacedEndsOfTheFlowsItWillRemove(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	placed, unplaced, kept := network.Clients[0], network.Clients[1], network.Clients[2]
	addresses := network.Insert(t, []storetest.Flow{outboundFlow(placed, network.Now-2000, 10, 1, 0)})
	if _, err := network.DB.Reclassify(ctx, addresses, network.Now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	network.Insert(t, []storetest.Flow{
		outboundFlow(unplaced, network.Now-1500, 20, 2, 0),
		outboundFlow(kept, network.Now-100, 30, 3, 0),
	})

	due, err := network.DB.PurgeDueAddresses(ctx, network.Now)
	if err != nil {
		t.Fatalf("reading the due addresses: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("with an unlimited retention %v are due", due)
	}
	if err := network.DB.SetSetting(ctx, "retention_seconds", "600", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	due, err = network.DB.PurgeDueAddresses(ctx, network.Now)
	if err != nil {
		t.Fatalf("reading the due addresses: %v", err)
	}
	// The placed flow's outside end stays unplaced for ever and is due; its inside end is
	// placed and is not. Both ends of the unplaced flow are due; nothing of the kept one is.
	want := []string{storetest.Outside(1, placed.V6), storetest.Outside(2, unplaced.V6), unplaced.Address}
	sort.Strings(want)
	if !reflect.DeepEqual(due, want) {
		t.Errorf("the due addresses are %v, not %v", due, want)
	}
}
