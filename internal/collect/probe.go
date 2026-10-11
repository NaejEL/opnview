package collect

import (
	"context"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/measurement"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/resolvercache"
	"github.com/NaejEL/opnview/internal/collect/resolverlocaldata"
	"github.com/NaejEL/opnview/internal/collect/securityevent"
	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// Availability probes and provider activation, written once for every kind.
//
// PROVIDER SELECTION READS THE FIREWALL'S OWN CONFIGURATION, never a preference order
// invented here. For each kind, every registered implementation is probed, and the ones the
// firewall's own configuration marks as serving clients are the candidates.
//
// What happens then depends on whether the kind admits several active providers, which is a
// property of the schema and not of this file (see internal/store/kinds.go). For an EXCLUSIVE
// kind, exactly one candidate becomes active, and if two qualify NEITHER is activated and the
// ambiguity is recorded on both rows: choosing would be opnview deciding which of two working
// sources is the truth, which is not opnview's decision to make. For a CONCURRENT kind there is
// nothing to decide — every row carries the provider that reported it — so every candidate is
// activated, and a firewall running Suricata beside CrowdSec is read as the two sources it is.
//
// Availability is reachability, and is a different question from activeness: a machine may
// have two reachable implementations of one kind. The firewall the survey probed has exactly
// that, twice over.

// probeable is one implementation reduced to what activation needs: its registry key and its
// probe. It is what lets this file treat five kinds identically without any of them knowing
// about the others.
type probeable struct {
	providerKey string
	run         func(ctx context.Context, host provider.Session) (provider.Result, error)
}

// probeablesOf returns the implementations of one kind in the collector's source set, each
// reduced to its key and its probe.
func probeablesOf[S any](registry *sources.Registry[S], probe func(S) func(context.Context,
	provider.Session) (provider.Result, error)) []probeable {
	implementations := registry.Sources()
	probeables := make([]probeable, 0, len(implementations))
	for key, source := range implementations {
		probeables = append(probeables, probeable{providerKey: key, run: probe(source)})
	}
	return sortedProbeables(probeables)
}

// sortedProbeables orders implementations by their registry key.
//
// It is ordering for REPRODUCIBILITY and not for preference: a Go map iterates in a random
// order, and a probe round that visited two implementations in a different order each time
// would produce a different sequence of requests for no reason. Nothing downstream reads the
// order to choose between them — that is the firewall's configuration's job, and a sorted
// list would be the wrong place to hide a preference.
func sortedProbeables(probeables []probeable) []probeable {
	sort.Slice(probeables, func(i, j int) bool {
		return probeables[i].providerKey < probeables[j].providerKey
	})
	return probeables
}

// ProbeAll runs one probe round over every kind this cycle reads.
//
// Three kinds are not probed, each for its own recorded reason. geo_asn: the dataset is acquired
// and its availability recorded by internal/maxmind, the second outbound call, which this
// package never makes. flow_volume: its
// destination is DERIVED from flow by step 5, so no implementation of it is registered.
// reconciled_state: the kind gives the surveyed state-shaped sources a destination, and writing
// a connector for one of them is not this cycle's work.
func (c *Collector) ProbeAll(ctx context.Context) error {
	var failures []error
	// Every implementation announces itself with a provider.json, and a refusal met while it did
	// (a malformed manifest, a kind mismatch, a duplicate key) is reported here, once per round,
	// rather than crashing the service: the implementations that did load are still probed.
	failures = append(failures, c.sources.Failures()...)
	if err := c.ensureProviders(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, probe := range []func(context.Context) error{
		c.probeFirewallLog,
		c.probeSecurityEvent,
		c.probeMeasurement,
		c.probeDHCPLease,
		c.probeDNSLookup,
		c.probeResolverCache,
		c.probeResolverLocalData,
		c.probeFlowRecord,
		c.probeReconciledState,
		c.probeDeclaredKinds,
	} {
		if err := probe(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the probe round was incomplete: %w", joinErrors(failures))
	}
	return nil
}

// probeFirewallLog probes every implementation of the firewall_log kind.
func (c *Collector) probeFirewallLog(ctx context.Context) error {
	return c.resolveKind(ctx, KindFirewallLog, probeablesOf(c.sources.FirewallLog, firewallLogProbe))
}

// probeSecurityEvent probes every implementation of the security_event kind.
func (c *Collector) probeSecurityEvent(ctx context.Context) error {
	return c.resolveKind(ctx, KindSecurityEvent, probeablesOf(c.sources.SecurityEvent, securityEventProbe))
}

// probeMeasurement probes every implementation of the measurement_sample kind.
func (c *Collector) probeMeasurement(ctx context.Context) error {
	return c.resolveKind(ctx, KindMeasurementSample, probeablesOf(c.sources.Measurement, measurementProbe))
}

// probeDHCPLease probes every implementation of the dhcp_lease kind.
func (c *Collector) probeDHCPLease(ctx context.Context) error {
	return c.resolveKind(ctx, KindDHCPLease, probeablesOf(c.sources.DHCPLease, leaseProbe))
}

// probeDNSLookup probes every implementation of the dns_lookup kind.
func (c *Collector) probeDNSLookup(ctx context.Context) error {
	return c.resolveKind(ctx, KindDNSLookup, probeablesOf(c.sources.DNSLookup, lookupProbe))
}

// probeResolverCache probes every implementation of the resolver_cache kind.
func (c *Collector) probeResolverCache(ctx context.Context) error {
	return c.resolveKind(ctx, KindResolverCache, probeablesOf(c.sources.ResolverCache, cacheProbe))
}

// probeResolverLocalData probes every implementation of the resolver_local_data kind.
func (c *Collector) probeResolverLocalData(ctx context.Context) error {
	return c.resolveKind(ctx, KindResolverLocalData, probeablesOf(c.sources.ResolverLocalData, localDataProbe))
}

// probedSource is one implementation as a probe round found it: its registry row and what
// its probe answered.
type probedSource struct {
	providerKey string
	providerID  int64
	result      provider.Result
}

// resolveKind probes each implementation of one kind, records what the firewall said about
// each, and activates the one the firewall's own configuration separates — or none, twice
// over: none when nothing qualifies, and none when more than one does.
func (c *Collector) resolveKind(ctx context.Context, kind string, probeables []probeable) error {
	outcomes := make([]probedSource, 0, len(probeables))
	var failures []error
	for _, candidate := range probeables {
		providerID, err := c.store.ProviderID(ctx, kind, candidate.providerKey)
		if err != nil {
			// An implementation with no registry row cannot be recorded against anything. That
			// is a mismatch between the code and the schema, and it is reported rather than
			// ignored.
			failures = append(failures, err)
			continue
		}
		result, err := candidate.run(ctx, c)
		if err != nil {
			failures = append(failures, err)
		}
		outcomes = append(outcomes, probedSource{
			providerKey: candidate.providerKey, providerID: providerID, result: result,
		})
	}

	// Kept for Reselect, which applies a selection saved before the next round from what
	// this one found.
	c.mutex.Lock()
	c.probed[kind] = append([]probedSource(nil), outcomes...)
	c.mutex.Unlock()

	selections, selectionFailures := c.loadSelections(ctx, kind, outcomes)
	failures = append(failures, selectionFailures...)
	active, ambiguity := decideActive(kind, outcomes, selections)

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
		if outcome == store.OutcomeHealthy {
			if read := c.readOutcome(entry.providerID); read != "" && read != store.OutcomeHealthy {
				outcome = read
			} else if c.sampleAbsence(entry.providerID) != "" {
				outcome = store.OutcomePartialReadings
			}
		}
		if state == "" {
			// The probe ended on an error before any answer was classified.
			state = store.StateUnavailable
			if outcome == "" {
				outcome = store.OutcomeRequestNotMade
			}
		}
		if err := c.writeAvailability(ctx, entry.providerID, state, outcome,
			entry.result.Probe, detail); err != nil {
			return err
		}
	}

	if err := c.store.SetActiveProviders(ctx, kind, active...); err != nil {
		return err
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: probing the %s kind was incomplete: %w",
			kind, joinErrors(failures))
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
	c.mutex.RLock()
	outcomes, probed := c.probed[kind]
	c.mutex.RUnlock()
	if !probed {
		return nil
	}
	selections, failures := c.loadSelections(ctx, kind, outcomes)
	if len(failures) > 0 {
		return fmt.Errorf("collect: applying the %s selection: %w", kind, joinErrors(failures))
	}
	active, _ := decideActive(kind, outcomes, selections)
	return c.store.SetActiveProviders(ctx, kind, active...)
}

// loadSelections reads the operator's selection of each implementation: off, on, or auto,
// which is what a missing row means. A selection that cannot be read is returned among the
// failures and given no value, so the decision treats that implementation as auto; the error
// names the setting key, so the operator can find the row.
func (c *Collector) loadSelections(ctx context.Context, kind string,
	outcomes []probedSource) (map[string]config.Selection, []error) {
	selections := make(map[string]config.Selection, len(outcomes))
	var failures []error
	for _, entry := range outcomes {
		selection, err := config.LoadSourceSelection(ctx, c.store, kind, entry.providerKey)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		selections[entry.providerKey] = selection
	}
	return selections, failures
}

// decideActive is the activation decision for one kind: the implementations opnview reads,
// and the sentence recording an ambiguity, empty when there is none. It reads nothing, so a
// probe round and Reselect cannot decide differently from the same facts.
func decideActive(kind string, outcomes []probedSource,
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
	concurrent := store.KindAdmitsSeveralActiveProviders(kind)
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

// activeSource is one implementation opnview reads for a kind: its registry key and its
// registry id.
type activeSource struct {
	providerKey string
	providerID  int64
}

// activeSources returns every implementation opnview reads for one kind.
//
// For an exclusive kind the answer holds at most one. For a kind that admits several — the
// security_event stack, the measurement sources — it holds every one the probe round
// activated, and the caller runs a pass per source. NONE ACTIVE IS A NORMAL STATE: the probe
// round may have found nothing reachable, or, for an exclusive kind, two reachable
// implementations the firewall's own configuration does not separate, and in both cases nothing
// is collected for that kind rather than a guess being made.
func (c *Collector) activeSources(ctx context.Context, kind string) ([]activeSource, error) {
	ids, err := c.store.ActiveProviderIDs(ctx, kind)
	if err != nil {
		return nil, err
	}
	sources := make([]activeSource, 0, len(ids))
	for _, id := range ids {
		key, err := c.store.ProviderKey(ctx, id)
		if err != nil {
			return nil, err
		}
		sources = append(sources, activeSource{providerKey: key, providerID: id})
	}
	return sources, nil
}

// activeSourceKey returns the provider key of the implementation opnview reads for one
// EXCLUSIVE kind, its registry id, and whether there is one. A kind that admits several active
// providers has no single answer, and asking for one here is a programming error rather than a
// state to report, so it is refused.
func (c *Collector) activeSourceKey(ctx context.Context, kind string) (string, int64, bool, error) {
	if store.KindAdmitsSeveralActiveProviders(kind) {
		return "", 0, false, fmt.Errorf(
			"collect: the %s kind admits several active providers, so it has no single active "+
				"source; the pass has to walk activeSources", kind)
	}
	sources, err := c.activeSources(ctx, kind)
	if err != nil || len(sources) == 0 {
		return "", 0, false, err
	}
	return sources[0].providerKey, sources[0].providerID, true, nil
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

// ensureProviders creates the registry row of every implementation that declared itself and has
// none yet, with the availability row the schema gives every registry row.
//
// THIS IS WHAT MAKES ADDING AN IMPLEMENTATION A DIRECTORY AND NOTHING ELSE. The schema seeds the
// rows of the implementations that shipped first, and that seed is historical: an implementation
// added later declares itself in its provider.json and gets its row here, at the first probe
// round, so no schema file is edited. A row that exists is left exactly as it is.
func (c *Collector) ensureProviders(ctx context.Context) error {
	var failures []error
	for _, manifest := range c.sources.Declared() {
		if _, _, err := c.store.EnsureProvider(ctx, manifest.Kind, manifest.Key, manifest.DisplayName,
			c.now()); err != nil {
			failures = append(failures, err)
		}
	}
	return joinErrors(failures)
}

// The probe of each core kind's implementations, as probeablesOf takes it.
func firewallLogProbe(source firewalllog.Source) func(context.Context, provider.Session) (provider.Result, error) {
	return source.Probe
}

func securityEventProbe(source securityevent.Source) func(context.Context, provider.Session) (
	provider.Result, error) {
	return source.Probe
}

func measurementProbe(source measurement.Source) func(context.Context, provider.Session) (provider.Result, error) {
	return source.Probe
}

func leaseProbe(source dhcplease.Source) func(context.Context, provider.Session) (provider.Result, error) {
	return source.Probe
}

func lookupProbe(source dnslookup.Source) func(context.Context, provider.Session) (provider.Result, error) {
	return source.Probe
}

func cacheProbe(source resolvercache.Source) func(context.Context, provider.Session) (provider.Result, error) {
	return source.Probe
}

func localDataProbe(source resolverlocaldata.Source) func(context.Context, provider.Session) (
	provider.Result, error) {
	return source.Probe
}
