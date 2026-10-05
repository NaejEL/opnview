package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The aggregate families: AC19, AC20, AC21 and AC34.

// storedFlow is a flow as a test reads it back, to recompute every family independently of
// the derivation statements.
type storedFlow struct {
	id                     int64
	observedAt             int64
	srcInterface, dstIface sql.NullInt64
	srcClient, dstClient   sql.NullInt64
	srcAddress, dstAddress string
	dstPort                sql.NullInt64
	protocol               string
	action, direction      string
	bytes                  int64
	ruleID                 sql.NullInt64
	scope                  string
	site                   sql.NullString
	owner                  sql.NullInt64
}

func readFlows(t *testing.T, database *Store) []storedFlow {
	t.Helper()
	rows, err := database.DB().Query(`SELECT f.id, f.observed_at, f.src_interface_id, f.dst_interface_id,
		f.src_client_id, f.dst_client_id, f.src_address, f.dst_address, f.dst_port, f.protocol,
		f.action, f.direction, f.packet_bytes, f.rule_id, f.traffic_scope, a.site_name
		FROM flow f LEFT JOIN domain_attribution a ON a.flow_id = f.id`)
	if err != nil {
		t.Fatalf("reading the flows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var flows []storedFlow
	for rows.Next() {
		var f storedFlow
		if err := rows.Scan(&f.id, &f.observedAt, &f.srcInterface, &f.dstIface, &f.srcClient,
			&f.dstClient, &f.srcAddress, &f.dstAddress, &f.dstPort, &f.protocol, &f.action,
			&f.direction, &f.bytes, &f.ruleID, &f.scope, &f.site); err != nil {
			t.Fatalf("reading the flows: %v", err)
		}
		flows = append(flows, f)
	}
	for index := range flows {
		if client, ok := flows[index].localClient(); ok {
			var owner sql.NullInt64
			if err := database.DB().QueryRow("SELECT owner_id FROM client WHERE id = ?", client).Scan(&owner); err != nil {
				t.Fatalf("reading an owner: %v", err)
			}
			flows[index].owner = owner
		}
	}
	return flows
}

// direction is the rule of docs/data-model.md, restated here independently of the view.
func (f storedFlow) trafficDirection() string {
	switch {
	case f.srcInterface.Valid && f.dstIface.Valid:
		return "inter_interface"
	case f.srcInterface.Valid:
		return "outbound"
	case f.dstIface.Valid:
		return "inbound"
	case f.direction == "out":
		return "outbound"
	default:
		return "inbound"
	}
}

func (f storedFlow) localClient() (int64, bool) {
	switch {
	case f.srcInterface.Valid:
		return f.srcClient.Int64, f.srcClient.Valid
	case f.dstIface.Valid:
		return f.dstClient.Int64, f.dstClient.Valid
	}
	return 0, false
}

func (f storedFlow) peer() string {
	switch f.trafficDirection() {
	case "inter_interface":
		return ""
	case "outbound":
		return f.dstAddress
	default:
		return f.srcAddress
	}
}

// otherEnd is the address at the other end from the flow's inside client: the
// destination when the client is the source, the source otherwise.
func (f storedFlow) otherEnd() string {
	if f.srcInterface.Valid {
		return f.dstAddress
	}
	return f.srcAddress
}

type figures struct {
	bytes, allowedBytes, blockedBytes, unknownBytes, allowed, blocked, unknown int64
}

func (f *figures) add(flow storedFlow) {
	f.bytes += flow.bytes
	switch flow.action {
	case "pass":
		f.allowedBytes += flow.bytes
		f.allowed++
	case "block", "reject":
		f.blockedBytes += flow.bytes
		f.blocked++
	default:
		f.unknownBytes += flow.bytes
		f.unknown++
	}
}

func nullable(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return fmt.Sprint(value.Int64)
}

// expectedFamily recomputes one family of one period from the flows.
func expectedFamily(flows []storedFlow, period Period, family string) map[string]figures {
	sums := map[string]figures{}
	for _, flow := range flows {
		slot := period.SlotStart(flow.observedAt)
		var key string
		switch family {
		case "volume":
			key = fmt.Sprintf("%d|%s|%s|%s|%s", slot, nullable(flow.srcInterface), nullable(flow.dstIface),
				flow.peer(), flow.trafficDirection())
		case "owner":
			if _, ok := flow.localClient(); !ok {
				continue
			}
			key = fmt.Sprintf("%d|%s|%s", slot, nullable(flow.owner), flow.scope)
		case "client":
			client, ok := flow.localClient()
			if !ok {
				continue
			}
			key = fmt.Sprintf("%d|%d|%s", slot, client, flow.trafficDirection())
		case "domain":
			if !flow.site.Valid {
				continue
			}
			key = fmt.Sprintf("%d|%s|%s", slot, flow.site.String, nullable(flow.srcClient))
		case "rule":
			key = fmt.Sprintf("%d|%s|%s|%s", slot, nullable(flow.ruleID), nullable(flow.srcInterface),
				nullable(flow.dstIface))
		case "peer":
			client, ok := flow.localClient()
			if !ok {
				continue
			}
			key = fmt.Sprintf("%d|%d|%s|%s", slot, client, flow.trafficDirection(), flow.otherEnd())
		}
		sum := sums[key]
		sum.add(flow)
		sums[key] = sum
	}
	return sums
}

// storedFamily reads one family of one period back, keyed as expectedFamily keys it.
func storedFamily(t *testing.T, database *Store, period Period, family string) map[string]figures {
	t.Helper()
	var query string
	switch family {
	case "volume":
		query = `SELECT period_start_at || '|' || ifnull(src_interface_id, 'null') || '|' ||
			ifnull(dst_interface_id, 'null') || '|' || ifnull(peer_address, '') || '|' || traffic_direction`
	case "owner":
		query = `SELECT period_start_at || '|' || ifnull(owner_id, 'null') || '|' || traffic_scope`
	case "client":
		query = `SELECT period_start_at || '|' || client_id || '|' || traffic_direction`
	case "domain":
		query = `SELECT period_start_at || '|' || site_name || '|' || ifnull(client_id, 'null')`
	case "rule":
		query = `SELECT period_start_at || '|' || ifnull(rule_id, 'null') || '|' ||
			ifnull(src_interface_id, 'null') || '|' || ifnull(dst_interface_id, 'null')`
	case "peer":
		query = `SELECT period_start_at || '|' || client_id || '|' || traffic_direction || '|' ||
			peer_address`
	}
	table := family + "_volume_aggregate_" + period.Name
	if family == "volume" {
		table = "volume_aggregate_" + period.Name
	}
	rows, err := database.DB().Query(query + `, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
		allowed_connections, blocked_connections, unknown_connections FROM ` + table)
	if err != nil {
		t.Fatalf("reading %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	stored := map[string]figures{}
	for rows.Next() {
		var (
			key string
			sum figures
		)
		if err := rows.Scan(&key, &sum.bytes, &sum.allowedBytes, &sum.blockedBytes,
			&sum.unknownBytes, &sum.allowed, &sum.blocked, &sum.unknown); err != nil {
			t.Fatalf("reading %s: %v", table, err)
		}
		if _, duplicate := stored[key]; duplicate {
			t.Errorf("%s holds the slot %s twice", table, key)
		}
		stored[key] = sum
	}
	return stored
}

// attributeNetwork gives the network lookups, so the domain family has rows: every client
// looks one name up just before each of its outbound flows of one kind.
func attributeNetwork(t *testing.T, network *testNetwork) {
	t.Helper()
	ctx := context.Background()
	for _, flow := range readFlows(t, network.db) {
		if flow.dstIface.Valid || !flow.srcInterface.Valid || flow.id%2 != 0 {
			continue
		}
		var clientID *int64
		if flow.srcClient.Valid {
			clientID = &flow.srcClient.Int64
		}
		if err := network.db.InsertDNSResolution(ctx, DNSResolution{
			LookupKey: fmt.Sprintf("example-lookup-%d", flow.id), ClientAddress: flow.srcAddress,
			ClientID: clientID, Domain: fmt.Sprintf("site-%d.example.invalid", flow.id%7),
			Resolver: "unbound", Action: "pass", AnswerSource: ptr("Recursion"),
			LookedUpAt: flow.observedAt - 2, IngestedAt: network.now,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	result, err := network.db.Attribute(ctx, 0, network.now, 5, network.now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, network.now, network.now, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
}

// TestEverySlotOfEveryFamilyEqualsTheSameSumsOverFlow is AC19.
func TestEverySlotOfEveryFamilyEqualsTheSameSumsOverFlow(t *testing.T) {
	for _, counts := range [][3]int{{2, 5, 300}, {4, 13, 900}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			// Forty days back, so slots of every period, months included, are closed and open.
			network := populated(t, counts[0], counts[1], counts[2], 40*86400)
			if _, err := network.db.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
				VALUES (1, 'example owner', 0, 0)`); err != nil {
				t.Fatalf("writing an owner: %v", err)
			}
			if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0
				WHERE id % 2 = 0`); err != nil {
				t.Fatalf("assigning an owner: %v", err)
			}
			attributeNetwork(t, network)
			recomputeEverything(t, network)

			flows := readFlows(t, network.db)
			var total figures
			for _, flow := range flows {
				total.add(flow)
			}
			var inbound int
			for _, flow := range flows {
				if !flow.srcInterface.Valid {
					inbound++
				}
			}
			if inbound == 0 {
				t.Fatal("no flow has an outside source, so the nullable source slot is not exercised")
			}
			for _, period := range Periods() {
				for _, family := range []string{"volume", "owner", "client", "domain", "rule", "peer"} {
					expected := expectedFamily(flows, period, family)
					stored := storedFamily(t, network.db, period, family)
					if len(expected) == 0 {
						t.Errorf("the %s family of %s has nothing to compare", family, period.Name)
					}
					for key, want := range expected {
						if got := stored[key]; got != want {
							t.Errorf("%s %s slot %s holds %+v, flow sums to %+v", family, period.Name, key, got, want)
						}
					}
					for key := range stored {
						if _, present := expected[key]; !present {
							t.Errorf("%s %s holds the slot %s that no flow produces", family, period.Name, key)
						}
					}
				}
				// No remainder: the interface and rule families hold every byte.
				for _, table := range []string{"volume_aggregate_", "rule_volume_aggregate_"} {
					var bytes, blocked int64
					if err := network.db.DB().QueryRow("SELECT sum(bytes), sum(blocked_bytes) FROM "+
						table+period.Name).Scan(&bytes, &blocked); err != nil {
						t.Fatalf("summing %s: %v", table, err)
					}
					if bytes != total.bytes || blocked != total.blockedBytes {
						t.Errorf("%s%s sums to %d bytes (%d blocked), the flows to %d (%d)",
							table, period.Name, bytes, blocked, total.bytes, total.blockedBytes)
					}
				}
			}
		})
	}
}

// rewritten renders a refresh's slots as a sorted list.
func rewritten(refresh Refresh) []string {
	var slots []string
	for _, slot := range refresh.Rewritten {
		slots = append(slots, fmt.Sprintf("%s@%d", slot.Period, slot.Start))
	}
	sort.Strings(slots)
	return slots
}

func currentSlots(now int64, extra ...int64) []string {
	var slots []string
	seen := map[string]bool{}
	for _, period := range Periods() {
		for _, instant := range append([]int64{now}, extra...) {
			slot := fmt.Sprintf("%s@%d", period.Name, period.SlotStart(instant))
			if !seen[slot] {
				seen[slot] = true
				slots = append(slots, slot)
			}
		}
	}
	sort.Strings(slots)
	return slots
}

// TestTheRefreshRewritesExactlyTheSlotsTheContractNames is AC20, its first three parts; the
// fourth -- the refresh runs after each filter-log pass -- is in internal/collect.
func TestTheRefreshRewritesExactlyTheSlotsTheContractNames(t *testing.T) {
	network := populated(t, 2, 6, 200, 60*86400)
	ctx := context.Background()
	computedAt := func() map[string]int64 {
		result := map[string]int64{}
		for _, table := range aggregateTables() {
			if table == "pair_volume_observation" {
				continue
			}
			rows, err := network.db.DB().Query("SELECT period_start_at, max(computed_at) FROM " + table +
				" GROUP BY period_start_at")
			if err != nil {
				t.Fatalf("reading %s: %v", table, err)
			}
			for rows.Next() {
				var start, computed int64
				if err := rows.Scan(&start, &computed); err != nil {
					t.Fatalf("reading %s: %v", table, err)
				}
				result[fmt.Sprintf("%s@%d", table, start)] = computed
			}
			_ = rows.Close()
		}
		return result
	}

	// A flow observed in a closed slot, ingested later.
	closed := network.now - 9*86400 - 1800
	later := network.now + 60
	port := int64(22)
	client := network.clients[0]
	addresses := network.insert(t, []networkFlow{{observedAt: closed, device: client.device,
		direction: "in", src: client.address, dst: outside(3, client.v6), dstPort: &port,
		protocol: "tcp", action: "pass", bytes: 777, ingestedAt: later - 30}})
	if _, err := network.db.Reclassify(ctx, addresses, later); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	before := computedAt()
	refresh, err := network.db.RefreshAggregates(ctx, network.now+1, later, nil)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if got, want := strings.Join(rewritten(refresh), " "), strings.Join(currentSlots(later, closed), " "); got != want {
		t.Errorf("a flow in a closed slot rewrote %s, not %s", got, want)
	}
	after := computedAt()
	for key, computed := range before {
		slot := key[strings.Index(key, "@")+1:]
		touched := false
		for _, period := range Periods() {
			if slot == fmt.Sprint(period.SlotStart(closed)) || slot == fmt.Sprint(period.SlotStart(later)) {
				touched = true
			}
		}
		if !touched && after[key] != computed {
			t.Errorf("the untouched slot %s was rewritten", key)
		}
	}

	// A pass with nothing new rewrites only the current slots.
	quiet := later + 60
	refresh, err = network.db.RefreshAggregates(ctx, later+1, quiet, nil)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if got, want := strings.Join(rewritten(refresh), " "), strings.Join(currentSlots(quiet), " "); got != want {
		t.Errorf("a pass with nothing new rewrote %s, not %s", got, want)
	}

	// A slot whose computed_at is not older than its newest ingested_at is not rewritten,
	// even when the pass is asked to consider every flow ever ingested.
	everything := quiet + 60
	refresh, err = network.db.RefreshAggregates(ctx, 0, everything, nil)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if got, want := strings.Join(rewritten(refresh), " "), strings.Join(currentSlots(everything), " "); got != want {
		t.Errorf("a pass over every flow rewrote fresh slots: %s, not %s", got, want)
	}
}

// TestADistinctCountIsNeverSummedAcrossSlots is AC21.
func TestADistinctCountIsNeverSummedAcrossSlots(t *testing.T) {
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	if _, err := network.db.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
		VALUES (1, 'example owner', 0, 0)`); err != nil {
		t.Fatalf("writing an owner: %v", err)
	}
	owned, unowned := network.clients[0], network.clients[1]
	dayStart := PeriodDay.SlotStart(network.now)
	port := int64(443)
	var specs []networkFlow
	// Each client is active in three different hours of the same day.
	for hour := int64(0); hour < 3; hour++ {
		for _, client := range []testClient{owned, unowned} {
			specs = append(specs, networkFlow{observedAt: dayStart + hour*3600 + 60, device: client.device,
				direction: "in", src: client.address, dst: outside(int(hour), client.v6), dstPort: &port,
				protocol: "tcp", action: "pass", bytes: 100})
		}
	}
	addresses := network.insert(t, specs)
	if _, err := network.db.Reclassify(ctx, addresses, network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0
		WHERE last_address = ?`, owned.address); err != nil {
		t.Fatalf("assigning the owner: %v", err)
	}
	recomputeEverything(t, network)

	totals, err := network.db.ReadOwnerTotals(ctx, PeriodHour, dayStart, PeriodDay.SlotEnd(dayStart))
	if err != nil {
		t.Fatalf("reading the owner totals: %v", err)
	}
	summedCounts := queryInt(t, network.db, `SELECT sum(client_count) FROM owner_volume_aggregate_1h
		WHERE period_start_at >= ? AND owner_id = 1`, dayStart)
	if summedCounts != 3 {
		t.Fatalf("the owned client should sit in three hour slots, it sits in %d", summedCounts)
	}
	found := map[string]bool{}
	for _, total := range totals {
		key := "unassigned"
		if total.OwnerID != nil {
			key = "owned"
		}
		found[key] = true
		if total.Clients != 1 {
			t.Errorf("the %s bucket counts %d clients over three hours; one machine is one client",
				key, total.Clients)
		}
		if total.Bytes != 300 {
			t.Errorf("the %s bucket sums %d bytes over three hours, not 300", key, total.Bytes)
		}
	}
	if !found["owned"] || !found["unassigned"] {
		t.Errorf("the owner totals hold %v, not both buckets", found)
	}
	for _, period := range Periods() {
		if rows := queryInt(t, network.db, "SELECT count(*) FROM owner_volume_aggregate_"+period.Name+
			" WHERE owner_id IS NULL"); rows == 0 {
			t.Errorf("the %s owner family holds no unassigned slot", period.Name)
		}
	}
	// And no read statement anywhere sums a per-slot distinct count.
	for _, name := range mustStatementNames(t) {
		text, _ := Statement(name)
		lowered := strings.ToLower(strings.Join(strings.Fields(text), " "))
		for _, column := range []string{"client_count", "distinct_peers"} {
			if strings.Contains(lowered, "sum("+column) || strings.Contains(lowered, "sum(v."+column) {
				t.Errorf("the statement %s sums %s across slots", name, column)
			}
		}
	}
}

func mustStatementNames(t *testing.T) []string {
	t.Helper()
	names, err := StatementNames()
	if err != nil {
		t.Fatalf("reading the statements: %v", err)
	}
	sort.Strings(names)
	return names
}

// TestThePairVolumeIsDerivedFromFlowOnTheRealDestinationPort is AC34, its first two parts.
func TestThePairVolumeIsDerivedFromFlowOnTheRealDestinationPort(t *testing.T) {
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	a, b := network.clients[0], network.clients[2]
	if a.v6 != b.v6 {
		t.Fatal("the two clients are of different families")
	}
	day := PeriodDay.SlotStart(network.now)
	port := int64(8443)
	addresses := network.insert(t, []networkFlow{
		{observedAt: day + 100, device: a.device, direction: "in", src: a.address, dst: b.address,
			dstPort: &port, protocol: "tcp", action: "pass", bytes: 100},
		{observedAt: day + 200, device: b.device, direction: "in", src: b.address, dst: a.address,
			dstPort: &port, protocol: "tcp", action: "block", bytes: 40},
		{observedAt: day + 300, device: a.device, direction: "in", src: a.address,
			dst: outside(9, a.v6), protocol: "icmp", action: "pass", bytes: 84},
	})
	if _, err := network.db.Reclassify(ctx, addresses, network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, 0, network.now, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	low, high := a.address, b.address
	if low > high {
		low, high = high, low
	}
	var octets, packets float64
	var rows int64
	if err := network.db.DB().QueryRow(`SELECT count(*), sum(octets), sum(packets)
		FROM pair_volume_observation WHERE day_start_at = ? AND endpoint_low = ? AND endpoint_high = ?
		  AND service_port = ? AND protocol = 'tcp'`, day, low, high, port).Scan(&rows, &octets, &packets); err != nil {
		t.Fatalf("reading the pair: %v", err)
	}
	if rows != 1 || octets != 140 || packets != 2 {
		t.Errorf("both directions of the pair on port %d made %d rows of %v octets in %v packets, "+
			"not one row of 140 in 2", port, rows, octets, packets)
	}
	if portless := queryInt(t, network.db, `SELECT count(*) FROM pair_volume_observation
		WHERE protocol = 'icmp' AND service_port = 0`); portless != 1 {
		t.Errorf("a portless protocol made %d rows with service port 0", portless)
	}
}

// TestAMonthStraddlingTheHorizonKeepsWhatThePurgeRemoved is AC19 and AC22 under a short
// retention: a month that straddles the flow horizon has lost flows to the purge, and a
// forced rewrite of it -- the one a reclassification after a discovery change makes -- must
// not drop them from the slot. It is composed from its days, which the purge keeps as long as
// the month, and it still takes in a flow ingested after the purge.
func TestAMonthStraddlingTheHorizonKeepsWhatThePurgeRemoved(t *testing.T) {
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	month := PeriodMonth.SlotStart(network.now)
	early := month + 2*86400 + 1800
	late := network.now - 600
	if PeriodWeek.SlotStart(early) == PeriodWeek.SlotStart(network.now) || network.now-month < 10*86400 {
		t.Fatalf("the reference instant does not leave room for a month straddling the horizon")
	}
	client := network.clients[0]
	port := int64(443)
	flow := func(at, bytes, ingested int64) networkFlow {
		return networkFlow{observedAt: at, device: client.device, direction: "in", src: client.address,
			dst: outside(int(at%97), client.v6), dstPort: &port, protocol: "tcp", action: "pass",
			bytes: bytes, ingestedAt: ingested}
	}
	addresses := network.insert(t, []networkFlow{flow(early, 500, 0), flow(late, 70, 0)})
	network.classifyAndRefresh(t, addresses)
	monthBytes := func(table string) int64 {
		t.Helper()
		return queryInt(t, network.db, "SELECT coalesce(sum(bytes), 0) FROM "+table+
			" WHERE period_start_at = ?", month)
	}
	if got := monthBytes("volume_aggregate_30d"); got != 570 {
		t.Fatalf("the month holds %d bytes before the purge, not 570", got)
	}

	// The horizon falls between the two flows, and the purge removes the early one.
	horizon := month + 10*86400
	if err := network.db.SetSetting(ctx, "retention_seconds", fmt.Sprint(network.now-horizon), network.now); err != nil {
		t.Fatalf("setting the horizon: %v", err)
	}
	if err := network.db.Purge(ctx, network.now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if flows := queryInt(t, network.db, "SELECT count(*) FROM flow"); flows != 1 {
		t.Fatalf("%d flows survived the purge, not the late one alone", flows)
	}
	if days := queryInt(t, network.db, "SELECT count(*) FROM volume_aggregate_24h WHERE period_start_at = ?",
		PeriodDay.SlotStart(early)); days == 0 {
		t.Fatal("the purge removed the early day, which the straddling month is composed from")
	}

	// A forced rewrite of every slot the two flows touched.
	if _, err := network.db.RefreshAggregates(ctx, 0, network.now+60, []int64{early, late}); err != nil {
		t.Fatalf("the forced refresh: %v", err)
	}
	for _, table := range []string{"volume_aggregate_30d", "rule_volume_aggregate_30d",
		"client_volume_aggregate_30d", "owner_volume_aggregate_30d"} {
		if got := monthBytes(table); got != 570 {
			t.Errorf("%s holds %d bytes after a forced rewrite, not the 570 it held: the purged "+
				"flows were dropped from a slot that outlives them", table, got)
		}
	}
	if clients := queryInt(t, network.db, `SELECT coalesce(max(client_count), 0) FROM owner_volume_aggregate_30d
		WHERE period_start_at = ?`, month); clients != 1 {
		t.Errorf("the composed month counts %d distinct clients, not the one", clients)
	}

	// A flow ingested after the purge, inside the horizon, still reaches the month.
	addresses = network.insert(t, []networkFlow{flow(network.now-300, 30, network.now+100)})
	if _, err := network.db.Reclassify(ctx, addresses, network.now+100); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, network.now+61, network.now+120, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if got := monthBytes("volume_aggregate_30d"); got != 600 {
		t.Errorf("the month holds %d bytes after a new flow, not 600", got)
	}
}
