package mirror

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// fixture is an answer holding every case of the lossless round trip (BR-AC5).
const fixture = `{"total": 5, "rowCount": 5, "current": 1, "meta": {"version": "26.7.3", "build": 7},
	"interfaces": {"igb0": {"name": "lan", "mtu": 1500}, "vlan01": {"name": "guests", "mtu": 1400}},
	"rows": [
	{"id": "a1", "zeros": "007", "tilde": "~", "nothing": null, "empty": "", "big": 9007199254740993,
	 "ratio": 1.50, "flag": true, "tags": ["x", "y"]},
	{"id": "a2", "zeros": "010", "tilde": "~", "nothing": null, "empty": "", "big": 18014398509481985,
	 "ratio": 2.250, "flag": false, "tags": []},
	{"id": "a3", "zeros": "000", "tilde": "x", "empty": "", "big": 1, "ratio": 1e3, "flag": true, "tags": ["z"]},
	{"id": "a4", "zeros": "1", "tilde": "~", "nothing": null, "empty": "e", "big": 2, "ratio": 0.10, "flag": true},
	{"id": "a5", "zeros": "2", "tilde": "~", "nothing": null, "empty": "", "big": 3, "ratio": -0.5, "flag": false,
	 "tags": ["w"]}]}`

type testMirror struct {
	mirror *Mirror
	now    int64
}

func newTestMirror(t *testing.T) *testMirror {
	t.Helper()
	database := storetest.Open(t)
	test := &testMirror{now: 1790000000}
	test.mirror = New(database, func() int64 { return test.now })
	return test
}

// observe reads paths of an answer as a reader would, and returns the reads.
func observe(value *answer.Value, read func(root *answer.Observed)) *answer.Reads {
	reads := answer.NewReads()
	read(answer.Observe(value, reads, nil))
	return reads
}

// readFields reads the given fields of every row, and total.
func readFields(fields ...string) func(*answer.Observed) {
	return func(root *answer.Observed) {
		_, _, _ = root.Get("total")
		rows, _, _ := root.Get("rows")
		count, _ := rows.Len()
		for index := 0; index < count; index++ {
			row := rows.Index(index)
			for _, field := range fields {
				_, _, _ = row.Get(field)
			}
		}
	}
}

func (tm *testMirror) store(t *testing.T, datum *Datum, body string, corrections infer.Corrections) *answer.Value {
	t.Helper()
	value, err := answer.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	shape := infer.Learn(value, corrections, nil, decode.Normaliser{})
	if _, err := tm.mirror.Store(context.Background(), datum, shape, value, Fetch{At: tm.now, StatusCode: 200,
		Body: []byte(body)}); err != nil {
		t.Fatalf("storing: %v", err)
	}
	return value
}

func columnsOf(t *testing.T, tm *testMirror, table string) []string {
	t.Helper()
	rows, err := tm.mirror.db.Query(`SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'opnsense' AND table_name = ? AND left(column_name, 1) <> '#' ORDER BY 1`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		names = append(names, name)
	}
	return names
}

func TestOnlyTheFieldsReadAreStoredAndANewOneIsAdded(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	datum, err := tm.mirror.Datum(ctx, Endpoint{Path: "/api/example/rows/search", Method: "GET"}, nil, "fw")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := answer.Parse([]byte(fixture))
	reads := observe(value, readFields("id", "zeros"))
	if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "1",
		Fields: reads.Fields()}); err != nil {
		t.Fatal(err)
	}
	tm.store(t, datum, fixture, infer.Corrections{})
	table := TableName(datum.Endpoint, "rows[]")
	if got := strings.Join(columnsOf(t, tm, table), ","); got != "id,zeros" {
		t.Fatalf("the rows table holds the columns %s", got)
	}
	served, err := tm.mirror.Load(ctx, datum, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	rows := served.Field("rows")
	if rows == nil || len(rows.Items) != 5 || rows.Items[0].Field("zeros").Text != "007" {
		t.Fatalf("the served rows are %s", served.Marshal())
	}
	holdingOf := answer.Observe(served, answer.NewReads(), nil)
	servedRows, _, _ := holdingOf.Get("rows")
	if _, _, err := servedRows.Index(0).Get("tilde"); err == nil {
		t.Error("a field the mirror does not hold was answered")
	}

	added, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "2",
		Fields: []string{"rows[].tilde"}})
	if err != nil || len(added) != 1 {
		t.Fatalf("a new field read was not reported new: %v %v", added, err)
	}
	tm.now += 60
	tm.store(t, datum, fixture, infer.Corrections{})
	if got := strings.Join(columnsOf(t, tm, table), ","); got != "id,tilde,zeros" {
		t.Errorf("after a new read the columns are %s", got)
	}
	served, _ = tm.mirror.Load(ctx, datum, Selection{})
	if served.Field("rows").Items[2].Field("tilde").Text != "x" {
		t.Errorf("the new field is not filled: %s", served.Marshal())
	}
}

