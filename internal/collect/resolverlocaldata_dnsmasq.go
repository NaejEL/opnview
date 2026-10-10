package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Dnsmasq as a resolver — one implementation of the resolver_local_data kind: registered,
// probed, and never read. OPNsense 26.7.3 exposes no read of the records Dnsmasq serves
// from its own configuration through the API (survey, "Resolver cache and local data, read
// from source for the resolver-cache cycle"), so the state is detected and named.

func init() { registerLocalDataSource(dnsmasqLocalData{}) }

// dnsmasqLocalData is the Dnsmasq resolver's local data: registered, probed, never read.
type dnsmasqLocalData struct{}

// noLocalDataRead is the named state of a resolver whose local data the API does not read.
const noLocalDataRead = "this resolver offers no local-data read through the API"

// providerKey is the registry row this implementation answers for.
func (dnsmasqLocalData) providerKey() string { return ProviderDnsmasq }

// probe reports whether the service runs, and never makes the implementation separable.
func (dnsmasqLocalData) probe(ctx context.Context, host session) (probeResult, error) {
	return dnsmasqNoReadProbe(ctx, host, noLocalDataRead)
}

// localData refuses, with the reason.
func (dnsmasqLocalData) localData(context.Context, session) (recordDump, probeResult, error) {
	return recordDump{}, probeResult{probe: opnsense.DnsmasqStatus, state: store.StateUnavailable},
		fmt.Errorf("%w: %s", ErrUnsupportedRead, noLocalDataRead)
}
