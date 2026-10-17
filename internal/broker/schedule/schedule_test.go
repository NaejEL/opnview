package schedule

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock the simulation moves by hand. After never fires: the simulation dispatches
// at every tick itself, so a wait for the rate ceiling is never needed.
type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *fakeClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *fakeClock) advance(d time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(d)
	c.mutex.Unlock()
}

// tick is the simulation's step and every call's latency: a call dispatched at one tick answers
// at the next.
const tick = 100 * time.Millisecond

// simulation drives a scheduler in fake time with calls that each take one tick.
type simulation struct {
	t         *testing.T
	clock     *fakeClock
	scheduler *Scheduler

	mutex sync.Mutex
	// gate is closed at the next tick, answering the calls waiting on it.
	gate    chan struct{}
	waiting int
	// calls are the call instants by datum key.
	calls map[string][]time.Time
	// outcomes answer a datum's calls; Succeeded when absent.
	outcomes map[string]Outcome
}

func newSimulation(t *testing.T, settings Settings) *simulation {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1790000000, 0)}
	scheduler := New(clock, settings)
	ctx, cancel := context.WithCancel(context.Background())
	scheduler.base = ctx
	sim := &simulation{t: t, clock: clock, scheduler: scheduler, gate: make(chan struct{}),
		calls: map[string][]time.Time{}, outcomes: map[string]Outcome{}}
	t.Cleanup(func() {
		cancel()
		sim.mutex.Lock()
		close(sim.gate)
		sim.gate = nil
		sim.mutex.Unlock()
	})
	return sim
}

// refresh is a datum's call: it records its instant and waits for the next tick.
func (sim *simulation) refresh(key string) Refresh {
	return func(context.Context) (Outcome, any) {
		sim.mutex.Lock()
		sim.calls[key] = append(sim.calls[key], sim.clock.Now())
		outcome := sim.outcomes[key]
		gate := sim.gate
		sim.waiting++
		sim.mutex.Unlock()
		if gate != nil {
			<-gate
		}
		return outcome, key
	}
}

// demand registers a reader's demand for a datum.
func (sim *simulation) demand(key, endpoint string, maxAge time.Duration) {
	sim.scheduler.Demand(key, endpoint, "reader", maxAge, 0, sim.refresh(key))
}

// settle waits until every call in flight is waiting on the current gate: every answered call
// has been recorded by the scheduler, and every call dispatched has started.
func (sim *simulation) settle() {
	sim.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		sim.scheduler.mutex.Lock()
		inFlight := sim.scheduler.inFlight
		sim.scheduler.mutex.Unlock()
		sim.mutex.Lock()
		waiting := sim.waiting
		sim.mutex.Unlock()
		if inFlight == waiting {
			return
		}
		if time.Now().After(deadline) {
			sim.t.Fatalf("the simulation did not settle: %d in flight, %d waiting", inFlight, waiting)
		}
		runtime.Gosched()
	}
}

// step moves the simulation one tick: the calls in flight answer, and the scheduler dispatches.
func (sim *simulation) step() {
	sim.mutex.Lock()
	answered := sim.gate
	sim.gate = make(chan struct{})
	sim.waiting = 0
	sim.mutex.Unlock()
	sim.clock.advance(tick)
	close(answered)
	sim.settle()
	sim.scheduler.kick()
	sim.settle()
}

// run moves the simulation for a span of fake time.
func (sim *simulation) run(span time.Duration) {
	for elapsed := time.Duration(0); elapsed < span; elapsed += tick {
		sim.step()
	}
}

// callsOf returns a datum's call instants.
func (sim *simulation) callsOf(key string) []time.Time {
	sim.mutex.Lock()
	defer sim.mutex.Unlock()
	return append([]time.Time(nil), sim.calls[key]...)
}

