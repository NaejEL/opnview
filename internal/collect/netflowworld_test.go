package collect_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/kinds/filterlog"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// THE NETFLOW PLUGIN'S WORLD, for the collector's tests of the join (NJ4 to NJ8 of the amendment "the NetFlow
// join with every interface captured", as PA9 and PA10 of the plugin-API amendment move it into the netflow
// plugin): a fake firewall whose discovery maps interfaces to kernel indexes, the pf filter log answered row by
// row through the real filter-log parser, NetFlow v9 datagrams built field by field from RFC 3954 in the
// template ng_netflow emits (survey, Push sources, S10) delivered through the core's real decoder and router
// into the plugin's own table, and the plugin's derivation run after every pass and delivery, as its triggers
// run it. Every address is a documentation address (RFC 5737, RFC 3849) and every identifier a synthesised
// example. NOTHING HERE DECIDES ANYTHING: the join is the plugin's JavaScript, and this file only drives it.

// flowRecordKind and netflowKey are the netflow plugin's kind and provider, as its manifest declares them.
const (
	flowRecordKind = "flow_record"
	netflowKey     = "netflow"
)

// netflowWorld is the world of one NetFlow test.
type netflowWorld struct {
	*collecttest.ProbeHarness
	Hub      *push.Hub
	Key      string
	Sequence uint32
	Legs     []LegRow
	// Offered are the identifiers getconfig offers in its option lists.
	Offered []string
	// Join is the netflow plugin's derivation.
	Join provider.Derivation
	// Loaded is the plugins the world loaded on its database.
	Loaded *plugin.Loaded
	// Deferred holds the derivation back after deliveries, for a replay delivering many datagrams.
	Deferred bool
}

// gapRecorder records a push entry's gaps under its provider, as the service does.
type gapRecorder struct {
	Database  *store.Store
	Providers map[string][2]string
}

func (r gapRecorder) RecordPushGap(ctx context.Context, subscription, reason string, from, to time.Time, count int64,
	detail string) error {
	owner := r.Providers[subscription]
	providerID, err := r.Database.ProviderID(ctx, owner[0], owner[1])
	if err != nil {
		return err
	}
	return r.Database.RecordPushGap(ctx, providerID, vocab.GapReason(reason), from.Unix(), to.Unix(), detail,
		to.Unix())
}

// newNetflowHarness builds the world: discovery at CaptureStartAt, the pf filter log and the netflow provider
// active, and a hub feeding the netflow plugin's push entry.
func newNetflowHarness(t *testing.T) *netflowWorld {
	t.Helper()
	return newNetflowWorld(t, InterfacesWithIndexes(), []string{InsideIdentifier, "example_if_b", UpstreamIdentifier})
}

// newNetflowWorld builds the world of a fixture: the built-in plugins loaded on the test's database,
// interfaces_info answering the fixture's interfaces, and getconfig offering the fixture's identifiers.
func newNetflowWorld(t *testing.T, interfacesInfo map[string]any, offered []string) *netflowWorld {
	t.Helper()
	ctx := context.Background()
	fake := providertest.NewFirewall(t)
	database := collecttest.NewPluginStore(t)
	clock := collecttest.NewFixedClock(time.Unix(CaptureStartAt, 0).UTC())
	loaded, err := plugin.Load(ctx, plugin.Options{DataDir: t.TempDir(), Store: database})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Fatalf("a built-in plugin was refused: %+v", refusal)
	}
	collector := collecttest.NewTestCollectorWithSources(t, providertest.NewClient(t, fake), database, clock, loaded.Set)
	h := &netflowWorld{ProbeHarness: &collecttest.ProbeHarness{Fake: fake, Store: database, Collector: collector,
		Clock: clock}, Key: netflowKey, Offered: offered, Loaded: loaded}
	for _, derivation := range loaded.Set.Derivations() {
		if derivation.Name() == "netflow.join" {
			h.Join = derivation
		}
	}
	if h.Join == nil {
		t.Fatal("the netflow plugin declares no derivation join")
	}
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
	fake.AnswerJSON(IsEnabledEndpoint, map[string]any{"netflow": 1, "local": 0})
	if err := collector.EnsureProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if err := collector.ProbeKind(ctx, flowRecordKind); err != nil {
		t.Fatalf("probing the flow_record kind: %v", err)
	}
	if active := h.ActiveKeyOf(t, flowRecordKind); active != netflowKey {
		t.Fatalf("the flow_record kind activated %q, want %q", active, netflowKey)
	}
	fake.AnswerJSON(opnsense.FirewallLog, []any{})
	if err := collector.ProbeKind(ctx, filterlog.Kind); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}

	// The hub, with the real NetFlow decoder, routing to every NetFlow push entry of the set.
	subscriptions, err := loaded.PushSubscriptions(plugin.PushEnvironment{Store: database, Session: collector,
		Kinds: collector})
	if err != nil {
		t.Fatal(err)
	}
	providers := loaded.PushProviders()
	h.Hub = push.NewHub(push.Options{Now: clock.Now, Gaps: gapRecorder{Database: database, Providers: providers}})
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
	// As the service does (cmd/opnview, push.go): after each read of a sequence bound a push entry declares, the
	// decoders of its protocol are given the counters the exporter runs, and follow them afresh when the
	// configuration changed.
	bounds := loaded.SequenceBounds()
	collector.SetSequenceBoundObserver(func(kind, key, name string, changed bool, counters int) {
		for _, bound := range bounds {
			if bound.Kind != kind || bound.Key != key || bound.Value != name {
				continue
			}
			if changed {
				h.Hub.ResetSequences(bound.Protocol)
			}
			h.Hub.SetSequenceCounters(bound.Protocol, counters)
		}
	})
	return h
}

