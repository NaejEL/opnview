package store

import (
	"context"
	"math"
	"strconv"
	"testing"
)

// The tests of the record-rate measurement. Rebuilt after the loss of 2 October
// 2026: the build of that evening recorded these three test names, not their
// bodies, so the bodies below are rewritten from what the names state.

// flowSpec is the record-rate spec of the filter log, as package sizing builds
// it: the flow table, measured on the firewall's clock and on opnview's.
var flowSpec = RecordRateSpec{
	Kind:           "firewall_log",
	Table:          "flow",
	SourceColumn:   "observed_at",
	IngestedColumn: "ingested_at",
}

// insertFlowsAt stores one synthesised flow per source instant, each ingested
// lag seconds after it. Nothing here is an address or an identifier read off a
// network.
func insertFlowsAt(t *testing.T, database *Store, lag int64, observedAt ...int64) {
	t.Helper()
	for index, instant := range observedAt {
		flow := exampleFlow(0)
		flow.LogDigest = "record-rate-digest-" + strconv.FormatInt(instant, 10) + "-" + strconv.Itoa(index)
		flow.ObservedAt = instant
		flow.IngestedAt = instant + lag
		if err := database.InsertFlow(context.Background(), flow); err != nil {
			t.Fatalf("storing a flow observed at %d: %v", instant, err)
		}
	}
}

// recordGap stores one collection gap of the filter log's pf implementation.
func recordGap(t *testing.T, database *Store, reason GapReason, start, end, detectedAt int64) {
	t.Helper()
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the filter-log provider: %v", err)
	}
	if err := database.RecordCollectionGap(ctx, CollectionGap{
		ProviderID: providerID, IntervalStartAt: start, IntervalEndAt: end,
		Reason: reason, DetectedAt: detectedAt,
	}); err != nil {
		t.Fatalf("recording a %s gap: %v", reason, err)
	}
}

