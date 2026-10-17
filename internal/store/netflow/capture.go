package netflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The capture configurations a flow_record provider read (PS18, kept by NJ6 of the amendment "the NetFlow
// join with every interface captured"): runs of reads that found the same configuration, each in force over
// its run, the configuration unknown between two runs that differ. What each column means is in
// schema.sql beside capture_configuration and capture_interface.

// The capture states of an instant: what the configuration in force says of NetFlow there. The widget
// API's measure names an interval by them (docs/widget-api.md, "Named states").
const (
	// CaptureConfigured is a readable configuration with the export enabled and at least one interface
	// captured: records can be joined and directions measured. opnview's own term: no product names it.
	CaptureConfigured = "capture_configured"
	// CaptureNotConfigured is a readable configuration that captures no interface, or whose export is off.
	CaptureNotConfigured = "netflow_not_configured"
	// CaptureUnknown is an instant whose configuration is not known: before the first read, between two
	// differing reads, or in a run of failed reads.
	CaptureUnknown = "capture_configuration_unknown"
)

// unitSeparator is the text the store joins an interface's parent_of with (chr(31) in SQL): a control
// character no identifier holds.
const unitSeparator = "\x1f"

// The values of a captured interface's exported (NJ2).
const (
	ExportedYes     = "yes"
	ExportedNo      = "no"
	ExportedUnknown = "unknown"
)

// CaptureInterface is one interface a capture configuration names.
type CaptureInterface struct {
	Identifier string
	Ingress    bool
	Egress     bool
	// Exported is ExportedYes, ExportedNo or ExportedUnknown: whether the exporter exports what is
	// configured for the interface, for each captured direction.
	Exported string
	// NotExportedReason names why, when Exported is ExportedNo.
	NotExportedReason string
	// ParentOf are the interfaces whose received packets this interface's exporter also exports with
	// this interface as ingress_interface, in identifier order.
	ParentOf []string
}

// Exports reports whether the interface's exporter exports a direction: captured, and exported.
func (i CaptureInterface) Exports(ingress bool) bool {
	if i.Exported != ExportedYes {
		return false
	}
	if ingress {
		return i.Ingress
	}
	return i.Egress
}

// CaptureConfiguration is one run of consecutive reads that found the same configuration: in force
// over [FirstReadAt, LastReadAt].
type CaptureConfiguration struct {
	ID          int64
	ProviderID  int64
	FirstReadAt int64
	LastReadAt  int64
	// Readable is false for a read that failed; Outcome and Detail then say what the broker answered.
	Readable bool
	Outcome  vocab.AvailabilityOutcome
	Detail   string
	// Enabled, ExportVersion, the timeouts and the interfaces are what a readable read found.
	Enabled                bool
	ExportVersion          string
	ActiveTimeoutSeconds   *int64
	InactiveTimeoutSeconds *int64
	Interfaces             []CaptureInterface
}

// Fingerprint is a readable configuration as one canonical text: two reads are the same configuration
// exactly when their fingerprints are equal.
func (c CaptureConfiguration) Fingerprint() string {
	interfaces := append([]CaptureInterface(nil), c.Interfaces...)
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Identifier < interfaces[j].Identifier })
	var text strings.Builder
	optional := func(value *int64) string {
		if value == nil {
			return "-"
		}
		return strconv.FormatInt(*value, 10)
	}
	fmt.Fprintf(&text, "enabled=%t;version=%s;active=%s;inactive=%s", c.Enabled, c.ExportVersion,
		optional(c.ActiveTimeoutSeconds), optional(c.InactiveTimeoutSeconds))
	for _, captured := range interfaces {
		parents := append([]string(nil), captured.ParentOf...)
		sort.Strings(parents)
		fmt.Fprintf(&text, ";%s:%t:%t:%s:%s:%s", captured.Identifier, captured.Ingress, captured.Egress,
			captured.Exported, captured.NotExportedReason, strings.Join(parents, "+"))
	}
	return text.String()
}

// Captured returns the interfaces with at least one direction captured.
func (c CaptureConfiguration) Captured() []CaptureInterface {
	var captured []CaptureInterface
	for _, candidate := range c.Interfaces {
		if candidate.Ingress || candidate.Egress {
			captured = append(captured, candidate)
		}
	}
	return captured
}

// Interface returns the captured interface of an identifier, and whether the configuration names it.
func (c CaptureConfiguration) Interface(identifier string) (CaptureInterface, bool) {
	for _, candidate := range c.Interfaces {
		if candidate.Identifier == identifier && identifier != "" {
			return candidate, true
		}
	}
	return CaptureInterface{}, false
}

