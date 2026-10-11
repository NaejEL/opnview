package collect

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/store"
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
		active, err := c.activeSources(ctx, kind)
		if err != nil {
			return err
		}
		for _, source := range active {
			if err := c.store.NotifyCollectionPass(ctx, kind, source.providerKey); err != nil {
				return err
			}
		}
		return nil
	}
}

// triggers routes the notifications heard to the loops whose provider named them.
type triggers struct {
	mutex  sync.Mutex
	routes map[string][]chan struct{}
}

// wakeOn returns the wake channel of a loop that the notifications named run, or nil when it names
// none. A name is "channel" or "channel:subject", the subject being the kind of a collection pass
// or the plugin of a plugin pass.
func (t *triggers) wakeOn(names []string) <-chan struct{} {
	if len(names) == 0 {
		return nil
	}
	wake := make(chan struct{}, 1)
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.routes == nil {
		t.routes = map[string][]chan struct{}{}
	}
	for _, name := range names {
		t.routes[name] = append(t.routes[name], wake)
	}
	return wake
}

// heard wakes every loop a notification names, without blocking: a loop already woken runs once.
func (t *triggers) heard(notification store.Notification) {
	subject := ""
	switch notification.Channel {
	case store.ChannelCollectionPass:
		subject, _ = notification.Payload["kind"].(string)
	case store.ChannelPluginPass:
		subject, _ = notification.Payload["plugin"].(string)
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, name := range []string{notification.Channel, notification.Channel + ":" + subject} {
		for _, wake := range t.routes[name] {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}

// channels returns the channels any loop listens for.
func (t *triggers) channels() []string {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	var channels []string
	for name := range t.routes {
		channel, _, _ := strings.Cut(name, ":")
		found := false
		for _, held := range channels {
			found = found || held == channel
		}
		if !found {
			channels = append(channels, channel)
		}
	}
	return channels
}

// listenRestart is the interval of the listening loop. Its pass returns only when the context ends
// -- Store.Listen opens a dropped connection again itself -- so it is a bound, never a cadence.
const listenRestart = time.Minute

// listenTask is the loop that hears the notifications the declared providers named, and wakes
// their loops. It runs only when one provider named one.
func (c *Collector) listenTask() []Task {
	channels := c.triggers.channels()
	if len(channels) == 0 {
		return nil
	}
	return []Task{{
		Name:     "notifications",
		Interval: FixedInterval(listenRestart),
		Run: func(ctx context.Context) error {
			return c.store.Listen(ctx, channels, c.triggers.heard)
		},
		RunAtStart: true,
	}}
}