// closeTo compares two rates without asking floating point for exactness.
func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestTheRecordRateIsMeasuredPerKindOverBothClocks pins what the measurement is
// for: a rate per kind, on the firewall's clock and on opnview's, with the peak
// a page must cover and the gaps recorded in the same window.
//
// The source clock and the ingested clock are given different spans on purpose,
// so a measurement that read one column for both would fail.
func TestTheRecordRateIsMeasuredPerKindOverBothClocks(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	window := RecordRateWindow{StartAt: 1750000000, EndAt: 1750003600}

	// Seven records over 600 s of source time, three of them in one minute,
	// ingested in a burst 20 s after each (so the ingested span is the same
	// 600 s shifted) — and one more record outside the window on each side.
	insertFlowsAt(t, database, 20,
		window.StartAt+100, window.StartAt+110, window.StartAt+115,
		window.StartAt+300, window.StartAt+400, window.StartAt+500, window.StartAt+700)
	insertFlowsAt(t, database, 20, window.StartAt-1, window.EndAt)

	recordGap(t, database, GapDigestOutsideWindow, window.StartAt+200, window.StartAt+260, window.StartAt+261)
	recordGap(t, database, GapDigestOutsideWindow, window.StartAt+600, window.StartAt+610, window.StartAt+611)
	recordGap(t, database, GapEveRotationLost, window.StartAt+50, window.StartAt+80, window.StartAt+90)
	// A gap detected outside the window is not counted, whatever span it covers.
	recordGap(t, database, GapDigestOutsideWindow, window.StartAt+10, window.StartAt+20, window.EndAt+1)

	measurement, err := database.MeasureRecordRate(ctx, flowSpec, window, 60)
	if err != nil {
		t.Fatalf("measuring the filter-log rate: %v", err)
	}
	if measurement.Kind != "firewall_log" || measurement.Window != window || measurement.BucketSeconds != 60 {
		t.Errorf("the measurement names %q over %+v in %d s buckets", measurement.Kind,
			measurement.Window, measurement.BucketSeconds)
	}
	if measurement.RecordCount != 7 {
		t.Errorf("the window holds %d records, want 7", measurement.RecordCount)
	}
	if !measurement.HasRecords || measurement.CoveredFromAt != window.StartAt+100 ||
		measurement.CoveredToAt != window.StartAt+700 {
		t.Errorf("the covered span is %d..%d (records: %v)", measurement.CoveredFromAt,
			measurement.CoveredToAt, measurement.HasRecords)
	}
	if got := measurement.CoveredSpanSeconds(); got != 600 {
		t.Errorf("the covered span is %d s, want 600", got)
	}
	if !measurement.MeanRate.Known || !closeTo(measurement.MeanRate.PerSecond, 7.0/600) {
		t.Errorf("the source mean rate is %+v, want 7 records over 600 s", measurement.MeanRate)
	}

	if !measurement.HasIngestedClock || measurement.IngestedFromAt != window.StartAt+120 ||
		measurement.IngestedToAt != window.StartAt+720 {
		t.Errorf("the ingested span is %d..%d (clock: %v)", measurement.IngestedFromAt,
			measurement.IngestedToAt, measurement.HasIngestedClock)
	}
	if !measurement.IngestedMeanRate.Known || !closeTo(measurement.IngestedMeanRate.PerSecond, 7.0/600) {
		t.Errorf("the ingested mean rate is %+v", measurement.IngestedMeanRate)
	}

	// The busiest 60 s bucket holds the three records at +100, +110 and +115.
	if measurement.PeakBucketRecords != 3 {
		t.Errorf("the busiest bucket holds %d records, want 3", measurement.PeakBucketRecords)
	}
	if !measurement.PeakRate.Known || !closeTo(measurement.PeakRate.PerSecond, 3.0/60) {
		t.Errorf("the peak rate is %+v, want 3 records over 60 s", measurement.PeakRate)
	}

	if len(measurement.Gaps) != 2 {
		t.Fatalf("the window holds gaps of %d reasons, want 2: %+v", len(measurement.Gaps), measurement.Gaps)
	}
	byReason := map[string]RecordRateGap{}
	for _, gap := range measurement.Gaps {
		byReason[gap.Reason] = gap
	}
	if gap := byReason[string(GapDigestOutsideWindow)]; gap.Count != 2 || gap.MissedSeconds != 70 {
		t.Errorf("the filter-log gaps are %+v, want 2 covering 70 s", gap)
	}
	if gap := byReason[string(GapEveRotationLost)]; gap.Count != 1 || gap.MissedSeconds != 30 {
		t.Errorf("the rotation gaps are %+v, want 1 covering 30 s", gap)
	}
	if measurement.GapCount != 3 || measurement.MissedSeconds != 100 {
		t.Errorf("the gaps total %d covering %d s, want 3 covering 100 s",
			measurement.GapCount, measurement.MissedSeconds)
	}

	// The same rows measured as a kind with no ingested clock: the source figures
	// are unchanged and the ingested ones are reported as not measured.
	sourceOnly := flowSpec
	sourceOnly.IngestedColumn = ""
	measured, err := database.MeasureRecordRate(ctx, sourceOnly, window, 60)
	if err != nil {
		t.Fatalf("measuring without an ingested clock: %v", err)
	}
	if measured.HasIngestedClock || measured.IngestedMeanRate.Known || measured.IngestedSpanSeconds() != 0 {
		t.Errorf("a table with no ingested clock reported one: %+v", measured)
	}
	if measured.RecordCount != 7 || measured.MeanRate != measurement.MeanRate {
		t.Errorf("dropping the ingested clock changed the source figures: %+v", measured)
	}

	// Another kind over the same window is measured on its own rows: the flows
	// are not its records and the filter log's gaps are not its gaps.
	other, err := database.MeasureRecordRate(ctx, RecordRateSpec{
		Kind: "security_event", Table: "security_event", SourceColumn: "occurred_at", IngestedColumn: "ingested_at",
	}, window, 60)
	if err != nil {
		t.Fatalf("measuring the security-event rate: %v", err)
	}
	if other.RecordCount != 0 || other.GapCount != 0 || other.Kind != "security_event" {
		t.Errorf("the security-event measurement counted another kind's rows: %+v", other)
	}
}

