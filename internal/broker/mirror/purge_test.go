package mirror

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/broker/infer"
)

// rowCount counts a learned table's rows.
func (tm *testMirror) rowCount(t *testing.T, table string) int {
	t.Helper()
	var count int
	if err := tm.mirror.db.QueryRow(`SELECT count(*) FROM ` + qualified(table)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestTheMirrorIsPurgedToTheExactSecond is A10's retention: a snapshot's row goes once no answer
// held it since the horizon, a record goes once its own time -- its timestamp cursor -- is past it,
// or, under a cursor that is no instant, once it was received before it; never a second early.
func TestTheMirrorIsPurgedToTheExactSecond(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	reading := func(path string, fields ...string) *Datum {
		datum, err := tm.mirror.Datum(ctx, Endpoint{Path: path, Method: "GET"}, nil, "fw")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "1",
			Fields: fields}); err != nil {
			t.Fatal(err)
		}
		return datum
	}
	start := tm.now

	// A snapshot: a2 is held by the second answer, a1 is not.
	snapshot := reading("/api/example/state/search", "rows[].id")
	tm.store(t, snapshot, `{"rows": [{"id": "a1"}, {"id": "a2"}]}`, infer.Corrections{Key: []string{"id"}})
	// A record stream on a timestamp cursor: each record's own time decides.
	timed := reading("/api/example/log/search", "rows[].time", "rows[].v")
	tm.store(t, timed, `{"rows": [{"time": 1790000500, "v": 1}, {"time": 1790000600, "v": 2},
		{"time": 1790000700, "v": 3}]}`, infer.Corrections{Nature: infer.NatureRecordStream, Cursor: []string{"time"}})
	// A record stream on a sequence: when it was received decides.
	sequenced := reading("/api/example/events/search", "rows[].sequence")
	tm.store(t, sequenced, `{"rows": [{"sequence": 1}, {"sequence": 2}, {"sequence": 3}]}`,
		infer.Corrections{Nature: infer.NatureRecordStream, Cursor: []string{"sequence"}})
	tm.now += 100
	tm.store(t, snapshot, `{"rows": [{"id": "a2"}]}`, infer.Corrections{Key: []string{"id"}})

	snapshotTable := TableName(snapshot.Endpoint, "rows[]")
	timedTable := TableName(timed.Endpoint, "rows[]")
	sequencedTable := TableName(sequenced.Endpoint, "rows[]")

	if err := tm.mirror.Purge(ctx, start); err != nil {
		t.Fatal(err)
	}
	if tm.rowCount(t, snapshotTable) != 2 || tm.rowCount(t, sequencedTable) != 3 || tm.rowCount(t, timedTable) != 3 {
		t.Error("a purge at the instant of the answers removed what they held")
	}
	if err := tm.mirror.Purge(ctx, start+1); err != nil {
		t.Fatal(err)
	}
	if got := tm.rowCount(t, snapshotTable); got != 1 {
		t.Errorf("a row no answer held since the horizon was kept: %d rows", got)
	}
	if got := tm.rowCount(t, sequencedTable); got != 0 {
		t.Errorf("records received before the horizon were kept: %d rows", got)
	}
	if got := tm.rowCount(t, timedTable); got != 3 {
		t.Errorf("records whose own time is after the horizon went with their reception: %d rows", got)
	}
	if err := tm.mirror.Purge(ctx, start+600); err != nil {
		t.Fatal(err)
	}
	if got := tm.rowCount(t, timedTable); got != 2 {
		t.Errorf("a purge at a record's own time left %d records, not the two at or after it", got)
	}
}
