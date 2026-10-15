package collect

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The NetFlow join's test world: a fake firewall whose discovery maps two interfaces to kernel
// indexes, a filter log answered row by row, and NetFlow v9 datagrams built field by field from
// RFC 3954 in the template ng_netflow emits (survey, Push sources, S10), delivered through the core's
// real decoder and router to the flow_record provider's push entry. Every address is a documentation
// address (RFC 5737, RFC 3849) and every identifier a synthesised example.

// The two interfaces the tests capture on, as the discovery below reports them.
const (
	insideIdentifier   = "example_if_a"
	insideDevice       = "exdev0"
	insideIndex        = 3
	upstreamIdentifier = "example_if_d"
	upstreamDevice     = "exdev3"
	upstreamIndex      = 5
)

// The ends of the test connections.
var (
	exporterAddress   = netip.MustParseAddr("203.0.113.2")
	insideClient      = netip.MustParseAddr("198.51.100.10")
	outsideServer     = netip.MustParseAddr("192.0.2.80")
	translatedSource  = netip.MustParseAddr("203.0.113.2")
	insideClientV6    = netip.MustParseAddr("2001:db8::10")
	outsideServerV6   = netip.MustParseAddr("2001:db8:ffff::80")
	secondInsideHost  = netip.MustParseAddr("198.51.100.11")
	captureStartAt    = referenceEpoch() - 7200
	legInstant        = referenceEpoch() - 3600
	netflowReadSecond = referenceEpoch()
)

