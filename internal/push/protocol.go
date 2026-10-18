package push

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// A PROTOCOL IS A SEAM (PS2 of the push amendment). Each one is a directory below
// internal/push/protocols holding a protocol.json marker, its decoder and its tests, and registers
// itself with Register from an init(); internal/push/protocols/protocols_gen.go, written by
// `go generate` from the directory tree, links them all into the binary. Batching, routing,
// storage, gaps and the catalogues below name no protocol: adding one is adding its directory.

// The transports a listener receives on.
const (
	TransportUDP = "udp"
	TransportTCP = "tcp"
)

// Protocol is one push protocol: the generic decoding of its messages into Records, and what a
// subscription to it may filter on. It knows the protocol's standard and nothing of any program's
// or exporter's own message format.
type Protocol interface {
	// Name is the word a push declaration's `protocol` names: syslog, netflow.
	Name() string
	// Transports are the transports it is received on.
	Transports() []string
	// NewDecoder returns the decoding state of one listener: a NetFlow decoder holds templates per
	// exporter, which a datagram of one exporter must not share with another listener's.
	NewDecoder(limits Limits) Decoder
	// NewFramer splits a stream connection into messages, for a protocol received over TCP; nil
	// for one that is not. maxFrame bounds one message.
	NewFramer(reader io.Reader, maxFrame int) Framer
	// Compile reads a declaration's filter, which is the protocol's own JSON, into the selector
	// routing indexes a subscriber by. An unknown key, or a name the protocol does not know (an
	// Information Element outside the registry), is an error naming it.
	Compile(filter json.RawMessage) (Selector, error)
	// Fields are the fields every record of the protocol carries whatever the sender, with their
	// type -- a syslog header's -- which a declaration may map to columns; nil for a protocol
	// whose fields are each record's own (NetFlow's, named by its template).
	Fields() map[string]string
	// FieldType is the type of a named field, for a protocol whose fields come from a registry;
	// ok is false for a name it does not know.
	FieldType(name string) (columnType string, ok bool)
	// LossDetectable says whether loss on the network is detectable on a transport: a protocol
	// carrying a sequence number detects it, syslog over UDP does not (PS7).
	LossDetectable(transport string) bool
}

// The two fields of a delivery that every push entry's field mapping may name, whatever its protocol:
// the SHA-256 of the datagram or frame a record was decoded from, in hexadecimal (Record.Delivery), and
// the record's place in it (Record.Position). With them a plugin's table keys a record by the delivery it
// came in, so a datagram delivered twice stores its records once and two records identical in every field
// of one datagram are two rows. The names are opnview's own: no registry names a delivery.
const (
	DeliveryField         = "delivery"
	DeliveryPositionField = "delivery_position"
)

// DeliveryValue is the value of one of the two delivery fields of a record, and whether the name is one.
func DeliveryValue(record Record, name string) (any, bool) {
	switch name {
	case DeliveryField:
		if record.Delivery == "" {
			return nil, true
		}
		return record.Delivery, true
	case DeliveryPositionField:
		if record.Delivery == "" {
			return nil, true
		}
		return int64(record.Position), true
	}
	return nil, false
}

// IsDeliveryField reports whether a field name is one of the two delivery fields.
func IsDeliveryField(name string) bool { return name == DeliveryField || name == DeliveryPositionField }

// Limits bound one decoder's state.
type Limits struct {
	// HeldRecords bounds the data records held waiting for their template, per exporter and
	// observation domain.
	HeldRecords int
	// HeldSeconds bounds how long they are held.
	HeldSeconds int
	// TemplatesPerExporter bounds the templates held per exporter.
	TemplatesPerExporter int
}

// Meta is what the listener knows of one datagram or frame.
type Meta struct {
	Sender     netip.Addr
	Transport  string
	ReceivedAt time.Time
}

// Decoder decodes datagrams or frames into records.
type Decoder interface {
	// Decode decodes one datagram or frame. It never panics on any input: a malformed one is a
	// Problem.
	Decode(payload []byte, meta Meta) Decoded
	// Expire is called on a timer: the decoder drops what it held past its bound, as Problems.
	Expire(now time.Time) Decoded
	// State reports what the decoder holds, for the catalogue: templates seen, per key.
	State() DecoderState
}

// DecoderState is what a decoder reports of itself for the API catalogue: names and counts only.
type DecoderState struct {
	// Templates are the templates (or, for syslog, the programs) seen, each with how many
	// records were decoded with it.
	Seen map[string]int64 `json:"seen"`
	// Flags are named conditions the decoder detected, such as a sequence stream it cannot follow.
	Flags []string `json:"flags,omitempty"`
}

// Decoded is what decoding produced: records, and the problems met.
type Decoded struct {
	Records  []Record
	Problems []Problem
}

// Problem is one thing decoding could not do, under a closed reason.
type Problem struct {
	// Reason is a counter name, and a gap reason when Gap is set.
	Reason string
	// Count is how many messages or records it covers.
	Count int
	// Gap marks a loss that is recorded as a gap of every subscriber of the protocol, from From to
	// To; one that is not a gap is only counted.
	Gap      bool
	From, To time.Time
	// Sequence marks a problem the decoder concluded from the sender's sequence numbers: a gap they
	// show, or a condition under which they no longer show one. A sink that knows whether the
	// sender's numbering can be trusted at an instant judges such a gap before it is recorded
	// (SequenceJudge).
	Sequence bool
	// Condition marks a fact the decoder detected that is neither a loss nor a malformation -- a
	// sequence stream it can no longer follow -- told to every sink that observes conditions
	// (ConditionObserver), at From, and counted.
	Condition bool
	// Scope names the stream the problem concerns, as the protocol scopes its state: for NetFlow, the
	// exporter's address and the observation domain, "<address>/<domain>". Empty when the problem
	// concerns no one stream.
	Scope string
	// Detail says what happened, with no field value.
	Detail string
}

