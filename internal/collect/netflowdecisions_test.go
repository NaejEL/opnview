package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/kinds/filterlog"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The outcomes the orchestrator's decisions of the live validation of 17 October 2026 change in the join
// (specs/SPEC-backend-completion-plugins.md, "Live validation of 17 October 2026: findings and the
// orchestrator's decisions", items 4 to 6), each on a shape of its own through the real pipeline.

// TestAFlowRecordWhoseLegFellInAFilterLogGapIsNotNoLoggedLeg is N2: a record no leg explains whose leg would
// have been logged inside a gap the filter log recorded is unmatched for the reason filter_log_gap -- the log for
// that time was not read -- and is never claimed to have no logged leg; one outside every gap still is.
func TestAFlowRecordWhoseLegFellInAFilterLogGapIsNotNoLoggedLeg(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newNetflowHarness(t)
	h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	h.At(NetflowReadSecond)
	providerID, err := h.Store.ProviderID(ctx, filterlog.Kind, collecttest.ProviderPf)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.RecordPushGap(ctx, providerID, vocab.GapReason("digest_outside_returned_window"),
		LegInstant-10, LegInstant+10, "the page did not reach back to the newest record stored", LegInstant+20); err != nil {
		t.Fatal(err)
	}
	inGap := Forward(17, 41001, 53, 0, 101, LegInstant, LegInstant+1)
	outside := Forward(17, 41002, 53, 0, 102, LegInstant-1800, LegInstant-1799)
	h.Deliver(t, LegInstant+30, inGap, outside)
	h.Logged(t)
	records := byOctets(t, h)
	if reason := OrNull(records[101].Reason); records[101].JoinState != joinUnmatched || reason != unmatchedFilterLogGap {
		t.Errorf("a record whose leg fell in a filter-log gap is %s/%s, want unmatched/filter_log_gap",
			records[101].JoinState, reason)
	}
	if reason := OrNull(records[102].Reason); reason != unmatchedNoLoggedLeg {
		t.Errorf("a record outside every gap is unmatched for %s, want no_logged_leg", reason)
	}
	if slots := collecttest.ScalarCount(t, h.Store, `SELECT count(*) FROM plugin_netflow.unmatched_record
		WHERE reason = 'filter_log_gap' AND bytes = 101`); slots != 1 {
		t.Errorf("the unmatched family holds %d filter_log_gap slots of the record, want 1", slots)
	}
}

// TestADirectionWhoseOnlyRecordsWereAmbiguousIsNotSettledMissing is N3: two connections logged at one instant with
// one tuple, and the one record of their forward direction, which the join cannot tell between them: each forward
// direction is ambiguous_records, not covered, rather than missing, which counts as covered; their reverse
// directions, of which no record was seen, are still missing.
func TestADirectionWhoseOnlyRecordsWereAmbiguousIsNotSettledMissing(t *testing.T) {
	t.Parallel()
	h := newNetflowHarness(t)
	h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	h.At(NetflowReadSecond)
	h.Deliver(t, LegInstant+30, Forward(17, 43000, 53, 0, 201, LegInstant, LegInstant+1))
	first := singleLeg("twin-a", LegInstant, "udp", 17, 43000, 53, "pass")
	second := singleLeg("twin-b", LegInstant, "udp", 17, 43000, 53, "pass")
	second.IPID = 4243
	h.Logged(t, first, second)
	if records := byOctets(t, h); OrNull(records[201].Reason) != unmatchedAmbiguous {
		t.Fatalf("the record joining two legs at one instant is %s/%s, want unmatched/ambiguous", records[201].JoinState,
			OrNull(records[201].Reason))
	}
	for _, digest := range []string{"twin-a", "twin-b"} {
		connection := h.FlowID(t, digest)
		if forward := volumeOf(t, h, connection, orientationForward); forward.State != volumeAmbiguousRecords ||
			coveredState(forward.State) {
			t.Errorf("%s: the forward direction, whose only record was ambiguous, is %s", digest, forward.State)
		}
		if reverse := volumeOf(t, h, connection, orientationReverse); reverse.State != volumeMissing {
			t.Errorf("%s: the reverse direction, of which no record was seen, is %s, want missing", digest, reverse.State)
		}
	}
}

// TestTheReverseOfARefusedConnectionIsNotUnsettled is D5's first clause: the reverse direction of a connection pf
// refused, which two exported interfaces could see, is not forwarding_export_unsettled -- no reply is expected --
// but not_expected_when_refused, which is no coverage failure; the same connection passed is still unsettled.
func TestTheReverseOfARefusedConnectionIsNotUnsettled(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"block", "pass"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			h := newNetflowHarness(t)
			h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier}, Captured{Identifier: UpstreamIdentifier})
			h.At(NetflowReadSecond)
			h.Logged(t, singleLeg("refused", LegInstant, "tcp", 6, 44000, 443, action))
			reverse := volumeOf(t, h, h.FlowID(t, "refused"), orientationReverse)
			want := volumeNotExpectedWhenRefused
			if action == "pass" {
				want = volumeForwardingUnsettled
			}
			if reverse.State != want {
				t.Errorf("the reverse of a connection pf answered %s is %s, want %s", action, reverse.State, want)
			}
			if action == "block" && !coveredState(reverse.State) {
				t.Errorf("not_expected_when_refused counts as a coverage failure")
			}
		})
	}
}
