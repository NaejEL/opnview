-- The refresh of the volume rollups (ADR 0021, step 3): the statements of the rollups plugin's
-- SQL-only derivation `refresh`, in the order the manifest lists them, one transaction, as the
-- plugin's role. They are the Go of internal/store/aggregates, moved: the same contract, written down
-- once in docs/data-model.md.
--
--  1. A slot is recomputed when a flow is ingested whose observed_at falls inside it; the
--     recomputation replaces the slot. The current slot of each period is recomputed on every run.
--  2. computed_at is the run's WATERMARK: every flow stamped before it is known to be committed when
--     the slot was read -- the oldest ingestion still in flight when the run began, or the run's
--     instant (:ingest_watermark, a core parameter, or :now). A slot whose computed_at is strictly
--     later than the newest ingested_at inside it, and than the newest purged_at of its purged part,
--     is not rewritten; one whose computed_at is equal or earlier is stale.
--  3. A slot is FORCED, rewritten whatever its computed_at says, when a derivation changed a flow
--     inside it since the last run (plugin_pf.flow.changed_at: placed, paired or attributed again),
--     when a measure's rows were computed for a connection inside it (connection_measure.computed_at),
--     when a change of ownership recorded its hour (owner_reassigned_hour), or when it is current.
--  4. A slot the purge would remove outright -- its own end, and for an hour or a day the end of its
--     ISO week and of its calendar month, all before the retention horizon -- is not rewritten unless
--     it is current.
--  5. An HOUR is computed from the flows inside it plus its purged part; a DAY is composed from its
--     hours; a WEEK and a MONTH from their days. The client family of every slot is regrouped from
--     the peer family of the same slot, the owner family from the client family.
--  6. The day slot rewrites the daily per-address-pair volume as well; the run ends by telling the
--     listeners which periods changed (pg_notify, the channel the events surface relays), forgetting
--     the reassigned hours it read, and advancing its watermark.
--
-- The run works over two tables of the plugin's own: refresh_slot, the candidate slots of every period
-- with whether each is forced and whether it is stale, and refresh_reassigned, the ids of the
-- reassigned hours read. The last run's watermark is refresh_state. :retention is the installation's
-- retention in seconds, 0 for unlimited.

-- statement: refresh_state_init
INSERT INTO plugin_rollups.refresh_state (id, watermark) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;

-- statement: refresh_clear_slots
DELETE FROM plugin_rollups.refresh_slot;

-- statement: refresh_clear_reassigned
DELETE FROM plugin_rollups.refresh_reassigned;

-- statement: refresh_read_reassigned
INSERT INTO plugin_rollups.refresh_reassigned (id)
SELECT id FROM plugin_rollups.owner_reassigned_hour;

-- statement: refresh_hours
INSERT INTO plugin_rollups.refresh_slot (period, slot_start, slot_end, forced, stale)
SELECT '1h', h.start, h.start + 3600, bool_or(h.forced), false
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, false AS forced
    FROM plugin_pf.flow AS f
    WHERE f.ingested_at >= (SELECT watermark FROM plugin_rollups.refresh_state WHERE id = 1)
    UNION ALL
    SELECT p.hour_start_at, false
    FROM plugin_rollups.purged_flow_hour AS p
    WHERE p.purged_at >= (SELECT watermark FROM plugin_rollups.refresh_state WHERE id = 1)
    UNION ALL
    SELECT (f.observed_at / 3600) * 3600, true
    FROM plugin_pf.flow AS f
    WHERE f.changed_at >= (SELECT watermark FROM plugin_rollups.refresh_state WHERE id = 1)
    UNION ALL
    SELECT (m.connection_start_at / 3600) * 3600, true
    FROM connection_measure AS m
    WHERE m.computed_at >= (SELECT watermark FROM plugin_rollups.refresh_state WHERE id = 1)
    UNION ALL
    SELECT r.hour_start_at, true
    FROM plugin_rollups.owner_reassigned_hour AS r
    JOIN plugin_rollups.refresh_reassigned AS x ON x.id = r.id
    UNION ALL
    SELECT (CAST(:now AS BIGINT) / 3600) * 3600, true
) AS h
GROUP BY h.start;

