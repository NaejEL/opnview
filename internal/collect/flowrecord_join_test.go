package collect_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The NetFlow join with every interface captured (NJ4 to NJ8 of the amendment "the NetFlow join with every
// interface captured" to specs/SPEC-backend-completion-plugins.md), on NJ12's fixtures: each test on the
// PostgreSQL test instance, at a fixed clock, with records delivered through the real decoder and router
// and legs through the real filter-log parser.

// The orders NJ-AC6 ingests the fixtures in.
const (
	orderForwardFirst = "forward first"
	orderReverseFirst = "reverse first"
	orderInterleaved  = "interleaved"
)

// pendingPassAt is the instant of the pass every order but the interleaved one runs first: past every
// record's start plus the tolerance, before every leg's export bound (NJ4 rule 4).
var pendingPassAt = collecttest.LegInstant + 100

// runJoinWorld builds a world, reads its capture configuration, and ingests fixtures in an order. A
// check, when given, runs between the pass at pendingPassAt and the last one.
func runJoinWorld(t *testing.T, world collecttest.JoinWorld, fixtures []collecttest.JoinFixture, order string,
	beforeBound func(h *collecttest.NetflowHarness)) *collecttest.NetflowHarness {
	t.Helper()
	h := collecttest.NewNetflowWorldHarness(t, world.Body, world.Offered())
	h.Capture(t, collecttest.CaptureStartAt, world.Captured(fixtures[0].Capture)...)
	h.At(collecttest.NetflowReadSecond)
	side := func(want string) func(collecttest.FixtureRecord) bool {
		return func(record collecttest.FixtureRecord) bool { return record.Side == want }
	}
	legs := collecttest.FixtureLegs(fixtures)
	switch order {
	case orderForwardFirst, orderReverseFirst:
		first, second := "forward", "reverse"
		if order == orderReverseFirst {
			first, second = second, first
		}
		h.DeliverFixtureRecords(t, fixtures, side(first))
		h.DeliverFixtureRecords(t, fixtures, side(second))
		h.LoggedAt(t, pendingPassAt, legs...)
		if beforeBound != nil {
			beforeBound(h)
		}
		h.Logged(t)
	case orderInterleaved:
		// The filter log read through an instant before every record's start plus the tolerance, so its
		// first half is stored and nothing is decided; then the forward records, then the rest.
		h.DeliverFixtureRecords(t, fixtures, side("reverse"))
		h.LoggedAt(t, collecttest.LegInstant+2, legs[:len(legs)/2]...)
		h.DeliverFixtureRecords(t, fixtures, side("forward"))
		h.Logged(t, legs[len(legs)/2:]...)
	}
	return h
}

// digestsOf are the digests of the legs of fixtures.
func digestsOf(fixtures []collecttest.JoinFixture) []string {
	var digests []string
	for _, fixture := range fixtures {
		for _, leg := range fixture.Legs {
			digests = append(digests, leg.Digest)
		}
	}
	return digests
}

// splitByCapture keeps apart the fixtures that run under the world's capture configuration and those
// that bring their own.
func splitByCapture(fixtures []collecttest.JoinFixture) (own, other []collecttest.JoinFixture) {
	for _, fixture := range fixtures {
		if len(fixture.Capture) == 0 {
			own = append(own, fixture)
		} else {
			other = append(other, fixture)
		}
	}
	return own, other
}

// TestTheJoinWithEveryInterfaceCaptured runs every shape of NJ12 and checks NJ-AC5 to NJ-AC12, each in
// the subtest named after it. The fixtures run together in one world, ingested in each of NJ-AC6's
// orders; the shape that brings its own capture configuration (F17) runs in a world of its own.
func TestTheJoinWithEveryInterfaceCaptured(t *testing.T) {
	t.Parallel()
	world := collecttest.LoadJoinWorld(t, collecttest.JoinFixtureDirectory)
	fixtures, upstreamIngress := splitByCapture(collecttest.LoadJoinFixtures(t, collecttest.JoinFixtureDirectory))
	digests := digestsOf(fixtures)
	var mutex sync.Mutex
	fingerprints, aggregates := map[string]string{}, map[string]string{}
	t.Run("worlds", func(t *testing.T) {
		for _, order := range []string{orderForwardFirst, orderReverseFirst, orderInterleaved} {
			t.Run(order, func(t *testing.T) {
				t.Parallel()
				var check func(h *collecttest.NetflowHarness)
				if order == orderForwardFirst {
					check = func(h *collecttest.NetflowHarness) { checkPendingBeforeBound(t, h, fixtures) }
				}
				h := runJoinWorld(t, world, fixtures, order, check)
				fingerprint := collecttest.JoinFingerprint(t, h, digests...)
				// A second derivation writes nothing (PS-AC34's method): no decision, no figure, not even its
				// computation instant.
				computed := collecttest.ScalarCount(t, h.Store,
					"SELECT coalesce(sum(computed_at), 0) + coalesce(sum(1), 0) FROM connection_measured_volume")
				decided := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(decided_at), 0) FROM flow_record")
				h.At(collecttest.NetflowReadSecond + 1)
				if err := h.Collector.RefreshAggregates(context.Background()); err != nil {
					t.Fatal(err)
				}
				if collecttest.ScalarCount(t, h.Store,
					"SELECT coalesce(sum(computed_at), 0) + coalesce(sum(1), 0) FROM connection_measured_volume") != computed ||
					collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(decided_at), 0) FROM flow_record") != decided {
					t.Errorf("a second derivation rewrote a decision or a figure")
				}
				if again := collecttest.JoinFingerprint(t, h, digests...); again != fingerprint {
					t.Errorf("a second derivation changed the decisions or the figures")
				}
				mutex.Lock()
				fingerprints[order] = fingerprint
				mutex.Unlock()
				if order != orderForwardFirst {
					return
				}
				checkEveryShape(t, h, fixtures)
				mutex.Lock()
				for _, period := range []string{"1h", "24h", "7d", "30d"} {
					aggregates[period] = periodFingerprint(t, h, period)
				}
				mutex.Unlock()
				checkAggregates(t, h, fixtures)
				checkThePurgeKeepsTheNetflowFigures(t, h, fixtures)
			})
		}
		t.Run("baseline", func(t *testing.T) {
			t.Parallel()
			// The same legs with no NetFlow record at all: what every logged column must stay.
			h := collecttest.NewNetflowWorldHarness(t, world.Body, world.Offered())
			h.Capture(t, collecttest.CaptureStartAt, world.Captured(nil)...)
			h.Logged(t, collecttest.FixtureLegs(fixtures)...)
			mutex.Lock()
			defer mutex.Unlock()
			for _, period := range []string{"1h", "24h", "7d", "30d"} {
				aggregates["baseline "+period] = periodFingerprint(t, h, period)
			}
		})
		t.Run("upstream captured on ingress", func(t *testing.T) {
			t.Parallel()
			h := runJoinWorld(t, world, upstreamIngress, orderForwardFirst, nil)
			checkEveryShape(t, h, upstreamIngress)
		})
	})
	t.Run("NJ-AC6 every order gives the same rows", func(t *testing.T) {
		for _, order := range []string{orderReverseFirst, orderInterleaved} {
			if fingerprints[order] != fingerprints[orderForwardFirst] {
				t.Errorf("%s gives\n%s\nforward first gives\n%s", order, fingerprints[order], fingerprints[orderForwardFirst])
			}
		}
	})
	t.Run("NJ-AC12 the logged columns equal the baseline at every period", func(t *testing.T) {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			if aggregates[period] != aggregates["baseline "+period] {
				t.Errorf("at %s a logged column changed:\nwith NetFlow    %s\nwithout NetFlow %s", period,
					aggregates[period], aggregates["baseline "+period])
			}
		}
	})
}

