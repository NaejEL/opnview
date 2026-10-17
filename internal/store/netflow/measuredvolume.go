package netflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// A logged connection's measured volume, each direction once (NJ5 of the amendment "the NetFlow join with
// every interface captured"), and the purge of the NetFlow tables.

// The per-direction states of a connection's measured volume (NJ5). The first five are the approved
// states of a direction one exporter counts; the others say why no figure is given.
const (
	VolumeMeasured                  = "measured"
	VolumeInProgress                = "in_progress"
	VolumeAwaitingExport            = "awaiting_export"
	VolumeMissing                   = "missing"
	VolumeExportGap                 = "export_gap"
	VolumeNotExportedWhenRefused    = "not_exported_when_refused"
	VolumeForwardingExportUnsettled = "forwarding_export_unsettled"
	VolumeIngressNotCaptured        = "ingress_not_captured"
	VolumeParentCopiesOnly          = "parent_copies_only"
	VolumeInterfaceNotExported      = "interface_not_exported"
	VolumeInterfaceNotCaptured      = "interface_not_captured"
	VolumeCaptureUnknown            = "capture_configuration_unknown"
)

// VolumeStates are the per-direction states, in the order netflow_parts holds them.
func VolumeStates() []string {
	return []string{VolumeMeasured, VolumeInProgress, VolumeAwaitingExport, VolumeMissing, VolumeExportGap,
		VolumeNotExportedWhenRefused, VolumeForwardingExportUnsettled, VolumeIngressNotCaptured,
		VolumeParentCopiesOnly, VolumeInterfaceNotExported, VolumeInterfaceNotCaptured, VolumeCaptureUnknown}
}

// CoveredState reports whether a direction in a state is covered (NJ5, "Coverage"): measured,
// in_progress, awaiting_export or missing.
func CoveredState(state string) bool {
	switch state {
	case VolumeMeasured, VolumeInProgress, VolumeAwaitingExport, VolumeMissing:
		return true
	}
	return false
}

// ConnectionLegs is one logged connection: its first (or single) leg, and its second leg when the
// pairing found one.
type ConnectionLegs struct {
	FirstID  int64
	StartAt  int64
	First    LoggedLeg
	SecondID *int64
	Second   LoggedLeg
	// FirstToFirewall says the first leg's destination is an address of the firewall.
	FirstToFirewall bool
}

// LoggedLeg is what the measured volume reads of one leg.
type LoggedLeg struct {
	Device     string
	Direction  string
	Action     string
	SrcAddress string
	DstAddress string
	SrcPort    *int64
	DstPort    *int64
}

// ConnectionsBetween returns the connections whose first (or single) leg was logged in [from, to].
func (s *NetflowStore) ConnectionsBetween(ctx context.Context, from, to int64) ([]ConnectionLegs, error) {
	return s.connections(ctx, "measured_volume_connections_between", map[string]any{"from": from, "to": to})
}

// ConnectionsOf returns the connections some legs belong to: a first or single leg's own, and a
// second leg's first leg's.
func (s *NetflowStore) ConnectionsOf(ctx context.Context, legIDs []int64) ([]ConnectionLegs, error) {
	if len(legIDs) == 0 {
		return nil, nil
	}
	return s.connections(ctx, "measured_volume_connections_of", map[string]any{"ids": idList(legIDs)})
}

