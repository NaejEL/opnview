// Package collect reads the five sources, decides which implementation of each
// kind opnview reads, records what it could not read, and writes the result.
//
// THE DIRECTORY TREE IS THE SEAM, AND THE LISTING IS THE DOCUMENTATION. There is not one
// collector per source here: there are KINDS, and several of them already have more than one
// implementation. So every directory below this one is named for exactly one of three things, and
// `ls` is enough to tell which:
//
//	<kind>/                  one KIND'S CONTRACT: its doc.go, its record types, its Source
//	                         interface and its registry (firewalllog, securityevent, dhcplease,
//	                         dnslookup, resolvercache, resolverlocaldata, measurement)
//	<kind>/<product>/        one IMPLEMENTATION of that kind: provider.json, its code, its
//	                         fixtures and its tests, holding only facts about that product
//	provider/                the contract every kind shares: the session port, the probe result,
//	                         the manifest and the registry primitive
//
// What stays in this package is machinery that genuinely spans the kinds -- each kind's
// COORDINATOR (<kind>.go: its cursor, its gap handling, everything true of any implementation of
// it, which needs the store and the discovery snapshot an implementation is never handed),
// runtime discovery, the probe round, the identity cascade, timestamp normalisation, the value
// helpers and the scheduler.
//
// The rule that layout has to keep passing -- ROADMAP.md, "Rules that apply to every step":
// ADDING AN IMPLEMENTATION IS ADDING A DIRECTORY, and touches nothing else. A Kea equivalent for
// another firewall is `dhcplease/<product>/` with a provider.json; no file here changes, because
// nothing selects an implementation at compile time, nothing branches on a product name outside
// that product's own directory, and the one file linking the directories into the binary
// (providers_gen.go) is generated from the tree. See docs/extending.md.
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
	"os"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/measurement"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/collect/resolvercache"
	"github.com/NaejEL/opnview/internal/collect/resolverlocaldata"
	"github.com/NaejEL/opnview/internal/collect/securityevent"
	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
)

