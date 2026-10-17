package widget

import (
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The measure's own tests that need no database: the twins of the core's statements, and the netflow
// figures as a script and a Volume carry them.

// TestEveryCoreNetflowTwinBindsItsStatementsParameters is twin validation for the core's own
// statements -- the store's and the core widgets' -- which no loader reads: every <name>_netflow binds
// exactly the parameters of <name> and is a period template exactly when it is. The built-in widget
// plugins' twins are checked by the loader, which would refuse them.
func TestEveryCoreNetflowTwinBindsItsStatementsParameters(t *testing.T) {
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
		if _, isTwin := strings.CutSuffix(name, request.NetflowStatementSuffix); isTwin {
			twins++
		}
		if mismatch := request.NetflowTwinMismatch(texts, name); mismatch != "" {
			t.Error(mismatch)
		}
	}
	if twins == 0 {
		t.Fatal("no netflow twin is registered: the test reads nothing")
	}
}

// TestNetflowTwinMismatchNamesWhatDiffers is the rule itself: other parameters, or a template beside a
// statement that is not one, is a mismatch; the same parameters in another order, or bound twice, is
// not; and a _netflow statement with no statement beside it is no twin.
func TestNetflowTwinMismatchNamesWhatDiffers(t *testing.T) {
	t.Parallel()
	texts := map[string]string{
		"widget_a":                "SELECT 1 FROM flow WHERE observed_at >= :from AND observed_at < :to",
		"widget_a_netflow":        "SELECT 1 FROM flow WHERE observed_at < :to AND observed_at >= :from AND :to > 0",
		"widget_b":                "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_b_netflow":        "SELECT 1 FROM flow WHERE observed_at >= :from AND id = :id",
		"widget_c":                "SELECT 1 FROM flow WHERE observed_at >= :from",
		"widget_c_netflow":        "SELECT 1 FROM volume_aggregate_@period@ WHERE period_start_at >= :from",
		"widget_orphan_netflow":   "SELECT 1 FROM flow WHERE id = :id",
		"widget_not_a_twin_other": "SELECT 1",
	}
	for name, want := range map[string]string{"widget_a_netflow": "", "widget_b_netflow": ":id",
		"widget_c_netflow": "period template", "widget_orphan_netflow": "", "widget_not_a_twin_other": ""} {
		got := request.NetflowTwinMismatch(texts, name)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: the mismatch is %q, want one naming %q", name, got, want)
		}
	}
}

// TestCoveredIsPS22sCoverage is the judgment best_available makes, as NJ5 restates it: a connection is
// covered when its capture was known and every direction is measured, in_progress, awaiting_export or
// missing; any other of NJ5's states, on one direction, uncovers it.
func TestCoveredIsPS22sCoverage(t *testing.T) {
	t.Parallel()
	covered := reads.NetflowCoverage{Measured: 1, Partial: 3, Unmeasured: 1, Directions: reads.DirectionStates{
		Measured: 2, InProgress: 1, AwaitingExport: 1, Missing: 2}}
	if !covered.Covered() {
		t.Errorf("%+v is not covered", covered)
	}
	for name, uncover := range map[string]func(*reads.NetflowCoverage){
		"capture_not_known":             func(c *reads.NetflowCoverage) { c.CaptureNotKnown = 1 },
		"export_gap":                    func(c *reads.NetflowCoverage) { c.Directions.ExportGap = 1 },
		"not_exported_when_refused":     func(c *reads.NetflowCoverage) { c.Directions.NotExportedWhenRefused = 1 },
		"forwarding_export_unsettled":   func(c *reads.NetflowCoverage) { c.Directions.ForwardingExportUnsettled = 1 },
		"ingress_not_captured":          func(c *reads.NetflowCoverage) { c.Directions.IngressNotCaptured = 1 },
		"parent_copies_only":            func(c *reads.NetflowCoverage) { c.Directions.ParentCopiesOnly = 1 },
		"interface_not_exported":        func(c *reads.NetflowCoverage) { c.Directions.InterfaceNotExported = 1 },
		"interface_not_captured":        func(c *reads.NetflowCoverage) { c.Directions.InterfaceNotCaptured = 1 },
		"capture_configuration_unknown": func(c *reads.NetflowCoverage) { c.Directions.CaptureConfigurationUnknown = 1 },
	} {
		coverage := covered
		uncover(&coverage)
		if coverage.Covered() {
			t.Errorf("%s: %+v is covered", name, coverage)
		}
	}
}