// TestDataAreRefreshedNearestDeadlineFirst is BR-AC11's first part.
func TestDataAreRefreshedNearestDeadlineFirst(t *testing.T) {
	t.Parallel()
	sim := newSimulation(t, Settings{MaxInFlight: 1, MaxPerSecond: 100})
	now := sim.clock.Now()
	for key, overdue := range map[string]time.Duration{"a": time.Second, "b": 3 * time.Second, "c": 2 * time.Second} {
		sim.demand(key, "/api/example/"+key, time.Minute)
		sim.scheduler.Fetched(key, now.Add(-time.Minute-overdue))
	}
	sim.scheduler.kick()
	sim.settle()
	sim.run(5 * tick)
	var order []string
	for _, key := range []string{"a", "b", "c"} {
		if len(sim.callsOf(key)) != 1 {
			t.Fatalf("%s was called %d times", key, len(sim.callsOf(key)))
		}
	}
	first := func(key string) time.Time { return sim.callsOf(key)[0] }
	for len(order) < 3 {
		var next string
		for _, key := range []string{"a", "b", "c"} {
			taken := false
			for _, done := range order {
				taken = taken || done == key
			}
			if !taken && (next == "" || first(key).Before(first(next))) {
				next = key
			}
		}
		order = append(order, next)
	}
	if fmt.Sprint(order) != "[b c a]" {
		t.Errorf("the data were refreshed in the order %v, not nearest deadline first", order)
	}
}

// TestABackgroundDatumIsServedUnderSaturatingRealTimeDemand is BR-AC11's second part: twenty
// real-time data at a one-second max age ask four times the calls-per-second ceiling for ten
// minutes, and a background datum at 300 s is still refreshed within the bound the package
// documents -- B / ((1 - r) * R) = 0.67 s with one background datum due, plus its call.
func TestABackgroundDatumIsServedUnderSaturatingRealTimeDemand(t *testing.T) {
	t.Parallel()
	sim := newSimulation(t, Settings{MaxInFlight: 2, MaxPerSecond: 5})
	for index := 0; index < 20; index++ {
		key := fmt.Sprintf("rt%02d", index)
		sim.demand(key, "/api/example/"+key, time.Second)
	}
	sim.demand("bg", "/api/example/bg", 300*time.Second)
	for elapsed := time.Duration(0); elapsed < 10*time.Minute; elapsed += 10 * time.Second {
		// The readers renew their demand, as a pass does.
		for index := 0; index < 20; index++ {
			key := fmt.Sprintf("rt%02d", index)
			sim.demand(key, "/api/example/"+key, time.Second)
		}
		sim.demand("bg", "/api/example/bg", 300*time.Second)
		sim.run(10 * time.Second)
	}
	calls := sim.callsOf("bg")
	if len(calls) < 2 {
		t.Fatalf("the background datum was refreshed %d times in ten minutes", len(calls))
	}
	bound := 300*time.Second + time.Second + 2*tick
	for index := 1; index < len(calls); index++ {
		if gap := calls[index].Sub(calls[index-1]); gap > bound {
			t.Errorf("the background datum waited %v between refreshes, beyond %v", gap, bound)
		}
	}
	stats := sim.scheduler.Stats()
	if stats.Calls < int(0.95*5*600) {
		t.Errorf("saturating demand made %d calls in ten minutes against a ceiling of 3000", stats.Calls)
	}
	if stats.MaxInFlight > 2 || stats.MaxPerWindow > 5 {
		t.Errorf("the ceilings were exceeded: %d in flight, %d in one second", stats.MaxInFlight, stats.MaxPerWindow)
	}
}

