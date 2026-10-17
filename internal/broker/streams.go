package broker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// Continuous event streams (A14 of the broker amendment).
//
// ONE SUBSCRIPTION PER STREAM AND PARAMETERS, fanned out to every subscriber. It opens at the
// first subscriber and closes when the last lets its subscription lapse -- unless it feeds a table,
// when a loaded plugin reads its fields, and then it stays open while that plugin is loaded. A
// dropped subscription reconnects with back-off, and the interval with no events is recorded as a
// gap, never as zeros. The events a manifest declares to discard after each connection are
// discarded: the built-in insight plugin declares one for cpu_usage/stream, whose first event is
// the average since boot.
//
// A reader never waits on the firewall for a stream: it reads the events received since a cursor
// it holds, and the bounded read of host.stream is answered from the shared subscription.

// StreamEvent is one event received.
type StreamEvent struct {
	Sequence   int64
	ReceivedAt int64
	Data       []byte
	// Value is the event parsed, nil when it is not JSON.
	Value *answer.Value
}

// subscriptionLapse is how long a subscription lives after its last use.
const subscriptionLapse = 2 * time.Minute

// bufferSize is how many events a subscription keeps for its readers.
const bufferSize = 4096

// subscription is one stream held open.
type subscription struct {
	key      string
	endpoint opnsense.Endpoint
	options  opnsense.RequestOptions

	subscribers map[string]time.Time
	feeds       map[string]bool

	events   []StreamEvent
	sequence int64
	notify   chan struct{}

	connected   bool
	outcome     opnsense.Outcome
	statusCode  int
	detail      string
	cancel      context.CancelFunc
	running     bool
	failures    int
	retryAt     time.Time
	cutAt       int64
	openedAt    time.Time
	lastEventAt int64
	discard     int
	discarded   int
	intervals   []float64
	connections int
}

// streams holds the subscriptions.
type streams struct {
	broker *Broker
	mutex  sync.Mutex
	held   map[string]*subscription
	wake   chan struct{}
}

func newStreams(broker *Broker) *streams {
	return &streams{broker: broker, held: map[string]*subscription{}, wake: make(chan struct{}, 1)}
}

// streamKey is a stream's identity: its path and its parameters.
func streamKey(endpoint opnsense.Endpoint, options opnsense.RequestOptions) string {
	key := endpoint.Path
	if len(options.Arguments) > 0 {
		key += "/" + strings.Join(options.Arguments, "/")
	}
	if len(options.Query) > 0 {
		key += "?" + options.Query.Encode()
	}
	return key
}

// subscribe registers or renews a requester's subscription to a stream.
func (s *streams) subscribe(requester string, endpoint opnsense.Endpoint, options opnsense.RequestOptions) *subscription {
	key := streamKey(endpoint, options)
	s.mutex.Lock()
	held, found := s.held[key]
	if !found {
		held = &subscription{key: key, endpoint: endpoint, options: options, subscribers: map[string]time.Time{},
			feeds: map[string]bool{}, notify: make(chan struct{})}
		if declared := s.broker.declarationOf(endpoint.Path); declared != nil {
			held.discard = declared.discard
		}
		s.held[key] = held
	}
	now := s.broker.clock.Now()
	held.subscribers[requester] = now
	if !held.running && !now.Before(held.retryAt) {
		// The first subscriber opens it at once; the run loop reconnects and closes.
		s.open(s.broker.baseContext(), held)
	}
	s.mutex.Unlock()
	s.signal()
	return held
}

// Unsubscribe ends a requester's subscription at once.
func (b *Broker) Unsubscribe(requester string, endpoint opnsense.Endpoint, options opnsense.RequestOptions) {
	key := streamKey(endpoint, options)
	b.streams.mutex.Lock()
	if held, found := b.streams.held[key]; found {
		delete(held.subscribers, requester)
	}
	b.streams.mutex.Unlock()
	b.streams.signal()
}

// Feed marks a stream as feeding a table for a reader that reads its fields, which keeps it open
// while that reader is loaded; unfeed releases it.
func (b *Broker) Feed(requester string, endpoint opnsense.Endpoint, options opnsense.RequestOptions, feeding bool) {
	key := streamKey(endpoint, options)
	b.streams.mutex.Lock()
	if held, found := b.streams.held[key]; found {
		if feeding {
			held.feeds[requester] = true
		} else {
			delete(held.feeds, requester)
		}
	}
	b.streams.mutex.Unlock()
	b.streams.signal()
}

