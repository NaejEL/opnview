package collect_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The capture configuration, the states and loss detectability it gives, and the identity of a record
// (PS-AC36, PS-AC49, PS-AC51 and PS-AC52 of the NetFlow join amendment).

// states maps each stored record's octets to its capture state.
func states(t *testing.T, h *collecttest.NetflowHarness) map[int64]string {
	t.Helper()
	indexed := map[int64]string{}
	for _, record := range h.Records(t) {
		indexed[record.Octets] = record.CaptureState
	}
	return indexed
}

// TestCoverageAndStatesFollowTheConfiguration is PS-AC36.
func TestCoverageAndStatesFollowTheConfiguration(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := collecttest.NatRecords()
	at := collecttest.LegInstant

	t.Run("egress only", func(t *testing.T) {
		t.Parallel()
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier, EgressOnly: true})
		h.At(collecttest.NetflowReadSecond)
		h.Deliver(t, at+30, insideReverse)
		h.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
		connection := h.FlowID(t, "nat1")
		if volume := volumeOf(t, h, connection, netflow.OrientationForward); volume.State != netflow.VolumeNotCaptured {
			t.Errorf("the forward direction of an in leg at an egress-only point is %s", volume.State)
		}
		if volume := volumeOf(t, h, connection, netflow.OrientationReverse); volume.State != netflow.VolumeMeasured ||
			volume.Bytes != 20000 {
			t.Errorf("the reverse direction is %+v, want measured", volume)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		h := collecttest.NewNetflowHarness(t)
		h.At(collecttest.CaptureStartAt)
		h.Fake.Answer(collecttest.GetconfigEndpoint, http.StatusForbidden, []byte(`{"message":"denied"}`))
		if err := h.Collector.CollectFlowRecordCapture(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.At(collecttest.NetflowReadSecond)
		h.Deliver(t, at+30, insideForward)
		h.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
		if state := states(t, h)[1000]; state != netflow.CaptureUnknown {
			t.Errorf("a record under an unreadable configuration is %s", state)
		}
		if volumes := h.Volumes(t); len(volumes) != 0 {
			t.Errorf("a connection under an unreadable configuration has figures: %+v", volumes)
		}
	})

	t.Run("sequence gap", func(t *testing.T) {
		t.Parallel()
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
		h.At(collecttest.NetflowReadSecond)
		long := insideForward
		long.End = at + 60
		h.Deliver(t, at+30, long)
		h.Sequence += 3
		h.Deliver(t, at+50)
		h.Deliver(t, at+55)
		h.RecordGaps()
		h.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), netflow.OrientationForward); volume.State != netflow.VolumeExportGap {
			t.Errorf("a direction a sequence gap meets is %s, want export_gap", volume.State)
		}
	})

	t.Run("refused legs", func(t *testing.T) {
		t.Parallel()
		// A refused `out` leg: its forward packets were blocked before the egress capture.
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.UpstreamIdentifier})
		h.At(collecttest.NetflowReadSecond)
		legs := collecttest.NatLegs("nat", at, "pass")
		legs[1].Action = "block"
		h.Logged(t, legs...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), netflow.OrientationForward); volume.State !=
			netflow.VolumeNotExportedWhenRefused || collecttest.OrNull(volume.Leg) != netflow.MeasuredAtSecondLeg {
			t.Errorf("the forward direction of a refused out leg is %+v (leg %s)", volume, collecttest.OrNull(volume.Leg))
		}

		// A refused `in` leg: its forward packets were captured at ingress, before pf.
		blocked := collecttest.NewNetflowHarness(t)
		blocked.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
		blocked.At(collecttest.NetflowReadSecond)
		blocked.Deliver(t, at+30, collecttest.Forward(6, 50500, 22, 0x02, 60, at, at))
		blocked.Logged(t, singleLeg("blocked", at, "tcp", 6, 50500, 22, "block"))
		if volume := volumeOf(t, blocked, blocked.FlowID(t, "blocked"), netflow.OrientationForward); volume.State !=
			netflow.VolumeMeasured || volume.Bytes != 60 {
			t.Errorf("the forward direction of a refused in leg is %+v, want measured", volume)
		}
	})

	t.Run("configuration change", func(t *testing.T) {
		t.Parallel()
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
		h.At(collecttest.NetflowReadSecond)
		before := collecttest.Forward(17, 41000, 53, 0, 101, at, at+1)
		h.Deliver(t, at+30, before)
		h.Capture(t, at+1800, collecttest.Captured{Identifier: collecttest.UpstreamIdentifier})
		h.At(collecttest.NetflowReadSecond)
		between := collecttest.Forward(17, 41001, 53, 0, 102, at+100, at+101)
		after := collecttest.Forward(17, 41002, 53, 0, 103, at+2000, at+2001)
		h.Deliver(t, at+2100, between, after)
		got := map[int64]string{}
		points := map[int64]string{}
		for _, record := range h.Records(t) {
			got[record.Octets], points[record.Octets] = record.CaptureState, collecttest.OrNull(record.Point)
		}
		if got[101] != netflow.CaptureObservationPoint || points[101] != collecttest.InsideIdentifier {
			t.Errorf("the record stored before the change is %s at %s, want its earlier point", got[101], points[101])
		}
		if got[102] != netflow.CaptureUnknown {
			t.Errorf("a record starting between the two reads is %s, want capture_configuration_unknown", got[102])
		}
		if got[103] != netflow.CaptureObservationPoint || points[103] != collecttest.UpstreamIdentifier {
			t.Errorf("a record after the new read is %s at %s", got[103], points[103])
		}
	})
}

