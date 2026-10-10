package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
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
		if err := database.DB().QueryRow("SELECT owner_id FROM client WHERE id = ?",
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
func expectedSlots(all []contribution, period store.Period, family string) map[string]slotFigures {
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
func storedSlots(t *testing.T, database *store.Store, period store.Period, family string) map[string]slotFigures {
	t.Helper()
	var key, table string
	switch family {
	case "volume":
		key = `period_start_at || '|' || ifnull(src_interface_id, 'null') || '|' ||
			ifnull(dst_interface_id, 'null') || '|' || ifnull(peer_address, '') || '|' || traffic_direction`
		table = "volume_aggregate_" + period.Name
	case "rule":
		key = `period_start_at || '|' || ifnull(rule_id, 'null') || '|' ||
			ifnull(src_interface_id, 'null') || '|' || ifnull(dst_interface_id, 'null')`
	case "domain":
		key = `period_start_at || '|' || site_name || '|' || ifnull(client_id, 'null')`
	case "client":
		key = `period_start_at || '|' || client_id || '|' || traffic_direction`
	case "peer":
		key = `period_start_at || '|' || client_id || '|' || traffic_direction || '|' || peer_address`
	case "owner":
		key = `period_start_at || '|' || ifnull(owner_id, 'null') || '|' || traffic_scope`
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
	for _, period := range store.Periods() {
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
func slotBytesAt(t *testing.T, database *store.Store, period store.Period, instant int64) int {
	t.Helper()
	return scalarCount(t, database, "SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_"+period.Name+
		" WHERE period_start_at = ?", period.SlotStart(instant))
}

// arrangeProbeRecord stands up a harness whose filter log offers the verifier's probe: one
// 77-byte record at now - 1000, from an address the interface networks place, under a
// retention of 600 s.
func arrangeProbeRecord(t *testing.T) (*probeHarness, int64) {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	observed := referenceEpoch() - 1000
	probe := pageRecord{digest: "exampledigest0000000000000000301", observedAt: observed, device: "exdev0",
		src: "198.51.100.10", dst: "203.0.113.77", version: "4", action: "pass", bytes: 77}
	// The page is the probe and nothing else, which a fixture would say nothing more about.
	harness.fake.answerJSON(opnsense.FirewallLog, []any{probe.body()})
	ctx := context.Background()
	if err := harness.collector.probeFirewallLog(ctx); err != nil {
		t.Fatalf("probing the filter log: %v", err)
	}
	if err := harness.store.SetSetting(ctx, "retention_seconds", "600", referenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	return harness, observed
}

// assertTheProbeIsInEverySlot checks the probe's 77 bytes are in the hour, day, week and
// month holding it, in the purged part, and nowhere twice.
func assertTheProbeIsInEverySlot(t *testing.T, harness *probeHarness, observed int64) {
	t.Helper()
	if left := countRows(t, harness.store, "flow"); left != 0 {
		t.Errorf("%d flows survived the purge, which should have moved the probe", left)
	}
	if purged := scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"); purged != 77 {
		t.Errorf("the purged part holds %d bytes, not the probe's 77", purged)
	}
	for _, period := range store.Periods() {
		if got := slotBytesAt(t, harness.store, period, observed); got != 77 {
			t.Errorf("the %s slot holding the probe holds %d bytes, not 77", period.Name, got)
		}
	}
	assertSlotsArePurgedPartPlusFlows(t, harness.store)
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
	harness.collector.hooks.purgeWaits = func() { close(waiting) }
	harness.collector.hooks.afterStore = func() {
		go func() { purged <- harness.collector.Purge(ctx) }()
		select {
		case <-waiting:
		case err := <-purged:
			// The purge did not wait: it ran between the store and the derivation.
			ranInsideTheWindow = true
			purged <- err
		}
	}
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if err := <-purged; err != nil {
		t.Fatalf("purging: %v", err)
	}
	if ranInsideTheWindow {
		t.Error("the purge ran between the pass storing its rows and the pass's derivation")
	}
	assertTheProbeIsInEverySlot(t, harness, observed)

	inside := interfaceIDOf(t, harness.store, "example_if_a")
	if placed := scalarCount(t, harness.store, `SELECT count(*) FROM purged_flow_hour
		WHERE src_interface_id = ? AND local_client_id IS NOT NULL`, inside); placed != 1 {
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
	harness.collector.hooks.unordered = true
	var purgeErr error
	harness.collector.hooks.afterStore = func() { purgeErr = harness.collector.Purge(ctx) }
	if err := harness.collector.CollectFirewallLog(ctx); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if purgeErr != nil {
		t.Fatalf("purging: %v", purgeErr)
	}
	if unplaced := scalarCount(t, harness.store, `SELECT count(*) FROM purged_flow_hour
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
	earlier := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, earlier.fake, earlier.collector)
	ctx := context.Background()
	observed := referenceEpoch() - 1000
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
		if err := earlier.store.InsertFlow(ctx, store.Flow{
			LogDigest: fmt.Sprintf("example-unplaced-%d", index), ObservedAt: observed + int64(index),
			IngestedAt: referenceEpoch() - 990, InterfaceDevice: record.device,
			InterfaceLookupState: store.LookupResolved, SrcAddress: record.src, DstAddress: record.dst,
			Protocol: "tcp", IPVersion: version, Action: record.action, Direction: "in",
			PacketBytes: record.bytes, RuleLookupState: store.LookupNotFound,
		}); err != nil {
			t.Fatalf("storing an unplaced flow: %v", err)
		}
	}
	if err := earlier.store.SetSetting(ctx, "retention_seconds", "600", referenceEpoch()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}

	// This run: a collector over the same database, whose first act is the purge.
	collector := New(newFakeClient(t, earlier.fake), earlier.store, earlier.clock)
	if err := collector.Purge(ctx); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if left := countRows(t, earlier.store, "flow"); left != 0 {
		t.Fatalf("%d flows survived the purge", left)
	}
	inside := interfaceIDOf(t, earlier.store, "example_if_a")
	var rows []string
	result, err := earlier.store.DB().Query(`SELECT bytes, ifnull(src_interface_id, 0),
		ifnull(dst_interface_id, 0), local_client_id IS NOT NULL, traffic_direction FROM purged_flow_hour
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
	assertSlotsArePurgedPartPlusFlows(t, earlier.store)
}

// arrangeEveryStoringSource stands up a harness whose filter log, lease backend and
// resolver are all active.
func arrangeEveryStoringSource(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	for endpoint, fixture := range map[opnsense.Endpoint]string{
		opnsense.FirewallLog:      "firewall_log.json",
		opnsense.KeaStatus:        "kea_status_disabled.json",
		opnsense.DnsmasqStatus:    "dnsmasq_status_running.json",
		opnsense.DnsmasqSettings:  "dnsmasq_settings.json",
		opnsense.DnsmasqLeases:    "dnsmasq_leases.json",
		opnsense.ARPTable:         "get_arp.json",
		opnsense.NDPTable:         "get_ndp.json",
		opnsense.UnboundStatus:    "unbound_status_running.json",
		opnsense.UnboundSettings:  "unbound_settings.json",
		opnsense.UnboundIsEnabled: "unbound_is_enabled_on.json",
		opnsense.SearchQueries:    "search_queries_page1.json",
	} {
		harness.fake.answerFixture(endpoint, fixture)
	}
	ctx := context.Background()
	for _, probe := range []func(context.Context) error{harness.collector.probeFirewallLog,
		harness.collector.probeDHCPLease, harness.collector.probeDNSLookup} {
		if err := probe(ctx); err != nil {
			t.Fatalf("probing: %v", err)
		}
	}
	for kind, want := range map[string]string{KindFirewallLog: ProviderPf, KindDHCPLease: ProviderDnsmasq,
		KindDNSLookup: ProviderUnbound} {
		if key := harness.activeKeyOf(t, kind); key != want {
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

// TestThePassesAndThePurgeRunConcurrentlyAndEverySlotHoldsEveryFlowOnce is AC3: a filter-log
// loop, a lease loop, a resolver loop and a purge loop run concurrently, each for 36
// iterations, under a retention of 600 s while the clock crosses an hour, a day and an ISO
// week.
//
// Each filter-log pass offers a fresh record and a late one, observed just past the
// horizon -- a record the firewall's page delivered late, older than the horizon a purge
// at this instant applies but newer than the last purge's watermark, so it is stored. Every
// purge is requested inside the window this cycle closes: after the pass has stored its
// rows, before it derives them. Without the ordering it would move the late record into the
// purged part unplaced, and where no other row of its hour was ingested since -- across the
// hour boundary -- before any refresh read it. The filter log also re-offers its last 25
// records every pass, so records the purge has removed come back and must be refused; the
// resolver logs a lookup just before every outbound record, so the domain family fills;
// the lease pass re-points the address-level clients. The lease and resolver loops run
// freely beside the other two.
//
// Afterwards every slot of every family holds the flows ever ingested, each once; no purged
// row is unplaced, every record having an end the interface networks place; and the run
// ended within its bound.
func TestThePassesAndThePurgeRunConcurrentlyAndEverySlotHoldsEveryFlowOnce(t *testing.T) {
	t.Parallel()
	harness := arrangeEveryStoringSource(t)
	ctx := context.Background()
	// Nine minutes before a Monday's midnight, so the run crosses an hour, a day and a week.
	start := referenceInstant().Add(23*time.Hour + 51*time.Minute)
	setClock(harness, start)
	if store.PeriodWeek.SlotStart(start.Unix()) == store.PeriodWeek.SlotStart(start.Add(18*time.Minute).Unix()) {
		t.Fatal("the run does not cross an ISO week, so the composed week is not exercised")
	}
	if err := harness.store.SetSetting(ctx, "retention_seconds", "600", start.Unix()); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	// The resolver's page starts empty; each resolver pass replaces it.
	emptyLookups := map[string]any{"total": 0, "rowCount": 0, "current": 1, "rows": []any{}}
	harness.fake.answerJSON(opnsense.SearchQueries, emptyLookups)

	const iterations = 36
	var (
		offeredMutex sync.Mutex
		offered      []pageRecord
	)
	ticks := []chan struct{}{make(chan struct{}, iterations), make(chan struct{}, iterations)}
	failures := make(chan error, 4*iterations)

	// The purge is requested once a pass has stored its rows, and the pass goes on to its
	// derivation once the purge is either waiting for it or -- with no ordering -- done.
	// The next pass begins only once that purge has finished, so a late record is never
	// older than the last purge's watermark when it is offered.
	var (
		stored     = make(chan struct{})
		settled    = make(chan struct{})
		purgeDone  = make(chan struct{}, iterations)
		purgeWaits sync.Mutex
		waited     bool
	)
	harness.collector.hooks.purgeWaits = func() {
		purgeWaits.Lock()
		waited = true
		purgeWaits.Unlock()
		settled <- struct{}{}
	}
	harness.collector.hooks.afterStore = func() {
		stored <- struct{}{}
		<-settled
	}
	var loops sync.WaitGroup
	loop := func(name string, tick chan struct{}, pass func(int) error) {
		loops.Add(1)
		go func() {
			defer loops.Done()
			for i := 0; i < iterations; i++ {
				if tick != nil {
					<-tick
				}
				if err := pass(i); err != nil {
					failures <- fmt.Errorf("%s, iteration %d: %w", name, i, err)
				}
			}
		}()
	}

	loop("filter log", nil, func(i int) error {
		if i > 0 {
			<-purgeDone
		}
		harness.clock.advance(30 * time.Second)
		now := harness.clock.Now().Unix()
		offeredMutex.Lock()
		offered = append(offered, generatedRecord(i, 5000, now-615), generatedRecord(i, 4000, now-5))
		var page []any
		for back := len(offered) - 1; back >= 0 && back >= len(offered)-25; back-- {
			page = append(page, offered[back].body())
		}
		offeredMutex.Unlock()
		// The page is generated, newest first, as the endpoint returns it.
		harness.fake.answerJSON(opnsense.FirewallLog, page)
		err := harness.collector.CollectFirewallLog(ctx)
		for _, tick := range ticks {
			tick <- struct{}{}
		}
		return err
	})
	loop("DHCP leases", ticks[0], func(int) error { return harness.collector.CollectDHCPLease(ctx) })
	loop("resolver lookups", ticks[1], func(int) error {
		offeredMutex.Lock()
		var rows []any
		for back := len(offered) - 1; back >= 0 && back >= len(offered)-25; back-- {
			record := offered[back]
			if record.device != "exdev0" {
				continue
			}
			rows = append(rows, map[string]any{
				"client": record.src, "domain": fmt.Sprintf("example-site-%d.invalid", back%3),
				"time": record.observedAt - 2, "action": "Pass", "source": "Recursion", "rcode": "NOERROR",
				"dnssec_status": nil, "blocklist": "", "uuid": nil, "status": "0",
			})
		}
		offeredMutex.Unlock()
		// The lookups are generated, one just before every outbound record, newest first.
		harness.fake.answerJSON(opnsense.SearchQueries, map[string]any{
			"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows})
		return harness.collector.CollectDNSLookup(ctx)
	})
	loop("retention purge", nil, func(int) error {
		<-stored
		purgeWaits.Lock()
		waited = false
		purgeWaits.Unlock()
		err := harness.collector.Purge(ctx)
		purgeWaits.Lock()
		ranInsideTheWindow := !waited
		purgeWaits.Unlock()
		if ranInsideTheWindow {
			settled <- struct{}{}
		}
		purgeDone <- struct{}{}
		if ranInsideTheWindow {
			return errors.New("the purge ran between a pass storing its rows and the pass's derivation")
		}
		return err
	})

	finished := make(chan struct{})
	go func() {
		loops.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Minute):
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		t.Fatalf("the loops did not finish within ten minutes, which is a deadlock:\n%s", stacks)
	}
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	// Every record was offered first when it was newer than the last purge's watermark, so
	// every one was ingested, and stored again never.
	var truth, connections int64
	truthBySlot := map[store.Period]map[int64]slotFigures{}
	for _, period := range store.Periods() {
		truthBySlot[period] = map[int64]slotFigures{}
	}
	for _, record := range offered {
		truth += record.bytes
		connections++
		for _, period := range store.Periods() {
			slot := truthBySlot[period][period.SlotStart(record.observedAt)]
			slot.add(record.action, record.bytes, 1)
			truthBySlot[period][period.SlotStart(record.observedAt)] = slot
		}
	}
	purgedBytes := int64(scalarCount(t, harness.store, "SELECT coalesce(sum(bytes), 0) FROM purged_flow_hour"))
	presentBytes := int64(scalarCount(t, harness.store, "SELECT coalesce(sum(packet_bytes), 0) FROM flow"))
	if purgedBytes == 0 || presentBytes == 0 {
		t.Errorf("the purged part holds %d bytes and flow %d: the run did not exercise both", purgedBytes,
			presentBytes)
	}
	if purgedBytes+presentBytes != truth {
		t.Errorf("the purged part holds %d bytes and flow %d; the records offered hold %d", purgedBytes,
			presentBytes, truth)
	}
	purgedRecords := int64(scalarCount(t, harness.store, "SELECT coalesce(sum(connections), 0) FROM purged_flow_hour"))
	if stored := purgedRecords + int64(countRows(t, harness.store, "flow")); stored != connections {
		t.Errorf("%d records are stored or purged, not the %d offered once each", stored, connections)
	}

	// Every slot of the families every flow reaches holds what the records sum to...
	for _, period := range store.Periods() {
		for _, family := range []string{"volume", "rule"} {
			bySlot := map[int64]slotFigures{}
			for key, figures := range storedSlots(t, harness.store, period, family) {
				var start int64
				if _, err := fmt.Sscanf(key, "%d|", &start); err != nil {
					t.Fatalf("reading the slot %s: %v", key, err)
				}
				sum := bySlot[start]
				sum.bytes += figures.bytes
				sum.allowedBytes += figures.allowedBytes
				sum.blockedBytes += figures.blockedBytes
				sum.unknownBytes += figures.unknownBytes
				sum.allowed += figures.allowed
				sum.blocked += figures.blocked
				sum.unknown += figures.unknown
				bySlot[start] = sum
			}
			starts := make([]int64, 0, len(truthBySlot[period]))
			for start := range truthBySlot[period] {
				starts = append(starts, start)
			}
			sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
			for _, start := range starts {
				if got, want := bySlot[start], truthBySlot[period][start]; got != want {
					t.Errorf("the %s slot at %d of the %s family holds %+v; the records ingested into it "+
						"sum to %+v", period.Name, start, family, got, want)
				}
			}
			for start, got := range bySlot {
				if _, present := truthBySlot[period][start]; !present && got != (slotFigures{}) {
					t.Errorf("the %s slot at %d of the %s family holds %+v, and no record was ingested "+
						"into it", period.Name, start, family, got)
				}
			}
		}
	}
	// ...and every slot of every family is its purged part plus the flows present.
	assertSlotsArePurgedPartPlusFlows(t, harness.store)
	if unplaced := scalarCount(t, harness.store, `SELECT count(*) FROM purged_flow_hour
		WHERE src_interface_id IS NULL AND dst_interface_id IS NULL`); unplaced != 0 {
		t.Errorf("%d purged rows are frozen unplaced, while every record has an end the interface "+
			"networks place", unplaced)
	}
	if named := scalarCount(t, harness.store, "SELECT count(*) FROM domain_volume_aggregate_1h"); named == 0 {
		t.Error("no flow was attributed a site name, so the domain family is not exercised")
	}
	if repointed := scalarCount(t, harness.store, `SELECT count(*) FROM purged_flow_hour p
		JOIN client c ON c.id = p.local_client_id WHERE c.identity_kind <> 'address_in_interface'`); repointed == 0 {
		t.Error("no purged row names a leased client, so the lease pass's identities are not exercised")
	}
}