-- statement: refresh_slots_24h
INSERT INTO plugin_rollups.refresh_slot (period, slot_start, slot_end, forced, stale)
SELECT '24h', c.start, c.start + 86400, bool_or(c.forced), false
FROM (
    SELECT (h.slot_start / 86400) * 86400 AS start, h.forced
    FROM plugin_rollups.refresh_slot AS h
    WHERE h.period = '1h'
    UNION ALL
    SELECT (CAST(:now AS BIGINT) / 86400) * 86400, true
) AS c
GROUP BY c.start;

-- statement: refresh_slots_7d
INSERT INTO plugin_rollups.refresh_slot (period, slot_start, slot_end, forced, stale)
SELECT '7d', c.start, c.start + 604800, bool_or(c.forced), false
FROM (
    SELECT (h.slot_start / 86400) * 86400 - (((h.slot_start / 86400) + 3) % 7) * 86400 AS start, h.forced
    FROM plugin_rollups.refresh_slot AS h
    WHERE h.period = '1h'
    UNION ALL
    SELECT (CAST(:now AS BIGINT) / 86400) * 86400 - (((CAST(:now AS BIGINT) / 86400) + 3) % 7) * 86400, true
) AS c
GROUP BY c.start;

-- statement: refresh_slots_30d
INSERT INTO plugin_rollups.refresh_slot (period, slot_start, slot_end, forced, stale)
SELECT '30d', c.start, next_month_start_at(c.start), bool_or(c.forced), false
FROM (
    SELECT CAST(extract(epoch FROM date_trunc('month', to_timestamp(h.slot_start) AT TIME ZONE 'UTC')) AS BIGINT) AS start, h.forced
    FROM plugin_rollups.refresh_slot AS h
    WHERE h.period = '1h'
    UNION ALL
    SELECT CAST(extract(epoch FROM date_trunc('month', to_timestamp(CAST(:now AS BIGINT)) AT TIME ZONE 'UTC')) AS BIGINT), true
) AS c
GROUP BY c.start;

-- statement: refresh_prune_purgeable
DELETE FROM plugin_rollups.refresh_slot AS s
WHERE CAST(:retention AS BIGINT) > 0
  AND s.slot_start <> CASE s.period
      WHEN '1h' THEN (CAST(:now AS BIGINT) / 3600) * 3600
      WHEN '24h' THEN (CAST(:now AS BIGINT) / 86400) * 86400
      WHEN '7d' THEN (CAST(:now AS BIGINT) / 86400) * 86400 - (((CAST(:now AS BIGINT) / 86400) + 3) % 7) * 86400
      ELSE CAST(extract(epoch FROM date_trunc('month', to_timestamp(CAST(:now AS BIGINT)) AT TIME ZONE 'UTC')) AS BIGINT) END
  AND s.slot_end < CAST(:now AS BIGINT) - CAST(:retention AS BIGINT)
  AND (s.period IN ('7d', '30d')
       OR ((s.slot_start / 86400) * 86400 - (((s.slot_start / 86400) + 3) % 7) * 86400 + 604800 < CAST(:now AS BIGINT) - CAST(:retention AS BIGINT)
           AND next_month_start_at(s.slot_start) < CAST(:now AS BIGINT) - CAST(:retention AS BIGINT)));

-- statement: refresh_stale_1h
WITH hour AS (
    SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
    FROM plugin_pf.flow AS f
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
    GROUP BY 1
    UNION ALL
    SELECT q.hour_start_at, max(q.purged_at)
    FROM plugin_rollups.purged_flow_hour AS q
    WHERE q.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND q.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
    GROUP BY 1
),
newest AS (
    SELECT s.slot_start, max(h.newest) AS newest
    FROM plugin_rollups.refresh_slot AS s
    JOIN hour AS h ON h.start >= s.slot_start AND h.start < s.slot_end
    WHERE s.period = '1h'
    GROUP BY s.slot_start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM plugin_rollups.volume_aggregate_1h AS v
    WHERE v.period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h')
    GROUP BY v.period_start_at
),
stale AS (
    SELECT s.slot_start
    FROM plugin_rollups.refresh_slot AS s
    LEFT JOIN newest AS n ON n.slot_start = s.slot_start
    LEFT JOIN computed AS k ON k.start = s.slot_start
    WHERE s.period = '1h'
      AND ((n.newest IS NULL AND k.computed_at IS NOT NULL)
           OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest)))
)
UPDATE plugin_rollups.refresh_slot AS s
SET stale = true
FROM stale AS x
WHERE s.period = '1h' AND s.slot_start = x.slot_start;

