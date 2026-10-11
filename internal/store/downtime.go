package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// opnview's heartbeat and the intervals during which it was not running
// (SPEC-step-5b-widget-api.md, D2). The schema's comment on opnview_heartbeat and
// opnview_downtime says why a dedicated table rather than a collection_gap reason; the
// meaning of the record is in docs/data-model.md, `opnview_downtime`.
//
// "Not running" is opnview's own term: no product opnview reads has a word for the
// intervals a reader of the firewall was itself absent, and it is recorded as such in
// docs/data-model.md, *Vocabulary*.

// NotRunningInterval is one interval during which opnview was not running: from the last
// instant it was known to run to the instant it started again.
type NotRunningInterval struct {
	From int64
	To   int64
	// CleanShutdown says the service recorded a clean stop at From. When false, From is
	// the last heartbeat of a process that ended without one -- a crash, a kill, a power
	// loss -- and the service may have stopped up to one heartbeat interval after it.
	CleanShutdown bool
	// DetectedAt is when the interval was recorded: the restart.
	DetectedAt int64
}

// RecordStart is called once when the service starts, before any collection: it compares
// the last heartbeat with now, records the interval since it when it is longer than the
// threshold, and takes the heartbeat over. It returns the interval it recorded, or nil.
//
// A first start ever has no heartbeat and records nothing: there is no instant before it
// at which opnview was known to run. A restart within the threshold records nothing
// either. The whole is one transaction, so two starts cannot record one interval twice.
func (s *Store) RecordStart(ctx context.Context, now, thresholdSeconds int64) (*NotRunningInterval, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: beginning the start record: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	recorded, err := recordAbsence(ctx, transaction, now, thresholdSeconds)
	if err != nil {
		return nil, err
	}
	if err := writeBeat(ctx, transaction, now, false); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing the start record: %w", err)
	}
	return recorded, nil
}

// Heartbeat records that the service is alive at now.
//
// A beat that finds the previous one older than the threshold records that interval too:
// the process was alive at both ends and did not run between them -- the host suspended,
// the process stopped by a signal and continued -- which is the same absence as a stop and
// a start, for every source opnview reads.
func (s *Store) Heartbeat(ctx context.Context, now, thresholdSeconds int64) (*NotRunningInterval, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: beginning the heartbeat: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	recorded, err := recordAbsence(ctx, transaction, now, thresholdSeconds)
	if err != nil {
		return nil, err
	}
	if err := writeBeat(ctx, transaction, now, false); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing the heartbeat: %w", err)
	}
	return recorded, nil
}

// RecordCleanStop records that the service is stopping cleanly at now: the next start
// bounds the interval by this instant, exactly, rather than by the last periodic beat.
func (s *Store) RecordCleanStop(ctx context.Context, now int64) error {
	return writeBeat(ctx, s.db, now, true)
}

// recordAbsence inserts the interval between the last heartbeat and now when it is longer
// than the threshold.
func recordAbsence(ctx context.Context, q querier, now, thresholdSeconds int64) (*NotRunningInterval, error) {
	var (
		beatAt    int64
		cleanStop int64
	)
	err := q.QueryRowContext(ctx,
		"SELECT beat_at, clean_stop FROM opnview_heartbeat WHERE id = 1").Scan(&beatAt, &cleanStop)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("store: reading the heartbeat: %w", err)
	}
	if now-beatAt <= thresholdSeconds {
		return nil, nil
	}
	interval := &NotRunningInterval{From: beatAt, To: now, CleanShutdown: cleanStop == 1, DetectedAt: now}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO opnview_downtime (interval_start_at, interval_end_at, clean_stop, detected_at)
		 VALUES (?, ?, ?, ?)`, interval.From, interval.To, cleanStop, now); err != nil {
		return nil, fmt.Errorf("store: recording the interval opnview was not running: %w", err)
	}
	return interval, nil
}

// writeBeat writes the one heartbeat row.
func writeBeat(ctx context.Context, q querier, now int64, cleanStop bool) error {
	clean := 0
	if cleanStop {
		clean = 1
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO opnview_heartbeat (id, beat_at, clean_stop) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET beat_at = excluded.beat_at, clean_stop = excluded.clean_stop`,
		now, clean); err != nil {
		return fmt.Errorf("store: writing the heartbeat: %w", err)
	}
	return nil
}

// ReadNotRunning returns the intervals during which opnview was not running that overlap
// [from, to), oldest first. Every source went unread during each of them, so a widget
// reads them as gaps of every provider it binds.
//
// Its statement is read_not_running in read.sql, so sql/schema-checks.sh plans it at
// both seed sizes like every other statement the API runs.
func (s *Store) ReadNotRunning(ctx context.Context, from, to int64) ([]NotRunningInterval, error) {
	text, err := Statement("read_not_running")
	if err != nil {
		return nil, err
	}
	rows, err := s.reader.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: reading the intervals opnview was not running: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var intervals []NotRunningInterval
	for rows.Next() {
		var (
			interval NotRunningInterval
			clean    int64
		)
		if err := rows.Scan(&interval.From, &interval.To, &clean, &interval.DetectedAt); err != nil {
			return nil, fmt.Errorf("store: reading the intervals opnview was not running: %w", err)
		}
		interval.CleanShutdown = clean == 1
		intervals = append(intervals, interval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the intervals opnview was not running: %w", err)
	}
	return intervals, nil
}
