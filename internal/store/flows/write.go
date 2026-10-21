package flows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
func (s *FlowStore) ProviderID(ctx context.Context, kind, providerKey string) (int64, error) {
	var id int64
	err := s.SQL.QueryRowContext(ctx,
		"SELECT id FROM provider WHERE kind = $1 AND provider_key = $2", kind, providerKey).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: looking up provider %s/%s: %w", kind, providerKey, err)
	}
	return id, nil
}

// RegisterDeclarations writes what declarations say of the registry, in four statements whatever their
// number (PA2 of the plugin-API amendment): each kind's attribute, its provider_kind row, inserted or
// brought up to date; each provider's registry row, inserted when absent and otherwise left exactly as it
// is, activeness included; the availability row every registry row has; and each declared gap reason's
// gap_reason row, inserted or given its declarer (decision 4 of the amendment's first verification). It is
// how the DDL can seed no product row: the built-ins' declarations write theirs when they load, and a
// plugin added later writes its own the same way. A reason no longer declared keeps its row, which the
// gaps recorded under it still reference.
// A provider of a kind the declarations do not name carries no attribute, and the row is refused.
func (s *FlowStore) RegisterDeclarations(ctx context.Context, kinds []vocab.ProviderKind,
	providers []vocab.ProviderDeclaration, gapReasons []vocab.GapReasonDeclaration, now int64) error {
	kindNames := make([]string, 0, len(kinds))
	concurrent := make([]bool, 0, len(kinds))
	for _, kind := range kinds {
		kindNames = append(kindNames, kind.Kind)
		concurrent = append(concurrent, kind.Concurrent)
	}
	keys := make([]string, 0, len(providers))
	providerKinds := make([]string, 0, len(providers))
	names := make([]string, 0, len(providers))
	for _, declared := range providers {
		providerKinds = append(providerKinds, declared.Kind)
		keys = append(keys, declared.Key)
		names = append(names, declared.DisplayName)
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the registration of the declarations: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO provider_kind (kind, concurrent) SELECT k, c FROM unnest(CAST($1 AS TEXT[]), CAST($2 AS BOOLEAN[])) AS d (k, c) "+
			"ON CONFLICT (kind) DO UPDATE SET concurrent = EXCLUDED.concurrent "+
			"WHERE provider_kind.concurrent IS DISTINCT FROM EXCLUDED.concurrent", kindNames, concurrent); err != nil {
		return fmt.Errorf("store: registering the kinds: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO provider (kind, provider_key, display_name, concurrent, is_active, registered_at) "+
			"SELECT d.k, d.p, d.n, pk.concurrent, false, $4 FROM unnest(CAST($1 AS TEXT[]), CAST($2 AS TEXT[]), CAST($3 AS TEXT[])) AS d (k, p, n) "+
			"LEFT JOIN provider_kind AS pk ON pk.kind = d.k "+
			"ON CONFLICT (kind, provider_key) DO NOTHING", providerKinds, keys, names, now); err != nil {
		return fmt.Errorf("store: registering the providers: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO source_availability (provider_id, state, outcome, probe, detail, checked_at) "+
			"SELECT p.id, 'unavailable', 'not_yet_probed', 'not_yet_probed', NULL, 0 FROM provider AS p "+
			"WHERE NOT EXISTS (SELECT 1 FROM source_availability AS a WHERE a.provider_id = p.id)"); err != nil {
		return fmt.Errorf("store: giving the providers their availability rows: %w", err)
	}
	reasons := make([]string, 0, len(gapReasons))
	declarers := make([]string, 0, len(gapReasons))
	for _, declared := range gapReasons {
		reasons = append(reasons, string(declared.Reason))
		declarers = append(declarers, declared.DeclaredBy)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO gap_reason (reason, declared_by) SELECT r, d FROM unnest(CAST($1 AS TEXT[]), CAST($2 AS TEXT[])) AS g (r, d) "+
			"ON CONFLICT (reason) DO UPDATE SET declared_by = EXCLUDED.declared_by "+
			"WHERE gap_reason.declared_by IS DISTINCT FROM EXCLUDED.declared_by", reasons, declarers); err != nil {
		return fmt.Errorf("store: registering the gap reasons: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing the registration of the declarations: %w", err)
	}
	return nil
}

// kindConcurrent reads whether a kind admits several active providers, from its provider_kind row: the
// declaration's attribute, which the schema's index enforces. A kind no declaration registered is
// exclusive.
func (s *FlowStore) kindConcurrent(ctx context.Context, q sqldb.Querier, kind string) (bool, error) {
	var concurrent bool
	err := q.QueryRowContext(ctx, "SELECT concurrent FROM provider_kind WHERE kind = $1", kind).Scan(&concurrent)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: reading the %s kind's attribute: %w", kind, err)
	}
	return concurrent, nil
}

// EnsureProvider returns the id of a registry row, creating it, and the availability row the
// schema gives every registry row, when it does not exist yet. created reports whether it did
// not.
//
// It is what lets an implementation be added as a directory and nothing else: an
// implementation declares its kind, key and display name in its provider.json, and the
// collector registers it here at the first probe round, so no schema file lists it. The
// schema's own seed holds the implementations that shipped first; a row that exists is left
// exactly as it is, activeness and availability included, and the call then writes nothing.
// A new row is inactive (activeness is the probe round's to decide) and starts as
// `unavailable` / `not_yet_probed`, which is the seed's own starting state. A kind outside the
// schema's CHECK list is refused by the schema, and the refusal is returned.
func (s *FlowStore) EnsureProvider(ctx context.Context, kind, providerKey, displayName string,
	now int64) (int64, bool, error) {
	var id int64
	err := s.SQL.QueryRowContext(ctx,
		"SELECT id FROM provider WHERE kind = $1 AND provider_key = $2", kind, providerKey).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("store: looking up provider %s/%s: %w", kind, providerKey, err)
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("store: starting the registration of %s/%s: %w", kind, providerKey, err)
	}
	defer func() { _ = transaction.Rollback() }()
	// A kind no declaration registered yet takes the default of a plugin's own kind: one active provider
	// at a time. A declaration registered later states its own (RegisterDeclarations).
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO provider_kind (kind, concurrent) VALUES ($1, false) ON CONFLICT (kind) DO NOTHING", kind); err != nil {
		return 0, false, fmt.Errorf("store: registering the kind of %s/%s: %w", kind, providerKey, err)
	}
	result, err := transaction.ExecContext(ctx,
		"INSERT INTO provider (kind, provider_key, display_name, concurrent, is_active, registered_at) "+
			"SELECT k.kind, $2, $3, k.concurrent, false, $4 FROM provider_kind AS k WHERE k.kind = $1 "+
			"ON CONFLICT (kind, provider_key) DO NOTHING",
		kind, providerKey, displayName, now)
	if err != nil {
		return 0, false, fmt.Errorf("store: registering provider %s/%s: %w", kind, providerKey, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("store: registering provider %s/%s: %w", kind, providerKey, err)
	}
	if err := transaction.QueryRowContext(ctx,
		"SELECT id FROM provider WHERE kind = $1 AND provider_key = $2", kind, providerKey).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("store: looking up provider %s/%s: %w", kind, providerKey, err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO source_availability (provider_id, state, outcome, probe, detail, checked_at) "+
			"SELECT CAST($1 AS BIGINT), 'unavailable', 'not_yet_probed', 'not_yet_probed', NULL, 0 "+
			"WHERE NOT EXISTS (SELECT 1 FROM source_availability WHERE provider_id = $2)", id, id); err != nil {
		return 0, false, fmt.Errorf("store: giving provider %s/%s its availability row: %w", kind, providerKey, err)
	}
	if err := transaction.Commit(); err != nil {
		return 0, false, fmt.Errorf("store: committing the registration of %s/%s: %w", kind, providerKey, err)
	}
	return id, inserted == 1, nil
}

