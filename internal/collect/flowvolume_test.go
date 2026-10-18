package collect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The sampled-measurement collector tests.
//
// The one that matters most is the argument. /api/diagnostics/traffic/top takes interface
// NAMES, and a device name returns an empty array with HTTP 200 — indistinguishable from
// an absence of traffic. A wrong argument there would not fail; it would quietly record
// silence, and every screen downstream would show a working, quiet network. So the
// argument is checked at the wire, and the collector refuses rather than samples when it
// is about to send the wrong kind.

// arrangeMeasurementCollection stands up a harness whose volume source is active and whose
// telemetry answers.
func arrangeMeasurementCollection(t *testing.T) *collecttest.ProbeHarness {
	t.Helper()
	harness := collecttest.NewProbeHarness(t)
	collecttest.ArrangeDiscoverableFirewall(t, harness.Fake, harness.Collector)
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "netflow_is_enabled"), "netflow_is_enabled_local.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "traffic_top"), "traffic_top.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "traffic_interface"), "traffic_interface.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "system_resources"), "system_resources.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "system_temperature"), "system_temperature.json")
	harness.Fake.AnswerFixture(opnsense.SystemTime, "system_time.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "system_disk"), "system_disk.json")
	// The processor figure's source since the resolver-cache cycle: a stream that never ends.
	harness.Fake.AnswerStreamFixture(opnsense.CPUUsageStream, "cpu_usage_stream.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "gateway_status"), "gateway_status.json")
	harness.Fake.AnswerFixture(collecttest.Declared("insight", "system_swap"), "system_swap.json")
	if err := harness.Collector.ProbeKind(context.Background(), "measurement_sample"); err != nil {
		t.Fatalf("probing the volume source: %v", err)
	}
	if key := harness.ActiveKeyOf(t, "measurement_sample"); key != "insight" {
		t.Fatalf("the volume kind activated %q", key)
	}
	return harness
}

// TestThePerPairSamplerSendsInterfaceNamesAndNeverADeviceName is the measured trap, tested
// at the wire rather than at the type.
//
// The fixture's interfaces carry an identifier and a device that DIFFER on every row, so
// the assertion has teeth: every path element the fake received must be an identifier the
// firewall reported, and none of them may be a device name.
func TestThePerPairSamplerSendsInterfaceNamesAndNeverADeviceName(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	harness.Fake.AssertEveryPathIsRegistered()

	snapshot := harness.Collector.Discovery()
	if len(snapshot.Identifiers) == 0 || len(snapshot.Devices) == 0 {
		t.Fatal("discovery found no identifier or no device, so this assertion is vacuous")
	}
	// The premise: the two sets are disjoint in the fixture, so sending one where the other
	// belongs is detectable.
	for _, identifier := range snapshot.Identifiers {
		if _, isAlsoADevice := snapshot.Devices[string(identifier)]; isAlsoADevice {
			t.Fatalf("the fixture uses %q as both an identifier and a device, so this test "+
				"cannot tell the two apart", identifier)
		}
	}

	requests := harness.Fake.RequestsTo(collecttest.Declared("insight", "traffic_top"))
	if len(requests) == 0 {
		t.Fatal("the per-pair endpoint was never requested")
	}
	sent := 0
	for _, request := range requests {
		if len(request.Arguments) == 0 {
			t.Error("a request carried no positional argument, which reaches a different command")
		}
		for _, argument := range request.Arguments {
			for _, name := range strings.Split(argument, ",") {
				sent++
				if _, isDevice := snapshot.Devices[name]; isDevice {
					t.Errorf("the sampler sent the device name %q, which returns an empty array "+
						"with HTTP 200 and looks exactly like an absence of traffic", name)
				}
				if _, isIdentifier := snapshot.InterfaceIDByIdentifier[name]; !isIdentifier {
					t.Errorf("the sampler sent %q, which is not an interface identifier the "+
						"firewall reported", name)
				}
			}
		}
	}
	if sent == 0 {
		t.Fatal("no interface name was sent at all")
	}
}

// TestASamplerRefusalIsRecordedRatherThanLookingLikeSilence is the other half: a refusal
// has to reach the availability row, or a refused sample would be as invisible as the
// empty answer it prevents.
func TestASamplerRefusalIsRecordedRatherThanLookingLikeSilence(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// A snapshot with the defect, installed behind the collector's back.
	broken := harness.Collector.Discovery()
	broken.Identifiers = nil
	for device := range broken.Devices {
		broken.Identifiers = append(broken.Identifiers, opnsense.InterfaceName(device))
	}
	harness.Collector.SetDiscovery(broken)

	if err := harness.Collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	state, _, _ := harness.AvailabilityOf(t, "measurement_sample", "insight")
	if state != vocab.StateUnavailable {
		t.Errorf("a refused sample left the volume source reading %q", state)
	}
	detail := harness.DetailOf(t, "measurement_sample", "insight")
	if !strings.Contains(detail, "device name") {
		t.Errorf("the detail is %q, which does not say why the sample was refused", detail)
	}
	if stored := collecttest.CountRows(t, harness.Store, "measurement_sample"); stored == 0 {
		t.Error("the refusal also stopped the firewall-health readings, which are unrelated")
	}
	if pairs := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair'"); pairs != 0 {
		t.Errorf("%d pair readings were stored from a refused sample", pairs)
	}
}

