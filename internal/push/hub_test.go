package push_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/push"
	_ "github.com/NaejEL/opnview/internal/push/protocols"
)

// A TEST PROTOCOL, ADDED THE WAY EVERY PROTOCOL IS: one decoder and one registration, and nothing of
// the hub's routing, batching or catalogue touched (PS-AC2). Each datagram is one record whose route
// key is its first word.
type lineProtocol struct{}

func init() { push.Register(lineProtocol{}) }

const testProtocol = "test_line"

func (lineProtocol) Name() string                         { return testProtocol }
func (lineProtocol) Transports() []string                 { return []string{push.TransportUDP} }
func (lineProtocol) NewDecoder(push.Limits) push.Decoder  { return lineDecoder{} }
func (lineProtocol) NewFramer(io.Reader, int) push.Framer { return nil }
func (lineProtocol) Fields() map[string]string            { return map[string]string{"text": push.TypeText} }
func (lineProtocol) FieldType(name string) (string, bool) { return push.TypeText, name == "text" }
func (lineProtocol) LossDetectable(string) bool           { return false }
func (lineProtocol) IndexFields() []string                { return nil }
func (lineProtocol) Compile(raw json.RawMessage) (push.Selector, error) {
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, err
	}
	return keySelector(key), nil
}

type keySelector string

func (k keySelector) Keys() []string { return []string{string(k)} }
func (k keySelector) Admits(record *push.Record) (bool, string) {
	return record.RouteKey == string(k), ""
}

type lineDecoder struct{}

func (lineDecoder) Decode(payload []byte, meta push.Meta) push.Decoded {
	key, _, _ := strings.Cut(string(payload), " ")
	return push.Decoded{Records: []push.Record{{RouteKey: key, Fields: map[string]any{"text": string(payload)},
		Instant: meta.ReceivedAt, Text: string(payload), Sender: meta.Sender, Transport: meta.Transport,
		ReceivedAt: meta.ReceivedAt}}}
}
func (lineDecoder) Expire(time.Time) push.Decoded { return push.Decoded{} }
func (lineDecoder) State() push.DecoderState      { return push.DecoderState{} }

// recordingSink keeps what it was asked to store; it may fail or stall.
type recordingSink struct {
	mutex    sync.Mutex
	prepares int
	stores   int
	records  []push.Record
	fail     bool
	block    chan struct{}
}

func (s *recordingSink) Prepare(_ context.Context, records []push.Record) push.Prepared {
	s.mutex.Lock()
	s.prepares++
	s.mutex.Unlock()
	return push.Prepared{Rows: len(records), Payload: records}
}

func (s *recordingSink) storeWith(prepared push.Prepared) error {
	if s.block != nil {
		<-s.block
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.stores++
	if s.fail {
		return errors.New("the database refused the batch")
	}
	s.records = append(s.records, prepared.Payload.([]push.Record)...)
	return nil
}

// sink adapts recordingSink so Store records.
type sink struct{ *recordingSink }

func (s sink) Store(_ context.Context, prepared push.Prepared) error { return s.storeWith(prepared) }

func (s *recordingSink) count() (int, int, int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.prepares, s.stores, len(s.records)
}

// gapRecorder keeps the gaps recorded.
type gapRecorder struct {
	mutex sync.Mutex
	gaps  []recordedGap
}

type recordedGap struct {
	subscription, reason string
	from, to             time.Time
	count                int64
}

func (g *gapRecorder) RecordPushGap(_ context.Context, subscription, reason string, from, to time.Time, count int64,
	_ string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.gaps = append(g.gaps, recordedGap{subscription, reason, from, to, count})
	return nil
}

func (g *gapRecorder) of(reason string) []recordedGap {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	var found []recordedGap
	for _, gap := range g.gaps {
		if gap.reason == reason {
			found = append(found, gap)
		}
	}
	return found
}

// clock is a fake clock.
type clock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *clock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *clock) advance(by time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(by)
	c.mutex.Unlock()
}