// checkPendingBeforeBound is NJ-AC6's F14 clause: before the filter log is read past its leg's export
// bound, a reverse record no forward record confirms stays pending.
func checkPendingBeforeBound(t *testing.T, h *collecttest.NetflowHarness, fixtures []collecttest.JoinFixture) {
	t.Helper()
	rows := rowsByKey(t, h)
	for _, fixture := range fixtures {
		for _, record := range fixture.Records {
			if !record.Expect.PendingBeforeBound {
				continue
			}
			for _, row := range rows[record.Key()] {
				if row.JoinState != netflow.JoinPending {
					t.Errorf("NJ-AC6: %s: a reverse record no forward record confirms is %s before its leg's export "+
						"bound, want pending", fixture.Name, row.JoinState)
				}
			}
		}
	}
}

// rowsByKey indexes the stored records by the fixture key they answer to.
func rowsByKey(t *testing.T, h *collecttest.NetflowHarness) map[string][]collecttest.RecordRow {
	t.Helper()
	rows := map[string][]collecttest.RecordRow{}
	for _, row := range h.Records(t) {
		var src, dst string
		var srcPort, dstPort, in, out *int64
		if err := h.Store.DB().QueryRowContext(context.Background(),
			`SELECT src_address, dst_address, src_port, dst_port, ingress_interface, egress_interface
			 FROM flow_record WHERE identity = ? AND start_at = ?`, row.Identity, row.StartAt).
			Scan(&src, &dst, &srcPort, &dstPort, &in, &out); err != nil {
			t.Fatal(err)
		}
		value := func(number *int64) int64 {
			if number == nil {
				return 0
			}
			return *number
		}
		key := collecttest.RecordKey(src, dst, value(srcPort), value(dstPort), value(in), value(out), row.Octets,
			row.StartAt)
		rows[key] = append(rows[key], row)
	}
	return rows
}

