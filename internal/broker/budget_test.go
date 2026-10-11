//go:build brokerbudget

package broker_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The broker budgets of BR-AC17, each written once. `pre-deployment` runs this file alone, without
// the race detector, and prints each median beside its limit.
const (
	// budgetServeFresh bounds the host-call time of a fresh 1 000-row datum served from the mirror
	// to a reader that reads ten fields of every row.
	budgetServeFresh = 25 * time.Millisecond
	// budgetOverhead bounds the broker's own time on a call to a zero-latency fake answering
	// 1 000 rows: observation, inference and the mirror write. It is the amendment's 50 ms, which the
	// maintainer kept on 12 October 2026 when he decided that the mirror stamps the newest generation
	// on the datum instead of rewriting the rows an answer holds unchanged (mirror/incremental.go):
	// the write that rewrote every row was measured at a median of 51 to 55 ms, the write that keeps
	// them at 18 to 19 ms, on the development instance.
	budgetOverhead = 50 * time.Millisecond
)

// budgetRuns is how many runs a median is taken over.
const budgetRuns = 15

// thousandRows is an answer of 1 000 rows of ten fields.
func thousandRows() string {
	rows := make([]string, 0, 1000)
	for index := 0; index < 1000; index++ {
		rows = append(rows, fmt.Sprintf(`{"id": "%d", "f1": "a%d", "f2": %d, "f3": "c", "f4": %d, "f5": "e%d",`+
			` "f6": true, "f7": "g", "f8": %d.5, "f9": "i%d"}`, index, index%7, index, index%13, index%3, index, index%11))
	}
	return `{"total": 1000, "rows": [` + strings.Join(rows, ",") + `]}`
}

var budgetFields = []string{"id", "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9"}

func median(durations []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// TestBrokerBudgets is BR-AC17's broker half.
func TestBrokerBudgets(t *testing.T) {
	w := newWorld(t)
	body := thousandRows()
	w.fake.answer(jsonAnswer(body))
	ep := endpoint(t, http.MethodGet, "/api/example/budget/search", opnsense.NoBody)
	requester := broker.Requester{Name: "example_reader", Version: "1"}
	w.call(requester.Name, ep, opnsense.RequestOptions{}, time.Hour, budgetFields...)

	// Serving fresh from the mirror, and reading ten fields of every row.
	var served []time.Duration
	for run := 0; run < budgetRuns; run++ {
		started := time.Now()
		answered, err := w.broker.Call(context.Background(), broker.Request{Requester: requester, Endpoint: ep,
			MaxAge: time.Hour})
		if err != nil || answered.ServedFrom != broker.ServedFromMirror {
			t.Fatalf("the datum was not served from the mirror: %v", err)
		}
		if _, _, notHeld := readRows(answered, budgetFields); notHeld {
			t.Fatal("the mirror does not hold the fields read")
		}
		served = append(served, time.Since(started))
	}

	// The broker's overhead: a fresh call, less the client's own time on the same answer.
	client := opnsense.NewClient(opnsense.Credentials{BaseURL: w.fake.server.URL, APIKey: w.key, APISecret: w.secret},
		http.DefaultTransport, 5*time.Second)
	var overhead []time.Duration
	for run := 0; run < budgetRuns; run++ {
		started := time.Now()
		if _, err := client.Call(context.Background(), ep, opnsense.RequestOptions{}); err != nil {
			t.Fatal(err)
		}
		direct := time.Since(started)
		started = time.Now()
		answered, err := w.broker.Call(context.Background(), broker.Request{Requester: requester, Endpoint: ep,
			MaxAge: time.Hour, Fresh: true})
		if err != nil || answered.ServedFrom != broker.ServedFromFirewall {
			t.Fatalf("the fresh call was not made: %v", err)
		}
		w.broker.Done(context.Background(), answered, requester, time.Hour, answer.NewReads())
		overhead = append(overhead, time.Since(started)-direct)
	}

	for _, line := range []struct {
		name       string
		got, limit time.Duration
	}{{"broker serve fresh 1000 rows x 10 fields", median(served), budgetServeFresh},
		{"broker overhead on a 1000-row call", median(overhead), budgetOverhead}} {
		fmt.Printf("budget: %s: median %.1f ms (limit %.0f ms)\n", line.name, float64(line.got.Microseconds())/1000,
			float64(line.limit.Microseconds())/1000)
		if line.got > line.limit {
			t.Errorf("%s: median %v over the budget of %v", line.name, line.got, line.limit)
		}
	}
}
