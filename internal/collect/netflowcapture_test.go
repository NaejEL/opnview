package collect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The capture configuration, the states it gives, the index map in force at reception, loss detection
// under several counters, and the identity of a record (PS-AC51 and PS-AC52 of the NetFlow join amendment,
// and NJ-AC2, NJ-AC4, NJ-AC10 and NJ-AC15 of its amendment "the NetFlow join with every interface
// captured").

// TestCoverageAndStatesFollowTheConfiguration is NJ-AC10 on the default world: what the configuration
// captures names each direction's state, and nothing is read from the records.
func TestCoverageAndStatesFollowTheConfiguration(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := NatRecords()
	at := LegInstant

	t.Run("egress only", func(t *testing.T) {
		t.Parallel()
		// The inside interface captured on egress only and the upstream not at all: no forward record exists,
		// so the reply's reverse join is never confirmed (NJ4 rule 4) and counts nothing.
		h := newNetflowHarness(t)
		h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier, EgressOnly: true})
		h.At(NetflowReadSecond)
		h.Deliver(t, at+30, insideReverse)
		h.Logged(t, NatLegs("nat", at, "pass")...)
		connection := h.FlowID(t, "nat1")
		if volume := volumeOf(t, h, connection, orientationForward); volume.State != volumeIngressNotCaptured {
			t.Errorf("the forward direction entering by an egress-only interface is %s", volume.State)
		}
		if volume := volumeOf(t, h, connection, orientationReverse); volume.State != volumeMissing ||
			volume.Bytes != 0 {
			t.Errorf("the reverse direction is %+v, want missing: its record has no confirming forward record", volume)
		}
		if records := byOctets(t, h); OrNull(records[20000].Reason) != unmatchedReverseUnconfirmed {
			t.Errorf("the reply is %s (%s), want reverse_unconfirmed", records[20000].JoinState,
				OrNull(records[20000].Reason))
		}
	})

	t.Run("sequence gap", func(t *testing.T) {
		t.Parallel()
		h := newNetflowHarness(t)
		h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
		h.At(NetflowReadSecond)
		long := insideForward
		long.End = at + 60
		h.Deliver(t, at+30, long)
		h.Sequence += 3
		h.Deliver(t, at+50)
		h.Deliver(t, at+55)
		h.RecordGaps()
		h.Logged(t, NatLegs("nat", at, "pass")...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), orientationForward); volume.State != volumeExportGap {
			t.Errorf("a direction a sequence gap meets is %s, want export_gap", volume.State)
		}
	})

	t.Run("refused legs", func(t *testing.T) {
		t.Parallel()
		// A refused `out` leg: its forward packets were blocked before the egress capture.
		h := newNetflowHarness(t)
		h.Capture(t, CaptureStartAt, Captured{Identifier: UpstreamIdentifier})
		h.At(NetflowReadSecond)
		legs := NatLegs("nat", at, "pass")
		legs[1].Action = "block"
		h.Logged(t, legs...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), orientationForward); volume.State !=
			volumeNotExportedWhenRefused {
			t.Errorf("the forward direction of a refused out leg is %+v", volume)
		}

		// A refused `in` leg: its forward packets were captured at ingress, before pf.
		blocked := newNetflowHarness(t)
		blocked.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
		blocked.At(NetflowReadSecond)
		blocked.Deliver(t, at+30, Forward(6, 50500, 22, 0x02, 60, at, at))
		blocked.Logged(t, singleLeg("blocked", at, "tcp", 6, 50500, 22, "block"))
		if volume := volumeOf(t, blocked, blocked.FlowID(t, "blocked"), orientationForward); volume.State !=
			volumeMeasured || volume.Bytes != 60 {
			t.Errorf("the forward direction of a refused in leg is %+v, want measured", volume)
		}
	})
}

