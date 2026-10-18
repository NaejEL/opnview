package collect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// PA10 and PA-AC14 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md: before the join left
// Go, its output over NJ12's fixtures F1 to F17 was recorded under plugins/netflow/testdata/go-join/ --
// every flow record's derived fields, every per-direction figure and state, every unmatched slot and every
// aggregate's netflow figures at 1h, 24h, 7d and 30d -- with column-map.json naming where each recorded
// column lives once the join is the netflow plugin's. The plugin's JavaScript join must reproduce it value
// for value, except where the orchestrator's decisions of the live validation of 17 October 2026 change an
// outcome on purpose (N2, N3 and D5): each such change is listed in recordingExpectations, by record class,
// with the decision it follows, and nothing else may differ.

// The orders NJ-AC6 ingests the fixtures in.
const (
	orderForwardFirst = "forward first"
	orderReverseFirst = "reverse first"
	orderInterleaved  = "interleaved"
)

// pendingPassAt is the instant of the pass every order but the interleaved one runs first: past every
// record's start plus the tolerance, before every leg's export bound (NJ4 rule 4).
var pendingPassAt = LegInstant + 100

// runJoinWorld builds a world, reads its capture configuration, and ingests fixtures in an order. A
// check, when given, runs between the pass at pendingPassAt and the last one.
func runJoinWorld(t *testing.T, world JoinWorld, fixtures []JoinFixture, order string,
	beforeBound func(h *netflowWorld)) *netflowWorld {
	t.Helper()
	h := newNetflowWorld(t, world.Body, world.Offered())
	h.Capture(t, CaptureStartAt, world.Captured(fixtures[0].Capture)...)
	h.At(NetflowReadSecond)
	side := func(want string) func(FixtureRecord) bool {
		return func(record FixtureRecord) bool { return record.Side == want }
	}
	legs := FixtureLegs(fixtures)
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
		h.LoggedAt(t, LegInstant+2, legs[:len(legs)/2]...)
		h.DeliverFixtureRecords(t, fixtures, side("forward"))
		h.Logged(t, legs[len(legs)/2:]...)
	}
	return h
}

// splitByCapture keeps apart the fixtures that run under the world's capture configuration and those
// that bring their own.
func splitByCapture(fixtures []JoinFixture) (own, other []JoinFixture) {
	for _, fixture := range fixtures {
		if len(fixture.Capture) == 0 {
			own = append(own, fixture)
		} else {
			other = append(other, fixture)
		}
	}
	return own, other
}

// digestsOf are the digests of the legs of fixtures.
func digestsOf(fixtures []JoinFixture) []string {
	var digests []string
	for _, fixture := range fixtures {
		for _, leg := range fixture.Legs {
			digests = append(digests, leg.Digest)
		}
	}
	return digests
}

// recordingDirectory is where the Go join's recording lies, relative to internal/collect.
const recordingDirectory = "../../plugins/netflow/testdata/go-join"

// recordingPeriods and recordingFamilies are the aggregate periods and families the recording covers.
var (
	recordingPeriods  = []string{"1h", "24h", "7d", "30d"}
	recordingFamilies = []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume", "rule_volume"}
)

// unmatchedTables names the plugin's counter table of each period, the recording's
// unmatched_record_aggregate_<period> (column-map.json).
var unmatchedTables = map[string]string{"1h": "unmatched_record", "24h": "unmatched_record_24h",
	"7d": "unmatched_record_7d", "30d": "unmatched_record_30d"}

// recordingQueries answer, per recorded relation, one JSON text per row in the recording's own column names
// (column-map.json): the flow records, the per-direction figures, the unmatched slots and every aggregate.
func recordingQueries() []struct{ name, query string } {
	queries := []struct{ name, query string }{
		{"flow_record", `SELECT jsonb_build_object('end_at', r.end_at, 'octets', r.octets, 'packets', r.packets,
		        'copy_leg', c.log_digest, 'dst_port', r.dst_port, 'identity', r.identity, 'protocol', r.protocol,
		        'src_port', r.src_port, 'start_at', r.start_at, 'copy_kind', r.copy_kind, 'tcp_flags', r.tcp_flags,
		        'tuple_key', r.tuple_key, 'decided_at', r.decided_at, 'ip_version', r.ip_version,
		        'join_state', r.join_state, 'joined_leg', j.log_digest, 'dst_address', r.dst_address,
		        'egress_role', r.egress_role, 'orientation', r.orientation, 'received_at', r.received_at,
		        'src_address', r.src_address, 'counted_copy', CAST(r.counted_copy AS INTEGER),
		        'ingress_role', r.ingress_role, 'egress_device', r.egress_device, 'ingress_device', r.ingress_device,
		        'end_millisecond', r.end_millisecond, 'loss_detectable', CAST(r.loss_detectable AS INTEGER),
		        'egress_interface', r.egress_interface, 'exporter_address', r.exporter_address,
		        'unmatched_reason', r.unmatched_reason, 'egress_identifier', r.egress_identifier,
		        'ingress_interface', r.ingress_interface, 'start_millisecond', r.start_millisecond,
		        'ingress_identifier', r.ingress_identifier, 'observation_domain', r.observation_domain)::text
		 FROM plugin_netflow.flow_record r
		 LEFT JOIN flow j ON j.id = r.joined_flow_id
		 LEFT JOIN flow c ON c.id = r.copy_flow_id`},
		{"connection_measured_volume", `SELECT jsonb_build_object('bytes', v.bytes, 'final', CAST(v.final AS INTEGER),
		        'state', v.state, 'due_at', v.due_at, 'packets', v.packets, 'records', v.records,
		        'direction', v.direction, 'computed_at', v.computed_at, 'counted_copy', v.markers & 1,
		        'connection_leg', f.log_digest, 'connection_start_at', v.connection_start_at)::text
		 FROM connection_measure v
		 LEFT JOIN flow f ON f.id = v.connection_flow_id
		 WHERE v.measure = 'netflow'`},
	}
	for _, period := range recordingPeriods {
		queries = append(queries, struct{ name, query string }{"unmatched_record_aggregate_" + period,
			"SELECT to_jsonb(a)::text FROM plugin_netflow." + unmatchedTables[period] + " a"})
	}
	for _, family := range recordingFamilies {
		for _, period := range recordingPeriods {
			table := family + "_aggregate_" + period
			queries = append(queries, struct{ name, query string }{table,
				"SELECT (to_jsonb(a) - 'computed_at')::text FROM " + table + " a"})
		}
	}
	return queries
}

