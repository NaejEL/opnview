package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The write-path tests. Each one is a guarantee the model rests on, exercised against a
// real SQLite database under t.TempDir rather than against a mock, because the
// guarantees are constraints in the schema and a mock would assert nothing about them.

// TestIngestingTheSamePageTwiceProducesTheSameFlowCount is idempotence on the filter
// log, including the record the endpoint echoes back.
//
// The endpoint returns the record matching a supplied digest, so every poll re-offers
// rows already stored; without the uniqueness on __digest__ each poll would inflate
// every volume that sums packet_bytes.
func TestIngestingTheSamePageTwiceProducesTheSameFlowCount(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	page := examplePage()

	for _, flow := range page {
		if err := database.InsertFlow(ctx, flow); err != nil {
			t.Fatalf("the first pass failed: %v", err)
		}
	}
	afterOne := count(t, database, "flow")
	for _, flow := range page {
		if err := database.InsertFlow(ctx, flow); err != nil {
			t.Fatalf("the second pass failed: %v", err)
		}
	}
	afterTwo := count(t, database, "flow")

	if afterOne != len(page) {
		t.Fatalf("one pass stored %d of %d records", afterOne, len(page))
	}
	if afterTwo != afterOne {
		t.Fatalf("a second pass of the same page stored %d rows, up from %d", afterTwo, afterOne)
	}
}

// TestIngestingTheSameSecurityEventTwiceProducesTheSameCount is idempotence on the
// alert feed, keyed on (provider_id, provider_event_key) — the key the provider itself
// guarantees stable.
func TestIngestingTheSameSecurityEventTwiceProducesTheSameCount(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "security_event", "suricata")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}

	event := SecurityEvent{
		ProviderEventKey: "3:8192",
		OccurredAt:       1750000000,
		IngestedAt:       1750000030,
		RuleIdentity:     "example-rule-identity",
		Signature:        "example signature text",
		EventAction:      "blocked",
		SrcAddress:       "example-source",
		DstAddress:       "example-destination",
	}
	for pass := 0; pass < 3; pass++ {
		if err := database.InsertSecurityEvent(ctx, providerID, event); err != nil {
			t.Fatalf("pass %d failed: %v", pass, err)
		}
	}
	if stored := count(t, database, "security_event"); stored != 1 {
		t.Fatalf("three passes of one event stored %d rows", stored)
	}
}

