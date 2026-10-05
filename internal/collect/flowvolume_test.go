package collect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
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
func arrangeMeasurementCollection(t *testing.T) *probeHarness {
	t.Helper()
	harness := newProbeHarness(t)
	arrangeDiscoverableFirewall(t, harness.fake, harness.collector)
	harness.fake.answerFixture(opnsense.NetflowIsEnabled, "netflow_is_enabled_local.json")
	harness.fake.answerFixture(opnsense.TrafficTop, "traffic_top.json")
	harness.fake.answerFixture(opnsense.TrafficInterface, "traffic_interface.json")
	harness.fake.answerFixture(opnsense.SystemResources, "system_resources.json")
	harness.fake.answerFixture(opnsense.SystemTemperature, "system_temperature.json")
	harness.fake.answerFixture(opnsense.SystemTime, "system_time.json")
	harness.fake.answerFixture(opnsense.SystemDisk, "system_disk.json")
	harness.fake.answerFixture(opnsense.Activity, "activity.json")
	harness.fake.answerFixture(opnsense.GatewayStatus, "gateway_status.json")
	harness.fake.answerFixture(opnsense.SystemSwap, "system_swap.json")
	if err := harness.collector.probeMeasurement(context.Background()); err != nil {
		t.Fatalf("probing the volume source: %v", err)
	}
	if key := harness.activeKeyOf(t, KindMeasurementSample); key != ProviderInsight {
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
	harness := arrangeMeasurementCollection(t)
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	harness.fake.assertEveryPathIsRegistered()

	snapshot := harness.collector.Discovery()
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

	requests := harness.fake.requestsTo(opnsense.TrafficTop)
	if len(requests) == 0 {
		t.Fatal("the per-pair endpoint was never requested")
	}
	sent := 0
	for _, request := range requests {
		if len(request.arguments) == 0 {
			t.Error("a request carried no positional argument, which reaches a different command")
		}
		for _, argument := range request.arguments {
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

// TestTheSamplerRefusesRatherThanSamplingWhenAnArgumentIsADeviceName is the runtime guard
// behind the wire check.
//
// It is driven by a discovery snapshot in which the identifiers were, wrongly, filled with
// device names — which is what a decoder reading the wrong key, or a refactor, would
// produce. The call is refused, because the alternative is an empty answer that reads as a
// quiet network.
func TestTheSamplerRefusesRatherThanSamplingWhenAnArgumentIsADeviceName(t *testing.T) {
	snapshot := newDiscovery()
	snapshot.RefreshedAt = referenceEpoch()
	snapshot.InterfaceIDByIdentifier["example_if_a"] = 1
	snapshot.InterfaceIDByDevice["exdev0"] = 1
	snapshot.Devices["exdev0"] = struct{}{}
	// The defect: the device name where the identifier belongs.
	snapshot.Identifiers = []opnsense.InterfaceName{"exdev0"}

	if _, err := guardInterfaceNames(snapshot); !errors.Is(err, ErrDeviceNameArgument) {
		t.Fatalf("the guard returned %v, not the refusal", err)
	}

	// And a correct snapshot passes, so the guard is not simply refusing everything.
	snapshot.Identifiers = []opnsense.InterfaceName{"example_if_a"}
	names, err := guardInterfaceNames(snapshot)
	if err != nil {
		t.Fatalf("the guard refused a correct snapshot: %v", err)
	}
	if len(names) != 1 || names[0] != "example_if_a" {
		t.Fatalf("the guard returned %v", names)
	}
}

// TestASamplerRefusalIsRecordedRatherThanLookingLikeSilence is the other half: a refusal
// has to reach the availability row, or a refused sample would be as invisible as the
// empty answer it prevents.
func TestASamplerRefusalIsRecordedRatherThanLookingLikeSilence(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// A snapshot with the defect, installed behind the collector's back.
	broken := harness.collector.Discovery()
	broken.Identifiers = nil
	for device := range broken.Devices {
		broken.Identifiers = append(broken.Identifiers, opnsense.InterfaceName(device))
	}
	harness.collector.setDiscovery(broken)

	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	state, _, _ := harness.availabilityOf(t, KindMeasurementSample, ProviderInsight)
	if state != store.StateUnavailable {
		t.Errorf("a refused sample left the volume source reading %q", state)
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
	if !strings.Contains(detail, "device name") {
		t.Errorf("the detail is %q, which does not say why the sample was refused", detail)
	}
	if stored := countRows(t, harness.store, "measurement_sample"); stored == 0 {
		t.Error("the refusal also stopped the firewall-health readings, which are unrelated")
	}
	if pairs := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair'"); pairs != 0 {
		t.Errorf("%d pair readings were stored from a refused sample", pairs)
	}
}

// TestAReadingOfEachKindRoundTripsThroughTheOneTable is the table doing both jobs it was
// created for: the per-pair volume nothing upstream keeps, and the firewall's own gauges.
func TestAReadingOfEachKindRoundTripsThroughTheOneTable(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	for _, expected := range []struct {
		subjectKind string
		measure     store.Measure
		unit        store.Unit
		why         string
	}{
		{"interface_endpoint_pair", store.MeasureCumulativeBytesIn, store.UnitByte,
			"the per-pair volume, which is a live snapshot upstream and history only here"},
		{"interface_endpoint_pair", store.MeasureRateBitsOut, store.UnitBitPerSecond,
			"the rate the same snapshot reports beside the counter"},
		{"firewall", store.MeasureMemoryUseRatio, store.UnitRatio, "memory"},
		{"firewall", store.MeasureCPUUseRatio, store.UnitRatio, "the processor"},
		{"firewall", store.MeasureTemperatureCelsius, store.UnitCelsius, "a temperature sensor"},
		{"firewall", store.MeasureUptimeSeconds, store.UnitSecond, "uptime"},
		{"firewall", store.MeasureLoadAverage, store.UnitDimensionless, "the load average"},
		{"firewall", store.MeasureDiskUseRatio, store.UnitRatio, "a filesystem"},
		{"interface", store.MeasureBytesIn, store.UnitByte, "a per-interface byte counter"},
		{"interface", store.MeasurePacketsOut, store.UnitPacket, "a per-interface packet counter"},
		{"interface", store.MeasureErrorsIn, store.UnitPacket, "a per-interface error counter"},
		{"gateway", store.MeasureDelayMilliseconds, store.UnitMillisecond, "a gateway round-trip time"},
		{"gateway", store.MeasureLossRatio, store.UnitRatio, "a gateway packet loss"},
	} {
		stored := scalarCount(t, harness.store,
			"SELECT count(*) FROM measurement_sample WHERE subject_kind = ? AND measure = ? AND unit = ?",
			expected.subjectKind, string(expected.measure), string(expected.unit))
		if stored == 0 {
			t.Errorf("no %s reading of %s was stored (%s)",
				expected.subjectKind, expected.measure, expected.why)
		}
	}

	// A pair subject names the device it was read on, then the local address, then the peer:
	// the two facts the canonical pair used to drop.
	devices := map[string]bool{}
	for _, device := range stringColumn(t, harness.store, "SELECT device FROM interface") {
		devices[device] = true
	}
	for _, key := range stringColumn(t, harness.store,
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
	if attributed := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'firewall' AND provider_id IS NOT NULL"); attributed != 0 {
		t.Errorf("%d firewall readings name a provider", attributed)
	}
	// The pair readings do name one: that volume is the measurement_sample kind's material.
	if unattributed := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair' AND provider_id IS NULL"); unattributed != 0 {
		t.Errorf("%d pair readings name no provider", unattributed)
	}
}

// TestASamplingPassRepeatedStoresNothingNew is idempotence inside one interval: a sampler
// that restarted between two ticks must not double every reading.
func TestASamplingPassRepeatedStoresNothingNew(t *testing.T) {
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	afterOne := countRows(t, harness.store, "measurement_sample")
	if afterOne == 0 {
		t.Fatal("the first pass stored nothing")
	}
	for pass := 0; pass < 2; pass++ {
		if err := harness.collector.CollectMeasurement(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if repeated := countRows(t, harness.store, "measurement_sample"); repeated != afterOne {
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
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// An empty array: the endpoint answered and reported no sensor, which is a fact about
	// the hardware. Written here rather than as a fixture because the empty value is the
	// whole of what it says.
	harness.fake.answerJSON(opnsense.SystemTemperature, []any{})

	before := harness.availabilityInstant(t, KindMeasurementSample, ProviderInsight)
	// The clock is advanced so "the row moved" is an observable fact rather than an
	// identical rewrite: a state recorded at the same instant is indistinguishable from one
	// that was never re-examined.
	harness.clock.advance(time.Minute)
	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	if temperatures := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE measure = ?",
		string(store.MeasureTemperatureCelsius)); temperatures != 0 {
		t.Errorf("%d temperature readings were written for a firewall that reports no sensor",
			temperatures)
	}
	after := harness.availabilityInstant(t, KindMeasurementSample, ProviderInsight)
	if after == before {
		t.Error("the availability row did not move, so the absent sensor was recorded nowhere")
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
	if !strings.Contains(detail, "temperature") ||
		!strings.Contains(detail, "not written as zeroes") {
		t.Errorf("the detail is %q, which does not record that the temperature reading did not "+
			"answer and was not zeroed", detail)
	}

	// And the readings that did answer are still there: one absent sensor does not cost the
	// rest of the pass.
	for _, measure := range []store.Measure{store.MeasureUptimeSeconds, store.MeasureMemoryUseRatio} {
		if scalarCount(t, harness.store,
			"SELECT count(*) FROM measurement_sample WHERE measure = ?", string(measure)) == 0 {
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
	harness := arrangeMeasurementCollection(t)
	ctx := context.Background()

	// Every telemetry endpoint answers with a well-formed body whose keys are none of the
	// ones the collector tries.
	for _, endpoint := range []opnsense.Endpoint{
		opnsense.SystemResources, opnsense.SystemTime, opnsense.Activity,
	} {
		harness.fake.answerJSON(endpoint, map[string]any{
			"a_key_no_candidate_list_names": 1, "another": "two",
		})
	}
	harness.fake.answerJSON(opnsense.SystemDisk,
		map[string]any{"devices": []any{map[string]any{"a_key_no_candidate_list_names": 1}}})
	harness.fake.answerJSON(opnsense.TrafficInterface, map[string]any{
		"example_if_a": map[string]any{"a_key_no_candidate_list_names": 1},
	})

	if err := harness.collector.CollectMeasurement(ctx); err != nil {
		t.Fatalf("sampling: %v", err)
	}

	for _, measure := range []store.Measure{
		store.MeasureMemoryUseRatio, store.MeasureCPUUseRatio, store.MeasureUptimeSeconds,
		store.MeasureLoadAverage, store.MeasureDiskUseRatio, store.MeasureBytesIn,
	} {
		if stored := scalarCount(t, harness.store,
			"SELECT count(*) FROM measurement_sample WHERE measure = ?", string(measure)); stored != 0 {
			t.Errorf("%d %s readings were written from a body that carries no figure this code "+
				"can read", stored, measure)
		}
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
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
	harness := arrangeMeasurementCollection(t)
	harness.fake.answerJSON(opnsense.TrafficTop, map[string]any{})

	if err := harness.collector.CollectMeasurement(context.Background()); err != nil {
		t.Fatalf("sampling: %v", err)
	}
	detail := harness.detailOf(t, KindMeasurementSample, ProviderInsight)
	if !strings.Contains(detail, "not an absence of traffic") ||
		!strings.Contains(detail, "wrong interface argument") {
		t.Errorf("the detail is %q, which does not say that an empty snapshot is not an "+
			"absence of traffic and that a wrong argument answers the same way", detail)
	}
	if pairs := scalarCount(t, harness.store,
		"SELECT count(*) FROM measurement_sample WHERE subject_kind = 'interface_endpoint_pair'"); pairs != 0 {
		t.Errorf("%d pair readings were invented from an empty snapshot", pairs)
	}
}

// availabilityInstant reads when one provider's state was last determined.
func (h *probeHarness) availabilityInstant(t *testing.T, kind, providerKey string) int64 {
	t.Helper()
	_, _, checkedAt := h.availabilityOf(t, kind, providerKey)
	return checkedAt
}
