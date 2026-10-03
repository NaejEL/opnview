package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// The record rate: how many records a kind writes per second, measured on the
// rows opnview already holds.
//
// IT IS WHAT SIZES A POLL. A paged read loses records when the source writes more
// between two polls than one page holds, and the API offers no way to recover
// them. The interval and the page size are therefore one decision, and the only
// honest input to it is the rate this installation actually produces, not a
// figure from somebody else's network. This file measures that rate; package
// sizing turns it into a suggested pair.
//
// TWO CLOCKS, BECAUSE THEY ANSWER TWO QUESTIONS. The source clock is when the
// firewall says a record happened; it gives the rate the source produces. The
// ingested clock is when opnview stored the record; it gives the rate opnview
// keeps up with. A table that records only one of them is measured on that one,
// and the other is reported as not measured rather than invented.
//
// THE MEASUREMENT IS ONE QUERY, AND THE SAME ONE A PERSON RUNS. RecordRateBranch
// builds the SQL; sql/queries/diagnostics.sql carries it under the name
// RecordRateDiagnosticName, so the figure the collection surface prints can be
// reproduced by hand with sqlite3 against the same database, and a figure that
// looks wrong can be checked rather than argued about.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening: the declarations, the SQL text and the behaviour are the compiled
// ones; the comments are rewritten.

// RecordRateSpec says where the records of one kind live: the table, the column
// holding the source clock, and the column holding the ingested clock if the
// table has one. It names columns of the schema and nothing else; it is built by
// package sizing from its own registry, never from user input, because its fields
// are spliced into SQL.
type RecordRateSpec struct {
	// Kind is the registry kind the records belong to, as in provider.kind.
	Kind string
	// Table is the table holding one row per record of the kind.
	Table string
	// SourceColumn holds the source clock, in Unix seconds: when the firewall
	// says the record happened. The window and the buckets are taken on it, so
	// a record is counted in the window its source places it in.
	SourceColumn string
	// IngestedColumn holds the ingested clock, in Unix seconds: when opnview
	// stored the record. Empty when the table records no such instant; the
	// ingested span and rate are then reported as not measured, never as the
	// source figures copied over.
	IngestedColumn string
}

// RecordRateGap is the collection gaps of one reason detected in the window, as
// collection_gap records them.
type RecordRateGap struct {
	// Reason is collection_gap.reason.
	Reason string
	// Count is how many gap rows of that reason the window holds.
	Count int64
	// MissedSeconds is the time those gaps cover, summed.
	MissedSeconds int64
}

// RecordRateWindow is the span measured, in Unix seconds, on the source clock.
type RecordRateWindow struct {
	// StartAt is included and EndAt is excluded, so consecutive windows tile.
	StartAt int64
	EndAt   int64
}

// Seconds is the length of the window, in seconds.
func (w RecordRateWindow) Seconds() int64 { return w.EndAt - w.StartAt }

// Rate is a rate in records per second, and whether it was measured at all.
//
// KNOWN IS WHAT KEEPS "NOTHING TO MEASURE" FROM PRINTING AS "NOUGHT PER SECOND".
// A window with no record, or with a single instant, has no span to divide by;
// its rate is unknown, which is a state of the measurement and not a figure, and
// a page sized on a figure of zero would be sized on nothing.
type Rate struct {
	PerSecond float64
	Known     bool
}

