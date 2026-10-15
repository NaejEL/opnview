package discovery

import "github.com/NaejEL/opnview/internal/collect/core"

// Discoverer is the part of the collector this package holds: runtime discovery of the firewall's interfaces, rules and clock.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type Discoverer struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *Discoverer { return &Discoverer{Collector: state} }
