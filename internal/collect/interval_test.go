package collect_test

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// Settings that reach a running service. Rebuilt after the loss of 2 October 2026: the
// build of that evening recorded these two test names, not their bodies, so the bodies
// below are rewritten from what the names state.

// recordingClock is a clock that records how long each wait asked for, and releases the
// waits one at a time when the test says so.
type recordingClock struct {
	mutex   sync.Mutex
	asked   []time.Duration
	waiters []chan time.Time
}

// Now is a fixed instant; nothing here reads it.
func (c *recordingClock) Now() time.Time { return collecttest.ReferenceInstant() }

// After records the duration and registers a waiter.
func (c *recordingClock) After(d time.Duration) <-chan time.Time {
	channel := make(chan time.Time, 1)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.asked = append(c.asked, d)
	c.waiters = append(c.waiters, channel)
	return channel
}

// awaitWaits blocks until count waits have been asked for, or fails the test.
func (c *recordingClock) awaitWaits(t *testing.T, count int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mutex.Lock()
		asked := append([]time.Duration(nil), c.asked...)
		c.mutex.Unlock()
		if len(asked) >= count {
			return asked
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d waits were asked for after five seconds", len(asked), count)
		}
		runtime.Gosched()
	}
}

// releaseOne ends the oldest wait still pending.
func (c *recordingClock) releaseOne() {
	c.mutex.Lock()
	channel := c.waiters[0]
	c.waiters = c.waiters[1:]
	c.mutex.Unlock()
	channel <- collecttest.ReferenceInstant()
}

// TestASavedIntervalChangesTheNextScheduledPassWithoutARestart: the interval is read before
// every wait, so a value saved while the loop runs is the length of the next wait, and a
// value that is not positive leaves the last good one in force rather than spinning.
func TestASavedIntervalChangesTheNextScheduledPassWithoutARestart(t *testing.T) {
	t.Parallel()
	settings := config.Defaults().WithInterval("firewall_log", "poll", 10*time.Second)
	live := config.NewLive(settings)

	clock := &recordingClock{}
	passes := make(chan struct{}, 8)
	task := core.Task{
		Name:     "filter log",
		Interval: live.Interval(func(c config.Config) time.Duration { return c.Interval("firewall_log", "poll") }),
		Run:      func(context.Context) error { passes <- struct{}{}; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx, clock, []core.Task{task}, nil) }()

	if asked := clock.awaitWaits(t, 1); asked[0] != 10*time.Second {
		t.Fatalf("the first wait is %v, want the 10 s in force", asked[0])
	}

	// Saved while the loop waits: the wait under way is not cut short, the next one is new.
	settings = settings.WithInterval("firewall_log", "poll", 30*time.Second)
	live.Set(settings)
	clock.releaseOne()
	<-passes
	if asked := clock.awaitWaits(t, 2); asked[1] != 30*time.Second {
		t.Fatalf("the wait after a saved interval is %v, want 30 s", asked[1])
	}

	// A value that is not positive keeps the last good one.
	settings = settings.WithInterval("firewall_log", "poll", 0)
	live.Set(settings)
	clock.releaseOne()
	<-passes
	if asked := clock.awaitWaits(t, 3); asked[2] != 30*time.Second {
		t.Fatalf("a saved interval of 0 gave a wait of %v, want the 30 s still in force", asked[2])
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("the run ended with %v, want a clean stop", err)
	}
}

// TestASavedPageSizeReachesARunningCollector: Configure replaces the page sizes of a running
// collector, and the next pass of each paged read asks the firewall for that page.
func TestASavedPageSizeReachesARunningCollector(t *testing.T) {
	t.Parallel()
	harness := collecttest.NewProbeHarness(t)
	ctx := context.Background()
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	if err := harness.Collector.ProbeKind(ctx, "firewall_log"); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}

	for _, kind := range []string{"firewall_log", "security_event", "dhcp_lease"} {
		if got := harness.Collector.PageSize(kind); got != config.Defaults().PageSize(kind) || got != 500 {
			t.Fatalf("a new collector asks for %d %s records a page, want the default 500", got, kind)
		}
	}

	settings := config.Defaults().WithPageSize("firewall_log", 250).WithPageSize("security_event", 125).
		WithPageSize("dhcp_lease", 2000)
	harness.Collector.Configure(settings)

	for kind, want := range map[string]int{"firewall_log": 250, "security_event": 125, "dhcp_lease": 2000} {
		if got := harness.Collector.PageSize(kind); got != want {
			t.Fatalf("after Configure the collector asks for %d %s records a page, want %d", got, kind, want)
		}
	}

	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("a filter-log pass: %v", err)
	}
	requests := harness.Fake.RequestsTo(opnsense.FirewallLog)
	if limit := requests[len(requests)-1].Query.Get("limit"); limit != "250" {
		t.Errorf("the pass after a saved page asked for %q records, want 250", limit)
	}
}