// checkEveryShape is NJ-AC5, NJ-AC7, NJ-AC8, NJ-AC9, NJ-AC10 and NJ-AC11 on the fixtures of one world.
func checkEveryShape(t *testing.T, h *collecttest.NetflowHarness, fixtures []collecttest.JoinFixture) {
	t.Helper()
	rows := rowsByKey(t, h)
	id := func(digest string) int64 { return h.FlowID(t, digest) }
	secondLegs := map[int64]bool{}
	for _, digest := range digestsOf(fixtures) {
		if collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow WHERE log_digest = ? AND pair_outcome = 'second_leg'",
			digest) == 1 {
			secondLegs[id(digest)] = true
		}
	}
	t.Run("NJ-AC5 NJ-AC7 NJ-AC8 each record decided as its fixture says", func(t *testing.T) {
		for _, fixture := range fixtures {
			for index, record := range fixture.Records {
				name := fmt.Sprintf("%s record %d", fixture.Name, index)
				matching := rows[record.Key()]
				if len(matching) == 0 {
					t.Errorf("%s: not stored", name)
					continue
				}
				for _, row := range matching {
					checkRecord(t, name, row, record.Expect, id, secondLegs)
				}
			}
		}
		// NJ-AC8: identical records are distinct rows, each stored.
		stored := len(h.Records(t))
		want := 0
		for _, fixture := range fixtures {
			want += len(fixture.Records)
		}
		if stored != want {
			t.Errorf("%d rows stored for %d records delivered: identical records were deduplicated", stored, want)
		}
	})
	volumes := map[[2]any]collecttest.VolumeRow{}
	for _, volume := range h.Volumes(t) {
		volumes[[2]any{volume.Connection, volume.Direction}] = volume
	}
	t.Run("NJ-AC10 NJ-AC11 each direction in its fixture's state", func(t *testing.T) {
		for _, fixture := range fixtures {
			for _, expected := range fixture.Volumes {
				volume, found := volumes[[2]any{id(expected.Connection), expected.Direction}]
				if !found {
					t.Errorf("%s: %s %s has no figure", fixture.Name, expected.Connection, expected.Direction)
					continue
				}
				if volume.State != expected.State || (volume.CountedCopy == 1) != expected.CountedCopy {
					t.Errorf("%s: %s %s is %s (counted copy %d), want %s (counted copy %t)", fixture.Name,
						expected.Connection, expected.Direction, volume.State, volume.CountedCopy, expected.State,
						expected.CountedCopy)
				}
				if !netflow.CoveredState(volume.State) && volume.State != netflow.VolumeExportGap &&
					(volume.Bytes != 0 || volume.Packets != 0) {
					t.Errorf("%s: %s %s is %s and still carries %d bytes", fixture.Name, expected.Connection,
						expected.Direction, volume.State, volume.Bytes)
				}
			}
		}
	})
	t.Run("NJ-AC9 each direction counted once", func(t *testing.T) {
		independent := map[[2]any][2]int64{}
		for _, fixture := range fixtures {
			for _, record := range fixture.Records {
				if record.Counts == nil {
					continue
				}
				key := [2]any{id(record.Counts.Connection), record.Counts.Direction}
				sums := independent[key]
				independent[key] = [2]int64{sums[0] + int64(record.Octets), sums[1] + int64(record.Packets)}
			}
		}
		for key, volume := range volumes {
			want := independent[key]
			if volume.Bytes != want[0] || volume.Packets != want[1] {
				t.Errorf("the connection %v %v counts %d bytes and %d packets, the records its fixture marks counted "+
					"sum to %d and %d", key[0], key[1], volume.Bytes, volume.Packets, want[0], want[1])
			}
		}
		for key := range independent {
			if _, found := volumes[key]; !found {
				t.Errorf("the connection %v %v has counted records and no figure", key[0], key[1])
			}
		}
		// No record contributes twice: a counted copy is never joined too, and every counted record is in
		// exactly one direction of one connection.
		if twice := collecttest.ScalarCount(t, h.Store,
			"SELECT count(*) FROM flow_record WHERE counted_copy = 1 AND joined_flow_id IS NOT NULL"); twice != 0 {
			t.Errorf("%d records are both joined and counted as copies", twice)
		}
		if counted := collecttest.ScalarCount(t, h.Store, `SELECT coalesce(sum(octets), 0) FROM flow_record
			WHERE join_state = 'joined' AND joined_flow_id IN (SELECT connection_flow_id FROM connection_measured_volume
			                                                   WHERE state IN ('measured', 'in_progress', 'export_gap'))
			   OR counted_copy = 1`); counted != int(sumOf(independent)) {
			t.Errorf("the records counted in figures hold %d bytes, the independent computation %d", counted,
				sumOf(independent))
		}
	})
}

