package derive

import (
	"context"
	"net/netip"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// A logged connection's measured volume: each direction counted once (NJ5 of the amendment "the NetFlow
// join with every interface captured").
//
// A connection is a first leg with its second leg, or a single leg. Each direction has an entry (the
// interface its packets arrive by, or the firewall itself) and an exit (the interface they leave by, or
// the firewall itself), from the configuration in force at the connection's start and from its legs.
// The exporters that can see a direction are the entry's ingress and the exit's egress wherever they are
// exported; on a translated connection an exporter on the second leg's interface sees only the translated
// form, and is excluded. What remains are the counted exporters:
//   - exactly one: the direction's figure is the sum of the records joined to the first leg in that
//     orientation, in one of the approved states;
//   - none, and the second leg's interface exports the translated form: the translated copies are
//     counted, and marked counted_copy (the sole-copy rule);
//   - two: forwarding_export_unsettled, with no bytes;
//   - none otherwise: the first cause that applies, the entry side looked at before the exit side.
// Every choice is made from the configuration alone, never from the records, so a direction is never
// counted twice. A connection that started under no readable, enabled configuration holds no row.

// The kinds of a direction's entry or exit.
const (
	sideInterface = "interface"
	sideLocal     = "local"
	sideUnknown   = "unknown"
)

// side is a direction's entry or exit.
type side struct {
	kind       string
	device     string
	identifier string
}

// deriveMeasuredVolumes computes again the measured volume of the connections the derivation touched:
// the window it was asked for, the legs whose joined records changed, and every connection whose
// figure was not final yet. A computation that changes nothing writes nothing.
// It returns the starts of the connections whose figure changed, whose slots the refresh rewrites.
func (c *Deriver) deriveMeasuredVolumes(ctx context.Context, request Derivation) ([]int64, error) {
	_, providerID, active, err := c.ActiveSourceKey(ctx, KindFlowRecord)
	if err != nil || !active {
		return nil, err
	}
	timeouts, known, err := c.readStateTimeouts(ctx)
	if err != nil || !known {
		// No state timeouts, no figure: nothing is decided, and nothing marked is forgotten, until a pass
		// reads them (flowrecord.go).
		return nil, err
	}
	history, err := c.History(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if len(history.Configurations) == 0 {
		// No configuration was ever read: no connection started under a known capture.
		_, _, _, _ = c.takeDirty()
		return nil, nil
	}
	dirty, from, to, legs := c.takeDirty()
	if request.pair {
		if dirty {
			from, to = min(from, request.pairFrom), max(to, request.pairTo)
		} else {
			dirty, from, to = true, request.pairFrom, request.pairTo
		}
	}
	byID := map[int64]netflow.ConnectionLegs{}
	if dirty {
		between, err := c.Store.ConnectionsBetween(ctx, from, to)
		if err != nil {
			return nil, err
		}
		for _, connection := range between {
			byID[connection.FirstID] = connection
		}
	}
	open, err := c.Store.OpenMeasuredVolumes(ctx, c.FilterLogReadThrough())
	if err != nil {
		return nil, err
	}
	of, err := c.Store.ConnectionsOf(ctx, append(legs, open...))
	if err != nil {
		return nil, err
	}
	for _, connection := range of {
		byID[connection.FirstID] = connection
	}
	if len(byID) == 0 {
		return nil, nil
	}
	connections := make([]netflow.ConnectionLegs, 0, len(byID))
	for _, connection := range byID {
		connections = append(connections, connection)
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].FirstID < connections[j].FirstID })
	identifiers, err := c.Store.InterfaceIdentifiers(ctx)
	if err != nil {
		return nil, err
	}
	return c.writeMeasuredVolumes(ctx, providerID, history, identifiers, connections, timeouts)
}

