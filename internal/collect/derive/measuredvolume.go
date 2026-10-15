package derive

import (
	"context"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// A logged connection's measured volume: each direction once, at the single Observation Point (PS20
// of the NetFlow join amendment to specs/SPEC-backend-completion-plugins.md).
//
// A connection is a first leg with its second leg, or a single leg. When the configuration in force
// at its start names one Observation Point X, its figures come from the leg logged on X -- the first
// leg when X carries it, the second when X carries only that one (X is then the upstream interface of
// a NAT connection, say) -- and each direction is the sum of that direction's records joined to the
// leg. A node accounts each packet once (survey, Push sources, S5) and only one Observation Point
// exists, so no direction is counted at two points or twice at one. A connection that started under
// any other configuration holds no row: its state is the interval's, read from the configuration.

// measuredLeg is the leg a connection's figures are measured at.
type measuredLeg struct {
	id         int64
	which      string
	observedAt int64
	direction  string
	action     string
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
	history, err := c.History(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if len(history.Configurations) == 0 {
		// No configuration was ever read: no connection started under an Observation Point.
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
	indexes, err := c.InterfaceIndexes(ctx)
	if err != nil {
		indexes = nil
	}
	return c.writeMeasuredVolumes(ctx, providerID, history, IndexesByIdentifier(indexes), connections)
}

// writeMeasuredVolumes computes and writes the measured volume of some connections.
func (c *Deriver) writeMeasuredVolumes(ctx context.Context, providerID int64, history *core.CaptureHistory,
	indexes map[string]MappedInterface, connections []netflow.ConnectionLegs) ([]int64, error) {
	readThrough, tolerance, now := c.FilterLogReadThrough(), c.joinTolerance(), c.Now()
	type planned struct {
		connection netflow.ConnectionLegs
		point      pointState
		leg        *measuredLeg
	}
	var plans []planned
	var legIDs []int64
	low, high := int64(-1), int64(0)
	for _, connection := range connections {
		point := StateAt(history, connection.StartAt, indexes)
		plan := planned{connection: connection, point: point}
		if point.State == netflow.CaptureObservationPoint {
			switch {
			case connection.FirstDevice == point.Device:
				plan.leg = &measuredLeg{id: connection.FirstID, which: netflow.MeasuredAtFirstLeg,
					observedAt: connection.StartAt, direction: connection.FirstDirection, action: connection.FirstAction}
			case connection.SecondID != nil && connection.SecondDevice == point.Device:
				plan.leg = &measuredLeg{id: *connection.SecondID, which: netflow.MeasuredAtSecondLeg,
					observedAt: connection.StartAt, direction: connection.SecondDirection,
					action: connection.SecondAction}
			}
			if plan.leg != nil {
				legIDs = append(legIDs, plan.leg.id)
			}
			if low < 0 || connection.StartAt < low {
				low = connection.StartAt
			}
			high = max(high, connection.StartAt)
		}
		plans = append(plans, plan)
	}
	joined, err := c.Store.JoinedRecordsOf(ctx, legIDs)
	if err != nil {
		return nil, err
	}
	var gaps []netflow.ProviderGap
	if low >= 0 {
		// A direction's span reaches at most its last record, or the export bound after its leg.
		reach := high + pfTCPEstablished
		for _, records := range joined {
			for _, record := range records {
				reach = max(reach, record.EndAt)
			}
		}
		if gaps, err = c.Store.ProviderGapsBetween(ctx, providerID, low, reach); err != nil {
			return nil, err
		}
	}
	ids := make([]int64, 0, len(plans))
	var computed []netflow.MeasuredVolume
	for _, plan := range plans {
		ids = append(ids, plan.connection.FirstID)
		if plan.point.State != netflow.CaptureObservationPoint {
			continue
		}
		for _, direction := range []string{netflow.OrientationForward, netflow.OrientationReverse} {
			computed = append(computed, measureDirection(plan.connection, plan.point, plan.leg, direction,
				joined, gaps, readThrough, tolerance, now))
		}
	}
	return c.Store.WriteMeasuredVolumes(ctx, ids, computed)
}

// measureDirection is one direction of one connection (PS20's state table).
func measureDirection(connection netflow.ConnectionLegs, point pointState, leg *measuredLeg, direction string,
	joined map[int64][]netflow.JoinedRecord, gaps []netflow.ProviderGap, readThrough, tolerance,
	now int64) netflow.MeasuredVolume {
	volume := netflow.MeasuredVolume{ConnectionFlowID: connection.FirstID, Direction: direction,
		ConnectionStartAt: connection.StartAt, ObservationPointIdentifier: point.Point.Identifier,
		ObservationPointDevice: point.Device, ComputedAt: now}
	if leg == nil {
		// No leg of the connection was logged on the Observation Point.
		volume.State, volume.Final = netflow.VolumeNotCaptured, true
		return volume
	}
	which, id := leg.which, leg.id
	volume.MeasuredAtLeg, volume.MeasuredFlowID = &which, &id
	atPoint := directionAtLeg(leg.direction, direction)
	switch {
	case atPoint == "" || !captures(point.Point, atPoint):
		volume.State, volume.Final = netflow.VolumeNotCaptured, true
		return volume
	case refused(leg.action) && leg.direction == "out" && direction == netflow.OrientationForward:
		// pf refused the packets on the way out, before the egress capture (S11): nothing was exported.
		volume.State, volume.Final = netflow.VolumeNotExportedWhenRefused, true
		return volume
	}
	var lastEnd, flags, protocol int64
	for _, record := range joined[leg.id] {
		if record.Orientation != direction {
			continue
		}
		volume.Bytes += record.Octets
		volume.Packets += record.Packets
		volume.Records++
		lastEnd = max(lastEnd, record.EndAt)
		protocol = record.Protocol
		if record.TCPFlags != nil {
			flags |= *record.TCPFlags
		}
	}
	bound := exportBound(point, tolerance)
	spanEnd := leg.observedAt + bound
	if volume.Records > 0 {
		spanEnd = max(lastEnd, leg.observedAt)
	}
	// Each state but a final one says when it may next change by time alone: the read-through instant it
	// falls due at.
	settle := func(state string, due int64) netflow.MeasuredVolume {
		volume.State = state
		if readThrough >= due && (state == netflow.VolumeExportGap || state == netflow.VolumeMeasured ||
			state == netflow.VolumeMissing) {
			volume.Final = true
			return volume
		}
		volume.DueAt = &due
		return volume
	}
	for _, gap := range gaps {
		if gap.From <= spanEnd && gap.To >= leg.observedAt {
			return settle(netflow.VolumeExportGap, spanEnd+finalityMarginSeconds+1)
		}
	}
	if volume.Records > 0 {
		timeout := stateTimeout(protocol, flags)
		if readThrough-lastEnd > timeout {
			return settle(netflow.VolumeMeasured, lastEnd+timeout+finalityMarginSeconds+1)
		}
		return settle(netflow.VolumeInProgress, lastEnd+timeout+1)
	}
	if readThrough <= leg.observedAt+bound {
		return settle(netflow.VolumeAwaitingExport, leg.observedAt+bound+1)
	}
	return settle(netflow.VolumeMissing, spanEnd+finalityMarginSeconds+1)
}

// directionAtLeg is a connection direction's direction at the Observation Point the leg was logged on:
// a leg logged `in` there is a packet received there, so its forward packets are ingress and its
// replies egress; a leg logged `out` the opposite. A leg whose direction pf did not name has none.
func directionAtLeg(legDirection, direction string) string {
	forward := direction == netflow.OrientationForward
	switch legDirection {
	case "in":
		if forward {
			return netflow.DirectionIngress
		}
		return netflow.DirectionEgress
	case "out":
		if forward {
			return netflow.DirectionEgress
		}
		return netflow.DirectionIngress
	}
	return ""
}

// captures reports whether an interface's configuration captures a direction.
func captures(point netflow.CaptureInterface, direction string) bool {
	if direction == netflow.DirectionIngress {
		return point.Ingress
	}
	return point.Egress
}

// refused reports whether pf refused a leg.
func refused(action string) bool { return action == "block" || action == "reject" }

// exportBound is how long after its leg a covered direction's first record may still be exported:
// the active timeout in force plus the tolerance. A configuration stating no active timeout bounds
// nothing, and the direction then waits for as long as a TCP state could live.
func exportBound(point pointState, tolerance int64) int64 {
	if point.configuration != nil && point.configuration.ActiveTimeoutSeconds != nil {
		return *point.configuration.ActiveTimeoutSeconds + tolerance
	}
	return pfTCPEstablished + tolerance
}