func (s *streams) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run opens the subscriptions that have subscribers, closes those that have none, and reconnects
// a dropped one after its back-off, until the context ends.
func (s *streams) run(ctx context.Context) {
	for {
		wait := s.step(ctx)
		select {
		case <-ctx.Done():
			s.mutex.Lock()
			for _, held := range s.held {
				if held.cancel != nil {
					held.cancel()
				}
			}
			s.mutex.Unlock()
			return
		case <-s.wake:
		case <-s.broker.clock.After(wait):
		}
	}
}

func (s *streams) step(ctx context.Context) time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := s.broker.clock.Now()
	next := time.Second
	for key, held := range s.held {
		for requester, renewed := range held.subscribers {
			if now.Sub(renewed) > subscriptionLapse {
				delete(held.subscribers, requester)
			}
		}
		wanted := len(held.subscribers) > 0 || len(held.feeds) > 0
		switch {
		case !wanted && held.running:
			held.cancel()
			held.running, held.connected = false, false
		case !wanted && !held.running:
			delete(s.held, key)
		case wanted && !held.running:
			if now.Before(held.retryAt) {
				if wait := held.retryAt.Sub(now); wait < next {
					next = wait
				}
				continue
			}
			s.open(ctx, held)
		}
	}
	return next
}

// open starts one subscription's connection.
func (s *streams) open(ctx context.Context, held *subscription) {
	listenCtx, cancel := context.WithCancel(ctx)
	held.cancel, held.running = cancel, true
	held.connections++
	broker := s.broker
	go func() {
		opened := false
		response, err := broker.client.Listen(listenCtx, held.endpoint, held.options, func() {
			s.mutex.Lock()
			held.connected, held.outcome, held.detail = true, opnsense.OutcomeOK, ""
			held.openedAt = broker.clock.Now()
			held.discarded = 0
			held.failures = 0
			opened = true
			s.mutex.Unlock()
		}, func(data []byte) {
			s.received(held, data)
		})
		broker.mutex.Lock()
		broker.counters.Calls++
		broker.counters.PerEndpoint[held.endpoint.Path]++
		broker.mutex.Unlock()
		s.mutex.Lock()
		defer s.mutex.Unlock()
		wasRunning := held.running
		held.running, held.connected = false, false
		// A reader waiting on the stream learns that it ended.
		close(held.notify)
		held.notify = make(chan struct{})
		if response.Outcome != "" {
			held.outcome, held.statusCode, held.detail = response.Outcome, response.StatusCode, response.Detail
		}
		if err != nil && response.Outcome == "" && listenCtx.Err() == nil {
			held.outcome, held.detail = opnsense.OutcomeTransportFailure, err.Error()
		}
		if !wasRunning || listenCtx.Err() != nil && ctx.Err() != nil {
			return
		}
		now := broker.clock.Now()
		if opened {
			held.cutAt = now.Unix()
			cutoff := now.Sub(held.openedAt).Seconds()
			broker.recordCutoff(ctx, held.endpoint, cutoff)
		}
		held.failures++
		held.retryAt = now.Add(backoffOf(broker.scheduler.Settings().BackoffBase,
			broker.scheduler.Settings().BackoffMax, held.failures))
		s.signal()
	}()
}

// backoffOf doubles a base per failure, up to a maximum.
func backoffOf(base, maximum time.Duration, failures int) time.Duration {
	wait := base
	for index := 1; index < failures && wait < maximum; index++ {
		wait *= 2
	}
	if wait > maximum {
		wait = maximum
	}
	return wait
}

