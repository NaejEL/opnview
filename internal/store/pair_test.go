package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The two legs of one connection: AC7 and AC9 to AC12 of
// specs/SPEC-step-5a-live-corrections.md.

// connection describes one client connection to an outside address.
type connection struct {
	client testClient
	peer   string
	at     int64
	ipID   int64
	seq    int64
	// lag is how long after the first leg the second was logged.
	lag int64
}

// legs returns the client's `in` record and the `out` record on the upstream interface:
// over IPv4 the second leg's source is the firewall's upstream address and its port
// another, as outbound NAT writes it; over IPv6 it is the client's, untranslated.
func (n *testNetwork) legs(c connection, upstream6 string) (networkFlow, networkFlow) {
	port := int64(443)
	clientPort := int64(41000 + c.ipID%1000)
	natPort := int64(52000 + c.ipID%1000)
	seq := c.seq
	first := networkFlow{observedAt: c.at, device: c.client.device, direction: "in", src: c.client.address,
		dst: c.peer, dstPort: &port, srcPort: &clientPort, tcpSeq: &seq, protocol: "tcp", action: "pass",
		bytes: 60}
	second := first
	second.device, second.direction, second.observedAt = n.upstream.device, "out", c.at+c.lag
	if c.client.v6 {
		_ = upstream6
		return first, second
	}
	id := c.ipID
	first.ipID, second.ipID = &id, &id
	second.src, second.srcPort = n.upstream.address, &natPort
	return first, second
}

// derivePairs classifies what was stored and pairs and refreshes the window, as a
// derivation does.
func (n *testNetwork) derivePairs(t *testing.T, addresses []string, from, to int64) Pairing {
	t.Helper()
	ctx := context.Background()
	if _, err := n.db.Reclassify(ctx, addresses, n.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	paired, err := n.db.Pair(ctx, from, to, DefaultPairingWindowSeconds)
	if err != nil {
		t.Fatalf("pairing: %v", err)
	}
	if _, err := n.db.RefreshAggregates(ctx, 0, n.now, paired.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	return paired
}

// pairState renders every record's outcome and partner by what the record is -- its
// interface, its ends and its instant -- so two databases can be compared whatever ids
// and digests they gave the rows.
func pairState(t *testing.T, database *Store) string {
	t.Helper()
	rows, err := database.DB().Query(`SELECT f.interface_device || ' ' || f.src_address || ' ' || f.dst_address || ' ' || f.observed_at,
		ifnull(f.pair_outcome, '-'),
		ifnull(p.interface_device || ' ' || p.src_address || ' ' || p.dst_address || ' ' || p.observed_at, '-')
		FROM flow AS f LEFT JOIN flow AS p ON p.id = f.paired_flow_id`)
	if err != nil {
		t.Fatalf("reading the pairs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var digest, outcome, partner string
		if err := rows.Scan(&digest, &outcome, &partner); err != nil {
			t.Fatalf("reading the pairs: %v", err)
		}
		lines = append(lines, digest+" "+outcome+" "+partner)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// outcomeOf reads one record's outcome and its partner's address pair.
func outcomeOf(t *testing.T, database *Store, device, src, dst string) (string, string) {
	t.Helper()
	var outcome, partner string
	if err := database.DB().QueryRow(`SELECT ifnull(f.pair_outcome, '-'),
		ifnull(p.interface_device || ' ' || p.src_address || ' ' || p.dst_address, '-')
		FROM flow AS f LEFT JOIN flow AS p ON p.id = f.paired_flow_id
		WHERE f.interface_device = ? AND f.src_address = ? AND f.dst_address = ?`, device, src, dst).
		Scan(&outcome, &partner); err != nil {
		t.Fatalf("reading the record %s %s -> %s: %v", device, src, dst, err)
	}
	return outcome, partner
}

func clientsOfFamily(network *testNetwork, v6 bool) []testClient {
	var clients []testClient
	for _, client := range network.clients {
		if client.v6 == v6 {
			clients = append(clients, client)
		}
	}
	return clients
}

// TestANatedPairIsPairedOneToOne and the IPv6 case below are AC9's first two points.
func TestANatedPairIsPairedOneToOne(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			client := clientsOfFamily(network, false)[0]
			first, second := network.legs(connection{client: client, peer: outside(60, false),
				at: network.now - 100, ipID: 4242, seq: 77000, lag: 1}, "")
			addresses := network.insert(t, []networkFlow{first, second})
			network.derivePairs(t, addresses, network.now-200, network.now)
			if outcome, partner := outcomeOf(t, network.db, first.device, first.src, first.dst); outcome != PairFirstLeg ||
				partner != second.device+" "+second.src+" "+second.dst {
				t.Errorf("the client's record is %s with partner %s", outcome, partner)
			}
			if outcome, partner := outcomeOf(t, network.db, second.device, second.src, second.dst); outcome != PairSecondLeg ||
				partner != first.device+" "+first.src+" "+first.dst {
				t.Errorf("the upstream record is %s with partner %s", outcome, partner)
			}
		})
	}
}

// TestAnUntranslatedIPv6PairIsPaired: the second leg keeps the client's source and port.
func TestAnUntranslatedIPv6PairIsPaired(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			client := clientsOfFamily(network, true)[0]
			first, second := network.legs(connection{client: client, peer: outside(61, true),
				at: network.now - 100, seq: 99000, lag: 0}, "")
			addresses := network.insert(t, []networkFlow{first, second})
			network.derivePairs(t, addresses, network.now-200, network.now)
			if outcome, _ := outcomeOf(t, network.db, first.device, first.src, first.dst); outcome != PairFirstLeg {
				t.Errorf("the client's record is %s", outcome)
			}
			if outcome, _ := outcomeOf(t, network.db, second.device, second.src, second.dst); outcome != PairSecondLeg {
				t.Errorf("the upstream record is %s", outcome)
			}
			if client := queryInt(t, network.db, `SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'
				AND src_client_id IS NOT NULL`); client != 1 {
				t.Errorf("the untranslated second leg keeps its client on %d rows, not 1", client)
			}
		})
	}
}