// The provider kinds, which are the eleven the schema constrains `provider.kind` to. Those with a
// collector-side implementation take their value from their kind package.
const (
	// KindRegistrableDomain is a list giving a site name its registrable domain. It is a
	// service kind: services.go refreshes and consults its active provider, which acquires
	// the list through the plugin host's fetch facility.
	KindRegistrableDomain = registrabledomain.Kind
	// KindFirewallLog is data source 1.
	KindFirewallLog = firewalllog.Kind
	// KindSecurityEvent is data source 2. It admits several concurrently active
	// providers: people run Suricata, CrowdSec and Zenarmor together.
	KindSecurityEvent = securityevent.Kind
	// KindFlowVolume is data source 3, and nothing collects it. Its destination,
	// pair_volume_observation, is DERIVED from flow by step 5: the only per-pair
	// endpoint carries neither a port nor a protocol and the filter log carries
	// both. No implementation of it is registered here, deliberately.
	KindFlowVolume = "flow_volume"
	// KindDHCPLease is data source 4.
	KindDHCPLease = dhcplease.Kind
	// KindDNSLookup is data source 5.
	KindDNSLookup = dnslookup.Kind
	// KindIPGeolocation is a dataset placing public addresses. It is a service kind:
	// services.go refreshes its active provider and places addresses with it.
	KindIPGeolocation = ipgeolocation.Kind
	// KindMeasurementSample is the sampled reading: the firewall's own gauges and
	// the live per-pair traffic snapshot, and the kind eight of the ten surveyed
	// sources that fit an existing shape fit. It owns the sampler this package
	// already had, which was registered under flow_volume only because the kind
	// did not exist. It admits several concurrently active providers.
	KindMeasurementSample = measurement.Kind
	// KindReconciledState is the complete set of things of one type as of one
	// instant — the shape ten of the eleven sources that fit nothing else produce.
	// No implementation is registered for it: the kind gives such a source a
	// destination, and writing a connector for one is not this cycle's work.
	KindReconciledState = "reconciled_state"
	// KindResolverCache is the records a resolver holds in its cache: the answer
	// addresses the query report does not carry. "Cache" is Unbound's own word
	// (unbound-control(8), dump_cache). It admits several active providers, since
	// every record it stores names the provider that read it.
	KindResolverCache = resolvercache.Kind
	// KindResolverLocalData is the records a resolver serves from its local data,
	// host overrides among them: Unbound's own words again (unbound.conf(5),
	// local-data; unbound-control(8), list_local_data). Concurrent for the same
	// reason.
	KindResolverLocalData = resolverlocaldata.Kind
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

// Discovery is the snapshot of the firewall's own configuration the collectors join against.
// It is declared in the provider contract, because the measurement sampler is handed it.
type Discovery = provider.Discovery

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

// Collector holds everything the collectors share: the one HTTP client, the
// store, the clock, and the discovery snapshot they join against.
type Collector struct {
	// Host is the session port an implementation is handed: the one outbound capability, the
	// normaliser and the clock, and nothing else (provider.Session). It is embedded so that the
	// collector IS the session of every implementation it runs.
	*provider.Host

	// sources are the implementations this collector reads: the built-in plugins and the
	// plugins of its data directory, as the plugin host loaded them (internal/collect/sources).
	sources *sources.Set

	// triggers routes the core notifications to the loops of the providers that named them
	// (notifications.go).
	triggers triggers

	client *opnsense.Client
	// broker is the one path to the firewall API: the collector's host, discovery and the probe
	// round ask it.
	broker *broker.Broker
	// cadences are the intervals in force, each a core kind's readers' max age.
	cadences config.Config
	store    *store.Store
	clock    Clock

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
	// cacheAnswerDelaySeconds is the attribution_max_cache_answer_delay_seconds
	// setting: how long before a flow, at most, a lookup may have been made and still
	// name it through the resolver's records. The mutex guards it.
	cacheAnswerDelaySeconds int64
	// readDetails holds, per provider of the two resolver-record kinds, what its last
	// read recorded in words -- the counts, the size, the time, or the absence -- so the
	// probe round's rewrite of the availability row restates it rather than erasing it.
	// The mutex guards it.
	readDetails map[int64]string
	// readOutcomes holds the outcome code of the same reads, beside their words.
	readOutcomes map[int64]store.AvailabilityOutcome
	// leaseReadAt and localDataReadAt are the instants the last complete lease pass and
	// the last successful local-data pass of this run began, zero before either; and
	// localDataFailed whether the last local-data pass failed. The unresolved host-name
	// lookups are examined only once every source in use has been read after they were
	// ingested; see hostnameBound. The mutex guards all three.
	leaseReadAt     int64
	localDataReadAt int64
	localDataFailed bool
	// lastRefreshAt is the instant of the last successful aggregate refresh, and
	// onLinkFingerprint what classification read of the interfaces themselves --
	// which are upstream, their link-local rule, the networks that count -- at the
	// last derivation that placed every address; the mutex guards both.
	lastRefreshAt     int64
	onLinkFingerprint string
	// sampleAbsences holds, per measurement provider, what its last sampling pass could
	// not read, in words, so the probe round's rewrite of the availability row restates
	// it rather than erasing it. The mutex guards it.
	sampleAbsences map[int64]string
	// pairingWindowSeconds is the leg_pairing_window_seconds setting: how far apart
	// the two records of one connection may have been logged and still be paired. The
	// mutex guards it.
	pairingWindowSeconds int64
	// leaseFailureLimit is the lease_backend_failure_pass_limit setting, and
	// leaseFailures how many lease passes in a row each lease backend has failed,
	// by provider id; see collectLeases. The mutex guards both.
	leaseFailureLimit int64
	leaseFailures     map[int64]int64
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
	// services is what the collector last heard from each service kind (services.go).
	services services
	// hooks are the test hooks of the purge's ordering, zero outside a test.
	hooks orderingHooks
	// live is the live sampling: the lease it runs under and when each live pass last ran
	// (live.go).
	live liveSampling
}

// New returns a collector. The client is the only thing in opnview that builds an
// HTTP request, so passing it here is what makes "one outbound destination" hold
// for every collector at once. The page sizes start at their defaults until
// Configure is called with the loaded configuration.
func New(client *opnsense.Client, database *store.Store, clock Clock) *Collector {
	return NewWithSources(client, database, clock, plugin.Builtins())
}

// NewWithSources returns a collector reading the implementations of one source set: the built-in
// plugins and those of a data directory, as plugin.Load returns them.
func NewWithSources(client *opnsense.Client, database *store.Store, clock Clock, set *sources.Set) *Collector {
	if set == nil {
		set = plugin.Builtins()
	}
	if clock == nil {
		clock = SystemClock{}
	}
	c := &Collector{
		sources:         set,
		client:          client,
		store:           database,
		clock:           clock,
		pageSizes:       config.DefaultPageSizes(),
		maxDelaySeconds: config.DefaultAttributionMaxDelaySeconds,
		discovery:       newDiscovery(),
		probed:          map[string][]probedSource{},

		cacheAnswerDelaySeconds: config.DefaultCacheAnswerDelaySeconds,
		readDetails:             map[int64]string{},

		pairingWindowSeconds: config.DefaultLegPairingWindowSeconds,
		leaseFailureLimit:    config.DefaultLeaseBackendFailurePassLimit,
		leaseFailures:        map[int64]int64{},
		cadences:             config.Defaults(),
	}
	var mirrored *mirror.Mirror
	if database != nil {
		mirrored = mirror.New(database, c.now)
	}
	// THE BROKER IS THE ONE PATH TO THE FIREWALL (A9 of the broker amendment): every pass, the
	// probe round and discovery ask it, and it answers from its mirror or from one call.
	var settingReader broker.SettingReader
	if database != nil {
		settingReader = database.Setting
	}
	c.broker = broker.New(client, mirrored, broker.Options{Clock: clock, Firewall: client.BaseURL,
		Normaliser: c.knownNormaliser, Vocabulary: c.vocabulary, Setting: settingReader})
	if settingReader != nil {
		settings, refused := broker.LoadSettings(context.Background(), settingReader)
		for _, refusal := range refused {
			fmt.Fprintf(os.Stderr, "opnview: %v\n", refusal)
		}
		c.broker.Scheduler().SetSettings(settings)
	}
	for _, loaded := range set.Declarers() {
		loaded(c.broker)
	}
	c.declareCoreEndpoints()
	c.Host = provider.NewHost(c.broker, c.measureNormaliser, c.now)
	return c
}

// Broker is the collector's broker: the one path to the firewall API.
func (c *Collector) Broker() *broker.Broker { return c.broker }

// vocabulary is the installation's interface identifiers and devices, which mark an object keyed
// by them as value-keyed in the mirror. They are read from discovery, never written into a schema.
func (c *Collector) vocabulary() []string {
	snapshot := c.Discovery()
	names := make([]string, 0, len(snapshot.InterfaceIDByIdentifier)+len(snapshot.Devices))
	for identifier := range snapshot.InterfaceIDByIdentifier {
		names = append(names, identifier)
	}
	for device := range snapshot.Devices {
		names = append(names, device)
	}
	return names
}

// Configure applies a configuration to a running collector: the page sizes and the
// attribution delay, which are the parts of it the collectors read. The intervals reach the scheduler
// through config.Live, not through here. The next pass of each collector uses the
// new pages; a pass already running finishes with the ones it started with. It is
// what web.Reconfigurable asks of a collector.
func (c *Collector) Configure(settings config.Config) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cadences = settings
	c.pageSizes = config.PageSizes{
		FirewallLog:   settings.FirewallLogPageSize,
		SecurityEvent: settings.SecurityEventPageSize,
		DHCPLease:     settings.DHCPLeasePageSize,
	}
	if settings.AttributionMaxDelaySeconds > 0 {
		c.maxDelaySeconds = settings.AttributionMaxDelaySeconds
	}
	if settings.CacheAnswerDelaySeconds > 0 {
		c.cacheAnswerDelaySeconds = settings.CacheAnswerDelaySeconds
	}
	if settings.LegPairingWindowSeconds >= 0 {
		c.pairingWindowSeconds = settings.LegPairingWindowSeconds
	}
	if settings.LeaseBackendFailurePassLimit > 0 {
		c.leaseFailureLimit = settings.LeaseBackendFailurePassLimit
	}
}

