package collect

import (
	"context"
)

// The retention purge, and its ordering against the passes.
//
// WHY THE PURGE IS ORDERED. A pass stores its rows with neither end placed and derives them
// afterwards: it places them, decides their attribution, and refreshes the slots they fall in
// (internal/collect/derive/derive.go). The purge moves every flow older than the horizon into the
// purged part of its hour (internal/store/purge/purge.sql). Were it to commit between a pass
// storing its rows and that pass's refresh, the rows would leave `flow` before any refresh had read
// them, unplaced; nothing would ever select their hour again, and the hour, and the day, week and
// month composed from it, would lose them. So the purge never runs inside that window, and a pass
// never stores or derives while a purge runs:
//
//   - every pass that stores rows holds purgeMutex for reading, from before it stores
//     its first row until its derivation has ended (see storing). Passes still run
//     side by side, as the scheduler intends;
//   - the purge holds purgeMutex for writing.
//
// A pass takes purgeMutex once and never again inside its own critical section, because a waiting
// purge blocks every new reader and a second acquisition would wait for itself; and it is never
// awaited while a database transaction is open.
//
// AND THE REFRESH DOES NOT DEPEND ON THE ORDERING. The hours a purge writes a purged part for are
// selected by the rollups plugin's next refresh whatever ran before it (plugins/rollups,
// refresh_hours), so the slot invariant would survive a broken ordering; orderingHooks.unordered is
// how a test proves it.

// Purge applies the retention purge, ordered against the passes as the comment above
// describes. It is the retention-purge loop's work.
func (c *Collector) Purge(ctx context.Context) error {
	if c.Hooks.Unordered {
		return c.Store.Purge(ctx, c.Now())
	}
	if !c.PurgeMutex.TryLock() {
		if c.Hooks.PurgeWaits != nil {
			c.Hooks.PurgeWaits()
		}
		c.PurgeMutex.Lock()
	}
	defer c.PurgeMutex.Unlock()
	// The flows the purge removes were placed by the clients plugin's derivation after the pass that
	// stored them (ADR 0020 wave 3), and the hours it writes a purged part for are selected by the
	// rollups plugin's next refresh whatever ran before (ADR 0021).
	now := c.Now()
	if err := c.Store.Purge(ctx, now); err != nil {
		return err
	}
	return c.PurgeMirror(ctx, now)
}