// TestTwoClientsToOneDestinationEachPairWithTheirOwnLeg: same destination, same port,
// within the window, told apart by their invariant fields.
func TestTwoClientsToOneDestinationEachPairWithTheirOwnLeg(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {4, 12}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			clients := clientsOfFamily(network, false)
			peer := outside(62, false)
			var specs []networkFlow
			var wanted [][2]networkFlow
			for index, client := range clients[:2] {
				first, second := network.legs(connection{client: client, peer: peer, at: network.now - 100,
					ipID: int64(1000 + index), seq: int64(5000 + index), lag: int64(index)}, "")
				// The two second legs share the firewall's address; their ports differ, which
				// the pairing does not read under NAT -- only the invariant fields tell them apart.
				specs = append(specs, first, second)
				wanted = append(wanted, [2]networkFlow{first, second})
			}
			network.derivePairs(t, network.insert(t, specs), network.now-200, network.now)
			for _, pair := range wanted {
				first, second := pair[0], pair[1]
				var partnerOfFirst int64
				if err := network.db.DB().QueryRow(`SELECT p.ip_id FROM flow AS f JOIN flow AS p ON p.id = f.paired_flow_id
					WHERE f.src_address = ? AND f.direction = 'in'`, first.src).Scan(&partnerOfFirst); err != nil {
					t.Fatalf("reading the partner of %s: %v", first.src, err)
				}
				if partnerOfFirst != *second.ipID {
					t.Errorf("the client %s was paired with the leg of identification %d, not %d",
						first.src, partnerOfFirst, *second.ipID)
				}
			}
		})
	}
}

