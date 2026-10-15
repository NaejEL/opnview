package collect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The NetFlow join and the measured volume (PS18-PS20 of the NetFlow join amendment), each test on the
// PostgreSQL test instance, at a fixed clock, with records delivered through the real decoder and router.

// natRecords are the four records the NAT connection produces at the two devices: its forward packets
// received inside and sent upstream translated, and its replies received upstream to the translated
// destination and sent inside. Each carries FIN, so its state closes within pf's closing timeout.
func natRecords() (insideForward, insideReverse, upstreamForward, upstreamReverse flowRecord) {
	start, end := legInstant, legInstant+10
	insideForward = flowRecord{src: insideClient, dst: outsideServer, srcPort: 50000, dstPort: 443, protocol: 6,
		flags: 0x1b, in: insideIndex, out: upstreamIndex, packets: 10, octets: 1000, start: start, end: end}
	insideReverse = flowRecord{src: outsideServer, dst: insideClient, srcPort: 443, dstPort: 50000, protocol: 6,
		flags: 0x1b, in: upstreamIndex, out: insideIndex, packets: 20, octets: 20000, start: start, end: end}
	upstreamForward = flowRecord{src: translatedSource, dst: outsideServer, srcPort: 61000, dstPort: 443, protocol: 6,
		flags: 0x1b, in: insideIndex, out: upstreamIndex, packets: 10, octets: 1000, start: start, end: end}
	upstreamReverse = flowRecord{src: outsideServer, dst: translatedSource, srcPort: 443, dstPort: 61000, protocol: 6,
		flags: 0x1b, in: upstreamIndex, out: insideIndex, packets: 20, octets: 20000, start: start, end: end}
	return
}

// aggregateFingerprint is every logged column of the six families' hours, for a comparison.
func aggregateFingerprint(t *testing.T, h *netflowHarness) string {
	t.Helper()
	var fingerprint string
	for _, family := range []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume",
		"rule_volume"} {
		rows, err := h.store.DB().QueryContext(context.Background(),
			fmt.Sprintf("SELECT * FROM %s_aggregate_1h ORDER BY 1", family))
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
			for index, column := range columns {
				if column == "computed_at" || column == "id" || strings.HasPrefix(column, "netflow_") {
					continue
				}
				fingerprint += fmt.Sprintf("%s.%s=%v;", family, column, values[index])
			}
			fingerprint += "\n"
		}
		_ = rows.Close()
	}
	return fingerprint
}

// TestTheNATConnectionIsMeasuredAtTheSingleObservationPoint is PS-AC30: one IPv4 connection logged `in`
// on the inside device untranslated and `out` on the upstream device translated, captured (a) on the
// inside device only and (b) on the upstream device only.
func TestTheNATConnectionIsMeasuredAtTheSingleObservationPoint(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, upstreamForward, upstreamReverse := natRecords()
	for _, scenario := range []struct {
		name       string
		capture    captured
		records    []flowRecord
		measuredAt string
		legDigest  string
	}{
		{"inside only", captured{identifier: insideIdentifier}, []flowRecord{insideForward, insideReverse},
			netflow.MeasuredAtFirstLeg, "nat1"},
		{"upstream only", captured{identifier: upstreamIdentifier}, []flowRecord{upstreamForward, upstreamReverse},
			netflow.MeasuredAtSecondLeg, "nat2"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			// The baseline: the same legs with no NetFlow at all.
			baseline := newNetflowHarness(t)
			baseline.logged(t, natLegs("nat", legInstant, "pass")...)
			want := aggregateFingerprint(t, baseline)

			h := newNetflowHarness(t)
			h.capture(t, captureStartAt, scenario.capture)
			h.at(netflowReadSecond)
			h.deliver(t, legInstant+30, scenario.records...)
			h.logged(t, natLegs("nat", legInstant, "pass")...)

			legID := h.flowID(t, scenario.legDigest)
			for _, record := range h.records(t) {
				if record.joinState != netflow.JoinJoined || record.joinedFlowID == nil || *record.joinedFlowID != legID {
					t.Errorf("a record of %d octets is %s (reason %s), joined to %v, want joined to the leg %d",
						record.octets, record.joinState, orNull(record.reason), record.joinedFlowID, legID)
				}
			}
			volumes := h.volumes(t)
			if len(volumes) != 2 {
				t.Fatalf("measured volumes %+v, want the connection's two directions", volumes)
			}
			wantBytes := map[string]int64{netflow.OrientationForward: 1000, netflow.OrientationReverse: 20000}
			for _, volume := range volumes {
				if volume.connection != h.flowID(t, "nat1") {
					t.Errorf("a figure belongs to the connection %d, not to its first leg", volume.connection)
				}
				if volume.state != netflow.VolumeMeasured || volume.bytes != wantBytes[volume.direction] ||
					orNull(volume.leg) != scenario.measuredAt || volume.point != scenario.capture.identifier {
					t.Errorf("the %s direction is %+v (leg %s), want measured, %d bytes, at the %s on %s",
						volume.direction, volume, orNull(volume.leg), wantBytes[volume.direction], scenario.measuredAt,
						scenario.capture.identifier)
				}
			}
			if scenario.measuredAt == netflow.MeasuredAtSecondLeg {
				// The figure measured at the second leg belongs to that leg's exit_leg_device: the device it
				// was logged on, which counts it as it counts the second leg's logged volume.
				exit := scalarCount(t, h.store,
					"SELECT count(*) FROM classified_flow WHERE id = ? AND exit_leg_device = ?", legID, upstreamDevice)
				if exit != 1 {
					t.Error("the second leg is not the exit leg of its device")
				}
			}
			if got := aggregateFingerprint(t, h); got != want {
				t.Errorf("a logged column changed:\nwith NetFlow    %s\nwithout NetFlow %s", got, want)
			}
			// The netflow columns: the connection once, on its first leg's rows, and -- measured at the
			// second leg -- under that leg's exit_leg_device as well (PS20, PS21).
			first := scalarCount(t, h.store,
				"SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h WHERE exit_leg_device IS NULL")
			exit := scalarCount(t, h.store,
				"SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h WHERE exit_leg_device = ?",
				upstreamDevice)
			wantExit := 0
			if scenario.measuredAt == netflow.MeasuredAtSecondLeg {
				wantExit = 21000
			}
			if first != 21000 || exit != wantExit {
				t.Errorf("the volume family counts %d measured bytes on the first leg and %d under the exit leg, "+
					"want 21000 and %d", first, exit, wantExit)
			}
			for _, family := range []string{"client_volume", "owner_volume", "peer_volume"} {
				if counted := scalarCount(t, h.store, "SELECT coalesce(sum((netflow_parts).allowed_measured_connections), 0) "+
					"FROM "+family+"_aggregate_1h"); counted != 1 {
					t.Errorf("the %s family counts the measured connection %d times", family, counted)
				}
			}
			// The finer coverage classes: the aggregates count the connection under each direction state
			// it met -- measured, and nothing else -- in the part of its action (allowed).
			if states := scalarCount(t, h.store, "SELECT coalesce(sum((netflow_parts).allowed_direction_measured_connections "+
				"- (netflow_parts).allowed_direction_not_captured_connections "+
				"- (netflow_parts).blocked_direction_measured_connections), 0) "+
				"FROM volume_aggregate_1h WHERE exit_leg_device IS NULL"); states != 1 {
				t.Errorf("the volume family counts %d allowed connections with a measured direction, want 1", states)
			}
		})
	}
}

