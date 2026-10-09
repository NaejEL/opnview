package store

import (
	"context"
	"fmt"
	"sort"
)

// The aggregate refresh.
//
// THE CONTRACT, written down once in docs/data-model.md and implemented here:
//
//  1. A slot is recomputed when a flow is ingested whose observed_at falls inside
//     it. The recomputation replaces the slot.
//  2. The current slot of each period -- the one holding the present instant -- is
//     recomputed on every pass, because it is still filling, WHATEVER THE
//     RETENTION.
//  3. computed_at is the refresh's WATERMARK: every flow stamped before it is
//     known to have been committed when the slot was read. A slot whose computed_at
//     is strictly later than the newest ingested_at among the flows inside its
//     window, and than the newest purged_at of its purged part, is not rewritten;
//     one whose computed_at is equal or earlier is stale. That comparison is the
//     whole staleness rule and there is no dirty flag.
//  4. A closed slot is never recomputed once no flow inside it has been ingested,
//     and no purge has written its purged part, since its computed_at, so
//     back-filled history reopens only the slots it touches.
//
// THE PURGE AND THE REFRESH. The collector never lets the retention purge run
// between a pass storing its rows and the end of that pass's derivation
// (internal/collect, purge.go). Should that ordering ever break, the hours a purge
// wrote a purged part for since the previous refresh are selected all the same
// (rules 1 and 3 read purged_flow_hour.purged_at beside flow.ingested_at), so a
// flow purged before any refresh read it still reaches its hour, day, week and
// month at the next refresh.
//
// Two additions, each the consequence of a derivation that can change a stored
// flow without ingesting one: classification and re-pointing a client identity
// rewrite flows' interfaces and clients, and attribution adds or removes a flow's
// site name. Both report the instants they touched, and those slots are rewritten
// whatever their computed_at says. The rule the data model carried as its fifth --
// that the 7 d and 30 d slots may lean on the firewall's own daily per-pair
// aggregate -- is struck: the Insight aggregate endpoints answer 404 on 26.7
// (survey, "Verified against a live firewall, 2026-09-27", "Wrong paths"), so every
// slot of every period is computed from opnview's own flows.
//
// HOW A SLOT IS COMPUTED, and why the retention cannot empty one. An hour is
// computed from the flows still inside it PLUS its purged part: the rows the
// retention purge wrote to purged_flow_hour for the flows it removed from that
// hour. A day is composed from its hours, and a week and a month are rolled up
// from their days, which the purge keeps as long as the week and the month. So a
// slot holding the horizon -- the current hour, day, week and month under a
// retention shorter than an hour -- keeps what it held for the flows already
// purged, still takes in a flow ingested late into it, and follows a
// reclassification of the flows still present. Only a slot whose flows CAN have
// been purged has a purged part, and that is the only sense in which a slot
// straddles the horizon.
//
// A slot the purge would remove outright -- every flow of it, and for an hour or a
// day every slot its week and month are composed from, lies before the horizon --
// is not rewritten unless it is current: a late flow inside it is purged at the
// next pass anyway, and its purged part then joins the hour's.
//
// Every family is rewritten together, slot by slot, in one transaction per slot;
// the day slot rewrites the derived per-pair volume as well.

// baseFamilies are the families an hour computes from `flow` and its purged part,
// and a day, a week or a month composes from its finer slots, in order.
// regroupedFamilies follow in every period: the client family is regrouped from
// the peer family of the same slot, the owner family from the client family.
var (
	baseFamilies      = []string{"volume", "rule", "domain", "peer"}
	regroupedFamilies = []string{"client", "owner"}
)

// SlotRef names one slot of one period.
type SlotRef struct {
	// Period is the period's name.
	Period string
	// Start is the slot's start, a UTC epoch.
	Start int64
}

// Refresh is what one refresh rewrote.
type Refresh struct {
	// Rewritten is every slot rewritten, in the order it was rewritten.
	Rewritten []SlotRef
}

// RefreshAggregates applies the contract with nothing ingesting concurrently: the
// refresh's watermark is its own instant. See RefreshAggregatesAt.
func (s *Store) RefreshAggregates(ctx context.Context, since, now int64, forced []int64) (Refresh, error) {
	return s.RefreshAggregatesAt(ctx, since, now, now, forced)
}

