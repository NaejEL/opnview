package neighbours

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// NeighbourCoordinator is the part of the collector this package holds: the coordinator of the
// neighbour kind. internal/collect composes it into its Collector, so its methods are the Collector's.
type NeighbourCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *NeighbourCoordinator { return &NeighbourCoordinator{Collector: state} }

func init() {
	core.RegisterPass(Kind, func(c *core.Collector) func(context.Context) error { return On(c).CollectNeighbour })
	// A pass places each neighbour on the interface discovery found, and leaves the neighbours of an
	// upstream interface out: it waits for the run's first discovery.
	core.RegisterDiscoveryReader(Kind)
}
