package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The resolver's records and the attribution they make exact: the store half of
// specs/SPEC-resolver-cache-attribution.md, AC6 to AC14, AC16 and AC20.
//
// Every name is under a reserved domain (RFC 2606, RFC 6761) and every outside address a
// documentation address (RFC 5737, RFC 3849), through outside(); the counts are
// parameters and both IP versions are exercised. The code under test reads every value
// back out of the rows.

// testWindows are the two attribution settings at their defaults.
var testWindows = AttributionWindows{LookupTimingDelay: 5, CacheAnswerDelay: DefaultCacheAnswerDelaySeconds}

// recordProvider returns the Unbound provider of one resolver-record kind.
func recordProvider(t *testing.T, database *Store, kind string) int64 {
	t.Helper()
	id, err := database.ProviderID(context.Background(), kind, "unbound")
	if err != nil {
		t.Fatalf("looking up the %s provider: %v", kind, err)
	}
	return id
}

// addressRecord is an A or an AAAA record, by the address's family.
func addressRecord(owner, address string, ttl int64) ResourceRecord {
	canonical, _ := CanonicalAddress(address)
	rrtype := RRTypeA
	if !canonical.Is4() {
		rrtype = RRTypeAAAA
	}
	return ResourceRecord{OwnerName: DNSName(owner), RRType: rrtype, Value: canonical.String(),
		Address: canonical.String(), TTLSeconds: ttl}
}

// cnameRecord is a CNAME record.
func cnameRecord(owner, target string, ttl int64) ResourceRecord {
	return ResourceRecord{OwnerName: DNSName(owner), RRType: RRTypeCNAME, Value: DNSName(target), TTLSeconds: ttl}
}

// localAddressRecord is a local-data A or AAAA record.
func localAddressRecord(owner, address string) ResourceRecord {
	record := addressRecord(owner, address, 0)
	record.HostLabel = HostnameLabel(owner)
	return record
}

// pollCache stores one successful poll of the cache.
func pollCache(t *testing.T, database *Store, at int64, records ...ResourceRecord) RecordStorage {
	t.Helper()
	storage, err := database.StoreResourceRecords(context.Background(), ResourceRecordRead{
		ProviderID: recordProvider(t, database, "resolver_cache"), HeldIn: HeldInCache,
		PolledAt: at, Records: records,
	})
	if err != nil {
		t.Fatalf("storing a cache poll at %d: %v", at, err)
	}
	return storage
}

// pollLocalData stores one successful poll of the local data.
func pollLocalData(t *testing.T, database *Store, at int64, records ...ResourceRecord) {
	t.Helper()
	if _, err := database.StoreResourceRecords(context.Background(), ResourceRecordRead{
		ProviderID: recordProvider(t, database, "resolver_local_data"), HeldIn: HeldInLocalData,
		PolledAt: at, Records: records,
	}); err != nil {
		t.Fatalf("storing a local-data poll at %d: %v", at, err)
	}
}

// observation is one stored observation, by content.
type observation struct {
	owner, rrtype, value                    string
	firstSeen, lastSeen, coveredFrom, until int64
}

// observationsOf reads the observations of one owner name, oldest first.
func observationsOf(t *testing.T, database *Store, owner string) []observation {
	t.Helper()
	rows, err := database.DB().Query(`SELECT owner_name, rrtype, value, first_seen_at, last_seen_at,
		covered_from_at, covered_until_at FROM resource_record_observation WHERE owner_name = ?
		ORDER BY first_seen_at, rrtype, value`, owner)
	if err != nil {
		t.Fatalf("reading the observations of %s: %v", owner, err)
	}
	defer func() { _ = rows.Close() }()
	var found []observation
	for rows.Next() {
		var o observation
		if err := rows.Scan(&o.owner, &o.rrtype, &o.value, &o.firstSeen, &o.lastSeen, &o.coveredFrom,
			&o.until); err != nil {
			t.Fatal(err)
		}
		found = append(found, o)
	}
	return found
}

// TestUnchangedPollsStoreAsManyObservationsAsOnePoll is AC6's first point: N polls of an
// unchanged dump store what one poll stores, whatever N and the dump's size.
func TestUnchangedPollsStoreAsManyObservationsAsOnePoll(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{1, 3}, {6, 20}} {
		polls, records := counts[0], counts[1]
		t.Run(fmt.Sprintf("%d polls of %d records", polls, records), func(t *testing.T) {
			database, _ := openTestStore(t)
			var dump []ResourceRecord
			for index := 0; index < records; index++ {
				owner := fmt.Sprintf("name-%d.example.com", index)
				dump = append(dump, addressRecord(owner, outside(index, index%2 == 1), 300))
				if index%3 == 0 {
					dump = append(dump, cnameRecord(fmt.Sprintf("alias-%d.example.com", index), owner, 300))
				}
			}
			for poll := 0; poll < polls; poll++ {
				pollCache(t, database, referenceNow+int64(poll*60), dump...)
			}
			if stored := queryInt(t, database, "SELECT count(*) FROM resource_record_observation"); stored != int64(len(dump)) {
				t.Errorf("%d polls of %d records stored %d observations, not %d", polls, len(dump), stored, len(dump))
			}
		})
	}
}

// TestCoverageIsExtendedAtEachPoll is AC6's second point.
func TestCoverageIsExtendedAtEachPoll(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		database, _ := openTestStore(t)
		record := addressRecord("extended.example.com", outside(1, v6), 300)
		for poll := int64(0); poll < 3; poll++ {
			pollCache(t, database, referenceNow+poll*60, record)
			got := observationsOf(t, database, "extended.example.com")
			if len(got) != 1 {
				t.Fatalf("after poll %d there are %d observations, not one", poll, len(got))
			}
			if got[0].lastSeen != referenceNow+poll*60 || got[0].until != referenceNow+poll*60+300 {
				t.Errorf("after poll %d the observation was last seen at %d and covers until %d",
					poll, got[0].lastSeen, got[0].until)
			}
		}
	}
}

// TestAnAbsentRecordThatReappearsStartsANewObservation is AC6's third point.
func TestAnAbsentRecordThatReappearsStartsANewObservation(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	record := addressRecord("returning.example.com", outside(2, false), 30)
	other := addressRecord("other.example.com", outside(3, true), 30)
	pollCache(t, database, referenceNow, record, other)
	pollCache(t, database, referenceNow+60, other)
	pollCache(t, database, referenceNow+120, record, other)
	got := observationsOf(t, database, "returning.example.com")
	if len(got) != 2 {
		t.Fatalf("a record absent from one poll and back at the next is %d observations, not two", len(got))
	}
	if got[1].coveredFrom != referenceNow+60 || got[1].firstSeen != referenceNow+120 {
		t.Errorf("the reappearance is covered from %d and first seen at %d", got[1].coveredFrom, got[1].firstSeen)
	}
	if got[0].until != referenceNow+30 {
		t.Errorf("the first observation covers until %d, not its own poll plus its ttl", got[0].until)
	}
}

