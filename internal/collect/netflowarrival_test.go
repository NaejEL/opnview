package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestTheJoinDoesNotDependOnArrivalOrder is PS-AC34: the same records and legs ingested records first,
// legs first, interleaved, and with the pairing decided after, give the same decisions and figures; a
// second derivation writes nothing; and no record is decided before the filter log has been read past
// its start plus the tolerance.
func TestTheJoinDoesNotDependOnArrivalOrder(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := NatRecords()
	legs := NatLegs("nat", LegInstant, "pass")
	extra := Forward(17, 40000, 53, 0, 77, LegInstant+1, LegInstant+2)
	orders := map[string]func(t *testing.T, h *netflowWorld){
		"records first": func(t *testing.T, h *netflowWorld) {
			h.Deliver(t, LegInstant+30, insideForward, insideReverse, extra)
			h.Logged(t, legs...)
		},
		"legs first": func(t *testing.T, h *netflowWorld) {
			h.Logged(t, legs...)
			h.Deliver(t, LegInstant+30, insideForward, insideReverse, extra)
			h.Refresh(t)
		},
		"interleaved": func(t *testing.T, h *netflowWorld) {
			h.Deliver(t, LegInstant+30, insideReverse)
			h.Logged(t, legs[0])
			h.Deliver(t, LegInstant+31, extra, insideForward)
			h.Logged(t, legs[1])
		},
		"pairing after": func(t *testing.T, h *netflowWorld) {
			h.Logged(t, legs[0])
			h.Deliver(t, LegInstant+30, insideForward, insideReverse, extra)
			h.Refresh(t)
			h.Logged(t, legs[1])
		},
	}
	fingerprints := map[string]string{}
	for name, ingest := range orders {
		h := newNetflowHarness(t)
		h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
		h.At(NetflowReadSecond)
		ingest(t, h)
		fingerprints[name] = JoinFingerprint(t, h, "nat1", "nat2")
		second, err := h.Join.Run(context.Background(), h.Collector.Collector, h.Collector.Now())
		if err != nil || second.Written != 0 {
			t.Errorf("%s: a second derivation changed %d rows (%v)", name, second.Written, err)
		}
		before := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(computed_at), 0) FROM connection_measure")
		h.Refresh(t)
		if after := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(computed_at), 0) FROM connection_measure"); after != before {
			t.Errorf("%s: a second derivation rewrote a measured volume", name)
		}
	}
	for name, fingerprint := range fingerprints {
		if fingerprint != fingerprints["records first"] {
			t.Errorf("%s gives\n%s\nrecords first gives\n%s", name, fingerprint, fingerprints["records first"])
		}
	}

	// Before the filter log has been read past a record's start plus the tolerance, it stays pending.
	early := newNetflowHarness(t)
	early.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	early.At(NetflowReadSecond)
	early.Deliver(t, LegInstant+30, insideForward)
	early.Refresh(t)
	early.At(LegInstant + 2)
	early.Fake.AnswerJSON(opnsense.FirewallLog, []any{legs[1].Row(), legs[0].Row()})
	if err := early.Collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, record := range early.Records(t) {
		if record.JoinState != joinPending {
			t.Errorf("a record was decided %s before the filter log was read past its start plus the tolerance",
				record.JoinState)
		}
	}
}
