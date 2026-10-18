package collect_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The retention purge's ordering against the passes: AC1 to AC4 of
// specs/SPEC-purge-serialisation.md, on the collection side.

// pageRecord is one filter-log record a test offers, and what the test knows of it.
type pageRecord struct {
	digest     string
	observedAt int64
	device     string
	src, dst   string
	version    string
	action     string
	bytes      int64
}

// body renders the record as the filter-log endpoint returns it.
func (r pageRecord) body() map[string]any {
	return map[string]any{
		"__digest__": r.digest, "__timestamp__": time.Unix(r.observedAt, 0).UTC().Format("2006-01-02T15:04:05"),
		"interface": r.device, "action": r.action, "dir": "in", "ipversion": r.version,
		"protoname": "tcp", "length": fmt.Sprint(r.bytes), "src": r.src, "dst": r.dst,
		"srcport": "40000", "dstport": "443", "reason": "match",
	}
}

// slotFigures are one slot's summable figures, as every family stores them.
type slotFigures struct {
	bytes, allowedBytes, blockedBytes, unknownBytes, allowed, blocked, unknown int64
}

func (f *slotFigures) add(action string, bytes, connections int64) {
	f.bytes += bytes
	switch action {
	case "pass":
		f.allowedBytes += bytes
		f.allowed += connections
	case "block", "reject":
		f.blockedBytes += bytes
		f.blocked += connections
	default:
		f.unknownBytes += bytes
		f.unknown += connections
	}
}

// contribution is what one flow still present, or one row of the purged part, adds to
// the slots. Both are read back here and keyed independently of the derivation's
// statements, by the rules docs/data-model.md states.
type contribution struct {
	at                         int64
	srcInterface, dstInterface sql.NullInt64
	srcClient, localClient     sql.NullInt64
	owner                      sql.NullInt64
	direction                  string
	peer                       string
	rule                       sql.NullInt64
	site                       sql.NullString
	action                     string
	bytes, connections         int64
}

// contributions reads every flow present and every row of the purged part.
func contributions(t *testing.T, database *store.Store) []contribution {
	t.Helper()
	var all []contribution
	rows, err := database.DB().Query(`SELECT f.observed_at, f.src_interface_id, f.dst_interface_id,
		f.src_client_id, f.dst_client_id, f.src_address, f.dst_address, f.direction, f.rule_id,
		a.site_name, f.action, f.packet_bytes
		FROM flow f LEFT JOIN domain_attribution a ON a.flow_id = f.id`)
	if err != nil {
		t.Fatalf("reading the flows: %v", err)
	}
	for rows.Next() {
		var (
			c                   contribution
			dstClient           sql.NullInt64
			srcAddress, dstAddr string
			loggedDirection     string
		)
		if err := rows.Scan(&c.at, &c.srcInterface, &c.dstInterface, &c.srcClient, &dstClient,
			&srcAddress, &dstAddr, &loggedDirection, &c.rule, &c.site, &c.action, &c.bytes); err != nil {
			t.Fatalf("reading the flows: %v", err)
		}
		c.connections = 1
		switch {
		case c.srcInterface.Valid && c.dstInterface.Valid:
			c.direction, c.localClient, c.peer = "inter_interface", c.srcClient, dstAddr
		case c.srcInterface.Valid:
			c.direction, c.localClient, c.peer = "outbound", c.srcClient, dstAddr
		case c.dstInterface.Valid:
			c.direction, c.localClient, c.peer = "inbound", dstClient, srcAddress
		case loggedDirection == "out":
			c.direction, c.peer = "outbound", dstAddr
		default:
			c.direction, c.peer = "inbound", srcAddress
		}
		all = append(all, c)
	}
	_ = rows.Close()

	rows, err = database.DB().Query(`SELECT hour_start_at, src_interface_id, dst_interface_id,
		src_client_id, local_client_id, traffic_direction, peer_address, rule_id, site_name, action,
		bytes, connections FROM purged_flow_hour`)
	if err != nil {
		t.Fatalf("reading the purged part: %v", err)
	}
	for rows.Next() {
		var c contribution
		if err := rows.Scan(&c.at, &c.srcInterface, &c.dstInterface, &c.srcClient, &c.localClient,
			&c.direction, &c.peer, &c.rule, &c.site, &c.action, &c.bytes, &c.connections); err != nil {
			t.Fatalf("reading the purged part: %v", err)
		}
		all = append(all, c)
	}
	_ = rows.Close()

	for index := range all {
		if !all[index].localClient.Valid {
			continue
		}
		if err := database.DB().QueryRow("SELECT owner_id FROM client WHERE id = $1",
			all[index].localClient.Int64).Scan(&all[index].owner); err != nil {
			t.Fatalf("reading the owner of client %d: %v", all[index].localClient.Int64, err)
		}
	}
	return all
}