// RefreshAggregatesAt applies the contract: every slot holding a flow ingested at or
// after since or a purged part a purge wrote at or after since, every slot holding
// one of the forced instants, and the current slot
// of each period; a slot that is neither current nor forced is skipped when it is
// not stale.
//
// now is the present instant, which decides the current slots and the flow
// horizon. watermark is the instant written as computed_at: the instant BEFORE
// WHICH every flow stamped with an ingested_at is known to be committed. A caller
// with ingestions in flight passes the oldest of their stamps, because a row stamped
// with it may commit after this refresh has read its slot; a caller with none
// passes now. A slot whose computed_at is not strictly later than the newest
// ingested_at inside it is stale, so a row stamped at the watermark -- or in the
// same second as a refresh -- is read again by the next refresh rather than lost.
//
// The periods are refreshed finest first, so a day is composed from hours already
// rewritten by the same pass, and a week or a month from days.
func (s *Store) RefreshAggregatesAt(ctx context.Context, since, watermark, now int64,
	forced []int64) (Refresh, error) {
	var result Refresh
	hours, err := queryInts(ctx, s.db, "dirty_hours", map[string]any{"since": since})
	if err != nil {
		return result, err
	}
	horizon, bounded, err := queryOptionalInt(ctx, s.db, "flow_horizon", map[string]any{"now": now})
	if err != nil {
		return result, err
	}

	// Every slot of a period runs the same statements, each slot in its own
	// transaction, so the statements are prepared once for the whole refresh
	// (statementCache, exec.go).
	cache := newStatementCache(s.db)
	defer cache.close()

	for _, period := range Periods() {
		candidates := map[int64]bool{}
		for _, hour := range hours {
			candidates[period.SlotStart(hour)] = false
		}
		for _, instant := range forced {
			candidates[period.SlotStart(instant)] = true
		}
		current := period.SlotStart(now)
		candidates[current] = true

		starts := make([]int64, 0, len(candidates))
		for start := range candidates {
			starts = append(starts, start)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

		for _, start := range starts {
			end := period.SlotEnd(start)
			if start != current && bounded && period.Purgeable(start, horizon) {
				continue
			}
			if !candidates[start] {
				stale, err := slotIsStale(ctx, cache.direct(), period, start, end)
				if err != nil {
					return result, err
				}
				if !stale {
					continue
				}
			}
			if err := cache.warm(ctx); err != nil {
				return result, err
			}
			if err := s.rewriteSlot(ctx, cache, period, start, end, watermark); err != nil {
				return result, err
			}
			result.Rewritten = append(result.Rewritten, SlotRef{Period: period.Name, Start: start})
		}
	}
	return result, nil
}

// slotIsStale is rule 3: a slot is stale when its computed_at is not strictly later
// than the newest ingested_at among the flows inside it or the newest purged_at of its
// purged part, and when it holds rows while neither is left inside it. Equal instants
// count as stale because one-second stamps cannot be ordered: a row stamped in the
// second a refresh ran may have committed after the refresh read the slot.
func slotIsStale(ctx context.Context, q querier, period Period, start, end int64) (bool, error) {
	newest, hasFlows, err := queryOptionalInt(ctx, q, "slot_newest_ingested",
		map[string]any{"slot_start": start, "slot_end": end})
	if err != nil {
		return false, err
	}
	text, err := PeriodStatement("slot_computed_at", period)
	if err != nil {
		return false, err
	}
	computed, hasRows, err := queryOptionalIntText(ctx, q, "slot_computed_at", text,
		map[string]any{"slot_start": start})
	if err != nil {
		return false, err
	}
	switch {
	case !hasFlows:
		return hasRows, nil
	case !hasRows:
		return true, nil
	default:
		return computed <= newest, nil
	}
}

// rewriteSlot replaces one slot of every family, in one transaction: an hour from
// `flow` and its purged part, any other period composed from its finer period's
// slots, and the client and owner families regrouped from the slot itself.
func (s *Store) rewriteSlot(ctx context.Context, cache *statementCache, period Period,
	start, end, computedAt int64) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the %s slot at %d: %w", period.Name, start, err)
	}
	defer func() { _ = transaction.Rollback() }()
	cached := cache.in(transaction)

	parameters := map[string]any{"slot_start": start, "slot_end": end, "now": computedAt}
	for _, family := range append(append([]string{}, baseFamilies...), regroupedFamilies...) {
		deletion := "delete_" + family
		text, err := PeriodStatement(deletion, period)
		if err != nil {
			return err
		}
		if _, err := execText(ctx, cached, deletion, text, parameters); err != nil {
			return err
		}
	}
	for _, family := range baseFamilies {
		var name, text string
		if _, composed := period.Child(); composed {
			name = "compose_" + family
			text, err = ComposeStatement(name, period)
		} else {
			name = "insert_" + family
			text, err = Statement(name)
		}
		if err != nil {
			return err
		}
		if _, err := execText(ctx, cached, name, text, parameters); err != nil {
			return err
		}
	}
	for _, family := range regroupedFamilies {
		name := "regroup_" + family
		text, err := PeriodStatement(name, period)
		if err != nil {
			return err
		}
		if _, err := execText(ctx, cached, name, text, parameters); err != nil {
			return err
		}
	}
	// The per-pair volume of a day is derived from `flow` and has no purged part: the
	// purge removes the rows of a day starting before the horizon, which are exactly
	// the days a rewrite could no longer read whole.
	if period == PeriodDay {
		for _, name := range []string{"delete_pair_volume", "insert_pair_volume"} {
			if _, err := execNamed(ctx, cached, name, parameters); err != nil {
				return err
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the %s slot at %d: %w", period.Name, start, err)
	}
	return nil
}
