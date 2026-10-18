package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The declared measures in the store (PA5 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): a measure a test declares here, which no file of the tree
// names, is aggregated, purged and counted on a second leg's exit row exactly as the netflow measure is,
// and declaring it adds no column.

// exampleMeasure is a measure no plugin of the tree declares: three states, one marker.
func exampleMeasure() vocab.DeclaredMeasure {
	return vocab.DeclaredMeasure{Name: "example_rate", Label: "plugin.example.measure", Description: "A test's measure.",
		ProviderKind: "example_kind", CompleteState: "counted", RefusedState: "refused",
		States: []vocab.DeclaredMeasureState{{Name: "counted", Covered: true}, {Name: "missing_data"},
			{Name: "refused"}},
		Markers: []string{"estimated"},
		Classes: vocab.MeasureClasses{Complete: "counted_both", Partial: "partial", Unmeasured: "unmeasured",
			NotKnown: "unknown_start"},
		WindowStates: vocab.MeasureWindowStates{Covered: "example_covered", Unrecorded: "example_unrecorded",
			NoProvider: "example_off", NotCovering: "example_partial", NotServed: "example_not_served"},
		BestAvailable: true}
}

// measureRow is one direction of a connection's output, as the test writes it.
type measureRow struct {
	connection, start, bytes, packets, records, markers int64
	direction, state                                    string
}

// TestADeclaredMeasureIsAggregatedPurgedAndCountedOnTheExitRow is PA-AC7's column rule and PA-AC8: a
// measure declared by a test adds no column; the volume family's parts of it, at 1h, 24h, 7d and 30d,
// equal an independent computation over the flows -- a second leg's exit row carrying its connection's
// figure, a refused second leg's forward direction counting nothing and counted in the measure's
// refused state; and a purge keeps a purged flow's share in every period.
func TestADeclaredMeasureIsAggregatedPurgedAndCountedOnTheExitRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	network := storetest.NewNetwork(t, 2, 4)
	measure := exampleMeasure()
	if err := vocab.RegisterMeasure(measure); err != nil {
		t.Fatal(err)
	}
	columns := "SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()"
	before := storetest.QueryInt(t, network.DB, columns)
	if err := network.DB.RegisterMeasures(ctx, []vocab.DeclaredMeasure{measure}, network.Now); err != nil {
		t.Fatal(err)
	}
	if after := storetest.QueryInt(t, network.DB, columns); after != before {
		t.Fatalf("declaring a measure took the schema from %d columns to %d", before, after)
	}

	// Six connections of two legs, the odd ones refused at their second leg, half of them two hours
	// old so the purge below removes them; and loose single records.
	var specs []storetest.Flow
	for index := 0; index < 6; index++ {
		at := network.Now - 100 - int64(index)*10
		if index%2 == 0 {
			at = network.Now - 7200 - int64(index)*10
		}
		first, second := network.Legs(storetest.Connection{Client: network.Clients[index%len(network.Clients)],
			Peer: storetest.Outside(70+index, false), At: at, IPID: int64(4100 + index), Seq: int64(71000 + index),
			Lag: 1}, "")
		if index%2 == 1 {
			second.Action = "block"
		}
		specs = append(specs, first, second)
	}
	specs = append(specs, network.FlowsFor(12, 3*3600)...)
	addresses := network.Insert(t, specs)
	network.DerivePairs(t, addresses, network.Now-4*3600, network.Now)

	// The output of every connection, as a derivation writes it: forward always counted, reverse counted
	// or with no data in turn, estimated in turn; one connection in five is not known at all.
	rows, err := network.DB.DB().QueryContext(ctx,
		"SELECT id, observed_at FROM flow WHERE pair_outcome IS DISTINCT FROM 'second_leg' ORDER BY id")
	check(t, err)
	var written []measureRow
	for rows.Next() {
		var id, at int64
		check(t, rows.Scan(&id, &at))
		if id%5 == 0 {
			continue
		}
		written = append(written, measureRow{connection: id, start: at, direction: "forward", state: "counted",
			bytes: 100 + id%7, packets: 2, records: 1, markers: id % 2})
		reverse := measureRow{connection: id, start: at, direction: "reverse", state: "counted", bytes: 50,
			packets: 1, records: 1}
		if id%3 == 0 {
			reverse.state, reverse.bytes, reverse.packets, reverse.records = "missing_data", 0, 0, 0
		}
		written = append(written, reverse)
	}
	check(t, rows.Close())
	for _, row := range written {
		_, err := network.DB.DB().ExecContext(ctx, `INSERT INTO connection_measure (measure, connection_flow_id,
			direction, connection_start_at, state, bytes, packets, records, markers, final, due_at, computed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, NULL, $10)`, measure.Name, row.connection,
			row.direction, row.start, row.state, row.bytes, row.packets, row.records, row.markers, network.Now)
		check(t, err)
	}
	instants := allFlowInstants(t, network.DB)
	if _, err := storetest.RefreshAggregates(ctx, network.DB, 0, network.Now+5, instants); err != nil {
		t.Fatal(err)
	}

	want := independentParts(t, network, measure, written)
	if want[measure.PartWidth()+5] == 0 && want[measure.PartWidth()+6+2] == 0 {
		t.Fatal("no refused second leg was counted: the test exercises nothing of the exit-row rule")
	}
	periodParts := func() map[string][]int64 {
		read := map[string][]int64{}
		for _, period := range vocab.Periods() {
			var text []byte
			table := "volume_aggregate_" + period.Name
			check(t, network.DB.DB().QueryRowContext(ctx,
				"SELECT measure_parts_sum(measure_parts) -> $1 FROM "+table, measure.Name).Scan(&text))
			var parts []int64
			check(t, json.Unmarshal(text, &parts))
			read[period.Name] = parts
		}
		return read
	}
	for period, got := range periodParts() {
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("the %s volume family holds the measure's parts\n%v\nthe flows sum to\n%v", period, got, want)
		}
	}

	check(t, network.DB.SetSetting(ctx, "retention_seconds", "3600", network.Now))
	check(t, network.DB.Purge(ctx, network.Now))
	if left := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM connection_measure WHERE measure = 'example_rate'"); left >= int64(len(written)) {
		t.Fatalf("the purge removed no measure output (%d rows left)", left)
	}
	if _, err := storetest.RefreshAggregates(ctx, network.DB, 0, network.Now+5, instants); err != nil {
		t.Fatal(err)
	}
	for period, got := range periodParts() {
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("after the purge the %s volume family holds\n%v\nnot what every flow ingested sums to\n%v",
				period, got, want)
		}
	}
}

