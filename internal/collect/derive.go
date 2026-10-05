package collect

import (
	"context"
	"fmt"
)

// The derivation that follows a collection pass: classification, site-name
// attribution, and the aggregate refresh.
//
// WHY IT FOLLOWS THE PASS RATHER THAN RUNNING INSIDE IT. A flow's ends are placed
// from on-link evidence, and the evidence for one record can arrive in a record
// read after it -- later in the same page, in a lease, in a neighbour table. So a
// record is stored first with neither end placed, and once the whole pass is
// stored every address it touched is placed from everything held, in sorted
// order. That is what makes the result independent of the order the records
// arrived in. The same holds of attribution: a lookup is usually read after the
// flow it names, because the resolver is polled less often than the filter log,
// so the resolver pass decides again on every flow its lookups could name.
//
// The refresh runs after every filter-log pass, the contract's own rule, and after
// any derivation that rewrote a stored flow or an attribution, with those slots
// forced.

// derivation is what one pass asks to be derived.
type derivation struct {
	// addresses are placed, and every row naming them rewritten.
	addresses []string
	// attributeFrom and attributeTo bound the flows whose attribution is decided
	// again; attribute says whether there are any.
	attribute     bool
	attributeFrom int64
	attributeTo   int64
	// everything reclassifies every address any row carries, which is what a
	// change of the upstream interfaces or of the networks that count calls for.
	// derive sets it itself when what classification reads of the interfaces has
	// changed since the last derivation that placed every address.
	everything bool
	// hostnames resolves again the lookups whose logged host name named no single
	// address, which a lease pass calls for.
	hostnames bool
}

// widen extends the attribution window to cover [from, to].
func (d *derivation) widen(from, to int64) {
	if !d.attribute {
		d.attribute, d.attributeFrom, d.attributeTo = true, from, to
		return
	}
	d.attributeFrom = min(d.attributeFrom, from)
	d.attributeTo = max(d.attributeTo, to)
}

// spanOf returns the smallest and largest of some instants.
func spanOf(instants []int64) (int64, int64, bool) {
	if len(instants) == 0 {
		return 0, 0, false
	}
	low, high := instants[0], instants[0]
	for _, instant := range instants[1:] {
		low, high = min(low, instant), max(high, instant)
	}
	return low, high, true
}

// derive runs one derivation. Derivations are serialised: two passes finishing
// together must not refresh the same slot from two half-classified states.
func (c *Collector) derive(ctx context.Context, request derivation) error {
	c.deriveMutex.Lock()
	defer c.deriveMutex.Unlock()
	return c.deriveLocked(ctx, request)
}

// deriveLocked is derive for a caller already holding deriveMutex: the retention
// purge, which places what it is about to remove inside its own critical section
// (purge.go).
func (c *Collector) deriveLocked(ctx context.Context, request derivation) error {
	now, watermark := c.refreshInstant()
	maxDelay := c.attributionMaxDelay()

	// An interface that became or stopped being upstream, a link-local rule, or a
	// network discovery detected or the operator added, removed or confirmed: each
	// changes where an address belongs, so every stored address is placed again,
	// and each one whose own evidence moved is placed in full.
	onLink, err := c.store.OnLinkFingerprint(ctx)
	if err != nil {
		return err
	}
	c.mutex.RLock()
	if onLink != c.onLinkFingerprint {
		request.everything = true
	}
	c.mutex.RUnlock()

	if request.hostnames {
		// From the watermark of the previous lease pass: a lookup pass in flight then
		// held it back to its own stamp, so no lookup is skipped between two passes.
		c.mutex.RLock()
		since := c.hostnamesSince
		c.mutex.RUnlock()
		resolved, err := c.store.ResolveLoggedHostnames(ctx, since)
		if err != nil {
			return err
		}
		c.mutex.Lock()
		c.hostnamesSince = max(c.hostnamesSince, watermark)
		c.mutex.Unlock()
		request.addresses = append(request.addresses, resolved.Addresses...)
		if low, high, ok := spanOf(resolved.Instants); ok {
			request.widen(low, high+maxDelay)
		}
	}

	var forced []int64
	var placed []int64
	var lookups []int64
	if request.everything {
		result, err := c.store.ReclassifyAll(ctx, now)
		if err != nil {
			return err
		}
		placed, lookups = result.FlowInstants, result.LookupInstants
	} else if len(request.addresses) > 0 {
		result, err := c.store.Reclassify(ctx, request.addresses, now)
		if err != nil {
			return err
		}
		placed, lookups = result.FlowInstants, result.LookupInstants
	}
	forced = append(forced, placed...)

	// A flow whose source client changed may now match other lookups, and a lookup
	// whose client changed may now match other flows.
	if low, high, ok := spanOf(placed); ok {
		request.widen(low, high)
	}
	if low, high, ok := spanOf(lookups); ok {
		request.widen(low, high+maxDelay)
	}
	if request.attribute {
		result, err := c.store.Attribute(ctx, request.attributeFrom, request.attributeTo, maxDelay, now)
		if err != nil {
			return err
		}
		forced = append(forced, result.FlowInstants...)
	}

	// The slots are stamped with the watermark rather than now: a row of a pass still in
	// flight, stamped with its start, may commit after this refresh reads its slot, and
	// a slot stamped no later than that row stays stale until a refresh has read it.
	if _, err := c.store.RefreshAggregatesAt(ctx, c.refreshedSince(), watermark, now, forced); err != nil {
		return fmt.Errorf("collect: refreshing the aggregates: %w", err)
	}
	c.setRefreshedSince(watermark)

	if request.everything {
		// Every reference to a remote-address client was cleared by the
		// reclassification and every slot it fed was rewritten above, so the rows
		// themselves can go now.
		if _, err := c.store.PurgeUnreferencedClients(ctx); err != nil {
			return err
		}
		c.mutex.Lock()
		c.onLinkFingerprint = onLink
		c.mutex.Unlock()
	}
	return nil
}

