package broker_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The stream tests (BR-AC18 to BR-AC21): a fake firewall stream the test feeds event by event,
// and the broker's subscriptions over it.

// waitFor polls a condition on the wall clock: events arrive over a real connection.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stream makes a fake stream endpoint and returns its event channel.
func (w *world) stream(path string) (opnsense.Endpoint, chan string) {
	w.t.Helper()
	ep := endpoint(w.t, http.MethodGet, path, opnsense.NoBody)
	return ep, w.replaceStream(ep)
}

// replaceStream gives a stream path a fresh event channel, for its next connection.
func (w *world) replaceStream(ep opnsense.Endpoint) chan string {
	events := make(chan string, 16)
	w.fake.mutex.Lock()
	w.fake.streams[ep.Path] = events
	w.fake.mutex.Unlock()
	return events
}

// openConnections is how many connections the fake holds open to a stream path.
func (w *world) openConnections(path string) int {
	w.fake.mutex.Lock()
	defer w.fake.mutex.Unlock()
	return w.fake.open[path]
}

// run runs the broker's loops for the test.
func (w *world) run() {
	ctx, cancel := context.WithCancel(context.Background())
	go w.broker.Run(ctx)
	w.t.Cleanup(cancel)
}

// connected reports whether the broker holds a stream connected.
func (w *world) connected(ep opnsense.Endpoint) bool {
	for _, state := range w.broker.Subscriptions() {
		if state.Key == ep.Path && state.Connected {
			return true
		}
	}
	return false
}

// TestOneSubscriptionIsSharedByEveryReader is BR-AC18.
func TestOneSubscriptionIsSharedByEveryReader(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.run()
	ep, events := w.stream("/api/example/shared/stream")
	readers := []string{"example_one", "example_two", "example_three"}
	for _, reader := range readers {
		w.broker.EventsAfter(broker.Requester{Name: reader}, ep, opnsense.RequestOptions{}, 0)
	}
	waitFor(t, "the stream to connect", func() bool { return w.connected(ep) })
	for index := 1; index <= 3; index++ {
		events <- fmt.Sprintf(`{"n": %d}`, index)
	}
	for _, reader := range readers {
		waitFor(t, reader+" to receive every event", func() bool {
			got, _ := w.broker.EventsAfter(broker.Requester{Name: reader}, ep, opnsense.RequestOptions{}, 0)
			return len(got) == 3
		})
	}
	if count := w.fake.count(ep.Path); count != 1 || w.openConnections(ep.Path) != 1 {
		t.Errorf("three readers opened %d connections (%d open)", count, w.openConnections(ep.Path))
	}
	for _, reader := range readers {
		w.broker.Unsubscribe(reader, ep, opnsense.RequestOptions{})
	}
	waitFor(t, "the connection to close after the last reader", func() bool { return w.openConnections(ep.Path) == 0 })

	// A reader that reads fields of the stream keeps it open while it is loaded.
	fed, _ := w.stream("/api/example/fed/stream")
	requester := broker.Requester{Name: "example_feeder", Version: "1"}
	w.broker.EventsAfter(requester, fed, opnsense.RequestOptions{}, 0)
	waitFor(t, "the fed stream to connect", func() bool { return w.connected(fed) })
	reads := answer.NewReads()
	reads.Field("n")
	w.broker.StreamDone(context.Background(), requester, fed, opnsense.RequestOptions{}, reads)
	w.broker.Unsubscribe(requester.Name, fed, opnsense.RequestOptions{})
	time.Sleep(100 * time.Millisecond)
	if w.openConnections(fed.Path) != 1 {
		t.Error("a stream feeding a table closed when its reader unsubscribed")
	}
	w.broker.Feed(requester.Name, fed, opnsense.RequestOptions{}, false)
	waitFor(t, "the fed stream to close once unfed", func() bool { return w.openConnections(fed.Path) == 0 })
}

// recordStream subscribes a reader that reads the field v of a stream, so the stream is recorded.
func (w *world) recordStream(ep opnsense.Endpoint) broker.Requester {
	w.t.Helper()
	requester := broker.Requester{Name: "example_recorder", Version: "1"}
	w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
	waitFor(w.t, "the stream to connect", func() bool { return w.connected(ep) })
	reads := answer.NewReads()
	reads.Field("v")
	w.broker.StreamDone(context.Background(), requester, ep, opnsense.RequestOptions{}, reads)
	return requester
}

// send sends an event and waits until the broker has received it.
func (w *world) send(requester broker.Requester, ep opnsense.Endpoint, events chan string, event string) {
	w.t.Helper()
	before, _ := w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
	events <- event
	waitFor(w.t, "the event "+event, func() bool {
		got, _ := w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
		return len(got) > len(before)
	})
}