// TestTheIndexMapInForceAtReceptionMapsARecord is NJ-AC4: a record is mapped with the index map in force
// at reception, and a later change of the map re-maps no stored row; an exporter whose uptime goes
// backwards has the map read afresh before its next record is mapped (a request counter); index 0, the
// loopback's index and an index the map does not hold get their roles.
func TestTheIndexMapInForceAtReceptionMapsARecord(t *testing.T) {
	t.Parallel()
	at := LegInstant
	h := newNetflowHarness(t)
	// The plugin reads the index map through the broker at most five minutes old, so a read the mirror can serve
	// is served from it, and only a restart asks the firewall again.
	h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	h.At(NetflowReadSecond)
	index := func(value uint16) FlowRecord {
		return FlowRecord{Src: InsideClient, Dst: OutsideServer, SrcPort: 41000 + value,
			DstPort: 53, Protocol: 17, In: value, Out: UpstreamIndex, Packets: 1, Octets: uint32(value),
			Start: at, End: at}
	}
	h.Deliver(t, at+30, index(InsideIndex), index(0), index(LoopbackIndex), index(99))
	reads := len(h.Fake.RequestsTo(opnsense.InterfacesInfo))

	// The map changes: the inside interface's kernel index is now another's. A record already stored
	// keeps what it was mapped to; nothing reads the map again while the mirror is fresh.
	changed := InterfacesWithIndexes()
	rows := changed["rows"].([]map[string]any)
	rows[0]["statistics"] = map[string]any{"index": "99"}
	h.Fake.AnswerJSON(opnsense.InterfacesInfo, changed)
	h.Deliver(t, at+31, index(98))
	if again := len(h.Fake.RequestsTo(opnsense.InterfacesInfo)); again != reads {
		t.Errorf("a record of a running exporter read the index map again (%d reads, then %d)", reads, again)
	}
	// The exporter restarts: its uptime goes backwards. The map is read before its next record is mapped.
	h.Sequence++
	h.DeliverRaw(t, DatagramBooted(at+32, h.Sequence, at, index(97)))
	if after := len(h.Fake.RequestsTo(opnsense.InterfacesInfo)); after <= reads {
		t.Errorf("after the exporter restarted the index map was not read again (%d reads)", after)
	}
	roles := map[int64][2]string{}
	for _, record := range h.Records(t) {
		roles[record.Octets] = [2]string{record.IngressRole, OrNull(record.IngressIdentifier)}
	}
	for octets, want := range map[int64][2]string{
		InsideIndex:   {roleInterface, InsideIdentifier},
		0:             {roleNoReceivingInterface, "<null>"},
		LoopbackIndex: {roleLocalDelivery, "<null>"},
		99:            {roleUnmapped, "<null>"},
		98:            {roleUnmapped, "<null>"},
		97:            {roleUnmapped, "<null>"},
	} {
		if roles[octets] != want {
			t.Errorf("the record of index %d was mapped %v, want %v", octets, roles[octets], want)
		}
	}
	// After the restart's read the map in force names index 99 the inside interface; the stored record of
	// index 99 is not re-mapped.
	h.Deliver(t, at+40, FlowRecord{Src: InsideClient, Dst: OutsideServer,
		SrcPort: 42000, DstPort: 53, Protocol: 17, In: 99, Out: UpstreamIndex, Packets: 1, Octets: 1099,
		Start: at, End: at})
	for _, record := range h.Records(t) {
		switch record.Octets {
		case 1099:
			if OrNull(record.IngressIdentifier) != InsideIdentifier {
				t.Errorf("a record received under the new map was mapped %s", OrNull(record.IngressIdentifier))
			}
		case 99:
			if record.IngressRole != roleUnmapped {
				t.Errorf("a stored record was re-mapped to %s when the map changed", record.IngressRole)
			}
		}
	}
}

