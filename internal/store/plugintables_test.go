package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
)

// exampleReadings is a plugin table with a time column, a dimension and a measure with a unit.
func exampleReadings(plugin string) plugindecl.PluginTable {
	return plugindecl.PluginTable{Plugin: plugin, Table: "readings",
		Columns: []plugindecl.PluginColumn{
			{Name: "sampled_at", Type: "integer", Role: plugindecl.RoleTime},
			{Name: "subject", Type: "text"},
			{Name: "value", Type: "real", Role: plugindecl.RoleMeasure, Unit: "byte"},
		},
		PrimaryKey: []string{"subject", "sampled_at"},
		TimeColumn: "sampled_at", RetentionSeconds: 3 * 86400}
}

// TestEveryPluginHasItsOwnSchemaAndATimedTableIsAHypertable is P-AC11's first half: two plugins
// declaring a table of the same name each get it in their own schema, and the table with a time
// column is a hypertable on it.
func TestEveryPluginHasItsOwnSchemaAndATimedTableIsAHypertable(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	for _, plugin := range []string{"example_one", "example_two"} {
		if _, err := database.EnsurePluginTable(ctx, exampleReadings(plugin)); err != nil {
			t.Fatalf("ensuring the table of %s: %v", plugin, err)
		}
	}
	untimed := plugindecl.PluginTable{Plugin: "example_one", Table: "names",
		Columns: []plugindecl.PluginColumn{{Name: "name", Type: "text"}}, PrimaryKey: []string{"name"}}
	if _, err := database.EnsurePluginTable(ctx, untimed); err != nil {
		t.Fatal(err)
	}
	var tables []string
	rows, err := database.DB().QueryContext(ctx,
		"SELECT schemaname || '.' || tablename FROM pg_tables WHERE schemaname LIKE 'plugin\\_%' ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	if strings.Join(tables, " ") != "plugin_example_one.names plugin_example_one.readings plugin_example_two.readings" {
		t.Errorf("the plugin schemas hold %v", tables)
	}
	var hypertables []string
	rows, err = database.DB().QueryContext(ctx,
		"SELECT hypertable_schema || '.' || hypertable_name FROM timescaledb_information.hypertables "+
			"WHERE hypertable_schema <> 'opnview' ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		hypertables = append(hypertables, name)
	}
	_ = rows.Close()
	if strings.Join(hypertables, " ") != "plugin_example_one.readings plugin_example_two.readings" {
		t.Errorf("the hypertables are %v", hypertables)
	}
}

// TestAUniqueKeyWithoutTheTimeColumnIsRefused: a hypertable cannot carry a unique key that omits
// its time column, so such a declaration is refused before anything is created.
func TestAUniqueKeyWithoutTheTimeColumnIsRefused(t *testing.T) {
	t.Parallel()
	table := exampleReadings("example_one")
	table.PrimaryKey = []string{"subject"}
	if err := plugindecl.ValidatePluginTable(table); !errors.Is(err, plugindecl.ErrUniqueKeyWithoutTime) {
		t.Errorf("a primary key without the time column gave %v", err)
	}
	table = exampleReadings("example_one")
	table.Indexes = []plugindecl.PluginIndex{{Columns: []string{"subject"}, Unique: true}}
	if err := plugindecl.ValidatePluginTable(table); !errors.Is(err, plugindecl.ErrUniqueKeyWithoutTime) {
		t.Errorf("a unique index without the time column gave %v", err)
	}
	table = exampleReadings("example_one")
	table.Columns[2].Unit = "Bytes Per Second"
	if err := plugindecl.ValidatePluginTable(table); err == nil {
		t.Error("a unit with capitals and spaces was accepted")
	}
}

