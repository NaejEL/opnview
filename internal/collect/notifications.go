package collect

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// The notifications a pass publishes, and the passes a notification triggers (P7 of
// specs/SPEC-backend-completion-plugins.md).
//
// A collection pass of a core kind that ended without an error publishes one notification per
// provider it read; a pass of a plugin's own kind publishes one; the aggregate refresh publishes its own
// (store.RefreshAggregatesAt). A provider of a kind of its own -- or of reconciled_state -- may name
// notifications in its manifest that run its pass besides its cadence, never more often than its
// declared lower bound and never concurrently with itself: its loop runs one pass at a time, and a
// woken pass waits out the gap (Task.MinGap).

// afterPass wraps a collection pass: once it returned with no error, the providers of its kind
// opnview reads are announced.
func (c *Collector) afterPass(kind string, pass func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := pass(ctx); err != nil {
			return err
		}
		active, err := c.ActiveSources(ctx, kind)
		if err != nil {
			return err
		}
		for _, source := range active {
			if err := c.Store.NotifyCollectionPass(ctx, kind, source.ProviderKey); err != nil {
				return err
			}
		}
		return nil
	}
}

// listenRestart is the interval of the listening loop. Its pass returns only when the context ends
// -- Store.Listen opens a dropped connection again itself -- so it is a bound, never a cadence.
const listenRestart = time.Minute

// listenTask is the loop that hears the notifications the declared providers named, and wakes
// their loops. It runs only when one provider named one.
func (c *Collector) listenTask() []core.Task {
	channels := c.Triggers.Channels()
	if len(channels) == 0 {
		return nil
	}
	return []core.Task{{
		Name:     "notifications",
		Interval: core.FixedInterval(listenRestart),
		Run: func(ctx context.Context) error {
			return c.Store.Listen(ctx, channels, c.Triggers.Heard)
		},
		RunAtStart: true,
	}}
}