-- statement: refresh_stale_24h
WITH hour AS (
    SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
    FROM plugin_pf.flow AS f
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '24h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '24h')
    GROUP BY 1
    UNION ALL
    SELECT q.hour_start_at, max(q.purged_at)
    FROM plugin_rollups.purged_flow_hour AS q
    WHERE q.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '24h') AND q.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '24h')
    GROUP BY 1
),
newest AS (
    SELECT s.slot_start, max(h.newest) AS newest
    FROM plugin_rollups.refresh_slot AS s
    JOIN hour AS h ON h.start >= s.slot_start AND h.start < s.slot_end
    WHERE s.period = '24h'
    GROUP BY s.slot_start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM plugin_rollups.volume_aggregate_24h AS v
    WHERE v.period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h')
    GROUP BY v.period_start_at
),
stale AS (
    SELECT s.slot_start
    FROM plugin_rollups.refresh_slot AS s
    LEFT JOIN newest AS n ON n.slot_start = s.slot_start
    LEFT JOIN computed AS k ON k.start = s.slot_start
    WHERE s.period = '24h'
      AND ((n.newest IS NULL AND k.computed_at IS NOT NULL)
           OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest)))
)
UPDATE plugin_rollups.refresh_slot AS s
SET stale = true
FROM stale AS x
WHERE s.period = '24h' AND s.slot_start = x.slot_start;

-- statement: refresh_stale_7d
WITH hour AS (
    SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
    FROM plugin_pf.flow AS f
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '7d') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '7d')
    GROUP BY 1
    UNION ALL
    SELECT q.hour_start_at, max(q.purged_at)
    FROM plugin_rollups.purged_flow_hour AS q
    WHERE q.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '7d') AND q.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '7d')
    GROUP BY 1
),
newest AS (
    SELECT s.slot_start, max(h.newest) AS newest
    FROM plugin_rollups.refresh_slot AS s
    JOIN hour AS h ON h.start >= s.slot_start AND h.start < s.slot_end
    WHERE s.period = '7d'
    GROUP BY s.slot_start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM plugin_rollups.volume_aggregate_7d AS v
    WHERE v.period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d')
    GROUP BY v.period_start_at
),
stale AS (
    SELECT s.slot_start
    FROM plugin_rollups.refresh_slot AS s
    LEFT JOIN newest AS n ON n.slot_start = s.slot_start
    LEFT JOIN computed AS k ON k.start = s.slot_start
    WHERE s.period = '7d'
      AND ((n.newest IS NULL AND k.computed_at IS NOT NULL)
           OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest)))
)
UPDATE plugin_rollups.refresh_slot AS s
SET stale = true
FROM stale AS x
WHERE s.period = '7d' AND s.slot_start = x.slot_start;

-- statement: refresh_stale_30d
WITH hour AS (
    SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
    FROM plugin_pf.flow AS f
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '30d') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '30d')
    GROUP BY 1
    UNION ALL
    SELECT q.hour_start_at, max(q.purged_at)
    FROM plugin_rollups.purged_flow_hour AS q
    WHERE q.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '30d') AND q.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '30d')
    GROUP BY 1
),
newest AS (
    SELECT s.slot_start, max(h.newest) AS newest
    FROM plugin_rollups.refresh_slot AS s
    JOIN hour AS h ON h.start >= s.slot_start AND h.start < s.slot_end
    WHERE s.period = '30d'
    GROUP BY s.slot_start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM plugin_rollups.volume_aggregate_30d AS v
    WHERE v.period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d')
    GROUP BY v.period_start_at
),
stale AS (
    SELECT s.slot_start
    FROM plugin_rollups.refresh_slot AS s
    LEFT JOIN newest AS n ON n.slot_start = s.slot_start
    LEFT JOIN computed AS k ON k.start = s.slot_start
    WHERE s.period = '30d'
      AND ((n.newest IS NULL AND k.computed_at IS NOT NULL)
           OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest)))
)
UPDATE plugin_rollups.refresh_slot AS s
SET stale = true
FROM stale AS x
WHERE s.period = '30d' AND s.slot_start = x.slot_start;

