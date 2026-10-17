package lookups

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// LookupCoordinator is the part of the collector this package holds: the coordinator of the dns_lookup kind.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type LookupCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *LookupCoordinator { return &LookupCoordinator{Collector: state} }

func init() {
	core.RegisterPass(Kind, func(c *core.Collector) func(context.Context) error { return On(c).CollectDNSLookup })
}
