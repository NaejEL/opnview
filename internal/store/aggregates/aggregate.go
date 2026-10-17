package aggregates

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
// THE PURGE AND THE REFRESH. The collector never lets the retention purge run between a pass
// storing its rows and the end of that pass's derivation (internal/collect,
// internal/store/purge/purge.go). Should that ordering ever break, the hours a purge wrote a purged
// part for since the previous refresh are selected all the same (rules 1 and 3 read
// purged_flow_hour.purged_at beside flow.ingested_at), so a flow purged before any refresh read it
// still reaches its hour, day, week and month at the next refresh.
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
// Every family is rewritten together: the hours of a refresh in one transaction for the set
// (rewriteHours), every coarser slot in one transaction per slot; the day slot rewrites the
// derived per-pair volume as well.

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
func (s *AggregateStore) RefreshAggregates(ctx context.Context, since, now int64, forced []int64) (Refresh, error) {
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
func (s *AggregateStore) RefreshAggregatesAt(ctx context.Context, since, watermark, now int64,
	forced []int64) (Refresh, error) {
	var result Refresh
	hours, err := sqldb.QueryInts(ctx, s.SQL, "dirty_hours", map[string]any{"since": since})
	if err != nil {
		return result, err
	}
	// The hours a change of ownership invalidated (internal/store/owners/owners.go): forced, like
	// the instants a re-pointed identity forces, and forgotten once this refresh has rewritten
	// them.
	reassigned, err := owners.ReadReassignedHours(ctx, s.SQL)
	if err != nil {
		return result, err
	}
	forced = append(append([]int64(nil), forced...), reassigned.Hours...)
	horizon, bounded, err := sqldb.QueryOptionalInt(ctx, s.SQL, "flow_horizon", map[string]any{"now": now})
	if err != nil {
		return result, err
	}

	for _, period := range vocab.Periods() {
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

		if period == vocab.PeriodHour {
			// The hours are rewritten together (aggregatehours.statements.sql): the same rows as
			// one slot at a time, in one transaction for the set.
			rewritten, err := s.rewriteHours(ctx, starts, candidates, current, bounded, horizon, watermark)
			if err != nil {
				return result, err
			}
			for _, start := range rewritten {
				result.Rewritten = append(result.Rewritten, SlotRef{Period: period.Name, Start: start})
			}
			continue
		}

		rewritten, err := s.rewriteSlots(ctx, period, starts, candidates, current, bounded, horizon, watermark)
		if err != nil {
			return result, err
		}
		for _, start := range rewritten {
			result.Rewritten = append(result.Rewritten, SlotRef{Period: period.Name, Start: start})
		}
	}
	if err := owners.ForgetReassignedHours(ctx, s.SQL, reassigned.IDs); err != nil {
		return result, err
	}
	// Every slot rewritten is committed: the listeners hear which periods changed.
	if len(result.Rewritten) > 0 {
		var periods []string
		for _, slot := range result.Rewritten {
			if !plugindecl.Contains(periods, slot.Period) {
				periods = append(periods, slot.Period)
			}
		}
		if err := sqldb.Notify(ctx, s.SQL, sqldb.ChannelAggregateRefresh, map[string]any{"periods": periods}); err != nil {
			return result, err
		}
	}
	return result, nil
}

// rewriteHours selects, among the candidate hours, those the contract rewrites -- a forced or the
// current hour, or a stale one, never one the purge would remove outright unless current -- and
// rewrites them together, in one transaction. It returns the hours rewritten, in order.
func (s *AggregateStore) rewriteHours(ctx context.Context, starts []int64, forced map[int64]bool, current int64,
	bounded bool, horizon, watermark int64) ([]int64, error) {
	var kept, unforced []int64
	for _, start := range starts {
		if start != current && bounded && vocab.PeriodHour.Purgeable(start, horizon) {
			continue
		}
		kept = append(kept, start)
		if !forced[start] {
			unforced = append(unforced, start)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	stale := map[int64]bool{}
	if len(unforced) > 0 {
		listed, err := sqldb.QueryInts(ctx, s.SQL, "stale_hours", hourParameters(unforced, watermark))
		if err != nil {
			return nil, err
		}
		for _, start := range listed {
			stale[start] = true
		}
	}
	var rewrite []int64
	for _, start := range kept {
		if forced[start] || stale[start] {
			rewrite = append(rewrite, start)
		}
	}
	if len(rewrite) == 0 {
		return nil, nil
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: starting the hour slots: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	parameters := hourParameters(rewrite, watermark)
	for _, name := range []string{"delete_volume_hours", "delete_rule_hours", "delete_domain_hours",
		"delete_peer_hours", "delete_client_hours", "delete_owner_hours", "insert_volume_hours",
		"insert_rule_hours", "insert_domain_hours", "insert_peer_hours", "regroup_client_hours",
		"regroup_owner_hours"} {
		if _, err := sqldb.ExecNamed(ctx, transaction, name, parameters); err != nil {
			return nil, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing the hour slots: %w", err)
	}
	return rewrite, nil
}

// hourParameters are the parameters of the hour statements over a set of hours.
func hourParameters(hours []int64, watermark int64) map[string]any {
	encoded, _ := json.Marshal(hours)
	return map[string]any{"hours": string(encoded), "from": hours[0], "to": hours[len(hours)-1] + 3600,
		"now": watermark}
}

// rewriteSlots is rewriteHours for a day, a week or a month: among the candidate slots of the period it
// selects those the contract rewrites -- a forced or the current slot, or a stale one, never one the
// purge would remove outright unless current -- and rewrites them together, in one transaction, every
// family composed from the finer period and the client and owner families regrouped
// (aggregateslots.statements.sql); a day rewrites the derived per-pair volume as well. Its statements
// are as many whatever the number of slots (PA17 of specs/SPEC-backend-completion-plugins.md). It
// returns the slots rewritten, in order.
func (s *AggregateStore) rewriteSlots(ctx context.Context, period vocab.Period, starts []int64,
	forced map[int64]bool, current int64, bounded bool, horizon, watermark int64) ([]int64, error) {
	var kept, unforced []int64
	for _, start := range starts {
		if start != current && bounded && period.Purgeable(start, horizon) {
			continue
		}
		kept = append(kept, start)
		if !forced[start] {
			unforced = append(unforced, start)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	stale := map[int64]bool{}
	if len(unforced) > 0 {
		text, err := sqldb.PeriodStatement("stale_slots", period)
		if err != nil {
			return nil, err
		}
		rows, err := sqldb.Query(ctx, s.SQL, text, slotParameters(period, unforced, watermark))
		if err != nil {
			return nil, fmt.Errorf("store: stale_slots: %w", err)
		}
		for rows.Next() {
			var start int64
			if err := rows.Scan(&start); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("store: stale_slots: %w", err)
			}
			stale[start] = true
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, fmt.Errorf("store: stale_slots: %w", err)
		}
	}
	var rewrite []int64
	for _, start := range kept {
		if forced[start] || stale[start] {
			rewrite = append(rewrite, start)
		}
	}
	if len(rewrite) == 0 {
		return nil, nil
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: starting the %s slots: %w", period.Name, err)
	}
	defer func() { _ = transaction.Rollback() }()
	parameters := slotParameters(period, rewrite, watermark)
	var names []string
	for _, family := range append(append([]string{}, baseFamilies...), regroupedFamilies...) {
		names = append(names, "delete_"+family+"_slots")
	}
	for _, family := range baseFamilies {
		names = append(names, "compose_"+family+"_slots")
	}
	for _, family := range regroupedFamilies {
		names = append(names, "regroup_"+family+"_slots")
	}
	for _, name := range names {
		text, err := sqldb.ComposeOrPeriodStatement(name, period)
		if err != nil {
			return nil, err
		}
		if _, err := sqldb.ExecText(ctx, transaction, name, text, parameters); err != nil {
			return nil, err
		}
	}
	// The per-pair volume of a day is derived from `flow` and has no purged part: the purge removes the
	// rows of a day starting before the horizon, which are exactly the days a rewrite could no longer
	// read whole.
	if period == vocab.PeriodDay {
		for _, name := range []string{"delete_pair_volume", "insert_pair_volume"} {
			if _, err := sqldb.ExecNamed(ctx, transaction, name, parameters); err != nil {
				return nil, err
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing the %s slots: %w", period.Name, err)
	}
	return rewrite, nil
}

// slotParameters are the parameters of the slot statements over a set of slots of one period.
func slotParameters(period vocab.Period, starts []int64, watermark int64) map[string]any {
	slots := make([][2]int64, len(starts))
	for index, start := range starts {
		slots[index] = [2]int64{start, period.SlotEnd(start)}
	}
	encoded, _ := json.Marshal(slots)
	return map[string]any{"slots": string(encoded), "from": starts[0], "to": period.SlotEnd(starts[len(starts)-1]),
		"now": watermark}
}
