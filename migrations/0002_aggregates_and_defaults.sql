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
-- (source segment, peer) pair:
--   * east-west  — dst_segment_id is set, peer_address is NULL;
--   * north-south — dst_segment_id is NULL, peer_address is the remote address.
-- The uniqueness index coalesces both, because SQLite treats NULLs in a
-- UNIQUE index as distinct and the slot would otherwise not be unique.
--
-- computed_at is the freshness timestamp every aggregate row carries. The
-- refresh contract step 4 must honour is written down in docs/data-model.md.
--
-- Observation-point limit: these figures count only what crossed the
-- firewall. Traffic between two devices inside one segment never reaches the
-- router and is absent from every column here. Every screen presenting them
-- must say so.

CREATE TABLE volume_aggregate_1h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_segment_id      INTEGER NOT NULL REFERENCES segment (id),
    dst_segment_id      INTEGER REFERENCES segment (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_1h_slot
    ON volume_aggregate_1h (period_start_at, src_segment_id,
                            ifnull(dst_segment_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_24h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_segment_id      INTEGER NOT NULL REFERENCES segment (id),
    dst_segment_id      INTEGER REFERENCES segment (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_24h_slot
    ON volume_aggregate_24h (period_start_at, src_segment_id,
                             ifnull(dst_segment_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_7d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_segment_id      INTEGER NOT NULL REFERENCES segment (id),
    dst_segment_id      INTEGER REFERENCES segment (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_7d_slot
    ON volume_aggregate_7d (period_start_at, src_segment_id,
                            ifnull(dst_segment_id, -1), ifnull(peer_address, ''));

CREATE TABLE volume_aggregate_30d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_segment_id      INTEGER NOT NULL REFERENCES segment (id),
    dst_segment_id      INTEGER REFERENCES segment (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX uq_volume_aggregate_30d_slot
    ON volume_aggregate_30d (period_start_at, src_segment_id,
                             ifnull(dst_segment_id, -1), ifnull(peer_address, ''));

-- Configuration defaults.
--
-- retention_seconds: the purge horizon, in seconds, so it can be set to a few
-- hours as easily as to months. 7776000 is 90 days, the documented default; 0
-- means unlimited and purges nothing. No retention duration appears anywhere
-- else: every query and every index reads the horizon from this row.
--
-- aggregate_mode: 'full' shows everything; 'no_domains' is the aggregate mode
-- the roadmap asks for — volumes, segments, countries and operators, with no
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
