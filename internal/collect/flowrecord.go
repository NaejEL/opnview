package collect

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/ianaprotocol"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
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

// netflowState is what the collector holds for the flow_record kind.
type netflowState struct {
	mutex sync.Mutex
	// readThrough is the filter log's read-through instant: every record the firewall logged before it
	// has been stored, or recorded as a gap. A flow record is decided only once it has passed the
	// record's start plus the tolerance (PS19 rule 8).
	readThrough int64
	// toleranceSeconds is netflow_join_tolerance_seconds.
	toleranceSeconds int64
	// histories caches each provider's capture configurations and stream conditions.
	histories map[int64]*captureHistory
	// dirty is the connection window and legs whose measured volume must be computed again.
	dirty     bool
	dirtyFrom int64
	dirtyTo   int64
	dirtyLegs map[int64]bool
	// notActive counts the records of a provider that is not the active one, which are not stored.
	notActive int64
	// onCaptureChange is told when a provider's configuration changed, or a read followed a stream
	// condition: the receiver then follows the exporter's sequence numbers afresh.
	onCaptureChange func(key string)
}

// captureHistory is one provider's capture configurations and stream conditions.
type captureHistory struct {
	configurations []store.CaptureConfiguration
	conditions     []store.FlowRecordCondition
}

// SetCaptureChangeObserver gives the collector what it tells when a flow_record provider's capture
// configuration changed, or a read followed a stream condition.
func (c *Collector) SetCaptureChangeObserver(observe func(key string)) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	c.netflow.onCaptureChange = observe
}

// setFilterLogReadThrough moves the filter log's read-through instant forward.
func (c *Collector) setFilterLogReadThrough(at int64) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	if at > c.netflow.readThrough {
		c.netflow.readThrough = at
	}
}

// FilterLogReadThrough is the filter log's read-through instant, zero before the first pass.
func (c *Collector) FilterLogReadThrough() int64 {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	return c.netflow.readThrough
}

// joinTolerance is netflow_join_tolerance_seconds in force.
func (c *Collector) joinTolerance() int64 {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	if c.netflow.toleranceSeconds <= 0 {
		return store.DefaultNetflowJoinToleranceSeconds
	}
	return c.netflow.toleranceSeconds
}

// markDirty asks the next derivation to compute again the connections of a window.
func (c *Collector) markDirty(from, to int64) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	if !c.netflow.dirty {
		c.netflow.dirty, c.netflow.dirtyFrom, c.netflow.dirtyTo = true, from, to
		return
	}
	c.netflow.dirtyFrom, c.netflow.dirtyTo = min(c.netflow.dirtyFrom, from), max(c.netflow.dirtyTo, to)
}

// markDirtyLegs asks the next derivation to compute again the connections of some legs.
func (c *Collector) markDirtyLegs(legs []int64) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	if c.netflow.dirtyLegs == nil {
		c.netflow.dirtyLegs = map[int64]bool{}
	}
	for _, leg := range legs {
		c.netflow.dirtyLegs[leg] = true
	}
}

// takeDirty returns and clears what is to be computed again.
func (c *Collector) takeDirty() (bool, int64, int64, []int64) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	dirty, from, to := c.netflow.dirty, c.netflow.dirtyFrom, c.netflow.dirtyTo
	legs := make([]int64, 0, len(c.netflow.dirtyLegs))
	for leg := range c.netflow.dirtyLegs {
		legs = append(legs, leg)
	}
	sort.Slice(legs, func(i, j int) bool { return legs[i] < legs[j] })
	c.netflow.dirty, c.netflow.dirtyLegs = false, nil
	return dirty, from, to, legs
}

