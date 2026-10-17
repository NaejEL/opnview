package collect_test

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The join's rules, one criterion per test (PS-AC31 to PS-AC34 of the NetFlow join amendment).

// singleLeg is one `in` leg of the inside client on the inside device, of any protocol.
func singleLeg(digest string, at int64, protocol string, number, srcPort, dstPort int, action string) collecttest.LegRow {
	return collecttest.LegRow{Digest: digest, At: at, Device: collecttest.InsideDevice, Dir: "in", Action: action, Src: collecttest.InsideClient.String(),
		Dst: collecttest.OutsideServer.String(), SrcPort: srcPort, DstPort: dstPort, Protocol: protocol, ProtocolNumber: number}
}

func reverse(protocol uint8, srcPort, dstPort uint16, flags uint8, octets uint32, start, end int64) collecttest.FlowRecord {
	return collecttest.FlowRecord{Src: collecttest.OutsideServer, Dst: collecttest.InsideClient, SrcPort: dstPort, DstPort: srcPort, Protocol: protocol,
		Flags: flags, In: collecttest.UpstreamIndex, Out: collecttest.InsideIndex, Packets: 1, Octets: octets, Start: start, End: end}
}

// volumeOf returns one direction of a connection's measured volume.
func volumeOf(t *testing.T, h *collecttest.NetflowHarness, connection int64, direction string) collecttest.VolumeRow {
	t.Helper()
	for _, volume := range h.Volumes(t) {
		if volume.Connection == connection && volume.Direction == direction {
			return volume
		}
	}
	t.Fatalf("the connection %d has no %s figure: %+v", connection, direction, h.Volumes(t))
	return collecttest.VolumeRow{}
}

// byOctets indexes the stored records by their octets, which each test keeps distinct.
func byOctets(t *testing.T, h *collecttest.NetflowHarness) map[int64]collecttest.RecordRow {
	t.Helper()
	indexed := map[int64]collecttest.RecordRow{}
	for _, record := range h.Records(t) {
		indexed[record.Octets] = record
	}
	return indexed
}

// TestSplitRecordsSumExactlyAndContinuityBoundsAConnection is PS-AC31: one TCP connection exported as
// several records each way -- split by the active timeout, and by an idle gap within pf's timeout --
// sums exactly; a later SYN record beyond the tolerance joins no old leg, and joins the new leg when
// one is logged; a UDP record after a gap longer than pf's default UDP timeout (S12) joins nothing.
func TestSplitRecordsSumExactlyAndContinuityBoundsAConnection(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	h.Deliver(t, at+1200,
		collecttest.Forward(6, 50000, 443, 0x02|0x10, 100, at, at+100),
		collecttest.Forward(6, 50000, 443, 0x10, 200, at+100, at+200),
		collecttest.Forward(6, 50000, 443, 0x10|0x01, 300, at+1000, at+1100),
		reverse(6, 50000, 443, 0x12, 1000, at, at+100),
		reverse(6, 50000, 443, 0x10|0x01, 2000, at+1000, at+1100),
		collecttest.Forward(6, 50000, 443, 0x02, 50, at+2000, at+2001),
		collecttest.Forward(17, 50001, 53, 0, 70, at, at+5),
		collecttest.Forward(17, 50001, 53, 0, 80, at+205, at+206))
	h.Logged(t, singleLeg("tcp", at, "tcp", 6, 50000, 443, "pass"), singleLeg("udp", at, "udp", 17, 50001, 53, "pass"))

	tcpLeg, udpLeg := h.FlowID(t, "tcp"), h.FlowID(t, "udp")
	records := byOctets(t, h)
	for _, octets := range []int64{100, 200, 300, 1000, 2000} {
		if record := records[octets]; record.JoinState != netflow.JoinJoined || *record.JoinedFlowID != tcpLeg {
			t.Errorf("the TCP record of %d octets is %s (%s)", octets, record.JoinState, collecttest.OrNull(record.Reason))
		}
	}
	if record := records[50]; record.JoinState != netflow.JoinUnmatched ||
		collecttest.OrNull(record.Reason) != netflow.UnmatchedNoLoggedLeg {
		t.Errorf("the later SYN record is %s (%s), want no_logged_leg", record.JoinState, collecttest.OrNull(record.Reason))
	}
	if record := records[70]; record.JoinState != netflow.JoinJoined || *record.JoinedFlowID != udpLeg {
		t.Errorf("the UDP record within the timeout is %s", record.JoinState)
	}
	if record := records[80]; record.JoinState != netflow.JoinUnmatched ||
		collecttest.OrNull(record.Reason) != netflow.UnmatchedNoLoggedLeg {
		t.Errorf("the UDP record after the timeout is %s (%s), want no_logged_leg", record.JoinState,
			collecttest.OrNull(record.Reason))
	}
	if volume := volumeOf(t, h, tcpLeg, netflow.OrientationForward); volume.Bytes != 600 || volume.Records != 3 ||
		volume.State != netflow.VolumeMeasured {
		t.Errorf("the forward figure is %+v, want 600 bytes in 3 records, measured", volume)
	}
	if volume := volumeOf(t, h, tcpLeg, netflow.OrientationReverse); volume.Bytes != 3000 || volume.Records != 2 {
		t.Errorf("the reverse figure is %+v, want 3000 bytes in 2 records", volume)
	}

	// The tuple's new connection, logged: the SYN record joins its leg.
	again := collecttest.NewNetflowHarness(t)
	again.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	again.At(collecttest.NetflowReadSecond)
	again.Deliver(t, at+2100, collecttest.Forward(6, 50000, 443, 0x02, 50, at+2000, at+2001))
	again.Logged(t, singleLeg("old", at, "tcp", 6, 50000, 443, "pass"),
		singleLeg("new", at+2000, "tcp", 6, 50000, 443, "pass"))
	if stored := again.Records(t); len(stored) != 1 || stored[0].JoinedFlowID == nil ||
		*stored[0].JoinedFlowID != again.FlowID(t, "new") {
		t.Errorf("the SYN record did not join the new leg: %+v", stored)
	}
}

