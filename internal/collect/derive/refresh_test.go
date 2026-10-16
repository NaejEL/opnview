package derive_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestARefreshRunningDuringAnIngestionDoesNotLoseItsSlot is the race behind AC20.
//
// A pass stamps its rows with the instant it began and commits them afterwards, while
// another pass's derivation runs in its own goroutine. That derivation must not move the
// refresh watermark past the stamp, or the rows commit "before" the watermark, no later
// refresh selects their slot, and a flow whose classification changes nothing -- here both
// ends outside, the shape of a blocked scan of the firewall's own address -- is never
// forced either. Its closed slot would stay empty for ever.
func TestARefreshRunningDuringAnIngestionDoesNotLoseItsSlot(t *testing.T) {
	t.Parallel()
	harness := collecttest.ArrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := harness.Collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the first refresh: %v", err)
	}

	stamped, token := harness.Collector.BeginIngest()
	harness.Clock.Advance(5 * time.Second)
	// Another pass finishes meanwhile, and its derivation refreshes.
	if err := harness.Collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the concurrent refresh: %v", err)
	}

	// The stamped row commits only now, in an hour that closed long ago.
	closedHour := vocab.PeriodHour.SlotStart(stamped) - 3*3600
	if err := harness.Store.InsertFlow(ctx, vocab.Flow{
		LogDigest: "refresh-race-probe", ObservedAt: closedHour + 60, IngestedAt: stamped,
		InterfaceDevice: "example-unmapped-device", InterfaceLookupState: vocab.LookupNotFound,
		SrcAddress: "203.0.113.7", DstAddress: "203.0.113.8", Protocol: "tcp", IPVersion: 4,
		Action: "block", Direction: "in", PacketBytes: 60, RuleLookupState: vocab.LookupNotFound,
	}); err != nil {
		t.Fatalf("inserting the stamped flow: %v", err)
	}
	harness.Collector.EndIngest(token)

	harness.Clock.Advance(5 * time.Second)
	if err := harness.Collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("the next refresh: %v", err)
	}
	if bytes := collecttest.ScalarCount(t, harness.Store, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
		WHERE period_start_at = ?`, closedHour); bytes != 60 {
		t.Errorf("the closed hour holds %d bytes after the next refresh, not the 60 of the flow "+
			"committed during the concurrent refresh", bytes)
	}
	// And the watermark is free again once nothing is in flight.
	if _, watermark := harness.Collector.RefreshInstant(); watermark != harness.Clock.Now().UTC().Unix() {
		t.Errorf("the watermark is held at %d with nothing in flight", watermark)
	}
}