// TestTheRealTimeReserveHoldsUnderBackgroundLoad is BR-AC12.
func TestTheRealTimeReserveHoldsUnderBackgroundLoad(t *testing.T) {
	t.Parallel()
	renew := func(sim *simulation, realTime, background int, rtAge time.Duration) {
		for index := 0; index < realTime; index++ {
			key := fmt.Sprintf("rt%02d", index)
			sim.demand(key, "/api/example/"+key, rtAge)
		}
		for index := 0; index < background; index++ {
			key := fmt.Sprintf("bg%03d", index)
			sim.demand(key, "/api/example/"+key, 10*time.Second)
		}
	}
	drive := func(sim *simulation, realTime, background int, rtAge, span time.Duration) {
		for elapsed := time.Duration(0); elapsed < span; elapsed += 10 * time.Second {
			renew(sim, realTime, background, rtAge)
			sim.run(10 * time.Second)
		}
	}

	// One real-time datum among background demand at twice the ceiling is refreshed at its max age.
	sim := newSimulation(t, Settings{MaxInFlight: 2, MaxPerSecond: 5})
	drive(sim, 1, 100, 2*time.Second, 2*time.Minute)
	calls := sim.callsOf("rt00")
	for index := 1; index < len(calls); index++ {
		if gap := calls[index].Sub(calls[index-1]); gap > 2*time.Second+2*tick {
			t.Errorf("the real-time datum waited %v under background load", gap)
		}
	}
	stats := sim.scheduler.Stats()
	if stats.MaxInFlight > 2 || stats.MaxPerWindow > 5 {
		t.Errorf("the ceilings were exceeded: %d in flight, %d in one second", stats.MaxInFlight, stats.MaxPerWindow)
	}

	// Both classes saturating: real-time calls take at least the reserved share, 70 % then 50 %.
	for _, share := range []float64{0.7, 0.5} {
		sim := newSimulation(t, Settings{MaxInFlight: 2, MaxPerSecond: 5, RealTimeShare: share})
		drive(sim, 20, 100, time.Second, 2*time.Minute)
		stats := sim.scheduler.Stats()
		if got := float64(stats.RealTimeCalls) / float64(stats.Calls); got < share-0.02 {
			t.Errorf("with the reserve at %.0f %%, real-time calls took %.1f %% of %d calls", share*100, got*100,
				stats.Calls)
		}
		if stats.MaxInFlight > 2 || stats.MaxPerWindow > 5 {
			t.Errorf("the ceilings were exceeded: %d in flight, %d in one second", stats.MaxInFlight, stats.MaxPerWindow)
		}
	}

	// No real-time demand: background calls use the whole budget.
	sim = newSimulation(t, Settings{MaxInFlight: 2, MaxPerSecond: 5})
	drive(sim, 0, 100, time.Second, time.Minute)
	if stats := sim.scheduler.Stats(); stats.BackgroundCalls < int(0.95*5*60) {
		t.Errorf("alone, background calls left %d times in a minute against a ceiling of 300", stats.BackgroundCalls)
	}
}

// TestFailuresBackOffExponentiallyAndAnAnswerEndsIt is BR-AC14.
func TestFailuresBackOffExponentiallyAndAnAnswerEndsIt(t *testing.T) {
	t.Parallel()
	settings := Settings{MaxInFlight: 2, MaxPerSecond: 50, BackoffBase: time.Second, BackoffMax: 16 * time.Second,
		QueueFailureEndpoints: 3}

	// One datum answering server errors: its calls space 1, 2, 4, 8, 16, 16 s; the first ok
	// restores its cadence.
	sim := newSimulation(t, settings)
	sim.outcomes["failing"] = Failed
	drive := func(span time.Duration) {
		for elapsed := time.Duration(0); elapsed < span; elapsed += 5 * time.Second {
			sim.demand("failing", "/api/example/failing", 2*time.Second)
			sim.run(5 * time.Second)
		}
	}
	drive(70 * time.Second)
	calls := sim.callsOf("failing")
	want := []time.Duration{1, 2, 4, 8, 16, 16}
	if len(calls) < len(want)+1 {
		t.Fatalf("a failing datum was called %d times", len(calls))
	}
	for index, factor := range want {
		gap := calls[index+1].Sub(calls[index])
		expected := factor*time.Second + tick
		if gap < expected || gap > expected+2*tick {
			t.Errorf("failure %d was followed by a wait of %v, not %v", index+1, gap, expected)
		}
	}
	sim.mutex.Lock()
	sim.outcomes["failing"] = Succeeded
	sim.mutex.Unlock()
	drive(40 * time.Second)
	calls = sim.callsOf("failing")
	last := calls[len(calls)-1].Sub(calls[len(calls)-2])
	if last > 2*time.Second+2*tick {
		t.Errorf("after an answer the cadence is %v, not the reader's", last)
	}
	if sim.scheduler.BackingOff("failing") {
		t.Error("an answer did not end the back-off")
	}

	// Time-outs across three endpoints back off the whole queue.
	sim = newSimulation(t, settings)
	for _, key := range []string{"one", "two", "three"} {
		sim.outcomes[key] = Failed
		sim.demand(key, "/api/example/"+key, 2*time.Second)
	}
	sim.run(3 * tick)
	if !sim.scheduler.BackingOff("/api/example/other") {
		t.Error("three failing endpoints did not back off the queue")
	}

	// not_found is a state: no back-off, retried at the reader's cadence.
	sim = newSimulation(t, settings)
	sim.outcomes["absent"] = Refused
	for elapsed := time.Duration(0); elapsed < 20*time.Second; elapsed += 5 * time.Second {
		sim.demand("absent", "/api/example/absent", 2*time.Second)
		sim.run(5 * time.Second)
	}
	calls = sim.callsOf("absent")
	if len(calls) < 8 || sim.scheduler.BackingOff("absent") {
		t.Errorf("not_found was backed off: %d calls in 20 s", len(calls))
	}
}