// SequenceCounters is how many sequence counters the configuration lets an exporter run under one
// observation domain: the interfaces it reports as exporting (NJ7). 0 when that is not known -- an
// interface whose export is unknown -- and no sequence gap is then asserted.
func (c CaptureConfiguration) SequenceCounters() int {
	counters := 0
	for _, captured := range c.Captured() {
		switch captured.Exported {
		case ExportedUnknown:
			return 0
		case ExportedYes:
			counters++
		}
	}
	return counters
}

// LossDetectable says whether a loss would have been recorded as a gap under the configuration: its
// sequence counters are bounded (NJ7).
func (c *CaptureConfiguration) LossDetectable() bool {
	return c != nil && c.Readable && c.Enabled && c.SequenceCounters() > 0
}

// CaptureConfigurationAt returns the configuration in force at an instant among a provider's
// configurations, oldest first (PS18): the row whose reads hold the instant; after the newest read,
// the newest row; nil before the first read, between the last read showing one configuration and
// the first showing another, and on a row whose read failed -- every instant whose configuration is
// unknown.
func CaptureConfigurationAt(configurations []CaptureConfiguration, at int64) *CaptureConfiguration {
	if len(configurations) == 0 || at < configurations[0].FirstReadAt {
		return nil
	}
	readable := func(row *CaptureConfiguration) *CaptureConfiguration {
		if !row.Readable {
			return nil
		}
		return row
	}
	for index := range configurations {
		row := &configurations[index]
		if at >= row.FirstReadAt && at <= row.LastReadAt {
			return readable(row)
		}
		if index+1 < len(configurations) && at > row.LastReadAt && at < configurations[index+1].FirstReadAt {
			// Between the last read showing one configuration and the first showing another.
			return nil
		}
	}
	return readable(&configurations[len(configurations)-1])
}

// CaptureStateAt is the capture state at an instant, from what is stored, with the configuration in
// force: CaptureConfigured, CaptureNotConfigured or CaptureUnknown.
func CaptureStateAt(configurations []CaptureConfiguration, at int64) (string, *CaptureConfiguration) {
	configuration := CaptureConfigurationAt(configurations, at)
	switch {
	case configuration == nil:
		return CaptureUnknown, nil
	case !configuration.Enabled || len(configuration.Captured()) == 0:
		return CaptureNotConfigured, configuration
	}
	return CaptureConfigured, configuration
}

