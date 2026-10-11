package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// BlockedOnly keeps the blocked half of figures, in their measure (store.SplitNetflow).
func BlockedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return store.SplitNetflow(figures, store.VolumeFigures{Bytes: figures.BlockedBytes,
		BlockedBytes: figures.BlockedBytes, BlockedConnections: figures.BlockedConnections}, figures.BlockedConnections)
}
