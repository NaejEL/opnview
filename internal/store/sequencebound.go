package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The sequence-loss bound of a push entry's sender (schema.sql, sequence_bound; NJ7 as PA7 of the plugin-API
// amendment restates it): how many sequence counters the sender runs, recorded over time by whatever reads
// its configuration, under the value name the push entry declares.

// RecordSequenceBound records one read of a provider's sequence bound at an instant. A read confirming the
// row in force -- the same counters, and changed false -- extends it to the instant; any other opens a new
// row, so a change is never bridged.
func (s *Store) RecordSequenceBound(ctx context.Context, providerID int64, name string, at, counters int64,
	changed bool) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: recording the sequence bound %s: %w", name, err)
	}
	defer func() { _ = transaction.Rollback() }()
	var (
		startsAt, held int64
		found          = true
	)
	err = transaction.QueryRowContext(ctx,
		`SELECT starts_at, counters FROM sequence_bound WHERE provider_id = $1 AND name = $2
		 ORDER BY starts_at DESC LIMIT 1 FOR UPDATE`, providerID, name).Scan(&startsAt, &held)
	if errors.Is(err, sql.ErrNoRows) {
		found = false
	} else if err != nil {
		return fmt.Errorf("store: reading the sequence bound %s: %w", name, err)
	}
	if found && !changed && held == counters && at >= startsAt {
		_, err = transaction.ExecContext(ctx,
			`UPDATE sequence_bound SET ends_at = GREATEST(ends_at, $4)
			 WHERE provider_id = $1 AND name = $2 AND starts_at = $3`, providerID, name, startsAt, at)
	} else {
		_, err = transaction.ExecContext(ctx,
			`INSERT INTO sequence_bound (provider_id, name, starts_at, ends_at, counters) VALUES ($1, $2, $3, $3, $4)
			 ON CONFLICT (provider_id, name, starts_at) DO UPDATE SET ends_at = excluded.ends_at,
			     counters = excluded.counters`, providerID, name, at, counters)
	}
	if err != nil {
		return fmt.Errorf("store: recording the sequence bound %s: %w", name, err)
	}
	return transaction.Commit()
}

// SequenceBoundAsserted reports whether a loss over [from, to] is asserted under a provider's sequence bound:
// one row holds both instants, and its counters are above 0. An instant after the newest row's last read is
// held by the newest row.
func (s *Store) SequenceBoundAsserted(ctx context.Context, providerID int64, name string, from, to int64) (bool, error) {
	var asserted bool
	err := s.SQL.QueryRowContext(ctx,
		`WITH newest AS (
		     SELECT max(starts_at) AS starts_at FROM sequence_bound WHERE provider_id = $1 AND name = $2
		 )
		 SELECT EXISTS (
		     SELECT 1 FROM sequence_bound AS b CROSS JOIN newest AS n
		     WHERE b.provider_id = $1 AND b.name = $2 AND b.counters > 0
		       AND b.starts_at <= $3 AND (b.ends_at >= $4 OR b.starts_at = n.starts_at)
		 )`, providerID, name, from, to).Scan(&asserted)
	if err != nil {
		return false, fmt.Errorf("store: reading the sequence bound %s: %w", name, err)
	}
	return asserted, nil
}