func nullText(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return fmt.Sprint(value.Int64)
}

// expectedSlots keys every contribution as one family of one period keys it.
func expectedSlots(all []contribution, period vocab.Period, family string) map[string]slotFigures {
	sums := map[string]slotFigures{}
	for _, c := range all {
		slot := period.SlotStart(c.at)
		var key string
		switch family {
		case "volume":
			peer := c.peer
			if c.direction == "inter_interface" {
				peer = ""
			}
			key = fmt.Sprintf("%d|%s|%s|%s|%s", slot, nullText(c.srcInterface), nullText(c.dstInterface),
				peer, c.direction)
		case "rule":
			key = fmt.Sprintf("%d|%s|%s|%s", slot, nullText(c.rule), nullText(c.srcInterface),
				nullText(c.dstInterface))
		case "domain":
			if !c.site.Valid {
				continue
			}
			key = fmt.Sprintf("%d|%s|%s", slot, c.site.String, nullText(c.srcClient))
		case "client":
			if !c.localClient.Valid {
				continue
			}
			key = fmt.Sprintf("%d|%d|%s", slot, c.localClient.Int64, c.direction)
		case "peer":
			if !c.localClient.Valid {
				continue
			}
			key = fmt.Sprintf("%d|%d|%s|%s", slot, c.localClient.Int64, c.direction, c.peer)
		case "owner":
			if !c.localClient.Valid {
				continue
			}
			scope := "north_south"
			if c.direction == "inter_interface" {
				scope = "east_west"
			}
			key = fmt.Sprintf("%d|%s|%s", slot, nullText(c.owner), scope)
		}
		sum := sums[key]
		sum.add(c.action, c.bytes, c.connections)
		sums[key] = sum
	}
	return sums
}

var orderingFamilies = []string{"volume", "rule", "domain", "client", "peer", "owner"}

