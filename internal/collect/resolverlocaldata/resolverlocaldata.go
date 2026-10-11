// Package resolverlocaldata is the resolver_local_data kind: a source of the records a resolver
// serves from its own local data, host overrides among them (unbound.conf(5), local-data;
// unbound-control(8), list_local_data).
//
// WHAT THE KIND PRODUCES. One recorddump.Dump per read. What is true of any resolver's local
// data is written once in internal/collect (resolverlocaldata.go and resourcerecord.go): the A,
// AAAA and PTR records are stored as observations that stay current while consecutive polls see
// them, and a host name the query report logged as a lookup's client, which no lease names, is
// resolved through the local data held at the lookup's instant. The kind admits several active
// providers for the same reason resolver_cache does.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/resolverlocaldata/<product>/ holding a
// provider.json whose kind is "resolver_local_data", a Go package whose init() calls Register
// with the embedded manifest and a Source, its fixtures and its tests. A resolver with no
// local-data read (Dnsmasq) is registered and probed and returns provider.ErrUnsupportedRead with
// the reason.
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