// TestTheTwoDirectionsOfOnePairCollapseToOneVolume is the de-duplication the firewall's
// own aggregate made necessary, restated against the derivation that now fills the table.
//
// The property has not stopped mattering — a per-pair figure is direction-free whether it
// came from the firewall's aggregate or from opnview's own flow records — but the actor
// has changed: pair_volume_observation is DERIVED from `flow` by step 5, so there is no
// write path to exercise. What is asserted here is therefore the contract that derivation
// must respect: offered under canonical ordering, the two directions of one pair are one
// row, and the schema rejects a row stored the other way round.
func TestTheTwoDirectionsOfOnePairCollapseToOneVolume(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	insert := func(endpointA, endpointB, direction string) error {
		low, high := endpointA, endpointB
		if low > high {
			low, high = high, low
		}
		_, err := database.DB().ExecContext(ctx,
			`INSERT INTO pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
			     service_port, protocol, octets, packets, observed_direction,
			     observed_interface_device, last_seen_at, ingested_at)
			 VALUES (1749945600, ?, ?, 443, 'tcp', 1000, 10, ?, NULL, 1750000000, 1750000000)
			 ON CONFLICT (day_start_at, endpoint_low, endpoint_high, service_port, protocol)
			 DO NOTHING`,
			low, high, direction)
		return err
	}

	for _, offered := range []struct{ a, b, direction string }{
		{"example-endpoint-b", "example-endpoint-a", "out"},
		{"example-endpoint-a", "example-endpoint-b", "in"},
		{"example-endpoint-b", "example-endpoint-a", "out"},
		{"example-endpoint-a", "example-endpoint-b", "in"},
	} {
		if err := insert(offered.a, offered.b, offered.direction); err != nil {
			t.Fatalf("deriving a pair volume: %v", err)
		}
	}
	if stored := count(t, database, "pair_volume_observation"); stored != 1 {
		t.Fatalf("two directions of one pair, offered twice each, stored %d rows", stored)
	}

	// And the stored ordering is canonical, which is what the schema's CHECK requires
	// and what makes a later read able to find the pair from either end.
	var low, high string
	if err := database.DB().QueryRowContext(ctx,
		"SELECT endpoint_low, endpoint_high FROM pair_volume_observation").Scan(&low, &high); err != nil {
		t.Fatalf("reading the pair back: %v", err)
	}
	if low > high {
		t.Fatalf("the stored pair is (%q, %q), which is not in lexicographic order", low, high)
	}
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
		     service_port, protocol, octets, packets, observed_direction,
		     observed_interface_device, last_seen_at, ingested_at)
		 VALUES (1749945600, 'example-endpoint-z', 'example-endpoint-a', 80, 'tcp',
		         1, 1, 'in', NULL, 1750000000, 1750000000)`); err == nil {
		t.Fatal("a pair stored in the wrong order was accepted, so the canonical ordering " +
			"the derivation has to apply is not enforced")
	}
}

// TestNoCodePathWritesThePairVolumeTable is the other half of the same ruling, and it is the
// assertion that makes the ruling durable rather than a comment.
//
// pair_volume_observation is derived from `flow` and collected from nowhere: the only per-pair
// endpoint the survey found carries neither a port nor a protocol, and the filter log carries
// both exactly. So no collector fills it, and this test fails the day one appears — which is
// what makes step 5's derivation a deliberate change rather than a drift. The test's own
// statements are excluded by construction: it scans the non-test sources.
//
// The scan NORMALISES EACH FILE WHOLE rather than reading it line by line, and that is what
// makes it as strict as the ruling: a long SQL statement in Go is ordinarily wrapped, either as
// a raw literal spanning lines or as quoted fragments joined with +, so a collector that put
// the table name on the line after INSERT INTO would have written the derived table and passed
// a per-line match. Both wrappings collapse here.
//
// EVERY WRITING VERB IS ENUMERATED, NOT ONLY INSERT, and the reason is worth recording because
// an earlier revision of this guard got it wrong: it enumerated the six INSERT conflict clauses
// and claimed that covered the statement, which is true of conflict clauses and false of the
// statement. REPLACE INTO is SQLite's own alias for INSERT OR REPLACE and is the most natural
// way to re-derive a day's slot -- precisely what step 5 will be doing -- so a guard blind to
// it was blind to the likeliest write there is. UPDATE and DELETE are writes too.
func TestNoCodePathWritesThePairVolumeTable(t *testing.T) {
	// The characters a Go source file puts between two halves of one wrapped SQL statement, and
	// which no SQL identifier contains: the quote and backtick that end and begin a literal, and
	// the + that joins them. Removing them lets `"INSERT INTO " + "pair_volume_observation"` read
	// as the one statement it is.
	// The characters and qualifiers a Go source file can put between a verb and its table, none
	// of which can occur inside an SQL identifier: the quote, backtick and bracket that delimit
	// a literal or a quoted name, the + that joins two fragments, and the schema qualifier.
	sqlLiteralNoise := strings.NewReplacer(
		"\"", " ", "`", " ", "+", " ", "[", " ", "]", " ", "main.", "")
	const table = "pair_volume_observation"
	writeContexts := []string{
		"insert into " + table,
		"insert or ignore into " + table,
		"insert or replace into " + table,
		"insert or abort into " + table,
		"insert or fail into " + table,
		"insert or rollback into " + table,
		"replace into " + table,
		"delete from " + table,
		"update " + table + " set",
	}
	var offenders []string
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			normalised := strings.Join(strings.Fields(sqlLiteralNoise.Replace(
				strings.ToLower(string(source)))), " ")
			for _, context := range writeContexts {
				if strings.Contains(normalised, context) {
					offenders = append(offenders, path+": "+context)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("pair_volume_observation is derived from flow and collected from nowhere, "+
			"and these lines write it: %s", strings.Join(offenders, "; "))
	}
}