// interfacesWithIndexes is interfaces_info as discovery and the index map read it: the fixture's four
// interfaces, each with the `statistics.index` the details argument adds (survey, S6).
func interfacesWithIndexes() map[string]any {
	rows := []map[string]any{
		{"identifier": insideIdentifier, "device": insideDevice, "description": "example description A",
			"status": "example-link-state-up", "enabled": "1", "link_type": "example-link-type-ethernet",
			"addr4": "198.51.100.1/24", "addr6": "2001:db8::1/64", "gateways": []any{},
			"statistics": map[string]any{"index": fmt.Sprint(insideIndex)}},
		{"identifier": "example_if_b", "device": "exdev1", "description": "example description B",
			"status": "example-link-state-up", "enabled": "1", "link_type": "example-link-type-vlan", "vlan_tag": 101,
			"statistics": map[string]any{"index": "4"}},
		{"identifier": upstreamIdentifier, "device": upstreamDevice, "description": "example description D",
			"link_type": "example-link-type-tunnel", "addr4": "203.0.113.2/24", "addr6": "",
			"gateways": []any{"203.0.113.1"}, "statistics": map[string]any{"index": fmt.Sprint(upstreamIndex)}},
	}
	return map[string]any{"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows}
}

// The two endpoints the built-in netflow plugin reads its configuration from.
var (
	getconfigEndpoint = opnsense.Endpoint{Path: "/api/diagnostics/netflow/getconfig", Method: "GET"}
	isEnabledEndpoint = opnsense.NetflowIsEnabled
)

// captured is one interface a test configuration captures on.
type captured struct {
	identifier string
	egressOnly bool
}

// getconfigBody is getconfig's answer for a set of captured interfaces, in the model's shape (S9a).
func getconfigBody(interfaces ...captured) map[string]any {
	options := map[string]any{}
	egress := map[string]any{}
	for _, identifier := range []string{insideIdentifier, "example_if_b", upstreamIdentifier} {
		options[identifier] = map[string]any{"value": identifier, "selected": 0}
		egress[identifier] = map[string]any{"value": identifier, "selected": 0}
	}
	for _, one := range interfaces {
		options[one.identifier] = map[string]any{"value": one.identifier, "selected": 1}
		if one.egressOnly {
			egress[one.identifier] = map[string]any{"value": one.identifier, "selected": 1}
		}
	}
	return map[string]any{"netflow": map[string]any{
		"capture": map[string]any{"interfaces": options, "egress_only": egress,
			"version": map[string]any{"v5": map[string]any{"value": "v5", "selected": 0}, "v9": map[string]any{"value": "v9", "selected": 1}},
			"targets": map[string]any{"192.0.2.10:2055": map[string]any{"value": "192.0.2.10:2055", "selected": 1}}},
		"collect": map[string]any{"enable": "0"}, "activeTimeout": "1800", "inactiveTimeout": "15"}}
}

// netflowHarness is the world of one NetFlow test.
type netflowHarness struct {
	*probeHarness
	hub      *push.Hub
	key      string
	sequence uint32
	legs     []legRow
}

// gapRecorder records a push entry's gaps under its provider, as the service does.
type gapRecorder struct {
	t         *testing.T
	database  *store.Store
	providers map[string][2]string
}

func (r gapRecorder) RecordPushGap(ctx context.Context, subscription, reason string, from, to time.Time, count int64,
	detail string) error {
	owner := r.providers[subscription]
	providerID, err := r.database.ProviderID(ctx, owner[0], owner[1])
	if err != nil {
		return err
	}
	return r.database.RecordPushGap(ctx, providerID, vocab.GapReason(reason), from.Unix(), to.Unix(), detail,
		to.Unix())
}

// newNetflowHarness builds the world: discovery at captureStartAt, the pf filter log and the flow_record
// provider active, and a hub feeding the flow_record providers' push entries.
func newNetflowHarness(t *testing.T) *netflowHarness {
	t.Helper()
	return newNetflowHarnessWith(t, nil, "netflow", func(fake *providertest.Firewall) {
		fake.AnswerJSON(isEnabledEndpoint, map[string]any{"netflow": 1, "local": 0})
	})
}

// newNetflowHarnessWith builds the world over a source set -- nil is the built-in plugins -- whose
// flow_record provider key is key, the fake arranged by arrange so that provider is the separable one.
func newNetflowHarnessWith(t *testing.T, loaded *plugin.Loaded, key string,
	arrange func(*providertest.Firewall)) *netflowHarness {
	t.Helper()
	ctx := context.Background()
	fake := providertest.NewFirewall(t)
	database := newTestStore(t)
	clock := newFixedClock(time.Unix(captureStartAt, 0).UTC())
	if loaded == nil {
		loaded = plugin.BuiltinsLoaded()
	}
	collector := newTestCollectorWithSources(t, providertest.NewClient(t, fake), database, clock, loaded.Set)
	h := &netflowHarness{probeHarness: &probeHarness{fake: fake, store: database, collector: collector, clock: clock},
		key: key}
	fake.AnswerJSON(opnsense.InterfacesInfo, interfacesWithIndexes())
	fake.AnswerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.AnswerFixture(opnsense.SearchRule, "search_rule.json")
	// The firewall's clock reads the harness's: a firewall on UTC, so its measured offset is zero.
	fake.AnswerJSON(opnsense.SystemTime, map[string]any{"uptime": "11 days, 10:17:36",
		"datetime": time.Unix(captureStartAt, 0).UTC().Format("Mon Jan _2 15:04:05 UTC 2006"),
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
	if active := h.activeKeyOf(t, derive.KindFlowRecord); active != key {
		t.Fatalf("the flow_record kind activated %q, want %q", active, key)
	}
	fake.AnswerJSON(opnsense.FirewallLog, []any{})
	if err := collector.probeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}

	// The hub, with the real NetFlow decoder, routing to every flow_record push entry of the set.
	subscriptions, err := loaded.PushSubscriptions(plugin.PushEnvironment{Store: database, Session: collector,
		Kinds: collector})
	if err != nil {
		t.Fatal(err)
	}
	providers := loaded.PushProviders()
	h.hub = push.NewHub(push.Options{Now: clock.Now,
		Gaps: gapRecorder{t: t, database: database, providers: providers}})
	for _, subscription := range subscriptions {
		if subscription.Protocol != "netflow" {
			continue
		}
		if err := h.hub.Subscribe(subscription); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.hub.AddLoopbackListener("netflow", push.TransportUDP); err != nil {
		t.Fatal(err)
	}
	h.hub.Senders().Set([]netip.Addr{exporterAddress})
	return h
}

// newTestCollectorWithSources is newTestCollector over a source set of the test's.
func newTestCollectorWithSources(t *testing.T, client *opnsense.Client, database *store.Store, clock core.Clock,
	set *sources.Set) *Collector {
	t.Helper()
	collector := NewWithSources(client, database, clock, set)
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

// at moves the world's clock to an instant.
func (h *netflowHarness) at(instant int64) {
	h.clock.mutex.Lock()
	h.clock.now = time.Unix(instant, 0).UTC()
	h.clock.mutex.Unlock()
}

// capture answers getconfig with a configuration and runs the capture pass at an instant.
func (h *netflowHarness) capture(t *testing.T, at int64, interfaces ...captured) {
	t.Helper()
	h.at(at)
	h.fake.AnswerJSON(getconfigEndpoint, getconfigBody(interfaces...))
	if err := h.collector.CollectFlowRecordCapture(context.Background()); err != nil {
		t.Fatalf("the capture pass: %v", err)
	}
}

// flowRecord is one record of a datagram.
type flowRecord struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
	protocol         uint8
	flags            uint8
	in, out          uint16
	packets, octets  uint32
	start, end       int64
}

// datagram builds one NetFlow v9 datagram exported at exportAt, holding ng_netflow's two templates and
// the records, IPv4 and IPv6 each in its own data set.
func datagram(exportAt int64, sequence uint32, records ...flowRecord) []byte {
	const uptime = 10_000_000
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
		templateBody = u16(templateBody, uint16(256+index))
		templateBody = u16(templateBody, uint16(len(fields)))
		for _, field := range fields {
			templateBody = u16(templateBody, field[0])
			templateBody = u16(templateBody, field[1])
		}
	}
	sets := [][]byte{setOf(0, templateBody)}
	for family, template := range map[int]uint16{4: 256, 6: 257} {
		var body []byte
		for _, record := range records {
			if (family == 4) != record.src.Is4() {
				continue
			}
			source, destination := record.src.AsSlice(), record.dst.AsSlice()
			body = append(body, source...)
			body = append(body, destination...)
			body = append(body, make([]byte, len(source))...)
			body = u16(body, record.in)
			body = u16(body, record.out)
			body = u32(body, record.packets)
			body = u32(body, record.octets)
			body = u32(body, 0)
			body = u32(body, 0)
			body = u32(body, uint32(uptime-(exportAt-record.start)*1000))
			body = u32(body, uint32(uptime-(exportAt-record.end)*1000))
			body = u16(body, record.srcPort)
			body = u16(body, record.dstPort)
			body = append(body, record.flags, record.protocol, 0)
			body = u32(body, 0)
			body = u32(body, 0)
			body = append(body, 24, 24)
		}
		if len(body) > 0 {
			sets = append(sets, setOf(template, body))
		}
	}
	sort.Slice(sets[1:], func(i, j int) bool {
		return binary.BigEndian.Uint16(sets[1+i]) < binary.BigEndian.Uint16(sets[1+j])
	})
	out = u16(out, 9)
	out = u16(out, uint16(len(sets)))
	out = u32(out, uptime)
	out = u32(out, uint32(exportAt))
	out = u32(out, sequence)
	out = u32(out, 0)
	for _, set := range sets {
		out = append(out, set...)
	}
	return out
}

// setOf wraps a body in a set header, padded to four octets.
func setOf(id uint16, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	out := binary.BigEndian.AppendUint16(nil, id)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)+4))
	return append(out, body...)
}

