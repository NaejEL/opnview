package events

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The stream's own mechanics, against a listener the test drives: hello, one event per
// notification, the resync of a reader that fell behind, the heartbeat and the end, and the one
// listener held only while a stream is open. The database round trip is events_test.go's.

// fakeListener hands the test the handler the hub listens with.
type fakeListener struct {
	mutex   sync.Mutex
	handle  func(sqldb.Notification)
	started chan struct{}
	ended   chan struct{}
	opened  int
}

func newFakeListener() *fakeListener {
	return &fakeListener{started: make(chan struct{}, 8), ended: make(chan struct{}, 8)}
}

func (f *fakeListener) Listen(ctx context.Context, _ []string, handle func(sqldb.Notification)) error {
	f.mutex.Lock()
	f.handle = handle
	f.opened++
	f.mutex.Unlock()
	f.started <- struct{}{}
	<-ctx.Done()
	f.ended <- struct{}{}
	return nil
}

func (f *fakeListener) deliver(notification sqldb.Notification) {
	f.mutex.Lock()
	handle := f.handle
	f.mutex.Unlock()
	handle(notification)
}

func wait(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen in five seconds", what)
	}
}

// received is one event read off a stream.
type received struct {
	name  string
	event Event
}

// openStream opens a stream on a server answering the handler alone and returns what it reads, a
// heartbeat counted as an event named "heartbeat".
func openStream(t *testing.T, s *surface) <-chan received {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(s.handleEvents))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		server.Close()
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := answer.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("the stream is served as %q", got)
	}
	return readStream(answer)
}

// readStream parses a text/event-stream body into events, until it ends.
func readStream(answer *http.Response) <-chan received {
	out := make(chan received, 256)
	go func() {
		defer close(out)
		defer func() { _ = answer.Body.Close() }()
		scanner := bufio.NewScanner(answer.Body)
		name, data := "", ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if name != "" {
					var event Event
					_ = json.Unmarshal([]byte(data), &event)
					out <- received{name: name, event: event}
				}
				name, data = "", ""
			case strings.HasPrefix(line, ": heartbeat"):
				out <- received{name: "heartbeat"}
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

func next(t *testing.T, stream <-chan received) received {
	t.Helper()
	select {
	case got, open := <-stream:
		if !open {
			t.Fatal("the stream ended")
		}
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the stream carried nothing in five seconds")
		return received{}
	}
}

func testSurface(source listener, heartbeat, lifetime time.Duration) *surface {
	clock := func() time.Time { return time.Unix(1790001800, 0) }
	return &surface{hub: newHub(source, clock), now: clock, heartbeat: heartbeat, lifetime: lifetime}
}

// TestAStreamOpensWithHelloAndRelaysEveryNotificationAsItsEvent: each channel the core publishes on
// is one event type, carrying what its notification names.
func TestAStreamOpensWithHelloAndRelaysEveryNotificationAsItsEvent(t *testing.T) {
	t.Parallel()
	source := newFakeListener()
	stream := openStream(t, testSurface(source, time.Hour, time.Hour))
	hello := next(t, stream)
	if hello.name != TypeHello || hello.event.HeartbeatSeconds != 3600 || hello.event.LifetimeSeconds != 3600 ||
		hello.event.At != 1790001800 {
		t.Fatalf("the stream opened with %+v", hello)
	}
	wait(t, source.started, "the listener's start")

	source.deliver(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "firewall_log", "provider_key": "example_log"}})
	if got := next(t, stream); got.name != TypeCollectionPass || got.event.Kind != "firewall_log" ||
		got.event.ProviderKey != "example_log" {
		t.Errorf("a collection pass was relayed as %+v", got)
	}
	source.deliver(sqldb.Notification{Channel: sqldb.ChannelPluginPass,
		Payload: map[string]any{"plugin": "example_plugin", "provider": "example_reader"}})
	if got := next(t, stream); got.name != TypePluginPass || got.event.Plugin != "example_plugin" ||
		got.event.Provider != "example_reader" {
		t.Errorf("a plugin pass was relayed as %+v", got)
	}
	source.deliver(sqldb.Notification{Channel: sqldb.ChannelAggregateRefresh, Payload: map[string]any{
		"periods":  []any{"1h"},
		"ranges":   []any{map[string]any{"period": "1h", "from": float64(1789999200), "to": float64(1790002800)}},
		"families": []any{"volume", "peer"}}})
	got := next(t, stream)
	if got.name != TypeAggregateRefresh || len(got.event.Periods) != 1 || got.event.Periods[0] != "1h" ||
		len(got.event.Ranges) != 1 || got.event.Ranges[0] != (Range{Period: "1h", From: 1789999200, To: 1790002800}) ||
		strings.Join(got.event.Families, ",") != "volume,peer" {
		t.Errorf("an aggregate refresh was relayed as %+v", got)
	}
	source.deliver(sqldb.Notification{Channel: sqldb.ChannelLiveSample, Payload: map[string]any{
		"kind": "measurement_sample", "provider_key": "example_rates", "sampled_at": float64(1790001795)}})
	got = next(t, stream)
	if got.name != TypeLiveSample || got.event.Kind != "measurement_sample" || got.event.ProviderKey != "example_rates" ||
		got.event.SampledAt == nil || *got.event.SampledAt != 1790001795 {
		t.Errorf("a live sample was relayed as %+v", got)
	}
	// A channel the stream does not know is not relayed.
	source.deliver(sqldb.Notification{Channel: "elsewhere", Payload: map[string]any{}})
	source.deliver(sqldb.Notification{Channel: sqldb.ChannelCollectionPass, Payload: map[string]any{"kind": "sentinel"}})
	if got := next(t, stream); got.event.Kind != "sentinel" {
		t.Errorf("an unknown channel was relayed as %+v", got)
	}
}