// TestTrafficScopeIsDerivedFromInterfaceMembershipAlone is the classification rule.
//
// East-west exactly when both endpoints sit in a discovered interface, north-south
// otherwise, and from nothing else: no address, no CIDR, no name, no assumed addressing
// plan. The write path computes it so a collector cannot disagree with the schema's
// CHECK.
func TestTrafficScopeIsDerivedFromInterfaceMembershipAlone(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	first, second := exampleTwoInterfaces(t, database)

	cases := []struct {
		name      string
		src, dst  *int64
		wantScope string
	}{
		{"both endpoints in a discovered interface", &first, &second, "east_west"},
		{"only the source in one", &first, nil, "north_south"},
		{"only the destination in one", nil, &second, "north_south"},
		{"neither in one", nil, nil, "north_south"},
	}
	for index, testCase := range cases {
		flow := exampleFlow(index)
		flow.SrcInterfaceID = testCase.src
		flow.DstInterfaceID = testCase.dst
		if err := database.InsertFlow(ctx, flow); err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		var scope string
		if err := database.DB().QueryRowContext(ctx,
			"SELECT traffic_scope FROM flow WHERE log_digest = ?", flow.LogDigest).Scan(&scope); err != nil {
			t.Fatalf("%s: reading the scope back: %v", testCase.name, err)
		}
		if scope != testCase.wantScope {
			t.Errorf("%s: the scope is %q, want %q", testCase.name, scope, testCase.wantScope)
		}
	}
}

// TestAFlowWhoseJoinKeysResolveToNothingIsStoredWithItsState is the not-found rule.
//
// A device absent from the interface map and a rid matching no rule are both normal, and
// both are states on the row. Dropping such a row would lose a packet that really
// crossed the firewall, which is the opposite of what this product is for.
func TestAFlowWhoseJoinKeysResolveToNothingIsStoredWithItsState(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	unmapped := exampleFlow(100)
	unmapped.InterfaceLookupState = LookupNotFound
	unmapped.RuleLookupState = LookupNotFound
	rid := "example-rid-that-matches-no-rule"
	unmapped.Rid = &rid
	if err := database.InsertFlow(ctx, unmapped); err != nil {
		t.Fatalf("storing a flow whose join keys resolved to nothing: %v", err)
	}

	var (
		interfaceState string
		ruleState      string
		storedRid      *string
		interfaceID    *int64
		ruleID         *int64
	)
	if err := database.DB().QueryRowContext(ctx,
		`SELECT interface_lookup_state, rule_lookup_state, rid, src_interface_id, rule_id
		 FROM flow WHERE log_digest = ?`, unmapped.LogDigest).
		Scan(&interfaceState, &ruleState, &storedRid, &interfaceID, &ruleID); err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if interfaceState != "not_found" || ruleState != "not_found" {
		t.Fatalf("the states are %q and %q", interfaceState, ruleState)
	}
	if storedRid == nil || *storedRid != rid {
		t.Fatal("the raw rid was not kept, so the screen cannot show the firewall's own identifier")
	}
	if interfaceID != nil || ruleID != nil {
		t.Fatal("a not-found state points at a row")
	}
}