// TestTheCoverageLowerBoundIsThePreviousSuccessfulPollAcrossAFailureAndARestart is AC7.
func TestTheCoverageLowerBoundIsThePreviousSuccessfulPollAcrossAFailureAndARestart(t *testing.T) {
	t.Parallel()
	database, directory := openTestStore(t)
	held := addressRecord("held.example.com", outside(4, false), 100)

	// The first poll ever: with no earlier poll, the lower bound is the poll's own instant.
	pollCache(t, database, referenceNow, held)
	if got := observationsOf(t, database, "held.example.com"); got[0].coveredFrom != referenceNow {
		t.Errorf("the first observation is covered from %d, not its own poll", got[0].coveredFrom)
	}
	// A failed poll at +60 stores nothing (the collector never calls the store for one), so
	// the poll at +120 follows the poll at 0: the record seen at both is one observation, and
	// a new record is covered from 0.
	held.TTLSeconds = 30
	appeared := addressRecord("appeared.example.com", outside(5, true), 300)
	pollCache(t, database, referenceNow+120, held, appeared)
	if got := observationsOf(t, database, "appeared.example.com"); got[0].coveredFrom != referenceNow {
		t.Errorf("across a failed poll the new record is covered from %d, not the last successful poll", got[0].coveredFrom)
	}
	got := observationsOf(t, database, "held.example.com")
	if len(got) != 1 {
		t.Fatalf("across a failed poll the held record is %d observations", len(got))
	}
	// The upper bound is the LATEST poll plus the ttl it reported: 120 + 30, beyond 0 + 100.
	if got[0].until != referenceNow+150 {
		t.Errorf("the held record covers until %d, not the latest poll plus its ttl", got[0].until)
	}

	// A restart: the database is opened again, and the next poll still follows +120.
	if err := database.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	reopened, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	pollCache(t, reopened, referenceNow+600, addressRecord("after-restart.example.com", outside(6, false), 60))
	if got := observationsOf(t, reopened, "after-restart.example.com"); got[0].coveredFrom != referenceNow+120 {
		t.Errorf("after a restart the new record is covered from %d, not the last successful poll", got[0].coveredFrom)
	}
}

// TestACNAMEChainGivesTheAddressWhileEveryLinkCoversTheInstant is AC8's first point.
func TestACNAMEChainGivesTheAddressWhileEveryLinkCoversTheInstant(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		database, _ := openTestStore(t)
		ctx := context.Background()
		address := outside(7, v6)
		// D -> CNAME -> CNAME -> A, the middle link covering the shortest interval.
		pollCache(t, database, referenceNow,
			cnameRecord("Site.Example.COM.", "first.example.net", 600),
			cnameRecord("first.example.net", "second.example.org", 120),
			addressRecord("second.example.org", address, 600))
		for _, instant := range []struct {
			at   int64
			want bool
		}{
			{referenceNow, true}, {referenceNow + 120, true}, {referenceNow + 121, false},
			{referenceNow - 1, false},
		} {
			got, err := database.AnswerAddresses(ctx, "site.example.com", instant.at)
			if err != nil {
				t.Fatal(err)
			}
			found := len(got) == 1 && got[0] == address
			if found != instant.want {
				t.Errorf("at %+d the chain gives %v; want the address %v", instant.at-referenceNow, got, instant.want)
			}
		}
	}
}

// TestACNAMELoopAndAChainOverTheBoundGiveNothing is AC8's second point.
func TestACNAMELoopAndAChainOverTheBoundGiveNothing(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	pollCache(t, database, referenceNow,
		cnameRecord("loop-a.example.com", "loop-b.example.com", 300),
		cnameRecord("loop-b.example.com", "loop-a.example.com", 300))
	if got, err := database.AnswerAddresses(ctx, "loop-a.example.com", referenceNow); err != nil || len(got) != 0 {
		t.Errorf("a loop gives %v, %v", got, err)
	}
	// A chain of exactly MaxCNAMELinks links is followed; one link more is not.
	for _, links := range []int{MaxCNAMELinks, MaxCNAMELinks + 1} {
		var records []ResourceRecord
		prefix := fmt.Sprintf("chain-%d", links)
		for link := 0; link < links; link++ {
			records = append(records, cnameRecord(fmt.Sprintf("%s-%d.example.com", prefix, link),
				fmt.Sprintf("%s-%d.example.com", prefix, link+1), 300))
		}
		records = append(records, addressRecord(fmt.Sprintf("%s-%d.example.com", prefix, links), outside(8, false), 300))
		pollCache(t, database, referenceNow+1, records...)
		got, err := database.AnswerAddresses(ctx, prefix+"-0.example.com", referenceNow+1)
		if err != nil {
			t.Fatal(err)
		}
		if within := links <= MaxCNAMELinks; within != (len(got) == 1) {
			t.Errorf("a chain of %d links gives %v", links, got)
		}
	}
}

// cacheScenario is one client, one flow to an outside destination, and whatever lookups
// and resolver records a test arranges around it.
type cacheScenario struct {
	network     *testNetwork
	client      testClient
	flowAt      int64
	destination string
	addresses   []string
	lookups     int
}

// newCacheScenario builds the network and the flow. eastWest sends the flow to a client of
// another interface instead.
func newCacheScenario(t *testing.T, v6, eastWest bool) *cacheScenario {
	t.Helper()
	network := newTestNetwork(t, 2, 6)
	scenario := &cacheScenario{network: network, flowAt: network.now - 100}
	var other testClient
	for _, candidate := range network.clients {
		if candidate.v6 != v6 {
			continue
		}
		if scenario.client.address == "" {
			scenario.client = candidate
		} else if other.address == "" || candidate.interfaceID != scenario.client.interfaceID {
			// Another client of the family, on another interface where there is one.
			other = candidate
		}
	}
	scenario.destination = outside(40, v6)
	if eastWest {
		scenario.destination = other.address
	}
	port := int64(443)
	scenario.addresses = network.insert(t, []networkFlow{{observedAt: scenario.flowAt,
		device: scenario.client.device, direction: "in", src: scenario.client.address,
		dst: scenario.destination, dstPort: &port, protocol: "tcp", action: "pass", bytes: 100}})
	return scenario
}

// lookup stores one lookup by the scenario's client.
func (s *cacheScenario) lookup(t *testing.T, domain string, secondsBefore int64, source, action string) {
	t.Helper()
	s.lookups++
	if err := s.network.db.InsertDNSResolution(context.Background(), DNSResolution{
		LookupKey: fmt.Sprintf("example-scenario-lookup-%d", s.lookups), ClientAddress: s.client.address,
		Domain: domain, Resolver: "unbound", Action: action, AnswerSource: ptr(source),
		LookedUpAt: s.flowAt - secondsBefore, IngestedAt: s.network.now,
	}); err != nil {
		t.Fatalf("writing a lookup: %v", err)
	}
}