// recordingRelations reads a world's database as the recording's relations, each row decoded.
func recordingRelations(t *testing.T, h *netflowWorld) map[string][]map[string]any {
	t.Helper()
	layout := netflowLayout(t, h)
	relations := map[string][]map[string]any{}
	for _, relation := range recordingQueries() {
		rows, err := h.Store.DB().QueryContext(context.Background(), relation.query)
		if err != nil {
			t.Fatalf("%s: %v", relation.name, err)
		}
		relations[relation.name] = []map[string]any{}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			if parts, carries := row["measure_parts"]; carries {
				delete(row, "measure_parts")
				layout.toRecorded(row, parts)
			}
			relations[relation.name] = append(relations[relation.name], row)
		}
		_ = rows.Close()
	}
	return relations
}

// measureLayout is the netflow measure's parts as the plugin declares them: the states and markers in their
// declared order.
type measureLayout struct {
	states  []string
	markers []string
}

// netflowLayout reads the netflow measure's declared states and markers.
func netflowLayout(t *testing.T, h *netflowWorld) measureLayout {
	t.Helper()
	var layout measureLayout
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT state FROM declared_measure_state WHERE measure = 'netflow' ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		layout.states = append(layout.states, name)
	}
	_ = rows.Close()
	rows, err = h.Store.DB().QueryContext(context.Background(),
		`SELECT marker FROM declared_measure_marker WHERE measure = 'netflow' ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		layout.markers = append(layout.markers, name)
	}
	_ = rows.Close()
	if len(layout.states) == 0 {
		t.Fatal("the netflow measure declares no state")
	}
	return layout
}

// recordingParts are the action parts, in their order in a measure's parts.
var recordingParts = []string{"allowed", "blocked", "unknown"}

// toRecorded writes an aggregate row's netflow parts in the recording's columns (column-map.json):
// netflow_bytes, and netflow_parts holding every part's packets, classes, direction states and marker.
func (l measureLayout) toRecorded(row map[string]any, raw any) {
	width := 6 + len(l.states) + len(l.markers)
	figures := make([]float64, 3*width)
	if object, isObject := raw.(map[string]any); isObject {
		if list, isList := object["netflow"].([]any); isList {
			for index, value := range list {
				if number, isNumber := value.(float64); isNumber && index < len(figures) {
					figures[index] = number
				}
			}
		}
	}
	parts := map[string]any{}
	bytes := 0.0
	for index, part := range recordingParts {
		base := index * width
		bytes += figures[base]
		row["netflow_"+part+"_bytes"] = figures[base]
		parts[part+"_packets"] = figures[base+1]
		parts[part+"_measured_connections"] = figures[base+2]
		parts[part+"_partial_connections"] = figures[base+3]
		parts[part+"_unmeasured_connections"] = figures[base+4]
		parts[part+"_capture_not_known_connections"] = figures[base+5]
		for position, state := range l.states {
			// A state the recording predates is written only when a direction is in it: the change is then a
			// difference an expectation names.
			if value := figures[base+6+position]; recordedStates[state] || value != 0 {
				parts[part+"_direction_"+state+"_connections"] = value
			}
		}
		for position, marker := range l.markers {
			parts[part+"_"+marker+"_connections"] = figures[base+6+len(l.states)+position]
		}
	}
	row["netflow_bytes"] = bytes
	row["netflow_parts"] = parts
}

// loadRecording reads one recorded file as its relations.
func loadRecording(t *testing.T, name string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recordingDirectory, name))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	relations := map[string][]map[string]any{}
	for relation, rows := range file {
		if relation == "__provenance__" {
			continue
		}
		var decoded []map[string]any
		if err := json.Unmarshal(rows, &decoded); err != nil {
			t.Fatalf("%s %s: %v", name, relation, err)
		}
		relations[relation] = decoded
	}
	return relations
}

// recordingExpectation is one outcome the recording holds that a decision of the orchestrator changes on
// purpose: in a relation, every recorded row matching `match` is expected as `becomes` says instead.
type recordingExpectation struct {
	// decision names the decision, as the spec numbers it.
	decision string
	// class says, in words, which records the change is about.
	class    string
	relation string
	match    func(row map[string]any) bool
	becomes  func(row map[string]any)
}

// comparable is a row as one canonical text, the columns the comparison leaves aside removed: the aggregate
// rows' ids, which number rows and decide nothing (the aggregate refresh numbers them per statement, budget
// item 1 of Lot 3), and the unmatched slots' updated_at, the computation's instant, which a table of counters
// does not carry (it adds every column but its key).
func comparable(relation string, row map[string]any) string {
	copied := map[string]any{}
	for key, value := range row {
		copied[key] = value
	}
	if strings.Contains(relation, "_aggregate_") {
		delete(copied, "id")
		delete(copied, "updated_at")
	}
	encoded, _ := json.Marshal(copied)
	return string(encoded)
}

// compareRecording compares a world's relations with a recording, applying the expected changes first, and
// answers how many rows of each relation were expected to change. Every other difference fails the test.
func compareRecording(t *testing.T, label string, recorded, got map[string][]map[string]any,
	expectations []recordingExpectation) map[string]int {
	t.Helper()
	changed := map[string]int{}
	names := make([]string, 0, len(recorded))
	for name := range recorded {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := map[string]int{}
		for _, row := range recorded[name] {
			for _, expectation := range expectations {
				if expectation.relation == name && expectation.match(row) {
					expectation.becomes(row)
					changed[expectation.decision+": "+expectation.class]++
				}
			}
			want[comparable(name, row)]++
		}
		have := map[string]int{}
		for _, row := range got[name] {
			have[comparable(name, row)]++
		}
		var missing, extra []string
		for text, count := range want {
			if have[text] < count {
				missing = append(missing, fmt.Sprintf("%dx %s", count-have[text], text))
			}
		}
		for text, count := range have {
			if want[text] < count {
				extra = append(extra, fmt.Sprintf("%dx %s", count-want[text], text))
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 || len(extra) > 0 {
			t.Errorf("%s, %s: %d recorded rows are not reproduced, and %d rows are not recorded\nrecorded, not "+
				"reproduced:\n  %s\nreproduced, not recorded:\n  %s", label, name, len(missing), len(extra),
				strings.Join(limit(missing, 12), "\n  "), strings.Join(limit(extra, 12), "\n  "))
		}
	}
	for name := range got {
		if _, recordedToo := recorded[name]; !recordedToo {
			t.Errorf("%s: the relation %s is reproduced and was not recorded", label, name)
		}
	}
	return changed
}

// limit keeps the first n lines of a list.
func limit(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return append(lines[:n:n], fmt.Sprintf("... and %d more", len(lines)-n))
}

// TestTheJoinInJavaScriptReproducesTheGoRecording is PA-AC14: for F1 to F17, the plugin's per-record fields,
// per-direction figures, states, markers, unmatched slots and aggregate figures equal the recorded Go golden
// under the column-name map, value for value, but for the outcomes N2, N3 and D5 change on purpose.
func TestTheJoinInJavaScriptReproducesTheGoRecording(t *testing.T) {
	t.Parallel()
	world := LoadJoinWorld(t, JoinFixtureDirectory)
	fixtures, upstreamIngress := splitByCapture(LoadJoinFixtures(t, JoinFixtureDirectory))
	t.Run("F1 to F15 and FA, one world", func(t *testing.T) {
		t.Parallel()
		h := runJoinWorld(t, world, fixtures, orderForwardFirst, nil)
		changed := compareRecording(t, "fixtures.json", loadRecording(t, "fixtures.json"), recordingRelations(t, h),
			recordingExpectations())
		for class, count := range changed {
			t.Logf("expected change, %s: %d rows", class, count)
		}
	})
	t.Run("F17, the upstream captured on ingress", func(t *testing.T) {
		t.Parallel()
		h := runJoinWorld(t, world, upstreamIngress, orderForwardFirst, nil)
		compareRecording(t, "f17_upstream_ingress_captured.json", loadRecording(t, "f17_upstream_ingress_captured.json"),
			recordingRelations(t, h), recordingExpectations())
	})
}

// recordingExpectations are the outcomes of the recording the orchestrator's decisions change on purpose.
func recordingExpectations() []recordingExpectation {
	return nil
}

// recordedStates are the direction states the recording knows: the twelve the netflow measure declared when it
// was made, before N3 and D5 of the live validation of 17 October 2026 added two.
var recordedStates = map[string]bool{"measured": true, "in_progress": true, "awaiting_export": true, "missing": true,
	"export_gap": true, "not_exported_when_refused": true, "forwarding_export_unsettled": true,
	"ingress_not_captured": true, "parent_copies_only": true, "interface_not_exported": true,
	"interface_not_captured": true, "capture_configuration_unknown": true}
