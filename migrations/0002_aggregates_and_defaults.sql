-- opnview migration 0002 — pre-computed volume aggregates, and the defaults
-- the model guarantees exist.
--
-- The four aggregates are materialised tables, not views. Two reasons, both
-- from the step-1 survey (docs/opnsense-api-survey.md, data source 3):
--
--   1. They are opnview's PRIMARY store of volume, not a cache over Insight.
--      Per-address-pair volume exists on the firewall only at daily
--      resolution and only for 62 days, and per-source totals at 300 s
--      resolution are kept for one hour. The 1 h and 24 h periods therefore
--      cannot be served from Insight at all; they are computed from opnview's
--      own stored observations.
--   2. A view over a growing base table cannot satisfy the query-plan
--      requirement: every read of it would rescan the base table.
--
-- The four tables share one shape. Each row is one period slot for one
-- (source interface, peer) pair:
--   * east-west  — dst_interface_id is set, peer_address is NULL;
--   * north-south — dst_interface_id is NULL, peer_address is the remote address.
-- The uniqueness index coalesces both, because SQLite treats NULLs in a
-- UNIQUE index as distinct and the slot would otherwise not be unique.
--
-- computed_at is the freshness timestamp every aggregate row carries. The
-- refresh contract step 4 must honour is written down in docs/data-model.md.
--
-- Observation-point limit: these figures count only what crossed the
-- firewall. Traffic between two clients behind one interface never reaches the
-- router and is absent from every column here. Every screen presenting them
-- must say so.

