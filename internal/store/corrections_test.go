package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The step-5A corrections: AC1 to AC6 and AC8 of specs/SPEC-step-5a-corrections.md, on
// the store's side.

// familyTables names the table of each family of one period.
func familyTable(family string, period vocab.Period) string {
	if family == "volume" {
		return "volume_aggregate_" + period.Name
	}
	return family + "_volume_aggregate_" + period.Name
}

var allFamilies = []string{"volume", "owner", "client", "domain", "rule", "peer"}

// slotBytes sums one family's bytes in the slot of period holding instant.
func slotBytes(t *testing.T, database *store.Store, family string, period vocab.Period, instant int64) int64 {
	t.Helper()
	return storetest.QueryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM "+familyTable(family, period)+
		" WHERE period_start_at = $1", period.SlotStart(instant))
}

// outboundFlow is one flow from a client to an outside address.
func outboundFlow(client storetest.Client, at, bytes int64, peer int, ingested int64) storetest.Flow {
	port := int64(443)
	return storetest.Flow{ObservedAt: at, Device: client.Device, Direction: "in", Src: client.Address,
		Dst: storetest.Outside(peer, client.V6), DstPort: &port, Protocol: "tcp", Action: "pass", Bytes: bytes,
		IngestedAt: ingested}
}

// lookupBefore records a passed lookup by a flow's source just before it, so the flow
// carries a site name and the domain family has a row.
func lookupBefore(t *testing.T, network *storetest.Network, key string, address string, at int64) {
	t.Helper()
	if err := network.DB.InsertDNSResolution(context.Background(), vocab.DNSResolution{
		LookupKey: key, ClientAddress: address, Domain: "example-site.example.invalid",
		Resolver: "unbound", Action: "pass", AnswerSource: storetest.Ptr("Recursion"),
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	if offset := network.Now - vocab.PeriodHour.SlotStart(network.Now); offset != 800 {
		t.Fatalf("the reference instant is %d s into its hour, not 800", offset)
	}
	if err := network.DB.SetSetting(ctx, "retention_seconds", "600", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	client := network.Clients[0]
	at := network.Now - 100
	addresses := network.Insert(t, []storetest.Flow{outboundFlow(client, at, 77, 1, 0)})
	lookupBefore(t, network, "example-lookup-ac1", client.Address, at)
	if _, err := network.DB.Reclassify(ctx, addresses, network.Now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.DB.Attribute(ctx, at-60, at, 5, network.Now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	for _, period := range vocab.Periods() {
		for _, family := range allFamilies {
			if got := slotBytes(t, network.DB, family, period, network.Now); got != 77 {
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
	network := storetest.NewNetwork(t, 3, 7)
	ctx := context.Background()
	// Flows over the last day, dense enough that the current hour holds every kind: the
	// current hour and the earlier hours of the current day, week and month.
	addresses := network.Insert(t, network.FlowsFor(500, 86400))
	network.ClassifyAndRefresh(t, addresses)
	attributeNetwork(t, network)
	flows := readFlows(t, network.DB)

	for _, retention := range []int64{0, 1, 599, 3599, 3600} {
		t.Run(fmt.Sprintf("retention %d", retention), func(t *testing.T) {
			if err := network.DB.SetSetting(ctx, "retention_seconds", fmt.Sprint(retention), network.Now); err != nil {
				t.Fatalf("setting the retention: %v", err)
			}
			for _, table := range aggregateTables() {
				if _, err := network.DB.DB().Exec("DELETE FROM " + table); err != nil {
					t.Fatalf("emptying %s: %v", table, err)
				}
			}
			if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now, nil); err != nil {
				t.Fatalf("refreshing: %v", err)
			}
			for _, period := range vocab.Periods() {
				current := period.SlotStart(network.Now)
				prefix := fmt.Sprintf("%d|", current)
				for _, family := range allFamilies {
					expected := expectedFamily(flows, period, family)
					stored := storedFamily(t, network.DB, period, family)
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	hour := vocab.PeriodHour.SlotStart(network.Now)
	client := network.Clients[0]
	if client.V6 {
		t.Fatal("the first client is expected to be IPv4")
	}
	// The horizon falls 400 s into the current hour.
	if err := network.DB.SetSetting(ctx, "retention_seconds", fmt.Sprint(network.Now-(hour+400)),
		network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	addresses := network.Insert(t, []storetest.Flow{
		outboundFlow(client, hour+100, 100, 1, 0),
		outboundFlow(client, hour+700, 200, 5, 0),
	})
	network.ClassifyAndRefresh(t, addresses)
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if flows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow"); flows != 1 {
		t.Fatalf("%d flows survived the purge, not the one after the horizon", flows)
	}

	// A flow ingested late into the hour, after the purge.
	late := network.Now + 10
	addresses = network.Insert(t, []storetest.Flow{outboundFlow(client, hour+500, 50, 3, late)})
	if _, err := network.DB.Reclassify(ctx, addresses, late); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.DB.RefreshAggregates(ctx, network.Now, late+1, nil); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	for _, family := range []string{"volume", "rule", "client", "owner", "peer"} {
		if got := slotBytes(t, network.DB, family, vocab.PeriodHour, hour); got != 350 {
			t.Errorf("the %s family holds %d bytes in the hour, not its purged 100 plus the 250 present",
				family, got)
		}
	}
	if peers := storetest.QueryInt(t, network.DB, `SELECT distinct_peers FROM client_volume_aggregate_1h
		WHERE period_start_at = $1 AND traffic_direction = 'outbound'`, hour); peers != 3 {
		t.Errorf("the hour counts %d distinct peers, not the purged one and the two present", peers)
	}

	// A reclassification of a flow still present: an operator network places the
	// destination of the 200-byte flow behind the second interface, so that flow moves
	// from outbound to between interfaces, and the purged part stays where it was.
	if _, err := network.DB.AddInterfaceNetwork(ctx, network.Inside[1].ID, storetest.Outside(5, false)+"/32",
		late+20); err != nil {
		t.Fatalf("adding the network: %v", err)
	}
	result, err := network.DB.ReclassifyAll(ctx, late+20)
	if err != nil {
		t.Fatalf("reclassifying: %v", err)
	}
	if len(result.FlowInstants) == 0 {
		t.Fatal("the operator network moved no flow, so the test proves nothing")
	}
	if _, err := network.DB.RefreshAggregates(ctx, late+2, late+30, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	byDirection := func(direction string) int64 {
		return storetest.QueryInt(t, network.DB, `SELECT coalesce(sum(bytes), 0) FROM volume_aggregate_1h
			WHERE period_start_at = $1 AND traffic_direction = $2`, hour, direction)
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
	network := storetest.NewNetwork(t, 3, 9)
	ctx := context.Background()
	if err := network.DB.SetSetting(ctx, "retention_seconds", "3600", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	month := vocab.PeriodMonth.SlotStart(network.Now)
	addresses := network.Insert(t, network.FlowsFor(300, network.Now-month))
	network.ClassifyAndRefresh(t, addresses)
	attributeNetwork(t, network)

	flows := readFlows(t, network.DB)
	for _, period := range []vocab.Period{vocab.PeriodWeek, vocab.PeriodMonth} {
		current := fmt.Sprintf("%d|", period.SlotStart(network.Now))
		for _, family := range allFamilies {
			expected := expectedFamily(flows, period, family)
			stored := storedFamily(t, network.DB, period, family)
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
			rolled := storetest.QueryInt(t, network.DB, "SELECT coalesce(sum(bytes), 0) FROM "+table+
				" WHERE period_start_at = $1", period.SlotStart(network.Now))
			days := storetest.QueryInt(t, network.DB, "SELECT coalesce(sum(bytes), 0) FROM "+familyTable(family, vocab.PeriodDay)+
				" WHERE period_start_at >= $1 AND period_start_at < $2", period.SlotStart(network.Now),
				period.SlotEnd(period.SlotStart(network.Now)))
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	client := network.Clients[0]
	day := vocab.PeriodDay.SlotStart(network.Now)
	yesterday := day - 86400
	if vocab.PeriodMonth.SlotStart(yesterday) != vocab.PeriodMonth.SlotStart(day) {
		t.Fatal("the reference day is the first of its month")
	}
	// Hour one: peers 1 and 2. Hour two: peers 1 and 3. Yesterday: peers 1 and 4. And
	// peer 1 once more at the present instant, which the purge below leaves alone, so
	// the client keeps a flow naming it and is not purged with the rest.
	addresses := network.Insert(t, []storetest.Flow{
		outboundFlow(client, day+60, 10, 1, 0), outboundFlow(client, day+120, 10, 2, 0),
		outboundFlow(client, day+3660, 10, 1, 0), outboundFlow(client, day+3720, 10, 3, 0),
		outboundFlow(client, yesterday+60, 10, 1, 0), outboundFlow(client, yesterday+120, 10, 4, 0),
		outboundFlow(client, network.Now, 10, 1, 0),
	})
	network.ClassifyAndRefresh(t, addresses)
	direct := func(from, to int64) int64 {
		return storetest.QueryInt(t, network.DB, `SELECT count(DISTINCT dst_address) FROM flow
			WHERE src_address = $1 AND observed_at >= $2 AND observed_at < $3`, client.Address, from, to)
	}
	wantDay, wantMonth := direct(day, day+86400), direct(vocab.PeriodMonth.SlotStart(day), network.Now+1)
	if wantDay != 3 || wantMonth != 4 {
		t.Fatalf("the direct counts are %d and %d, not 3 and 4", wantDay, wantMonth)
	}
	peers := func(period vocab.Period) int64 {
		return storetest.QueryInt(t, network.DB, "SELECT coalesce(max(distinct_peers), 0) FROM client_volume_aggregate_"+
			period.Name+" WHERE period_start_at = $1 AND traffic_direction = 'outbound'", period.SlotStart(day))
	}
	check := func(when string) {
		t.Helper()
		if got := peers(vocab.PeriodDay); got != wantDay {
			t.Errorf("%s, the composed day counts %d distinct peers, not %d", when, got, wantDay)
		}
		if got := peers(vocab.PeriodMonth); got != wantMonth {
			t.Errorf("%s, the rolled-up month counts %d distinct peers, not %d", when, got, wantMonth)
		}
	}
	check("with every flow present")

	// Every flow purged; a forced rewrite composes the day and the month from what is left.
	if err := network.DB.SetSetting(ctx, "retention_seconds", "1", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if flows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow"); flows != 1 {
		t.Fatalf("%d flows survived the purge, not the present one", flows)
	}
	forced := []int64{day + 60, day + 3660, yesterday + 60}
	if _, err := network.DB.RefreshAggregates(ctx, 0, network.Now+5, forced); err != nil {
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

// rowVersions fingerprints every row version of the core tables: a row inserted, updated or
// deleted changes it, because PostgreSQL gives every new row version its own location and creating
// transaction, so two equal fingerprints mean nothing was written in between.
func rowVersions(t *testing.T, database *store.Store) string {
	t.Helper()
	ctx := context.Background()
	rows, err := database.DB().QueryContext(ctx,
		"SELECT tablename FROM pg_tables WHERE schemaname = $1 ORDER BY tablename", sqldb.CoreSchema)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	_ = rows.Close()
	var fingerprint strings.Builder
	for _, table := range tables {
		var versions string
		if err := database.DB().QueryRowContext(ctx, "SELECT count(*) || '/' || coalesce(md5(string_agg(ctid::text || ':' || xmin::text, ',' ORDER BY ctid)), '') FROM "+table).
			Scan(&versions); err != nil {
			t.Fatalf("fingerprinting %s: %v", table, err)
		}
		fingerprint.WriteString(table + "=" + versions + " ")
	}
	return fingerprint.String()
}

// TestASecondClassificationPassWithNothingChangedWritesNoRow is AC5, its first part.
func TestASecondClassificationPassWithNothingChangedWritesNoRow(t *testing.T) {
	t.Parallel()
	network := storetest.Populated(t, 2, 6, 200, 86400)
	ctx := context.Background()
	provider, err := network.DB.ProviderID(ctx, "security_event", "suricata")
	if err != nil {
		t.Fatalf("looking up the event provider: %v", err)
	}
	var addresses []string
	for index, client := range network.Clients {
		if err := network.DB.InsertSecurityEvent(ctx, provider, vocab.SecurityEvent{
			ProviderEventKey: fmt.Sprintf("example-event-%d", index), OccurredAt: network.Now - 60,
			IngestedAt: network.Now - 60, RuleIdentity: "1", Signature: "example", EventAction: "allowed",
			SrcAddress: client.Address, DstAddress: storetest.Outside(index, client.V6),
		}); err != nil {
			t.Fatalf("writing an event: %v", err)
		}
		addresses = append(addresses, client.Address, storetest.Outside(index, client.V6))
	}
	for _, flow := range readFlows(t, network.DB) {
		addresses = append(addresses, flow.srcAddress, flow.dstAddress)
	}
	if _, err := network.DB.Reclassify(ctx, addresses, network.Now); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	before := rowVersions(t, network.DB)
	second, err := network.DB.Reclassify(ctx, addresses, network.Now)
	if err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if after := rowVersions(t, network.DB); after != before {
		t.Errorf("a second pass with no evidence changed wrote rows:\n before %s\n after  %s", before, after)
	}
	if len(second.FlowInstants) != 0 || len(second.LookupInstants) != 0 || second.Placed != 0 {
		t.Errorf("a second pass changed %d flows and %d lookups and placed %d addresses in full",
			len(second.FlowInstants), len(second.LookupInstants), second.Placed)
	}
	if records := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM address_classification"); records == 0 {
		t.Error("no evidence was recorded, so the pass cannot be incremental")
	}
}

// flowEnds renders every flow's placed ends, keyed by digest.
func flowEnds(t *testing.T, database *store.Store) map[string]string {
	t.Helper()
	rows, err := database.DB().Query(`SELECT log_digest, src_address, dst_address,
		coalesce(src_interface_id, -1), coalesce(dst_interface_id, -1), coalesce(src_client_id, -1),
		coalesce(dst_client_id, -1), traffic_scope FROM flow`)
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
	network := storetest.Populated(t, 2, 6, 200, 86400)
	ctx := context.Background()
	var target storetest.Client
	for _, client := range network.Clients {
		if !client.Leased {
			target = client
			break
		}
	}
	all := map[string]bool{}
	for _, flow := range readFlows(t, network.DB) {
		all[flow.srcAddress], all[flow.dstAddress] = true, true
	}
	var addresses []string
	for address := range all {
		addresses = append(addresses, address)
	}
	before := flowEnds(t, network.DB)

	mac := "00:00:00:00:fe:02"
	leased, err := network.DB.UpsertClient(ctx, vocab.Client{
		Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac}, InterfaceID: &target.InterfaceID,
		MAC: &mac, LastAddress: &target.Address,
	}, network.Now)
	if err != nil {
		t.Fatalf("writing the leased client: %v", err)
	}
	provider, _ := network.DB.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	expiry := network.Now + 3600
	if err := network.DB.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
		ClientID: &leased, Backend: "dnsmasq", Address: target.Address, MAC: &mac, LeaseState: "active",
		InterfaceID: &target.InterfaceID, GenerationKey: vocab.GenerationKeyOf(nil, &expiry, 0),
		ExpiresAt: &expiry, ObservedAt: network.Now,
	}); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
	result, err := network.DB.Reclassify(ctx, addresses, network.Now)
	if err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if result.Placed != 1 {
		t.Errorf("%d addresses were placed in full, not the one the lease names", result.Placed)
	}
	targetFlows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow WHERE src_address = $1 OR dst_address = $2",
		target.Address, target.Address)
	if targetFlows == 0 || int64(len(result.FlowInstants)) != targetFlows {
		t.Errorf("the lease moved %d flows, the address has %d", len(result.FlowInstants), targetFlows)
	}
	after := flowEnds(t, network.DB)
	for digest, was := range before {
		now := after[digest]
		ends := strings.SplitN(strings.Fields(was)[0], ">", 2)
		touchesTarget := ends[0] == target.Address || ends[1] == target.Address
		if was != now && !touchesTarget {
			t.Errorf("the flow %s of another address changed from %s to %s", digest, was, now)
		}
	}
}

// placedOn reads the interface a flow end at an address was placed on, or -1.
func placedOn(t *testing.T, database *store.Store, address string) int64 {
	t.Helper()
	return storetest.QueryInt(t, database, `SELECT coalesce(max(coalesce(src_interface_id, -1)), -2) FROM flow
		WHERE src_address = $1`, address)
}

// TestOperatorNetworksFollowDecisionD1 is AC6, its second and third parts: detected
// networks count until the operator overrides them, an operator network wins over a
// detected one, a removed one stops counting, and each edit, at the next derivation,
// leaves slots equal to a recomputation.
func TestOperatorNetworksFollowDecisionD1(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 2)
	ctx := context.Background()
	first, second := network.Inside[0], network.Inside[1]
	// An address inside the first interface's detected network, logged outbound on the
	// upstream interface: no lease, no identity, no sighting on an inside interface.
	address := "10.1.0.200"
	port := int64(443)
	addresses := network.Insert(t, []storetest.Flow{{ObservedAt: network.Now - 30, Device: network.Upstream.Device,
		Direction: "out", Src: address, Dst: storetest.Outside(1, false), DstPort: &port, Protocol: "tcp",
		Action: "pass", Bytes: 90}})
	network.ClassifyAndRefresh(t, addresses)
	if got := placedOn(t, network.DB, address); got != first.ID {
		t.Fatalf("the address is placed on %d, not on the interface whose detected network holds it", got)
	}

	derive := func(at int64) {
		t.Helper()
		result, err := network.DB.ReclassifyAll(ctx, at)
		if err != nil {
			t.Fatalf("reclassifying: %v", err)
		}
		if _, err := network.DB.RefreshAggregates(ctx, 0, at, result.FlowInstants); err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		settled := derivedSnapshot(t, network.DB)
		recomputeEverything(t, network)
		if derivedSnapshot(t, network.DB) != settled {
			t.Error("the slots after an edit differ from a recomputation")
		}
	}

	// An operator network on the second interface wins, wider as it is.
	edit, err := network.DB.AddInterfaceNetwork(ctx, second.ID, "10.1.0.0/16", network.Now)
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	if len(edit.Overlaps) == 0 {
		t.Error("an operator network over another interface's detected network was not reported")
	}
	derive(network.Now)
	if got := placedOn(t, network.DB, address); got != second.ID {
		t.Errorf("after the operator's network, the address is placed on %d, not %d", got, second.ID)
	}

	// Removed, it no longer counts, and the detected network counts again.
	if _, err := network.DB.RemoveInterfaceNetwork(ctx, second.ID, "10.1.0.0/16", network.Now); err != nil {
		t.Fatalf("removing: %v", err)
	}
	derive(network.Now)
	if got := placedOn(t, network.DB, address); got != first.ID {
		t.Errorf("after removing the operator's network, the address is placed on %d, not %d", got, first.ID)
	}

	// The operator removes the detected network itself: the address is outside.
	if _, err := network.DB.RemoveInterfaceNetwork(ctx, first.ID, "10.1.0.0/24", network.Now); err != nil {
		t.Fatalf("removing the detected network: %v", err)
	}
	derive(network.Now)
	if got := placedOn(t, network.DB, address); got != -1 {
		t.Errorf("with its only network removed, the address is placed on %d", got)
	}
	// Discovery proposing it again does not bring it back.
	if err := network.DB.DetectNetworks(ctx, first.ID, storetest.Prefixes(t, "10.1.0.0/24", "fd00:1::/64"),
		network.Now+60); err != nil {
		t.Fatalf("detecting: %v", err)
	}
	derive(network.Now + 60)
	if got := placedOn(t, network.DB, address); got != -1 {
		t.Errorf("a removed network came back with the next detection; the address is on %d", got)
	}

	// Confirmed, it counts again, as the operator's.
	edit, err = network.DB.ConfirmInterfaceNetwork(ctx, first.ID, "10.1.0.0/24", network.Now+60)
	if err != nil {
		t.Fatalf("confirming: %v", err)
	}
	if edit.Network.Origin != flows.NetworkOriginOperator || !edit.Network.Counts {
		t.Errorf("the confirmed network is %+v", edit.Network)
	}
	derive(network.Now + 60)
	if got := placedOn(t, network.DB, address); got != first.ID {
		t.Errorf("after confirming, the address is placed on %d, not %d", got, first.ID)
	}
}

// TestALinkLocalAddressIsEvidenceOnlyWhereTheRuleSaysSo is AC6, its fourth part.
func TestALinkLocalAddressIsEvidenceOnlyWhereTheRuleSaysSo(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 2)
	ctx := context.Background()
	first, second := network.Inside[0], network.Inside[1]
	address := "fe80::5"
	port := int64(547)
	addresses := network.Insert(t, []storetest.Flow{
		{ObservedAt: network.Now - 30, Device: first.Device, Direction: "in", Src: address,
			Dst: storetest.Outside(2, true), DstPort: &port, Protocol: "udp", Action: "pass", Bytes: 40},
		{ObservedAt: network.Now - 20, Device: second.Device, Direction: "in", Src: address,
			Dst: storetest.Outside(3, true), DstPort: &port, Protocol: "udp", Action: "pass", Bytes: 40},
	})
	network.ClassifyAndRefresh(t, addresses)
	if got := placedOn(t, network.DB, address); got != -1 {
		t.Errorf("with the rule at its default, the link-local address is placed on %d", got)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM client WHERE last_address = $1", address); rows != 0 {
		t.Errorf("with the rule at its default, the link-local address has %d client rows", rows)
	}
	for _, step := range []struct {
		on   storetest.Interface
		want int64
	}{{first, first.ID}, {second, second.ID}} {
		for _, iface := range network.Inside {
			if err := network.DB.SetLinkLocalEvidence(ctx, iface.ID, iface.ID == step.on.ID); err != nil {
				t.Fatalf("setting the rule: %v", err)
			}
		}
		if _, err := network.DB.ReclassifyAll(ctx, network.Now); err != nil {
			t.Fatalf("reclassifying: %v", err)
		}
		if got := placedOn(t, network.DB, address); got != step.want {
			t.Errorf("with the rule on interface %d only, the address is placed on %d", step.on.ID, got)
		}
	}
	if err := network.DB.SetLinkLocalEvidence(ctx, 99999, true); !errors.Is(err, flows.ErrUnknownInterface) {
		t.Errorf("setting the rule on no interface returned %v", err)
	}
}

// TestTheOperatorsWritePathRefusesAMalformedNetwork is AC6, its fifth part.
func TestTheOperatorsWritePathRefusesAMalformedNetwork(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 2)
	ctx := context.Background()
	for _, text := range []string{"", "not a network", "10.9.0.1/24", "10.9.0.0/33", "10.9.0.0",
		"2001:db8::1/64", "::ffff:10.9.0.0/120"} {
		if _, err := network.DB.AddInterfaceNetwork(ctx, network.Inside[0].ID, text, network.Now); !errors.Is(err, flows.ErrMalformedNetwork) {
			t.Errorf("the range %q was not refused as malformed: %v", text, err)
		}
	}
	for _, text := range []string{"fe80::/64", "fe00::/7", "::/0"} {
		if _, err := network.DB.AddInterfaceNetwork(ctx, network.Inside[0].ID, text, network.Now); !errors.Is(err, flows.ErrLinkLocalNetwork) {
			t.Errorf("the range %q, over the link-local prefix, was not refused: %v", text, err)
		}
	}
	if _, err := network.DB.AddInterfaceNetwork(ctx, network.Upstream.ID, "10.9.0.0/24", network.Now); !errors.Is(err, flows.ErrUpstreamInterface) {
		t.Errorf("a network on the upstream interface was not refused: %v", err)
	}
	if _, err := network.DB.AddInterfaceNetwork(ctx, 99999, "10.9.0.0/24", network.Now); !errors.Is(err, flows.ErrUnknownInterface) {
		t.Errorf("a network on no interface was not refused: %v", err)
	}
	if _, err := network.DB.RemoveInterfaceNetwork(ctx, network.Inside[0].ID, "10.9.0.0/24", network.Now); !errors.Is(err, flows.ErrUnknownNetwork) {
		t.Errorf("removing a network the interface does not carry returned %v", err)
	}
	edit, err := network.DB.AddInterfaceNetwork(ctx, network.Inside[1].ID, "10.1.0.128/25", network.Now)
	if err != nil {
		t.Fatalf("adding: %v", err)
	}
	if len(edit.Overlaps) != 1 || edit.Overlaps[0].InterfaceID != network.Inside[0].ID {
		t.Errorf("the overlap with the first interface's network was reported as %v", edit.Overlaps)
	}
	if rows := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM interface_network WHERE origin = 'operator'"); rows != 1 {
		t.Errorf("%d operator networks are stored, not the one valid one", rows)
	}
}

// TestAHostNameLoggedAsTheClientIsResolvedThroughTheLeases is AC8, on the store's side.
func TestAHostNameLoggedAsTheClientIsResolvedThroughTheLeases(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	provider, _ := network.DB.ProviderID(ctx, "dhcp_lease", "dnsmasq")
	lease := func(address, hostname string, expires int64) {
		t.Helper()
		if err := network.DB.InsertDHCPLease(ctx, provider, vocab.DHCPLease{
			Backend: "dnsmasq", Address: address, Hostname: &hostname, LeaseState: "active",
			GenerationKey: vocab.GenerationKeyOf(nil, &expires, 0), ExpiresAt: &expires, ObservedAt: network.Now,
		}); err != nil {
			t.Fatalf("writing a lease: %v", err)
		}
	}
	client := network.Clients[0]
	// Expiries the network's own leases do not carry, so each is a lease generation of
	// its own.
	lease(client.Address, "example-host", network.Now+3601)
	lease(network.Clients[1].Address, "example-twin", network.Now+3601)
	lease(network.Clients[2].Address, "example-twin", network.Now+3601)
	lease(network.Clients[3].Address, "example-gone", network.Now-7200)

	for _, step := range []struct {
		logged, address, resolution string
	}{
		{"example-host.example.invalid", client.Address, vocab.ClientResolutionLeased},
		{"EXAMPLE-HOST", client.Address, vocab.ClientResolutionLeased},
		{"example-twin.example.invalid", "example-twin.example.invalid", vocab.ClientResolutionAmbiguous},
		{"example-gone", "example-gone", vocab.ClientResolutionUnknown},
		{"example-nobody", "example-nobody", vocab.ClientResolutionUnknown},
	} {
		address, resolution, err := network.DB.ResolveLeaseHostname(ctx, step.logged, network.Now-60)
		if err != nil {
			t.Fatalf("resolving %s: %v", step.logged, err)
		}
		if address != step.address || resolution != step.resolution {
			t.Errorf("%s resolved to %s (%s), not %s (%s)", step.logged, address, resolution,
				step.address, step.resolution)
		}
	}
}
