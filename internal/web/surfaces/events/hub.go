package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The event types the stream carries. The first four are the notifications of
// internal/store/sqldb, one each; the last three are the stream's own.
const (
	// TypeCollectionPass is a collection pass of a core kind that stored its rows.
	TypeCollectionPass = "collection_pass"
	// TypePluginPass is a pass of a plugin's own kind that stored its rows.
	TypePluginPass = "plugin_pass"
	// TypeAggregateRefresh is an aggregate refresh that rewrote slots.
	TypeAggregateRefresh = "aggregate_refresh"
	// TypeLiveSample is a live pass that stored fine samples.
	TypeLiveSample = "live_sample"
	// TypeHello opens every stream.
	TypeHello = "hello"
	// TypeResync says events were lost for this reader: everything it shows may have changed.
	TypeResync = "resync"
	// TypeEnd closes a stream whose lifetime elapsed; the reader opens it again.
	TypeEnd = "end"
)

// Range is the span of the slots of one period an aggregate refresh rewrote.
type Range struct {
	Period string `json:"period"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
}

// Event is one event of the stream.
type Event struct {
	// Type is one of the Type constants, and the event's SSE name.
	Type string `json:"type"`
	// At is when the server relayed it, a UTC epoch in seconds.
	At int64 `json:"at"`
	// Kind and ProviderKey name the provider of a collection pass or a live sample.
	Kind        string `json:"kind,omitempty"`
	ProviderKey string `json:"provider_key,omitempty"`
	// Plugin and Provider name the provider of a plugin pass.
	Plugin   string `json:"plugin,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Periods, Ranges and Families say what an aggregate refresh rewrote.
	Periods  []string `json:"periods,omitempty"`
	Ranges   []Range  `json:"ranges,omitempty"`
	Families []string `json:"families,omitempty"`
	// SampledAt is the instant of a live sample's newest reading.
	SampledAt *int64 `json:"sampled_at,omitempty"`
	// HeartbeatSeconds and LifetimeSeconds are the stream's bounds, carried by hello.
	HeartbeatSeconds int64 `json:"heartbeat_seconds,omitempty"`
	LifetimeSeconds  int64 `json:"lifetime_seconds,omitempty"`
}

// translate turns one notification into the event it announces; false for a channel the stream
// does not relay.
func translate(notification sqldb.Notification, at int64) (Event, bool) {
	payload := notification.Payload
	switch notification.Channel {
	case sqldb.ChannelCollectionPass:
		return Event{Type: TypeCollectionPass, At: at, Kind: text(payload, "kind"),
			ProviderKey: text(payload, "provider_key")}, true
	case sqldb.ChannelPluginPass:
		return Event{Type: TypePluginPass, At: at, Plugin: text(payload, "plugin"),
			Provider: text(payload, "provider")}, true
	case sqldb.ChannelAggregateRefresh:
		event := Event{Type: TypeAggregateRefresh, At: at, Periods: texts(payload, "periods"),
			Families: texts(payload, "families")}
		listed, _ := payload["ranges"].([]any)
		for _, entry := range listed {
			span, isObject := entry.(map[string]any)
			if !isObject {
				continue
			}
			from, hasFrom := number(span, "from")
			to, hasTo := number(span, "to")
			if !hasFrom || !hasTo {
				continue
			}
			event.Ranges = append(event.Ranges, Range{Period: text(span, "period"), From: from, To: to})
		}
		return event, true
	case sqldb.ChannelLiveSample:
		event := Event{Type: TypeLiveSample, At: at, Kind: text(payload, "kind"),
			ProviderKey: text(payload, "provider_key")}
		if sampledAt, found := number(payload, "sampled_at"); found {
			event.SampledAt = &sampledAt
		}
		return event, true
	}
	return Event{}, false
}

func text(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func texts(payload map[string]any, key string) []string {
	listed, _ := payload[key].([]any)
	var result []string
	for _, entry := range listed {
		if value, isText := entry.(string); isText {
			result = append(result, value)
		}
	}
	return result
}

// number reads a whole number of a JSON payload, which decodes every number as a float64.
func number(payload map[string]any, key string) (int64, bool) {
	value, isNumber := payload[key].(float64)
	return int64(value), isNumber
}

// listener is what the hub hears notifications from: the store.
type listener interface {
	Listen(ctx context.Context, channels []string, handle func(sqldb.Notification)) error
}

// readerBuffer is how many events one stream holds before it loses some.
const readerBuffer = 64

// reader is one open stream.
type reader struct {
	events chan Event
	// lost says an event was dropped for it since it last said resync.
	lost atomic.Bool
}

// hub fans every notification out to the open streams, holding one listener while any is open.
type hub struct {
	source listener
	now    func() time.Time

	mutex   sync.Mutex
	readers map[*reader]struct{}
	stop    context.CancelFunc
	stopped chan struct{}
}

func newHub(source listener, now func() time.Time) *hub {
	return &hub{source: source, now: now, readers: map[*reader]struct{}{}}
}

// subscribe opens a reader, and the listener with the first.
func (h *hub) subscribe() *reader {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	opened := &reader{events: make(chan Event, readerBuffer)}
	h.readers[opened] = struct{}{}
	if h.stop == nil {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		h.stop, h.stopped = cancel, stopped
		go func() {
			defer close(stopped)
			// Listen opens a dropped connection again itself and returns only once ctx ends.
			_ = h.source.Listen(ctx, sqldb.Channels(), h.publish)
		}()
	}
	return opened
}

// unsubscribe closes a reader, and the listener with the last, waiting for it to end.
func (h *hub) unsubscribe(closed *reader) {
	h.mutex.Lock()
	delete(h.readers, closed)
	var stopped chan struct{}
	if len(h.readers) == 0 && h.stop != nil {
		h.stop()
		stopped = h.stopped
		h.stop, h.stopped = nil, nil
	}
	h.mutex.Unlock()
	// Waited for outside the lock: the listener's own last delivery takes it.
	if stopped != nil {
		<-stopped
	}
}

// publish hands one notification to every open reader, never blocking on one.
func (h *hub) publish(notification sqldb.Notification) {
	event, relayed := translate(notification, h.now().Unix())
	if !relayed {
		return
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for open := range h.readers {
		select {
		case open.events <- event:
		default:
			open.lost.Store(true)
		}
	}
}