// TestAnUnmatchedRecordIsCountedInNoFigure is PS-AC32: a record with no leg, a record meeting two legs
// logged in the same second, and a record that started before the filter log's horizon each carry
// their reason and reach no connection's figure; a record under a configuration of two captured
// interfaces is joined all the same.
func TestAnUnmatchedRecordIsCountedInNoFigure(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	if _, err := h.Store.DB().ExecContext(context.Background(),
		"INSERT INTO retention_purge (id, purged_before, purged_at) VALUES (1, $1, $2)", at-100, at); err != nil {
		t.Fatal(err)
	}
	h.At(collecttest.NetflowReadSecond)
	h.Deliver(t, at+60,
		collecttest.Forward(17, 40000, 123, 0, 11, at+10, at+11),
		collecttest.Forward(6, 40001, 443, 0x10, 22, at+10, at+11),
		collecttest.Forward(6, 40002, 443, 0x10, 33, at-200, at-199))
	duplicate := singleLeg("dupa", at, "tcp", 6, 40001, 443, "pass")
	twin := duplicate
	twin.Digest = "dupb"
	h.Logged(t, duplicate, twin, singleLeg("other", at, "tcp", 6, 40009, 443, "pass"))
	records := byOctets(t, h)
	want := map[int64]string{11: netflow.UnmatchedNoLoggedLeg, 22: netflow.UnmatchedAmbiguous,
		33: netflow.UnmatchedBeyondFlowHorizon}
	for octets, reason := range want {
		if record := records[octets]; record.JoinState != netflow.JoinUnmatched || collecttest.OrNull(record.Reason) != reason {
			t.Errorf("the record of %d octets is %s (%s), want unmatched for %s", octets, record.JoinState,
				collecttest.OrNull(record.Reason), reason)
		}
	}
	for _, volume := range h.Volumes(t) {
		if volume.Bytes != 0 || volume.Records != 0 {
			t.Errorf("an unmatched record reached a figure: %+v", volume)
		}
	}
	// The unmatched family holds them, keyed by their ingress and egress interfaces, protocol, version and
	// reason, at every period (NJ8); no other family counts their bytes.
	for _, period := range []string{"1h", "24h", "7d", "30d"} {
		for octets, reason := range want {
			if got := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(bytes), 0) FROM unmatched_record_aggregate_"+period+
				" WHERE reason = $1 AND ingress_interface = $2 AND egress_interface = $3", reason,
				collecttest.InsideIdentifier, collecttest.UpstreamIdentifier); got != int(octets) {
				t.Errorf("at %s the unmatched family holds %d bytes for %s, want %d", period, got, reason, octets)
			}
		}
	}
	if counted := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(measure_total_bytes(measure_parts, 'netflow')), 0) FROM volume_aggregate_1h"); counted != 0 {
		t.Errorf("the volume family counts %d measured bytes of unmatched records", counted)
	}

	// Under two captured interfaces the record is joined all the same: every record is joined from its own
	// indexes (NJ2, the amendment "the NetFlow join with every interface captured").
	two := collecttest.NewNetflowHarness(t)
	two.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier}, collecttest.Captured{Identifier: collecttest.UpstreamIdentifier})
	two.At(collecttest.NetflowReadSecond)
	two.Deliver(t, at+60, collecttest.Forward(6, 40003, 443, 0x10, 44, at, at+1))
	two.Logged(t, singleLeg("two", at, "tcp", 6, 40003, 443, "pass"))
	if stored := two.Records(t); len(stored) != 1 || stored[0].JoinState != netflow.JoinJoined ||
		*stored[0].JoinedFlowID != two.FlowID(t, "two") {
		t.Errorf("under two captured interfaces the record is %+v, want joined to its leg", stored)
	}
}

