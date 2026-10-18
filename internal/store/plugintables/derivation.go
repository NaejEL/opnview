package plugintables

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
)

// What one run of a derivation writes (PA4 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): rows of its plugin's own tables, counters added to its counter
// tables, a declared measure's per-connection output (schema.sql, connection_measure) and its window
// intervals (measure_interval). ALL OF IT IN ONE TRANSACTION, so a run that fails anywhere writes nothing.
//
// A RUN THAT WRITES WHAT IS ALREADY HELD CHANGES NOTHING. A row replaces the held row of its key only when a
// value differs, a measure row its held row only when a figure, its state or its markers differ, and a
// measure's intervals are rewritten only when they differ; so running a derivation twice over the same
// inputs writes nothing the second time, and the rows reported written are the rows changed. A counter is
// the exception by nature -- it adds -- which is why the host hands a run only what changed since the last.

// ConnectionMeasureRow is one direction of one connection in a measure (schema.sql, connection_measure).
type ConnectionMeasureRow struct {
	ConnectionFlowID  int64
	Direction         string
	ConnectionStartAt int64
	State             string
	Bytes             int64
	Packets           int64
	Records           int64
	// Markers are the direction's markers, as the bits of the measure's declared order.
	Markers int64
	// DueAt is the instant from which the row may change by time alone; nil is final.
	DueAt *int64
	// Remove deletes the held row of the connection and direction instead: the derivation gives it no figure
	// any more -- it started under no configuration the derivation can read.
	Remove bool
}

// MeasureInterval is one window interval of a measure (schema.sql, measure_interval); EndsAt nil is open.
type MeasureInterval struct {
	StartsAt int64
	EndsAt   *int64
	State    string
}

// DerivationWrite is everything one run writes.
type DerivationWrite struct {
	// Plugin owns every table written.
	Plugin string
	// Rows are rows of the plugin's tables, by the table's own name: ordinary tables and counter tables
	// alike, each written as its declaration says.
	Rows map[string][]map[string]any
	// Measures are per-connection rows, by measure name.
	Measures map[string][]ConnectionMeasureRow
	// Intervals are, by measure name, the whole set of the measure's window intervals for IntervalProvider.
	Intervals        map[string][]MeasureInterval
	IntervalProvider int64
	// ComputedAt stamps the measure rows written.
	ComputedAt int64
}

// DerivationWritten is what a run changed: how many rows, the start instants of the connections whose
// measure rows changed -- the aggregate slots to refresh -- and how many statements it took.
type DerivationWritten struct {
	Rows       int64
	Instants   []int64
	Statements int
}

