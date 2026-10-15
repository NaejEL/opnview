package resolvercache

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/recorddump"
)

// Kind is the registry kind this package answers for.
const Kind = "resolver_cache"

// Source is one implementation of the resolver_cache kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Cache returns every record the cache holds now, or provider.ErrUnsupportedRead.
	Cache(ctx context.Context, host provider.Session) (recorddump.Dump, provider.Result, error)
}
