package plugin

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/push"
)

// A push entry delivering records of a kind (PS9 of the push amendment, as PS17 of the NetFlow join
// amendment amends it; PA2 of the plugin-API amendment). Whether a kind may be pushed is data -- its kind
// file's `push`, and `push_record` the table an entry fills -- and every such kind goes the same way:
// its records are built either by the entry's parse function or, with no script at all, by its field
// mapping -- each contract field named after the protocol fields it is read from, the first present
// one winning -- given what the kind derives from the delivery (provider.PushConversionOf), checked
// against the kind's contract as a pulled answer is, and handed to the core through one receiver
// (provider.PushReceiver), which routes them to the kind's coordinator: a kind fed by two paths keeps
// one path per instant (internal/collect/kinds/filterlog/pushpaths.go), a push-only record goes to the
// kind's own store.

// kindSink hands a push entry's records to the core.
type kindSink struct {
	binding     *pushBinding
	plugin      *Plugin
	environment PushEnvironment
}

// kindBatch is what Prepare made: the checked records, under their contract's field names.
type kindBatch struct {
	records []provider.Answer
}

func (s *kindSink) Prepare(ctx context.Context, records []push.Record) push.Prepared {
	prepared := push.Prepared{Counts: map[string]int64{}}
	kind := s.binding.declaration.Kind
	kindContract, _ := provider.ContractOf(kind)
	conversion := provider.PushConversionOf(kind)
	var converted []provider.Answer
	for start := 0; start < len(records); start += ParseBatch {
		chunk := records[start:min(len(records), start+ParseBatch)]
		var objects []map[string]any
		var sources []push.Record
		if s.binding.declaration.Parse == "" {
			// The field mapping: no script runs, per record or per batch.
			objects = mappedAnswers(kindContract, kindContract.PushRecord, s.binding.declaration.Fields, chunk)
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
		for index, object := range objects {
			delivery := provider.PushDelivery{Fields: sources[index].Fields, Delivery: sources[index].Delivery,
				Position: sources[index].Position}
			if conversion.Identify != nil {
				conversion.Identify(object, delivery)
			}
			checked, err := scripthost.CheckRecord(kindContract, kindContract.PushRecord, object, fmt.Sprintf("record %d", index))
			if err != nil {
				prepared.Counts[pushCounterRecordsInvalid]++
				continue
			}
			if conversion.Normalise != nil {
				normalised, ok := conversion.Normalise(checked, delivery)
				if !ok {
					prepared.Counts[pushCounterRecordsInvalid]++
					continue
				}
				checked = normalised
			}
			converted = append(converted, checked)
		}
	}
	prepared.Rows = len(converted)
	if len(converted) > 0 {
		prepared.Payload = kindBatch{records: converted}
	}
	return prepared
}

func (s *kindSink) Store(ctx context.Context, prepared push.Prepared) error {
	batch, ok := prepared.Payload.(kindBatch)
	if !ok || s.environment.Kinds == nil {
		return nil
	}
	return s.environment.Kinds.ReceivePushed(ctx, s.binding.declaration.Kind, s.binding.providerKey, batch.records)
}

// SequenceAsserted asks the core whether a loss the sender's sequence numbers show is asserted.
func (s *kindSink) SequenceAsserted(ctx context.Context, problem push.Problem) bool {
	if s.environment.Kinds == nil {
		return true
	}
	return s.environment.Kinds.PushSequenceAsserted(ctx, s.binding.declaration.Kind, s.binding.providerKey,
		problem.From.Unix(), problem.To.Unix())
}

// ObserveCondition tells the core a condition of the stream.
func (s *kindSink) ObserveCondition(ctx context.Context, problem push.Problem) {
	if s.environment.Kinds == nil {
		return
	}
	condition := provider.PushCondition{Reason: problem.Reason, Scope: problem.Scope, At: problem.From.Unix()}
	if err := s.environment.Kinds.ObservePushCondition(ctx, s.binding.declaration.Kind, s.binding.providerKey,
		condition); err != nil && s.environment.Report != nil {
		s.environment.Report(fmt.Errorf("plugin: %s: recording %s: %w", s.binding.ID(), problem.Reason, err))
	}
}

// contractView is a pushed record under its contract's field names, as a test reads it beside one
// t.call returned: every field of the table, an absent optional one null, and what the core derived.
func contractView(kind string, record provider.Answer) map[string]any {
	view := map[string]any{}
	kindContract, _ := provider.ContractOf(kind)
	if table, found := provider.LookupRecord(kindContract, kindContract.PushRecord); found {
		for _, field := range table.Fields {
			view[field.Name] = nil
		}
	}
	for name, value := range record {
		view[name] = value
	}
	return view
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