// received records one event: discarded when the manifest says the connection begins with it,
// else buffered for the readers, a gap recorded when it ends a silence the subscription was cut.
func (s *streams) received(held *subscription, data []byte) {
	now := s.broker.clock.Now().Unix()
	s.mutex.Lock()
	if held.discarded < held.discard {
		held.discarded++
		s.mutex.Unlock()
		return
	}
	if held.lastEventAt > 0 {
		held.intervals = append(held.intervals, float64(now-held.lastEventAt))
		if len(held.intervals) > 64 {
			held.intervals = held.intervals[len(held.intervals)-64:]
		}
	}
	cut := held.cutAt
	previous := held.lastEventAt
	held.cutAt = 0
	held.lastEventAt = now
	held.sequence++
	event := StreamEvent{Sequence: held.sequence, ReceivedAt: now, Data: append([]byte(nil), data...)}
	if value, err := answer.Parse(data); err == nil {
		event.Value = value
	}
	held.events = append(held.events, event)
	if len(held.events) > bufferSize {
		held.events = held.events[len(held.events)-bufferSize:]
	}
	notify := held.notify
	held.notify = make(chan struct{})
	feeding := len(held.feeds) > 0
	key := held.key
	s.mutex.Unlock()
	close(notify)
	if cut > 0 && previous > 0 {
		s.broker.recordStreamGap(key, previous, now)
	}
	if feeding {
		s.broker.recordEvent(key, held.endpoint, event)
	}
}

// Stream answers a bounded read of a stream from the shared subscription: the events received
// from now, until the bound's number of events or its time.
func (b *Broker) Stream(ctx context.Context, requester Requester, endpoint opnsense.Endpoint,
	bound opnsense.StreamBound) (opnsense.StreamResponse, error) {
	response := opnsense.StreamResponse{Endpoint: endpoint}
	if reason := CheckRead(endpoint.Method, endpoint.Path); reason != "" {
		b.refuseWrite(ctx, Request{Requester: requester, Endpoint: endpoint}, reason)
		return response, &ErrWriteRefused{Requester: requester.Name, Method: endpoint.Method, Path: endpoint.Path,
			Reason: reason}
	}
	if bound.Events <= 0 || bound.Within <= 0 {
		return response, fmt.Errorf("broker: a stream read of %s needs a positive event count and time", endpoint.Path)
	}
	held := b.streams.subscribe(requester.Name, endpoint, opnsense.RequestOptions{})
	b.streams.mutex.Lock()
	start := held.sequence
	b.streams.mutex.Unlock()
	// A bound is a wait on the wall clock: events arrive in real time, whatever clock dates them.
	deadline := time.After(bound.Within)
	for {
		b.streams.mutex.Lock()
		var events [][]byte
		for _, event := range held.events {
			if event.Sequence > start && len(events) < bound.Events {
				events = append(events, event.Data)
			}
		}
		notify := held.notify
		outcome, status, detail, connected, running := held.outcome, held.statusCode, held.detail, held.connected,
			held.running
		ended := held.connections > 0 && !running && !connected && outcome == opnsense.OutcomeOK
		b.streams.mutex.Unlock()
		if len(events) >= bound.Events {
			response.Outcome, response.StatusCode, response.Events = opnsense.OutcomeOK, 200, events
			response.EndedBy = opnsense.StreamEndedAtEventLimit
			return response, nil
		}
		if !running && !connected && outcome != "" && outcome != opnsense.OutcomeOK {
			response.Outcome, response.StatusCode, response.Detail = outcome, status, detail
			response.Events = events
			return response, nil
		}
		if ended {
			// The firewall closed the stream before the bound was reached.
			response.Outcome, response.StatusCode, response.Events = opnsense.OutcomeOK, 200, events
			response.EndedBy = opnsense.StreamEndedByServer
			return response, nil
		}
		select {
		case <-notify:
		case <-deadline:
			response.Outcome, response.StatusCode, response.Events = opnsense.OutcomeOK, 200, events
			response.EndedBy = opnsense.StreamEndedAtTimeLimit
			if !connected {
				response.Detail = "the stream did not open within the bound"
			}
			return response, nil
		case <-ctx.Done():
			return response, ctx.Err()
		}
	}
}

// EventsAfter returns a stream's events received after a sequence a reader holds, and whether the
// subscription is connected. It subscribes the reader, or renews its subscription.
func (b *Broker) EventsAfter(requester Requester, endpoint opnsense.Endpoint, options opnsense.RequestOptions,
	after int64) ([]StreamEvent, bool) {
	held := b.streams.subscribe(requester.Name, endpoint, options)
	b.streams.mutex.Lock()
	defer b.streams.mutex.Unlock()
	var events []StreamEvent
	for _, event := range held.events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return events, held.connected
}

