package provider

import (
	"context"
	"sync"
)

// Records a push entry delivers (PS9 of the push amendment, as PS17 of the NetFlow join amendment
// amends it; PA2 of the plugin-API amendment). Whether a kind may be pushed is its kind file's
// `push`, and the record table an entry fills its `push_record`; the plugin host checks each record
// against that table, as a pulled answer is checked, and hands the checked records to the core through
// one receiver whatever the kind.
//
// WHAT A KIND DERIVES FROM THE DELIVERY ITSELF is the kind's own: a pushed filter-log record's identity
// is namespaced apart from every pulled one, a flow record's identity is composed from the datagram it
// came in. A kind with such a rule registers its PushConversion from its own file of this package, by
// init(), so no list here names a kind, and a kind with none is checked against its contract alone.

// PushDelivery is what the core knows of a pushed record beside its fields: the protocol fields it was
// decoded from, the delivery (one datagram, one stream position) it came in, and its position there.
type PushDelivery struct {
	Fields   map[string]any
	Delivery string
	Position int
}

// PushConversion is a kind's own rule for a pushed record.
type PushConversion struct {
	// Identify gives an answer the fields the core derives from its delivery, before the contract check.
	Identify func(answer map[string]any, delivery PushDelivery)
	// Normalise reads a checked answer, refusing what the contract's types cannot express, and adds the
	// fields the core derives; false refuses the record.
	Normalise func(checked Answer, delivery PushDelivery) (Answer, bool)
}

// PushCondition is a condition a decoder detected on a stream a push entry is fed by: a sequence
// restart, a template missing.
type PushCondition struct {
	// Reason is the decoder's name for it.
	Reason string
	// Scope is the stream: the exporter's address and the observation domain, "<address>/<domain>".
	Scope string
	// At is the instant the stream showed it, in UTC epoch seconds.
	At int64
}

// PushReceiver is the core's side of every push entry delivering a kind's records.
type PushReceiver interface {
	// ReceivePushed takes the records a provider's push entry delivered, checked and converted.
	ReceivePushed(ctx context.Context, kind, key string, records []Answer) error
	// PushSequenceAsserted says whether a loss the sender's sequence numbers show over [from, to] is
	// asserted for a provider of a kind; a kind with no rule of its own asserts every one.
	PushSequenceAsserted(ctx context.Context, kind, key string, from, to int64) bool
	// ObservePushCondition is told a condition the decoder detected on a stream a provider's push entry
	// is fed by.
	ObservePushCondition(ctx context.Context, kind, key string, condition PushCondition) error
}

var pushConversions = struct {
	sync.RWMutex
	byKind map[string]PushConversion
}{byKind: map[string]PushConversion{}}

// RegisterPushConversion registers a kind's own rule for its pushed records, from that kind's file.
func RegisterPushConversion(kind string, conversion PushConversion) {
	pushConversions.Lock()
	defer pushConversions.Unlock()
	pushConversions.byKind[kind] = conversion
}

// PushConversionOf returns a kind's own rule for its pushed records; the zero value when it has none.
func PushConversionOf(kind string) PushConversion {
	pushConversions.RLock()
	defer pushConversions.RUnlock()
	return pushConversions.byKind[kind]
}
