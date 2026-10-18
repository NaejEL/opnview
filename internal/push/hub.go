package push

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// The hub: listeners, one decoding pass per message, routing by subscription, bounded queues,
// batched stores and the gaps every drop becomes (PS2 and PS7 of the push amendment).
//
// THE PATH OF ONE MESSAGE. A listener reads a datagram, or a frame of a TCP connection, from an
// accepted sender, and hands it to its listener's decode queue. ONE decoder per listener decodes it
// ONCE -- a syslog header, a NetFlow packet through its templates -- and routing looks its
// subscribers up by the record's route key (a syslog program), or asks the subscribers that index
// nothing (a NetFlow subscriber, whose templates are not known in advance). A record nobody
// subscribes to is counted and dropped: it costs one decode and one lookup, and no regular
// expression or script runs for it. Each subscriber's records wait in its own bounded queue, and
// its flusher hands them, a batch at a time, to its sink, which extracts its own declared columns
// and stores them in one transaction. Two subscribers of one program never share a row.
//
// EVERY QUEUE IS BOUNDED, AND EVERY DROP IS A GAP. The UDP socket's receive buffer, the decode queue,
// each subscriber's queue, the NetFlow records held for their template and the templates per
// exporter all have a bound written once below. A datagram that finds the decode queue full, and a
// record that finds its subscriber's queue full, is dropped, counted, and recorded as a gap of reason
// push_buffer_overflow over the reception instants of what was lost, merged within GapMergeWindow.
// A TCP frame is never dropped there: the connection's reader waits, and TCP's own flow control
// slows the sender.

// The reasons of the gaps the push sources record (docs/plugins.md, "Push sources"): the hub's own, and
// GapNotAlive, a subscription that stopped receiving, which the service records from the hub's liveness.
// Each protocol's decoder adds its own (GapReasoner); DeclaredGapReasons lists them all.
const (
	GapBufferOverflow = "push_buffer_overflow"
	GapStoreFailed    = "push_store_failed"
	GapListenerDown   = "push_listener_down"
	GapScriptFailed   = "push_script_failed"
	GapNotAlive       = "push_not_alive"
)

// The counters the hub keeps beside the decoders' problems.
const (
	CounterNoSubscriber       = "no_subscriber"
	CounterFrameTooLarge      = "frame_too_large"
	CounterConnectionsRefused = "connections_refused"
	CounterInstantUnresolved  = "instant_unresolved"
	CounterValueInvalid       = "push_value_invalid"
	// CounterSequenceNotAsserted counts a loss the sender's sequence numbers showed that a sink's
	// SequenceJudge refused to assert: no gap is recorded for it.
	CounterSequenceNotAsserted = "sequence_gap_not_asserted"
)

// The bounds, each written once (docs/plugins.md, "Push sources", lists them).
const (
	// ReceiveBufferBytes is the UDP socket's receive buffer asked of the operating system.
	ReceiveBufferBytes = 4 << 20
	// MaxDatagram bounds one UDP datagram.
	MaxDatagram = 65535
	// DecodeQueue bounds the datagrams or frames waiting for their listener's decoder.
	DecodeQueue = 4096
	// SubscriberQueue bounds the records waiting for one subscriber's flusher.
	SubscriberQueue = 50000
	// BatchRows is the most records one flush hands its sink, and BatchInterval the longest a record
	// waits for one: a trickle is committed within it.
	BatchRows     = 1000
	BatchInterval = time.Second
	// MaxFrame bounds one TCP frame: above RFC 5425's minimum of 2048 octets a receiver must accept.
	MaxFrame = 64 * 1024
	// MaxConnections bounds the TCP connections held per listener, and ConnectionIdle closes one
	// that has sent nothing for that long; the sender reconnects.
	MaxConnections = 16
	ConnectionIdle = 10 * time.Minute
	// HeldRecords and HeldSeconds bound the NetFlow data sets held for their template, per exporter
	// and domain; TemplatesPerExporter bounds the templates kept.
	HeldRecords          = 1024
	HeldSeconds          = 60
	TemplatesPerExporter = 256
	// StoreRetries is how many times a failing batch is retried, StoreBackoff the first wait.
	StoreRetries = 3
	StoreBackoff = 250 * time.Millisecond
	// GapMergeWindow merges drops of one reason closer than it into one gap.
	GapMergeWindow = 10 * time.Second
	// HeartbeatInterval is how often a listening listener records that it is alive, which bounds
	// the push_listener_down gap of a stop that recorded nothing.
	HeartbeatInterval = 30 * time.Second
	// LivenessFloor is the shortest liveness span a declaration may state, and DefaultLiveness the
	// span of one that states none. A source stops being alive only after its span plus half of it
	// (the hysteresis), so a sender close to its span does not flip at every message.
	LivenessFloor   = 10 * time.Second
	DefaultLiveness = 120 * time.Second
)

