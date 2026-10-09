package collect

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The security-event collector tests.
//
// The cursor is the subject. Paging counts offsets from the end of the file, so any newly
// appended record shifts every offset and `current`/`rowCount` paging cannot be resumed
// across polls; the durable cursor is (fileid, filepos). And a rotation that discarded a
// watermarked file is a permanent loss, which has to be recorded as a loss rather than
// reset — a reset would make it look like a successful read of nothing.

// arrangeAlertCollection stands up a harness whose alert feed is the active source.
func arrangeAlertCollection(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.IDSStatus, "ids_status_running.json")
	harness.fake.answerFixture(opnsense.AlertLogs, "get_alert_logs.json")
	harness.fake.answerFixture(opnsense.QueryAlerts, "query_alerts.json")
	if err := harness.collector.probeSecurityEvent(context.Background()); err != nil {
		t.Fatalf("probing the alert feed: %v", err)
	}
	return harness
}

// TestAnAlertPassStoresEveryEventAndComposesItsProviderKey is the ingest.
//
// The key is composed from the eve file id and the byte offset inside it, because that is
// the pair the provider guarantees stable. The ingestion coordinate is not a column on the
// event table: it lives in the cursor, which answers a different question.
func TestAnAlertPassStoresEveryEventAndComposesItsProviderKey(t *testing.T) {
	t.Parallel()
	harness := arrangeAlertCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	harness.fake.assertEveryPathIsRegistered()

	events := len(fixtureRecords(t, "query_alerts.json"))
	if stored := countRows(t, harness.store, "security_event"); stored != events {
		t.Fatalf("stored %d of the %d alerts", stored, events)
	}

	// Every key is (file, offset), which is what makes a replay a no-op.
	keys := stringColumn(t, harness.store,
		"SELECT provider_event_key FROM security_event ORDER BY provider_event_key")
	for _, key := range keys {
		if !strings.Contains(key, ":") {
			t.Errorf("the event key %q does not compose a file and an offset", key)
		}
	}

	// Severity is NULL on every one, and that is not an oversight: the backend destroys
	// the nested alert object before opnview can read it, so severity is resolved through
	// the per-provider rule-info cache and from nowhere else.
	if withSeverity := scalarCount(t, harness.store,
		"SELECT count(*) FROM security_event WHERE normalised_severity IS NOT NULL"); withSeverity != 0 {
		t.Errorf("%d events carry an inline severity, which this feed cannot supply", withSeverity)
	}

	// A rule identity that is not a number is stored unchanged, so a provider whose rules
	// are named rather than numbered fits without a schema change.
	if named := scalarCount(t, harness.store,
		"SELECT count(*) FROM security_event WHERE CAST(rule_identity AS INTEGER) = 0 "+
			"AND rule_identity <> '0'"); named == 0 {
		t.Error("no event carries a non-numeric rule identity, so the text column is untested")
	}

	// An action outside the vocabulary becomes unknown rather than being claimed as
	// blocked, which would say the firewall stopped something it may have let through.
	if unknown := scalarCount(t, harness.store,
		"SELECT count(*) FROM security_event WHERE event_action = 'unknown'"); unknown == 0 {
		t.Error("the event whose action is outside the vocabulary was not stored as unknown")
	}
}

// TestTheCursorResumesWithoutLossOrDoubleCountAcrossShiftingOffsets is the resume
// guarantee, exercised the way a real poll meets it.
//
// The first pass reads the page and watermarks each file. The second pass is offered the
// same page — the offsets have not moved in the fixture, but that is precisely the case
// where a collector that paged instead of watermarking would re-ingest — and must store
// nothing new. A third pass is offered a page with a NEW record at a higher offset, which
// must be stored, and the older ones, which must not.
func TestTheCursorResumesWithoutLossOrDoubleCountAcrossShiftingOffsets(t *testing.T) {
	t.Parallel()
	harness := arrangeAlertCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	afterOne := countRows(t, harness.store, "security_event")
	if afterOne == 0 {
		t.Fatal("the first pass stored nothing")
	}

	for pass := 0; pass < 2; pass++ {
		if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := countRows(t, harness.store, "security_event"); repeated != afterOne {
		t.Fatalf("re-reading the same page stored %d events, up from %d", repeated, afterOne)
	}

	// A new record appended to the current file, at a higher offset. Every other offset in
	// a real response would have shifted; what makes the resume correct is the watermark,
	// not the page position, so the fixture is re-served with one row added.
	appended := fixtureRecords(t, "query_alerts.json")
	newest := map[string]any{
		"fileid": "3", "filepos": float64(16384), "alert_sid": "2000009",
		"alert_action": "blocked", "alert": "example signature text four",
		"timestamp": "2026-09-26T23:59:00+00:00",
		"src_ip":    "198.51.100.19", "dest_ip": "203.0.113.39", "proto": "TCP",
		"in_iface": "exdev0", "event_type": "alert",
	}
	rows := make([]any, 0, len(appended)+1)
	rows = append(rows, newest)
	for _, record := range appended {
		rows = append(rows, record)
	}
	harness.fake.answerJSON(opnsense.QueryAlerts, map[string]any{
		"rows": rows, "total_rows": len(rows), "origin": "example-eve-current",
	})

	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("the pass after the append: %v", err)
	}
	if final := countRows(t, harness.store, "security_event"); final != afterOne+1 {
		t.Fatalf("the appended record left %d events, want %d", final, afterOne+1)
	}
}

