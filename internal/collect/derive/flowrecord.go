package derive

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/ianaprotocol"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The flow_record kind: the records an exporter sends, the capture configuration that gives them an
// Observation Point, the join of a record to the leg logged at it, and a logged connection's measured
// volume (PS17-PS20 of the NetFlow join amendment to specs/SPEC-backend-completion-plugins.md;
// docs/data-model.md, "NetFlow volumes").
//
// THIS FILE READS CONTRACT FIELDS ONLY. Which endpoint a provider reads its configuration from, what
// its exporter's model calls an option, and which Information Element fills which field are the
// provider's plugin's (PS-AC47): a second exporter is a second plugin directory, and nothing here
// changes for it.
//
// THE OBSERVATION POINT IS READ, NEVER INFERRED. A record has one only when the configuration in
// force at its start names exactly one captured interface, and the discovery's index map holds that
// interface. The configuration in force at an instant is the one of the read run holding it; between
// two runs that differ, before the first, and in an unreadable run it is unknown; after the newest
// read the newest configuration is taken as in force. A record's Observation Point is fixed at
// reception and never rewritten when a later read differs.

// KindFlowRecord is the flow_record kind.
const KindFlowRecord = flowrecord.Kind

// pf's default state timeouts, in seconds (survey, Push sources, S12: pf.h's PFTM_*_VAL). The values
// in force are not readable through any endpoint, so the join's continuity uses these.
const (
	pfTCPFirst       = 120
	pfTCPEstablished = 86400
	pfTCPClosing     = 900
	pfTCPClosed      = 90
	pfUDPMultiple    = 60
	pfICMPFirst      = 20
	pfOtherMultiple  = 60
)

// The IANA protocol numbers the join treats on their own: TCP and UDP carry ports, ICMP and ICMPv6
// do not (RFC 792, RFC 4443).
const (
	protocolICMP   = 1
	protocolTCP    = 6
	protocolUDP    = 17
	protocolICMPv6 = 58
)

// The TCP flags the continuity rule reads (RFC 9293, section 3.1).
const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10
)

// finalityMarginSeconds is how long past a direction's span its state waits before it is final: the
// gaps the receiver records are merged over ten seconds and a sequence jump is confirmed within a
// minute (docs/plugins.md, "Push sources"), so a gap meeting the span is recorded within it.
const finalityMarginSeconds = 120

// SetFilterLogReadThrough moves the filter log's read-through instant forward.
func (c *Deriver) SetFilterLogReadThrough(at int64) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	if at > c.Netflow.ReadThrough {
		c.Netflow.ReadThrough = at
	}
}

// FilterLogReadThrough is the filter log's read-through instant, zero before the first pass.
func (c *Deriver) FilterLogReadThrough() int64 {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	return c.Netflow.ReadThrough
}

// joinTolerance is netflow_join_tolerance_seconds in force.
func (c *Deriver) joinTolerance() int64 {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	if c.Netflow.ToleranceSeconds <= 0 {
		return netflow.DefaultNetflowJoinToleranceSeconds
	}
	return c.Netflow.ToleranceSeconds
}

// MarkDirty asks the next derivation to compute again the connections of a window.
func (c *Deriver) MarkDirty(from, to int64) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	if !c.Netflow.Dirty {
		c.Netflow.Dirty, c.Netflow.DirtyFrom, c.Netflow.DirtyTo = true, from, to
		return
	}
	c.Netflow.DirtyFrom, c.Netflow.DirtyTo = min(c.Netflow.DirtyFrom, from), max(c.Netflow.DirtyTo, to)
}

// markDirtyLegs asks the next derivation to compute again the connections of some legs.
func (c *Deriver) markDirtyLegs(legs []int64) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	if c.Netflow.DirtyLegs == nil {
		c.Netflow.DirtyLegs = map[int64]bool{}
	}
	for _, leg := range legs {
		c.Netflow.DirtyLegs[leg] = true
	}
}

