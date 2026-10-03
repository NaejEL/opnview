package collect

import (
	"context"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Kea — one implementation of the dhcp_lease kind.
//
// Everything in this file is a fact about Kea and about the way OPNsense exposes it. What the
// KIND does with the observations — the identity cascade, resolving the interface, writing the
// generation — is in dhcplease.go and is written once.
//
// IT IS THE ONLY BACKEND THAT REPORTS A REAL VALIDITY START. `valid_lifetime` is published, so
// the start is `expire` minus it. What the other backends do instead, and why that is a finding
// rather than a detail, is recorded with the kind.

func init() { registerLeaseSource(keaLeases{}) }

// keaLeases reads /api/kea/leases4/search.
type keaLeases struct{}

// providerKey is the registry row this implementation answers for.
func (keaLeases) providerKey() string { return ProviderKea }

// probe reads the service state and then the configured state behind it.
//
// Both are needed and neither is enough: a running daemon with DHCPv4 switched off serves no
// client, and a configured backend that is not running serves none either. Only the
// combination marks this backend as the one the firewall's own configuration separates.
func (keaLeases) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.KeaStatus}

	running, state, detail, err := host.serviceState(ctx, opnsense.KeaStatus)
	if err != nil {
		return result, err
	}
	result.state, result.detail = state, detail
	if !running {
		return result, nil
	}

	settings, ok, err := host.readObject(ctx, opnsense.KeaDHCPv4)
	if err != nil {
		return result, err
	}
	configured := ok && decode.FlagOrFalse(decode.NestedFlag(settings, "dhcpv4", "general", "enabled"))
	if !configured {
		result.state = store.StatePresentButDisabled
		result.detail = "the service runs and dhcpv4.general.enabled is not set"
		return result, nil
	}
	result.separable = true
	return result, nil
}

// leases reads the lease table from the running daemon through its control agent.
func (keaLeases) leases(ctx context.Context, host session, pageSize int) (
	[]leaseObservation, probeResult, error) {
	rows, result, err := readLeaseRows(ctx, host, opnsense.KeaLeases, pageSize)
	if err != nil || rows == nil {
		if result.state == store.StateReachable && len(rows) == 0 {
			// The survey names the likely cause, so the detail names it too rather than
			// leaving the user to guess: the leases are read live from the daemon, and if its
			// control agent is off the list is empty although DHCP is working.
			result.detail = "the backend is running and reported no active lease, which is not " +
				"an absence of machines; the Kea control agent being off produces exactly this"
		}
		return nil, result, err
	}

	observations := make([]leaseObservation, 0, len(rows))
	for _, row := range rows {
		observation, ok := decodeLease(row)
		if !ok {
			continue
		}
		// The only backend that reports a real validity start. Where `valid_lifetime` is
		// absent the start stays nil rather than falling back to the expiry: the lease table
		// now carries a nullable start, so "this row does not know" is expressible.
		if observation.ExpiresAt != nil {
			if lifetime, present := decode.Int(row, "valid_lifetime"); present && lifetime > 0 {
				startsAt := *observation.ExpiresAt - lifetime
				if startsAt < 0 {
					startsAt = 0
				}
				observation.StartsAt = &startsAt
			}
		}
		observations = append(observations, observation)
	}
	return observations, result, nil
}