// attribute places every address and decides the window's attributions.
func (s *cacheScenario) attribute(t *testing.T) Attribution {
	t.Helper()
	ctx := context.Background()
	if _, err := s.network.db.Reclassify(ctx, append(s.addresses, s.client.address), s.network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	result, err := s.network.db.AttributeWith(ctx, s.flowAt-3600, s.flowAt+60, testWindows, s.network.now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	return result
}

// attributionRow is one attribution, by content.
type attributionRow struct {
	site, method, observedOwner string
	delay                       int64
}

// rows reads the scenario's attributions.
func (s *cacheScenario) rows(t *testing.T) []attributionRow {
	t.Helper()
	return attributionRows(t, s.network.db)
}

// attributionRows reads every attribution, by content.
func attributionRows(t *testing.T, database *Store) []attributionRow {
	t.Helper()
	rows, err := database.DB().Query(`SELECT a.site_name, a.method, ifnull(o.owner_name, ''),
		a.correlation_delay_seconds
		FROM domain_attribution AS a
		LEFT JOIN resource_record_observation AS o ON o.id = a.address_observation_id
		ORDER BY a.site_name`)
	if err != nil {
		t.Fatalf("reading the attributions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var found []attributionRow
	for rows.Next() {
		var row attributionRow
		if err := rows.Scan(&row.site, &row.method, &row.observedOwner, &row.delay); err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	return found
}

// TestThreeDomainsInTheWindowOneAnsweringTheDestinationAreAttributedByTheCache is AC9's
// first outcome: the case the 5A rule could not name.
func TestThreeDomainsInTheWindowOneAnsweringTheDestinationAreAttributedByTheCache(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "first.example.com", 3, "Recursion", "pass")
		scenario.lookup(t, "second.example.com", 2, "Recursion", "pass")
		scenario.lookup(t, "third.example.com", 1, "Cache", "pass")
		scenario.attribute(t)
		if got := scenario.rows(t); len(got) != 0 {
			t.Fatalf("with no resolver record, three domains in the window give %v; 5A gave no row", got)
		}
		pollCache(t, scenario.network.db, scenario.flowAt-30,
			addressRecord("second.example.com", scenario.destination, 300),
			addressRecord("first.example.com", outside(41, v6), 300))
		scenario.attribute(t)
		got := scenario.rows(t)
		if len(got) != 1 || got[0].site != "second.example.com" || got[0].method != MethodResolverCacheAnswer {
			t.Fatalf("IPv6 %t: the attribution is %v, want second.example.com by resolver_cache_answer", v6, got)
		}
		if got[0].delay != 2 {
			t.Errorf("the delay is %d, not the lookup's 2 seconds", got[0].delay)
		}
	}
}

// TestTwoExactCandidatesWithDistinctDomainsGiveNoRow is AC9's second outcome: a shared
// address, two names the client looked up.
func TestTwoExactCandidatesWithDistinctDomainsGiveNoRow(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "shared-a.example.com", 900, "Recursion", "pass")
		scenario.lookup(t, "shared-b.example.com", 2, "Recursion", "pass")
		pollCache(t, scenario.network.db, scenario.flowAt-1000,
			addressRecord("shared-a.example.com", scenario.destination, 3600),
			addressRecord("shared-b.example.com", scenario.destination, 3600))
		scenario.attribute(t)
		if got := scenario.rows(t); len(got) != 0 {
			t.Errorf("two names answering the destination give %v; the evidence does not say which", got)
		}
	}
}

// TestOneExactCandidateIsAttributedWithItsEvidenceNamed is AC9's third outcome, through a
// CNAME chain: the site name is the name looked up, verbatim, and the observation named is
// the A or AAAA record the chain ended at (AC8's third point).
func TestOneExactCandidateIsAttributedWithItsEvidenceNamed(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "Www.Example.COM.", 600, "Recursion", "pass")
		pollCache(t, scenario.network.db, scenario.flowAt-700,
			cnameRecord("www.example.com", "edge.example.net", 3600),
			addressRecord("edge.example.net", scenario.destination, 3600))
		scenario.attribute(t)
		got := scenario.rows(t)
		if len(got) != 1 {
			t.Fatalf("one exact candidate gives %v", got)
		}
		if got[0].site != "Www.Example.COM." || got[0].method != MethodResolverCacheAnswer ||
			got[0].observedOwner != "edge.example.net" || got[0].delay != 600 {
			t.Errorf("IPv6 %t: the attribution is %+v; want the looked-up name verbatim, the cache method, "+
				"the A or AAAA observation of edge.example.net and a delay of 600", v6, got[0])
		}
	}
}

// TestNoCacheEvidenceAndOneTimingCandidateIsLookupTiming is AC9's fourth outcome.
func TestNoCacheEvidenceAndOneTimingCandidateIsLookupTiming(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "timing.example.com", 3, "Recursion", "pass")
		// A record of the other family is no evidence about this destination.
		other := outside(42, !v6)
		pollCache(t, scenario.network.db, scenario.flowAt-30, addressRecord("timing.example.com", other, 300))
		scenario.attribute(t)
		got := scenario.rows(t)
		if len(got) != 1 || got[0].method != MethodLookupTiming || got[0].observedOwner != "" ||
			got[0].site != "timing.example.com" {
			t.Errorf("IPv6 %t: the attribution is %v, want timing.example.com by lookup_timing naming no observation",
				v6, got)
		}
	}
}

// TestContradictingEvidenceGivesNoRow is AC9's fifth outcome, decision 3.
func TestContradictingEvidenceGivesNoRow(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "contradicted.example.com", 3, "Recursion", "pass")
		pollCache(t, scenario.network.db, scenario.flowAt-30,
			addressRecord("contradicted.example.com", outside(43, v6), 300))
		scenario.attribute(t)
		if got := scenario.rows(t); len(got) != 0 {
			t.Errorf("IPv6 %t: the cache held the name's answers without the destination, and the flow "+
				"was named anyway: %v", v6, got)
		}
	}
}

// TestIneligibleLookupsAndFlowsGetNoRowUnderEitherMethod is AC9's last outcome: a blocked
// lookup, an east-west flow and a flow to this firewall, each with cache evidence that
// would otherwise name it.
func TestIneligibleLookupsAndFlowsGetNoRowUnderEitherMethod(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		blocked := newCacheScenario(t, v6, false)
		blocked.lookup(t, "blocked.example.com", 2, "Local", "block")
		pollCache(t, blocked.network.db, blocked.flowAt-30, addressRecord("blocked.example.com", blocked.destination, 300))
		blocked.attribute(t)
		if got := blocked.rows(t); len(got) != 0 {
			t.Errorf("IPv6 %t: a blocked lookup named a flow: %v", v6, got)
		}

		eastWest := newCacheScenario(t, v6, true)
		eastWest.lookup(t, "inside.example.com", 2, "Local-data", "pass")
		pollLocalData(t, eastWest.network.db, eastWest.flowAt-30, localAddressRecord("inside.example.com", eastWest.destination))
		pollCache(t, eastWest.network.db, eastWest.flowAt-30, addressRecord("inside.example.com", eastWest.destination, 300))
		eastWest.attribute(t)
		if got := eastWest.rows(t); len(got) != 0 {
			t.Errorf("IPv6 %t: an east-west flow was named: %v", v6, got)
		}
	}

	firewall := newCacheScenario(t, false, false)
	network := firewall.network
	dns := int64(53)
	network.insert(t, []networkFlow{{observedAt: firewall.flowAt + 10, device: firewall.client.device,
		direction: "in", src: firewall.client.address, dst: network.inside[0].address, dstPort: &dns,
		protocol: "udp", action: "pass", bytes: 70}})
	firewall.addresses = append(firewall.addresses, network.inside[0].address)
	firewall.lookup(t, "firewall.example.com", -8, "Recursion", "pass")
	pollCache(t, network.db, firewall.flowAt-30, addressRecord("firewall.example.com", network.inside[0].address, 300))
	firewall.attribute(t)
	if named := queryInt(t, network.db, `SELECT count(*) FROM domain_attribution AS a
		JOIN flow AS f ON f.id = a.flow_id WHERE f.dst_is_this_firewall = 1`); named != 0 {
		t.Errorf("%d flows to this firewall were named", named)
	}
}