-- statement: refresh_drop_unchanged
DELETE FROM plugin_rollups.refresh_slot WHERE NOT forced AND NOT stale;

-- statement: delete_volume_1h
DELETE FROM plugin_rollups.volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: delete_rule_1h
DELETE FROM plugin_rollups.rule_volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: delete_domain_1h
DELETE FROM plugin_rollups.domain_volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: delete_peer_1h
DELETE FROM plugin_rollups.peer_volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: delete_client_1h
DELETE FROM plugin_rollups.client_volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: delete_owner_1h
DELETE FROM plugin_rollups.owner_volume_aggregate_1h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h');

-- statement: insert_volume_1h
INSERT INTO plugin_rollups.volume_aggregate_1h (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, measure_parts, computed_at)
SELECT u.start, u.start + 3600, u.src_interface_id, u.dst_interface_id, u.peer_address,
    CASE WHEN u.traffic_direction IN ('to_this_firewall', 'from_this_firewall') THEN 'this_firewall'
         WHEN u.src_interface_id IS NOT NULL AND u.dst_interface_id IS NOT NULL
         THEN 'east_west' ELSE 'north_south' END,
    u.traffic_direction, u.exit_leg_device,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    measure_parts_sum(u.measure_parts),
    coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.src_interface_id, f.dst_interface_id,
           f.peer_address, f.traffic_direction, f.exit_leg_device, f.action,
           f.packet_bytes AS bytes, 1 AS connections, n.parts AS measure_parts
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
    UNION ALL
    SELECT p.hour_start_at, p.src_interface_id, p.dst_interface_id,
           CASE WHEN p.traffic_direction = 'inter_interface' THEN NULL ELSE p.peer_address END,
           p.traffic_direction, p.exit_leg_device, p.action, p.bytes, p.connections, p.measure_parts
    FROM plugin_rollups.purged_flow_hour AS p
    WHERE p.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND p.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
) AS u
WHERE u.start IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h')
GROUP BY u.start, u.src_interface_id, u.dst_interface_id, u.peer_address, u.traffic_direction, u.exit_leg_device;

-- statement: insert_rule_1h
INSERT INTO plugin_rollups.rule_volume_aggregate_1h (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT u.start, u.start + 3600, u.rule_id, u.src_interface_id, u.dst_interface_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    measure_parts_sum(u.measure_parts),
    coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.rule_id, f.src_interface_id, f.dst_interface_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections, n.parts AS measure_parts
    FROM plugin_pf.flow AS f
    LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
    UNION ALL
    SELECT p.hour_start_at, p.rule_id, p.src_interface_id, p.dst_interface_id, p.action, p.bytes, p.connections,
           p.measure_parts
    FROM plugin_rollups.purged_flow_hour AS p
    WHERE p.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND p.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
) AS u
WHERE u.start IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h')
GROUP BY u.start, u.rule_id, u.src_interface_id, u.dst_interface_id;

-- statement: insert_domain_1h
INSERT INTO plugin_rollups.domain_volume_aggregate_1h (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT u.start, u.start + 3600, u.site_name, u.client_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    measure_parts_sum(u.measure_parts),
    coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, a.site_name, f.src_client_id AS client_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections, n.parts AS measure_parts
    FROM plugin_pf.flow AS f
    JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
    LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
      AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
    UNION ALL
    SELECT p.hour_start_at, p.site_name, p.src_client_id, p.action, p.bytes, p.connections, p.measure_parts
    FROM plugin_rollups.purged_flow_hour AS p
    WHERE p.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND p.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
      AND p.site_name IS NOT NULL
) AS u
WHERE u.start IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h')
GROUP BY u.start, u.site_name, u.client_id;