// onLinkChanged reports whether what classification reads of the interfaces has
// changed since the last derivation that placed every address.
func (c *Collector) onLinkChanged(ctx context.Context) (bool, error) {
	onLink, err := c.store.OnLinkFingerprint(ctx)
	if err != nil {
		return false, err
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return onLink != c.onLinkFingerprint, nil
}

// deriveAfter runs the derivation of what a pass stored, whether or not the pass
// failed part-way. A pass that stored rows and then failed -- a page that could not
// be written to the end, a gap that could not be recorded -- still leaves those
// rows placed, attributed and in their slots, and its own error is still returned,
// joined with the derivation's if that failed too.
func (c *Collector) deriveAfter(ctx context.Context, request derivation, passErr error) error {
	if len(request.addresses) == 0 && !request.attribute {
		if passErr != nil {
			return passErr
		}
		return c.derive(ctx, request)
	}
	deriveErr := c.derive(ctx, request)
	if passErr == nil {
		return deriveErr
	}
	if deriveErr == nil {
		return passErr
	}
	return joinErrors([]error{passErr, deriveErr})
}

// refreshedSince is the watermark of the last successful refresh: every flow
// stamped with an ingested_at at or after it may not have been read by a refresh
// yet, and none before it can still be unread. Zero before the first: the first
// refresh of a run finds every slot holding a flow, and skips each one whose
// computed_at is not older than its newest flow.
func (c *Collector) refreshedSince() int64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.lastRefreshAt
}

func (c *Collector) setRefreshedSince(at int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.lastRefreshAt = at
}

// beginIngest returns the instant a pass stamps its rows' ingested_at with, and
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
func (c *Collector) beginIngest() (int64, uint64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	now := c.now()
	if c.ingesting == nil {
		c.ingesting = map[uint64]int64{}
	}
	c.ingestSequence++
	c.ingesting[c.ingestSequence] = now
	return now, c.ingestSequence
}

// endIngest records that the pass holding token has written every row it stamped.
// Ending twice is harmless.
func (c *Collector) endIngest(token uint64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.ingesting, token)
}

// refreshInstant returns the instant a refresh writes as computed_at, and the
// watermark it may record once it has run: that instant, or the oldest ingestion
// still in flight when the refresh began, whichever is earlier. Both are read under
// one lock, so an ingestion that begins after this call stamps an instant no
// earlier than the refresh's own.
func (c *Collector) refreshInstant() (int64, int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	now := c.now()
	watermark := now
	for _, stamped := range c.ingesting {
		watermark = min(watermark, stamped)
	}
	return now, watermark
}

// attributionMaxDelay is the attribution_max_delay_seconds setting in force.
func (c *Collector) attributionMaxDelay() int64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.maxDelaySeconds
}

// ReclassifyEverything places every address any stored row carries and refreshes
// every slot that moved. It is what clears the remote-address clients an earlier
// classification created, and running it again changes nothing.
func (c *Collector) ReclassifyEverything(ctx context.Context) error {
	return c.derive(ctx, derivation{everything: true})
}

// RefreshAggregates runs the refresh on its own, with nothing forced.
func (c *Collector) RefreshAggregates(ctx context.Context) error {
	return c.derive(ctx, derivation{})
}
