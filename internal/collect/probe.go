package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Availability probes and provider activation, written once for every kind.
//
// PROVIDER SELECTION READS THE FIREWALL'S OWN CONFIGURATION, never a preference order
// invented here. For each kind, every registered implementation is probed; the ones the
// firewall's own configuration marks as serving clients are the candidates; if exactly one
// qualifies it becomes active, and if two do, NEITHER is activated and the ambiguity is
// recorded on both rows. Choosing in that case would be opnview deciding which of two working
// sources is the truth, which is not opnview's decision to make.
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

// measurementProbeables returns the registered implementations of the flow_volume kind.
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
// geo_asn is not probed: acquiring the dataset is cycle 4C, and no second outbound destination
// exists in this cycle's code.
func (c *Collector) ProbeAll(ctx context.Context) error {
	var failures []error
	for _, probe := range []func(context.Context) error{
		c.probeFirewallLog,
		c.probeSecurityEvent,
		c.probeFlowVolume,
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

// probeFlowVolume probes every implementation of the flow_volume kind.
func (c *Collector) probeFlowVolume(ctx context.Context) error {
	return c.resolveKind(ctx, KindFlowVolume, measurementProbeables())
}

// probeDHCPLease probes every implementation of the dhcp_lease kind.
func (c *Collector) probeDHCPLease(ctx context.Context) error {
	return c.resolveKind(ctx, KindDHCPLease, leaseProbeables())
}

// probeDNSLookup probes every implementation of the dns_lookup kind.
func (c *Collector) probeDNSLookup(ctx context.Context) error {
	return c.resolveKind(ctx, KindDNSLookup, lookupProbeables())
}

// resolveKind probes each implementation of one kind, records what the firewall said about
// each, and activates the one the firewall's own configuration separates — or none, twice
// over: none when nothing qualifies, and none when more than one does.
func (c *Collector) resolveKind(ctx context.Context, kind string, probeables []probeable) error {
	type outcome struct {
		providerKey string
		providerID  int64
		result      probeResult
	}

	outcomes := make([]outcome, 0, len(probeables))
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
		outcomes = append(outcomes, outcome{
			providerKey: candidate.providerKey, providerID: providerID, result: result,
		})
	}

	separable := make([]string, 0, len(outcomes))
	for _, entry := range outcomes {
		if entry.result.separable {
			separable = append(separable, entry.providerKey)
		}
	}

	ambiguity := ""
	if len(separable) > 1 {
		ambiguity = "two implementations of this kind are both reachable and configured (" +
			joinWithComma(separable) + "), and the firewall's own configuration does not say " +
			"which serves clients, so opnview reads neither"
	}

	for _, entry := range outcomes {
		detail := entry.result.detail
		if ambiguity != "" && entry.result.separable {
			if detail == "" {
				detail = ambiguity
			} else {
				detail += "; " + ambiguity
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

	active := ""
	if len(separable) == 1 {
		active = separable[0]
	}
	if err := c.store.SetActiveProvider(ctx, kind, active); err != nil {
		return err
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: probing the %s kind was incomplete: %w",
			kind, joinErrors(failures))
	}
	return nil
}

// activeSourceKey returns the provider key of the implementation opnview reads for one kind,
// its registry id, and whether there is one.
//
// None active is a normal state: the probe round may have found nothing reachable, or two
// reachable implementations the firewall's own configuration does not separate, and in both
// cases nothing is collected for that kind rather than a guess being made.
func (c *Collector) activeSourceKey(ctx context.Context, kind string) (string, int64, bool, error) {
	providerID, active, err := c.store.ActiveProviderID(ctx, kind)
	if err != nil || !active {
		return "", 0, false, err
	}
	key, err := c.store.ProviderKey(ctx, providerID)
	if err != nil {
		return "", 0, false, err
	}
	return key, providerID, true, nil
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
