package collect

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The fake firewall every collector test runs against is internal/collect/providertest: an
// implementation's own tests run against the same one. What is left here is what only the
// coordinators' tests need.

// fixedClock is a clock a test drives. It never advances on its own, so nothing in a
// test sleeps and nothing depends on how fast the machine is.
//
// Each call to After hands out ITS OWN channel, and that matters: a single shared
// channel would let whichever loop re-selects first take every release, and a test
// built on it would report that the other loops had stalled when they had merely not
// been woken. One channel per waiter makes "release every waiting loop once" a thing a
// test can actually do.
type fixedClock struct {
	mutex   sync.Mutex
	now     time.Time
	waiters []chan time.Time
}

// newFixedClock returns a clock stopped at an instant.
func newFixedClock(at time.Time) *fixedClock {
	return &fixedClock{now: at}
}

// Now returns the clock's instant.
func (c *fixedClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// After registers a waiter and returns its channel.
func (c *fixedClock) After(time.Duration) <-chan time.Time {
	channel := make(chan time.Time, 1)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.waiters = append(c.waiters, channel)
	return channel
}

// waiting reports how many loops are currently waiting for a tick.
func (c *fixedClock) waiting() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.waiters)
}

// releaseAll wakes every loop that is currently waiting, exactly once each, and returns
// how many it woke. The channels are buffered, so it never blocks on a loop that has
// already stopped.
func (c *fixedClock) releaseAll() int {
	c.mutex.Lock()
	waiters := c.waiters
	c.waiters = nil
	instant := c.now
	c.mutex.Unlock()

	for _, channel := range waiters {
		channel <- instant
	}
	return len(waiters)
}

// awaitWaiters blocks until at least count loops are waiting for a tick, or fails the
// test. It is how a test synchronises with the scheduler without sleeping for a guessed
// duration.
func (c *fixedClock) awaitWaiters(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.waiting() < count {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d loops are waiting for a tick after five seconds",
				c.waiting(), count)
		}
		runtime.Gosched()
	}
}

// advance moves the clock forward.
func (c *fixedClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(by)
}

// newTestStore opens a database under t.TempDir, so no test ever writes a database,
// a -wal or a -shm file into the working tree.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(context.Background(), storetest.Database(t))
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database: %v", err)
		}
	})
	return database
}

// countRows counts a table, failing the test on an error.
func countRows(t *testing.T, database *store.Store, table string) int {
	t.Helper()
	count, err := database.CountRows(context.Background(), table)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return count
}

// arrangeDiscoverableFirewall configures the fake with the discovery responses and
// runs one discovery pass, which is the precondition of every collector.
func arrangeDiscoverableFirewall(t *testing.T, fake *providertest.Firewall, collector *Collector) {
	t.Helper()
	fake.AnswerFixture(opnsense.InterfacesInfo, "interfaces_info.json")
	fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
	// The firewall's clock, from which discovery measures its offset from UTC: the
	// fixture is a firewall on UTC at the reference instant, so the offset is zero.
	fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
}

// referenceInstant is the clock every collector test runs at.
//
// It is the day after the instants the fixtures carry, so a year-less filter-log
// timestamp resolves against a reference that is a few minutes ahead of it rather than
// a year away — which is what a real poll looks like, and what makes the year inference
// meaningful here rather than trivially satisfied.
func referenceInstant() time.Time {
	return time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
}

// referenceEpoch is referenceInstant as the UTC epoch every column stores.
func referenceEpoch() int64 { return referenceInstant().Unix() }

// newTestCollector is a collector for a test: its broker's ceilings raised past anything a test
// reaches -- a test's clock does not move, so a calls-per-second ceiling would hold its calls for
// ever -- and its broker closed when the test ends, so no subscription holds the fake firewall.
func newTestCollector(t *testing.T, client *opnsense.Client, database *store.Store, clock Clock) *Collector {
	t.Helper()
	collector := New(client, database, clock)
	collector.Broker().Scheduler().SetSettings(schedule.Settings{MaxInFlight: 64, MaxPerSecond: 1e6})
	// A test runs its passes back to back at one frozen instant, where a pass at its interval would
	// find the answer it read an instant before fresh in the mirror: its readers' max ages are zero,
	// so every pass asks the firewall, as each test arranges it. The broker's serving from its mirror
	// is internal/broker's tests' subject.
	settings := config.Defaults()
	settings.DiscoveryInterval, settings.FirewallLogInterval, settings.SecurityEventInterval = 0, 0, 0
	settings.MeasurementInterval, settings.DHCPLeaseInterval, settings.DNSLookupInterval = 0, 0, 0
	settings.ResolverCacheInterval, settings.ResolverLocalDataInterval = 0, 0
	collector.Configure(settings)
	t.Cleanup(collector.Broker().Close)
	return collector
}
