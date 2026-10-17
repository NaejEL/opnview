package flowrecords

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
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

// receivePushed stores the flow records the push entry of a flow_record provider delivered,
// each with what its two indexes name under the index map in force at reception (NJ3) and whether a loss
// would have been detected over its start (NJ7). Records of a provider that is not the active one are
// counted and not stored.
func (c *FlowRecordCoordinator) receivePushed(ctx context.Context, key string, records []provider.Answer) error {
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
func flowRecordRow(providerID int64, record provider.Answer, history *core.CaptureHistory,
	indexes derive.IndexMap) (netflow.FlowRecordRow, bool) {
	source, sourceErr := netip.ParseAddr(provider.Text(record, "src_address"))
	destination, destinationErr := netip.ParseAddr(provider.Text(record, "dst_address"))
	startMillis := provider.FlowRecordMillis(provider.Number(record, "start_at"))
	endMillis := provider.FlowRecordMillis(provider.Number(record, "end_at"))
	if sourceErr != nil || destinationErr != nil || startMillis < 0 || endMillis < startMillis {
		return netflow.FlowRecordRow{}, false
	}
	source, destination = source.Unmap(), destination.Unmap()
	start := startMillis / 1000
	version, protocol := provider.Integer(record, "ip_version"), provider.Integer(record, "protocol")
	srcPort, dstPort := provider.IntegerPointer(record, "src_port"), provider.IntegerPointer(record, "dst_port")
	ingress, egress := provider.IntegerPointer(record, "ingress_interface"), provider.IntegerPointer(record, "egress_interface")
	return netflow.FlowRecordRow{ProviderID: providerID, Identity: provider.Text(record, "identity"), StartAt: start,
		StartMillisecond: startMillis % 1000, EndAt: endMillis / 1000,
		EndMillisecond: endMillis % 1000, ReceivedAt: int64(provider.Number(record, "received_at")),
		ExporterAddress: provider.Text(record, "exporter_address"),
		ObservationDomain: provider.Integer(record, "observation_domain"),
		IngressInterface: ingress, EgressInterface: egress,
		IPVersion: version, Protocol: protocol, SrcAddress: source.String(),
		DstAddress: destination.String(), SrcPort: srcPort, DstPort: dstPort,
		TCPFlags: provider.IntegerPointer(record, "tcp_flags"), Octets: provider.Integer(record, "octets"),
		Packets: provider.Integer(record, "packets"),
		Ingress: indexes.Map(ingress), Egress: indexes.Map(egress),
		LossDetectable: history.ConfigurationAt(start).LossDetectable(),
		TupleKey:       derive.TupleKey(version, protocol, source, destination, srcPort, dstPort),
	}, true
}

// observeCondition is told a condition the decoder detected on a provider's stream. Every
// condition it reports today says the exporter restarted, which can renumber interfaces: the index map
// is read afresh before the next record is mapped (NJ3). Nothing is deduced from records.
func (c *FlowRecordCoordinator) observeCondition() error {
	c.Netflow.Mutex.Lock()
	defer c.Netflow.Mutex.Unlock()
	c.Netflow.IndexMapStale = true
	return nil
}

// sequenceAsserted says whether a loss the exporter's sequence numbers show over [from, to]
// is asserted: only when one readable configuration with the export enabled is in force over both ends,
// and it bounds the exporter's sequence counters (NJ7).
func (c *FlowRecordCoordinator) sequenceAsserted(ctx context.Context, key string, from, to int64) bool {
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
	source, registered := c.SourceSet.Lookup(derive.KindFlowRecord, key)
	if !registered {
		return fmt.Errorf("collect: the active flow_record provider %q has no implementation", key)
	}
	readAt := c.Now()
	configuration, result, readErr := captureOf(ctx, c, source)
	if err := c.WriteAvailability(ctx, providerID, result.State, availabilityOutcome(result.Outcome),
		result.Probe, result.Detail); err != nil {
		return err
	}
	read := netflow.CaptureConfiguration{Readable: readErr == nil, Detail: result.Detail}
	if readErr == nil {
		read.Enabled, read.ExportVersion = configuration.Enabled, configuration.ExportVersion
		read.ActiveTimeoutSeconds = configuration.ActiveTimeoutSeconds
		read.InactiveTimeoutSeconds = configuration.InactiveTimeoutSeconds
		read.Interfaces = configuration.Interfaces
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

// Kind is the kind this coordinator collects.
const Kind = "flow_record"

func init() {
	core.RegisterPushHandler(Kind, core.PushHandler{
		Receive: func(ctx context.Context, c *core.Collector, key string, records []provider.Answer) error {
			return On(c).receivePushed(ctx, key, records)
		},
		SequenceAsserted: func(ctx context.Context, c *core.Collector, key string, from, to int64) bool {
			return On(c).sequenceAsserted(ctx, key, from, to)
		},
		Observe: func(_ context.Context, c *core.Collector, _ string, _ provider.PushCondition) error {
			return On(c).observeCondition()
		},
	})
}

// captureOf reads a provider's capture configuration: its capture answer, checked against the contract,
// and what the contract's types cannot express -- each interface named once and never its own parent, a
// not_exported_reason exactly when exported is no -- refused as records_invalid. An interface whose
// export the provider did not establish is unknown.
func captureOf(ctx context.Context, c *core.Collector, source provider.Registration) (netflow.CaptureConfiguration,
	provider.Result, error) {
	answer, result, err := source.Implementation.Call(ctx, c, "capture")
	if err != nil {
		return netflow.CaptureConfiguration{}, result, err
	}
	invalid := func(detail string) (netflow.CaptureConfiguration, provider.Result, error) {
		failed := fmt.Errorf("collect: the %s provider %s: %s", Kind, source.Key, detail)
		return netflow.CaptureConfiguration{}, provider.Result{State: vocab.StateUnavailable,
			Outcome: vocab.OutcomeRecordsInvalid, Probe: result.Probe, Detail: failed.Error()}, failed
	}
	captured, present := answer["capture"].(map[string]any)
	if !present {
		return invalid("capture returned neither a capture nor an error")
	}
	configuration := netflow.CaptureConfiguration{Enabled: provider.Flag(captured, "enabled", false),
		ExportVersion:          provider.Text(captured, "export_version"),
		ActiveTimeoutSeconds:   provider.IntegerPointer(captured, "active_timeout_seconds"),
		InactiveTimeoutSeconds: provider.IntegerPointer(captured, "inactive_timeout_seconds")}
	seen := map[string]bool{}
	for index, row := range provider.Records(captured, "interfaces") {
		identifier := provider.Text(row, "identifier")
		if identifier == "" || seen[identifier] {
			return invalid(fmt.Sprintf("capture.interfaces[%d] names an empty or repeated identifier", index))
		}
		seen[identifier] = true
		captureInterface := netflow.CaptureInterface{Identifier: identifier, Ingress: provider.Flag(row, "ingress", false),
			Egress: provider.Flag(row, "egress", false), Exported: provider.Text(row, "exported"),
			NotExportedReason: provider.Text(row, "not_exported_reason"), ParentOf: provider.Texts(row, "parent_of")}
		if captureInterface.Exported == "" {
			// A provider that does not say whether the interface is exported has not established it.
			captureInterface.Exported = netflow.ExportedUnknown
		}
		if (captureInterface.Exported == netflow.ExportedNo) != (captureInterface.NotExportedReason != "") {
			return invalid(fmt.Sprintf("capture.interfaces[%d] names a not_exported_reason exactly when exported "+
				"is not \"no\"", index))
		}
		for _, of := range captureInterface.ParentOf {
			if of == "" || of == identifier {
				return invalid(fmt.Sprintf("capture.interfaces[%d] lists an empty identifier, or itself, in parent_of",
					index))
			}
		}
		configuration.Interfaces = append(configuration.Interfaces, captureInterface)
	}
	return configuration, result, nil
}