// RecordRateMeasurement is the record rate of one kind over one window, with
// everything a reader needs to judge it: what was counted, over which span, at
// which peak, and which gaps the collectors recorded in the same window. It is
// a value; nothing in it refers back to the database.
type RecordRateMeasurement struct {
	// Kind is the kind measured.
	Kind string
	// Window is the span asked for.
	Window RecordRateWindow
	// RetentionSeconds is the purge horizon in force when the measurement was
	// taken, read from the same database, 0 for unlimited. A window longer than
	// the horizon cannot hold the records it asks for.
	RetentionSeconds int64
	// BucketSeconds is the width of the buckets the peak is counted in.
	BucketSeconds int64

	// RecordCount is the number of records whose source clock is in the window.
	RecordCount int64
	// CoveredFromAt and CoveredToAt are the earliest and latest source instants
	// among them. They are meaningful only when HasRecords is true.
	CoveredFromAt int64
	CoveredToAt   int64
	// HasRecords is false when the window holds no record at all.
	HasRecords bool

	// IngestedFromAt and IngestedToAt are the earliest and latest ingested
	// instants among the same records. They are meaningful only when
	// HasIngestedClock and HasRecords are both true.
	IngestedFromAt int64
	IngestedToAt   int64
	// HasIngestedClock is false when the kind's table records no ingested
	// instant.
	HasIngestedClock bool

	// MeanRate is RecordCount over the covered span on the source clock.
	MeanRate Rate
	// IngestedMeanRate is RecordCount over the span on the ingested clock.
	IngestedMeanRate Rate
	// PeakRate is the busiest bucket's records over the bucket's width: the
	// figure a page must cover, since a mean hides the burst that overflows it.
	PeakRate Rate
	// PeakBucketRecords is the number of records in that busiest bucket.
	PeakBucketRecords int64

	// Gaps are the collection gaps detected in the window, one per reason, in
	// the order of the reason. A gap is counted by when it was detected, so a
	// gap detected in the window about a span before it is still counted: it is
	// the window in which the operator learned of the loss.
	Gaps []RecordRateGap
	// GapCount and MissedSeconds total them.
	GapCount      int64
	MissedSeconds int64
}

// CoveredSpanSeconds is the span between the earliest and latest record on the
// source clock, 0 when there is no record.
func (m RecordRateMeasurement) CoveredSpanSeconds() int64 {
	if !m.HasRecords {
		return 0
	}
	return m.CoveredToAt - m.CoveredFromAt
}

// IngestedSpanSeconds is the same span on the ingested clock, 0 without one.
func (m RecordRateMeasurement) IngestedSpanSeconds() int64 {
	if !m.HasRecords || !m.HasIngestedClock {
		return 0
	}
	return m.IngestedToAt - m.IngestedFromAt
}

// RecordRateDiagnosticName is the name the record-rate measurement carries in
// sql/queries/diagnostics.sql.
//
// The diagnostic and the measurement are the same SQL, and a test in package
// sizing holds them to that: the query the collection surface runs is assembled
// from RecordRateBranch, and the file a person runs by hand is the same branch
// for every measured kind. A figure on the screen that the diagnostic cannot
// reproduce would be a figure nobody can check.
const RecordRateDiagnosticName = "Record rate per kind"

// RecordRateBranch returns the SQL measuring one kind over a window bound to the
// named parameters :window_start and :window_end, with the peak counted in
// buckets of bucketSeconds. It returns one row per gap reason found in the
// window, or a single row with a NULL reason when there is none; every other
// column is the same on every row.
//
// The table and column names come from spec and are spliced into the text, which
// is why spec is built from a registry in code and never from input. The kind is
// spliced too, as a literal, so that a kind with no record still yields its row.
func RecordRateBranch(spec RecordRateSpec, bucketSeconds int64) string {
	where := spec.SourceColumn + " >= :window_start AND " + spec.SourceColumn + " < :window_end"
	scalar := func(expression string) string {
		return "(SELECT " + expression + " FROM " + spec.Table + "\n" +
			"      WHERE " + where + ")"
	}
	ingestedFrom, ingestedTo := "NULL", "NULL"
	if spec.IngestedColumn != "" {
		ingestedFrom = scalar("min(" + spec.IngestedColumn + ")")
		ingestedTo = scalar("max(" + spec.IngestedColumn + ")")
	}
	return strings.Join([]string{
		"SELECT",
		"    k.kind AS kind,",
		"    :window_start AS window_start_at,",
		"    :window_end AS window_end_at,",
		"    (SELECT CAST(value AS INTEGER) FROM setting",
		"      WHERE key = 'retention_seconds')",
		"        AS retention_seconds,",
		"    " + scalar("count(*)"),
		"        AS record_count,",
		"    " + scalar("min("+spec.SourceColumn+")"),
		"        AS covered_from_at,",
		"    " + scalar("max("+spec.SourceColumn+")"),
		"        AS covered_to_at,",
		"    " + ingestedFrom,
		"        AS ingested_from_at,",
		"    " + ingestedTo,
		"        AS ingested_to_at,",
		"    (SELECT max(bucket_records) FROM",
		"      (SELECT count(*) AS bucket_records FROM " + spec.Table,
		"        WHERE " + where,
		"        GROUP BY " + spec.SourceColumn + " / " + decimal(bucketSeconds) + "))",
		"        AS peak_bucket_records,",
		"    g.reason AS gap_reason,",
		"    count(g.id) AS gap_count,",
		"    coalesce(sum(g.interval_end_at - g.interval_start_at), 0) AS missed_seconds",
		"FROM (SELECT '" + spec.Kind + "' AS kind) AS k",
		"LEFT JOIN provider AS p ON p.kind = k.kind",
		"LEFT JOIN collection_gap AS g ON g.provider_id = p.id",
		"     AND g.detected_at >= :window_start AND g.detected_at < :window_end",
		"GROUP BY k.kind, g.reason",
	}, "\n")
}

