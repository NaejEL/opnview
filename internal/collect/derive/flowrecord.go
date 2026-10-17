package derive

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The flow_record kind: the records an exporter sends, the capture configuration that says what is
// exported, the join of a record to the leg its own interface indexes name, and a logged connection's
// measured volume (PS17 of the NetFlow join amendment to specs/SPEC-backend-completion-plugins.md, and
// NJ3 to NJ5 of its amendment "the NetFlow join with every interface captured"; docs/data-model.md,
// "NetFlow volumes").
//
// THIS FILE READS CONTRACT FIELDS ONLY. Which endpoint a provider reads its configuration from, what
// its exporter's model calls an option, and which Information Element fills which field are the
// provider's plugin's (PS-AC47, NJ-AC17): a second exporter is a second plugin directory, and nothing
// here changes for it. The join reads a record's two mapped indexes, the legs logged on the devices they
// name, the capture configuration's parent_of, and the addresses discovery holds for the firewall; it
// names no device, interface, VLAN or address.
//
// EVERY RECORD IS JOINED FROM ITS OWN INDEXES (NJ4, the analysis's "Rule B"). No Observation Point is
// assigned to a record: OPNsense exports none. Records are never deduplicated by content; a copy -- the
// translated form of a packet a second node exported, or a VLAN's packet its parent's node exported
// again -- is stored and named, and counted only under NJ5's sole-copy rule.

// KindFlowRecord is the flow_record kind.
const KindFlowRecord = "flow_record"

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
	read := &core.CaptureHistory{Configurations: configurations}
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

// portless reports whether a record joins on its two addresses alone: ICMP, ICMPv6, and any record
// with no port or ports 0 -- ICMP and non-first fragments carry ports 0 (survey, Push sources, S10).
func portless(protocol int64, source, destination *int64) bool {
	return protocol == protocolICMP || protocol == protocolICMPv6 || source == nil || destination == nil ||
		(*source == 0 && *destination == 0)
}

// TupleKey is a record's two ends, unordered, with its version and protocol: the join groups records
// by it.
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
	if err != nil || len(groups) == 0 {
		return 0, err
	}
	if input.history, err = c.History(ctx, providerID); err != nil {
		return 0, err
	}
	addresses, err := c.Store.FirewallAddresses(ctx)
	if err != nil {
		return 0, err
	}
	input.firewall = parseFirewallAddresses(addresses)
	// The devices of the interfaces a parent_of names, for NJ4 rule 6, as discovery holds them.
	identifiers, err := c.Store.InterfaceIdentifiers(ctx)
	if err != nil {
		return 0, err
	}
	input.devices = map[string]string{}
	for device, identifier := range identifiers {
		input.devices[identifier] = device
	}
	var changed int64
	for _, group := range groups {
		written, err := c.joinGroup(ctx, providerID, group, input)
		if err != nil {
			return changed, err
		}
		changed += written
	}
	return changed, nil
}

// joinGroup decides one group, writes what changed, and marks the legs whose connections changed.
func (c *Deriver) joinGroup(ctx context.Context, providerID int64, group string, input joinInput) (int64, error) {
	records, err := c.Store.FlowRecordGroup(ctx, providerID, group)
	if err != nil || len(records) == 0 {
		return 0, err
	}
	latest := int64(0)
	devices := map[string]bool{}
	for _, record := range records {
		latest = max(latest, record.StartAt)
		for _, mapped := range []netflow.MappedIndex{record.Ingress, record.Egress} {
			if mapped.Device != nil {
				devices[*mapped.Device] = true
			}
		}
		for _, child := range input.childrenOf(record) {
			devices[child] = true
		}
	}
	addressFamily, err := strconv.ParseInt(strings.SplitN(group, "|", 2)[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("collect: the group key %q names no IP version", group)
	}
	deviceList := make([]string, 0, len(devices))
	for device := range devices {
		deviceList = append(deviceList, device)
	}
	sort.Strings(deviceList)
	legs, err := c.Store.JoinLegs(ctx, deviceList, records[0].SrcAddress, records[0].DstAddress, addressFamily,
		latest+input.tolerance)
	if err != nil {
		return 0, err
	}
	decisions := decideGroup(records, legs, input)
	written, err := c.Store.DecideFlowRecords(ctx, decisions)
	if err != nil || written == 0 {
		return written, err
	}
	var touched []int64
	for _, decision := range decisions {
		if decision.Same() {
			continue
		}
		for _, leg := range []*int64{decision.Record.JoinedFlowID, decision.Record.CopyFlowID, decision.JoinedFlowID,
			decision.CopyFlowID} {
			if leg != nil {
				touched = append(touched, *leg)
			}
		}
	}
	c.markDirtyLegs(touched)
	return written, nil
}

// joinInput is what a group's decision reads besides its records and legs.
type joinInput struct {
	readThrough  int64
	tolerance    int64
	purgedBefore int64
	purged       bool
	now          int64
	// history is the provider's capture configurations: parent_of and the active timeout in force.
	history *core.CaptureHistory
	// firewall is the addresses discovery holds, or held, for the firewall.
	firewall []firewallAddress
	// devices maps an interface identifier to its device.
	devices map[string]string
}

// firewallAddress is one address of the firewall and the span it held it over.
type firewallAddress struct {
	address     netip.Addr
	from, until int64
	current     bool
}

func parseFirewallAddresses(addresses []netflow.FirewallAddress) []firewallAddress {
	var parsed []firewallAddress
	for _, held := range addresses {
		address, err := netip.ParseAddr(held.Address)
		if err != nil {
			continue
		}
		parsed = append(parsed, firewallAddress{address: address.Unmap(), from: held.FirstSeenAt,
			until: held.LastSeenAt, current: held.Current})
	}
	return parsed
}

// firewallHeld reports whether an address was the firewall's at an instant, or is a loopback address.
func (input joinInput) firewallHeld(address netip.Addr, at int64) bool {
	if address.IsLoopback() {
		return true
	}
	for _, held := range input.firewall {
		if held.address == address && at >= held.from && (at <= held.until || held.current) {
			return true
		}
	}
	return false
}

// parentOf is the parent_of of an interface in the configuration in force at an instant.
func (input joinInput) parentOf(identifier *string, at int64) []string {
	if identifier == nil || input.history == nil {
		return nil
	}
	configuration := input.history.ConfigurationAt(at)
	if configuration == nil {
		return nil
	}
	captured, found := configuration.Interface(*identifier)
	if !found {
		return nil
	}
	return captured.ParentOf
}

// childrenOf are the devices of the interfaces a record's ingress interface is the parent of.
func (input joinInput) childrenOf(record netflow.GroupRecord) []string {
	var devices []string
	for _, child := range input.parentOf(record.Ingress.Identifier, record.StartAt) {
		if device, found := input.devices[child]; found {
			devices = append(devices, device)
		}
	}
	return devices
}

// exportBoundAt is how long after a leg its records may still be exported: the active timeout in force
// at the leg plus the tolerance, or pf's established TCP timeout when no timeout is known.
func (input joinInput) exportBoundAt(at int64) int64 {
	if input.history != nil {
		if configuration := input.history.ConfigurationAt(at); configuration != nil &&
			configuration.ActiveTimeoutSeconds != nil {
			return *configuration.ActiveTimeoutSeconds + input.tolerance
		}
	}
	return pfTCPEstablished + input.tolerance
}
