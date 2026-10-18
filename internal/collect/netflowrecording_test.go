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
	return readRecording(t, filepath.Join(recordingDirectory, name))
}

// readRecording reads a recorded file, by its path, as its relations.
func readRecording(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
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
			t.Fatalf("%s %s: %v", path, relation, err)
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
	// regroups says the change also moves the connection between aggregate rows, which a regrouping of the same
	// decision checks: the states it moves directions out of or into are then not left aside in every aggregate.
	regroups bool
}

// recordingChanges are every change the decisions make to the recording: per row, and per aggregate row group.
type recordingChanges struct {
	rows []recordingExpectation
	// regroupings are built from the recording as it was recorded, before any row expectation is applied.
	regroupings func(recorded map[string][]map[string]any) []aggregateRegrouping
}

// comparable is a row as one canonical text, the columns the comparison leaves aside removed: the aggregate
// rows' ids, which number rows and decide nothing (the aggregate refresh numbers them per statement, budget
// item 1 of Lot 3), and the unmatched slots' updated_at, the computation's instant, which a table of counters
// does not carry (it adds every column but its key).
func comparable(relation string, row map[string]any, changedStates map[string]bool) string {
	copied := map[string]any{}
	for key, value := range row {
		copied[key] = value
	}
	if strings.Contains(relation, "_aggregate_") {
		delete(copied, "id")
		delete(copied, "updated_at")
		// The count of connections in a state an expectation moved directions out of or into follows from those
		// directions, and the aggregation that counts it is tested on its own (PA-AC8): it is left aside here.
		if parts, isObject := copied["netflow_parts"].(map[string]any); isObject && len(changedStates) > 0 {
			kept := map[string]any{}
			for key, value := range parts {
				drop := false
				for state := range changedStates {
					drop = drop || strings.HasSuffix(key, "_direction_"+state+"_connections")
				}
				if !drop {
					kept[key] = value
				}
			}
			copied["netflow_parts"] = kept
		}
	}
	encoded, _ := json.Marshal(copied)
	return string(encoded)
}

// compareRecording compares a world's relations with a recording, applying the expected changes first, and
// answers how many rows of each relation were expected to change. Every other difference fails the test.
func compareRecording(t *testing.T, label string, recorded, got map[string][]map[string]any,
	changes recordingChanges) map[string]int {
	t.Helper()
	changed := map[string]int{}
	expectations := changes.rows
	var regroupings []aggregateRegrouping
	if changes.regroupings != nil {
		regroupings = changes.regroupings(recorded)
	}
	names := make([]string, 0, len(recorded))
	for name := range recorded {
		names = append(names, name)
	}
	sort.Strings(names)
	// The expected changes first, every relation's, so the states they move are known before any aggregate is
	// compared.
	changedStates := map[string]bool{}
	for _, name := range names {
		for _, row := range recorded[name] {
			for _, expectation := range expectations {
				if expectation.relation == name && expectation.match(row) {
					before := fmt.Sprint(row["state"])
					expectation.becomes(row)
					if !expectation.regroups {
						changedStates[before] = true
						changedStates[fmt.Sprint(row["state"])] = true
					}
					changed[expectation.decision+": "+expectation.class]++
				}
			}
		}
	}
	for _, name := range names {
		recordedRows, gotRows := recorded[name], got[name]
		if strings.Contains(name, "_aggregate_") && len(regroupings) > 0 {
			var touched int
			recordedRows, gotRows, touched = regroup(t, label, name, recordedRows, gotRows, regroupings)
			if touched > 0 {
				changed["aggregate rows regrouped, checked by their sums: "+name] += touched
			}
		}
		want := map[string]int{}
		for _, row := range recordedRows {
			want[comparable(name, row, changedStates)]++
		}
		have := map[string]int{}
		for _, row := range gotRows {
			have[comparable(name, row, changedStates)]++
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
			recordingExpectations(t, h))
		for class, count := range changed {
			t.Logf("expected change, %s: %d rows", class, count)
		}
	})
	t.Run("F17, the upstream captured on ingress", func(t *testing.T) {
		t.Parallel()
		h := runJoinWorld(t, world, upstreamIngress, orderForwardFirst, nil)
		compareRecording(t, "f17_upstream_ingress_captured.json", loadRecording(t, "f17_upstream_ingress_captured.json"),
			recordingRelations(t, h), recordingExpectations(t, h))
	})
}

// recordedStates are the direction states the recording knows: the twelve the netflow measure declared when it
// was made, before N3 and D5 of the live validation of 17 October 2026 added two.
var recordedStates = map[string]bool{"measured": true, "in_progress": true, "awaiting_export": true, "missing": true,
	"export_gap": true, "not_exported_when_refused": true, "forwarding_export_unsettled": true,
	"ingress_not_captured": true, "parent_copies_only": true, "interface_not_exported": true,
	"interface_not_captured": true, "capture_configuration_unknown": true}

