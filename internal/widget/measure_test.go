package widget

import (
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The measure's own tests that need no database: the twins of the core's statements, and the coverage
// judgment of a declared measure.

// TestEveryCoreMeasuredTwinBindsItsStatementsParameters is twin validation for the core's own
// statements -- the store's and the core widgets' -- which no loader reads: every <name>_measured binds
// exactly the parameters of <name> and :measure, and is a period template exactly when it is. The
// built-in widget plugins' twins are checked by the loader, which would refuse them.
func TestEveryCoreMeasuredTwinBindsItsStatementsParameters(t *testing.T) {
	t.Parallel()
	names, err := sqldb.StatementNames()
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, name := range names {
		text, err := sqldb.Statement(name)
		if err != nil {
			t.Fatal(err)
		}
		texts[name] = text
	}
	twins := 0
	for _, name := range names {
		if _, isTwin := strings.CutSuffix(name, request.MeasuredStatementSuffix); isTwin {
			twins++
		}
		if mismatch := request.MeasuredTwinMismatch(texts, name); mismatch != "" {
			t.Error(mismatch)
		}
	}
	if twins == 0 {
		t.Fatal("no measured twin is registered: the test reads nothing")
	}
}

// TestMeasuredTwinMismatchNamesWhatDiffers is the rule itself: other parameters, a missing :measure, or a
// template beside a statement that is not one, is a mismatch; the same parameters in another order, or
// bound twice, is not; and a _measured statement with no statement beside it is no twin.
func TestMeasuredTwinMismatchNamesWhatDiffers(t *testing.T) {
	t.Parallel()
	texts := map[string]string{
		"widget_a":                "SELECT 1 FROM flow WHERE observed_at >= :from AND observed_at < :to",
		"widget_a_measured":       "SELECT :measure FROM flow WHERE observed_at < :to AND observed_at >= :from AND :to > 0",
		"widget_b":                "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_b_measured":       "SELECT :measure FROM flow WHERE observed_at >= :from AND id = :id",
		"widget_c":                "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_c_measured":       "SELECT :measure FROM volume_aggregate_@period@ WHERE period_start_at >= :from",
		"widget_d":                "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_d_measured":       "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_orphan_measured":  "SELECT 1 FROM flow WHERE id = :id",
		"widget_not_a_twin_other": "SELECT 1",
	}
	for name, want := range map[string]string{"widget_a_measured": "", "widget_b_measured": ":id",
		"widget_c_measured": "period template", "widget_d_measured": ":measure", "widget_orphan_measured": "",
		"widget_not_a_twin_other": ""} {
		got := request.MeasuredTwinMismatch(texts, name)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: the mismatch is %q, want one naming %q", name, got, want)
		}
	}
}

// TestCoveredIsTheDeclaredMeasuresCoverage is the judgment best_available makes (PS22, as PA5 generalises
// it): a connection is covered when the measure knew of it and every direction is in a state the measure
// declares covered -- for netflow measured, in_progress, awaiting_export or missing -- and any other state,
// on one direction, uncovers it.
func TestCoveredIsTheDeclaredMeasuresCoverage(t *testing.T) {
	t.Parallel()
	measure, declared := vocab.MeasureNamed("netflow")
	if !declared {
		t.Fatal("the built-in netflow measure is not registered")
	}
	index := func(state string) int {
		position := measure.StateIndex(state)
		if position < 0 {
			t.Fatalf("netflow declares no state %s", state)
		}
		return position
	}
	covered := reads.MeasureCoverage{Complete: 1, Partial: 3, Unmeasured: 1}
	covered.States[index("measured")], covered.States[index("in_progress")] = 2, 1
	covered.States[index("awaiting_export")], covered.States[index("missing")] = 1, 2
	if !covered.Covered(measure) {
		t.Errorf("%+v is not covered", covered)
	}
	notKnown := covered
	notKnown.NotKnown = 1
	if notKnown.Covered(measure) {
		t.Errorf("a connection the measure knew nothing of is covered: %+v", notKnown)
	}
	for _, state := range []string{"export_gap", "not_exported_when_refused", "forwarding_export_unsettled",
		"ingress_not_captured", "parent_copies_only", "interface_not_exported", "interface_not_captured",
		"capture_configuration_unknown"} {
		coverage := covered
		coverage.States[index(state)] = 1
		if coverage.Covered(measure) {
			t.Errorf("%s: %+v is covered", state, coverage)
		}
	}
}