// Sink is where one subscription's records go: the subscribing plugin's own table, or a core
// kind's coordinator. Prepare extracts the subscriber's own columns from a batch -- its regular
// expression, its parse function, its element mapping -- and Store commits what Prepare made in one
// transaction, which the hub retries.
type Sink interface {
	Prepare(ctx context.Context, records []Record) Prepared
	Store(ctx context.Context, prepared Prepared) error
}

// SequenceJudge is a sink that decides whether a loss its sender's sequence numbers show is asserted
// at all: a core kind whose provider knows, from the configuration it read, whether the sender's
// sequence counters are bounded (NJ7 of the amendment "the NetFlow join with every interface
// captured"). A gap of a Sequence problem the judge refuses is counted (CounterSequenceNotAsserted) and
// not recorded.
type SequenceJudge interface {
	SequenceAsserted(ctx context.Context, problem Problem) bool
}

// ConditionObserver is a sink told every Condition a decoder detected on a stream it may feed.
type ConditionObserver interface {
	ObserveCondition(ctx context.Context, problem Problem)
}

// Prepared is what a sink extracted from one batch.
type Prepared struct {
	// Rows is how many rows Store will write; Payload is the sink's own.
	Rows    int
	Payload any
	// Counts are the batch's counters: skipped, push_value_invalid, script calls, ...
	Counts map[string]int64
	// Failed marks records lost by the extraction itself -- a script that threw or timed out --
	// recorded as a gap of reason Failed over their reception instants.
	Failed       int
	FailedReason string
}

// GapRecorder records a gap of one subscription.
type GapRecorder interface {
	RecordPushGap(ctx context.Context, subscription, reason string, from, to time.Time, count int64,
		detail string) error
}

// ListenerHistory remembers when each listener was last heard alive, across runs.
type ListenerHistory interface {
	ListenerAliveAt(ctx context.Context, protocol, transport string) (time.Time, bool, error)
	RecordListenerAlive(ctx context.Context, protocol, transport string, at time.Time) error
}

// LivenessObserver is told when a subscription becomes alive or stops being alive (PS10).
type LivenessObserver func(subscription string, alive bool, at time.Time)

// Subscription is one push declaration of one plugin, as the hub routes to it.
type Subscription struct {
	// ID names it: <plugin>.<entry>.
	ID       string
	Protocol string
	Selector Selector
	// Transports are the transports it accepts records from; empty accepts every one.
	Transports []string
	Sink       Sink
	// Liveness is its liveness span.
	Liveness time.Duration
	// Describe is what the catalogue says of it: the plugin, the destination, the columns.
	Describe SubscriptionDescription
}

