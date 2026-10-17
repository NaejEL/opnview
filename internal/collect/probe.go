package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// Availability probes and provider activation, written once for every kind.
//
// PROVIDER SELECTION READS THE FIREWALL'S OWN CONFIGURATION, never a preference order
// invented here. For each kind, every registered implementation is probed, and the ones the
// firewall's own configuration marks as serving clients are the candidates.
//
// What happens then depends on whether the kind admits several active providers, which is a
// property of the schema and not of this file (see internal/store/vocab/kinds.go). For an EXCLUSIVE
// kind, exactly one candidate becomes active, and if two qualify NEITHER is activated and the
// ambiguity is recorded on both rows: choosing would be opnview deciding which of two working
// sources is the truth, which is not opnview's decision to make. For a CONCURRENT kind there is
// nothing to decide — every row carries the provider that reported it — so every candidate is
// activated, and a firewall running Suricata beside CrowdSec is read as the two sources it is.
//
// Availability is reachability, and is a different question from activeness: a machine may
// have two reachable implementations of one kind. The firewall the survey probed has exactly
// that, twice over.

// ProbeAll runs one probe round over every kind this cycle reads.
//
// Three kinds are not probed, each for its own recorded reason. geo_asn: the dataset is acquired
// and its availability recorded by the maxmind plugin (plugins/maxmind), through an outbound call
// this package never makes. flow_volume: its
// destination is DERIVED from flow by step 5, so no implementation of it is registered.
// reconciled_state: the kind gives the surveyed state-shaped sources a destination, and writing
// a connector for one of them is not this cycle's work.
func (c *Collector) ProbeAll(ctx context.Context) error {
	var failures []error
	// Every implementation announces itself with a provider.json, and a refusal met while it did
	// (a malformed manifest, a kind mismatch, a duplicate key) is reported here, once per round,
	// rather than crashing the service: the implementations that did load are still probed.
	failures = append(failures, c.SourceSet.Failures()...)
	if err := c.EnsureProviders(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, probe := range []func(context.Context) error{
		c.ProbeFirewallLog,
		c.ProbeSecurityEvent,
		c.ProbeMeasurement,
		c.ProbeDHCPLease,
		c.ProbeDNSLookup,
		c.ProbeResolverCache,
		c.ProbeResolverLocalData,
		c.ProbeFlowRecord,
		c.ProbeReconciledState,
		c.ProbeDeclaredKinds,
	} {
		if err := probe(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the probe round was incomplete: %w", core.JoinErrors(failures))
	}
	return nil
}

// Reselect applies the operator's selection of one kind at once, from what the last probe
// round found for it, WITHOUT PROBING THE FIREWALL AGAIN. It is what the collection surface
// calls after a selection is saved, so a source turned on or off is read, or stops being
// read, from the next pass rather than from the next probe round.
//
// It writes no availability row: nothing was checked, and an availability row records a
// check. Before the first probe round of the process there is nothing to decide from, so it
// does nothing, and that round applies the selection as every round does. A selection that
// cannot be read is an error, and the active implementations are then left as they were.
// It is what web.Reconfigurable asks of a collector.
func (c *Collector) Reselect(ctx context.Context, kind string) error {
	c.Mutex.RLock()
	outcomes, probed := c.Probed[kind]
	c.Mutex.RUnlock()
	if !probed {
		return nil
	}
	selections, failures := c.LoadSelections(ctx, kind, outcomes)
	if len(failures) > 0 {
		return fmt.Errorf("collect: applying the %s selection: %w", kind, core.JoinErrors(failures))
	}
	active, _ := core.DecideActive(kind, outcomes, selections)
	return c.Store.SetActiveProviders(ctx, kind, active...)
}
