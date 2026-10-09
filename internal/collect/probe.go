package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
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
	run         func(ctx context.Context, host session) (probeResult, error)
}

// firewallLogProbeables returns the registered implementations of the firewall_log kind.
func firewallLogProbeables() []probeable {
	probeables := make([]probeable, 0, len(firewallLogSources))
	for key, source := range firewallLogSources {
		probeables = append(probeables, probeable{providerKey: key, run: source.probe})
	}
	return sortedProbeables(probeables)
}

// securityEventProbeables returns the registered implementations of the security_event kind.
func securityEventProbeables() []probeable {
	probeables := make([]probeable, 0, len(securityEventSources))
	for key, source := range securityEventSources {
		probeables = append(probeables, probeable{providerKey: key, run: source.probe})
	}
	return sortedProbeables(probeables)
}

// measurementProbeables returns the registered implementations of the measurement_sample kind.
func measurementProbeables() []probeable {
	probeables := make([]probeable, 0, len(measurementSources))
	for key, source := range measurementSources {
		probeables = append(probeables, probeable{providerKey: key, run: source.probe})
	}
	return sortedProbeables(probeables)
}

// leaseProbeables returns the registered implementations of the dhcp_lease kind.
func leaseProbeables() []probeable {
	probeables := make([]probeable, 0, len(leaseSources))
	for key, source := range leaseSources {
		probeables = append(probeables, probeable{providerKey: key, run: source.probe})
	}
	return sortedProbeables(probeables)
}

// lookupProbeables returns the registered implementations of the dns_lookup kind.
func lookupProbeables() []probeable {
	probeables := make([]probeable, 0, len(lookupSources))
	for key, source := range lookupSources {
		probeables = append(probeables, probeable{providerKey: key, run: source.probe})
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
	for _, probe := range []func(context.Context) error{
		c.probeFirewallLog,
		c.probeSecurityEvent,
		c.probeMeasurement,
		c.probeDHCPLease,
		c.probeDNSLookup,
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
	return c.resolveKind(ctx, KindFirewallLog, firewallLogProbeables())
}

// probeSecurityEvent probes every implementation of the security_event kind.
func (c *Collector) probeSecurityEvent(ctx context.Context) error {
	return c.resolveKind(ctx, KindSecurityEvent, securityEventProbeables())
}

// probeMeasurement probes every implementation of the measurement_sample kind.
func (c *Collector) probeMeasurement(ctx context.Context) error {
	return c.resolveKind(ctx, KindMeasurementSample, measurementProbeables())
}

// probeDHCPLease probes every implementation of the dhcp_lease kind.
func (c *Collector) probeDHCPLease(ctx context.Context) error {
	return c.resolveKind(ctx, KindDHCPLease, leaseProbeables())
}

// probeDNSLookup probes every implementation of the dns_lookup kind.
func (c *Collector) probeDNSLookup(ctx context.Context) error {
	return c.resolveKind(ctx, KindDNSLookup, lookupProbeables())
}

// probedSource is one implementation as a probe round found it: its registry row and what
// its probe answered.
type probedSource struct {
	providerKey string
	providerID  int64
	result      probeResult
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
		detail := entry.result.detail
		if ambiguity != "" && entry.result.separable {
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
		state := entry.result.state
		if state == "" {
			state = store.StateUnavailable
		}
		if err := c.writeAvailability(ctx, entry.providerID, state,
			entry.result.probe, detail); err != nil {
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
		if entry.result.separable {
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

// serviceState probes a service status endpoint and reports whether it is running, along with
// the availability state and detail its answer implies. Every implementation whose product
// exposes an OPNsense service uses it, which is why it lives here rather than in one of them.
func (c *Collector) serviceState(ctx context.Context, endpoint opnsense.Endpoint) (
	bool, store.AvailabilityState, string, error) {
	response, err := c.client.Call(ctx, endpoint, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return false, store.StateUnavailable, "", err
	}
	if !response.OK() {
		return false, store.StateUnavailable, response.Detail, nil
	}
	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return false, store.StateUnavailable, "the status body could not be read", nil
	}
	status, present := decode.String(body, "status")
	if !present {
		return false, store.StateUnavailable, "the status body reported no status", nil
	}
	if decode.LowerASCII(status) == "running" {
		return true, store.StateReachable, "", nil
	}
	return false, store.StatePresentButDisabled,
		"the service reported " + quoteForDetail(status), nil
}

// readObject calls an endpoint and decodes its body as a JSON object, reporting whether that
// worked. A failure is not an error here: the caller turns it into an availability state,
// which is what an unreadable answer is.
func (c *Collector) readObject(ctx context.Context, endpoint opnsense.Endpoint) (
	decode.Object, bool, error) {
	response, err := c.client.Call(ctx, endpoint, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return nil, false, err
	}
	if !response.OK() {
		return nil, false, nil
	}
	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return nil, false, nil
	}
	return body, true, nil
}

// readCollection calls an endpoint and returns its rows, whatever collection shape it used: a
// bare array, a grid-search envelope, a map keyed by something, or a list under one of the
// wrapper keys the caller names.
//
// The wrapper keys are an UNVERIFIED assumption, like the telemetry field names: the survey
// establishes that those endpoints answer and not how they shape their answers. A body whose
// shape none of them matches yields no rows, which the caller records as an absent reading
// rather than as a zero.
func (c *Collector) readCollection(ctx context.Context, endpoint opnsense.Endpoint,
	wrapperKeys ...string) ([]decode.Object, bool, error) {
	response, err := c.client.Call(ctx, endpoint, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return nil, false, err
	}
	if !response.OK() {
		return nil, false, nil
	}

	var decoded any
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		return nil, false, nil
	}
	if object, isObject := decoded.(decode.Object); isObject {
		for _, key := range wrapperKeys {
			if nested, present := object[key]; present {
				if rows := decode.Objects(nested); len(rows) > 0 {
					return rows, true, nil
				}
			}
		}
	}
	rows, err := decode.Rows(response.Body)
	if err != nil {
		return nil, false, nil
	}
	return rows, true, nil
}

// quoteForDetail renders a value the firewall reported for an availability detail, so a reader
// can tell an empty answer from a missing one.
func quoteForDetail(value string) string {
	if value == "" {
		return "an empty status"
	}
	return "a status of " + value
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