// TestTheWindowIsBoundedByTheRetentionHorizon keeps the measurement honest about
// what the database can hold. A record is counted only if its source instant is
// inside the window, start included and end excluded, and the measurement
// reports the purge horizon in force, read from the same database, so that a
// reader can tell a quiet day from a day the purge already removed.
func TestTheWindowIsBoundedByTheRetentionHorizon(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	window := RecordRateWindow{StartAt: 1750000000, EndAt: 1750086400}
	if window.Seconds() != 86400 {
		t.Fatalf("the window is %d s long, want 86400", window.Seconds())
	}

	insertFlowsAt(t, database, 0, window.StartAt-1, window.StartAt, window.EndAt-1, window.EndAt)

	if err := database.SetSetting(ctx, "retention_seconds", "3600", window.StartAt); err != nil {
		t.Fatalf("setting a one-hour horizon: %v", err)
	}
	measurement, err := database.MeasureRecordRate(ctx, flowSpec, window, 60)
	if err != nil {
		t.Fatalf("measuring: %v", err)
	}
	if measurement.RecordCount != 2 {
		t.Errorf("the window counted %d records, want the 2 inside it", measurement.RecordCount)
	}
	if measurement.CoveredFromAt != window.StartAt || measurement.CoveredToAt != window.EndAt-1 {
		t.Errorf("the covered span is %d..%d, want the window's own bounds",
			measurement.CoveredFromAt, measurement.CoveredToAt)
	}
	if measurement.RetentionSeconds != 3600 {
		t.Errorf("the measurement reports a horizon of %d s, want the 3600 in force",
			measurement.RetentionSeconds)
	}

	if err := database.SetSetting(ctx, "retention_seconds", "0", window.StartAt); err != nil {
		t.Fatalf("setting an unlimited horizon: %v", err)
	}
	unlimited, err := database.MeasureRecordRate(ctx, flowSpec, window, 60)
	if err != nil {
		t.Fatalf("measuring under an unlimited horizon: %v", err)
	}
	if unlimited.RetentionSeconds != 0 {
		t.Errorf("an unlimited horizon is reported as %d s, want 0", unlimited.RetentionSeconds)
	}
}

// TestAWindowWithNoRecordIsAStateAndNotARateOfNought is the honesty rule applied
// to a measurement. A source that wrote nothing in the window has no rate, which
// is a state to show; reporting it as nought per second would size a page on
// nothing and look exactly like a measured quiet source.
func TestAWindowWithNoRecordIsAStateAndNotARateOfNought(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	window := RecordRateWindow{StartAt: 1750000000, EndAt: 1750003600}

	empty, err := database.MeasureRecordRate(ctx, flowSpec, window, 60)
	if err != nil {
		t.Fatalf("an empty window is not an error, got %v", err)
	}
	if empty.HasRecords || empty.RecordCount != 0 {
		t.Errorf("an empty window reports records: %+v", empty)
	}
	if empty.MeanRate.Known || empty.IngestedMeanRate.Known || empty.PeakRate.Known {
		t.Errorf("an empty window reports a rate: mean %+v, ingested %+v, peak %+v",
			empty.MeanRate, empty.IngestedMeanRate, empty.PeakRate)
	}
	if empty.CoveredSpanSeconds() != 0 || empty.IngestedSpanSeconds() != 0 {
		t.Errorf("an empty window reports a span")
	}

	// One record has a peak but no span: the peak is known, the means are not.
	insertFlowsAt(t, database, 5, window.StartAt+10)
	single, err := database.MeasureRecordRate(ctx, flowSpec, window, 60)
	if err != nil {
		t.Fatalf("measuring one record: %v", err)
	}
	if !single.HasRecords || single.RecordCount != 1 {
		t.Errorf("one record is reported as %+v", single)
	}
	if single.MeanRate.Known || single.IngestedMeanRate.Known {
		t.Errorf("one instant has no span to divide by, got mean %+v and ingested %+v",
			single.MeanRate, single.IngestedMeanRate)
	}
	if !single.PeakRate.Known || !closeTo(single.PeakRate.PerSecond, 1.0/60) {
		t.Errorf("the peak of one record is %+v, want 1 over 60 s", single.PeakRate)
	}

	// A bucket that is not a bucket is refused rather than divided by.
	for _, bucket := range []int64{0, -60} {
		if _, err := database.MeasureRecordRate(ctx, flowSpec, window, bucket); err == nil {
			t.Errorf("a bucket of %d s was accepted", bucket)
		}
	}
}
