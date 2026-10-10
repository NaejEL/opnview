package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Dnsmasq as a resolver — one implementation of the resolver_cache kind: registered,
// probed, and never read.
//
// OPNsense 26.7.3 exposes no read of Dnsmasq's cache through the API: the Dnsmasq module's
// controllers are its settings, its service and its leases (survey, "Resolver cache and
// local data, read from source for the resolver-cache cycle"), and nothing dumps what the
// resolver holds. So this implementation detects whether the service runs and reports the
// named state, and the reason travels with the refusal.

func init() { registerCacheSource(dnsmasqCache{}) }

// dnsmasqCache is the Dnsmasq resolver's cache: registered, probed, and never read.
type dnsmasqCache struct{}

// noCacheRead is the named state of a resolver whose cache the API does not read.
const noCacheRead = "this resolver offers no cache read through the API"

// providerKey is the registry row this implementation answers for.
func (dnsmasqCache) providerKey() string { return ProviderDnsmasq }

// probe reports whether the service runs, and never makes the implementation separable:
// there is nothing to read.
func (dnsmasqCache) probe(ctx context.Context, host session) (probeResult, error) {
	return dnsmasqNoReadProbe(ctx, host, noCacheRead)
}

// cache refuses, with the reason.
func (dnsmasqCache) cache(context.Context, session) (recordDump, probeResult, error) {
	return recordDump{}, probeResult{probe: opnsense.DnsmasqStatus, state: store.StateUnavailable},
		fmt.Errorf("%w: %s", ErrUnsupportedRead, noCacheRead)
}

// dnsmasqNoReadProbe is the probe of a Dnsmasq read the API does not offer: the service
// state, and the named state when it runs.
//
// A running Dnsmasq is UNAVAILABLE for these kinds, not present-but-disabled: that state
// names a setting the user can turn on, and no setting makes the API read this material.
// The material is absent from the API, which is what unavailable says.
func dnsmasqNoReadProbe(ctx context.Context, host session, state string) (probeResult, error) {
	result := probeResult{probe: opnsense.DnsmasqStatus}
	running, availability, detail, err := host.serviceState(ctx, opnsense.DnsmasqStatus)
	if err != nil {
		return result, err
	}
	result.state, result.detail = availability, detail
	if running {
		result.state = store.StateUnavailable
		result.detail = "the service runs, and " + state
	}
	return result, nil
}
