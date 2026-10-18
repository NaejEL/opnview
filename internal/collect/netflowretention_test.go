package collect_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
)

// TestFlowRecordsArePurgedAtTheRetentionTheirSettingSays is PS-AC44 for the netflow plugin's flow records
// (PA-AC12: the setting keeps its bounds and its meaning as a plugin setting): the table is a hypertable on its
// start; with rows over several days and the plugin's retention_flow_record_seconds ending inside a day, whole
// chunks past the horizon are dropped, and the chunk holding it keeps exactly the rows at or after it.
func TestFlowRecordsArePurgedAtTheRetentionTheirSettingSays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newNetflowHarness(t)
	var column string
	if err := h.Store.DB().QueryRowContext(ctx, `SELECT column_name FROM timescaledb_information.dimensions
		WHERE hypertable_schema = 'plugin_netflow' AND hypertable_name = 'flow_record'`).Scan(&column); err != nil {
		t.Fatalf("plugin_netflow.flow_record is not a hypertable: %v", err)
	}
	if column != "flow_start_at" {
		t.Errorf("plugin_netflow.flow_record is chunked on %s", column)
	}
	now := collecttest.ReferenceEpoch()
	retention := int64(2*86400 + 3600)
	if err := h.Store.SetSetting(ctx, "netflow_retention_flow_record_seconds", fmt.Sprint(retention), now); err != nil {
		t.Fatal(err)
	}
	horizon := now - retention
	var starts []int64
	for offset := int64(-5 * 86400); offset <= 0; offset += 3600 {
		starts = append(starts, now+offset)
	}
	starts = append(starts, horizon-1, horizon)
	for index, start := range starts {
		at := time.Unix(start, 0).UTC()
		if _, err := h.Store.DB().ExecContext(ctx, `INSERT INTO plugin_netflow.flow_record (delivery, delivery_position,
			    flow_start_at, flow_end_at, collected_at, exporter_address, observation_domain, protocol, src_address,
			    dst_address, octets, packets)
			VALUES ($1, 0, $2, $2, $2, '192.0.2.1', 0, 17, '198.51.100.10', '192.0.2.80', 100, 1)`,
			fmt.Sprintf("%064x", index), at); err != nil {
			t.Fatal(err)
		}
	}
	chunks := func() int {
		return collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM show_chunks('plugin_netflow.flow_record')")
	}
	before := chunks()
	if err := h.Store.Purge(ctx, now); err != nil {
		t.Fatal(err)
	}
	if after := chunks(); after >= before {
		t.Errorf("the purge dropped no whole chunk: %d before, %d after", before, after)
	}
	older := collecttest.ScalarCount(t, h.Store,
		"SELECT count(*) FROM plugin_netflow.flow_record WHERE flow_start_at < to_timestamp($1)", horizon)
	kept := collecttest.ScalarCount(t, h.Store,
		"SELECT count(*) FROM plugin_netflow.flow_record WHERE flow_start_at >= to_timestamp($1)", horizon)
	expected := 0
	for _, start := range starts {
		if start >= horizon {
			expected++
		}
	}
	if older != 0 || kept != expected {
		t.Errorf("after the purge %d records are before the horizon and %d at or after it, want 0 and %d", older, kept,
			expected)
	}
}
