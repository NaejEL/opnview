package filterlog

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// FilterLogCoordinator is the part of the collector this package holds: the coordinator of the firewall_log kind, pulled and pushed.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type FilterLogCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *FilterLogCoordinator { return &FilterLogCoordinator{Collector: state} }

func init() {
	core.RegisterPass(Kind, func(c *core.Collector) func(context.Context) error { return On(c).CollectFirewallLog })
}
