package collect

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// opnview's heartbeat (SPEC-step-5b-widget-api.md, D2).
//
// WHILE OPNVIEW IS STOPPED, EVERY SOURCE GOES UNREAD AT ONCE, and nothing any endpoint
// returns afterwards says so: the next poll returns what the firewall still holds and is
// silent about what rotated out meanwhile. So the running service records that it is
// alive, on its own loop, and on start compares the last record with the clock; an
// absence longer than the threshold is written to opnview_downtime, one row per interval,
// which a widget reads as a gap of every provider it binds.
//
// The three moments, and who calls each: RecordStart, from cmd/opnview before any
// collection runs; the heartbeat task, which the scheduler runs on the interval the
// `heartbeat_interval_seconds` row sets; and RecordCleanStop, from cmd/opnview once the
// loops have stopped. A crash runs only the first two, and the next start bounds the
// interval by the last beat.

// RecordStart records the start of the service: the interval since the last heartbeat
// when it exceeds the threshold, and a heartbeat at now.
func RecordStart(ctx context.Context, database *store.Store, settings config.Config, clock Clock) (
	*store.NotRunningInterval, error) {
	return database.RecordStart(ctx, clock.Now().UTC().Unix(), settings.DowntimeThresholdSeconds)
}

// RecordCleanStop records a clean stop at now.
func RecordCleanStop(ctx context.Context, database *store.Store, clock Clock) error {
	return database.RecordCleanStop(ctx, clock.Now().UTC().Unix())
}

// HeartbeatTask is the loop that records that the service is alive. Its interval and its
// threshold are read from the live configuration on every turn, so a saved value takes
// effect without a restart. It does not run at start: RecordStart has just beaten.
func HeartbeatTask(settings *config.Live, database *store.Store, clock Clock) Task {
	return Task{
		Name: "heartbeat",
		Interval: settings.Interval(func(s config.Config) time.Duration {
			return time.Duration(s.HeartbeatIntervalSeconds) * time.Second
		}),
		Run: func(ctx context.Context) error {
			_, err := database.Heartbeat(ctx, clock.Now().UTC().Unix(),
				settings.Get().DowntimeThresholdSeconds)
			return err
		},
	}
}
