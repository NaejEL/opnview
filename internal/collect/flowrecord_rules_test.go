package collect

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The join's rules, one criterion per test (PS-AC31 to PS-AC34 of the NetFlow join amendment).

// singleLeg is one `in` leg of the inside client on the inside device, of any protocol.
func singleLeg(digest string, at int64, protocol string, number, srcPort, dstPort int, action string) legRow {
	return legRow{digest: digest, at: at, device: insideDevice, dir: "in", action: action, src: insideClient.String(),
		dst: outsideServer.String(), srcPort: srcPort, dstPort: dstPort, protocol: protocol, protocolNumber: number}
}

// forward is one record of the inside client's connection received on the inside device; reverse one
// of its replies sent there.
func forward(protocol uint8, srcPort, dstPort uint16, flags uint8, octets uint32, start, end int64) flowRecord {
	return flowRecord{src: insideClient, dst: outsideServer, srcPort: srcPort, dstPort: dstPort, protocol: protocol,
		flags: flags, in: insideIndex, out: upstreamIndex, packets: 1, octets: octets, start: start, end: end}
}

func reverse(protocol uint8, srcPort, dstPort uint16, flags uint8, octets uint32, start, end int64) flowRecord {
	return flowRecord{src: outsideServer, dst: insideClient, srcPort: dstPort, dstPort: srcPort, protocol: protocol,
		flags: flags, in: upstreamIndex, out: insideIndex, packets: 1, octets: octets, start: start, end: end}
}

// volumeOf returns one direction of a connection's measured volume.
func volumeOf(t *testing.T, h *netflowHarness, connection int64, direction string) volumeRow {
	t.Helper()
	for _, volume := range h.volumes(t) {
		if volume.connection == connection && volume.direction == direction {
			return volume
		}
	}
	t.Fatalf("the connection %d has no %s figure: %+v", connection, direction, h.volumes(t))
	return volumeRow{}
}

// byOctets indexes the stored records by their octets, which each test keeps distinct.
func byOctets(t *testing.T, h *netflowHarness) map[int64]recordRow {
	t.Helper()
	indexed := map[int64]recordRow{}
	for _, record := range h.records(t) {
		indexed[record.octets] = record
	}
	return indexed
}

