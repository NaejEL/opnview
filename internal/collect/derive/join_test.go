package derive

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The translated-copy rule's two signals, each shown alone (NJ-AC7, "by each of the two signals
// separately"), on F2's shape: an outbound connection from an untagged interface, its upstream second
// leg logged translated, and the upstream node's post-translation copy. The fixtures run both signals
// together, because a second leg is paired only when its translated source is the firewall's
// (internal/store/flows, pair.go); this test takes the join's own inputs apart.

func text(value string) *string { return &value }

func port(value int64) *int64 { return &value }

// builtinTimeouts are pf's state timeouts as the built-in pf plugin exports them: the values the join reads
// from the active firewall_log provider in production (flowrecord.go), never restated in Go.
func builtinTimeouts(t *testing.T) stateTimeouts {
	t.Helper()
	registration, found := plugin.Builtins().Lookup("firewall_log", "pf")
	if !found {
		t.Fatal("the built-in pf plugin provides no firewall_log")
	}
	var timeouts stateTimeouts
	fields := []*int64{&timeouts.tcpFirst, &timeouts.tcpEstablished, &timeouts.tcpClosing, &timeouts.tcpClosed,
		&timeouts.udpMultiple, &timeouts.icmpFirst, &timeouts.otherMultiple}
	for index, name := range stateTimeoutNames {
		encoded, _ := json.Marshal(registration.Values[name])
		if err := json.Unmarshal(encoded, fields[index]); err != nil || *fields[index] <= 0 {
			t.Fatalf("the pf plugin exports %s as %v", name, registration.Values[name])
		}
	}
	return timeouts
}

// translatedShape is the post-translation copy of F2 and the legs a group of it reads.
func translatedShape() (netflow.GroupRecord, []netflow.JoinLeg) {
	inside, upstream := "exnic0", "exnic2"
	ingress, egress := int64(21), int64(25)
	record := netflow.GroupRecord{Identity: "copy", StartAt: 1000, EndAt: 1008, SrcAddress: "203.0.113.2",
		DstAddress: "192.0.2.82", SrcPort: port(61003), DstPort: port(443), Protocol: 6, IPVersion: 4, Octets: 800,
		Packets: 8, IngressIndex: &ingress, EgressIndex: &egress,
		Ingress:   netflow.MappedIndex{Role: netflow.RoleInterface, Identifier: text("example_if_a"), Device: &inside},
		Egress:    netflow.MappedIndex{Role: netflow.RoleInterface, Identifier: text("example_if_u"), Device: &upstream},
		JoinState: netflow.JoinPending}
	legs := []netflow.JoinLeg{
		{FlowID: 1, ObservedAt: 1000, Device: inside, SrcAddress: "198.51.100.10", DstAddress: "192.0.2.82",
			SrcPort: port(50003), DstPort: port(443), Protocol: "tcp", Direction: "in", Action: "pass", PairOutcome: "first_leg"},
		{FlowID: 2, ObservedAt: 1000, Device: upstream, SrcAddress: "203.0.113.2", DstAddress: "192.0.2.82",
			SrcPort: port(61003), DstPort: port(443), Protocol: "tcp", Direction: "out", Action: "pass", PairOutcome: "second_leg"},
	}
	return record, legs
}

func decideOne(t *testing.T, record netflow.GroupRecord, legs []netflow.JoinLeg, input joinInput) netflow.FlowRecordDecision {
	t.Helper()
	decisions := decideGroup([]netflow.GroupRecord{record}, legs, input)
	if len(decisions) != 1 {
		t.Fatalf("%d decisions for one record", len(decisions))
	}
	return decisions[0]
}

// TestATranslatedCopyIsClassifiedByEachSignalAlone is NJ-AC7's two-signal clause.
func TestATranslatedCopyIsClassifiedByEachSignalAlone(t *testing.T) {
	t.Parallel()
	record, legs := translatedShape()
	input := joinInput{readThrough: 5000, tolerance: 3, now: 5000, timeouts: builtinTimeouts(t)}

	// The second leg's logged tuple alone: no address of the copy is the firewall's.
	byLeg := decideOne(t, record, legs, input)
	if byLeg.JoinState != netflow.JoinCopy || byLeg.CopyKind == nil || *byLeg.CopyKind != netflow.CopyTranslated ||
		byLeg.CopyFlowID == nil || *byLeg.CopyFlowID != 2 || byLeg.Orientation == nil ||
		*byLeg.Orientation != netflow.OrientationForward {
		t.Errorf("by the second leg's tuple alone the copy is %+v, want a translated copy of leg 2, forward", byLeg)
	}

	// The firewall's address alone: no second leg was logged.
	input.firewall = []firewallAddress{{address: netip.MustParseAddr("203.0.113.2"), from: 0, until: 9999}}
	byAddress := decideOne(t, record, legs[:1], input)
	if byAddress.JoinState != netflow.JoinCopy || byAddress.CopyKind == nil ||
		*byAddress.CopyKind != netflow.CopyTranslated || byAddress.CopyFlowID != nil {
		t.Errorf("by the firewall's address alone the copy is %+v, want a translated copy of no leg", byAddress)
	}

	// Neither: the record is unmatched, never a copy.
	input.firewall = nil
	neither := decideOne(t, record, legs[:1], input)
	if neither.JoinState != netflow.JoinUnmatched || neither.UnmatchedReason == nil ||
		*neither.UnmatchedReason != netflow.UnmatchedNoLoggedLeg {
		t.Errorf("with neither signal the record is %+v, want no_logged_leg", neither)
	}
	// And a record the second leg's tuple matches never joins that leg (NJ4 rule 3).
	if byLeg.JoinedFlowID != nil {
		t.Errorf("the copy joined the second leg %d", *byLeg.JoinedFlowID)
	}
}
