package collect

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// The scheduler tests.
//
// The property under test is failure isolation. A firewall with no Suricata, a resolver with
// its reporting off, a lease backend the firewall does not separate — each of those makes one
// collector fail every pass, and none of them may stall the others. Without that, a
// product installed on an ordinary firewall would stop collecting the filter log because the
// intrusion-detection engine is not installed.
//
// The clock is injected, so nothing here sleeps.

// TestEveryLoopRunsAtItsConfiguredCadence pins the wiring: every collector, runtime
// discovery, the probe round, the retention purge and the resolver's cache and local-data
// reads, each at the interval its own justification carries. The loops are the keys of
// the map below and nowhere else, so adding one is adding a key, and this comment and the
// test's name state no count to fall out of date.
func TestEveryLoopRunsAtItsConfiguredCadence(t *testing.T) {
	t.Parallel()
	harness := newProbeHarness(t)
	settings := config.Defaults()
	tasks := harness.collector.Tasks(config.NewLive(settings))

	wanted := map[string]time.Duration{
		"runtime discovery":   settings.DiscoveryInterval,
		"availability probes": settings.DiscoveryInterval,
		"filter log":          settings.FirewallLogInterval,
		"security events":     settings.SecurityEventInterval,
		"sampled measurement": settings.MeasurementInterval,
		"DHCP leases":         settings.DHCPLeaseInterval,
		"resolver lookups":    settings.DNSLookupInterval,
		"retention purge":     settings.PurgeInterval,
		// The two reads of the resolver-cache cycle, at decision 4's 60 s and 300 s.
		"resolver cache":      settings.ResolverCacheInterval,
		"resolver local data": settings.ResolverLocalDataInterval,
	}
	if len(tasks) != len(wanted) {
		t.Fatalf("the scheduler has %d loops, want %d", len(tasks), len(wanted))
	}
	for _, task := range tasks {
		interval, named := wanted[task.Name]
		if !named {
			t.Errorf("the loop %q is not one this test knows about", task.Name)
			continue
		}
		if task.Interval() != interval {
			t.Errorf("the %q loop runs every %v, want %v", task.Name, task.Interval(), interval)
		}
		if task.Run == nil {
			t.Errorf("the %q loop has no work", task.Name)
		}
		delete(wanted, task.Name)
	}
	for name := range wanted {
		t.Errorf("there is no %q loop", name)
	}

	// The purge is the one loop that does not run at start-up: there is nothing to purge in
	// the first second of a run, and a delete competing with the first ingest buys nothing.
	for _, task := range tasks {
		if task.Name == "retention purge" && task.RunAtStart {
			t.Error("the purge runs at start-up, which competes with the first ingest for nothing")
		}
		if task.Name != "retention purge" && !task.RunAtStart {
			t.Errorf("the %q loop does not run at start-up, so its first pass waits a full "+
				"interval for no reason", task.Name)
		}
	}
}