// setSampleAbsence records what one measurement provider's last pass could not read.
func (c *Collector) setSampleAbsence(providerID int64, absent string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.sampleAbsences == nil {
		c.sampleAbsences = map[int64]string{}
	}
	if absent == "" {
		delete(c.sampleAbsences, providerID)
		return
	}
	c.sampleAbsences[providerID] = absent
}

// setReadDetail records what one resolver-record provider's last read said.
//
// The outcome is kept beside the words for the same reason: a probe round restating a
// read's refusal in the detail must not stamp the row `healthy` beside it.
func (c *Collector) setReadDetail(providerID int64, outcome store.AvailabilityOutcome, detail string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.readDetails == nil {
		c.readDetails = map[int64]string{}
	}
	if c.readOutcomes == nil {
		c.readOutcomes = map[int64]store.AvailabilityOutcome{}
	}
	c.readDetails[providerID] = detail
	c.readOutcomes[providerID] = outcome
}

// readDetail is what one resolver-record provider's last read said, or empty.
func (c *Collector) readDetail(providerID int64) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.readDetails[providerID]
}

// readOutcome is the outcome of one resolver-record provider's last read, or empty.
func (c *Collector) readOutcome(providerID int64) store.AvailabilityOutcome {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.readOutcomes[providerID]
}

