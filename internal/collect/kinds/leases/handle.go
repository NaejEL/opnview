package leases

import "github.com/NaejEL/opnview/internal/collect/core"

// LeaseCoordinator is the part of the collector this package holds: the coordinator of the dhcp_lease kind, and of the neighbour table read beside it.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type LeaseCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *LeaseCoordinator { return &LeaseCoordinator{Collector: state} }