// TestARecordHasOneIdentityPerDelivery is PS-AC51.
func TestARecordHasOneIdentityPerDelivery(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	one := collecttest.Forward(17, 42000, 53, 0, 1, at, at+1)
	payload := collecttest.Datagram(at+30, 1, one)
	h.DeliverRaw(t, payload)
	h.DeliverRaw(t, payload)
	if count := len(h.Records(t)); count != 1 {
		t.Errorf("a datagram delivered twice stored %d rows, want 1", count)
	}
	h.DeliverRaw(t, collecttest.Datagram(at+31, 2, collecttest.Forward(17, 42001, 53, 0, 2, at, at+1), collecttest.Forward(17, 42001, 53, 0, 2, at, at+1)))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow_record WHERE octets = 2"); count != 2 {
		t.Errorf("two identical records of one datagram stored %d rows, want 2", count)
	}
	// Two nodes, one sequence number and one export time: two datagrams whose octets differ.
	h.DeliverRaw(t, collecttest.Datagram(at+32, 3, collecttest.Forward(17, 42002, 53, 0, 3, at, at+1)))
	h.DeliverRaw(t, collecttest.Datagram(at+32, 3, collecttest.Forward(17, 42003, 53, 0, 4, at, at+1)))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow_record WHERE octets IN (3, 4)"); count != 2 {
		t.Errorf("the datagrams of two nodes stored %d rows, want 2", count)
	}
	// The node recreated: its sequence restarts, and the same record arrives in a new datagram.
	h.DeliverRaw(t, collecttest.Datagram(at+90, 1, one))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow_record WHERE octets = 1"); count != 2 {
		t.Errorf("a record after a sequence restart stored %d rows in all, want 2", count)
	}
}