// sampleAbsence is what one provider's last sampling pass could not read, or empty.
func (c *Collector) sampleAbsence(providerID int64) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.sampleAbsences[providerID]
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

// measureNormaliser returns the timestamp normaliser for this instant, carrying the
// reference time the year-less filter-log shape needs and the firewall's offset
// from UTC.
//
// NO ZONE-LESS TIMESTAMP IS READ BEFORE THE OFFSET IS KNOWN. At start the collectors
// and discovery run side by side, so a collector that gets here first measures the
// offset itself; if that fails, its pass fails and is retried, rather than storing
// records at a guessed time that nothing would later correct.
func (c *Collector) measureNormaliser(ctx context.Context) (decode.Normaliser, error) {
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
	return c.writeAvailability(ctx, providerID, state, provider.ResponseOutcome(response), response.Endpoint, detail)
}

// writeAvailability records a state determined by one probe endpoint, with the outcome
// code that names why. An empty outcome is refused rather than defaulted: a code nobody
// chose is a code that would have to be guessed back from the detail.
func (c *Collector) writeAvailability(ctx context.Context, providerID int64,
	state store.AvailabilityState, outcome store.AvailabilityOutcome, probe opnsense.Endpoint,
	detail string) error {
	if outcome == "" {
		return fmt.Errorf("collect: the availability of provider %d (%s %s) was written with no "+
			"outcome code", providerID, probe.Method, probe.Path)
	}
	var detailValue *string
	if detail != "" {
		detailValue = &detail
	}
	probeName := probe.Method + " " + probe.Path
	if err := c.store.SetAvailability(ctx, providerID, state, outcome, probeName, detailValue,
		c.now()); err != nil {
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

// Sources returns the implementations this collector reads.
func (c *Collector) Sources() *sources.Set { return c.sources }
