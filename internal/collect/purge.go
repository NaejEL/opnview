package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/derive"
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
//   - the purge holds purgeMutex for writing, and deriveMutex as well, so it also
//     never runs during a derivation with no pass behind it -- runtime discovery's,
//     or the refresh on its own.
//
// THE LOCK ORDER IS purgeMutex, THEN deriveMutex, THEN mutex, and nothing takes them
// the other way round: a pass takes purgeMutex before its derivation takes
// deriveMutex, and nothing holding deriveMutex ever waits for purgeMutex. A pass
// takes purgeMutex once and never again inside its own critical section, because a
// waiting purge blocks every new reader and a second acquisition would wait for
// itself. And none of the three is ever awaited while a database transaction is
// open, so the single connection the store holds cannot close the cycle either.
//
// THE PURGE PLACES WHAT IT REMOVES FIRST. Inside its critical section it runs a
// derivation of every unplaced end of a flow it is about to remove, so a row stored
// by a pass whose derivation failed, or by an earlier run that stopped before
// deriving, enters the purged part where the evidence held puts it. That derivation
// is also the first of a run when the purge runs before any pass has derived, and
// the first derivation of a run places every address (internal/collect/derive/derive.go).
//
// AND THE REFRESH DOES NOT DEPEND ON THE ORDERING. The hours a purge writes a purged part for are
// selected by the next refresh whatever ran before it
// (internal/store/aggregates/aggregate.statements.sql, dirty_hours), so the slot invariant would
// survive a broken ordering; orderingHooks.unordered is how a test proves it.

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
	c.DeriveMutex.Lock()
	defer c.DeriveMutex.Unlock()

	// The flows the purge removes were placed by the clients plugin's derivation after the pass that
	// stored them (ADR 0020 wave 3); the refresh before the purge reads what it changed since.
	now := c.Now()
	if err := c.DeriveLocked(ctx, derive.Derivation{}); err != nil {
		return fmt.Errorf("collect: refreshing before the purge: %w", err)
	}
	if err := c.Store.Purge(ctx, now); err != nil {
		return err
	}
	return c.PurgeMirror(ctx, now)
}
