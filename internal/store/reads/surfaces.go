package reads

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The read paths of the Interfaces and Sources surfaces.
//
// EVERY FUNCTION HERE READS AND NONE WRITES. The interface networks are edited through
// internal/store/flows/network.go's write path, and availability and gaps are written by the
// collectors; what these return is what those wrote, for a screen to draw. They are kept in a file
// of their own so the surfaces' reads are one place, and so the step-7 Source availability widget
// can reuse the same read the server-rendered Sources surface draws from.

// ProbeNotYetProbed is the probe the schema writes into every availability row
// before any probe has run (internal/store/schema.sql, the source_availability
// seed). A row still carrying it has never been checked: it is "not yet probed",
// and never an outage.
const ProbeNotYetProbed = "not_yet_probed"

// InterfaceRow is one interface as the Interfaces surface shows it: what discovery
// read, whether a gateway sits behind it, and the operator's link-local rule.
type InterfaceRow struct {
	// ID is interface.id.
	ID int64
	// Identifier is the configuration key OPNsense stores the interface under.
	Identifier string
	// Device is the network device the interface runs on.
	Device string
	// Description is the description entered in OPNsense.
	Description string
	// IsUpstream is interface.is_upstream: discovery read a gateway behind it.
	IsUpstream bool
	// LinkLocalEvidence is the operator's rule: whether an IPv6 link-local address
	// seen on the interface is evidence that it belongs there.
	LinkLocalEvidence bool
}

