package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/runtime"
	"github.com/NaejEL/opnview/internal/push"
)

// A push entry delivering records of a core kind (PS9 of the push amendment, as PS17 of the NetFlow
// join amendment amends it). Its records are built either by its parse function or, with no script
// at all, by its field mapping -- each contract field named after the protocol fields it is read
// from, the first present one winning -- and checked against the kind's contract (contract.go) as a
// pulled answer is. They then go to the core: a kind fed by two paths to its coordinator, which keeps
// one path per instant (internal/collect/kinds/filterlog/pushpaths.go); a push-only record to the kind's receiver.

// KindReceiver is the core's side: it takes the records a push entry delivered for one provider, and
// what the decoder detected on the stream they came by.
type KindReceiver interface {
	ReceivePushedFirewallLog(ctx context.Context, key string, records []firewalllog.Record) error
	// ReceivePushedFlowRecords stores the flow records a provider's push entry delivered.
	ReceivePushedFlowRecords(ctx context.Context, key string, records []flowrecord.Record) error
	// ObserveFlowRecordCondition is told a condition the decoder detected on a stream the provider's
	// push entry is fed by.
	ObserveFlowRecordCondition(ctx context.Context, key string, condition flowrecord.Condition) error
	// FlowRecordSequenceAsserted says whether a loss the exporter's sequence numbers show over
	// [from, to] is asserted: only where the capture configuration in force makes one sequence one
	// counter (PS18).
	FlowRecordSequenceAsserted(ctx context.Context, key string, from, to int64) bool
}

// pushKind is how one core kind receives what a push entry delivers.
type pushKind struct {
	// convert checks a batch of answers, each built from the record beside it, and returns what the
	// receiver takes, how many records that is, and how many answers were refused.
	convert func(answers []map[string]any, records []push.Record) (batch any, rows int, invalid int)
	// store hands a batch to the receiver.
	store func(ctx context.Context, receiver KindReceiver, key string, batch any) error
	// view is a batch under its contract's field names, as the plugin test world reads it.
	view func(batch any) []any
	// judge decides a loss the sender's sequence numbers show; nil asserts every one.
	judge func(ctx context.Context, receiver KindReceiver, key string, problem push.Problem) bool
	// observe is told a condition of the stream; nil ignores it.
	observe func(ctx context.Context, receiver KindReceiver, key string, problem push.Problem) error
}

// pushKinds are the core kinds a push entry may deliver. A kind absent here is refused at load.
var pushKinds = map[string]pushKind{
	firewalllog.Kind: {
		convert: firewallLogFromPush,
		store: func(ctx context.Context, receiver KindReceiver, key string, batch any) error {
			records, _ := batch.([]firewalllog.Record)
			return receiver.ReceivePushedFirewallLog(ctx, key, records)
		},
		view: func(batch any) []any {
			records, _ := batch.([]firewalllog.Record)
			viewed := make([]any, 0, len(records))
			for _, record := range records {
				viewed = append(viewed, firewallLogContractRecord(record))
			}
			return viewed
		},
	},
	flowrecord.Kind: {
		convert: flowRecordsFromPush,
		store: func(ctx context.Context, receiver KindReceiver, key string, batch any) error {
			records, _ := batch.([]flowrecord.Record)
			return receiver.ReceivePushedFlowRecords(ctx, key, records)
		},
		view: func(batch any) []any {
			records, _ := batch.([]flowrecord.Record)
			viewed := make([]any, 0, len(records))
			for _, record := range records {
				viewed = append(viewed, flowRecordContractRecord(record))
			}
			return viewed
		},
		judge: func(ctx context.Context, receiver KindReceiver, key string, problem push.Problem) bool {
			return receiver.FlowRecordSequenceAsserted(ctx, key, problem.From.Unix(), problem.To.Unix())
		},
		observe: func(ctx context.Context, receiver KindReceiver, key string, problem push.Problem) error {
			return receiver.ObserveFlowRecordCondition(ctx, key, flowrecord.Condition{Reason: problem.Reason,
				Scope: problem.Scope, At: problem.From.Unix()})
		},
	},
}

