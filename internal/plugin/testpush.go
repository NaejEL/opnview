package plugin

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/plugin/runtime"
	"github.com/NaejEL/opnview/internal/push"
)

// The push half of a plugin's test world (PS15 of the push amendment): recorded syslog lines and
// NetFlow packets are delivered THROUGH THE CORE'S REAL DECODER AND ROUTER -- the same hub, decoders,
// header filter, regular expression, field mapping and parse function the service runs -- and the
// test reads what reached the entry's table or kind. A test never calls its parse function directly
// to prove ingestion.
//
//	t.deliverSyslog(lines, {transport: "tcp", sender: "<address>"})  each line one syslog message
//	t.deliverNetflow(packets, {sender: "<address>"})                  each packet hexadecimal text
//	t.pushed("<entry>")       the rows (or core-kind records) the entry stored, instants as epoch seconds
//	t.pushCounters("<entry>") the entry's counters: admitted, skipped, push_value_invalid, script_calls...
//	t.listenerCounters("<protocol>", "<transport>") the listener's: header_unparseable, no_subscriber...
//	t.fixture("<file>")      the body of a recorded fixture of testdata/, such as a list of lines
//
// The sender defaults to the test world's firewall address, which the test world accepts as the
// firewall's; a test naming another sender is refused unless it said so with t.acceptSender. That
// address is a fixture, testdata/test_world.json -- a documentation address (RFC 5737) -- and never
// a literal of the code, which presupposes no address at all.

// testWorldFixture is the test world's own labelled fixture {__provenance__, body}.
//
//go:embed testdata/test_world.json
var testWorldFixture []byte

// defaultTestSender reads the test world's firewall address from its fixture.
func defaultTestSender() (netip.Addr, error) {
	var fixture struct {
		Body struct {
			Sender string `json:"sender"`
		} `json:"body"`
	}
	if err := json.Unmarshal(testWorldFixture, &fixture); err != nil {
		return netip.Addr{}, fmt.Errorf("plugin: reading the test world's fixture: %w", err)
	}
	sender, err := netip.ParseAddr(fixture.Body.Sender)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("plugin: the test world's fixture names no sender address: %w", err)
	}
	return sender, nil
}

// pushWorld is a test world's hub and what its sinks captured.
type pushWorld struct {
	hub *push.Hub
	// sender is the address a delivered message comes from when the test names none.
	sender   netip.Addr
	accepted []netip.Addr
	mutex    sync.Mutex
	captured map[string][]any
}

// capturingSink stores through the real sink, and keeps what it stored for the test to read.
type capturingSink struct {
	inner push.Sink
	entry string
	world *pushWorld
}

func (s capturingSink) Prepare(ctx context.Context, records []push.Record) push.Prepared {
	return s.inner.Prepare(ctx, records)
}

// SequenceAsserted and ObserveCondition reach the real sink, so a test world judges and observes as the
// service does.
func (s capturingSink) SequenceAsserted(ctx context.Context, problem push.Problem) bool {
	if judge, judges := s.inner.(push.SequenceJudge); judges {
		return judge.SequenceAsserted(ctx, problem)
	}
	return true
}

func (s capturingSink) ObserveCondition(ctx context.Context, problem push.Problem) {
	if observer, observes := s.inner.(push.ConditionObserver); observes {
		observer.ObserveCondition(ctx, problem)
	}
}

func (s capturingSink) Store(ctx context.Context, prepared push.Prepared) error {
	if table, isTable := s.inner.(*tableSink); isTable && table.environment.Store != nil {
		if err := s.inner.Store(ctx, prepared); err != nil {
			return err
		}
	}
	var rows []any
	switch payload := prepared.Payload.(type) {
	case tableBatch:
		for _, row := range payload.rows {
			rows = append(rows, jsonRow(row))
		}
	case kindBatch:
		if kind, isKind := s.inner.(*kindSink); isKind {
			if err := s.inner.Store(ctx, prepared); err != nil {
				return err
			}
			rows = append(rows, pushKinds[kind.binding.declaration.Kind].view(payload.records)...)
		}
	}
	s.world.mutex.Lock()
	s.world.captured[s.entry] = append(s.world.captured[s.entry], rows...)
	s.world.mutex.Unlock()
	return nil
}