// decimal writes an integer into SQL text.
//
// The bucket width is written into the text of the measurement rather than
// bound as a parameter, so that the branch the diagnostic file carries is the
// complete query for one bucket width, readable and runnable as it stands. It
// is an int64 formatted in base ten, so nothing but digits and a sign can reach
// the text, whatever the caller passes.
func decimal(value int64) string { return strconv.FormatInt(value, 10) }

// MeasureRecordRate measures the record rate of one kind over one window.
//
// A window with no record is not an error: it comes back with HasRecords false
// and every rate unknown, because "this source wrote nothing" is a state worth
// showing. A bucket width that is not positive is an error, since there is
// nothing to count the peak in. The measurement returned with an error carries
// what was known before it, and nothing more.
func (s *Store) MeasureRecordRate(ctx context.Context, spec RecordRateSpec, window RecordRateWindow,
	bucketSeconds int64) (RecordRateMeasurement, error) {
	measurement := RecordRateMeasurement{
		Kind:             spec.Kind,
		Window:           window,
		BucketSeconds:    bucketSeconds,
		HasIngestedClock: spec.IngestedColumn != "",
	}
	if bucketSeconds <= 0 {
		return measurement,
			fmt.Errorf("store: a record-rate bucket of %d seconds is not a bucket", bucketSeconds)
	}

	rows, err := s.db.QueryContext(ctx, RecordRateBranch(spec, bucketSeconds)+" ORDER BY g.reason",
		sql.Named("window_start", window.StartAt), sql.Named("window_end", window.EndAt))
	if err != nil {
		return measurement, fmt.Errorf("store: measuring the %s record rate: %w", spec.Kind, err)
	}
	defer func() { _ = rows.Close() }()

	read := false
	for rows.Next() {
		var (
			kind                       string
			windowStartAt, windowEndAt int64
			retention                  sql.NullInt64
			recordCount, gapCount      int64
			coveredFrom, coveredTo     sql.NullInt64
			ingestedFrom, ingestedTo   sql.NullInt64
			peak                       sql.NullInt64
			gapReason                  sql.NullString
			missedSeconds              int64
		)
		if err := rows.Scan(&kind, &windowStartAt, &windowEndAt, &retention, &recordCount,
			&coveredFrom, &coveredTo, &ingestedFrom, &ingestedTo, &peak,
			&gapReason, &gapCount, &missedSeconds); err != nil {
			return measurement, fmt.Errorf("store: reading the %s record rate: %w", spec.Kind, err)
		}
		read = true
		measurement.RetentionSeconds = retention.Int64
		measurement.RecordCount = recordCount
		measurement.HasRecords = coveredFrom.Valid && coveredTo.Valid
		measurement.CoveredFromAt, measurement.CoveredToAt = coveredFrom.Int64, coveredTo.Int64
		if measurement.HasIngestedClock && ingestedFrom.Valid && ingestedTo.Valid {
			measurement.IngestedFromAt, measurement.IngestedToAt = ingestedFrom.Int64, ingestedTo.Int64
		}
		measurement.PeakBucketRecords = peak.Int64
		if peak.Valid {
			measurement.PeakRate = Rate{Known: true,
				PerSecond: float64(peak.Int64) / float64(bucketSeconds)}
		}
		// Every row carries the same measurement and one gap reason; the LEFT
		// JOIN yields a single row with a NULL reason when the window holds no
		// gap, and that row adds nothing to the gaps. The totals are summed here
		// rather than in SQL so that each reason stays visible on its own.
		if gapReason.Valid {
			measurement.Gaps = append(measurement.Gaps, RecordRateGap{
				Reason:        gapReason.String,
				Count:         gapCount,
				MissedSeconds: missedSeconds,
			})
			measurement.GapCount += gapCount
			measurement.MissedSeconds += missedSeconds
		}
	}
	if err := rows.Err(); err != nil {
		return measurement, fmt.Errorf("store: reading the %s record rate: %w", spec.Kind, err)
	}
	if !read {
		return measurement,
			fmt.Errorf("store: the %s record-rate measurement returned no row at all", spec.Kind)
	}

	// The means are taken over the span the records actually cover, not over
	// the window asked for: a source that started an hour ago has an hour of
	// records in a day's window, and dividing them by the day would report a
	// twenty-fourth of its rate. A span of zero — no record, or records at one
	// instant — has no rate, and the rate stays unknown.
	if span := measurement.CoveredSpanSeconds(); span > 0 {
		measurement.MeanRate = Rate{PerSecond: float64(measurement.RecordCount) / float64(span), Known: true}
	}
	if span := measurement.IngestedSpanSeconds(); span > 0 {
		measurement.IngestedMeanRate = Rate{Known: true,
			PerSecond: float64(measurement.RecordCount) / float64(span)}
	}

	return measurement, nil
}

