package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The NetFlow side of the store (PS17-PS20 of the NetFlow join amendment to
// specs/SPEC-backend-completion-plugins.md): the capture configurations a flow_record provider read,
// the conditions its stream showed, the flow records themselves, the join's reads and writes, and the
// logged connections' measured volume. What each table means is in schema.sql beside it and in
// docs/data-model.md, "NetFlow volumes"; what is decided from them is internal/collect's.

// The capture states a flow record is stored with: the state of the capture configuration in force at
// its start (PS18). Only CaptureObservationPoint gives it an Observation Point.
const (
	CaptureObservationPoint       = "observation_point"
	CaptureNotConfigured          = "netflow_not_configured"
	CaptureObservationPointHidden = "observation_point_not_exported"
	CaptureUnknown                = "capture_configuration_unknown"
	CaptureIndexUnmapped          = "interface_index_unmapped"
)

// The join states of a flow record (PS19).
const (
	JoinPending     = "pending"
	JoinJoined      = "joined"
	JoinUnmatched   = "unmatched"
	JoinNotJoinable = "not_joinable"
)

// The unmatched reasons, a closed list (PS19 rule 7).
const (
	UnmatchedNoLoggedLeg       = "no_logged_leg"
	UnmatchedAmbiguous         = "ambiguous"
	UnmatchedBeyondFlowHorizon = "beyond_flow_horizon"
)

// The per-direction states of a connection's measured volume (PS20). The interval states of PS18 are
// the capture states above, read from the configuration rather than stored here.
const (
	VolumeMeasured               = "measured"
	VolumeInProgress             = "in_progress"
	VolumeAwaitingExport         = "awaiting_export"
	VolumeMissing                = "missing"
	VolumeNotCaptured            = "not_captured"
	VolumeExportGap              = "export_gap"
	VolumeNotExportedWhenRefused = "not_exported_when_refused"
	MeasuredAtFirstLeg           = "first_leg"
	MeasuredAtSecondLeg          = "second_leg"
	OrientationForward           = "forward"
	OrientationReverse           = "reverse"
	DirectionIngress             = "ingress"
	DirectionEgress              = "egress"
)

// The NetFlow settings the store reads or writes defaults for (schema.sql).
const (
	// NetflowJoinToleranceKey is how far after a leg was logged a flow record may start and still
	// join it, in seconds.
	NetflowJoinToleranceKey = "netflow_join_tolerance_seconds"
	// DefaultNetflowJoinToleranceSeconds is its default, which internal/config justifies.
	DefaultNetflowJoinToleranceSeconds int64 = 3
	// FlowRecordRetentionKey is how long a flow record is kept, in seconds; 0 follows
	// retention_seconds.
	FlowRecordRetentionKey = "retention_flow_record_seconds"
)

// CaptureInterface is one interface a capture configuration captures on.
type CaptureInterface struct {
	Identifier string
	Ingress    bool
	Egress     bool
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
	Outcome  AvailabilityOutcome
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
		fmt.Fprintf(&text, ";%s:%t:%t", captured.Identifier, captured.Ingress, captured.Egress)
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

// CaptureRead is what RecordCaptureRead did.
type CaptureRead struct {
	// Opened says the read found a configuration other than the newest row's, and opened a row.
	Opened bool
	// ResolvedConditions counts the stream conditions this read ended the suspension of.
	ResolvedConditions int64
}

// RecordCaptureRead records one read of a provider's capture configuration at readAt: a read equal to
// the newest row's -- the same fingerprint, or the same failure -- extends it; any other opens a row.
// The same transaction ends every suspension a stream condition began before the read (PS18).
func (s *Store) RecordCaptureRead(ctx context.Context, providerID, readAt int64, read CaptureConfiguration) (
	CaptureRead, error) {
	var done CaptureRead
	if !read.Readable && read.Outcome == "" {
		return done, errors.New("store: an unreadable capture configuration names no outcome")
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return done, fmt.Errorf("store: recording a capture read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var (
		latestID                  int64
		latestLast                int64
		latestPrint, latestReason sql.NullString
	)
	err = transaction.QueryRowContext(ctx,
		`SELECT id, last_read_at, fingerprint, outcome FROM capture_configuration
		 WHERE provider_id = ? ORDER BY first_read_at DESC LIMIT 1 FOR UPDATE`, providerID).
		Scan(&latestID, &latestLast, &latestPrint, &latestReason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return done, fmt.Errorf("store: reading the newest capture configuration: %w", err)
	}
	found := err == nil
	fingerprint := ""
	if read.Readable {
		fingerprint = read.Fingerprint()
	}
	same := found && readAt >= latestLast &&
		((read.Readable && latestPrint.Valid && latestPrint.String == fingerprint) ||
			(!read.Readable && !latestPrint.Valid && latestReason.String == string(read.Outcome)))
	if found && readAt < latestLast {
		// A read older than the newest one recorded changes nothing: the history is written forward.
		return done, transaction.Commit()
	}
	if same {
		if _, err := transaction.ExecContext(ctx,
			"UPDATE capture_configuration SET last_read_at = ?, detail = ? WHERE id = ?",
			readAt, nullableText(read.Detail), latestID); err != nil {
			return done, fmt.Errorf("store: extending the capture configuration: %w", err)
		}
	} else {
		var id int64
		if read.Readable {
			err = transaction.QueryRowContext(ctx,
				`INSERT INTO capture_configuration (provider_id, first_read_at, last_read_at, fingerprint, enabled,
				     export_version, active_timeout_seconds, inactive_timeout_seconds, detail)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				providerID, readAt, readAt, fingerprint, read.Enabled, nullableText(read.ExportVersion),
				optional(read.ActiveTimeoutSeconds), optional(read.InactiveTimeoutSeconds),
				nullableText(read.Detail)).Scan(&id)
		} else {
			err = transaction.QueryRowContext(ctx,
				`INSERT INTO capture_configuration (provider_id, first_read_at, last_read_at, outcome, detail)
				 VALUES (?, ?, ?, ?, ?) RETURNING id`,
				providerID, readAt, readAt, string(read.Outcome), nullableText(read.Detail)).Scan(&id)
		}
		if err != nil {
			return done, fmt.Errorf("store: recording a capture configuration: %w", err)
		}
		for _, captured := range read.Interfaces {
			if !read.Readable {
				break
			}
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO capture_interface (capture_configuration_id, identifier, ingress, egress)
				 VALUES (?, ?, ?, ?)`, id, captured.Identifier, captured.Ingress, captured.Egress); err != nil {
				return done, fmt.Errorf("store: recording a captured interface: %w", err)
			}
		}
		done.Opened = true
	}
	result, err := transaction.ExecContext(ctx,
		`UPDATE flow_record_condition SET resolved_at = ?
		 WHERE provider_id = ? AND resolved_at IS NULL AND observed_at <= ?`, readAt, providerID, readAt)
	if err != nil {
		return done, fmt.Errorf("store: ending the suspensions of stream conditions: %w", err)
	}
	done.ResolvedConditions, _ = result.RowsAffected()
	if err := transaction.Commit(); err != nil {
		return done, fmt.Errorf("store: committing a capture read: %w", err)
	}
	return done, nil
}

// CaptureConfigurations returns a provider's capture configurations, oldest first.
func (s *Store) CaptureConfigurations(ctx context.Context, providerID int64) ([]CaptureConfiguration, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, first_read_at, last_read_at, fingerprint, outcome, detail, enabled, export_version,
		        active_timeout_seconds, inactive_timeout_seconds
		 FROM capture_configuration WHERE provider_id = ? ORDER BY first_read_at`, providerID)
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
			enabled, active, inactive    sql.NullInt64
			version                      sql.NullString
		)
		if err := rows.Scan(&configuration.ID, &configuration.FirstReadAt, &configuration.LastReadAt, &fingerprint,
			&outcome, &detail, &enabled, &version, &active, &inactive); err != nil {
			return nil, fmt.Errorf("store: reading a capture configuration: %w", err)
		}
		configuration.ProviderID = providerID
		configuration.Readable = fingerprint.Valid
		configuration.Outcome = AvailabilityOutcome(outcome.String)
		configuration.Detail = detail.String
		configuration.Enabled = enabled.Valid && enabled.Int64 == 1
		configuration.ExportVersion = version.String
		configuration.ActiveTimeoutSeconds = nullInt(active)
		configuration.InactiveTimeoutSeconds = nullInt(inactive)
		byID[configuration.ID] = len(configurations)
		configurations = append(configurations, configuration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the capture configurations: %w", err)
	}
	_ = rows.Close()
	interfaces, err := s.db.QueryContext(ctx,
		`SELECT i.capture_configuration_id, i.identifier, i.ingress, i.egress
		 FROM capture_interface AS i
		 JOIN capture_configuration AS c ON c.id = i.capture_configuration_id
		 WHERE c.provider_id = ? ORDER BY i.capture_configuration_id, i.identifier`, providerID)
	if err != nil {
		return nil, fmt.Errorf("store: reading the captured interfaces: %w", err)
	}
	defer func() { _ = interfaces.Close() }()
	for interfaces.Next() {
		var id, ingress, egress int64
		var identifier string
		if err := interfaces.Scan(&id, &identifier, &ingress, &egress); err != nil {
			return nil, fmt.Errorf("store: reading a captured interface: %w", err)
		}
		if index, known := byID[id]; known {
			configurations[index].Interfaces = append(configurations[index].Interfaces,
				CaptureInterface{Identifier: identifier, Ingress: ingress == 1, Egress: egress == 1})
		}
	}
	return configurations, interfaces.Err()
}

// FlowRecordCondition is a condition the decoder detected on a provider's stream, and when the next
// configuration read ended the suspension it began.
type FlowRecordCondition struct {
	Reason     string
	Scope      string
	ObservedAt int64
	ResolvedAt *int64
}

// RecordFlowRecordCondition records a condition of a provider's stream.
func (s *Store) RecordFlowRecordCondition(ctx context.Context, providerID int64, reason, scope string,
	observedAt int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO flow_record_condition (provider_id, reason, scope, observed_at) VALUES (?, ?, ?, ?)`,
		providerID, reason, scope, observedAt); err != nil {
		return fmt.Errorf("store: recording a stream condition: %w", err)
	}
	return nil
}

// FlowRecordConditions returns a provider's stream conditions, oldest first.
func (s *Store) FlowRecordConditions(ctx context.Context, providerID int64) ([]FlowRecordCondition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT reason, scope, observed_at, resolved_at FROM flow_record_condition
		 WHERE provider_id = ? AND observed_at >= 0 ORDER BY observed_at, id`, providerID)
	if err != nil {
		return nil, fmt.Errorf("store: reading the stream conditions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var conditions []FlowRecordCondition
	for rows.Next() {
		var condition FlowRecordCondition
		var resolved sql.NullInt64
		if err := rows.Scan(&condition.Reason, &condition.Scope, &condition.ObservedAt, &resolved); err != nil {
			return nil, fmt.Errorf("store: reading a stream condition: %w", err)
		}
		condition.ResolvedAt = nullInt(resolved)
		conditions = append(conditions, condition)
	}
	return conditions, rows.Err()
}

// SuspendFlowRecords takes their Observation Point from a provider's undecided records that started
// at or after an instant: the stream contradicted the configuration they were stored under (PS18).
// A decided record is never changed.
func (s *Store) SuspendFlowRecords(ctx context.Context, providerID, from int64) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE flow_record
		 SET capture_state = 'capture_configuration_unknown', observation_point_identifier = NULL,
		     observation_point_device = NULL, direction = NULL, loss_detectable = 0, join_state = 'not_joinable'
		 WHERE provider_id = ? AND join_state = 'pending' AND start_at >= ?`, providerID, from)
	if err != nil {
		return 0, fmt.Errorf("store: suspending the join: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

// FlowRecordRow is one flow_record row.
type FlowRecordRow struct {
	ProviderID                 int64
	Identity                   string
	StartAt                    int64
	StartMillisecond           int64
	EndAt                      int64
	EndMillisecond             int64
	ReceivedAt                 int64
	ExporterAddress            string
	ObservationDomain          int64
	IngressInterface           *int64
	EgressInterface            *int64
	IPVersion                  int64
	Protocol                   int64
	SrcAddress                 string
	DstAddress                 string
	SrcPort                    *int64
	DstPort                    *int64
	TCPFlags                   *int64
	Octets                     int64
	Packets                    int64
	CaptureState               string
	CapturedInterfaces         *int64
	ObservationPointIdentifier *string
	ObservationPointDevice     *string
	Direction                  *string
	LossDetectable             bool
	TupleKey                   string
	JoinState                  string
	JoinedFlowID               *int64
	Orientation                *string
	UnmatchedReason            *string
	DecidedAt                  *int64
}

// InsertFlowRecords stores flow records in one transaction; a record already stored -- the same
// identity and start -- is left as it is. It returns how many were new.
func (s *Store) InsertFlowRecords(ctx context.Context, records []FlowRecordRow) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: storing flow records: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	statement, err := transaction.PrepareContext(ctx,
		`INSERT INTO flow_record (provider_id, identity, start_at, start_millisecond, end_at, end_millisecond,
		     received_at, exporter_address, observation_domain, ingress_interface, egress_interface, ip_version,
		     protocol, src_address, dst_address, src_port, dst_port, tcp_flags, octets, packets, capture_state,
		     captured_interfaces, observation_point_identifier, observation_point_device, direction,
		     loss_detectable, tuple_key, join_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (identity, start_at) DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("store: preparing the flow-record insert: %w", err)
	}
	defer func() { _ = statement.Close() }()
	var inserted int64
	for _, record := range records {
		result, err := statement.ExecContext(ctx, record.ProviderID, record.Identity, record.StartAt,
			record.StartMillisecond, record.EndAt, record.EndMillisecond, record.ReceivedAt, record.ExporterAddress,
			record.ObservationDomain, optional(record.IngressInterface), optional(record.EgressInterface),
			record.IPVersion, record.Protocol, record.SrcAddress, record.DstAddress, optional(record.SrcPort),
			optional(record.DstPort), optional(record.TCPFlags), record.Octets, record.Packets, record.CaptureState,
			optional(record.CapturedInterfaces), optionalText(record.ObservationPointIdentifier),
			optionalText(record.ObservationPointDevice), optionalText(record.Direction), record.LossDetectable,
			record.TupleKey, record.JoinState)
		if err != nil {
			return 0, fmt.Errorf("store: storing a flow record: %w", err)
		}
		affected, _ := result.RowsAffected()
		inserted += affected
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing flow records: %w", err)
	}
	return inserted, nil
}

// FlowRecordGroupKey names one group of the join: an Observation Point's device and an unordered tuple.
type FlowRecordGroupKey struct {
	Device   string
	TupleKey string
}

// PendingFlowRecordGroups returns the groups holding an undecided record of a provider that started
// before a bound.
func (s *Store) PendingFlowRecordGroups(ctx context.Context, providerID, before int64) ([]FlowRecordGroupKey, error) {
	text, err := Statement("flow_record_pending_groups")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, map[string]any{"provider_id": providerID,
		"before": before})...)
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_pending_groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var groups []FlowRecordGroupKey
	for rows.Next() {
		var group FlowRecordGroupKey
		if err := rows.Scan(&group.Device, &group.TupleKey); err != nil {
			return nil, fmt.Errorf("store: flow_record_pending_groups: %w", err)
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// GroupRecord is one record of a group, as the join reads it.
type GroupRecord struct {
	Identity         string
	StartAt          int64
	StartMillisecond int64
	EndAt            int64
	EndMillisecond   int64
	SrcAddress       string
	DstAddress       string
	SrcPort          *int64
	DstPort          *int64
	Protocol         int64
	TCPFlags         *int64
	Direction        string
	JoinState        string
	JoinedFlowID     *int64
	Orientation      *string
	UnmatchedReason  *string
	DecidedAt        *int64
	// ObservationPoint, IPVersion, Octets and Packets are what the unmatched family counts.
	ObservationPoint string
	IPVersion        int64
	Octets           int64
	Packets          int64
}

// FlowRecordGroup returns every record of a group that has an Observation Point, oldest first.
func (s *Store) FlowRecordGroup(ctx context.Context, providerID int64, group FlowRecordGroupKey) (
	[]GroupRecord, error) {
	text, err := Statement("flow_record_group")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, map[string]any{"provider_id": providerID,
		"device": group.Device, "tuple_key": group.TupleKey})...)
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_group: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []GroupRecord
	for rows.Next() {
		var (
			record                              GroupRecord
			srcPort, dstPort, flags, joined, at sql.NullInt64
			direction                           sql.NullString
			orientation, reason                 sql.NullString
		)
		if err := rows.Scan(&record.Identity, &record.StartAt, &record.StartMillisecond, &record.EndAt,
			&record.EndMillisecond, &record.SrcAddress, &record.DstAddress, &srcPort, &dstPort, &record.Protocol,
			&flags, &direction, &record.JoinState, &joined, &orientation, &reason, &at, &record.ObservationPoint,
			&record.IPVersion, &record.Octets, &record.Packets); err != nil {
			return nil, fmt.Errorf("store: flow_record_group: %w", err)
		}
		record.SrcPort, record.DstPort, record.TCPFlags = nullInt(srcPort), nullInt(dstPort), nullInt(flags)
		record.Direction = direction.String
		record.JoinedFlowID, record.DecidedAt = nullInt(joined), nullInt(at)
		record.Orientation, record.UnmatchedReason = nullString(orientation), nullString(reason)
		records = append(records, record)
	}
	return records, rows.Err()
}

// nullString is a nullable text as a pointer.
func nullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copied := value.String
	return &copied
}

// JoinLeg is one filter-log record a flow record may join.
type JoinLeg struct {
	FlowID     int64
	ObservedAt int64
	SrcAddress string
	DstAddress string
	SrcPort    *int64
	DstPort    *int64
	Protocol   string
	Direction  string
	Action     string
}

// JoinLegs returns the legs logged on a device, of a version, whose two addresses are a and b in
// either orientation, logged at or before latest.
func (s *Store) JoinLegs(ctx context.Context, device, a, b string, addressFamily, latest int64) ([]JoinLeg, error) {
	text, err := Statement("flow_record_join_legs")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, map[string]any{"device": device, "address_a": a,
		"address_b": b, "ip_version": addressFamily, "latest": latest})...)
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_join_legs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var legs []JoinLeg
	for rows.Next() {
		var leg JoinLeg
		var srcPort, dstPort sql.NullInt64
		if err := rows.Scan(&leg.FlowID, &leg.ObservedAt, &leg.SrcAddress, &leg.DstAddress, &srcPort, &dstPort,
			&leg.Protocol, &leg.Direction, &leg.Action); err != nil {
			return nil, fmt.Errorf("store: flow_record_join_legs: %w", err)
		}
		leg.SrcPort, leg.DstPort = nullInt(srcPort), nullInt(dstPort)
		legs = append(legs, leg)
	}
	return legs, rows.Err()
}

// FlowRecordDecision is one record's decision.
type FlowRecordDecision struct {
	// Record is the record decided, as the group read it: its previous decision and what the unmatched
	// family counts of it.
	Record          GroupRecord
	Identity        string
	StartAt         int64
	JoinState       string
	JoinedFlowID    *int64
	Orientation     *string
	UnmatchedReason *string
	DecidedAt       int64
}

// DecideFlowRecords writes decisions in one transaction, each only where it differs from what is
// stored, and returns how many rows changed: a second derivation of the same rows changes none.
func (s *Store) DecideFlowRecords(ctx context.Context, decisions []FlowRecordDecision) (int64, error) {
	if len(decisions) == 0 {
		return 0, nil
	}
	text, err := Statement("flow_record_decide")
	if err != nil {
		return 0, err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: deciding flow records: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var changed int64
	for _, decision := range decisions {
		var decidedAt any
		if decision.JoinState == JoinJoined || decision.JoinState == JoinUnmatched {
			decidedAt = decision.DecidedAt
		}
		affected, err := execText(ctx, transaction, "flow_record_decide", text, map[string]any{
			"identity": decision.Identity, "start_at": decision.StartAt, "join_state": decision.JoinState,
			"joined_flow_id": optional(decision.JoinedFlowID), "orientation": optionalText(decision.Orientation),
			"unmatched_reason": optionalText(decision.UnmatchedReason), "decided_at": decidedAt})
		if err != nil {
			return 0, err
		}
		changed += affected
		if affected == 0 {
			continue
		}
		// The unmatched family follows the decision: out of its previous reason's slots, into its
		// new reason's.
		previous := decision.Record
		if previous.JoinState == JoinUnmatched && previous.UnmatchedReason != nil {
			if err := accumulateUnmatched(ctx, transaction, previous, *previous.UnmatchedReason, -1,
				decision.DecidedAt); err != nil {
				return 0, err
			}
		}
		if decision.JoinState == JoinUnmatched && decision.UnmatchedReason != nil {
			if err := accumulateUnmatched(ctx, transaction, previous, *decision.UnmatchedReason, 1,
				decision.DecidedAt); err != nil {
				return 0, err
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing the decisions: %w", err)
	}
	return changed, nil
}

// accumulateUnmatched adds a record to, or takes it out of (sign -1), the four slots of the
// unmatched family holding its start, and drops a slot row it leaves empty.
func accumulateUnmatched(ctx context.Context, transaction *sql.Tx, record GroupRecord, reason string, sign,
	now int64) error {
	if record.ObservationPoint == "" || record.Direction == "" {
		return nil
	}
	for _, period := range Periods() {
		table := "unmatched_record_aggregate_" + period.Name
		start := period.SlotStart(record.StartAt)
		key := []any{start, record.ObservationPoint, record.Direction, record.Protocol, record.IPVersion, reason}
		if sign < 0 {
			if _, err := transaction.ExecContext(ctx,
				`UPDATE `+table+` SET bytes = bytes - ?, packets = packets - ?, records = records - 1, updated_at = ?
				 WHERE period_start_at = ? AND observation_point_identifier = ? AND direction = ? AND protocol = ?
				   AND ip_version = ? AND reason = ?`,
				append([]any{record.Octets, record.Packets, now}, key...)...); err != nil {
				return fmt.Errorf("store: taking a record out of the unmatched family: %w", err)
			}
			if _, err := transaction.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE period_start_at = ? AND observation_point_identifier = ? AND direction = ?
				   AND protocol = ? AND ip_version = ? AND reason = ? AND records = 0`, key...); err != nil {
				return fmt.Errorf("store: emptying an unmatched slot: %w", err)
			}
			continue
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO `+table+` AS t (period_start_at, period_end_at, observation_point_identifier, direction,
			     protocol, ip_version, reason, bytes, packets, records, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (period_start_at, observation_point_identifier, direction, protocol, ip_version, reason)
			 DO UPDATE SET bytes = t.bytes + excluded.bytes, packets = t.packets + excluded.packets,
			     records = t.records + excluded.records, updated_at = excluded.updated_at`,
			start, period.SlotEnd(start), record.ObservationPoint, record.Direction, record.Protocol,
			record.IPVersion, reason, record.Octets, record.Packets, 1, now); err != nil {
			return fmt.Errorf("store: accumulating the unmatched family: %w", err)
		}
	}
	return nil
}

// ConnectionLegs is one logged connection: its first (or single) leg, and its second leg when the
// pairing found one.
type ConnectionLegs struct {
	FirstID         int64
	StartAt         int64
	FirstDevice     string
	FirstDirection  string
	FirstAction     string
	SecondID        *int64
	SecondDevice    string
	SecondDirection string
	SecondAction    string
}

// ConnectionsBetween returns the connections whose first (or single) leg was logged in [from, to].
func (s *Store) ConnectionsBetween(ctx context.Context, from, to int64) ([]ConnectionLegs, error) {
	return s.connections(ctx, "measured_volume_connections_between", map[string]any{"from": from, "to": to})
}

// ConnectionsOf returns the connections some legs belong to: a first or single leg's own, and a
// second leg's first leg's.
func (s *Store) ConnectionsOf(ctx context.Context, legIDs []int64) ([]ConnectionLegs, error) {
	if len(legIDs) == 0 {
		return nil, nil
	}
	return s.connections(ctx, "measured_volume_connections_of", map[string]any{"ids": idList(legIDs)})
}

// idList renders ids as the comma-separated text the statements unnest.
func idList(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

func (s *Store) connections(ctx context.Context, name string, parameters map[string]any) ([]ConnectionLegs, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, parameters)...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var connections []ConnectionLegs
	for rows.Next() {
		var connection ConnectionLegs
		var secondID sql.NullInt64
		var secondDevice, secondDirection, secondAction sql.NullString
		if err := rows.Scan(&connection.FirstID, &connection.StartAt, &connection.FirstDevice,
			&connection.FirstDirection, &connection.FirstAction, &secondID, &secondDevice, &secondDirection,
			&secondAction); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		connection.SecondID = nullInt(secondID)
		connection.SecondDevice, connection.SecondDirection = secondDevice.String, secondDirection.String
		connection.SecondAction = secondAction.String
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

// JoinedRecord is one record joined to a leg, as a connection's figure reads it.
type JoinedRecord struct {
	LegID       int64
	Orientation string
	Octets      int64
	Packets     int64
	StartAt     int64
	EndAt       int64
	TCPFlags    *int64
	Protocol    int64
}

// JoinedRecordsOf returns the records joined to some legs, by leg.
func (s *Store) JoinedRecordsOf(ctx context.Context, legIDs []int64) (map[int64][]JoinedRecord, error) {
	joined := map[int64][]JoinedRecord{}
	if len(legIDs) == 0 {
		return joined, nil
	}
	text, err := Statement("measured_volume_joined_records")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, named(text, map[string]any{"ids": idList(legIDs)})...)
	if err != nil {
		return nil, fmt.Errorf("store: measured_volume_joined_records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record JoinedRecord
		var flags sql.NullInt64
		if err := rows.Scan(&record.LegID, &record.Orientation, &record.Octets, &record.Packets, &record.StartAt,
			&record.EndAt, &flags, &record.Protocol); err != nil {
			return nil, fmt.Errorf("store: measured_volume_joined_records: %w", err)
		}
		record.TCPFlags = nullInt(flags)
		joined[record.LegID] = append(joined[record.LegID], record)
	}
	return joined, rows.Err()
}

// OpenMeasuredVolumes returns the connections whose measured volume is due, by an instant, to change by
// time alone.
func (s *Store) OpenMeasuredVolumes(ctx context.Context, due int64) ([]int64, error) {
	return queryInts(ctx, s.db, "measured_volume_open", map[string]any{"due": due})
}

// MeasuredVolume is one direction of one connection's measured volume.
type MeasuredVolume struct {
	ConnectionFlowID           int64
	Direction                  string
	ConnectionStartAt          int64
	State                      string
	Bytes                      int64
	Packets                    int64
	Records                    int64
	ObservationPointIdentifier string
	ObservationPointDevice     string
	MeasuredAtLeg              *string
	MeasuredFlowID             *int64
	Final                      bool
	// DueAt is the read-through instant from which the state may change by time alone; nil once final.
	DueAt      *int64
	ComputedAt int64
}

// sameFigures reports whether two rows say the same thing; computed_at is not part of it.
func (v MeasuredVolume) sameFigures(other MeasuredVolume) bool {
	pointerEqual := func(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	textEqual := func(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	return v.ConnectionStartAt == other.ConnectionStartAt && v.State == other.State && v.Bytes == other.Bytes &&
		v.Packets == other.Packets && v.Records == other.Records &&
		v.ObservationPointIdentifier == other.ObservationPointIdentifier &&
		v.ObservationPointDevice == other.ObservationPointDevice && textEqual(v.MeasuredAtLeg, other.MeasuredAtLeg) &&
		pointerEqual(v.MeasuredFlowID, other.MeasuredFlowID) && v.Final == other.Final && pointerEqual(v.DueAt, other.DueAt)
}

// MeasuredVolumesOf returns the stored measured volume of some connections.
func (s *Store) MeasuredVolumesOf(ctx context.Context, connectionIDs []int64) (map[[2]any]MeasuredVolume, error) {
	stored := map[[2]any]MeasuredVolume{}
	if len(connectionIDs) == 0 {
		return stored, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT connection_flow_id, direction, connection_start_at, state, bytes, packets, records,
		        observation_point_identifier, observation_point_device, measured_at_leg, measured_flow_id, final,
		        due_at, computed_at
		 FROM connection_measured_volume
		 WHERE connection_flow_id = ANY (CAST(string_to_array(?, ',') AS BIGINT[]))`, idList(connectionIDs))
	if err != nil {
		return nil, fmt.Errorf("store: reading measured volumes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var volume MeasuredVolume
		var leg sql.NullString
		var measured, due sql.NullInt64
		var final int64
		if err := rows.Scan(&volume.ConnectionFlowID, &volume.Direction, &volume.ConnectionStartAt, &volume.State,
			&volume.Bytes, &volume.Packets, &volume.Records, &volume.ObservationPointIdentifier,
			&volume.ObservationPointDevice, &leg, &measured, &final, &due, &volume.ComputedAt); err != nil {
			return nil, fmt.Errorf("store: reading a measured volume: %w", err)
		}
		volume.MeasuredAtLeg, volume.MeasuredFlowID, volume.Final = nullString(leg), nullInt(measured), final == 1
		volume.DueAt = nullInt(due)
		stored[[2]any{volume.ConnectionFlowID, volume.Direction}] = volume
	}
	return stored, rows.Err()
}

// WriteMeasuredVolumes writes the measured volume of some connections in one transaction: each row
// computed is written where it differs from the stored one, and every stored row of those connections
// not computed is deleted -- a connection that no longer started under one Observation Point holds
// none. It returns the start of every connection whose rows changed: the slots its figure counts in,
// which the refresh then rewrites (PS21, the refresh contract's rule 3).
func (s *Store) WriteMeasuredVolumes(ctx context.Context, connectionIDs []int64, computed []MeasuredVolume) (
	[]int64, error) {
	if len(connectionIDs) == 0 {
		return nil, nil
	}
	stored, err := s.MeasuredVolumesOf(ctx, connectionIDs)
	if err != nil {
		return nil, err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: writing measured volumes: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var changed []int64
	kept := map[[2]any]bool{}
	for _, volume := range computed {
		key := [2]any{volume.ConnectionFlowID, volume.Direction}
		kept[key] = true
		if previous, found := stored[key]; found && previous.sameFigures(volume) {
			continue
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO connection_measured_volume (connection_flow_id, direction, connection_start_at, state, bytes,
			     packets, records, observation_point_identifier, observation_point_device, measured_at_leg,
			     measured_flow_id, final, due_at, computed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (connection_flow_id, direction) DO UPDATE SET
			     connection_start_at = excluded.connection_start_at, state = excluded.state, bytes = excluded.bytes,
			     packets = excluded.packets, records = excluded.records,
			     observation_point_identifier = excluded.observation_point_identifier,
			     observation_point_device = excluded.observation_point_device,
			     measured_at_leg = excluded.measured_at_leg, measured_flow_id = excluded.measured_flow_id,
			     final = excluded.final, due_at = excluded.due_at, computed_at = excluded.computed_at`,
			volume.ConnectionFlowID, volume.Direction, volume.ConnectionStartAt, volume.State, volume.Bytes,
			volume.Packets, volume.Records, volume.ObservationPointIdentifier, volume.ObservationPointDevice,
			optionalText(volume.MeasuredAtLeg), optional(volume.MeasuredFlowID), volume.Final, optional(volume.DueAt),
			volume.ComputedAt); err != nil {
			return nil, fmt.Errorf("store: writing a measured volume: %w", err)
		}
		changed = append(changed, volume.ConnectionStartAt)
	}
	for key := range stored {
		if kept[key] {
			continue
		}
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM connection_measured_volume WHERE connection_flow_id = ? AND direction = ?",
			key[0], key[1]); err != nil {
			return nil, fmt.Errorf("store: deleting a measured volume: %w", err)
		}
		changed = append(changed, stored[key].ConnectionStartAt)
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing measured volumes: %w", err)
	}
	return changed, nil
}

// ProviderGap is one interval a provider knows it did not cover.
type ProviderGap struct {
	From   int64
	To     int64
	Reason GapReason
}

// ProviderGapsBetween returns a provider's gaps meeting [from, to].
func (s *Store) ProviderGapsBetween(ctx context.Context, providerID, from, to int64) ([]ProviderGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT interval_start_at, interval_end_at, reason FROM collection_gap
		 WHERE provider_id = ? AND interval_start_at <= ? AND interval_end_at >= ?
		 ORDER BY interval_start_at, id`, providerID, to, from)
	if err != nil {
		return nil, fmt.Errorf("store: reading a provider's gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var gaps []ProviderGap
	for rows.Next() {
		var gap ProviderGap
		var reason string
		if err := rows.Scan(&gap.From, &gap.To, &reason); err != nil {
			return nil, fmt.Errorf("store: reading a provider's gap: %w", err)
		}
		gap.Reason = GapReason(reason)
		gaps = append(gaps, gap)
	}
	return gaps, rows.Err()
}

// purgeNetflow purges the NetFlow tables inside the retention purge's transaction: the flow records by
// their own retention (chunks then rows), the measured volume with the connections it belongs to, and
// the configurations and conditions older than the records' horizon -- the newest configuration of
// each provider kept, since it is the one in force.
func purgeNetflow(ctx context.Context, transaction *sql.Tx, now int64) error {
	var retention int64
	err := transaction.QueryRowContext(ctx,
		"SELECT CAST(value AS BIGINT) FROM setting WHERE key = ?", FlowRecordRetentionKey).Scan(&retention)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: reading %s: %w", FlowRecordRetentionKey, err)
	}
	if horizon, bounded, err := retentionHorizon(ctx, transaction, now, retention); err != nil {
		return err
	} else if bounded {
		if err := purgeTimeTable(ctx, transaction, CoreSchema+".flow_record", "start_at", horizon); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx,
			`DELETE FROM capture_configuration AS c
			 WHERE c.last_read_at < ?
			   AND c.first_read_at < (SELECT max(n.first_read_at) FROM capture_configuration AS n
			                          WHERE n.provider_id = c.provider_id)`, horizon); err != nil {
			return fmt.Errorf("store: purging capture configurations: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM flow_record_condition WHERE observed_at < ?",
			horizon); err != nil {
			return fmt.Errorf("store: purging stream conditions: %w", err)
		}
	}
	if horizon, bounded, err := retentionHorizon(ctx, transaction, now, 0); err != nil {
		return err
	} else if bounded {
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM connection_measured_volume WHERE connection_start_at < ?", horizon); err != nil {
			return fmt.Errorf("store: purging measured volumes: %w", err)
		}
		// The unmatched family's slots go at the flows' horizon, as an aggregate slot that is not
		// composed from finer ones: once the slot has ended before it.
		for _, period := range Periods() {
			if _, err := transaction.ExecContext(ctx,
				"DELETE FROM unmatched_record_aggregate_"+period.Name+" WHERE period_end_at < ?", horizon); err != nil {
				return fmt.Errorf("store: purging the unmatched family: %w", err)
			}
		}
	}
	return nil
}
