package purge

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The retention purge's file, embedded beside the code that runs it. sql/schema-checks.sh reads
// this same file.
//
//go:embed purge.sql
var purgeFile embed.FS

// PurgedBefore returns the furthest horizon the retention purge has applied, and whether a purge
// with a finite retention has run at all. Every flow and lookup observed before it may have been
// purged. InsertFlow and InsertDNSResolution refuse such a record inside their own insert
// statements, which is what makes the refusal hold against a purge committing at any moment;
// this read serves the tests and the screens.
func (s *PurgeStore) PurgedBefore(ctx context.Context) (int64, bool, error) {
	var before int64
	err := s.SQL.QueryRowContext(ctx, "SELECT purged_before FROM retention_purge WHERE id = 1").Scan(&before)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: reading how far back the purge has purged: %w", err)
	}
	return before, true, nil
}

// Purge applies the retention purge in one transaction, so it is all-or-nothing: every plugin's
// before_purge statements, then purge.sql -- the core's own tables, reading the horizon from
// setting.retention_seconds, where 0 means unlimited and deletes nothing: the scalar subquery yields
// NULL, so every comparison is NULL -- then the plugin tables purged by their declared retention
// (internal/store/plugindecl/plugintables.go), then every plugin's purge statements, each plugin's as
// its own role (plugintables.RunPurgeHooks; ADR 0021, step 6). hooks are the loaded plugins', in load
// order; nil purges the core's tables alone.
//
// ORDERING IS THE CALLER'S. A running service purges through collect.Collector.Purge, which never
// lets this run between a pass storing its rows and the end of that pass's derivation
// (docs/data-model.md, "Purge"). Whatever the ordering, the hours the rollups' before_purge writes a
// purged part for are selected by that plugin's next refresh.
func (s *PurgeStore) Purge(ctx context.Context, now int64, hooks []plugintables.PurgeHook) error {
	purge, err := purgeFile.ReadFile("purge.sql")
	if err != nil {
		return fmt.Errorf("store: reading the embedded purge: %w", err)
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the purge transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	// The two settings are read by the keys purge.sql and schema.sql name them by, not through
	// internal/config, whose tests open a store: the store cannot import it back.
	retention, _, err := sqldb.QueryOptionalIntText(ctx, transaction, "the retention",
		"SELECT CAST(value AS BIGINT) FROM setting WHERE key = 'retention_seconds' AND value ~ '^[0-9]+$'", nil)
	if err != nil {
		return err
	}
	liveSampleRetention, found, err := sqldb.QueryOptionalIntText(ctx, transaction, "the live sample retention",
		"SELECT CAST(value AS BIGINT) FROM setting WHERE key = 'live_sample_retention_seconds' AND value ~ '^[1-9][0-9]*$'", nil)
	if err != nil {
		return err
	}
	if !found {
		// The default schema.sql seeds for the setting (its settings block): one hour.
		liveSampleRetention = 3600
	}
	parameters := plugintables.PurgeParameters(now, retention, liveSampleRetention)
	if err := plugintables.RunPurgeHooks(ctx, transaction, hooks, true, parameters); err != nil {
		return err
	}
	for _, statement := range sqldb.SplitStatements(string(purge)) {
		upper := strings.ToUpper(strings.TrimSpace(stripComments(statement)))
		if strings.HasPrefix(upper, "BEGIN") || strings.HasPrefix(upper, "COMMIT") {
			continue
		}
		if _, err := sqldb.Exec(ctx, transaction, statement, map[string]any{"now": now}); err != nil {
			return fmt.Errorf("store: purging: %w\nstatement: %s", err, sqldb.FirstLine(statement))
		}
	}
	if err := plugintables.On(s.Handle).PurgePluginTables(ctx, transaction, now); err != nil {
		return err
	}
	if err := plugintables.RunPurgeHooks(ctx, transaction, hooks, false, parameters); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the purge: %w", err)
	}
	return nil
}

// stripComments removes the -- comment lines of a statement.
func stripComments(statement string) string {
	var kept []string
	for _, line := range strings.Split(statement, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
