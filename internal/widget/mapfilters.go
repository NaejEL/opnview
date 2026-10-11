package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// BlockedOnly keeps the blocked part of figures, in their measure, with its own packets and coverage
// (store.VolumeFigures.BlockedPart).
func BlockedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return figures.BlockedPart()
}
