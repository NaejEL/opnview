package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The typed write paths. Every guarantee the model rests on is implemented once,
// here, rather than in each collector:
//
//   - re-ingesting a page inserts nothing new, because each identity is a
//     uniqueness constraint and every insert names it in ON CONFLICT;
//   - flow.traffic_scope is written from interface membership alone, through
//     ScopeOf, which is the one expression the schema's CHECK pins;
//   - a pair volume is stored under its canonical ordering, so the firewall's
//     direction doubling collapses instead of doubling the volume;
//   - a discovery refresh never overwrites a user's own label, and a user's own
//     label never overwrites discovery.

// ProviderID returns the id of one registry row.
func (s *Store) ProviderID(ctx context.Context, kind, providerKey string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM provider WHERE kind = ? AND provider_key = ?", kind, providerKey).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: looking up provider %s/%s: %w", kind, providerKey, err)
	}
	return id, nil
}

// ProviderKey returns the provider_key of one registry row. It is how a collector
// learns which implementation the probe round activated without the registry's
// names being compiled into a branch anywhere else.
func (s *Store) ProviderKey(ctx context.Context, providerID int64) (string, error) {
	var key string
	if err := s.db.QueryRowContext(ctx,
		"SELECT provider_key FROM provider WHERE id = ?", providerID).Scan(&key); err != nil {
		return "", fmt.Errorf("store: reading provider %d: %w", providerID, err)
	}
	return key, nil
}

// ActiveProviderID returns the active provider of one kind, and whether there is
// one. None is active on a fresh database: activeness is decided by the probe
// round, never by the schema.
func (s *Store) ActiveProviderID(ctx context.Context, kind string) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM provider WHERE kind = ? AND is_active = 1", kind).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("store: looking up the active %s provider: %w", kind, err)
	}
	return id, true, nil
}

// SetActiveProvider makes one implementation of a kind the active one, or, when
// providerKey is empty, leaves the kind with no active provider at all.
//
// The deactivation runs first because the schema carries a partial unique index
// over kind where is_active = 1: setting a second one active fails, which is the
// guarantee, so the switch has to be two statements in one transaction.
func (s *Store) SetActiveProvider(ctx context.Context, kind, providerKey string) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the provider activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		"UPDATE provider SET is_active = 0 WHERE kind = ?", kind); err != nil {
		return fmt.Errorf("store: deactivating the %s providers: %w", kind, err)
	}
	if providerKey != "" {
		result, err := transaction.ExecContext(ctx,
			"UPDATE provider SET is_active = 1 WHERE kind = ? AND provider_key = ?", kind, providerKey)
		if err != nil {
			return fmt.Errorf("store: activating %s/%s: %w", kind, providerKey, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: activating %s/%s: %w", kind, providerKey, err)
		}
		if affected != 1 {
			return fmt.Errorf("store: %s/%s is not a registered provider", kind, providerKey)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the provider activation: %w", err)
	}
	return nil
}

// SetAvailability records the state of one provider, the probe that determined
// it and when. Exactly one row exists per registry row for the life of the
// database, so this is an update and never an insert.
func (s *Store) SetAvailability(ctx context.Context, providerID int64,
	state AvailabilityState, probe string, detail *string, checkedAt int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE source_availability
		 SET state = ?, probe = ?, detail = ?, checked_at = ?
		 WHERE provider_id = ?`,
		string(state), probe, detail, checkedAt, providerID)
	if err != nil {
		return fmt.Errorf("store: recording availability for provider %d: %w", providerID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: recording availability for provider %d: %w", providerID, err)
	}
	if affected != 1 {
		return fmt.Errorf("store: provider %d has no availability row", providerID)
	}
	return nil
}

// Availability reads back one provider's state, probe and instant.
func (s *Store) Availability(ctx context.Context, providerID int64) (AvailabilityState, string, int64, error) {
	var (
		state     string
		probe     string
		checkedAt int64
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT state, probe, checked_at FROM source_availability WHERE provider_id = ?", providerID).
		Scan(&state, &probe, &checkedAt)
	if err != nil {
		return "", "", 0, fmt.Errorf("store: reading availability for provider %d: %w", providerID, err)
	}
	return AvailabilityState(state), probe, checkedAt, nil
}

// UpsertInterface writes one discovered interface and returns its id.
//
// user_label is absent from the statement on purpose: a discovery refresh must
// never overwrite a label the maintainer set, and first_seen_at survives for the
// same reason — the row's history is not rediscovered every five minutes.
func (s *Store) UpsertInterface(ctx context.Context, iface Interface, now int64) (int64, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO interface (identifier, device, description, status, enabled,
		                        link_type, link_kind, vlan_tag, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (identifier) DO UPDATE SET
		     device       = excluded.device,
		     description  = excluded.description,
		     status       = excluded.status,
		     enabled      = excluded.enabled,
		     link_type    = excluded.link_type,
		     link_kind    = excluded.link_kind,
		     vlan_tag     = excluded.vlan_tag,
		     last_seen_at = excluded.last_seen_at`,
		iface.Identifier, iface.Device, iface.Description, iface.Status, iface.Enabled,
		iface.LinkType, iface.LinkKind, iface.VLANTag, now, now)
	if err != nil {
		return 0, fmt.Errorf("store: writing interface %s: %w", iface.Identifier, err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM interface WHERE identifier = ?", iface.Identifier).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back interface %s: %w", iface.Identifier, err)
	}
	return id, nil
}

