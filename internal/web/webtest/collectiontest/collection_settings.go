package collectiontest

import (
	"testing"
)

// DurationFieldShows says whether a card's duration field holds a figure in a unit.
func DurationFieldShows(t *testing.T, card, id, figure, unit string) bool {
	t.Helper()
	return FieldValue(card, id) == figure && SelectedIn(t, card, id+"_unit") == unit
}