// takeDirty returns and clears what is to be computed again.
func (c *Deriver) takeDirty() (bool, int64, int64, []int64) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	dirty, from, to := c.Netflow.Dirty, c.Netflow.DirtyFrom, c.Netflow.DirtyTo
	legs := make([]int64, 0, len(c.Netflow.DirtyLegs))
	for leg := range c.Netflow.DirtyLegs {
		legs = append(legs, leg)
	}
	sort.Slice(legs, func(i, j int) bool { return legs[i] < legs[j] })
	c.Netflow.Dirty, c.Netflow.DirtyLegs = false, nil
	return dirty, from, to, legs
}

// History returns a provider's capture history, read once and kept until it changes.
func (c *Deriver) History(ctx context.Context, providerID int64) (*core.CaptureHistory, error) {
	c.Netflow.Mutex.Lock()
	cached := c.Netflow.Histories[providerID]
	c.Netflow.Mutex.Unlock()
	if cached != nil {
		return cached, nil
	}
	configurations, err := c.Store.CaptureConfigurations(ctx, providerID)
	if err != nil {
		return nil, err
	}
	conditions, err := c.Store.FlowRecordConditions(ctx, providerID)
	if err != nil {
		return nil, err
	}
	read := &core.CaptureHistory{Configurations: configurations, Conditions: conditions}
	c.Netflow.Mutex.Lock()
	if c.Netflow.Histories == nil {
		c.Netflow.Histories = map[int64]*core.CaptureHistory{}
	}
	c.Netflow.Histories[providerID] = read
	c.Netflow.Mutex.Unlock()
	return read, nil
}

// ForgetHistory drops a provider's cached history.
func (c *Deriver) ForgetHistory(providerID int64) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	delete(c.Netflow.Histories, providerID)
}

// MappedInterface is one interface as the index map holds it.
type MappedInterface struct {
	index  uint64
	device string
}

// IndexesByIdentifier is the index map keyed by interface identifier; an identifier two indexes
// claim maps to neither.
func IndexesByIdentifier(indexes map[uint64]push.Interface) map[string]MappedInterface {
	byIdentifier := map[string]MappedInterface{}
	claimed := map[string]int{}
	for index, mapped := range indexes {
		if mapped.Identifier == "" {
			continue
		}
		claimed[mapped.Identifier]++
		byIdentifier[mapped.Identifier] = MappedInterface{index: index, device: mapped.Device}
	}
	for identifier, count := range claimed {
		if count > 1 {
			delete(byIdentifier, identifier)
		}
	}
	return byIdentifier
}

// pointState is the state of the configuration in force at an instant.
type pointState struct {
	// State is store.CaptureObservationPoint or one of PS18's named states.
	State string
	// Captured is how many interfaces the configuration captured on; -1 when it is unknown.
	Captured int64
	// Point is the one captured interface, its index and device, when there is one.
	Point  netflow.CaptureInterface
	Index  uint64
	Device string
	// LossDetectable says the exporter's sequence was one counter's.
	LossDetectable bool
	// configuration is the configuration in force, nil when unknown.
	configuration *netflow.CaptureConfiguration
}

// StateAt evaluates the configuration in force at an instant (PS18's table).
// What is stored decides all of it but the index mapping (store.CaptureStateAt, which the widget
// API's measure reads too); the mapping is the collector's.
func StateAt(history *core.CaptureHistory, at int64, indexes map[string]MappedInterface) pointState {
	state, count, configuration := netflow.CaptureStateAt(history.Configurations, history.Conditions, at)
	result := pointState{State: state, Captured: count, configuration: configuration}
	if state != netflow.CaptureObservationPoint {
		return result
	}
	captured := configuration.Captured()
	result.Point, result.LossDetectable = captured[0], true
	mapped, found := indexes[captured[0].Identifier]
	if !found {
		result.State = netflow.CaptureIndexUnmapped
		return result
	}
	result.State, result.Index, result.Device = netflow.CaptureObservationPoint, mapped.index, mapped.device
	return result
}