// history returns a provider's capture history, read once and kept until it changes.
func (c *Collector) history(ctx context.Context, providerID int64) (*captureHistory, error) {
	c.netflow.mutex.Lock()
	cached := c.netflow.histories[providerID]
	c.netflow.mutex.Unlock()
	if cached != nil {
		return cached, nil
	}
	configurations, err := c.store.CaptureConfigurations(ctx, providerID)
	if err != nil {
		return nil, err
	}
	conditions, err := c.store.FlowRecordConditions(ctx, providerID)
	if err != nil {
		return nil, err
	}
	read := &captureHistory{configurations: configurations, conditions: conditions}
	c.netflow.mutex.Lock()
	if c.netflow.histories == nil {
		c.netflow.histories = map[int64]*captureHistory{}
	}
	c.netflow.histories[providerID] = read
	c.netflow.mutex.Unlock()
	return read, nil
}

// forgetHistory drops a provider's cached history.
func (c *Collector) forgetHistory(providerID int64) {
	c.netflow.mutex.Lock()
	defer c.netflow.mutex.Unlock()
	delete(c.netflow.histories, providerID)
}

// configurationAt returns the configuration in force at an instant, or nil when it is unknown.
func (h *captureHistory) configurationAt(at int64) *store.CaptureConfiguration {
	rows := h.configurations
	if len(rows) == 0 || at < rows[0].FirstReadAt {
		return nil
	}
	for index := range rows {
		row := &rows[index]
		if at >= row.FirstReadAt && at <= row.LastReadAt {
			return readableOrNil(row)
		}
		if index+1 < len(rows) && at > row.LastReadAt && at < rows[index+1].FirstReadAt {
			// Between the last read showing one configuration and the first showing another.
			return nil
		}
	}
	return readableOrNil(&rows[len(rows)-1])
}

func readableOrNil(row *store.CaptureConfiguration) *store.CaptureConfiguration {
	if !row.Readable {
		return nil
	}
	return row
}

// suspended reports whether a stream condition's suspension holds an instant: from the condition
// until the next configuration read.
func (h *captureHistory) suspended(at int64) bool {
	for _, condition := range h.conditions {
		if at >= condition.ObservedAt && (condition.ResolvedAt == nil || at < *condition.ResolvedAt) {
			return true
		}
	}
	return false
}

// mappedInterface is one interface as the index map holds it.
type mappedInterface struct {
	index  uint64
	device string
}

