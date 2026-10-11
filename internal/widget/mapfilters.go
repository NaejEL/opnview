package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// BlockedOnly keeps the blocked half of figures.
func BlockedOnly(figures store.VolumeFigures) store.VolumeFigures {
	return store.VolumeFigures{Bytes: figures.BlockedBytes, BlockedBytes: figures.BlockedBytes,
		BlockedConnections: figures.BlockedConnections}
}
