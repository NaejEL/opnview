package request_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/shapes"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// exampleMeasuredFigures are figures of the netflow measure whose every figure differs from every other.
func exampleMeasuredFigures(t *testing.T) reads.VolumeFigures {
	t.Helper()
	measure, declared := vocab.MeasureNamed("netflow")
	if !declared {
		t.Fatal("the built-in netflow measure is not registered")
	}
	figures := reads.VolumeFigures{Bytes: 600, AllowedBytes: 300, BlockedBytes: 200, UnknownBytes: 100,
		AllowedConnections: 3, BlockedConnections: 2, UnknownConnections: 1}
	width := measure.PartWidth()
	array := make([]int64, 3*width)
	for index := range array {
		array[index] = int64(index + 1)
	}
	// The bytes of each part are the figures' own.
	array[0], array[width], array[2*width] = figures.AllowedBytes, figures.BlockedBytes, figures.UnknownBytes
	if err := figures.SetPartsArray(measure.Name, array); err != nil {
		t.Fatal(err)
	}
	return figures
}

// TestScriptFiguresCarryEveryMeasurePart is the members a script's measured figures carry: the measure's
// name and its parts as one array in the measure's layout -- what a statement's measured twin answers
// as measure_parts -- so figures handed to a script and back are the same figures, and logged figures
// carry neither.
func TestScriptFiguresCarryEveryMeasurePart(t *testing.T) {
	t.Parallel()
	figures := exampleMeasuredFigures(t)
	measure, _ := vocab.MeasureNamed(figures.Measure)
	summable := traffic.Summable(figures)
	if summable.Measure != "netflow" || len(summable.MeasureParts) != 3*measure.PartWidth() {
		t.Fatalf("the script's members are %+v", summable.MeasureMembers)
	}
	encoded, err := json.Marshal(summable)
	if err != nil {
		t.Fatal(err)
	}
	var read traffic.SummableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.ToStore() != figures {
		t.Errorf("figures handed to a script and back are\n%+v\nnot\n%+v", read.ToStore(), figures)
	}
	logged := figures
	logged.Measure, logged.Parts = "", reads.MeasureParts{}
	if encoded, _ := json.Marshal(traffic.Summable(logged)); strings.Contains(string(encoded), "measure") {
		t.Errorf("logged figures carry measure members: %s", encoded)
	}
}

// TestAMeasuredVolumeHandedBackKeepsItsParts is "coverage per action" through a script: a measured
// Volume carries the packets and coverage of each part, so a script summing Volumes it was handed
// (owner_activity, client_volume_ranking) keeps every part's own figures.
func TestAMeasuredVolumeHandedBackKeepsItsParts(t *testing.T) {
	t.Parallel()
	figures := exampleMeasuredFigures(t)
	volume := request.MeasuredVolume(figures)
	if volume.Measure != "netflow" || *volume.Packets != figures.Packets() ||
		*volume.BlockedPackets != figures.Parts.Blocked.Packets ||
		volume.BlockedCoverage.StoreCoverage("netflow") != figures.Parts.Blocked.Coverage ||
		volume.Coverage.StoreCoverage("netflow") != figures.Coverage() {
		t.Errorf("the Volume is %+v", volume)
	}
	encoded, err := json.Marshal(volume)
	if err != nil {
		t.Fatal(err)
	}
	var read traffic.SummableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.ToStore() != figures {
		t.Errorf("a Volume handed back is read as\n%+v\nnot\n%+v", read.ToStore(), figures)
	}
	blocked := shapes.BlockedOnly(figures)
	if blocked.Parts != (reads.MeasureParts{Blocked: figures.Parts.Blocked}) || blocked.Bytes != figures.BlockedBytes ||
		blocked.Connections() != figures.BlockedConnections {
		t.Errorf("the blocked part is %+v", blocked)
	}
	allowed := shapes.AllowedOnly(figures)
	if allowed.Parts != (reads.MeasureParts{Allowed: figures.Parts.Allowed}) || allowed.Bytes != figures.AllowedBytes {
		t.Errorf("the allowed part is %+v", allowed)
	}
}

// TestTheCoverageObjectIsWrittenInTheMeasuresNamesAndOrder is PA5's coverage object: the declared
// classes, then direction_states in the declared order, then the markers -- for the netflow measure the
// object NJ5 and NJ8 defined, byte for byte.
func TestTheCoverageObjectIsWrittenInTheMeasuresNamesAndOrder(t *testing.T) {
	t.Parallel()
	figures := exampleMeasuredFigures(t)
	encoded, err := json.Marshal(request.MeasuredVolume(figures).AllowedCoverage)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"measured":3,"partial":4,"unmeasured":5,"capture_not_known":6,"direction_states":{"measured":7,` +
		`"in_progress":8,"awaiting_export":9,"missing":10,"export_gap":11,"not_exported_when_refused":12,` +
		`"forwarding_export_unsettled":13,"ingress_not_captured":14,"parent_copies_only":15,` +
		`"interface_not_exported":16,"interface_not_captured":17,"capture_configuration_unknown":18,` +
		// The two states N3 and D5 of the live validation of 17 October 2026 added, after the twelve.
		`"ambiguous_records":19,"not_expected_when_refused":20},"counted_copy":21}`
	if string(encoded) != want {
		t.Errorf("the coverage object is\n%s\nnot\n%s", encoded, want)
	}
}

// TestVolumeMeasureOffersLoggedEveryDeclaredMeasureAndBestAvailable is PA-AC7's first bullet:
// volume_measure's values are logged, every declared measure in name order -- a measure a test declares
// as soon as it is registered -- and best_available, its default.
func TestVolumeMeasureOffersLoggedEveryDeclaredMeasureAndBestAvailable(t *testing.T) {
	t.Parallel()
	fixture := vocab.DeclaredMeasure{Name: "example_request_rate", Label: "plugin.example.measure",
		Description: "A test's measure.", ProviderKind: "example_kind", CompleteState: "counted",
		RefusedState: "refused", States: []vocab.DeclaredMeasureState{{Name: "counted", Covered: true},
			{Name: "refused"}},
		Classes: vocab.MeasureClasses{Complete: "complete", Partial: "partial", Unmeasured: "unmeasured",
			NotKnown: "not_known"},
		WindowStates: vocab.MeasureWindowStates{Covered: "covered", Unrecorded: "unrecorded", NoProvider: "off",
			NotCovering: "partial_window", NotServed: "not_served"}}
	if err := vocab.RegisterMeasure(fixture); err != nil {
		t.Fatal(err)
	}
	want := []string{"logged"}
	for _, measure := range vocab.Measures() {
		want = append(want, measure.Name)
	}
	want = append(want, "best_available")
	parameter := request.VolumeMeasureParam()
	if strings.Join(parameter.Values, ",") != strings.Join(want, ",") || parameter.Default != "best_available" {
		t.Errorf("volume_measure offers %v (default %v), not %v", parameter.Values, parameter.Default, want)
	}
	if !strings.Contains(strings.Join(parameter.Values, ","), "netflow") ||
		!strings.Contains(strings.Join(parameter.Values, ","), fixture.Name) {
		t.Errorf("volume_measure offers %v, without the netflow plugin's measure or the test's", parameter.Values)
	}
}