// TestAReadingOfEachKindRoundTripsThroughTheOneTable is the table doing both jobs it was
// created for: the per-pair volume nothing upstream keeps, and the firewall's own gauges.
func TestAReadingOfEachKindRoundTripsThroughTheOneTable(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	for _, expected := range []struct {
		subjectKind string
		measure     vocab.Measure
		unit        vocab.Unit
		why         string
	}{
		{"interface_endpoint_pair", vocab.MeasureCumulativeBytesIn, vocab.UnitByte,
			"the per-pair volume, which is a live snapshot upstream and history only here"},
		{"interface_endpoint_pair", vocab.MeasureRateBitsOut, vocab.UnitBitPerSecond,
			"the rate the same snapshot reports beside the counter"},
		{"firewall", vocab.MeasureMemoryUseRatio, vocab.UnitRatio, "memory"},
		{"firewall", vocab.MeasureCPUUseRatio, vocab.UnitRatio, "the processor"},
		{"firewall", vocab.MeasureTemperatureCelsius, vocab.UnitCelsius, "a temperature sensor"},
		{"firewall", vocab.MeasureUptimeSeconds, vocab.UnitSecond, "uptime"},
		{"firewall", vocab.MeasureLoadAverage, vocab.UnitDimensionless, "the load average"},
		{"firewall", vocab.MeasureDiskUseRatio, vocab.UnitRatio, "a filesystem"},
		{"interface", vocab.MeasureBytesIn, vocab.UnitByte, "a per-interface byte counter"},
		{"interface", vocab.MeasurePacketsOut, vocab.UnitPacket, "a per-interface packet counter"},
		{"interface", vocab.MeasureErrorsIn, vocab.UnitPacket, "a per-interface error counter"},
		{"gateway", vocab.MeasureDelayMilliseconds, vocab.UnitMillisecond, "a gateway round-trip time"},
		{"gateway", vocab.MeasureLossRatio, vocab.UnitRatio, "a gateway packet loss"},
	} {
		stored := collecttest.ScalarCount(t, harness.Store,
			"SELECT count(*) FROM measurement_sample WHERE subject_kind = $1 AND measure = $2 AND unit = $3",
			expected.subjectKind, string(expected.measure), string(expected.unit))
		if stored == 0 {
			t.Errorf("no %s reading of %s was stored (%s)",
				expected.subjectKind, expected.measure, expected.why)
		}
	}

	// A pair subject names the device it was read on, then the local address, then the peer:
	// the two facts the canonical pair used to drop.
	devices := map[string]bool{}
	for _, device := range stringColumn(t, harness.Store, "SELECT device FROM interface") {
		devices[device] = true
	}
	for _, key := range stringColumn(t, harness.Store,
		"SELECT DISTINCT subject_key FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair'") {
		parts := strings.Split(key, " ")
		if len(parts) != 3 {
			t.Errorf("the pair subject %q is not a device, a local address and a peer", key)
			continue
		}
		if !devices[parts[0]] {
			t.Errorf("the pair subject %q does not begin with a discovered device", key)
		}
	}

	// The firewall's own telemetry belongs to no provider kind, so those rows name none.
	// Attributing them to the volume provider would say the volume source measured the
	// temperature.
	if attributed := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'firewall' AND provider_id IS NOT NULL"); attributed != 0 {
		t.Errorf("%d firewall readings name a provider", attributed)
	}
	// The pair readings do name one: that volume is the measurement_sample kind's material.
	if unattributed := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair' AND provider_id IS NULL"); unattributed != 0 {
		t.Errorf("%d pair readings name no provider", unattributed)
	}
}