// Decision 6 of specs/SPEC-resolver-cache-attribution.md, tested by
// specs/SPEC-resolver-cache-closing.md scope 1: local-data A and AAAA records are exact
// evidence only for lookups whose answer_source is Local-data, and only such lookups have
// their timing attribution contradicted by local-data answers.

// pollLocalDataAround stores two local-data polls, so the records cover every instant
// from secondsBefore before the scenario's flow to 30 s after it.
func (s *cacheScenario) pollLocalDataAround(t *testing.T, secondsBefore int64, records ...ResourceRecord) {
	t.Helper()
	pollLocalData(t, s.network.db, s.flowAt-secondsBefore, records...)
	pollLocalData(t, s.network.db, s.flowAt+30, records...)
}

// namedObservationHeldIn reads where the observation each attribution names was held,
// "" for an attribution that names none.
func namedObservationHeldIn(t *testing.T, database *Store) []string {
	t.Helper()
	rows, err := database.DB().Query(`SELECT ifnull(o.held_in, '') FROM domain_attribution AS a
		LEFT JOIN resource_record_observation AS o ON o.id = a.address_observation_id
		ORDER BY a.site_name`)
	if err != nil {
		t.Fatalf("reading the named observations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var heldIn string
		if err := rows.Scan(&heldIn); err != nil {
			t.Fatal(err)
		}
		found = append(found, heldIn)
	}
	return found
}

// TestALocalDataLookupIsAttributedByItsLocalDataRecord is decision 6's first case: a
// lookup answered from Local-data, whose name only a local-data record maps to the
// destination, is a resolver_cache_answer naming that local-data observation. The lookup
// lies outside the timing window, so only the exact method can name the flow.
func TestALocalDataLookupIsAttributedByItsLocalDataRecord(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "override.example.test", 600, "Local-data", "pass")
		scenario.pollLocalDataAround(t, 700, localAddressRecord("override.example.test", scenario.destination))
		scenario.attribute(t)
		got := scenario.rows(t)
		if len(got) != 1 || got[0].site != "override.example.test" || got[0].method != MethodResolverCacheAnswer ||
			got[0].observedOwner != "override.example.test" || got[0].delay != 600 {
			t.Fatalf("IPv6 %t: the attribution is %v; want override.example.test by resolver_cache_answer, "+
				"naming its local-data observation, with a delay of 600", v6, got)
		}
		if heldIn := namedObservationHeldIn(t, scenario.network.db); len(heldIn) != 1 || heldIn[0] != HeldInLocalData {
			t.Errorf("IPv6 %t: the observation named is held in %v, not the local data", v6, heldIn)
		}
	}
}

// TestALookupAnsweredByRecursionOrTheCacheDoesNotUseALocalDataRecord is decision 6's
// second case: the same local-data record is no evidence for a lookup the resolver
// answered by recursion or from its cache. Inside the timing window the flow is named by
// lookup_timing; outside it, by nothing.
func TestALookupAnsweredByRecursionOrTheCacheDoesNotUseALocalDataRecord(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"Recursion", "Cache"} {
		for _, v6 := range []bool{false, true} {
			for _, testCase := range []struct {
				secondsBefore int64
				want          []attributionRow
			}{
				{2, []attributionRow{{site: "override.example.test", method: MethodLookupTiming, delay: 2}}},
				{600, nil},
			} {
				scenario := newCacheScenario(t, v6, false)
				scenario.lookup(t, "override.example.test", testCase.secondsBefore, source, "pass")
				scenario.pollLocalDataAround(t, 700, localAddressRecord("override.example.test", scenario.destination))
				scenario.attribute(t)
				if got := scenario.rows(t); fmt.Sprint(got) != fmt.Sprint(testCase.want) {
					t.Errorf("%s, IPv6 %t, lookup %d s before the flow: the attribution is %v, want %v",
						source, v6, testCase.secondsBefore, got, testCase.want)
				}
			}
		}
	}
}

// TestLocalDataAnswersExcludingTheDestinationContradictOnlyALocalDataLookup is decision
// 6's third case: local-data answers of the destination's family that do not hold it
// suppress the timing rule for a lookup answered from Local-data, and do not for a lookup
// answered by recursion.
func TestLocalDataAnswersExcludingTheDestinationContradictOnlyALocalDataLookup(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		for _, testCase := range []struct {
			source string
			want   []attributionRow
		}{
			{"Local-data", nil},
			{"Recursion", []attributionRow{{site: "elsewhere.example.test", method: MethodLookupTiming, delay: 2}}},
		} {
			scenario := newCacheScenario(t, v6, false)
			scenario.lookup(t, "elsewhere.example.test", 2, testCase.source, "pass")
			scenario.pollLocalDataAround(t, 700, localAddressRecord("elsewhere.example.test", outside(46, v6)))
			scenario.attribute(t)
			if got := scenario.rows(t); fmt.Sprint(got) != fmt.Sprint(testCase.want) {
				t.Errorf("%s, IPv6 %t: with local-data answers excluding the destination the attribution is %v, want %v",
					testCase.source, v6, got, testCase.want)
			}
		}
	}
}

// specs/SPEC-resolver-cache-follow-ups.md, scope 1: no CNAME is followed into the local
// data. Each of the three parts reading the rule is tested on the same case, a cached
// CNAME chain from the looked-up name whose last name only a local-data A or AAAA record
// holds; the lookup is answered from Local-data, the one source local-data evidence
// could count for, as well as by recursion and from the cache.

// cnameIntoLocalData stores the chain case: the cache's CNAME from name to its target,
// covering the flow's instant, and the target's local-data record of address.
func (s *cacheScenario) cnameIntoLocalData(t *testing.T, name, target, address string) {
	t.Helper()
	pollCache(t, s.network.db, s.flowAt-700, cnameRecord(name, target, 3600))
	s.pollLocalDataAround(t, 700, localAddressRecord(target, address))
}

// TestTheExactMethodDoesNotFollowACNAMEIntoLocalData is AC1's exact-method test: the
// chain ends at the destination, the lookup lies outside the timing window, and no
// resolver_cache_answer is written through the chain.
func TestTheExactMethodDoesNotFollowACNAMEIntoLocalData(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"Local-data", "Recursion", "Cache"} {
		for _, v6 := range []bool{false, true} {
			scenario := newCacheScenario(t, v6, false)
			scenario.lookup(t, "alias.example.com", 600, source, "pass")
			scenario.cnameIntoLocalData(t, "alias.example.com", "host.example.test", scenario.destination)
			scenario.attribute(t)
			if got := scenario.rows(t); len(got) != 0 {
				t.Errorf("%s, IPv6 %t: a CNAME into the local data named the flow: %v", source, v6, got)
			}
		}
	}
}

