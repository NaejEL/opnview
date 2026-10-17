package derive

import (
	"net/netip"

	"github.com/NaejEL/opnview/internal/ianaprotocol"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The join of NJ4 (the analysis's "Rule B"), decided for one group of records -- one unordered tuple --
// from stored rows only. Definitions: dev(i) is the interface a record's mapped index i names; L is the
// loopback's index (the local_delivery role); a leg is a `flow` row; a second leg is a leg whose
// pairing outcome is second_leg, and is never a candidate.
//
//  1. ingress_interface = 0 (firewall-originated): forward to the `out` leg on dev(egress), or in
//     reverse to the `in` leg on dev(egress).
//  2. ingress_interface != 0: (a) forward to the `in` leg on dev(ingress); (b) in reverse to the `in`
//     leg on dev(egress); (c) if egress_interface = L, in reverse to the `out` leg on dev(ingress).
//  3. The candidates of every case are pooled and PS19's time, ambiguity, SYN and continuity rules choose.
//  4. A reverse join under rule 1 or 2(b) needs a forward record joined to the same leg whose
//     egress_interface is this record's ingress_interface, 0 and L counted equal; until one exists the
//     record stays pending, up to the leg plus the active timeout plus the tolerance.
//  5. A record joining nothing, with ingress_interface != 0 and egress_interface != L, whose tuple is a
//     second leg's logged tuple on dev(egress) or dev(ingress), or one of whose ends is the firewall's,
//     is a translated copy.
//  6. A record whose dev(ingress) declares a parent_of is a parent copy when it would join under rule 2
//     with dev(ingress) replaced by one of them, or when rule 4 rejected it and the accepted forward
//     record's egress interface is one of them.

// joinLeg is one candidate leg as the join reads it.
type joinLeg struct {
	netflow.JoinLeg
	source, destination netip.Addr
	protocol            int64
	protocolKnown       bool
}

// candidate is one leg in one orientation; confirm says the join needs rule 4's confirmation.
type candidate struct {
	leg         *joinLeg
	orientation string
	confirm     bool
}

// legChain is what the join has decided of one leg so far: per orientation, the end of the last record
// joined, and the TCP flags of every record joined.
type legChain struct {
	lastEnd map[string]int64
	flags   int64
}

// groupRecord is one record of a group with its two ends read as addresses.
type groupRecord struct {
	netflow.GroupRecord
	source, destination netip.Addr
	parsed              bool
}

// joinOutcome is what joinOne decided of one record.
type joinOutcome struct {
	chosen *candidate
	// reason is the unmatched reason when nothing was chosen; empty when no candidate existed at all,
	// which the copy rules then examine.
	reason string
}

// decideGroup decides every record of one group, oldest first: a record starting while the filter log
// has not been read past its start plus the tolerance stays pending, and so does every later one. Rule
// 4's rejections are found by passes over the group until none is added.
func decideGroup(records []netflow.GroupRecord, legs []netflow.JoinLeg, input joinInput) []netflow.FlowRecordDecision {
	prepared := make([]*joinLeg, 0, len(legs))
	for index := range legs {
		leg := &joinLeg{JoinLeg: legs[index]}
		source, sourceErr := netip.ParseAddr(leg.SrcAddress)
		destination, destinationErr := netip.ParseAddr(leg.DstAddress)
		if sourceErr != nil || destinationErr != nil {
			continue
		}
		leg.source, leg.destination = source.Unmap(), destination.Unmap()
		leg.protocol, leg.protocolKnown = ianaprotocol.Number(leg.Protocol)
		prepared = append(prepared, leg)
	}
	group := make([]groupRecord, 0, len(records))
	for _, record := range records {
		read := groupRecord{GroupRecord: record}
		source, sourceErr := netip.ParseAddr(record.SrcAddress)
		destination, destinationErr := netip.ParseAddr(record.DstAddress)
		if sourceErr == nil && destinationErr == nil {
			read.source, read.destination, read.parsed = source.Unmap(), destination.Unmap(), true
		}
		group = append(group, read)
	}
	rejected := map[string]bool{}
	for {
		decisions, unconfirmed := decidePass(group, prepared, input, rejected)
		grown := false
		for identity := range unconfirmed {
			if !rejected[identity] {
				rejected[identity], grown = true, true
			}
		}
		if !grown {
			return decisions
		}
	}
}

// decidePass decides the group once, rule 4 applied to the records already rejected; it returns the
// decisions and every record whose reverse join found no confirming forward record in this pass.
func decidePass(group []groupRecord, legs []*joinLeg, input joinInput, rejected map[string]bool) (
	[]netflow.FlowRecordDecision, map[string]bool) {
	chains := map[int64]*legChain{}
	chainOf := func(id int64) *legChain {
		chain := chains[id]
		if chain == nil {
			chain = &legChain{lastEnd: map[string]int64{}}
			chains[id] = chain
		}
		return chain
	}
	join := func(record groupRecord, id int64, orientation string) {
		chain := chainOf(id)
		chain.lastEnd[orientation] = max(chain.lastEnd[orientation], record.EndAt)
		if record.TCPFlags != nil {
			chain.flags |= *record.TCPFlags
		}
	}
	type tentative struct {
		record  groupRecord
		outcome joinOutcome
	}
	var decided []tentative
	forwards := map[int64][]groupRecord{}
	for _, record := range group {
		if record.StartAt+input.tolerance >= input.readThrough {
			break
		}
		if input.purged && record.StartAt < input.purgedBefore && record.JoinState != netflow.JoinPending {
			// Decided before the purge passed it: never decided again, and still the chain's history.
			if record.JoinState == netflow.JoinJoined && record.JoinedFlowID != nil && record.Orientation != nil {
				join(record, *record.JoinedFlowID, *record.Orientation)
				if *record.Orientation == netflow.OrientationForward {
					forwards[*record.JoinedFlowID] = append(forwards[*record.JoinedFlowID], record)
				}
			}
			continue
		}
		outcome := joinOne(record, legs, chains, input)
		if chosen := outcome.chosen; chosen != nil && !(chosen.confirm && rejected[record.Identity]) {
			join(record, chosen.leg.FlowID, chosen.orientation)
			if chosen.orientation == netflow.OrientationForward {
				forwards[chosen.leg.FlowID] = append(forwards[chosen.leg.FlowID], record)
			}
		}
		decided = append(decided, tentative{record: record, outcome: outcome})
	}
	unconfirmed := map[string]bool{}
	decisions := make([]netflow.FlowRecordDecision, 0, len(decided))
	for _, item := range decided {
		record, chosen := item.record, item.outcome.chosen
		decision := netflow.FlowRecordDecision{Record: record.GroupRecord, DecidedAt: input.now}
		switch {
		case chosen != nil && chosen.confirm && !confirmedBy(record, forwards[chosen.leg.FlowID]):
			unconfirmed[record.Identity] = true
			decision = unconfirmedDecision(record, chosen, forwards[chosen.leg.FlowID], legs, input, decision)
		case chosen != nil:
			id, orientation := chosen.leg.FlowID, chosen.orientation
			decision.JoinState, decision.JoinedFlowID, decision.Orientation = netflow.JoinJoined, &id, &orientation
		case item.outcome.reason != "":
			decision = unmatched(decision, item.outcome.reason)
		default:
			decision = unjoinedDecision(record, legs, input, decision)
		}
		decisions = append(decisions, decision)
	}
	return decisions, unconfirmed
}

// unmatched is a decision with no leg, for a reason.
func unmatched(decision netflow.FlowRecordDecision, reason string) netflow.FlowRecordDecision {
	decision.JoinState, decision.UnmatchedReason = netflow.JoinUnmatched, &reason
	return decision
}

// copyOf is a decision classing a record a copy.
func copyOf(decision netflow.FlowRecordDecision, kind string) netflow.FlowRecordDecision {
	decision.JoinState, decision.CopyKind = netflow.JoinCopy, &kind
	return decision
}

// confirmedBy reports whether a forward record joined to the same leg confirms a reverse record (rule
// 4): its egress_interface is the reverse record's ingress_interface, 0 and L counted as equal.
func confirmedBy(reverse groupRecord, forwards []groupRecord) bool {
	for _, forward := range forwards {
		if sameIndex(forward.EgressIndex, forward.Egress, reverse.IngressIndex, reverse.Ingress) {
			return true
		}
	}
	return false
}

// sameIndex reports whether two raw indexes are one interface, or both no interface at all: index 0
// and the loopback's index are counted equal.
func sameIndex(a *int64, aMapped netflow.MappedIndex, b *int64, bMapped netflow.MappedIndex) bool {
	if a == nil || b == nil {
		return false
	}
	if *a == *b {
		return true
	}
	local := func(index int64, mapped netflow.MappedIndex) bool {
		return index == 0 || mapped.Role == netflow.RoleLocalDelivery
	}
	return local(*a, aMapped) && local(*b, bMapped)
}

// unconfirmedDecision decides a record whose reverse join rule 4 did not confirm: a parent copy by
// rule 6(ii) when the accepted forward record left by an interface its ingress interface is the parent
// of; pending until the leg's export bound; past it a parent copy by rule 6(i), or else
// reverse_unconfirmed.
func unconfirmedDecision(record groupRecord, chosen *candidate, forwards []groupRecord, legs []*joinLeg,
	input joinInput, decision netflow.FlowRecordDecision) netflow.FlowRecordDecision {
	parents := input.parentOf(record.Ingress.Identifier, record.StartAt)
	for _, forward := range forwards {
		if forward.Egress.Identifier != nil && contains(parents, *forward.Egress.Identifier) {
			return copyOf(decision, netflow.CopyParent)
		}
	}
	if input.readThrough <= chosen.leg.ObservedAt+input.exportBoundAt(chosen.leg.ObservedAt) {
		decision.JoinState = netflow.JoinPending
		return decision
	}
	if wouldJoinAsChild(record, legs, input) {
		return copyOf(decision, netflow.CopyParent)
	}
	return unmatched(decision, netflow.UnmatchedReverseUnconfirmed)
}

// unjoinedDecision decides a record no candidate was found for: a parent copy (rule 6(i)), a
// translated copy (rule 5), or unmatched.
func unjoinedDecision(record groupRecord, legs []*joinLeg, input joinInput,
	decision netflow.FlowRecordDecision) netflow.FlowRecordDecision {
	if wouldJoinAsChild(record, legs, input) {
		return copyOf(decision, netflow.CopyParent)
	}
	if record.Ingress.Role == netflow.RoleInterface && record.Egress.Role != netflow.RoleLocalDelivery {
		if leg, orientation := translatedOf(record, legs, input); leg != nil {
			id := leg.FlowID
			decision = copyOf(decision, netflow.CopyTranslated)
			decision.CopyFlowID, decision.Orientation = &id, &orientation
			return decision
		}
		if input.firewallHeld(record.source, record.StartAt) || input.firewallHeld(record.destination, record.StartAt) {
			return copyOf(decision, netflow.CopyTranslated)
		}
	}
	switch {
	case record.Ingress.Role == netflow.RoleUnmapped,
		record.Ingress.Role != netflow.RoleLocalDelivery && record.Egress.Role == netflow.RoleUnmapped:
		// The index this record needed names no interface the map holds.
		return unmatched(decision, netflow.UnmatchedIndexUnmapped)
	case input.purged && record.StartAt-input.timeouts.longest(record.Protocol) < input.purgedBefore:
		// Its leg may have been logged before the horizon and purged: never claimed absent.
		return unmatched(decision, netflow.UnmatchedBeyondFlowHorizon)
	}
	return unmatched(decision, netflow.UnmatchedNoLoggedLeg)
}

// joinOne decides one record: the candidate it joins, why it joins none, or that no candidate exists.
func joinOne(record groupRecord, legs []*joinLeg, chains map[int64]*legChain, input joinInput) joinOutcome {
	if !record.parsed {
		return joinOutcome{reason: netflow.UnmatchedNoLoggedLeg}
	}
	if input.purged && record.StartAt < input.purgedBefore {
		// Started before the filter log's horizon: its leg may be gone.
		return joinOutcome{reason: netflow.UnmatchedBeyondFlowHorizon}
	}
	candidates := candidatesOf(record, legs, record.StartAt+input.tolerance)
	if len(candidates) == 0 {
		return joinOutcome{}
	}
	// The latest leg wins; two at that instant are never told apart by guessing.
	latest := candidates[0]
	tie := false
	for _, other := range candidates[1:] {
		switch {
		case other.leg.ObservedAt > latest.leg.ObservedAt:
			latest, tie = other, false
		case other.leg.ObservedAt == latest.leg.ObservedAt:
			tie = true
		}
	}
	if tie {
		return joinOutcome{reason: netflow.UnmatchedAmbiguous}
	}
	// A TCP record opening a connection joins only a leg logged with its first packet.
	if record.Protocol == protocolTCP && record.TCPFlags != nil && *record.TCPFlags&tcpSYN != 0 &&
		latest.leg.ObservedAt < record.StartAt-input.tolerance {
		return joinOutcome{reason: netflow.UnmatchedNoLoggedLeg}
	}
	// Continuity: the connection's pf state must still be able to exist.
	base := latest.leg.ObservedAt
	var flags int64
	if chain := chains[latest.leg.FlowID]; chain != nil {
		if end, joined := chain.lastEnd[latest.orientation]; joined && end > base {
			base = end
		}
		flags = chain.flags
	}
	if record.StartAt-base > input.timeouts.idle(record.Protocol, flags) {
		return joinOutcome{reason: netflow.UnmatchedNoLoggedLeg}
	}
	return joinOutcome{chosen: &latest}
}

// candidatesOf pools the candidates of rules 1 and 2 (NJ4 rule 3): legs that are not second legs, of
// the record's protocol, logged at or before latestAllowed, on the device and with the pf direction a
// case names, whose tuple is the record's in that case's orientation.
func candidatesOf(record groupRecord, legs []*joinLeg, latestAllowed int64) []candidate {
	var candidates []candidate
	add := func(device *string, legDirection, orientation string, confirm bool) {
		if device == nil {
			return
		}
		for _, leg := range legs {
			if leg.Device != *device || leg.Direction != legDirection || leg.PairOutcome == "second_leg" ||
				!leg.protocolKnown || leg.protocol != record.Protocol || leg.ObservedAt > latestAllowed {
				continue
			}
			if sameTuple(record, leg, orientation) {
				candidates = append(candidates, candidate{leg: leg, orientation: orientation, confirm: confirm})
			}
		}
	}
	ingress, egress := record.Ingress, record.Egress
	switch ingress.Role {
	case netflow.RoleNoReceivingInterface:
		if egress.Role == netflow.RoleInterface {
			add(egress.Device, "out", netflow.OrientationForward, false)
			add(egress.Device, "in", netflow.OrientationReverse, true)
		}
	case netflow.RoleInterface:
		add(ingress.Device, "in", netflow.OrientationForward, false)
		switch egress.Role {
		case netflow.RoleInterface:
			add(egress.Device, "in", netflow.OrientationReverse, true)
		case netflow.RoleLocalDelivery:
			add(ingress.Device, "out", netflow.OrientationReverse, false)
		}
	}
	return candidates
}

// wouldJoinAsChild reports whether a record would join under rule 2 if its dev(ingress) were replaced by
// an interface of its parent_of (rule 6(i)): a leg logged `in` there with its tuple forward, or, when
// its egress_interface is L, a leg logged `out` there with its tuple reversed.
func wouldJoinAsChild(record groupRecord, legs []*joinLeg, input joinInput) bool {
	if !record.parsed || record.Ingress.Role != netflow.RoleInterface {
		return false
	}
	latestAllowed := record.StartAt + input.tolerance
	for _, child := range input.childrenOf(record.GroupRecord) {
		device := child
		replaced := record
		replaced.Ingress = netflow.MappedIndex{Role: netflow.RoleInterface, Device: &device}
		for _, found := range candidatesOf(replaced, legs, latestAllowed) {
			if found.leg.Device == child {
				return true
			}
		}
	}
	return false
}

// translatedOf is the second leg whose logged tuple a record's is, in either orientation, on
// dev(egress) or dev(ingress) (rule 5's first signal): the latest such leg at or before the record's
// start plus the tolerance. Two at that instant name neither.
func translatedOf(record groupRecord, legs []*joinLeg, input joinInput) (*joinLeg, string) {
	if !record.parsed {
		return nil, ""
	}
	var found *joinLeg
	var orientation string
	tie := false
	for _, leg := range legs {
		if leg.PairOutcome != "second_leg" || !leg.protocolKnown || leg.protocol != record.Protocol ||
			leg.ObservedAt > record.StartAt+input.tolerance || !onDevice(leg.Device, record.Ingress, record.Egress) {
			continue
		}
		for _, candidateOrientation := range []string{netflow.OrientationForward, netflow.OrientationReverse} {
			if !sameTuple(record, leg, candidateOrientation) {
				continue
			}
			switch {
			case found == nil || leg.ObservedAt > found.ObservedAt:
				found, orientation, tie = leg, candidateOrientation, false
			case leg.ObservedAt == found.ObservedAt && leg.FlowID != found.FlowID:
				tie = true
			}
		}
	}
	if tie {
		return nil, ""
	}
	return found, orientation
}

// onDevice reports whether a device is one a record's indexes name.
func onDevice(device string, mapped ...netflow.MappedIndex) bool {
	for _, one := range mapped {
		if one.Device != nil && *one.Device == device {
			return true
		}
	}
	return false
}

// sameTuple reports whether a record's tuple is a leg's in an orientation: forward is the leg's logged
// tuple, reverse its swap (RFC 5103). Addresses are compared as values; a record joining on its
// addresses alone ignores the ports.
func sameTuple(record groupRecord, leg *joinLeg, orientation string) bool {
	legSource, legDestination, legSourcePort, legDestinationPort := leg.source, leg.destination, leg.SrcPort, leg.DstPort
	if orientation == netflow.OrientationReverse {
		legSource, legDestination = leg.destination, leg.source
		legSourcePort, legDestinationPort = leg.DstPort, leg.SrcPort
	}
	if record.source != legSource || record.destination != legDestination {
		return false
	}
	if portless(record.Protocol, record.SrcPort, record.DstPort) {
		return true
	}
	return samePort(record.SrcPort, legSourcePort) && samePort(record.DstPort, legDestinationPort)
}

func samePort(a, b *int64) bool { return a != nil && b != nil && *a == *b }

// contains reports whether a list holds a value.
func contains(list []string, value string) bool {
	for _, one := range list {
		if one == value {
			return true
		}
	}
	return false
}