// TestAChangedDeclarationMovesTheTableIntoTheAsideSchema is A6 on PostgreSQL: the old table moves
// into the aside schema with its rows, the new one is empty, the plugin's state is reset, the event
// names the aside table and its rows, the aside table is unreadable by the read role, and it is
// purged by its retention and dropped once empty.
func TestAChangedDeclarationMovesTheTableIntoTheAsideSchema(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	table := exampleReadings("example_one")
	if _, err := database.EnsurePluginTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	now := int64(1790000000)
	if err := database.WritePluginPass(ctx, "example_one", "example_one", plugintables.PluginRows{"readings": {
		{"sampled_at": now - 10*86400, "subject": "a", "value": 1.5},
		{"sampled_at": now - 60, "subject": "b", "value": 2.5},
	}}, map[string]string{"cursor": "42"}, now); err != nil {
		t.Fatal(err)
	}
	changed := table
	changed.Columns = append(append([]plugindecl.PluginColumn(nil), table.Columns...), plugindecl.PluginColumn{Name: "note", Type: "text",
		Nullable: true})
	event, err := database.EnsurePluginTable(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Code != plugintables.PluginTableRebuilt || event.Rows != 2 ||
		!strings.HasPrefix(event.Aside, plugindecl.AsideSchema+"."+plugintables.AsidePrefix) {
		t.Fatalf("the rebuild reported %+v", event)
	}
	if rows, err := database.PluginTableRows(ctx, changed); err != nil || rows != 0 {
		t.Errorf("the rebuilt table holds %d rows (%v)", rows, err)
	}
	if state, _ := database.PluginState(ctx, "example_one"); len(state) != 0 {
		t.Errorf("the plugin's state survived the rebuild: %v", state)
	}
	aside, err := database.AsideTables(ctx, "example_one")
	if err != nil || len(aside) != 1 || aside[0].Rows != 2 {
		t.Fatalf("the aside tables are %+v (%v)", aside, err)
	}
	if _, err := database.Reader.DB.QueryContext(ctx, "SELECT count(*) FROM "+aside[0].Name); err == nil {
		t.Error("the read role reads the aside schema")
	}
	if err := database.Purge(ctx, now); err != nil {
		t.Fatal(err)
	}
	aside, _ = database.AsideTables(ctx, "example_one")
	if len(aside) != 1 || aside[0].Rows != 1 {
		t.Errorf("after the purge the aside tables are %+v", aside)
	}
	if err := database.Purge(ctx, now+4*86400); err != nil {
		t.Fatal(err)
	}
	if aside, _ = database.AsideTables(ctx, "example_one"); len(aside) != 0 {
		t.Errorf("an emptied aside table was kept: %+v", aside)
	}
}

// TestAPluginHypertableIsPurgedByChunkToTheExactSecond is P-AC8's chunk half: rows over several
// days, a retention ending inside a day; the whole chunks past the horizon are dropped, the chunk
// holding it keeps exactly the rows at or after the horizon, and nothing younger is touched.
func TestAPluginHypertableIsPurgedByChunkToTheExactSecond(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	table := exampleReadings("example_one")
	if _, err := database.EnsurePluginTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	now := int64(1790000000)
	horizon := now - table.RetentionSeconds
	var rows []map[string]any
	for offset := int64(-6 * 86400); offset <= 0; offset += 3600 {
		rows = append(rows, map[string]any{"sampled_at": now + offset, "subject": "s", "value": float64(offset)})
	}
	rows = append(rows, map[string]any{"sampled_at": horizon - 1, "subject": "edge-before", "value": 0.0},
		map[string]any{"sampled_at": horizon, "subject": "edge-at", "value": 0.0})
	if err := database.WritePluginPass(ctx, "example_one", "example_one", plugintables.PluginRows{"readings": rows}, nil, now); err != nil {
		t.Fatal(err)
	}
	chunks := func() int {
		var count int
		if err := database.DB().QueryRowContext(ctx,
			"SELECT count(*) FROM show_chunks('plugin_example_one.readings')").Scan(&count); err != nil {
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
		"SELECT count(*) FILTER (WHERE sampled_at < ?), count(*) FILTER (WHERE sampled_at >= ?) "+
			"FROM plugin_example_one.readings", horizon, horizon).Scan(&older, &kept); err != nil {
		t.Fatal(err)
	}
	expected := 0
	for _, row := range rows {
		if row["sampled_at"].(int64) >= horizon {
			expected++
		}
	}
	if older != 0 || kept != expected {
		t.Errorf("after the purge %d rows are before the horizon and %d at or after it, want 0 and %d",
			older, kept, expected)
	}
}

// TestARemovedPluginsTablesAreListedAndKept is P-AC11's last half: a plugin no longer loaded keeps
// its schema and rows, and is listed as orphaned with its row counts.
func TestARemovedPluginsTablesAreListedAndKept(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	table := exampleReadings("example_gone")
	if _, err := database.EnsurePluginTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	if err := database.WritePluginPass(ctx, "example_gone", "example_gone", plugintables.PluginRows{"readings": {
		{"sampled_at": int64(1790000000), "subject": "a", "value": 1.0}}}, nil, 1790000000); err != nil {
		t.Fatal(err)
	}
	orphaned, err := database.OrphanedPluginTables(ctx, []string{"example_present"})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != 1 || orphaned[0].Plugin != "example_gone" || orphaned[0].Rows != 1 {
		t.Errorf("the orphaned tables are %+v", orphaned)
	}
	if orphaned, _ := database.OrphanedPluginTables(ctx, []string{"example_gone"}); len(orphaned) != 0 {
		t.Errorf("a loaded plugin's tables were listed as orphaned: %+v", orphaned)
	}
	if _, err := database.Reader.DB.QueryContext(ctx, "SELECT count(*) FROM plugin_example_gone.readings"); err != nil {
		t.Errorf("the read role cannot read a plugin's table: %v", err)
	}
}