// deliver sends one datagram, exported at exportAt with the next sequence number, through the
// decoder and the router, and stores what the entries made of it.
func (h *netflowHarness) deliver(t *testing.T, exportAt int64, records ...flowRecord) {
	t.Helper()
	h.sequence++
	h.deliverRaw(t, datagram(exportAt, h.sequence, records...))
}

// deliverRaw sends one datagram as it is.
func (h *netflowHarness) deliverRaw(t *testing.T, payload []byte) {
	t.Helper()
	ctx := context.Background()
	if err := h.hub.Deliver(ctx, "netflow", push.TransportUDP, payload, exporterAddress); err != nil {
		t.Fatal(err)
	}
	h.hub.Flush(ctx)
}

// recordGaps records the gaps the hub holds, as its timer does.
func (h *netflowHarness) recordGaps() {
	h.hub.Tick(context.Background(), true)
}

// legRow is one filter-log line, as the pf plugin reads it from the filter-log endpoint.
type legRow struct {
	digest           string
	at               int64
	device, dir      string
	action           string
	src, dst         string
	srcPort, dstPort int
	protocol         string
	protocolNumber   int
	ipID, sequence   int
}

func (l legRow) row() map[string]any {
	version := "4"
	if netip.MustParseAddr(canonical(l.src)).Is6() {
		version = "6"
	}
	row := map[string]any{"__digest__": l.digest, "__timestamp__": time.Unix(l.at, 0).UTC().Format("2006-01-02T15:04:05"),
		"interface": l.device, "action": l.action, "dir": l.dir, "ipversion": version, "protoname": l.protocol,
		"protonum": fmt.Sprint(l.protocolNumber), "length": "60", "src": l.src, "dst": l.dst, "reason": "match"}
	if l.srcPort != 0 || l.dstPort != 0 {
		row["srcport"], row["dstport"] = fmt.Sprint(l.srcPort), fmt.Sprint(l.dstPort)
	}
	if l.ipID != 0 && version == "4" {
		row["id"] = fmt.Sprint(l.ipID)
	}
	if l.sequence != 0 {
		row["seq"] = fmt.Sprint(l.sequence)
	}
	return row
}

