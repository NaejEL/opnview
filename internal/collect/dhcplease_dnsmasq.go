package collect

import (
	"context"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Dnsmasq as a DHCP backend — one implementation of the dhcp_lease kind.
//
// Everything in this file is a fact about Dnsmasq's lease table. The same product is also a
// resolver, and that half of it is a separate implementation of a separate kind in
// dnslookup_dnsmasq.go: the two share a product name and nothing else.
//
// IT RETURNS STRUCTURED JSON, which is the verified correction to the survey's inferred text:
// that document's claim that Dnsmasq needs a free-text parser is wrong for leases. No parser is
// needed, and the rows carry the interface under all three of its names.

func init() { registerLeaseSource(dnsmasqLeases{}) }

// dnsmasqLeases reads /api/dnsmasq/leases/search.
type dnsmasqLeases struct{}

// providerKey is the registry row this implementation answers for.
func (dnsmasqLeases) providerKey() string { return ProviderDnsmasq }

// probe reads the service state and then whether the backend serves any range.
func (dnsmasqLeases) probe(ctx context.Context, host session) (probeResult, error) {
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
	ranges := false
	if ok {
		if root, present := decode.Nested(settings, "dnsmasq"); present {
			// Only the EMPTINESS of the ranges is read. The ranges themselves are the
			// firewall's addressing configuration, which opnview neither stores nor interprets.
			ranges = decode.NonEmpty(root["dhcp_ranges"])
		}
	}
	if !ranges {
		result.state = store.StatePresentButDisabled
		result.detail = "the service runs and dnsmasq.dhcp_ranges is empty, so it serves no lease"
		return result, nil
	}
	result.separable = true
	return result, nil
}

// leases reads the lease file through the API.
func (dnsmasqLeases) leases(ctx context.Context, host session) (
	[]leaseObservation, probeResult, error) {
	rows, result, err := readLeaseRows(ctx, host, opnsense.DnsmasqLeases)
	if err != nil || rows == nil {
		if result.state == store.StateReachable && len(rows) == 0 {
			result.detail = "the backend is running and reported no active lease, which is not " +
				"an absence of machines"
		}
		return nil, result, err
	}

	observations := make([]leaseObservation, 0, len(rows))
	for _, row := range rows {
		observation, ok := decodeLease(row)
		if !ok {
			continue
		}
		observations = append(observations, observation)
	}
	return observations, result, nil
}
