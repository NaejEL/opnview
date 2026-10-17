//go:build njreplay || njrecord

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

	"github.com/NaejEL/opnview/internal/collect/collecttest"
)

// The recording of the Go join that PA10 of specs/SPEC-backend-completion-plugins.md (amendment of 17
// October 2026) requires before the join leaves Go: every stored decision and figure, written as one
// JSON object per row, with every flow id replaced by the digest of the leg it names, so that another
// implementation that stores the same legs under other ids can be compared with it value for value.
// Nothing here runs in the project commands: njrecord_test.go and njreplay_test.go are run by hand.

// joinDumpPeriods are the aggregate periods the recording covers.
var joinDumpPeriods = []string{"1h", "24h", "7d", "30d"}

// joinDumpFamilies are the aggregate families whose rows carry a NetFlow figure.
var joinDumpFamilies = []string{"volume", "owner_volume", "client_volume", "peer_volume", "domain_volume", "rule_volume"}

// joinDumpQueries are the recorded relations, by name, each as a query answering one JSON text per row.
func joinDumpQueries() []struct{ name, query string } {
	queries := []struct{ name, query string }{
		{"flow_record", `SELECT (to_jsonb(r) - 'provider_id' - 'joined_flow_id' - 'copy_flow_id' ||
		                         jsonb_build_object('joined_leg', j.log_digest, 'copy_leg', c.log_digest))::text
		                 FROM flow_record r
		                 LEFT JOIN flow j ON j.id = r.joined_flow_id
		                 LEFT JOIN flow c ON c.id = r.copy_flow_id`},
		{"connection_measure", `SELECT (to_jsonb(v) - 'connection_flow_id' - 'measure' ||
		                                        jsonb_build_object('connection_leg', f.log_digest))::text
		                                FROM connection_measure v
		                                LEFT JOIN flow f ON f.id = v.connection_flow_id`},
	}
	for _, period := range joinDumpPeriods {
		queries = append(queries, struct{ name, query string }{"unmatched_record_aggregate_" + period,
			"SELECT to_jsonb(a)::text FROM unmatched_record_aggregate_" + period + " a"})
	}
	for _, family := range joinDumpFamilies {
		for _, period := range joinDumpPeriods {
			table := family + "_aggregate_" + period
			queries = append(queries, struct{ name, query string }{table,
				"SELECT (to_jsonb(a) - 'computed_at')::text FROM " + table + " a"})
		}
	}
	return queries
}

// dumpJoin writes every recorded relation of a harness's database to a file: a JSON object whose keys
// are the relations, each an array of rows, one row per line, the rows sorted by their text.
func dumpJoin(t *testing.T, h *collecttest.NetflowHarness, path, provenance string, extra map[string][]string) {
	t.Helper()
	sections := map[string][]string{}
	for name, rows := range extra {
		sections[name] = rows
	}
	for _, relation := range joinDumpQueries() {
		rows, err := h.Store.DB().QueryContext(context.Background(), relation.query)
		if err != nil {
			t.Fatalf("%s: %v", relation.name, err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		sort.Strings(lines)
		sections[relation.name] = lines
	}
	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	out.WriteString("{\n")
	if provenance != "" {
		statement, err := json.Marshal(map[string]string{"statement": provenance})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%q: %s,\n", "__provenance__", statement)
	}
	for index, name := range names {
		fmt.Fprintf(&out, "%q: [", name)
		for row, line := range sections[name] {
			if row > 0 {
				out.WriteString(",")
			}
			out.WriteString("\n  " + line)
		}
		if len(sections[name]) > 0 {
			out.WriteString("\n")
		}
		out.WriteString("]")
		if index < len(names)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