// independentParts computes a measure's parts over every classified flow from the rows written, as
// the layout of schema.sql states them, without the store's functions: each flow counts its
// connection's directions in the part of its action, a refused second leg's forward direction counting
// nothing and in the refused state.
func independentParts(t *testing.T, network *storetest.Network, measure vocab.DeclaredMeasure,
	written []measureRow) []int64 {
	t.Helper()
	byConnection := map[int64][]measureRow{}
	for _, row := range written {
		byConnection[row.connection] = append(byConnection[row.connection], row)
	}
	width := measure.PartWidth()
	parts := make([]int64, 3*width)
	rows, err := network.DB.DB().Query(`SELECT id, coalesce(pair_outcome, ''), coalesce(paired_flow_id, 0), action
		FROM classified_flow`)
	check(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, paired int64
		var outcome, action string
		check(t, rows.Scan(&id, &outcome, &paired, &action))
		part := map[string]int{"pass": 0, "block": 1, "reject": 1, "unknown": 2}
		index, counted := part[action]
		if !counted {
			continue
		}
		connection := id
		if outcome == "second_leg" {
			connection = paired
		}
		refused := outcome == "second_leg" && index == 1
		base := index * width
		directions := byConnection[connection]
		if len(directions) == 0 {
			parts[base+5]++
			continue
		}
		complete, recorded := 0, 0
		var markers int64
		met := map[string]bool{}
		for _, row := range directions {
			state, bytes, packets, records, bits := row.state, row.bytes, row.packets, row.records, row.markers
			if refused && row.direction == "forward" {
				state, bytes, packets, records, bits = measure.RefusedState, 0, 0, 0, 0
			}
			parts[base] += bytes
			parts[base+1] += packets
			if state == measure.CompleteState {
				complete++
			}
			if records > 0 {
				recorded++
			}
			met[state] = true
			markers |= bits
		}
		switch {
		case complete == 2:
			parts[base+2]++
		case recorded > 0:
			parts[base+3]++
		default:
			parts[base+4]++
		}
		for position, state := range measure.States {
			if met[state.Name] {
				parts[base+6+position]++
			}
		}
		for position := range measure.Markers {
			parts[base+6+len(measure.States)+position] += (markers >> position) & 1
		}
	}
	check(t, rows.Err())
	return parts
}

// check fails the test on an error.
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
