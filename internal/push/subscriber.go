package push

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// subscriber is one subscription's queue, flusher, counters, liveness and pending gaps.
type subscriber struct {
	Subscription
	hub *Hub

	mutex    sync.Mutex
	queue    []Record
	counters map[string]int64
	wake     chan struct{}
	// lastAccepted is the reception instant of the last record admitted; alive is the liveness
	// state, aliveSince when it began.
	lastAccepted time.Time
	alive        bool
	aliveSince   time.Time
	gaps         map[string]*pendingGap
	flushes      int64
	stored       int64
}

// pendingGap is a run of drops of one reason, merged while they are closer than GapMergeWindow.
type pendingGap struct {
	from, to time.Time
	count    int64
	lastSeen time.Time
}

func (s *subscriber) acceptsTransport(transport string) bool {
	if len(s.Transports) == 0 {
		return true
	}
	for _, accepted := range s.Transports {
		if accepted == transport {
			return true
		}
	}
	return false
}

// offer admits a record into the subscriber's queue, or counts why not.
func (s *subscriber) offer(ctx context.Context, record *Record) bool {
	admitted, reason := s.Selector.Admits(record)
	if !admitted {
		if reason != "" {
			s.count(reason, 1)
		}
		return false
	}
	if !s.acceptsTransport(record.Transport) {
		s.count("transport_not_accepted_"+record.Transport, 1)
		return true
	}
	s.mutex.Lock()
	becameAlive := false
	if record.ReceivedAt.After(s.lastAccepted) {
		s.lastAccepted = record.ReceivedAt
	}
	if !s.alive {
		s.alive, s.aliveSince, becameAlive = true, record.ReceivedAt, true
	}
	if len(s.queue) >= s.hub.options.SubscriberQueue {
		s.counters[GapBufferOverflow]++
		s.mutex.Unlock()
		s.noteGap(ctx, GapBufferOverflow, record.ReceivedAt, record.ReceivedAt, 1)
		s.announce(becameAlive, record.ReceivedAt)
		return true
	}
	s.queue = append(s.queue, *record)
	s.counters["admitted"]++
	full := len(s.queue) >= s.hub.options.BatchRows
	s.mutex.Unlock()
	s.announce(becameAlive, record.ReceivedAt)
	if full {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return true
}

func (s *subscriber) announce(becameAlive bool, at time.Time) {
	if becameAlive && s.hub.options.OnLiveness != nil {
		s.hub.options.OnLiveness(s.ID, true, at)
	}
}

func (s *subscriber) count(name string, delta int64) {
	s.mutex.Lock()
	s.counters[name] += delta
	s.mutex.Unlock()
}

// evaluateLiveness ends the alive state once nothing was admitted for the span plus its hysteresis.
func (s *subscriber) evaluateLiveness(now time.Time) {
	s.mutex.Lock()
	ended := false
	if s.alive && now.Sub(s.lastAccepted) > s.Liveness+s.Liveness/2 {
		s.alive = false
		ended = true
	}
	last := s.lastAccepted
	s.mutex.Unlock()
	if ended && s.hub.options.OnLiveness != nil {
		s.hub.options.OnLiveness(s.ID, false, last)
	}
}

// Liveness is a subscription's liveness: whether it is alive, since when, and the last record.
type Liveness struct {
	Alive        bool   `json:"alive"`
	Since        *int64 `json:"alive_since_at,omitempty"`
	LastAccepted *int64 `json:"last_accepted_at,omitempty"`
	SpanSeconds  int64  `json:"liveness_seconds"`
}

func (s *subscriber) liveness() Liveness {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	state := Liveness{Alive: s.alive, SpanSeconds: int64(s.Liveness / time.Second)}
	if s.alive {
		since := s.aliveSince.Unix()
		state.Since = &since
	}
	if !s.lastAccepted.IsZero() {
		last := s.lastAccepted.Unix()
		state.LastAccepted = &last
	}
	return state
}

// flushLoop flushes the queue at BatchRows records or every BatchInterval, whichever comes first,
// and once more when ctx ends.
func (s *subscriber) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(s.hub.options.BatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			s.Flush(ctx)
		case <-s.wake:
			s.Flush(ctx)
		}
	}
}

// Flush stores everything queued, a batch of at most BatchRows at a time.
func (s *subscriber) Flush(ctx context.Context) {
	for {
		s.mutex.Lock()
		if len(s.queue) == 0 {
			s.mutex.Unlock()
			return
		}
		size := min(len(s.queue), s.hub.options.BatchRows)
		batch := append([]Record(nil), s.queue[:size]...)
		s.queue = append(s.queue[:0], s.queue[size:]...)
		s.mutex.Unlock()
		s.flushBatch(ctx, batch)
	}
}