// canonical reads an address the way a test wrote it, non-canonical text included.
func canonical(text string) string { return netip.MustParseAddr(text).String() }

// logged answers the filter log with every leg logged so far plus these, newest first, and runs a
// filter-log pass at the reference instant: the legs are stored, and the derivation that follows the
// pass classifies, pairs, joins and measures.
func (h *netflowHarness) logged(t *testing.T, legs ...legRow) {
	t.Helper()
	h.legs = append(h.legs, legs...)
	rows := make([]any, 0, len(h.legs))
	for index := len(h.legs) - 1; index >= 0; index-- {
		rows = append(rows, h.legs[index].row())
	}
	h.at(netflowReadSecond)
	h.fake.AnswerJSON(opnsense.FirewallLog, rows)
	if err := h.collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("the filter-log pass: %v", err)
	}
}

// derive runs a derivation with nothing new, at the reference instant.
func (h *netflowHarness) derive(t *testing.T) {
	t.Helper()
	h.at(netflowReadSecond)
	if err := h.collector.RefreshAggregates(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// natLegs is one outbound IPv4 TCP connection: `in` on the inside device, untranslated, and `out` on
// the upstream device with the translated source and port, paired by its identification and sequence.
func natLegs(digest string, at int64, action string) []legRow {
	return []legRow{
		{digest: digest + "1", at: at, device: insideDevice, dir: "in", action: action, src: insideClient.String(),
			dst: outsideServer.String(), srcPort: 50000, dstPort: 443, protocol: "tcp", protocolNumber: 6,
			ipID: 4242, sequence: 777},
		{digest: digest + "2", at: at, device: upstreamDevice, dir: "out", action: action, src: translatedSource.String(),
			dst: outsideServer.String(), srcPort: 61000, dstPort: 443, protocol: "tcp", protocolNumber: 6,
			ipID: 4242, sequence: 777},
	}
}

// recordRow is one stored flow record as a test reads it.
type recordRow struct {
	startAt                  int64
	captureState             string
	captured                 *int64
	point, device, direction *string
	joinState                string
	joinedFlowID             *int64
	orientation, reason      *string
	lossDetectable, octets   int64
}

// records reads every stored flow record, oldest first.
func (h *netflowHarness) records(t *testing.T) []recordRow {
	t.Helper()
	rows, err := h.store.DB().QueryContext(context.Background(),
		`SELECT start_at, capture_state, captured_interfaces, observation_point_identifier, observation_point_device,
		        direction, join_state, joined_flow_id, orientation, unmatched_reason, loss_detectable, octets
		 FROM flow_record ORDER BY start_at, octets, identity`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var read []recordRow
	for rows.Next() {
		var row recordRow
		if err := rows.Scan(&row.startAt, &row.captureState, &row.captured, &row.point, &row.device, &row.direction,
			&row.joinState, &row.joinedFlowID, &row.orientation, &row.reason, &row.lossDetectable,
			&row.octets); err != nil {
			t.Fatal(err)
		}
		read = append(read, row)
	}
	return read
}

// volumeRow is one direction of a connection's measured volume.
type volumeRow struct {
	connection     int64
	direction      string
	state          string
	bytes, packets int64
	records        int64
	leg            *string
	point          string
}

// volumes reads every measured volume row.
func (h *netflowHarness) volumes(t *testing.T) []volumeRow {
	t.Helper()
	rows, err := h.store.DB().QueryContext(context.Background(),
		`SELECT connection_flow_id, direction, state, bytes, packets, records, measured_at_leg,
		        observation_point_identifier
		 FROM connection_measured_volume ORDER BY connection_flow_id, direction`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var read []volumeRow
	for rows.Next() {
		var row volumeRow
		if err := rows.Scan(&row.connection, &row.direction, &row.state, &row.bytes, &row.packets, &row.records,
			&row.leg, &row.point); err != nil {
			t.Fatal(err)
		}
		read = append(read, row)
	}
	return read
}

// flowID reads the id of the flow logged under a digest.
func (h *netflowHarness) flowID(t *testing.T, digest string) int64 {
	t.Helper()
	return int64(scalarCount(t, h.store, "SELECT id FROM flow WHERE log_digest = ?", digest))
}

// orNull reads a nullable text.
func orNull(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}