func TestTheMirrorServesEveryFieldExactlyAsTheFirewallWroteIt(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	datum, _ := tm.mirror.Datum(ctx, Endpoint{Path: "/api/example/rows/search", Method: "GET"}, nil, "fw")
	value, _ := answer.Parse([]byte(fixture))
	reads := observe(value, func(root *answer.Observed) {
		_, _ = root.Whole()
	})
	if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "whole", ReaderVersion: "1",
		Whole: reads.WholePaths()}); err != nil {
		t.Fatal(err)
	}
	original := tm.store(t, datum, fixture, infer.Corrections{})
	// The body is kept for a datum read whole; the rows are rebuilt from the tables alone.
	datum.Body = ""
	served, err := tm.mirror.Load(ctx, datum, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	var compare func(path string, want, got *answer.Value)
	compare = func(path string, want, got *answer.Value) {
		if want == nil {
			if got != nil {
				t.Errorf("%s: an absent field is served as %s", path, got.Marshal())
			}
			return
		}
		if got == nil {
			t.Errorf("%s: %s is not served", path, want.Marshal())
			return
		}
		if want.Kind != got.Kind || want.Text != got.Text {
			t.Errorf("%s: the firewall wrote %s, the mirror serves %s", path, want.Marshal(), got.Marshal())
			return
		}
		switch want.Kind {
		case answer.Array:
			if len(want.Items) != len(got.Items) {
				t.Errorf("%s: %d items, served %d", path, len(want.Items), len(got.Items))
				return
			}
			for index := range want.Items {
				compare(path+"[]", want.Items[index], got.Items[index])
			}
		case answer.Object:
			keys := append([]string(nil), want.Keys...)
			sort.Strings(keys)
			for _, key := range keys {
				compare(path+"."+key, want.Fields[key], got.Fields[key])
			}
			for _, key := range got.Keys {
				if _, present := want.Fields[key]; !present {
					t.Errorf("%s.%s is served and was never answered", path, key)
				}
			}
		}
	}
	compare("", original, served)
	interfaces := TableName(datum.Endpoint, "interfaces.*")
	if got := strings.Join(columnsOf(t, tm, interfaces), ","); got != "mtu,name" {
		t.Errorf("the value-keyed object is stored with the columns %s, not as rows", got)
	}
}

func TestATypeChangeConvertsTheColumnToJSONBWithoutLoss(t *testing.T) {
	t.Parallel()
	tm := newTestMirror(t)
	ctx := context.Background()
	datum, _ := tm.mirror.Datum(ctx, Endpoint{Path: "/api/example/value/get", Method: "GET"}, nil, "fw")
	if _, err := tm.mirror.RecordReads(ctx, datum.ID, Read{Reader: "example", ReaderVersion: "1",
		Fields: []string{"value"}}); err != nil {
		t.Fatal(err)
	}
	tm.store(t, datum, `{"value": 42}`, infer.Corrections{})
	tm.now += 60
	tm.store(t, datum, `{"value": "forty-two"}`, infer.Corrections{})
	var kind string
	if err := tm.mirror.db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = 'opnsense'
		AND table_name = ? AND column_name = 'value'`, TableName(datum.Endpoint, "")).Scan(&kind); err != nil ||
		kind != "jsonb" {
		t.Fatalf("the column is %q (%v)", kind, err)
	}
	served, _ := tm.mirror.Load(ctx, datum, Selection{})
	if field := served.Field("value"); field == nil || field.Kind != answer.String || field.Text != "forty-two" {
		t.Errorf("the converted column serves %s", served.Marshal())
	}
	var versions int
	_ = tm.mirror.db.QueryRow(`SELECT count(*) FROM opnsense.mirror_schema_version WHERE endpoint_id = ?
		AND cause LIKE '%converted%'`, datum.EndpointID).Scan(&versions)
	if versions != 1 {
		t.Errorf("%d conversion versions recorded", versions)
	}
}
