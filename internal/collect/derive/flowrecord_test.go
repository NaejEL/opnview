package derive_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// TestTheJoinDoesNotDependOnArrivalOrder is PS-AC34: the same records and legs ingested records first,
// legs first, interleaved, and with the pairing decided after, give the same decisions and figures; a
// second derivation writes nothing; and no record is decided before the filter log has been read past
// its start plus the tolerance.
func TestTheJoinDoesNotDependOnArrivalOrder(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := collecttest.NatRecords()
	legs := collecttest.NatLegs("nat", collecttest.LegInstant, "pass")
	extra := collecttest.Forward(17, 40000, 53, 0, 77, collecttest.LegInstant+1, collecttest.LegInstant+2)
	orders := map[string]func(t *testing.T, h *collecttest.NetflowHarness){
		"records first": func(t *testing.T, h *collecttest.NetflowHarness) {
			h.Deliver(t, collecttest.LegInstant+30, insideForward, insideReverse, extra)
			h.Logged(t, legs...)
		},
		"legs first": func(t *testing.T, h *collecttest.NetflowHarness) {
			h.Logged(t, legs...)
			h.Deliver(t, collecttest.LegInstant+30, insideForward, insideReverse, extra)
			h.Derive(t)
		},
		"interleaved": func(t *testing.T, h *collecttest.NetflowHarness) {
			h.Deliver(t, collecttest.LegInstant+30, insideReverse)
			h.Logged(t, legs[0])
			h.Deliver(t, collecttest.LegInstant+31, extra, insideForward)
			h.Logged(t, legs[1])
		},
		"pairing after": func(t *testing.T, h *collecttest.NetflowHarness) {
			h.Logged(t, legs[0])
			h.Deliver(t, collecttest.LegInstant+30, insideForward, insideReverse, extra)
			h.Derive(t)
			h.Logged(t, legs[1])
		},
	}
	fingerprints := map[string]string{}
	for name, ingest := range orders {
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
		h.At(collecttest.NetflowReadSecond)
		ingest(t, h)
		fingerprints[name] = collecttest.JoinFingerprint(t, h, "nat1", "nat2")
		changed, err := h.Collector.JoinFlowRecords(context.Background())
		if err != nil || changed != 0 {
			t.Errorf("%s: a second join changed %d rows (%v)", name, changed, err)
		}
		before := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(computed_at), 0) FROM connection_measure")
		h.Derive(t)
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
	early := collecttest.NewNetflowHarness(t)
	early.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	early.At(collecttest.NetflowReadSecond)
	early.Deliver(t, collecttest.LegInstant+30, insideForward)
	early.Derive(t)
	early.At(collecttest.LegInstant + 2)
	early.Fake.AnswerJSON(opnsense.FirewallLog, []any{legs[1].Row(), legs[0].Row()})
	if err := early.Collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, record := range early.Records(t) {
		if record.JoinState != netflow.JoinPending {
			t.Errorf("a record was decided %s before the filter log was read past its start plus the tolerance",
				record.JoinState)
		}
	}
}