// detailRows counts a stream's recorded detail rows, waiting for the recording to catch up.
func (w *world) detailRows(ep opnsense.Endpoint, want int) {
	w.t.Helper()
	table := streamTableOf(ep)
	waitFor(w.t, fmt.Sprintf("%d recorded events", want), func() bool {
		var count int
		err := w.store.DB().QueryRow(`SELECT count(*) FROM "opnsense".` + table).Scan(&count)
		return err == nil && count == want
	})
}

// streamTableOf is the quoted name of a stream's detail table, as the catalogue lists it.
func streamTableOf(ep opnsense.Endpoint) string {
	return `"` + strings.ReplaceAll(strings.Trim(ep.Path, "/"), "/", "_") + `__events"`
}

// TestAStreamCutIsAGapNeverAZero is BR-AC19.
func TestAStreamCutIsAGapNeverAZero(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep, events := w.stream("/api/example/cut/stream")
	w.broker.Declare("example_recorder", false, ep, broker.EndpointDeclaration{Discard: 1})
	requester := w.recordStream(ep)
	start := w.clock.Now().Unix()
	// The first event of a connection is declared discarded: it is never stored.
	events <- `{"v": 999}`
	for second := 0; second <= 60; second += 10 {
		w.send(requester, ep, events, fmt.Sprintf(`{"v": %d}`, second))
		w.clock.advance(10 * time.Second)
	}
	w.detailRows(ep, 7)
	// The fake closes the stream after 60 s and refuses the first reconnection.
	w.fake.mutex.Lock()
	w.fake.refuse[ep.Path] = 1
	w.fake.mutex.Unlock()
	next := w.replaceStream(ep)
	close(events)
	waitFor(t, "the cut to be seen", func() bool { return !w.connected(ep) && w.openConnections(ep.Path) == 0 })
	w.clock.advance(3 * time.Second)
	w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
	waitFor(t, "the refused reconnection", func() bool { return w.fake.count(ep.Path) == 2 })
	if w.connected(ep) {
		t.Fatal("a refused reconnection is connected")
	}
	w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
	time.Sleep(50 * time.Millisecond)
	if w.fake.count(ep.Path) != 2 {
		t.Error("the subscription reconnected inside its back-off")
	}
	w.clock.advance(5 * time.Minute)
	w.broker.EventsAfter(requester, ep, opnsense.RequestOptions{}, 0)
	waitFor(t, "the reconnection", func() bool { return w.connected(ep) })
	resumed := w.clock.Now().Unix()
	next <- `{"v": 998}`
	w.send(requester, ep, next, `{"v": 1}`)
	w.detailRows(ep, 8)

	gaps, err := w.broker.StreamGaps(context.Background(), ep, opnsense.RequestOptions{}, start, resumed)
	if err != nil || len(gaps) != 1 || gaps[0].From != start+60 || gaps[0].To != resumed {
		t.Errorf("the cut is recorded as %+v (%v), not as the silence from %d to %d", gaps, err, start+60, resumed)
	}
	table := streamTableOf(ep)
	var inside, discarded int
	_ = w.store.DB().QueryRow(`SELECT count(*) FILTER (WHERE "#received_at" > ? AND "#received_at" < ?),
		count(*) FILTER (WHERE v IN (999, 998)) FROM "opnsense".`+table, start+60, resumed).Scan(&inside, &discarded)
	if inside != 0 || discarded != 0 {
		t.Errorf("the silence holds %d rows and the discarded events %d", inside, discarded)
	}
	for _, suffix := range []string{"#minute", "#hour"} {
		var zeros int
		aggregate := `"opnsense"."` + table[1:len(table)-1] + suffix + `"`
		_ = w.store.DB().QueryRow(`SELECT count(*) FROM `+aggregate+` WHERE "#count" = 0 OR ("#bucket" > ? AND "#bucket" + 60 <= ?
			AND ? = '#minute')`, start+60, resumed-resumed%60, suffix).Scan(&zeros)
		if zeros != 0 {
			t.Errorf("the %s aggregate holds %d rows for the silence", suffix, zeros)
		}
	}
	var listed *dataset.Declaration
	for _, declaration := range w.broker.Datasets(context.Background()) {
		if declaration.Name == dataset.OriginOpnsense+"."+table[1:len(table)-1] {
			declaration := declaration
			listed = &declaration
		}
	}
	if listed == nil || listed.Nature != dataset.NatureSample {
		t.Errorf("the recording is catalogued as %+v, not as a sample dataset whose empty buckets are gaps", listed)
	}
}

