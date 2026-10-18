package broker_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestNoInstantIsDerivedFromFirewallLocalTimeBeforeTheOffsetIsRead is N4 of the live validation of
// 17 October 2026: a firewall on CEST writes its log with zone-less local times. The first read
// after a start came before the offset from UTC was read, and recorded a mirror gap ending 7 200 s
// in the future. Until the offset is read, nothing dated by the firewall's local time is mirrored;
// once it is, the gap is recorded where it lies.
func TestNoInstantIsDerivedFromFirewallLocalTimeBeforeTheOffsetIsRead(t *testing.T) {
	t.Parallel()
	const offset = 2 * 3600 // CEST, which the code under test is never told but through the normaliser
	var mutex sync.Mutex
	known := true
	w := newWorldWith(t, func(clock *testClock) func(context.Context) (decode.Normaliser, error) {
		return func(context.Context) (decode.Normaliser, error) {
			mutex.Lock()
			defer mutex.Unlock()
			return decode.Normaliser{Reference: clock.Now(), OffsetSeconds: offset, OffsetUnknown: !known}, nil
		}
	})
	ep := endpoint(t, http.MethodGet, "/api/example/locallog/search", opnsense.NoBody)
	start := w.clock.Now().Unix()
	rows := []int64{start - 300, start - 200, start - 100}
	w.fake.AnswerWith(func(*http.Request, string) (int, string, string) {
		mutex.Lock()
		defer mutex.Unlock()
		var items []string
		for index := len(rows) - 1; index >= 0; index-- {
			local := time.Unix(rows[index]+offset, 0).UTC().Format("2006-01-02T15:04:05")
			items = append(items, fmt.Sprintf(`{"id": "line-%d", "time": "%s"}`, rows[index], local))
		}
		return http.StatusOK, "application/json", `{"rows": [` + strings.Join(items, ",") + `]}`
	})
	w.call("example_reader", ep, opnsense.RequestOptions{}, 10*time.Second, "id", "time")

	// A restart: the offset is not read yet, and the firewall's log has moved on past a silence.
	w.clock.advance(20 * time.Minute)
	now := w.clock.Now().Unix()
	mutex.Lock()
	known = false
	rows = []int64{now - 10}
	mutex.Unlock()
	served, _ := w.call("example_reader", ep, opnsense.RequestOptions{}, 10*time.Second, "id", "time")
	if served.Response.Outcome != opnsense.OutcomeOK || served.Value == nil {
		t.Fatalf("the reader was not answered before the offset was read: %+v", served.Response)
	}
	gaps := w.gapsOf(t, ep.Path)
	if len(gaps) != 0 {
		t.Errorf("a gap was recorded before the offset was read: %v (now %d)", gaps, now)
	}
	var newest *float64
	if err := w.store.DB().QueryRow(`SELECT max("#cursor") FROM ` + w.rowTable(t, ep.Path)).
		Scan(&newest); err != nil {
		t.Fatal(err)
	}
	if newest == nil {
		t.Fatal("the first read left no row in the mirror")
	}
	if int64(*newest) > now {
		t.Errorf("a row was mirrored at %d, after now %d", int64(*newest), now)
	}

	// The offset is read: the next read records the silence, ending at the oldest row it got.
	mutex.Lock()
	known = true
	mutex.Unlock()
	w.clock.advance(20 * time.Second)
	w.call("example_reader", ep, opnsense.RequestOptions{}, 10*time.Second, "id", "time")
	gaps = w.gapsOf(t, ep.Path)
	if len(gaps) != 1 || gaps[0][0] != start-100 || gaps[0][1] != now-10 {
		t.Errorf("once the offset was read the gap was %v, want [%d %d]", gaps, start-100, now-10)
	}
}

// gapsOf reads the mirror gaps of an endpoint's datums, as [from, to].
func (w *world) gapsOf(t *testing.T, path string) [][2]int64 {
	t.Helper()
	rows, err := w.store.DB().Query(`SELECT g.from_at, g.to_at FROM opnsense.mirror_gap AS g
		JOIN opnsense.mirror_datum AS d ON d.id = g.datum_id JOIN opnsense.mirror_endpoint AS e ON e.id = d.endpoint_id
		WHERE e.path = $1 ORDER BY g.from_at`, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var gaps [][2]int64
	for rows.Next() {
		var gap [2]int64
		if err := rows.Scan(&gap[0], &gap[1]); err != nil {
			t.Fatal(err)
		}
		gaps = append(gaps, gap)
	}
	return gaps
}

// rowTable is the qualified, quoted name of the table holding a GET endpoint's main rows.
func (w *world) rowTable(t *testing.T, path string) string {
	t.Helper()
	var rowSet string
	if err := w.store.DB().QueryRow(`SELECT d.row_set FROM opnsense.mirror_datum AS d
		JOIN opnsense.mirror_endpoint AS e ON e.id = d.endpoint_id WHERE e.path = $1`, path).Scan(&rowSet); err != nil {
		t.Fatalf("no datum of %s: %v", path, err)
	}
	name := mirror.TableName(mirror.Endpoint{Path: path, Method: http.MethodGet}, rowSet)
	return `"` + mirror.Schema + `"."` + name + `"`
}