func (s *NetflowStore) connections(ctx context.Context, name string, parameters map[string]any) ([]ConnectionLegs, error) {
	text, err := sqldb.Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, parameters)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var connections []ConnectionLegs
	for rows.Next() {
		var (
			connection                             ConnectionLegs
			toFirewall                             bool
			firstSrcPort, firstDstPort             sql.NullInt64
			secondID, secondSrcPort, secondDstPort sql.NullInt64
			secondDevice, secondDirection          sql.NullString
			secondAction, secondSrc, secondDst     sql.NullString
		)
		first := &connection.First
		if err := rows.Scan(&connection.FirstID, &connection.StartAt, &first.Device, &first.Direction, &first.Action,
			&first.SrcAddress, &first.DstAddress, &firstSrcPort, &firstDstPort, &toFirewall, &secondID, &secondDevice,
			&secondDirection, &secondAction, &secondSrc, &secondDst, &secondSrcPort, &secondDstPort); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		first.SrcPort, first.DstPort = sqldb.NullInt(firstSrcPort), sqldb.NullInt(firstDstPort)
		connection.FirstToFirewall = toFirewall
		connection.SecondID = sqldb.NullInt(secondID)
		connection.Second = LoggedLeg{Device: secondDevice.String, Direction: secondDirection.String,
			Action: secondAction.String, SrcAddress: secondSrc.String, DstAddress: secondDst.String,
			SrcPort: sqldb.NullInt(secondSrcPort), DstPort: sqldb.NullInt(secondDstPort)}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

// InterfaceIdentifiers maps each device discovery holds to its interface identifier: how a leg's device
// is found in a capture configuration, which names interfaces by identifier.
func (s *NetflowStore) InterfaceIdentifiers(ctx context.Context) (map[string]string, error) {
	rows, err := s.SQL.QueryContext(ctx, "SELECT device, identifier FROM interface ORDER BY device")
	if err != nil {
		return nil, fmt.Errorf("store: reading the interfaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	identifiers := map[string]string{}
	for rows.Next() {
		var device, identifier string
		if err := rows.Scan(&device, &identifier); err != nil {
			return nil, fmt.Errorf("store: reading an interface: %w", err)
		}
		identifiers[device] = identifier
	}
	return identifiers, rows.Err()
}

// CountedRecord is one record a connection's figure may count: joined to its first leg, or a
// translated copy of its second leg.
type CountedRecord struct {
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
func (s *NetflowStore) JoinedRecordsOf(ctx context.Context, legIDs []int64) (map[int64][]CountedRecord, error) {
	return s.countedRecords(ctx, "measured_volume_joined_records", legIDs)
}

// CopyRecordsOf returns the translated copies matched to some second legs, by leg.
func (s *NetflowStore) CopyRecordsOf(ctx context.Context, legIDs []int64) (map[int64][]CountedRecord, error) {
	return s.countedRecords(ctx, "measured_volume_copy_records", legIDs)
}

func (s *NetflowStore) countedRecords(ctx context.Context, name string, legIDs []int64) (map[int64][]CountedRecord,
	error) {
	counted := map[int64][]CountedRecord{}
	if len(legIDs) == 0 {
		return counted, nil
	}
	text, err := sqldb.Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, map[string]any{"ids": idList(legIDs)})
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record CountedRecord
		var flags sql.NullInt64
		if err := rows.Scan(&record.LegID, &record.Orientation, &record.Octets, &record.Packets, &record.StartAt,
			&record.EndAt, &flags, &record.Protocol); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		record.TCPFlags = sqldb.NullInt(flags)
		counted[record.LegID] = append(counted[record.LegID], record)
	}
	return counted, rows.Err()
}

// OpenMeasuredVolumes returns the connections whose measured volume is due, by an instant, to change by
// time alone.
func (s *NetflowStore) OpenMeasuredVolumes(ctx context.Context, due int64) ([]int64, error) {
	return sqldb.QueryInts(ctx, s.SQL, "measured_volume_open", map[string]any{"due": due})
}

// MeasuredVolume is one direction of one connection's measured volume.
type MeasuredVolume struct {
	ConnectionFlowID  int64
	Direction         string
	ConnectionStartAt int64
	State             string
	Bytes             int64
	Packets           int64
	Records           int64
	CountedCopy       bool
	Final             bool
	// DueAt is the read-through instant from which the state may change by time alone; nil once final.
	DueAt      *int64
	ComputedAt int64
}

// sameFigures reports whether two rows say the same thing; computed_at is not part of it.
func (v MeasuredVolume) sameFigures(other MeasuredVolume) bool {
	return v.ConnectionStartAt == other.ConnectionStartAt && v.State == other.State && v.Bytes == other.Bytes &&
		v.Packets == other.Packets && v.Records == other.Records && v.CountedCopy == other.CountedCopy &&
		v.Final == other.Final && equalInt(v.DueAt, other.DueAt)
}

// MeasuredVolumesOf returns the stored measured volume of some connections.
func (s *NetflowStore) MeasuredVolumesOf(ctx context.Context, connectionIDs []int64) (map[[2]any]MeasuredVolume, error) {
	stored := map[[2]any]MeasuredVolume{}
	if len(connectionIDs) == 0 {
		return stored, nil
	}
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT connection_flow_id, direction, connection_start_at, state, bytes, packets, records, counted_copy,
		        final, due_at, computed_at
		 FROM connection_measured_volume
		 WHERE connection_flow_id = ANY (CAST(string_to_array($1, ',') AS BIGINT[]))`, idList(connectionIDs))
	if err != nil {
		return nil, fmt.Errorf("store: reading measured volumes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var volume MeasuredVolume
		var due sql.NullInt64
		if err := rows.Scan(&volume.ConnectionFlowID, &volume.Direction, &volume.ConnectionStartAt, &volume.State,
			&volume.Bytes, &volume.Packets, &volume.Records, &volume.CountedCopy, &volume.Final, &due,
			&volume.ComputedAt); err != nil {
			return nil, fmt.Errorf("store: reading a measured volume: %w", err)
		}
		volume.DueAt = sqldb.NullInt(due)
		stored[[2]any{volume.ConnectionFlowID, volume.Direction}] = volume
	}
	return stored, rows.Err()
}

// CopyMarks says, for one second leg, which orientations of its translated copies NJ5's sole-copy
// rule counts.
type CopyMarks struct {
	SecondLegID int64
	Forward     bool
	Reverse     bool
}

// WriteMeasuredVolumes writes the measured volume of some connections in one transaction: each row
// computed is written where it differs from the stored one, every stored row of those connections not
// computed is deleted -- a connection that no longer started under a readable configuration holds
// none -- and the translated copies of their second legs carry counted_copy exactly where marks say.
// It returns the start of every connection whose rows changed: the slots its figure counts in, which
// the refresh then rewrites (PS21, the refresh contract's rule 3).
func (s *NetflowStore) WriteMeasuredVolumes(ctx context.Context, connectionIDs []int64, computed []MeasuredVolume,
	marks []CopyMarks) ([]int64, error) {
	if len(connectionIDs) == 0 {
		return nil, nil
	}
	stored, err := s.MeasuredVolumesOf(ctx, connectionIDs)
	if err != nil {
		return nil, err
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
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
			     packets, records, counted_copy, final, due_at, computed_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT (connection_flow_id, direction) DO UPDATE SET
			     connection_start_at = excluded.connection_start_at, state = excluded.state, bytes = excluded.bytes,
			     packets = excluded.packets, records = excluded.records, counted_copy = excluded.counted_copy,
			     final = excluded.final, due_at = excluded.due_at, computed_at = excluded.computed_at`,
			volume.ConnectionFlowID, volume.Direction, volume.ConnectionStartAt, volume.State, volume.Bytes,
			volume.Packets, volume.Records, volume.CountedCopy, volume.Final, sqldb.Optional(volume.DueAt),
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
			"DELETE FROM connection_measured_volume WHERE connection_flow_id = $1 AND direction = $2",
			key[0], key[1]); err != nil {
			return nil, fmt.Errorf("store: deleting a measured volume: %w", err)
		}
		changed = append(changed, stored[key].ConnectionStartAt)
	}
	for _, mark := range marks {
		if _, err := transaction.ExecContext(ctx,
			`UPDATE flow_record
			 SET counted_copy = (orientation = 'forward' AND $1) OR (orientation = 'reverse' AND $2)
			 WHERE copy_flow_id = $3
			   AND counted_copy <> ((orientation = 'forward' AND $4) OR (orientation = 'reverse' AND $5))`,
			mark.Forward, mark.Reverse, mark.SecondLegID, mark.Forward, mark.Reverse); err != nil {
			return nil, fmt.Errorf("store: marking the counted copies: %w", err)
		}
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
	Reason vocab.GapReason
}

// ProviderGapsBetween returns a provider's gaps meeting [from, to].
func (s *NetflowStore) ProviderGapsBetween(ctx context.Context, providerID, from, to int64) ([]ProviderGap, error) {
	rows, err := s.SQL.QueryContext(ctx,
		`SELECT interval_start_at, interval_end_at, reason FROM collection_gap
		 WHERE provider_id = $1 AND interval_start_at <= $2 AND interval_end_at >= $3
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
		gap.Reason = vocab.GapReason(reason)
		gaps = append(gaps, gap)
	}
	return gaps, rows.Err()
}

// PurgeNetflow purges the NetFlow tables inside the retention purge's transaction: the flow records by
// their own retention (chunks then rows), the measured volume with the connections it belongs to, and
// the configurations older than the records' horizon -- the newest configuration of each provider
// kept, since it is the one in force.
func PurgeNetflow(ctx context.Context, transaction *sql.Tx, now int64) error {
	var retention int64
	err := transaction.QueryRowContext(ctx,
		"SELECT CAST(value AS BIGINT) FROM setting WHERE key = $1", FlowRecordRetentionKey).Scan(&retention)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: reading %s: %w", FlowRecordRetentionKey, err)
	}
	if horizon, bounded, err := plugintables.RetentionHorizon(ctx, transaction, now, retention); err != nil {
		return err
	} else if bounded {
		if err := plugintables.PurgeTimeTable(ctx, transaction, sqldb.CoreSchema+".flow_record", "start_at", horizon); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx,
			`DELETE FROM capture_configuration AS c
			 WHERE c.last_read_at < $1
			   AND c.first_read_at < (SELECT max(n.first_read_at) FROM capture_configuration AS n
			                          WHERE n.provider_id = c.provider_id)`, horizon); err != nil {
			return fmt.Errorf("store: purging capture configurations: %w", err)
		}
	}
	if horizon, bounded, err := plugintables.RetentionHorizon(ctx, transaction, now, 0); err != nil {
		return err
	} else if bounded {
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM connection_measured_volume WHERE connection_start_at < $1", horizon); err != nil {
			return fmt.Errorf("store: purging measured volumes: %w", err)
		}
		// The unmatched family's slots go at the flows' horizon, as an aggregate slot that is not
		// composed from finer ones: once the slot has ended before it.
		for _, period := range vocab.Periods() {
			if _, err := transaction.ExecContext(ctx,
				"DELETE FROM unmatched_record_aggregate_"+period.Name+" WHERE period_end_at < $1", horizon); err != nil {
				return fmt.Errorf("store: purging the unmatched family: %w", err)
			}
		}
	}
	return nil
}
