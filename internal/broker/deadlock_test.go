package broker_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The broker deadlock of the live validation of 17 October 2026 (N1 in
// specs/SPEC-backend-completion-plugins.md): a reader's call and the scheduler's advance refresh of
// the same datum waited on each other until the reader's context ended. A reader that finds the
// datum stale while its refresh is due, or running, gets the result of one firewall call, and so
// does the refresh.

// step makes the scheduler look at its queue once, synchronously: a call with a context already
// ended queues a job for a datum of its own, dispatches what the ceilings allow, and returns.
func step(t *testing.T, w *world, key string) {
	t.Helper()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := w.broker.Scheduler().Do(ended, key, "/api/example/trigger/get", time.Hour,
		func(context.Context) (schedule.Outcome, any) { return schedule.Succeeded, nil })
	if err == nil {
		t.Fatal("a call with an ended context was waited for")
	}
}

// TestAReaderAndTheAdvanceRefreshOfOneDatumShareOneCall is N1: the reader's pass at the 60 s
// cadence finds the datum stale exactly when its advance refresh is queued. The reader is answered,
// the refresh ends, and the firewall is called once for both.
func TestAReaderAndTheAdvanceRefreshOfOneDatumShareOneCall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.broker.Scheduler().SetSettings(schedule.Settings{MaxInFlight: 1, MaxPerSecond: 1e6})
	datum := endpoint(t, http.MethodGet, "/api/example/collided/get", opnsense.NoBody)
	blocker := endpoint(t, http.MethodGet, "/api/example/blocker/get", opnsense.NoBody)
	release := make(chan struct{})
	w.fake.AnswerWith(func(request *http.Request, _ string) (int, string, string) {
		if request.URL.Path == blocker.Path {
			<-release
		}
		return http.StatusOK, "application/json", rowsBody
	})
	const reader = "example_reader"
	w.call(reader, datum, opnsense.RequestOptions{}, time.Minute, "a")
	if calls := w.fake.CountTo(datum.Path); calls != 1 {
		t.Fatalf("the first read made %d calls", calls)
	}

	// Another datum holds the only call slot, so what the scheduler queues next waits in the queue.
	blocked := make(chan error, 1)
	go func() {
		_, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: reader,
			Version: "1"}, Endpoint: blocker, Fresh: true})
		blocked <- err
	}()
	waitFor(t, "the blocking call to reach the firewall", func() bool { return w.fake.CountTo(blocker.Path) == 1 })

	// One max age later the advance refresh is due, and is queued.
	w.clock.advance(time.Minute + time.Second)
	step(t, w, "trigger")

	// The reader's pass finds the datum stale while its refresh waits in the queue.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answered := make(chan error, 1)
	var served *broker.Answer
	go func() {
		var err error
		served, err = w.broker.Call(ctx, broker.Request{Requester: broker.Requester{Name: reader, Version: "1"},
			Endpoint: datum, MaxAge: time.Minute})
		answered <- err
	}()
	// The reader is waiting, not answered: the datum's only call is the queued one.
	select {
	case err := <-answered:
		t.Fatalf("the reader was answered before the call slot was free: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-blocked; err != nil {
		t.Fatalf("the blocking call: %v", err)
	}
	if err := <-answered; err != nil {
		t.Fatalf("the reader and the advance refresh waited on each other: %v", err)
	}
	if served == nil || served.ServedFrom != broker.ServedFromFirewall || served.Response.Outcome != opnsense.OutcomeOK {
		t.Fatalf("the reader was served %+v", served)
	}
	// The blocker, the refresh and the trigger have all run; the datum was called once more.
	waitFor(t, "every queued call to run", func() bool { return w.broker.Scheduler().Stats().Calls == 4 })
	if calls := w.fake.CountTo(datum.Path); calls != 2 {
		t.Errorf("the reader and the refresh made %d calls of the datum, want one between them", calls-1)
	}
}

// TestAReaderArrivingDuringTheAdvanceRefreshSharesItsCall is N1's other order: the advance refresh
// is already in flight when the reader finds the datum stale. The reader joins it.
func TestAReaderArrivingDuringTheAdvanceRefreshSharesItsCall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	datum := endpoint(t, http.MethodGet, "/api/example/joined/get", opnsense.NoBody)
	release := make(chan struct{})
	w.fake.AnswerWith(func(request *http.Request, _ string) (int, string, string) {
		if request.URL.Path == datum.Path && w.fake.CountTo(datum.Path) > 1 {
			<-release
		}
		return http.StatusOK, "application/json", rowsBody
	})
	const reader = "example_reader"
	w.call(reader, datum, opnsense.RequestOptions{}, time.Minute, "a")

	w.clock.advance(time.Minute + time.Second)
	step(t, w, "trigger")
	waitFor(t, "the advance refresh to reach the firewall", func() bool { return w.fake.CountTo(datum.Path) == 2 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answered := make(chan error, 1)
	var served *broker.Answer
	go func() {
		var err error
		served, err = w.broker.Call(ctx, broker.Request{Requester: broker.Requester{Name: reader, Version: "1"},
			Endpoint: datum, MaxAge: time.Minute})
		answered <- err
	}()
	select {
	case err := <-answered:
		t.Fatalf("the reader was answered while the only call was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-answered; err != nil {
		t.Fatalf("the reader did not share the refresh's call: %v", err)
	}
	if served == nil || served.Response.Outcome != opnsense.OutcomeOK {
		t.Fatalf("the reader was served %+v", served)
	}
	waitFor(t, "every queued call to run", func() bool { return w.broker.Scheduler().Stats().Calls == 3 })
	if calls := w.fake.CountTo(datum.Path); calls != 2 {
		t.Errorf("the reader and the refresh made %d calls of the datum, want one between them", calls-1)
	}
}