// TestSplitRecordsSumExactlyAndContinuityBoundsAConnection is PS-AC31: one TCP connection exported as
// several records each way -- split by the active timeout, and by an idle gap within pf's timeout --
// sums exactly; a later SYN record beyond the tolerance joins no old leg, and joins the new leg when
// one is logged; a UDP record after a gap longer than pf's default UDP timeout (S12) joins nothing.
func TestSplitRecordsSumExactlyAndContinuityBoundsAConnection(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliver(t, at+1200,
		forward(6, 50000, 443, 0x02|0x10, 100, at, at+100),
		forward(6, 50000, 443, 0x10, 200, at+100, at+200),
		forward(6, 50000, 443, 0x10|0x01, 300, at+1000, at+1100),
		reverse(6, 50000, 443, 0x12, 1000, at, at+100),
		reverse(6, 50000, 443, 0x10|0x01, 2000, at+1000, at+1100),
		forward(6, 50000, 443, 0x02, 50, at+2000, at+2001),
		forward(17, 50001, 53, 0, 70, at, at+5),
		forward(17, 50001, 53, 0, 80, at+205, at+206))
	h.logged(t, singleLeg("tcp", at, "tcp", 6, 50000, 443, "pass"), singleLeg("udp", at, "udp", 17, 50001, 53, "pass"))

	tcpLeg, udpLeg := h.flowID(t, "tcp"), h.flowID(t, "udp")
	records := byOctets(t, h)
	for _, octets := range []int64{100, 200, 300, 1000, 2000} {
		if record := records[octets]; record.joinState != store.JoinJoined || *record.joinedFlowID != tcpLeg {
			t.Errorf("the TCP record of %d octets is %s (%s)", octets, record.joinState, orNull(record.reason))
		}
	}
	if record := records[50]; record.joinState != store.JoinUnmatched ||
		orNull(record.reason) != store.UnmatchedNoLoggedLeg {
		t.Errorf("the later SYN record is %s (%s), want no_logged_leg", record.joinState, orNull(record.reason))
	}
	if record := records[70]; record.joinState != store.JoinJoined || *record.joinedFlowID != udpLeg {
		t.Errorf("the UDP record within the timeout is %s", record.joinState)
	}
	if record := records[80]; record.joinState != store.JoinUnmatched ||
		orNull(record.reason) != store.UnmatchedNoLoggedLeg {
		t.Errorf("the UDP record after the timeout is %s (%s), want no_logged_leg", record.joinState,
			orNull(record.reason))
	}
	if volume := volumeOf(t, h, tcpLeg, store.OrientationForward); volume.bytes != 600 || volume.records != 3 ||
		volume.state != store.VolumeMeasured {
		t.Errorf("the forward figure is %+v, want 600 bytes in 3 records, measured", volume)
	}
	if volume := volumeOf(t, h, tcpLeg, store.OrientationReverse); volume.bytes != 3000 || volume.records != 2 {
		t.Errorf("the reverse figure is %+v, want 3000 bytes in 2 records", volume)
	}

	// The tuple's new connection, logged: the SYN record joins its leg.
	again := newNetflowHarness(t)
	again.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	again.at(netflowReadSecond)
	again.deliver(t, at+2100, forward(6, 50000, 443, 0x02, 50, at+2000, at+2001))
	again.logged(t, singleLeg("old", at, "tcp", 6, 50000, 443, "pass"),
		singleLeg("new", at+2000, "tcp", 6, 50000, 443, "pass"))
	if stored := again.records(t); len(stored) != 1 || stored[0].joinedFlowID == nil ||
		*stored[0].joinedFlowID != again.flowID(t, "new") {
		t.Errorf("the SYN record did not join the new leg: %+v", stored)
	}
}

// TestAnUnmatchedRecordIsCountedInNoFigure is PS-AC32: a record with no leg, a record meeting two legs
// logged in the same second, and a record that started before the filter log's horizon each carry
// their reason and reach no connection's figure; a record under a configuration of two captured
// interfaces reaches nothing at all.
func TestAnUnmatchedRecordIsCountedInNoFigure(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	if _, err := h.store.DB().ExecContext(context.Background(),
		"INSERT INTO retention_purge (id, purged_before, purged_at) VALUES (1, ?, ?)", at-100, at); err != nil {
		t.Fatal(err)
	}
	h.at(netflowReadSecond)
	h.deliver(t, at+60,
		forward(17, 40000, 123, 0, 11, at+10, at+11),
		forward(6, 40001, 443, 0x10, 22, at+10, at+11),
		forward(6, 40002, 443, 0x10, 33, at-200, at-199))
	duplicate := singleLeg("dupa", at, "tcp", 6, 40001, 443, "pass")
	twin := duplicate
	twin.digest = "dupb"
	h.logged(t, duplicate, twin, singleLeg("other", at, "tcp", 6, 40009, 443, "pass"))
	records := byOctets(t, h)
	want := map[int64]string{11: store.UnmatchedNoLoggedLeg, 22: store.UnmatchedAmbiguous,
		33: store.UnmatchedBeyondFlowHorizon}
	for octets, reason := range want {
		if record := records[octets]; record.joinState != store.JoinUnmatched || orNull(record.reason) != reason {
			t.Errorf("the record of %d octets is %s (%s), want unmatched for %s", octets, record.joinState,
				orNull(record.reason), reason)
		}
	}
	for _, volume := range h.volumes(t) {
		if volume.bytes != 0 || volume.records != 0 {
			t.Errorf("an unmatched record reached a figure: %+v", volume)
		}
	}
	// The unmatched family holds them, keyed by Observation Point, direction, protocol, version and
	// reason, at every period; no other family counts their bytes.
	for _, period := range []string{"1h", "24h", "7d", "30d"} {
		for octets, reason := range want {
			if got := scalarCount(t, h.store, "SELECT coalesce(sum(bytes), 0) FROM unmatched_record_aggregate_"+period+
				" WHERE reason = ? AND observation_point_identifier = ? AND direction = 'ingress'", reason,
				insideIdentifier); got != int(octets) {
				t.Errorf("at %s the unmatched family holds %d bytes for %s, want %d", period, got, reason, octets)
			}
		}
	}
	if counted := scalarCount(t, h.store, "SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h"); counted != 0 {
		t.Errorf("the volume family counts %d measured bytes of unmatched records", counted)
	}

	two := newNetflowHarness(t)
	two.capture(t, captureStartAt, captured{identifier: insideIdentifier}, captured{identifier: upstreamIdentifier})
	two.at(netflowReadSecond)
	two.deliver(t, at+60, forward(6, 40003, 443, 0x10, 44, at, at+1))
	two.logged(t, singleLeg("two", at, "tcp", 6, 40003, 443, "pass"))
	stored := two.records(t)
	if len(stored) != 1 || stored[0].joinState != store.JoinNotJoinable || stored[0].reason != nil {
		t.Errorf("under two captured interfaces the record is %+v, want not joinable with no reason", stored)
	}
	if volumes := two.volumes(t); len(volumes) != 0 {
		t.Errorf("under two captured interfaces a connection has figures: %+v", volumes)
	}
}