// TestTheJoinInJavaScriptReproducesTheGoRecordingOfF16 is PA-AC14 for F16: datagrams from five interleaved
// counters, one dropped, give the recorded gap and the recorded records, the loss bound supplied by the
// netflow plugin's capture read (PA-AC11).
func TestTheJoinInJavaScriptReproducesTheGoRecordingOfF16(t *testing.T) {
	t.Parallel()
	world := LoadJoinWorld(t, JoinFixtureDirectory)
	var counters struct {
		Provenance          map[string]any `json:"__provenance__"`
		Name                string         `json:"name"`
		Counters            []uint32       `json:"counters"`
		DatagramsPerCounter int            `json:"datagrams_per_counter"`
		Dropped             struct {
			Counter  int `json:"counter"`
			Datagram int `json:"datagram"`
		} `json:"dropped"`
		Record FixtureRecord `json:"record"`
	}
	raw, err := os.ReadFile(filepath.Join(JoinFixtureDirectory, "f16_interleaved_counters.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &counters); err != nil {
		t.Fatal(err)
	}
	h := newNetflowWorld(t, world.Body, world.Offered())
	h.Capture(t, CaptureStartAt, world.Captured(nil)...)
	h.At(NetflowReadSecond)
	h.Deferred = true
	export := LegInstant + 10
	for round := 0; round < counters.DatagramsPerCounter; round++ {
		for counter, first := range counters.Counters {
			export++
			if counter == counters.Dropped.Counter && round == counters.Dropped.Datagram {
				continue
			}
			record := counters.Record.FlowRecord()
			record.SrcPort = uint16(40000 + counter*100 + round)
			h.DeliverRaw(t, Datagram(export, first+uint32(round), record))
		}
	}
	h.RecordGaps()
	h.Derive(t)
	got := recordingRelations(t, h)
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT (to_jsonb(g) - 'id' - 'provider_id')::text FROM collection_gap g`)
	if err != nil {
		t.Fatal(err)
	}
	got["collection_gap"] = []map[string]any{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		row := map[string]any{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		got["collection_gap"] = append(got["collection_gap"], row)
	}
	_ = rows.Close()
	compareRecording(t, "f16_interleaved_counters.json", loadRecording(t, "f16_interleaved_counters.json"), got,
		recordingExpectations(t, h))
}

// loadRecordingFile reads a recording kept outside the repository -- the capture's, which is never committed
// (PA-AC16) -- as its relations.
func loadRecordingFile(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	return readRecording(t, path)
}

// recordingExpectations are the outcomes of the recording the orchestrator's decisions of the live validation of
// 17 October 2026 change on purpose, each class found in the world by a reading of its own -- the legs and the
// records as stored, never the plugin's code:
//   - D5, local delivery: a connection to a limited broadcast or multicast destination ends at the firewall
//     itself, so its directions take NJ5's answer with the firewall as an end, and the aggregate rows holding it
//     are regrouped (netflowlocaldelivery_test.go);
//   - D5, refused: the reverse direction of a connection pf refused -- its first or second leg blocked or
//     rejected -- is not forwarding_export_unsettled: no reply is expected, and it is not_expected_when_refused,
//     covered;
//   - N3: a direction counted by one exporter, with no record joined and settled missing, whose records the join
//     found but left ambiguous between two legs -- an ambiguous record with the first leg's tuple in the
//     direction's orientation, started within the leg's export bound -- is ambiguous_records, not covered.
//
// The local-delivery class is applied first: its connection is not one leaving by an unknown interface, so the
// refused class's two exporters do not apply to it.
//
// N2 (filter_log_gap) changes no outcome the recording holds: neither the fixtures nor the capture record a gap of
// the filter log. TestAFlowRecordWhoseLegFellInAFilterLogGapIsNotNoLoggedLeg shows it on a fixture of its own.
func recordingExpectations(t *testing.T, h *netflowWorld) recordingChanges {
	t.Helper()
	refused, ambiguous := refusedConnections(t, h), ambiguousDirections(t, h)
	local := readLocalDelivery(t, h, ambiguous)
	measure := "connection_measured_volume"
	return recordingChanges{rows: []recordingExpectation{
		local.rowExpectation(),
		{decision: "D5", class: "the reverse of a refused connection, once forwarding_export_unsettled", relation: measure,
			match: func(row map[string]any) bool {
				_, localDelivery := local.connections[fmt.Sprint(row["connection_leg"])]
				return !localDelivery && row["state"] == "forwarding_export_unsettled" && row["direction"] == "reverse" &&
					refused[fmt.Sprint(row["connection_leg"])]
			},
			becomes: func(row map[string]any) { row["state"] = "not_expected_when_refused" }},
		{decision: "N3", class: "a direction whose only records were ambiguous, once missing", relation: measure,
			match: func(row map[string]any) bool {
				_, localDelivery := local.connections[fmt.Sprint(row["connection_leg"])]
				return !localDelivery && row["state"] == "missing" && row["counted_copy"] == float64(0) &&
					ambiguous[fmt.Sprint(row["connection_leg"])+"|"+fmt.Sprint(row["direction"])]
			},
			becomes: func(row map[string]any) { row["state"] = "ambiguous_records" }},
	}, regroupings: func(recorded map[string][]map[string]any) []aggregateRegrouping {
		return []aggregateRegrouping{local.regrouping(recorded)}
	}}
}

// refusedConnections are the connections pf refused, by their first (or single) leg's digest.
func refusedConnections(t *testing.T, h *netflowWorld) map[string]bool {
	t.Helper()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT f.log_digest FROM flow AS f
		 LEFT JOIN flow AS s ON f.pair_outcome = 'first_leg' AND s.id = f.paired_flow_id
		 WHERE f.pair_outcome IS DISTINCT FROM 'second_leg'
		   AND (f.action IN ('block', 'reject') OR s.action IN ('block', 'reject'))`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	refused := map[string]bool{}
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			t.Fatal(err)
		}
		refused[digest] = true
	}
	return refused
}

