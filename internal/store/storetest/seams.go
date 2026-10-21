package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/aggregates"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The reads only tests make of a store: what a test asserts on and the product never asks. They live here,
// in the test-support package, rather than as methods of the store the binary links (CL-AC3 of
// cleanup-amendment.md, restated by PA-AC24).

// CountRows counts the rows of a table. The table name cannot be a bound parameter; every caller passes a
// literal, and an unknown name fails rather than reading anything.
func CountRows(ctx context.Context, database *store.Store, table string) (int, error) {
	var count int
	if err := database.DB().QueryRowContext(ctx, "SELECT count(*) FROM \""+table+"\"").Scan(&count); err != nil {
		return 0, fmt.Errorf("storetest: counting %s: %w", table, err)
	}
	return count, nil
}

// AvailabilityOutcomeOf reads the outcome a provider's availability row holds.
func AvailabilityOutcomeOf(ctx context.Context, database *store.Store, providerID int64) (vocab.AvailabilityOutcome,
	error) {
	var outcome string
	if err := database.DB().QueryRowContext(ctx,
		"SELECT outcome FROM source_availability WHERE provider_id = $1", providerID).Scan(&outcome); err != nil {
		return "", fmt.Errorf("storetest: reading the availability outcome of provider %d: %w", providerID, err)
	}
	return vocab.AvailabilityOutcome(outcome), nil
}

// InsertDNSResolution stores one lookup through the store's own write, as the resolver collector does,
// without the report of whether it was new.
func InsertDNSResolution(ctx context.Context, database *store.Store, lookup vocab.DNSResolution) error {
	_, err := database.StoreDNSResolution(ctx, lookup)
	return err
}

// ActiveProviderID returns the active provider of an exclusive kind, and whether there is one. It refuses a
// kind that admits several active providers: asking for THE active one would read one source where there
// may be several.
func ActiveProviderID(ctx context.Context, database *store.Store, kind string) (int64, bool, error) {
	var concurrent bool
	if err := database.DB().QueryRowContext(ctx, "SELECT concurrent FROM provider_kind WHERE kind = $1", kind).
		Scan(&concurrent); err != nil {
		return 0, false, fmt.Errorf("storetest: reading whether %s is concurrent: %w", kind, err)
	}
	if concurrent {
		return 0, false, fmt.Errorf("storetest: the %s kind admits several active providers, so asking for THE "+
			"active one would read one source where there may be several", kind)
	}
	var id int64
	err := database.DB().QueryRowContext(ctx, "SELECT id FROM provider WHERE kind = $1 AND is_active", kind).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("storetest: looking up the active %s provider: %w", kind, err)
	}
	return id, true, nil
}

// OrphanedPluginTables returns the recorded tables of every plugin not among the loaded ones, with their row
// counts: what a plugin removed leaves behind, untouched, for the operator to drop.
func OrphanedPluginTables(ctx context.Context, database *store.Store, loaded []string) ([]plugintables.OrphanedTable,
	error) {
	rows, err := database.DB().QueryContext(ctx, "SELECT plugin, table_name FROM plugin_table ORDER BY plugin, table_name")
	if err != nil {
		return nil, fmt.Errorf("storetest: listing the plugin tables: %w", err)
	}
	var orphaned []plugintables.OrphanedTable
	for rows.Next() {
		var table plugintables.OrphanedTable
		if err := rows.Scan(&table.Plugin, &table.Table); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !plugindecl.Contains(loaded, table.Plugin) {
			orphaned = append(orphaned, table)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range orphaned {
		var exists bool
		if err := database.DB().QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = $2)",
			plugindecl.PluginSchema(orphaned[index].Plugin), orphaned[index].Table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		if err := database.DB().QueryRowContext(ctx, "SELECT count(*) FROM "+
			plugindecl.PluginTable{Plugin: orphaned[index].Plugin, Table: orphaned[index].Table}.Quoted()).
			Scan(&orphaned[index].Rows); err != nil {
			return nil, err
		}
	}
	return orphaned, nil
}

// ReadStatementCount reports how many distinct statement texts the read connections have run since the
// store opened. A text is translated once and nothing is prepared on the server, so a second call of a read
// function adds nothing to it (AC31 of 5B, restated by P-AC13).
func ReadStatementCount(database *store.Store) int {
	if database.ReadStore.Reader == nil {
		return 0
	}
	return database.ReadStore.Reader.RunCount()
}

// ReadStatementsOutsideTheFiles returns the text of every statement the read connections have run that is
// not a registered named statement, nor one of their period expansions. sql/schema-checks.sh plans every
// named statement at both seed sizes and nothing else, so a text returned here is one the API runs unplanned
// (AC17).
func ReadStatementsOutsideTheFiles(database *store.Store) ([]string, error) {
	if database.ReadStore.Reader == nil {
		return nil, nil
	}
	names, err := sqldb.StatementNames()
	if err != nil {
		return nil, err
	}
	translated := func(text string) string {
		translation, err := sqldb.Translate(text)
		if err != nil {
			return text
		}
		return translation.Text
	}
	planned := map[string]bool{}
	for _, name := range names {
		text, err := sqldb.Statement(name)
		if err != nil {
			return nil, err
		}
		planned[text] = true
		planned[translated(text)] = true
		if IsPeriodTemplate(name) {
			for _, period := range vocab.Periods() {
				expanded, err := sqldb.PeriodStatement(name, period)
				if err != nil {
					return nil, err
				}
				planned[expanded] = true
				planned[translated(expanded)] = true
			}
		}
	}
	var outside []string
	for _, text := range database.ReadStore.Reader.RunTexts() {
		if !planned[text] {
			outside = append(outside, text)
		}
	}
	return outside, nil
}

// RefreshAggregates applies the refresh contract with nothing ingesting concurrently: the refresh's
// watermark is its own instant (store.RefreshAggregatesAt).
func RefreshAggregates(ctx context.Context, database *store.Store, since, now int64, forced []int64) (
	aggregates.Refresh, error) {
	return database.RefreshAggregatesAt(ctx, since, now, now, forced)
}

// IsPeriodTemplate reports whether a named statement is a period template: one PeriodStatement expands.
func IsPeriodTemplate(name string) bool {
	_, err := sqldb.PeriodStatement(name, vocab.PeriodHour)
	return err == nil
}
