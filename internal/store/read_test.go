package store

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// The read functions: AC2, AC12, AC15, AC16, AC22, AC23, AC24, AC25 and AC30.

// directWindow computes a window's totals over flow alone, independently of the slots.
func directWindow(t *testing.T, database *Store, from, to int64) map[string]VolumeFigures {
	t.Helper()
	rows, err := database.DB().Query(`SELECT traffic_direction,
		sum(packet_bytes), sum(CASE WHEN action = 'pass' THEN packet_bytes ELSE 0 END),
		sum(CASE WHEN action IN ('block', 'reject') THEN packet_bytes ELSE 0 END),
		sum(CASE WHEN action = 'unknown' THEN packet_bytes ELSE 0 END),
		sum(CASE WHEN action = 'pass' THEN 1 ELSE 0 END),
		sum(CASE WHEN action IN ('block', 'reject') THEN 1 ELSE 0 END),
		sum(CASE WHEN action = 'unknown' THEN 1 ELSE 0 END)
		FROM classified_flow WHERE observed_at >= ? AND observed_at < ? GROUP BY traffic_direction`, from, to)
	if err != nil {
		t.Fatalf("computing a window directly: %v", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]VolumeFigures{}
	for rows.Next() {
		var (
			direction string
			figures   VolumeFigures
		)
		if err := rows.Scan(&direction, &figures.Bytes, &figures.AllowedBytes, &figures.BlockedBytes,
			&figures.UnknownBytes, &figures.AllowedConnections, &figures.BlockedConnections,
			&figures.UnknownConnections); err != nil {
			t.Fatalf("computing a window directly: %v", err)
		}
		result[direction] = figures
	}
	return result
}

// TestTheRollingWindowEqualsADirectComputationInsideTheHorizon is AC22, both halves.
func TestTheRollingWindowEqualsADirectComputationInsideTheHorizon(t *testing.T) {
	network := populated(t, 3, 10, 1200, 10*86400)
	ctx := context.Background()
	random := rand.New(rand.NewSource(5))
	for draw := 0; draw < 60; draw++ {
		from := network.now - random.Int63n(10*86400)
		to := from + 1 + random.Int63n(3*86400)
		window, err := network.db.ReadVolumeWindow(ctx, from, to, network.now)
		if err != nil {
			t.Fatalf("reading a window: %v", err)
		}
		direct := directWindow(t, network.db, from, to)
		got := window.ByDirection()
		for _, direction := range []string{"outbound", "inbound", "inter_interface"} {
			if got[direction] != direct[direction] {
				t.Errorf("window [%d, %d) %s: assembled %+v, direct %+v", from, to, direction,
					got[direction], direct[direction])
			}
		}
	}

	// Beyond the horizon: the flows are purged, the slots survive, and the function reads
	// them and says what it covers.
	horizon := network.now - 4*86400
	whole := PeriodHour.SlotStart(horizon) + 3600
	if _, err := network.db.DB().Exec("DELETE FROM flow WHERE observed_at < ?", whole); err != nil {
		t.Fatalf("purging the old flows: %v", err)
	}
	from := network.now - 8*86400 + 1234
	to := network.now - 2*86400 + 77
	window, err := network.db.ReadVolumeWindow(ctx, from, to, network.now)
	if err != nil {
		t.Fatalf("reading a window across the horizon: %v", err)
	}
	if window.Covered == nil {
		t.Fatal("a window over slots reports no coverage")
	}
	firstHour := PeriodHour.SlotStart(from) + 3600
	if window.Covered.From != firstHour || window.Covered.To != to {
		t.Errorf("the window [%d, %d) reports coverage [%d, %d), not [%d, %d): its left edge's flows "+
			"are gone and its hours are slots", from, to, window.Covered.From, window.Covered.To, firstHour, to)
	}
	if window.Total().Bytes == 0 {
		t.Error("the slots beyond the horizon contributed nothing")
	}
	var slotBytes int64
	if err := network.db.DB().QueryRow(`SELECT sum(bytes) FROM volume_aggregate_1h
		WHERE period_start_at >= ? AND period_start_at < ?`, firstHour, PeriodHour.SlotStart(to)).Scan(&slotBytes); err != nil {
		t.Fatalf("summing the slots: %v", err)
	}
	right := directWindow(t, network.db, PeriodHour.SlotStart(to), to)
	var rightBytes int64
	for _, figures := range right {
		rightBytes += figures.Bytes
	}
	if window.Total().Bytes != slotBytes+rightBytes {
		t.Errorf("beyond the horizon the window sums %d bytes, its slots and right edge %d",
			window.Total().Bytes, slotBytes+rightBytes)
	}

	// A window before anything was kept covers nothing.
	empty, err := network.db.ReadVolumeWindow(ctx, network.now-400*86400, network.now-300*86400, network.now)
	if err != nil {
		t.Fatalf("reading an empty window: %v", err)
	}
	if empty.Covered != nil {
		t.Errorf("a window before any data reports coverage %+v", *empty.Covered)
	}
}

// TestASeriesSumsToItsWindowAndMarksItsGaps is AC23.
func TestASeriesSumsToItsWindowAndMarksItsGaps(t *testing.T) {
	network := populated(t, 2, 6, 600, 3*86400)
	ctx := context.Background()
	from := network.now - 2*86400 - 1700
	to := network.now + 2*3600
	// A collection gap over two hours of the window.
	gapStart := PeriodHour.SlotStart(network.now-30*3600) + 600
	providerID, _ := network.db.ProviderID(ctx, "firewall_log", "pf")
	if err := network.db.RecordCollectionGap(ctx, CollectionGap{ProviderID: providerID,
		IntervalStartAt: gapStart, IntervalEndAt: gapStart + 3600, Reason: GapDigestOutsideWindow,
		DetectedAt: network.now}); err != nil {
		t.Fatalf("recording a gap: %v", err)
	}
	// And an hour with no flow at all inside the covered interval.
	silent := PeriodHour.SlotStart(network.now - 10*3600)
	if _, err := network.db.DB().Exec("DELETE FROM flow WHERE observed_at >= ? AND observed_at < ?",
		silent, silent+3600); err != nil {
		t.Fatalf("emptying an hour: %v", err)
	}
	// A flow is removed only by the retention purge, which removes the slots with it; this
	// test removes some by hand, so it forces their hour.
	if _, err := network.db.RefreshAggregates(ctx, 0, network.now,
		append(allFlowInstants(t, network.db), silent)); err != nil {
		t.Fatalf("recomputing: %v", err)
	}

	buckets, err := network.db.ReadVolumeSeries(ctx, from, to, PeriodHour, network.now)
	if err != nil {
		t.Fatalf("reading the series: %v", err)
	}
	window, err := network.db.ReadVolumeWindow(ctx, from, to, network.now)
	if err != nil {
		t.Fatalf("reading the window: %v", err)
	}
	var sum int64
	var gaps, zeroes int
	for _, bucket := range buckets {
		if bucket.From%3600 != 0 && bucket.From != from {
			t.Errorf("a bucket starts off a slot boundary: %d", bucket.From)
		}
		if bucket.Total != nil {
			sum += bucket.Total.Bytes
		}
		overlapsGap := bucket.From < gapStart+3600 && bucket.To >= gapStart
		future := bucket.To > network.now
		switch {
		case overlapsGap || future:
			gaps++
			if !bucket.Gap {
				t.Errorf("the bucket [%d, %d) is not marked a gap", bucket.From, bucket.To)
			}
			if future && bucket.From >= network.now && bucket.Total != nil {
				t.Errorf("a bucket after now carries a figure: %+v", *bucket.Total)
			}
		case bucket.From == silent:
			zeroes++
			if bucket.Gap || bucket.Total == nil || bucket.Total.Bytes != 0 {
				t.Errorf("a covered hour with no flow is %+v, not a zero", bucket)
			}
		}
	}
	if gaps == 0 || zeroes != 1 {
		t.Errorf("the series exercised %d gaps and %d zero buckets", gaps, zeroes)
	}
	if sum != window.Total().Bytes {
		t.Errorf("the buckets sum to %d bytes, the window to %d", sum, window.Total().Bytes)
	}
}

// TestEveryFlowLandsInExactlyOneNodePerLevelOfTheTree is AC24, the rule of
// docs/mockups/check-tree-reconciliation.js: children sum to their parent, and each side's
// first level sums to every flow of the window.
func TestEveryFlowLandsInExactlyOneNodePerLevelOfTheTree(t *testing.T) {
	network := populated(t, 3, 9, 700, 2*86400)
	attributeNetwork(t, network)
	ctx := context.Background()
	if _, err := network.db.DB().Exec(`INSERT INTO owner (id, display_name, created_at, updated_at)
		VALUES (1, 'example owner', 0, 0)`); err != nil {
		t.Fatalf("writing an owner: %v", err)
	}
	if _, err := network.db.DB().Exec(`UPDATE client SET owner_id = 1, owner_assigned_at = 0 WHERE id % 3 = 0`); err != nil {
		t.Fatalf("assigning an owner: %v", err)
	}
	if _, err := network.db.DB().Exec(`INSERT INTO geo_asn (address, provider_id, lookup_state,
		country_code, asn, operator, dataset_build_at, looked_up_at)
		SELECT DISTINCT dst_address, (SELECT id FROM provider WHERE kind = 'geo_asn'), 'resolved', 'ZZ',
		       64500 + (length(dst_address) % 3), 'example operator', 1, 1
		FROM flow WHERE dst_interface_id IS NULL AND id % 3 = 0`); err != nil {
		t.Fatalf("placing addresses: %v", err)
	}
	if _, err := network.db.DB().Exec(`INSERT OR IGNORE INTO geo_asn (address, lookup_state, looked_up_at)
		SELECT DISTINCT dst_address, 'miss', 1 FROM flow WHERE dst_interface_id IS NULL AND id % 3 = 1`); err != nil {
		t.Fatalf("recording misses: %v", err)
	}
	from, to := network.now-86400, network.now
	var flows, bytes, blocked int64
	if err := network.db.DB().QueryRow(`SELECT count(*), sum(packet_bytes),
		sum(CASE WHEN action IN ('block', 'reject') THEN 1 ELSE 0 END) FROM flow
		WHERE observed_at >= ? AND observed_at < ?`, from, to).Scan(&flows, &bytes, &blocked); err != nil {
		t.Fatalf("counting the window: %v", err)
	}
	for _, root := range []TreeRoot{TreeRootInterface, TreeRootClient, TreeRootOwner} {
		tree, err := network.db.ReadConnectionTree(ctx, from, to, root)
		if err != nil {
			t.Fatalf("reading the %s tree: %v", root, err)
		}
		for side, nodes := range map[string][]*TreeNode{"inside": tree.Inside, "outside": tree.Outside} {
			var levelBytes, levelConnections, levelBlocked int64
			keys := map[string]bool{}
			for _, node := range nodes {
				if keys[node.Key] {
					t.Errorf("%s root, %s side: the node %s appears twice", root, side, node.Key)
				}
				keys[node.Key] = true
				levelBytes += node.Bytes
				levelConnections += node.Connections
				levelBlocked += node.Blocked
				if len(node.Children) == 0 {
					continue
				}
				var childBytes, childConnections, childBlocked int64
				for _, child := range node.Children {
					childBytes += child.Bytes
					childConnections += child.Connections
					childBlocked += child.Blocked
				}
				if childBytes != node.Bytes || childConnections != node.Connections || childBlocked != node.Blocked {
					t.Errorf("%s root, %s side: the children of %s sum to %d bytes, %d connections, %d "+
						"blocked; the node holds %d, %d, %d", root, side, node.Key, childBytes,
						childConnections, childBlocked, node.Bytes, node.Connections, node.Blocked)
				}
			}
			if levelBytes != bytes || levelConnections != flows || levelBlocked != blocked {
				t.Errorf("%s root, %s side: the first level holds %d bytes in %d connections, the "+
					"window %d in %d", root, side, levelBytes, levelConnections, bytes, flows)
			}
		}
		if root == TreeRootInterface {
			found := map[string]bool{}
			for _, node := range tree.Outside {
				found[node.Key[:min(len(node.Key), 8)]] = true
				for _, child := range node.Children {
					found[child.Key[:min(len(child.Key), 5)]] = true
				}
			}
			for _, prefix := range []string{"operator", "unplaced", "inter_in", "site:"} {
				if !found[prefix] {
					t.Errorf("the outside side holds no %s node, so the case is not exercised", prefix)
				}
			}
		}
	}
	if _, err := network.db.ReadConnectionTree(ctx, from, to, TreeRoot("example-root")); err == nil {
		t.Error("an unknown root was accepted")
	}
}

// TestEveryRefusalAppearsOnceUnderTheExtendedVocabulary is AC25.
func TestEveryRefusalAppearsOnceUnderTheExtendedVocabulary(t *testing.T) {
	network := populated(t, 2, 6, 400, 86400)
	ctx := context.Background()
	purposes := []any{"advertising", "tracking", "threat", "parental", "other", nil}
	for index, purpose := range purposes {
		var assigned any
		if purpose != nil {
			assigned = network.now
		}
		if _, err := network.db.DB().Exec(`INSERT INTO blocklist (id, name, purpose, purpose_assigned_at,
			first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, 0, 0)`,
			index+1, fmt.Sprintf("example-list-%d", index), purpose, assigned); err != nil {
			t.Fatalf("writing a list: %v", err)
		}
	}
	client := network.clients[0]
	for index := 0; index < 14; index++ {
		var listID any
		if index%7 != 6 {
			listID = index%7 + 1
		}
		action := "block"
		if index%2 == 1 {
			action = "drop"
		}
		if _, err := network.db.DB().Exec(`INSERT INTO dns_resolution (lookup_key, client_address, domain,
			resolver, action, blocklist_id, looked_up_at, ingested_at)
			VALUES (?, ?, 'blocked.example.invalid', 'unbound', ?, ?, ?, ?)`,
			fmt.Sprintf("example-blocked-%d", index), client.address, action, listID,
			network.now-100-int64(index), network.now); err != nil {
			t.Fatalf("writing a blocked lookup: %v", err)
		}
	}
	eventProvider, _ := network.db.ProviderID(ctx, "security_event", "suricata")
	for index := 0; index < 4; index++ {
		action := "blocked"
		if index == 3 {
			action = "allowed"
		}
		if err := network.db.InsertSecurityEvent(ctx, eventProvider, SecurityEvent{
			ProviderEventKey: fmt.Sprintf("example-blocked-event-%d", index), OccurredAt: network.now - 50,
			IngestedAt: network.now, RuleIdentity: "1", Signature: "example", EventAction: action,
			SrcAddress: outside(index, false), DstAddress: network.upstream.address,
		}); err != nil {
			t.Fatalf("writing an event: %v", err)
		}
	}

	direct := map[string]int64{}
	add := func(kind string, query string) {
		direct[kind] += queryInt(t, network.db, query)
	}
	add("firewall_rule", "SELECT count(*) FROM flow WHERE action IN ('block', 'reject') AND log_reason = 'match'")
	add("firewall_no_rule", "SELECT count(*) FROM flow WHERE action IN ('block', 'reject') AND log_reason = 'state-mismatch'")
	add("firewall_reason_not_recorded", `SELECT count(*) FROM flow WHERE action IN ('block', 'reject')
		AND (log_reason IS NULL OR log_reason = 'unknown(16)')`)
	for index, purpose := range []string{"advertising", "tracking", "threat", "parental", "other"} {
		add("dns_"+purpose+"_list", fmt.Sprintf(
			"SELECT count(*) FROM dns_resolution WHERE action IN ('block', 'drop') AND blocklist_id = %d", index+1))
	}
	add("dns_unassigned_list", "SELECT count(*) FROM dns_resolution WHERE action IN ('block', 'drop') AND blocklist_id = 6")
	add("dns_list_not_recorded", "SELECT count(*) FROM dns_resolution WHERE action IN ('block', 'drop') AND blocklist_id IS NULL")
	add("security_engine", "SELECT count(*) FROM security_event WHERE event_action = 'blocked'")

	counts, err := network.db.ReadBlockedDecisionCounts(ctx, 0, network.now+1)
	if err != nil {
		t.Fatalf("counting the refusals: %v", err)
	}
	for kind, want := range direct {
		if want == 0 {
			t.Errorf("the network holds no %s refusal, so the kind is not exercised", kind)
		}
		if counts[kind] != want {
			t.Errorf("blocked_decision counts %d %s refusals, a direct count %d", counts[kind], kind, want)
		}
	}
	for kind := range counts {
		if _, present := direct[kind]; !present {
			t.Errorf("blocked_decision invents the kind %s", kind)
		}
	}
	if duplicates := queryInt(t, network.db, `SELECT count(*) FROM (SELECT source_table, source_id
		FROM blocked_decision GROUP BY 1, 2 HAVING count(*) > 1)`); duplicates != 0 {
		t.Errorf("%d refusals appear more than once", duplicates)
	}
	total := queryInt(t, network.db, "SELECT count(*) FROM flow WHERE action IN ('block', 'reject')") +
		queryInt(t, network.db, "SELECT count(*) FROM dns_resolution WHERE action IN ('block', 'drop')") +
		queryInt(t, network.db, "SELECT count(*) FROM security_event WHERE event_action = 'blocked'")
	if all := queryInt(t, network.db, "SELECT count(*) FROM blocked_decision"); all != total {
		t.Errorf("blocked_decision holds %d rows for %d refusals", all, total)
	}
	if toFirewall := queryInt(t, network.db, `SELECT count(*) FROM blocked_decision
		WHERE source_table = 'security_event' AND target_is_this_firewall = 1`); toFirewall != 3 {
		t.Errorf("%d refusals aimed at the firewall's own address say so, not 3", toFirewall)
	}
}

// TestTheAttributionRateIsUndefinedWithoutAReachableResolver is AC30.
func TestTheAttributionRateIsUndefinedWithoutAReachableResolver(t *testing.T) {
	network := populated(t, 2, 4, 300, 5*86400)
	ctx := context.Background()
	rate, err := network.db.ReadAttributionRate(ctx, network.now-86400, network.now+1, nil)
	if err != nil {
		t.Fatalf("reading the rate: %v", err)
	}
	if rate.ResolverCovered || rate.Rate != nil {
		t.Errorf("with no reachable resolver the rate is %v, not undefined", rate.Rate)
	}
	if rate.EligibleFlows == 0 {
		t.Fatal("no eligible flow, so the rate is not exercised")
	}
	// The eligible flows are those a client sent outside, and no others: a flow with both
	// ends outside, which classified_flow may call inbound, is not one, and the count is the
	// one the per-client diagnostic sums to.
	unsent := queryInt(t, network.db, `SELECT count(*) FROM flow WHERE observed_at >= ? AND observed_at < ?
		AND dst_interface_id IS NULL AND src_client_id IS NULL`, network.now-86400, network.now+1)
	if unsent == 0 {
		t.Fatal("the window holds no flow with both ends outside, so the eligibility rule has no teeth")
	}
	if sent := queryInt(t, network.db, `SELECT count(*) FROM flow WHERE observed_at >= ? AND observed_at < ?
		AND dst_interface_id IS NULL AND src_client_id IS NOT NULL`, network.now-86400, network.now+1); rate.EligibleFlows != sent {
		t.Errorf("the rate counts %d eligible flows, not the %d a client sent outside", rate.EligibleFlows, sent)
	}
	unbound, _ := network.db.ProviderID(ctx, "dns_lookup", "unbound")
	if err := network.db.SetAvailability(ctx, unbound, StateReachable, "example probe", nil, network.now); err != nil {
		t.Fatalf("recording the resolver reachable: %v", err)
	}
	rate, err = network.db.ReadAttributionRate(ctx, network.now-86400, network.now+1, nil)
	if err != nil {
		t.Fatalf("reading the rate: %v", err)
	}
	if rate.Rate == nil || *rate.Rate != 0 {
		t.Errorf("with a reachable resolver that named nothing the rate is %v, not 0", rate.Rate)
	}
	if rate.MeanDelaySeconds != nil || rate.MaxDelaySeconds != nil {
		t.Error("a delay is reported where nothing was attributed")
	}

	// OVER THE WINDOW, not now. A past window holding no lookup is undefined although the
	// resolver is reachable now: nothing says it answered then.
	past := network.now - 3*86400
	rate, err = network.db.ReadAttributionRate(ctx, past, past+86400, nil)
	if err != nil {
		t.Fatalf("reading a past rate: %v", err)
	}
	if rate.EligibleFlows == 0 {
		t.Fatal("no eligible flow in the past window, so the case is not exercised")
	}
	if rate.ResolverCovered || rate.Rate != nil {
		t.Errorf("a past window with no lookup reads %v although only today's probe was reachable", rate.Rate)
	}
	// And a past window holding a lookup is defined although the resolver is unreachable now.
	if err := network.db.InsertDNSResolution(ctx, DNSResolution{
		LookupKey: "example-past-lookup", ClientAddress: network.clients[0].address,
		Domain: "past.example.invalid", Resolver: "unbound", Action: "pass",
		AnswerSource: ptr("Recursion"), LookedUpAt: past + 600, IngestedAt: past + 700,
	}); err != nil {
		t.Fatalf("writing a past lookup: %v", err)
	}
	if err := network.db.SetAvailability(ctx, unbound, StateUnavailable, "example probe", nil, network.now); err != nil {
		t.Fatalf("recording the resolver unavailable: %v", err)
	}
	rate, err = network.db.ReadAttributionRate(ctx, past, past+86400, nil)
	if err != nil {
		t.Fatalf("reading a past rate: %v", err)
	}
	if !rate.ResolverCovered || rate.Rate == nil {
		t.Errorf("a past window holding a lookup reads %v, as if the resolver had not answered then", rate.Rate)
	}
	rate, err = network.db.ReadAttributionRate(ctx, network.now-86400, network.now+1, nil)
	if err != nil {
		t.Fatalf("reading the rate: %v", err)
	}
	if rate.ResolverCovered || rate.Rate != nil {
		t.Errorf("today's window, with the resolver unreachable and no lookup, reads %v", rate.Rate)
	}
}

// TestAChangeOfPublicAddressIsANewRowAndTheOldOneKeepsItsLastSighting is AC12.
func TestAChangeOfPublicAddressIsANewRowAndTheOldOneKeepsItsLastSighting(t *testing.T) {
	network := newTestNetwork(t, 1, 1)
	ctx := context.Background()
	bits := int64(24)
	for _, step := range []struct {
		at      int64
		address string
	}{
		{network.now + 100, "203.0.113.2"},
		{network.now + 200, "203.0.113.2"},
		{network.now + 300, "192.0.2.77"},
		{network.now + 400, "192.0.2.77"},
	} {
		if err := network.db.UpsertInterfaceAddress(ctx, InterfaceAddress{
			InterfaceID: network.upstream.id, SourceField: SourceFieldAddr4, Address: step.address,
			PrefixLength: &bits, AddressFamily: 4}, step.at); err != nil {
			t.Fatalf("writing an address: %v", err)
		}
	}
	addresses, err := network.db.ReadPublicAddresses(ctx)
	if err != nil {
		t.Fatalf("reading the public addresses: %v", err)
	}
	if len(addresses) != 1 || addresses[0].InterfaceID != network.upstream.id {
		t.Fatalf("the public addresses are %+v", addresses)
	}
	var current, previous *AddressHistory
	for index := range addresses[0].History {
		entry := &addresses[0].History[index]
		if entry.SourceField != SourceFieldAddr4 {
			continue
		}
		switch entry.Address {
		case "192.0.2.77":
			current = entry
		case "203.0.113.2":
			previous = entry
		}
	}
	if current == nil || previous == nil {
		t.Fatalf("the history does not hold both addresses: %+v", addresses[0].History)
	}
	if current.FirstSeenAt != network.now+300 || current.LastSeenAt != network.now+400 {
		t.Errorf("the current address was first seen %d and last seen %d", current.FirstSeenAt, current.LastSeenAt)
	}
	if previous.FirstSeenAt != network.now-86400 || previous.LastSeenAt != network.now+200 {
		t.Errorf("the previous address keeps first %d and last %d", previous.FirstSeenAt, previous.LastSeenAt)
	}
	if addresses[0].History[0].Address != "192.0.2.77" {
		t.Error("the history is not newest first")
	}
}

// insertReading writes one measurement.
func insertReading(t *testing.T, database *Store, kind SubjectKind, key string, measure Measure,
	unit Unit, value float64, at int64) {
	t.Helper()
	providerID, _ := database.ProviderID(context.Background(), "measurement_sample", "insight")
	var provider *int64
	if kind == SubjectInterfaceEndpointPair || kind == SubjectInterfaceEndpoint {
		provider = &providerID
	}
	if err := database.InsertMeasurementSample(context.Background(), MeasurementSample{
		ProviderID: provider, SubjectKind: kind, SubjectKey: key, Measure: measure, Unit: unit,
		Value: value, SampledAt: at,
	}); err != nil {
		t.Fatalf("writing a reading: %v", err)
	}
}

// TestAClientsCurrentRateIsItsLatestPairReadingsWithinTheBound is AC15.
//
// A client's rate is the totals of its local address -- the only place traffic/top reports
// the address's sending -- so a client talking to several peers has an outbound figure, and
// the bound is twice the measurement interval the store reads from the setting row.
func TestAClientsCurrentRateIsItsLatestPairReadingsWithinTheBound(t *testing.T) {
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	const interval = 300
	if err := network.db.SetSetting(ctx, MeasurementIntervalKey, fmt.Sprint(interval), network.now); err != nil {
		t.Fatalf("writing the interval: %v", err)
	}
	bound, err := network.db.CurrentSampleBound(ctx)
	if err != nil || bound != 2*interval {
		t.Fatalf("the bound is %d, %v, not twice the interval", bound, err)
	}
	leased := network.clients[0]
	clientID := queryInt(t, network.db, "SELECT id FROM client WHERE last_address = ?", leased.address)
	older, latest := network.now-500, network.now-200
	peers := []string{outside(1, leased.v6), outside(2, leased.v6)}

	endpoint := func(device, local string, at int64, in float64, out *float64) {
		key := InterfaceEndpointKey(device, local)
		insertReading(t, network.db, SubjectInterfaceEndpoint, key, MeasureRateBitsIn, UnitBitPerSecond, in, at)
		if out != nil {
			insertReading(t, network.db, SubjectInterfaceEndpoint, key, MeasureRateBitsOut, UnitBitPerSecond, *out, at)
		}
	}
	pair := func(device, local, peer string, at int64, in float64) {
		insertReading(t, network.db, SubjectInterfaceEndpointPair, InterfaceEndpointPairKey(device, local, peer),
			MeasureRateBitsIn, UnitBitPerSecond, in, at)
	}
	figure := func(value float64) *float64 { return &value }

	// Two peers, so no per-peer outbound figure exists: the total is the client's outbound.
	endpoint(leased.device, leased.address, older, 1000, figure(2000))
	endpoint(leased.device, leased.address, latest, 130, figure(240))
	pair(leased.device, leased.address, peers[0], latest, 100)
	pair(leased.device, leased.address, peers[1], latest, 30)
	// The same conversation read on the upstream interface, whose local address is the
	// firewall's own: counted per device, never per client.
	endpoint(network.upstream.device, network.upstream.address, latest, 5000, figure(5000))
	pair(network.upstream.device, network.upstream.address, peers[0], latest, 5000)
	// A client whose peers lie outside AND behind an interface: its outbound total cannot be
	// split, and is reported whole.
	mixed, inner := network.clients[1], network.clients[2]
	endpoint(mixed.device, mixed.address, latest, 50, figure(70))
	pair(mixed.device, mixed.address, outside(3, mixed.v6), latest, 20)
	pair(mixed.device, mixed.address, inner.address, latest, 30)
	// A record that carried no outbound total: the client's outbound is unmeasured, not 0.
	silent := network.clients[3]
	silentID := queryInt(t, network.db, "SELECT id FROM client WHERE last_address = ?", silent.address)
	endpoint(silent.device, silent.address, latest, 10, nil)
	pair(silent.device, silent.address, outside(4, silent.v6), latest, 10)

	rate, state, err := network.db.ReadClientCurrentRate(ctx, clientID, network.now)
	if err != nil {
		t.Fatalf("reading the rate: %v", err)
	}
	if state != RateCurrent || rate.InBitsPerSecond != 130 || rate.OutBitsPerSecond != 240 || !rate.OutMeasured {
		t.Errorf("the client's current rate is %s %+v, not in 130 and a measured out of 240", state, rate)
	}
	if rate.SampledAt != latest {
		t.Errorf("the rate carries the instant %d, not %d", rate.SampledAt, latest)
	}
	rates, err := network.db.ReadCurrentRates(ctx, network.now)
	if err != nil {
		t.Fatalf("reading the rates: %v", err)
	}
	// The outbound direction holds the multi-peer client's 240, and says it is not the whole:
	// the silent client's outbound was not measured.
	if outbound := rates.ByDirection["outbound"]; outbound.OutBitsPerSecond != 240 || outbound.OutMeasured {
		t.Errorf("the outbound rate is %+v, not the multi-peer client's 240 marked incomplete", outbound)
	}
	if rates.ByDirection["inbound"].InBitsPerSecond != 160 || rates.ByDirection["inter_interface"].InBitsPerSecond != 30 {
		t.Errorf("the inbound figures per direction are %+v", rates.ByDirection)
	}
	if rates.OutboundUnsplit.OutBitsPerSecond != 70 || !rates.OutboundUnsplit.OutMeasured {
		t.Errorf("the unsplit outbound is %+v, not the mixed client's 70", rates.OutboundUnsplit)
	}
	if silentRate := rates.ByClient[silentID]; silentRate.OutMeasured || silentRate.InBitsPerSecond != 10 {
		t.Errorf("a client whose record carried no outbound figure reads %+v, as if it had been measured", silentRate)
	}
	if rates.ByDevice[network.upstream.device].Total() != 10000 {
		t.Errorf("the upstream device's rate is %v", rates.ByDevice[network.upstream.device])
	}

	// Beyond the bound there is no current sample, which is a state and not a zero.
	_, state, err = network.db.ReadClientCurrentRate(ctx, clientID, network.now+bound+1)
	if err != nil {
		t.Fatalf("reading the rate: %v", err)
	}
	if state != RateNoCurrentSample {
		t.Errorf("beyond the bound the state is %s", state)
	}
	// A setting that is not a positive whole number is refused rather than bounding nothing.
	if err := network.db.SetSetting(ctx, MeasurementIntervalKey, "0", network.now); err != nil {
		t.Fatalf("writing the interval: %v", err)
	}
	if _, err := network.db.CurrentSampleBound(ctx); err == nil {
		t.Error("an interval of 0 was accepted")
	}
}

// TestAThroughputIsDerivedFromConsecutiveCountersAndAResetIsNotANegativeRate is AC16.
func TestAThroughputIsDerivedFromConsecutiveCountersAndAResetIsNotANegativeRate(t *testing.T) {
	network := newTestNetwork(t, 1, 1)
	device := network.inside[0].device
	for _, sample := range []struct {
		at    int64
		value float64
	}{{100, 1000}, {400, 4000}, {700, 500}, {1000, 3500}} {
		insertReading(t, network.db, SubjectInterface, device, MeasureBytesIn, UnitByte, sample.value,
			network.now+sample.at)
	}
	points, err := network.db.ReadInterfaceThroughput(context.Background(), device, MeasureBytesIn,
		network.now, network.now+2000)
	if err != nil {
		t.Fatalf("reading the throughput: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("three intervals gave %d points", len(points))
	}
	if points[0].PerSecond == nil || *points[0].PerSecond != 10 {
		t.Errorf("the first interval is %+v, not 10 per second", points[0])
	}
	if !points[1].Reset || points[1].PerSecond != nil {
		t.Errorf("a decreasing counter gave %+v, not a reset", points[1])
	}
	if points[2].PerSecond == nil || *points[2].PerSecond != 10 {
		t.Errorf("the interval after a reset is %+v", points[2])
	}
	for _, point := range points {
		if point.PerSecond != nil && *point.PerSecond < 0 {
			t.Errorf("a negative rate: %+v", point)
		}
	}
}

// TestSampledBytesAreBytesSeenInSamplesAndAnUnsampledPeriodIsNotZero is AC2's code half.
func TestSampledBytesAreBytesSeenInSamplesAndAnUnsampledPeriodIsNotZero(t *testing.T) {
	network := newTestNetwork(t, 1, 2)
	ctx := context.Background()
	client := network.clients[0]
	key := InterfaceEndpointKey(client.device, client.address)
	for _, at := range []int64{100, 400} {
		insertReading(t, network.db, SubjectInterfaceEndpoint, key, MeasureCumulativeBytesIn, UnitByte, 300, network.now+at)
		insertReading(t, network.db, SubjectInterfaceEndpoint, key, MeasureCumulativeBytesOut, UnitByte, 200, network.now+at)
	}
	// A per-peer reading is not counted again: the totals already hold it.
	insertReading(t, network.db, SubjectInterfaceEndpointPair,
		InterfaceEndpointPairKey(client.device, client.address, outside(1, client.v6)),
		MeasureCumulativeBytesIn, UnitByte, 300, network.now+100)
	sampled, state, err := network.db.ReadSampledBytes(ctx, network.now, network.now+1000)
	if err != nil {
		t.Fatalf("reading the sampled bytes: %v", err)
	}
	if state != Sampled || len(sampled) != 1 || sampled[0].Bytes != 1000 || sampled[0].Samples != 2 ||
		sampled[0].SampledSeconds != 2*SampleSeconds {
		t.Errorf("the sampled bytes are %s %+v", state, sampled)
	}
	_, state, err = network.db.ReadSampledBytes(ctx, network.now+2000, network.now+3000)
	if err != nil {
		t.Fatalf("reading an unsampled period: %v", err)
	}
	if state != NotSampled {
		t.Errorf("a period with no sample is %s, not not_sampled", state)
	}
}

// TestNonLoggingRulesAreCountedPerInterfaceByKey is the store half of AC13: keys resolve by
// identifier, a legacy rule's descriptions are left unresolved, and a floating rule is its own
// count.
func TestNonLoggingRulesAreCountedPerInterfaceByKey(t *testing.T) {
	network := newTestNetwork(t, 3, 3)
	ctx := context.Background()
	no, yes, legacy := false, true, true
	for index, rule := range []Rule{
		{Interface: ptr("example_if_0"), LogsMatches: &no},
		{Interface: ptr("example_if_0,example_if_2"), LogsMatches: &no},
		{Interface: ptr("example_if_1"), LogsMatches: &yes},
		{Interface: ptr(""), LogsMatches: &no},
		{Interface: ptr("example description 0"), Legacy: &legacy, LogsMatches: &no},
		{Interface: ptr("example_if_0")},
	} {
		rule.PfLabel = fmt.Sprintf("example-logging-rule-%d", index)
		rule.Action = "pass"
		if _, err := network.db.UpsertRule(ctx, rule, network.now); err != nil {
			t.Fatalf("writing a rule: %v", err)
		}
	}
	logging, err := network.db.ReadRuleLogging(ctx)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	first, third := network.inside[0].id, network.inside[2].id
	if logging.NotLoggingByInterface[first] != 2 || logging.NotLoggingByInterface[third] != 1 ||
		logging.NotLoggingByInterface[network.inside[1].id] != 0 {
		t.Errorf("the per-interface counts are %v", logging.NotLoggingByInterface)
	}
	if logging.FloatingNotLogging != 1 || logging.UnresolvedNotLogging != 1 {
		t.Errorf("floating %d, unresolved %d", logging.FloatingNotLogging, logging.UnresolvedNotLogging)
	}
	// The network's own rule reports no flag, like the last one above.
	if logging.NotReported != 2 {
		t.Errorf("%d rules report no flag, not 2", logging.NotReported)
	}
}
