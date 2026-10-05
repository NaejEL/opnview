package collect

import (
	"context"
	"fmt"
)

// The retention purge, and its ordering against the passes.
//
// WHY THE PURGE IS ORDERED. A pass stores its rows with neither end placed and
// derives them afterwards: it places them, decides their attribution, and refreshes
// the slots they fall in (derive.go). The purge moves every flow older than the
// horizon into the purged part of its hour (internal/store/purge.sql). Were it to
// commit between a pass storing its rows and that pass's refresh, the rows would
// leave `flow` before any refresh had read them, unplaced; nothing would ever select
// their hour again, and the hour, and the day, week and month composed from it,
// would lose them. So the purge never runs inside that window, and a pass never
// stores or derives while a purge runs:
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
// the first derivation of a run places every address (derive.go).
//
// AND THE REFRESH DOES NOT DEPEND ON THE ORDERING. The hours a purge writes a purged
// part for are selected by the next refresh whatever ran before it
// (internal/store/derive.sql, dirty_hours), so the slot invariant would survive a
// broken ordering; orderingHooks.unordered is how a test proves it.

// orderingHooks let a test stop a pass between its store and its derivation, see a
// purge wait for it, and turn the ordering off to show what the refresh does alone.
// Every field is nil or false outside a test, and nothing else sets them.
type orderingHooks struct {
	// afterStore runs in a filter-log pass once its rows are stored, before its
	// derivation.
	afterStore func()
	// purgeWaits runs when a purge finds a pass or a derivation in progress and is
	// about to wait for it.
	purgeWaits func()
	// unordered makes Purge run the store's purge alone, with no lock and no
	// placement before it.
	unordered bool
}

// storing marks the start of a pass's store-and-derive: it waits for a purge in
// progress, and keeps the next one waiting until the returned function is called,
// once the pass's derivation has ended.
func (c *Collector) storing() func() {
	c.purgeMutex.RLock()
	return c.purgeMutex.RUnlock
}

// Purge applies the retention purge, ordered against the passes as the comment above
// describes. It is the retention-purge loop's work.
func (c *Collector) Purge(ctx context.Context) error {
	if c.hooks.unordered {
		return c.store.Purge(ctx, c.now())
	}
	if !c.purgeMutex.TryLock() {
		if c.hooks.purgeWaits != nil {
			c.hooks.purgeWaits()
		}
		c.purgeMutex.Lock()
	}
	defer c.purgeMutex.Unlock()
	c.deriveMutex.Lock()
	defer c.deriveMutex.Unlock()

	// One instant for both, so the purge removes exactly the flows the derivation
	// was asked to place.
	now := c.now()
	due, err := c.store.PurgeDueAddresses(ctx, now)
	if err != nil {
		return err
	}
	if err := c.deriveLocked(ctx, derivation{addresses: due}); err != nil {
		return fmt.Errorf("collect: placing what the purge is about to remove: %w", err)
	}
	return c.store.Purge(ctx, now)
}