// jsonRow is a stored row as a test reads it: instants as epoch seconds, addresses as text.
func jsonRow(row map[string]any) map[string]any {
	converted := make(map[string]any, len(row))
	for name, value := range row {
		switch typed := value.(type) {
		case time.Time:
			converted[name] = epochSeconds(typed)
		case netip.Addr:
			converted[name] = typed.String()
		case net.HardwareAddr:
			converted[name] = typed.String()
		default:
			converted[name] = typed
		}
	}
	return converted
}

// pushWorldOf builds the world's hub at its first use: the plugin's push entries subscribed, one
// listener per protocol and transport with no socket, fed by Deliver.
func (w *testWorld) pushWorldOf() (*pushWorld, error) {
	w.mutex.Lock()
	built := w.push
	w.mutex.Unlock()
	if built != nil {
		return built, nil
	}
	world := &pushWorld{captured: map[string][]any{}}
	reference := w.reference
	world.hub = push.NewHub(push.Options{
		Now: func() time.Time { return reference },
		Resolve: func(_ context.Context, zoneless string) (time.Time, error) {
			epoch, err := decode.Normaliser{Reference: reference}.NormaliseFilterLogTimestamp(zoneless)
			return time.Unix(epoch, 0).UTC(), err
		},
	})
	environment := PushEnvironment{Store: w.database, Session: w.session(), Kinds: discardKinds{}}
	for _, binding := range w.plugin.pushes {
		subscription := pushSubscriptionOf(w.plugin, binding, environment)
		subscription.Sink = capturingSink{inner: subscription.Sink, entry: binding.declaration.Name, world: world}
		if err := world.hub.Subscribe(subscription); err != nil {
			return nil, err
		}
	}
	for _, name := range push.Protocols() {
		protocol, _ := push.Lookup(name)
		for _, transport := range protocol.Transports() {
			if err := world.hub.AddLoopbackListener(name, transport); err != nil {
				return nil, err
			}
		}
	}
	sender, err := defaultTestSender()
	if err != nil {
		return nil, err
	}
	world.sender = sender
	world.accepted = []netip.Addr{sender}
	world.hub.Senders().Set(world.accepted)
	w.mutex.Lock()
	w.push = world
	w.mutex.Unlock()
	return world, nil
}

// discardKinds is the core-kind receiver of the test world: a test reads what reached the kind
// through t.pushed, and no coordinator runs.
type discardKinds struct{}

func (discardKinds) ReceivePushedFirewallLog(context.Context, string, []firewalllog.Record) error {
	return nil
}

func (discardKinds) ReceivePushedFlowRecords(context.Context, string, []flowrecord.Record) error {
	return nil
}

func (discardKinds) ObserveFlowRecordCondition(context.Context, string, flowrecord.Condition) error {
	return nil
}

func (discardKinds) FlowRecordSequenceAsserted(context.Context, string, int64, int64) bool {
	return true
}

