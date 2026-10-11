package mirror

import (
	"context"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/broker/infer"
)

// generations reads the generation each row of a learned table was last written by, by key, and
// whether the datum's newest answer holds it.
func (tm *testMirror) generations(t *testing.T, table string) map[string][2]int64 {
	t.Helper()
	rows, err := tm.mirror.db.Query(`SELECT "#key", "#generation", CASE WHEN "#held" THEN 1 ELSE 0 END FROM ` +
		qualified(table) + ` WHERE "#parent" <> 0 OR "#key" <> ''`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string][2]int64{}
	for rows.Next() {
		var key string
		var generation, held int64
		if err := rows.Scan(&key, &generation, &held); err != nil {
			t.Fatal(err)
		}
		result[key] = [2]int64{generation, held}
	}
	return result
}

// servedKeys loads a datum from the mirror and returns the ids of its rows, in order.
func (tm *testMirror) servedKeys(t *testing.T, datum *Datum) []string {
	t.Helper()
	served, err := tm.mirror.Load(context.Background(), datum, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, row := range served.Field("rows").Items {
		keys = append(keys, row.Field("id").Text)
	}
	return keys
}

// TestTheNewestGenerationIsStampedOnTheDatumNotOnUnchangedRows is the maintainer's decision of 12
// October 2026: an answer repeating the last one writes no row -- every row keeps the generation that
// wrote it, and the datum carries the newest -- while it is served whole; a changed row is written
// again, alone; a row the answer no longer holds is no longer served, and is kept until retention;
// and a row coming back is held again.
func TestTheNewestGenerationIsStampedOnTheDatumNotOnUnchangedRows(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	datum, err := tm.mirror.Datum(ctx, Endpoint{Path: "/api/example/incremental/search", Method: "GET"}, nil, "fw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "1",
		Fields: []string{"total", "rows[].id", "rows[].v", "rows[].tags[]"}}); err != nil {
		t.Fatal(err)
	}
	keyed := infer.Corrections{Key: []string{"id"}}
	table := TableName(datum.Endpoint, "rows[]")
	first := `{"total": 3, "rows": [{"id": "a", "v": 1, "tags": ["x"]}, {"id": "b", "v": 2, "tags": []},
		{"id": "c", "v": 3, "tags": ["y", "z"]}]}`

	tm.store(t, datum, first, keyed)
	tm.now += 60
	tm.store(t, datum, first, keyed)
	if datum.Generation != 2 {
		t.Fatalf("the datum's generation is %d after two answers", datum.Generation)
	}
	for key, state := range tm.generations(t, table) {
		if state != [2]int64{1, 1} {
			t.Errorf("an unchanged row %s was written again: generation %d, held %d", key, state[0], state[1])
		}
	}
	if got := tm.servedKeys(t, datum); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("the repeated answer serves %v", got)
	}

	tm.now += 60
	tm.store(t, datum, `{"total": 2, "rows": [{"id": "a", "v": 1, "tags": ["x"]}, {"id": "c", "v": 4, "tags": ["y", "z"]}]}`,
		keyed)
	states := tm.generations(t, table)
	if states["a"] != [2]int64{1, 1} || states["c"] != [2]int64{3, 1} || states["b"] != [2]int64{1, 0} {
		t.Errorf("after one change and one removal the rows are %v", states)
	}
	if got := tm.servedKeys(t, datum); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("a row the answer no longer holds is served: %v", got)
	}
	served, err := tm.mirror.Load(ctx, datum, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if tags := served.Field("rows").Items[1].Field("tags"); tags == nil || len(tags.Items) != 2 {
		t.Errorf("the nested rows of a changed row are %s", served.Marshal())
	}

	tm.now += 60
	tm.store(t, datum, first, keyed)
	states = tm.generations(t, table)
	if states["b"] != [2]int64{4, 1} {
		t.Errorf("a row coming back is %v, not held again by the newest answer", states["b"])
	}
	got := tm.servedKeys(t, datum)
	sort.Strings(got)
	if len(got) != 3 {
		t.Errorf("the answer holding all three again serves %v", got)
	}
}

// TestARepeatedAnswerIsHeldUntilTheRetentionOfItsNewestAnswer: a row held unchanged keeps the
// instant it was last written, and is purged by the newest answer's, never its own.
func TestARepeatedAnswerIsHeldUntilTheRetentionOfItsNewestAnswer(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	datum, err := tm.mirror.Datum(ctx, Endpoint{Path: "/api/example/held/search", Method: "GET"}, nil, "fw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "1",
		Fields: []string{"rows[].id"}}); err != nil {
		t.Fatal(err)
	}
	start := tm.now
	body := `{"rows": [{"id": "a"}, {"id": "b"}]}`
	tm.store(t, datum, body, infer.Corrections{Key: []string{"id"}})
	tm.now += 1000
	tm.store(t, datum, body, infer.Corrections{Key: []string{"id"}})
	if err := tm.mirror.Purge(ctx, start+500); err != nil {
		t.Fatal(err)
	}
	if got := tm.rowCount(t, TableName(datum.Endpoint, "rows[]")); got != 2 {
		t.Errorf("a purge past the rows' own writing and before the newest answer left %d rows of 2", got)
	}
}
