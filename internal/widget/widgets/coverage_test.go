package widgets

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// AC9 and AC10: coverage, gaps and freshness.

// TestCoverageGapsAndBucketsNeverInventAZero is AC9: a window starting before the earliest
// stored data reports a covered interval narrower than the requested one; a firewall_log
// collection gap inside the window is listed; a bucket of a series overlapping it is a gap
// and never a zero; and the buckets sum to the window's total.
func TestCoverageGapsAndBucketsNeverInventAZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	ctx := context.Background()
	month := f.respond(t, "interface_traffic_matrix", map[string]any{"period": "30d"})
	window := month.Window
	if window.Covered == nil || window.Covered.From <= window.Requested.From || window.Coverage != request.CoveragePartial {
		t.Errorf("a 30-day window over three days of data reads %+v", window)
	}

	provider, err := f.db.ProviderID(ctx, "firewall_log", "pf")
	check(t, err)
	gapStart := vocab.PeriodHour.SlotStart(f.now-5*3600) + 120
	check(t, f.db.RecordCollectionGap(ctx, vocab.CollectionGap{ProviderID: provider, IntervalStartAt: gapStart,
		IntervalEndAt: gapStart + 1200, Reason: vocab.GapDigestOutsideWindow, DetectedAt: f.now}))
	f.exec(t, "DELETE FROM flow WHERE observed_at >= $1 AND observed_at < $2", gapStart, gapStart+1200)
	_, err = f.db.RefreshAggregates(ctx, 0, f.now, []int64{gapStart})
	check(t, err)

	response := f.respond(t, "traffic_over_time_by_scope", map[string]any{"period": "24h"})
	listed := false
	for _, gap := range response.Gaps {
		if gap.ProviderKind == "firewall_log" && gap.From == gapStart {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("the collection gap is not listed: %+v", response.Gaps)
	}
	series := decodeData[timeSeriesData](t, response).Series
	direct := map[string]int64{}
	rows, err := f.db.DB().QueryContext(ctx, `SELECT traffic_direction, sum(packet_bytes) FROM classified_flow
		WHERE observed_at >= $1 AND observed_at < $2 AND exit_leg_device IS NULL GROUP BY 1`,
		response.Window.Requested.From, response.Window.Requested.To)
	check(t, err)
	for rows.Next() {
		var (
			direction string
			bytes     int64
		)
		check(t, rows.Scan(&direction, &bytes))
		direct[direction] = bytes
	}
	check(t, rows.Close())
	for _, entry := range series {
		var sum int64
		gapBuckets := 0
		for _, bucket := range entry.Buckets {
			if bucket.Gap {
				gapBuckets++
				if bucket.Value != nil && *bucket.Value == 0 && bucket.From >= gapStart && bucket.To <= gapStart+1200 {
					t.Errorf("%s: an empty gap bucket reads 0", entry.Key)
				}
			}
			if bucket.Volume != nil {
				sum += bucket.Volume.Bytes
			}
		}
		if gapBuckets == 0 {
			t.Errorf("%s: no bucket is marked a gap", entry.Key)
		}
		if sum != direct[entry.Key] || entry.TotalVolume.Bytes != sum {
			t.Errorf("%s: the buckets sum to %d, the total reads %d, the flows hold %d", entry.Key, sum,
				entry.TotalVolume.Bytes, direct[entry.Key])
		}
	}
}

// TestAnAggregateBehindItsFlowsIsFlaggedStale is AC10: a period read from slots whose
// computation is behind their flows is flagged stale, and a freshly refreshed one is not.
func TestAnAggregateBehindItsFlowsIsFlaggedStale(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	fresh := f.respond(t, "interface_traffic_matrix", map[string]any{"period": "24h"})
	if len(fresh.Freshness) != 1 || fresh.Freshness[0].Stale || fresh.Freshness[0].SlotsRead == 0 {
		t.Errorf("a refreshed window reads %+v", fresh.Freshness)
	}
	f.exec(t, "UPDATE volume_aggregate_1h SET computed_at = $1 WHERE period_start_at = $2",
		f.now-7200, vocab.PeriodHour.SlotStart(f.now-6*3600))
	stale := f.respond(t, "interface_traffic_matrix", map[string]any{"period": "24h"})
	if len(stale.Freshness) != 1 || !stale.Freshness[0].Stale || stale.Freshness[0].StaleSlots == 0 {
		t.Errorf("a slot behind its flows reads %+v", stale.Freshness)
	}
	table := decodeData[freshnessData](t, f.respond(t, "aggregate_freshness", nil))
	for _, row := range table.Periods {
		if row.Period == "1h" && !row.Stale {
			t.Errorf("Aggregate freshness does not flag the stale hour: %+v", row)
		}
	}
}
