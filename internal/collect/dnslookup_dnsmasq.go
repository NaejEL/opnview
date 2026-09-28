package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Dnsmasq as a resolver — one implementation of the dns_lookup kind: registered, probed, and
// never read.
//
// The same product is also a DHCP backend, and that half of it is a separate implementation of
// a separate kind in dhcplease_dnsmasq.go. The two share a product name and nothing else — one
// of them can be read and the other cannot — which is exactly why the seam is the kind rather
// than the product.

func init() { registerLookupSource(dnsmasqLookups{}) }

// dnsmasqLookups is the Dnsmasq resolver: registered, probed, and never read.
//
// It offers no structured query API. The only path to its per-client lookups is parsing the
// free-text `line` of the generic log endpoint, and that grammar is dnsmasq's own rather than
// an OPNsense contract; the survey marks it UNVERIFIED and nobody has recorded it. Writing a
// parser against it would be guessing at a contract, so this implementation detects the state
// — including whether the user has turned query logging on — and reports it.
//
// opnview reads `dnsmasq.log_queries` and NEVER writes it. Turning that setting on is a manual
// operation the user performs in the firewall's own interface.
//
// Because it can never be separable, the two resolvers never present the ambiguity the DHCP
// kind can, and that is a consequence of the finding rather than a convenience.
type dnsmasqLookups struct{}

// providerKey is the registry row this implementation answers for.
func (dnsmasqLookups) providerKey() string { return ProviderDnsmasq }

// pageBound is one, and no page is ever read.
func (dnsmasqLookups) pageBound() int { return 1 }

// requestedSpanSeconds is zero: no window is ever requested.
func (dnsmasqLookups) requestedSpanSeconds() int64 { return 0 }

// probe reports the service state and whether query logging is on.
func (dnsmasqLookups) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.DnsmasqStatus}

	running, state, detail, err := host.serviceState(ctx, opnsense.DnsmasqStatus)
	if err != nil {
		return result, err
	}
	result.state, result.detail = state, detail
	if !running {
		return result, nil
	}

	settings, ok, err := host.readObject(ctx, opnsense.DnsmasqSettings)
	if err != nil {
		return result, err
	}
	logging := ok && decode.FlagOrFalse(decode.NestedFlag(settings, "dnsmasq", "log_queries"))

	result.state = store.StatePresentButDisabled
	if logging {
		result.detail = "query logging is on, but its log grammar is dnsmasq's own and is not " +
			"recorded, so opnview does not parse it"
	} else {
		result.detail = "query logging is off, and its log grammar is not recorded either, so " +
			"opnview does not parse it"
	}
	return result, nil
}

// lookups refuses, with the reason.
func (dnsmasqLookups) lookups(context.Context, session, int) (
	[]lookupRecord, probeResult, error) {
	return nil, probeResult{probe: opnsense.DnsmasqSettings, state: store.StatePresentButDisabled},
		fmt.Errorf("%w: this resolver exposes its lookups only as free text under a grammar "+
			"nobody has recorded", ErrUnsupportedRead)
}
