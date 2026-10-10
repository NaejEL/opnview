package collect

import (
	"context"

	"github.com/NaejEL/opnview/internal/store"
)

// The resolver_cache kind.
//
// What is true of any resolver's cache, and is therefore here or in resourcerecord.go: the
// records are read once per pass, the A, AAAA and CNAME records are stored as observations
// whose coverage interval a later poll extends, every other type is counted, and the flows
// whose attribution the new records can change are decided again. Which resolver, which
// endpoint and which envelope are in resolvercache_unbound.go and resolvercache_dnsmasq.go.
//
// THE CACHE READ AND THE QUERY REPORT ARE TWO KINDS WITH TWO AVAILABILITY ROWS, and neither
// ever writes the other's (scope B1 of specs/SPEC-resolver-cache-attribution.md). A
// resolver whose cache cannot be read still reports its lookups, and the reverse, and a
// screen has to be able to say which of the two is missing.

// CollectResolverCache runs one pass of every active implementation of the resolver_cache
// kind.
func (c *Collector) CollectResolverCache(ctx context.Context) error {
	// No retention purge runs between this pass storing its records and the end of its
	// derivation (purge.go).
	defer c.storing()()
	_, request, passErr := c.readResourceRecords(ctx, KindResolverCache, store.HeldInCache,
		func(providerKey string) (recordReader, bool) {
			source, registered := cacheSources[providerKey]
			if !registered {
				return nil, false
			}
			return source.cache, true
		})
	return c.deriveRecordPass(ctx, request, passErr)
}