-- statement: insert_peer_1h
INSERT INTO plugin_rollups.peer_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT u.start, u.start + 3600, u.client_id, u.traffic_direction, u.peer_address,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    measure_parts_sum(u.measure_parts),
    coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.local_client_id AS client_id, f.traffic_direction,
           CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address ELSE f.src_address END AS peer_address,
           f.action, f.packet_bytes AS bytes, 1 AS connections, n.parts AS measure_parts
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND f.observed_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
      AND f.local_client_id IS NOT NULL
      AND f.traffic_scope <> 'this_firewall'
      AND f.exit_leg_device IS NULL
    UNION ALL
    SELECT p.hour_start_at, p.local_client_id, p.traffic_direction, p.peer_address, p.action,
           p.bytes, p.connections, p.measure_parts
    FROM plugin_rollups.purged_flow_hour AS p
    WHERE p.hour_start_at >= (SELECT min(slot_start) FROM plugin_rollups.refresh_slot WHERE period = '1h') AND p.hour_start_at < (SELECT max(slot_end) FROM plugin_rollups.refresh_slot WHERE period = '1h')
      AND p.local_client_id IS NOT NULL
      AND p.traffic_direction NOT IN ('to_this_firewall', 'from_this_firewall')
      AND p.exit_leg_device IS NULL
) AS u
WHERE u.start IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '1h')
GROUP BY u.start, u.client_id, u.traffic_direction, u.peer_address;

-- statement: regroup_client_1h
INSERT INTO plugin_rollups.client_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       count(DISTINCT q.peer_address), measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '1h') AS s
JOIN plugin_rollups.peer_volume_aggregate_1h AS q ON q.period_start_at = s.slot_start
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction;

-- statement: regroup_owner_1h
INSERT INTO plugin_rollups.owner_volume_aggregate_1h (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), measure_parts_sum(c.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '1h') AS s
JOIN plugin_rollups.client_volume_aggregate_1h AS c ON c.period_start_at = s.slot_start
JOIN plugin_installation.client AS k ON k.id = c.client_id
GROUP BY s.slot_start, s.slot_end, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END;