// UpsertInterfaceMapEntry writes one device-name-to-description entry, the first
// of the two first-class join keys.
func (s *Store) UpsertInterfaceMapEntry(ctx context.Context, device, description string,
	interfaceID *int64, now int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO interface_map (device, description, interface_id, discovered_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (device) DO UPDATE SET
		     description   = excluded.description,
		     interface_id  = excluded.interface_id,
		     discovered_at = excluded.discovered_at`,
		device, description, interfaceID, now)
	if err != nil {
		return fmt.Errorf("store: writing interface map entry %s: %w", device, err)
	}
	return nil
}

// UpsertRule writes one discovered rule and returns its id.
func (s *Store) UpsertRule(ctx context.Context, rule Rule, now int64) (int64, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rule (pf_label, description, action, direction, logs_matches,
		                   is_automatic, discovered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (pf_label) DO UPDATE SET
		     description   = excluded.description,
		     action        = excluded.action,
		     direction     = excluded.direction,
		     logs_matches  = excluded.logs_matches,
		     is_automatic  = excluded.is_automatic,
		     discovered_at = excluded.discovered_at`,
		rule.PfLabel, rule.Description, rule.Action, rule.Direction,
		boolToNullableInt(rule.LogsMatches), boolToInt(rule.IsAutomatic), now)
	if err != nil {
		return 0, fmt.Errorf("store: writing rule %s: %w", rule.PfLabel, err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM rule WHERE pf_label = ?", rule.PfLabel).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back rule %s: %w", rule.PfLabel, err)
	}
	return id, nil
}

// UpsertClient writes one machine and returns its id.
//
// owner_id is absent from the statement on purpose, exactly as user_label is
// absent from UpsertInterface: ownership is assigned by hand and nothing derives
// it from a hostname, a MAC prefix, a vendor hint or an address.
func (s *Store) UpsertClient(ctx context.Context, client Client, now int64) (int64, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO client (identity_kind, identity_key, interface_id, mac, hostname,
		                     vendor_hint, last_address, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (identity_kind, identity_key) DO UPDATE SET
		     interface_id = coalesce(excluded.interface_id, client.interface_id),
		     mac          = coalesce(excluded.mac, client.mac),
		     hostname     = coalesce(excluded.hostname, client.hostname),
		     vendor_hint  = coalesce(excluded.vendor_hint, client.vendor_hint),
		     last_address = coalesce(excluded.last_address, client.last_address),
		     last_seen_at = excluded.last_seen_at`,
		client.Identity.Kind, client.Identity.Key, client.InterfaceID, client.MAC,
		client.Hostname, client.VendorHint, client.LastAddress, now, now)
	if err != nil {
		return 0, fmt.Errorf("store: writing client %s/%s: %w", client.Identity.Kind, client.Identity.Key, err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM client WHERE identity_kind = ? AND identity_key = ?",
		client.Identity.Kind, client.Identity.Key).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back client %s/%s: %w",
			client.Identity.Kind, client.Identity.Key, err)
	}
	return id, nil
}

// ClientRefByAddressSince returns the machine most recently seen at an address,
// and the interface it was seen behind, provided it was seen no earlier than
// notBefore.
//
// The bound is what makes an address reissued to another machine stay two clients.
// A lease reissue is separated by the lease's own identity; an address that no
// lease ever named is separated by this window, and by nothing else — see the
// cascade's level 3 in internal/collect.
func (s *Store) ClientRefByAddressSince(ctx context.Context, address string, notBefore int64) (
	int64, *int64, bool, error) {
	var (
		id          int64
		interfaceID *int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, interface_id FROM client
		 WHERE last_address = ? AND last_seen_at >= ?
		 ORDER BY last_seen_at DESC, id DESC LIMIT 1`,
		address, notBefore).Scan(&id, &interfaceID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil, false, nil
	case err != nil:
		return 0, nil, false, fmt.Errorf("store: looking up a client by address: %w", err)
	}
	return id, interfaceID, true, nil
}