// Interfaces returns every interface row, ordered by its id.
//
// THE ORDER INTERPRETS NOTHING. The id is the order discovery first stored the
// interfaces in, which is stable across refreshes and carries no meaning; sorting
// by a description or an identifier would rank interfaces by what somebody typed,
// and no name decides anything in opnview.
func (s *ReadStore) Interfaces(ctx context.Context) ([]InterfaceRow, error) {
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT id, identifier, device, description, is_upstream, link_local_evidence
		 FROM interface ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing the interfaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var interfaces []InterfaceRow
	for rows.Next() {
		var (
			row                 InterfaceRow
			upstream, linkLocal int64
		)
		if err := rows.Scan(&row.ID, &row.Identifier, &row.Device, &row.Description,
			&upstream, &linkLocal); err != nil {
			return nil, fmt.Errorf("store: listing the interfaces: %w", err)
		}
		row.IsUpstream = upstream == 1
		row.LinkLocalEvidence = linkLocal == 1
		interfaces = append(interfaces, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing the interfaces: %w", err)
	}
	return interfaces, nil
}

// SourceRow is one registered provider with its availability, exactly the columns
// the query `-- diagnostic: Source availability` of sql/queries/diagnostics.sql
// returns.
type SourceRow struct {
	// Kind and ProviderKey identify the registry row.
	Kind        string
	ProviderKey string
	// DisplayName is provider.display_name.
	DisplayName string
	// HasAvailability is false when the provider has no availability row at all,
	// which the schema prevents and a screen still has to name: "state unknown",
	// never healthy.
	HasAvailability bool
	// State, Outcome, Probe, Detail and CheckedAt are the availability row's columns.
	// Outcome is the closed code an interface translates (internal/store/vocab/outcome.go); Detail
	// is free text written by a collector; HasDetail is false when it is NULL.
	State     vocab.AvailabilityState
	Outcome   vocab.AvailabilityOutcome
	Probe     string
	Detail    string
	HasDetail bool
	CheckedAt int64
	// IsActive is whether opnview reads this provider for its kind.
	IsActive bool
}

// NotYetProbed reports whether the row has never been checked: the seed's
// unavailable state with the seed's probe.
func (r SourceRow) NotYetProbed() bool {
	return r.HasAvailability && r.State == vocab.StateUnavailable && r.Probe == ProbeNotYetProbed
}

// SourceAvailability returns one row per registered provider, ordered by kind and
// provider key: the query `-- diagnostic: Source availability`, unchanged, with the
// LEFT JOIN that keeps a provider whose availability row is missing.
//
// The operator's selection of each row is not joined here. It is a setting row
// whose key internal/config composes (config.KeySourceSelection), and that package
// is the one place the key's shape is written; a caller reads it through
// config.LoadSourceSelection, which also refuses a selection that is not one of
// the three words.
func (s *ReadStore) SourceAvailability(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT p.kind, p.provider_key, p.display_name,
		        a.provider_id IS NOT NULL, coalesce(a.state, ''), coalesce(a.outcome, ''),
		        coalesce(a.probe, ''),
		        a.detail, coalesce(a.checked_at, 0), p.is_active
		 FROM provider AS p
		 LEFT JOIN source_availability AS a ON a.provider_id = p.id
		 ORDER BY p.kind, p.provider_key`)
	if err != nil {
		return nil, fmt.Errorf("store: reading the source availability: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var sources []SourceRow
	for rows.Next() {
		var (
			row     SourceRow
			state   string
			outcome string
			detail  sql.NullString
			active  int64
		)
		if err := rows.Scan(&row.Kind, &row.ProviderKey, &row.DisplayName, &row.HasAvailability,
			&state, &outcome, &row.Probe, &detail, &row.CheckedAt, &active); err != nil {
			return nil, fmt.Errorf("store: reading the source availability: %w", err)
		}
		row.State = vocab.AvailabilityState(state)
		row.Outcome = vocab.AvailabilityOutcome(outcome)
		row.Detail, row.HasDetail = detail.String, detail.Valid
		row.IsActive = active == 1
		sources = append(sources, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the source availability: %w", err)
	}
	return sources, nil
}

// RecentGap is one collection gap, with the registry row it was recorded against.
type RecentGap struct {
	// Kind and ProviderKey identify the provider.
	Kind        string
	ProviderKey string
	// Reason is the schema's closed vocabulary.
	Reason vocab.GapReason
	// IntervalStartAt and IntervalEndAt bound what was missed; DetectedAt is when
	// opnview found out.
	IntervalStartAt int64
	IntervalEndAt   int64
	DetectedAt      int64
	// Detail is the free text the detection wrote; HasDetail is false when NULL.
	Detail    string
	HasDetail bool
}

// MissedSeconds is the length of the interval.
func (g RecentGap) MissedSeconds() int64 { return g.IntervalEndAt - g.IntervalStartAt }

// recentGapsStatement is the read RecentCollectionGaps runs, written once so a test
// can ask PostgreSQL for its query plan rather than for a copy of it.
//
// IT READS THROUGH idx_collection_gap_detected_at. The range on detected_at is the
// index's own column and the order is the index's order read backwards, so the plan
// is a search of that index and never a scan of the table: collection_gap is a
// growing table, and the screen reads one day of it.
const RecentGapsStatement = `SELECT p.kind, p.provider_key, collection_gap.reason,
        collection_gap.interval_start_at, collection_gap.interval_end_at,
        collection_gap.detected_at, collection_gap.detail
 FROM collection_gap
 JOIN provider AS p ON p.id = collection_gap.provider_id
 WHERE collection_gap.detected_at >= ?
 ORDER BY collection_gap.detected_at DESC, collection_gap.id DESC`

// RecentCollectionGaps returns every gap detected at or after since, newest first.
func (s *ReadStore) RecentCollectionGaps(ctx context.Context, since int64) ([]RecentGap, error) {
	rows, err := s.SQL.QueryContext(ctx, RecentGapsStatement, since)
	if err != nil {
		return nil, fmt.Errorf("store: reading the recent collection gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var gaps []RecentGap
	for rows.Next() {
		var (
			gap    RecentGap
			reason string
			detail sql.NullString
		)
		if err := rows.Scan(&gap.Kind, &gap.ProviderKey, &reason, &gap.IntervalStartAt,
			&gap.IntervalEndAt, &gap.DetectedAt, &detail); err != nil {
			return nil, fmt.Errorf("store: reading the recent collection gaps: %w", err)
		}
		gap.Reason = vocab.GapReason(reason)
		gap.Detail, gap.HasDetail = detail.String, detail.Valid
		gaps = append(gaps, gap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the recent collection gaps: %w", err)
	}
	return gaps, nil
}