// TestACollectorThatFailsDoesNotStopTheOthers is the isolation, driven by an injected clock.
func TestACollectorThatFailsDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	clock := newFixedClock(referenceInstant())
	failing := errors.New("collect_test: this source is unavailable on this firewall")

	var counts [4]atomic.Int64
	reported := make(chan string, 64)

	tasks := []Task{
		{Name: "the failing one", Interval: FixedInterval(time.Second), RunAtStart: true,
			Run: func(context.Context) error { counts[0].Add(1); return failing }},
		{Name: "the first healthy one", Interval: FixedInterval(time.Second), RunAtStart: true,
			Run: func(context.Context) error { counts[1].Add(1); return nil }},
		{Name: "the second healthy one", Interval: FixedInterval(time.Second), RunAtStart: true,
			Run: func(context.Context) error { counts[2].Add(1); return nil }},
		{Name: "the third healthy one", Interval: FixedInterval(time.Second), RunAtStart: true,
			Run: func(context.Context) error { counts[3].Add(1); return nil }},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var finished sync.WaitGroup
	finished.Add(1)
	go func() {
		defer finished.Done()
		if err := Run(ctx, clock, tasks, func(name string, _ error) { reported <- name }); err != nil {
			t.Errorf("the run returned %v", err)
		}
	}()

	// Three rounds. Each round waits until every loop is waiting for a tick and then
	// wakes them all, so every loop gets the same number of passes and a loop that had
	// stalled would be visible as a missing waiter rather than as a low count.
	const rounds = 3
	for round := 0; round < rounds; round++ {
		clock.awaitWaiters(t, len(tasks))
		if woken := clock.releaseAll(); woken != len(tasks) {
			t.Fatalf("round %d woke %d of %d loops", round, woken, len(tasks))
		}
	}
	// Wait for the last round to come back round before stopping, so the counts below are
	// not racing the final pass.
	clock.awaitWaiters(t, len(tasks))
	cancel()
	clock.releaseAll()
	finished.Wait()

	for index := range counts {
		if ran := counts[index].Load(); ran < int64(rounds) {
			t.Errorf("loop %d ran %d times, want at least %d; a failing sibling stalled it",
				index, ran, rounds)
		}
	}
	if counts[0].Load() < int64(rounds) {
		t.Error("the failing loop stopped after its first failure rather than continuing")
	}
	close(reported)
	failures := 0
	for name := range reported {
		if name != "the failing one" {
			t.Errorf("the loop %q was reported as failing", name)
		}
		failures++
	}
	if failures < rounds {
		t.Errorf("the failing loop was reported %d times; each failed pass has to be reported",
			failures)
	}
}

// TestTheRunLoopStopsOnCancellationWithinABoundedDeadline is the shutdown guarantee the
// service unit depends on.
func TestTheRunLoopStopsOnCancellationWithinABoundedDeadline(t *testing.T) {
	t.Parallel()
	clock := newFixedClock(referenceInstant())
	tasks := []Task{
		{Name: "a loop that waits", Interval: FixedInterval(time.Hour), RunAtStart: true,
			Run: func(context.Context) error { return nil }},
		{Name: "another loop that waits", Interval: FixedInterval(time.Hour), RunAtStart: true,
			Run: func(context.Context) error { return nil }},
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- Run(ctx, clock, tasks, nil) }()

	cancel()
	select {
	case err := <-stopped:
		// A cancelled context is how the program stops, not a failure: returning an error
		// for it would make an ordinary stop look like a crash to whatever reads the exit
		// code.
		if err != nil {
			t.Fatalf("a clean cancellation returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run loop did not stop within five seconds of cancellation")
	}
}

// TestALoopWithNoCadenceIsNotRun keeps a misconfigured interval from becoming a loop that
// runs as fast as the processor allows.
func TestALoopWithNoCadenceIsNotRun(t *testing.T) {
	t.Parallel()
	clock := newFixedClock(referenceInstant())
	var ran atomic.Int64
	tasks := []Task{
		{Name: "a loop with no interval", Interval: FixedInterval(0), RunAtStart: true,
			Run: func(context.Context) error { ran.Add(1); return nil }},
		{Name: "a loop with no work", Interval: FixedInterval(time.Second), RunAtStart: true},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, clock, tasks, nil); err != nil {
		t.Fatalf("the run returned %v", err)
	}
	if ran.Load() != 0 {
		t.Fatalf("a loop with no cadence ran %d times", ran.Load())
	}
}

// TestAPassThatFailsBecauseTheRunIsStoppingIsNotReported keeps a stop from filling the log
// with failures that are only the stop itself.
func TestAPassThatFailsBecauseTheRunIsStoppingIsNotReported(t *testing.T) {
	t.Parallel()
	clock := newFixedClock(referenceInstant())
	reported := make(chan string, 8)

	ctx, cancel := context.WithCancel(context.Background())
	tasks := []Task{{
		Name: "a loop that fails as the context closes", Interval: FixedInterval(time.Hour), RunAtStart: true,
		Run: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
	}}
	if err := Run(ctx, clock, tasks, func(name string, _ error) { reported <- name }); err != nil {
		t.Fatalf("the run returned %v", err)
	}
	close(reported)
	for name := range reported {
		t.Errorf("the loop %q was reported as failing, and it only failed because the run was "+
			"stopping", name)
	}
}