// CaptureChangePoints are the instants inside [from, to) at which CaptureStateAt may change, from with
// them: the state is constant from one to the next.
func CaptureChangePoints(configurations []CaptureConfiguration, from, to int64) []int64 {
	points := map[int64]bool{from: true}
	add := func(at int64) {
		if at > from && at < to {
			points[at] = true
		}
	}
	for _, row := range configurations {
		add(row.FirstReadAt)
		add(row.LastReadAt + 1)
	}
	sorted := make([]int64, 0, len(points))
	for at := range points {
		sorted = append(sorted, at)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

// RecordCaptureRead records one read of a provider's capture configuration at readAt: a read equal to
// the newest row's -- the same fingerprint, or the same failure -- extends it; any other opens a row.
// It reports whether it opened one.
func (s *NetflowStore) RecordCaptureRead(ctx context.Context, providerID, readAt int64, read CaptureConfiguration) (
	bool, error) {
	if !read.Readable && read.Outcome == "" {
		return false, errors.New("store: an unreadable capture configuration names no outcome")
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: recording a capture read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var (
		latestID                  int64
		latestLast                int64
		latestPrint, latestReason sql.NullString
	)
	err = transaction.QueryRowContext(ctx,
		`SELECT id, last_read_at, fingerprint, outcome FROM capture_configuration
		 WHERE provider_id = $1 ORDER BY first_read_at DESC LIMIT 1 FOR UPDATE`, providerID).
		Scan(&latestID, &latestLast, &latestPrint, &latestReason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("store: reading the newest capture configuration: %w", err)
	}
	found := err == nil
	if found && readAt < latestLast {
		// A read older than the newest one recorded changes nothing: the history is written forward.
		return false, transaction.Commit()
	}
	fingerprint := ""
	if read.Readable {
		fingerprint = read.Fingerprint()
	}
	same := found && ((read.Readable && latestPrint.Valid && latestPrint.String == fingerprint) ||
		(!read.Readable && !latestPrint.Valid && latestReason.String == string(read.Outcome)))
	if same {
		if _, err := transaction.ExecContext(ctx,
			"UPDATE capture_configuration SET last_read_at = $1, detail = $2 WHERE id = $3",
			readAt, sqldb.NullableText(read.Detail), latestID); err != nil {
			return false, fmt.Errorf("store: extending the capture configuration: %w", err)
		}
		return false, transaction.Commit()
	}
	var id int64
	if read.Readable {
		err = transaction.QueryRowContext(ctx,
			`INSERT INTO capture_configuration (provider_id, first_read_at, last_read_at, fingerprint, enabled,
			     export_version, active_timeout_seconds, inactive_timeout_seconds, detail)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			providerID, readAt, readAt, fingerprint, read.Enabled, sqldb.NullableText(read.ExportVersion),
			sqldb.Optional(read.ActiveTimeoutSeconds), sqldb.Optional(read.InactiveTimeoutSeconds),
			sqldb.NullableText(read.Detail)).Scan(&id)
	} else {
		err = transaction.QueryRowContext(ctx,
			`INSERT INTO capture_configuration (provider_id, first_read_at, last_read_at, outcome, detail)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			providerID, readAt, readAt, string(read.Outcome), sqldb.NullableText(read.Detail)).Scan(&id)
	}
	if err != nil {
		return false, fmt.Errorf("store: recording a capture configuration: %w", err)
	}
	if read.Readable {
		if err := insertCaptureInterfaces(ctx, transaction, id, read.Interfaces); err != nil {
			return false, err
		}
	}
	if err := recordMeasureIntervals(ctx, transaction, providerID); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, fmt.Errorf("store: committing a capture read: %w", err)
	}
	return true, nil
}

// insertCaptureInterfaces writes the interfaces of a configuration row and their parent_of.
func insertCaptureInterfaces(ctx context.Context, transaction *sql.Tx, id int64, interfaces []CaptureInterface) error {
	for _, captured := range interfaces {
		exported := captured.Exported
		if exported == "" {
			exported = ExportedUnknown
		}
		var reason any
		if exported == ExportedNo {
			reason = captured.NotExportedReason
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO capture_interface (capture_configuration_id, identifier, ingress, egress, exported,
			     not_exported_reason)
			 VALUES ($1, $2, $3, $4, $5, $6)`, id, captured.Identifier, captured.Ingress, captured.Egress, exported,
			reason); err != nil {
			return fmt.Errorf("store: recording a captured interface: %w", err)
		}
		for _, of := range captured.ParentOf {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO capture_interface_parent_of (capture_configuration_id, identifier, of_identifier)
				 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, captured.Identifier, of); err != nil {
				return fmt.Errorf("store: recording a parent_of: %w", err)
			}
		}
	}
	return nil
}

// CaptureConfigurations returns a provider's capture configurations, oldest first.
func (s *NetflowStore) CaptureConfigurations(ctx context.Context, providerID int64) ([]CaptureConfiguration, error) {
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT id, first_read_at, last_read_at, fingerprint, outcome, detail, enabled, export_version,
		        active_timeout_seconds, inactive_timeout_seconds
		 FROM capture_configuration WHERE provider_id = $1 ORDER BY first_read_at`, providerID)
	if err != nil {
		return nil, fmt.Errorf("store: reading the capture configurations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var configurations []CaptureConfiguration
	byID := map[int64]int{}
	for rows.Next() {
		var (
			configuration                CaptureConfiguration
			fingerprint, outcome, detail sql.NullString
			enabled                      sql.NullBool
			active, inactive             sql.NullInt64
			version                      sql.NullString
		)
		if err := rows.Scan(&configuration.ID, &configuration.FirstReadAt, &configuration.LastReadAt, &fingerprint,
			&outcome, &detail, &enabled, &version, &active, &inactive); err != nil {
			return nil, fmt.Errorf("store: reading a capture configuration: %w", err)
		}
		configuration.ProviderID = providerID
		configuration.Readable = fingerprint.Valid
		configuration.Outcome = vocab.AvailabilityOutcome(outcome.String)
		configuration.Detail = detail.String
		configuration.Enabled = enabled.Valid && enabled.Bool
		configuration.ExportVersion = version.String
		configuration.ActiveTimeoutSeconds = sqldb.NullInt(active)
		configuration.InactiveTimeoutSeconds = sqldb.NullInt(inactive)
		byID[configuration.ID] = len(configurations)
		configurations = append(configurations, configuration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the capture configurations: %w", err)
	}
	_ = rows.Close()
	if err := s.readCaptureInterfaces(ctx, providerID, configurations, byID); err != nil {
		return nil, err
	}
	return configurations, nil
}

// readCaptureInterfaces fills the interfaces of a provider's configurations, located by id.
func (s *NetflowStore) readCaptureInterfaces(ctx context.Context, providerID int64,
	configurations []CaptureConfiguration, byID map[int64]int) error {
	interfaces, err := s.SQL.QueryContext(ctx,
		`SELECT i.capture_configuration_id, i.identifier, i.ingress, i.egress, i.exported,
		        coalesce(i.not_exported_reason, ''),
		        coalesce((SELECT string_agg(p.of_identifier, chr(31) ORDER BY p.of_identifier)
		                  FROM capture_interface_parent_of AS p
		                  WHERE p.capture_configuration_id = i.capture_configuration_id
		                    AND p.identifier = i.identifier), '')
		 FROM capture_interface AS i
		 JOIN capture_configuration AS c ON c.id = i.capture_configuration_id
		 WHERE c.provider_id = $1 ORDER BY i.capture_configuration_id, i.identifier`, providerID)
	if err != nil {
		return fmt.Errorf("store: reading the captured interfaces: %w", err)
	}
	defer func() { _ = interfaces.Close() }()
	for interfaces.Next() {
		var (
			id                                          int64
			ingress, egress                             bool
			identifier, exported, reason, parentsJoined string
		)
		if err := interfaces.Scan(&id, &identifier, &ingress, &egress, &exported, &reason, &parentsJoined); err != nil {
			return fmt.Errorf("store: reading a captured interface: %w", err)
		}
		index, known := byID[id]
		if !known {
			continue
		}
		captured := CaptureInterface{Identifier: identifier, Ingress: ingress, Egress: egress,
			Exported: exported, NotExportedReason: reason}
		if parentsJoined != "" {
			captured.ParentOf = strings.Split(parentsJoined, unitSeparator)
		}
		configurations[index].Interfaces = append(configurations[index].Interfaces, captured)
	}
	return interfaces.Err()
}

// recordMeasureIntervals rewrites a provider's window intervals of the netflow measure from its capture
// configurations (measure_interval, PA5 of the plugin-API amendment): each configuration is in force from
// its first read until the next one's first read, or, when the next one began after its last read, until
// the instant after its last read -- after which nothing is recorded, the measure's unrecorded state -- and
// the newest for as long as no later read says otherwise. A configuration whose read failed is
// capture_configuration_unknown; a readable one is capture_configured when its export is enabled and it
// captures an interface in some direction, and netflow_not_configured otherwise: CaptureStateAt's states,
// recorded so that the widget measure reads them without reading this package's tables.
func recordMeasureIntervals(ctx context.Context, transaction *sql.Tx, providerID int64) error {
	if _, err := transaction.ExecContext(ctx,
		"DELETE FROM measure_interval WHERE measure = $1 AND provider_id = $2", MeasureName, providerID); err != nil {
		return fmt.Errorf("store: rewriting the measure's intervals: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO measure_interval (measure, provider_id, starts_at, ends_at, state)
		 SELECT $1, c.provider_id, c.first_read_at,
		        CASE WHEN lead(c.first_read_at) OVER w IS NULL THEN NULL ELSE c.last_read_at + 1 END,
		        CASE WHEN c.fingerprint IS NULL THEN $3
		             WHEN coalesce(c.enabled, false)
		                  AND EXISTS (SELECT 1 FROM capture_interface AS i
		                              WHERE i.capture_configuration_id = c.id AND (i.ingress OR i.egress)) THEN $4
		             ELSE $5 END
		 FROM capture_configuration AS c
		 WHERE c.provider_id = $2
		 WINDOW w AS (ORDER BY c.first_read_at)`,
		MeasureName, providerID, CaptureUnknown, CaptureConfigured, CaptureNotConfigured); err != nil {
		return fmt.Errorf("store: recording the measure's intervals: %w", err)
	}
	return nil
}

// RecordMeasureIntervals rewrites every provider's window intervals of the netflow measure from its
// capture configurations, as each capture read rewrites its own provider's (recordMeasureIntervals): what
// a fixture writing configurations straight into the tables runs after it.
func (s *NetflowStore) RecordMeasureIntervals(ctx context.Context) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: rewriting the measure's intervals: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	rows, err := transaction.QueryContext(ctx, "SELECT DISTINCT provider_id FROM capture_configuration ORDER BY 1")
	if err != nil {
		return fmt.Errorf("store: reading the capture providers: %w", err)
	}
	var providers []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: reading the capture providers: %w", err)
		}
		providers = append(providers, id)
	}
	_ = rows.Close()
	if _, err := transaction.ExecContext(ctx, "DELETE FROM measure_interval WHERE measure = $1", MeasureName); err != nil {
		return fmt.Errorf("store: rewriting the measure's intervals: %w", err)
	}
	for _, provider := range providers {
		if err := recordMeasureIntervals(ctx, transaction, provider); err != nil {
			return err
		}
	}
	return transaction.Commit()
}