func newClock() *clock { return &clock{now: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)} }

var firewall = netip.MustParseAddr("192.0.2.1")

func syslogFilter(programs ...string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"program": programs})
	return encoded
}

func compile(t *testing.T, protocol string, filter json.RawMessage) push.Selector {
	t.Helper()
	registered, _ := push.Lookup(protocol)
	selector, err := registered.Compile(filter)
	if err != nil {
		t.Fatal(err)
	}
	return selector
}

func listenerOf(hub *push.Hub, protocol, transport string) push.ListenerEntry {
	for _, listener := range hub.Catalogue().Listeners {
		if listener.Protocol == protocol && listener.Transport == transport {
			return listener
		}
	}
	return push.ListenerEntry{}
}

// TestATestProtocolIsOneFileAndOneRegistration is PS-AC2's second half: the protocol above routes,
// batches and reaches the catalogue with nothing of the hub edited.
func TestATestProtocolIsOneFileAndOneRegistration(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{})
	recorded := &recordingSink{}
	if err := hub.Subscribe(push.Subscription{ID: "test.lines", Protocol: testProtocol,
		Selector: compile(t, testProtocol, json.RawMessage(`"alpha"`)), Sink: sink{recorded}}); err != nil {
		t.Fatal(err)
	}
	if err := hub.AddLoopbackListener(testProtocol, push.TransportUDP); err != nil {
		t.Fatal(err)
	}
	hub.Senders().Set([]netip.Addr{firewall})
	for _, line := range []string{"alpha one", "beta two", "alpha three"} {
		if err := hub.Deliver(context.Background(), testProtocol, push.TransportUDP, []byte(line), firewall); err != nil {
			t.Fatal(err)
		}
	}
	hub.Flush(context.Background())
	if _, _, stored := recorded.count(); stored != 2 {
		t.Errorf("the test protocol's subscriber stored %d records, want 2", stored)
	}
	if listenerOf(hub, testProtocol, push.TransportUDP).Counters[push.CounterNoSubscriber] != 1 {
		t.Error("the record nobody subscribed to is not counted")
	}
	if len(push.Duplicates()) != 0 {
		t.Errorf("protocols registered twice: %v", push.Duplicates())
	}
}

// TestALineNobodySubscribesToCostsOneDecode is PS-AC9: no regular expression, no script, no row --
// the sink is not even asked to prepare -- and it is counted.
func TestALineNobodySubscribesToCostsOneDecode(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "a.lines", Protocol: "syslog", Selector: compile(t, "syslog",
		syslogFilter("example-program")), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener("syslog", push.TransportTCP)
	hub.Senders().Set([]netip.Addr{firewall})
	_ = hub.Deliver(context.Background(), "syslog", push.TransportTCP,
		[]byte("<13>Oct 11 01:02:03 host other-program: text"), firewall)
	hub.Flush(context.Background())
	if prepares, _, _ := recorded.count(); prepares != 0 {
		t.Errorf("the sink prepared %d batches for a line nobody subscribes to", prepares)
	}
	if got := listenerOf(hub, "syslog", push.TransportTCP).Counters[push.CounterNoSubscriber]; got != 1 {
		t.Errorf("no_subscriber is %d", got)
	}
}