// TestIndistinguishableCandidatesLeaveBothLegsUnpaired: two connections nothing logged
// tells apart are not guessed at.
func TestIndistinguishableCandidatesLeaveBothLegsUnpaired(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {3, 10}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			clients := clientsOfFamily(network, false)
			peer := outside(63, false)
			var specs []networkFlow
			for _, client := range clients[:2] {
				first, second := network.legs(connection{client: client, peer: peer, at: network.now - 100,
					ipID: 777, seq: 888, lag: 0}, "")
				specs = append(specs, first, second)
			}
			network.derivePairs(t, network.insert(t, specs), network.now-200, network.now)
			if paired := queryInt(t, network.db, `SELECT count(*) FROM flow
				WHERE pair_outcome IN ('first_leg', 'second_leg')`); paired != 0 {
				t.Errorf("%d records were paired although two candidates were indistinguishable", paired)
			}
			if clients := queryInt(t, network.db, `SELECT count(*) FROM flow
				WHERE pair_outcome = 'not_paired' AND src_client_id IS NOT NULL`); clients != 0 {
				t.Errorf("%d unpaired records carry a client", clients)
			}
		})
	}
}

// TestAnOutLegIngestedBeforeItsInLegIsPairedAtTheSecondPass, and the order and
// idempotence points of AC9.
func TestAnOutLegIngestedBeforeItsInLegIsPairedAtTheSecondPass(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {4, 11}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			ctx := context.Background()
			build := func(order []int) (*testNetwork, []networkFlow) {
				network := newTestNetwork(t, counts[0], counts[1])
				var specs []networkFlow
				for index, client := range network.clients[:4] {
					first, second := network.legs(connection{client: client, peer: outside(70+index, client.v6),
						at: network.now - 300 + int64(index), ipID: int64(3000 + index),
						seq: int64(6000 + index), lag: int64(index % 2)}, "")
					specs = append(specs, first, second)
				}
				// Each pass stores some records and derives its own window.
				for _, index := range order {
					addresses := network.insert(t, []networkFlow{specs[index]})
					at := specs[index].observedAt
					network.derivePairs(t, addresses, at, at)
				}
				return network, specs
			}
			outFirst := []int{1, 3, 5, 7, 0, 2, 4, 6}
			inFirst := []int{0, 2, 4, 6, 1, 3, 5, 7}
			early, specs := build(outFirst)
			late, _ := build(inFirst)
			if got := queryInt(t, early.db, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); got != 4 {
				t.Errorf("with every out leg ingested a pass before its in leg, %d pairs were made, not 4", got)
			}
			if early, late := pairState(t, early.db), pairState(t, late.db); early != late {
				t.Errorf("two ingestion orders give different rows:\n%s\nagainst\n%s", early, late)
			}
			if a, b := derivedSnapshot(t, early.db), derivedSnapshot(t, late.db); a != b {
				t.Error("two ingestion orders give different aggregates")
			}
			again, err := early.db.Pair(ctx, early.now-400, early.now, DefaultPairingWindowSeconds)
			if err != nil {
				t.Fatalf("pairing again: %v", err)
			}
			if len(again.FlowInstants) != 0 {
				t.Errorf("a second derivation wrote %d records", len(again.FlowInstants))
			}
			_ = specs
		})
	}
}

// pairingStatements are the statements that decide a pairing.
var pairingStatements = []string{"pairing_in_candidates", "pairing_out_candidates"}

// comparedColumns returns every column a statement compares on a candidate record.
func comparedColumns(text string) []string {
	columns := map[string]struct{}{}
	for _, match := range regexp.MustCompile(`\b[io]\.([a-z_]+)\b`).FindAllStringSubmatch(text, -1) {
		columns[match[1]] = struct{}{}
	}
	var sorted []string
	for column := range columns {
		sorted = append(sorted, column)
	}
	sort.Strings(sorted)
	return sorted
}

