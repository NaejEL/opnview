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

-- Availability is a modelled state, so the five rows exist from the first
-- migration onwards. Until a probe has run, each source is 'unavailable' with
-- the probe recorded as not yet run — never an absent row, which a screen
-- could not tell apart from a source that is fine.
INSERT INTO source_availability (source, state, probe, detail, checked_at) VALUES
    ('filter_log', 'unavailable', 'not_yet_probed', NULL, 0),
    ('suricata_eve', 'unavailable', 'not_yet_probed', NULL, 0),
    ('netflow_insight', 'unavailable', 'not_yet_probed', NULL, 0),
    ('dhcp_leases', 'unavailable', 'not_yet_probed', NULL, 0),
    ('resolver_dns', 'unavailable', 'not_yet_probed', NULL, 0);

INSERT INTO schema_version (filename, applied_at)
VALUES ('0002_aggregates_and_defaults.sql', CAST(strftime('%s', 'now') AS INTEGER));