func sumOf(sums map[[2]any][2]int64) int64 {
	var total int64
	for _, sum := range sums {
		total += sum[0]
	}
	return total
}

// checkRecord compares one stored record with its fixture's expectation.
func checkRecord(t *testing.T, name string, row collecttest.RecordRow, expect collecttest.FixtureExpect,
	id func(string) int64, secondLegs map[int64]bool) {
	t.Helper()
	text := func(value *string) string { return collecttest.OrNull(value) }
	if row.JoinState != expect.JoinState {
		t.Errorf("%s: %s (%s), want %s", name, row.JoinState, text(row.Reason), expect.JoinState)
		return
	}
	if row.JoinedFlowID != nil && secondLegs[*row.JoinedFlowID] {
		t.Errorf("%s: joined a second leg", name)
	}
	switch expect.JoinState {
	case netflow.JoinJoined:
		if row.JoinedFlowID == nil || *row.JoinedFlowID != id(expect.Leg) || text(row.Orientation) != expect.Orientation {
			t.Errorf("%s: joined %v %s, want %s %s", name, row.JoinedFlowID, text(row.Orientation), expect.Leg,
				expect.Orientation)
		}
	case netflow.JoinCopy:
		if text(row.CopyKind) != expect.CopyKind {
			t.Errorf("%s: a %s, want a %s", name, text(row.CopyKind), expect.CopyKind)
		}
		switch {
		case expect.CopyLeg == "" && row.CopyFlowID != nil:
			t.Errorf("%s: a copy of the leg %d, want none", name, *row.CopyFlowID)
		case expect.CopyLeg != "" && (row.CopyFlowID == nil || *row.CopyFlowID != id(expect.CopyLeg) ||
			text(row.Orientation) != expect.Orientation):
			t.Errorf("%s: a copy of %v %s, want of %s %s", name, row.CopyFlowID, text(row.Orientation), expect.CopyLeg,
				expect.Orientation)
		}
		if (row.CountedCopy == 1) != expect.CountedCopy {
			t.Errorf("%s: counted_copy %d, want %t", name, row.CountedCopy, expect.CountedCopy)
		}
	case netflow.JoinUnmatched:
		if text(row.Reason) != expect.Reason {
			t.Errorf("%s: unmatched for %s, want %s", name, text(row.Reason), expect.Reason)
		}
	}
}