// writeMeasuredVolumes computes and writes the measured volume of some connections.
func (c *Deriver) writeMeasuredVolumes(ctx context.Context, providerID int64, history *core.CaptureHistory,
	identifiers map[string]string, connections []netflow.ConnectionLegs, timeouts stateTimeouts) ([]int64, error) {
	measure := volumeInput{readThrough: c.FilterLogReadThrough(), tolerance: c.joinTolerance(ctx), now: c.Now(),
		timeouts: timeouts}
	var firstIDs, secondIDs []int64
	low, high := int64(-1), int64(0)
	for _, connection := range connections {
		firstIDs = append(firstIDs, connection.FirstID)
		if connection.SecondID != nil {
			secondIDs = append(secondIDs, *connection.SecondID)
		}
		if low < 0 || connection.StartAt < low {
			low = connection.StartAt
		}
		high = max(high, connection.StartAt)
	}
	var err error
	if measure.joined, err = c.Store.JoinedRecordsOf(ctx, firstIDs); err != nil {
		return nil, err
	}
	if measure.copies, err = c.Store.CopyRecordsOf(ctx, secondIDs); err != nil {
		return nil, err
	}
	// A direction's span reaches at most its last record, or the export bound after its leg.
	reach := high + measure.timeouts.tcpEstablished
	for _, records := range []map[int64][]netflow.CountedRecord{measure.joined, measure.copies} {
		for _, list := range records {
			for _, record := range list {
				reach = max(reach, record.EndAt)
			}
		}
	}
	if measure.gaps, err = c.Store.ProviderGapsBetween(ctx, providerID, low, reach); err != nil {
		return nil, err
	}
	var computed []netflow.MeasuredVolume
	var marks []netflow.CopyMarks
	for _, connection := range connections {
		configuration := history.ConfigurationAt(connection.StartAt)
		if configuration == nil || !configuration.Enabled || len(configuration.Captured()) == 0 {
			// Started under no readable, enabled configuration: no row (capture_not_known).
			if connection.SecondID != nil {
				marks = append(marks, netflow.CopyMarks{SecondLegID: *connection.SecondID})
			}
			continue
		}
		plan := planConnection(connection, identifiers)
		var mark netflow.CopyMarks
		for _, direction := range []string{netflow.OrientationForward, netflow.OrientationReverse} {
			volume := measure.direction(connection, configuration, plan, direction)
			computed = append(computed, volume)
			if volume.CountedCopy {
				if direction == netflow.OrientationForward {
					mark.Forward = true
				} else {
					mark.Reverse = true
				}
			}
		}
		if connection.SecondID != nil {
			mark.SecondLegID = *connection.SecondID
			marks = append(marks, mark)
		}
	}
	return c.Store.WriteMeasuredVolumes(ctx, firstIDs, computed, marks)
}

// connectionPlan is what a connection's legs say of its directions.
type connectionPlan struct {
	// forwardEntry and forwardExit; the reverse direction swaps them.
	forwardEntry, forwardExit side
	// translated says the second leg's logged tuple differs from the first's: an exporter on the second
	// leg's interface sees the translated form.
	translated bool
	// second is the second leg's interface, when there is one.
	second *side
	// forwardRefused says pf refused the forward packets before they could leave: a refused `out` leg on
	// the exit, or a refused `in` first leg. refusedOut says the refusal was an `out` leg's.
	forwardRefused, refusedOut bool
}

// planConnection reads the entry and exit of a connection's forward direction from its legs (NJ5's
// table): a first leg `in` on A leaves by its second leg's interface, by the firewall itself when its
// destination is the firewall's, and by an unknown interface otherwise; a single `out` leg on A is
// firewall-originated, entering from the firewall and leaving by A.
func planConnection(connection netflow.ConnectionLegs, identifiers map[string]string) connectionPlan {
	interfaceSide := func(device string) side {
		return side{kind: sideInterface, device: device, identifier: identifiers[device]}
	}
	first := connection.First
	plan := connectionPlan{}
	refused := func(action string) bool { return action == "block" || action == "reject" }
	switch {
	case first.Direction == "out" && connection.SecondID == nil:
		plan.forwardEntry, plan.forwardExit = side{kind: sideLocal}, interfaceSide(first.Device)
		plan.forwardRefused, plan.refusedOut = refused(first.Action), refused(first.Action)
	default:
		plan.forwardEntry = interfaceSide(first.Device)
		switch {
		case connection.SecondID != nil:
			second := interfaceSide(connection.Second.Device)
			plan.forwardExit, plan.second = second, &second
			plan.translated = !sameLoggedTuple(first, connection.Second)
			plan.refusedOut = refused(connection.Second.Action)
		case connection.FirstToFirewall:
			plan.forwardExit = side{kind: sideLocal}
		default:
			plan.forwardExit = side{kind: sideUnknown}
		}
		plan.forwardRefused = plan.refusedOut || refused(first.Action)
	}
	return plan
}