// pushAPI adds the push functions to a test's API.
func (w *testWorld) pushAPI(vm *goja.Runtime, set func(string, func(goja.FunctionCall) goja.Value)) {
	deliver := func(protocol string, decodeHex bool) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			world, err := w.pushWorldOf()
			if err != nil {
				runtime.Throw(vm, "%v", err)
			}
			transport := push.TransportUDP
			if protocol != "netflow" {
				transport = push.TransportTCP
			}
			address := world.sender
			if options, isObject := call.Argument(1).Export().(map[string]any); isObject {
				if value, ok := options["transport"].(string); ok {
					transport = value
				}
				if value, ok := options["sender"].(string); ok {
					if address, err = netip.ParseAddr(value); err != nil {
						runtime.Throw(vm, "the sender %q is not an address", value)
					}
				}
			}
			payloads, isList := call.Argument(0).Export().([]any)
			if !isList {
				runtime.Throw(vm, "the first argument is a list of messages")
			}
			ctx := context.Background()
			for _, item := range payloads {
				text, isText := item.(string)
				if !isText {
					runtime.Throw(vm, "each message is a string")
				}
				payload := []byte(text)
				if decodeHex {
					payload, err = hex.DecodeString(strings.Join(strings.Fields(text), ""))
					if err != nil {
						runtime.Throw(vm, "a NetFlow packet is hexadecimal text: %v", err)
					}
				}
				if err := world.hub.Deliver(ctx, protocol, transport, payload, address); err != nil {
					runtime.Throw(vm, "%v", err)
				}
			}
			world.hub.Flush(ctx)
			return goja.Undefined()
		}
	}
	set("fixture", func(call goja.FunctionCall) goja.Value {
		raw, err := w.fixture(call.Argument(0).String())
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		var body any
		if err := json.Unmarshal(raw, &body); err != nil {
			runtime.Throw(vm, "%v", err)
		}
		return vm.ToValue(body)
	})
	set("deliverSyslog", deliver("syslog", false))
	set("deliverNetflow", deliver("netflow", true))
	set("acceptSender", func(call goja.FunctionCall) goja.Value {
		world, err := w.pushWorldOf()
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		address, err := netip.ParseAddr(call.Argument(0).String())
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		world.accepted = append(world.accepted, address)
		world.hub.Senders().Set(world.accepted)
		return goja.Undefined()
	})
	set("pushed", func(call goja.FunctionCall) goja.Value {
		world, err := w.pushWorldOf()
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		world.mutex.Lock()
		defer world.mutex.Unlock()
		rows := world.captured[call.Argument(0).String()]
		if rows == nil {
			rows = []any{}
		}
		return vm.ToValue(rows)
	})
	set("pushCounters", func(call goja.FunctionCall) goja.Value {
		world, err := w.pushWorldOf()
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		state, found := world.hub.SubscriptionState(w.plugin.Name + "." + call.Argument(0).String())
		if !found {
			runtime.Throw(vm, "the plugin declares no push entry %q", call.Argument(0).String())
		}
		counters := map[string]any{}
		for name, count := range state.Counters {
			counters[name] = count
		}
		return vm.ToValue(counters)
	})
	set("listenerCounters", func(call goja.FunctionCall) goja.Value {
		world, err := w.pushWorldOf()
		if err != nil {
			runtime.Throw(vm, "%v", err)
		}
		counters := map[string]any{}
		for _, listener := range world.hub.Catalogue().Listeners {
			if listener.Protocol == call.Argument(0).String() && listener.Transport == call.Argument(1).String() {
				for name, count := range listener.Counters {
					counters[name] = count
				}
			}
		}
		return vm.ToValue(counters)
	})
}

// firewallLogContractRecord is a firewall_log record under its contract's field names, as a test
// reads it beside one t.call returned.
func firewallLogContractRecord(record firewalllog.Record) map[string]any {
	optional := func(value *int64) any {
		if value == nil {
			return nil
		}
		return *value
	}
	text := func(value *string) any {
		if value == nil {
			return nil
		}
		return *value
	}
	return map[string]any{
		"digest": record.Digest, "observed_at": record.ObservedAt, "device": record.Device,
		"src_address": record.SrcAddress, "dst_address": record.DstAddress, "src_port": optional(record.SrcPort),
		"dst_port": optional(record.DstPort), "protocol": record.Protocol, "ip_version": record.IPVersion,
		"action": record.Action, "direction": record.Direction, "log_reason": text(record.LogReason),
		"packet_bytes": record.PacketBytes, "rid": text(record.Rid), "ip_id": optional(record.IPID),
		"tcp_seq": optional(record.TCPSeq),
	}
}
