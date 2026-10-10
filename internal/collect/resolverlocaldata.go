package collect

import (
	"context"

	"github.com/NaejEL/opnview/internal/store"
)

// The resolver_local_data kind.
//
// What is true of any resolver's local data: the records are read once per pass, the A,
// AAAA and PTR records are stored as observations that stay current while consecutive
// polls see them, and a host name the query report logged as a lookup's client, which no
// lease names, is resolved through the local data held at the lookup's instant (scope D of
// specs/SPEC-resolver-cache-attribution.md). A lookup answered from Local-data also takes
// the local data's A and AAAA records as exact evidence of its answer (decision 6). Which
// resolver and which endpoint are in resolverlocaldata_unbound.go and
// resolverlocaldata_dnsmasq.go.

// CollectResolverLocalData runs one pass of every active implementation of the
// resolver_local_data kind.
func (c *Collector) CollectResolverLocalData(ctx context.Context) error {
	// No retention purge runs between this pass storing its records and the end of its
	// derivation (purge.go).
	defer c.storing()()
	pass, request, passErr := c.readResourceRecords(ctx, KindResolverLocalData, store.HeldInLocalData,
		func(providerKey string) (recordReader, bool) {
			source, registered := localDataSources[providerKey]
			if !registered {
				return nil, false
			}
			return source.localData, true
		})

	// The local data names hosts no lease names, so the lookups whose logged host name is
	// still unresolved are examined again -- once every source of host names in use has
	// been read after they were ingested. A provider's first poll ever covers no instant
	// before its own, so it does not count as a read of the local data a past lookup could
	// be resolved against: the examination waits for the poll after it.
	switch {
	case pass.read && pass.followsAPoll:
		before, examine, err := c.noteLocalDataRead(ctx, pass.readAt)
		if err != nil {
			passErr = joinErrors(append(nonNil(passErr), err))
		} else if examine {
			request.hostnames, request.hostnamesBefore = true, before
		}
	case pass.failed:
		c.noteLocalDataFailed()
	}
	return c.deriveRecordPass(ctx, request, passErr)
}

// nonNil returns an error as a list, empty when it is nil.
func nonNil(err error) []error {
	if err == nil {
		return nil
	}
	return []error{err}
}
