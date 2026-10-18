package collecttest

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/plugin"

	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/kinds/filterlog"
	"github.com/NaejEL/opnview/internal/collect/kinds/leases"
	"github.com/NaejEL/opnview/internal/collect/kinds/resolverrecords"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The fake firewall every collector test runs against is internal/collect/providertest: an
// implementation's own tests run against the same one. What is left here is what only the
// coordinators' tests need.

// FixedClock is a clock a test drives. It never advances on its own, so nothing in a
// test sleeps and nothing depends on how fast the machine is.
//
// Each call to After hands out ITS OWN channel, and that matters: a single shared
// channel would let whichever loop re-selects first take every release, and a test
// built on it would report that the other loops had stalled when they had merely not
// been woken. One channel per waiter makes "release every waiting loop once" a thing a
// test can actually do.
type FixedClock struct {
	Mutex   sync.Mutex
	Instant time.Time
	Waiters []chan time.Time
}

// NewFixedClock returns a clock stopped at an instant.
func NewFixedClock(at time.Time) *FixedClock {
	return &FixedClock{Instant: at}
}

// Now returns the clock's instant.
func (c *FixedClock) Now() time.Time {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	return c.Instant
}

// After registers a waiter and returns its channel.
func (c *FixedClock) After(time.Duration) <-chan time.Time {
	channel := make(chan time.Time, 1)
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.Waiters = append(c.Waiters, channel)
	return channel
}

// Waiting reports how many loops are currently waiting for a tick.
func (c *FixedClock) Waiting() int {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	return len(c.Waiters)
}

// ReleaseAll wakes every loop that is currently waiting, exactly once each, and returns
// how many it woke. The channels are buffered, so it never blocks on a loop that has
// already stopped.
func (c *FixedClock) ReleaseAll() int {
	c.Mutex.Lock()
	waiters := c.Waiters
	c.Waiters = nil
	instant := c.Instant
	c.Mutex.Unlock()

	for _, channel := range waiters {
		channel <- instant
	}
	return len(waiters)
}

// AwaitWaiters blocks until at least count loops are waiting for a tick, or fails the
// test. It is how a test synchronises with the scheduler without sleeping for a guessed
// duration.
func (c *FixedClock) AwaitWaiters(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.Waiting() < count {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d loops are waiting for a tick after five seconds",
				c.Waiting(), count)
		}
		runtime.Gosched()
	}
}

// Advance moves the clock forward.
func (c *FixedClock) Advance(by time.Duration) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.Instant = c.Instant.Add(by)
}

// NewTestStore opens a database under t.TempDir, so no test ever writes a database,
// a -wal or a -shm file into the working tree.
func NewTestStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(context.Background(), storetest.Database(t))
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database: %v", err)
		}
	})
	return database
}

// CountRows counts a table, failing the test on an error.
func CountRows(t *testing.T, database *store.Store, table string) int {
	t.Helper()
	count, err := database.CountRows(context.Background(), table)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return count
}

// ArrangeDiscoverableFirewall configures the fake with the discovery responses and
// runs one discovery pass, which is the precondition of every collector.
func ArrangeDiscoverableFirewall(t *testing.T, fake *providertest.Firewall, collector *collect.Collector) {
	t.Helper()
	fake.AnswerFixture(opnsense.InterfacesInfo, "interfaces_info.json")
	fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
	// The firewall's clock, from which discovery measures its offset from UTC: the
	// fixture is a firewall on UTC at the reference instant, so the offset is zero.
	fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
}

// ReferenceInstant is the clock every collector test runs at.
//
// It is the day after the instants the fixtures carry, so a year-less filter-log
// timestamp resolves against a reference that is a few minutes ahead of it rather than
// a year away — which is what a real poll looks like, and what makes the year inference
// meaningful here rather than trivially satisfied.
func ReferenceInstant() time.Time {
	return time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
}

// ReferenceEpoch is referenceInstant as the UTC epoch every column stores.
func ReferenceEpoch() int64 { return ReferenceInstant().Unix() }