// TestTwoSubscribersShareOneDecode is PS-AC9: two plugins on one program, one decode per line, each
// with its own records.
func TestTwoSubscribersShareOneDecode(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{})
	first, second := &recordingSink{}, &recordingSink{}
	for id, recorded := range map[string]*recordingSink{"one.entry": first, "two.entry": second} {
		_ = hub.Subscribe(push.Subscription{ID: id, Protocol: "syslog", Selector: compile(t, "syslog",
			syslogFilter("shared")), Sink: sink{recorded}})
	}
	_ = hub.AddLoopbackListener("syslog", push.TransportTCP)
	hub.Senders().Set([]netip.Addr{firewall})
	for range 3 {
		_ = hub.Deliver(context.Background(), "syslog", push.TransportTCP,
			[]byte("<13>1 2026-10-11T01:02:03Z host shared - - - text"), firewall)
	}
	hub.Flush(context.Background())
	if decoded := listenerOf(hub, "syslog", push.TransportTCP).Decoded; decoded != 3 {
		t.Errorf("3 lines were decoded %d times", decoded)
	}
	for name, recorded := range map[string]*recordingSink{"first": first, "second": second} {
		if _, _, stored := recorded.count(); stored != 3 {
			t.Errorf("the %s subscriber stored %d", name, stored)
		}
	}
}

// waitFor polls a condition for up to two seconds.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s did not happen within two seconds", what)
}

// TestOnlyTheFirewallMaySend is PS-AC8, over real loopback sockets on ephemeral ports: a datagram and
// a connection from an address outside the set store nothing and are counted per address; once the
// set holds the address, with no restart, the same are stored.
func TestOnlyTheFirewallMaySend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := push.NewHub(push.Options{BatchInterval: 20 * time.Millisecond})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "p.lines", Protocol: "syslog", Selector: compile(t, "syslog",
		syslogFilter("prog")), Sink: sink{recorded}})
	hub.Start(ctx)
	for _, transport := range []string{push.TransportUDP, push.TransportTCP} {
		if err := hub.Listen(ctx, "syslog", transport, "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
	}
	send := func() {
		address, _ := hub.Address("syslog", push.TransportUDP)
		connection, err := net.Dial("udp", address)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = connection.Write([]byte("<13>1 2026-10-11T01:02:03Z host prog - - - udp"))
		_ = connection.Close()
		address, _ = hub.Address("syslog", push.TransportTCP)
		stream, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = stream.Write([]byte("<13>1 2026-10-11T01:02:03Z host prog - - - tcp\n"))
		_ = stream.Close()
	}
	send()
	waitFor(t, "two refusals of the loopback sender", func() bool {
		state := hub.Senders().State()
		return len(state.Refused) == 1 && state.Refused[0].Count >= 2
	})
	if _, _, stored := recorded.count(); stored != 0 {
		t.Errorf("a refused sender stored %d records", stored)
	}
	hub.Senders().Set([]netip.Addr{netip.MustParseAddr("127.0.0.1")})
	send()
	waitFor(t, "both messages stored", func() bool { _, _, stored := recorded.count(); return stored == 2 })
	cancel()
	hub.Wait()
}

// TestAConnectionBeyondTheBoundIsClosed is PS-AC5: the connection bound is enforced.
func TestAConnectionBeyondTheBoundIsClosed(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := push.NewHub(push.Options{})
	hub.Senders().Set([]netip.Addr{netip.MustParseAddr("127.0.0.1")})
	hub.Start(ctx)
	if err := hub.Listen(ctx, "syslog", push.TransportTCP, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	address, _ := hub.Address("syslog", push.TransportTCP)
	var held []net.Conn
	for range push.MaxConnections {
		connection, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, connection)
	}
	waitFor(t, "the connections to be served", func() bool {
		return listenerOf(hub, "syslog", push.TransportTCP).Connections == push.MaxConnections
	})
	extra, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	_ = extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Error("the connection beyond the bound was not closed")
	}
	waitFor(t, "the refusal to be counted", func() bool {
		return listenerOf(hub, "syslog", push.TransportTCP).Counters[push.CounterConnectionsRefused] == 1
	})
	for _, connection := range append(held, extra) {
		_ = connection.Close()
	}
	cancel()
	hub.Wait()
}