// firewallLogFromPush checks a batch of firewall_log records against the contract, and gives each the
// pushed identity: a digest the core computes from the pushed message under a prefix no pulled
// digest carries, whatever the script said, so the two paths' identities can never meet.
func firewallLogFromPush(answers []map[string]any, records []push.Record) (any, int, int) {
	contract, _ := provider.ContractOf(firewalllog.Kind)
	var converted []firewalllog.Record
	invalid := 0
	for index, answer := range answers {
		answer["digest"] = pushedDigest(records[index])
		checked, err := runtime.CheckRecord(contract, "firewall_log_record", answer, fmt.Sprintf("record %d", index))
		if err != nil {
			invalid++
			continue
		}
		record := firewallLogRecord(checked)
		if firewallLogRecordsInvalid([]firewalllog.Record{record}) != "" {
			invalid++
			continue
		}
		converted = append(converted, record)
	}
	return converted, len(converted), invalid
}

// flowRecordsFromPush checks a batch of flow_record records against the contract and gives each its
// identity, which is the core's and composed from what the delivery says, never from a field a
// mapping filled (PS17, *Identity*):
//
//	SHA-256 of  exporter address | observation domain | SHA-256 of the datagram | position in it
//
// so a datagram delivered twice stores its records once; two records of one datagram, identical in
// every field, keep their two positions and are two rows; a record after the exporter's sequence
// restarted, and the records of two counters sharing one observation domain with the same sequence
// number and export time, come in datagrams whose octets differ and never collide. The one case the
// identity cannot separate is two datagrams identical octet for octet: they are one delivery.
func flowRecordsFromPush(answers []map[string]any, records []push.Record) (any, int, int) {
	contract, _ := provider.ContractOf(flowrecord.Kind)
	var converted []flowrecord.Record
	invalid := 0
	for index, answer := range answers {
		checked, err := runtime.CheckRecord(contract, flowrecord.Kind+"_record", answer, fmt.Sprintf("record %d", index))
		if err != nil {
			invalid++
			continue
		}
		record, ok := flowRecordOf(checked)
		if !ok || records[index].Delivery == "" {
			invalid++
			continue
		}
		record.Identity = flowRecordIdentity(record, records[index])
		converted = append(converted, record)
	}
	return converted, len(converted), invalid
}

// flowRecordOf converts one checked flow_record record, refusing what the contract's types cannot:
// the ranges, and an address that is not one.
func flowRecordOf(checked map[string]any) (flowrecord.Record, bool) {
	source, sourceErr := netip.ParseAddr(runtime.Text(checked, "src_address"))
	destination, destinationErr := netip.ParseAddr(runtime.Text(checked, "dst_address"))
	exporter, exporterErr := netip.ParseAddr(runtime.Text(checked, "exporter_address"))
	if sourceErr != nil || destinationErr != nil || exporterErr != nil {
		return flowrecord.Record{}, false
	}
	record := flowrecord.Record{
		ExporterAddress: exporter.Unmap().String(), ObservationDomain: runtime.Integer(checked, "observation_domain"),
		IngressInterface: runtime.IntegerPointer(checked, "ingress_interface"),
		EgressInterface:  runtime.IntegerPointer(checked, "egress_interface"),
		IPVersion:        runtime.Integer(checked, "ip_version"), Protocol: runtime.Integer(checked, "protocol"),
		SrcAddress: source.Unmap().String(), DstAddress: destination.Unmap().String(),
		SrcPort: runtime.IntegerPointer(checked, "src_port"), DstPort: runtime.IntegerPointer(checked, "dst_port"),
		TCPFlags: runtime.IntegerPointer(checked, "tcp_flags"), Octets: runtime.Integer(checked, "octets"),
		Packets:     runtime.Integer(checked, "packets"),
		StartMillis: millisecondsOf(runtime.Number(checked, "start_at")),
		EndMillis:   millisecondsOf(runtime.Number(checked, "end_at")),
		ReceivedAt:  int64(math.Floor(runtime.Number(checked, "received_at"))),
	}
	// The version an exporter does not send is the addresses' own; one it sends must agree with them.
	family := int64(6)
	if source.Unmap().Is4() {
		family = 4
	}
	if _, sent := checked["ip_version"]; !sent {
		record.IPVersion = family
	}
	destinationFamily := int64(6)
	if destination.Unmap().Is4() {
		destinationFamily = 4
	}
	switch {
	case record.IPVersion != family || destinationFamily != family,
		record.IPVersion != 4 && record.IPVersion != 6,
		record.Protocol < 0 || record.Protocol > 255,
		record.Octets < 0 || record.Packets < 0,
		!portValid(record.SrcPort) || !portValid(record.DstPort),
		record.StartMillis < 0 || record.EndMillis < record.StartMillis,
		record.ObservationDomain < 0:
		return flowrecord.Record{}, false
	}
	return record, true
}