// Record is one decoded message or flow record: its fields by name, typed by the vocabulary of
// types.go (an int64, uint64, float64, string, bool, netip.Addr, net.HardwareAddr, time.Time or
// []byte), and what the listener knew of it.
type Record struct {
	// RouteKey is what routing indexes subscribers by: a syslog program, a NetFlow template.
	RouteKey string
	// Fields are the decoded fields by name.
	Fields map[string]any
	// Instant is the record's own instant, when its header carried an absolute one.
	Instant time.Time
	// Zoneless is the record's instant as text carrying no zone (an RFC 3164 timestamp, or an
	// ISO one without an offset), which routing resolves with the firewall's measured offset
	// (PS5) before any subscriber sees it. Empty when Instant is set.
	Zoneless string
	// InstantField is the field the resolved instant is written to.
	InstantField string
	// Text is the free text a regular expression matches: syslog's MSG. Empty otherwise.
	Text   string
	Sender netip.Addr
	// Transport is udp or tcp.
	Transport  string
	ReceivedAt time.Time
	// Delivery identifies the datagram or frame the record was decoded from: the SHA-256 of its
	// octets, in hexadecimal. The same datagram delivered twice has the same Delivery; two datagrams
	// differing in any octet do not. Empty when the decoder records none.
	Delivery string
	// Position is the record's place within its delivery -- for NetFlow, its set's ordinal in the
	// datagram times 65 536 plus its own ordinal in the set -- so two records identical in every
	// field of one datagram keep two positions.
	Position int
}

// The decoder's own interfaces, which a decoder implements when it has the state they act on.

// SequenceResetter is a decoder that follows its senders' sequence numbers and can forget what it
// concluded of them: every counter it follows is followed afresh from the next datagram.
type SequenceResetter interface {
	ResetSequences()
}

// SequenceBounder is a decoder that follows several sequence counters under one observation domain and
// asserts a loss only once more counters are open than the bound it is given: how many counters the
// sender runs, which whoever reads the sender's configuration knows. 0 is a bound not known, under which
// no loss is asserted.
type SequenceBounder interface {
	SetSequenceCounters(counters int)
}

// ScriptDecoded is a protocol whose messages carry no structure of their own -- a datagram, a line -- so the
// core decodes nothing in them: a push entry of it names a decode function of its script, called with ONE
// message, which returns what the message held (PA7 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md). Its records carry the message's octets under PayloadField.
type ScriptDecoded interface {
	ScriptDecoded() bool
}

// PayloadField is the field a record of a ScriptDecoded protocol carries its message's octets under.
const PayloadField = "payload"

// IsScriptDecoded reports whether a protocol's messages are decoded by a script.
func IsScriptDecoded(protocol Protocol) bool {
	decoded, is := protocol.(ScriptDecoded)
	return is && decoded.ScriptDecoded()
}

// Framer splits a stream into messages.
type Framer interface {
	// Next returns the next message, io.EOF at the end of the stream, or ErrFrameTooLarge.
	Next() ([]byte, error)
}

// ErrFrameTooLarge is a frame above the bound: counted, and its connection closed.
var ErrFrameTooLarge = fmt.Errorf("push: a frame exceeds the bound")

// Selector is a subscriber's compiled filter.
type Selector interface {
	// Keys are the route keys the subscriber is indexed under; nil when every record must be
	// asked (Admits), such as a NetFlow subscriber whose templates are not known in advance.
	Keys() []string
	// Admits reports whether one record is the subscriber's, and why not when it is not -- a
	// required field missing, for the counter of that name; "" when the filter simply does not
	// select it.
	Admits(record *Record) (bool, string)
}

var (
	registryMutex sync.RWMutex
	registry      = map[string]Protocol{}
	duplicates    []string
)

// Register adds a protocol; it is called from the init() of the protocol's own package. A second
// protocol of a name already taken is not added, and is listed by Duplicates, which the
// modularity test asserts is empty.
func Register(protocol Protocol) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	if _, taken := registry[protocol.Name()]; taken {
		duplicates = append(duplicates, protocol.Name())
		return
	}
	registry[protocol.Name()] = protocol
}

// Lookup returns a registered protocol.
func Lookup(name string) (Protocol, bool) {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	protocol, found := registry[name]
	return protocol, found
}

// GapReasoner is a protocol that records collection gaps for reasons of its own, beside the push
// sources' common ones: what its own framing reveals was lost. The protocol declares them, so the
// core names none of a protocol's reasons.
type GapReasoner interface {
	GapReasons() []string
}

// ProtocolGapReasons returns the gap reasons every registered protocol declares, sorted.
func ProtocolGapReasons() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	var reasons []string
	for _, protocol := range registry {
		if reasoner, declares := protocol.(GapReasoner); declares {
			reasons = append(reasons, reasoner.GapReasons()...)
		}
	}
	sort.Strings(reasons)
	return reasons
}

// Protocols returns the names of every registered protocol, sorted.
func Protocols() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
