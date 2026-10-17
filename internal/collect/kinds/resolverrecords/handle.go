package resolverrecords

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// ResolverRecordCoordinator is the part of the collector this package holds: the coordinators of the resolver_cache and resolver_local_data kinds, which read resource records alike.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type ResolverRecordCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *ResolverRecordCoordinator {
	return &ResolverRecordCoordinator{Collector: state}
}

func init() {
	core.RegisterPass(KindCache, func(c *core.Collector) func(context.Context) error { return On(c).CollectResolverCache })
}

func init() {
	core.RegisterPass(KindLocalData, func(c *core.Collector) func(context.Context) error { return On(c).CollectResolverLocalData })
}

// The two kinds this coordinator reads: the records a resolver holds in its cache -- "cache" is
// Unbound's own word (unbound-control(8), dump_cache) -- and those it serves from its local data, host
// overrides among them (unbound.conf(5), local-data; unbound-control(8), list_local_data).
const (
	KindCache     = "resolver_cache"
	KindLocalData = "resolver_local_data"
)
