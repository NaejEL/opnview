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

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The provider kinds, which are the nine the schema constrains `provider.kind` to.
const (
	// KindPublicSuffix is the Public Suffix List. This package neither probes nor
	// reads it: internal/publicsuffix does, and the third outbound destination lives
	// there alone.
	KindPublicSuffix = "public_suffix"
	// KindFirewallLog is data source 1.
	KindFirewallLog = "firewall_log"
	// KindSecurityEvent is data source 2. It admits several concurrently active
	// providers: people run Suricata, CrowdSec and Zenarmor together.
	KindSecurityEvent = "security_event"
	// KindFlowVolume is data source 3, and nothing collects it. Its destination,
	// pair_volume_observation, is DERIVED from flow by step 5: the only per-pair
	// endpoint carries neither a port nor a protocol and the filter log carries
	// both. No implementation of it is registered here, deliberately.
	KindFlowVolume = "flow_volume"
	// KindDHCPLease is data source 4.
	KindDHCPLease = "dhcp_lease"
	// KindDNSLookup is data source 5.
	KindDNSLookup = "dns_lookup"
	// KindGeoASN is the MaxMind dataset. This package neither probes nor reads it:
	// internal/maxmind does, and the second outbound destination lives there alone.
	KindGeoASN = "geo_asn"
	// KindMeasurementSample is the sampled reading: the firewall's own gauges and
	// the live per-pair traffic snapshot, and the kind eight of the ten surveyed
	// sources that fit an existing shape fit. It owns the sampler this package
	// already had, which was registered under flow_volume only because the kind
	// did not exist. It admits several concurrently active providers.
	KindMeasurementSample = "measurement_sample"
	// KindReconciledState is the complete set of things of one type as of one
	// instant — the shape ten of the eleven sources that fit nothing else produce.
	// No implementation is registered for it: the kind gives such a source a
	// destination, and writing a connector for one is not this cycle's work.
	KindReconciledState = "reconciled_state"
)

// The provider keys the schema registers. They are data, not configuration:
// nothing branches on them beyond choosing which endpoint set to call, which is
// exactly what a provider is.
const (
	// ProviderPf is the pf filter log.
	ProviderPf = "pf"
	// ProviderSuricata is the IDS alert feed.
	ProviderSuricata = "suricata"
	// ProviderInsight is the firewall's own sampler: the live per-pair traffic
	// snapshot and the system gauges, whose activeness the firewall's NetFlow
	// configuration governs. It keeps the key it was registered under in 4A; the
	// kind it answers for is measurement_sample.
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
	// Upstream holds the interfaces the firewall reports a gateway behind. Evidence
	// read on one of them -- a neighbour, a lease -- does not make an address a member
	// of it: an address reached through an upstream interface is outside.
	Upstream map[int64]struct{}
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
		Upstream:                map[int64]struct{}{},
	}
}

// Collector holds everything the five collectors share: the one HTTP client, the
// store, the clock, and the discovery snapshot they join against.
type Collector struct {
	client *opnsense.Client
	store  *store.Store
	clock  Clock

	// firewallOffsetSeconds is survey gap 7: the firewall's offset from UTC, needed
	// by the timestamps that carry no zone. It is zero until discovery has measured
	// it from the firewall's own clock, and measured again on every refresh; the
	// mutex below guards it.
	firewallOffsetSeconds int
	// offsetMeasured says whether it has been measured at least once.
	offsetMeasured bool

	// pageSizes is how many records each paged read asks for. It is the operator's
	// setting, held here rather than read from the database on every pass, and
	// replaced by Configure when the collection surface saves a new one; the mutex
	// below guards it with the discovery snapshot. A collector reads it once per
	// pass, through pages.
	pageSizes config.PageSizes

	mutex     sync.RWMutex
	discovery Discovery
	// probed is what the last probe round found for each kind, kept so that a
	// selection the operator saves can be applied at once, without probing the
	// firewall again; see Reselect. The mutex above guards it.
	probed map[string][]probedSource

	// maxDelaySeconds is the attribution_max_delay_seconds setting: how long before a
	// flow a lookup may have been made and still name it. The mutex guards it.
	maxDelaySeconds int64
	// lastRefreshAt is the instant of the last successful aggregate refresh, and
	// onLinkFingerprint what classification read of the interfaces themselves --
	// which are upstream, their link-local rule, the networks that count -- at the
	// last derivation that placed every address; the mutex guards both.
	lastRefreshAt     int64
	onLinkFingerprint string
	// hostnamesSince is where the next lease pass starts resolving host-name lookups
	// again: the watermark of the previous one, so a pass resolves only the lookups
	// ingested since and its work does not grow with those that stay unresolved. It
	// starts at the collector's creation, so a run does not retry what an earlier run
	// tried. The mutex guards it.
	hostnamesSince int64
	// ingesting holds the ingestion instant of every pass that has stamped rows with
	// it and has not yet finished writing them, keyed by a sequence number; the
	// mutex guards both. The refresh never moves its watermark past the oldest of
	// them, because rows stamped with that instant may commit after the refresh
	// has read; see beginIngest.
	ingesting      map[uint64]int64
	ingestSequence uint64
	// deriveMutex serialises the derivations, so two passes finishing together
	// never refresh one slot from two half-classified states.
	deriveMutex sync.Mutex
	// purgeMutex orders the retention purge against the passes: a pass holds it for
	// reading from before it stores its first row until its derivation has ended, and
	// the purge holds it for writing. It is taken before deriveMutex, never after; see
	// purge.go.
	purgeMutex sync.RWMutex
	// hooks are the test hooks of the purge's ordering, zero outside a test.
	hooks orderingHooks
}