// indexesByIdentifier is the index map keyed by interface identifier; an identifier two indexes
// claim maps to neither.
func indexesByIdentifier(indexes map[uint64]push.Interface) map[string]mappedInterface {
	byIdentifier := map[string]mappedInterface{}
	claimed := map[string]int{}
	for index, mapped := range indexes {
		if mapped.Identifier == "" {
			continue
		}
		claimed[mapped.Identifier]++
		byIdentifier[mapped.Identifier] = mappedInterface{index: index, device: mapped.Device}
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
	// state is store.CaptureObservationPoint or one of PS18's named states.
	state string
	// captured is how many interfaces the configuration captured on; -1 when it is unknown.
	captured int64
	// point is the one captured interface, its index and device, when there is one.
	point  store.CaptureInterface
	index  uint64
	device string
	// lossDetectable says the exporter's sequence was one counter's.
	lossDetectable bool
	// configuration is the configuration in force, nil when unknown.
	configuration *store.CaptureConfiguration
}

// stateAt evaluates the configuration in force at an instant (PS18's table).
func stateAt(history *captureHistory, at int64, indexes map[string]mappedInterface) pointState {
	configuration := history.configurationAt(at)
	if configuration == nil {
		return pointState{state: store.CaptureUnknown, captured: -1}
	}
	captured := configuration.Captured()
	result := pointState{captured: int64(len(captured)), configuration: configuration}
	switch {
	case !configuration.Enabled || len(captured) == 0:
		result.state = store.CaptureNotConfigured
		return result
	case len(captured) >= 2:
		result.state = store.CaptureObservationPointHidden
		return result
	case history.suspended(at):
		result.state = store.CaptureUnknown
		return result
	}
	result.point, result.lossDetectable = captured[0], true
	mapped, found := indexes[captured[0].Identifier]
	if !found {
		result.state = store.CaptureIndexUnmapped
		return result
	}
	result.state, result.index, result.device = store.CaptureObservationPoint, mapped.index, mapped.device
	return result
}

// directionAt is a record's direction at its Observation Point (PS19 rule 1): from the configuration
// and the record's ingress interface, never from its egress interface, which is the route lookup's
// interface rather than the one the packet left by (survey, Push sources, S6).
//   - An interface captured on egress only exports egress records.
//   - Otherwise a record whose ingress interface is the point's index was received there: ingress. A
//     packet both received and sent there is accounted once, at its first pass (S5).
//   - Otherwise egress: received elsewhere, or originated by the firewall, whose records carry index 0.
func directionAt(point store.CaptureInterface, index uint64, ingress *int64) string {
	if point.Egress && !point.Ingress {
		return store.DirectionEgress
	}
	if ingress != nil && *ingress >= 0 && uint64(*ingress) == index {
		return store.DirectionIngress
	}
	return store.DirectionEgress
}

// portless reports whether a record joins on its two addresses alone: ICMP, ICMPv6, and any record
// with no port or ports 0 -- ICMP and non-first fragments carry ports 0 (survey, Push sources, S10).
func portless(protocol int64, source, destination *int64) bool {
	return protocol == protocolICMP || protocol == protocolICMPv6 || source == nil || destination == nil ||
		(*source == 0 && *destination == 0)
}

// tupleKey is a record's two ends, unordered, with its version and protocol: the join groups the
// records of one Observation Point by it.
func tupleKey(addressFamily, protocol int64, source, destination netip.Addr, sourcePort, destinationPort *int64) string {
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

// ReceivePushedFlowRecords stores the flow records the push entry of a flow_record provider delivered,
// each with the Observation Point and the direction the configuration in force at its start gives it
// (PS17, *Core-written columns*). Records of a provider that is not the active one are counted and
// not stored.
func (c *Collector) ReceivePushedFlowRecords(ctx context.Context, key string, records []flowrecord.Record) error {
	activeKey, providerID, active, err := c.activeSourceKey(ctx, KindFlowRecord)
	if err != nil {
		return err
	}
	if !active || activeKey != key {
		c.netflow.mutex.Lock()
		c.netflow.notActive += int64(len(records))
		c.netflow.mutex.Unlock()
		return nil
	}
	history, err := c.history(ctx, providerID)
	if err != nil {
		return err
	}
	indexes, err := c.InterfaceIndexes(ctx)
	if err != nil {
		// An index map that cannot be read maps nothing: a one-interface configuration's records are
		// then interface_index_unmapped, never given a guessed point.
		indexes = nil
	}
	byIdentifier := indexesByIdentifier(indexes)
	now := c.now()
	rows := make([]store.FlowRecordRow, 0, len(records))
	for _, record := range records {
		row, ok := flowRecordRow(providerID, record, history, byIdentifier)
		if !ok {
			continue
		}
		if row.ReceivedAt == 0 {
			row.ReceivedAt = now
		}
		rows = append(rows, row)
	}
	_, err = c.store.InsertFlowRecords(ctx, rows)
	return err
}

// flowRecordRow builds a record's row, its core-written columns included.
func flowRecordRow(providerID int64, record flowrecord.Record, history *captureHistory,
	indexes map[string]mappedInterface) (store.FlowRecordRow, bool) {
	source, sourceErr := netip.ParseAddr(record.SrcAddress)
	destination, destinationErr := netip.ParseAddr(record.DstAddress)
	if sourceErr != nil || destinationErr != nil || record.StartMillis < 0 || record.EndMillis < record.StartMillis {
		return store.FlowRecordRow{}, false
	}
	source, destination = source.Unmap(), destination.Unmap()
	start := record.StartMillis / 1000
	row := store.FlowRecordRow{ProviderID: providerID, Identity: record.Identity, StartAt: start,
		StartMillisecond: record.StartMillis % 1000, EndAt: record.EndMillis / 1000,
		EndMillisecond: record.EndMillis % 1000, ReceivedAt: record.ReceivedAt,
		ExporterAddress: record.ExporterAddress, ObservationDomain: record.ObservationDomain,
		IngressInterface: record.IngressInterface, EgressInterface: record.EgressInterface,
		IPVersion: record.IPVersion, Protocol: record.Protocol, SrcAddress: source.String(),
		DstAddress: destination.String(), SrcPort: record.SrcPort, DstPort: record.DstPort,
		TCPFlags: record.TCPFlags, Octets: record.Octets, Packets: record.Packets,
		TupleKey: tupleKey(record.IPVersion, record.Protocol, source, destination, record.SrcPort, record.DstPort)}
	point := stateAt(history, start, indexes)
	row.CaptureState, row.LossDetectable = point.state, point.lossDetectable
	if point.captured >= 0 {
		captured := point.captured
		row.CapturedInterfaces = &captured
	}
	switch point.state {
	case store.CaptureObservationPoint:
		identifier, device := point.point.Identifier, point.device
		direction := directionAt(point.point, point.index, record.IngressInterface)
		row.ObservationPointIdentifier, row.ObservationPointDevice, row.Direction = &identifier, &device, &direction
		row.JoinState = store.JoinPending
	case store.CaptureIndexUnmapped:
		identifier := point.point.Identifier
		row.ObservationPointIdentifier = &identifier
		row.JoinState = store.JoinNotJoinable
	default:
		row.JoinState = store.JoinNotJoinable
	}
	return row, true
}

// ObserveFlowRecordCondition records a condition the decoder detected on a provider's stream. A
// sequence that interleaves while the configuration in force names one captured interface
// contradicts it: the join is suspended from that instant until the next configuration read, and
// the records already stored and not decided lose their Observation Point (PS18). Nothing is deduced
// from records.
func (c *Collector) ObserveFlowRecordCondition(ctx context.Context, key string, condition flowrecord.Condition) error {
	providerID, err := c.store.ProviderID(ctx, KindFlowRecord, key)
	if err != nil {
		return err
	}
	if err := c.store.RecordFlowRecordCondition(ctx, providerID, condition.Reason, condition.Scope,
		condition.At); err != nil {
		return err
	}
	c.forgetHistory(providerID)
	_, err = c.store.SuspendFlowRecords(ctx, providerID, condition.At)
	if err == nil {
		c.markDirty(condition.At, c.now())
	}
	return err
}

// FlowRecordSequenceAsserted says whether a loss the exporter's sequence numbers show over [from, to]
// is asserted: only when the configuration in force at both ends names exactly one captured
// interface, unsuspended. With two or more, the per-interface counters interleave under one domain and
// no gap is asserted, whatever the decoder saw (PS18, *Loss detectability*).
func (c *Collector) FlowRecordSequenceAsserted(ctx context.Context, key string, from, to int64) bool {
	providerID, err := c.store.ProviderID(ctx, KindFlowRecord, key)
	if err != nil {
		return false
	}
	history, err := c.history(ctx, providerID)
	if err != nil {
		return false
	}
	for _, at := range []int64{from, to} {
		configuration := history.configurationAt(at)
		if configuration == nil || !configuration.Enabled || len(configuration.Captured()) != 1 ||
			history.suspended(at) {
			return false
		}
	}
	return history.configurationAt(from) == history.configurationAt(to)
}

// CollectFlowRecordCapture reads the active flow_record provider's capture configuration and records
// it (PS18). A read that fails is recorded as an unreadable configuration with what the broker
// answered: the interval is capture_configuration_unknown, never an empty configuration.
func (c *Collector) CollectFlowRecordCapture(ctx context.Context) error {
	key, providerID, active, err := c.activeSourceKey(ctx, KindFlowRecord)
	if err != nil || !active {
		return err
	}
	source, registered := c.sources.FlowRecord.Lookup(key)
	if !registered {
		return fmt.Errorf("collect: the active flow_record provider %q has no implementation", key)
	}
	readAt := c.now()
	configuration, result, readErr := source.Capture(ctx, c)
	if err := c.writeAvailability(ctx, providerID, result.State, availabilityOutcome(result.Outcome),
		result.Probe, result.Detail); err != nil {
		return err
	}
	read := store.CaptureConfiguration{Readable: readErr == nil, Detail: result.Detail}
	if readErr == nil {
		read.Enabled, read.ExportVersion = configuration.Enabled, configuration.ExportVersion
		read.ActiveTimeoutSeconds = configuration.ActiveTimeoutSeconds
		read.InactiveTimeoutSeconds = configuration.InactiveTimeoutSeconds
		for _, captured := range configuration.Interfaces {
			read.Interfaces = append(read.Interfaces, store.CaptureInterface{Identifier: captured.Identifier,
				Ingress: captured.Ingress, Egress: captured.Egress})
		}
	} else {
		read.Outcome = availabilityOutcome(result.Outcome)
		if read.Detail == "" {
			read.Detail = readErr.Error()
		}
	}
	previous, err := c.history(ctx, providerID)
	if err != nil {
		return err
	}
	recorded, err := c.store.RecordCaptureRead(ctx, providerID, readAt, read)
	if err != nil {
		return err
	}
	if !recorded.Opened && recorded.ResolvedConditions == 0 {
		// The same configuration again: the interval it is in force over grew, which only the cached
		// history needs to know.
		c.forgetHistory(providerID)
		return nil
	}
	c.forgetHistory(providerID)
	from := readAt
	if rows := previous.configurations; len(rows) > 0 {
		from = rows[len(rows)-1].LastReadAt
	}
	c.markDirty(from, readAt)
	c.netflow.mutex.Lock()
	observe := c.netflow.onCaptureChange
	c.netflow.mutex.Unlock()
	if observe != nil {
		observe(key)
	}
	return c.derive(ctx, derivation{})
}

// availabilityOutcome is an outcome, or result_failed for a read that failed before one was named.
func availabilityOutcome(outcome store.AvailabilityOutcome) store.AvailabilityOutcome {
	if outcome == "" {
		return store.OutcomeResultFailed
	}
	return outcome
}

// joinLeg is one candidate leg as the join reads it.
type joinLeg struct {
	store.JoinLeg
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
		return (direction == store.DirectionIngress) == (orientation == store.OrientationForward)
	case "out":
		return (direction == store.DirectionEgress) == (orientation == store.OrientationForward)
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
func decideGroup(records []store.GroupRecord, legs []store.JoinLeg, input joinInput) []store.FlowRecordDecision {
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
	var decisions []store.FlowRecordDecision
	for _, record := range records {
		if record.StartAt+input.tolerance >= input.readThrough {
			break
		}
		frozen := input.purged && record.StartAt < input.purgedBefore && record.JoinState != store.JoinPending
		if frozen {
			// Decided before the purge passed it: never decided again, and still the chain's history.
			if record.JoinState == store.JoinJoined && record.JoinedFlowID != nil && record.Orientation != nil {
				chain := chainOf(*record.JoinedFlowID)
				chain.lastEnd[*record.Orientation] = max(chain.lastEnd[*record.Orientation], record.EndAt)
				if record.TCPFlags != nil {
					chain.flags |= *record.TCPFlags
				}
			}
			continue
		}
		decision := store.FlowRecordDecision{Record: record, Identity: record.Identity, StartAt: record.StartAt,
			DecidedAt: input.now}
		reason, chosen := joinOne(record, prepared, chains, input)
		if chosen != nil {
			decision.JoinState = store.JoinJoined
			id, orientation := chosen.leg.FlowID, chosen.orientation
			decision.JoinedFlowID, decision.Orientation = &id, &orientation
			chain := chainOf(id)
			chain.lastEnd[orientation] = max(chain.lastEnd[orientation], record.EndAt)
			if record.TCPFlags != nil {
				chain.flags |= *record.TCPFlags
			}
		} else {
			decision.JoinState = store.JoinUnmatched
			decision.UnmatchedReason = &reason
		}
		decisions = append(decisions, decision)
	}
	return decisions
}

// joinOne decides one record: the candidate it joins, or why it joins none.
func joinOne(record store.GroupRecord, legs []*joinLeg, chains map[int64]*legChain, input joinInput) (string,
	*candidate) {
	source, sourceErr := netip.ParseAddr(record.SrcAddress)
	destination, destinationErr := netip.ParseAddr(record.DstAddress)
	if sourceErr != nil || destinationErr != nil {
		return store.UnmatchedNoLoggedLeg, nil
	}
	if input.purged && record.StartAt < input.purgedBefore {
		// Started before the filter log's horizon: its leg may be gone.
		return store.UnmatchedBeyondFlowHorizon, nil
	}
	weak := portless(record.Protocol, record.SrcPort, record.DstPort)
	latestAllowed := record.StartAt + input.tolerance
	var candidates []candidate
	for _, leg := range legs {
		if !leg.protocolKnown || leg.protocol != record.Protocol || leg.ObservedAt > latestAllowed {
			continue
		}
		for _, orientation := range []string{store.OrientationForward, store.OrientationReverse} {
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
			return store.UnmatchedBeyondFlowHorizon, nil
		}
		return store.UnmatchedNoLoggedLeg, nil
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
		return store.UnmatchedAmbiguous, nil
	}
	// A TCP record opening a connection joins only a leg logged with its first packet.
	if record.Protocol == protocolTCP && record.TCPFlags != nil && *record.TCPFlags&tcpSYN != 0 {
		if latest.leg.ObservedAt < record.StartAt-input.tolerance {
			return store.UnmatchedNoLoggedLeg, nil
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
		return store.UnmatchedNoLoggedLeg, nil
	}
	return "", &latest
}

// sameTuple reports whether a record's tuple is a leg's in an orientation: forward is the leg's logged
// tuple, reverse its swap (RFC 5103). Addresses are compared as values; a record joining on its
// addresses alone ignores the ports.
func sameTuple(record store.GroupRecord, source, destination netip.Addr, leg *joinLeg, orientation string,
	weak bool) bool {
	legSource, legDestination, legSourcePort, legDestinationPort := leg.source, leg.destination, leg.SrcPort, leg.DstPort
	if orientation == store.OrientationReverse {
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
func (c *Collector) joinFlowRecords(ctx context.Context) (int64, error) {
	_, providerID, active, err := c.activeSourceKey(ctx, KindFlowRecord)
	if err != nil || !active {
		return 0, err
	}
	input := joinInput{readThrough: c.FilterLogReadThrough(), tolerance: c.joinTolerance(), now: c.now()}
	if input.readThrough == 0 {
		return 0, nil
	}
	if input.purgedBefore, input.purged, err = c.store.PurgedBefore(ctx); err != nil {
		return 0, err
	}
	groups, err := c.store.PendingFlowRecordGroups(ctx, providerID, input.readThrough-input.tolerance)
	if err != nil {
		return 0, err
	}
	var changed int64
	for _, group := range groups {
		records, err := c.store.FlowRecordGroup(ctx, providerID, group)
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
		legs, err := c.store.JoinLegs(ctx, group.Device, records[0].SrcAddress, records[0].DstAddress, addressFamily,
			latest+input.tolerance)
		if err != nil {
			return changed, err
		}
		decisions := decideGroup(records, legs, input)
		previous := map[string]*int64{}
		for index := range records {
			previous[records[index].Identity] = records[index].JoinedFlowID
		}
		written, err := c.store.DecideFlowRecords(ctx, decisions)
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

// probeFlowRecord probes every implementation of the flow_record kind.
func (c *Collector) probeFlowRecord(ctx context.Context) error {
	return c.resolveKind(ctx, KindFlowRecord, probeablesOf(c.sources.FlowRecord,
		func(source flowrecord.Source) func(context.Context, provider.Session) (provider.Result, error) {
			return source.Probe
		}))
}