// TestAMeasurementOfEachKindRoundTrips is the sampled-measurement table doing the two
// jobs one table was created for: a firewall gauge, and a sampled per-pair volume.
func TestAMeasurementOfEachKindRoundTrips(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "flow_volume", "insight")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}

	samples := []MeasurementSample{
		{
			// The firewall-health half. provider_id is NULL: the firewall's own
			// telemetry implements no external contract.
			SubjectKind: SubjectFirewall, SubjectKey: "", Measure: MeasureUptimeSeconds,
			Unit: UnitSecond, Value: 987654, SampledAt: 1750000000,
		},
		{
			SubjectKind: SubjectFirewall, SubjectKey: "example-sensor-0",
			Measure: MeasureTemperatureCelsius, Unit: UnitCelsius, Value: 41.5,
			SampledAt: 1750000000,
		},
		{
			SubjectKind: SubjectInterface, SubjectKey: "example-device",
			Measure: MeasureBytesIn, Unit: UnitByte, Value: 123456, SampledAt: 1750000000,
		},
		{
			// The per-pair half, which exists because no endpoint answers for a past
			// window and opnview has to build that history itself.
			ProviderID: &providerID, SubjectKind: SubjectEndpointPair,
			SubjectKey: "example-endpoint-a example-endpoint-b",
			Measure:    MeasureCumulativeBytesIn, Unit: UnitByte, Value: 400,
			SampledAt: 1750000000,
		},
	}
	for _, sample := range samples {
		if err := database.InsertMeasurementSample(ctx, sample); err != nil {
			t.Fatalf("writing a %s reading: %v", sample.Measure, err)
		}
	}
	if stored := count(t, database, "measurement_sample"); stored != len(samples) {
		t.Fatalf("stored %d of %d readings", stored, len(samples))
	}

	// Re-reading the same instant is a no-op, which is what a sampler restarting inside
	// one interval needs.
	for _, sample := range samples {
		if err := database.InsertMeasurementSample(ctx, sample); err != nil {
			t.Fatalf("re-writing a %s reading: %v", sample.Measure, err)
		}
	}
	if stored := count(t, database, "measurement_sample"); stored != len(samples) {
		t.Fatalf("a second pass stored %d readings, up from %d", stored, len(samples))
	}

	for _, sample := range samples {
		var (
			unit  string
			value float64
		)
		if err := database.DB().QueryRowContext(ctx,
			`SELECT unit, value FROM measurement_sample
			 WHERE subject_kind = ? AND subject_key = ? AND measure = ? AND sampled_at = ?`,
			string(sample.SubjectKind), sample.SubjectKey, string(sample.Measure),
			sample.SampledAt).Scan(&unit, &value); err != nil {
			t.Fatalf("reading the %s reading back: %v", sample.Measure, err)
		}
		if unit != string(sample.Unit) || value != sample.Value {
			t.Errorf("the %s reading round-tripped as %s %v, want %s %v",
				sample.Measure, unit, value, sample.Unit, sample.Value)
		}
	}
}

// TestAMalformedMeasureTermIsRejected is the restatement of the assertion that used to be
// TestAMeasureOutsideTheVocabularyIsRejected, because the property that one named is one the
// promotion deliberately changed rather than one that stopped mattering. A measure outside the
// vocabulary is now ACCEPTED, by design; what is still refused is a malformed term.
//
// The vocabulary was closed for exactly one provider: the survey establishes the telemetry
// endpoints and not their field names, so 4A's list was opnview's own and complete. It cannot
// stay closed now that measurement_sample is a kind eight surveyed sources fit, none of which
// shares a vocabulary with the others. What the column still refuses is a term that is not a
// well-formed token — a spaced or upper-cased spelling would let two names for one measure
// coexist, and a screen grouping by measure would then show one reading twice. Extensibility is
// asserted in measurement_test.go, and the full shape table with it; this is the one direction
// worth keeping here, where the vocabulary assertion used to live.
func TestAMalformedMeasureTermIsRejected(t *testing.T) {
	database, _ := openTestStore(t)
	err := database.InsertMeasurementSample(context.Background(), MeasurementSample{
		SubjectKind: SubjectFirewall, Measure: Measure("example measure nobody declared"),
		Unit: UnitRatio, Value: 1, SampledAt: 1750000000,
	})
	if err == nil {
		t.Fatal("a measure that is not a well-formed token was accepted")
	}
}

// TestACollectionGapIsARowWithItsIntervalAndItsReason is the gap table doing its job: a
// window opnview did not cover is recorded, not smoothed over and not written as a zero.
func TestACollectionGapIsARowWithItsIntervalAndItsReason(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}

	detail := "the page began after the newest record stored"
	gap := CollectionGap{
		ProviderID: providerID, IntervalStartAt: 1749999000, IntervalEndAt: 1749999900,
		Reason: GapDigestOutsideWindow, Detail: &detail, DetectedAt: 1750000000,
	}
	if err := database.RecordCollectionGap(ctx, gap); err != nil {
		t.Fatalf("recording a gap: %v", err)
	}

	var (
		storedProvider int64
		start, end     int64
		reason         string
		storedDetail   *string
	)
	if err := database.DB().QueryRowContext(ctx,
		`SELECT provider_id, interval_start_at, interval_end_at, reason, detail
		 FROM collection_gap`).
		Scan(&storedProvider, &start, &end, &reason, &storedDetail); err != nil {
		t.Fatalf("reading the gap back: %v", err)
	}
	if storedProvider != providerID || start != gap.IntervalStartAt || end != gap.IntervalEndAt {
		t.Fatalf("the gap reads provider %d over [%d, %d]", storedProvider, start, end)
	}
	if reason != string(GapDigestOutsideWindow) {
		t.Fatalf("the reason is %q", reason)
	}
	if storedDetail == nil || *storedDetail != detail {
		t.Fatal("the gap carries no detail saying why")
	}
}

