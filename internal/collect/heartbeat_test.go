package collect

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// TestTheHeartbeatLoopAndTheStartRecordBoundTheIntervalOpnviewWasNotRunning is AD3 through
// the wiring cmd/opnview uses: RecordStart before collection, the heartbeat task on its
// configured interval, RecordCleanStop at the stop, and a crash that runs only the first
// two. The clock is the test's.
func TestTheHeartbeatLoopAndTheStartRecordBoundTheIntervalOpnviewWasNotRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	settings := config.Defaults()
	live := config.NewLive(settings)

	for _, clean := range []bool{true, false} {
		harness := newProbeHarness(t)
		start := referenceInstant()
		if absence, err := RecordStart(ctx, harness.store, settings, harness.clock); err != nil || absence != nil {
			t.Fatalf("a first start recorded %+v (%v)", absence, err)
		}
		task := HeartbeatTask(live, harness.store, harness.clock)
		if task.RunAtStart {
			t.Error("the heartbeat runs at start, although RecordStart has just beaten")
		}
		if interval := task.Interval(); interval != time.Duration(settings.HeartbeatIntervalSeconds)*time.Second {
			t.Errorf("the heartbeat interval is %s", interval)
		}
		lastAlive := start.Add(5 * time.Minute)
		for at := start.Add(time.Minute); !at.After(lastAlive); at = at.Add(time.Minute) {
			setClock(harness, at)
			if err := task.Run(ctx); err != nil {
				t.Fatalf("a beat: %v", err)
			}
		}
		if clean {
			lastAlive = lastAlive.Add(10 * time.Second)
			setClock(harness, lastAlive)
			if err := RecordCleanStop(ctx, harness.store, harness.clock); err != nil {
				t.Fatalf("the clean stop: %v", err)
			}
		}

		restart := lastAlive.Add(time.Hour)
		setClock(harness, restart)
		absence, err := RecordStart(ctx, harness.store, settings, harness.clock)
		if err != nil {
			t.Fatalf("the restart: %v", err)
		}
		if absence == nil || absence.From != lastAlive.Unix() || absence.To != restart.Unix() ||
			absence.CleanShutdown != clean {
			t.Errorf("clean %t: the restart recorded %+v, not [%d, %d)", clean, absence,
				lastAlive.Unix(), restart.Unix())
		}
		if count := countRows(t, harness.store, "opnview_downtime"); count != 1 {
			t.Errorf("clean %t: %d intervals recorded, not one", clean, count)
		}
	}
}
