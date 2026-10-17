package netflow

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The NetFlow side of the store (PS17 of the NetFlow join amendment to
// specs/SPEC-backend-completion-plugins.md, and NJ2 to NJ8 of its amendment "the NetFlow join with every
// interface captured"): the capture configurations a flow_record provider read (capture.go), the flow
// records themselves and the join's reads and writes (this file), and the logged connections' measured
// volume (measuredvolume.go). What each table means is in schema.sql beside it and in
// docs/data-model.md, "NetFlow volumes"; what is decided from them is internal/collect's.

// The roles of a record's mapped index (NJ3).
const (
	// RoleInterface is an index the map holds for an interface.
	RoleInterface = "interface"
	// RoleNoReceivingInterface is index 0: a packet the firewall originated (survey S6a).
	RoleNoReceivingInterface = "no_receiving_interface"
	// RoleLocalDelivery is the loopback's index: a packet delivered to the firewall itself (S6b).
	RoleLocalDelivery = "local_delivery"
	// RoleUnmapped is any other index the map does not hold, or no index at all.
	RoleUnmapped = "unmapped"
)

// The join states of a flow record (NJ2).
const (
	JoinPending   = "pending"
	JoinJoined    = "joined"
	JoinCopy      = "copy"
	JoinUnmatched = "unmatched"
)

// The copy kinds (NJ4 rules 5 and 6).
const (
	CopyTranslated = "translated_copy"
	CopyParent     = "parent_copy"
)

// The unmatched reasons, a closed list (NJ4 rule 9).
const (
	UnmatchedNoLoggedLeg        = "no_logged_leg"
	UnmatchedAmbiguous          = "ambiguous"
	UnmatchedBeyondFlowHorizon  = "beyond_flow_horizon"
	UnmatchedIndexUnmapped      = "interface_index_unmapped"
	UnmatchedReverseUnconfirmed = "reverse_unconfirmed"
)

// The orientations of a record to a leg (RFC 5103).
const (
	OrientationForward = "forward"
	OrientationReverse = "reverse"
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

// MappedIndex is one of a record's two indexes as the index map in force at reception named it.
type MappedIndex struct {
	Role       string
	Identifier *string
	Device     *string
}

// Name is how the unmatched family keys the index: the interface's identifier, or its role.
func (m MappedIndex) Name() string {
	if m.Role == RoleInterface && m.Identifier != nil && *m.Identifier != "" {
		return *m.Identifier
	}
	return m.Role
}

// FlowRecordRow is one flow_record row as it is stored at reception.
type FlowRecordRow struct {
	ProviderID        int64
	Identity          string
	StartAt           int64
	StartMillisecond  int64
	EndAt             int64
	EndMillisecond    int64
	ReceivedAt        int64
	ExporterAddress   string
	ObservationDomain int64
	IngressInterface  *int64
	EgressInterface   *int64
	IPVersion         int64
	Protocol          int64
	SrcAddress        string
	DstAddress        string
	SrcPort           *int64
	DstPort           *int64
	TCPFlags          *int64
	Octets            int64
	Packets           int64
	Ingress           MappedIndex
	Egress            MappedIndex
	LossDetectable    bool
	TupleKey          string
}

// InsertFlowRecords stores flow records in one transaction, each pending; a record already stored --
// the same identity and start -- is left as it is. It returns how many were new.
func (s *NetflowStore) InsertFlowRecords(ctx context.Context, records []FlowRecordRow) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: storing flow records: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	statement, err := transaction.PrepareContext(ctx,
		`INSERT INTO flow_record (provider_id, identity, start_at, start_millisecond, end_at, end_millisecond,
		     received_at, exporter_address, observation_domain, ingress_interface, egress_interface, ip_version,
		     protocol, src_address, dst_address, src_port, dst_port, tcp_flags, octets, packets, ingress_role,
		     ingress_identifier, ingress_device, egress_role, egress_identifier, egress_device, loss_detectable,
		     tuple_key, join_state)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, 'pending')
		 ON CONFLICT (identity, start_at) DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("store: preparing the flow-record insert: %w", err)
	}
	defer func() { _ = statement.Close() }()
	var inserted int64
	for _, record := range records {
		result, err := statement.ExecContext(ctx, record.ProviderID, record.Identity, record.StartAt,
			record.StartMillisecond, record.EndAt, record.EndMillisecond, record.ReceivedAt, record.ExporterAddress,
			record.ObservationDomain, sqldb.Optional(record.IngressInterface), sqldb.Optional(record.EgressInterface),
			record.IPVersion, record.Protocol, record.SrcAddress, record.DstAddress, sqldb.Optional(record.SrcPort),
			sqldb.Optional(record.DstPort), sqldb.Optional(record.TCPFlags), record.Octets, record.Packets,
			record.Ingress.Role, sqldb.OptionalText(record.Ingress.Identifier), sqldb.OptionalText(record.Ingress.Device),
			record.Egress.Role, sqldb.OptionalText(record.Egress.Identifier), sqldb.OptionalText(record.Egress.Device),
			record.LossDetectable, record.TupleKey)
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

// PendingFlowRecordGroups returns the tuple keys of the groups holding an undecided record of a
// provider that started before a bound.
func (s *NetflowStore) PendingFlowRecordGroups(ctx context.Context, providerID, before int64) ([]string, error) {
	text, err := sqldb.Statement("flow_record_pending_groups")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, map[string]any{"provider_id": providerID,
		"before": before})
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_pending_groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var groups []string
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
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
	IPVersion        int64
	TCPFlags         *int64
	Octets           int64
	Packets          int64
	// IngressIndex and EgressIndex are the raw indexes; Ingress and Egress what they were mapped to.
	IngressIndex *int64
	EgressIndex  *int64
	Ingress      MappedIndex
	Egress       MappedIndex
	// The record's decision as stored.
	JoinState       string
	JoinedFlowID    *int64
	CopyKind        *string
	CopyFlowID      *int64
	Orientation     *string
	UnmatchedReason *string
	DecidedAt       *int64
}

// FlowRecordGroup returns every record of a provider's group, oldest first.
func (s *NetflowStore) FlowRecordGroup(ctx context.Context, providerID int64, tupleKey string) ([]GroupRecord, error) {
	text, err := sqldb.Statement("flow_record_group")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, map[string]any{"provider_id": providerID,
		"tuple_key": tupleKey})
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_group: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []GroupRecord
	for rows.Next() {
		var (
			record                                      GroupRecord
			srcPort, dstPort, flags, joined, copied, at sql.NullInt64
			ingressIndex, egressIndex                   sql.NullInt64
			ingressIdentifier, ingressDevice            sql.NullString
			egressIdentifier, egressDevice              sql.NullString
			copyKind, orientation, reason               sql.NullString
		)
		if err := rows.Scan(&record.Identity, &record.StartAt, &record.StartMillisecond, &record.EndAt,
			&record.EndMillisecond, &record.SrcAddress, &record.DstAddress, &srcPort, &dstPort, &record.Protocol,
			&record.IPVersion, &flags, &record.Octets, &record.Packets, &ingressIndex, &egressIndex,
			&record.Ingress.Role, &ingressIdentifier, &ingressDevice, &record.Egress.Role, &egressIdentifier,
			&egressDevice, &record.JoinState, &joined, &copyKind, &copied, &orientation, &reason, &at); err != nil {
			return nil, fmt.Errorf("store: flow_record_group: %w", err)
		}
		record.SrcPort, record.DstPort, record.TCPFlags = sqldb.NullInt(srcPort), sqldb.NullInt(dstPort), sqldb.NullInt(flags)
		record.IngressIndex, record.EgressIndex = sqldb.NullInt(ingressIndex), sqldb.NullInt(egressIndex)
		record.Ingress.Identifier, record.Ingress.Device = nullString(ingressIdentifier), nullString(ingressDevice)
		record.Egress.Identifier, record.Egress.Device = nullString(egressIdentifier), nullString(egressDevice)
		record.JoinedFlowID, record.CopyFlowID, record.DecidedAt = sqldb.NullInt(joined), sqldb.NullInt(copied), sqldb.NullInt(at)
		record.CopyKind, record.Orientation, record.UnmatchedReason = nullString(copyKind), nullString(orientation),
			nullString(reason)
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

// JoinLeg is one filter-log record a flow record may join, or whose logged tuple a copy may match.
type JoinLeg struct {
	FlowID      int64
	ObservedAt  int64
	Device      string
	SrcAddress  string
	DstAddress  string
	SrcPort     *int64
	DstPort     *int64
	Protocol    string
	Direction   string
	Action      string
	PairOutcome string
}

// JoinLegs returns the legs logged on some devices, of a version, whose two addresses are a and b in
// either orientation, logged at or before latest.
func (s *NetflowStore) JoinLegs(ctx context.Context, devices []string, a, b string, addressFamily, latest int64) (
	[]JoinLeg, error) {
	if len(devices) == 0 {
		return nil, nil
	}
	text, err := sqldb.Statement("flow_record_join_legs")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, map[string]any{
		"devices": strings.Join(devices, unitSeparator), "address_a": a, "address_b": b, "ip_version": addressFamily,
		"latest": latest})
	if err != nil {
		return nil, fmt.Errorf("store: flow_record_join_legs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var legs []JoinLeg
	for rows.Next() {
		var leg JoinLeg
		var srcPort, dstPort sql.NullInt64
		var pair sql.NullString
		if err := rows.Scan(&leg.FlowID, &leg.ObservedAt, &leg.Device, &leg.SrcAddress, &leg.DstAddress, &srcPort,
			&dstPort, &leg.Protocol, &leg.Direction, &leg.Action, &pair); err != nil {
			return nil, fmt.Errorf("store: flow_record_join_legs: %w", err)
		}
		leg.SrcPort, leg.DstPort, leg.PairOutcome = sqldb.NullInt(srcPort), sqldb.NullInt(dstPort), pair.String
		legs = append(legs, leg)
	}
	return legs, rows.Err()
}

// FirewallAddress is one address discovery holds for the firewall, and when it held it.
type FirewallAddress struct {
	Address     string
	FirstSeenAt int64
	LastSeenAt  int64
	Current     bool
}

// FirewallAddresses returns every address discovery holds, or held, for the firewall (the
// this_firewall_address view): what NJ4 rule 5's second signal compares a record's ends with.
func (s *NetflowStore) FirewallAddresses(ctx context.Context) ([]FirewallAddress, error) {
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT address, first_seen_at, last_seen_at, is_current FROM this_firewall_address ORDER BY address`)
	if err != nil {
		return nil, fmt.Errorf("store: reading the firewall's addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var addresses []FirewallAddress
	for rows.Next() {
		var address FirewallAddress
		var current int64
		if err := rows.Scan(&address.Address, &address.FirstSeenAt, &address.LastSeenAt, &current); err != nil {
			return nil, fmt.Errorf("store: reading a firewall address: %w", err)
		}
		address.Current = current == 1
		addresses = append(addresses, address)
	}
	return addresses, rows.Err()
}

// FlowRecordDecision is one record's decision.
type FlowRecordDecision struct {
	// Record is the record decided, as the group read it: its previous decision and what the unmatched
	// family counts of it.
	Record          GroupRecord
	JoinState       string
	JoinedFlowID    *int64
	CopyKind        *string
	CopyFlowID      *int64
	Orientation     *string
	UnmatchedReason *string
	DecidedAt       int64
}

// Same reports whether the decision is what is stored.
func (d FlowRecordDecision) Same() bool {
	r := d.Record
	return r.JoinState == d.JoinState && equalInt(r.JoinedFlowID, d.JoinedFlowID) && equalText(r.CopyKind, d.CopyKind) &&
		equalInt(r.CopyFlowID, d.CopyFlowID) && equalText(r.Orientation, d.Orientation) &&
		equalText(r.UnmatchedReason, d.UnmatchedReason)
}

func equalInt(a, b *int64) bool   { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func equalText(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// DecideFlowRecords writes decisions in one transaction, each only where it differs from what is
// stored, and returns how many rows changed: a second derivation of the same rows changes none.
func (s *NetflowStore) DecideFlowRecords(ctx context.Context, decisions []FlowRecordDecision) (int64, error) {
	if len(decisions) == 0 {
		return 0, nil
	}
	text, err := sqldb.Statement("flow_record_decide")
	if err != nil {
		return 0, err
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: deciding flow records: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var changed int64
	for _, decision := range decisions {
		if decision.Same() {
			continue
		}
		var decidedAt any
		if decision.JoinState != JoinPending {
			decidedAt = decision.DecidedAt
		}
		affected, err := sqldb.ExecText(ctx, transaction, "flow_record_decide", text, map[string]any{
			"identity": decision.Record.Identity, "start_at": decision.Record.StartAt, "join_state": decision.JoinState,
			"joined_flow_id": sqldb.Optional(decision.JoinedFlowID), "copy_kind": sqldb.OptionalText(decision.CopyKind),
			"copy_flow_id": sqldb.Optional(decision.CopyFlowID), "orientation": sqldb.OptionalText(decision.Orientation),
			"unmatched_reason": sqldb.OptionalText(decision.UnmatchedReason), "decided_at": decidedAt})
		if err != nil {
			return 0, err
		}
		changed += affected
		if affected == 0 {
			continue
		}
		// The unmatched family follows the decision: out of its previous reason's slots, into its
		// new reason's. A copy is in no slot.
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
// unmatched family holding its start, and drops a slot row it leaves empty (NJ8's key: the slot, the
// ingress and egress interfaces or roles, the protocol, the IP version and the reason).
func accumulateUnmatched(ctx context.Context, transaction *sql.Tx, record GroupRecord, reason string, sign,
	now int64) error {
	for _, period := range vocab.Periods() {
		table := "unmatched_record_aggregate_" + period.Name
		start := period.SlotStart(record.StartAt)
		key := []any{start, record.Ingress.Name(), record.Egress.Name(), record.Protocol, record.IPVersion, reason}
		if sign < 0 {
			if _, err := transaction.ExecContext(ctx,
				`UPDATE `+table+` SET bytes = bytes - $1, packets = packets - $2, records = records - 1, updated_at = $3
				 WHERE period_start_at = $4 AND ingress_interface = $5 AND egress_interface = $6 AND protocol = $7
				   AND ip_version = $8 AND reason = $9`,
				append([]any{record.Octets, record.Packets, now}, key...)...); err != nil {
				return fmt.Errorf("store: taking a record out of the unmatched family: %w", err)
			}
			if _, err := transaction.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE period_start_at = $1 AND ingress_interface = $2 AND egress_interface = $3
				   AND protocol = $4 AND ip_version = $5 AND reason = $6 AND records = 0`, key...); err != nil {
				return fmt.Errorf("store: emptying an unmatched slot: %w", err)
			}
			continue
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO `+table+` AS t (period_start_at, period_end_at, ingress_interface, egress_interface,
			     protocol, ip_version, reason, bytes, packets, records, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT (period_start_at, ingress_interface, egress_interface, protocol, ip_version, reason)
			 DO UPDATE SET bytes = t.bytes + excluded.bytes, packets = t.packets + excluded.packets,
			     records = t.records + excluded.records, updated_at = excluded.updated_at`,
			start, period.SlotEnd(start), record.Ingress.Name(), record.Egress.Name(), record.Protocol,
			record.IPVersion, reason, record.Octets, record.Packets, 1, now); err != nil {
			return fmt.Errorf("store: accumulating the unmatched family: %w", err)
		}
	}
	return nil
}

// idList renders ids as the comma-separated text the statements unnest.
func idList(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}