// TestAReaderThatFellBehindIsToldToResync: an event a full reader cannot take is lost, and the
// reader is told so rather than left believing it saw everything.
func TestAReaderThatFellBehindIsToldToResync(t *testing.T) {
	t.Parallel()
	h := newHub(newFakeListener(), time.Now)
	open := h.subscribe()
	defer h.unsubscribe(open)
	for index := 0; index < readerBuffer+5; index++ {
		h.publish(sqldb.Notification{Channel: sqldb.ChannelCollectionPass, Payload: map[string]any{"kind": "firewall_log"}})
	}
	if len(open.events) != readerBuffer || !open.lost.Load() {
		t.Fatalf("a full reader holds %d events, lost %v", len(open.events), open.lost.Load())
	}

	source := newFakeListener()
	s := testSurface(source, time.Hour, time.Hour)
	stream := openStream(t, s)
	next(t, stream)
	wait(t, source.started, "the listener's start")
	s.hub.mutex.Lock()
	for reader := range s.hub.readers {
		reader.lost.Store(true)
	}
	s.hub.mutex.Unlock()
	source.deliver(sqldb.Notification{Channel: sqldb.ChannelCollectionPass, Payload: map[string]any{"kind": "dhcp_lease"}})
	if got := next(t, stream); got.name != TypeCollectionPass {
		t.Fatalf("the event before the resync was %+v", got)
	}
	if got := next(t, stream); got.name != TypeResync {
		t.Errorf("a reader that lost events was told %+v, not resync", got)
	}
}

// TestAStreamBeatsAndEndsAfterItsLifetime: a heartbeat between events, and end once the lifetime
// has elapsed, after which the server closes the stream.
func TestAStreamBeatsAndEndsAfterItsLifetime(t *testing.T) {
	t.Parallel()
	stream := openStream(t, testSurface(newFakeListener(), 50*time.Millisecond, 400*time.Millisecond))
	next(t, stream)
	if got := next(t, stream); got.name != "heartbeat" {
		t.Errorf("an idle stream carried %+v, not a heartbeat", got)
	}
	for {
		got := next(t, stream)
		if got.name == "heartbeat" {
			continue
		}
		if got.name != TypeEnd {
			t.Fatalf("the stream carried %+v before its end", got)
		}
		break
	}
	select {
	case _, open := <-stream:
		if open {
			t.Error("the stream carried something after its end")
		}
	case <-time.After(5 * time.Second):
		t.Error("the server did not close the stream after its end")
	}
}

// TestOneListenerIsHeldOnlyWhileAStreamIsOpen: two streams share one listener, which ends with the
// last of them and opens again with the next.
func TestOneListenerIsHeldOnlyWhileAStreamIsOpen(t *testing.T) {
	t.Parallel()
	source := newFakeListener()
	h := newHub(source, time.Now)
	first := h.subscribe()
	second := h.subscribe()
	wait(t, source.started, "the listener's start")
	h.unsubscribe(first)
	select {
	case <-source.ended:
		t.Fatal("the listener ended while a stream was still open")
	case <-time.After(50 * time.Millisecond):
	}
	h.unsubscribe(second)
	wait(t, source.ended, "the listener's end")
	third := h.subscribe()
	wait(t, source.started, "the listener's second start")
	h.unsubscribe(third)
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if source.opened != 2 {
		t.Errorf("the listener was opened %d times, not twice", source.opened)
	}
}
