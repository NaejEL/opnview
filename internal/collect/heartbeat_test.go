package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/collecttest"

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
		harness := collecttest.NewProbeHarness(t)
		start := collecttest.ReferenceInstant()
		if absence, err := collect.RecordStart(ctx, harness.Store, settings, harness.Clock); err != nil || absence != nil {
			t.Fatalf("a first start recorded %+v (%v)", absence, err)
		}
		task := collect.HeartbeatTask(live, harness.Store, harness.Clock)
		if task.RunAtStart {
			t.Error("the heartbeat runs at start, although RecordStart has just beaten")
		}
		if interval := task.Interval(); interval != time.Duration(settings.HeartbeatIntervalSeconds)*time.Second {
			t.Errorf("the heartbeat interval is %s", interval)
		}
		lastAlive := start.Add(5 * time.Minute)
		for at := start.Add(time.Minute); !at.After(lastAlive); at = at.Add(time.Minute) {
			collecttest.SetClock(harness, at)
			if err := task.Run(ctx); err != nil {
				t.Fatalf("a beat: %v", err)
			}
		}
		if clean {
			lastAlive = lastAlive.Add(10 * time.Second)
			collecttest.SetClock(harness, lastAlive)
			if err := collect.RecordCleanStop(ctx, harness.Store, harness.Clock); err != nil {
				t.Fatalf("the clean stop: %v", err)
			}
		}

		restart := lastAlive.Add(time.Hour)
		collecttest.SetClock(harness, restart)
		absence, err := collect.RecordStart(ctx, harness.Store, settings, harness.Clock)
		if err != nil {
			t.Fatalf("the restart: %v", err)
		}
		if absence == nil || absence.From != lastAlive.Unix() || absence.To != restart.Unix() ||
			absence.CleanShutdown != clean {
			t.Errorf("clean %t: the restart recorded %+v, not [%d, %d)", clean, absence,
				lastAlive.Unix(), restart.Unix())
		}
		if count := collecttest.CountRows(t, harness.Store, "opnview_downtime"); count != 1 {
			t.Errorf("clean %t: %d intervals recorded, not one", clean, count)
		}
	}
}
