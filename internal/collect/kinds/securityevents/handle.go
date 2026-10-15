package securityevents

import "github.com/NaejEL/opnview/internal/collect/core"

// SecurityEventCoordinator is the part of the collector this package holds: the coordinator of the security_event kind.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type SecurityEventCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *SecurityEventCoordinator {
	return &SecurityEventCoordinator{Collector: state}
}
