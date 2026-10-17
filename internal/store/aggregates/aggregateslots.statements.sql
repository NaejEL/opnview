-- opnview — the day, week and month slots of a refresh, rewritten together, period by period
-- (internal/store/aggregates/aggregate.go).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters, @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
--
-- WHY A SET FORM OF THE SLOT STATEMENTS. A refresh rewrites every slot it selects. Written one slot at
-- a time, a pass touching a hundred hours spread over five days costs five day slots of fourteen
-- statements each; PA17 of specs/SPEC-backend-completion-plugins.md bounds the refresh after a pass to
-- a number of statements independent of the slots it touches. These are the statements of
-- aggregate.statements.sql over a set of slots of one period at once: each computes exactly what its
-- one-slot counterpart computes for every slot of the set, grouped by the slot as well. The rows are
-- the same rows (aggregatehours.statements.sql does the same for the hours).
--
-- Parameters:
--   :slots  the slots to rewrite, a JSON array of [start, end] pairs, UTC epoch seconds
--   :from   the earliest start
--   :to     the latest end
--   :now    the refresh's watermark, written as computed_at


-- The stale slots among candidates of one period: rule 3 of aggregate.go, for every candidate at
-- once. A slot is stale when its computed_at is not strictly later than the newest ingested_at of a
-- flow inside it or the newest purged_at of its purged part, or when it holds rows while neither is
-- left inside it. Every slot is a whole number of hours.
-- statement: stale_slots
WITH slot AS (
    SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
    FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)
),
hour AS (
    SELECT (f.observed_at / 3600) * 3600 AS start, max(f.ingested_at) AS newest
    FROM flow AS f
    WHERE f.observed_at >= :from AND f.observed_at < :to
    GROUP BY 1
    UNION ALL
    SELECT p.hour_start_at, max(p.purged_at)
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at >= :from AND p.hour_start_at < :to
    GROUP BY 1
),
newest AS (
    SELECT s.slot_start, max(h.newest) AS newest
    FROM slot AS s
    JOIN hour AS h ON h.start >= s.slot_start AND h.start < s.slot_end
    GROUP BY s.slot_start
),
computed AS (
    SELECT v.period_start_at AS start, max(v.computed_at) AS computed_at
    FROM volume_aggregate_@period@ AS v
    WHERE v.period_start_at >= :from AND v.period_start_at < :to
    GROUP BY v.period_start_at
)
SELECT s.slot_start
FROM slot AS s
LEFT JOIN newest AS n ON n.slot_start = s.slot_start
LEFT JOIN computed AS k ON k.start = s.slot_start
WHERE (n.newest IS NULL AND k.computed_at IS NOT NULL)
   OR (n.newest IS NOT NULL AND (k.computed_at IS NULL OR k.computed_at <= n.newest))
ORDER BY s.slot_start;


-- statement: delete_volume_slots
DELETE FROM volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: delete_rule_slots
DELETE FROM rule_volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: delete_domain_slots
DELETE FROM domain_volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: delete_peer_slots
DELETE FROM peer_volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: delete_client_slots
DELETE FROM client_volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: delete_owner_slots
DELETE FROM owner_volume_aggregate_@period@
WHERE period_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: compose_volume_slots
INSERT INTO volume_aggregate_@period@ (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, v.exit_leg_device, sum(v.bytes), sum(v.allowed_bytes),
       sum(v.blocked_bytes), sum(v.unknown_bytes), sum(v.allowed_connections),
       sum(v.blocked_connections), sum(v.unknown_connections), measure_parts_sum(v.measure_parts), :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN volume_aggregate_@child@ AS v ON v.period_start_at >= s.slot_start AND v.period_start_at < s.slot_end
WHERE v.period_start_at >= :from AND v.period_start_at < :to
GROUP BY s.slot_start, s.slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction, v.exit_leg_device;


-- statement: compose_rule_slots
INSERT INTO rule_volume_aggregate_@period@ (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id,
       sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections), measure_parts_sum(r.measure_parts),
       :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN rule_volume_aggregate_@child@ AS r ON r.period_start_at >= s.slot_start AND r.period_start_at < s.slot_end
WHERE r.period_start_at >= :from AND r.period_start_at < :to
GROUP BY s.slot_start, s.slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id;


-- statement: compose_domain_slots
INSERT INTO domain_volume_aggregate_@period@ (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, d.site_name, d.client_id, sum(d.bytes),
       sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections), measure_parts_sum(d.measure_parts),
       :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN domain_volume_aggregate_@child@ AS d ON d.period_start_at >= s.slot_start AND d.period_start_at < s.slot_end
WHERE d.period_start_at >= :from AND d.period_start_at < :to
GROUP BY s.slot_start, s.slot_end, d.site_name, d.client_id;


-- statement: compose_peer_slots
INSERT INTO peer_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, p.client_id, p.traffic_direction, p.peer_address,
       sum(p.bytes), sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections), measure_parts_sum(p.measure_parts),
       :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN peer_volume_aggregate_@child@ AS p ON p.period_start_at >= s.slot_start AND p.period_start_at < s.slot_end
WHERE p.period_start_at >= :from AND p.period_start_at < :to
GROUP BY s.slot_start, s.slot_end, p.client_id, p.traffic_direction, p.peer_address;


-- statement: regroup_client_slots
INSERT INTO client_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, p.client_id, p.traffic_direction, sum(p.bytes),
       sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections),
       count(DISTINCT p.peer_address), measure_parts_sum(p.measure_parts), :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN peer_volume_aggregate_@period@ AS p ON p.period_start_at = s.slot_start
GROUP BY s.slot_start, s.slot_end, p.client_id, p.traffic_direction;


-- statement: regroup_owner_slots
INSERT INTO owner_volume_aggregate_@period@ (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, measure_parts, computed_at)
SELECT s.slot_start, s.slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
            ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), measure_parts_sum(c.measure_parts), :now
FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
      FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
JOIN client_volume_aggregate_@period@ AS c ON c.period_start_at = s.slot_start
JOIN client AS k ON k.id = c.client_id
GROUP BY s.slot_start, s.slot_end, k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
              ELSE 'north_south' END;


