package flows

import "context"

// What this package's external tests call that the package keeps unexported.

// HeldMargin is heldMargin.
var HeldMargin = heldMargin

// OverlapsLinkLocal is overlapsLinkLocal.
var OverlapsLinkLocal = overlapsLinkLocal

// PairChunks is pairChunks.
var PairChunks = pairChunks

// PairInChunks is pairInChunks.
func (s *FlowStore) PairInChunks(ctx context.Context, from, to, window, size int64) (Pairing, error) {
	return s.pairInChunks(ctx, from, to, window, size)
}