// TestARotationThatDiscardedAWatermarkedFileIsRecordedAsLostAndAsAGap is the permanent
// loss, recorded twice and reset never.
func TestARotationThatDiscardedAWatermarkedFileIsRecordedAsLostAndAsAGap(t *testing.T) {
	t.Parallel()
	harness := arrangeAlertCollection(t)
	ctx := context.Background()

	// A watermark in a file the firewall no longer lists. get_alert_logs enumerates
	// sequences 1, 2 and 3, so a watermark in file 0 is one rotation has discarded.
	sequence := int64(0)
	if err := harness.store.UpsertEveCursor(ctx, store.EveCursor{
		FileID: "0", ByteOffset: 4096, FileSequence: &sequence,
		RotationState: "rotated", ObservedAt: referenceEpoch() - 604800,
	}); err != nil {
		t.Fatalf("seeding a watermark in a file that has since rotated away: %v", err)
	}

	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}

	watermarks, err := harness.store.EveWatermarks(ctx)
	if err != nil {
		t.Fatalf("reading the watermarks: %v", err)
	}
	if watermarks["0"].RotationState != "lost" {
		t.Errorf("the discarded file reads %q, want lost", watermarks["0"].RotationState)
	}
	if watermarks["0"].ByteOffset != 4096 {
		t.Error("the watermark was reset, which would make the loss look like a successful " +
			"read of nothing")
	}

	gaps := scalarCount(t, harness.store,
		"SELECT count(*) FROM collection_gap WHERE reason = ?", string(store.GapEveRotationLost))
	if gaps != 1 {
		t.Fatalf("the rotation wrote %d gap rows, want exactly one", gaps)
	}
	var detail *string
	if err := harness.store.DB().QueryRowContext(ctx,
		"SELECT detail FROM collection_gap WHERE reason = ?",
		string(store.GapEveRotationLost)).Scan(&detail); err != nil {
		t.Fatalf("reading the gap back: %v", err)
	}
	if detail == nil || !strings.Contains(*detail, "gone") {
		t.Error("the gap does not say that the records are gone for good")
	}

	// A second pass must not write the gap again: one loss is one row, not an unbounded
	// stream of identical ones every minute.
	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if repeated := scalarCount(t, harness.store,
		"SELECT count(*) FROM collection_gap WHERE reason = ?",
		string(store.GapEveRotationLost)); repeated != 1 {
		t.Fatalf("a second pass left %d gap rows for one loss", repeated)
	}
}

// TestAnUnreadableRotationListConcludesNoLoss is the honest negative.
//
// If the rotated-file list did not answer, a lost file cannot be told from a present one.
// Concluding "lost" there would invent a permanent gap out of a failed call, so nothing is
// concluded and the state is recorded instead.
func TestAnUnreadableRotationListConcludesNoLoss(t *testing.T) {
	t.Parallel()
	harness := arrangeAlertCollection(t)
	ctx := context.Background()

	sequence := int64(0)
	if err := harness.store.UpsertEveCursor(ctx, store.EveCursor{
		FileID: "0", ByteOffset: 4096, FileSequence: &sequence,
		RotationState: "rotated", ObservedAt: referenceEpoch() - 604800,
	}); err != nil {
		t.Fatalf("seeding a watermark: %v", err)
	}
	// The rotation view answers 404 by removing its arranged reply.
	harness.fake.answer(opnsense.AlertLogs, 404, []byte(`{"errorMessage":"Endpoint not found"}`))

	if err := harness.collector.CollectSecurityEvent(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if gaps := countRows(t, harness.store, "collection_gap"); gaps != 0 {
		t.Fatalf("an unreadable rotation list invented %d gap rows", gaps)
	}
	watermarks, err := harness.store.EveWatermarks(ctx)
	if err != nil {
		t.Fatalf("reading the watermarks: %v", err)
	}
	if watermarks["0"].RotationState == "lost" {
		t.Error("an unreadable rotation list declared a file lost")
	}
}

// TestAnEmptyAlertFeedFromARunningEngineStaysReachable is the case the verified section
// measured on the maintainer's own firewall: five abuse.ch indicator feeds, enabled and
// current, with nothing to report. A reachable engine with a narrow ruleset and no alerts
// is a true reading, and the collector must not treat it as a fault.
func TestAnEmptyAlertFeedFromARunningEngineStaysReachable(t *testing.T) {
	t.Parallel()
	harness := arrangeAlertCollection(t)
	harness.fake.answerJSON(opnsense.QueryAlerts, map[string]any{
		"rows": []any{}, "total_rows": 0, "origin": "example-eve-current",
	})

	if err := harness.collector.CollectSecurityEvent(context.Background()); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	state, _, _ := harness.availabilityOf(t, KindSecurityEvent, ProviderSuricata)
	if state != store.StateReachable {
		t.Fatalf("a running engine with nothing to report reads %q", state)
	}
	detail := harness.detailOf(t, KindSecurityEvent, ProviderSuricata)
	if !strings.Contains(detail, "not a fault") {
		t.Errorf("the detail is %q, which does not record that silence here is a true reading",
			detail)
	}
	if stored := countRows(t, harness.store, "security_event"); stored != 0 {
		t.Fatalf("an empty feed stored %d events", stored)
	}
}

// stringColumn reads one text column of a query.
func stringColumn(t *testing.T, database *store.Store, query string) []string {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("running %s: %v", query, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return values
}
