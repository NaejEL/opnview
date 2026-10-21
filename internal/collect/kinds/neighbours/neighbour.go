package neighbours

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The neighbour kind (PA12 of the plugin-API amendment, specs/SPEC-backend-completion-plugins.md).
//
// What is true of any neighbour table, and is therefore here: a neighbour carrying a hardware
// address is a machine at the MAC level of the identity cascade; an entry at an address this
// firewall holds is this firewall, which is never a client; and a neighbour of an upstream
// interface -- the provider's router, typically -- is outside, and never becomes a client row. Which
// tables, at which paths, with which fields, is the implementation's: plugins/neighbours.
//
// They matter because on a firewall whose DHCP is unreadable -- Kea with its control agent off, a
// Dnsmasq with no ranges -- they are the only client identity there is, and without them every
// machine on such a network would fall to the weakest level of the cascade. A failure is recorded in
// the provider's availability with the outcome the implementation reports, and stops no other pass.

// Kind is the kind this coordinator collects.
const Kind = "neighbour"

// CollectNeighbour runs one pass of the active implementation: every neighbour it reports, written
// in one statement, then the addresses read placed again, which re-points the rows naming them.
func (c *NeighbourCoordinator) CollectNeighbour(ctx context.Context) error {
	// No retention purge runs between this pass storing its machines and the end of its derivation
	// (purge.go).
	defer c.Storing()()
	sources, err := c.ActiveSources(ctx, Kind)
	if err != nil {
		return err
	}
	var (
		failures []error
		seen     []string
	)
	for _, active := range sources {
		addresses, err := c.collectFrom(ctx, active)
		if err != nil {
			failures = append(failures, err)
		}
		seen = append(seen, addresses...)
	}
	if err := derive.On(c.Collector).Derive(ctx, derive.Derivation{}); err != nil {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the neighbour pass was incomplete: %w", core.JoinErrors(failures))
	}
	return nil
}

// collectFrom reads one implementation, stores its neighbours and records its availability, and
// returns the addresses it read.
func (c *NeighbourCoordinator) collectFrom(ctx context.Context, active core.ActiveSource) ([]string, error) {
	source, registered := c.SourceSet.Lookup(Kind, active.ProviderKey)
	if !registered {
		return nil, fmt.Errorf("collect: the active %s provider %q has no implementation", Kind, active.ProviderKey)
	}
	answer, result, readErr := source.Implementation.Call(ctx, c, "neighbours")
	if readErr != nil {
		// The firewall answered something the implementation could not read, or nothing: the
		// outcome it reports is recorded, and nothing is inferred from any other data.
		if writeErr := c.WriteAvailability(ctx, active.ProviderID, result.State, result.Outcome, result.Probe,
			result.Detail); writeErr != nil {
			return nil, core.JoinErrors([]error{readErr, writeErr})
		}
		return nil, readErr
	}
	addresses, err := c.storeNeighbours(ctx, provider.Records(answer, "neighbours"))
	if err != nil {
		// Unavailable, because nothing of the answer reached the store a widget reads; the outcome
		// says the fault is opnview's, not the firewall's.
		detail := fmt.Sprintf("the tables answered and their neighbours could not be stored: %v", err)
		if writeErr := c.WriteAvailability(ctx, active.ProviderID, vocab.StateUnavailable, vocab.OutcomeStoreFailure,
			result.Probe, detail); writeErr != nil {
			return nil, core.JoinErrors([]error{err, writeErr})
		}
		return nil, err
	}
	return addresses, c.WriteAvailability(ctx, active.ProviderID, result.State, result.Outcome, result.Probe,
		result.Detail)
}

// storeNeighbours writes the machines the neighbours identify, in one statement whatever their number
// (PA17), and returns every address read.
func (c *NeighbourCoordinator) storeNeighbours(ctx context.Context, observations []provider.Answer) ([]string, error) {
	snapshot := c.Discovery()
	now := c.Now()
	addresses := make([]string, 0, len(observations))
	var clients []vocab.Client
	for _, observation := range observations {
		address := provider.Text(observation, "address")
		addresses = append(addresses, address)
		client, keep, err := c.neighbourClient(ctx, observation, address, snapshot, now)
		if err != nil {
			return addresses, err
		}
		if keep {
			clients = append(clients, client)
		}
	}
	if _, err := c.Store.UpsertClients(ctx, clients, now); err != nil {
		return addresses, err
	}
	return addresses, nil
}

// neighbourClient is the machine one neighbour identifies, and whether it identifies one.
func (c *NeighbourCoordinator) neighbourClient(ctx context.Context, observation provider.Answer, address string,
	snapshot core.Discovery, now int64) (vocab.Client, bool, error) {
	mac := decode.MACPointer(provider.Text(observation, "mac"))
	if mac == nil || address == "" {
		// Without both there is nothing here the cascade can use: a MAC with no address names a
		// machine opnview cannot match to a flow, and an address with no MAC is what the
		// filter-log collector already handles.
		return vocab.Client{}, false, nil
	}
	var interfaceID *int64
	if id, found := snapshot.InterfaceIDByDevice[provider.Text(observation, "interface_device")]; found {
		interfaceID = &id
	}
	if interfaceID != nil {
		if _, upstream := snapshot.Upstream[*interfaceID]; upstream {
			// A neighbour of an upstream interface is outside, and an outside address never becomes a
			// client row.
			return vocab.Client{}, false, nil
		}
	}
	// The tables list this firewall's own interface entries as well as its neighbours. An entry at an
	// address the firewall holds at this instant is this firewall, which is never a client (step-5A
	// live corrections, item 1.2; defect L2 of specs/SPEC-test-budget-and-two-live-defects.md), so it
	// creates and updates nothing.
	if firewall, err := c.Store.IsThisFirewallAt(ctx, address, now); err != nil || firewall {
		return vocab.Client{}, false, err
	}
	return vocab.Client{
		Identity:    vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: *mac},
		InterfaceID: interfaceID,
		MAC:         mac,
		// The vendor string is context and is never a basis for classifying a machine.
		VendorHint:  provider.TextPointer(observation, "vendor_hint"),
		LastAddress: &address,
	}, true, nil
}
