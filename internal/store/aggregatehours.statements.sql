-- opnview — the hour slots of a refresh, rewritten together (internal/store/aggregate.go).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters. sql/schema-checks.sh plans each statement
-- at both seed sizes.
--
-- WHY A SECOND FORM OF THE HOUR STATEMENTS. A refresh rewrites every hour slot it selects; written
-- one slot at a time, a back-filled month is some seven hundred slots of fourteen statements each.
-- These are the same statements over a set of hours at once: each computes exactly what its
-- one-slot counterpart (aggregate.statements.sql) computes for every hour in the set, grouped by
-- the hour as well, in one transaction for the whole set. The rows are the same rows; the
-- contract of aggregate.go is unchanged.
--
-- Parameters:
--   :hours  the hour starts to rewrite, a JSON array of UTC epoch seconds
--   :from   the earliest of them
--   :to     one hour after the latest of them
--   :now    the refresh's watermark, written as computed_at


-- The stale hours among candidates: rule 3 of aggregate.go, for every candidate at once. An hour
-- is stale when its computed_at is not strictly later than the newest ingested_at inside it or the
-- newest purged_at of its purged part, or when it holds rows while neither is left inside it.
-- statement: stale_hours
WITH candidate AS (
    SELECT CAST(j.value AS BIGINT) AS start
    FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value)
),
newest AS (
    SELECT u.start, max(u.newest) AS newest
    FROM (SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
          FROM flow AS f
          WHERE f.observed_at >= :from AND f.observed_at < :to
          GROUP BY 1
          UNION ALL
          SELECT p.hour_start_at, max(p.purged_at)
          FROM purged_flow_hour AS p
          WHERE p.hour_start_at >= :from AND p.hour_start_at < :to
          GROUP BY 1) AS u
    GROUP BY u.start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM volume_aggregate_1h AS v
    WHERE v.period_start_at >= :from AND v.period_start_at < :to
    GROUP BY v.period_start_at
)
SELECT c.start
FROM candidate AS c
LEFT JOIN newest AS n ON n.start = c.start
LEFT JOIN computed AS k ON k.start = c.start
WHERE (n.newest IS NULL AND k.computed_at IS NOT NULL)
   OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest))
ORDER BY c.start;


-- statement: delete_volume_hours
DELETE FROM volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));

-- statement: delete_rule_hours
DELETE FROM rule_volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));

-- statement: delete_domain_hours
DELETE FROM domain_volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));

-- statement: delete_peer_hours
DELETE FROM peer_volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));

-- statement: delete_client_hours
DELETE FROM client_volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));

-- statement: delete_owner_hours
DELETE FROM owner_volume_aggregate_1h
WHERE period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value));


-- statement: insert_volume_hours
INSERT INTO volume_aggregate_1h (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, computed_at)
SELECT
    u.start,
    u.start + 3600,
    u.src_interface_id,
    u.dst_interface_id,
    u.peer_address,
    CASE WHEN u.traffic_direction IN ('to_this_firewall', 'from_this_firewall') THEN 'this_firewall'
         WHEN u.src_interface_id IS NOT NULL AND u.dst_interface_id IS NOT NULL
         THEN 'east_west' ELSE 'north_south' END,
    u.traffic_direction,
    u.exit_leg_device,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.src_interface_id, f.dst_interface_id,
           f.peer_address, f.traffic_direction, f.exit_leg_device, f.action,
           f.packet_bytes AS bytes, 1 AS connections
    FROM classified_flow AS f
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
    UNION ALL
    SELECT p.hour_start_at, p.src_interface_id, p.dst_interface_id,
           CASE WHEN p.traffic_direction = 'inter_interface' THEN NULL ELSE p.peer_address END,
           p.traffic_direction, p.exit_leg_device, p.action, p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at >= :from
      AND p.hour_start_at < :to
) AS u
WHERE u.start IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY u.start, u.src_interface_id, u.dst_interface_id, u.peer_address, u.traffic_direction,
         u.exit_leg_device;


-- statement: insert_rule_hours
INSERT INTO rule_volume_aggregate_1h (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT
    u.start,
    u.start + 3600,
    u.rule_id,
    u.src_interface_id,
    u.dst_interface_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.rule_id, f.src_interface_id,
           f.dst_interface_id, f.action, f.packet_bytes AS bytes, 1 AS connections
    FROM flow AS f
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
    UNION ALL
    SELECT p.hour_start_at, p.rule_id, p.src_interface_id, p.dst_interface_id, p.action, p.bytes,
           p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at >= :from
      AND p.hour_start_at < :to
) AS u
WHERE u.start IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY u.start, u.rule_id, u.src_interface_id, u.dst_interface_id;


-- statement: insert_domain_hours
INSERT INTO domain_volume_aggregate_1h (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, computed_at)
SELECT
    u.start,
    u.start + 3600,
    u.site_name,
    u.client_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, a.site_name, f.src_client_id AS client_id,
           f.action, f.packet_bytes AS bytes, 1 AS connections
    FROM flow AS f
    JOIN domain_attribution AS a ON a.flow_id = f.id
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
    UNION ALL
    SELECT p.hour_start_at, p.site_name, p.src_client_id, p.action, p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at >= :from
      AND p.hour_start_at < :to
      AND p.site_name IS NOT NULL
) AS u
WHERE u.start IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY u.start, u.site_name, u.client_id;


-- statement: insert_peer_hours
INSERT INTO peer_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT
    u.start,
    u.start + 3600,
    u.client_id,
    u.traffic_direction,
    u.peer_address,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT (f.observed_at / 3600) * 3600 AS start, f.local_client_id AS client_id,
           f.traffic_direction,
           CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address
                ELSE f.src_address END AS peer_address,
           f.action, f.packet_bytes AS bytes, 1 AS connections
    FROM classified_flow AS f
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND f.local_client_id IS NOT NULL
      AND f.traffic_scope <> 'this_firewall'
      AND f.exit_leg_device IS NULL
    UNION ALL
    SELECT p.hour_start_at, p.local_client_id, p.traffic_direction, p.peer_address, p.action,
           p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at >= :from
      AND p.hour_start_at < :to
      AND p.local_client_id IS NOT NULL
      AND p.traffic_direction NOT IN ('to_this_firewall', 'from_this_firewall')
      AND p.exit_leg_device IS NULL
) AS u
WHERE u.start IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY u.start, u.client_id, u.traffic_direction, u.peer_address;


-- statement: regroup_client_hours
INSERT INTO client_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, computed_at)
SELECT p.period_start_at, p.period_start_at + 3600, p.client_id, p.traffic_direction, sum(p.bytes),
       sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections),
       count(DISTINCT p.peer_address), :now
FROM peer_volume_aggregate_1h AS p
WHERE p.period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY p.period_start_at, p.client_id, p.traffic_direction;


-- statement: regroup_owner_hours
INSERT INTO owner_volume_aggregate_1h (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, computed_at)
SELECT c.period_start_at, c.period_start_at + 3600, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
            ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), :now
FROM client_volume_aggregate_1h AS c
JOIN client AS k ON k.id = c.client_id
WHERE c.period_start_at IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:hours AS jsonb)) AS j(value))
GROUP BY c.period_start_at, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
              ELSE 'north_south' END;