// TestTheContradictionDoesNotFollowACNAMEIntoLocalData is AC1's contradiction test: the
// chain ends at a local-data address of the destination's family that is not the
// destination, and the timing rule still names the flow.
func TestTheContradictionDoesNotFollowACNAMEIntoLocalData(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"Local-data", "Recursion"} {
		for _, v6 := range []bool{false, true} {
			scenario := newCacheScenario(t, v6, false)
			scenario.lookup(t, "alias.example.com", 2, source, "pass")
			scenario.cnameIntoLocalData(t, "alias.example.com", "host.example.test", outside(48, v6))
			scenario.attribute(t)
			want := []attributionRow{{site: "alias.example.com", method: MethodLookupTiming, delay: 2}}
			if got := scenario.rows(t); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s, IPv6 %t: a CNAME into the local data contradicted the timing rule: %v, want %v",
					source, v6, got, want)
			}
		}
	}
}

// otherFamilyDiagnostic runs live_timing_fallback_other_family_cached over the
// scenario's flow.
func (s *cacheScenario) otherFamilyDiagnostic(t *testing.T) int64 {
	t.Helper()
	var counted int64
	if err := s.network.db.DB().QueryRow(
		diagnosticText(t, "live_timing_fallback_other_family_cached"),
		sql.Named("window_start", s.flowAt-600), sql.Named("window_end", s.flowAt+1),
	).Scan(&counted); err != nil {
		t.Fatalf("running the diagnostic: %v", err)
	}
	return counted
}

// TestTheOtherFamilyDiagnosticDoesNotFollowACNAMEIntoLocalData is AC1's diagnostic
// test: a lookup_timing attribution whose name reaches a local-data record of the other
// family only through a cached CNAME is not counted, while one whose looked-up name
// itself holds that local-data record is.
func TestTheOtherFamilyDiagnosticDoesNotFollowACNAMEIntoLocalData(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		for _, testCase := range []struct {
			name    string
			through bool
			want    int64
		}{
			{"through a CNAME", true, 0},
			{"held by the looked-up name itself", false, 1},
		} {
			scenario := newCacheScenario(t, v6, false)
			scenario.lookup(t, "alias.example.test", 2, "Local-data", "pass")
			if testCase.through {
				scenario.cnameIntoLocalData(t, "alias.example.test", "host.example.test", outside(49, !v6))
			} else {
				scenario.pollLocalDataAround(t, 700, localAddressRecord("alias.example.test", outside(49, !v6)))
			}
			scenario.attribute(t)
			if got := scenario.rows(t); len(got) != 1 || got[0].method != MethodLookupTiming {
				t.Fatalf("IPv6 %t, %s: the attribution is %v, want one by lookup_timing", v6, testCase.name, got)
			}
			if counted := scenario.otherFamilyDiagnostic(t); counted != testCase.want {
				t.Errorf("IPv6 %t, the other family's local-data record %s: the diagnostic counts %d, want %d",
					v6, testCase.name, counted, testCase.want)
			}
		}
	}
}

// orderingData is the flows, lookups and resolver polls the six-order test stores.
type orderingData struct {
	flows   []networkFlow
	lookups []DNSResolution
	polls   []ResourceRecordRead
}

// orderingFixture builds, on one network, one flow per outcome in each family: named by
// the cache through a chain, named by timing, contradicted, and ambiguous.
func orderingFixture(network *testNetwork, cacheProvider int64) orderingData {
	var data orderingData
	port := int64(443)
	var records []ResourceRecord
	pollAt := network.now - 4000
	for _, v6 := range []bool{false, true} {
		var client testClient
		for _, candidate := range network.clients {
			if candidate.v6 == v6 {
				client = candidate
				break
			}
		}
		family := 0
		if v6 {
			family = 100
		}
		for outcome, domains := range [][]string{
			{"chained.example.com", "other-chained.example.com"},
			{"timed.example.com"},
			{"contradicted.example.com"},
			{"ambiguous-a.example.com", "ambiguous-b.example.com"},
		} {
			flowAt := network.now - int64(600*(outcome+1)) - int64(family)
			destination := outside(60+outcome+family, v6)
			data.flows = append(data.flows, networkFlow{observedAt: flowAt, device: client.device,
				direction: "in", src: client.address, dst: destination, dstPort: &port, protocol: "tcp",
				action: "pass", bytes: int64(100 + outcome)})
			for index, domain := range domains {
				data.lookups = append(data.lookups, DNSResolution{
					LookupKey: fmt.Sprintf("example-order-%t-%d-%d", v6, outcome, index), ClientAddress: client.address,
					Domain: domain, Resolver: "unbound", Action: "pass", AnswerSource: ptr("Recursion"),
					LookedUpAt: flowAt - int64(2+index), IngestedAt: network.now,
				})
			}
			switch outcome {
			case 0:
				records = append(records, cnameRecord("chained.example.com", fmt.Sprintf("edge-%t.example.net", v6), 7200),
					addressRecord(fmt.Sprintf("edge-%t.example.net", v6), destination, 7200))
			case 2:
				records = append(records, addressRecord("contradicted.example.com", outside(90+family, v6), 7200))
			case 3:
				records = append(records, addressRecord("ambiguous-a.example.com", destination, 7200),
					addressRecord("ambiguous-b.example.com", destination, 7200))
			}
		}
	}
	data.polls = []ResourceRecordRead{
		{ProviderID: cacheProvider, HeldIn: HeldInCache, PolledAt: pollAt, Records: records},
		{ProviderID: cacheProvider, HeldIn: HeldInCache, PolledAt: pollAt + 60, Records: records},
	}
	return data
}

// fullAttributionRow is one attribution by content, its flow and lookup included.
type fullAttributionRow struct {
	flowDigest, lookupKey, site, method, observation string
	delay                                            int64
}