// NewTestCollector is a collector for a test: its broker's ceilings raised past anything a test
// reaches -- a test's clock does not move, so a calls-per-second ceiling would hold its calls for
// ever -- and its broker closed when the test ends, so no subscription holds the fake firewall.
func NewTestCollector(t *testing.T, client *opnsense.Client, database *store.Store, clock core.Clock) *collect.Collector {
	t.Helper()
	collector := collect.NewWithSources(client, database, clock, nil)
	collector.Broker().Scheduler().SetSettings(schedule.Settings{MaxInFlight: 64, MaxPerSecond: 1e6})
	// A test runs its passes back to back at one frozen instant, where a pass at its interval would
	// find the answer it read an instant before fresh in the mirror: its readers' max ages are zero,
	// so every pass asks the firewall, as each test arranges it. The broker's serving from its mirror
	// is internal/broker's tests' subject.
	collector.Configure(ZeroMaxAges(config.Defaults()))
	t.Cleanup(collector.Broker().Close)
	return collector
}

// ArrangeLeaseCollection stands up a harness whose Dnsmasq backend is the active one, which
// is what the surveyed firewall reported: Kea disabled, Dnsmasq running.
func ArrangeLeaseCollection(t *testing.T) *ProbeHarness {
	t.Helper()
	harness := NewProbeHarness(t)
	ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(KeaStatus, "kea_status_disabled.json")
	harness.Fake.AnswerFixture(DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(DnsmasqSettings, "dnsmasq_settings.json")
	harness.Fake.AnswerFixture(DnsmasqLeases, "dnsmasq_leases.json")
	harness.Fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	harness.Fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")
	if err := harness.Collector.ProbeKind(context.Background(), leases.Kind); err != nil {
		t.Fatalf("probing the lease backends: %v", err)
	}
	if key := harness.ActiveKeyOf(t, leases.Kind); key != ProviderDnsmasq {
		t.Fatalf("the lease kind activated %q", key)
	}
	return harness
}

// StringPointer returns a pointer to a value, so a test can express "the backend reported this
// field" and "the backend did not report it" as two different values rather than as a zero.
func StringPointer(value string) *string { return &value }

// ArrangeFilterLogCollection stands up a harness whose filter log is the active source.
func ArrangeFilterLogCollection(t *testing.T) *ProbeHarness {
	t.Helper()
	harness := NewProbeHarness(t)
	ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(opnsense.FirewallLog, "firewall_log.json")
	if err := harness.Collector.ProbeKind(context.Background(), filterlog.Kind); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if key := harness.ActiveKeyOf(t, filterlog.Kind); key != ProviderPf {
		t.Fatalf("the filter-log kind activated %q", key)
	}
	return harness
}

// ScalarCount runs a counting query against the test database.
func ScalarCount(t *testing.T, database *store.Store, query string, arguments ...any) int {
	t.Helper()
	var value int
	if err := database.DB().QueryRowContext(context.Background(), query, arguments...).
		Scan(&value); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return value
}

// AvailabilityInstant reads when one provider's state was last determined.
func (h *ProbeHarness) AvailabilityInstant(t *testing.T, kind, providerKey string) int64 {
	t.Helper()
	_, _, checkedAt := h.AvailabilityOf(t, kind, providerKey)
	return checkedAt
}

// LeaseRetryHarness is the lease harness with a pending lookup whose host name a lease
// names: the lookup is resolved by the first lease pass that examines it.
func LeaseRetryHarness(t *testing.T, key, hostname string) *ProbeHarness {
	t.Helper()
	harness := ArrangeLeaseCollection(t)
	ctx := context.Background()
	UnresolvedLookup(t, harness.Store, key, hostname+".example.invalid",
		ReferenceEpoch()-120, ReferenceEpoch()-100)
	provider, err := harness.Store.ProviderID(ctx, leases.Kind, ProviderDnsmasq)
	if err != nil {
		t.Fatalf("looking up the readable backend: %v", err)
	}
	label := hostname
	if err := harness.Store.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		Backend: "dnsmasq", Address: "198.51.100.51", Hostname: &label, LeaseState: "reserved",
		GenerationKey: vocab.GenerationKeyOf(nil, nil, ReferenceEpoch()), ObservedAt: ReferenceEpoch(),
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	return harness
}

// MustProviderID looks one lease provider's registry id up.
func MustProviderID(t *testing.T, harness *ProbeHarness, key string) int64 {
	t.Helper()
	id, err := harness.Store.ProviderID(context.Background(), leases.Kind, key)
	if err != nil {
		t.Fatalf("looking up %s: %v", key, err)
	}
	return id
}

// UnresolvedLookup stores one lookup whose logged host name named no lease.
func UnresolvedLookup(t *testing.T, database *store.Store, key, hostname string, lookedUpAt, ingestedAt int64) {
	t.Helper()
	name := hostname
	if err := database.InsertDNSResolution(context.Background(), vocab.DNSResolution{
		LookupKey: key, ClientAddress: name, ClientHostname: &name,
		ClientResolution: vocab.ClientResolutionUnknown, Domain: "example-site.example.invalid",
		Resolver: "unbound", Action: "pass", LookedUpAt: lookedUpAt, IngestedAt: ingestedAt,
	}); err != nil {
		t.Fatalf("writing the lookup: %v", err)
	}
}

// Examined reads whether a lease pass has examined one lookup, and its resolution.
func Examined(t *testing.T, database *store.Store, key string) (bool, string) {
	t.Helper()
	var (
		at         *int64
		resolution string
	)
	if err := database.DB().QueryRow(`SELECT hostname_examined_at, client_resolution FROM dns_resolution
		WHERE lookup_key = $1`, key).Scan(&at, &resolution); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return at != nil, resolution
}

// OutcomeOf reads one provider's recorded outcome code.
func (h *ProbeHarness) OutcomeOf(t *testing.T, kind, providerKey string) vocab.AvailabilityOutcome {
	t.Helper()
	ctx := context.Background()
	providerID, err := h.Store.ProviderID(ctx, kind, providerKey)
	if err != nil {
		t.Fatalf("looking up %s/%s: %v", kind, providerKey, err)
	}
	outcome, err := h.Store.AvailabilityOutcomeOf(ctx, providerID)
	if err != nil {
		t.Fatalf("reading the outcome of %s/%s: %v", kind, providerKey, err)
	}
	return outcome
}

// PlantInsertFailure makes every insert into a table fail, as a full disk or a broken
// database would, through a trigger planted in the test database.
func PlantInsertFailure(t *testing.T, database *store.Store, table string) {
	t.Helper()
	PlantFailure(t, database, table, "true")
}

// PlantFailure makes every insert into a table that meets a condition fail, as a store failure
// would: a trigger raising an error, in the test's own database.
func PlantFailure(t *testing.T, database *store.Store, table, condition string) {
	t.Helper()
	for _, statement := range []string{
		`CREATE FUNCTION planted_` + table + `_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF ` + condition + ` THEN RAISE EXCEPTION 'planted store failure'; END IF;
			RETURN NEW;
		END $$`,
		`CREATE TRIGGER planted_` + table + `_failure BEFORE INSERT ON ` + table +
			` FOR EACH ROW EXECUTE FUNCTION planted_` + table + `_failure()`,
	} {
		if _, err := database.DB().ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("planting the store failure on %s: %v", table, err)
		}
	}
}

