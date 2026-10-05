package store

import (
	"context"
	"testing"
)

// The measurement-sample tests that belong to the promotion rather than to 4A's sampler.
//
// Two things changed when measurement_sample became a kind: its three vocabularies stopped being
// closed CHECKs, because the eight surveyed sources that fit its shape share no vocabulary at
// all, and the provider became part of a reading's identity, because the kind admits several
// concurrently active providers.

// TestAProviderCanIntroduceASubjectMeasureAndUnitTheSchemaDidNotShipWith is the extensibility
// the promotion is for.
//
// A UPS reports volts, SMART reports reallocated sectors, and HAProxy's subject is a backend and
// not an interface or a client. Under a closed CHECK each of those would have been a schema
// change, which is exactly the plugin-hostile design the promotion removes. So the test reads the
// schema before and after and asserts that nothing in it moved.
func TestAProviderCanIntroduceASubjectMeasureAndUnitTheSchemaDidNotShipWith(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	schemaBefore := schemaDefinitions(t, database)

	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatalf("looking up the measurement provider: %v", err)
	}
	newTerms := MeasurementSample{
		ProviderID:  &providerID,
		SubjectKind: "example-power-supply",
		SubjectKey:  "example-unit-one",
		Measure:     "example_input_volts",
		Unit:        "example_volt",
		Value:       231.5,
		SampledAt:   1750000000,
	}
	if err := database.InsertMeasurementSample(ctx, newTerms); err != nil {
		t.Fatalf("a provider's own subject, measure and unit were refused: %v", err)
	}

	// It reads back, unchanged and under its own terms.
	var (
		subjectKind, measure, unit string
		value                      float64
	)
	if err := database.DB().QueryRowContext(ctx,
		`SELECT subject_kind, measure, unit, value FROM measurement_sample
		 WHERE subject_key = ?`, newTerms.SubjectKey).
		Scan(&subjectKind, &measure, &unit, &value); err != nil {
		t.Fatalf("reading the provider's reading back: %v", err)
	}
	if subjectKind != string(newTerms.SubjectKind) || measure != string(newTerms.Measure) ||
		unit != string(newTerms.Unit) || value != newTerms.Value {
		t.Errorf("the reading came back as %s/%s/%s/%v", subjectKind, measure, unit, value)
	}

	if after := schemaDefinitions(t, database); after != schemaBefore {
		t.Error("introducing a term changed the schema, so it was not extensible after all")
	}
}

// TestAMeasurementTermThatIsNotAWellFormedTokenIsRejected is the restatement of the closed-CHECK
// assertions, and it is what keeps extensibility from meaning free text.
//
// The vocabularies are no longer a list, so membership cannot be checked; what is checked instead
// is the SHAPE of a term. A term that is empty, upper-cased or spaced would let two spellings of
// one measure both exist, and a screen grouping by measure would then show one reading twice
// under two names.
func TestAMeasurementTermThatIsNotAWellFormedTokenIsRejected(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	base := MeasurementSample{
		SubjectKind: SubjectFirewall, SubjectKey: "", Measure: MeasureUptimeSeconds,
		Unit: UnitSecond, Value: 1, SampledAt: 1750000000,
	}
	for name, mutate := range map[string]func(sample *MeasurementSample){
		"a subject kind that is empty":     func(s *MeasurementSample) { s.SubjectKind = "" },
		"a subject kind carrying a space":  func(s *MeasurementSample) { s.SubjectKind = "example subject" },
		"a subject kind that is not lower": func(s *MeasurementSample) { s.SubjectKind = "Firewall" },
		"a measure that is empty":          func(s *MeasurementSample) { s.Measure = "" },
		"a measure carrying a space":       func(s *MeasurementSample) { s.Measure = "uptime seconds" },
		"a measure that is not lower":      func(s *MeasurementSample) { s.Measure = "Uptime_Seconds" },
		"a unit that is empty":             func(s *MeasurementSample) { s.Unit = "" },
		"a unit carrying a space":          func(s *MeasurementSample) { s.Unit = "bit per second" },
		"a unit that is not lower":         func(s *MeasurementSample) { s.Unit = "Celsius" },
	} {
		sample := base
		mutate(&sample)
		if err := database.InsertMeasurementSample(ctx, sample); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestAFirewallGaugeNamesNoProviderAndASampledReadingNamesItsOwn is the attribution rule, both
// ways, which is what the criterion asks for.
//
// The firewall's own telemetry implements no external contract — it is the machine reporting on
// itself — and attributing it to a source would say that source measured the temperature. A
// reading a provider supplied names that provider, because it is that provider's material.
func TestAFirewallGaugeNamesNoProviderAndASampledReadingNamesItsOwn(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatalf("looking up the measurement provider: %v", err)
	}
	if err := database.InsertMeasurementSample(ctx, MeasurementSample{
		SubjectKind: SubjectFirewall, SubjectKey: "", Measure: MeasureUptimeSeconds,
		Unit: UnitSecond, Value: 3600, SampledAt: 1750000000,
	}); err != nil {
		t.Fatalf("writing a firewall gauge: %v", err)
	}
	if err := database.InsertMeasurementSample(ctx, MeasurementSample{
		ProviderID: &providerID, SubjectKind: SubjectInterfaceEndpointPair,
		SubjectKey: "example-endpoint-a example-endpoint-b",
		Measure:    MeasureCumulativeBytesIn, Unit: UnitByte, Value: 1000, SampledAt: 1750000000,
	}); err != nil {
		t.Fatalf("writing a sampled pair volume: %v", err)
	}

	var attributed int
	if err := database.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM measurement_sample
		 WHERE subject_kind = 'firewall' AND provider_id IS NOT NULL`).Scan(&attributed); err != nil {
		t.Fatalf("counting attributed gauges: %v", err)
	}
	if attributed != 0 {
		t.Errorf("%d firewall gauges name a provider", attributed)
	}

	var unattributed int
	if err := database.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM measurement_sample
		 WHERE subject_kind = 'interface_endpoint_pair' AND provider_id IS NULL`).Scan(&unattributed); err != nil {
		t.Fatalf("counting unattributed pair readings: %v", err)
	}
	if unattributed != 0 {
		t.Errorf("%d sampled pair readings name no provider", unattributed)
	}
}