// fullAttributionRows reads every attribution by content.
func fullAttributionRows(t *testing.T, database *Store) []fullAttributionRow {
	t.Helper()
	rows, err := database.DB().Query(`SELECT f.log_digest, r.lookup_key, a.site_name, a.method,
		ifnull(o.owner_name || ' ' || o.rrtype || ' ' || o.value || ' ' || o.first_seen_at, ''),
		a.correlation_delay_seconds
		FROM domain_attribution AS a
		JOIN flow AS f ON f.id = a.flow_id
		JOIN dns_resolution AS r ON r.id = a.dns_resolution_id
		LEFT JOIN resource_record_observation AS o ON o.id = a.address_observation_id
		ORDER BY f.log_digest`)
	if err != nil {
		t.Fatalf("reading the attributions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var found []fullAttributionRow
	for rows.Next() {
		var row fullAttributionRow
		if err := rows.Scan(&row.flowDigest, &row.lookupKey, &row.site, &row.method, &row.observation,
			&row.delay); err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	return found
}

// storeOrderingBatch stores one batch and derives the attributions, as a pass would.
func storeOrderingBatch(t *testing.T, network *testNetwork, data orderingData, batch string, addresses *[]string) Attribution {
	t.Helper()
	ctx := context.Background()
	switch batch {
	case "flows":
		*addresses = append(*addresses, network.insert(t, data.flows)...)
	case "lookups":
		for _, lookup := range data.lookups {
			if err := network.db.InsertDNSResolution(ctx, lookup); err != nil {
				t.Fatalf("writing a lookup: %v", err)
			}
			*addresses = append(*addresses, lookup.ClientAddress)
		}
	case "observations":
		for _, poll := range data.polls {
			if _, err := network.db.StoreResourceRecords(ctx, poll); err != nil {
				t.Fatalf("storing a poll: %v", err)
			}
		}
	}
	if _, err := network.db.Reclassify(ctx, *addresses, network.now); err != nil {
		t.Fatalf("classifying: %v", err)
	}
	result, err := network.db.AttributeWith(ctx, network.now-7200, network.now, testWindows, network.now)
	if err != nil {
		t.Fatalf("attributing: %v", err)
	}
	return result
}

// TestAllSixOrdersOfFlowsLookupsAndObservationsGiveIdenticalRows is AC10's first point,
// and its third: a second derivation writes nothing.
func TestAllSixOrdersOfFlowsLookupsAndObservationsGiveIdenticalRows(t *testing.T) {
	t.Parallel()
	orders := [][]string{
		{"flows", "lookups", "observations"}, {"flows", "observations", "lookups"},
		{"lookups", "flows", "observations"}, {"lookups", "observations", "flows"},
		{"observations", "flows", "lookups"}, {"observations", "lookups", "flows"},
	}
	var reference []fullAttributionRow
	for index, order := range orders {
		network := newTestNetwork(t, 2, 6)
		data := orderingFixture(network, recordProvider(t, network.db, "resolver_cache"))
		var addresses []string
		for _, batch := range order {
			storeOrderingBatch(t, network, data, batch, &addresses)
		}
		got := fullAttributionRows(t, network.db)
		if index == 0 {
			reference = got
			methods := map[string]int{}
			for _, row := range got {
				methods[row.method]++
			}
			if methods[MethodResolverCacheAnswer] != 2 || methods[MethodLookupTiming] != 2 || len(got) != 4 {
				t.Fatalf("the reference order gives %v; want one chained cache answer and one timing "+
					"attribution per family, and no row for the contradicted and the ambiguous flows", got)
			}
		} else if fmt.Sprint(got) != fmt.Sprint(reference) {
			t.Errorf("the order %v gives\n%v\nand %v gives\n%v", order, got, orders[0], reference)
		}
		again, err := network.db.AttributeWith(context.Background(), network.now-7200, network.now,
			testWindows, network.now)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.FlowInstants) != 0 {
			t.Errorf("the order %v: a second derivation rewrote %d attributions", order, len(again.FlowInstants))
		}
	}
}

// TestAnObservationStoredAPassLaterChangesTheAttributionAtThatPass is AC10's second point.
func TestAnObservationStoredAPassLaterChangesTheAttributionAtThatPass(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 6)
	data := orderingFixture(network, recordProvider(t, network.db, "resolver_cache"))
	var addresses []string
	storeOrderingBatch(t, network, data, "flows", &addresses)
	storeOrderingBatch(t, network, data, "lookups", &addresses)
	before := fullAttributionRows(t, network.db)
	for _, row := range before {
		if row.method != MethodLookupTiming {
			t.Fatalf("before any resolver record an attribution is %v", row)
		}
	}
	changed := storeOrderingBatch(t, network, data, "observations", &addresses)
	if len(changed.FlowInstants) == 0 {
		t.Fatal("the pass that stored the observations changed no attribution")
	}
	after := fullAttributionRows(t, network.db)
	if fmt.Sprint(after) == fmt.Sprint(before) {
		t.Fatal("the observations stored a pass later left every attribution as it was")
	}
	cache := 0
	for _, row := range after {
		if row.method == MethodResolverCacheAnswer {
			cache++
		}
	}
	if cache == 0 {
		t.Error("no attribution became a resolver_cache_answer at the pass that stored the evidence")
	}
}

// TestNoAttributionInventsADomain is AC11, on the ordering fixture's outcomes: every
// site_name is its lookup's domain and that lookup's client is the flow's source client
// -- the live validation's V2 query, which must count nothing.
func TestNoAttributionInventsADomain(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 6)
	data := orderingFixture(network, recordProvider(t, network.db, "resolver_cache"))
	var addresses []string
	for _, batch := range []string{"observations", "flows", "lookups"} {
		storeOrderingBatch(t, network, data, batch, &addresses)
	}
	if total := queryInt(t, network.db, "SELECT count(*) FROM domain_attribution"); total == 0 {
		t.Fatal("nothing was attributed, so the check has nothing to hold")
	}
	invented := queryInt(t, network.db, `SELECT count(*) FROM domain_attribution a
		JOIN dns_resolution r ON r.id = a.dns_resolution_id
		JOIN flow f ON f.id = a.flow_id
		WHERE a.site_name <> r.domain OR r.looked_up_at > f.observed_at
		   OR NOT (CASE WHEN f.src_client_id IS NOT NULL AND r.client_id IS NOT NULL
		                THEN r.client_id = f.src_client_id
		                ELSE r.client_address = f.src_address END)`)
	if invented != 0 {
		t.Errorf("%d attributions name a domain their lookup did not, or a lookup another client made", invented)
	}
}

// TestTheMethodCheckRefusesAnUnknownValue is AC12's Go half: the column exists, refuses a
// third method, and requires the observation exactly for the cache method.
func TestTheMethodCheckRefusesAnUnknownValue(t *testing.T) {
	t.Parallel()
	scenario := newCacheScenario(t, false, false)
	scenario.lookup(t, "checked.example.com", 2, "Recursion", "pass")
	scenario.attribute(t)
	if got := scenario.rows(t); len(got) != 1 {
		t.Fatalf("the scenario gives %v", got)
	}
	for _, statement := range []string{
		"UPDATE domain_attribution SET method = 'suricata_dns'",
		"UPDATE domain_attribution SET method = 'resolver_cache_answer'",
	} {
		_, err := scenario.network.db.DB().Exec(statement)
		if err == nil || !strings.Contains(err.Error(), "constraint failed") {
			t.Errorf("%s gave %v, not a constraint failure", statement, err)
		}
	}
}

// diagnosticText reads one named query of sql/queries/diagnostics.sql.
func diagnosticText(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../sql/queries/diagnostics.sql")
	if err != nil {
		t.Fatalf("reading the diagnostics: %v", err)
	}
	marker := "-- diagnostic: " + name + "\n"
	start := strings.Index(string(raw), marker)
	if start < 0 {
		t.Fatalf("no diagnostic is named %q", name)
	}
	text := string(raw)[start+len(marker):]
	if end := strings.Index(text, "\n-- diagnostic: "); end >= 0 {
		text = text[:end]
	}
	return text
}

