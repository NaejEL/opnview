package collecttest

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push"

	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/collect/sources"
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
	collector := collect.New(client, database, clock)
	collector.Broker().Scheduler().SetSettings(schedule.Settings{MaxInFlight: 64, MaxPerSecond: 1e6})
	// A test runs its passes back to back at one frozen instant, where a pass at its interval would
	// find the answer it read an instant before fresh in the mirror: its readers' max ages are zero,
	// so every pass asks the firewall, as each test arranges it. The broker's serving from its mirror
	// is internal/broker's tests' subject.
	settings := config.Defaults()
	settings.DiscoveryInterval, settings.FirewallLogInterval, settings.SecurityEventInterval = 0, 0, 0
	settings.MeasurementInterval, settings.DHCPLeaseInterval, settings.DNSLookupInterval = 0, 0, 0
	settings.ResolverCacheInterval, settings.ResolverLocalDataInterval = 0, 0
	collector.Configure(settings)
	t.Cleanup(collector.Broker().Close)
	return collector
}

// ArrangeLeaseCollection stands up a harness whose Dnsmasq backend is the active one, which
// is what the surveyed firewall reported: Kea disabled, Dnsmasq running.
func ArrangeLeaseCollection(t *testing.T) *ProbeHarness {
	t.Helper()
	harness := NewProbeHarness(t)
	ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(opnsense.KeaStatus, "kea_status_disabled.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqLeases, "dnsmasq_leases.json")
	harness.Fake.AnswerFixture(opnsense.ARPTable, "get_arp.json")
	harness.Fake.AnswerFixture(opnsense.NDPTable, "get_ndp.json")
	if err := harness.Collector.ProbeDHCPLease(context.Background()); err != nil {
		t.Fatalf("probing the lease backends: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindDHCPLease); key != ProviderDnsmasq {
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
	if err := harness.Collector.ProbeFirewallLog(context.Background()); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if key := harness.ActiveKeyOf(t, core.KindFirewallLog); key != ProviderPf {
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

// The two interfaces the tests capture on, as the discovery below reports them.
const (
	InsideIdentifier   = "example_if_a"
	InsideDevice       = "exdev0"
	InsideIndex        = 23
	UpstreamIdentifier = "example_if_d"
	UpstreamDevice     = "exdev3"
	UpstreamIndex      = 25
	// LoopbackIndex is the loopback's kernel index, which the row carrying the `loopback` flag holds.
	LoopbackIndex = 27
)

// The ends of the test connections.
var (
	ExporterAddress   = netip.MustParseAddr("203.0.113.2")
	InsideClient      = netip.MustParseAddr("198.51.100.10")
	OutsideServer     = netip.MustParseAddr("192.0.2.80")
	TranslatedSource  = netip.MustParseAddr("203.0.113.2")
	InsideClientV6    = netip.MustParseAddr("2001:db8::10")
	OutsideServerV6   = netip.MustParseAddr("2001:db8:ffff::80")
	SecondInsideHost  = netip.MustParseAddr("198.51.100.11")
	CaptureStartAt    = ReferenceEpoch() - 7200
	LegInstant        = ReferenceEpoch() - 3600
	NetflowReadSecond = ReferenceEpoch()
)

// InterfacesWithIndexes is interfaces_info as discovery, the index map and the netflow plugin read it: the
// fixture's three assigned interfaces, each with the `statistics.index` the details argument adds (survey,
// S6) and the `macaddr` of an interface ifconfig shows an `ether` line for (S5c), and the loopback, which no
// assignment names and whose `flags` hold `loopback` (S6b).
func InterfacesWithIndexes() map[string]any {
	rows := []map[string]any{
		{"identifier": InsideIdentifier, "device": InsideDevice, "description": "example description A",
			"status": "example-link-state-up", "enabled": "1", "link_type": "example-link-type-ethernet",
			"addr4": "198.51.100.1/24", "addr6": "2001:db8::1/64", "gateways": []any{},
			"macaddr": "02:00:5e:00:00:21", "statistics": map[string]any{"index": fmt.Sprint(InsideIndex)}},
		{"identifier": "example_if_b", "device": "exdev1", "description": "example description B",
			"status": "example-link-state-up", "enabled": "1", "link_type": "example-link-type-vlan", "vlan_tag": 101,
			"macaddr": "02:00:5e:00:00:24", "statistics": map[string]any{"index": "24"}},
		{"identifier": UpstreamIdentifier, "device": UpstreamDevice, "description": "example description D",
			"link_type": "example-link-type-tunnel", "addr4": "203.0.113.2/24", "addr6": "",
			"gateways": []any{"203.0.113.1"}, "macaddr": "02:00:5e:00:00:25",
			"statistics": map[string]any{"index": fmt.Sprint(UpstreamIndex)}},
		{"identifier": "", "device": "exlo0", "description": "Unassigned Interface", "flags": []any{"up", "loopback",
			"running", "multicast"}, "statistics": map[string]any{"index": fmt.Sprint(LoopbackIndex)}},
	}
	return map[string]any{"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows}
}

// The two endpoints the built-in netflow plugin reads its configuration from.
var (
	GetconfigEndpoint = opnsense.Endpoint{Path: "/api/diagnostics/netflow/getconfig", Method: "GET"}
	IsEnabledEndpoint = opnsense.NetflowIsEnabled
)

// Captured is one interface a test configuration captures on.
type Captured struct {
	Identifier string
	EgressOnly bool
}

// GetconfigBodyOffering is GetconfigBody for a firewall offering some identifiers.
func GetconfigBodyOffering(offered []string, interfaces ...Captured) map[string]any {
	options := map[string]any{}
	egress := map[string]any{}
	for _, identifier := range offered {
		options[identifier] = map[string]any{"value": identifier, "selected": 0}
		egress[identifier] = map[string]any{"value": identifier, "selected": 0}
	}
	for _, one := range interfaces {
		options[one.Identifier] = map[string]any{"value": one.Identifier, "selected": 1}
		if one.EgressOnly {
			egress[one.Identifier] = map[string]any{"value": one.Identifier, "selected": 1}
		}
	}
	return map[string]any{"netflow": map[string]any{
		"capture": map[string]any{"interfaces": options, "egress_only": egress,
			"version": map[string]any{"v5": map[string]any{"value": "v5", "selected": 0}, "v9": map[string]any{"value": "v9", "selected": 1}},
			"targets": map[string]any{"192.0.2.10:2055": map[string]any{"value": "192.0.2.10:2055", "selected": 1}}},
		"collect": map[string]any{"enable": "0"}, "activeTimeout": "1800", "inactiveTimeout": "15"}}
}

// NetflowHarness is the world of one NetFlow test.
type NetflowHarness struct {
	*ProbeHarness
	Hub      *push.Hub
	Key      string
	Sequence uint32
	Legs     []LegRow
	// Offered are the identifiers getconfig offers in its option lists.
	Offered []string
}

// GapRecorder records a push entry's gaps under its provider, as the service does.
type GapRecorder struct {
	T         *testing.T
	Database  *store.Store
	Providers map[string][2]string
}

func (r GapRecorder) RecordPushGap(ctx context.Context, subscription, reason string, from, to time.Time, count int64,
	detail string) error {
	owner := r.Providers[subscription]
	providerID, err := r.Database.ProviderID(ctx, owner[0], owner[1])
	if err != nil {
		return err
	}
	return r.Database.RecordPushGap(ctx, providerID, vocab.GapReason(reason), from.Unix(), to.Unix(), detail,
		to.Unix())
}

// NewNetflowHarness builds the world: discovery at captureStartAt, the pf filter log and the flow_record
// provider active, and a hub feeding the flow_record providers' push entries.
func NewNetflowHarness(t *testing.T) *NetflowHarness {
	t.Helper()
	return NewNetflowHarnessWith(t, nil, "netflow", func(fake *providertest.Firewall) {
		fake.AnswerJSON(IsEnabledEndpoint, map[string]any{"netflow": 1, "local": 0})
	})
}

// NewNetflowHarnessWith builds the world over a source set -- nil is the built-in plugins -- whose
// flow_record provider key is key, the fake arranged by arrange so that provider is the separable one.
func NewNetflowHarnessWith(t *testing.T, loaded *plugin.Loaded, key string,
	arrange func(*providertest.Firewall)) *NetflowHarness {
	t.Helper()
	return newNetflowHarness(t, loaded, key, arrange, InterfacesWithIndexes(),
		[]string{InsideIdentifier, "example_if_b", UpstreamIdentifier})
}

// NewNetflowWorldHarness builds the world of a fixture: the built-in plugins, interfaces_info answering
// the fixture's interfaces, and getconfig offering the fixture's identifiers.
func NewNetflowWorldHarness(t *testing.T, interfacesInfo map[string]any, offered []string) *NetflowHarness {
	t.Helper()
	return newNetflowHarness(t, nil, "netflow", func(fake *providertest.Firewall) {
		fake.AnswerJSON(IsEnabledEndpoint, map[string]any{"netflow": 1, "local": 0})
	}, interfacesInfo, offered)
}

func newNetflowHarness(t *testing.T, loaded *plugin.Loaded, key string, arrange func(*providertest.Firewall),
	interfacesInfo map[string]any, offered []string) *NetflowHarness {
	t.Helper()
	ctx := context.Background()
	fake := providertest.NewFirewall(t)
	database := NewTestStore(t)
	clock := NewFixedClock(time.Unix(CaptureStartAt, 0).UTC())
	if loaded == nil {
		loaded = plugin.BuiltinsLoaded()
	}
	collector := NewTestCollectorWithSources(t, providertest.NewClient(t, fake), database, clock, loaded.Set)
	h := &NetflowHarness{ProbeHarness: &ProbeHarness{Fake: fake, Store: database, Collector: collector, Clock: clock},
		Key: key, Offered: offered}
	fake.AnswerJSON(opnsense.InterfacesInfo, interfacesInfo)
	fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
	// The firewall's clock reads the harness's: a firewall on UTC, so its measured offset is zero.
	fake.AnswerJSON(opnsense.SystemTime, map[string]any{"uptime": "11 days, 10:17:36",
		"datetime": time.Unix(CaptureStartAt, 0).UTC().Format("Mon Jan _2 15:04:05 UTC 2006"),
		"boottime": "Tue Sep 15 13:42:24 UTC 2026", "loadavg": "0.42, 0.31, 0.25"})
	if err := collector.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
	if err := collector.DiscoverClock(ctx); err != nil {
		t.Fatal(err)
	}
	arrange(fake)
	if err := collector.EnsureProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if err := collector.ProbeFlowRecord(ctx); err != nil {
		t.Fatalf("probing the flow_record kind: %v", err)
	}
	if active := h.ActiveKeyOf(t, derive.KindFlowRecord); active != key {
		t.Fatalf("the flow_record kind activated %q, want %q", active, key)
	}
	fake.AnswerJSON(opnsense.FirewallLog, []any{})
	if err := collector.ProbeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}

	// The hub, with the real NetFlow decoder, routing to every flow_record push entry of the set.
	subscriptions, err := loaded.PushSubscriptions(plugin.PushEnvironment{Store: database, Session: collector,
		Kinds: collector})
	if err != nil {
		t.Fatal(err)
	}
	providers := loaded.PushProviders()
	h.Hub = push.NewHub(push.Options{Now: clock.Now,
		Gaps: GapRecorder{T: t, Database: database, Providers: providers}})
	for _, subscription := range subscriptions {
		if subscription.Protocol != "netflow" {
			continue
		}
		if err := h.Hub.Subscribe(subscription); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Hub.AddLoopbackListener("netflow", push.TransportUDP); err != nil {
		t.Fatal(err)
	}
	h.Hub.Senders().Set([]netip.Addr{ExporterAddress})
	// As the service does (cmd/opnview, push.go): after each capture read the decoders are given the
	// sequence counters the exporter runs, and follow them afresh when the configuration changed.
	collector.SetCaptureReadObserver(func(_ string, changed bool, counters int) {
		if changed {
			h.Hub.ResetSequences("netflow")
		}
		h.Hub.SetSequenceCounters("netflow", counters)
	})
	return h
}

// NewTestCollectorWithSources is newTestCollector over a source set of the test's.
func NewTestCollectorWithSources(t *testing.T, client *opnsense.Client, database *store.Store, clock core.Clock,
	set *sources.Set) *collect.Collector {
	t.Helper()
	collector := collect.NewWithSources(client, database, clock, set)
	// As newTestCollector: ceilings past anything a test reaches, every reader's max age zero, and the
	// broker closed when the test ends.
	collector.Broker().Scheduler().SetSettings(schedule.Settings{MaxInFlight: 64, MaxPerSecond: 1e6})
	settings := config.Defaults()
	settings.DiscoveryInterval, settings.FirewallLogInterval, settings.SecurityEventInterval = 0, 0, 0
	settings.MeasurementInterval, settings.DHCPLeaseInterval, settings.DNSLookupInterval = 0, 0, 0
	settings.ResolverCacheInterval, settings.ResolverLocalDataInterval, settings.FlowRecordInterval = 0, 0, 0
	collector.Configure(settings)
	t.Cleanup(collector.Broker().Close)
	return collector
}

// At moves the world's clock to an instant.
func (h *NetflowHarness) At(instant int64) {
	h.Clock.Mutex.Lock()
	h.Clock.Instant = time.Unix(instant, 0).UTC()
	h.Clock.Mutex.Unlock()
}

// Capture answers getconfig with a configuration and runs the capture pass at an instant.
func (h *NetflowHarness) Capture(t *testing.T, at int64, interfaces ...Captured) {
	t.Helper()
	h.At(at)
	h.Fake.AnswerJSON(GetconfigEndpoint, GetconfigBodyOffering(h.Offered, interfaces...))
	if err := h.Collector.CollectFlowRecordCapture(context.Background()); err != nil {
		t.Fatalf("the capture pass: %v", err)
	}
}

// FlowRecord is one record of a datagram.
type FlowRecord struct {
	Src, Dst         netip.Addr
	SrcPort, DstPort uint16
	Protocol         uint8
	Flags            uint8
	In, Out          uint16
	Packets, Octets  uint32
	Start, End       int64
}

// Datagram builds one NetFlow v9 datagram exported at exportAt, source_id 0, holding ng_netflow's two
// templates -- 256 for IPv4 and 259 for IPv6, with the analysis's Q1 field list -- and the records, IPv4
// and IPv6 each in its own data set.
func Datagram(exportAt int64, sequence uint32, records ...FlowRecord) []byte {
	return DatagramBooted(exportAt, sequence, ExporterBootAt, records...)
}

// ExporterBootAt is when the test exporter booted: its uptime in a datagram exported at an instant is the
// time since.
var ExporterBootAt = ReferenceEpoch() - 100_000

// DatagramBooted is Datagram from an exporter booted at bootAt: a later boot than an earlier datagram's is
// a restart, its uptime going backwards.
func DatagramBooted(exportAt int64, sequence uint32, bootAt int64, records ...FlowRecord) []byte {
	uptime := (exportAt - bootAt) * 1000
	var out []byte
	u16 := func(b []byte, v uint16) []byte { return binary.BigEndian.AppendUint16(b, v) }
	u32 := func(b []byte, v uint32) []byte { return binary.BigEndian.AppendUint32(b, v) }
	tail := [][2]uint16{{10, 2}, {14, 2}, {2, 4}, {1, 4}, {24, 4}, {23, 4}, {22, 4}, {21, 4}, {7, 2}, {11, 2},
		{6, 1}, {4, 1}, {5, 1}, {16, 4}, {17, 4}, {9, 1}, {13, 1}}
	templates := [][][2]uint16{
		append([][2]uint16{{8, 4}, {12, 4}, {15, 4}}, tail...),
		append([][2]uint16{{27, 16}, {28, 16}, {62, 16}}, tail...),
	}
	var templateBody []byte
	for index, fields := range templates {
		templateBody = u16(templateBody, []uint16{256, 259}[index])
		templateBody = u16(templateBody, uint16(len(fields)))
		for _, field := range fields {
			templateBody = u16(templateBody, field[0])
			templateBody = u16(templateBody, field[1])
		}
	}
	sets := [][]byte{SetOf(0, templateBody)}
	for family, template := range map[int]uint16{4: 256, 6: 259} {
		var body []byte
		for _, record := range records {
			if (family == 4) != record.Src.Is4() {
				continue
			}
			source, destination := record.Src.AsSlice(), record.Dst.AsSlice()
			body = append(body, source...)
			body = append(body, destination...)
			body = append(body, make([]byte, len(source))...)
			body = u16(body, record.In)
			body = u16(body, record.Out)
			body = u32(body, record.Packets)
			body = u32(body, record.Octets)
			body = u32(body, 0)
			body = u32(body, 0)
			body = u32(body, uint32(uptime-(exportAt-record.Start)*1000))
			body = u32(body, uint32(uptime-(exportAt-record.End)*1000))
			body = u16(body, record.SrcPort)
			body = u16(body, record.DstPort)
			body = append(body, record.Flags, record.Protocol, 0)
			body = u32(body, 0)
			body = u32(body, 0)
			body = append(body, 24, 24)
		}
		if len(body) > 0 {
			sets = append(sets, SetOf(template, body))
		}
	}
	sort.Slice(sets[1:], func(i, j int) bool {
		return binary.BigEndian.Uint16(sets[1+i]) < binary.BigEndian.Uint16(sets[1+j])
	})
	out = u16(out, 9)
	out = u16(out, uint16(len(sets)))
	out = u32(out, uint32(uptime))
	out = u32(out, uint32(exportAt))
	out = u32(out, sequence)
	out = u32(out, 0)
	for _, set := range sets {
		out = append(out, set...)
	}
	return out
}

// SetOf wraps a body in a set header, padded to four octets.
func SetOf(id uint16, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	out := binary.BigEndian.AppendUint16(nil, id)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)+4))
	return append(out, body...)
}

// Deliver sends one datagram, exported at exportAt with the next sequence number, through the
// decoder and the router, and stores what the entries made of it.
func (h *NetflowHarness) Deliver(t *testing.T, exportAt int64, records ...FlowRecord) {
	t.Helper()
	h.Sequence++
	h.DeliverRaw(t, Datagram(exportAt, h.Sequence, records...))
}

// DeliverRaw sends one datagram as it is.
func (h *NetflowHarness) DeliverRaw(t *testing.T, payload []byte) {
	t.Helper()
	ctx := context.Background()
	if err := h.Hub.Deliver(ctx, "netflow", push.TransportUDP, payload, ExporterAddress); err != nil {
		t.Fatal(err)
	}
	h.Hub.Flush(ctx)
}

// RecordGaps records the gaps the hub holds, as its timer does.
func (h *NetflowHarness) RecordGaps() {
	h.Hub.Tick(context.Background(), true)
}

// LegRow is one filter-log line, as the pf plugin reads it from the filter-log endpoint.
type LegRow struct {
	Digest           string
	At               int64
	Device, Dir      string
	Action           string
	Src, Dst         string
	SrcPort, DstPort int
	Protocol         string
	ProtocolNumber   int
	IPID, Sequence   int
}

func (l LegRow) Row() map[string]any {
	version := "4"
	if netip.MustParseAddr(Canonical(l.Src)).Is6() {
		version = "6"
	}
	row := map[string]any{"__digest__": l.Digest, "__timestamp__": time.Unix(l.At, 0).UTC().Format("2006-01-02T15:04:05"),
		"interface": l.Device, "action": l.Action, "dir": l.Dir, "ipversion": version, "protoname": l.Protocol,
		"protonum": fmt.Sprint(l.ProtocolNumber), "length": "60", "src": l.Src, "dst": l.Dst, "reason": "match"}
	if l.SrcPort != 0 || l.DstPort != 0 {
		row["srcport"], row["dstport"] = fmt.Sprint(l.SrcPort), fmt.Sprint(l.DstPort)
	}
	if l.IPID != 0 && version == "4" {
		row["id"] = fmt.Sprint(l.IPID)
	}
	if l.Sequence != 0 {
		row["seq"] = fmt.Sprint(l.Sequence)
	}
	return row
}

// Canonical reads an address the way a test wrote it, non-canonical text included.
func Canonical(text string) string { return netip.MustParseAddr(text).String() }

// Logged answers the filter log with every leg logged so far plus these, newest first, and runs a
// filter-log pass at the reference instant: the legs are stored, and the derivation that follows the
// pass classifies, pairs, joins and measures.
func (h *NetflowHarness) Logged(t *testing.T, legs ...LegRow) {
	t.Helper()
	h.LoggedAt(t, NetflowReadSecond, legs...)
}

// LoggedAt is Logged with the pass run at an instant: the filter log is then read through that instant
// only.
func (h *NetflowHarness) LoggedAt(t *testing.T, at int64, legs ...LegRow) {
	t.Helper()
	h.Legs = append(h.Legs, legs...)
	rows := make([]any, 0, len(h.Legs))
	for index := len(h.Legs) - 1; index >= 0; index-- {
		rows = append(rows, h.Legs[index].Row())
	}
	h.At(at)
	h.Fake.AnswerJSON(opnsense.FirewallLog, rows)
	if err := h.Collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("the filter-log pass: %v", err)
	}
}

// Derive runs a derivation with nothing new, at the reference instant.
func (h *NetflowHarness) Derive(t *testing.T) {
	t.Helper()
	h.At(NetflowReadSecond)
	if err := h.Collector.RefreshAggregates(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// NatLegs is one outbound IPv4 TCP connection: `in` on the inside device, untranslated, and `out` on
// the upstream device with the translated source and port, paired by its identification and sequence.
func NatLegs(digest string, at int64, action string) []LegRow {
	return []LegRow{
		{Digest: digest + "1", At: at, Device: InsideDevice, Dir: "in", Action: action, Src: InsideClient.String(),
			Dst: OutsideServer.String(), SrcPort: 50000, DstPort: 443, Protocol: "tcp", ProtocolNumber: 6,
			IPID: 4242, Sequence: 777},
		{Digest: digest + "2", At: at, Device: UpstreamDevice, Dir: "out", Action: action, Src: TranslatedSource.String(),
			Dst: OutsideServer.String(), SrcPort: 61000, DstPort: 443, Protocol: "tcp", ProtocolNumber: 6,
			IPID: 4242, Sequence: 777},
	}
}

// RecordRow is one stored flow record as a test reads it.
type RecordRow struct {
	Identity                 string
	StartAt                  int64
	IngressRole, EgressRole  string
	IngressIdentifier        *string
	EgressIdentifier         *string
	JoinState                string
	JoinedFlowID, CopyFlowID *int64
	CopyKind                 *string
	Orientation, Reason      *string
	CountedCopy              int64
	LossDetectable, Octets   int64
	Packets                  int64
}

// Records reads every stored flow record, oldest first.
func (h *NetflowHarness) Records(t *testing.T) []RecordRow {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT identity, start_at, ingress_role, egress_role, ingress_identifier, egress_identifier, join_state,
		        joined_flow_id, copy_flow_id, copy_kind, orientation, unmatched_reason, CAST(counted_copy AS INTEGER), CAST(loss_detectable AS INTEGER),
		        octets, packets
		 FROM flow_record ORDER BY start_at, octets, identity`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var read []RecordRow
	for rows.Next() {
		var row RecordRow
		if err := rows.Scan(&row.Identity, &row.StartAt, &row.IngressRole, &row.EgressRole, &row.IngressIdentifier,
			&row.EgressIdentifier, &row.JoinState, &row.JoinedFlowID, &row.CopyFlowID, &row.CopyKind, &row.Orientation,
			&row.Reason, &row.CountedCopy, &row.LossDetectable, &row.Octets, &row.Packets); err != nil {
			t.Fatal(err)
		}
		read = append(read, row)
	}
	return read
}

// VolumeRow is one direction of a connection's measured volume.
type VolumeRow struct {
	Connection     int64
	Direction      string
	State          string
	Bytes, Packets int64
	Records        int64
	CountedCopy    int64
}

// Volumes reads every measured volume row.
func (h *NetflowHarness) Volumes(t *testing.T) []VolumeRow {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT connection_flow_id, direction, state, bytes, packets, records, CAST(counted_copy AS INTEGER)
		 FROM connection_measured_volume ORDER BY connection_flow_id, direction`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var read []VolumeRow
	for rows.Next() {
		var row VolumeRow
		if err := rows.Scan(&row.Connection, &row.Direction, &row.State, &row.Bytes, &row.Packets, &row.Records,
			&row.CountedCopy); err != nil {
			t.Fatal(err)
		}
		read = append(read, row)
	}
	return read
}

// FlowID reads the id of the flow logged under a digest.
func (h *NetflowHarness) FlowID(t *testing.T, digest string) int64 {
	t.Helper()
	return int64(ScalarCount(t, h.Store, "SELECT id FROM flow WHERE log_digest = $1", digest))
}

// OrNull reads a nullable text.
func OrNull(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}

// NatRecords are the four records the NAT connection produces at the two devices: its forward packets
// received inside and sent upstream translated, and its replies received upstream to the translated
// destination and sent inside. Each carries FIN, so its state closes within pf's closing timeout.
func NatRecords() (insideForward, insideReverse, upstreamForward, upstreamReverse FlowRecord) {
	start, end := LegInstant, LegInstant+10
	insideForward = FlowRecord{Src: InsideClient, Dst: OutsideServer, SrcPort: 50000, DstPort: 443, Protocol: 6,
		Flags: 0x1b, In: InsideIndex, Out: UpstreamIndex, Packets: 10, Octets: 1000, Start: start, End: end}
	insideReverse = FlowRecord{Src: OutsideServer, Dst: InsideClient, SrcPort: 443, DstPort: 50000, Protocol: 6,
		Flags: 0x1b, In: UpstreamIndex, Out: InsideIndex, Packets: 20, Octets: 20000, Start: start, End: end}
	upstreamForward = FlowRecord{Src: TranslatedSource, Dst: OutsideServer, SrcPort: 61000, DstPort: 443, Protocol: 6,
		Flags: 0x1b, In: InsideIndex, Out: UpstreamIndex, Packets: 10, Octets: 1000, Start: start, End: end}
	upstreamReverse = FlowRecord{Src: OutsideServer, Dst: TranslatedSource, SrcPort: 443, DstPort: 61000, Protocol: 6,
		Flags: 0x1b, In: UpstreamIndex, Out: InsideIndex, Packets: 20, Octets: 20000, Start: start, End: end}
	return
}

// Forward is one record of the inside client's connection received on the inside device; reverse one
// of its replies sent there.
func Forward(protocol uint8, srcPort, dstPort uint16, flags uint8, octets uint32, start, end int64) FlowRecord {
	return FlowRecord{Src: InsideClient, Dst: OutsideServer, SrcPort: srcPort, DstPort: dstPort, Protocol: protocol,
		Flags: flags, In: InsideIndex, Out: UpstreamIndex, Packets: 1, Octets: octets, Start: start, End: end}
}

// JoinFingerprint is every decision and every figure of a run, legs named by their digests.
func JoinFingerprint(t *testing.T, h *NetflowHarness, digests ...string) string {
	t.Helper()
	names := map[int64]string{}
	for _, digest := range digests {
		names[h.FlowID(t, digest)] = digest
	}
	leg := func(id *int64) string {
		if id == nil {
			return "-"
		}
		return names[*id]
	}
	// Each part is sorted: the order of rows alike in what is printed depends on their identities, which are
	// the datagrams' digests and differ from one delivery order to another.
	var records, volumes []string
	for _, record := range h.Records(t) {
		records = append(records, fmt.Sprintf("%d %d %s %s %s %s %s %s %s %d;", record.StartAt, record.Octets,
			record.IngressRole, record.JoinState, leg(record.JoinedFlowID), OrNull(record.CopyKind), leg(record.CopyFlowID),
			OrNull(record.Orientation), OrNull(record.Reason), record.CountedCopy))
	}
	for _, volume := range h.Volumes(t) {
		volumes = append(volumes, fmt.Sprintf("%s %s %s %d %d %d %d;", names[volume.Connection], volume.Direction,
			volume.State, volume.Bytes, volume.Packets, volume.Records, volume.CountedCopy))
	}
	sort.Strings(records)
	sort.Strings(volumes)
	return strings.Join(records, "") + strings.Join(volumes, "")
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
	provider, err := harness.Store.ProviderID(ctx, core.KindDHCPLease, ProviderDnsmasq)
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
	id, err := harness.Store.ProviderID(context.Background(), core.KindDHCPLease, key)
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
func MustFirewallLogSource(t *testing.T, key string) firewalllog.Source {
	t.Helper()
	source, found := plugin.Builtins().FirewallLog.Lookup(key)
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
	harness.Fake.AnswerFixture(opnsense.UnboundStatus, "unbound_status_running.json")
	harness.Fake.AnswerFixture(opnsense.UnboundSettings, "unbound_settings.json")
	harness.Fake.AnswerFixture(opnsense.UnboundIsEnabled, "unbound_is_enabled_on.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqStatus, "dnsmasq_status_running.json")
	harness.Fake.AnswerFixture(opnsense.DnsmasqSettings, "dnsmasq_settings.json")
	ctx := context.Background()
	if err := harness.Collector.ProbeResolverCache(ctx); err != nil {
		t.Fatalf("probing the cache: %v", err)
	}
	if err := harness.Collector.ProbeResolverLocalData(ctx); err != nil {
		t.Fatalf("probing the local data: %v", err)
	}
	for _, kind := range []string{core.KindResolverCache, core.KindResolverLocalData} {
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
