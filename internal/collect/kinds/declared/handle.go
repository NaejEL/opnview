package declared

import "github.com/NaejEL/opnview/internal/collect/core"

// DeclaredKindCoordinator is the part of the collector this package holds: the coordinator of the kinds plugins declare, and of the reconciled state.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type DeclaredKindCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *DeclaredKindCoordinator {
	return &DeclaredKindCoordinator{Collector: state}
}