// perMethodFromDiagnostic sums the per-client diagnostic's per-method columns.
func perMethodFromDiagnostic(t *testing.T, database *Store, from, to int64) (int64, int64, int64, int64) {
	t.Helper()
	rows, err := database.DB().Query(diagnosticText(t, "Attribution rate per client"),
		sql.Named("window_start", from), sql.Named("window_end", to))
	if err != nil {
		t.Fatalf("running the diagnostic: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var eligible, named, cache, timing int64
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for index, column := range columns {
			value, _ := values[index].(int64)
			switch column {
			case "flow_count":
				eligible += value
			case "attributed_count":
				named += value
			case "named_by_resolver_cache_answer":
				cache += value
			case "named_by_lookup_timing":
				timing += value
			}
		}
	}
	return eligible, named, cache, timing
}

// checkRateAgreesWithTheDiagnostic holds ReadAttributionRate and the diagnostic to the
// same per-method counts, summing to the named total.
func checkRateAgreesWithTheDiagnostic(t *testing.T, database *Store, rate AttributionRate, from, to int64) {
	t.Helper()
	eligible, named, cache, timing := perMethodFromDiagnostic(t, database, from, to)
	if rate.NamedByCacheAnswer+rate.NamedByLookupTiming != rate.NamedFlows {
		t.Errorf("the per-method counts %d + %d do not sum to the %d named", rate.NamedByCacheAnswer,
			rate.NamedByLookupTiming, rate.NamedFlows)
	}
	if eligible != rate.EligibleFlows || named != rate.NamedFlows || cache != rate.NamedByCacheAnswer ||
		timing != rate.NamedByLookupTiming {
		t.Errorf("the diagnostic counts %d eligible, %d named, %d by the cache and %d by timing; "+
			"ReadAttributionRate %d, %d, %d and %d", eligible, named, cache, timing, rate.EligibleFlows,
			rate.NamedFlows, rate.NamedByCacheAnswer, rate.NamedByLookupTiming)
	}
}

// TestTheOtherFamilyTimingFallbackDiagnosticCountsAPlantedCase is
// specs/SPEC-resolver-cache-closing.md AC5: live_timing_fallback_other_family_cached
// counts a lookup_timing attribution whose name the cache held in the other address
// family only, at the flow's instant, and does not count one whose name it did not
// hold at all.
func TestTheOtherFamilyTimingFallbackDiagnosticCountsAPlantedCase(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		for _, testCase := range []struct {
			name        string
			otherFamily bool
			want        int64
		}{
			{"the name held in the other family only", true, 1},
			{"the name not held at all", false, 0},
		} {
			scenario := newCacheScenario(t, v6, false)
			scenario.lookup(t, "other-family.example.com", 3, "Recursion", "pass")
			if testCase.otherFamily {
				// Through a CNAME, so the diagnostic follows the chain as the rule does.
				pollCache(t, scenario.network.db, scenario.flowAt-30,
					cnameRecord("other-family.example.com", "edge-other-family.example.net", 300),
					addressRecord("edge-other-family.example.net", outside(47, !v6), 300))
			}
			scenario.attribute(t)
			if got := scenario.rows(t); len(got) != 1 || got[0].method != MethodLookupTiming {
				t.Fatalf("IPv6 %t, %s: the attribution is %v, want one by lookup_timing", v6, testCase.name, got)
			}
			var counted int64
			if err := scenario.network.db.DB().QueryRow(
				diagnosticText(t, "live_timing_fallback_other_family_cached"),
				sql.Named("window_start", scenario.flowAt-600), sql.Named("window_end", scenario.flowAt+1),
			).Scan(&counted); err != nil {
				t.Fatalf("running the diagnostic: %v", err)
			}
			if counted != testCase.want {
				t.Errorf("IPv6 %t, %s: the diagnostic counts %d, want %d", v6, testCase.name, counted, testCase.want)
			}
		}
	}
}

// TestTheOtherFamilyDiagnosticDoesNotCountANameHeldInBothFamilies is
// specs/SPEC-resolver-cache-follow-ups.md AC2: a lookup_timing attribution whose name the
// cache held in both families at the flow's instant is not counted. The cache's first
// poll comes after the lookup, so its records cover the flow and not the lookup: neither
// the exact method nor the contradiction sees them, timing names the flow, and the
// diagnostic, which reads the flow's instant, sees both families.
func TestTheOtherFamilyDiagnosticDoesNotCountANameHeldInBothFamilies(t *testing.T) {
	t.Parallel()
	for _, v6 := range []bool{false, true} {
		scenario := newCacheScenario(t, v6, false)
		scenario.lookup(t, "both-families.example.com", 3, "Recursion", "pass")
		pollCache(t, scenario.network.db, scenario.flowAt-1,
			addressRecord("both-families.example.com", scenario.destination, 300),
			addressRecord("both-families.example.com", outside(50, !v6), 300))
		scenario.attribute(t)
		if got := scenario.rows(t); len(got) != 1 || got[0].method != MethodLookupTiming {
			t.Fatalf("IPv6 %t: the attribution is %v, want one by lookup_timing", v6, got)
		}
		if counted := scenario.otherFamilyDiagnostic(t); counted != 0 {
			t.Errorf("IPv6 %t: a name held in both families is counted %d times, want 0", v6, counted)
		}
	}
}