// checkAggregates is NJ-AC12's netflow clauses and NJ-AC7's "in no family, no total": at every period
// the first legs' netflow columns sum to the independent computation, the exit rows carry the
// connections with a passed second leg, and the unmatched family is keyed by the record's interfaces or
// roles.
func checkAggregates(t *testing.T, h *collecttest.NetflowHarness, fixtures []collecttest.JoinFixture) {
	t.Helper()
	var counted, exit int64
	secondPassed := map[string]bool{}
	for _, fixture := range fixtures {
		for _, leg := range fixture.Legs {
			if leg.Dir == "out" && leg.Action == "pass" && collecttest.ScalarCount(t, h.Store,
				"SELECT count(*) FROM flow WHERE log_digest = ? AND pair_outcome = 'second_leg'", leg.Digest) == 1 {
				for _, other := range fixture.Legs {
					if other.Dir == "in" && other.IPID == leg.IPID && other.IPID != 0 {
						secondPassed[other.Digest] = true
					}
				}
			}
		}
	}
	for _, fixture := range fixtures {
		for _, record := range fixture.Records {
			if record.Counts != nil {
				counted += int64(record.Octets)
				if secondPassed[record.Counts.Connection] {
					exit += int64(record.Octets)
				}
			}
		}
	}
	t.Run("NJ-AC12 NJ-AC7 the netflow columns equal an independent computation", func(t *testing.T) {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			if got := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_"+period+
				" WHERE exit_leg_device IS NULL"); int64(got) != counted {
				t.Errorf("at %s the first legs count %d measured bytes, want %d", period, got, counted)
			}
			if got := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_"+period+
				" WHERE exit_leg_device IS NOT NULL"); int64(got) != exit {
				t.Errorf("at %s the exit rows count %d measured bytes, want %d: both directions of every connection "+
					"whose second leg passed", period, got, exit)
			}
			for _, family := range []string{"client_volume", "peer_volume", "rule_volume"} {
				if got := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(netflow_bytes), 0) FROM "+family+
					"_aggregate_"+period); int64(got) > counted+exit {
					t.Errorf("at %s the %s family counts %d measured bytes, more than every counted record", period,
						family, got)
				}
			}
		}
		if copies := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum((netflow_parts).allowed_counted_copy_connections), 0) "+
			"FROM volume_aggregate_1h WHERE exit_leg_device IS NULL"); copies != 1 {
			t.Errorf("the volume family counts %d connections with a counted copy, want F7's one", copies)
		}
	})
	t.Run("NJ-AC7 the flow_record dataset serves the counted copy", func(t *testing.T) {
		relation, err := os.ReadFile(filepath.Join("..", "dataset", "core", "flow_record", "relation.sql"))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.NewReplacer(":from", "0", ":to", "4102444799").Replace(string(relation))
		if served := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM ("+text+") AS d "+
			"WHERE d.join_state = 'copy' AND d.copy_kind = 'translated_copy' AND d.counted_copy = 1"); served != 1 {
			t.Errorf("the flow_record dataset serves %d counted copies, want F7's one", served)
		}
	})
	t.Run("NJ-AC12 the unmatched family uses the new key", func(t *testing.T) {
		for _, key := range []struct{ ingress, egress, reason string }{
			{"example_if_a", "example_if_c", netflow.UnmatchedAmbiguous},
			{netflow.RoleUnmapped, "example_if_u", netflow.UnmatchedIndexUnmapped},
			{"example_if_u", "example_if_a", netflow.UnmatchedReverseUnconfirmed},
		} {
			for _, period := range []string{"1h", "24h", "7d", "30d"} {
				if rows := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM unmatched_record_aggregate_"+period+
					" WHERE ingress_interface = ? AND egress_interface = ? AND reason = ?", key.ingress, key.egress,
					key.reason); rows != 1 {
					t.Errorf("at %s the unmatched family holds %d slots for %s -> %s %s, want 1", period, rows,
						key.ingress, key.egress, key.reason)
				}
			}
		}
		// A copy is in no unmatched slot: the family holds the unmatched records' bytes and nothing else.
		unmatched := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(octets), 0) FROM flow_record WHERE join_state = 'unmatched'")
		if family := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(bytes), 0) FROM unmatched_record_aggregate_1h"); family != unmatched {
			t.Errorf("the unmatched family holds %d bytes, the unmatched records %d", family, unmatched)
		}
	})
}