// ProviderKey returns the provider_key of one registry row. It is how a collector
// learns which implementation the probe round activated without the registry's
// names being compiled into a branch anywhere else.
func (s *FlowStore) ProviderKey(ctx context.Context, providerID int64) (string, error) {
	var key string
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT provider_key FROM provider WHERE id = $1", providerID).Scan(&key); err != nil {
		return "", fmt.Errorf("store: reading provider %d: %w", providerID, err)
	}
	return key, nil
}

// ActiveProviderIDs returns every active provider of one kind, in registry order.
// For an exclusive kind the answer holds at most one id; for a concurrent one it
// is the whole stack the firewall is running.
func (s *FlowStore) ActiveProviderIDs(ctx context.Context, kind string) ([]int64, error) {
	rows, err := s.SQL.QueryContext(ctx,
		"SELECT id FROM provider WHERE kind = $1 AND is_active ORDER BY provider_key", kind)
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
// is_active and setting a second one active fails — which is the guarantee, so
// the switch cannot be two separate statements. For a concurrent kind there is no
// such index and the whole list is activated; passing several keys for an
// exclusive kind is refused here rather than left to the index, so the caller gets
// a sentence instead of a constraint violation.
func (s *FlowStore) SetActiveProviders(ctx context.Context, kind string, providerKeys ...string) error {
	concurrent, err := s.kindConcurrent(ctx, s.SQL, kind)
	if err != nil {
		return err
	}
	if len(providerKeys) > 1 && !concurrent {
		return fmt.Errorf("store: the %s kind admits one active provider and %d were given",
			kind, len(providerKeys))
	}
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting the provider activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		"UPDATE provider SET is_active = false WHERE kind = $1", kind); err != nil {
		return fmt.Errorf("store: deactivating the %s providers: %w", kind, err)
	}
	for _, providerKey := range providerKeys {
		if providerKey == "" {
			continue
		}
		result, err := transaction.ExecContext(ctx,
			"UPDATE provider SET is_active = true WHERE kind = $1 AND provider_key = $2", kind, providerKey)
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

// SetAvailability records the state of one provider, the outcome code of the probe
// or read that determined it, the probe, and when. Exactly one row exists per
// registry row for the life of the database, so this is an update and never an
// insert. An outcome outside AvailabilityOutcomes is refused here, before the
// schema's CHECK would refuse it.
func (s *FlowStore) SetAvailability(ctx context.Context, providerID int64,
	state vocab.AvailabilityState, outcome vocab.AvailabilityOutcome, probe string, detail *string,
	checkedAt int64) error {
	if !outcome.Valid() {
		return fmt.Errorf("store: %q is not an availability outcome (provider %d)", outcome, providerID)
	}
	// The bounds of when the state began (coverage row 39 of docs/widget-api.md). The SET
	// expressions read the row's PREVIOUS values, as PostgreSQL evaluates an UPDATE: a check
	// finding another state than the recorded one bounds the change between the previous
	// check (checked_at, still the old value here) and this one. The row's first check after
	// it was created bounds nothing, because "not yet probed" is not a state anything was
	// seen in; a check finding the same state keeps the bounds it had.
	result, err := s.SQL.ExecContext(ctx,
		`UPDATE source_availability
		 SET state_since_after_at = CASE
		         WHEN state = $1 THEN state_since_after_at
		         WHEN outcome = 'not_yet_probed' THEN NULL
		         ELSE LEAST(checked_at, $2) END,
		     state_since_by_at = CASE
		         WHEN state = $1 THEN state_since_by_at
		         WHEN outcome = 'not_yet_probed' THEN NULL
		         ELSE $2 END,
		     state = $1, outcome = $3, probe = $4, detail = $5,
		     checked_at = $2
		 WHERE provider_id = $6`,
		string(state), checkedAt, string(outcome), probe, detail, providerID)
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
func (s *FlowStore) Availability(ctx context.Context, providerID int64) (vocab.AvailabilityState, string, int64, error) {
	var (
		state     string
		probe     string
		checkedAt int64
	)
	err := s.SQL.QueryRowContext(ctx,
		"SELECT state, probe, checked_at FROM source_availability WHERE provider_id = $1", providerID).
		Scan(&state, &probe, &checkedAt)
	if err != nil {
		return "", "", 0, fmt.Errorf("store: reading availability for provider %d: %w", providerID, err)
	}
	return vocab.AvailabilityState(state), probe, checkedAt, nil
}

// UpsertInterface writes one discovered interface and returns its id.
//
// user_label is absent from the statement on purpose: a discovery refresh must
// never overwrite a label the maintainer set, and first_seen_at survives for the
// same reason — the row's history is not rediscovered every five minutes.
func (s *FlowStore) UpsertInterface(ctx context.Context, iface vocab.Interface, now int64) (int64, error) {
	_, err := s.SQL.ExecContext(ctx,
		`INSERT INTO interface (identifier, device, description, status, enabled,
		                        link_type, link_kind, vlan_tag, is_upstream,
		                        first_seen_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
		iface.LinkType, iface.LinkKind, iface.VLANTag, iface.IsUpstream, now, now)
	if err != nil {
		return 0, fmt.Errorf("store: writing interface %s: %w", iface.Identifier, err)
	}
	var id int64
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT id FROM interface WHERE identifier = $1", iface.Identifier).Scan(&id); err != nil {
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
func (s *FlowStore) UpsertInterfaceAddress(ctx context.Context, address vocab.InterfaceAddress,
	previousDiscovery *int64, now int64) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
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
		 WHERE interface_id = $1 AND source_field = $2 AND address = $3
		   AND coalesce(prefix_length, -1) = coalesce($4, -1)
		 ORDER BY first_seen_at DESC LIMIT 1`,
		address.InterfaceID, address.SourceField, address.Address, address.PrefixLength).
		Scan(&latestID, &latestSeen)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: reading an address of interface %d: %w", address.InterfaceID, err)
	}
	if found && (previousDiscovery == nil || latestSeen >= *previousDiscovery) {
		_, err = transaction.ExecContext(ctx,
			"UPDATE interface_address SET last_seen_at = GREATEST(last_seen_at, $1) WHERE id = $2", now, latestID)
	} else {
		_, err = transaction.ExecContext(ctx,
			`INSERT INTO interface_address (interface_id, source_field, address, prefix_length,
			                                address_family, first_seen_at, last_seen_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (interface_id, source_field, address, prefix_length, first_seen_at)
			 DO UPDATE SET last_seen_at = GREATEST(interface_address.last_seen_at, excluded.last_seen_at)`,
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
func (s *FlowStore) UpsertInterfaceMapEntry(ctx context.Context, device, description string,
	interfaceID *int64, now int64) error {
	_, err := s.SQL.ExecContext(ctx,
		`INSERT INTO interface_map (device, description, interface_id, discovered_at)
		 VALUES ($1, $2, $3, $4)
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
func (s *FlowStore) UpsertRule(ctx context.Context, rule vocab.Rule, now int64) (int64, error) {
	_, err := s.SQL.ExecContext(ctx,
		`INSERT INTO rule (pf_label, description, action, direction, interface, legacy,
		                   logs_matches, enabled, is_automatic, discovered_at, destination_net)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (pf_label) DO UPDATE SET
		     description     = excluded.description,
		     action          = excluded.action,
		     direction       = excluded.direction,
		     interface       = excluded.interface,
		     legacy          = excluded.legacy,
		     logs_matches    = excluded.logs_matches,
		     enabled         = excluded.enabled,
		     is_automatic    = excluded.is_automatic,
		     discovered_at   = excluded.discovered_at,
		     destination_net = excluded.destination_net`,
		rule.PfLabel, rule.Description, rule.Action, rule.Direction, rule.Interface,
		rule.Legacy, rule.LogsMatches, rule.Enabled,
		rule.IsAutomatic, now, rule.DestinationNet)
	if err != nil {
		return 0, fmt.Errorf("store: writing rule %s: %w", rule.PfLabel, err)
	}
	var id int64
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT id FROM rule WHERE pf_label = $1", rule.PfLabel).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back rule %s: %w", rule.PfLabel, err)
	}
	return id, nil
}

// UpsertClient writes one machine and returns its id.
//
// owner_id is absent from the statement on purpose, exactly as user_label is
// absent from UpsertInterface: ownership is assigned by hand and nothing derives
// it from a hostname, a MAC prefix, a vendor hint or an address.
func (s *FlowStore) UpsertClient(ctx context.Context, client vocab.Client, now int64) (int64, error) {
	return upsertClient(ctx, s.SQL, client, now)
}

// upsertClient is UpsertClient on a querier, so classification can mint a
// client inside its own transaction.
func upsertClient(ctx context.Context, q sqldb.Querier, client vocab.Client, now int64) (int64, error) {
	ids, err := upsertClients(ctx, q, []vocab.Client{client}, now)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// UpsertClients is UpsertClient over every client of a pass, in ONE statement whatever their number
// (PA17 of specs/SPEC-backend-completion-plugins.md). It returns each client's id, in the order given.
// Two clients of one identity are written as the second of two UpsertClient calls would leave the
// row: each field the later one reports replaces the earlier one's.
func (s *FlowStore) UpsertClients(ctx context.Context, clients []vocab.Client, now int64) ([]int64, error) {
	return upsertClients(ctx, s.SQL, clients, now)
}

func upsertClients(ctx context.Context, q sqldb.Querier, clients []vocab.Client, now int64) ([]int64, error) {
	if len(clients) == 0 {
		return nil, nil
	}
	type identity struct{ kind, key string }
	merged := map[identity]int{}
	var rows []map[string]any
	for _, client := range clients {
		key := identity{string(client.Identity.Kind), client.Identity.Key}
		index, seen := merged[key]
		if !seen {
			index = len(rows)
			merged[key] = index
			rows = append(rows, map[string]any{"identity_kind": key.kind, "identity_key": key.key, "ordinal": index})
		}
		row := rows[index]
		for name, value := range map[string]*string{"mac": client.MAC, "hostname": client.Hostname,
			"vendor_hint": client.VendorHint, "last_address": client.LastAddress} {
			if value != nil {
				row[name] = *value
			}
		}
		if client.InterfaceID != nil {
			row["interface_id"] = *client.InterfaceID
		}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("store: encoding clients: %w", err)
	}
	result, err := q.QueryContext(ctx,
		`INSERT INTO client (identity_kind, identity_key, interface_id, mac, hostname,
		                     vendor_hint, last_address, first_seen_at, last_seen_at)
		 SELECT r.identity_kind, r.identity_key, r.interface_id, r.mac, r.hostname, r.vendor_hint,
		        r.last_address, $2, $2
		 FROM jsonb_to_recordset(CAST($1 AS jsonb)) AS r (identity_kind TEXT,
		      identity_key TEXT, interface_id BIGINT, mac TEXT, hostname TEXT, vendor_hint TEXT,
		      last_address TEXT, ordinal BIGINT)
		 ORDER BY r.ordinal
		 ON CONFLICT (identity_kind, identity_key) DO UPDATE SET
		     interface_id = coalesce(excluded.interface_id, client.interface_id),
		     mac          = coalesce(excluded.mac, client.mac),
		     hostname     = coalesce(excluded.hostname, client.hostname),
		     vendor_hint  = coalesce(excluded.vendor_hint, client.vendor_hint),
		     last_address = coalesce(excluded.last_address, client.last_address),
		     last_seen_at = excluded.last_seen_at
		 RETURNING id, identity_kind, identity_key`, string(encoded), now)
	if err != nil {
		return nil, fmt.Errorf("store: writing %d clients: %w", len(rows), err)
	}
	defer func() { _ = result.Close() }()
	idOf := map[identity]int64{}
	for result.Next() {
		var id int64
		var key identity
		if err := result.Scan(&id, &key.kind, &key.key); err != nil {
			return nil, fmt.Errorf("store: reading back the clients: %w", err)
		}
		idOf[key] = id
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("store: reading back the clients: %w", err)
	}
	ids := make([]int64, len(clients))
	for index, client := range clients {
		id, found := idOf[identity{string(client.Identity.Kind), client.Identity.Key}]
		if !found {
			return nil, fmt.Errorf("store: the client %s/%s was not written", client.Identity.Kind,
				client.Identity.Key)
		}
		ids[index] = id
	}
	return ids, nil
}

// ClientRefByAddressSince returns the machine most recently seen at an address,
// and the interface it was seen behind, provided it was seen no earlier than
// notBefore.
//
// The bound is what makes an address reissued to another machine stay two clients.
// A lease reissue is separated by the lease's own identity; an address that no
// lease ever named is separated by this window, and by nothing else — see the
// cascade's level 3 in internal/collect.
func (s *FlowStore) ClientRefByAddressSince(ctx context.Context, address string, notBefore int64) (
	int64, *int64, bool, error) {
	var (
		id          int64
		interfaceID *int64
	)
	err := s.SQL.QueryRowContext(ctx,
		`SELECT id, interface_id FROM client
		 WHERE last_address = $1 AND last_seen_at >= $2
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

// InsertFlow writes one filter-log record, or does nothing if its digest is
// already stored, or if it was observed before the furthest horizon the retention
// purge has applied (retention_purge).
//
// THAT CHECK IS PART OF THE INSERT STATEMENT, AND THAT IS THE POINT. The purge runs in its own
// loop, ordered against a pass only from the pass's first stored row to the end of its derivation
// (internal/collect, internal/store/purge/purge.go), and moves the records it removes into the
// purged part of their hour; the filter-log page keeps offering them. A collector that read the
// watermark first and inserted afterwards could store again a record a purge removed in between,
// and count it twice. Read inside the insert, the watermark is read in the insert's own snapshot,
// which holds every purge that committed before it; a purge that commits after it cannot see the
// row it has not committed yet, leaves it, and the next purge removes it -- once.
//
// traffic_scope is not a parameter: it is derived here from interface membership
// through ScopeOf, so a collector cannot write a scope that disagrees with the
// row's interfaces. The schema's CHECK would reject one anyway; this makes the
// rejection unreachable rather than merely caught.
func (s *FlowStore) InsertFlow(ctx context.Context, flow vocab.Flow) error {
	return s.InsertFlows(ctx, []vocab.Flow{flow})
}

// InsertFlows is InsertFlow over every record of a pass, in ONE statement whatever their number
// (PA17 of specs/SPEC-backend-completion-plugins.md): the rows travel as one JSON array read back by
// jsonb_to_recordset, so a page of a thousand records costs the statements a page of ten does. Each
// row is written or refused exactly as InsertFlow writes or refuses it.
func (s *FlowStore) InsertFlows(ctx context.Context, flows []vocab.Flow) error {
	if len(flows) == 0 {
		return nil
	}
	rows := make([]map[string]any, len(flows))
	for index, flow := range flows {
		rows[index] = map[string]any{
			"log_digest": flow.LogDigest, "observed_at": flow.ObservedAt, "ingested_at": flow.IngestedAt,
			"interface_device": flow.InterfaceDevice, "interface_lookup_state": string(flow.InterfaceLookupState),
			"src_interface_id": flow.SrcInterfaceID, "dst_interface_id": flow.DstInterfaceID,
			"src_client_id": flow.SrcClientID, "dst_client_id": flow.DstClientID,
			"src_address": flow.SrcAddress, "dst_address": flow.DstAddress, "src_port": flow.SrcPort,
			"dst_port": flow.DstPort, "protocol": flow.Protocol, "ip_version": flow.IPVersion,
			"action": flow.Action, "direction": flow.Direction, "log_reason": flow.LogReason,
			"packet_bytes": flow.PacketBytes, "rid": flow.Rid, "rule_id": flow.RuleID,
			"rule_lookup_state": string(flow.RuleLookupState), "ip_id": flow.IPID, "tcp_seq": flow.TCPSeq,
			"traffic_scope": string(vocab.ScopeOf(flow.SrcInterfaceID, flow.DstInterfaceID, false, false)),
		}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("store: encoding flows: %w", err)
	}
	// A record is stored with neither end recognised as this firewall: that is classification's to
	// decide, from the addresses the firewall held at the record's instant (classify.go).
	_, err = s.SQL.ExecContext(ctx,
		`INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device,
		                   interface_lookup_state, src_interface_id, dst_interface_id,
		                   src_client_id, dst_client_id, src_address, dst_address,
		                   src_port, dst_port, protocol, ip_version, action, direction,
		                   log_reason, packet_bytes, rid, rule_id, rule_lookup_state,
		                   ip_id, tcp_seq, traffic_scope)
		 SELECT r.log_digest, r.observed_at, r.ingested_at, r.interface_device, r.interface_lookup_state,
		        r.src_interface_id, r.dst_interface_id, r.src_client_id, r.dst_client_id, r.src_address,
		        r.dst_address, r.src_port, r.dst_port, r.protocol, r.ip_version, r.action, r.direction,
		        r.log_reason, r.packet_bytes, r.rid, r.rule_id, r.rule_lookup_state, r.ip_id, r.tcp_seq,
		        r.traffic_scope
		 FROM jsonb_to_recordset(CAST($1 AS jsonb)) AS r (log_digest TEXT, observed_at BIGINT,
		      ingested_at BIGINT, interface_device TEXT, interface_lookup_state TEXT, src_interface_id BIGINT,
		      dst_interface_id BIGINT, src_client_id BIGINT, dst_client_id BIGINT, src_address TEXT,
		      dst_address TEXT, src_port BIGINT, dst_port BIGINT, protocol TEXT, ip_version BIGINT,
		      action TEXT, direction TEXT, log_reason TEXT, packet_bytes BIGINT, rid TEXT, rule_id BIGINT,
		      rule_lookup_state TEXT, ip_id BIGINT, tcp_seq BIGINT, traffic_scope TEXT)
		 WHERE NOT EXISTS (SELECT 1 FROM retention_purge WHERE purged_before > r.observed_at)
		 ON CONFLICT (log_digest) DO NOTHING`, string(encoded))
	if err != nil {
		return fmt.Errorf("store: writing flows: %w", err)
	}
	return nil
}

// StoredFlowDigests reports which of the digests are stored already, in one statement.
func (s *FlowStore) StoredFlowDigests(ctx context.Context, digests []string) (map[string]bool, error) {
	stored := map[string]bool{}
	if len(digests) == 0 {
		return stored, nil
	}
	encoded, err := json.Marshal(digests)
	if err != nil {
		return nil, fmt.Errorf("store: encoding flow digests: %w", err)
	}
	rows, err := s.SQL.QueryContext(ctx, `SELECT log_digest FROM flow
		WHERE log_digest IN (SELECT jsonb_array_elements_text(CAST($1 AS jsonb)))`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("store: looking up flow digests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, fmt.Errorf("store: looking up flow digests: %w", err)
		}
		stored[digest] = true
	}
	return stored, rows.Err()
}

// NewestFlowObservedAt returns the newest observed_at stored, and whether there
// is one. It is what the filter-log collector compares the oldest line of a page
// against in order to detect a gap rather than assume one.
func (s *FlowStore) NewestFlowObservedAt(ctx context.Context) (int64, bool, error) {
	var newest sql.NullInt64
	if err := s.SQL.QueryRowContext(ctx, "SELECT max(observed_at) FROM flow").Scan(&newest); err != nil {
		return 0, false, fmt.Errorf("store: reading the newest flow instant: %w", err)
	}
	if !newest.Valid {
		return 0, false, nil
	}
	return newest.Int64, true, nil
}

// HasFlowDigest reports whether a filter-log record is already stored. It is what
// makes the echoed record harmless without an insert attempt per line.
func (s *FlowStore) HasFlowDigest(ctx context.Context, digest string) (bool, error) {
	var one int
	err := s.SQL.QueryRowContext(ctx, "SELECT 1 FROM flow WHERE log_digest = $1", digest).Scan(&one)
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
//
// lookedUpAt is the instant of the lookup that named the list, and the two instants
// are the lookups', not the ingestion's (decision 14 of
// specs/SPEC-step-5b-widget-api.md): first_seen_at is the earliest looked_up_at of a
// lookup naming the list and last_seen_at the latest, so "when did this list last
// refuse something" is answered by the resolver's own clock. NEITHER EVER MOVES
// BACKWARDS. The resolver's ring buffer offers the same lookups again on every pass
// and a pass may meet an older lookup after a newer one, so each instant only ever
// widens: min() for the first, max() for the last. Writing the ingestion instant, as
// this did until step 5B, made last_seen_at the time opnview read the buffer, which
// is later than any lookup and says nothing about the list.
func (s *FlowStore) UpsertBlocklist(ctx context.Context, name string, lookedUpAt int64) (int64, error) {
	_, err := s.SQL.ExecContext(ctx,
		`INSERT INTO blocklist (name, first_seen_at, last_seen_at) VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE SET
		     first_seen_at = LEAST(blocklist.first_seen_at, excluded.first_seen_at),
		     last_seen_at  = GREATEST(blocklist.last_seen_at, excluded.last_seen_at)`,
		name, lookedUpAt, lookedUpAt)
	if err != nil {
		return 0, fmt.Errorf("store: writing blocklist %s: %w", name, err)
	}
	var id int64
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT id FROM blocklist WHERE name = $1", name).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: reading back blocklist %s: %w", name, err)
	}
	return id, nil
}

// StoreDNSResolution is InsertDNSResolution, reporting whether the lookup was stored:
// false when its lookup_key is already held or it is older than the purge horizon.
// The resolver collector decides from it, and not from the lookup's instant, whether a
// row was new: two different lookups can share an instant.
func (s *FlowStore) StoreDNSResolution(ctx context.Context, lookup vocab.DNSResolution) (bool, error) {
	var blocklistID *int64
	if lookup.BlocklistName != "" {
		id, err := s.UpsertBlocklist(ctx, lookup.BlocklistName, lookup.LookedUpAt)
		if err != nil {
			return false, err
		}
		blocklistID = &id
	}
	resolution := lookup.ClientResolution
	if resolution == "" {
		resolution = vocab.ClientResolutionLoggedAddress
	}
	result, err := s.SQL.ExecContext(ctx,
		`INSERT INTO dns_resolution (lookup_key, client_address, client_hostname,
		                             client_resolution, client_family, client_id, domain, resolver,
		                             action, answer_source, rcode, dnssec_status, blocklist_id,
		                             looked_up_at, ingested_at)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		 WHERE NOT EXISTS (SELECT 1 FROM retention_purge WHERE purged_before > $16)
		 ON CONFLICT (lookup_key) DO NOTHING`,
		lookup.LookupKey, lookup.ClientAddress, lookup.ClientHostname, resolution,
		lookup.ClientFamily, lookup.ClientID, lookup.Domain, lookup.Resolver,
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
func (s *FlowStore) NewestDNSResolutionAt(ctx context.Context) (int64, bool, error) {
	var newest sql.NullInt64
	if err := s.SQL.QueryRowContext(ctx,
		"SELECT max(looked_up_at) FROM dns_resolution").Scan(&newest); err != nil {
		return 0, false, fmt.Errorf("store: reading the newest lookup instant: %w", err)
	}
	if !newest.Valid {
		return 0, false, nil
	}
	return newest.Int64, true, nil
}

// RecordCollectionGap writes one interval opnview knows it did not cover. It is
// never smoothed over and never written as a zero.
func (s *FlowStore) RecordCollectionGap(ctx context.Context, gap vocab.CollectionGap) error {
	_, err := s.SQL.ExecContext(ctx,
		`INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at,
		                             reason, detail, detected_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		gap.ProviderID, gap.IntervalStartAt, gap.IntervalEndAt,
		string(gap.Reason), gap.Detail, gap.DetectedAt)
	if err != nil {
		return fmt.Errorf("store: recording a %s gap: %w", gap.Reason, err)
	}
	return nil
}

// LatestInterfaceDiscoveryAt returns the instant of the latest discovery that wrote the
// interfaces -- every interface of one discovery carries its instant in last_seen_at --
// and whether there was one. A discovery pass reads it before it writes anything, and
// hands it to UpsertInterfaceAddress as the previous discovery.
func (s *FlowStore) LatestInterfaceDiscoveryAt(ctx context.Context) (int64, bool, error) {
	var latest sql.NullInt64
	if err := s.SQL.QueryRowContext(ctx, "SELECT max(last_seen_at) FROM interface").Scan(&latest); err != nil {
		return 0, false, fmt.Errorf("store: reading the latest discovery: %w", err)
	}
	return latest.Int64, latest.Valid, nil
}