// SubscriptionDescription is the catalogue's account of a subscription's declaration.
type SubscriptionDescription struct {
	Plugin      string   `json:"plugin"`
	Entry       string   `json:"entry"`
	OutputTable string   `json:"output_table,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Columns     []string `json:"columns"`
	Replaces    string   `json:"replaces,omitempty"`
}

// Options configure a hub.
type Options struct {
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Resolve turns a zoneless timestamp into an instant with the firewall's measured offset (PS5).
	Resolve func(ctx context.Context, zoneless string) (time.Time, error)
	Senders *Senders
	Gaps    GapRecorder
	History ListenerHistory
	// OnLiveness is told every liveness change; nil tells nobody.
	OnLiveness LivenessObserver
	// Report is told what goes wrong outside any request: a gap that could not be recorded.
	Report func(error)
	// BatchRows, BatchInterval and SubscriberQueue override the bounds, for tests.
	BatchRows       int
	BatchInterval   time.Duration
	SubscriberQueue int
	DecodeQueue     int
	// StoreBackoff overrides the first wait before a failed batch is retried, for tests.
	StoreBackoff time.Duration
}

// Hub is the push receiver of one process.
type Hub struct {
	options       Options
	mutex         sync.RWMutex
	subscriptions []*subscriber
	byProtocol    map[string]*route
	listeners     []*listener
	started       bool
	wait          sync.WaitGroup
}

// route is the routing index of one protocol.
type route struct {
	byKey   map[string][]*subscriber
	dynamic []*subscriber
	all     []*subscriber
}

// NewHub returns a hub with no subscription and no listener.
func NewHub(options Options) *Hub {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Senders == nil {
		options.Senders = NewSenders()
	}
	if options.BatchRows <= 0 {
		options.BatchRows = BatchRows
	}
	if options.BatchInterval <= 0 {
		options.BatchInterval = BatchInterval
	}
	if options.SubscriberQueue <= 0 {
		options.SubscriberQueue = SubscriberQueue
	}
	if options.DecodeQueue <= 0 {
		options.DecodeQueue = DecodeQueue
	}
	if options.StoreBackoff <= 0 {
		options.StoreBackoff = StoreBackoff
	}
	if options.Report == nil {
		options.Report = func(error) {}
	}
	return &Hub{options: options, byProtocol: map[string]*route{}}
}

// Senders is the hub's accepted-sender set.
func (h *Hub) Senders() *Senders { return h.options.Senders }

// Subscribe adds a subscription. It is refused once the hub has started, and for a protocol that is
// not registered.
func (h *Hub) Subscribe(subscription Subscription) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.started {
		return errors.New("push: a subscription is added before the hub starts")
	}
	if _, known := Lookup(subscription.Protocol); !known {
		return fmt.Errorf("push: the protocol %q is not registered", subscription.Protocol)
	}
	if subscription.Liveness <= 0 {
		subscription.Liveness = DefaultLiveness
	}
	s := &subscriber{Subscription: subscription, hub: h, counters: map[string]int64{},
		wake: make(chan struct{}, 1), gaps: map[string]*pendingGap{}}
	h.subscriptions = append(h.subscriptions, s)
	index := h.byProtocol[subscription.Protocol]
	if index == nil {
		index = &route{byKey: map[string][]*subscriber{}}
		h.byProtocol[subscription.Protocol] = index
	}
	index.all = append(index.all, s)
	keys := subscription.Selector.Keys()
	if keys == nil {
		index.dynamic = append(index.dynamic, s)
	}
	for _, key := range keys {
		index.byKey[key] = append(index.byKey[key], s)
	}
	return nil
}

// Start begins the hub's flushers and timers; listeners are opened with Listen. It returns when ctx
// is cancelled and everything it started has stopped, every queue flushed.
func (h *Hub) Start(ctx context.Context) {
	h.mutex.Lock()
	h.started = true
	subscribers := append([]*subscriber(nil), h.subscriptions...)
	h.mutex.Unlock()
	for _, s := range subscribers {
		h.wait.Add(1)
		go func() {
			defer h.wait.Done()
			s.flushLoop(ctx)
		}()
	}
	h.wait.Add(1)
	go func() {
		defer h.wait.Done()
		h.timers(ctx)
	}()
}

// Wait returns once everything Start and Listen started has stopped.
func (h *Hub) Wait() { h.wait.Wait() }

// timers expires what decoders hold, evaluates liveness and flushes merged gaps.
func (h *Hub) timers(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.Tick(context.WithoutCancel(ctx), true)
			return
		case <-ticker.C:
			h.Tick(ctx, false)
		}
	}
}

// Tick runs the timers once: expiry of what decoders hold, liveness, and the gaps whose merge
// window has passed (all of them when final). Tests call it with a fake clock.
func (h *Hub) Tick(ctx context.Context, final bool) {
	now := h.options.Now()
	h.mutex.RLock()
	listeners := append([]*listener(nil), h.listeners...)
	subscribers := append([]*subscriber(nil), h.subscriptions...)
	h.mutex.RUnlock()
	for _, l := range listeners {
		h.route(ctx, l, l.decoder.Expire(now))
	}
	for _, s := range subscribers {
		s.evaluateLiveness(now)
		s.flushGaps(ctx, now, final)
	}
}

// Listen opens one listener: a protocol on a transport at an address (host:port). It returns once
// the socket is bound, and serves until ctx is cancelled.
func (h *Hub) Listen(ctx context.Context, protocolName, transport, address string) error {
	protocol, known := Lookup(protocolName)
	if !known {
		return fmt.Errorf("push: the protocol %q is not registered (known: %v)", protocolName, Protocols())
	}
	served := false
	for _, offered := range protocol.Transports() {
		served = served || offered == transport
	}
	if !served {
		return fmt.Errorf("push: %s is not received over %s (it is over %v)", protocolName, transport,
			protocol.Transports())
	}
	l := &listener{hub: h, protocol: protocol, transport: transport,
		decoder: protocol.NewDecoder(Limits{HeldRecords: HeldRecords, HeldSeconds: HeldSeconds,
			TemplatesPerExporter: TemplatesPerExporter}),
		queue: make(chan packet, h.options.DecodeQueue), counters: map[string]int64{}}
	var config net.ListenConfig
	switch transport {
	case TransportUDP:
		connection, err := config.ListenPacket(ctx, "udp", address)
		if err != nil {
			return fmt.Errorf("push: listening for %s on udp %s: %w", protocolName, address, err)
		}
		if udp, ok := connection.(*net.UDPConn); ok {
			_ = udp.SetReadBuffer(ReceiveBufferBytes)
		}
		l.packetConn = connection
		l.address = connection.LocalAddr().String()
	case TransportTCP:
		socket, err := config.Listen(ctx, "tcp", address)
		if err != nil {
			return fmt.Errorf("push: listening for %s on tcp %s: %w", protocolName, address, err)
		}
		l.streamListener = socket
		l.address = socket.Addr().String()
	}
	h.mutex.Lock()
	h.listeners = append(h.listeners, l)
	h.mutex.Unlock()
	h.recordListenerDown(ctx, l)
	l.listening.Store(true)
	h.noteListening(l)
	h.wait.Add(3)
	go func() {
		defer h.wait.Done()
		l.decodeLoop(ctx)
	}()
	go func() {
		defer h.wait.Done()
		l.heartbeat(ctx)
	}()
	go func() {
		defer h.wait.Done()
		l.serve(ctx)
	}()
	return nil
}

// recordListenerDown records, for every subscriber the listener feeds, the interval since the
// listener was last heard alive -- opnview's own downtime between runs included.
func (h *Hub) recordListenerDown(ctx context.Context, l *listener) {
	if h.options.History == nil {
		return
	}
	now := h.options.Now()
	last, found, err := h.options.History.ListenerAliveAt(ctx, l.protocol.Name(), l.transport)
	if err != nil {
		h.options.Report(err)
		return
	}
	if found && now.After(last) {
		for _, s := range h.subscribersOf(l) {
			if h.options.Gaps == nil {
				continue
			}
			if err := h.options.Gaps.RecordPushGap(ctx, s.ID, GapListenerDown, last, now, 0,
				"the listener was not listening between these instants"); err != nil {
				h.options.Report(err)
			}
		}
	}
	if err := h.options.History.RecordListenerAlive(ctx, l.protocol.Name(), l.transport, now); err != nil {
		h.options.Report(err)
	}
}

// subscribersOf are the subscribers a listener can feed.
func (h *Hub) subscribersOf(l *listener) []*subscriber {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	index := h.byProtocol[l.protocol.Name()]
	if index == nil {
		return nil
	}
	var result []*subscriber
	for _, s := range index.all {
		if s.acceptsTransport(l.transport) {
			result = append(result, s)
		}
	}
	return result
}

// Address is the bound address of the first listener of a protocol and transport, for tests that
// listen on an ephemeral port.
func (h *Hub) Address(protocolName, transport string) (string, bool) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	for _, l := range h.listeners {
		if l.protocol.Name() == protocolName && l.transport == transport {
			return l.address, true
		}
	}
	return "", false
}

// route hands decoded records to their subscribers, and the decoder's problems to the counters and
// the gaps of every subscriber the listener feeds.
func (h *Hub) route(ctx context.Context, l *listener, decoded Decoded) {
	for _, problem := range decoded.Problems {
		l.count(problem.Reason, int64(problem.Count))
		if problem.Condition {
			for _, s := range h.subscribersOf(l) {
				if observer, observes := s.Sink.(ConditionObserver); observes {
					observer.ObserveCondition(ctx, problem)
				}
			}
			continue
		}
		if !problem.Gap {
			continue
		}
		for _, s := range h.subscribersOf(l) {
			if judge, judges := s.Sink.(SequenceJudge); judges && problem.Sequence &&
				!judge.SequenceAsserted(ctx, problem) {
				s.count(CounterSequenceNotAsserted, int64(problem.Count))
				continue
			}
			s.noteGap(ctx, problem.Reason, problem.From, problem.To, int64(problem.Count))
		}
	}
	if len(decoded.Records) == 0 {
		return
	}
	h.mutex.RLock()
	index := h.byProtocol[l.protocol.Name()]
	h.mutex.RUnlock()
	for i := range decoded.Records {
		record := &decoded.Records[i]
		delivered := false
		if index != nil {
			for _, s := range index.byKey[record.RouteKey] {
				delivered = s.offer(ctx, record) || delivered
			}
			for _, s := range index.dynamic {
				delivered = s.offer(ctx, record) || delivered
			}
		}
		if !delivered {
			l.count(CounterNoSubscriber, 1)
		}
	}
}

// SetSequenceCounters gives every decoder of a protocol that follows several sequence counters the
// number of counters its senders run, read from their configuration: a loss is asserted only once more
// are open (NJ7 of the amendment "the NetFlow join with every interface captured"). A protocol whose
// decoders follow no sequence is left alone.
func (h *Hub) SetSequenceCounters(protocolName string, counters int) {
	h.mutex.RLock()
	listeners := append([]*listener(nil), h.listeners...)
	h.mutex.RUnlock()
	for _, l := range listeners {
		if l.protocol.Name() != protocolName {
			continue
		}
		if bounder, bounds := l.decoder.(SequenceBounder); bounds {
			bounder.SetSequenceCounters(counters)
		}
	}
}

// ResetSequences tells every decoder of a protocol that follows sequence numbers to follow them
// afresh: what it concluded of a stream no longer holds once whoever reads the sender's configuration
// has seen it change, its counters recreated. A protocol whose decoders follow no sequence is left
// alone.
func (h *Hub) ResetSequences(protocolName string) {
	h.mutex.RLock()
	listeners := append([]*listener(nil), h.listeners...)
	h.mutex.RUnlock()
	for _, l := range listeners {
		if l.protocol.Name() != protocolName {
			continue
		}
		if resetter, follows := l.decoder.(SequenceResetter); follows {
			resetter.ResetSequences()
		}
	}
}

// packet is one datagram or frame waiting for its decoder.
type packet struct {
	payload []byte
	meta    Meta
}

// listener is one socket of one protocol on one transport.
type listener struct {
	hub            *Hub
	protocol       Protocol
	transport      string
	address        string
	decoder        Decoder
	queue          chan packet
	packetConn     net.PacketConn
	streamListener net.Listener
	listening      atomic.Bool
	connections    atomic.Int64
	mutex          sync.Mutex
	counters       map[string]int64
	received       atomic.Int64
	decodedPackets atomic.Int64
}

func (l *listener) count(name string, delta int64) {
	if delta == 0 {
		return
	}
	l.mutex.Lock()
	l.counters[name] += delta
	l.mutex.Unlock()
}

// serve reads the socket until ctx is cancelled.
func (l *listener) serve(ctx context.Context) {
	defer l.listening.Store(false)
	stop := context.AfterFunc(ctx, func() {
		if l.packetConn != nil {
			_ = l.packetConn.Close()
		}
		if l.streamListener != nil {
			_ = l.streamListener.Close()
		}
	})
	defer stop()
	if l.packetConn != nil {
		l.serveDatagrams(ctx)
		return
	}
	l.serveStreams(ctx)
}

func (l *listener) serveDatagrams(ctx context.Context) {
	buffer := make([]byte, MaxDatagram)
	for {
		n, from, err := l.packetConn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		sender := addressOf(from)
		if !l.hub.options.Senders.Accepts(sender) {
			continue
		}
		l.received.Add(1)
		received := l.hub.options.Now()
		item := packet{payload: append([]byte(nil), buffer[:n]...),
			meta: Meta{Sender: sender, Transport: TransportUDP, ReceivedAt: received}}
		select {
		case l.queue <- item:
		default:
			l.count(GapBufferOverflow, 1)
			for _, s := range l.hub.subscribersOf(l) {
				s.noteGap(ctx, GapBufferOverflow, received, received, 1)
			}
		}
	}
}

func (l *listener) serveStreams(ctx context.Context) {
	for {
		connection, err := l.streamListener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		sender := addressOf(connection.RemoteAddr())
		if !l.hub.options.Senders.Accepts(sender) {
			_ = connection.Close()
			continue
		}
		if l.connections.Load() >= MaxConnections {
			l.count(CounterConnectionsRefused, 1)
			_ = connection.Close()
			continue
		}
		l.connections.Add(1)
		l.hub.wait.Add(1)
		go func() {
			defer l.hub.wait.Done()
			defer l.connections.Add(-1)
			l.serveConnection(ctx, connection, sender)
		}()
	}
}

// idleReader closes nothing itself: it extends the connection's deadline before every read, so a
// connection silent for ConnectionIdle fails its read and is closed.
type idleReader struct{ connection net.Conn }

func (r idleReader) Read(buffer []byte) (int, error) {
	_ = r.connection.SetReadDeadline(time.Now().Add(ConnectionIdle))
	return r.connection.Read(buffer)
}

func (l *listener) serveConnection(ctx context.Context, connection net.Conn, sender netip.Addr) {
	defer func() { _ = connection.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	framer := l.protocol.NewFramer(idleReader{connection: connection}, MaxFrame)
	for {
		frame, err := framer.Next()
		if err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				l.count(CounterFrameTooLarge, 1)
			}
			return
		}
		l.received.Add(1)
		item := packet{payload: frame, meta: Meta{Sender: sender, Transport: TransportTCP,
			ReceivedAt: l.hub.options.Now()}}
		select {
		case l.queue <- item:
		case <-ctx.Done():
			return
		}
	}
}

// Deliver hands one datagram or frame to a listener's decoder as if its socket had read it, the
// sender check included: the path the plugin test world delivers recorded fixtures through.
func (h *Hub) Deliver(ctx context.Context, protocolName, transport string, payload []byte, sender netip.Addr) error {
	h.mutex.RLock()
	var target *listener
	for _, l := range h.listeners {
		if l.protocol.Name() == protocolName && l.transport == transport {
			target = l
		}
	}
	h.mutex.RUnlock()
	if target == nil {
		return fmt.Errorf("push: no %s listener on %s", protocolName, transport)
	}
	if !h.options.Senders.Accepts(sender) {
		return nil
	}
	target.received.Add(1)
	target.decodeOne(ctx, packet{payload: payload, meta: Meta{Sender: sender, Transport: transport,
		ReceivedAt: h.options.Now()}})
	return nil
}

// AddLoopbackListener registers a listener with no socket, fed only by Deliver: the plugin test
// world's, which never binds a port.
func (h *Hub) AddLoopbackListener(protocolName, transport string) error {
	protocol, known := Lookup(protocolName)
	if !known {
		return fmt.Errorf("push: the protocol %q is not registered", protocolName)
	}
	l := &listener{hub: h, protocol: protocol, transport: transport,
		decoder: protocol.NewDecoder(Limits{HeldRecords: HeldRecords, HeldSeconds: HeldSeconds,
			TemplatesPerExporter: TemplatesPerExporter}),
		counters: map[string]int64{}, address: "loopback"}
	l.listening.Store(true)
	h.mutex.Lock()
	h.listeners = append(h.listeners, l)
	h.mutex.Unlock()
	h.noteListening(l)
	return nil
}

// noteListening tells every subscriber a listener feeds that it is listening from now on, so a
// subscription that never receives anything has an instant its silence starts from.
func (h *Hub) noteListening(l *listener) {
	now := h.options.Now()
	for _, s := range h.subscribersOf(l) {
		s.listening(now)
	}
}

func (l *listener) decodeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// What is still queued is decoded and routed, so a stop loses nothing it accepted.
			for {
				select {
				case item := <-l.queue:
					l.decodeOne(context.WithoutCancel(ctx), item)
				default:
					return
				}
			}
		case item := <-l.queue:
			l.decodeOne(ctx, item)
		}
	}
}

func (l *listener) decodeOne(ctx context.Context, item packet) {
	l.decodedPackets.Add(1)
	l.hub.route(ctx, l, l.decoder.Decode(item.payload, item.meta))
}

func (l *listener) heartbeat(ctx context.Context) {
	history := l.hub.options.History
	if history == nil {
		return
	}
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := history.RecordListenerAlive(context.WithoutCancel(ctx), l.protocol.Name(), l.transport,
				l.hub.options.Now()); err != nil {
				l.hub.options.Report(err)
			}
			return
		case <-ticker.C:
			if err := history.RecordListenerAlive(ctx, l.protocol.Name(), l.transport, l.hub.options.Now()); err != nil {
				l.hub.options.Report(err)
			}
		}
	}
}

// addressOf reads the address of a socket address.
func addressOf(address net.Addr) netip.Addr {
	switch typed := address.(type) {
	case *net.UDPAddr:
		value, _ := netip.AddrFromSlice(typed.IP)
		return value.Unmap()
	case *net.TCPAddr:
		value, _ := netip.AddrFromSlice(typed.IP)
		return value.Unmap()
	}
	return netip.Addr{}
}