// DirectionAt is a record's direction at its Observation Point (PS19 rule 1): from the configuration
// and the record's ingress interface, never from its egress interface, which is the route lookup's
// interface rather than the one the packet left by (survey, Push sources, S6).
//   - An interface captured on egress only exports egress records.
//   - Otherwise a record whose ingress interface is the point's index was received there: ingress. A
//     packet both received and sent there is accounted once, at its first pass (S5).
//   - Otherwise egress: received elsewhere, or originated by the firewall, whose records carry index 0.
func DirectionAt(point netflow.CaptureInterface, index uint64, ingress *int64) string {
	if point.Egress && !point.Ingress {
		return netflow.DirectionEgress
	}
	if ingress != nil && *ingress >= 0 && uint64(*ingress) == index {
		return netflow.DirectionIngress
	}
	return netflow.DirectionEgress
}

// portless reports whether a record joins on its two addresses alone: ICMP, ICMPv6, and any record
// with no port or ports 0 -- ICMP and non-first fragments carry ports 0 (survey, Push sources, S10).
func portless(protocol int64, source, destination *int64) bool {
	return protocol == protocolICMP || protocol == protocolICMPv6 || source == nil || destination == nil ||
		(*source == 0 && *destination == 0)
}

// TupleKey is a record's two ends, unordered, with its version and protocol: the join groups the
// records of one Observation Point by it.
func TupleKey(addressFamily, protocol int64, source, destination netip.Addr, sourcePort, destinationPort *int64) string {
	end := func(address netip.Addr, port *int64) string {
		if portless(protocol, sourcePort, destinationPort) {
			return address.String()
		}
		return address.String() + "#" + strconv.FormatInt(*port, 10)
	}
	low, high := end(source, sourcePort), end(destination, destinationPort)
	if destination.Less(source) || (destination == source && !portless(protocol, sourcePort, destinationPort) &&
		*destinationPort < *sourcePort) {
		low, high = high, low
	}
	return fmt.Sprintf("%d|%d|%s|%s", addressFamily, protocol, low, high)
}

// joinLeg is one candidate leg as the join reads it.
type joinLeg struct {
	netflow.JoinLeg
	source, destination netip.Addr
	protocol            int64
	protocolKnown       bool
}

// candidate is one leg in one orientation.
type candidate struct {
	leg         *joinLeg
	orientation string
}

// consistent reports whether a record's direction at its Observation Point, its orientation to a leg
// and the leg's pf direction agree (PS19 rule 2's table): a leg logged `in` on the point is a packet
// received there, so its forward packets arrive (ingress) and its replies leave (egress).
func consistent(direction, orientation, legDirection string) bool {
	switch legDirection {
	case "in":
		return (direction == netflow.DirectionIngress) == (orientation == netflow.OrientationForward)
	case "out":
		return (direction == netflow.DirectionEgress) == (orientation == netflow.OrientationForward)
	}
	return false
}

// stateTimeout is pf's default timeout bounding the idle time of a connection of a protocol, given the
// TCP flags its records carried so far (survey, S12).
func stateTimeout(protocol, flags int64) int64 {
	switch protocol {
	case protocolTCP:
		switch {
		case flags&tcpRST != 0:
			return pfTCPClosed
		case flags&tcpFIN != 0:
			return pfTCPClosing
		case flags&tcpACK == 0:
			// No packet acknowledged anything: the state never left its first packet.
			return pfTCPFirst
		}
		return pfTCPEstablished
	case protocolUDP:
		return pfUDPMultiple
	case protocolICMP, protocolICMPv6:
		return pfICMPFirst
	}
	return pfOtherMultiple
}

// longestStateTimeout is the longest a connection of a protocol can stay idle and keep its pf state:
// an established TCP connection's, for TCP.
func longestStateTimeout(protocol int64) int64 {
	if protocol == protocolTCP {
		return pfTCPEstablished
	}
	return stateTimeout(protocol, tcpACK)
}