// sameLoggedTuple reports whether two legs logged one tuple: the same addresses, compared as values, and
// the same ports.
func sameLoggedTuple(a, b netflow.LoggedLeg) bool {
	address := func(text string) netip.Addr {
		parsed, err := netip.ParseAddr(text)
		if err != nil {
			return netip.Addr{}
		}
		return parsed.Unmap()
	}
	port := func(value *int64) int64 {
		if value == nil {
			return -1
		}
		return *value
	}
	return address(a.SrcAddress) == address(b.SrcAddress) && address(a.DstAddress) == address(b.DstAddress) &&
		port(a.SrcPort) == port(b.SrcPort) && port(a.DstPort) == port(b.DstPort)
}

// volumeInput is what a direction's measured volume reads besides its connection.
type volumeInput struct {
	readThrough int64
	tolerance   int64
	now         int64
	// timeouts are pf's state timeouts, as the active firewall_log provider exports them (flowrecord.go).
	timeouts stateTimeouts
	joined   map[int64][]netflow.CountedRecord
	copies   map[int64][]netflow.CountedRecord
	gaps     []netflow.ProviderGap
}

// direction is one direction of one connection (NJ5).
func (m volumeInput) direction(connection netflow.ConnectionLegs, configuration *netflow.CaptureConfiguration,
	plan connectionPlan, direction string) netflow.MeasuredVolume {
	volume := netflow.MeasuredVolume{ConnectionFlowID: connection.FirstID, Direction: direction,
		ConnectionStartAt: connection.StartAt, ComputedAt: m.now}
	final := func(state string) netflow.MeasuredVolume {
		volume.State, volume.Final = state, true
		return volume
	}
	entry, exit := plan.forwardEntry, plan.forwardExit
	refused := plan.forwardRefused
	if direction == netflow.OrientationReverse {
		entry, exit, refused = exit, entry, false
	}
	onSecond := func(s side) bool {
		return plan.translated && plan.second != nil && s.kind == sideInterface && s.device == plan.second.device
	}
	counted := 0
	// The entry's ingress.
	switch entry.kind {
	case sideInterface:
		if captured, found := configuration.Interface(entry.identifier); found && captured.Exports(true) && !onSecond(entry) {
			counted++
		}
	case sideUnknown:
		if anyExports(configuration, true, exit) {
			counted++
		}
	}
	// The exit's egress: nothing leaves when pf refused the forward packets.
	if !refused {
		switch exit.kind {
		case sideInterface:
			if captured, found := configuration.Interface(exit.identifier); found && captured.Exports(false) && !onSecond(exit) {
				counted++
			}
		case sideUnknown:
			if anyExports(configuration, false, entry) {
				counted++
			}
		}
	}
	switch {
	case counted >= 2:
		// Untranslated forwarding between two exported interfaces, or an unknown side another interface
		// exports: each packet may be exported once or twice, which no record says (decided question 1).
		return final(netflow.VolumeForwardingExportUnsettled)
	case counted == 1:
		return m.measured(volume, configuration, connection.StartAt, recordsIn(m.joined[connection.FirstID], direction))
	}
	// The sole-copy rule: no exporter sees the first leg's form, and the second leg's interface exports
	// the translated form of this direction.
	if plan.translated && plan.second != nil && !refused {
		if captured, found := configuration.Interface(plan.second.identifier); found &&
			captured.Exports(direction == netflow.OrientationReverse) {
			volume.CountedCopy = true
			return m.measured(volume, configuration, connection.StartAt,
				recordsIn(m.copies[*connection.SecondID], direction))
		}
	}
	return final(causeOf(configuration, entry, exit, refused && plan.refusedOut))
}

// anyExports reports whether an interface other than a direction's known side exports one side of it:
// what an unknown entry or exit could be counted by.
func anyExports(configuration *netflow.CaptureConfiguration, ingress bool, known side) bool {
	for _, captured := range configuration.Captured() {
		if known.kind == sideInterface && captured.Identifier == known.identifier {
			continue
		}
		if captured.Exports(ingress) {
			return true
		}
	}
	return false
}

// causeOf is the first cause of NJ5's table that applies to a direction no exporter counts, each row
// looked at on the entry side, then on the exit side.
func causeOf(configuration *netflow.CaptureConfiguration, entry, exit side, refusedOut bool) string {
	if refusedOut {
		return netflow.VolumeNotExportedWhenRefused
	}
	entryCause, exitCause := sideCause(configuration, entry, true), sideCause(configuration, exit, false)
	for _, state := range []string{netflow.VolumeIngressNotCaptured, netflow.VolumeParentCopiesOnly,
		netflow.VolumeInterfaceNotExported, netflow.VolumeInterfaceNotCaptured, netflow.VolumeCaptureUnknown} {
		if entryCause == state || exitCause == state {
			return state
		}
	}
	return netflow.VolumeInterfaceNotCaptured
}