// ProbeHarness is a collector, a fake and a store, wired together.
type ProbeHarness struct {
	Fake      *providertest.Firewall
	Store     *store.Store
	Collector *collect.Collector
	Clock     *FixedClock
}

// NewProbeHarness returns a harness with a fake that answers 404 everywhere, which is the
// honest starting point: an endpoint the test did not arrange is a component that is not
// installed.
func NewProbeHarness(t *testing.T) *ProbeHarness {
	t.Helper()
	fake := providertest.NewFirewall(t)
	database := NewTestStore(t)
	clock := NewFixedClock(ReferenceInstant())
	return &ProbeHarness{
		Fake:      fake,
		Store:     database,
		Collector: NewTestCollector(t, providertest.NewClient(t, fake), database, clock),
		Clock:     clock,
	}
}

// AvailabilityOf reads one provider's recorded state.
func (h *ProbeHarness) AvailabilityOf(t *testing.T, kind, providerKey string) (
	vocab.AvailabilityState, string, int64) {
	t.Helper()
	providerID, err := h.Store.ProviderID(context.Background(), kind, providerKey)
	if err != nil {
		t.Fatalf("looking up %s/%s: %v", kind, providerKey, err)
	}
	state, probe, checkedAt, err := h.Store.Availability(context.Background(), providerID)
	if err != nil {
		t.Fatalf("reading availability for %s/%s: %v", kind, providerKey, err)
	}
	return state, probe, checkedAt
}

