package derive_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// TestMoreOrFewerThanOneCapturedInterfaceIsStoredAndNeverJoined is PS-AC49.
func TestMoreOrFewerThanOneCapturedInterfaceIsStoredAndNeverJoined(t *testing.T) {
	t.Parallel()
	insideForward, _, _, _ := collecttest.NatRecords()
	at := collecttest.LegInstant
	baseline := collecttest.NewNetflowHarness(t)
	baseline.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
	want := collecttest.AggregateFingerprint(t, baseline)
	for _, scenario := range []struct {
		name       string
		interfaces []collecttest.Captured
		state      string
		count      int64
	}{
		{"zero", nil, netflow.CaptureNotConfigured, 0},
		{"two", []collecttest.Captured{{Identifier: collecttest.InsideIdentifier}, {Identifier: collecttest.UpstreamIdentifier}},
			netflow.CaptureObservationPointHidden, 2},
		{"three", []collecttest.Captured{{Identifier: collecttest.InsideIdentifier}, {Identifier: "example_if_b"},
			{Identifier: collecttest.UpstreamIdentifier}}, netflow.CaptureObservationPointHidden, 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			h := collecttest.NewNetflowHarness(t)
			h.Capture(t, collecttest.CaptureStartAt, scenario.interfaces...)
			h.At(collecttest.NetflowReadSecond)
			// S11's pair: a forwarded packet exported by two nodes as two records identical in every field.
			h.Deliver(t, at+30, insideForward, insideForward)
			// A forward sequence jump, confirmed by the next datagram.
			h.Sequence += 4
			h.Deliver(t, at+40)
			h.Deliver(t, at+45)
			h.RecordGaps()
			h.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
			records := h.Records(t)
			if len(records) != 2 {
				t.Fatalf("stored %d records, want S11's identical pair as two rows", len(records))
			}
			for _, record := range records {
				if record.CaptureState != scenario.state || record.Point != nil || record.Device != nil ||
					record.Direction != nil || record.JoinState != netflow.JoinNotJoinable || record.LossDetectable != 0 {
					t.Errorf("a record is %+v, want %s with no Observation Point, not joinable, loss not detectable",
						record, scenario.state)
				}
				if record.Captured == nil || *record.Captured != scenario.count {
					t.Errorf("a record names %v captured interfaces, want %d", record.Captured, scenario.count)
				}
			}
			if changed, err := h.Collector.JoinFlowRecords(context.Background()); err != nil || changed != 0 {
				t.Errorf("the join changed %d rows (%v)", changed, err)
			}
			if volumes := h.Volumes(t); len(volumes) != 0 {
				t.Errorf("a connection has figures: %+v", volumes)
			}
			if gaps := collecttest.ScalarCount(t, h.Store,
				"SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'"); gaps != 0 {
				t.Errorf("%d sequence gaps were recorded where none is asserted", gaps)
			}
			if state, found := h.Hub.SubscriptionState("netflow.flow_records"); !found ||
				state.Counters["sequence_gap_not_asserted"] == 0 {
				t.Errorf("the jump was not counted as a gap not asserted: %+v", state.Counters)
			}
			if got := collecttest.AggregateFingerprint(t, h); got != want {
				t.Errorf("a logged column changed:\n%s\nwant\n%s", got, want)
			}
		})
	}
}

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
		before := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(computed_at), 0) FROM connection_measured_volume")
		h.Derive(t)
		if after := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(computed_at), 0) FROM connection_measured_volume"); after != before {
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
