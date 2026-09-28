// Package collect reads the five sources, decides which implementation of each
// kind opnview reads, records what it could not read, and writes the result.
//
// THE FILE NAMES ARE THE SEAM, AND THE LISTING IS THE DOCUMENTATION. There are
// not five collectors here: there are five KINDS, and several of them already have
// more than one implementation. So every file is named for exactly one of three
// things, and `ls` is enough to tell which:
//
//	<kind>.go               the kind: its coordinator, its cursor, its gap
//	                        handling — everything true of any implementation of it
//	<kind>_<product>.go     one implementation of that kind, holding only facts
//	                        about that product
//	everything else         machinery that genuinely spans the kinds: the contract
//	                        (provider.go) and how an implementation announces
//	                        itself (registry.go), runtime discovery, the probe
//	                        round, the identity cascade, timestamp normalisation,
//	                        the value helpers, the scheduler
//
// The rule that layout has to keep passing — ROADMAP.md, "Rules that apply to
// every step": ADDING AN IMPLEMENTATION IS ADDING ONE FILE AND ONE REGISTRY ROW,
// and touches nothing else. A Kea equivalent for another firewall is
// `dhcplease_<product>.go` and a row in the schema's registry; no file here
// changes, because nothing selects an implementation at compile time and nothing
// branches on a product name outside that product's own file.
//
// Three further properties are worth stating before the code, because each one is
// the answer to something measured rather than a style choice.
//
// AN EMPTY SUCCESS IS A CLAIM, AND A CLAIM IS CHECKED. Every source returns an
// empty list both when it is disabled and when it is quiet, so nothing here ever
// concludes "no data" from an empty answer. What it concludes is recorded in
// source_availability, with the probe that determined it, and when a window was
// genuinely missed it is recorded in collection_gap.
//
// NOTHING IS WRITTEN TO THE FIREWALL. internal/opnsense refuses a mutating
// command before a request exists, and where a firewall setting has to be turned
// on for a source to exist — the resolver query log, the Suricata event types —
// opnview reads the flag and reports it. It never sets it.
//
// NOTHING IS CLASSIFIED BY ITS NAME. No interface identifier, description, VLAN
// name, address or CIDR is a literal here, and no branch reads the TEXT of a
// name, a description or a label. Where a classification cannot be made from a
// field whose meaning the survey establishes, it is left unclassified and said
// so, rather than guessed from a token.
package collect

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The provider kinds, which are the six the schema constrains `provider.kind` to.
const (
	// KindFirewallLog is data source 1.
	KindFirewallLog = "firewall_log"
	// KindSecurityEvent is data source 2.
	KindSecurityEvent = "security_event"
	// KindFlowVolume is data source 3, and in this cycle also the owner of the
	// sampled-measurement collector: the per-pair volume it samples is flow
	// volume, and no aggregate of it exists upstream.
	KindFlowVolume = "flow_volume"
	// KindDHCPLease is data source 4.
	KindDHCPLease = "dhcp_lease"
	// KindDNSLookup is data source 5.
	KindDNSLookup = "dns_lookup"
	// KindGeoASN is the MaxMind dataset. This cycle neither probes nor reads it:
	// acquisition is cycle 4C, and no second outbound destination exists here.
	KindGeoASN = "geo_asn"
)

// The provider keys the schema registers. They are data, not configuration:
// nothing branches on them beyond choosing which endpoint set to call, which is
// exactly what a provider is.
const (
	// ProviderPf is the pf filter log.
	ProviderPf = "pf"
	// ProviderSuricata is the IDS alert feed.
	ProviderSuricata = "suricata"
	// ProviderInsight is the NetFlow / Insight volume source.
	ProviderInsight = "insight"
	// ProviderKea is the Kea DHCP backend.
	ProviderKea = "kea"
	// ProviderDnsmasq is the Dnsmasq backend, which serves both DHCP and DNS.
	ProviderDnsmasq = "dnsmasq"
	// ProviderISC is the end-of-life ISC dhcpd plugin.
	ProviderISC = "isc"
	// ProviderUnbound is the Unbound resolver.
	ProviderUnbound = "unbound"
)