// TestPairingReadsOnlyVerifiedFields is AC7: the pairing compares the structural fields
// of the rule and the logged fields verified invariant, and nothing whose invariance is
// unverified; and the check has teeth.
func TestPairingReadsOnlyVerifiedFields(t *testing.T) {
	t.Parallel()
	structural := map[string]bool{
		"id": true, "dst_address": true, "observed_at": true, "direction": true, "action": true,
		"interface_device": true, "protocol": true, "ip_version": true, "dst_port": true,
		"src_is_this_firewall": true, "src_address": true, "src_port": true,
	}
	verified := map[string]bool{}
	unverified := map[string]bool{}
	for _, field := range PairingFields {
		if field.Verified {
			if field.Column == "" {
				t.Errorf("the verified field %s is not stored", field.LogField)
			}
			verified[field.Column] = true
			continue
		}
		if !strings.HasPrefix(field.Evidence, "UNVERIFIED:") {
			t.Errorf("the unverified field %s does not say UNVERIFIED: %s", field.LogField, field.Evidence)
		}
		if field.Column != "" {
			unverified[field.Column] = true
		}
	}
	if len(verified) == 0 || len(unverified) == 0 {
		t.Fatal("the registry holds no verified or no unverified column, so the check proves nothing")
	}
	check := func(name, text string) []string {
		var offending []string
		for _, column := range comparedColumns(text) {
			if !structural[column] && !verified[column] {
				offending = append(offending, column)
			}
		}
		return offending
	}
	for _, name := range pairingStatements {
		text, err := Statement(name)
		if err != nil {
			t.Fatal(err)
		}
		if offending := check(name, text); len(offending) != 0 {
			t.Errorf("%s compares %v, whose invariance is not verified", name, offending)
		}
		for column := range verified {
			if !strings.Contains(text, column+" IS :"+column) {
				t.Errorf("%s does not compare the verified field %s", name, column)
			}
		}
	}
	// The teeth: a statement comparing an unverified field is caught.
	for column := range unverified {
		if offending := check("teeth", "WHERE i."+column+" = :"+column); len(offending) == 0 {
			t.Errorf("a comparison of the unverified column %s went unnoticed", column)
		}
	}
}

// pairedNetwork is a network whose traffic includes paired connections of both families.
func pairedNetwork(t *testing.T, interfaces, clients int) *testNetwork {
	t.Helper()
	network := newTestNetwork(t, interfaces, clients)
	specs := network.flowsFor(120, 3*3600)
	for index, client := range network.clients {
		first, second := network.legs(connection{client: client, peer: outside(90+index, client.v6),
			at: network.now - 1000 - int64(index)*37, ipID: int64(9000 + index), seq: int64(12000 + index),
			lag: int64(index % 2)}, "")
		specs = append(specs, first, second)
	}
	addresses := network.insert(t, specs)
	network.derivePairs(t, addresses, network.now-4*3600, network.now)
	// The clients looked their peers up just before, so the domain family has rows.
	ctx := context.Background()
	for index, client := range network.clients {
		if err := network.db.InsertDNSResolution(ctx, DNSResolution{
			LookupKey: fmt.Sprintf("example-paired-lookup-%d", index), ClientAddress: client.address,
			Domain: fmt.Sprintf("site-%d.example.invalid", index), Resolver: "unbound", Action: "pass",
			AnswerSource: ptr("Recursion"), LookedUpAt: network.now - 1000 - int64(index)*37 - 1,
			IngestedAt: network.now - 1,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	var lookupAddresses []string
	for _, client := range network.clients {
		lookupAddresses = append(lookupAddresses, client.address)
	}
	if _, err := network.db.Reclassify(ctx, lookupAddresses, network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.db.Attribute(ctx, network.now-4*3600, network.now, 5, network.now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	recomputeEverything(t, network)
	if pairs := queryInt(t, network.db, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); pairs !=
		int64(len(network.clients)) {
		t.Fatalf("%d pairs were made of the %d connections", pairs, len(network.clients))
	}
	return network
}

// familySum is the bytes of one family over every 24 h slot.
func familySum(t *testing.T, database *Store, table, where string) int64 {
	t.Helper()
	return queryInt(t, database, fmt.Sprintf("SELECT coalesce(sum(bytes), 0) FROM %s_24h %s", table, where))
}

// TestAPairedConnectionCountsOnceInTheVolumeFamily is AC11 for the volume family, and
// the upstream interface's own figures, which count its legs.
func TestAPairedConnectionCountsOnceInTheVolumeFamily(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			once := queryInt(t, network.db, `SELECT sum(packet_bytes) FROM flow
				WHERE pair_outcome IS NULL OR pair_outcome <> 'second_leg'`)
			if got := familySum(t, network.db, "volume_aggregate", "WHERE exit_leg_device IS NULL"); got != once {
				t.Errorf("the volume family's totals hold %d bytes; counted once, the flows hold %d", got, once)
			}
			legs := queryInt(t, network.db, `SELECT sum(packet_bytes) FROM flow WHERE pair_outcome = 'second_leg'
				AND interface_device = ?`, network.upstream.device)
			if got := familySum(t, network.db, "volume_aggregate", "WHERE exit_leg_device = '"+network.upstream.device+"'"); got != legs {
				t.Errorf("the upstream interface's own figures hold %d bytes of second legs, the flows %d", got, legs)
			}
			window, err := network.db.ReadVolumeWindow(ctx, network.now-86400, network.now+1, network.now)
			if err != nil {
				t.Fatalf("reading the window: %v", err)
			}
			if got := window.Total().Bytes; got != queryInt(t, network.db, `SELECT sum(packet_bytes) FROM flow
				WHERE observed_at >= ? AND (pair_outcome IS NULL OR pair_outcome <> 'second_leg')`, network.now-86400) {
				t.Errorf("the window's total %d counts a paired connection twice", got)
			}
			if got := window.ExitLegs()[network.upstream.device].Bytes; got != legs {
				t.Errorf("the window's exit legs on the upstream interface hold %d bytes, the flows %d", got, legs)
			}
		})
	}
}

// TestAPairedConnectionCountsOnceInTheClientPeerAndOwnerFamilies is AC11 for the three
// families keyed on a client: the untranslated second leg still names the client, and is
// left out.
func TestAPairedConnectionCountsOnceInTheClientPeerAndOwnerFamilies(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			direct := queryInt(t, network.db, `SELECT coalesce(sum(packet_bytes), 0) FROM classified_flow
				WHERE local_client_id IS NOT NULL AND traffic_scope <> 'this_firewall'
				  AND (pair_outcome IS NULL OR pair_outcome <> 'second_leg')`)
			if named := queryInt(t, network.db, `SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'
				AND src_client_id IS NOT NULL`); named == 0 {
				t.Fatal("no second leg names a client, so the family has nothing to leave out")
			}
			for _, table := range []string{"client_volume_aggregate", "peer_volume_aggregate", "owner_volume_aggregate"} {
				if got := familySum(t, network.db, table, ""); got != direct {
					t.Errorf("%s holds %d bytes; counted once, the flows hold %d", table, got, direct)
				}
			}
		})
	}
}

