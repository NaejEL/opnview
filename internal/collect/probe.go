package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Availability probes and provider activation, written once for every kind.
//
// PROVIDER SELECTION READS THE FIREWALL'S OWN CONFIGURATION, never a preference order
// invented here. For each kind, every registered implementation is probed, and the ones the
// firewall's own configuration marks as serving clients are the candidates.
//
// What happens then depends on whether the kind admits several active providers, which is a
// property of the kind's declaration and not of this file (its kind file, internal/collect/provider/kinds). For an EXCLUSIVE
// kind, exactly one candidate becomes active, and if two qualify NEITHER is activated and the
// ambiguity is recorded on both rows: choosing would be opnview deciding which of two working
// sources is the truth, which is not opnview's decision to make. For a CONCURRENT kind there is
// nothing to decide — every row carries the provider that reported it — so every candidate is
// activated, and a firewall running Suricata beside CrowdSec is read as the two sources it is.
//
// Availability is reachability, and is a different question from activeness: a machine may
// have two reachable implementations of one kind. The firewall the survey probed has exactly
// that, twice over.

// ProbeAll runs one probe round over every kind a probe reads. A service kind's provider records
// its own state at each refresh (internal/collect/services), and a core-only kind -- flow_volume, DERIVED
// from flow -- has no implementation to probe.
func (c *Collector) ProbeAll(ctx context.Context) error {
	var failures []error
	// Every implementation announces itself with a provider.json, and a refusal met while it did
	// (a malformed manifest, a kind mismatch, a duplicate key) is reported here, once per round,
	// rather than crashing the service: the implementations that did load are still probed.
	failures = append(failures, c.SourceSet.Failures()...)
	if err := c.EnsureProviders(ctx); err != nil {
		failures = append(failures, err)
	}
	// Every kind whose implementations a probe round reads: each core kind a plugin may implement with a
	// probe in its contract, and each kind a plugin declared -- never a service kind, whose provider
	// reports its own state at its refresh, nor a core-only kind, which nothing implements.
	for _, kind := range c.SourceSet.Kinds() {
		if kindContract, isCore := provider.ContractOf(kind); isCore {
			if _, probes := kindContract.FunctionOf("probe"); !probes {
				continue
			}
		} else if _, declared := c.SourceSet.DeclaredKindOwner(kind); !declared {
			continue
		}
		if err := c.ProbeKind(ctx, kind); err != nil {
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
	active, _ := core.DecideActive(c.SourceSet.Concurrent(kind), outcomes, selections)
	return c.Store.SetActiveProviders(ctx, kind, active...)
}
