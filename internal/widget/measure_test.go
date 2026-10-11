package widget

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
)

// The measure's own tests that need no database: the twins of the core's statements, and the netflow
// figures as a script and a Volume carry them.

// TestEveryCoreNetflowTwinBindsItsStatementsParameters is twin validation for the core's own
// statements -- the store's and the core widgets' -- which no loader reads: every <name>_netflow binds
// exactly the parameters of <name> and is a period template exactly when it is. The built-in widget
// plugins' twins are checked by the loader, which would refuse them.
func TestEveryCoreNetflowTwinBindsItsStatementsParameters(t *testing.T) {
	t.Parallel()
	names, err := store.StatementNames()
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, name := range names {
		text, err := store.Statement(name)
		if err != nil {
			t.Fatal(err)
		}
		texts[name] = text
	}
	twins := 0
	for _, name := range names {
		if _, isTwin := strings.CutSuffix(name, netflowStatementSuffix); isTwin {
			twins++
		}
		if mismatch := netflowTwinMismatch(texts, name); mismatch != "" {
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
		got := netflowTwinMismatch(texts, name)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: the mismatch is %q, want one naming %q", name, got, want)
		}
	}
}

// exampleNetflowFigures are netflow figures whose every field differs from every other.
func exampleNetflowFigures() store.VolumeFigures {
	figures := store.VolumeFigures{Bytes: 600, AllowedBytes: 300, BlockedBytes: 200, UnknownBytes: 100,
		AllowedConnections: 3, BlockedConnections: 2, UnknownConnections: 1, Netflow: true}
	for index, field := range figures.Parts.Fields() {
		*field = int64(index + 1)
	}
	return figures
}

// TestScriptFiguresCarryEveryNetflowPartMember is the members a script's netflow figures carry: the
// columns of netflow_parts by name, so a statement's (netflow_sum(...)).* row is read as figures, and
// figures handed to a script and back are the same figures.
func TestScriptFiguresCarryEveryNetflowPartMember(t *testing.T) {
	t.Parallel()
	var names []string
	members := reflect.TypeOf(netflowMembers{})
	for index := 0; index < members.NumField(); index++ {
		tag := strings.Split(members.Field(index).Tag.Get("json"), ",")[0]
		if members.Field(index).Type == reflect.TypeOf((*int64)(nil)) {
			names = append(names, tag)
		}
	}
	if strings.Join(names, ",") != strings.Join(store.NetflowPartsColumns(), ",") {
		t.Errorf("the script's netflow members are\n%v\nnot the columns of netflow_parts\n%v", names,
			store.NetflowPartsColumns())
	}
	figures := exampleNetflowFigures()
	encoded, err := json.Marshal(summable(figures))
	if err != nil {
		t.Fatal(err)
	}
	var read summableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.toStore() != figures {
		t.Errorf("figures handed to a script and back are\n%+v\nnot\n%+v", read.toStore(), figures)
	}
	logged := figures
	logged.Netflow, logged.Parts = false, store.NetflowParts{}
	if encoded, _ := json.Marshal(summable(logged)); strings.Contains(string(encoded), "packets") {
		t.Errorf("logged figures carry netflow members: %s", encoded)
	}
}

// TestANetflowVolumeHandedBackKeepsItsParts is "coverage per action" through a script: a netflow
// Volume carries the packets and coverage of each part, so a script summing Volumes it was handed
// (owner_activity, client_volume_ranking) keeps every part's own figures.
func TestANetflowVolumeHandedBackKeepsItsParts(t *testing.T) {
	t.Parallel()
	figures := exampleNetflowFigures()
	volume := netflowVolume(figures)
	if *volume.Packets != figures.Packets() || *volume.BlockedPackets != figures.Parts.Blocked.Packets ||
		volume.BlockedCoverage.storeCoverage() != figures.Parts.Blocked.Coverage ||
		volume.Coverage.storeCoverage() != figures.Coverage() {
		t.Errorf("the Volume is %+v", volume)
	}
	encoded, err := json.Marshal(volume)
	if err != nil {
		t.Fatal(err)
	}
	var read summableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.toStore() != figures {
		t.Errorf("a Volume handed back is read as\n%+v\nnot\n%+v", read.toStore(), figures)
	}
	blocked := BlockedOnly(figures)
	if blocked.Parts != (store.NetflowParts{Blocked: figures.Parts.Blocked}) || blocked.Bytes != figures.BlockedBytes ||
		blocked.Connections() != figures.BlockedConnections {
		t.Errorf("the blocked part is %+v", blocked)
	}
	allowed := AllowedOnly(figures)
	if allowed.Parts != (store.NetflowParts{Allowed: figures.Parts.Allowed}) || allowed.Bytes != figures.AllowedBytes {
		t.Errorf("the allowed part is %+v", allowed)
	}
}

// TestCoveredIsPS22sCoverage is the judgment best_available makes: a connection is covered when it is
// joinable and none of its directions is not_captured, export_gap or not_exported_when_refused.
func TestCoveredIsPS22sCoverage(t *testing.T) {
	t.Parallel()
	covered := store.NetflowCoverage{Measured: 1, Partial: 3, Unmeasured: 1, Directions: store.DirectionStates{
		Measured: 2, InProgress: 1, AwaitingExport: 1, Missing: 2}}
	if !covered.Covered() {
		t.Errorf("%+v is not covered", covered)
	}
	for name, uncover := range map[string]func(*store.NetflowCoverage){
		"not_joinable":              func(c *store.NetflowCoverage) { c.NotJoinable = 1 },
		"not_captured":              func(c *store.NetflowCoverage) { c.Directions.NotCaptured = 1 },
		"export_gap":                func(c *store.NetflowCoverage) { c.Directions.ExportGap = 1 },
		"not_exported_when_refused": func(c *store.NetflowCoverage) { c.Directions.NotExportedWhenRefused = 1 },
	} {
		coverage := covered
		uncover(&coverage)
		if coverage.Covered() {
			t.Errorf("%s: %+v is covered", name, coverage)
		}
	}
}
