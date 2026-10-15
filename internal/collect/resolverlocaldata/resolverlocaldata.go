package resolverlocaldata

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/recorddump"
)

// Kind is the registry kind this package answers for.
const Kind = "resolver_local_data"

// Source is one implementation of the resolver_local_data kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// LocalData returns every record of the local data now, or provider.ErrUnsupportedRead.
	LocalData(ctx context.Context, host provider.Session) (recorddump.Dump, provider.Result, error)
}