// New returns a collector. The client is the only thing in opnview that builds an
// HTTP request, so passing it here is what makes "one outbound destination" hold
// for every collector at once. The page sizes start at their defaults until
// Configure is called with the loaded configuration.
func New(client *opnsense.Client, database *store.Store, clock Clock) *Collector {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Collector{
		client:          client,
		store:           database,
		clock:           clock,
		pageSizes:       config.DefaultPageSizes(),
		maxDelaySeconds: config.DefaultAttributionMaxDelaySeconds,
		discovery:       newDiscovery(),
		probed:          map[string][]probedSource{},
		hostnamesSince:  clock.Now().Unix(),
	}
}

// Configure applies a configuration to a running collector: the page sizes and the
// attribution delay, which are the parts of it the collectors read. The intervals reach the scheduler
// through config.Live, not through here. The next pass of each collector uses the
// new pages; a pass already running finishes with the ones it started with. It is
// what web.Reconfigurable asks of a collector.
func (c *Collector) Configure(settings config.Config) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.pageSizes = config.PageSizes{
		FirewallLog:   settings.FirewallLogPageSize,
		SecurityEvent: settings.SecurityEventPageSize,
		DHCPLease:     settings.DHCPLeasePageSize,
	}
	if settings.AttributionMaxDelaySeconds > 0 {
		c.maxDelaySeconds = settings.AttributionMaxDelaySeconds
	}
}

// PageSizes returns the page sizes the collectors use now.
//
// It exists for a test to assert that a saved setting reached a running
// collector. Nothing should use it as a second source of truth: the setting rows
// are the truth, and this is what the collectors were last told.
func (c *Collector) PageSizes() config.PageSizes { return c.pages() }

// pages returns the page sizes in force.
func (c *Collector) pages() config.PageSizes {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.pageSizes
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
// reference time the year-less filter-log shape needs and the firewall's offset
// from UTC.
//
// NO ZONE-LESS TIMESTAMP IS READ BEFORE THE OFFSET IS KNOWN. At start the collectors
// and discovery run side by side, so a collector that gets here first measures the
// offset itself; if that fails, its pass fails and is retried, rather than storing
// records at a guessed time that nothing would later correct.
func (c *Collector) normaliser(ctx context.Context) (decode.Normaliser, error) {
	c.mutex.RLock()
	offset, measured := c.firewallOffsetSeconds, c.offsetMeasured
	c.mutex.RUnlock()
	if !measured {
		if err := c.discoverClock(ctx); err != nil {
			return decode.Normaliser{}, fmt.Errorf("collect: the firewall's offset from UTC "+
				"is not known yet, so no zone-less timestamp is read: %w", err)
		}
		offset = c.FirewallOffsetSeconds()
	}
	return decode.Normaliser{Reference: c.clock.Now(), OffsetSeconds: offset}, nil
}

// FirewallOffsetSeconds is the firewall's offset from UTC as discovery last measured
// it, and zero before it has.
func (c *Collector) FirewallOffsetSeconds() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.firewallOffsetSeconds
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