// millisecondsOf turns epoch seconds with a fraction into epoch milliseconds, rounding the fraction
// the float cannot hold exactly.
func millisecondsOf(seconds float64) int64 {
	return int64(math.Round(seconds * 1000))
}

// flowRecordIdentity composes a flow record's identity from its delivery.
func flowRecordIdentity(record flowrecord.Record, delivered push.Record) string {
	sum := sha256.Sum256([]byte(record.ExporterAddress + "|" + strconv.FormatInt(record.ObservationDomain, 10) + "|" +
		delivered.Delivery + "|" + strconv.Itoa(delivered.Position)))
	return hex.EncodeToString(sum[:])
}

// mappedAnswers builds a kind entry's answers from its field mapping, with no script: each contract
// field takes the value of the first of its protocol fields the record carries, as the contract's
// JSON type reads it. A field none of whose sources is present is absent, and a required one then
// refuses the record when it is checked.
func mappedAnswers(contract provider.Contract, table string, mapping map[string]contract.PushField,
	records []push.Record) []map[string]any {
	record, _ := provider.LookupRecord(contract, table)
	answers := make([]map[string]any, 0, len(records))
	for _, delivered := range records {
		answer := map[string]any{}
		for column, sources := range mapping {
			field, known := record.Field(column)
			if !known {
				continue
			}
			for _, source := range sources {
				value, present := delivered.Fields[source]
				if !present || value == nil {
					continue
				}
				if converted, ok := contractValue(field.Type, value); ok {
					answer[column] = converted
				}
				break
			}
		}
		answers = append(answers, answer)
	}
	return answers
}

// contractValue reads a decoded protocol value as a contract field of a type reads it: an address
// is text, an instant is epoch seconds with its fraction, an unsigned number above the signed range
// is refused rather than wrapped.
func contractValue(fieldType provider.FieldType, value any) (any, bool) {
	switch fieldType {
	case provider.FieldText:
		switch typed := value.(type) {
		case string:
			return typed, true
		case netip.Addr:
			return typed.Unmap().String(), true
		case net.HardwareAddr:
			return typed.String(), true
		}
		converted, err := push.Convert(push.TypeText, value)
		return converted, err == nil
	case provider.FieldInteger:
		converted, err := push.Convert(push.TypeInteger, value)
		return converted, err == nil
	case provider.FieldNumber:
		if instant, isInstant := value.(time.Time); isInstant {
			return epochSeconds(instant), true
		}
		converted, err := push.Convert(push.TypeReal, value)
		return converted, err == nil
	case provider.FieldBoolean:
		converted, err := push.Convert(push.TypeBoolean, value)
		return converted, err == nil
	}
	return nil, false
}

// pushedDigest is a pushed record's identity: the push prefix and the SHA-256 of what the message
// said and who said it -- header and message -- so the same line delivered twice is stored once.
func pushedDigest(record push.Record) string {
	var text strings.Builder
	for _, name := range runtime.SortedKeys(record.Fields) {
		fmt.Fprintf(&text, "%s=%v\x00", name, record.Fields[name])
	}
	sum := sha256.Sum256([]byte(text.String()))
	return pushedDigestPrefix + hex.EncodeToString(sum[:])
}

// pushedDigestPrefix is internal/collect's PushedDigestPrefix, which the collector reads the newest
// pushed record by; the plugin package cannot import the collector, so a test holds them equal.
const pushedDigestPrefix = "push:"

// kindSink hands a push entry's records to the core kind's receiver.
type kindSink struct {
	binding     *pushBinding
	plugin      *Plugin
	environment PushEnvironment
}

// kindBatch is what Prepare made: the converted records, of the kind's own type.
type kindBatch struct {
	records any
}