CREATE TABLE volume_aggregate_1h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_1h_slot
    ON volume_aggregate_1h (period_start_at, src_interface_id,
                            ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_24h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_24h_slot
    ON volume_aggregate_24h (period_start_at, src_interface_id,
                             ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_7d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_7d_slot
    ON volume_aggregate_7d (period_start_at, src_interface_id,
                            ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_30d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_30d_slot
    ON volume_aggregate_30d (period_start_at, src_interface_id,
                             ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

-- ---------------------------------------------------------------------------
-- The per-owner aggregates — the same four periods, keyed on the person
-- rather than on the interface pair.
--
-- Why they exist: the four tables above carry interfaces and peer addresses
-- and no owner dimension at all, so "how much did Bob's machines move this
-- month" had nothing to read once the question left the flow horizon, which is
-- bounded by the retention_seconds row of setting. That was gap G11 in
-- docs/widget-catalogue.md and this family closes it.
--
-- Keyed as the four above are: the slot is (period_start_at, owner, scope),
-- with the same ifnull wrapper on the NULL-bearing column, because SQLite
-- treats NULLs in a UNIQUE index as distinct. One difference is deliberate and
-- is not a drift: traffic_scope is part of the key here, where above it is
-- derivable from whether dst_interface_id is set. A per-owner slot has no
-- destination interface to derive it from, so it is keyed explicitly.
--
-- owner_id IS NULL IS THE UNASSIGNED BUCKET, AND IT IS NOT OPTIONAL. Ownership
-- is assigned by hand and most machines on a network belong to nobody in
-- particular, so a per-person aggregate that dropped the unowned clients would
-- under-report the network while looking complete. The column is therefore
-- NULL-bearing by design and the unassigned slot is an ordinary row --
-- the same guarantee the "Clients per owner" diagnostic already carries.
--
-- Nothing here infers an owner. These rows are computed from flow joined to
-- client.owner_id, which only a statement somebody wrote can fill.
--
-- client_count is the number of distinct clients that contributed to THIS
-- slot. It is a per-slot figure and MUST NOT be summed across slots: the same
-- machine active in two hours would be counted twice. Summing bytes, allowed
-- and blocked across slots is correct; summing client_count is not.
--
-- Refresh contract: identical to the four above, and written down once in
-- docs/data-model.md. A slot is recomputed when a flow inside its window is
-- ingested; the current slot is recomputed on every pass; computed_at against
-- the newest ingested_at inside the window is the staleness rule; a closed
-- slot is left alone. One addition specific to this family: reassigning a
-- client to another owner, or clearing its owner, invalidates every slot whose
-- window holds a flow from that client, because ownership is an attribute of
-- the person and not of the flow.
--
-- Observation-point limit applies here exactly as above: these are lower
-- bounds, and a per-person screen must say so.
-- ---------------------------------------------------------------------------

CREATE TABLE owner_volume_aggregate_1h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_owner_volume_aggregate_1h_slot
    ON owner_volume_aggregate_1h (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE owner_volume_aggregate_24h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_owner_volume_aggregate_24h_slot
    ON owner_volume_aggregate_24h (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE owner_volume_aggregate_7d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_owner_volume_aggregate_7d_slot
    ON owner_volume_aggregate_7d (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE owner_volume_aggregate_30d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_owner_volume_aggregate_30d_slot
    ON owner_volume_aggregate_30d (period_start_at, ifnull(owner_id, -1), traffic_scope);

-- Configuration defaults.
--
-- retention_seconds: the purge horizon, in seconds, so it can be set to a few
-- hours as easily as to months. 7776000 is 90 days, the documented default; 0
-- means unlimited and purges nothing. No retention duration appears anywhere
-- else: every query and every index reads the horizon from this row.
--
-- aggregate_mode: 'full' shows everything; 'no_domains' is the aggregate mode
-- the roadmap asks for — volumes, interfaces, countries and operators, with no
-- domain name read at all. The Map screen query is that query, and it reads
-- neither dns_resolution nor domain_attribution.
INSERT INTO setting (key, value, updated_at) VALUES
    ('retention_seconds', '7776000', CAST(strftime('%s', 'now') AS INTEGER)),
    ('aggregate_mode', 'full', CAST(strftime('%s', 'now') AS INTEGER));

-- The provider registry, seeded with the implementations that exist and have
-- been surveyed today — one row per IMPLEMENTATION, not one per kind. Unbound
-- and Dnsmasq are two providers of the dns_lookup kind; Kea, Dnsmasq and ISC
-- dhcpd are three of the dhcp_lease kind. Step-1 detection already tells them
-- apart, so "two providers of one kind" is exercised by real rows.
--
-- These names are DATA, not configuration. A firewall with no Suricata still
-- gets a Suricata row, in the 'unavailable' state: that is the modelled-state
-- design working, not a hardcoded assumption about the installation. Nothing
-- in the DDL, in a query or in an index tests any of these strings.
--
-- No provider is active. Activeness says which implementation opnview reads,
-- and it is decided by step-4 detection against a live firewall; a migration
-- has no way to know and must not pretend to. Every citation below is in
-- docs/opnsense-api-survey.md, and docs/data-model.md tabulates them.
INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at) VALUES
    ('firewall_log',     'pf',                'pf filter log',        0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('security_event', 'suricata',          'Suricata',             0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('flow_volume',    'insight',           'NetFlow / Insight',    0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'kea',               'Kea DHCP',             0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'dnsmasq',           'Dnsmasq DHCP',         0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'isc',               'ISC dhcpd',            0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dns_lookup',     'unbound',           'Unbound',              0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dns_lookup',     'dnsmasq',           'Dnsmasq resolver',     0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('geo_asn',        'maxmind_geolite2',  'MaxMind GeoLite2',     0, CAST(strftime('%s', 'now') AS INTEGER));

-- Availability is a modelled state, so exactly one row exists per registry row
-- from the first migration onwards. Until a probe has run, each provider is
-- 'unavailable' with the probe recorded as not yet run — never an absent row,
-- which a screen could not tell apart from a provider that is fine. The row
-- set is derived from the registry, so registering a provider and forgetting
-- its availability row is not possible here.
INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
SELECT id, 'unavailable', 'not_yet_probed', NULL, 0 FROM provider;

INSERT INTO schema_version (filename, applied_at)
VALUES ('0002_aggregates_and_defaults.sql', CAST(strftime('%s', 'now') AS INTEGER));
