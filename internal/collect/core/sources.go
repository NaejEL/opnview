package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Probeable is one implementation reduced to what activation needs: its registry key and its
// probe. It is what lets this file treat five kinds identically without any of them knowing
// about the others.
type Probeable struct {
	ProviderKey string
	Run         func(ctx context.Context, host provider.Session) (provider.Result, error)
}

// ProbeablesOf returns the implementations of one kind in the collector's provider set, each
// reduced to its key and its probe.
func (c *Collector) ProbeablesOf(kind string) []Probeable {
	registrations := c.SourceSet.Of(kind)
	probeables := make([]Probeable, 0, len(registrations))
	for _, registration := range registrations {
		probeables = append(probeables, Probeable{ProviderKey: registration.Key, Run: registration.Probe})
	}
	return SortedProbeables(probeables)
}

// ProbeKind probes every implementation of one kind and activates as ResolveKind decides.
func (c *Collector) ProbeKind(ctx context.Context, kind string) error {
	return c.ResolveKind(ctx, kind, c.ProbeablesOf(kind))
}

// SortedProbeables orders implementations by their registry key.
//
// It is ordering for REPRODUCIBILITY and not for preference: a Go map iterates in a random
// order, and a probe round that visited two implementations in a different order each time
// would produce a different sequence of requests for no reason. Nothing downstream reads the
// order to choose between them — that is the firewall's configuration's job, and a sorted
// list would be the wrong place to hide a preference.
func SortedProbeables(probeables []Probeable) []Probeable {
	sort.Slice(probeables, func(i, j int) bool {
		return probeables[i].ProviderKey < probeables[j].ProviderKey
	})
	return probeables
}

// ResolveKind probes each implementation of one kind, records what the firewall said about
// each, and activates the one the firewall's own configuration separates — or none, twice
// over: none when nothing qualifies, and none when more than one does.
func (c *Collector) ResolveKind(ctx context.Context, kind string, probeables []Probeable) error {
	outcomes := make([]ProbedSource, 0, len(probeables))
	var failures []error
	for _, candidate := range probeables {
		providerID, err := c.Store.ProviderID(ctx, kind, candidate.ProviderKey)
		if err != nil {
			// An implementation with no registry row cannot be recorded against anything. That
			// is a mismatch between the code and the schema, and it is reported rather than
			// ignored.
			failures = append(failures, err)
			continue
		}
		result, err := candidate.Run(ctx, c)
		if err != nil {
			failures = append(failures, err)
		}
		outcomes = append(outcomes, ProbedSource{
			providerKey: candidate.ProviderKey, providerID: providerID, result: result,
		})
	}

	// Kept for Reselect, which applies a selection saved before the next round from what
	// this one found.
	c.Mutex.Lock()
	c.Probed[kind] = append([]ProbedSource(nil), outcomes...)
	c.Mutex.Unlock()

	selections, selectionFailures := c.LoadSelections(ctx, kind, outcomes)
	failures = append(failures, selectionFailures...)
	active, ambiguity := DecideActive(c.SourceSet.Concurrent(kind), outcomes, selections)

	for _, entry := range outcomes {
		detail := entry.result.Detail
		if ambiguity != "" && entry.result.Separable {
			if detail == "" {
				detail = ambiguity
			} else {
				detail += "; " + ambiguity
			}
		}
		// What the last sampling pass of this provider could not read. The probe tests
		// whether the source answers and reads none of the readings, so writing its
		// detail alone would erase the record of every reading that did not answer --
		// which is how a gateway with no figure went unrecorded on a live firewall.
		if absent := c.sampleAbsence(entry.providerID); absent != "" {
			if detail == "" {
				detail = absent
			} else {
				detail += "; " + absent
			}
		}
		// And what the last read of a resolver's records said, for the same reason: the
		// counts, the size and the time of the dump, or the absence that stopped it.
		if read := c.readDetail(entry.providerID); read != "" {
			if detail == "" {
				detail = read
			} else {
				detail += "; " + read
			}
		}
		state, outcome := entry.result.State, entry.result.Outcome
		// A probe that found the source healthy restates what the last read or sampling
		// pass recorded, in its code as well as in its words, so the row's code never
		// contradicts its detail.
		if outcome == vocab.OutcomeHealthy {
			if read := c.readOutcome(entry.providerID); read != "" && read != vocab.OutcomeHealthy {
				outcome = read
			} else if c.sampleAbsence(entry.providerID) != "" {
				outcome = vocab.OutcomePartialReadings
			}
		}
		if state == "" {
			// The probe ended on an error before any answer was classified.
			state = vocab.StateUnavailable
			if outcome == "" {
				outcome = vocab.OutcomeRequestNotMade
			}
		}
		if err := c.WriteAvailability(ctx, entry.providerID, state, outcome,
			entry.result.Probe, detail); err != nil {
			return err
		}
	}

	if err := c.Store.SetActiveProviders(ctx, kind, active...); err != nil {
		return err
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: probing the %s kind was incomplete: %w",
			kind, JoinErrors(failures))
	}
	return nil
}

// LoadSelections reads the operator's selection of each implementation: off, on, or auto,
// which is what a missing row means. A selection that cannot be read is returned among the
// failures and given no value, so the decision treats that implementation as auto; the error
// names the setting key, so the operator can find the row.
func (c *Collector) LoadSelections(ctx context.Context, kind string,
	outcomes []ProbedSource) (map[string]config.Selection, []error) {
	selections := make(map[string]config.Selection, len(outcomes))
	var failures []error
	for _, entry := range outcomes {
		selection, err := config.LoadSourceSelection(ctx, c.Store, kind, entry.providerKey)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		selections[entry.providerKey] = selection
	}
	return selections, failures
}

