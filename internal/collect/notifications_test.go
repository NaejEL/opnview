package collect

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestATriggeredPassRunsAfterTheEventNeverSoonerThanItsBoundNorConcurrently is P-AC14's second
// half, on an injected clock: a provider that names a notification runs a pass when it is heard,
// waits out its lower bound since the last pass began before it does, folds the notifications
// heard during a pass into one more pass, and never runs two passes at once. A notification it did
// not name wakes nothing.
func TestATriggeredPassRunsAfterTheEventNeverSoonerThanItsBoundNorConcurrently(t *testing.T) {
	t.Parallel()
	clock := newFixedClock(referenceInstant())
	var routes core.Triggers
	wake := routes.WakeOn([]string{sqldb.ChannelCollectionPass + ":dhcp_lease"})
	if channels := routes.Channels(); len(channels) != 1 || channels[0] != sqldb.ChannelCollectionPass {
		t.Fatalf("the listener would listen on %v", channels)
	}

	const bound = 10 * time.Minute
	var passes, running, overlapped atomic.Int64
	started := make(chan time.Time, 16)
	release := make(chan struct{})
	var holding atomic.Bool
	task := core.Task{Name: "example_kind example_reader", Interval: core.FixedInterval(time.Hour), RunAtStart: true,
		Wake: wake, MinGap: bound,
		Run: func(context.Context) error {
			if running.Add(1) > 1 {
				overlapped.Add(1)
			}
			defer running.Add(-1)
			passes.Add(1)
			started <- clock.Now()
			if holding.Load() {
				<-release
			}
			return nil
		}}

	ctx, cancel := context.WithCancel(context.Background())
	var finished sync.WaitGroup
	finished.Add(1)
	go func() {
		defer finished.Done()
		_ = core.Run(ctx, clock, []core.Task{task}, nil)
	}()
	first := <-started

	// A notification of another kind wakes nothing.
	routes.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "firewall_log", "provider_key": "pf"}})
	// The one it named does, but only once the bound has passed since the first pass began.
	routes.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
		Payload: map[string]any{"kind": "dhcp_lease", "provider_key": "kea"}})
	clock.awaitWaiters(t, 2) // the abandoned interval wait, and the wait for the bound
	if got := passes.Load(); got != 1 {
		t.Fatalf("a woken pass ran before its lower bound: %d passes", got)
	}
	clock.advance(bound)
	clock.releaseAll()
	second := <-started
	if gap := second.Sub(first); gap < bound {
		t.Errorf("two passes began %v apart, below the bound of %v", gap, bound)
	}

	// Three notifications heard while a pass runs make one pass more, not three, and none of
	// them starts while the pass runs.
	holding.Store(true)
	clock.advance(time.Hour)
	clock.awaitWaiters(t, 1)
	clock.releaseAll()
	<-started
	for range 3 {
		routes.Heard(sqldb.Notification{Channel: sqldb.ChannelCollectionPass,
			Payload: map[string]any{"kind": "dhcp_lease"}})
	}
	holding.Store(false)
	release <- struct{}{}
	clock.awaitWaiters(t, 2) // the abandoned interval wait, and the wait for the bound
	clock.advance(bound)
	clock.releaseAll()
	<-started
	clock.awaitWaiters(t, 1)
	if got := passes.Load(); got != 4 {
		t.Errorf("three notifications during a pass gave %d passes in all, want 4", got)
	}
	if overlapped.Load() != 0 {
		t.Error("two passes of one provider ran at once")
	}
	cancel()
	clock.releaseAll()
	finished.Wait()
}