// TestLossIsDetectedUnderSeveralCounters is NJ-AC15 on F16: five counters interleaving under one
// observation domain, as many as the configuration reports exporting, assert no loss; one dropped
// datagram is one netflow_sequence_gap; and every record says its loss is detectable.
func TestLossIsDetectedUnderSeveralCounters(t *testing.T) {
	t.Parallel()
	var fixture struct {
		Provenance          map[string]any `json:"__provenance__"`
		Name                string         `json:"name"`
		Counters            []uint32       `json:"counters"`
		DatagramsPerCounter int            `json:"datagrams_per_counter"`
		Dropped             struct {
			Counter  int `json:"counter"`
			Datagram int `json:"datagram"`
		} `json:"dropped"`
		Record FixtureRecord `json:"record"`
	}
	raw, err := os.ReadFile(filepath.Join(JoinFixtureDirectory, "f16_interleaved_counters.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	world := LoadJoinWorld(t, JoinFixtureDirectory)
	h := newNetflowWorld(t, world.Body, world.Offered())
	h.Capture(t, CaptureStartAt, world.Captured(nil)...)
	h.At(NetflowReadSecond)
	gaps := func() int {
		h.RecordGaps()
		return collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'")
	}
	export := LegInstant + 10
	for round := 0; round < fixture.DatagramsPerCounter; round++ {
		if round == fixture.Dropped.Datagram {
			// Every round so far went through: five interleaved counters, as many as the configuration
			// exports, and no loss.
			if found := gaps(); found != 0 {
				t.Errorf("%s: five interleaved counters, as many as the configuration exports, gave %d sequence gaps",
					fixture.Name, found)
			}
		}
		for counter, first := range fixture.Counters {
			export++
			if counter == fixture.Dropped.Counter && round == fixture.Dropped.Datagram {
				continue
			}
			record := fixture.Record.FlowRecord()
			record.SrcPort = uint16(40000 + counter*100 + round)
			h.DeliverRaw(t, Datagram(export, first+uint32(round), record))
		}
	}
	if found := gaps(); found != 1 {
		t.Errorf("%s: one dropped datagram gave %d sequence gaps, want 1", fixture.Name, found)
	}
	for _, record := range h.Records(t) {
		if record.LossDetectable != 1 {
			t.Errorf("%s: a record under a configuration bounding the counters says its loss is not detectable",
				fixture.Name)
			break
		}
	}
}

// TestARecordHasOneIdentityPerDelivery is PS-AC51.
func TestARecordHasOneIdentityPerDelivery(t *testing.T) {
	t.Parallel()
	at := LegInstant
	h := newNetflowHarness(t)
	h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	h.At(NetflowReadSecond)
	one := Forward(17, 42000, 53, 0, 1, at, at+1)
	payload := Datagram(at+30, 1, one)
	h.DeliverRaw(t, payload)
	h.DeliverRaw(t, payload)
	if count := len(h.Records(t)); count != 1 {
		t.Errorf("a datagram delivered twice stored %d rows, want 1", count)
	}
	h.DeliverRaw(t, Datagram(at+31, 2, Forward(17, 42001, 53, 0, 2, at, at+1), Forward(17, 42001, 53, 0, 2, at, at+1)))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM plugin_netflow.flow_record WHERE octets = 2"); count != 2 {
		t.Errorf("two identical records of one datagram stored %d rows, want 2", count)
	}
	// Two nodes, one sequence number and one export time: two datagrams whose octets differ.
	h.DeliverRaw(t, Datagram(at+32, 3, Forward(17, 42002, 53, 0, 3, at, at+1)))
	h.DeliverRaw(t, Datagram(at+32, 3, Forward(17, 42003, 53, 0, 4, at, at+1)))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM plugin_netflow.flow_record WHERE octets IN (3, 4)"); count != 2 {
		t.Errorf("the datagrams of two nodes stored %d rows, want 2", count)
	}
	// The node recreated: its sequence restarts, and the same record arrives in a new datagram.
	h.DeliverRaw(t, Datagram(at+90, 1, one))
	if count := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM plugin_netflow.flow_record WHERE octets = 1"); count != 2 {
		t.Errorf("a record after a sequence restart stored %d rows in all, want 2", count)
	}
}

// TestAnUnavailableConfigurationIsReportedAndNothingIsGuessed is PS-AC52 and NJ-AC2: a 404, a 403 and an
// unparseable answer of getconfig each give capture_configuration_unknown, with what the firewall
// answered in the availability; the same answers of interfaces_info leave the configuration readable with
// every interface's export unknown, the directions it would measure capture_configuration_unknown, and
// the broker's outcome named in the availability; and the reads are read-only actions, by GET, and
// nothing else.
func TestAnUnavailableConfigurationIsReportedAndNothingIsGuessed(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name    string
		status  int
		body    string
		outcome vocab.AvailabilityOutcome
	}{
		{"not found", http.StatusNotFound, `{"message":"no"}`, vocab.OutcomeNotFound},
		{"forbidden", http.StatusForbidden, `{"message":"no"}`, vocab.OutcomeDenied},
		{"unparseable", http.StatusOK, `{"netflow": "not the model"}`, vocab.OutcomeUndecodable},
	}
	t.Run("getconfig", func(t *testing.T) {
		t.Parallel()
		// One world, read three times, each read past the provider's cadence after the last, so the broker asks the
		// firewall again: each failure is its own unreadable run, carrying its outcome.
		h := newNetflowHarness(t)
		for index, scenario := range scenarios {
			h.At(CaptureStartAt + int64(index)*100)
			h.Fake.Answer(GetconfigEndpoint, scenario.status, []byte(scenario.body))
			if err := h.Collector.CollectDeclaredKind(context.Background(), flowRecordKind, netflowKey); err != nil {
				t.Fatal(err)
			}
			h.Derive(t)
			if outcome := availabilityOutcomeOf(t, h); outcome != string(scenario.outcome) {
				t.Errorf("%s: the availability says %s, want %s", scenario.name, outcome, scenario.outcome)
			}
			if stored := collecttest.ScalarCount(t, h.Store,
				"SELECT count(*) FROM plugin_netflow.capture_configuration WHERE NOT readable AND outcome = $1",
				string(scenario.outcome)); stored != 1 {
				t.Errorf("%s: the unreadable configuration was recorded %d times with its outcome", scenario.name, stored)
			}
		}
		h.At(NetflowReadSecond)
		h.Deliver(t, LegInstant+30, Forward(17, 44000, 53, 0, 9, LegInstant, LegInstant+1))
		h.Logged(t, singleLeg("dns", LegInstant, "udp", 17, 44000, 53, "pass"))
		if volumes := h.Volumes(t); len(volumes) != 0 {
			t.Errorf("a connection under an unreadable configuration has figures: %+v", volumes)
		}
		if records := byOctets(t, h); records[9].JoinState != joinJoined {
			t.Errorf("a record under an unreadable configuration is %s: every record is joined from its own indexes",
				records[9].JoinState)
		}
		onlyReadOnlyCalls(t, h)
	})
	t.Run("interfaces_info", func(t *testing.T) {
		t.Parallel()
		// One world, read three times: each failure leaves the configuration readable, every export
		// unknown, and the broker's outcome named in the availability's detail.
		h := newNetflowHarness(t)
		for index, scenario := range scenarios {
			body := scenario.body
			if scenario.status == http.StatusOK {
				body = `{"rows": "not the envelope"}`
			}
			h.Fake.Answer(opnsense.InterfacesInfo, scenario.status, []byte(body))
			h.Capture(t, CaptureStartAt+int64(index)*100, Captured{Identifier: InsideIdentifier},
				Captured{Identifier: UpstreamIdentifier})
			if unknown := collecttest.ScalarCount(t, h.Store, `SELECT count(*) FROM plugin_netflow.capture_interface
				WHERE exported = 'unknown'
				  AND first_read_at = (SELECT max(first_read_at) FROM plugin_netflow.capture_configuration)`); unknown != 2 {
				t.Errorf("%s: %d captured interfaces have an unknown export, want the two captured", scenario.name, unknown)
			}
			if detail := availabilityDetailOf(t, h); !strings.Contains(detail, "interfaces_info answered") {
				t.Errorf("%s: the availability's detail %q does not name what interfaces_info answered", scenario.name,
					detail)
			}
		}
		h.Fake.AnswerJSON(opnsense.InterfacesInfo, InterfacesWithIndexes())
		h.At(NetflowReadSecond)
		h.Logged(t, NatLegs("nat", LegInstant, "pass")...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), orientationForward); volume.State != volumeCaptureUnknown {
			t.Errorf("a direction entering by an interface whose export is unknown is %s, want "+
				"capture_configuration_unknown", volume.State)
		}
		onlyReadOnlyCalls(t, h)
	})
}

