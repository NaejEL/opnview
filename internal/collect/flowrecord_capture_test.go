package collect

import (
	"context"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The capture configuration, the states and loss detectability it gives, and the identity of a record
// (PS-AC36, PS-AC49, PS-AC51 and PS-AC52 of the NetFlow join amendment).

// states maps each stored record's octets to its capture state.
func states(t *testing.T, h *netflowHarness) map[int64]string {
	t.Helper()
	indexed := map[int64]string{}
	for _, record := range h.records(t) {
		indexed[record.octets] = record.captureState
	}
	return indexed
}

// TestCoverageAndStatesFollowTheConfiguration is PS-AC36.
func TestCoverageAndStatesFollowTheConfiguration(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := natRecords()
	at := legInstant

	t.Run("egress only", func(t *testing.T) {
		t.Parallel()
		h := newNetflowHarness(t)
		h.capture(t, captureStartAt, captured{identifier: insideIdentifier, egressOnly: true})
		h.at(netflowReadSecond)
		h.deliver(t, at+30, insideReverse)
		h.logged(t, natLegs("nat", at, "pass")...)
		connection := h.flowID(t, "nat1")
		if volume := volumeOf(t, h, connection, netflow.OrientationForward); volume.state != netflow.VolumeNotCaptured {
			t.Errorf("the forward direction of an in leg at an egress-only point is %s", volume.state)
		}
		if volume := volumeOf(t, h, connection, netflow.OrientationReverse); volume.state != netflow.VolumeMeasured ||
			volume.bytes != 20000 {
			t.Errorf("the reverse direction is %+v, want measured", volume)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		h := newNetflowHarness(t)
		h.at(captureStartAt)
		h.fake.Answer(getconfigEndpoint, http.StatusForbidden, []byte(`{"message":"denied"}`))
		if err := h.collector.CollectFlowRecordCapture(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.at(netflowReadSecond)
		h.deliver(t, at+30, insideForward)
		h.logged(t, natLegs("nat", at, "pass")...)
		if state := states(t, h)[1000]; state != netflow.CaptureUnknown {
			t.Errorf("a record under an unreadable configuration is %s", state)
		}
		if volumes := h.volumes(t); len(volumes) != 0 {
			t.Errorf("a connection under an unreadable configuration has figures: %+v", volumes)
		}
	})

	t.Run("sequence gap", func(t *testing.T) {
		t.Parallel()
		h := newNetflowHarness(t)
		h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
		h.at(netflowReadSecond)
		long := insideForward
		long.end = at + 60
		h.deliver(t, at+30, long)
		h.sequence += 3
		h.deliver(t, at+50)
		h.deliver(t, at+55)
		h.recordGaps()
		h.logged(t, natLegs("nat", at, "pass")...)
		if volume := volumeOf(t, h, h.flowID(t, "nat1"), netflow.OrientationForward); volume.state != netflow.VolumeExportGap {
			t.Errorf("a direction a sequence gap meets is %s, want export_gap", volume.state)
		}
	})

	t.Run("refused legs", func(t *testing.T) {
		t.Parallel()
		// A refused `out` leg: its forward packets were blocked before the egress capture.
		h := newNetflowHarness(t)
		h.capture(t, captureStartAt, captured{identifier: upstreamIdentifier})
		h.at(netflowReadSecond)
		legs := natLegs("nat", at, "pass")
		legs[1].action = "block"
		h.logged(t, legs...)
		if volume := volumeOf(t, h, h.flowID(t, "nat1"), netflow.OrientationForward); volume.state !=
			netflow.VolumeNotExportedWhenRefused || orNull(volume.leg) != netflow.MeasuredAtSecondLeg {
			t.Errorf("the forward direction of a refused out leg is %+v (leg %s)", volume, orNull(volume.leg))
		}

		// A refused `in` leg: its forward packets were captured at ingress, before pf.
		blocked := newNetflowHarness(t)
		blocked.capture(t, captureStartAt, captured{identifier: insideIdentifier})
		blocked.at(netflowReadSecond)
		blocked.deliver(t, at+30, forward(6, 50500, 22, 0x02, 60, at, at))
		blocked.logged(t, singleLeg("blocked", at, "tcp", 6, 50500, 22, "block"))
		if volume := volumeOf(t, blocked, blocked.flowID(t, "blocked"), netflow.OrientationForward); volume.state !=
			netflow.VolumeMeasured || volume.bytes != 60 {
			t.Errorf("the forward direction of a refused in leg is %+v, want measured", volume)
		}
	})

	t.Run("configuration change", func(t *testing.T) {
		t.Parallel()
		h := newNetflowHarness(t)
		h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
		h.at(netflowReadSecond)
		before := forward(17, 41000, 53, 0, 101, at, at+1)
		h.deliver(t, at+30, before)
		h.capture(t, at+1800, captured{identifier: upstreamIdentifier})
		h.at(netflowReadSecond)
		between := forward(17, 41001, 53, 0, 102, at+100, at+101)
		after := forward(17, 41002, 53, 0, 103, at+2000, at+2001)
		h.deliver(t, at+2100, between, after)
		got := map[int64]string{}
		points := map[int64]string{}
		for _, record := range h.records(t) {
			got[record.octets], points[record.octets] = record.captureState, orNull(record.point)
		}
		if got[101] != netflow.CaptureObservationPoint || points[101] != insideIdentifier {
			t.Errorf("the record stored before the change is %s at %s, want its earlier point", got[101], points[101])
		}
		if got[102] != netflow.CaptureUnknown {
			t.Errorf("a record starting between the two reads is %s, want capture_configuration_unknown", got[102])
		}
		if got[103] != netflow.CaptureObservationPoint || points[103] != upstreamIdentifier {
			t.Errorf("a record after the new read is %s at %s", got[103], points[103])
		}
	})
}

// TestMoreOrFewerThanOneCapturedInterfaceIsStoredAndNeverJoined is PS-AC49.
func TestMoreOrFewerThanOneCapturedInterfaceIsStoredAndNeverJoined(t *testing.T) {
	t.Parallel()
	insideForward, _, _, _ := natRecords()
	at := legInstant
	baseline := newNetflowHarness(t)
	baseline.logged(t, natLegs("nat", at, "pass")...)
	want := aggregateFingerprint(t, baseline)
	for _, scenario := range []struct {
		name       string
		interfaces []captured
		state      string
		count      int64
	}{
		{"zero", nil, netflow.CaptureNotConfigured, 0},
		{"two", []captured{{identifier: insideIdentifier}, {identifier: upstreamIdentifier}},
			netflow.CaptureObservationPointHidden, 2},
		{"three", []captured{{identifier: insideIdentifier}, {identifier: "example_if_b"},
			{identifier: upstreamIdentifier}}, netflow.CaptureObservationPointHidden, 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			h := newNetflowHarness(t)
			h.capture(t, captureStartAt, scenario.interfaces...)
			h.at(netflowReadSecond)
			// S11's pair: a forwarded packet exported by two nodes as two records identical in every field.
			h.deliver(t, at+30, insideForward, insideForward)
			// A forward sequence jump, confirmed by the next datagram.
			h.sequence += 4
			h.deliver(t, at+40)
			h.deliver(t, at+45)
			h.recordGaps()
			h.logged(t, natLegs("nat", at, "pass")...)
			records := h.records(t)
			if len(records) != 2 {
				t.Fatalf("stored %d records, want S11's identical pair as two rows", len(records))
			}
			for _, record := range records {
				if record.captureState != scenario.state || record.point != nil || record.device != nil ||
					record.direction != nil || record.joinState != netflow.JoinNotJoinable || record.lossDetectable != 0 {
					t.Errorf("a record is %+v, want %s with no Observation Point, not joinable, loss not detectable",
						record, scenario.state)
				}
				if record.captured == nil || *record.captured != scenario.count {
					t.Errorf("a record names %v captured interfaces, want %d", record.captured, scenario.count)
				}
			}
			if changed, err := h.collector.JoinFlowRecords(context.Background()); err != nil || changed != 0 {
				t.Errorf("the join changed %d rows (%v)", changed, err)
			}
			if volumes := h.volumes(t); len(volumes) != 0 {
				t.Errorf("a connection has figures: %+v", volumes)
			}
			if gaps := scalarCount(t, h.store,
				"SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'"); gaps != 0 {
				t.Errorf("%d sequence gaps were recorded where none is asserted", gaps)
			}
			if state, found := h.hub.SubscriptionState("netflow.flow_records"); !found ||
				state.Counters["sequence_gap_not_asserted"] == 0 {
				t.Errorf("the jump was not counted as a gap not asserted: %+v", state.Counters)
			}
			if got := aggregateFingerprint(t, h); got != want {
				t.Errorf("a logged column changed:\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestARecordHasOneIdentityPerDelivery is PS-AC51.
func TestARecordHasOneIdentityPerDelivery(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	one := forward(17, 42000, 53, 0, 1, at, at+1)
	payload := datagram(at+30, 1, one)
	h.deliverRaw(t, payload)
	h.deliverRaw(t, payload)
	if count := len(h.records(t)); count != 1 {
		t.Errorf("a datagram delivered twice stored %d rows, want 1", count)
	}
	h.deliverRaw(t, datagram(at+31, 2, forward(17, 42001, 53, 0, 2, at, at+1), forward(17, 42001, 53, 0, 2, at, at+1)))
	if count := scalarCount(t, h.store, "SELECT count(*) FROM flow_record WHERE octets = 2"); count != 2 {
		t.Errorf("two identical records of one datagram stored %d rows, want 2", count)
	}
	// Two nodes, one sequence number and one export time: two datagrams whose octets differ.
	h.deliverRaw(t, datagram(at+32, 3, forward(17, 42002, 53, 0, 3, at, at+1)))
	h.deliverRaw(t, datagram(at+32, 3, forward(17, 42003, 53, 0, 4, at, at+1)))
	if count := scalarCount(t, h.store, "SELECT count(*) FROM flow_record WHERE octets IN (3, 4)"); count != 2 {
		t.Errorf("the datagrams of two nodes stored %d rows, want 2", count)
	}
	// The node recreated: its sequence restarts, and the same record arrives in a new datagram.
	h.deliverRaw(t, datagram(at+90, 1, one))
	if count := scalarCount(t, h.store, "SELECT count(*) FROM flow_record WHERE octets = 1"); count != 2 {
		t.Errorf("a record after a sequence restart stored %d rows in all, want 2", count)
	}
}

// TestAnInterleavingStreamSuspendsTheJoinUnderOneInterface is PS-AC51's last clause, PS18's
// configuration contradicting the stream: under a configuration naming one captured interface, a
// sequence that goes backwards suspends the join until the next configuration read.
func TestAnInterleavingStreamSuspendsTheJoinUnderOneInterface(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.collector.SetCaptureChangeObserver(func(string) { h.hub.ResetSequences("netflow") })
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliverRaw(t, datagram(at+10, 10, forward(17, 43000, 53, 0, 1, at, at+1)))
	h.deliverRaw(t, datagram(at+11, 500, forward(17, 43001, 53, 0, 2, at+1, at+2)))
	// Back: a second counter shares the domain. The stream contradicts the configuration from here.
	h.deliverRaw(t, datagram(at+12, 11, forward(17, 43002, 53, 0, 3, at+12, at+13)))
	got := states(t, h)
	if got[1] != netflow.CaptureObservationPoint || got[2] != netflow.CaptureObservationPoint {
		t.Errorf("records before the contradiction are %s and %s, want their Observation Point", got[1], got[2])
	}
	if got[3] != netflow.CaptureUnknown {
		t.Errorf("a record after the contradiction is %s, want capture_configuration_unknown", got[3])
	}
	// A later read ends the suspension.
	h.capture(t, at+100, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliverRaw(t, datagram(at+120, 12, forward(17, 43003, 53, 0, 4, at+110, at+111)))
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
			h := newNetflowHarness(t)
			h.at(captureStartAt)
			h.fake.Answer(getconfigEndpoint, scenario.status, []byte(scenario.body))
			if err := h.collector.CollectFlowRecordCapture(context.Background()); err != nil {
				t.Fatal(err)
			}
			providerID, err := h.store.ProviderID(context.Background(), derive.KindFlowRecord, h.key)
			if err != nil {
				t.Fatal(err)
			}
			var outcome string
			if err := h.store.DB().QueryRowContext(context.Background(),
				"SELECT outcome FROM source_availability WHERE provider_id = ?", providerID).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			if outcome != string(scenario.outcome) {
				t.Errorf("the availability says %s, want %s", outcome, scenario.outcome)
			}
			if stored := scalarCount(t, h.store,
				"SELECT count(*) FROM capture_configuration WHERE fingerprint IS NULL AND outcome = ?",
				string(scenario.outcome)); stored != 1 {
				t.Errorf("the unreadable configuration was recorded %d times with its outcome", stored)
			}
			h.at(netflowReadSecond)
			h.deliver(t, legInstant+30, forward(17, 44000, 53, 0, 9, legInstant, legInstant+1))
			if state := states(t, h)[9]; state != netflow.CaptureUnknown {
				t.Errorf("a record is %s, want capture_configuration_unknown", state)
			}
			for _, request := range h.fake.Requests() {
				if request.Path == getconfigEndpoint.Path || request.Path == isEnabledEndpoint.Path {
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
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliver(t, legInstant+10, forward(17, 45000, 53, 0, 5, legInstant, legInstant+1))
	h.sequence += 4
	h.deliver(t, legInstant+20)
	h.deliver(t, legInstant+25)
	h.recordGaps()
	if gaps := scalarCount(t, h.store,
		"SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'"); gaps != 1 {
		t.Errorf("%d sequence gaps were recorded, want 1", gaps)
	}
	for _, record := range h.records(t) {
		if record.lossDetectable != 1 {
			t.Errorf("a record under one captured interface says its loss is not detectable")
		}
	}
}