// InsertDHCPLease writes one lease generation. A reissue is a second row rather
// than an overwrite, so ON CONFLICT DO NOTHING is the whole of the idempotence.
func (s *Store) InsertDHCPLease(ctx context.Context, lease DHCPLease) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dhcp_lease (client_id, backend, address, mac, hostname, dhcp_client_id,
		                         duid, iaid, vendor_hint, lease_state, interface_id,
		                         starts_at, expires_at, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (address, starts_at, backend) DO NOTHING`,
		lease.ClientID, lease.Backend, lease.Address, lease.MAC, lease.Hostname,
		lease.DHCPClientID, lease.DUID, lease.IAID, lease.VendorHint, lease.LeaseState,
		lease.InterfaceID, lease.StartsAt, lease.ExpiresAt, lease.ObservedAt)
	if err != nil {
		return fmt.Errorf("store: writing a lease: %w", err)
	}
	return nil
}

// InsertFlow writes one filter-log record, or does nothing if its digest is
// already stored.
//
// traffic_scope is not a parameter: it is derived here from interface membership
// through ScopeOf, so a collector cannot write a scope that disagrees with the
// row's interfaces. The schema's CHECK would reject one anyway; this makes the
// rejection unreachable rather than merely caught.
func (s *Store) InsertFlow(ctx context.Context, flow Flow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device,
		                   interface_lookup_state, src_interface_id, dst_interface_id,
		                   src_client_id, dst_client_id, src_address, dst_address,
		                   src_port, dst_port, protocol, ip_version, action, direction,
		                   log_reason, packet_bytes, rid, rule_id, rule_lookup_state,
		                   traffic_scope)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (log_digest) DO NOTHING`,
		flow.LogDigest, flow.ObservedAt, flow.IngestedAt, flow.InterfaceDevice,
		string(flow.InterfaceLookupState), flow.SrcInterfaceID, flow.DstInterfaceID,
		flow.SrcClientID, flow.DstClientID, flow.SrcAddress, flow.DstAddress,
		flow.SrcPort, flow.DstPort, flow.Protocol, flow.IPVersion, flow.Action,
		flow.Direction, flow.LogReason, flow.PacketBytes, flow.Rid, flow.RuleID,
		string(flow.RuleLookupState), string(ScopeOf(flow.SrcInterfaceID, flow.DstInterfaceID)))
	if err != nil {
		return fmt.Errorf("store: writing a flow: %w", err)
	}
	return nil
}

// NewestFlowObservedAt returns the newest observed_at stored, and whether there
// is one. It is what the filter-log collector compares the oldest line of a page
// against in order to detect a gap rather than assume one.
func (s *Store) NewestFlowObservedAt(ctx context.Context) (int64, bool, error) {
	var newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT max(observed_at) FROM flow").Scan(&newest); err != nil {
		return 0, false, fmt.Errorf("store: reading the newest flow instant: %w", err)
	}
	if !newest.Valid {
		return 0, false, nil
	}
	return newest.Int64, true, nil
}

// HasFlowDigest reports whether a filter-log record is already stored. It is what
// makes the echoed record harmless without an insert attempt per line.
func (s *Store) HasFlowDigest(ctx context.Context, digest string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM flow WHERE log_digest = ?", digest).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: looking up a flow digest: %w", err)
	}
	return true, nil
}

// UpsertBlocklist writes one observed list name and returns its id. The NAME is
// observed; the PURPOSE is user input and is absent from this statement, exactly
// as owner_id and user_label are absent from theirs.
func (s *Store) UpsertBlocklist(ctx context.Context, name string, now int64) (int64, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO blocklist (name, first_seen_at, last_seen_at) VALUES (?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		name, now, now)
	if err != nil {
		return 0, fmt.Errorf("store: writing blocklist %s: %w", name, err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM blocklist WHERE name = ?", name).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back blocklist %s: %w", name, err)
	}
	return id, nil
}