// sideCause is why one side of a direction counts nothing: the entry's ingress (ingress set) or the
// exit's egress; empty when that side is the firewall itself.
func sideCause(configuration *netflow.CaptureConfiguration, s side, ingress bool) string {
	switch s.kind {
	case sideLocal:
		return ""
	case sideUnknown:
		return netflow.VolumeInterfaceNotCaptured
	}
	captured, found := configuration.Interface(s.identifier)
	switch {
	case !found && ingress && isParentOf(configuration, s.identifier):
		return netflow.VolumeParentCopiesOnly
	case !found:
		return netflow.VolumeInterfaceNotCaptured
	case ingress && !captured.Ingress && captured.Egress:
		return netflow.VolumeIngressNotCaptured
	case !ingress && !captured.Egress:
		return netflow.VolumeInterfaceNotCaptured
	case captured.Exported == netflow.ExportedNo:
		return netflow.VolumeInterfaceNotExported
	case captured.Exported == netflow.ExportedUnknown:
		return netflow.VolumeCaptureUnknown
	}
	return ""
}

// isParentOf reports whether an exported interface lists an interface in its parent_of: that
// interface's received packets are exported only as parent copies, which are never counted.
func isParentOf(configuration *netflow.CaptureConfiguration, identifier string) bool {
	if identifier == "" {
		return false
	}
	for _, captured := range configuration.Captured() {
		if captured.Exports(true) && contains(captured.ParentOf, identifier) {
			return true
		}
	}
	return false
}

// recordsIn keeps the records of one orientation.
func recordsIn(records []netflow.CountedRecord, direction string) []netflow.CountedRecord {
	var kept []netflow.CountedRecord
	for _, record := range records {
		if record.Orientation == direction {
			kept = append(kept, record)
		}
	}
	return kept
}

// measured is a direction one exporter counts, from its records, in one of the approved states:
// measured, in_progress, awaiting_export, missing or export_gap.
func (m volumeInput) measured(volume netflow.MeasuredVolume, configuration *netflow.CaptureConfiguration,
	legAt int64, records []netflow.CountedRecord) netflow.MeasuredVolume {
	var lastEnd, flags, protocol int64
	for _, record := range records {
		volume.Bytes += record.Octets
		volume.Packets += record.Packets
		volume.Records++
		lastEnd = max(lastEnd, record.EndAt)
		protocol = record.Protocol
		if record.TCPFlags != nil {
			flags |= *record.TCPFlags
		}
	}
	bound := m.timeouts.tcpEstablished + m.tolerance
	if configuration.ActiveTimeoutSeconds != nil {
		bound = *configuration.ActiveTimeoutSeconds + m.tolerance
	}
	spanEnd := legAt + bound
	if volume.Records > 0 {
		spanEnd = max(lastEnd, legAt)
	}
	// Each state but a final one says when it may next change by time alone: the read-through instant it
	// falls due at.
	settle := func(state string, due int64) netflow.MeasuredVolume {
		volume.State = state
		if m.readThrough >= due && (state == netflow.VolumeExportGap || state == netflow.VolumeMeasured ||
			state == netflow.VolumeMissing) {
			volume.Final = true
			return volume
		}
		volume.DueAt = &due
		return volume
	}
	for _, gap := range m.gaps {
		if gap.From <= spanEnd && gap.To >= legAt {
			return settle(netflow.VolumeExportGap, spanEnd+finalityMarginSeconds+1)
		}
	}
	if volume.Records > 0 {
		timeout := m.timeouts.idle(protocol, flags)
		if m.readThrough-lastEnd > timeout {
			return settle(netflow.VolumeMeasured, lastEnd+timeout+finalityMarginSeconds+1)
		}
		return settle(netflow.VolumeInProgress, lastEnd+timeout+1)
	}
	if m.readThrough <= legAt+bound {
		return settle(netflow.VolumeAwaitingExport, legAt+bound+1)
	}
	return settle(netflow.VolumeMissing, spanEnd+finalityMarginSeconds+1)
}