// DecideActive is the activation decision for one kind: the implementations opnview reads,
// and the sentence recording an ambiguity, empty when there is none. It reads nothing, so a
// probe round and Reselect cannot decide differently from the same facts.
func DecideActive(concurrent bool, outcomes []ProbedSource,
	selections map[string]config.Selection) ([]string, string) {
	separable := make([]string, 0, len(outcomes))
	forced := make([]string, 0, len(outcomes))
	for _, entry := range outcomes {
		switch selections[entry.providerKey] {
		case config.SelectionOff:
			// Turned off: never activated, whatever the firewall says. Its availability is
			// still recorded, because selection is a decision and not a claim.
			continue
		case config.SelectionOn:
			forced = append(forced, entry.providerKey)
			continue
		}
		if entry.result.Separable {
			separable = append(separable, entry.providerKey)
		}
	}

	// Two qualifying implementations are an AMBIGUITY only where the kind is exclusive. Where
	// the kind admits several — a firewall running Suricata beside CrowdSec, two measurement
	// sources reporting different subjects — there is nothing to choose between: every row
	// carries the provider that reported it, so both are read and neither figure is doubled.
	//
	// AN IMPLEMENTATION TURNED ON IS THE OPERATOR SAYING WHICH ONE: it is the separation the
	// firewall's own configuration did not make, so it settles the ambiguity.
	ambiguity := ""
	if len(forced) > 0 {
		// A choice made by a person replaces the firewall's answer for the kind: the
		// implementations left on auto are not activated beside it.
		separable = separable[:0]
	} else if len(separable) > 1 && !concurrent {
		// Nobody chose, and the firewall's configuration does not separate the two: neither
		// is activated.
		ambiguity = "two implementations of this kind are both reachable and configured (" +
			joinWithComma(separable) + "), and the firewall's own configuration does not say " +
			"which serves clients, so opnview reads neither"
		separable = nil
	}
	// What opnview reads: every implementation turned on, then those the probe round
	// activates on its own. For an exclusive kind two turned on is refused by the store
	// with a sentence naming the kind, and reported like any other failure; it is never
	// settled here by picking one.
	return append(append([]string{}, forced...), separable...), ambiguity
}

// ActiveSource is one implementation opnview reads for a kind: its registry key and its
// registry id.
type ActiveSource struct {
	ProviderKey string
	ProviderID  int64
}

// ActiveSources returns every implementation opnview reads for one kind.
//
// For an exclusive kind the answer holds at most one. For a kind that admits several — the
// security_event stack, the measurement sources — it holds every one the probe round
// activated, and the caller runs a pass per source. NONE ACTIVE IS A NORMAL STATE: the probe
// round may have found nothing reachable, or, for an exclusive kind, two reachable
// implementations the firewall's own configuration does not separate, and in both cases nothing
// is collected for that kind rather than a guess being made.
func (c *Collector) ActiveSources(ctx context.Context, kind string) ([]ActiveSource, error) {
	ids, err := c.Store.ActiveProviderIDs(ctx, kind)
	if err != nil {
		return nil, err
	}
	sources := make([]ActiveSource, 0, len(ids))
	for _, id := range ids {
		key, err := c.Store.ProviderKey(ctx, id)
		if err != nil {
			return nil, err
		}
		sources = append(sources, ActiveSource{ProviderKey: key, ProviderID: id})
	}
	return sources, nil
}

// ActiveSourceKey returns the provider key of the implementation opnview reads for one
// EXCLUSIVE kind, its registry id, and whether there is one. A kind that admits several active
// providers has no single answer, and asking for one here is a programming error rather than a
// state to report, so it is refused.
func (c *Collector) ActiveSourceKey(ctx context.Context, kind string) (string, int64, bool, error) {
	if c.SourceSet.Concurrent(kind) {
		return "", 0, false, fmt.Errorf(
			"collect: the %s kind admits several active providers, so it has no single active "+
				"source; the pass has to walk activeSources", kind)
	}
	sources, err := c.ActiveSources(ctx, kind)
	if err != nil || len(sources) == 0 {
		return "", 0, false, err
	}
	return sources[0].ProviderKey, sources[0].ProviderID, true, nil
}

// joinWithComma joins names for a human-readable detail.
func joinWithComma(names []string) string {
	joined := ""
	for i, name := range names {
		if i > 0 {
			joined += ", "
		}
		joined += name
	}
	return joined
}

// EnsureProviders writes what the collector's declarations say of the registry: every kind it knows with
// its attribute, and the registry row of every implementation that declared itself and has none yet, with
// the availability row the schema gives every registry row (store.RegisterDeclarations).
//
// THIS IS WHAT MAKES ADDING AN IMPLEMENTATION A DIRECTORY AND NOTHING ELSE, A NEW KIND INCLUDED. The schema
// seeds no row: an implementation declares itself in its plugin's manifest and gets its row here, at the
// first probe round, so no schema file is edited. A row that exists is left exactly as it is.
func (c *Collector) EnsureProviders(ctx context.Context) error {
	kinds, providers := provider.SetDeclarations(c.SourceSet)
	if err := c.Store.RegisterProviderDeclarations(ctx, kinds, providers, c.Now()); err != nil {
		return err
	}
	// The values each implementation exports (PA6): the active ones are the provider_value dataset.
	values, implementations := provider.SetValues(c.SourceSet)
	return c.Store.RecordProviderValues(ctx, values, implementations, c.Now())
}
