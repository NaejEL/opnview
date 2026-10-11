package store

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// pushOutputTable is a push output table holding every type of the vocabulary.
func pushOutputTable(plugin string) PluginTable {
	return PluginTable{Plugin: plugin, Table: "lines",
		Columns: []PluginColumn{
			{Name: "logged_at", Type: "instant", Role: RoleTime},
			{Name: "client", Type: "ip"},
			{Name: "station", Type: "mac", Nullable: true},
			{Name: "cached", Type: "boolean", Nullable: true},
			{Name: "count", Type: "integer", Role: RoleMeasure},
			{Name: "seconds", Type: "real", Nullable: true, Role: RoleMeasure, Unit: "second"},
			{Name: "name", Type: "text", Nullable: true},
		},
		TimeColumn: "logged_at", RetentionSeconds: 86400}
}

// TestAPushOutputTableMapsEveryTypeAndIsAHypertableOnItsInstant is PS-AC10's second half: the table
// is created in the plugin's schema as a hypertable on its instant column, each vocabulary type is
// the PostgreSQL type PS4 maps it to, and no column is JSON.
func TestAPushOutputTableMapsEveryTypeAndIsAHypertableOnItsInstant(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	table := pushOutputTable("example_push")
	if _, err := database.EnsurePluginTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	rows, err := database.DB().QueryContext(ctx,
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_schema = 'plugin_example_push' AND table_name = 'lines' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatal(err)
		}
		got[name] = dataType
	}
	_ = rows.Close()
	want := map[string]string{"logged_at": "timestamp with time zone", "client": "inet", "station": "macaddr",
		"cached": "boolean", "count": "bigint", "seconds": "double precision", "name": "text"}
	for name, dataType := range want {
		if got[name] != dataType {
			t.Errorf("%s is %q, want %q", name, got[name], dataType)
		}
	}
	for name, dataType := range got {
		if strings.Contains(dataType, "json") {
			t.Errorf("%s is %s", name, dataType)
		}
	}
	var dimension string
	if err := database.DB().QueryRowContext(ctx,
		`SELECT column_name FROM timescaledb_information.dimensions
		 WHERE hypertable_schema = 'plugin_example_push' AND hypertable_name = 'lines'`).Scan(&dimension); err != nil {
		t.Fatalf("the table is not a hypertable: %v", err)
	}
	if dimension != "logged_at" {
		t.Errorf("the hypertable is chunked on %s", dimension)
	}
}

// TestPushRowsAreBatchedTypedAndPurgedByTheirInstant: a batch of rows is one transaction of few
// statements, each value is stored as its type, and the purge removes rows by the instant column.
func TestPushRowsAreBatchedTypedAndPurgedByTheirInstant(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	table := pushOutputTable("example_rows")
	if _, err := database.EnsurePluginTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	mac, _ := net.ParseMAC("00:00:5e:00:53:01")
	var rows []map[string]any
	for index := range 2500 {
		rows = append(rows, map[string]any{"logged_at": now.Add(-time.Duration(index) * time.Minute),
			"client": netip.MustParseAddr("192.0.2.10"), "station": mac, "cached": index%2 == 0,
			"count": int64(index), "seconds": 0.25, "name": "example.org."})
	}
	statements, err := database.InsertPluginRows(ctx, table, rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := (2500*len(table.Columns) + maxBoundParameters - 1) / maxBoundParameters; statements != want {
		t.Errorf("2500 rows took %d statements, want %d", statements, want)
	}
	var client, station string
	var cached bool
	if err := database.DB().QueryRowContext(ctx,
		`SELECT host(client), CAST(station AS text), cached FROM plugin_example_rows.lines WHERE count = 0`).
		Scan(&client, &station, &cached); err != nil {
		t.Fatal(err)
	}
	if client != "192.0.2.10" || station != "00:00:5e:00:53:01" || !cached {
		t.Errorf("read back %s %s %v", client, station, cached)
	}
	if _, err := database.DB().ExecContext(ctx,
		"UPDATE setting SET value = '0' WHERE key = 'retention_seconds'"); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(ctx, now.Unix()); err != nil {
		t.Fatal(err)
	}
	var kept int64
	if err := database.DB().QueryRowContext(ctx, "SELECT count(*) FROM plugin_example_rows.lines").Scan(&kept); err != nil {
		t.Fatal(err)
	}
	// A day's retention keeps the rows of the last 24 hours, the horizon itself included: 1441.
	if kept != 1441 {
		t.Errorf("the purge kept %d rows, want 1441", kept)
	}
}

// TestAPushGapSeenAgainIsExtended: a silence re-recorded as it lengthens is one gap, not many.
func TestAPushGapSeenAgainIsExtended(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, _, err := database.EnsureProvider(ctx, "example_push_kind", "example", "Example", 1_790_467_200)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []int64{1_790_467_230, 1_790_467_260, 1_790_467_250} {
		if err := database.RecordPushGap(ctx, providerID, GapPushNotAlive, 1_790_467_200, to, "silent",
			1_790_467_300); err != nil {
			t.Fatal(err)
		}
	}
	var count, end int64
	if err := database.DB().QueryRowContext(ctx,
		"SELECT count(*), max(interval_end_at) FROM collection_gap WHERE provider_id = ?", providerID).
		Scan(&count, &end); err != nil {
		t.Fatal(err)
	}
	if count != 1 || end != 1_790_467_260 {
		t.Errorf("%d gaps ending at %d", count, end)
	}
}

// TestSourcePeriodsNeverOverlap is part of PS-AC14: each boundary closes one period and opens the
// next one second later.
func TestSourcePeriodsNeverOverlap(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, _, err := database.EnsureProvider(ctx, "example_source_period_kind", "example", "Example", 1_790_467_200)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.OpenSourcePeriod(ctx, providerID, PathPush, 1_790_467_100); err != nil {
		t.Fatal(err)
	}
	if err := database.OpenSourcePeriod(ctx, providerID, PathPull, 1_790_467_150); err != nil {
		t.Fatal(err)
	}
	periods, err := database.SourcePeriods(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 3 || periods[0].Path != PathPull || periods[1].Path != PathPush || periods[2].Path != PathPull {
		t.Fatalf("periods %+v", periods)
	}
	for index := 1; index < len(periods); index++ {
		if periods[index-1].ToAt == nil || *periods[index-1].ToAt+1 != periods[index].FromAt {
			t.Errorf("period %d does not end one second before the next begins: %+v", index-1, periods)
		}
	}
	if periods[2].ToAt != nil {
		t.Error("the last period is not open")
	}
}
