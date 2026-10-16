package flowrecords

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// SetCaptureChangeObserver gives the collector what it tells when a flow_record provider's capture
// configuration changed, or a read followed a stream condition.
func (c *FlowRecordCoordinator) SetCaptureChangeObserver(observe func(key string)) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	c.Netflow.OnCaptureChange = observe
}

// ReceivePushedFlowRecords stores the flow records the push entry of a flow_record provider delivered,
// each with the Observation Point and the direction the configuration in force at its start gives it
// (PS17, *Core-written columns*). Records of a provider that is not the active one are counted and
// not stored.
func (c *FlowRecordCoordinator) ReceivePushedFlowRecords(ctx context.Context, key string, records []flowrecord.Record) error {
	activeKey, providerID, active, err := c.ActiveSourceKey(ctx, derive.KindFlowRecord)
	if err != nil {
		return err
	}
	if !active || activeKey != key {
		c.Netflow.Mutex.Lock()
		c.Netflow.NotActive += int64(len(records))
		c.Netflow.Mutex.Unlock()
		return nil
	}
	history, err := derive.On(c.Collector).History(ctx, providerID)
	if err != nil {
		return err
	}
	indexes, err := derive.On(c.Collector).InterfaceIndexes(ctx)
	if err != nil {
		// An index map that cannot be read maps nothing: a one-interface configuration's records are
		// then interface_index_unmapped, never given a guessed point.
		indexes = nil
	}
	byIdentifier := derive.IndexesByIdentifier(indexes)
	now := c.Now()
	rows := make([]netflow.FlowRecordRow, 0, len(records))
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
	_, err = c.Store.InsertFlowRecords(ctx, rows)
	return err
}

// flowRecordRow builds a record's row, its core-written columns included.
func flowRecordRow(providerID int64, record flowrecord.Record, history *core.CaptureHistory,
	indexes map[string]derive.MappedInterface) (netflow.FlowRecordRow, bool) {
	source, sourceErr := netip.ParseAddr(record.SrcAddress)
	destination, destinationErr := netip.ParseAddr(record.DstAddress)
	if sourceErr != nil || destinationErr != nil || record.StartMillis < 0 || record.EndMillis < record.StartMillis {
		return netflow.FlowRecordRow{}, false
	}
	source, destination = source.Unmap(), destination.Unmap()
	start := record.StartMillis / 1000
	row := netflow.FlowRecordRow{ProviderID: providerID, Identity: record.Identity, StartAt: start,
		StartMillisecond: record.StartMillis % 1000, EndAt: record.EndMillis / 1000,
		EndMillisecond: record.EndMillis % 1000, ReceivedAt: record.ReceivedAt,
		ExporterAddress: record.ExporterAddress, ObservationDomain: record.ObservationDomain,
		IngressInterface: record.IngressInterface, EgressInterface: record.EgressInterface,
		IPVersion: record.IPVersion, Protocol: record.Protocol, SrcAddress: source.String(),
		DstAddress: destination.String(), SrcPort: record.SrcPort, DstPort: record.DstPort,
		TCPFlags: record.TCPFlags, Octets: record.Octets, Packets: record.Packets,
		TupleKey: derive.TupleKey(record.IPVersion, record.Protocol, source, destination, record.SrcPort, record.DstPort)}
	point := derive.StateAt(history, start, indexes)
	row.CaptureState, row.LossDetectable = point.State, point.LossDetectable
	if point.Captured >= 0 {
		captured := point.Captured
		row.CapturedInterfaces = &captured
	}
	switch point.State {
	case netflow.CaptureObservationPoint:
		identifier, device := point.Point.Identifier, point.Device
		direction := derive.DirectionAt(point.Point, point.Index, record.IngressInterface)
		row.ObservationPointIdentifier, row.ObservationPointDevice, row.Direction = &identifier, &device, &direction
		row.JoinState = netflow.JoinPending
	case netflow.CaptureIndexUnmapped:
		identifier := point.Point.Identifier
		row.ObservationPointIdentifier = &identifier
		row.JoinState = netflow.JoinNotJoinable
	default:
		row.JoinState = netflow.JoinNotJoinable
	}
	return row, true
}

// ObserveFlowRecordCondition records a condition the decoder detected on a provider's stream. A
// sequence that interleaves while the configuration in force names one captured interface
// contradicts it: the join is suspended from that instant until the next configuration read, and
// the records already stored and not decided lose their Observation Point (PS18). Nothing is deduced
// from records.
func (c *FlowRecordCoordinator) ObserveFlowRecordCondition(ctx context.Context, key string, condition flowrecord.Condition) error {
	providerID, err := c.Store.ProviderID(ctx, derive.KindFlowRecord, key)
	if err != nil {
		return err
	}
	if err := c.Store.RecordFlowRecordCondition(ctx, providerID, condition.Reason, condition.Scope,
		condition.At); err != nil {
		return err
	}
	derive.On(c.Collector).ForgetHistory(providerID)
	_, err = c.Store.SuspendFlowRecords(ctx, providerID, condition.At)
	if err == nil {
		derive.On(c.Collector).MarkDirty(condition.At, c.Now())
	}
	return err
}

