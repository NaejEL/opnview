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
//   - a reconciled set lands whole or not at all, because a half-written snapshot
//     would state that every member missing from it had left;
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

// ActiveProviderID returns the active provider of one EXCLUSIVE kind, and whether
// there is one. None is active on a fresh database: activeness is decided by the
// probe round, never by the schema.
//
// It refuses a kind that admits several active providers, rather than returning
// the first row and letting a caller read one source where there are three. Such a
// caller wants ActiveProviderIDs.
func (s *Store) ActiveProviderID(ctx context.Context, kind string) (int64, bool, error) {
	if KindAdmitsSeveralActiveProviders(kind) {
		return 0, false, fmt.Errorf(
			"store: the %s kind admits several active providers, so asking for THE active one "+
				"would read one source where there may be several", kind)
	}
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

// ActiveProviderIDs returns every active provider of one kind, in registry order.
// For an exclusive kind the answer holds at most one id; for a concurrent one it
// is the whole stack the firewall is running.
func (s *Store) ActiveProviderIDs(ctx context.Context, kind string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM provider WHERE kind = ? AND is_active = 1 ORDER BY provider_key", kind)
	if err != nil {
		return nil, fmt.Errorf("store: looking up the active %s providers: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: looking up the active %s providers: %w", kind, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: looking up the active %s providers: %w", kind, err)
	}
	return ids, nil
}

// SetActiveProviders records which implementations of a kind opnview reads. An
// empty list leaves the kind with no active provider at all, which is a normal
// state.
//
// The deactivation runs first, in one transaction with the activation, because for
// an EXCLUSIVE kind the schema carries a partial unique index over kind where
// is_active = 1 and setting a second one active fails — which is the guarantee, so
// the switch cannot be two separate statements. For a concurrent kind there is no
// such index and the whole list is activated; passing several keys for an
// exclusive kind is refused here rather than left to the index, so the caller gets
// a sentence instead of a constraint violation.
func (s *Store) SetActiveProviders(ctx context.Context, kind string, providerKeys ...string) error {
	if len(providerKeys) > 1 && !KindAdmitsSeveralActiveProviders(kind) {
		return fmt.Errorf("store: the %s kind admits one active provider and %d were given",
			kind, len(providerKeys))
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the provider activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		"UPDATE provider SET is_active = 0 WHERE kind = ?", kind); err != nil {
		return fmt.Errorf("store: deactivating the %s providers: %w", kind, err)
	}
	for _, providerKey := range providerKeys {
		if providerKey == "" {
			continue
		}
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
		                        link_type, link_kind, vlan_tag, is_upstream,
		                        first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (identifier) DO UPDATE SET
		     device       = excluded.device,
		     description  = excluded.description,
		     status       = excluded.status,
		     enabled      = excluded.enabled,
		     link_type    = excluded.link_type,
		     link_kind    = excluded.link_kind,
		     vlan_tag     = excluded.vlan_tag,
		     is_upstream  = excluded.is_upstream,
		     last_seen_at = excluded.last_seen_at`,
		iface.Identifier, iface.Device, iface.Description, iface.Status, iface.Enabled,
		iface.LinkType, iface.LinkKind, iface.VLANTag, boolToInt(iface.IsUpstream), now, now)
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

// UpsertInterfaceAddress records that an interface holds an address, or has a
// gateway behind it, as of now. A value read again moves its last_seen_at; a new
// value is a new row, and its predecessor keeps the last instant it was seen, so
// a change of address is history rather than an overwrite.
//
// A ROW IS ONE HOLDING. The value's latest row is continued only when the previous
// discovery read it too. previousDiscovery is that discovery's instant, which the caller
// captures BEFORE its pass writes anything -- LatestInterfaceDiscoveryAt -- so it does not
// depend on the order the pass writes the interfaces in; nil means there was none, and
// the latest row is continued. A value read again after a discovery that did not read
// it -- released, then re-acquired -- starts a new row at now, so the interval between
// the two holdings belongs to neither and "held at an instant" (the
// this_firewall_address view) does not span it. A discovery that failed writes nothing,
// moves no interface's last_seen_at, and so breaks no holding.
func (s *Store) UpsertInterfaceAddress(ctx context.Context, address InterfaceAddress,
	previousDiscovery *int64, now int64) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: writing an address of interface %d: %w", address.InterfaceID, err)
	}
	defer func() { _ = transaction.Rollback() }()

	var (
		latestID   int64
		latestSeen int64
	)
	err = transaction.QueryRowContext(ctx,
		`SELECT id, last_seen_at FROM interface_address
		 WHERE interface_id = ? AND source_field = ? AND address = ?
		   AND ifnull(prefix_length, -1) = ifnull(?, -1)
		 ORDER BY first_seen_at DESC LIMIT 1`,
		address.InterfaceID, address.SourceField, address.Address, address.PrefixLength).
		Scan(&latestID, &latestSeen)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: reading an address of interface %d: %w", address.InterfaceID, err)
	}
	if found && (previousDiscovery == nil || latestSeen >= *previousDiscovery) {
		_, err = transaction.ExecContext(ctx,
			"UPDATE interface_address SET last_seen_at = max(last_seen_at, ?) WHERE id = ?", now, latestID)
	} else {
		_, err = transaction.ExecContext(ctx,
			`INSERT INTO interface_address (interface_id, source_field, address, prefix_length,
			                                address_family, first_seen_at, last_seen_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (interface_id, source_field, address, ifnull(prefix_length, -1), first_seen_at)
			 DO UPDATE SET last_seen_at = max(interface_address.last_seen_at, excluded.last_seen_at)`,
			address.InterfaceID, address.SourceField, address.Address, address.PrefixLength,
			address.AddressFamily, now, now)
	}
	if err != nil {
		return fmt.Errorf("store: writing an address of interface %d: %w", address.InterfaceID, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing an address of interface %d: %w", address.InterfaceID, err)
	}
	return nil
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
		`INSERT INTO rule (pf_label, description, action, direction, interface, legacy,
		                   logs_matches, enabled, is_automatic, discovered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (pf_label) DO UPDATE SET
		     description   = excluded.description,
		     action        = excluded.action,
		     direction     = excluded.direction,
		     interface     = excluded.interface,
		     legacy        = excluded.legacy,
		     logs_matches  = excluded.logs_matches,
		     enabled       = excluded.enabled,
		     is_automatic  = excluded.is_automatic,
		     discovered_at = excluded.discovered_at`,
		rule.PfLabel, rule.Description, rule.Action, rule.Direction, rule.Interface,
		boolToNullableInt(rule.Legacy), boolToNullableInt(rule.LogsMatches), boolToNullableInt(rule.Enabled),
		boolToInt(rule.IsAutomatic), now)
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
	return upsertClient(ctx, s.db, client, now)
}

// upsertClient is UpsertClient on a querier, so classification can mint a
// client inside its own transaction.
func upsertClient(ctx context.Context, q querier, client Client, now int64) (int64, error) {
	_, err := q.ExecContext(ctx,
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
	if err := q.QueryRowContext(ctx,
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

// InsertDHCPLease writes one lease generation, attributed to the server that issued
// it. A reissue is a second row rather than an overwrite, so ON CONFLICT DO NOTHING
// is the whole of the idempotence, and the conflict is on
// (address, generation_key, provider_id).
//
// Two of those three are there for reasons worth keeping together. The GENERATION
// KEY rather than the validity start, because the start is null on every backend
// that cannot report one and a null in a uniqueness constraint is distinct from
// every other null — keying on it would have made a re-poll of such a lease insert
// a row every time. The PROVIDER, because this kind admits several concurrently
// active servers, so one address legitimately leased on two scopes is two leases
// and not one contested row. The provider is a parameter rather than a field of the
// struct so that a caller cannot leave it out of a sixteen-field literal and
// discover it from a foreign-key violation.
func (s *Store) InsertDHCPLease(ctx context.Context, providerID int64, lease DHCPLease) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dhcp_lease (provider_id, client_id, backend, address, mac, hostname,
		                         dhcp_client_id, duid, iaid, vendor_hint, lease_state,
		                         interface_id, starts_at, generation_key, expires_at, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (address, generation_key, provider_id) DO NOTHING`,
		providerID, lease.ClientID, lease.Backend, lease.Address, lease.MAC, lease.Hostname,
		lease.DHCPClientID, lease.DUID, lease.IAID, lease.VendorHint, lease.LeaseState,
		lease.InterfaceID, lease.StartsAt, lease.GenerationKey, lease.ExpiresAt, lease.ObservedAt)
	if err != nil {
		return fmt.Errorf("store: writing a lease: %w", err)
	}
	return nil
}

// InsertFlow writes one filter-log record, or does nothing if its digest is
// already stored, or if it was observed before the furthest horizon the retention
// purge has applied (retention_purge).
//
// THAT CHECK IS PART OF THE INSERT STATEMENT, AND THAT IS THE POINT. The purge runs in
// its own loop, ordered against a pass only from the pass's first stored row to the end
// of its derivation (internal/collect, purge.go), and moves the records it removes
// into the purged part of their hour; the filter-log page keeps offering them. A
// collector that read the watermark first and inserted afterwards could store again a
// record a purge removed in between, and count it twice. SQLite runs one write at a
// time, so the watermark read inside the insert sees every purge that committed before
// it, and a purge that commits after it purges the row once.
//
// traffic_scope is not a parameter: it is derived here from interface membership
// through ScopeOf, so a collector cannot write a scope that disagrees with the
// row's interfaces. The schema's CHECK would reject one anyway; this makes the
// rejection unreachable rather than merely caught.
func (s *Store) InsertFlow(ctx context.Context, flow Flow) error {
	// A record is stored with neither end recognised as this firewall: that is
	// classification's to decide, from the addresses the firewall held at the
	// record's instant (classify.go).
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device,
		                   interface_lookup_state, src_interface_id, dst_interface_id,
		                   src_client_id, dst_client_id, src_address, dst_address,
		                   src_port, dst_port, protocol, ip_version, action, direction,
		                   log_reason, packet_bytes, rid, rule_id, rule_lookup_state,
		                   ip_id, tcp_seq, traffic_scope)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM retention_purge WHERE purged_before > ?)
		 ON CONFLICT (log_digest) DO NOTHING`,
		flow.LogDigest, flow.ObservedAt, flow.IngestedAt, flow.InterfaceDevice,
		string(flow.InterfaceLookupState), flow.SrcInterfaceID, flow.DstInterfaceID,
		flow.SrcClientID, flow.DstClientID, flow.SrcAddress, flow.DstAddress,
		flow.SrcPort, flow.DstPort, flow.Protocol, flow.IPVersion, flow.Action,
		flow.Direction, flow.LogReason, flow.PacketBytes, flow.Rid, flow.RuleID,
		string(flow.RuleLookupState), flow.IPID, flow.TCPSeq,
		string(ScopeOf(flow.SrcInterfaceID, flow.DstInterfaceID, false, false)),
		flow.ObservedAt)
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
// a row first when the endpoint named one. A lookup made before the furthest horizon
// the purge has applied is not stored, checked inside the insert for the reason
// InsertFlow gives: the resolver's buffer keeps offering lookups the purge removed.
func (s *Store) InsertDNSResolution(ctx context.Context, lookup DNSResolution) error {
	_, err := s.StoreDNSResolution(ctx, lookup)
	return err
}

// StoreDNSResolution is InsertDNSResolution, reporting whether the lookup was stored:
// false when its lookup_key is already held or it is older than the purge horizon.
// The resolver collector decides from it, and not from the lookup's instant, whether a
// row was new: two different lookups can share an instant.
func (s *Store) StoreDNSResolution(ctx context.Context, lookup DNSResolution) (bool, error) {
	var blocklistID *int64
	if lookup.BlocklistName != "" {
		id, err := s.UpsertBlocklist(ctx, lookup.BlocklistName, lookup.IngestedAt)
		if err != nil {
			return false, err
		}
		blocklistID = &id
	}
	resolution := lookup.ClientResolution
	if resolution == "" {
		resolution = ClientResolutionLoggedAddress
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO dns_resolution (lookup_key, client_address, client_hostname,
		                             client_resolution, client_id, domain, resolver,
		                             action, answer_source, rcode, dnssec_status, blocklist_id,
		                             looked_up_at, ingested_at)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM retention_purge WHERE purged_before > ?)
		 ON CONFLICT (lookup_key) DO NOTHING`,
		lookup.LookupKey, lookup.ClientAddress, lookup.ClientHostname, resolution,
		lookup.ClientID, lookup.Domain, lookup.Resolver,
		lookup.Action, lookup.AnswerSource, lookup.Rcode, lookup.DNSSECStatus, blocklistID,
		lookup.LookedUpAt, lookup.IngestedAt, lookup.LookedUpAt)
	if err != nil {
		return false, fmt.Errorf("store: writing a resolver lookup: %w", err)
	}
	stored, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: writing a resolver lookup: %w", err)
	}
	return stored > 0, nil
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

// THERE IS NO COLLECTOR WRITE PATH FOR pair_volume_observation, and that is the
// ruling rather than an omission: the table is DERIVED from `flow`, because the only
// per-pair endpoint carries neither a port nor a protocol and `flow` carries both.
// The derivation is the statement insert_pair_volume of derive.sql, run by the
// refresh in aggregate.go, and a test fails if anything else writes the table.

// InsertMeasurementSample writes one reading. Re-reading the same instant is a
// no-op, which is what a sampler restarting inside one interval needs.
//
// The conflict target names ifnull(provider_id, -1) because the uniqueness of a
// reading includes the provider — this kind admits several concurrently active
// providers, and two of them reading one subject at one instant are two readings —
// and because the firewall's own gauges carry a null provider, which a bare column
// in a uniqueness constraint would make distinct from every other null.
func (s *Store) InsertMeasurementSample(ctx context.Context, sample MeasurementSample) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
		                                 unit, value, sampled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (subject_kind, subject_key, measure, sampled_at, ifnull(provider_id, -1))
		 DO NOTHING`,
		sample.ProviderID, string(sample.SubjectKind), sample.SubjectKey,
		string(sample.Measure), string(sample.Unit), sample.Value, sample.SampledAt)
	if err != nil {
		return fmt.Errorf("store: writing a %s measurement: %w", sample.Measure, err)
	}
	return nil
}

// InsertStateSnapshot writes one complete set and its members, in one transaction.
//
// THE TRANSACTION IS THE POINT, not a precaution. A snapshot asserts that the set
// was complete at one instant, and a departure is computed by comparing one
// snapshot's members against the next one's; a half-written snapshot would
// therefore not be a slightly incomplete row, it would be a false statement that
// every member missing from it had LEFT. So either the whole set lands or none of
// it does.
//
// Re-reading the same instant is a no-op, which is what a poller restarting inside
// one interval needs: the snapshot identity is (provider, set, instant).
func (s *Store) InsertStateSnapshot(ctx context.Context, snapshot StateSnapshot) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the %s snapshot: %w", snapshot.SetKey, err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO state_snapshot (provider_id, set_key, captured_at) VALUES (?, ?, ?)
		 ON CONFLICT (provider_id, set_key, captured_at) DO NOTHING`,
		snapshot.ProviderID, snapshot.SetKey, snapshot.CapturedAt); err != nil {
		return fmt.Errorf("store: writing the %s snapshot: %w", snapshot.SetKey, err)
	}

	var snapshotID int64
	if err := transaction.QueryRowContext(ctx,
		`SELECT id FROM state_snapshot
		 WHERE provider_id = ? AND set_key = ? AND captured_at = ?`,
		snapshot.ProviderID, snapshot.SetKey, snapshot.CapturedAt).Scan(&snapshotID); err != nil {
		return fmt.Errorf("store: reading back the %s snapshot: %w", snapshot.SetKey, err)
	}

	for _, item := range snapshot.Items {
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO state_item (snapshot_id, item_key, attributes, valid_until_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (snapshot_id, item_key) DO UPDATE SET
			     attributes     = excluded.attributes,
			     valid_until_at = excluded.valid_until_at`,
			snapshotID, item.ItemKey, item.Attributes, item.ValidUntilAt); err != nil {
			return fmt.Errorf("store: writing a member of the %s set: %w", snapshot.SetKey, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the %s snapshot: %w", snapshot.SetKey, err)
	}
	return nil
}

// StateItemsAt returns the members of one snapshot, by set and instant.
func (s *Store) StateItemsAt(ctx context.Context, providerID int64, setKey string,
	capturedAt int64) ([]StateItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT i.item_key, i.attributes, i.valid_until_at
		 FROM state_snapshot AS s
		 JOIN state_item AS i ON i.snapshot_id = s.id
		 WHERE s.provider_id = ? AND s.set_key = ? AND s.captured_at = ?
		 ORDER BY i.item_key`,
		providerID, setKey, capturedAt)
	if err != nil {
		return nil, fmt.Errorf("store: reading the %s set: %w", setKey, err)
	}
	defer func() { _ = rows.Close() }()

	var items []StateItem
	for rows.Next() {
		var item StateItem
		if err := rows.Scan(&item.ItemKey, &item.Attributes, &item.ValidUntilAt); err != nil {
			return nil, fmt.Errorf("store: reading the %s set: %w", setKey, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the %s set: %w", setKey, err)
	}
	return items, nil
}

// StateDepartures returns what was in the previous complete snapshot of a set and
// is not in the latest one.
//
// It reads the state_item_departure view rather than composing the comparison
// here, so the statement a screen reads and the statement a test asserts are the
// same one. A set observed only once yields nothing, which is correct: nothing can
// be said to have left a set seen once.
func (s *Store) StateDepartures(ctx context.Context, providerID int64, setKey string) (
	[]StateDeparture, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT provider_id, set_key, item_key, attributes, valid_until_at,
		        last_present_at, absent_since_at
		 FROM state_item_departure
		 WHERE provider_id = ? AND set_key = ?
		 ORDER BY item_key`,
		providerID, setKey)
	if err != nil {
		return nil, fmt.Errorf("store: reading the departures from the %s set: %w", setKey, err)
	}
	defer func() { _ = rows.Close() }()

	var departures []StateDeparture
	for rows.Next() {
		var departure StateDeparture
		if err := rows.Scan(&departure.ProviderID, &departure.SetKey, &departure.ItemKey,
			&departure.Attributes, &departure.ValidUntilAt,
			&departure.LastPresentAt, &departure.AbsentSinceAt); err != nil {
			return nil, fmt.Errorf("store: reading the departures from the %s set: %w", setKey, err)
		}
		departures = append(departures, departure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the departures from the %s set: %w", setKey, err)
	}
	return departures, nil
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

// LatestInterfaceDiscoveryAt returns the instant of the latest discovery that wrote the
// interfaces -- every interface of one discovery carries its instant in last_seen_at --
// and whether there was one. A discovery pass reads it before it writes anything, and
// hands it to UpsertInterfaceAddress as the previous discovery.
func (s *Store) LatestInterfaceDiscoveryAt(ctx context.Context) (int64, bool, error) {
	var latest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT max(last_seen_at) FROM interface").Scan(&latest); err != nil {
		return 0, false, fmt.Errorf("store: reading the latest discovery: %w", err)
	}
	return latest.Int64, latest.Valid, nil
}