func (s *subscriber) flushBatch(ctx context.Context, batch []Record) {
	resolved := batch[:0]
	for _, record := range batch {
		if record.Zoneless != "" {
			if s.hub.options.Resolve == nil {
				s.count(CounterInstantUnresolved, 1)
				continue
			}
			instant, err := s.hub.options.Resolve(ctx, record.Zoneless)
			if err != nil {
				s.count(CounterInstantUnresolved, 1)
				continue
			}
			record.Instant = instant.UTC()
			if record.InstantField != "" {
				fields := make(map[string]any, len(record.Fields)+1)
				for name, value := range record.Fields {
					fields[name] = value
				}
				fields[record.InstantField] = record.Instant
				record.Fields = fields
			}
			record.Zoneless = ""
		}
		resolved = append(resolved, record)
	}
	if len(resolved) == 0 {
		return
	}
	s.mapIndexes(ctx, resolved)
	prepared := s.Sink.Prepare(ctx, resolved)
	s.mutex.Lock()
	for name, delta := range prepared.Counts {
		s.counters[name] += delta
	}
	s.mutex.Unlock()
	if prepared.Failed > 0 {
		from, to := span(resolved)
		reason := prepared.FailedReason
		if reason == "" {
			reason = GapScriptFailed
		}
		s.noteGap(ctx, reason, from, to, int64(prepared.Failed))
	}
	if prepared.Rows == 0 && prepared.Payload == nil {
		return
	}
	var err error
	backoff := s.hub.options.StoreBackoff
	for attempt := 0; attempt <= StoreRetries; attempt++ {
		if err = s.Sink.Store(ctx, prepared); err == nil {
			break
		}
		if attempt == StoreRetries || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
		}
		backoff *= 2
	}
	s.mutex.Lock()
	s.flushes++
	if err == nil {
		s.stored += int64(prepared.Rows)
	} else {
		s.counters[GapStoreFailed] += int64(len(resolved))
	}
	s.mutex.Unlock()
	if err != nil {
		from, to := span(resolved)
		s.noteGap(ctx, GapStoreFailed, from, to, int64(len(resolved)))
		s.hub.options.Report(fmt.Errorf("push: %s: a batch of %d records could not be stored: %w", s.ID,
			len(resolved), err))
	}
}

// span is the interval of a batch's reception instants.
func span(records []Record) (time.Time, time.Time) {
	from, to := records[0].ReceivedAt, records[0].ReceivedAt
	for _, record := range records[1:] {
		if record.ReceivedAt.Before(from) {
			from = record.ReceivedAt
		}
		if record.ReceivedAt.After(to) {
			to = record.ReceivedAt
		}
	}
	return from, to
}

// noteGap adds a loss to the pending gap of its reason, merging it with the previous one when they
// are within GapMergeWindow, and records the previous one otherwise.
func (s *subscriber) noteGap(ctx context.Context, reason string, from, to time.Time, count int64) {
	s.mutex.Lock()
	pending := s.gaps[reason]
	var finished *pendingGap
	switch {
	case pending == nil:
		s.gaps[reason] = &pendingGap{from: from, to: to, count: count, lastSeen: s.hub.options.Now()}
	case from.Sub(pending.to) <= GapMergeWindow && pending.from.Sub(to) <= GapMergeWindow:
		if from.Before(pending.from) {
			pending.from = from
		}
		if to.After(pending.to) {
			pending.to = to
		}
		pending.count += count
		pending.lastSeen = s.hub.options.Now()
	default:
		finished = pending
		s.gaps[reason] = &pendingGap{from: from, to: to, count: count, lastSeen: s.hub.options.Now()}
	}
	s.mutex.Unlock()
	if finished != nil {
		s.recordGap(ctx, reason, finished)
	}
}

// flushGaps records every pending gap whose merge window has passed, or all of them when final.
func (s *subscriber) flushGaps(ctx context.Context, now time.Time, final bool) {
	s.mutex.Lock()
	var finished []struct {
		reason string
		gap    *pendingGap
	}
	for reason, pending := range s.gaps {
		if final || now.Sub(pending.lastSeen) > GapMergeWindow {
			finished = append(finished, struct {
				reason string
				gap    *pendingGap
			}{reason, pending})
			delete(s.gaps, reason)
		}
	}
	s.mutex.Unlock()
	for _, item := range finished {
		s.recordGap(ctx, item.reason, item.gap)
	}
}

func (s *subscriber) recordGap(ctx context.Context, reason string, gap *pendingGap) {
	if s.hub.options.Gaps == nil {
		return
	}
	detail := fmt.Sprintf("%d records or messages were lost (%s)", gap.count, reason)
	if err := s.hub.options.Gaps.RecordPushGap(ctx, s.ID, reason, gap.from, gap.to, gap.count, detail); err != nil {
		s.hub.options.Report(err)
	}
}

// mapIndexes adds, beside every interface index a record carries, the firewall's interface it maps
// to -- the mapping in force when the batch is flushed, within a second of reception, so a later
// renumbering never rewrites a stored row. An index no interface holds is kept, its interface left
// absent, and counted; the core never matches an index by position, name or count (PS11).
func (s *subscriber) mapIndexes(ctx context.Context, records []Record) {
	protocol, known := Lookup(s.Protocol)
	if !known || len(protocol.IndexFields()) == 0 {
		return
	}
	var interfaces map[uint64]Interface
	if s.hub.options.Interfaces != nil {
		read, err := s.hub.options.Interfaces(ctx)
		if err != nil {
			s.hub.options.Report(fmt.Errorf("push: reading the interface indexes: %w", err))
		}
		interfaces = read
	}
	for index := range records {
		fields := records[index].Fields
		var added map[string]any
		for _, field := range protocol.IndexFields() {
			raw, present := fields[field].(uint64)
			if !present {
				continue
			}
			mapped, found := interfaces[raw]
			if !found {
				s.count(CounterInterfaceUnmapped, 1)
				continue
			}
			if added == nil {
				added = make(map[string]any, len(fields)+4)
				for name, value := range fields {
					added[name] = value
				}
			}
			added[field+IndexIdentifierSuffix] = mapped.Identifier
			added[field+IndexDeviceSuffix] = mapped.Device
		}
		if added != nil {
			records[index].Fields = added
		}
	}
}