// replacing returns the stream that replaces a polled endpoint for a reader while it runs: a stream
// the reader declared equivalent, subscribed to and connected. Equivalence is declared, never
// guessed.
func (s *streams) replacing(requester, path string) string {
	broker := s.broker
	broker.mutex.Lock()
	var candidates []string
	for streamPath, held := range broker.declared {
		if replaced, found := held.replaces[requester]; found && replaced == path {
			candidates = append(candidates, streamPath)
		}
	}
	broker.mutex.Unlock()
	sort.Strings(candidates)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, candidate := range candidates {
		for _, held := range s.held {
			if held.endpoint.Path == candidate && held.connected {
				if _, subscribed := held.subscribers[requester]; subscribed {
					return candidate
				}
			}
		}
	}
	return ""
}

// recordCutoff records how long a stream's connection lasted before the server cut it.
func (b *Broker) recordCutoff(ctx context.Context, endpoint opnsense.Endpoint, seconds float64) {
	if b.mirror == nil {
		return
	}
	endpointID, err := b.mirror.EndpointID(ctx, mirror.Endpoint{Path: endpoint.Path, Method: endpoint.Method,
		Encoding: encodingOf(endpoint)})
	if err != nil {
		return
	}
	_, _ = b.mirror.Exec(ctx, `UPDATE opnsense.mirror_endpoint SET observed_stream = true,
		stream_cutoff_seconds = greatest(coalesce(stream_cutoff_seconds, 0), CAST($1 AS double precision)) WHERE id = $2`,
		seconds, endpointID)
}

// recordStreamGap records the silence of a cut stream as a gap.
func (b *Broker) recordStreamGap(key string, from, to int64) {
	if b.mirror == nil {
		return
	}
	_, err := b.mirror.Exec(context.Background(), `INSERT INTO opnsense.mirror_gap (stream, from_at, to_at, reason,
		recorded_at) VALUES ($1, $2, $3, 'stream_cut', $4)`, key, from, to, b.now())
	if err != nil {
		b.options.Log(fmt.Sprintf("recording a gap of %s: %v", key, err))
	}
}

// StreamGaps returns a stream's recorded gaps meeting an interval.
func (b *Broker) StreamGaps(ctx context.Context, endpoint opnsense.Endpoint, options opnsense.RequestOptions,
	from, to int64) ([]mirror.Gap, error) {
	if b.mirror == nil {
		return []mirror.Gap{}, nil
	}
	rows, err := b.mirror.Query(ctx, `SELECT from_at, to_at, reason FROM opnsense.mirror_gap WHERE stream = $1
		AND to_at >= $2 AND from_at <= $3 ORDER BY from_at`, streamKey(endpoint, options), from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	gaps := []mirror.Gap{}
	for rows.Next() {
		var gap mirror.Gap
		if err := rows.Scan(&gap.From, &gap.To, &gap.Reason); err != nil {
			return nil, err
		}
		gaps = append(gaps, gap)
	}
	return gaps, rows.Err()
}

// SubscriptionState is one subscription as the catalogue reports it.
type SubscriptionState struct {
	Key         string  `json:"stream"`
	Subscribers int     `json:"subscribers"`
	Feeding     bool    `json:"feeds_a_table"`
	Connected   bool    `json:"connected"`
	Connections int     `json:"connections"`
	Events      int64   `json:"events_received"`
	IntervalMS  float64 `json:"median_interval_ms"`
}

// Subscriptions returns the subscriptions held.
func (b *Broker) Subscriptions() []SubscriptionState {
	b.streams.mutex.Lock()
	defer b.streams.mutex.Unlock()
	var states []SubscriptionState
	for _, held := range b.streams.held {
		state := SubscriptionState{Key: held.key, Subscribers: len(held.subscribers), Feeding: len(held.feeds) > 0,
			Connected: held.connected, Connections: held.connections, Events: held.sequence}
		if len(held.intervals) > 0 {
			sorted := append([]float64(nil), held.intervals...)
			sort.Float64s(sorted)
			state.IntervalMS = sorted[len(sorted)/2] * 1000
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Key < states[j].Key })
	return states
}
