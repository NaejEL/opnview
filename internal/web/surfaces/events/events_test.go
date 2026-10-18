package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/surfaces/events"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The event stream over the real server and the real database: behind the session, and carrying
// what a commit announces -- a collection pass, an aggregate refresh, a live sample -- to an open
// stream.

type received struct {
	name  string
	event events.Event
}

// open opens the stream as the harness's reader and returns its events, the hello included.
func open(t *testing.T, harness *webtest.Harness) <-chan received {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	// Registered after the harness's own cleanups, so it runs first: the stream is closed before the
	// server waits for its requests to end.
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, harness.HTTP.URL+events.PathEvents, nil)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := harness.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if answer.StatusCode != http.StatusOK || !strings.HasPrefix(answer.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("the stream answered %d %q", answer.StatusCode, answer.Header.Get("Content-Type"))
	}
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
					var event events.Event
					_ = json.Unmarshal([]byte(data), &event)
					out <- received{name: name, event: event}
				}
				name, data = "", ""
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

// listening waits until the stream's listener hears the database: it publishes a probe until the
// probe comes back.
func listening(t *testing.T, harness *webtest.Harness, stream <-chan received) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := harness.Store.NotifyCollectionPass(context.Background(), "probe", "probe"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-stream:
			if got.name == events.TypeCollectionPass && got.event.Kind == "probe" {
				// Drain the probes still in flight.
				for {
					select {
					case <-stream:
						continue
					case <-time.After(200 * time.Millisecond):
					}
					return
				}
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("the stream heard nothing from the database in ten seconds")
}

func nextNamed(t *testing.T, stream <-chan received, name string) received {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got, isOpen := <-stream:
			if !isOpen {
				t.Fatalf("the stream ended before a %s event", name)
			}
			if got.name == name {
				return got
			}
		case <-deadline:
			t.Fatalf("no %s event in ten seconds", name)
			return received{}
		}
	}
}

// TestTheEventStreamRefusesAReaderWithoutASession: the stream is authenticated like every read.
func TestTheEventStreamRefusesAReaderWithoutASession(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	anonymous := webtest.FreshClient(t, harness)
	answer := anonymous.Get(events.PathEvents)
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusUnauthorized || !strings.Contains(body, `"condition"`) {
		t.Errorf("a stream without a session answered %d %s", answer.StatusCode, body)
	}
}

// TestACommitReachesAnOpenStreamAsItsEvent: a committed collection pass, aggregate refresh and live
// sample each reach an open stream, the refresh with the range of the current hour it rewrote.
func TestACommitReachesAnOpenStreamAsItsEvent(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	stream := open(t, harness)
	if hello := nextNamed(t, stream, events.TypeHello); hello.event.LifetimeSeconds == 0 || hello.event.HeartbeatSeconds == 0 {
		t.Errorf("the stream opened with %+v", hello.event)
	}
	listening(t, harness, stream)
	ctx := context.Background()

	if err := harness.Store.NotifyCollectionPass(ctx, "firewall_log", "example_log"); err != nil {
		t.Fatal(err)
	}
	if got := nextNamed(t, stream, events.TypeCollectionPass); got.event.Kind != "firewall_log" ||
		got.event.ProviderKey != "example_log" {
		t.Errorf("the collection pass reached the stream as %+v", got.event)
	}

	now := time.Now().Unix()
	if _, err := harness.Store.RefreshAggregates(ctx, now, now, nil); err != nil {
		t.Fatal(err)
	}
	got := nextNamed(t, stream, events.TypeAggregateRefresh)
	hour := vocab.PeriodHour.SlotStart(now)
	found := false
	for _, span := range got.event.Ranges {
		if span.Period == vocab.PeriodHour.Name && span.From <= hour && span.To >= hour+3600 {
			found = true
		}
	}
	if !found || len(got.event.Families) == 0 {
		t.Errorf("the refresh reached the stream as %+v, without the current hour or its families", got.event)
	}

	if err := harness.Store.NotifyLiveSample(ctx, "measurement_sample", "example_rates", now); err != nil {
		t.Fatal(err)
	}
	if got := nextNamed(t, stream, events.TypeLiveSample); got.event.SampledAt == nil || *got.event.SampledAt != now {
		t.Errorf("the live sample reached the stream as %+v", got.event)
	}
}