// DetailOf reads one provider's recorded detail.
func (h *ProbeHarness) DetailOf(t *testing.T, kind, providerKey string) string {
	t.Helper()
	ctx := context.Background()
	providerID, err := h.Store.ProviderID(ctx, kind, providerKey)
	if err != nil {
		t.Fatalf("looking up %s/%s: %v", kind, providerKey, err)
	}
	var detail *string
	if err := h.Store.DB().QueryRowContext(ctx,
		"SELECT detail FROM source_availability WHERE provider_id = $1", providerID).
		Scan(&detail); err != nil {
		t.Fatalf("reading the detail for %s/%s: %v", kind, providerKey, err)
	}
	if detail == nil {
		return ""
	}
	return *detail
}

// MovedRows returns the registry rows whose availability now reads exactly one state AND
// names one probe endpoint, rendered as sorted "kind/provider_key".
//
// It is the quantifier. Asserting that the row under test moved says nothing about the
// other eight: ProbeAll rewrites every provider's row on every pass, so a collector that
// smeared one source's conclusion across a kind would leave the target row correct and
// still be wrong. Scoping to (state, probe) is what makes "exactly one row moved to the
// state the survey names" a thing that can fail.
func (h *ProbeHarness) MovedRows(t *testing.T,
	state vocab.AvailabilityState, probePath string) []string {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT p.kind, p.provider_key FROM source_availability a
		 JOIN provider p ON p.id = a.provider_id
		 WHERE a.state = $1 AND a.probe LIKE '%' || $2 || '%'
		 ORDER BY p.kind, p.provider_key`,
		string(state), probePath)
	if err != nil {
		t.Fatalf("reading the availability table: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var moved []string
	for rows.Next() {
		var kind, key string
		if err := rows.Scan(&kind, &key); err != nil {
			t.Fatalf("reading the availability table: %v", err)
		}
		moved = append(moved, kind+"/"+key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the availability table: %v", err)
	}
	return moved
}

// RowsInState counts the registry rows holding one state, whatever probe determined it.
func (h *ProbeHarness) RowsInState(t *testing.T, state vocab.AvailabilityState) int {
	t.Helper()
	var count int
	if err := h.Store.DB().QueryRowContext(context.Background(),
		"SELECT count(*) FROM source_availability WHERE state = $1", string(state)).
		Scan(&count); err != nil {
		t.Fatalf("counting the rows in state %q: %v", state, err)
	}
	return count
}

// ActiveKeysOf returns every provider key opnview reads for a kind, in registry order. It
// reads the plural accessor whatever the kind, because a kind that admits several active
// providers has no single answer and asking for one is refused.
func (h *ProbeHarness) ActiveKeysOf(t *testing.T, kind string) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := h.Store.ActiveProviderIDs(ctx, kind)
	if err != nil {
		t.Fatalf("reading the active %s providers: %v", kind, err)
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		key, err := h.Store.ProviderKey(ctx, id)
		if err != nil {
			t.Fatalf("reading an active %s provider's key: %v", kind, err)
		}
		keys = append(keys, key)
	}
	return keys
}

// ActiveKeyOf returns the one provider key opnview reads for a kind, or the empty string. It
// fails the test if a kind has several active providers, which is what makes it safe to call
// on the kinds whose contract is exclusive.
func (h *ProbeHarness) ActiveKeyOf(t *testing.T, kind string) string {
	t.Helper()
	keys := h.ActiveKeysOf(t, kind)
	switch len(keys) {
	case 0:
		return ""
	case 1:
		return keys[0]
	default:
		t.Fatalf("the %s kind has %d active providers: %v", kind, len(keys), keys)
		return ""
	}
}

// SelectSource stores the operator's selection of one implementation.
func (h *ProbeHarness) SelectSource(t *testing.T, kind, providerKey, value string) {
	t.Helper()
	key := config.KeySourceSelection(kind, providerKey)
	if err := h.Store.SetSetting(context.Background(), key, value, ReferenceInstant().Unix()); err != nil {
		t.Fatalf("storing the selection %s = %q: %v", key, value, err)
	}
}

// The provider keys the coordinator tests arrange and assert against.
//
// They are fixture values and nothing more: the shipped implementations declare their own keys in
// their plugin manifest (plugins/<name>/plugin.json), no non-test code in this package names one, and a test reads the key it
// needs from this list the way it reads a fixture file name. A test that needs the key of an
// implementation that does not ship declares its own.
const (
	ProviderPf       = "pf"
	ProviderSuricata = "suricata"
	ProviderInsight  = "insight"
	ProviderKea      = "kea"
	ProviderDnsmasq  = "dnsmasq"
	ProviderISC      = "isc"
	ProviderUnbound  = "unbound"
)

// MustFirewallLogSource returns a registered firewall_log implementation.
func MustFirewallLogSource(t *testing.T, key string) provider.Registration {
	t.Helper()
	source, found := plugin.Builtins().Lookup(filterlog.Kind, key)
	if !found {
		t.Fatalf("the firewall_log implementation %q is not registered", key)
	}
	return source
}

// ArrangeRecordCollection stands up a harness whose Unbound runs, so the cache and the
// local data are both active.
func ArrangeRecordCollection(t *testing.T) *ProbeHarness {
	t.Helper()
	harness := NewProbeHarness(t)
	ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(UnboundStatus, "unbound_status_running.json")
	harness.Fake.AnswerFixture(UnboundSettings, "unbound_settings.json")
	harness.Fake.AnswerFixture(UnboundIsEnabled, "unbound_is_enabled_on.json")
	harness.Fake.AnswerFixture(DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(DnsmasqSettings, "dnsmasq_settings.json")
	ctx := context.Background()
	if err := harness.Collector.ProbeKind(ctx, resolverrecords.KindCache); err != nil {
		t.Fatalf("probing the cache: %v", err)
	}
	if err := harness.Collector.ProbeKind(ctx, resolverrecords.KindLocalData); err != nil {
		t.Fatalf("probing the local data: %v", err)
	}
	for _, kind := range []string{resolverrecords.KindCache, resolverrecords.KindLocalData} {
		if keys := harness.ActiveKeysOf(t, kind); len(keys) != 1 || keys[0] != ProviderUnbound {
			t.Fatalf("the %s kind activated %v, not Unbound alone", kind, keys)
		}
	}
	return harness
}

// SetClock moves the harness's clock.
func SetClock(harness *ProbeHarness, at time.Time) {
	harness.Clock.Mutex.Lock()
	defer harness.Clock.Mutex.Unlock()
	harness.Clock.Instant = at
}

// ZeroMaxAges is a configuration whose readers' max ages are all zero: discovery's and every core kind's
// poll cadence, as the kind files declare them, so every pass of a test asks the firewall.
func ZeroMaxAges(settings config.Config) config.Config {
	settings.DiscoveryInterval = 0
	intervals := make(map[string]time.Duration, len(settings.Intervals))
	for key, interval := range settings.Intervals {
		intervals[key] = interval
	}
	for _, cadence := range provider.Cadences() {
		if cadence.Role == provider.CadencePoll {
			intervals[cadence.Setting] = 0
		}
	}
	settings.Intervals = intervals
	return settings
}

// NewTestCollectorWithSources is newTestCollector over a source set of the test's.
func NewTestCollectorWithSources(t *testing.T, client *opnsense.Client, database *store.Store, clock core.Clock,
	set *provider.Set) *collect.Collector {
	t.Helper()
	collector := collect.NewWithSources(client, database, clock, set)
	// As newTestCollector: ceilings past anything a test reaches, every reader's max age zero, and the
	// broker closed when the test ends.
	collector.Broker().Scheduler().SetSettings(schedule.Settings{MaxInFlight: 64, MaxPerSecond: 1e6})
	collector.Configure(ZeroMaxAges(config.Defaults()))
	t.Cleanup(collector.Broker().Close)
	return collector
}
