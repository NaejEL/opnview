//go:build brokerbudget

package scripthost

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// budgetObservedRatio is BR-AC17's third budget, written once: a pass reading 1 000 rows of ten
// fields through the observed value takes at most this many times the same pass over a plain
// object.
const budgetObservedRatio = 2.0

// TestObservedReadBudget is BR-AC17's plugin half. `pre-deployment` runs it without the race
// detector and prints the ratio beside its limit.
func TestObservedReadBudget(t *testing.T) {
	rows := make([]string, 0, 1000)
	for index := 0; index < 1000; index++ {
		rows = append(rows, fmt.Sprintf(`{"id": "%d", "f1": "a", "f2": %d, "f3": "c", "f4": 4, "f5": "e",`+
			` "f6": true, "f7": "g", "f8": 8.5, "f9": "i"}`, index, index))
	}
	body := []byte(`{"rows": [` + strings.Join(rows, ",") + `]}`)
	compiled, err := Compile("example", "example.js", `exports.example = { pass: function (host, mode) {
			var answer = host.call("rows", {});
			var rows = mode === "plain" ? JSON.parse(answer.body).rows : answer.value.rows;
			var total = 0;
			for (var i = 0; i < rows.length; i++) {
				var row = rows[i];
				total += String(row.id).length + row.f1.length + row.f2 + row.f3.length + row.f4 + row.f5.length +
					(row.f6 ? 1 : 0) + row.f7.length + row.f8 + row.f9.length;
			}
			return { total: total };
		} };`, map[string]opnsense.Endpoint{"rows": {Path: "/api/example/rows/search", Method: "GET"}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session := stubSession{body: body, now: 1750000000}
	measure := func(mode string) time.Duration {
		var runs []time.Duration
		for run := 0; run < 15; run++ {
			started := time.Now()
			if _, _, failed := compiled.Call(context.Background(), session, "example", "pass", mode); failed != nil {
				t.Fatalf("the %s pass failed: %+v", mode, failed)
			}
			runs = append(runs, time.Since(started))
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		return runs[len(runs)/2]
	}
	plain, observed := measure("plain"), measure("observed")
	ratio := float64(observed) / float64(plain)
	fmt.Printf("budget: observed read of 1000 rows x 10 fields: %.2fx the plain pass (%.1f ms against %.1f ms; limit %.1fx)\n",
		ratio, float64(observed.Microseconds())/1000, float64(plain.Microseconds())/1000, budgetObservedRatio)
	if ratio > budgetObservedRatio {
		t.Errorf("the observed pass takes %.2f times the plain one, over the budget of %.1f", ratio, budgetObservedRatio)
	}
}