// TestAStreamIsRecordedAggregatedAndPurgedOnItsOwnClock is BR-AC20.
func TestAStreamIsRecordedAggregatedAndPurgedOnItsOwnClock(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep, events := w.stream("/api/example/recorded/stream")
	requester := w.recordStream(ep)
	start := w.clock.Now().Unix()
	values := []int{5, 3, 9, 4, 7, 1, 8, 2, 6, 10, 11, 12}
	for _, value := range values {
		w.send(requester, ep, events, fmt.Sprintf(`{"v": %d, "label": "x"}`, value))
		w.clock.advance(25 * time.Second)
	}
	w.detailRows(ep, len(values))
	table := streamTableOf(ep)
	var received int64
	if err := w.store.DB().QueryRow(`SELECT min("#received_at") FROM "opnsense".` + table).Scan(&received); err != nil ||
		received != start {
		t.Errorf("the first event is timestamped %d, not at its reception %d (%v)", received, start, err)
	}
	for _, period := range []struct {
		suffix string
		width  int64
	}{{"#minute", 60}, {"#hour", 3600}} {
		aggregate := `"opnsense"."` + table[1:len(table)-1] + period.suffix + `"`
		var mismatches int
		err := w.store.DB().QueryRow(`WITH independent AS (
				SELECT "#received_at" - "#received_at" % CAST(? AS bigint) AS bucket, min(v) AS low, max(v) AS high,
					sum(v) AS total, count(*) AS events
				FROM "opnsense".`+table+` GROUP BY 1)
			SELECT count(*) FROM independent AS i FULL JOIN `+aggregate+` AS a ON a."#bucket" = i.bucket
			WHERE a."#bucket" IS NULL OR i.bucket IS NULL OR a."v#min" <> i.low OR a."v#max" <> i.high
				OR a."v#sum" <> i.total OR a."#count" <> i.events`, period.width).Scan(&mismatches)
		if err != nil || mismatches != 0 {
			t.Errorf("the %s aggregate differs from the detail on %d buckets (%v)", period.suffix, mismatches, err)
		}
	}
	// Detail older than its retention goes, to the exact second.
	now := w.clock.Now().Unix()
	horizon := start + 100
	if err := w.broker.PurgeStreams(context.Background(), now, now-horizon, 0); err != nil {
		t.Fatal(err)
	}
	var oldest int64
	var kept int
	if err := w.store.DB().QueryRow(`SELECT min("#received_at"), count(*) FROM "opnsense".`+table).Scan(&oldest,
		&kept); err != nil || oldest != horizon || kept != len(values)-4 {
		t.Errorf("after the purge at %d the oldest row is %d, %d rows kept (%v)", horizon, oldest, kept, err)
	}
	var jobs int
	if err := w.store.DB().QueryRow(`SELECT count(*) FROM timescaledb_information.jobs
		WHERE hypertable_schema = 'opnsense'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Errorf("%d TimescaleDB background jobs run on the mirror (%v)", jobs, err)
	}
}

// TestADeclaredStreamReplacesPollingWhileItRuns is BR-AC21.
func TestADeclaredStreamReplacesPollingWhileItRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	polled := endpoint(t, http.MethodGet, "/api/example/polled/get", opnsense.NoBody)
	w.fake.answer(jsonAnswer(rowsBody))
	ep, events := w.stream("/api/example/replacing/stream")
	w.broker.Declare("example_streamer", false, ep, broker.EndpointDeclaration{Replaces: polled.Path})
	streamer := broker.Requester{Name: "example_streamer", Version: "1"}
	w.broker.EventsAfter(streamer, ep, opnsense.RequestOptions{}, 0)
	waitFor(t, "the stream to connect", func() bool { return w.connected(ep) })

	served, _ := w.call(streamer.Name, polled, opnsense.RequestOptions{}, time.Second)
	if served.ServedFrom != broker.ServedFromStream || served.ReplacedBy != ep.Path || w.fake.count(polled.Path) != 0 {
		t.Errorf("while its stream runs the plugin was served %s and caused %d calls", served.ServedFrom,
			w.fake.count(polled.Path))
	}
	other, _ := w.call("example_other", polled, opnsense.RequestOptions{}, time.Second)
	if other.ServedFrom != broker.ServedFromFirewall || w.fake.count(polled.Path) != 1 {
		t.Errorf("a plugin that declares nothing was served %s", other.ServedFrom)
	}

	close(events)
	waitFor(t, "the stream to drop", func() bool { return !w.connected(ep) })
	w.clock.advance(2 * time.Second)
	resumed, _ := w.call(streamer.Name, polled, opnsense.RequestOptions{}, time.Second)
	if resumed.ServedFrom != broker.ServedFromFirewall || resumed.ReplacedBy != "" || w.fake.count(polled.Path) != 2 {
		t.Errorf("after the stream dropped the plugin was served %s (replaced by %q)", resumed.ServedFrom,
			resumed.ReplacedBy)
	}
}
