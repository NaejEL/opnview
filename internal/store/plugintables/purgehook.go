package plugintables

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// THE PURGE IS THE PLUGINS' OWN (ADR 0021, step 6). A plugin's manifest names, among the statements
// its statement files hold, those run at the retention purge: `before_purge`, run before any row of
// any plugin is purged -- the rollups' purged part, read from the flows about to go -- and `purge`,
// deleting the plugin's own rows past the horizon. The core's purge.sql keeps the core's tables
// alone; it names no plugin's table.
//
// Every statement runs as its plugin, by role, inside the one purge transaction, bound with the same
// three parameters (PurgeParameters). The phases run across the plugins in load order: every
// plugin's before_purge, then the core's statements and the tables purged by their declared
// retention, then every plugin's purge. A required plugin loads before the plugins requiring it, so
// a plugin that deletes rows another plugin's rows point at runs first, and the clients plugin,
// which loads after every table that names a client, removes the clients nothing names any more.

// PurgeHook is what one plugin does at the retention purge.
type PurgeHook struct {
	// Plugin owns the statements, and is the role they run as.
	Plugin string
	// Before are the statements run before any row is purged.
	Before []NamedStatement
	// Purge are the statements purging the plugin's own rows.
	Purge []NamedStatement
}

// NamedStatement is one statement of a plugin's statement files, by the name the file gives it.
type NamedStatement struct {
	Name string
	Text string
}

// PurgeParameters are what every purge statement is bound with: :now, the instant the purge runs;
// :retention, the installation's retention_seconds, 0 for unlimited; :horizon, now - retention, or
// null when the retention is unlimited, so `WHERE observed_at < CAST(:horizon AS BIGINT)` deletes
// nothing then, as a comparison with null is null; :live_sample_retention, how long the fine samples
// of a live pass are kept (the core's live_sample_retention_seconds, or its default).
func PurgeParameters(now, retention, liveSampleRetention int64) map[string]any {
	parameters := map[string]any{"now": now, "retention": retention, "horizon": nil,
		"live_sample_retention": liveSampleRetention}
	if retention > 0 {
		parameters["horizon"] = now - retention
	}
	return parameters
}

// RunPurgeHooks runs one phase of the hooks, in order: the before statements when before is set,
// the purge statements otherwise, each plugin's as that plugin's role.
func RunPurgeHooks(ctx context.Context, transaction *sql.Tx, hooks []PurgeHook, before bool, parameters map[string]any) error {
	for _, hook := range hooks {
		statements := hook.Purge
		if before {
			statements = hook.Before
		}
		if len(statements) == 0 {
			continue
		}
		if err := sqldb.TakePluginRole(ctx, transaction, hook.Plugin); err != nil {
			return err
		}
		for _, statement := range statements {
			if _, err := sqldb.ExecText(ctx, transaction, statement.Name, statement.Text, parameters); err != nil {
				return fmt.Errorf("store: purging as the plugin %s: %w", hook.Plugin, err)
			}
		}
		if err := sqldb.GivePluginRoleBack(ctx, transaction); err != nil {
			return err
		}
	}
	return nil
}
