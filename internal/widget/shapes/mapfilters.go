package shapes

import (
	"github.com/NaejEL/opnview/internal/store/reads"
)

// BlockedOnly keeps the blocked part of figures, in their measure, with its own packets and coverage
// (store.VolumeFigures.BlockedPart).
func BlockedOnly(figures reads.VolumeFigures) reads.VolumeFigures {
	return figures.BlockedPart()
}