// TestAPairedConnectionCountsOnceInTheDomainFamily is AC11 for the domain family: a
// second leg is never attributed.
func TestAPairedConnectionCountsOnceInTheDomainFamily(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			if attributed := queryInt(t, network.db, `SELECT count(*) FROM domain_attribution AS a
				JOIN flow AS f ON f.id = a.flow_id WHERE f.pair_outcome = 'second_leg'`); attributed != 0 {
				t.Errorf("%d second legs carry a site name", attributed)
			}
			direct := queryInt(t, network.db, `SELECT coalesce(sum(f.packet_bytes), 0) FROM flow AS f
				JOIN domain_attribution AS a ON a.flow_id = f.id`)
			if direct == 0 {
				t.Fatal("no flow is attributed, so the family is not exercised")
			}
			if got := familySum(t, network.db, "domain_volume_aggregate", ""); got != direct {
				t.Errorf("the domain family holds %d bytes, the attributed flows %d", got, direct)
			}
		})
	}
}

// TestTheRuleFamilyCountsEveryRecordOfAPairedConnection is AC11 for the rule family:
// each record is a match of its own rule.
func TestTheRuleFamilyCountsEveryRecordOfAPairedConnection(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			every := queryInt(t, network.db, "SELECT sum(packet_bytes) FROM flow")
			if got := familySum(t, network.db, "rule_volume_aggregate", ""); got != every {
				t.Errorf("the rule family holds %d bytes, every record %d", got, every)
			}
		})
	}
}

// TestThePairVolumeLeavesSecondLegsOut is AC11 for pair_volume_observation.
func TestThePairVolumeLeavesSecondLegsOut(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			once := queryInt(t, network.db, `SELECT sum(packet_bytes) FROM flow
				WHERE pair_outcome IS NULL OR pair_outcome <> 'second_leg'`)
			if got := queryInt(t, network.db, "SELECT CAST(sum(octets) AS INTEGER) FROM pair_volume_observation"); got != once {
				t.Errorf("the per-pair volume holds %d octets; counted once, the flows hold %d", got, once)
			}
		})
	}
}