-- statement: delete_volume_24h
DELETE FROM plugin_rollups.volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: delete_rule_24h
DELETE FROM plugin_rollups.rule_volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: delete_domain_24h
DELETE FROM plugin_rollups.domain_volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: delete_peer_24h
DELETE FROM plugin_rollups.peer_volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: delete_client_24h
DELETE FROM plugin_rollups.client_volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: delete_owner_24h
DELETE FROM plugin_rollups.owner_volume_aggregate_24h
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: compose_volume_24h
INSERT INTO plugin_rollups.volume_aggregate_24h (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, v.exit_leg_device, sum(v.bytes), sum(v.allowed_bytes), sum(v.blocked_bytes), sum(v.unknown_bytes),
       sum(v.allowed_connections), sum(v.blocked_connections), sum(v.unknown_connections),
       measure_parts_sum(v.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.volume_aggregate_1h AS v ON v.period_start_at >= s.slot_start AND v.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction, v.exit_leg_device;

-- statement: compose_rule_24h
INSERT INTO plugin_rollups.rule_volume_aggregate_24h (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id, sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections),
       measure_parts_sum(r.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.rule_volume_aggregate_1h AS r ON r.period_start_at >= s.slot_start AND r.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id;

-- statement: compose_domain_24h
INSERT INTO plugin_rollups.domain_volume_aggregate_24h (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, d.site_name, d.client_id, sum(d.bytes), sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections),
       measure_parts_sum(d.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.domain_volume_aggregate_1h AS d ON d.period_start_at >= s.slot_start AND d.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, d.site_name, d.client_id;

-- statement: compose_peer_24h
INSERT INTO plugin_rollups.peer_volume_aggregate_24h (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.peer_volume_aggregate_1h AS q ON q.period_start_at >= s.slot_start AND q.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address;

-- statement: regroup_client_24h
INSERT INTO plugin_rollups.client_volume_aggregate_24h (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       count(DISTINCT q.peer_address), measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.peer_volume_aggregate_24h AS q ON q.period_start_at = s.slot_start
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction;

-- statement: regroup_owner_24h
INSERT INTO plugin_rollups.owner_volume_aggregate_24h (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), measure_parts_sum(c.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
JOIN plugin_rollups.client_volume_aggregate_24h AS c ON c.period_start_at = s.slot_start
JOIN plugin_installation.client AS k ON k.id = c.client_id
GROUP BY s.slot_start, s.slot_end, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END;

-- statement: delete_pair_volume
DELETE FROM plugin_rollups.pair_volume_observation
WHERE day_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '24h');

-- statement: insert_pair_volume
INSERT INTO plugin_rollups.pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
    service_port, protocol, octets, packets, observed_direction,
    observed_interface_device, last_seen_at, ingested_at)
SELECT p.day_start_at, p.endpoint_low, p.endpoint_high, p.service_port, p.protocol, p.octets, p.packets,
       first.direction, first.interface_device, p.last_seen_at, coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (
    SELECT s.slot_start AS day_start_at,
           LEAST(f.src_address, f.dst_address) AS endpoint_low,
           GREATEST(f.src_address, f.dst_address) AS endpoint_high,
           coalesce(f.dst_port, 0) AS service_port,
           f.protocol AS protocol,
           sum(f.packet_bytes) AS octets,
           count(*) AS packets,
           min(f.id) AS first_flow_id,
           max(f.observed_at) AS last_seen_at
    FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '24h') AS s
    JOIN plugin_pf.flow AS f ON f.observed_at >= s.slot_start AND f.observed_at < s.slot_end
    WHERE (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
    GROUP BY 1, 2, 3, 4, 5
) AS p
JOIN plugin_pf.flow AS first ON first.id = p.first_flow_id;

-- statement: delete_volume_7d
DELETE FROM plugin_rollups.volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: delete_rule_7d
DELETE FROM plugin_rollups.rule_volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: delete_domain_7d
DELETE FROM plugin_rollups.domain_volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: delete_peer_7d
DELETE FROM plugin_rollups.peer_volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: delete_client_7d
DELETE FROM plugin_rollups.client_volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: delete_owner_7d
DELETE FROM plugin_rollups.owner_volume_aggregate_7d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '7d');

-- statement: compose_volume_7d
INSERT INTO plugin_rollups.volume_aggregate_7d (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, v.exit_leg_device, sum(v.bytes), sum(v.allowed_bytes), sum(v.blocked_bytes), sum(v.unknown_bytes),
       sum(v.allowed_connections), sum(v.blocked_connections), sum(v.unknown_connections),
       measure_parts_sum(v.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.volume_aggregate_24h AS v ON v.period_start_at >= s.slot_start AND v.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction, v.exit_leg_device;

-- statement: compose_rule_7d
INSERT INTO plugin_rollups.rule_volume_aggregate_7d (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id, sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections),
       measure_parts_sum(r.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.rule_volume_aggregate_24h AS r ON r.period_start_at >= s.slot_start AND r.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id;

-- statement: compose_domain_7d
INSERT INTO plugin_rollups.domain_volume_aggregate_7d (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, d.site_name, d.client_id, sum(d.bytes), sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections),
       measure_parts_sum(d.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.domain_volume_aggregate_24h AS d ON d.period_start_at >= s.slot_start AND d.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, d.site_name, d.client_id;

-- statement: compose_peer_7d
INSERT INTO plugin_rollups.peer_volume_aggregate_7d (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.peer_volume_aggregate_24h AS q ON q.period_start_at >= s.slot_start AND q.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address;

-- statement: regroup_client_7d
INSERT INTO plugin_rollups.client_volume_aggregate_7d (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       count(DISTINCT q.peer_address), measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.peer_volume_aggregate_7d AS q ON q.period_start_at = s.slot_start
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction;

-- statement: regroup_owner_7d
INSERT INTO plugin_rollups.owner_volume_aggregate_7d (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), measure_parts_sum(c.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '7d') AS s
JOIN plugin_rollups.client_volume_aggregate_7d AS c ON c.period_start_at = s.slot_start
JOIN plugin_installation.client AS k ON k.id = c.client_id
GROUP BY s.slot_start, s.slot_end, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END;

-- statement: delete_volume_30d
DELETE FROM plugin_rollups.volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: delete_rule_30d
DELETE FROM plugin_rollups.rule_volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: delete_domain_30d
DELETE FROM plugin_rollups.domain_volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: delete_peer_30d
DELETE FROM plugin_rollups.peer_volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: delete_client_30d
DELETE FROM plugin_rollups.client_volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: delete_owner_30d
DELETE FROM plugin_rollups.owner_volume_aggregate_30d
WHERE period_start_at IN (SELECT slot_start FROM plugin_rollups.refresh_slot WHERE period = '30d');

-- statement: compose_volume_30d
INSERT INTO plugin_rollups.volume_aggregate_30d (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, v.exit_leg_device, sum(v.bytes), sum(v.allowed_bytes), sum(v.blocked_bytes), sum(v.unknown_bytes),
       sum(v.allowed_connections), sum(v.blocked_connections), sum(v.unknown_connections),
       measure_parts_sum(v.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.volume_aggregate_24h AS v ON v.period_start_at >= s.slot_start AND v.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction, v.exit_leg_device;

-- statement: compose_rule_30d
INSERT INTO plugin_rollups.rule_volume_aggregate_30d (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id, sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections),
       measure_parts_sum(r.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.rule_volume_aggregate_24h AS r ON r.period_start_at >= s.slot_start AND r.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id;

-- statement: compose_domain_30d
INSERT INTO plugin_rollups.domain_volume_aggregate_30d (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, d.site_name, d.client_id, sum(d.bytes), sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections),
       measure_parts_sum(d.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.domain_volume_aggregate_24h AS d ON d.period_start_at >= s.slot_start AND d.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, d.site_name, d.client_id;

-- statement: compose_peer_30d
INSERT INTO plugin_rollups.peer_volume_aggregate_30d (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.peer_volume_aggregate_24h AS q ON q.period_start_at >= s.slot_start AND q.period_start_at < s.slot_end
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction, q.peer_address;

-- statement: regroup_client_30d
INSERT INTO plugin_rollups.client_volume_aggregate_30d (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, q.client_id, q.traffic_direction, sum(q.bytes), sum(q.allowed_bytes), sum(q.blocked_bytes), sum(q.unknown_bytes),
       sum(q.allowed_connections), sum(q.blocked_connections), sum(q.unknown_connections),
       count(DISTINCT q.peer_address), measure_parts_sum(q.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.peer_volume_aggregate_30d AS q ON q.period_start_at = s.slot_start
GROUP BY s.slot_start, s.slot_end, q.client_id, q.traffic_direction;

-- statement: regroup_owner_30d
INSERT INTO plugin_rollups.owner_volume_aggregate_30d (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), measure_parts_sum(c.measure_parts), coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT))
FROM (SELECT slot_start, slot_end FROM plugin_rollups.refresh_slot WHERE period = '30d') AS s
JOIN plugin_rollups.client_volume_aggregate_30d AS c ON c.period_start_at = s.slot_start
JOIN plugin_installation.client AS k ON k.id = c.client_id
GROUP BY s.slot_start, s.slot_end, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west' ELSE 'north_south' END;

-- statement: refresh_notify
WITH span AS (
    SELECT period, min(slot_start) AS from_at, max(slot_end) AS to_at,
           CASE period WHEN '1h' THEN 1 WHEN '24h' THEN 2 WHEN '7d' THEN 3 ELSE 4 END AS ord
    FROM plugin_rollups.refresh_slot
    GROUP BY period
)
SELECT pg_notify('opnview_aggregate_refresh', CAST(jsonb_build_object(
    'periods', jsonb_agg(period ORDER BY ord),
    'ranges', jsonb_agg(jsonb_build_object('period', period, 'from', from_at, 'to', to_at) ORDER BY ord),
    'families', jsonb_build_array('volume', 'rule', 'domain', 'peer', 'client', 'owner')) AS TEXT))
FROM span
HAVING count(*) > 0;

-- statement: refresh_forget_reassigned
DELETE FROM plugin_rollups.owner_reassigned_hour AS r
USING plugin_rollups.refresh_reassigned AS x
WHERE x.id = r.id;

-- statement: refresh_state_advance
UPDATE plugin_rollups.refresh_state SET watermark = coalesce(CAST(:ingest_watermark AS BIGINT), CAST(:now AS BIGINT)) WHERE id = 1;