// At moves the world's clock to an instant.
func (h *netflowWorld) At(instant int64) {
	h.Clock.Mutex.Lock()
	h.Clock.Instant = time.Unix(instant, 0).UTC()
	h.Clock.Mutex.Unlock()
}

// Derive runs the plugin's derivation as its loop does, at the world's instant: what it writes, and the
// aggregate refresh of the connections whose measure rows changed.
func (h *netflowWorld) Derive(t *testing.T) {
	t.Helper()
	if err := h.Collector.RunDerivation(context.Background(), h.Join); err != nil {
		t.Fatalf("the netflow plugin's derivation: %v", err)
	}
}

// Capture answers getconfig with a configuration and runs the provider's pass at an instant, then the
// derivation its pass triggers.
func (h *netflowWorld) Capture(t *testing.T, at int64, interfaces ...Captured) {
	t.Helper()
	h.At(at)
	h.Fake.AnswerJSON(GetconfigEndpoint, GetconfigBodyOffering(h.Offered, interfaces...))
	if err := h.Collector.CollectDeclaredKind(context.Background(), flowRecordKind, netflowKey); err != nil {
		t.Fatalf("the capture pass: %v", err)
	}
	h.Derive(t)
}

// Logged answers the filter log with every leg logged so far plus these, newest first, and runs a filter-log
// pass at the reference instant: the legs are stored, classified and paired, and the derivation the pass
// triggers joins and measures.
func (h *netflowWorld) Logged(t *testing.T, legs ...LegRow) {
	t.Helper()
	h.LoggedAt(t, NetflowReadSecond, legs...)
}

// LoggedAt is Logged with the pass run at an instant: the filter log is then read through that instant only.
func (h *netflowWorld) LoggedAt(t *testing.T, at int64, legs ...LegRow) {
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
	h.Join.Note(provider.DerivationChange{Trigger: provider.TriggerPass, Kind: filterlog.Kind, Provider: "pf", At: at})
	h.Derive(t)
}

// Refresh runs the derivation and the refresh with nothing new, at the reference instant.
func (h *netflowWorld) Refresh(t *testing.T) {
	t.Helper()
	h.At(NetflowReadSecond)
	h.Derive(t)
	if err := h.Collector.RefreshAggregates(context.Background()); err != nil {
		t.Fatal(err)
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

// Records reads every flow record the derivation mapped, oldest first.
func (h *netflowWorld) Records(t *testing.T) []RecordRow {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT identity, start_at, ingress_role, egress_role, ingress_identifier, egress_identifier, join_state,
		        joined_flow_id, copy_flow_id, copy_kind, orientation, unmatched_reason,
		        CAST(coalesce(counted_copy, false) AS INTEGER), CAST(coalesce(loss_detectable, false) AS INTEGER),
		        octets, packets
		 FROM plugin_netflow.flow_record WHERE join_state IS NOT NULL ORDER BY start_at, octets, identity`)
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
func (h *netflowWorld) Volumes(t *testing.T) []VolumeRow {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT connection_flow_id, direction, state, bytes, packets, records, markers & 1
		 FROM connection_measure WHERE measure = 'netflow' ORDER BY connection_flow_id, direction`)
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
func (h *netflowWorld) FlowID(t *testing.T, digest string) int64 {
	t.Helper()
	return int64(collecttest.ScalarCount(t, h.Store, "SELECT id FROM flow WHERE log_digest = $1", digest))
}

// OrNull reads a nullable text.
func OrNull(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}

// JoinFingerprint is every decision and every figure of a run, legs named by their digests.
func JoinFingerprint(t *testing.T, h *netflowWorld, digests ...string) string {
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

// The datagram builder writes big-endian fields.
var _ = binary.BigEndian

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
	CaptureStartAt    = collecttest.ReferenceEpoch() - 7200
	LegInstant        = collecttest.ReferenceEpoch() - 3600
	NetflowReadSecond = collecttest.ReferenceEpoch()
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
	GetconfigEndpoint = collecttest.Declared("netflow", "getconfig")
	IsEnabledEndpoint = collecttest.Declared("netflow", "is_enabled")
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
var ExporterBootAt = collecttest.ReferenceEpoch() - 100_000

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
func (h *netflowWorld) Deliver(t *testing.T, exportAt int64, records ...FlowRecord) {
	t.Helper()
	h.Sequence++
	h.DeliverRaw(t, Datagram(exportAt, h.Sequence, records...))
}

// DeliverRaw sends one datagram as it is.
func (h *netflowWorld) DeliverRaw(t *testing.T, payload []byte) {
	t.Helper()
	ctx := context.Background()
	if err := h.Hub.Deliver(ctx, "netflow", push.TransportUDP, payload, ExporterAddress); err != nil {
		t.Fatal(err)
	}
	h.Hub.Flush(ctx)
	// The derivation the stored batch triggers maps what arrived, with the index map in force at reception.
	if !h.Deferred {
		h.Derive(t)
	}
}

// RecordGaps records the gaps the hub holds, as its timer does.
func (h *netflowWorld) RecordGaps() {
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

// singleLeg is one `in` leg of the inside client's connection to the outside server, on the inside device.
func singleLeg(digest string, at int64, protocol string, number, srcPort, dstPort int, action string) LegRow {
	return LegRow{Digest: digest, At: at, Device: InsideDevice, Dir: "in", Action: action, Src: InsideClient.String(),
		Dst: OutsideServer.String(), SrcPort: srcPort, DstPort: dstPort, Protocol: protocol, ProtocolNumber: number}
}
