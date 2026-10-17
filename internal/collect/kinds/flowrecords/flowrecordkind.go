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

// SetCaptureReadObserver gives the collector what it tells after every read of a flow_record
// provider's capture configuration: whether the read opened a new configuration, and how many sequence
// counters the configuration in force lets the exporter run (NJ7 of the amendment "the NetFlow join with
// every interface captured").
func (c *FlowRecordCoordinator) SetCaptureReadObserver(observe func(key string, changed bool, counters int)) {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	c.Netflow.OnCaptureRead = observe
}

// ReceivePushedFlowRecords stores the flow records the push entry of a flow_record provider delivered,
// each with what its two indexes name under the index map in force at reception (NJ3) and whether a loss
// would have been detected over its start (NJ7). Records of a provider that is not the active one are
// counted and not stored.
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
	indexes := c.indexMap(ctx)
	now := c.Now()
	rows := make([]netflow.FlowRecordRow, 0, len(records))
	for _, record := range records {
		row, ok := flowRecordRow(providerID, record, history, indexes)
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

// indexMap is the index map in force: the latest read of interfaces_info at or before reception,
// through the broker at discovery's max age -- read afresh after the exporter restarted, before the next
// record is mapped (NJ3). A map that cannot be read maps nothing: every index other than 0 is then
// unmapped, never guessed.
func (c *FlowRecordCoordinator) indexMap(ctx context.Context) derive.IndexMap {
	c.Netflow.Mutex.Lock()
	fresh := c.Netflow.IndexMapStale
	c.Netflow.Mutex.Unlock()
	indexes, err := derive.On(c.Collector).ReadIndexMap(ctx, fresh)
	if err != nil {
		return derive.IndexMap{}
	}
	if fresh {
		c.Netflow.Mutex.Lock()
		c.Netflow.IndexMapStale = false
		c.Netflow.Mutex.Unlock()
	}
	return indexes
}

// flowRecordRow builds a record's row, its core-written columns included.
func flowRecordRow(providerID int64, record flowrecord.Record, history *core.CaptureHistory,
	indexes derive.IndexMap) (netflow.FlowRecordRow, bool) {
	source, sourceErr := netip.ParseAddr(record.SrcAddress)
	destination, destinationErr := netip.ParseAddr(record.DstAddress)
	if sourceErr != nil || destinationErr != nil || record.StartMillis < 0 || record.EndMillis < record.StartMillis {
		return netflow.FlowRecordRow{}, false
	}
	source, destination = source.Unmap(), destination.Unmap()
	start := record.StartMillis / 1000
	return netflow.FlowRecordRow{ProviderID: providerID, Identity: record.Identity, StartAt: start,
		StartMillisecond: record.StartMillis % 1000, EndAt: record.EndMillis / 1000,
		EndMillisecond: record.EndMillis % 1000, ReceivedAt: record.ReceivedAt,
		ExporterAddress: record.ExporterAddress, ObservationDomain: record.ObservationDomain,
		IngressInterface: record.IngressInterface, EgressInterface: record.EgressInterface,
		IPVersion: record.IPVersion, Protocol: record.Protocol, SrcAddress: source.String(),
		DstAddress: destination.String(), SrcPort: record.SrcPort, DstPort: record.DstPort,
		TCPFlags: record.TCPFlags, Octets: record.Octets, Packets: record.Packets,
		Ingress: indexes.Map(record.IngressInterface), Egress: indexes.Map(record.EgressInterface),
		LossDetectable: history.ConfigurationAt(start).LossDetectable(),
		TupleKey:       derive.TupleKey(record.IPVersion, record.Protocol, source, destination, record.SrcPort, record.DstPort),
	}, true
}

// ObserveFlowRecordCondition is told a condition the decoder detected on a provider's stream. Every
// condition it reports today says the exporter restarted, which can renumber interfaces: the index map
// is read afresh before the next record is mapped (NJ3). Nothing is deduced from records.
func (c *FlowRecordCoordinator) ObserveFlowRecordCondition(_ context.Context, _ string, _ flowrecord.Condition) error {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	c.Netflow.IndexMapStale = true
	return nil
}

// FlowRecordSequenceAsserted says whether a loss the exporter's sequence numbers show over [from, to]
// is asserted: only when one readable configuration with the export enabled is in force over both ends,
// and it bounds the exporter's sequence counters (NJ7).
func (c *FlowRecordCoordinator) FlowRecordSequenceAsserted(ctx context.Context, key string, from, to int64) bool {
	providerID, err := c.Store.ProviderID(ctx, derive.KindFlowRecord, key)
	if err != nil {
		return false
	}
	history, err := derive.On(c.Collector).History(ctx, providerID)
	if err != nil {
		return false
	}
	configuration := history.ConfigurationAt(from)
	return configuration.LossDetectable() && history.ConfigurationAt(to) == configuration
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
				Ingress: captured.Ingress, Egress: captured.Egress, Exported: captured.Exported,
				NotExportedReason: captured.NotExportedReason, ParentOf: captured.ParentOf})
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
	opened, err := c.Store.RecordCaptureRead(ctx, providerID, readAt, read)
	if err != nil {
		return err
	}
	derive.On(c.Collector).ForgetHistory(providerID)
	counters := 0
	if current, err := derive.On(c.Collector).History(ctx, providerID); err == nil {
		if configuration := current.ConfigurationAt(readAt); configuration != nil && configuration.Enabled {
			counters = configuration.SequenceCounters()
		}
	}
	c.Netflow.Mutex.Lock()
	observe := c.Netflow.OnCaptureRead
	c.Netflow.Mutex.Unlock()
	if observe != nil {
		observe(key, opened, counters)
	}
	if !opened {
		// The same configuration again: the interval it is in force over grew, which only the cached
		// history needed to know.
		return nil
	}
	from := readAt
	if rows := previous.Configurations; len(rows) > 0 {
		from = rows[len(rows)-1].LastReadAt
	}
	derive.On(c.Collector).MarkDirty(from, readAt)
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