// TestAnIPv6ConnectionAndICMPJoinOnTheirAddresses is PS-AC33: an IPv6 connection crossing two interfaces,
// one captured, joins although its leg's addresses are written non-canonically; an ICMP echo leg joins
// on its addresses alone; an ICMP record meeting two ICMP legs of the same second joins neither.
func TestAnIPv6ConnectionAndICMPJoinOnTheirAddresses(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliver(t, at+60,
		flowRecord{src: insideClientV6, dst: outsideServerV6, srcPort: 50100, dstPort: 443, protocol: 6, flags: 0x11,
			in: insideIndex, out: upstreamIndex, packets: 3, octets: 600, start: at, end: at + 2},
		forward(1, 0, 0, 0, 84, at+5, at+6),
		flowRecord{src: secondInsideHost, dst: outsideServer, protocol: 1, in: insideIndex, out: upstreamIndex,
			packets: 1, octets: 85, start: at + 20, end: at + 20})
	h.logged(t,
		legRow{digest: "v6in", at: at, device: insideDevice, dir: "in", action: "pass", src: "2001:db8:0:0:0:0:0:10",
			dst: "2001:DB8:FFFF:0::80", srcPort: 50100, dstPort: 443, protocol: "tcp", protocolNumber: 6, sequence: 99},
		legRow{digest: "v6out", at: at, device: upstreamDevice, dir: "out", action: "pass", src: "2001:db8::10",
			dst: "2001:db8:ffff::80", srcPort: 50100, dstPort: 443, protocol: "tcp", protocolNumber: 6, sequence: 99},
		legRow{digest: "icmp", at: at + 5, device: insideDevice, dir: "in", action: "pass", src: insideClient.String(),
			dst: outsideServer.String(), protocol: "icmp", protocolNumber: 1},
		legRow{digest: "icmpa", at: at + 20, device: insideDevice, dir: "in", action: "pass",
			src: secondInsideHost.String(), dst: outsideServer.String(), protocol: "icmp", protocolNumber: 1},
		legRow{digest: "icmpb", at: at + 20, device: insideDevice, dir: "in", action: "pass",
			src: secondInsideHost.String(), dst: outsideServer.String(), protocol: "icmp", protocolNumber: 1})
	records := byOctets(t, h)
	if record := records[600]; record.joinedFlowID == nil || *record.joinedFlowID != h.flowID(t, "v6in") {
		t.Errorf("the IPv6 record is %s (%s), not joined to its inside leg", record.joinState, orNull(record.reason))
	}
	if record := records[84]; record.joinedFlowID == nil || *record.joinedFlowID != h.flowID(t, "icmp") {
		t.Errorf("the ICMP record is %s (%s), not joined to its echo leg", record.joinState, orNull(record.reason))
	}
	if record := records[85]; orNull(record.reason) != store.UnmatchedAmbiguous {
		t.Errorf("the ICMP record meeting two legs is %s (%s), want ambiguous", record.joinState,
			orNull(record.reason))
	}
}