// TestAnInterleavingStreamSuspendsTheJoinUnderOneInterface is PS-AC51's last clause, PS18's
// configuration contradicting the stream: under a configuration naming one captured interface, a
// sequence that goes backwards suspends the join until the next configuration read.
func TestAnInterleavingStreamSuspendsTheJoinUnderOneInterface(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	h.Collector.SetCaptureChangeObserver(func(string) { h.Hub.ResetSequences("netflow") })
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	h.DeliverRaw(t, collecttest.Datagram(at+10, 10, collecttest.Forward(17, 43000, 53, 0, 1, at, at+1)))
	h.DeliverRaw(t, collecttest.Datagram(at+11, 500, collecttest.Forward(17, 43001, 53, 0, 2, at+1, at+2)))
	// Back: a second counter shares the domain. The stream contradicts the configuration from here.
	h.DeliverRaw(t, collecttest.Datagram(at+12, 11, collecttest.Forward(17, 43002, 53, 0, 3, at+12, at+13)))
	got := states(t, h)
	if got[1] != netflow.CaptureObservationPoint || got[2] != netflow.CaptureObservationPoint {
		t.Errorf("records before the contradiction are %s and %s, want their Observation Point", got[1], got[2])
	}
	if got[3] != netflow.CaptureUnknown {
		t.Errorf("a record after the contradiction is %s, want capture_configuration_unknown", got[3])
	}
	// A later read ends the suspension.
	h.Capture(t, at+100, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	h.DeliverRaw(t, collecttest.Datagram(at+120, 12, collecttest.Forward(17, 43003, 53, 0, 4, at+110, at+111)))
	if state := states(t, h)[4]; state != netflow.CaptureObservationPoint {
		t.Errorf("a record after the next read is %s, want its Observation Point again", state)
	}
}

// TestAnUnavailableConfigurationIsReportedAndNothingIsGuessed is PS-AC52: a 404, a 403 and an
// unparseable answer each give capture_configuration_unknown, with what the firewall answered in the
// availability; and the reads are the two read-only actions, by GET, and nothing else.
func TestAnUnavailableConfigurationIsReportedAndNothingIsGuessed(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name    string
		status  int
		body    string
		outcome vocab.AvailabilityOutcome
	}{
		{"not found", http.StatusNotFound, `{"message":"no"}`, vocab.OutcomeNotFound},
		{"forbidden", http.StatusForbidden, `{"message":"no"}`, vocab.OutcomeDenied},
		{"unparseable", http.StatusOK, `{"netflow": "not the model"}`, vocab.OutcomeUndecodable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			h := collecttest.NewNetflowHarness(t)
			h.At(collecttest.CaptureStartAt)
			h.Fake.Answer(collecttest.GetconfigEndpoint, scenario.status, []byte(scenario.body))
			if err := h.Collector.CollectFlowRecordCapture(context.Background()); err != nil {
				t.Fatal(err)
			}
			providerID, err := h.Store.ProviderID(context.Background(), derive.KindFlowRecord, h.Key)
			if err != nil {
				t.Fatal(err)
			}
			var outcome string
			if err := h.Store.DB().QueryRowContext(context.Background(),
				"SELECT outcome FROM source_availability WHERE provider_id = ?", providerID).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			if outcome != string(scenario.outcome) {
				t.Errorf("the availability says %s, want %s", outcome, scenario.outcome)
			}
			if stored := collecttest.ScalarCount(t, h.Store,
				"SELECT count(*) FROM capture_configuration WHERE fingerprint IS NULL AND outcome = ?",
				string(scenario.outcome)); stored != 1 {
				t.Errorf("the unreadable configuration was recorded %d times with its outcome", stored)
			}
			h.At(collecttest.NetflowReadSecond)
			h.Deliver(t, collecttest.LegInstant+30, collecttest.Forward(17, 44000, 53, 0, 9, collecttest.LegInstant, collecttest.LegInstant+1))
			if state := states(t, h)[9]; state != netflow.CaptureUnknown {
				t.Errorf("a record is %s, want capture_configuration_unknown", state)
			}
			for _, request := range h.Fake.Requests() {
				if request.Path == collecttest.GetconfigEndpoint.Path || request.Path == collecttest.IsEnabledEndpoint.Path {
					if request.Method != http.MethodGet {
						t.Errorf("%s was called with %s", request.Path, request.Method)
					}
					continue
				}
				if len(request.Path) > len("/api/diagnostics/netflow/") &&
					request.Path[:len("/api/diagnostics/netflow/")] == "/api/diagnostics/netflow/" {
					t.Errorf("a NetFlow action other than the two reads was called: %s %s", request.Method,
						request.Path)
				}
			}
		})
	}
}

// TestASequenceGapIsAssertedUnderOneInterface is PS18's loss detectability with one captured interface:
// a confirmed forward jump is recorded as netflow_sequence_gap, and the records carry loss_detectable.
func TestASequenceGapIsAssertedUnderOneInterface(t *testing.T) {
	t.Parallel()
	h := collecttest.NewNetflowHarness(t)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	h.Deliver(t, collecttest.LegInstant+10, collecttest.Forward(17, 45000, 53, 0, 5, collecttest.LegInstant, collecttest.LegInstant+1))
	h.Sequence += 4
	h.Deliver(t, collecttest.LegInstant+20)
	h.Deliver(t, collecttest.LegInstant+25)
	h.RecordGaps()
	if gaps := collecttest.ScalarCount(t, h.Store,
		"SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'"); gaps != 1 {
		t.Errorf("%d sequence gaps were recorded, want 1", gaps)
	}
	for _, record := range h.Records(t) {
		if record.LossDetectable != 1 {
			t.Errorf("a record under one captured interface says its loss is not detectable")
		}
	}
}