// storedSlots reads one family of one period back, keyed as expectedSlots keys it.
func storedSlots(t *testing.T, database *store.Store, period vocab.Period, family string) map[string]slotFigures {
	t.Helper()
	var key, table string
	switch family {
	case "volume":
		key = `period_start_at || '|' || coalesce(CAST(src_interface_id AS TEXT), 'null') || '|' ||
			coalesce(CAST(dst_interface_id AS TEXT), 'null') || '|' || coalesce(peer_address, '') || '|' || traffic_direction`
		table = "volume_aggregate_" + period.Name
	case "rule":
		key = `period_start_at || '|' || coalesce(CAST(rule_id AS TEXT), 'null') || '|' ||
			coalesce(CAST(src_interface_id AS TEXT), 'null') || '|' || coalesce(CAST(dst_interface_id AS TEXT), 'null')`
	case "domain":
		key = `period_start_at || '|' || site_name || '|' || coalesce(CAST(client_id AS TEXT), 'null')`
	case "client":
		key = `period_start_at || '|' || client_id || '|' || traffic_direction`
	case "peer":
		key = `period_start_at || '|' || client_id || '|' || traffic_direction || '|' || peer_address`
	case "owner":
		key = `period_start_at || '|' || coalesce(CAST(owner_id AS TEXT), 'null') || '|' || traffic_scope`
	}
	if table == "" {
		table = family + "_volume_aggregate_" + period.Name
	}
	rows, err := database.DB().Query("SELECT " + key + `, bytes, allowed_bytes, blocked_bytes,
		unknown_bytes, allowed_connections, blocked_connections, unknown_connections FROM ` + table)
	if err != nil {
		t.Fatalf("reading %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	stored := map[string]slotFigures{}
	for rows.Next() {
		var (
			slot string
			sum  slotFigures
		)
		if err := rows.Scan(&slot, &sum.bytes, &sum.allowedBytes, &sum.blockedBytes, &sum.unknownBytes,
			&sum.allowed, &sum.blocked, &sum.unknown); err != nil {
			t.Fatalf("reading %s: %v", table, err)
		}
		if _, duplicate := stored[slot]; duplicate {
			t.Errorf("%s holds the slot %s twice", table, slot)
		}
		stored[slot] = sum
	}
	return stored
}

// assertSlotsArePurgedPartPlusFlows checks every hour, day, week and month slot of every
// family against the purged part plus the flows present.
func assertSlotsArePurgedPartPlusFlows(t *testing.T, database *store.Store) {
	t.Helper()
	all := contributions(t, database)
	for _, period := range vocab.Periods() {
		for _, family := range orderingFamilies {
			expected := expectedSlots(all, period, family)
			stored := storedSlots(t, database, period, family)
			for key, want := range expected {
				if got := stored[key]; got != want {
					t.Errorf("the %s slot %s of the %s family holds %+v; its purged part plus the flows "+
						"present sum to %+v", period.Name, key, family, got, want)
				}
			}
			for key, got := range stored {
				if _, present := expected[key]; !present {
					t.Errorf("the %s family holds the %s slot %s (%+v), which neither its purged part nor "+
						"any flow present produces", family, period.Name, key, got)
				}
			}
		}
	}
}

// slotBytesAt sums the volume family's bytes in the slot of period holding instant.
func slotBytesAt(t *testing.T, database *store.Store, period vocab.Period, instant int64) int {
	t.Helper()
	return collecttest.ScalarCount(t, database, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_"+period.Name+
		" WHERE period_start_at = $1", period.SlotStart(instant))
}

// arrangeProbeRecord stands up a harness whose filter log offers the verifier's probe: one
// 77-byte record at now - 1000, from an address the interface networks place, under a
// retention of 600 s.
func arrangeProbeRecord(t *testing.T) (*collecttest.ProbeHarness, int64) {
	t.Helper()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	observed := collecttest.ReferenceEpoch() - 1000
	probe := pageRecord{digest: "exampledigest0000000000000000301", observedAt: observed, device: "exdev0",
		src: "198.51.100.10", dst: "203.0.113.77", version: "4", action: "pass", bytes: 77}
	// The page is the probe and nothing else, which a fixture would say nothing more about.
	harness.Fake.AnswerJSON(opnsense.FirewallLog, []any{probe.body()})
	ctx := context.Background()
	if err := harness.Collector.ProbeKind(ctx, "firewall_log"); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if err := harness.Store.SetSetting(ctx, "retention_seconds", "600", collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	return harness, observed
}

// assertTheProbeIsInEverySlot checks the probe's 77 bytes are in the hour, day, week and
// month holding it, in the purged part, and nowhere twice.
func assertTheProbeIsInEverySlot(t *testing.T, harness *collecttest.ProbeHarness, observed int64) {
	t.Helper()
	if left := collecttest.CountRows(t, harness.Store, "flow"); left != 0 {
		t.Errorf("%d flows survived the purge, which should have moved the probe", left)
	}
	if purged := collecttest.ScalarCount(t, harness.Store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"); purged != 77 {
		t.Errorf("the purged part holds %d bytes, not the probe's 77", purged)
	}
	for _, period := range vocab.Periods() {
		if got := slotBytesAt(t, harness.Store, period, observed); got != 77 {
			t.Errorf("the %s slot holding the probe holds %d bytes, not 77", period.Name, got)
		}
	}
	assertSlotsArePurgedPartPlusFlows(t, harness.Store)
}

// TestAPurgeRequestedBetweenAPassStoreAndItsRefreshWaitsForTheDerivation is AC1 at
// collector level: a filter-log pass stores the probe, and a purge is requested before
// the pass derives. The purge waits until the pass's derivation has placed the row and
// refreshed its slots, and only then moves it into the purged part; every slot of every
// family then equals its purged part plus the flows present.
func TestAPurgeRequestedBetweenAPassStoreAndItsRefreshWaitsForTheDerivation(t *testing.T) {
	t.Parallel()
	harness, observed := arrangeProbeRecord(t)
	ctx := context.Background()

	waiting := make(chan struct{})
	purged := make(chan error, 1)
	ranInsideTheWindow := false
	harness.Collector.Hooks.PurgeWaits = func() { close(waiting) }
	harness.Collector.Hooks.AfterStore = func() {
		go func() { purged <- harness.Collector.Purge(ctx) }()
		select {
		case <-waiting:
		case err := <-purged:
			// The purge did not wait: it ran between the store and the derivation.
			ranInsideTheWindow = true
			purged <- err
		}
	}
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if err := <-purged; err != nil {
		t.Fatalf("purging: %v", err)
	}
	if ranInsideTheWindow {
		t.Error("the purge ran between the pass storing its rows and the pass's derivation")
	}
	assertTheProbeIsInEverySlot(t, harness, observed)

	inside := interfaceIDOf(t, harness.Store, "example_if_a")
	if placed := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM purged_flow_hour
		WHERE src_interface_id = $1 AND local_client_id IS NOT NULL`, inside); placed != 1 {
		t.Errorf("the purged part holds the probe placed %d times, not once: the purge froze it "+
			"before the derivation placed it", placed)
	}
}

// TestWithTheOrderingOffTheNextRefreshStillFillsEverySlot is AC4: with the ordering
// disabled, the purge commits inside the window AC1 closes, and the pass's own refresh --
// the next one -- still brings every slot its purged part, from the hours the purge wrote
// alone.
func TestWithTheOrderingOffTheNextRefreshStillFillsEverySlot(t *testing.T) {
	t.Parallel()
	harness, observed := arrangeProbeRecord(t)
	ctx := context.Background()
	harness.Collector.Hooks.Unordered = true
	var purgeErr error
	harness.Collector.Hooks.AfterStore = func() { purgeErr = harness.Collector.Purge(ctx) }
	if err := harness.Collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if purgeErr != nil {
		t.Fatalf("purging: %v", purgeErr)
	}
	if unplaced := collecttest.ScalarCount(t, harness.Store, `SELECT count(*) FROM purged_flow_hour
		WHERE src_interface_id IS NULL AND dst_interface_id IS NULL`); unplaced != 1 {
		t.Fatalf("the purged part holds %d unplaced rows: the purge did not run inside the window, so "+
			"this test proves nothing about the refresh alone", unplaced)
	}
	assertTheProbeIsInEverySlot(t, harness, observed)
}

// TestAPurgeBeforeTheFirstDerivationPlacesWhatItRemoves is AC2: rows stored by a pass that
// never derived them -- an earlier run stopped between the two -- are found by a purge
// that runs before any derivation of this run. Every purged row whose address has
// membership evidence enters the purged part placed; only the row with none stays
// unplaced, which is what it is.
func TestAPurgeBeforeTheFirstDerivationPlacesWhatItRemoves(t *testing.T) {
	t.Parallel()
	earlier := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, earlier.Fake, earlier.Collector)
	ctx := context.Background()
	observed := collecttest.ReferenceEpoch() - 1000
	for index, record := range []pageRecord{
		{device: "exdev0", src: "198.51.100.10", dst: "203.0.113.77", version: "4", action: "pass", bytes: 70},
		{device: "exdev3", src: "203.0.113.78", dst: "198.51.100.11", version: "4", action: "block", bytes: 80},
		{device: "exdev0", src: "2001:db8::10", dst: "2001:db8:1::5", version: "6", action: "pass", bytes: 90},
		{device: "exdev3", src: "203.0.113.79", dst: "203.0.113.2", version: "4", action: "block", bytes: 100},
	} {
		version := int64(4)
		if record.version == "6" {
			version = 6
		}
		if err := earlier.Store.InsertFlow(ctx, vocab.Flow{
			LogDigest: fmt.Sprintf("example-unplaced-%d", index), ObservedAt: observed + int64(index),
			IngestedAt: collecttest.ReferenceEpoch() - 990, InterfaceDevice: record.device,
			InterfaceLookupState: vocab.LookupResolved, SrcAddress: record.src, DstAddress: record.dst,
			Protocol: "tcp", IPVersion: version, Action: record.action, Direction: "in",
			PacketBytes: record.bytes, RuleLookupState: vocab.LookupNotFound,
		}); err != nil {
			t.Fatalf("storing an unplaced flow: %v", err)
		}
	}
	if err := earlier.Store.SetSetting(ctx, "retention_seconds", "600", collecttest.ReferenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}

	// This run: a collector over the same database, whose first act is the purge.
	collector := collecttest.NewTestCollector(t, providertest.NewClient(t, earlier.Fake), earlier.Store, earlier.Clock)
	if err := collector.Purge(ctx); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if left := collecttest.CountRows(t, earlier.Store, "flow"); left != 0 {
		t.Fatalf("%d flows survived the purge", left)
	}
	inside := interfaceIDOf(t, earlier.Store, "example_if_a")
	var rows []string
	result, err := earlier.Store.DB().Query(`SELECT bytes, coalesce(src_interface_id, 0),
		coalesce(dst_interface_id, 0), local_client_id IS NOT NULL, traffic_direction FROM purged_flow_hour
		ORDER BY bytes`)
	if err != nil {
		t.Fatalf("reading the purged part: %v", err)
	}
	for result.Next() {
		var (
			bytes, src, dst int64
			client          bool
			direction       string
		)
		if err := result.Scan(&bytes, &src, &dst, &client, &direction); err != nil {
			t.Fatalf("reading the purged part: %v", err)
		}
		placedOn := func(id int64) string {
			if id == inside {
				return "inside"
			}
			return fmt.Sprint(id)
		}
		rows = append(rows, fmt.Sprintf("%d %s %s %t %s", bytes, placedOn(src), placedOn(dst), client, direction))
	}
	_ = result.Close()
	want := []string{
		"70 inside 0 true outbound",
		"80 0 inside true inbound",
		"90 inside 0 true outbound",
		"100 0 0 false inbound",
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("the purged part is %q, not %q: a row with membership evidence was frozen unplaced", rows, want)
	}

	if err := collector.RefreshAggregates(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	assertSlotsArePurgedPartPlusFlows(t, earlier.Store)
}

// arrangeEveryStoringSource stands up a harness whose filter log, lease backend and
// resolver are all active.
func arrangeEveryStoringSource(t *testing.T) *collecttest.ProbeHarness {
	t.Helper()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	for endpoint, fixture := range map[opnsense.Endpoint]string{
		opnsense.FirewallLog:                              "firewall_log.json",
		collecttest.Declared("kea", "status"):             "kea_status_disabled.json",
		collecttest.Declared("dnsmasq", "status"):         "dnsmasq_status_running.json",
		collecttest.Declared("dnsmasq", "settings"):       "dnsmasq_settings.json",
		collecttest.Declared("dnsmasq", "leases"):         "dnsmasq_leases.json",
		collecttest.Declared("neighbours", "arp"):         "get_arp.json",
		collecttest.Declared("neighbours", "ndp"):         "get_ndp.json",
		collecttest.Declared("unbound", "status"):         "unbound_status_running.json",
		collecttest.Declared("unbound", "settings"):       "unbound_settings.json",
		collecttest.Declared("unbound", "is_enabled"):     "unbound_is_enabled_on.json",
		collecttest.Declared("unbound", "search_queries"): "search_queries_page1.json",
	} {
		harness.Fake.AnswerFixture(endpoint, fixture)
	}
	ctx := context.Background()
	for _, kind := range []string{"firewall_log", "dhcp_lease", "dns_lookup"} {
		if err := harness.Collector.ProbeKind(ctx, kind); err != nil {
			t.Fatalf("probing: %v", err)
		}
	}
	for kind, want := range map[string]string{"firewall_log": collecttest.ProviderPf, "dhcp_lease": "dnsmasq",
		"dns_lookup": "unbound"} {
		if key := harness.ActiveKeyOf(t, kind); key != want {
			t.Fatalf("the %s kind activated %q, not %q", kind, key, want)
		}
	}
	return harness
}

// generatedRecord is the i-th record the stress test's filter log offers, observed at, its
// digest numbered from base. Four shapes, by i: a leased client to the outside, a second
// one refused on its way out, the outside to a third, and an IPv6 client to the outside.
func generatedRecord(i, base int, at int64) pageRecord {
	record := pageRecord{digest: fmt.Sprintf("exampledigest%019d", base+i), observedAt: at,
		version: "4", action: "pass", bytes: int64(60 + (i*37)%900)}
	outside := fmt.Sprintf("192.0.2.%d", i%50+1)
	switch i % 4 {
	case 0:
		record.device, record.src, record.dst = "exdev0", "198.51.100.10", outside
	case 1:
		record.device, record.src, record.dst, record.action = "exdev0", "198.51.100.11", outside, "block"
	case 2:
		record.device, record.src, record.dst = "exdev3", outside, "198.51.100.12"
	default:
		record.device, record.src, record.dst, record.version = "exdev0", "2001:db8::10",
			fmt.Sprintf("2001:db8:1::%x", i%50+1), "6"
	}
	if i%7 == 0 {
		record.action = "reject"
	}
	return record
}
