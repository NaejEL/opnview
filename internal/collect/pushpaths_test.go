package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// PS-AC14, the critical criterion: one series of filter-log lines delivered through the polled
// endpoint and through the push path, across a handover from pull to push and back, every boundary
// second holding lines both paths delivered.

// lineSecond is the instant of the test's lines: an hour before the reference, so every one is inside
// the retention and the pairing window is irrelevant.
func lineSecond(second int) int64 { return referenceEpoch() - 3600 + int64(second) }

// line is one filter-log line of the series: two per second, distinguished by the source port.
type line struct {
	second, index int
}

func (l line) port() int64 { return int64(40000 + l.second*2 + l.index) }

// pulledRow is the line as /api/diagnostics/firewall/log returns it; its timestamp is the firewall's
// local time, which the measured offset turns back into the instant.
func (l line) pulledRow(offset int) map[string]any {
	local := time.Unix(lineSecond(l.second)+int64(offset), 0).UTC().Format("2006-01-02T15:04:05")
	return map[string]any{"__digest__": fmt.Sprintf("%032x", l.port()), "__timestamp__": local,
		"interface": "exdev0", "action": "pass", "dir": "in", "ipversion": "4", "protoname": "tcp", "protonum": "6",
		"length": "100", "src": "198.51.100.10", "dst": "203.0.113.20", "srcport": fmt.Sprint(l.port()),
		"dstport": "443", "reason": "match"}
}

// pushedRecord is the same line as the push path delivers it, under its namespaced identity.
func (l line) pushedRecord() firewalllog.Record {
	sum := sha256.Sum256([]byte(fmt.Sprintf("pushed %d", l.port())))
	source, destination := l.port(), int64(443)
	return firewalllog.Record{Digest: PushedDigestPrefix + hex.EncodeToString(sum[:]), ObservedAt: lineSecond(l.second),
		Device: "exdev0", SrcAddress: "198.51.100.10", DstAddress: "203.0.113.20", SrcPort: &source,
		DstPort: &destination, Protocol: "tcp", IPVersion: 4, Action: "pass", Direction: "in", PacketBytes: 100}
}

func lines(from, to int) []line {
	var result []line
	for second := from; second <= to; second++ {
		result = append(result, line{second, 0}, line{second, 1})
	}
	return result
}

// answerPage arranges the filter log to answer the lines of [from, to], newest first.
func answerPage(h *probeHarness, from, to int) {
	offset := h.collector.FirewallOffsetSeconds()
	var rows []map[string]any
	for second := to; second >= from; second-- {
		for index := 1; index >= 0; index-- {
			rows = append(rows, line{second, index}.pulledRow(offset))
		}
	}
	h.fake.AnswerJSON(opnsense.FirewallLog, rows)
}

func deliverPushed(t *testing.T, h *probeHarness, series []line) {
	t.Helper()
	var records []firewalllog.Record
	for _, l := range series {
		records = append(records, l.pushedRecord())
	}
	if err := h.collector.ReceivePushedFirewallLog(context.Background(), ProviderPf, records); err != nil {
		t.Fatal(err)
	}
}