// legChain is what the join has decided of one leg so far: per orientation, the end of the last record
// joined, and the TCP flags of every record joined.
type legChain struct {
	lastEnd map[string]int64
	flags   int64
}

// joinInput is what a group's decision reads besides its records and legs.
type joinInput struct {
	readThrough  int64
	tolerance    int64
	purgedBefore int64
	purged       bool
	now          int64
}

// decideGroup decides every record of one group from stored rows only, oldest first: a record
// starting while the filter log has not been read past its start plus the tolerance stays pending,
// and so does every later one (PS19 rules 2 to 8).
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
	chains := map[int64]*legChain{}
	chainOf := func(id int64) *legChain {
		chain := chains[id]
		if chain == nil {
			chain = &legChain{lastEnd: map[string]int64{}}
			chains[id] = chain
		}
		return chain
	}
	var decisions []netflow.FlowRecordDecision
	for _, record := range records {
		if record.StartAt+input.tolerance >= input.readThrough {
			break
		}
		frozen := input.purged && record.StartAt < input.purgedBefore && record.JoinState != netflow.JoinPending
		if frozen {
			// Decided before the purge passed it: never decided again, and still the chain's history.
			if record.JoinState == netflow.JoinJoined && record.JoinedFlowID != nil && record.Orientation != nil {
				chain := chainOf(*record.JoinedFlowID)
				chain.lastEnd[*record.Orientation] = max(chain.lastEnd[*record.Orientation], record.EndAt)
				if record.TCPFlags != nil {
					chain.flags |= *record.TCPFlags
				}
			}
			continue
		}
		decision := netflow.FlowRecordDecision{Record: record, Identity: record.Identity, StartAt: record.StartAt,
			DecidedAt: input.now}
		reason, chosen := joinOne(record, prepared, chains, input)
		if chosen != nil {
			decision.JoinState = netflow.JoinJoined
			id, orientation := chosen.leg.FlowID, chosen.orientation
			decision.JoinedFlowID, decision.Orientation = &id, &orientation
			chain := chainOf(id)
			chain.lastEnd[orientation] = max(chain.lastEnd[orientation], record.EndAt)
			if record.TCPFlags != nil {
				chain.flags |= *record.TCPFlags
			}
		} else {
			decision.JoinState = netflow.JoinUnmatched
			decision.UnmatchedReason = &reason
		}
		decisions = append(decisions, decision)
	}
	return decisions
}

