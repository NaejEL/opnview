package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The end-of-life ISC dhcpd plugin — one implementation of the dhcp_lease kind: registered,
// probed, and never read.
//
// /api/dhcpv4/leases/searchLease answered 404 on a live 26.7.3_11, and the survey marks the
// plugin's parameter and field contract UNVERIFIED because it lives in the plugin repository
// rather than in opnsense/core. Reading it would mean guessing at a contract nobody has, so
// this implementation reports why instead. A 404 from its presence probe says the plugin is
// not installed HERE, which is the unavailable state working rather than a fault.
//
// It is a whole file for a backend that returns nothing on purpose, and that is the point: the
// refusal and its reason are one implementation of the kind, not a special case inside another
// one.

func init() { registerLeaseSource(iscLeases{}) }

// iscLeases answers for the legacy plugin's registry row.
type iscLeases struct{}

// providerKey is the registry row this implementation answers for.
func (iscLeases) providerKey() string { return ProviderISC }

// probe checks only for presence.
func (iscLeases) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.ISCStatus, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.ISCStatus, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return result, err
	}
	result.detail = "the end-of-life plugin is not installed"
	if response.OK() {
		result.detail = "the plugin answers, but its lease endpoint answered 404 on the surveyed " +
			"firmware and its field contract is unverified, so opnview does not read it"
	}
	// Never separable: even a present plugin is not read, so it can never be the
	// implementation opnview reads for this kind.
	return result, nil
}

// leases refuses, with the reason.
func (iscLeases) leases(context.Context, session, int) ([]leaseObservation, probeResult, error) {
	return nil, probeResult{probe: opnsense.ISCStatus, state: store.StateUnavailable},
		fmt.Errorf("%w: the ISC lease endpoint answered 404 on the surveyed firmware and its "+
			"field contract is unverified", ErrUnsupportedRead)
}
