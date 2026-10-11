package widget

import (
	"context"

	"github.com/NaejEL/opnview/internal/store"
)

// When a provider's current availability state began (coverage row 39 of docs/widget-api.md: "the
// detection engine stopped at 06:12").
//
// IT IS AN INTERVAL, NEVER AN INSTANT. opnview sees a provider only when it checks it, so a
// change of state is known to lie after the last check that saw the previous state and no later
// than the first check that saw the current one: source_availability.state_since_after_at and
// state_since_by_at, written by Store.SetAvailability. A state never seen changing -- the first
// check after the row was created bounds nothing -- answers the named absence rather than an
// instant, and a provider with no availability row answers state_unknown.

// The named absences of a state's beginning.
const (
	// SinceNotObserved is a state no check has yet seen begin.
	SinceNotObserved = "state_change_not_observed"
	// SinceStateUnknown is a provider with no availability row at all.
	SinceStateUnknown = ConditionStateUnknown
)

// StateSince bounds when a provider's current state began: after AfterAt and by ByAt, or, with
// both null, the named absence in State.
type StateSince struct {
	AfterAt *int64  `json:"after_at"`
	ByAt    *int64  `json:"by_at"`
	State   *string `json:"state"`
}

// SinceKey is the key ReadAvailabilitySince answers a provider under: its kind, a slash, its key.
func SinceKey(kind, providerKey string) string { return kind + "/" + providerKey }

// ReadAvailabilitySince reads, for every registered provider, the bounds of when its current state
// began, by SinceKey.
func ReadAvailabilitySince(ctx context.Context, database *store.Store) (map[string]StateSince, error) {
	since := map[string]StateSince{}
	err := database.WidgetRows(ctx, "widget_availability_since", store.Period{}, nil, func(row store.RowScanner) error {
		var (
			kind, providerKey string
			hasRow            bool
			afterAt, byAt     store.NullInt64
		)
		if err := row.Scan(&kind, &providerKey, &hasRow, &afterAt, &byAt); err != nil {
			return err
		}
		since[SinceKey(kind, providerKey)] = BoundsOf(hasRow, NullableNumber(afterAt), NullableNumber(byAt))
		return nil
	})
	return since, err
}

// BoundsOf is the beginning of a state as a reader is told it: both bounds, or the named absence.
func BoundsOf(hasRow bool, afterAt, byAt *int64) StateSince {
	switch {
	case !hasRow:
		state := SinceStateUnknown
		return StateSince{State: &state}
	case afterAt == nil || byAt == nil:
		state := SinceNotObserved
		return StateSince{State: &state}
	}
	return StateSince{AfterAt: afterAt, ByAt: byAt}
}