// joined is what a caller of Do received.
type joined struct {
	value any
	err   error
}

// do calls Do for a datum on its own goroutine, with a call of its own that must never run.
func (sim *simulation) do(key string) (chan joined, *bool) {
	ran := new(bool)
	done := make(chan joined, 1)
	go func() {
		_, value, err := sim.scheduler.Do(context.Background(), key, "/api/example/"+key, time.Minute,
			func(context.Context) (Outcome, any) {
				sim.mutex.Lock()
				*ran = true
				sim.mutex.Unlock()
				return Succeeded, "the caller's own call"
			})
		done <- joined{value: value, err: err}
	}()
	return done, ran
}

// attached waits until one caller waits on the datum's queued job, or on its job in flight.
func (sim *simulation) attached(key string, inFlight bool) {
	sim.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		sim.scheduler.mutex.Lock()
		waiting := 0
		if entry := sim.scheduler.data[key]; entry != nil {
			held := entry.queued
			if inFlight {
				held = entry.running
			}
			if held != nil {
				waiting = len(held.waiters)
			}
		}
		sim.scheduler.mutex.Unlock()
		if waiting == 1 {
			return
		}
		if time.Now().After(deadline) {
			sim.t.Fatalf("the caller never joined %s's call", key)
		}
		runtime.Gosched()
	}
}

// ownCallRan reads whether a caller's own call ran.
func (sim *simulation) ownCallRan(ran *bool) bool {
	sim.mutex.Lock()
	defer sim.mutex.Unlock()
	return *ran
}

// TestACallerJoiningAQueuedAdvanceRefreshIsHandedItsResult is N1's scheduler half: a caller that
// finds the datum's advance refresh queued joins it, and is handed what that one call returned.
func TestACallerJoiningAQueuedAdvanceRefreshIsHandedItsResult(t *testing.T) {
	t.Parallel()
	sim := newSimulation(t, Settings{MaxInFlight: 1, MaxPerSecond: 50})
	// A datum of its own holds the only slot.
	sim.demand("blocker", "/api/example/blocker", time.Minute)
	sim.scheduler.kick()
	sim.settle()
	// The advance refresh is queued behind it.
	sim.demand("datum", "/api/example/datum", time.Minute)
	sim.scheduler.kick()
	sim.settle()
	done, ran := sim.do("datum")
	sim.attached("datum", false)
	sim.step()
	sim.step()
	got := <-done
	if got.err != nil || got.value != "datum" {
		t.Fatalf("the caller was handed %v (%v), not the refresh's result", got.value, got.err)
	}
	if calls := len(sim.callsOf("datum")); calls != 1 || sim.ownCallRan(ran) {
		t.Errorf("the datum was called %d times, the caller's own call ran: %v", calls, sim.ownCallRan(ran))
	}
}

// TestACallerArrivingDuringACallJoinsIt is N1's other order: a call already in flight for the
// datum is joined, never followed by a second.
func TestACallerArrivingDuringACallJoinsIt(t *testing.T) {
	t.Parallel()
	sim := newSimulation(t, Settings{MaxInFlight: 2, MaxPerSecond: 50})
	sim.demand("datum", "/api/example/datum", time.Minute)
	sim.scheduler.kick()
	sim.settle()
	done, ran := sim.do("datum")
	sim.attached("datum", true)
	sim.step()
	got := <-done
	if got.err != nil || got.value != "datum" {
		t.Fatalf("the caller was handed %v (%v), not the call's result", got.value, got.err)
	}
	if calls := len(sim.callsOf("datum")); calls != 1 || sim.ownCallRan(ran) || sim.scheduler.Stats().Calls != 1 {
		t.Errorf("the datum was called %d times (%d dispatched), the caller's own call ran: %v", calls,
			sim.scheduler.Stats().Calls, sim.ownCallRan(ran))
	}
}
