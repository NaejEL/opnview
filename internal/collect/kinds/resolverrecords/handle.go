package resolverrecords

import "github.com/NaejEL/opnview/internal/collect/core"

// ResolverRecordCoordinator is the part of the collector this package holds: the coordinators of the resolver_cache and resolver_local_data kinds, which read resource records alike.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type ResolverRecordCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *ResolverRecordCoordinator {
	return &ResolverRecordCoordinator{Collector: state}
}
