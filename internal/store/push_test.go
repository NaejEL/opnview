package store_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store/push"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestAPushGapSeenAgainIsExtended: a silence re-recorded as it lengthens is one gap, not many.
func TestAPushGapSeenAgainIsExtended(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, _, err := database.EnsureProvider(ctx, "example_push_kind", "example", "Example", 1_790_467_200)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []int64{1_790_467_230, 1_790_467_260, 1_790_467_250} {
		if err := database.RecordPushGap(ctx, providerID, vocab.GapPushNotAlive, 1_790_467_200, to, "silent",
			1_790_467_300); err != nil {
			t.Fatal(err)
		}
	}
	var count, end int64
	if err := database.DB().QueryRowContext(ctx,
		"SELECT count(*), max(interval_end_at) FROM collection_gap WHERE provider_id = $1", providerID).
		Scan(&count, &end); err != nil {
		t.Fatal(err)
	}
	if count != 1 || end != 1_790_467_260 {
		t.Errorf("%d gaps ending at %d", count, end)
	}
}

// TestSourcePeriodsNeverOverlap is part of PS-AC14: each boundary closes one period and opens the
// next one second later.
func TestSourcePeriodsNeverOverlap(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, _, err := database.EnsureProvider(ctx, "example_source_period_kind", "example", "Example", 1_790_467_200)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.OpenSourcePeriod(ctx, providerID, push.PathPush, 1_790_467_100); err != nil {
		t.Fatal(err)
	}
	if err := database.OpenSourcePeriod(ctx, providerID, push.PathPull, 1_790_467_150); err != nil {
		t.Fatal(err)
	}
	periods, err := database.SourcePeriods(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 3 || periods[0].Path != push.PathPull || periods[1].Path != push.PathPush || periods[2].Path != push.PathPull {
		t.Fatalf("periods %+v", periods)
	}
	for index := 1; index < len(periods); index++ {
		if periods[index-1].ToAt == nil || *periods[index-1].ToAt+1 != periods[index].FromAt {
			t.Errorf("period %d does not end one second before the next begins: %+v", index-1, periods)
		}
	}
	if periods[2].ToAt != nil {
		t.Error("the last period is not open")
	}
}