// KindsInProviderTable returns every kind the provider registry holds a row for.
//
// Package sizing has its own list of the kinds it measures; this is what lets a
// test hold that list to the schema, so that a kind added to one is not
// silently missing from the other.
func (s *Store) KindsInProviderTable(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT kind FROM provider ORDER BY kind")
	if err != nil {
		return nil, fmt.Errorf("store: listing the registered provider kinds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	kinds := map[string]struct{}{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, fmt.Errorf("store: listing the registered provider kinds: %w", err)
		}
		kinds[kind] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing the registered provider kinds: %w", err)
	}
	return kinds, nil
}

// ProviderOfKind is one implementation of a kind, as the collection surface shows it.
type ProviderOfKind struct {
	// ProviderKey is provider.provider_key.
	ProviderKey string
	// DisplayName is provider.display_name.
	DisplayName string
	// State is the last availability recorded, empty when none has been.
	State AvailabilityState
	// IsActive is whether opnview reads this implementation now.
	IsActive bool
}

// ProvidersOfKind returns the implementations of one kind with their last recorded
// availability and whether each is active, in registry order.
//
// It is what tells "this kind is not collected" apart from "this kind is
// collected and quiet" on the collection surface: a kind whose implementations
// are all inactive gets no suggestion, because a rate measured on a source
// nobody reads is the rate of nothing.
//
// The LEFT JOIN keeps an implementation never probed, with an empty state.
func (s *Store) ProvidersOfKind(ctx context.Context, kind string) ([]ProviderOfKind, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.provider_key, p.display_name, coalesce(a.state, ''), p.is_active
		   FROM provider AS p
		   LEFT JOIN source_availability AS a ON a.provider_id = p.id
		  WHERE p.kind = ?
		  ORDER BY p.provider_key`,
		kind)
	if err != nil {
		return nil, fmt.Errorf("store: listing the %s providers: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var providers []ProviderOfKind
	for rows.Next() {
		var provider ProviderOfKind
		var state string
		var active int
		if err := rows.Scan(&provider.ProviderKey, &provider.DisplayName, &state, &active); err != nil {
			return nil, fmt.Errorf("store: listing the %s providers: %w", kind, err)
		}
		provider.State = AvailabilityState(strings.TrimSpace(state))
		provider.IsActive = active == 1
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing the %s providers: %w", kind, err)
	}
	return providers, nil
}