// periodFingerprint is every logged column of the six families at one period.
func periodFingerprint(t *testing.T, h *netflowHarness, period string) string {
	t.Helper()
	var fingerprint string
	for _, family := range []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume",
		"rule_volume"} {
		rows, err := h.store.DB().QueryContext(context.Background(),
			fmt.Sprintf("SELECT * FROM %s_aggregate_%s ORDER BY 1", family, period))
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
			for index, column := range columns {
				if column != "computed_at" && column != "id" && !strings.HasPrefix(column, "netflow_") {
					fingerprint += fmt.Sprintf("%s.%s=%v;", family, column, values[index])
				}
			}
			fingerprint += "\n"
		}
		_ = rows.Close()
	}
	return fingerprint
}

// TestTheMeasuresAreCountedOnceAndNeverSummed is PS-AC35: at every period every family's logged
// columns equal the baseline's; the netflow columns equal a computation made connection by connection,
// at the single Observation Point; and after the purge has removed the hour's flows, the hour keeps its
// netflow figures.
func TestTheMeasuresAreCountedOnceAndNeverSummed(t *testing.T) {
	t.Parallel()
	insideForward, insideReverse, _, _ := natRecords()
	second := forward(17, 47000, 53, 0, 500, legInstant+60, legInstant+61)
	legs := append(natLegs("nat", legInstant, "pass"), singleLeg("dns", legInstant+60, "udp", 17, 47000, 53, "pass"))
	baseline := newNetflowHarness(t)
	baseline.logged(t, legs...)

	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	h.deliver(t, legInstant+120, insideForward, insideReverse, second)
	h.logged(t, legs...)
	for _, period := range []string{"1h", "24h", "7d", "30d"} {
		if got, want := periodFingerprint(t, h, period), periodFingerprint(t, baseline, period); got != want {
			t.Errorf("at %s a logged column changed:\n%s\nwant\n%s", period, got, want)
		}
		// The connection by connection computation: each connection's directions, once.
		independent := scalarCount(t, h.store, "SELECT coalesce(sum(bytes), 0) FROM connection_measured_volume")
		for _, family := range []string{"volume", "peer_volume", "client_volume", "owner_volume"} {
			column := "exit_leg_device IS NULL"
			if family != "volume" {
				column = "true"
			}
			counted := scalarCount(t, h.store, fmt.Sprintf(
				"SELECT coalesce(sum(netflow_bytes), 0) FROM %s_aggregate_%s WHERE %s", family, period, column))
			if counted != independent || independent != 21500 {
				t.Errorf("at %s the %s family counts %d measured bytes, the connections hold %d (want 21500)",
					period, family, counted, independent)
			}
		}
	}

	// The purge removes the hour's flows; the hour's netflow figures stay in its purged part.
	if err := h.store.SetSetting(context.Background(), "retention_seconds", "1800", netflowReadSecond); err != nil {
		t.Fatal(err)
	}
	h.at(netflowReadSecond)
	if err := h.collector.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if flows := countRows(t, h.store, "flow"); flows != 0 {
		t.Fatalf("the purge kept %d flows", flows)
	}
	if _, err := h.store.RefreshAggregates(context.Background(), 0, netflowReadSecond,
		[]int64{legInstant}); err != nil {
		t.Fatal(err)
	}
	if kept := scalarCount(t, h.store,
		"SELECT coalesce(sum(netflow_bytes), 0) FROM volume_aggregate_1h WHERE exit_leg_device IS NULL"); kept != 21500 {
		t.Errorf("after the purge the hour counts %d measured bytes, want 21500", kept)
	}
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
