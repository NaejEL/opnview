package push

import (
	"context"
	"sort"
)

// The push entries of the API catalogue (PS13 of the push amendment): one per listener and one per
// declaration. NAMES, COUNTS AND STATES ONLY: no message, no field value, no credential and no base
// URL. The refused senders are listed by address, because a container network rewriting source
// addresses must be visible at once (PS6).

// Catalogue is the push part of the API catalogue.
type Catalogue struct {
	Listeners     []ListenerEntry     `json:"listeners"`
	Subscriptions []SubscriptionEntry `json:"subscriptions"`
	Senders       SendersState        `json:"senders"`
	// Protocols are the protocols this build decodes.
	Protocols []string `json:"protocols"`
}

// ListenerEntry is one listener.
type ListenerEntry struct {
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`
	// State is listening or stopped.
	State string `json:"state"`
	// Received counts the datagrams or frames accepted from the firewall, Decoded those decoded.
	Received int64 `json:"received"`
	Decoded  int64 `json:"decoded"`
	// Connections is the TCP connections open now.
	Connections int64 `json:"connections"`
	// Counters are the drops and problems by reason.
	Counters map[string]int64 `json:"counters"`
	// Seen are the programs or templates seen, with the records decoded under each.
	Seen map[string]int64 `json:"seen"`
	// Flags are conditions the decoder detected.
	Flags []string `json:"flags"`
	// LossDetectable says whether a loss on the network is detectable on this listener.
	LossDetectable bool `json:"loss_detectable"`
}

// SubscriptionEntry is one push declaration.
type SubscriptionEntry struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	SubscriptionDescription
	Transports []string `json:"transports"`
	// ListenerState is listening, or listener_not_configured when no listener of its protocol on
	// any transport it accepts is open: a silence then is not an absence of data.
	ListenerState string   `json:"listener_state"`
	Liveness      Liveness `json:"liveness"`
	// Stored counts the rows stored, Flushes the batches.
	Stored  int64 `json:"stored"`
	Flushes int64 `json:"flushes"`
	// Queued is how many records wait for the flusher now.
	Queued   int              `json:"queued"`
	Counters map[string]int64 `json:"counters"`
}

// The listener states.
const (
	ListenerListening     = "listening"
	ListenerStopped       = "stopped"
	ListenerNotConfigured = "listener_not_configured"
)

// Catalogue reports every listener and subscription.
func (h *Hub) Catalogue() Catalogue {
	h.mutex.RLock()
	listeners := append([]*listener(nil), h.listeners...)
	subscribers := append([]*subscriber(nil), h.subscriptions...)
	h.mutex.RUnlock()
	catalogue := Catalogue{Listeners: []ListenerEntry{}, Subscriptions: []SubscriptionEntry{},
		Senders: h.options.Senders.State(), Protocols: Protocols()}
	for _, l := range listeners {
		state := l.decoder.State()
		entry := ListenerEntry{Protocol: l.protocol.Name(), Transport: l.transport, State: ListenerStopped,
			Received: l.received.Load(), Decoded: l.decodedPackets.Load(), Connections: l.connections.Load(),
			Counters: map[string]int64{}, Seen: state.Seen, Flags: state.Flags,
			LossDetectable: l.protocol.LossDetectable(l.transport)}
		if entry.Seen == nil {
			entry.Seen = map[string]int64{}
		}
		if entry.Flags == nil {
			entry.Flags = []string{}
		}
		if l.listening.Load() {
			entry.State = ListenerListening
		}
		l.mutex.Lock()
		for name, count := range l.counters {
			entry.Counters[name] = count
		}
		l.mutex.Unlock()
		catalogue.Listeners = append(catalogue.Listeners, entry)
	}
	for _, s := range subscribers {
		catalogue.Subscriptions = append(catalogue.Subscriptions, h.subscriptionEntry(s))
	}
	sort.Slice(catalogue.Subscriptions, func(i, j int) bool {
		return catalogue.Subscriptions[i].ID < catalogue.Subscriptions[j].ID
	})
	return catalogue
}

func (h *Hub) subscriptionEntry(s *subscriber) SubscriptionEntry {
	entry := SubscriptionEntry{ID: s.ID, Protocol: s.Protocol, SubscriptionDescription: s.Describe,
		Transports: append([]string{}, s.Transports...), ListenerState: h.ListenerState(s.ID),
		Liveness: s.liveness(), Counters: map[string]int64{}}
	if entry.Columns == nil {
		entry.Columns = []string{}
	}
	s.mutex.Lock()
	entry.Stored, entry.Flushes, entry.Queued = s.stored, s.flushes, len(s.queue)
	for name, count := range s.counters {
		entry.Counters[name] = count
	}
	s.mutex.Unlock()
	return entry
}

// ListenerState is a subscription's listener state: listening when a listener of its protocol on a
// transport it accepts is open, listener_not_configured otherwise.
func (h *Hub) ListenerState(subscription string) string {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	var found *subscriber
	for _, s := range h.subscriptions {
		if s.ID == subscription {
			found = s
		}
	}
	if found == nil {
		return ListenerNotConfigured
	}
	for _, l := range h.listeners {
		if l.protocol.Name() == found.Protocol && found.acceptsTransport(l.transport) && l.listening.Load() {
			return ListenerListening
		}
	}
	return ListenerNotConfigured
}

// SubscriptionState is one subscription's state, for whoever reports its availability.
func (h *Hub) SubscriptionState(subscription string) (SubscriptionEntry, bool) {
	h.mutex.RLock()
	var found *subscriber
	for _, s := range h.subscriptions {
		if s.ID == subscription {
			found = s
		}
	}
	h.mutex.RUnlock()
	if found == nil {
		return SubscriptionEntry{}, false
	}
	return h.subscriptionEntry(found), true
}

// Flush stores everything every subscriber holds, now: for tests and for an orderly stop.
func (h *Hub) Flush(ctx context.Context) {
	h.mutex.RLock()
	subscribers := append([]*subscriber(nil), h.subscriptions...)
	h.mutex.RUnlock()
	for _, s := range subscribers {
		s.Flush(ctx)
	}
}
