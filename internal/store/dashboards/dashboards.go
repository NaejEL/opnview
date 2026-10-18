package dashboards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The dashboards, stored as the text they were given. Nothing here reads inside a document: its
// structure is validated by internal/dashboard before a write reaches this package, and its
// references are never checked (docs/dashboard-format.md, "Import validation").

// The origins of a dashboard (the CHECK of dashboard.origin).
const (
	OriginCreated  = "created"
	OriginImported = "imported"
	OriginDefault  = "default"
)

// ErrUnknownDashboard is an id naming no dashboard.
var ErrUnknownDashboard = errors.New("store: no dashboard has that id")

// ErrOrderIncomplete is an order that does not name every dashboard exactly once.
var ErrOrderIncomplete = errors.New("store: an order names every dashboard exactly once")

// RevisionConflict is a save naming a revision that is not the stored one: somebody saved since.
type RevisionConflict struct {
	// Current is the stored revision.
	Current int64
}

// Error names the stored revision.
func (c *RevisionConflict) Error() string {
	return fmt.Sprintf("store: the dashboard is at revision %d", c.Current)
}

// Dashboard is one stored dashboard.
type Dashboard struct {
	ID         int64
	Position   int64
	Document   string
	Revision   int64
	Origin     string
	SourceName *string
	CreatedAt  int64
	UpdatedAt  int64
}

// scanDashboard reads one row of the statements' common column list.
func scanDashboard(row interface{ Scan(...any) error }) (Dashboard, error) {
	var dashboard Dashboard
	var source sql.NullString
	err := row.Scan(&dashboard.ID, &dashboard.Position, &dashboard.Document, &dashboard.Revision, &dashboard.Origin,
		&source, &dashboard.CreatedAt, &dashboard.UpdatedAt)
	if source.Valid {
		name := source.String
		dashboard.SourceName = &name
	}
	return dashboard, err
}

// queryOne runs a named statement answering at most one dashboard.
func queryOne(ctx context.Context, q sqldb.Querier, name string, parameters map[string]any) (Dashboard, error) {
	text, err := sqldb.Statement(name)
	if err != nil {
		return Dashboard{}, err
	}
	dashboard, err := scanDashboard(sqldb.QueryRow(ctx, q, text, parameters))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Dashboard{}, ErrUnknownDashboard
	case err != nil:
		return Dashboard{}, fmt.Errorf("store: %s: %w", name, err)
	}
	return dashboard, nil
}

// Dashboards lists every dashboard, in order.
func (s *DashboardStore) Dashboards(ctx context.Context) ([]Dashboard, error) {
	text, err := sqldb.Statement("dashboards_list")
	if err != nil {
		return nil, err
	}
	rows, err := s.SQL.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: dashboards_list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	listed := []Dashboard{}
	for rows.Next() {
		dashboard, err := scanDashboard(rows)
		if err != nil {
			return nil, fmt.Errorf("store: dashboards_list: %w", err)
		}
		listed = append(listed, dashboard)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dashboards_list: %w", err)
	}
	return listed, nil
}

// Dashboard reads one dashboard; ErrUnknownDashboard when the id names none.
func (s *DashboardStore) Dashboard(ctx context.Context, id int64) (Dashboard, error) {
	return queryOne(ctx, s.SQL, "dashboard_get", map[string]any{"id": id})
}

// CreateDashboard stores a new dashboard after every other one, at revision 1.
func (s *DashboardStore) CreateDashboard(ctx context.Context, document, origin string, sourceName *string,
	now int64) (Dashboard, error) {
	return queryOne(ctx, s.SQL, "dashboard_create", map[string]any{"document": document, "origin": origin,
		"source_name": nullable(sourceName), "now": now})
}

// SaveDashboard stores a new document for a dashboard whose stored revision is revision, and
// answers it at the next revision. A stale revision is a *RevisionConflict and nothing is written.
func (s *DashboardStore) SaveDashboard(ctx context.Context, id, revision int64, document string,
	now int64) (Dashboard, error) {
	var saved Dashboard
	err := s.inTransaction(ctx, "saving a dashboard", func(tx *sql.Tx) error {
		stored, err := queryOne(ctx, tx, "dashboard_lock", map[string]any{"id": id})
		if err != nil {
			return err
		}
		if stored.Revision != revision {
			return &RevisionConflict{Current: stored.Revision}
		}
		saved, err = queryOne(ctx, tx, "dashboard_save", map[string]any{"id": id, "document": document, "now": now})
		return err
	})
	return saved, err
}