// Clock is the time the scheduler and the collectors read. It is an interface so
// that a test drives every loop deterministically instead of sleeping.
type Clock interface {
	// Now is the current instant.
	Now() time.Time
	// After returns a channel that delivers once, after d.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current instant.
func (SystemClock) Now() time.Time { return time.Now() }

// After returns a channel that delivers once, after d.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Discovery is the snapshot of the firewall's own configuration the collectors
// join against: the two first-class join keys, and the interface identifiers the
// per-pair sampler needs.
type Discovery struct {
	// InterfaceIDByIdentifier maps a configuration key to its stored row.
	InterfaceIDByIdentifier map[string]int64
	// InterfaceIDByDevice maps a network device name to its stored row. A device
	// absent from this map is the modelled `not_found` state, never a dropped row.
	InterfaceIDByDevice map[string]int64
	// Identifiers are the configuration keys, which is what
	// /api/diagnostics/traffic/top takes.
	Identifiers []opnsense.InterfaceName
	// Devices are the network device names, which is what that endpoint must
	// NEVER be given: a device name returns an empty array with HTTP 200, which
	// is indistinguishable from an absence of traffic.
	Devices map[string]struct{}
	// RuleIDByPfLabel maps a filter-log `rid` to its stored rule. A rid absent
	// from this map is normal: the rule was removed.
	RuleIDByPfLabel map[string]int64
	// RefreshedAt is when this snapshot was read.
	RefreshedAt int64
}

// newDiscovery returns an empty snapshot. Empty is the honest starting state: on
// the first pass nothing is discovered, so every join key reports `pending`
// rather than `not_found`.
func newDiscovery() Discovery {
	return Discovery{
		InterfaceIDByIdentifier: map[string]int64{},
		InterfaceIDByDevice:     map[string]int64{},
		Devices:                 map[string]struct{}{},
		RuleIDByPfLabel:         map[string]int64{},
	}
}

// Collector holds everything the five collectors share: the one HTTP client, the
// store, the clock, and the discovery snapshot they join against.
type Collector struct {
	client *opnsense.Client
	store  *store.Store
	clock  Clock

	// firewallOffsetSeconds is the recorded assumption of survey gap 7: the
	// firewall's offset from UTC, needed by the one timestamp shape that carries
	// no zone. It is zero until a live firewall says otherwise.
	firewallOffsetSeconds int

	mutex     sync.RWMutex
	discovery Discovery
}

// New returns a collector. The client is the only thing in opnview that builds an
// HTTP request, so passing it here is what makes "one outbound destination" hold
// for every collector at once.
func New(client *opnsense.Client, database *store.Store, clock Clock) *Collector {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Collector{
		client:    client,
		store:     database,
		clock:     clock,
		discovery: newDiscovery(),
	}
}

// Discovery returns the current snapshot.
func (c *Collector) Discovery() Discovery {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.discovery
}

// setDiscovery replaces the snapshot.
func (c *Collector) setDiscovery(snapshot Discovery) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.discovery = snapshot
}

// call issues one request to the firewall API.
//
// It exists so that the port an implementation is handed can grant ONE OUTBOUND
// CALL rather than the client that makes it. The client is the only thing in
// opnview that builds an HTTP request, and handing it across the seam would hand
// over its construction, its credentials and whatever it grows next; handing over
// this method hands over a request and a response and nothing else. It is also the
// shape that survives connectors moving out of process, where the host performs the
// call and the connector never holds a client at all.
func (c *Collector) call(ctx context.Context, endpoint opnsense.Endpoint,
	options opnsense.RequestOptions) (opnsense.Response, error) {
	return c.client.Call(ctx, endpoint, options)
}

// normaliser returns the timestamp normaliser for this instant, carrying the
// reference time the year-less filter-log shape needs.
func (c *Collector) normaliser() decode.Normaliser {
	return decode.Normaliser{Reference: c.clock.Now(), OffsetSeconds: c.firewallOffsetSeconds}
}

// now is the current instant as the UTC epoch every column stores.
func (c *Collector) now() int64 { return c.clock.Now().UTC().Unix() }

// recordOutcome turns one response into the availability state the survey names
// for it, and writes it with the probe and the instant.
//
// The mapping is the survey's, not an invention, and the important half is what
// it refuses to do: an OK response with an empty collection is `reachable`, never
// `unavailable`, because a source that is working and has nothing to say is a
// normal state — a reachable IDS with a narrow ruleset and nothing to report is
// the case the verified section describes, and treating it as a fault would be
// the defect.
func (c *Collector) recordOutcome(ctx context.Context, providerID int64,
	response opnsense.Response, extra string) error {
	state := store.StateUnavailable
	switch response.Outcome {
	case opnsense.OutcomeOK:
		state = store.StateReachable
	case opnsense.OutcomeNotFound:
		// An absent module is unavailable, and it is the normal signal that an
		// optional component is not installed.
		state = store.StateUnavailable
	case opnsense.OutcomeForbidden, opnsense.OutcomeEmptyBody,
		opnsense.OutcomeResultFailed, opnsense.OutcomeServerError,
		opnsense.OutcomeTransportFailure:
		state = store.StateUnavailable
	}
	detail := response.Detail
	if extra != "" {
		if detail == "" {
			detail = extra
		} else {
			detail += "; " + extra
		}
	}
	return c.writeAvailability(ctx, providerID, state, response.Endpoint, detail)
}

// writeAvailability records a state determined by one probe endpoint.
func (c *Collector) writeAvailability(ctx context.Context, providerID int64,
	state store.AvailabilityState, probe opnsense.Endpoint, detail string) error {
	var detailValue *string
	if detail != "" {
		detailValue = &detail
	}
	probeName := probe.Method + " " + probe.Path
	if err := c.store.SetAvailability(ctx, providerID, state, probeName, detailValue, c.now()); err != nil {
		return fmt.Errorf("collect: recording availability: %w", err)
	}
	return nil
}

// activeProvider returns the provider of a kind opnview reads, and whether there
// is one. None active is a normal state: the probe round may have found nothing
// reachable, or two reachable implementations the firewall's own configuration
// does not separate, and in both cases nothing is collected for that kind rather
// than a guess being made.
func (c *Collector) activeProvider(ctx context.Context, kind string) (int64, bool, error) {
	return c.store.ActiveProviderID(ctx, kind)
}

// joinErrors folds several failures into one error without losing any of them.
//
// It stayed in this package when the decoding layer moved out: a pass that reads five kinds
// reports every failure it met rather than the first, and that is a fact about how collection
// is organised, not about how a field is read.
func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	joined := errs[0]
	for _, err := range errs[1:] {
		joined = fmt.Errorf("%w; %w", joined, err)
	}
	return joined
}