// FlowRecordSequenceAsserted says whether a loss the exporter's sequence numbers show over [from, to]
// is asserted: only when the configuration in force at both ends names exactly one captured
// interface, unsuspended. With two or more, the per-interface counters interleave under one domain and
// no gap is asserted, whatever the decoder saw (PS18, *Loss detectability*).
func (c *FlowRecordCoordinator) FlowRecordSequenceAsserted(ctx context.Context, key string, from, to int64) bool {
	providerID, err := c.Store.ProviderID(ctx, derive.KindFlowRecord, key)
	if err != nil {
		return false
	}
	history, err := derive.On(c.Collector).History(ctx, providerID)
	if err != nil {
		return false
	}
	for _, at := range []int64{from, to} {
		configuration := history.ConfigurationAt(at)
		if configuration == nil || !configuration.Enabled || len(configuration.Captured()) != 1 ||
			history.Suspended(at) {
			return false
		}
	}
	return history.ConfigurationAt(from) == history.ConfigurationAt(to)
}

// CollectFlowRecordCapture reads the active flow_record provider's capture configuration and records
// it (PS18). A read that fails is recorded as an unreadable configuration with what the broker
// answered: the interval is capture_configuration_unknown, never an empty configuration.
func (c *FlowRecordCoordinator) CollectFlowRecordCapture(ctx context.Context) error {
	key, providerID, active, err := c.ActiveSourceKey(ctx, derive.KindFlowRecord)
	if err != nil || !active {
		return err
	}
	source, registered := c.SourceSet.FlowRecord.Lookup(key)
	if !registered {
		return fmt.Errorf("collect: the active flow_record provider %q has no implementation", key)
	}
	readAt := c.Now()
	configuration, result, readErr := source.Capture(ctx, c)
	if err := c.WriteAvailability(ctx, providerID, result.State, availabilityOutcome(result.Outcome),
		result.Probe, result.Detail); err != nil {
		return err
	}
	read := netflow.CaptureConfiguration{Readable: readErr == nil, Detail: result.Detail}
	if readErr == nil {
		read.Enabled, read.ExportVersion = configuration.Enabled, configuration.ExportVersion
		read.ActiveTimeoutSeconds = configuration.ActiveTimeoutSeconds
		read.InactiveTimeoutSeconds = configuration.InactiveTimeoutSeconds
		for _, captured := range configuration.Interfaces {
			read.Interfaces = append(read.Interfaces, netflow.CaptureInterface{Identifier: captured.Identifier,
				Ingress: captured.Ingress, Egress: captured.Egress})
		}
	} else {
		read.Outcome = availabilityOutcome(result.Outcome)
		if read.Detail == "" {
			read.Detail = readErr.Error()
		}
	}
	previous, err := derive.On(c.Collector).History(ctx, providerID)
	if err != nil {
		return err
	}
	recorded, err := c.Store.RecordCaptureRead(ctx, providerID, readAt, read)
	if err != nil {
		return err
	}
	if !recorded.Opened && recorded.ResolvedConditions == 0 {
		// The same configuration again: the interval it is in force over grew, which only the cached
		// history needs to know.
		derive.On(c.Collector).ForgetHistory(providerID)
		return nil
	}
	derive.On(c.Collector).ForgetHistory(providerID)
	from := readAt
	if rows := previous.Configurations; len(rows) > 0 {
		from = rows[len(rows)-1].LastReadAt
	}
	derive.On(c.Collector).MarkDirty(from, readAt)
	c.Netflow.Mutex.Lock()
	observe := c.Netflow.OnCaptureChange
	c.Netflow.Mutex.Unlock()
	if observe != nil {
		observe(key)
	}
	return derive.On(c.Collector).Derive(ctx, derive.Derivation{})
}

// availabilityOutcome is an outcome, or result_failed for a read that failed before one was named.
func availabilityOutcome(outcome vocab.AvailabilityOutcome) vocab.AvailabilityOutcome {
	if outcome == "" {
		return vocab.OutcomeResultFailed
	}
	return outcome
}

// ProbeFlowRecord probes every implementation of the flow_record kind.
func (c *FlowRecordCoordinator) ProbeFlowRecord(ctx context.Context) error {
	return c.ResolveKind(ctx, derive.KindFlowRecord, core.ProbeablesOf(c.SourceSet.FlowRecord,
		func(source flowrecord.Source) func(context.Context, provider.Session) (provider.Result, error) {
			return source.Probe
		}))
}