// InsertDNSResolution writes one resolver lookup, resolving the blocklist name to
// a row first when the endpoint named one.
func (s *Store) InsertDNSResolution(ctx context.Context, lookup DNSResolution) error {
	var blocklistID *int64
	if lookup.BlocklistName != "" {
		id, err := s.UpsertBlocklist(ctx, lookup.BlocklistName, lookup.IngestedAt)
		if err != nil {
			return err
		}
		blocklistID = &id
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dns_resolution (lookup_key, client_address, client_id, domain, resolver,
		                             action, answer_source, rcode, dnssec_status, blocklist_id,
		                             looked_up_at, ingested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (lookup_key) DO NOTHING`,
		lookup.LookupKey, lookup.ClientAddress, lookup.ClientID, lookup.Domain, lookup.Resolver,
		lookup.Action, lookup.AnswerSource, lookup.Rcode, lookup.DNSSECStatus, blocklistID,
		lookup.LookedUpAt, lookup.IngestedAt)
	if err != nil {
		return fmt.Errorf("store: writing a resolver lookup: %w", err)
	}
	return nil
}

// NewestDNSResolutionAt returns the newest looked_up_at stored, and whether there
// is one. The resolver endpoint is a ring buffer with no working window, so this
// is the only thing a gap can be measured against.
func (s *Store) NewestDNSResolutionAt(ctx context.Context) (int64, bool, error) {
	var newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		"SELECT max(looked_up_at) FROM dns_resolution").Scan(&newest); err != nil {
		return 0, false, fmt.Errorf("store: reading the newest lookup instant: %w", err)
	}
	if !newest.Valid {
		return 0, false, nil
	}
	return newest.Int64, true, nil
}

// InsertSecurityEvent writes one event, or does nothing if that provider has
// already contributed that key.
func (s *Store) InsertSecurityEvent(ctx context.Context, providerID int64, event SecurityEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
		                             rule_identity, signature, event_action, normalised_severity,
		                             src_address, src_port, dst_address, dst_port, protocol,
		                             in_interface_device, src_client_id, src_interface_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (provider_id, provider_event_key) DO NOTHING`,
		providerID, event.ProviderEventKey, event.OccurredAt, event.IngestedAt,
		event.RuleIdentity, event.Signature, event.EventAction, event.NormalisedSeverity,
		event.SrcAddress, event.SrcPort, event.DstAddress, event.DstPort, event.Protocol,
		event.InInterfaceDevice, event.SrcClientID, event.SrcInterfaceID)
	if err != nil {
		return fmt.Errorf("store: writing a security event: %w", err)
	}
	return nil
}

// UpsertEveCursor writes a watermark. The unique key is (file_id, byte_offset),
// so re-reading the same offset is a no-op, and a rotation state that changed is
// recorded on the row that already exists.
func (s *Store) UpsertEveCursor(ctx context.Context, cursor EveCursor) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO eve_ingest_cursor (file_id, byte_offset, file_sequence, rotation_state, observed_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (file_id, byte_offset) DO UPDATE SET
		     file_sequence  = excluded.file_sequence,
		     rotation_state = excluded.rotation_state,
		     observed_at    = excluded.observed_at`,
		cursor.FileID, cursor.ByteOffset, cursor.FileSequence, cursor.RotationState, cursor.ObservedAt)
	if err != nil {
		return fmt.Errorf("store: writing the eve watermark for %s: %w", cursor.FileID, err)
	}
	return nil
}

// EveWatermark is where the reader stopped in one eve.json file.
type EveWatermark struct {
	// ByteOffset is the highest filepos read in that file.
	ByteOffset int64
	// ObservedAt is when that offset was recorded. It is the only instant a lost
	// rotation can date its gap from.
	ObservedAt int64
	// RotationState is the state already recorded for the file.
	RotationState string
}

// EveWatermarks returns the highest byte offset read per eve file, which is where
// a restart resumes. Paging is offset-from-end-of-file and therefore unstable, so
// this is the only durable resume point.
func (s *Store) EveWatermarks(ctx context.Context) (map[string]EveWatermark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_id, max(byte_offset), max(observed_at),
		        max(CASE WHEN rotation_state = 'lost' THEN 1 ELSE 0 END)
		 FROM eve_ingest_cursor GROUP BY file_id`)
	if err != nil {
		return nil, fmt.Errorf("store: reading the eve watermarks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	watermarks := make(map[string]EveWatermark)
	for rows.Next() {
		var (
			fileID     string
			offset     int64
			observedAt int64
			lost       int
		)
		if err := rows.Scan(&fileID, &offset, &observedAt, &lost); err != nil {
			return nil, fmt.Errorf("store: reading the eve watermarks: %w", err)
		}
		state := "current"
		if lost == 1 {
			state = "lost"
		}
		watermarks[fileID] = EveWatermark{
			ByteOffset: offset, ObservedAt: observedAt, RotationState: state,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the eve watermarks: %w", err)
	}
	return watermarks, nil
}

// MarkEveFileLost records that rotation discarded a watermarked file. The loss is
// permanent, so the state is written rather than the watermark being reset.
func (s *Store) MarkEveFileLost(ctx context.Context, fileID string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE eve_ingest_cursor SET rotation_state = 'lost', observed_at = ? WHERE file_id = ?",
		now, fileID)
	if err != nil {
		return fmt.Errorf("store: marking eve file %s lost: %w", fileID, err)
	}
	return nil
}

// InsertPairVolumeObservation writes one daily per-pair volume under its
// canonical ordering.
//
// The ordering is applied here and not by the caller, which is what makes the
// firewall's direction doubling collapse: the aggregate writes each flow once per
// interface and once per direction with the endpoints swapped, so sorting the two
// addresses before the insert turns both observations into the same key.
func (s *Store) InsertPairVolumeObservation(ctx context.Context, pair PairVolumeObservation) error {
	low, high := pair.EndpointA, pair.EndpointB
	if low > high {
		low, high = high, low
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
		                                      service_port, protocol, octets, packets,
		                                      observed_direction, observed_interface_device,
		                                      last_seen_at, ingested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (day_start_at, endpoint_low, endpoint_high, service_port, protocol)
		 DO NOTHING`,
		pair.DayStartAt, low, high, pair.ServicePort, pair.Protocol, pair.Octets, pair.Packets,
		pair.ObservedDirection, pair.ObservedInterfaceDevice, pair.LastSeenAt, pair.IngestedAt)
	if err != nil {
		return fmt.Errorf("store: writing a pair volume observation: %w", err)
	}
	return nil
}

// InsertMeasurementSample writes one reading. Re-reading the same instant is a
// no-op, which is what a sampler restarting inside one interval needs.
func (s *Store) InsertMeasurementSample(ctx context.Context, sample MeasurementSample) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
		                                 unit, value, sampled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (subject_kind, subject_key, measure, sampled_at) DO NOTHING`,
		sample.ProviderID, string(sample.SubjectKind), sample.SubjectKey,
		string(sample.Measure), string(sample.Unit), sample.Value, sample.SampledAt)
	if err != nil {
		return fmt.Errorf("store: writing a %s measurement: %w", sample.Measure, err)
	}
	return nil
}

// RecordCollectionGap writes one interval opnview knows it did not cover. It is
// never smoothed over and never written as a zero.
func (s *Store) RecordCollectionGap(ctx context.Context, gap CollectionGap) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at,
		                             reason, detail, detected_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		gap.ProviderID, gap.IntervalStartAt, gap.IntervalEndAt,
		string(gap.Reason), gap.Detail, gap.DetectedAt)
	if err != nil {
		return fmt.Errorf("store: recording a %s gap: %w", gap.Reason, err)
	}
	return nil
}

// CountRows returns the number of rows in one table. It exists for the tests and
// for the diagnostics a later cycle puts on a screen; it takes no user input, so
// the table name is never interpolated from anything but a constant in the
// caller.
func (s *Store) CountRows(ctx context.Context, table string) (int, error) {
	var count int
	// The table name cannot be a bound parameter in SQL. Every caller passes a
	// literal, and an unknown name fails here rather than reading anything.
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM \""+table+"\"").Scan(&count); err != nil {
		return 0, fmt.Errorf("store: counting %s: %w", table, err)
	}
	return count, nil
}

// boolToInt renders a Go bool as the 0 or 1 the schema's CHECK expects.
func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// boolToNullableInt renders a three-state flag: true, false, or not reported.
func boolToNullableInt(value *bool) *int64 {
	if value == nil {
		return nil
	}
	rendered := boolToInt(*value)
	return &rendered
}
