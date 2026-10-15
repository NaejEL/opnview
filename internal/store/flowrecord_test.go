package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/netflow"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The NetFlow tables of the store (PS17, PS18 and the retention of PS-AC44 of the NetFlow join
// amendment).

// flowRecordAt is one flow record of a provider starting at an instant, with no Observation Point.
func flowRecordAt(providerID, at int64, identity string) netflow.FlowRecordRow {
	captured := int64(2)
	return netflow.FlowRecordRow{ProviderID: providerID, Identity: identity, StartAt: at, EndAt: at + 1, ReceivedAt: at + 30,
		ExporterAddress: "192.0.2.1", IPVersion: 4, Protocol: 17, SrcAddress: "198.51.100.10",
		DstAddress: "192.0.2.80", Octets: 100, Packets: 1, CaptureState: netflow.CaptureObservationPointHidden,
		CapturedInterfaces: &captured, TupleKey: "4|17|192.0.2.80|198.51.100.10", JoinState: netflow.JoinNotJoinable}
}

// identityOf is a 64-hexadecimal-digit identity for a test.
func identityOf(number int) string { return fmt.Sprintf("%064x", number) }

// TestFlowRecordIsAHypertableOnItsStart is PS-AC29's catalogue inspection: flow_record is a hypertable
// whose one dimension is start_at.
func TestFlowRecordIsAHypertableOnItsStart(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	var column string
	if err := database.DB().QueryRowContext(context.Background(),
		`SELECT column_name FROM timescaledb_information.dimensions
		 WHERE hypertable_schema = 'opnview' AND hypertable_name = 'flow_record'`).Scan(&column); err != nil {
		t.Fatalf("flow_record is not a hypertable: %v", err)
	}
	if column != "start_at" {
		t.Errorf("flow_record is chunked on %s", column)
	}
}