// TestTheRateWithNoResolverIsUndefined is AC13's first state.
func TestTheRateWithNoResolverIsUndefined(t *testing.T) {
	t.Parallel()
	scenario := newCacheScenario(t, true, false)
	scenario.attribute(t)
	from, to := scenario.flowAt-600, scenario.flowAt+1
	rate, err := scenario.network.db.ReadAttributionRate(context.Background(), from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rate.Rate != nil || rate.ResolverCovered || rate.CacheCovered || rate.EligibleFlows == 0 {
		t.Errorf("with no resolver the rate reads %+v; want undefined, no coverage of either kind, and the eligible flow", rate)
	}
	checkRateAgreesWithTheDiagnostic(t, scenario.network.db, rate, from, to)
}

// TestTheRateWithTheQueryReportOnlyCountsTiming is AC13's second state.
func TestTheRateWithTheQueryReportOnlyCountsTiming(t *testing.T) {
	t.Parallel()
	scenario := newCacheScenario(t, false, false)
	scenario.lookup(t, "report-only.example.com", 2, "Recursion", "pass")
	scenario.attribute(t)
	from, to := scenario.flowAt-600, scenario.flowAt+1
	rate, err := scenario.network.db.ReadAttributionRate(context.Background(), from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rate.Rate == nil || !rate.ResolverCovered || rate.CacheCovered || rate.NamedByLookupTiming != 1 ||
		rate.NamedByCacheAnswer != 0 {
		t.Errorf("with the query report alone the rate reads %+v", rate)
	}
	checkRateAgreesWithTheDiagnostic(t, scenario.network.db, rate, from, to)
}

// TestTheRateWithTheQueryReportAndTheCacheCountsBothMethods is AC13's third state.
func TestTheRateWithTheQueryReportAndTheCacheCountsBothMethods(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 6)
	data := orderingFixture(network, recordProvider(t, network.db, "resolver_cache"))
	var addresses []string
	for _, batch := range []string{"flows", "lookups", "observations"} {
		storeOrderingBatch(t, network, data, batch, &addresses)
	}
	from, to := network.now-7200, network.now+1
	rate, err := network.db.ReadAttributionRate(context.Background(), from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rate.Rate == nil || !rate.ResolverCovered || !rate.CacheCovered || rate.NamedByCacheAnswer != 2 ||
		rate.NamedByLookupTiming != 2 {
		t.Errorf("with the query report and the cache the rate reads %+v", rate)
	}
	checkRateAgreesWithTheDiagnostic(t, network.db, rate, from, to)
}

// TestResolverRecordsAreStoredWhateverTheAggregateModeSays is AC14's first point, decision
// 7: the mode withholds names at the API, and the store writes them in both modes.
func TestResolverRecordsAreStoredWhateverTheAggregateModeSays(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	if err := database.SetSetting(context.Background(), "aggregate_mode", "no_domains", referenceNow); err != nil {
		t.Fatal(err)
	}
	pollCache(t, database, referenceNow, addressRecord("mode.example.com", outside(9, false), 60),
		addressRecord("mode.example.com", outside(9, true), 60))
	pollLocalData(t, database, referenceNow, localAddressRecord("host.example.test", outside(10, false)))
	if stored := queryInt(t, database, "SELECT count(*) FROM resource_record_observation"); stored != 3 {
		t.Errorf("no_domains stored %d resolver records, not the 3 read", stored)
	}
}

// TestThePurgeRemovesEndedObservationsAndKeepsReferencedOnes is AC14's second point.
func TestThePurgeRemovesEndedObservationsAndKeepsReferencedOnes(t *testing.T) {
	t.Parallel()
	scenario := newCacheScenario(t, false, false)
	database := scenario.network.db
	scenario.lookup(t, "kept.example.com", 2, "Recursion", "pass")
	// An unreferenced record whose coverage ends long before the flow, the record that
	// names the flow, and a record covering past the horizon below.
	pollCache(t, database, scenario.flowAt-1000, addressRecord("ended.example.com", outside(44, false), 40))
	pollCache(t, database, scenario.flowAt-30, addressRecord("kept.example.com", scenario.destination, 40))
	pollCache(t, database, scenario.flowAt+5000, addressRecord("current.example.com", outside(45, false), 300))
	scenario.attribute(t)
	if got := scenario.rows(t); len(got) != 1 || got[0].method != MethodResolverCacheAnswer {
		t.Fatalf("the scenario gives %v", got)
	}
	// The horizon below is flowAt - 500, so the attributed flow is kept. Its observation
	// covers the flow, so the rule alone never leaves it ended before the horizon; it is
	// aged here so the purge's guard is what keeps it -- without the guard the foreign key
	// would refuse the purge outright.
	if _, err := database.DB().Exec(`UPDATE resource_record_observation
		SET covered_from_at = ?, first_seen_at = ?, last_seen_at = ?, covered_until_at = ?
		WHERE owner_name = 'kept.example.com'`, scenario.flowAt-1000, scenario.flowAt-1000,
		scenario.flowAt-1000, scenario.flowAt-900); err != nil {
		t.Fatalf("ageing the referenced observation: %v", err)
	}
	if err := database.SetSetting(context.Background(), "retention_seconds", "3000", scenario.network.now); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(context.Background(), scenario.flowAt+2500); err != nil {
		t.Fatalf("purging: %v", err)
	}
	for owner, want := range map[string]int{"kept.example.com": 1, "ended.example.com": 0, "current.example.com": 1} {
		if got := len(observationsOf(t, database, owner)); got != want {
			t.Errorf("after the purge %s has %d observations, not %d", owner, got, want)
		}
	}
	if violations := foreignKeyViolations(t, database); violations != 0 {
		t.Errorf("after the purge, PRAGMA foreign_key_check reports %d rows", violations)
	}
}

// TestALoggedHostNameNoLeaseNamesResolvesThroughTheLocalData is AC16's store half: one
// address answering gives local_data_hostname, two give ambiguous_hostname, and the local
// data valid at the lookup's instant is what is read.
func TestALoggedHostNameNoLeaseNamesResolvesThroughTheLocalData(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	pollLocalData(t, database, referenceNow,
		localAddressRecord("single.example.test", outside(11, true)),
		localAddressRecord("double.example.test", outside(12, false)),
		localAddressRecord("double.example.test", outside(13, true)))
	pollLocalData(t, database, referenceNow+300,
		localAddressRecord("single.example.test", outside(11, true)),
		localAddressRecord("double.example.test", outside(12, false)),
		localAddressRecord("double.example.test", outside(13, true)))
	for _, testCase := range []struct {
		logged, wantResolution, wantAddress string
		at                                  int64
	}{
		{"SINGLE.example.test.", ClientResolutionLocalData, outside(11, true), referenceNow + 100},
		{"single", ClientResolutionLocalData, outside(11, true), referenceNow + 300},
		{"double.example.test", ClientResolutionAmbiguous, "double.example.test", referenceNow + 100},
		{"single.example.test", ClientResolutionUnknown, "single.example.test", referenceNow + 301},
		{"absent.example.test", ClientResolutionUnknown, "absent.example.test", referenceNow + 100},
	} {
		address, resolution, err := database.ResolveLoggedHostname(ctx, testCase.logged, testCase.at)
		if err != nil {
			t.Fatal(err)
		}
		if resolution != testCase.wantResolution || address != testCase.wantAddress {
			t.Errorf("%s at %+d resolves to %s, %s; want %s, %s", testCase.logged, testCase.at-referenceNow,
				address, resolution, testCase.wantAddress, testCase.wantResolution)
		}
	}
}

// TestTheNewStatementsSearchAnIndex is AC15's Go half for the statements the
// resolver-cache cycle adds: none plans a scan of a growing table (sql/schema-checks.sh
// repeats this over every statement at both seed sizes).
func TestTheNewStatementsSearchAnIndex(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	for _, name := range []string{
		"resource_records_of_address", "resource_record_cname_owners", "resource_records_of_name",
		"attribution_name_lookups", "local_data_addresses_for_hostname", "resource_record_current",
		"read_cache_coverage",
	} {
		text, err := Statement(name)
		if err != nil {
			t.Fatal(err)
		}
		var arguments []any
		for _, parameter := range []string{"address", "at", "name", "start", "end", "client_id", "label",
			"provider_id", "owner_name", "rrtype", "value", "previous_at", "from", "to"} {
			if usesParameter(text, parameter) {
				arguments = append(arguments, sql.Named(parameter, referenceNow))
			}
		}
		rows, err := database.DB().Query("EXPLAIN QUERY PLAN "+text, arguments...)
		if err != nil {
			t.Fatalf("planning %s: %v", name, err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"resource_record_observation", "dns_resolution", "flow"} {
				if strings.HasPrefix(detail, "SCAN") && containsWord(detail, table) {
					t.Errorf("%s scans %s: %s", name, table, detail)
				}
			}
		}
		_ = rows.Close()
	}
}

// TestTheDatabaseIsOpenedWithSynchronousNormalBesideWAL is AC20.
func TestTheDatabaseIsOpenedWithSynchronousNormalBesideWAL(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	if synchronous := queryInt(t, database, "PRAGMA synchronous"); synchronous != 1 {
		t.Errorf("PRAGMA synchronous is %d, not 1 (NORMAL)", synchronous)
	}
	var mode string
	if err := database.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("PRAGMA journal_mode is %q, not wal", mode)
	}
	source, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, citation := range []string{"https://www.sqlite.org/pragma.html#pragma_synchronous",
		"https://www.sqlite.org/wal.html", "9 October\n\t// 2026"} {
		if !strings.Contains(string(source), citation) {
			t.Errorf("store.go does not document the trade with %q", citation)
		}
	}
}
