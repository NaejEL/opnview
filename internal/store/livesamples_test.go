package store_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The fine samples of the live pass (C3 of the backend-completion cycle, C-AC12): stored beside the
// baseline samples and told apart from them, promoted rather than duplicated when the baseline pass
// reads the same capture, purged after live_sample_retention_seconds while the baseline is kept.

// liveSample is one rate reading of one local address, fine when cadence is not nil.
func liveSample(providerID int64, at int64, cadence *int64) vocab.MeasurementSample {
	return withParts(vocab.MeasurementSample{ProviderID: &providerID, SubjectKind: vocab.SubjectInterfaceEndpoint,
		SubjectKey: endpointKey("exdev0", "192.0.2.10"), Measure: vocab.MeasureRateBitsIn,
		Unit: "bit_per_second", Value: 1000, SampledAt: at, LiveCadenceSeconds: cadence})
}

// fineCadences reads the live cadence of every reading of the subject, by instant; -1 is a
// baseline reading.
func fineCadences(t *testing.T, database *store.Store) map[int64]int64 {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(),
		`SELECT sampled_at, coalesce(live_cadence_seconds, -1) FROM measurement_sample WHERE subject_key = $1`,
		endpointKey("exdev0", "192.0.2.10"))
	if err != nil {
		t.Fatalf("reading the samples: %v", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[int64]int64{}
	for rows.Next() {
		var at, cadence int64
		if err := rows.Scan(&at, &cadence); err != nil {
			t.Fatalf("reading a sample: %v", err)
		}
		if _, twice := result[at]; twice {
			t.Errorf("the instant %d holds two readings of one subject and measure", at)
		}
		result[at] = cadence
	}
	return result
}

func TestLiveFineAndBaselineSamplesAreStoredAndDistinguishable(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	cadence := int64(10)
	for _, sample := range []vocab.MeasurementSample{
		liveSample(providerID, 1790000000, nil),
		liveSample(providerID, 1790000010, &cadence),
		liveSample(providerID, 1790000020, &cadence),
	} {
		if err := database.InsertMeasurementSample(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	got := fineCadences(t, database)
	want := map[int64]int64{1790000000: -1, 1790000010: 10, 1790000020: 10}
	for at, cadence := range want {
		if got[at] != cadence {
			t.Errorf("the reading at %d carries the cadence %d, want %d (-1 is a baseline reading)", at, got[at], cadence)
		}
	}
}

func TestLiveABaselineReadingOfAFineSamplesCapturePromotesItRatherThanStoringItTwice(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	cadence := int64(10)
	// The live pass stores its capture; the baseline pass, served the same answer from the mirror,
	// stores it again at the same instant.
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000010, &cadence)); err != nil {
		t.Fatal(err)
	}
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000010, nil)); err != nil {
		t.Fatal(err)
	}
	// A late fine reading of a capture the baseline already holds changes nothing.
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000300, nil)); err != nil {
		t.Fatal(err)
	}
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000300, &cadence)); err != nil {
		t.Fatal(err)
	}
	got := fineCadences(t, database)
	if len(got) != 2 || got[1790000010] != -1 || got[1790000300] != -1 {
		t.Errorf("the readings are %v, want the two instants held once each as baseline readings", got)
	}
}

func TestLiveFineSamplesArePurgedAfterTheirRetentionAndBaselineSamplesAreNot(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1790010000)
	if err := database.SetSetting(ctx, "live_sample_retention_seconds", "600", now); err != nil {
		t.Fatal(err)
	}
	cadence := int64(10)
	for _, sample := range []vocab.MeasurementSample{
		liveSample(providerID, now-601, &cadence), // a fine sample past its retention
		liveSample(providerID, now-600, &cadence), // exactly at the horizon: kept
		liveSample(providerID, now-599, &cadence),
		liveSample(providerID, now-3000, nil), // a baseline sample as old as the first, and older
		liveSample(providerID, now-601+1000, nil),
	} {
		if err := database.InsertMeasurementSample(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	got := fineCadences(t, database)
	if _, kept := got[now-601]; kept {
		t.Error("a fine sample older than live_sample_retention_seconds survived the purge")
	}
	for _, at := range []int64{now - 600, now - 599} {
		if _, kept := got[at]; !kept {
			t.Errorf("the fine sample at %d, within its retention, was purged", at)
		}
	}
	if _, kept := got[now-3000]; !kept {
		t.Error("a baseline sample within retention_seconds was purged with the fine samples")
	}
}

func TestLiveRatesCarryTheirCadenceAndAreStaleOnlyPastTwiceIt(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	cadence := int64(10)
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000000, &cadence)); err != nil {
		t.Fatal(err)
	}
	rates, err := database.ReadCurrentRates(ctx, 1790000015)
	if err != nil {
		t.Fatal(err)
	}
	rate, present := rates.ByDevice["exdev0"]
	if !present {
		t.Fatal("the device's fine sample is not a current rate")
	}
	if rate.CadenceSeconds != 10 {
		t.Errorf("the rate carries the cadence %d, want the fine sample's 10", rate.CadenceSeconds)
	}
	if rate.Stale(1790000015) {
		t.Error("a rate 15 s old at a 10 s cadence is stale; one missed pass is tolerated")
	}
	if !rate.Stale(1790000021) {
		t.Error("a rate 21 s old at a 10 s cadence is not stale")
	}
	// A baseline reading is judged against the measurement interval.
	if err := database.InsertMeasurementSample(ctx, liveSample(providerID, 1790000100, nil)); err != nil {
		t.Fatal(err)
	}
	rates, err = database.ReadCurrentRates(ctx, 1790000500)
	if err != nil {
		t.Fatal(err)
	}
	if rate := rates.ByDevice["exdev0"]; rate.CadenceSeconds != samples.DefaultMeasurementIntervalSeconds || rate.Stale(1790000500) {
		t.Errorf("a baseline rate 400 s old reads cadence %d, stale %v; want %d, not stale",
			rate.CadenceSeconds, rate.Stale(1790000500), samples.DefaultMeasurementIntervalSeconds)
	}
}

func TestLiveTheRateSeriesHoldsEveryInstantFineAndBaseline(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatal(err)
	}
	cadence := int64(10)
	// A baseline instant, then a live period, then the baseline again.
	instants := []struct {
		at      int64
		cadence *int64
	}{{1790000000, nil}, {1790000300, nil}, {1790000310, &cadence}, {1790000320, &cadence}, {1790000600, nil}}
	for _, instant := range instants {
		if err := database.InsertMeasurementSample(ctx, liveSample(providerID, instant.at, instant.cadence)); err != nil {
			t.Fatal(err)
		}
	}
	series, err := database.ReadRateSeries(ctx, 1790000000, 1790000601)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != len(instants) {
		t.Fatalf("the series holds %d instants, want %d", len(series), len(instants))
	}
	for index, instant := range instants {
		if series[index].At != instant.at {
			t.Errorf("instant %d is %d, want %d", index, series[index].At, instant.at)
		}
		if _, present := series[index].Rates.ByDevice["exdev0"]; !present {
			t.Errorf("the instant %d holds no rate of the device", instant.at)
		}
	}
}
