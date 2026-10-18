package store_test

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The two legs of one connection: AC7 and AC9 to AC12 of
// specs/SPEC-step-5a-live-corrections.md.

// TestANatedPairIsPairedOneToOne and the IPv6 case below are AC9's first two points.
func TestANatedPairIsPairedOneToOne(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			client := storetest.ClientsOfFamily(network, false)[0]
			first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(60, false),
				At: network.Now - 100, IPID: 4242, Seq: 77000, Lag: 1}, "")
			addresses := network.Insert(t, []storetest.Flow{first, second})
			network.DerivePairs(t, addresses, network.Now-200, network.Now)
			if outcome, partner := storetest.OutcomeOf(t, network.DB, first.Device, first.Src, first.Dst); outcome != flows.PairFirstLeg ||
				partner != second.Device+" "+second.Src+" "+second.Dst {
				t.Errorf("the client's record is %s with partner %s", outcome, partner)
			}
			if outcome, partner := storetest.OutcomeOf(t, network.DB, second.Device, second.Src, second.Dst); outcome != flows.PairSecondLeg ||
				partner != first.Device+" "+first.Src+" "+first.Dst {
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			client := storetest.ClientsOfFamily(network, true)[0]
			first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(61, true),
				At: network.Now - 100, Seq: 99000, Lag: 0}, "")
			addresses := network.Insert(t, []storetest.Flow{first, second})
			network.DerivePairs(t, addresses, network.Now-200, network.Now)
			if outcome, _ := storetest.OutcomeOf(t, network.DB, first.Device, first.Src, first.Dst); outcome != flows.PairFirstLeg {
				t.Errorf("the client's record is %s", outcome)
			}
			if outcome, _ := storetest.OutcomeOf(t, network.DB, second.Device, second.Src, second.Dst); outcome != flows.PairSecondLeg {
				t.Errorf("the upstream record is %s", outcome)
			}
			if client := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			clients := storetest.ClientsOfFamily(network, false)
			peer := storetest.Outside(62, false)
			var specs []storetest.Flow
			var wanted [][2]storetest.Flow
			for index, client := range clients[:2] {
				first, second := network.Legs(storetest.Connection{Client: client, Peer: peer, At: network.Now - 100,
					IPID: int64(1000 + index), Seq: int64(5000 + index), Lag: int64(index)}, "")
				// The two second legs share the firewall's address; their ports differ, which
				// the pairing does not read under NAT -- only the invariant fields tell them apart.
				specs = append(specs, first, second)
				wanted = append(wanted, [2]storetest.Flow{first, second})
			}
			network.DerivePairs(t, network.Insert(t, specs), network.Now-200, network.Now)
			for _, pair := range wanted {
				first, second := pair[0], pair[1]
				var partnerOfFirst int64
				if err := network.DB.DB().QueryRow(`SELECT p.ip_id FROM flow AS f JOIN flow AS p ON p.id = f.paired_flow_id
					WHERE f.src_address = $1 AND f.direction = 'in'`, first.Src).Scan(&partnerOfFirst); err != nil {
					t.Fatalf("reading the partner of %s: %v", first.Src, err)
				}
				if partnerOfFirst != *second.IPID {
					t.Errorf("the client %s was paired with the leg of identification %d, not %d",
						first.Src, partnerOfFirst, *second.IPID)
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			clients := storetest.ClientsOfFamily(network, false)
			peer := storetest.Outside(63, false)
			var specs []storetest.Flow
			for _, client := range clients[:2] {
				first, second := network.Legs(storetest.Connection{Client: client, Peer: peer, At: network.Now - 100,
					IPID: 777, Seq: 888, Lag: 0}, "")
				specs = append(specs, first, second)
			}
			network.DerivePairs(t, network.Insert(t, specs), network.Now-200, network.Now)
			if paired := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
				WHERE pair_outcome IN ('first_leg', 'second_leg')`); paired != 0 {
				t.Errorf("%d records were paired although two candidates were indistinguishable", paired)
			}
			if clients := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
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
			t.Parallel()
			ctx := context.Background()
			build := func(order []int) (*storetest.Network, []storetest.Flow) {
				network := storetest.NewNetwork(t, counts[0], counts[1])
				var specs []storetest.Flow
				for index, client := range network.Clients[:4] {
					first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(70+index, client.V6),
						At: network.Now - 300 + int64(index), IPID: int64(3000 + index),
						Seq: int64(6000 + index), Lag: int64(index % 2)}, "")
					specs = append(specs, first, second)
				}
				// Each pass stores some records and derives its own window.
				for _, index := range order {
					addresses := network.Insert(t, []storetest.Flow{specs[index]})
					at := specs[index].ObservedAt
					network.DerivePairs(t, addresses, at, at)
				}
				return network, specs
			}
			outFirst := []int{1, 3, 5, 7, 0, 2, 4, 6}
			inFirst := []int{0, 2, 4, 6, 1, 3, 5, 7}
			early, specs := build(outFirst)
			late, _ := build(inFirst)
			if got := storetest.QueryInt(t, early.DB, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); got != 4 {
				t.Errorf("with every out leg ingested a pass before its in leg, %d pairs were made, not 4", got)
			}
			if early, late := storetest.PairState(t, early.DB), storetest.PairState(t, late.DB); early != late {
				t.Errorf("two ingestion orders give different rows:\n%s\nagainst\n%s", early, late)
			}
			if a, b := derivedSnapshot(t, early.DB), derivedSnapshot(t, late.DB); a != b {
				t.Error("two ingestion orders give different aggregates")
			}
			again, err := early.DB.Pair(ctx, early.Now-400, early.Now, flows.DefaultPairingWindowSeconds)
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
	for _, field := range flows.PairingFields {
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
		text, err := sqldb.Statement(name)
		if err != nil {
			t.Fatal(err)
		}
		if offending := check(name, text); len(offending) != 0 {
			t.Errorf("%s compares %v, whose invariance is not verified", name, offending)
		}
		for column := range verified {
			if !strings.Contains(text, column+" IS NOT DISTINCT FROM :"+column) {
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
func pairedNetwork(t *testing.T, interfaces, clients int) *storetest.Network {
	t.Helper()
	network := storetest.NewNetwork(t, interfaces, clients)
	specs := network.FlowsFor(120, 3*3600)
	for index, client := range network.Clients {
		first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(90+index, client.V6),
			At: network.Now - 1000 - int64(index)*37, IPID: int64(9000 + index), Seq: int64(12000 + index),
			Lag: int64(index % 2)}, "")
		specs = append(specs, first, second)
	}
	addresses := network.Insert(t, specs)
	network.DerivePairs(t, addresses, network.Now-4*3600, network.Now)
	// The clients looked their peers up just before, so the domain family has rows.
	ctx := context.Background()
	for index, client := range network.Clients {
		if err := network.DB.InsertDNSResolution(ctx, vocab.DNSResolution{
			LookupKey: fmt.Sprintf("example-paired-lookup-%d", index), ClientAddress: client.Address,
			Domain: fmt.Sprintf("site-%d.example.invalid", index), Resolver: "unbound", Action: "pass",
			AnswerSource: storetest.Ptr("Recursion"), LookedUpAt: network.Now - 1000 - int64(index)*37 - 1,
			IngestedAt: network.Now - 1,
		}); err != nil {
			t.Fatalf("writing a lookup: %v", err)
		}
	}
	var lookupAddresses []string
	for _, client := range network.Clients {
		lookupAddresses = append(lookupAddresses, client.Address)
	}
	if _, err := network.DB.Reclassify(ctx, lookupAddresses, network.Now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := network.DB.Attribute(ctx, network.Now-4*3600, network.Now, 5, network.Now); err != nil {
		t.Fatalf("attributing: %v", err)
	}
	recomputeEverything(t, network)
	if pairs := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); pairs !=
		int64(len(network.Clients)) {
		t.Fatalf("%d pairs were made of the %d connections", pairs, len(network.Clients))
	}
	return network
}

// familySum is the bytes of one family over every 24 h slot.
func familySum(t *testing.T, database *store.Store, table, where string) int64 {
	t.Helper()
	return storetest.QueryInt(t, database, fmt.Sprintf("SELECT coalesce(sum(bytes), 0) FROM %s_24h %s", table, where))
}

// TestAPairedConnectionCountsOnceInTheVolumeFamily is AC11 for the volume family, and
// the upstream interface's own figures, which count its legs.
func TestAPairedConnectionCountsOnceInTheVolumeFamily(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 5}, {3, 9}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			network := pairedNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			once := storetest.QueryInt(t, network.DB, `SELECT sum(packet_bytes) FROM flow
				WHERE pair_outcome IS NULL OR pair_outcome <> 'second_leg'`)
			if got := familySum(t, network.DB, "volume_aggregate", "WHERE exit_leg_device IS NULL"); got != once {
				t.Errorf("the volume family's totals hold %d bytes; counted once, the flows hold %d", got, once)
			}
			legs := storetest.QueryInt(t, network.DB, `SELECT sum(packet_bytes) FROM flow WHERE pair_outcome = 'second_leg'
				AND interface_device = $1`, network.Upstream.Device)
			if got := familySum(t, network.DB, "volume_aggregate", "WHERE exit_leg_device = '"+network.Upstream.Device+"'"); got != legs {
				t.Errorf("the upstream interface's own figures hold %d bytes of second legs, the flows %d", got, legs)
			}
			window, err := network.DB.ReadVolumeWindow(ctx, network.Now-86400, network.Now+1, network.Now)
			if err != nil {
				t.Fatalf("reading the window: %v", err)
			}
			if got := window.Total().Bytes; got != storetest.QueryInt(t, network.DB, `SELECT sum(packet_bytes) FROM flow
				WHERE observed_at >= $1 AND (pair_outcome IS NULL OR pair_outcome <> 'second_leg')`, network.Now-86400) {
				t.Errorf("the window's total %d counts a paired connection twice", got)
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
			direct := storetest.QueryInt(t, network.DB, `SELECT coalesce(sum(packet_bytes), 0) FROM classified_flow
				WHERE local_client_id IS NOT NULL AND traffic_scope <> 'this_firewall'
				  AND (pair_outcome IS NULL OR pair_outcome <> 'second_leg')`)
			if named := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'
				AND src_client_id IS NOT NULL`); named == 0 {
				t.Fatal("no second leg names a client, so the family has nothing to leave out")
			}
			for _, table := range []string{"client_volume_aggregate", "peer_volume_aggregate", "owner_volume_aggregate"} {
				if got := familySum(t, network.DB, table, ""); got != direct {
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
			if attributed := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM domain_attribution AS a
				JOIN flow AS f ON f.id = a.flow_id WHERE f.pair_outcome = 'second_leg'`); attributed != 0 {
				t.Errorf("%d second legs carry a site name", attributed)
			}
			direct := storetest.QueryInt(t, network.DB, `SELECT coalesce(sum(f.packet_bytes), 0) FROM flow AS f
				JOIN domain_attribution AS a ON a.flow_id = f.id`)
			if direct == 0 {
				t.Fatal("no flow is attributed, so the family is not exercised")
			}
			if got := familySum(t, network.DB, "domain_volume_aggregate", ""); got != direct {
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
			every := storetest.QueryInt(t, network.DB, "SELECT sum(packet_bytes) FROM flow")
			if got := familySum(t, network.DB, "rule_volume_aggregate", ""); got != every {
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
			once := storetest.QueryInt(t, network.DB, `SELECT sum(packet_bytes) FROM flow
				WHERE pair_outcome IS NULL OR pair_outcome <> 'second_leg'`)
			if got := storetest.QueryInt(t, network.DB, "SELECT CAST(sum(octets) AS INTEGER) FROM pair_volume_observation"); got != once {
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
			t.Parallel()
			network := storetest.NewNetwork(t, counts[0], counts[1])
			clients := storetest.ClientsOfFamily(network, false)
			var specs []storetest.Flow

			paired, pairedOut := network.Legs(storetest.Connection{Client: clients[0], Peer: storetest.Outside(109, false),
				At: network.Now - 600, IPID: 50, Seq: 500, Lag: 0}, "")
			specs = append(specs, paired, pairedOut)

			rewrittenIn, rewrittenOut := network.Legs(storetest.Connection{Client: clients[0], Peer: storetest.Outside(110, false),
				At: network.Now - 500, IPID: 100, Seq: 1000, Lag: 0}, "")
			rewritten := int64(31337)
			rewrittenOut.IPID = &rewritten
			specs = append(specs, rewrittenIn, rewrittenOut)

			for _, client := range clients[1:3] {
				first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(111, false),
					At: network.Now - 400, IPID: 200, Seq: 2000, Lag: 0}, "")
				specs = append(specs, first, second)
			}

			dns := int64(53)
			own := storetest.Flow{ObservedAt: network.Now - 300, Device: network.Upstream.Device, Direction: "out",
				Src: network.Upstream.Address, Dst: storetest.Outside(112, false), DstPort: &dns, Protocol: "udp",
				Action: "pass", Bytes: 70}
			specs = append(specs, own)
			network.DerivePairs(t, network.Insert(t, specs), network.Now-1000, network.Now)

			if outcome, _ := storetest.OutcomeOf(t, network.DB, pairedOut.Device, pairedOut.Src, pairedOut.Dst); outcome != flows.PairSecondLeg {
				t.Errorf("the paired leg is %s, not %s", outcome, flows.PairSecondLeg)
			}
			if others := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
				WHERE pair_outcome IS NOT NULL AND pair_outcome NOT IN ('first_leg', 'second_leg', 'not_paired')`); others != 0 {
				t.Errorf("%d records carry an outcome other than a leg or not paired", others)
			}
			if wrong := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow AS f JOIN interface AS i
				ON i.device = f.interface_device WHERE f.direction = 'out' AND f.src_is_this_firewall
				AND i.is_upstream AND (f.pair_outcome IS NULL
				OR (f.pair_outcome = 'not_paired') = (f.paired_flow_id IS NOT NULL)
				OR f.pair_outcome NOT IN ('second_leg', 'not_paired'))`); wrong != 0 {
				t.Errorf("%d upstream records sourced by this firewall have no single outcome", wrong)
			}
			if outcome, _ := storetest.OutcomeOf(t, network.DB, own.Device, own.Src, own.Dst); outcome != flows.PairNotPaired {
				t.Errorf("the record no client record matches is %s, not %s", outcome, flows.PairNotPaired)
			}
			if outcome, _ := storetest.OutcomeOf(t, network.DB, rewrittenOut.Device, rewrittenOut.Src, rewrittenOut.Dst); outcome != flows.PairNotPaired {
				t.Errorf("the leg whose identification was rewritten is %s, not %s", outcome, flows.PairNotPaired)
			}
			if clients := storetest.QueryInt(t, network.DB, `SELECT count(*) FROM flow
				WHERE pair_outcome = 'not_paired' AND src_client_id IS NOT NULL`); clients != 0 {
				t.Errorf("%d unpaired records carry a client", clients)
			}
			if _, err := network.DB.DB().Exec(`UPDATE flow SET pair_outcome = 'this_firewall_traffic'
				WHERE pair_outcome = 'not_paired'`); err == nil {
				t.Error("the schema accepts the removed outcome this_firewall_traffic")
			}
		})
	}
}