// joinFingerprint is every decision and every figure of a run, legs named by their digests.
func joinFingerprint(t *testing.T, h *netflowHarness, digests ...string) string {
	t.Helper()
	names := map[int64]string{}
	for _, digest := range digests {
		names[h.flowID(t, digest)] = digest
	}
	var fingerprint string
	for _, record := range h.records(t) {
		joined := "-"
		if record.joinedFlowID != nil {
			joined = names[*record.joinedFlowID]
		}
		fingerprint += fmt.Sprintf("%d %d %s %s %s %s;", record.startAt, record.octets, record.joinState, joined,
			orNull(record.orientation), orNull(record.reason))
	}
	for _, volume := range h.volumes(t) {
		fingerprint += fmt.Sprintf("%s %s %s %d %d %d %s;", names[volume.connection], volume.direction, volume.state,
			volume.bytes, volume.packets, volume.records, orNull(volume.leg))
	}
	return fingerprint
}

// TestTheJoinDoesNotDependOnArrivalOrder is PS-AC34: the same records and legs ingested records first,
// legs first, interleaved, and with the pairing decided after, give the same decisions and figures; a
// second derivation writes nothing; and no record is decided before the filter log has been read past
// its start plus the tolerance.
func TestTheJoinDoesNotDependOnArrivalOrder(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := natRecords()
	legs := natLegs("nat", legInstant, "pass")
	extra := forward(17, 40000, 53, 0, 77, legInstant+1, legInstant+2)
	orders := map[string]func(t *testing.T, h *netflowHarness){
		"records first": func(t *testing.T, h *netflowHarness) {
			h.deliver(t, legInstant+30, insideForward, insideReverse, extra)
			h.logged(t, legs...)
		},
		"legs first": func(t *testing.T, h *netflowHarness) {
			h.logged(t, legs...)
			h.deliver(t, legInstant+30, insideForward, insideReverse, extra)
			h.derive(t)
		},
		"interleaved": func(t *testing.T, h *netflowHarness) {
			h.deliver(t, legInstant+30, insideReverse)
			h.logged(t, legs[0])
			h.deliver(t, legInstant+31, extra, insideForward)
			h.logged(t, legs[1])
		},
		"pairing after": func(t *testing.T, h *netflowHarness) {
			h.logged(t, legs[0])
			h.deliver(t, legInstant+30, insideForward, insideReverse, extra)
			h.derive(t)
			h.logged(t, legs[1])
		},
	}
	fingerprints := map[string]string{}
	for name, ingest := range orders {
		h := newNetflowHarness(t)
		h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
		h.at(netflowReadSecond)
		ingest(t, h)
		fingerprints[name] = joinFingerprint(t, h, "nat1", "nat2")
		changed, err := h.collector.joinFlowRecords(context.Background())
		if err != nil || changed != 0 {
			t.Errorf("%s: a second join changed %d rows (%v)", name, changed, err)
		}
		before := scalarCount(t, h.store, "SELECT coalesce(sum(computed_at), 0) FROM connection_measured_volume")
		h.derive(t)
		if after := scalarCount(t, h.store,
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
	early := newNetflowHarness(t)
	early.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	early.at(netflowReadSecond)
	early.deliver(t, legInstant+30, insideForward)
	early.derive(t)
	early.at(legInstant + 2)
	early.fake.AnswerJSON(opnsense.FirewallLog, []any{legs[1].row(), legs[0].row()})
	if err := early.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, record := range early.records(t) {
		if record.joinState != store.JoinPending {
			t.Errorf("a record was decided %s before the filter log was read past its start plus the tolerance",
				record.joinState)
		}
	}
}