// WriteDerivation writes one run's outputs in one transaction.
func (s *PluginTableStore) WriteDerivation(ctx context.Context, write DerivationWrite) (DerivationWritten, error) {
	var written DerivationWritten
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return written, fmt.Errorf("store: writing a derivation of the plugin %s: %w", write.Plugin, err)
	}
	defer func() { _ = transaction.Rollback() }()
	for _, name := range sortedKeys(write.Rows) {
		rows := write.Rows[name]
		if len(rows) == 0 {
			continue
		}
		table, found := s.LookupPluginTable(plugindecl.PluginTableSQLName(write.Plugin, name))
		if !found {
			return DerivationWritten{}, fmt.Errorf("store: the plugin %s wrote into %q, which it does not own", write.Plugin, name)
		}
		if table.Counters {
			rows = plugindecl.SumPerKey(table, rows)
		} else {
			rows = plugindecl.LastPerKey(table, rows)
		}
		perStatement := max(1, maxBoundParameters/len(table.Columns))
		for start := 0; start < len(rows); start += perStatement {
			statement, arguments, err := table.DerivationInsert(rows[start:min(len(rows), start+perStatement)])
			if err != nil {
				return DerivationWritten{}, err
			}
			result, err := transaction.ExecContext(ctx, statement, arguments...)
			if err != nil {
				return DerivationWritten{}, fmt.Errorf("store: writing into %s: %w", table.SQLName(), err)
			}
			changed, _ := result.RowsAffected()
			written.Rows += changed
			written.Statements++
			if table.Counters {
				// A counter whose figures all came back to zero -- a record counted and then taken out -- holds
				// nothing, and is removed.
				statement, arguments, err := table.CounterEmptyDelete(rows[start:min(len(rows), start+perStatement)])
				if err != nil {
					return DerivationWritten{}, err
				}
				if _, err := transaction.ExecContext(ctx, statement, arguments...); err != nil {
					return DerivationWritten{}, fmt.Errorf("store: removing the empty counters of %s: %w", table.SQLName(), err)
				}
				written.Statements++
			}
		}
	}
	for _, measure := range sortedKeys(write.Measures) {
		if err := writeMeasureRows(ctx, transaction, measure, write.Measures[measure], write.ComputedAt, &written); err != nil {
			return DerivationWritten{}, err
		}
	}
	for _, measure := range sortedKeys(write.Intervals) {
		if err := writeMeasureIntervals(ctx, transaction, measure, write.IntervalProvider, write.Intervals[measure],
			&written); err != nil {
			return DerivationWritten{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return DerivationWritten{}, fmt.Errorf("store: committing a derivation of the plugin %s: %w", write.Plugin, err)
	}
	sort.Slice(written.Instants, func(i, j int) bool { return written.Instants[i] < written.Instants[j] })
	written.Instants = slices.Compact(written.Instants)
	return written, nil
}

// writeMeasureRows upserts a measure's rows in one statement, replacing a held row only when it differs, and
// collects the start instants of the rows it changed.
func writeMeasureRows(ctx context.Context, transaction *sql.Tx, measure string, rows []ConnectionMeasureRow,
	computedAt int64, written *DerivationWritten) error {
	if len(rows) == 0 {
		return nil
	}
	// One row per connection and direction: the last given wins, as a sequence of single writes would keep it.
	latest := map[[2]any]int{}
	var given []ConnectionMeasureRow
	for _, row := range rows {
		key := [2]any{row.ConnectionFlowID, row.Direction}
		if index, seen := latest[key]; seen {
			given[index] = row
			continue
		}
		latest[key] = len(given)
		given = append(given, row)
	}
	var kept []ConnectionMeasureRow
	var goneIDs []int64
	var goneDirections []string
	for _, row := range given {
		if row.Remove {
			goneIDs, goneDirections = append(goneIDs, row.ConnectionFlowID), append(goneDirections, row.Direction)
			continue
		}
		kept = append(kept, row)
	}
	if len(goneIDs) > 0 {
		gone, err := transaction.QueryContext(ctx,
			`DELETE FROM connection_measure AS v
			 USING unnest(CAST($2 AS BIGINT[]), CAST($3 AS TEXT[])) AS g (id, direction)
			 WHERE v.measure = $1 AND v.connection_flow_id = g.id AND v.direction = g.direction
			 RETURNING v.connection_start_at`, measure, goneIDs, goneDirections)
		if err != nil {
			return fmt.Errorf("store: removing the %s measure's rows: %w", measure, err)
		}
		written.Statements++
		for gone.Next() {
			var at int64
			if err := gone.Scan(&at); err != nil {
				_ = gone.Close()
				return fmt.Errorf("store: removing the %s measure's rows: %w", measure, err)
			}
			written.Rows++
			written.Instants = append(written.Instants, at)
		}
		_ = gone.Close()
		if err := gone.Err(); err != nil {
			return fmt.Errorf("store: removing the %s measure's rows: %w", measure, err)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	var (
		ids, starts, bytes, packets, records, markers, dues []int64
		directions, states                                  []string
		finals, dueKnown                                    []bool
	)
	for _, row := range kept {
		due := int64(0)
		if row.DueAt != nil {
			due = *row.DueAt
		}
		ids, directions, starts = append(ids, row.ConnectionFlowID), append(directions, row.Direction),
			append(starts, row.ConnectionStartAt)
		states, bytes, packets = append(states, row.State), append(bytes, row.Bytes), append(packets, row.Packets)
		records, markers = append(records, row.Records), append(markers, row.Markers)
		finals, dues, dueKnown = append(finals, row.DueAt == nil), append(dues, due), append(dueKnown, row.DueAt != nil)
	}
	changed, err := transaction.QueryContext(ctx,
		`INSERT INTO connection_measure AS v (measure, connection_flow_id, direction, connection_start_at, state, bytes,
		     packets, records, markers, final, due_at, computed_at)
		 SELECT $1, w.id, w.direction, w.start_at, w.state, w.bytes, w.packets, w.records, w.markers, w.final,
		        CASE WHEN w.due_known THEN w.due_at END, $13
		 FROM unnest(CAST($2 AS BIGINT[]), CAST($3 AS TEXT[]), CAST($4 AS BIGINT[]), CAST($5 AS TEXT[]),
		             CAST($6 AS BIGINT[]), CAST($7 AS BIGINT[]), CAST($8 AS BIGINT[]), CAST($9 AS BIGINT[]),
		             CAST($10 AS BOOLEAN[]), CAST($11 AS BIGINT[]), CAST($12 AS BOOLEAN[]))
		      AS w (id, direction, start_at, state, bytes, packets, records, markers, final, due_at, due_known)
		 ON CONFLICT (measure, connection_flow_id, direction) DO UPDATE SET
		     connection_start_at = excluded.connection_start_at, state = excluded.state, bytes = excluded.bytes,
		     packets = excluded.packets, records = excluded.records, markers = excluded.markers,
		     final = excluded.final, due_at = excluded.due_at, computed_at = excluded.computed_at
		 WHERE (v.connection_start_at, v.state, v.bytes, v.packets, v.records, v.markers, v.final, v.due_at)
		       IS DISTINCT FROM (excluded.connection_start_at, excluded.state, excluded.bytes, excluded.packets,
		                         excluded.records, excluded.markers, excluded.final, excluded.due_at)
		 RETURNING v.connection_start_at`,
		measure, ids, directions, starts, states, bytes, packets, records, markers, finals, dues, dueKnown, computedAt)
	if err != nil {
		return fmt.Errorf("store: writing the %s measure's rows: %w", measure, err)
	}
	defer func() { _ = changed.Close() }()
	written.Statements++
	for changed.Next() {
		var at int64
		if err := changed.Scan(&at); err != nil {
			return fmt.Errorf("store: writing the %s measure's rows: %w", measure, err)
		}
		written.Rows++
		written.Instants = append(written.Instants, at)
	}
	return changed.Err()
}

// writeMeasureIntervals rewrites a measure's window intervals for one provider as a whole, when they differ
// from those held.
func writeMeasureIntervals(ctx context.Context, transaction *sql.Tx, measure string, providerID int64,
	intervals []MeasureInterval, written *DerivationWritten) error {
	held, err := transaction.QueryContext(ctx,
		`SELECT starts_at, ends_at, state FROM measure_interval WHERE measure = $1 AND provider_id = $2
		 ORDER BY starts_at`, measure, providerID)
	if err != nil {
		return fmt.Errorf("store: reading the %s measure's intervals: %w", measure, err)
	}
	var current []MeasureInterval
	for held.Next() {
		var interval MeasureInterval
		var ends sql.NullInt64
		if err := held.Scan(&interval.StartsAt, &ends, &interval.State); err != nil {
			_ = held.Close()
			return fmt.Errorf("store: reading the %s measure's intervals: %w", measure, err)
		}
		if ends.Valid {
			end := ends.Int64
			interval.EndsAt = &end
		}
		current = append(current, interval)
	}
	_ = held.Close()
	if err := held.Err(); err != nil {
		return fmt.Errorf("store: reading the %s measure's intervals: %w", measure, err)
	}
	wanted := append([]MeasureInterval(nil), intervals...)
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].StartsAt < wanted[j].StartsAt })
	if sameIntervals(current, wanted) {
		return nil
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM measure_interval WHERE measure = $1 AND provider_id = $2",
		measure, providerID); err != nil {
		return fmt.Errorf("store: rewriting the %s measure's intervals: %w", measure, err)
	}
	written.Statements++
	if len(wanted) == 0 {
		written.Rows += int64(len(current))
		return nil
	}
	var starts, ends []int64
	var open []bool
	var states []string
	for _, interval := range wanted {
		end := int64(0)
		if interval.EndsAt != nil {
			end = *interval.EndsAt
		}
		starts, ends, open, states = append(starts, interval.StartsAt), append(ends, end),
			append(open, interval.EndsAt == nil), append(states, interval.State)
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO measure_interval (measure, provider_id, starts_at, ends_at, state)
		 SELECT $1, $2, i.starts_at, CASE WHEN i.open THEN NULL ELSE i.ends_at END, i.state
		 FROM unnest(CAST($3 AS BIGINT[]), CAST($4 AS BIGINT[]), CAST($5 AS BOOLEAN[]), CAST($6 AS TEXT[]))
		      AS i (starts_at, ends_at, open, state)`, measure, providerID, starts, ends, open, states); err != nil {
		return fmt.Errorf("store: rewriting the %s measure's intervals: %w", measure, err)
	}
	written.Statements++
	written.Rows += int64(len(wanted))
	return nil
}

// sameIntervals compares two interval sets, each ordered by start.
func sameIntervals(a, b []MeasureInterval) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		x, y := a[index], b[index]
		if x.StartsAt != y.StartsAt || x.State != y.State || (x.EndsAt == nil) != (y.EndsAt == nil) ||
			(x.EndsAt != nil && *x.EndsAt != *y.EndsAt) {
			return false
		}
	}
	return true
}

// sortedKeys returns a map's keys, sorted, so a run's statements run in one order whatever the map's.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