// ambiguousDirections are the directions of connections, "<first leg digest>|<direction>", that an ambiguous record
// belongs to: the record's tuple is the first leg's logged tuple, forward, or its swap, reverse -- the ports set
// aside for ICMP, ICMPv6 and a record with no port -- and it started no earlier than the leg's instant minus the
// join tolerance and no later than the leg's export bound, the configuration's active timeout plus the tolerance.
func ambiguousDirections(t *testing.T, h *netflowWorld) map[string]bool {
	t.Helper()
	ctx := context.Background()
	const tolerance = 3
	var active int64
	if err := h.Store.DB().QueryRowContext(ctx, `SELECT coalesce(max(active_timeout_seconds), 0)
		FROM plugin_netflow.capture_configuration`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	type tuple struct {
		src, dst         string
		srcPort, dstPort int64
		protocol         int64
	}
	records := map[tuple][]int64{}
	rows, err := h.Store.DB().QueryContext(ctx, `SELECT canonical_address(src_address), canonical_address(dst_address),
		    coalesce(src_port, -1), coalesce(dst_port, -1), protocol, start_at
		FROM plugin_netflow.flow_record WHERE join_state = 'unmatched' AND unmatched_reason = 'ambiguous'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key tuple
		var start int64
		if err := rows.Scan(&key.src, &key.dst, &key.srcPort, &key.dstPort, &key.protocol, &start); err != nil {
			t.Fatal(err)
		}
		if key.protocol == 1 || key.protocol == 58 || key.srcPort < 0 || key.dstPort < 0 ||
			(key.srcPort == 0 && key.dstPort == 0) {
			key.srcPort, key.dstPort = 0, 0
		}
		records[key] = append(records[key], start)
	}
	_ = rows.Close()
	directions := map[string]bool{}
	if len(records) == 0 {
		return directions
	}
	legs, err := h.Store.DB().QueryContext(ctx, `SELECT log_digest, canonical_address(src_address),
		    canonical_address(dst_address), coalesce(src_port, -1), coalesce(dst_port, -1), protocol, observed_at
		FROM flow WHERE pair_outcome IS DISTINCT FROM 'second_leg'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legs.Close() }()
	numbers := map[string]int64{"tcp": 6, "udp": 17, "icmp": 1, "ipv6-icmp": 58}
	for legs.Next() {
		var digest, src, dst, name string
		var srcPort, dstPort, at int64
		if err := legs.Scan(&digest, &src, &dst, &srcPort, &dstPort, &name, &at); err != nil {
			t.Fatal(err)
		}
		protocol, known := numbers[name]
		if !known {
			continue
		}
		for _, direction := range []string{"forward", "reverse"} {
			key := tuple{src: src, dst: dst, srcPort: srcPort, dstPort: dstPort, protocol: protocol}
			if direction == "reverse" {
				key = tuple{src: dst, dst: src, srcPort: dstPort, dstPort: srcPort, protocol: protocol}
			}
			if protocol == 1 || protocol == 58 || key.srcPort < 0 || key.dstPort < 0 ||
				(key.srcPort == 0 && key.dstPort == 0) {
				key.srcPort, key.dstPort = 0, 0
			}
			for _, start := range records[key] {
				if start >= at-tolerance && start <= at+active+tolerance {
					directions[digest+"|"+direction] = true
				}
			}
		}
	}
	return directions
}