// joinOne decides one record: the candidate it joins, or why it joins none.
func joinOne(record netflow.GroupRecord, legs []*joinLeg, chains map[int64]*legChain, input joinInput) (string,
	*candidate) {
	source, sourceErr := netip.ParseAddr(record.SrcAddress)
	destination, destinationErr := netip.ParseAddr(record.DstAddress)
	if sourceErr != nil || destinationErr != nil {
		return netflow.UnmatchedNoLoggedLeg, nil
	}
	if input.purged && record.StartAt < input.purgedBefore {
		// Started before the filter log's horizon: its leg may be gone.
		return netflow.UnmatchedBeyondFlowHorizon, nil
	}
	weak := portless(record.Protocol, record.SrcPort, record.DstPort)
	latestAllowed := record.StartAt + input.tolerance
	var candidates []candidate
	for _, leg := range legs {
		if !leg.protocolKnown || leg.protocol != record.Protocol || leg.ObservedAt > latestAllowed {
			continue
		}
		for _, orientation := range []string{netflow.OrientationForward, netflow.OrientationReverse} {
			if !sameTuple(record, source, destination, leg, orientation, weak) {
				continue
			}
			if consistent(record.Direction, orientation, leg.Direction) {
				candidates = append(candidates, candidate{leg: leg, orientation: orientation})
			}
		}
	}
	if len(candidates) == 0 {
		if input.purged && record.StartAt-longestStateTimeout(record.Protocol) < input.purgedBefore {
			// Its leg may have been logged before the horizon and purged: never claimed absent.
			return netflow.UnmatchedBeyondFlowHorizon, nil
		}
		return netflow.UnmatchedNoLoggedLeg, nil
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
		return netflow.UnmatchedAmbiguous, nil
	}
	// A TCP record opening a connection joins only a leg logged with its first packet.
	if record.Protocol == protocolTCP && record.TCPFlags != nil && *record.TCPFlags&tcpSYN != 0 {
		if latest.leg.ObservedAt < record.StartAt-input.tolerance {
			return netflow.UnmatchedNoLoggedLeg, nil
		}
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
	if record.StartAt-base > stateTimeout(record.Protocol, flags) {
		return netflow.UnmatchedNoLoggedLeg, nil
	}
	return "", &latest
}

// sameTuple reports whether a record's tuple is a leg's in an orientation: forward is the leg's logged
// tuple, reverse its swap (RFC 5103). Addresses are compared as values; a record joining on its
// addresses alone ignores the ports.
func sameTuple(record netflow.GroupRecord, source, destination netip.Addr, leg *joinLeg, orientation string,
	weak bool) bool {
	legSource, legDestination, legSourcePort, legDestinationPort := leg.source, leg.destination, leg.SrcPort, leg.DstPort
	if orientation == netflow.OrientationReverse {
		legSource, legDestination = leg.destination, leg.source
		legSourcePort, legDestinationPort = leg.DstPort, leg.SrcPort
	}
	if source != legSource || destination != legDestination {
		return false
	}
	if weak {
		return true
	}
	return samePort(record.SrcPort, legSourcePort) && samePort(record.DstPort, legDestinationPort)
}

func samePort(a, b *int64) bool { return a != nil && b != nil && *a == *b }

// joinFlowRecords decides the records of the active provider the filter log has been read past, a
// group at a time, and marks the connections whose legs gained or lost a record. A second derivation
// over the same rows writes nothing.
func (c *Deriver) joinFlowRecords(ctx context.Context) (int64, error) {
	_, providerID, active, err := c.ActiveSourceKey(ctx, KindFlowRecord)
	if err != nil || !active {
		return 0, err
	}
	input := joinInput{readThrough: c.FilterLogReadThrough(), tolerance: c.joinTolerance(), now: c.Now()}
	if input.readThrough == 0 {
		return 0, nil
	}
	if input.purgedBefore, input.purged, err = c.Store.PurgedBefore(ctx); err != nil {
		return 0, err
	}
	groups, err := c.Store.PendingFlowRecordGroups(ctx, providerID, input.readThrough-input.tolerance)
	if err != nil {
		return 0, err
	}
	var changed int64
	for _, group := range groups {
		records, err := c.Store.FlowRecordGroup(ctx, providerID, group)
		if err != nil {
			return changed, err
		}
		if len(records) == 0 {
			continue
		}
		latest := int64(0)
		for _, record := range records {
			latest = max(latest, record.StartAt)
		}
		addressFamily, err := strconv.ParseInt(strings.SplitN(group.TupleKey, "|", 2)[0], 10, 64)
		if err != nil {
			return changed, fmt.Errorf("collect: the group key %q names no IP version", group.TupleKey)
		}
		legs, err := c.Store.JoinLegs(ctx, group.Device, records[0].SrcAddress, records[0].DstAddress, addressFamily,
			latest+input.tolerance)
		if err != nil {
			return changed, err
		}
		decisions := decideGroup(records, legs, input)
		previous := map[string]*int64{}
		for index := range records {
			previous[records[index].Identity] = records[index].JoinedFlowID
		}
		written, err := c.Store.DecideFlowRecords(ctx, decisions)
		if err != nil {
			return changed, err
		}
		changed += written
		if written == 0 {
			continue
		}
		var touched []int64
		for _, decision := range decisions {
			if before := previous[decision.Identity]; before != nil {
				touched = append(touched, *before)
			}
			if decision.JoinedFlowID != nil {
				touched = append(touched, *decision.JoinedFlowID)
			}
		}
		c.markDirtyLegs(touched)
	}
	return changed, nil
}
