package services

import "github.com/NaejEL/opnview/internal/collect/core"

// ServiceCoordinator is the part of the collector this package holds: the service kinds, geolocation and the registrable-domain list, as their active providers last answered.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type ServiceCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *ServiceCoordinator { return &ServiceCoordinator{Collector: state} }