// TestAGapReasonOutsideTheVocabularyIsRejected keeps the reason a closed set. These are
// opnview's own detections, so enumerating them invents nothing — and an open column
// would let one collector describe a loss in words no screen reads.
func TestAGapReasonOutsideTheVocabularyIsRejected(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	err = database.RecordCollectionGap(ctx, CollectionGap{
		ProviderID: providerID, IntervalStartAt: 1, IntervalEndAt: 2,
		Reason: GapReason("example-reason-nobody-declared"), DetectedAt: 3,
	})
	if err == nil {
		t.Fatal("a gap reason outside the vocabulary was accepted")
	}
}

// TestAnEveWatermarkResumesWithoutLossOrDoubleCount is the durable cursor.
//
// Paging is offset-from-end-of-file and therefore unstable across polls, so the
// watermark is the only resume point. The uniqueness on (file_id, byte_offset) is what
// makes a restart unable to double-count.
func TestAnEveWatermarkResumesWithoutLossOrDoubleCount(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	sequence := int64(3)
	for pass := 0; pass < 2; pass++ {
		if err := database.UpsertEveCursor(ctx, EveCursor{
			FileID: "3", ByteOffset: 8192, FileSequence: &sequence,
			RotationState: "current", ObservedAt: 1750000000,
		}); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if stored := count(t, database, "eve_ingest_cursor"); stored != 1 {
		t.Fatalf("two passes at one offset stored %d watermarks", stored)
	}

	watermarks, err := database.EveWatermarks(ctx)
	if err != nil {
		t.Fatalf("reading the watermarks: %v", err)
	}
	if watermarks["3"].ByteOffset != 8192 {
		t.Fatalf("the watermark for file 3 is %d, want 8192", watermarks["3"].ByteOffset)
	}

	// A rotation that discarded the file is recorded as a permanent loss rather than
	// reset, because a reset would make the loss look like a successful read of nothing.
	if err := database.MarkEveFileLost(ctx, "3", 1750000100); err != nil {
		t.Fatalf("marking the file lost: %v", err)
	}
	watermarks, err = database.EveWatermarks(ctx)
	if err != nil {
		t.Fatalf("re-reading the watermarks: %v", err)
	}
	if watermarks["3"].RotationState != "lost" {
		t.Fatalf("the rotation state is %q, want lost", watermarks["3"].RotationState)
	}
	if watermarks["3"].ByteOffset != 8192 {
		t.Fatal("marking the file lost moved the watermark, so the loss is no longer bounded")
	}
}

// TestADiscoveryRefreshNeverOverwritesAUserLabel is the rule the schema and the write
// path both carry: relabelling never overwrites discovery, and discovery never
// overwrites a label.
func TestADiscoveryRefreshNeverOverwritesAUserLabel(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	iface := Interface{
		Identifier: "example_if_a", Device: "exdev0", Description: "example description A",
		LinkType: "example-link-type", LinkKind: "other",
	}
	id, err := database.UpsertInterface(ctx, iface, 1750000000)
	if err != nil {
		t.Fatalf("discovering an interface: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE interface SET user_label = ? WHERE id = ?",
		"a label the maintainer chose", id); err != nil {
		t.Fatalf("setting a label: %v", err)
	}

	// A later refresh reports a changed description, which must land, and must leave the
	// label alone.
	iface.Description = "example description A as it was later renamed"
	if _, err := database.UpsertInterface(ctx, iface, 1750000300); err != nil {
		t.Fatalf("refreshing discovery: %v", err)
	}

	var label, description string
	var firstSeen, lastSeen int64
	if err := database.DB().QueryRowContext(ctx,
		"SELECT user_label, description, first_seen_at, last_seen_at FROM interface WHERE id = ?", id).
		Scan(&label, &description, &firstSeen, &lastSeen); err != nil {
		t.Fatalf("reading the interface back: %v", err)
	}
	if label != "a label the maintainer chose" {
		t.Fatalf("a discovery refresh overwrote the label with %q", label)
	}
	if description != iface.Description {
		t.Fatalf("the refreshed description is %q", description)
	}
	if firstSeen != 1750000000 {
		t.Fatalf("first_seen_at moved to %d, so the row's history was rediscovered", firstSeen)
	}
	if lastSeen != 1750000300 {
		t.Fatalf("last_seen_at is %d, so the refresh was not recorded", lastSeen)
	}
}

// TestAClientReissuedAnAddressAfterTheIdleWindowStaysTwoClients is the substitute for
// the validity start the filter log does not carry, exercised at the storage boundary
// that implements it.
func TestAClientReissuedAnAddressAfterTheIdleWindowStaysTwoClients(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()
	address := "example-address"

	firstSeen := int64(1750000000)
	if _, err := database.UpsertClient(ctx, Client{
		Identity:    ClientIdentity{Kind: IdentityAddressInInterface, Key: "1|example-address|1749945600"},
		LastAddress: &address,
	}, firstSeen); err != nil {
		t.Fatalf("storing the first machine: %v", err)
	}

	// Within the window, the same address resolves to the same machine.
	if _, _, found, err := database.ClientRefByAddressSince(ctx, address, firstSeen-86400); err != nil {
		t.Fatalf("looking the machine up: %v", err)
	} else if !found {
		t.Fatal("the machine was not found inside the idle window")
	}

	// Beyond it, it does not, so the caller mints a second identity rather than merging
	// two machines into one phantom client.
	if _, _, found, err := database.ClientRefByAddressSince(ctx, address, firstSeen+86401); err != nil {
		t.Fatalf("looking the machine up beyond the window: %v", err)
	} else if found {
		t.Fatal("an address unseen for longer than the window still resolved to the old machine")
	}
}

// examplePage is a page of filter-log rows, as the collector would have built them.
func examplePage() []Flow {
	page := make([]Flow, 0, 3)
	for index := 0; index < 3; index++ {
		page = append(page, exampleFlow(index))
	}
	return page
}

// exampleFlow is one row. Every value is synthesised from the index: nothing here is an
// address, an identifier or a count read off anyone's network.
func exampleFlow(index int) Flow {
	digits := []byte{byte('0' + index%10)}
	return Flow{
		LogDigest:            "example-digest-" + string(digits),
		ObservedAt:           1750000000 - int64(index),
		IngestedAt:           1750000005,
		InterfaceDevice:      "example-device",
		InterfaceLookupState: LookupResolved,
		SrcAddress:           "example-source-" + string(digits),
		DstAddress:           "example-destination-" + string(digits),
		Protocol:             "tcp",
		IPVersion:            4,
		Action:               "pass",
		Direction:            "in",
		PacketBytes:          int64(100 + index),
		RuleLookupState:      LookupPending,
	}
}

// exampleTwoInterfaces discovers two interfaces and returns their ids.
func exampleTwoInterfaces(t *testing.T, database *Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	first, err := database.UpsertInterface(ctx, Interface{
		Identifier: "example_if_a", Device: "exdev0", Description: "example description A",
		LinkType: "example-link-type", LinkKind: "other",
	}, 1750000000)
	if err != nil {
		t.Fatalf("discovering the first interface: %v", err)
	}
	second, err := database.UpsertInterface(ctx, Interface{
		Identifier: "example_if_b", Device: "exdev1", Description: "example description B",
		LinkType: "example-link-type", LinkKind: "vlan",
	}, 1750000000)
	if err != nil {
		t.Fatalf("discovering the second interface: %v", err)
	}
	return first, second
}

// count counts a table, failing the test on an error.
func count(t *testing.T, database *Store, table string) int {
	t.Helper()
	value, err := database.CountRows(context.Background(), table)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return value
}