// TestNoRecordIsEverLabelledThisFirewallsOwnTraffic is AC10 as amended on 7 October 2026:
// every upstream `out` record sourced by this firewall is a second leg or not paired --
// one whose identification was rewritten, two connections nothing tells apart, and a
// record with no client record anywhere near it alike -- and none carries a client.
func TestNoRecordIsEverLabelledThisFirewallsOwnTraffic(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 6}, {3, 10}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := newTestNetwork(t, counts[0], counts[1])
			clients := clientsOfFamily(network, false)
			var specs []networkFlow

			paired, pairedOut := network.legs(connection{client: clients[0], peer: outside(109, false),
				at: network.now - 600, ipID: 50, seq: 500, lag: 0}, "")
			specs = append(specs, paired, pairedOut)

			rewrittenIn, rewrittenOut := network.legs(connection{client: clients[0], peer: outside(110, false),
				at: network.now - 500, ipID: 100, seq: 1000, lag: 0}, "")
			rewritten := int64(31337)
			rewrittenOut.ipID = &rewritten
			specs = append(specs, rewrittenIn, rewrittenOut)

			for _, client := range clients[1:3] {
				first, second := network.legs(connection{client: client, peer: outside(111, false),
					at: network.now - 400, ipID: 200, seq: 2000, lag: 0}, "")
				specs = append(specs, first, second)
			}

			dns := int64(53)
			own := networkFlow{observedAt: network.now - 300, device: network.upstream.device, direction: "out",
				src: network.upstream.address, dst: outside(112, false), dstPort: &dns, protocol: "udp",
				action: "pass", bytes: 70}
			specs = append(specs, own)
			network.derivePairs(t, network.insert(t, specs), network.now-1000, network.now)

			if outcome, _ := outcomeOf(t, network.db, pairedOut.device, pairedOut.src, pairedOut.dst); outcome != PairSecondLeg {
				t.Errorf("the paired leg is %s, not %s", outcome, PairSecondLeg)
			}
			if others := queryInt(t, network.db, `SELECT count(*) FROM flow
				WHERE pair_outcome IS NOT NULL AND pair_outcome NOT IN ('first_leg', 'second_leg', 'not_paired')`); others != 0 {
				t.Errorf("%d records carry an outcome other than a leg or not paired", others)
			}
			if wrong := queryInt(t, network.db, `SELECT count(*) FROM flow AS f JOIN interface AS i
				ON i.device = f.interface_device WHERE f.direction = 'out' AND f.src_is_this_firewall = 1
				AND i.is_upstream = 1 AND (f.pair_outcome IS NULL
				OR (f.pair_outcome = 'not_paired') = (f.paired_flow_id IS NOT NULL)
				OR f.pair_outcome NOT IN ('second_leg', 'not_paired'))`); wrong != 0 {
				t.Errorf("%d upstream records sourced by this firewall have no single outcome", wrong)
			}
			if outcome, _ := outcomeOf(t, network.db, own.device, own.src, own.dst); outcome != PairNotPaired {
				t.Errorf("the record no client record matches is %s, not %s", outcome, PairNotPaired)
			}
			if outcome, _ := outcomeOf(t, network.db, rewrittenOut.device, rewrittenOut.src, rewrittenOut.dst); outcome != PairNotPaired {
				t.Errorf("the leg whose identification was rewritten is %s, not %s", outcome, PairNotPaired)
			}
			if clients := queryInt(t, network.db, `SELECT count(*) FROM flow
				WHERE pair_outcome = 'not_paired' AND src_client_id IS NOT NULL`); clients != 0 {
				t.Errorf("%d unpaired records carry a client", clients)
			}
			if _, err := network.db.DB().Exec(`UPDATE flow SET pair_outcome = 'this_firewall_traffic'
				WHERE pair_outcome = 'not_paired'`); err == nil {
				t.Error("the schema accepts the removed outcome this_firewall_traffic")
			}
		})
	}
}