// TestAnIPv6ConnectionAndICMPJoinOnTheirAddresses is PS-AC33: an IPv6 connection crossing two interfaces,
// one captured, joins although its leg's addresses are written non-canonically; an ICMP echo leg joins
// on its addresses alone; an ICMP record meeting two ICMP legs of the same second joins neither.
func TestAnIPv6ConnectionAndICMPJoinOnTheirAddresses(t *testing.T) {
	t.Parallel()
	at := collecttest.LegInstant
	h := collecttest.NewNetflowHarness(t)
	h.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	h.At(collecttest.NetflowReadSecond)
	h.Deliver(t, at+60,
		collecttest.FlowRecord{Src: collecttest.InsideClientV6, Dst: collecttest.OutsideServerV6, SrcPort: 50100, DstPort: 443, Protocol: 6, Flags: 0x11,
			In: collecttest.InsideIndex, Out: collecttest.UpstreamIndex, Packets: 3, Octets: 600, Start: at, End: at + 2},
		collecttest.Forward(1, 0, 0, 0, 84, at+5, at+6),
		collecttest.FlowRecord{Src: collecttest.SecondInsideHost, Dst: collecttest.OutsideServer, Protocol: 1, In: collecttest.InsideIndex, Out: collecttest.UpstreamIndex,
			Packets: 1, Octets: 85, Start: at + 20, End: at + 20})
	h.Logged(t,
		collecttest.LegRow{Digest: "v6in", At: at, Device: collecttest.InsideDevice, Dir: "in", Action: "pass", Src: "2001:db8:0:0:0:0:0:10",
			Dst: "2001:DB8:FFFF:0::80", SrcPort: 50100, DstPort: 443, Protocol: "tcp", ProtocolNumber: 6, Sequence: 99},
		collecttest.LegRow{Digest: "v6out", At: at, Device: collecttest.UpstreamDevice, Dir: "out", Action: "pass", Src: "2001:db8::10",
			Dst: "2001:db8:ffff::80", SrcPort: 50100, DstPort: 443, Protocol: "tcp", ProtocolNumber: 6, Sequence: 99},
		collecttest.LegRow{Digest: "icmp", At: at + 5, Device: collecttest.InsideDevice, Dir: "in", Action: "pass", Src: collecttest.InsideClient.String(),
			Dst: collecttest.OutsideServer.String(), Protocol: "icmp", ProtocolNumber: 1},
		collecttest.LegRow{Digest: "icmpa", At: at + 20, Device: collecttest.InsideDevice, Dir: "in", Action: "pass",
			Src: collecttest.SecondInsideHost.String(), Dst: collecttest.OutsideServer.String(), Protocol: "icmp", ProtocolNumber: 1},
		collecttest.LegRow{Digest: "icmpb", At: at + 20, Device: collecttest.InsideDevice, Dir: "in", Action: "pass",
			Src: collecttest.SecondInsideHost.String(), Dst: collecttest.OutsideServer.String(), Protocol: "icmp", ProtocolNumber: 1})
	records := byOctets(t, h)
	if record := records[600]; record.JoinedFlowID == nil || *record.JoinedFlowID != h.FlowID(t, "v6in") {
		t.Errorf("the IPv6 record is %s (%s), not joined to its inside leg", record.JoinState, collecttest.OrNull(record.Reason))
	}
	if record := records[84]; record.JoinedFlowID == nil || *record.JoinedFlowID != h.FlowID(t, "icmp") {
		t.Errorf("the ICMP record is %s (%s), not joined to its echo leg", record.JoinState, collecttest.OrNull(record.Reason))
	}
	if record := records[85]; collecttest.OrNull(record.Reason) != netflow.UnmatchedAmbiguous {
		t.Errorf("the ICMP record meeting two legs is %s (%s), want ambiguous", record.JoinState,
			collecttest.OrNull(record.Reason))
	}
}