func collectPage(t *testing.T, h *probeHarness) {
	t.Helper()
	if err := h.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestNothingIsCountedTwiceAcrossAHandover is PS-AC14.
func TestNothingIsCountedTwiceAcrossAHandover(t *testing.T) {
	t.Parallel()
	h := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := h.collector.discoverClock(ctx); err != nil {
		t.Fatal(err)
	}

	// Pull alone: seconds 0 to 19.
	answerPage(h, 0, 19)
	collectPage(t, h)

	// The push source becomes alive; its lines from second 15 overlap what polling read.
	if err := h.collector.PushLiveness(ctx, KindFirewallLog, ProviderPf, true, lineSecond(15)); err != nil {
		t.Fatal(err)
	}
	deliverPushed(t, h, lines(15, 30))
	// The overlap pass reads to second 25: the boundary is second 24, both paths delivered 15 to 25.
	answerPage(h, 0, 25)
	collectPage(t, h)

	// Push alone: polling is suspended -- zero calls -- and a late pushed line before the boundary is
	// superseded.
	requests := len(h.fake.RequestsTo(opnsense.FirewallLog))
	collectPage(t, h)
	if after := len(h.fake.RequestsTo(opnsense.FirewallLog)); after != requests {
		t.Errorf("polling made %d calls while the push path answered", after-requests)
	}
	deliverPushed(t, h, lines(31, 40))
	deliverPushed(t, h, []line{{10, 0}})

	// The push source stops; polling resumes and reads back over the boundary, second 40.
	if err := h.collector.PushLiveness(ctx, KindFirewallLog, ProviderPf, false, lineSecond(40)); err != nil {
		t.Fatal(err)
	}
	answerPage(h, 30, 50)
	collectPage(t, h)

	// Every line is stored exactly once, by one path, and none is lost.
	for second := 0; second <= 50; second++ {
		stored := scalarCount(t, h.store, "SELECT count(*) FROM flow WHERE observed_at = ?", lineSecond(second))
		if stored != 2 {
			t.Errorf("second %d holds %d flows, want 2", second, stored)
		}
		pushed := scalarCount(t, h.store, "SELECT count(*) FROM flow WHERE observed_at = ? AND log_digest LIKE 'push:%'",
			lineSecond(second))
		wantPushed := 0
		if second >= 25 && second <= 40 {
			wantPushed = 2
		}
		if pushed != wantPushed {
			t.Errorf("second %d holds %d pushed flows, want %d", second, pushed, wantPushed)
		}
	}
	if total := countRows(t, h.store, "flow"); total != 102 {
		t.Errorf("%d flows stored for 102 lines", total)
	}
	// The segments never overlap and cover the series with no hole.
	providerID, err := h.store.ProviderID(ctx, KindFirewallLog, ProviderPf)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := h.store.SourceSegments(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 || segments[0].Path != store.PathPull || *segments[0].ToAt != lineSecond(24) ||
		segments[1].Path != store.PathPush || segments[1].FromAt != lineSecond(25) || *segments[1].ToAt != lineSecond(40) ||
		segments[2].Path != store.PathPull || segments[2].FromAt != lineSecond(41) || segments[2].ToAt != nil {
		t.Errorf("segments %+v", segments)
	}
	state, _ := h.collector.PushPathOf(KindFirewallLog, ProviderPf)
	// Superseded: pushed 15-24 (20) and the late pushed line (1); pulled 30-40 after the handover
	// back (22).
	if state.Superseded != 43 {
		t.Errorf("superseded %d, want 43", state.Superseded)
	}
	if gaps := scalarCount(t, h.store, "SELECT count(*) FROM collection_gap WHERE provider_id = ?", providerID); gaps != 0 {
		t.Errorf("%d gaps were recorded over an interval both paths covered", gaps)
	}
	if digests := scalarCount(t, h.store, "SELECT count(DISTINCT log_digest) FROM flow"); digests != 102 {
		t.Errorf("%d distinct digests", digests)
	}
}

// TestAPullGapIsClippedByThePushSegment is PS-AC14's gap clause: when polling resumes after a push
// segment and cannot reach back to its end, the gap runs from the boundary, never over the interval
// the push path stored.
func TestAPullGapIsClippedByThePushSegment(t *testing.T) {
	t.Parallel()
	h := arrangeFilterLogCollection(t)
	ctx := context.Background()
	if err := h.collector.discoverClock(ctx); err != nil {
		t.Fatal(err)
	}
	answerPage(h, 0, 10)
	collectPage(t, h)
	if err := h.collector.PushLiveness(ctx, KindFirewallLog, ProviderPf, true, lineSecond(5)); err != nil {
		t.Fatal(err)
	}
	deliverPushed(t, h, lines(5, 20))
	answerPage(h, 0, 12)
	collectPage(t, h)
	if err := h.collector.PushLiveness(ctx, KindFirewallLog, ProviderPf, false, lineSecond(20)); err != nil {
		t.Fatal(err)
	}
	// The page now begins at second 30: seconds 21 to 29 were read by neither path.
	answerPage(h, 30, 35)
	collectPage(t, h)
	providerID, _ := h.store.ProviderID(ctx, KindFirewallLog, ProviderPf)
	var from, to int64
	if err := h.store.DB().QueryRowContext(ctx,
		"SELECT interval_start_at, interval_end_at FROM collection_gap WHERE provider_id = ?", providerID).
		Scan(&from, &to); err != nil {
		t.Fatalf("no gap was recorded: %v", err)
	}
	if from != lineSecond(20) || to != lineSecond(30) {
		t.Errorf("the gap runs from %d to %d, want %d to %d", from, to, lineSecond(20), lineSecond(30))
	}
}

// TestInterfaceIndexesAreReadThroughTheBroker is PS-AC17: the index-to-interface mapping is the
// broker's read of interfaces_info with details; a row with no index maps nothing.
func TestInterfaceIndexesAreReadThroughTheBroker(t *testing.T) {
	t.Parallel()
	h := newProbeHarness(t)
	h.fake.AnswerJSON(opnsense.InterfacesInfo, map[string]any{"total": 2, "rowCount": 2, "current": 1, "rows": []any{
		map[string]any{"identifier": "lan", "device": "exdev0", "statistics": map[string]any{"index": "3"}},
		map[string]any{"identifier": "opt1", "device": "exdev1", "statistics": map[string]any{}},
	}})
	indexes, err := h.collector.InterfaceIndexes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || indexes[3].Identifier != "lan" || indexes[3].Device != "exdev0" {
		t.Errorf("indexes %+v", indexes)
	}
	requests := h.fake.RequestsTo(opnsense.InterfacesInfo)
	if len(requests) != 1 || !strings.HasSuffix(requests[0].Path, "/interfaces_info/1") {
		t.Errorf("requests %+v", requests)
	}
}