// DeleteDashboard deletes a dashboard and answers it whole, so the deletion can be undone.
func (s *DashboardStore) DeleteDashboard(ctx context.Context, id int64) (Dashboard, error) {
	var deleted Dashboard
	err := s.inTransaction(ctx, "deleting a dashboard", func(tx *sql.Tx) error {
		var err error
		deleted, err = queryOne(ctx, tx, "dashboard_lock", map[string]any{"id": id})
		if err != nil {
			return err
		}
		_, err = sqldb.ExecNamed(ctx, tx, "dashboard_delete", map[string]any{"id": id})
		return err
	})
	return deleted, err
}

// RestoreDashboard recreates a deleted dashboard as it was: under its own id when no dashboard holds
// it any more, else under a new one, at its own position, revision, origin and instants.
func (s *DashboardStore) RestoreDashboard(ctx context.Context, dashboard Dashboard) (Dashboard, error) {
	var restored Dashboard
	err := s.inTransaction(ctx, "restoring a dashboard", func(tx *sql.Tx) error {
		text, err := sqldb.Statement("dashboard_id_taken")
		if err != nil {
			return err
		}
		var taken bool
		if err := sqldb.QueryRow(ctx, tx, text, map[string]any{"id": dashboard.ID}).
			Scan(&taken); err != nil {
			return fmt.Errorf("store: dashboard_id_taken: %w", err)
		}
		var id any = dashboard.ID
		if taken {
			id = nil
		}
		restored, err = queryOne(ctx, tx, "dashboard_restore", map[string]any{"id": id,
			"position": dashboard.Position, "document": dashboard.Document, "revision": dashboard.Revision,
			"origin": dashboard.Origin, "source_name": nullable(dashboard.SourceName),
			"created_at": dashboard.CreatedAt, "updated_at": dashboard.UpdatedAt})
		return err
	})
	return restored, err
}

// OrderDashboards gives every dashboard its index in ids as its position. ids must name every
// dashboard exactly once: an id naming none is ErrUnknownDashboard, and a list missing one or naming
// one twice is ErrOrderIncomplete; nothing is written then.
func (s *DashboardStore) OrderDashboards(ctx context.Context, ids []int64) error {
	return s.inTransaction(ctx, "ordering the dashboards", func(tx *sql.Tx) error {
		stored, err := sqldb.QueryInts(ctx, tx, "dashboard_ids", nil)
		if err != nil {
			return err
		}
		known := map[int64]bool{}
		for _, id := range stored {
			known[id] = true
		}
		named := map[int64]bool{}
		for _, id := range ids {
			if !known[id] {
				return ErrUnknownDashboard
			}
			if named[id] {
				return ErrOrderIncomplete
			}
			named[id] = true
		}
		if len(named) != len(known) {
			return ErrOrderIncomplete
		}
		encoded, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		_, err = sqldb.ExecNamed(ctx, tx, "dashboards_order", map[string]any{"ids": string(encoded)})
		return err
	})
}

// SeedDefault stores the default dashboard once, with origin default (F7-28): the first call ever
// claims the mark key and, when no dashboard is stored, creates document; every later call does
// nothing, so deleting every dashboard never brings the default back. It answers whether it created
// the dashboard.
func (s *DashboardStore) SeedDefault(ctx context.Context, key, document string, now int64) (bool, error) {
	created := false
	err := s.inTransaction(ctx, "offering the default dashboard", func(tx *sql.Tx) error {
		text, err := sqldb.Statement("dashboard_default_claim")
		if err != nil {
			return err
		}
		var claimed string
		err = sqldb.QueryRow(ctx, tx, text, map[string]any{"key": key, "now": now}).Scan(&claimed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("store: dashboard_default_claim: %w", err)
		}
		counted, err := sqldb.Statement("dashboard_count")
		if err != nil {
			return err
		}
		var stored int64
		if err := tx.QueryRowContext(ctx, counted).Scan(&stored); err != nil {
			return fmt.Errorf("store: dashboard_count: %w", err)
		}
		if stored > 0 {
			return nil
		}
		if _, err := queryOne(ctx, tx, "dashboard_create", map[string]any{"document": document, "origin": OriginDefault,
			"source_name": nil, "now": now}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

// nullable is a name, or NULL.
func nullable(name *string) any {
	if name == nil {
		return nil
	}
	return *name
}

// inTransaction runs work in one transaction, committed when work succeeds.
func (s *DashboardStore) inTransaction(ctx context.Context, what string, work func(tx *sql.Tx) error) error {
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing %s: %w", what, err)
	}
	return nil
}