func (s *kindSink) Prepare(ctx context.Context, records []push.Record) push.Prepared {
	prepared := push.Prepared{Counts: map[string]int64{}}
	kind := pushKinds[s.binding.declaration.Kind]
	contract, _ := provider.ContractOf(s.binding.declaration.Kind)
	var batches []any
	rows := 0
	for start := 0; start < len(records); start += ParseBatch {
		chunk := records[start:min(len(records), start+ParseBatch)]
		var objects []map[string]any
		var sources []push.Record
		if s.binding.declaration.Parse == "" {
			// The field mapping: no script runs, per record or per batch.
			objects = mappedAnswers(contract, s.binding.declaration.Kind+"_record", s.binding.declaration.Fields,
				chunk)
			sources = chunk
		} else {
			answers, failed := callParse(ctx, s.plugin, s.environment.Session, s.binding.declaration.Parse, chunk,
				prepared.Counts)
			if failed != "" {
				prepared.Failed += len(chunk)
				prepared.FailedReason = push.GapScriptFailed
				continue
			}
			for index, answer := range answers {
				if answer == nil {
					prepared.Counts[pushCounterSkipped]++
					continue
				}
				object, isObject := answer.(map[string]any)
				if !isObject {
					prepared.Counts[pushCounterRecordsInvalid]++
					continue
				}
				objects = append(objects, object)
				sources = append(sources, chunk[index])
			}
		}
		batch, count, invalid := kind.convert(objects, sources)
		prepared.Counts[pushCounterRecordsInvalid] += int64(invalid)
		if count > 0 {
			batches = append(batches, batch)
			rows += count
		}
	}
	prepared.Rows = rows
	if rows > 0 {
		prepared.Payload = kindBatch{records: joinBatches(batches)}
	}
	return prepared
}

// joinBatches joins the converted chunks of one flush into one slice of the kind's own type.
func joinBatches(batches []any) any {
	if len(batches) == 1 {
		return batches[0]
	}
	switch batches[0].(type) {
	case []firewalllog.Record:
		var all []firewalllog.Record
		for _, batch := range batches {
			all = append(all, batch.([]firewalllog.Record)...)
		}
		return all
	case []flowrecord.Record:
		var all []flowrecord.Record
		for _, batch := range batches {
			all = append(all, batch.([]flowrecord.Record)...)
		}
		return all
	}
	return batches[0]
}

func (s *kindSink) Store(ctx context.Context, prepared push.Prepared) error {
	batch, ok := prepared.Payload.(kindBatch)
	if !ok {
		return nil
	}
	return pushKinds[s.binding.declaration.Kind].store(ctx, s.environment.Kinds, s.binding.providerKey,
		batch.records)
}

// SequenceAsserted asks the kind whether a loss the sender's sequence numbers show is asserted.
func (s *kindSink) SequenceAsserted(ctx context.Context, problem push.Problem) bool {
	kind := pushKinds[s.binding.declaration.Kind]
	if kind.judge == nil || s.environment.Kinds == nil {
		return true
	}
	return kind.judge(ctx, s.environment.Kinds, s.binding.providerKey, problem)
}

// ObserveCondition tells the kind a condition of the stream.
func (s *kindSink) ObserveCondition(ctx context.Context, problem push.Problem) {
	kind := pushKinds[s.binding.declaration.Kind]
	if kind.observe == nil || s.environment.Kinds == nil {
		return
	}
	if err := kind.observe(ctx, s.environment.Kinds, s.binding.providerKey, problem); err != nil &&
		s.environment.Report != nil {
		s.environment.Report(fmt.Errorf("plugin: %s: recording %s: %w", s.binding.ID(), problem.Reason, err))
	}
}

// flowRecordContractRecord is a flow_record record under its contract's field names, with the identity
// the core gave it, as a test reads it.
func flowRecordContractRecord(record flowrecord.Record) map[string]any {
	optional := func(value *int64) any {
		if value == nil {
			return nil
		}
		return *value
	}
	return map[string]any{
		"identity": record.Identity, "exporter_address": record.ExporterAddress,
		"observation_domain": record.ObservationDomain, "ingress_interface": optional(record.IngressInterface),
		"egress_interface": optional(record.EgressInterface), "ip_version": record.IPVersion,
		"protocol": record.Protocol, "src_address": record.SrcAddress, "dst_address": record.DstAddress,
		"src_port": optional(record.SrcPort), "dst_port": optional(record.DstPort),
		"tcp_flags": optional(record.TCPFlags), "octets": record.Octets, "packets": record.Packets,
		"start_at": float64(record.StartMillis) / 1000, "end_at": float64(record.EndMillis) / 1000,
		"received_at": record.ReceivedAt,
	}
}
