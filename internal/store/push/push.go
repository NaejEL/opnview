package push

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// What the push sources store beside their rows: their gaps, and when each listener was last heard
// alive (PS7 of the push amendment to specs/SPEC-backend-completion-plugins.md).

// RecordPushGap records one gap of a push declaration's provider. A gap of the same provider and
// reason starting at the same instant is the same gap seen again -- a silence that is still going
// on, re-recorded as it lengthens -- and is extended rather than written twice.
func (s *PushStore) RecordPushGap(ctx context.Context, providerID int64, reason vocab.GapReason, from, to int64, detail string,
	now int64) error {
	if to < from {
		from, to = to, from
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: recording a %s gap: %w", reason, err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx,
		`UPDATE collection_gap SET interval_end_at = greatest(interval_end_at, ?), detail = ?, detected_at = ?
		 WHERE provider_id = ? AND reason = ? AND interval_start_at = ?`,
		to, detail, now, providerID, string(reason), from)
	if err != nil {
		return fmt.Errorf("store: recording a %s gap: %w", reason, err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at, reason, detail, detected_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			providerID, from, to, string(reason), detail, now); err != nil {
			return fmt.Errorf("store: recording a %s gap: %w", reason, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: recording a %s gap: %w", reason, err)
	}
	return nil
}

// ListenerAliveAt returns when a push listener was last heard alive, and whether it ever was.
func (s *PushStore) ListenerAliveAt(ctx context.Context, protocol, transport string) (int64, bool, error) {
	var at int64
	err := s.SQL.QueryRowContext(ctx, "SELECT alive_at FROM push_listener WHERE protocol = ? AND transport = ?",
		protocol, transport).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: reading when the %s/%s listener was alive: %w", protocol, transport, err)
	}
	return at, true, nil
}

// RecordListenerAlive records that a push listener is alive at an instant.
func (s *PushStore) RecordListenerAlive(ctx context.Context, protocol, transport string, at int64) error {
	if _, err := s.SQL.ExecContext(ctx,
		`INSERT INTO push_listener (protocol, transport, alive_at) VALUES (?, ?, ?)
		 ON CONFLICT (protocol, transport) DO UPDATE SET alive_at = greatest(push_listener.alive_at, excluded.alive_at)`,
		protocol, transport, at); err != nil {
		return fmt.Errorf("store: recording the %s/%s listener alive: %w", protocol, transport, err)
	}
	return nil
}

// SourcePeriod is one interval of a provider's history and the path that delivered it (PS9).
type SourcePeriod struct {
	Path   string `json:"path"`
	FromAt int64  `json:"from_at"`
	// ToAt is nil for the open period.
	ToAt *int64 `json:"to_at"`
}

// The paths of a source period.
const (
	PathPull = "pull"
	PathPush = "push"
)

// SourcePeriods returns a provider's source periods, oldest first.
func (s *PushStore) SourcePeriods(ctx context.Context, providerID int64) ([]SourcePeriod, error) {
	rows, err := s.SQL.QueryContext(ctx,
		"SELECT path, from_at, to_at FROM provider_source_period WHERE provider_id = ? ORDER BY from_at", providerID)
	if err != nil {
		return nil, fmt.Errorf("store: reading the source periods: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var periods []SourcePeriod
	for rows.Next() {
		var period SourcePeriod
		var to sql.NullInt64
		if err := rows.Scan(&period.Path, &period.FromAt, &to); err != nil {
			return nil, fmt.Errorf("store: reading the source periods: %w", err)
		}
		if to.Valid {
			end := to.Int64
			period.ToAt = &end
		}
		periods = append(periods, period)
	}
	return periods, rows.Err()
}

// OpenSourcePeriod closes a provider's open period at boundary and opens one of path from
// boundary + 1, in one transaction; a provider with no period yet gets a first one of the other
// path from 0 to boundary. The two never overlap.
func (s *PushStore) OpenSourcePeriod(ctx context.Context, providerID int64, path string, boundary int64) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: opening a source period: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var count int64
	if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM provider_source_period WHERE provider_id = ?",
		providerID).Scan(&count); err != nil {
		return fmt.Errorf("store: opening a source period: %w", err)
	}
	previous := PathPull
	if path == PathPull {
		previous = PathPush
	}
	if count == 0 {
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO provider_source_period (provider_id, path, from_at, to_at) VALUES (?, ?, 0, ?)",
			providerID, previous, boundary); err != nil {
			return fmt.Errorf("store: opening a source period: %w", err)
		}
	} else if _, err := transaction.ExecContext(ctx,
		"UPDATE provider_source_period SET to_at = greatest(from_at, ?) WHERE provider_id = ? AND to_at IS NULL",
		boundary, providerID); err != nil {
		return fmt.Errorf("store: closing a source period: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO provider_source_period (provider_id, path, from_at) VALUES (?, ?, ?)",
		providerID, path, boundary+1); err != nil {
		return fmt.Errorf("store: opening a source period: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: opening a source period: %w", err)
	}
	return nil
}

// NewestPushedFlowObservedAt is the newest instant of a flow a push path delivered: its digest
// carries the pushed prefix.
func (s *PushStore) NewestPushedFlowObservedAt(ctx context.Context, prefix string) (int64, bool, error) {
	var newest sql.NullInt64
	if err := s.SQL.QueryRowContext(ctx, "SELECT max(observed_at) FROM flow WHERE log_digest LIKE ?",
		prefix+"%").Scan(&newest); err != nil {
		return 0, false, fmt.Errorf("store: reading the newest pushed flow: %w", err)
	}
	return newest.Int64, newest.Valid, nil
}

// CurrentFirewallAddresses returns every address the firewall's own interfaces hold as discovery last
// read them: the this_firewall_address view's current rows. The push listeners accept datagrams and
// connections from these, and from the configured base URL's host, and from nothing else (PS6).
func (s *PushStore) CurrentFirewallAddresses(ctx context.Context) ([]netip.Addr, error) {
	rows, err := s.SQL.QueryContext(ctx, "SELECT DISTINCT address FROM this_firewall_address WHERE is_current = 1")
	if err != nil {
		return nil, fmt.Errorf("store: reading the firewall's addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var addresses []netip.Addr
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, fmt.Errorf("store: reading the firewall's addresses: %w", err)
		}
		if address, err := netip.ParseAddr(text); err == nil {
			addresses = append(addresses, address.Unmap())
		}
	}
	return addresses, rows.Err()
}
