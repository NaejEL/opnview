package collect

import (
	"context"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// Unbound — one implementation of the resolver_local_data kind, and the only one that can
// be read.
//
// Unbound/Api/DiagnosticsController.php, listlocaldataAction, runs the configd action
// `unbound listlocaldata`, which is scripts/unbound/wrapper.py -d over `unbound-control
// list_local_data` (opnsense/core 26.7.3; survey, "Resolver cache and local data, read from
// source for the resolver-cache cycle"). The script splits each line on whitespace and
// writes {name, ttl, type, rrtype, value} for every line of five fields or more, `value`
// being the record data's first field only -- the address of an A or AAAA record, the
// target of a PTR record -- and `ttl` the TTL the local data is configured with, not a
// time remaining. The envelope, its failed status and the ACL page are the cache dump's,
// and so is the reader, in resolvercache_unbound.go.
//
// OPNsense writes host overrides as `local-data:` lines (src/etc/inc/plugins.inc.d/
// unbound.inc), so this is where a host with no DHCP lease is named.

func init() { registerLocalDataSource(unboundLocalData{}) }

// unboundLocalData reads /api/unbound/diagnostics/listlocaldata.
type unboundLocalData struct{}

// providerKey is the registry row this implementation answers for.
func (unboundLocalData) providerKey() string { return ProviderUnbound }

// probe is the cache's: the service runs and is enabled.
func (unboundLocalData) probe(ctx context.Context, host session) (probeResult, error) {
	return unboundResolverProbe(ctx, host)
}

// localData reads the local data. The owner is named `name` here, not `host`.
func (unboundLocalData) localData(ctx context.Context, host session) (recordDump, probeResult, error) {
	return readUnboundRecords(ctx, host, opnsense.UnboundListLocalData, "name")
}
