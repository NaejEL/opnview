package derive

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// The derivation that follows a collection pass is the aggregate refresh (ADR 0020): the
// classification, the pairing and the site-name attribution are the clients and unbound plugins'
// derivations, which stamp every flow they changed with changed_at; the refresh reads those flows'
// slots, after every filter-log pass and after every derivation run, and rewrites them.

// Derivation is what one pass asks to be derived.
type Derivation struct {
	// Instants are slots the refresh rewrites whatever else changed: the start instants of the connections a
	// plugin's derivation wrote measure rows for (PA4 of the plugin-API amendment).
	Instants []int64
}

// Derive runs one derivation. Derivations are serialised: two passes finishing
// together must not refresh the same slot from two half-classified states.
func (c *Deriver) Derive(ctx context.Context, request Derivation) error {
	c.DeriveMutex.Lock()
	defer c.DeriveMutex.Unlock()
	return c.DeriveLocked(ctx, request)
}

// DeriveLocked is derive for a caller already holding deriveMutex: the retention
// purge, which places what it is about to remove inside its own critical section
// (purge.go).
func (c *Deriver) DeriveLocked(ctx context.Context, request Derivation) error {
	now, watermark := c.refreshInstant()

	// The flows the plugins' derivations changed since the last refresh -- placed, paired or attributed
	// again (changed_at) -- have their slots rewritten.
	changed, err := c.Store.ChangedFlowInstants(ctx, c.refreshedSince())
	if err != nil {
		return err
	}
	forced := append([]int64(nil), changed...)

	// The slots of the connections a plugin's derivation wrote measure rows for (PA4, PA5): rewritten whatever
	// else changed, as a declared measure's parts count there.
	forced = append(forced, request.Instants...)

	// The slots are stamped with the watermark rather than now: a row of a pass still in
	// flight, stamped with its start, may commit after this refresh reads its slot, and
	// a slot stamped no later than that row stays stale until a refresh has read it.
	if _, err := c.Store.RefreshAggregatesAt(ctx, c.refreshedSince(), watermark, now, forced); err != nil {
		return fmt.Errorf("collect: refreshing the aggregates: %w", err)
	}
	c.setRefreshedSince(watermark)
	return nil
}

// DeriveAfter runs the derivation of what a pass stored, whether or not the pass
// failed part-way. A pass that stored rows and then failed -- a page that could not
// be written to the end, a gap that could not be recorded -- still leaves those
// rows placed, attributed and in their slots, and its own error is still returned,
// joined with the derivation's if that failed too.
func (c *Deriver) DeriveAfter(ctx context.Context, request Derivation, passErr error) error {
	deriveErr := c.Derive(ctx, request)
	if passErr == nil {
		return deriveErr
	}
	if deriveErr == nil {
		return passErr
	}
	return core.JoinErrors([]error{passErr, deriveErr})
}

// refreshedSince is the watermark of the last successful refresh: every flow
// stamped with an ingested_at at or after it may not have been read by a refresh
// yet, and none before it can still be unread. Zero before the first: the first
// refresh of a run finds every slot holding a flow, and skips each one whose
// computed_at is not older than its newest flow.
func (c *Deriver) refreshedSince() int64 {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.LastRefreshAt
}

func (c *Deriver) setRefreshedSince(at int64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.LastRefreshAt = at
}

// BeginIngest returns the instant a pass stamps its rows' ingested_at with, and
// records it as in flight until endIngest.
//
// WHY THE WATERMARK NEEDS IT. A pass stamps every row with the instant it began
// and commits them one by one afterwards, while the scheduler runs the other
// passes -- each ending in a derivation -- in their own goroutines. A derivation
// that ran between the stamp and the commit, and moved the watermark to its own
// instant, would leave those rows stamped BEFORE the watermark and unread: no
// later refresh would select their slots, and a flow whose classification changed
// nothing would never be forced either. So the instant is taken under the same
// lock that records it, and refreshInstant never moves the watermark past an
// instant still in flight.
func (c *Deriver) BeginIngest() (int64, uint64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	now := c.Now()
	if c.Ingesting == nil {
		c.Ingesting = map[uint64]int64{}
	}
	c.IngestSequence++
	c.Ingesting[c.IngestSequence] = now
	return now, c.IngestSequence
}

// EndIngest records that the pass holding token has written every row it stamped.
// Ending twice is harmless.
func (c *Deriver) EndIngest(token uint64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	delete(c.Ingesting, token)
}

// refreshInstant returns the instant a refresh writes as computed_at, and the
// watermark it may record once it has run: that instant, or the oldest ingestion
// still in flight when the refresh began, whichever is earlier. Both are read under
// one lock, so an ingestion that begins after this call stamps an instant no
// earlier than the refresh's own.
func (c *Deriver) refreshInstant() (int64, int64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	now := c.Now()
	watermark := now
	for _, stamped := range c.Ingesting {
		watermark = min(watermark, stamped)
	}
	return now, watermark
}