// TestAStalledStoreDropsAndRecordsAGap is PS-AC13: the queue fills, the drops are counted, and one
// push_buffer_overflow gap spans their reception instants; the queue never grows past its bound.
func TestAStalledStoreDropsAndRecordsAGap(t *testing.T) {
	t.Parallel()
	now := newClock()
	gaps := &gapRecorder{}
	hub := push.NewHub(push.Options{Now: now.Now, Gaps: gaps, SubscriberQueue: 10})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "s.lines", Protocol: testProtocol,
		Selector: compile(t, testProtocol, json.RawMessage(`"k"`)), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener(testProtocol, push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	var firstDrop, lastDrop time.Time
	for index := range 15 {
		if index == 10 {
			firstDrop = now.Now()
		}
		lastDrop = now.Now()
		_ = hub.Deliver(context.Background(), testProtocol, push.TransportUDP, []byte(fmt.Sprintf("k %d", index)), firewall)
		now.advance(time.Second)
	}
	state, _ := hub.SubscriptionState("s.lines")
	if state.Queued != 10 || state.Counters[push.GapBufferOverflow] != 5 {
		t.Errorf("queued %d, dropped %d", state.Queued, state.Counters[push.GapBufferOverflow])
	}
	hub.Tick(context.Background(), true)
	overflows := gaps.of(push.GapBufferOverflow)
	if len(overflows) != 1 || overflows[0].count != 5 || !overflows[0].from.Equal(firstDrop) ||
		!overflows[0].to.Equal(lastDrop) {
		t.Errorf("overflow gaps %+v", overflows)
	}
}

// TestAFailingStoreIsRetriedThenAGap is PS-AC13: a batch the store refuses is retried within its
// bound, then recorded as push_store_failed.
func TestAFailingStoreIsRetriedThenAGap(t *testing.T) {
	t.Parallel()
	gaps := &gapRecorder{}
	hub := push.NewHub(push.Options{Gaps: gaps, StoreBackoff: time.Millisecond})
	recorded := &recordingSink{fail: true}
	_ = hub.Subscribe(push.Subscription{ID: "f.lines", Protocol: testProtocol,
		Selector: compile(t, testProtocol, json.RawMessage(`"k"`)), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener(testProtocol, push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	_ = hub.Deliver(context.Background(), testProtocol, push.TransportUDP, []byte("k one"), firewall)
	hub.Flush(context.Background())
	if _, stores, _ := recorded.count(); stores != push.StoreRetries+1 {
		t.Errorf("the batch was tried %d times, want %d", stores, push.StoreRetries+1)
	}
	hub.Tick(context.Background(), true)
	if failed := gaps.of(push.GapStoreFailed); len(failed) != 1 || failed[0].count != 1 {
		t.Errorf("store failure gaps %+v", failed)
	}
}

// TestInsertsAreBatched is PS-AC13: N records are at most ceil(N / batch) stores.
func TestInsertsAreBatched(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{BatchRows: 100})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "b.lines", Protocol: testProtocol,
		Selector: compile(t, testProtocol, json.RawMessage(`"k"`)), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener(testProtocol, push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	for index := range 250 {
		_ = hub.Deliver(context.Background(), testProtocol, push.TransportUDP, []byte(fmt.Sprintf("k %d", index)), firewall)
	}
	hub.Flush(context.Background())
	if _, stores, stored := recorded.count(); stores != 3 || stored != 250 {
		t.Errorf("%d records in %d stores", stored, stores)
	}
}

// TestATrickleIsCommittedWithinTheTimeBound is PS-AC13: one record is stored within the batch
// interval, with no more records to fill a batch.
func TestATrickleIsCommittedWithinTheTimeBound(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := push.NewHub(push.Options{BatchInterval: 50 * time.Millisecond})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "t.lines", Protocol: testProtocol,
		Selector: compile(t, testProtocol, json.RawMessage(`"k"`)), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener(testProtocol, push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	hub.Start(ctx)
	_ = hub.Deliver(ctx, testProtocol, push.TransportUDP, []byte("k one"), firewall)
	waitFor(t, "the trickle's commit", func() bool { _, _, stored := recorded.count(); return stored == 1 })
	cancel()
	hub.Wait()
}

// TestLivenessHasHysteresis is PS-AC15's third clause, on a fake clock: a source sending a little
// slower than its span stays alive; silence past the span plus half of it ends it, once.
func TestLivenessHasHysteresis(t *testing.T) {
	t.Parallel()
	now := newClock()
	var mutex sync.Mutex
	var changes []bool
	hub := push.NewHub(push.Options{Now: now.Now, OnLiveness: func(_ string, alive bool, _ time.Time) {
		mutex.Lock()
		changes = append(changes, alive)
		mutex.Unlock()
	}})
	_ = hub.Subscribe(push.Subscription{ID: "l.lines", Protocol: testProtocol, Liveness: 10 * time.Second,
		Selector: compile(t, testProtocol, json.RawMessage(`"k"`)), Sink: sink{&recordingSink{}}})
	_ = hub.AddLoopbackListener(testProtocol, push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	for range 5 {
		_ = hub.Deliver(context.Background(), testProtocol, push.TransportUDP, []byte("k"), firewall)
		now.advance(12 * time.Second)
		hub.Tick(context.Background(), false)
	}
	now.advance(10 * time.Second)
	hub.Tick(context.Background(), false)
	mutex.Lock()
	defer mutex.Unlock()
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Errorf("liveness changes %v, want [true false]", changes)
	}
}

// history is a fake listener history.
type history struct {
	mutex sync.Mutex
	at    map[string]time.Time
}

func (h *history) ListenerAliveAt(_ context.Context, protocol, transport string) (time.Time, bool, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	at, found := h.at[protocol+"/"+transport]
	return at, found, nil
}

func (h *history) RecordListenerAlive(_ context.Context, protocol, transport string, at time.Time) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.at[protocol+"/"+transport] = at
	return nil
}

// TestTheListenerDownIntervalIsAGap is PS-AC13's last clause: the interval since the listener was
// last heard alive -- opnview's own restart included -- is a push_listener_down gap of its
// subscribers.
func TestTheListenerDownIntervalIsAGap(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := newClock()
	stoppedAt := now.Now().Add(-10 * time.Minute)
	gaps := &gapRecorder{}
	past := &history{at: map[string]time.Time{"syslog/udp": stoppedAt}}
	hub := push.NewHub(push.Options{Now: now.Now, Gaps: gaps, History: past})
	_ = hub.Subscribe(push.Subscription{ID: "d.lines", Protocol: "syslog", Selector: compile(t, "syslog",
		syslogFilter("prog")), Sink: sink{&recordingSink{}}})
	hub.Start(ctx)
	if err := hub.Listen(ctx, "syslog", push.TransportUDP, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	down := gaps.of(push.GapListenerDown)
	if len(down) != 1 || !down[0].from.Equal(stoppedAt) || !down[0].to.Equal(now.Now()) {
		t.Errorf("listener-down gaps %+v", down)
	}
	cancel()
	hub.Wait()
}

// TestZonelessInstantsAreResolvedBeforeAnySubscriber is PS-AC4's syslog half: an RFC 3164 instant is
// what the resolver -- the firewall's measured offset -- says.
func TestZonelessInstantsAreResolvedBeforeAnySubscriber(t *testing.T) {
	t.Parallel()
	resolved := time.Date(2026, 12, 31, 22, 59, 59, 0, time.UTC)
	hub := push.NewHub(push.Options{Resolve: func(_ context.Context, zoneless string) (time.Time, error) {
		if zoneless != "Dec 31 23:59:59" {
			return time.Time{}, fmt.Errorf("unexpected %q", zoneless)
		}
		return resolved, nil
	}})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "z.lines", Protocol: "syslog", Selector: compile(t, "syslog",
		syslogFilter("prog")), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener("syslog", push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	_ = hub.Deliver(context.Background(), "syslog", push.TransportUDP, []byte("<13>Dec 31 23:59:59 h prog: m"), firewall)
	hub.Flush(context.Background())
	recorded.mutex.Lock()
	defer recorded.mutex.Unlock()
	if len(recorded.records) != 1 || !recorded.records[0].Instant.Equal(resolved) ||
		!recorded.records[0].Fields["timestamp"].(time.Time).Equal(resolved) {
		t.Errorf("records %+v", recorded.records)
	}
}

// TestNoPushSocketWithoutAListener is PS-AC21's first clause, in the hub: with no Listen, nothing is
// bound and every declaration reports listener_not_configured.
func TestNoPushSocketWithoutAListener(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{})
	_ = hub.Subscribe(push.Subscription{ID: "n.lines", Protocol: "syslog", Selector: compile(t, "syslog",
		syslogFilter("prog")), Sink: sink{&recordingSink{}}})
	catalogue := hub.Catalogue()
	if len(catalogue.Listeners) != 0 || catalogue.Subscriptions[0].ListenerState != push.ListenerNotConfigured {
		t.Errorf("catalogue %+v", catalogue)
	}
}

// examplePacket is a NetFlow v9 packet of ng_netflow's IPv4 template and one record: input index 3,
// output index 1, documentation addresses (the example of docs/plugins.md, *A NetFlow subscriber*).
const examplePacket = "00090002000186a06ab85c800000000100000000000000580101001400080004000c0004000f0004000a0002000e00020002" +
	"0004000100040018000400170004001600040015000400070002000b0002000600010004000100050001001000040011000400090001000d" +
	"000101010040c633640acb00711400000000000300010000000a00003778000000000000000000009c4000017318ad2101bb1b06000000" +
	"0000000000001818000000"

// TestInterfaceIndexesAreMappedAtReceptionOrCounted is PS-AC17 in the hub: a mapped index gets its
// interface beside it, an unknown one keeps its raw value, has none, and is counted.
func TestInterfaceIndexesAreMappedAtReceptionOrCounted(t *testing.T) {
	t.Parallel()
	hub := push.NewHub(push.Options{Interfaces: func(context.Context) (map[uint64]push.Interface, error) {
		return map[uint64]push.Interface{3: {Identifier: "lan", Device: "exdev0"}}, nil
	}})
	recorded := &recordingSink{}
	_ = hub.Subscribe(push.Subscription{ID: "n.flows", Protocol: "netflow",
		Selector: compile(t, "netflow", json.RawMessage(`{"require": ["octetDeltaCount"]}`)), Sink: sink{recorded}})
	_ = hub.AddLoopbackListener("netflow", push.TransportUDP)
	hub.Senders().Set([]netip.Addr{firewall})
	packet, err := hex.DecodeString(examplePacket)
	if err != nil {
		t.Fatal(err)
	}
	_ = hub.Deliver(context.Background(), "netflow", push.TransportUDP, packet, firewall)
	hub.Flush(context.Background())
	recorded.mutex.Lock()
	defer recorded.mutex.Unlock()
	if len(recorded.records) != 1 {
		t.Fatalf("%d records", len(recorded.records))
	}
	fields := recorded.records[0].Fields
	if fields["ingressInterface"] != uint64(3) || fields["ingressInterface_identifier"] != "lan" ||
		fields["ingressInterface_device"] != "exdev0" {
		t.Errorf("the mapped index: %v", fields)
	}
	if fields["egressInterface"] != uint64(1) || fields["egressInterface_device"] != nil {
		t.Errorf("the unmapped index: %v", fields)
	}
	state, _ := hub.SubscriptionState("n.flows")
	if state.Counters[push.CounterInterfaceUnmapped] != 1 {
		t.Errorf("unmapped indexes counted %d", state.Counters[push.CounterInterfaceUnmapped])
	}
}
