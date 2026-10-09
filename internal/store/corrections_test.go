package store

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The step-5A corrections: AC1 to AC6 and AC8 of specs/SPEC-step-5a-corrections.md, on
// the store's side.

// familyTables names the table of each family of one period.
func familyTable(family string, period Period) string {
	if family == "volume" {
		return "volume_aggregate_" + period.Name
	}
	return family + "_volume_aggregate_" + period.Name
}

var allFamilies = []string{"volume", "owner", "client", "domain", "rule", "peer"}

// slotBytes sums one family's bytes in the slot of period holding instant.
func slotBytes(t *testing.T, database *Store, family string, period Period, instant int64) int64 {
	t.Helper()
	return queryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM "+familyTable(family, period)+
		" WHERE period_start_at = ?", period.SlotStart(instant))
}

// outboundFlow is one flow from a client to an outside address.
func outboundFlow(client testClient, at, bytes int64, peer int, ingested int64) networkFlow {
	port := int64(443)
	return networkFlow{observedAt: at, device: client.device, direction: "in", src: client.address,
		dst: outside(peer, client.v6), dstPort: &port, protocol: "tcp", action: "pass", bytes: bytes,
		ingestedAt: ingested}
}

// lookupBefore records a passed lookup by a flow's source just before it, so the flow
// carries a site name and the domain family has a row.
func lookupBefore(t *testing.T, network *testNetwork, key string, address string, at int64) {
	t.Helper()
	if err := network.db.InsertDNSResolution(context.Background(), DNSResolution{
		LookupKey: key, ClientAddress: address, Domain: "example-site.example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: ptr("Recursion"),
		LookedUpAt: at - 2, IngestedAt: at,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
}

// TestARetentionShorterThanAnHourStillFillsTheCurrentSlots is AC1, its first part: with
// retention_seconds at 600 and now 800 s into its hour, a 77-byte flow at now - 100 is in
// the current 1h, 24h, 7d and 30d slots of every family.
func TestARetentionShorterThanAnHourStillFillsTheCurrentSlots(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	if offset := network.now - PeriodHour.SlotStart(network.now); offset != 800 {
		t.Fatalf("the reference instant is %d s into its hour, not 800", offset)
	}
	if err := network.db.SetSetting(ctx, "retention_seconds", "600", network.now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	client := network.clients[0]
	at := network.now - 100
	addresses := network.insert(t, []networkFlow{outboundFlow(client, at, 77, 1, 0)})
	lookupBefore(t, network, "example-lookup-ac1", client.address, at)
	if _, err := network.db.Reclassify(ctx, addresses, network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.Attribute(ctx, at-60, at, 5, network.now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if err := network.db.Purge(ctx, network.now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, 0, network.now, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	for _, period := range Periods() {
		for _, family := range allFamilies {
			if got := slotBytes(t, network.db, family, period, network.now); got != 77 {
				t.Errorf("the current %s slot of the %s family holds %d bytes, not the 77 of the flow",
					period.Name, family, got)
			}
		}
	}
}

// TestTheCurrentSlotsEqualTheSumsOverFlowWhateverTheRetention is AC1, its second part. One
// network is refreshed from empty aggregate tables under each retention in turn, so every
// retention computes the current slots on its own.
func TestTheCurrentSlotsEqualTheSumsOverFlowWhateverTheRetention(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 3, 7)
	ctx := context.Background()
	// Flows over the last day, dense enough that the current hour holds every kind: the
	// current hour and the earlier hours of the current day, week and month.
	addresses := network.insert(t, network.flowsFor(500, 86400))
	network.classifyAndRefresh(t, addresses)
	attributeNetwork(t, network)
	flows := readFlows(t, network.db)

	for _, retention := range []int64{0, 1, 599, 3599, 3600} {
		t.Run(fmt.Sprintf("retention %d", retention), func(t *testing.T) {
			if err := network.db.SetSetting(ctx, "retention_seconds", fmt.Sprint(retention), network.now); err != nil {
				t.Fatalf("setting the retention: %v", err)
			}
			for _, table := range aggregateTables() {
				if _, err := network.db.DB().Exec("DELETE FROM " + table); err != nil {
					t.Fatalf("emptying %s: %v", table, err)
				}
			}
			if _, err := network.db.RefreshAggregates(ctx, 0, network.now, nil); err != nil {
				t.Fatalf("refreshing: %v", err)
			}
			for _, period := range Periods() {
				current := period.SlotStart(network.now)
				prefix := fmt.Sprintf("%d|", current)
				for _, family := range allFamilies {
					expected := expectedFamily(flows, period, family)
					stored := storedFamily(t, network.db, period, family)
					compared := 0
					for key, want := range expected {
						if !strings.HasPrefix(key, prefix) {
							continue
						}
						compared++
						if got := stored[key]; got != want {
							t.Errorf("the current %s slot %s of the %s family holds %+v, flow sums to %+v",
								period.Name, key, family, got, want)
						}
					}
					for key := range stored {
						if _, present := expected[key]; strings.HasPrefix(key, prefix) && !present {
							t.Errorf("the current %s slot of the %s family holds %s, which no flow produces",
								period.Name, family, key)
						}
					}
					if compared == 0 {
						t.Errorf("the current %s slot of the %s family has nothing to compare", period.Name, family)
					}
				}
			}
		})
	}
}

// TestAnHourHoldingTheHorizonTakesALateFlowAndAReclassification is AC2.
func TestAnHourHoldingTheHorizonTakesALateFlowAndAReclassification(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	hour := PeriodHour.SlotStart(network.now)
	client := network.clients[0]
	if client.v6 {
		t.Fatal("the first client is expected to be IPv4")
	}
	// The horizon falls 400 s into the current hour.
	if err := network.db.SetSetting(ctx, "retention_seconds", fmt.Sprint(network.now-(hour+400)),
		network.now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	addresses := network.insert(t, []networkFlow{
		outboundFlow(client, hour+100, 100, 1, 0),
		outboundFlow(client, hour+700, 200, 5, 0),
	})
	network.classifyAndRefresh(t, addresses)
	if err := network.db.Purge(ctx, network.now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if flows := queryInt(t, network.db, "SELECT count(*) FROM flow"); flows != 1 {
		t.Fatalf("%d flows survived the purge, not the one after the horizon", flows)
	}

	// A flow ingested late into the hour, after the purge.
	late := network.now + 10
	addresses = network.insert(t, []networkFlow{outboundFlow(client, hour+500, 50, 3, late)})
	if _, err := network.db.Reclassify(ctx, addresses, late); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.RefreshAggregates(ctx, network.now, late+1, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	for _, family := range []string{"volume", "rule", "client", "owner", "peer"} {
		if got := slotBytes(t, network.db, family, PeriodHour, hour); got != 350 {
			t.Errorf("the %s family holds %d bytes in the hour, not its purged 100 plus the 250 present",
				family, got)
		}
	}
	if peers := queryInt(t, network.db, `SELECT distinct_peers FROM client_volume_aggregate_1h
		WHERE period_start_at = ? AND traffic_direction = 'outbound'`, hour); peers != 3 {
		t.Errorf("the hour counts %d distinct peers, not the purged one and the two present", peers)
	}

	// A reclassification of a flow still present: an operator network places the
	// destination of the 200-byte flow behind the second interface, so that flow moves
	// from outbound to between interfaces, and the purged part stays where it was.
	if _, err := network.db.AddInterfaceNetwork(ctx, network.inside[1].id, outside(5, false)+"/32",
		late+20); err != nil {
		t.Fatalf("adding the network: %v", err)
	}
	result, err := network.db.ReclassifyAll(ctx, late+20)
	if err != nil {
		t.Fatalf("reclassifying: %v", err)
	}
	if len(result.FlowInstants) == 0 {
		t.Fatal("the operator network moved no flow, so the test proves nothing")
	}
	if _, err := network.db.RefreshAggregates(ctx, late+2, late+30, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	byDirection := func(direction string) int64 {
		return queryInt(t, network.db, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
			WHERE period_start_at = ? AND traffic_direction = ?`, hour, direction)
	}
	if got := byDirection("inter_interface"); got != 200 {
		t.Errorf("the hour holds %d bytes between interfaces after the reclassification, not 200", got)
	}
	if got := byDirection("outbound"); got != 150 {
		t.Errorf("the hour holds %d outbound bytes after the reclassification, not the purged 100 "+
			"plus the late 50", got)
	}
}

// TestTheRolledUpWeekAndMonthEqualADirectComputation is AC3: under a retention short
// enough to put the horizon inside the current hour, the current week and month, rolled up
// from their days, equal the same sums over flow in every family, allowed, blocked and
// unknown included, and each equals the sum of its day slots.
func TestTheRolledUpWeekAndMonthEqualADirectComputation(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 3, 9)
	ctx := context.Background()
	if err := network.db.SetSetting(ctx, "retention_seconds", "3600", network.now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	month := PeriodMonth.SlotStart(network.now)
	addresses := network.insert(t, network.flowsFor(300, network.now-month))
	network.classifyAndRefresh(t, addresses)
	attributeNetwork(t, network)

	flows := readFlows(t, network.db)
	for _, period := range []Period{PeriodWeek, PeriodMonth} {
		current := fmt.Sprintf("%d|", period.SlotStart(network.now))
		for _, family := range allFamilies {
			expected := expectedFamily(flows, period, family)
			stored := storedFamily(t, network.db, period, family)
			compared := 0
			for key, want := range expected {
				if !strings.HasPrefix(key, current) {
					continue
				}
				compared++
				if got := stored[key]; got != want {
					t.Errorf("the rolled-up %s slot %s of the %s family holds %+v, flow sums to %+v",
						period.Name, key, family, got, want)
				}
			}
			if compared == 0 {
				t.Errorf("the current %s slot of the %s family has nothing to compare", period.Name, family)
			}
			table := familyTable(family, period)
			rolled := queryInt(t, network.db, "SELECT coalesce(sum(bytes), 0) FROM "+table+
				" WHERE period_start_at = ?", period.SlotStart(network.now))
			days := queryInt(t, network.db, "SELECT coalesce(sum(bytes), 0) FROM "+familyTable(family, PeriodDay)+
				" WHERE period_start_at >= ? AND period_start_at < ?", period.SlotStart(network.now),
				period.SlotEnd(period.SlotStart(network.now)))
			if rolled != days {
				t.Errorf("%s holds %d bytes in the current slot, its days %d", table, rolled, days)
			}
		}
	}
}

// TestDistinctPeersIsExactInAComposedAndARolledUpSlot is AC4. A day is composed from its
// hours and a month rolled up from its days; one peer active in two of the finer slots
// counts once, and every figure equals a direct distinct count -- after the flows have been
// purged, when the finer slots are all there is.
func TestDistinctPeersIsExactInAComposedAndARolledUpSlot(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	client := network.clients[0]
	day := PeriodDay.SlotStart(network.now)
	yesterday := day - 86400
	if PeriodMonth.SlotStart(yesterday) != PeriodMonth.SlotStart(day) {
		t.Fatal("the reference day is the first of its month")
	}
	// Hour one: peers 1 and 2. Hour two: peers 1 and 3. Yesterday: peers 1 and 4. And
	// peer 1 once more at the present instant, which the purge below leaves alone, so
	// the client keeps a flow naming it and is not purged with the rest.
	addresses := network.insert(t, []networkFlow{
		outboundFlow(client, day+60, 10, 1, 0), outboundFlow(client, day+120, 10, 2, 0),
		outboundFlow(client, day+3660, 10, 1, 0), outboundFlow(client, day+3720, 10, 3, 0),
		outboundFlow(client, yesterday+60, 10, 1, 0), outboundFlow(client, yesterday+120, 10, 4, 0),
		outboundFlow(client, network.now, 10, 1, 0),
	})
	network.classifyAndRefresh(t, addresses)
	direct := func(from, to int64) int64 {
		return queryInt(t, network.db, `SELECT count(DISTINCT dst_address) FROM flow
			WHERE src_address = ? AND observed_at >= ? AND observed_at < ?`, client.address, from, to)
	}
	wantDay, wantMonth := direct(day, day+86400), direct(PeriodMonth.SlotStart(day), network.now+1)
	if wantDay != 3 || wantMonth != 4 {
		t.Fatalf("the direct counts are %d and %d, not 3 and 4", wantDay, wantMonth)
	}
	peers := func(period Period) int64 {
		return queryInt(t, network.db, "SELECT coalesce(max(distinct_peers), 0) FROM client_volume_aggregate_"+
			period.Name+" WHERE period_start_at = ? AND traffic_direction = 'outbound'", period.SlotStart(day))
	}
	check := func(when string) {
		t.Helper()
		if got := peers(PeriodDay); got != wantDay {
			t.Errorf("%s, the composed day counts %d distinct peers, not %d", when, got, wantDay)
		}
		if got := peers(PeriodMonth); got != wantMonth {
			t.Errorf("%s, the rolled-up month counts %d distinct peers, not %d", when, got, wantMonth)
		}
	}
	check("with every flow present")

	// Every flow purged; a forced rewrite composes the day and the month from what is left.
	if err := network.db.SetSetting(ctx, "retention_seconds", "1", network.now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	if err := network.db.Purge(ctx, network.now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if flows := queryInt(t, network.db, "SELECT count(*) FROM flow"); flows != 1 {
		t.Fatalf("%d flows survived the purge, not the present one", flows)
	}
	forced := []int64{day + 60, day + 3660, yesterday + 60}
	if _, err := network.db.RefreshAggregates(ctx, 0, network.now+5, forced); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	check("after the purge")
}

// TestTheDataModelCarriesNoLowerBoundForDistinctPeers is AC4, its last part.
func TestTheDataModelCarriesNoLowerBoundForDistinctPeers(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "data-model.md"))
	if err != nil {
		t.Fatalf("reading the data model: %v", err)
	}
	for _, paragraph := range strings.Split(string(raw), "\n\n") {
		lowered := strings.ToLower(paragraph)
		if strings.Contains(lowered, "distinct_peers") && strings.Contains(lowered, "lower bound") {
			t.Errorf("a paragraph still calls distinct_peers a lower bound:\n%s", paragraph)
		}
	}
}

// totalChanges reads the rows the connection has written since it opened.
func totalChanges(t *testing.T, database *Store) int64 {
	t.Helper()
	return queryInt(t, database, "SELECT total_changes()")
}

// TestASecondClassificationPassWithNothingChangedWritesNoRow is AC5, its first part.
func TestASecondClassificationPassWithNothingChangedWritesNoRow(t *testing.T) {
	t.Parallel()
	network := populated(t, 2, 6, 200, 86400)
	ctx := context.Background()
	provider, err := network.db.ProviderID(ctx, "security_event", "suricata")
	if err != nil {
		t.Fatalf("looking up the event provider: %v", err)
	}
	var addresses []string
	for index, client := range network.clients {
		if err := network.db.InsertSecurityEvent(ctx, provider, SecurityEvent{
			ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: network.now - 60,
			IngestedAt: network.now - 60, RuleIdentity: "1", Signature: "example", EventAction: "allowed",
			SrcAddress: client.address, DstAddress: outside(index, client.v6),
		}); err != nil {
			t.Fatalf("writing an event: %v", err)
		}
		addresses = append(addresses, client.address, outside(index, client.v6))
	}
	for _, flow := range readFlows(t, network.db) {
		addresses = append(addresses, flow.srcAddress, flow.dstAddress)
	}
	if _, err := network.db.Reclassify(ctx, addresses, network.now); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	before := totalChanges(t, network.db)
	second, err := network.db.Reclassify(ctx, addresses, network.now)
	if err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if written := totalChanges(t, network.db) - before; written != 0 {
		t.Errorf("a second pass with no evidence changed wrote %d rows", written)
	}
	if len(second.FlowInstants) != 0 || len(second.LookupInstants) != 0 || second.Placed != 0 {
		t.Errorf("a second pass changed %d flows and %d lookups and placed %d addresses in full",
			len(second.FlowInstants), len(second.LookupInstants), second.Placed)
	}
	if records := queryInt(t, network.db, "SELECT count(*) FROM address_classification"); records == 0 {
		t.Error("no evidence was recorded, so the pass cannot be incremental")
	}
}

// flowEnds renders every flow's placed ends, keyed by digest.
func flowEnds(t *testing.T, database *Store) map[string]string {
	t.Helper()
	rows, err := database.DB().Query(`SELECT log_digest, src_address, dst_address,
		ifnull(src_interface_id, -1), ifnull(dst_interface_id, -1), ifnull(src_client_id, -1),
		ifnull(dst_client_id, -1), traffic_scope FROM flow`)
	if err != nil {
		t.Fatalf("reading the flows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]string{}
	for rows.Next() {
		var digest, src, dst, scope string
		var a, b, c, d int64
		if err := rows.Scan(&digest, &src, &dst, &a, &b, &c, &d, &scope); err != nil {
			t.Fatalf("reading the flows: %v", err)
		}
		result[digest] = fmt.Sprintf("%s>%s %d %d %d %d %s", src, dst, a, b, c, d, scope)
	}
	return result
}

// TestALeaseNamingOneAddressReclassifiesThatAddressAlone is AC5, its second part.
func TestALeaseNamingOneAddressReclassifiesThatAddressAlone(t *testing.T) {
	t.Parallel()
	network := populated(t, 2, 6, 200, 86400)
	ctx := context.Background()
	var target testClient
	for _, client := range network.clients {
		if !client.leased {
			target = client
			break
		}
	}
	all := map[string]bool{}
	for _, flow := range readFlows(t, network.db) {
		all[flow.srcAddress], all[flow.dstAddress] = true, true
	}
	var addresses []string
	for address := range all {
		addresses = append(addresses, address)
	}
	before := flowEnds(t, network.db)

	mac := "00:00:00:00:fe:02"
	leased, err := network.db.UpsertClient(ctx, Client{
		Identity: ClientIdentity{Kind: IdentityMAC, Key: mac}, InterfaceID: &target.interfaceID,
		MAC: &mac, LastAddress: &target.address,
	}, network.now)
	if err != nil {
		t.Fatalf("writing the leased client: %v", err)
	}
	provider, _ := network.db.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	expiry := network.now + 3600
	if err := network.db.InsertDHCPLease(ctx, provider, DHCPLease{
		ClientID: &leased, Backend: "dnsmasq", Address: target.address, MAC: &mac, LeaseState: "active",
		InterfaceID: &target.interfaceID, GenerationKey: GenerationKeyOf(nil, &expiry, 0),
		ExpiresAt: &expiry, ObservedAt: network.now,
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	result, err := network.db.Reclassify(ctx, addresses, network.now)
	if err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if result.Placed != 1 {
		t.Errorf("%d addresses were placed in full, not the one the lease names", result.Placed)
	}
	targetFlows := queryInt(t, network.db, "SELECT count(*) FROM flow WHERE src_address = ? OR dst_address = ?",
		target.address, target.address)
	if targetFlows == 0 || int64(len(result.FlowInstants)) != targetFlows {
		t.Errorf("the lease moved %d flows, the address has %d", len(result.FlowInstants), targetFlows)
	}
	after := flowEnds(t, network.db)
	for digest, was := range before {
		now := after[digest]
		ends := strings.SplitN(strings.Fields(was)[0], ">", 2)
		touchesTarget := ends[0] == target.address || ends[1] == target.address
		if was != now && !touchesTarget {
			t.Errorf("the flow %s of another address changed from %s to %s", digest, was, now)
		}
	}
}

// TestNetworksAreDetectedWithoutTheLinkLocalPrefix is AC6, its first part, on the store's
// side: a link-local prefix offered as detected is never stored.
func TestNetworksAreDetectedWithoutTheLinkLocalPrefix(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	if err := network.db.DetectNetworks(ctx, network.inside[0].id, prefixesOf(t,
		"192.0.2.129/25", "2001:db8:5::/64", "fe80::/64", "fe80::1/10"), network.now); err != nil {
		t.Fatalf("detecting: %v", err)
	}
	networks, err := network.db.InterfaceNetworks(ctx)
	if err != nil {
		t.Fatalf("reading the networks: %v", err)
	}
	found := map[string]bool{}
	for _, stored := range networks {
		if stored.InterfaceID == network.inside[0].id {
			found[stored.Network.String()] = stored.Origin == NetworkOriginDetected
		}
		if overlapsLinkLocal(stored.Network) {
			t.Errorf("the link-local network %s was stored", stored.Network)
		}
	}
	for _, want := range []string{"192.0.2.128/25", "2001:db8:5::/64"} {
		if !found[want] {
			t.Errorf("the detected network %s is not stored as detected: %v", want, found)
		}
	}
}

// prefixesOf parses ranges a test writes.
func prefixesOf(t *testing.T, texts ...string) []netip.Prefix {
	t.Helper()
	var prefixes []netip.Prefix
	for _, text := range texts {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			t.Fatalf("parsing %s: %v", text, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// placedOn reads the interface a flow end at an address was placed on, or -1.
func placedOn(t *testing.T, database *Store, address string) int64 {
	t.Helper()
	return queryInt(t, database, `SELECT coalesce(max(ifnull(src_interface_id, -1)), -2) FROM flow
		WHERE src_address = ?`, address)
}

// TestOperatorNetworksFollowDecisionD1 is AC6, its second and third parts: detected
// networks count until the operator overrides them, an operator network wins over a
// detected one, a removed one stops counting, and each edit, at the next derivation,
// leaves slots equal to a recomputation.
func TestOperatorNetworksFollowDecisionD1(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	first, second := network.inside[0], network.inside[1]
	// An address inside the first interface's detected network, logged outbound on the
	// upstream interface: no lease, no identity, no sighting on an inside interface.
	address := "10.1.0.200"
	port := int64(443)
	addresses := network.insert(t, []networkFlow{{observedAt: network.now - 30, device: network.upstream.device,
		direction: "out", src: address, dst: outside(1, false), dstPort: &port, protocol: "tcp",
		action: "pass", bytes: 90}})
	network.classifyAndRefresh(t, addresses)
	if got := placedOn(t, network.db, address); got != first.id {
		t.Fatalf("the address is placed on %d, not on the interface whose detected network holds it", got)
	}

	derive := func(at int64) {
		t.Helper()
		result, err := network.db.ReclassifyAll(ctx, at)
		if err != nil {
			t.Fatalf("reclassifying: %v", err)
		}
		if _, err := network.db.RefreshAggregates(ctx, 0, at, result.FlowInstants); err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		settled := derivedSnapshot(t, network.db)
		recomputeEverything(t, network)
		if derivedSnapshot(t, network.db) != settled {
			t.Error("the slots after an edit differ from a recomputation")
		}
	}

	// An operator network on the second interface wins, wider as it is.
	edit, err := network.db.AddInterfaceNetwork(ctx, second.id, "10.1.0.0/16", network.now)
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	if len(edit.Overlaps) == 0 {
		t.Error("an operator network over another interface's detected network was not reported")
	}
	derive(network.now)
	if got := placedOn(t, network.db, address); got != second.id {
		t.Errorf("after the operator's network, the address is placed on %d, not %d", got, second.id)
	}

	// Removed, it no longer counts, and the detected network counts again.
	if _, err := network.db.RemoveInterfaceNetwork(ctx, second.id, "10.1.0.0/16", network.now); err != nil {
		t.Fatalf("removing: %v", err)
	}
	derive(network.now)
	if got := placedOn(t, network.db, address); got != first.id {
		t.Errorf("after removing the operator's network, the address is placed on %d, not %d", got, first.id)
	}

	// The operator removes the detected network itself: the address is outside.
	if _, err := network.db.RemoveInterfaceNetwork(ctx, first.id, "10.1.0.0/24", network.now); err != nil {
		t.Fatalf("removing the detected network: %v", err)
	}
	derive(network.now)
	if got := placedOn(t, network.db, address); got != -1 {
		t.Errorf("with its only network removed, the address is placed on %d", got)
	}
	// Discovery proposing it again does not bring it back.
	if err := network.db.DetectNetworks(ctx, first.id, prefixesOf(t, "10.1.0.0/24", "fd00:1::/64"),
		network.now+60); err != nil {
		t.Fatalf("detecting: %v", err)
	}
	derive(network.now + 60)
	if got := placedOn(t, network.db, address); got != -1 {
		t.Errorf("a removed network came back with the next detection; the address is on %d", got)
	}

	// Confirmed, it counts again, as the operator's.
	edit, err = network.db.ConfirmInterfaceNetwork(ctx, first.id, "10.1.0.0/24", network.now+60)
	if err != nil {
		t.Fatalf("confirming: %v", err)
	}
	if edit.Network.Origin != NetworkOriginOperator || !edit.Network.Counts {
		t.Errorf("the confirmed network is %+v", edit.Network)
	}
	derive(network.now + 60)
	if got := placedOn(t, network.db, address); got != first.id {
		t.Errorf("after confirming, the address is placed on %d, not %d", got, first.id)
	}
}

// TestALinkLocalAddressIsEvidenceOnlyWhereTheRuleSaysSo is AC6, its fourth part.
func TestALinkLocalAddressIsEvidenceOnlyWhereTheRuleSaysSo(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	first, second := network.inside[0], network.inside[1]
	address := "fe80::5"
	port := int64(547)
	addresses := network.insert(t, []networkFlow{
		{observedAt: network.now - 30, device: first.device, direction: "in", src: address,
			dst: outside(2, true), dstPort: &port, protocol: "udp", action: "pass", bytes: 40},
		{observedAt: network.now - 20, device: second.device, direction: "in", src: address,
			dst: outside(3, true), dstPort: &port, protocol: "udp", action: "pass", bytes: 40},
	})
	network.classifyAndRefresh(t, addresses)
	if got := placedOn(t, network.db, address); got != -1 {
		t.Errorf("with the rule at its default, the link-local address is placed on %d", got)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM client WHERE last_address = ?", address); rows != 0 {
		t.Errorf("with the rule at its default, the link-local address has %d client rows", rows)
	}
	for _, step := range []struct {
		on   testInterface
		want int64
	}{{first, first.id}, {second, second.id}} {
		for _, iface := range network.inside {
			if err := network.db.SetLinkLocalEvidence(ctx, iface.id, iface.id == step.on.id); err != nil {
				t.Fatalf("setting the rule: %v", err)
			}
		}
		if _, err := network.db.ReclassifyAll(ctx, network.now); err != nil {
			t.Fatalf("reclassifying: %v", err)
		}
		if got := placedOn(t, network.db, address); got != step.want {
			t.Errorf("with the rule on interface %d only, the address is placed on %d", step.on.id, got)
		}
	}
	if err := network.db.SetLinkLocalEvidence(ctx, 99999, true); !errors.Is(err, ErrUnknownInterface) {
		t.Errorf("setting the rule on no interface returned %v", err)
	}
}

// TestTheOperatorsWritePathRefusesAMalformedNetwork is AC6, its fifth part.
func TestTheOperatorsWritePathRefusesAMalformedNetwork(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 2)
	ctx := context.Background()
	for _, text := range []string{"", "not a network", "10.9.0.1/24", "10.9.0.0/33", "10.9.0.0",
		"2001:db8::1/64", "::ffff:10.9.0.0/120"} {
		if _, err := network.db.AddInterfaceNetwork(ctx, network.inside[0].id, text, network.now); !errors.Is(err, ErrMalformedNetwork) {
			t.Errorf("the range %q was not refused as malformed: %v", text, err)
		}
	}
	for _, text := range []string{"fe80::/64", "fe00::/7", "::/0"} {
		if _, err := network.db.AddInterfaceNetwork(ctx, network.inside[0].id, text, network.now); !errors.Is(err, ErrLinkLocalNetwork) {
			t.Errorf("the range %q, over the link-local prefix, was not refused: %v", text, err)
		}
	}
	if _, err := network.db.AddInterfaceNetwork(ctx, network.upstream.id, "10.9.0.0/24", network.now); !errors.Is(err, ErrUpstreamInterface) {
		t.Errorf("a network on the upstream interface was not refused: %v", err)
	}
	if _, err := network.db.AddInterfaceNetwork(ctx, 99999, "10.9.0.0/24", network.now); !errors.Is(err, ErrUnknownInterface) {
		t.Errorf("a network on no interface was not refused: %v", err)
	}
	if _, err := network.db.RemoveInterfaceNetwork(ctx, network.inside[0].id, "10.9.0.0/24", network.now); !errors.Is(err, ErrUnknownNetwork) {
		t.Errorf("removing a network the interface does not carry returned %v", err)
	}
	edit, err := network.db.AddInterfaceNetwork(ctx, network.inside[1].id, "10.1.0.128/25", network.now)
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	if len(edit.Overlaps) != 1 || edit.Overlaps[0].InterfaceID != network.inside[0].id {
		t.Errorf("the overlap with the first interface's network was reported as %v", edit.Overlaps)
	}
	if rows := queryInt(t, network.db, "SELECT count(*) FROM interface_network WHERE origin = 'operator'"); rows != 1 {
		t.Errorf("%d operator networks are stored, not the one valid one", rows)
	}
}

// TestAHostNameLoggedAsTheClientIsResolvedThroughTheLeases is AC8, on the store's side.
func TestAHostNameLoggedAsTheClientIsResolvedThroughTheLeases(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	provider, _ := network.db.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	lease := func(address, hostname string, expires int64) {
		t.Helper()
		if err := network.db.InsertDHCPLease(ctx, provider, DHCPLease{
			Backend: "dnsmasq", Address: address, Hostname: &hostname, LeaseState: "active",
			GenerationKey: GenerationKeyOf(nil, &expires, 0), ExpiresAt: &expires, ObservedAt: network.now,
		}); err != nil {
			t.Fatalf("writing a lease: %v", err)
		}
	}
	client := network.clients[0]
	// Expiries the network's own leases do not carry, so each is a lease generation of
	// its own.
	lease(client.address, "example-host", network.now+3601)
	lease(network.clients[1].address, "example-twin", network.now+3601)
	lease(network.clients[2].address, "example-twin", network.now+3601)
	lease(network.clients[3].address, "example-gone", network.now-7200)

	for _, step := range []struct {
		logged, address, resolution string
	}{
		{"example-host.example.invalid", client.address, ClientResolutionLeased},
		{"EXAMPLE-HOST", client.address, ClientResolutionLeased},
		{"example-twin.example.invalid", "example-twin.example.invalid", ClientResolutionAmbiguous},
		{"example-gone", "example-gone", ClientResolutionUnknown},
		{"example-nobody", "example-nobody", ClientResolutionUnknown},
	} {
		address, resolution, err := network.db.ResolveLeaseHostname(ctx, step.logged, network.now-60)
		if err != nil {
			t.Fatalf("resolving %s: %v", step.logged, err)
		}
		if address != step.address || resolution != step.resolution {
			t.Errorf("%s resolved to %s (%s), not %s (%s)", step.logged, address, resolution,
				step.address, step.resolution)
		}
	}
}