// availabilityOutcomeOf and availabilityDetailOf read the flow_record provider's availability.
func availabilityOutcomeOf(t *testing.T, h *netflowWorld) string {
	t.Helper()
	outcome, _ := availabilityOf(t, h)
	return outcome
}

func availabilityDetailOf(t *testing.T, h *netflowWorld) string {
	t.Helper()
	_, detail := availabilityOf(t, h)
	return detail
}

func availabilityOf(t *testing.T, h *netflowWorld) (string, string) {
	t.Helper()
	providerID, err := h.Store.ProviderID(context.Background(), flowRecordKind, h.Key)
	if err != nil {
		t.Fatal(err)
	}
	var outcome string
	var detail *string
	if err := h.Store.DB().QueryRowContext(context.Background(),
		"SELECT outcome, detail FROM source_availability WHERE provider_id = $1", providerID).Scan(&outcome, &detail); err != nil {
		t.Fatal(err)
	}
	return outcome, OrNull(detail)
}

// onlyReadOnlyCalls checks that no NetFlow action other than the two reads was called, and those by GET
// (BR-AC15).
func onlyReadOnlyCalls(t *testing.T, h *netflowWorld) {
	t.Helper()
	for _, request := range h.Fake.Requests() {
		if request.Path == GetconfigEndpoint.Path || request.Path == IsEnabledEndpoint.Path {
			if request.Method != http.MethodGet {
				t.Errorf("%s was called with %s", request.Path, request.Method)
			}
			continue
		}
		if strings.HasPrefix(request.Path, "/api/diagnostics/netflow/") {
			t.Errorf("a NetFlow action other than the two reads was called: %s %s", request.Method, request.Path)
		}
	}
}

// TestASequenceGapIsAssertedUnderOneInterface is NJ7's bound at one exporting interface: a forward jump is
// recorded as netflow_sequence_gap, and the records carry loss_detectable.
func TestASequenceGapIsAssertedUnderOneInterface(t *testing.T) {
	t.Parallel()
	h := newNetflowHarness(t)
	h.Capture(t, CaptureStartAt, Captured{Identifier: InsideIdentifier})
	h.At(NetflowReadSecond)
	h.Deliver(t, LegInstant+10, Forward(17, 45000, 53, 0, 5, LegInstant, LegInstant+1))
	h.Sequence += 4
	h.Deliver(t, LegInstant+20)
	h.Deliver(t, LegInstant+25)
	h.RecordGaps()
	if gaps := collecttest.ScalarCount(t, h.Store,
		"SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'"); gaps != 1 {
		t.Errorf("%d sequence gaps were recorded, want 1", gaps)
	}
	for _, record := range h.Records(t) {
		if record.LossDetectable != 1 {
			t.Errorf("a record under one exporting interface says its loss is not detectable")
		}
	}
}