// TestTwoProvidersReadingOneSubjectAtOneInstantAreTwoReadingsAndOneGaugeIsStillIdempotent is the
// consequence of the provider joining a reading's identity, and the trap that came with it.
//
// Two active measurement providers reading the same subject and measure at the same instant are
// two readings and must both survive. But the firewall's own gauges carry a NULL provider, and
// SQLite treats nulls in a uniqueness constraint as distinct — so a bare column in the key would
// have made every gauge non-idempotent, which is the opposite of what a sampler restarting inside
// one interval needs. The index wraps the column in ifnull, and both halves are asserted here.
func TestTwoProvidersReadingOneSubjectAtOneInstantAreTwoReadingsAndOneGaugeIsStillIdempotent(t *testing.T) {
	database, _ := openTestStore(t)
	ctx := context.Background()

	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
		 VALUES ('measurement_sample', 'example-second-sampler', 'Example second sampler',
		         1, 1750000000)`); err != nil {
		t.Fatalf("registering a second measurement provider: %v", err)
	}
	first, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatalf("looking up the first measurement provider: %v", err)
	}
	second, err := database.ProviderID(ctx, "measurement_sample", "example-second-sampler")
	if err != nil {
		t.Fatalf("looking up the second measurement provider: %v", err)
	}

	reading := MeasurementSample{
		SubjectKind: SubjectInterface, SubjectKey: "exdev0", Measure: MeasureBytesIn,
		Unit: UnitByte, Value: 1000, SampledAt: 1750000000,
	}
	for _, provider := range []*int64{&first, &second} {
		attributed := reading
		attributed.ProviderID = provider
		if err := database.InsertMeasurementSample(ctx, attributed); err != nil {
			t.Fatalf("writing a reading: %v", err)
		}
	}
	if stored := count(t, database, "measurement_sample"); stored != 2 {
		t.Errorf("two providers reading one subject at one instant stored %d rows, want 2", stored)
	}

	// And the gauge nobody reported, offered three times, is one row.
	gauge := MeasurementSample{
		SubjectKind: SubjectFirewall, SubjectKey: "", Measure: MeasureUptimeSeconds,
		Unit: UnitSecond, Value: 3600, SampledAt: 1750000000,
	}
	for pass := 0; pass < 3; pass++ {
		if err := database.InsertMeasurementSample(ctx, gauge); err != nil {
			t.Fatalf("pass %d of the gauge: %v", pass, err)
		}
	}
	var gauges int
	if err := database.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM measurement_sample WHERE provider_id IS NULL`).Scan(&gauges); err != nil {
		t.Fatalf("counting the gauges: %v", err)
	}
	if gauges != 1 {
		t.Errorf("three passes of one provider-less gauge stored %d rows", gauges)
	}
}

// schemaDefinitions renders every object in the database's schema, so a test can assert that a
// statement changed no DDL at all.
func schemaDefinitions(t *testing.T, database *Store) string {
	t.Helper()
	rows, err := database.DB().QueryContext(context.Background(),
		`SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master
		 ORDER BY type, name`)
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	defer func() { _ = rows.Close() }()

	fingerprint := ""
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("reading the schema: %v", err)
		}
		fingerprint += line + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	return fingerprint
}