// checkThePurgeKeepsTheNetflowFigures is NJ-AC12's purge clause: once every leg is purged, the purged part
// keeps the first legs' measured bytes.
func checkThePurgeKeepsTheNetflowFigures(t *testing.T, h *collecttest.NetflowHarness, fixtures []collecttest.JoinFixture) {
	t.Helper()
	t.Run("NJ-AC12 the purge keeps the purged part's netflow figures", func(t *testing.T) {
		before := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h WHERE exit_leg_device IS NULL")
		ctx := context.Background()
		if err := h.Store.SetSetting(ctx, "retention_seconds", "600", collecttest.ReferenceEpoch()); err != nil {
			t.Fatal(err)
		}
		if err := h.Collector.Purge(ctx); err != nil {
			t.Fatal(err)
		}
		h.Derive(t)
		if left := collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow"); left != 0 {
			t.Fatalf("%d legs survived the purge", left)
		}
		purged := collecttest.ScalarCount(t, h.Store, "SELECT coalesce(sum(netflow_bytes), 0) FROM purged_flow_hour")
		after := collecttest.ScalarCount(t, h.Store,
			"SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h WHERE exit_leg_device IS NULL")
		if purged < before || after != before {
			t.Errorf("before the purge %d measured bytes; after it the purged part holds %d and the aggregates %d",
				before, purged, after)
		}
	})
}

// periodFingerprint is every logged column of the six families at one period.
func periodFingerprint(t *testing.T, h *collecttest.NetflowHarness, period string) string {
	t.Helper()
	var lines []string
	for _, family := range []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume",
		"rule_volume"} {
		rows, err := h.Store.DB().QueryContext(context.Background(),
			fmt.Sprintf("SELECT * FROM %s_aggregate_%s", family, period))
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			var line string
			for index, column := range columns {
				if column != "computed_at" && column != "id" && !strings.HasPrefix(column, "netflow_") {
					line += fmt.Sprintf("%s.%s=%v;", family, column, values[index])
				}
			}
			lines = append(lines, line)
		}
		_ = rows.Close()
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestNoStatementSumsTwoMeasures is PS-AC35's last clause, for the store: no statement adds a netflow
// figure to a logged one, or a logged figure to a netflow one.
func TestNoStatementSumsTwoMeasures(t *testing.T) {
	t.Parallel()
	mixed := regexp.MustCompile(`netflow_\w+\s*[+-]\s*(?:\w+\.)?(?:bytes|packet_bytes|allowed_bytes|blocked_bytes|` +
		`unknown_bytes)\b|\b(?:bytes|packet_bytes|allowed_bytes|blocked_bytes|unknown_bytes)\s*[+-]\s*(?:\w+\.)?netflow_`)
	// The store's statement files sit beside the Go file of each part that runs them.
	files, err := filepath.Glob(filepath.Join("..", "store", "*.sql"))
	parts, partsErr := filepath.Glob(filepath.Join("..", "store", "*", "*.sql"))
	files = append(files, parts...)
	if err != nil || partsErr != nil || len(parts) == 0 {
		t.Fatalf("no statement file was found: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if location := mixed.FindIndex(raw); location != nil {
			t.Errorf("%s sums two measures: %q", file, raw[location[0]:location[1]])
		}
	}
}