// TestASamplingPassRepeatedStoresNothingNew is idempotence inside one interval: a sampler
// that restarted between two ticks must not double every reading.
func TestASamplingPassRepeatedStoresNothingNew(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	if err := harness.Collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	afterOne := collecttest.CountRows(t, harness.Store, "measurement_sample")
	if afterOne == 0 {
		t.Fatal("the first pass stored nothing")
	}
	for pass := 0; pass < 2; pass++ {
		if err := harness.Collector.CollectMeasurement(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := collecttest.CountRows(t, harness.Store, "measurement_sample"); repeated != afterOne {
		t.Fatalf("three passes at one instant stored %d readings, up from %d", repeated, afterOne)
	}
}

// TestATelemetryReadingThatDoesNotAnswerIsRecordedAndNeverWrittenAsAZero is the difference
// between "the sensor says nothing is happening" and "there is no sensor".
//
// The fake is put in the state of a firewall with no temperature sensor. No temperature row
// may be written, the availability row must move, and its detail must name the reading that
// did not answer.
func TestATelemetryReadingThatDoesNotAnswerIsRecordedAndNeverWrittenAsAZero(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// An empty array: the endpoint answered and reported no sensor, which is a fact about
	// the hardware. Written here rather than as a fixture because the empty value is the
	// whole of what it says.
	harness.Fake.AnswerJSON(collecttest.Declared("insight", "system_temperature"), []any{})

	before := harness.AvailabilityInstant(t, "measurement_sample", "insight")
	// The clock is advanced so "the row moved" is an observable fact rather than an
	// identical rewrite: a state recorded at the same instant is indistinguishable from one
	// that was never re-examined.
	harness.Clock.Advance(time.Minute)
	if err := harness.Collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	if temperatures := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM measurement_sample WHERE measure = $1",
		string(vocab.MeasureTemperatureCelsius)); temperatures != 0 {
		t.Errorf("%d temperature readings were written for a firewall that reports no sensor",
			temperatures)
	}
	after := harness.AvailabilityInstant(t, "measurement_sample", "insight")
	if after == before {
		t.Error("the availability row did not move, so the absent sensor was recorded nowhere")
	}
	detail := harness.DetailOf(t, "measurement_sample", "insight")
	if !strings.Contains(detail, "temperature") ||
		!strings.Contains(detail, "not written as zeroes") {
		t.Errorf("the detail is %q, which does not record that the temperature reading did not "+
			"answer and was not zeroed", detail)
	}

	// And the readings that did answer are still there: one absent sensor does not cost the
	// rest of the pass.
	for _, measure := range []vocab.Measure{vocab.MeasureUptimeSeconds, vocab.MeasureMemoryUseRatio} {
		if collecttest.ScalarCount(t, harness.Store,
			"SELECT count(*) FROM measurement_sample WHERE measure = $1", string(measure)) == 0 {
			t.Errorf("the %s reading was lost along with the temperature", measure)
		}
	}
}

// TestTelemetryWhoseFieldNamesDoNotMatchIsRecordedAsAbsent is the honest answer to the one
// thing this cycle genuinely does not know.
//
// The survey establishes that these endpoints answer and does NOT establish their field
// names. If a real firewall spells them differently from the candidates internal/collect
// tries, the reading has to come out as ABSENT rather than as a zero — because a zero would
// be a measurement nobody took.
func TestTelemetryWhoseFieldNamesDoNotMatchIsRecordedAsAbsent(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// Every telemetry endpoint answers with a well-formed body whose keys are none of the
	// ones the collector tries.
	for _, endpoint := range []opnsense.Endpoint{
		collecttest.Declared("insight", "system_resources"), opnsense.SystemTime,
	} {
		harness.Fake.AnswerJSON(endpoint, map[string]any{
			"a_key_no_candidate_list_names": 1, "another": "two",
		})
	}
	// The processor's stream answers too, with events whose fields are none of the two
	// the reading is derived from.
	harness.Fake.AnswerStream(opnsense.CPUUsageStream, [][]byte{
		[]byte(`{"a_key_no_candidate_list_names":1}`), []byte(`{"another":"two"}`),
	}, true)
	harness.Fake.AnswerJSON(collecttest.Declared("insight", "system_disk"),
		map[string]any{"devices": []any{map[string]any{"a_key_no_candidate_list_names": 1}}})
	harness.Fake.AnswerJSON(collecttest.Declared("insight", "traffic_interface"), map[string]any{
		"example_if_a": map[string]any{"a_key_no_candidate_list_names": 1},
	})

	if err := harness.Collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	for _, measure := range []vocab.Measure{
		vocab.MeasureMemoryUseRatio, vocab.MeasureCPUUseRatio, vocab.MeasureUptimeSeconds,
		vocab.MeasureLoadAverage, vocab.MeasureDiskUseRatio, vocab.MeasureBytesIn,
	} {
		if stored := collecttest.ScalarCount(t, harness.Store,
			"SELECT count(*) FROM measurement_sample WHERE measure = $1", string(measure)); stored != 0 {
			t.Errorf("%d %s readings were written from a body that carries no figure this code "+
				"can read", stored, measure)
		}
	}
	detail := harness.DetailOf(t, "measurement_sample", "insight")
	for _, phrase := range []string{"memory", "processor", "uptime", "disk", "interface counters"} {
		if !strings.Contains(detail, phrase) {
			t.Errorf("the detail is %q, which does not name the %s reading as absent", detail, phrase)
		}
	}
}

// TestAnEmptyTrafficSnapshotIsNotReportedAsAnAbsenceOfTraffic is the case a wrong argument
// produces, and the reason the detail has to say so: the collector cannot tell the two
// apart, so it must not claim either.
func TestAnEmptyTrafficSnapshotIsNotReportedAsAnAbsenceOfTraffic(t *testing.T) {
	t.Parallel()
	harness := arrangeMeasurementCollection(t)
	harness.Fake.AnswerJSON(collecttest.Declared("insight", "traffic_top"), map[string]any{})

	if err := harness.Collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	detail := harness.DetailOf(t, "measurement_sample", "insight")
	if !strings.Contains(detail, "not an absence of traffic") ||
		!strings.Contains(detail, "wrong interface argument") {
		t.Errorf("the detail is %q, which does not say that an empty snapshot is not an "+
			"absence of traffic and that a wrong argument answers the same way", detail)
	}
	if pairs := collecttest.ScalarCount(t, harness.Store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair'"); pairs != 0 {
		t.Errorf("%d pair readings were invented from an empty snapshot", pairs)
	}
}
