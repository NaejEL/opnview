package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// purgingManifest is a plugin owning a table of readings it purges itself (ADR 0021, step 6): a statement
// run before any row is purged counts what is about to go, and one deletes the rows past the horizon.
const purgingManifest = `{"api_version": 3, "name": "decl_purging", "version": "1.0.0", "display_name": "Purging",
  "tables": [{"name": "reading", "columns": [{"name": "id", "type": "integer", "identity": true},
                                             {"name": "observed_at", "type": "integer"}],
              "primary_key": ["id"]},
             {"name": "purged", "columns": [{"name": "purged_at", "type": "integer"}, {"name": "rows", "type": "integer"}],
              "primary_key": ["purged_at"]}],
  "statements": ["purge.statements.sql"],
  "before_purge": ["count_purged"], "purge": ["purge_readings"],
  "widgets": ["decl_purging"]}`

const purgingStatements = `-- statement: count_purged
INSERT INTO plugin_decl_purging.purged (purged_at, rows)
SELECT CAST(:now AS BIGINT), count(*) FROM plugin_decl_purging.reading WHERE observed_at < CAST(:horizon AS BIGINT);

-- statement: purge_readings
DELETE FROM plugin_decl_purging.reading WHERE observed_at < CAST(:horizon AS BIGINT);
`

// TestAPluginPurgesItsOwnRowsAtTheRetentionPurge runs the purge with the loaded plugins' hooks: the
// before_purge statement sees the rows about to go, the purge statement removes them, and an unlimited
// retention deletes nothing.
func TestAPluginPurgesItsOwnRowsAtTheRetentionPurge(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	dataDir := t.TempDir()
	widget := `{"manifest_version": 1, "type": "decl_purging", "shape": "table", "windowed": false, "parameters": [],
	  "queries": ["q.statements.sql"], "script": "w.js"}`
	writePlugin(t, dataDir, "decl_purging", purgingManifest, map[string]string{
		"purge.statements.sql":                  purgingStatements,
		"widgets/decl_purging/manifest.json":    widget,
		"widgets/decl_purging/q.statements.sql": "-- statement: widget_decl_purging\nSELECT 1 AS one",
		"widgets/decl_purging/w.js":             "exports.widget = {serve: function (host) { return {data: {}, has_rows: false}; }};",
	})
	ctx := context.Background()
	loaded, err := plugin.Load(ctx, plugin.Options{DataDir: dataDir, Store: database})
	if err != nil {
		t.Fatal(err)
	}
	if refusal := refusalOf(loaded, "decl_purging"); refusal != nil {
		t.Fatalf("refused: %v", refusal)
	}
	hooks := loaded.Set.PurgeHooks()
	found := false
	for _, hook := range hooks {
		if hook.Plugin == "decl_purging" {
			found = len(hook.Before) == 1 && len(hook.Purge) == 1
		}
	}
	if !found {
		t.Fatalf("the plugin's purge hook is missing or incomplete among %d hooks", len(hooks))
	}
	const now = int64(1_790_000_000)
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO plugin_decl_purging.reading (observed_at) VALUES ($1), ($2), ($3)",
		now-3*86400, now-2*86400, now-60); err != nil {
		t.Fatal(err)
	}
	// Unlimited retention: nothing goes, and the count before is of nothing.
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO setting (key, value, updated_at) VALUES ('retention_seconds', '0', $1) "+
		"ON CONFLICT (key) DO UPDATE SET value = excluded.value", now); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(ctx, now, hooks); err != nil {
		t.Fatalf("the purge under an unlimited retention: %v", err)
	}
	if left := storetest.QueryInt(t, database, "SELECT count(*) FROM plugin_decl_purging.reading"); left != 3 {
		t.Errorf("an unlimited retention purged readings: %d left", left)
	}
	if counted := storetest.QueryInt(t, database, "SELECT coalesce(sum(rows), 0) FROM plugin_decl_purging.purged"); counted != 0 {
		t.Errorf("an unlimited retention counted %d rows about to go", counted)
	}
	// A day of retention: the two older readings go, counted first.
	if _, err := database.SQL.ExecContext(ctx, "UPDATE setting SET value = '86400' WHERE key = 'retention_seconds'"); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(ctx, now+1, hooks); err != nil {
		t.Fatalf("the purge: %v", err)
	}
	if left := storetest.QueryInt(t, database, "SELECT count(*) FROM plugin_decl_purging.reading"); left != 1 {
		t.Errorf("%d readings are left, not 1", left)
	}
	if counted := storetest.QueryInt(t, database, "SELECT rows FROM plugin_decl_purging.purged WHERE purged_at = $1", now+1); counted != 2 {
		t.Errorf("the statement before the purge counted %d rows about to go, not 2", counted)
	}
}

// TestAPurgeStatementMustBeHeld is the refusal: a purge naming a statement the files do not hold, or one twice.
func TestAPurgeStatementMustBeHeld(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]string{
		"a statement no file holds": strings.Replace(purgingManifest, `"purge": ["purge_readings"]`, `"purge": ["purge_nothing"]`, 1),
		"a statement named twice":   strings.Replace(purgingManifest, `"purge": ["purge_readings"]`, `"purge": ["count_purged"]`, 1),
	} {
		dataDir := t.TempDir()
		widget := `{"manifest_version": 1, "type": "decl_purging", "shape": "table", "windowed": false, "parameters": [],
		  "queries": ["q.statements.sql"], "script": "w.js"}`
		writePlugin(t, dataDir, "decl_purging", change, map[string]string{
			"purge.statements.sql":                  purgingStatements,
			"widgets/decl_purging/manifest.json":    widget,
			"widgets/decl_purging/q.statements.sql": "-- statement: widget_decl_purging\nSELECT 1 AS one",
			"widgets/decl_purging/w.js":             "exports.widget = {serve: function (host) { return {data: {}, has_rows: false}; }};",
		})
		loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
		if err != nil {
			t.Fatal(err)
		}
		refusal := refusalOf(loaded, "decl_purging")
		if refusal == nil || refusal.Code != contract.RefusalPurgeInvalid {
			t.Errorf("%s: loaded, or refused otherwise: %+v", name, refusal)
		}
	}
}