// TestFlowRecordsArePurgedByChunkThenByRowAtTheirOwnRetention is PS-AC44 for flow_record: rows over
// several days, a retention of their own ending inside a day; whole chunks past the horizon are
// dropped, the chunk holding it keeps exactly the rows at or after it; an older capture configuration
// goes and the newest stays, in force.
func TestFlowRecordsArePurgedByChunkThenByRowAtTheirOwnRetention(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	now := int64(1790000000)
	providerID, _, err := database.EnsureProvider(ctx, "flow_record", "example", "Example", now)
	if err != nil {
		t.Fatal(err)
	}
	retention := int64(2*86400 + 3600)
	if err := database.SetSetting(ctx, netflow.FlowRecordRetentionKey, fmt.Sprint(retention), now); err != nil {
		t.Fatal(err)
	}
	horizon := now - retention
	var rows []netflow.FlowRecordRow
	for offset, number := int64(-5*86400), 0; offset <= 0; offset, number = offset+3600, number+1 {
		rows = append(rows, flowRecordAt(providerID, now+offset, identityOf(number)))
	}
	rows = append(rows, flowRecordAt(providerID, horizon-1, identityOf(10000)),
		flowRecordAt(providerID, horizon, identityOf(10001)))
	if inserted, err := database.InsertFlowRecords(ctx, rows); err != nil || inserted != int64(len(rows)) {
		t.Fatalf("inserted %d of %d (%v)", inserted, len(rows), err)
	}
	for _, at := range []int64{now - 5*86400, now - 60} {
		configuration := netflow.CaptureConfiguration{Readable: true, Enabled: true,
			Interfaces: []netflow.CaptureInterface{{Identifier: fmt.Sprintf("example_if_%d", at%7), Egress: true}}}
		if _, err := database.RecordCaptureRead(ctx, providerID, at, configuration); err != nil {
			t.Fatal(err)
		}
	}
	chunks := func() int {
		var count int
		if err := database.DB().QueryRowContext(ctx,
			"SELECT count(*) FROM show_chunks('opnview.flow_record')").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := chunks()
	if err := database.Purge(ctx, now); err != nil {
		t.Fatal(err)
	}
	if after := chunks(); after >= before {
		t.Errorf("the purge dropped no whole chunk: %d before, %d after", before, after)
	}
	var older, kept int
	if err := database.DB().QueryRowContext(ctx,
		"SELECT count(*) FILTER (WHERE start_at < ?), count(*) FILTER (WHERE start_at >= ?) FROM flow_record",
		horizon, horizon).Scan(&older, &kept); err != nil {
		t.Fatal(err)
	}
	expected := 0
	for _, row := range rows {
		if row.StartAt >= horizon {
			expected++
		}
	}
	if older != 0 || kept != expected {
		t.Errorf("after the purge %d records are before the horizon and %d at or after it, want 0 and %d",
			older, kept, expected)
	}
	configurations, err := database.CaptureConfigurations(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(configurations) != 1 || configurations[0].FirstReadAt != now-60 {
		t.Errorf("the configurations after the purge are %+v, want the newest alone", configurations)
	}
}

// TestACaptureReadExtendsItsRunOrOpensOne is PS18's history: a read equal to the newest extends its
// run, a different one opens a run, and an unreadable one is a run of its own carrying its outcome.
func TestACaptureReadExtendsItsRunOrOpensOne(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, _, err := database.EnsureProvider(ctx, "flow_record", "example", "Example", 100)
	if err != nil {
		t.Fatal(err)
	}
	one := netflow.CaptureConfiguration{Readable: true, Enabled: true,
		Interfaces: []netflow.CaptureInterface{{Identifier: "example_if_a", Ingress: true, Egress: true}}}
	two := netflow.CaptureConfiguration{Readable: true, Enabled: true, Interfaces: append(one.Interfaces,
		netflow.CaptureInterface{Identifier: "example_if_b", Egress: true})}
	unreadable := netflow.CaptureConfiguration{Outcome: vocab.OutcomeDenied, Detail: "HTTP 403"}
	for _, read := range []struct {
		at            int64
		configuration netflow.CaptureConfiguration
		opens         bool
	}{
		{1000, one, true}, {1060, one, false}, {1120, two, true}, {1180, unreadable, true}, {1240, unreadable, false},
		{1300, one, true},
	} {
		done, err := database.RecordCaptureRead(ctx, providerID, read.at, read.configuration)
		if err != nil {
			t.Fatal(err)
		}
		if done.Opened != read.opens {
			t.Errorf("the read at %d opened a run: %t, want %t", read.at, done.Opened, read.opens)
		}
	}
	runs, err := database.CaptureConfigurations(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int64{{1000, 1060}, {1120, 1120}, {1180, 1240}, {1300, 1300}}
	if len(runs) != len(want) {
		t.Fatalf("runs %+v", runs)
	}
	for index, run := range runs {
		if run.FirstReadAt != want[index][0] || run.LastReadAt != want[index][1] {
			t.Errorf("run %d spans [%d, %d], want %v", index, run.FirstReadAt, run.LastReadAt, want[index])
		}
	}
	if runs[2].Readable || runs[2].Outcome != vocab.OutcomeDenied || len(runs[1].Interfaces) != 2 {
		t.Errorf("the unreadable run is %+v and the two-interface run %+v", runs[2], runs[1])
	}
}

// TestAnAddressIsComparedAsAValue is PS19 rule 2's address comparison: canonical_address reads an
// address the filter log wrote in any form as one canonical text, and a text that is not an address as
// NULL, never as an error that would refuse its flow.
func TestAnAddressIsComparedAsAValue(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	for text, want := range map[string]string{
		"2001:DB8:0:0:0:0:0:10": "2001:db8::10", "2001:db8::10": "2001:db8::10", "198.51.100.10": "198.51.100.10",
	} {
		var got string
		if err := database.DB().QueryRowContext(context.Background(), "SELECT canonical_address(?)", text).
			Scan(&got); err != nil || got != want {
			t.Errorf("canonical_address(%q) = %q (%v), want %q", text, got, err, want)
		}
	}
	var null *string
	if err := database.DB().QueryRowContext(context.Background(), "SELECT canonical_address('not an address')").
		Scan(&null); err != nil || null != nil {
		t.Errorf("a text that is not an address reads %v (%v), want NULL", null, err)
	}
}
