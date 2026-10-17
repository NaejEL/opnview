package collect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/netflow"
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
	insideForward, insideReverse, _, _ := collecttest.NatRecords()
	at := collecttest.LegInstant

	t.Run("egress only", func(t *testing.T) {
		t.Parallel()
		// The inside interface captured on egress only and the upstream not at all: no forward record exists,
		// so the reply's reverse join is never confirmed (NJ4 rule 4) and counts nothing.
		h := collecttest.NewNetflowHarness(t)
		h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier, EgressOnly: true})
		h.At(collecttest.NetflowReadSecond)
		h.Deliver(t, at+30, insideReverse)
		h.Logged(t, collecttest.NatLegs("nat", at, "pass")...)
		connection := h.FlowID(t, "nat1")
		if volume := volumeOf(t, h, connection, netflow.OrientationForward); volume.State != netflow.VolumeIngressNotCaptured {
			t.Errorf("the forward direction entering by an egress-only interface is %s", volume.State)
		}
		if volume := volumeOf(t, h, connection, netflow.OrientationReverse); volume.State != netflow.VolumeMissing ||
			volume.Bytes != 0 {
			t.Errorf("the reverse direction is %+v, want missing: its record has no confirming forward record", volume)
		}
		if records := byOctets(t, h); collecttest.OrNull(records[20000].Reason) != netflow.UnmatchedReverseUnconfirmed {
			t.Errorf("the reply is %s (%s), want reverse_unconfirmed", records[20000].JoinState,
				collecttest.OrNull(records[20000].Reason))
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
			netflow.VolumeNotExportedWhenRefused {
			t.Errorf("the forward direction of a refused out leg is %+v", volume)
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
}

// TestTheIndexMapInForceAtReceptionMapsARecord is NJ-AC4: a record is mapped with the index map in force
// at reception, and a later change of the map re-maps no stored row; an exporter whose uptime goes
// backwards has the map read afresh before its next record is mapped (a request counter); index 0, the
// loopback's index and an index the map does not hold get their roles.
func TestTheIndexMapInForceAtReceptionMapsARecord(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	// The index map is read through the broker at discovery's max age: an hour here, so a read the mirror
	// can serve is served from it, and only a restart asks the firewall again.
	settings := config.Defaults()
	settings.FirewallLogInterval, settings.SecurityEventInterval, settings.MeasurementInterval = 0, 0, 0
	settings.DHCPLeaseInterval, settings.DNSLookupInterval, settings.ResolverCacheInterval = 0, 0, 0
	settings.ResolverLocalDataInterval, settings.FlowRecordInterval = 0, 0
	settings.DiscoveryInterval = time.Hour
	h.Collector.Configure(settings)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	index := func(value uint16) collecttest.FlowRecord {
		return collecttest.FlowRecord{Src: collecttest.InsideClient, Dst: collecttest.OutsideServer, SrcPort: 41000 + value,
			DstPort: 53, Protocol: 17, In: value, Out: collecttest.UpstreamIndex, Packets: 1, Octets: uint32(value),
			Start: at, End: at}
	}
	h.Deliver(t, at+30, index(collecttest.InsideIndex), index(0), index(collecttest.LoopbackIndex), index(99))
	reads := len(h.Fake.RequestsTo(opnsense.InterfacesInfo))

	// The map changes: the inside interface's kernel index is now another's. A record already stored
	// keeps what it was mapped to; nothing reads the map again while the mirror is fresh.
	changed := collecttest.InterfacesWithIndexes()
	rows := changed["rows"].([]map[string]any)
	rows[0]["statistics"] = map[string]any{"index": "99"}
	h.Fake.AnswerJSON(opnsense.InterfacesInfo, changed)
	h.Deliver(t, at+31, index(98))
	if again := len(h.Fake.RequestsTo(opnsense.InterfacesInfo)); again != reads {
		t.Errorf("a record of a running exporter read the index map again (%d reads, then %d)", reads, again)
	}
	// The exporter restarts: its uptime goes backwards. The map is read before its next record is mapped.
	h.Sequence++
	h.DeliverRaw(t, collecttest.DatagramBooted(at+32, h.Sequence, at, index(97)))
	if after := len(h.Fake.RequestsTo(opnsense.InterfacesInfo)); after <= reads {
		t.Errorf("after the exporter restarted the index map was not read again (%d reads)", after)
	}
	roles := map[int64][2]string{}
	for _, record := range h.Records(t) {
		roles[record.Octets] = [2]string{record.IngressRole, collecttest.OrNull(record.IngressIdentifier)}
	}
	for octets, want := range map[int64][2]string{
		collecttest.InsideIndex:   {netflow.RoleInterface, collecttest.InsideIdentifier},
		0:                         {netflow.RoleNoReceivingInterface, "<null>"},
		collecttest.LoopbackIndex: {netflow.RoleLocalDelivery, "<null>"},
		99:                        {netflow.RoleUnmapped, "<null>"},
		98:                        {netflow.RoleUnmapped, "<null>"},
		97:                        {netflow.RoleUnmapped, "<null>"},
	} {
		if roles[octets] != want {
			t.Errorf("the record of index %d was mapped %v, want %v", octets, roles[octets], want)
		}
	}
	// After the restart's read the map in force names index 99 the inside interface; the stored record of
	// index 99 is not re-mapped.
	h.Deliver(t, at+40, collecttest.FlowRecord{Src: collecttest.InsideClient, Dst: collecttest.OutsideServer,
		SrcPort: 42000, DstPort: 53, Protocol: 17, In: 99, Out: collecttest.UpstreamIndex, Packets: 1, Octets: 1099,
		Start: at, End: at})
	for _, record := range h.Records(t) {
		switch record.Octets {
		case 1099:
			if collecttest.OrNull(record.IngressIdentifier) != collecttest.InsideIdentifier {
				t.Errorf("a record received under the new map was mapped %s", collecttest.OrNull(record.IngressIdentifier))
			}
		case 99:
			if record.IngressRole != netflow.RoleUnmapped {
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
		Record collecttest.FixtureRecord `json:"record"`
	}
	raw, err := os.ReadFile(filepath.Join(collecttest.JoinFixtureDirectory, "f16_interleaved_counters.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	world := collecttest.LoadJoinWorld(t, collecttest.JoinFixtureDirectory)
	h := collecttest.NewNetflowWorldHarness(t, world.Body, world.Offered())
	h.Capture(t, collecttest.CaptureStartAt, world.Captured(nil)...)
	h.At(collecttest.NetflowReadSecond)
	gaps := func() int {
		h.RecordGaps()
		return collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM collection_gap WHERE reason = 'netflow_sequence_gap'")
	}
	export := collecttest.LegInstant + 10
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
			h.DeliverRaw(t, collecttest.Datagram(export, first+uint32(round), record))
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
		// One world, read three times: each failure is its own unreadable run, carrying its outcome.
		h := collecttest.NewNetflowHarness(t)
		for index, scenario := range scenarios {
			h.At(collecttest.CaptureStartAt + int64(index)*10)
			h.Fake.Answer(collecttest.GetconfigEndpoint, scenario.status, []byte(scenario.body))
			if err := h.Collector.CollectFlowRecordCapture(context.Background()); err != nil {
				t.Fatal(err)
			}
			if outcome := availabilityOutcomeOf(t, h); outcome != string(scenario.outcome) {
				t.Errorf("%s: the availability says %s, want %s", scenario.name, outcome, scenario.outcome)
			}
			if stored := collecttest.ScalarCount(t, h.Store,
				"SELECT count(*) FROM capture_configuration WHERE fingerprint IS NULL AND outcome = $1",
				string(scenario.outcome)); stored != 1 {
				t.Errorf("%s: the unreadable configuration was recorded %d times with its outcome", scenario.name, stored)
			}
		}
		h.At(collecttest.NetflowReadSecond)
		h.Deliver(t, collecttest.LegInstant+30, collecttest.Forward(17, 44000, 53, 0, 9, collecttest.LegInstant, collecttest.LegInstant+1))
		h.Logged(t, singleLeg("dns", collecttest.LegInstant, "udp", 17, 44000, 53, "pass"))
		if volumes := h.Volumes(t); len(volumes) != 0 {
			t.Errorf("a connection under an unreadable configuration has figures: %+v", volumes)
		}
		if records := byOctets(t, h); records[9].JoinState != netflow.JoinJoined {
			t.Errorf("a record under an unreadable configuration is %s: every record is joined from its own indexes",
				records[9].JoinState)
		}
		onlyReadOnlyCalls(t, h)
	})
	t.Run("interfaces_info", func(t *testing.T) {
		t.Parallel()
		// One world, read three times: each failure leaves the configuration readable, every export
		// unknown, and the broker's outcome named in the availability's detail.
		h := collecttest.NewNetflowHarness(t)
		for index, scenario := range scenarios {
			body := scenario.body
			if scenario.status == http.StatusOK {
				body = `{"rows": "not the envelope"}`
			}
			h.Fake.Answer(opnsense.InterfacesInfo, scenario.status, []byte(body))
			h.Capture(t, collecttest.CaptureStartAt+int64(index)*10, collecttest.Captured{Identifier: collecttest.InsideIdentifier},
				collecttest.Captured{Identifier: collecttest.UpstreamIdentifier})
			if unknown := collecttest.ScalarCount(t, h.Store, `SELECT count(*) FROM capture_interface
				WHERE exported = 'unknown' AND capture_configuration_id = (SELECT max(id) FROM capture_configuration)`); unknown != 2 {
				t.Errorf("%s: %d captured interfaces have an unknown export, want the two captured", scenario.name, unknown)
			}
			if detail := availabilityDetailOf(t, h); !strings.Contains(detail, "interfaces_info answered") {
				t.Errorf("%s: the availability's detail %q does not name what interfaces_info answered", scenario.name,
					detail)
			}
		}
		h.Fake.AnswerJSON(opnsense.InterfacesInfo, collecttest.InterfacesWithIndexes())
		h.At(collecttest.NetflowReadSecond)
		h.Logged(t, collecttest.NatLegs("nat", collecttest.LegInstant, "pass")...)
		if volume := volumeOf(t, h, h.FlowID(t, "nat1"), netflow.OrientationForward); volume.State != netflow.VolumeCaptureUnknown {
			t.Errorf("a direction entering by an interface whose export is unknown is %s, want "+
				"capture_configuration_unknown", volume.State)
		}
		onlyReadOnlyCalls(t, h)
	})
}

// availabilityOutcomeOf and availabilityDetailOf read the flow_record provider's availability.
func availabilityOutcomeOf(t *testing.T, h *collecttest.NetflowHarness) string {
	t.Helper()
	outcome, _ := availabilityOf(t, h)
	return outcome
}

func availabilityDetailOf(t *testing.T, h *collecttest.NetflowHarness) string {
	t.Helper()
	_, detail := availabilityOf(t, h)
	return detail
}

func availabilityOf(t *testing.T, h *collecttest.NetflowHarness) (string, string) {
	t.Helper()
	providerID, err := h.Store.ProviderID(context.Background(), derive.KindFlowRecord, h.Key)
	if err != nil {
		t.Fatal(err)
	}
	var outcome string
	var detail *string
	if err := h.Store.DB().QueryRowContext(context.Background(),
		"SELECT outcome, detail FROM source_availability WHERE provider_id = $1", providerID).Scan(&outcome, &detail); err != nil {
		t.Fatal(err)
	}
	return outcome, collecttest.OrNull(detail)
}

// onlyReadOnlyCalls checks that no NetFlow action other than the two reads was called, and those by GET
// (BR-AC15).
func onlyReadOnlyCalls(t *testing.T, h *collecttest.NetflowHarness) {
	t.Helper()
	for _, request := range h.Fake.Requests() {
		if request.Path == collecttest.GetconfigEndpoint.Path || request.Path == collecttest.IsEnabledEndpoint.Path {
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
			t.Errorf("a record under one exporting interface says its loss is not detectable")
		}
	}
}
