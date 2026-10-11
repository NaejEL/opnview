-- opnview — the statements run by internal/store/aggregate.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- THE REFRESH. A slot is rewritten whole: its rows are deleted and recomputed,
-- for every family at once.
--
-- HOW EACH PERIOD IS COMPUTED, and why no slot is ever wrong under a short
-- retention:
--   * an HOUR is computed from the flows observed inside it PLUS its purged part,
--     the purged_flow_hour rows the retention purge wrote for the flows it
--     removed from that hour. So an hour holding the horizon -- the current hour
--     under a retention shorter than an hour -- keeps what it held for the purged
--     flows, and still takes in a flow ingested late into it and a
--     reclassification of a flow still present;
--   * a DAY is composed from its 24 hours;
--   * a WEEK and a MONTH are rolled up from their days, which tile them exactly.
-- The client family of every slot is regrouped from the peer family of the same
-- slot, and the owner family from the client family of the same slot, so the
-- two distinct figures -- distinct_peers and client_count -- are counted, never
-- summed, in every period.
-- ---------------------------------------------------------------------------

-- The hours a refresh considers: every hour holding a flow ingested since the
-- previous refresh, and every hour a purge has written a purged part for since then.
-- The second half is the belt and braces of the purge's ordering (docs/data-model.md,
-- "Purge"): the purge moves the flows of an hour into its purged part, and once they
-- have left `flow` nothing else would select that hour, so a purge that ever ran
-- before a refresh had read those flows would leave the hour without their bytes.
-- statement: dirty_hours
SELECT (observed_at / 3600) * 3600
FROM flow
WHERE ingested_at >= :since
UNION
SELECT hour_start_at
FROM purged_flow_hour
WHERE purged_at >= :since;


-- The newest instant at which anything inside a slot's window changed: a flow ingested
-- into it, or a purge writing a purged part for one of its hours. A slot whose
-- computed_at is not later than that instant is stale (internal/store/aggregate.go).
-- statement: slot_newest_ingested
SELECT max(newest)
FROM (SELECT max(ingested_at) AS newest
      FROM flow
      WHERE observed_at >= :slot_start
        AND observed_at < :slot_end
      UNION ALL
      SELECT max(purged_at)
      FROM purged_flow_hour
      WHERE hour_start_at >= :slot_start
        AND hour_start_at < :slot_end);


-- statement: slot_computed_at
SELECT max(computed_at)
FROM volume_aggregate_@period@
WHERE period_start_at = :slot_start;


-- statement: delete_volume
DELETE FROM volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- statement: delete_rule
DELETE FROM rule_volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- statement: delete_domain
DELETE FROM domain_volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- statement: delete_peer
DELETE FROM peer_volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- statement: delete_client
DELETE FROM client_volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- statement: delete_owner
DELETE FROM owner_volume_aggregate_@period@ WHERE period_start_at = :slot_start;


-- ---------------------------------------------------------------------------
-- The hour: the flows still present, plus the purged part. Each statement reads
-- one UNION ALL of the two, with the same columns -- bytes and connections are
-- one flow's packet_bytes and 1, or a purged row's sums -- and groups it on the
-- family's key.
-- ---------------------------------------------------------------------------

-- statement: insert_volume
INSERT INTO volume_aggregate_1h (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT
    :slot_start,
    :slot_end,
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
    sum(u.netflow_bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.netflow_bytes ELSE 0 END),
    netflow_sum(u.netflow_parts),
    :now
FROM (
    SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_direction,
           f.exit_leg_device, f.action, f.packet_bytes AS bytes, 1 AS connections,
           n.bytes AS netflow_bytes, n.parts AS netflow_parts
    FROM classified_flow AS f
    LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    UNION ALL
    SELECT p.src_interface_id, p.dst_interface_id,
           CASE WHEN p.traffic_direction = 'inter_interface' THEN NULL ELSE p.peer_address END,
           p.traffic_direction, p.exit_leg_device, p.action, p.bytes, p.connections,
           p.netflow_bytes, p.netflow_parts
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
) AS u
GROUP BY u.src_interface_id, u.dst_interface_id, u.peer_address, u.traffic_direction,
         u.exit_leg_device;


-- statement: insert_rule
INSERT INTO rule_volume_aggregate_1h (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT
    :slot_start,
    :slot_end,
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
    sum(u.netflow_bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.netflow_bytes ELSE 0 END),
    netflow_sum(u.netflow_parts),
    :now
FROM (
    SELECT f.rule_id, f.src_interface_id, f.dst_interface_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections,
           n.bytes AS netflow_bytes, n.parts AS netflow_parts
    FROM flow AS f
    LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    UNION ALL
    SELECT p.rule_id, p.src_interface_id, p.dst_interface_id, p.action, p.bytes, p.connections,
           p.netflow_bytes, p.netflow_parts
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
) AS u
GROUP BY u.rule_id, u.src_interface_id, u.dst_interface_id;


-- statement: insert_domain
INSERT INTO domain_volume_aggregate_1h (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT
    :slot_start,
    :slot_end,
    u.site_name,
    u.client_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    sum(u.netflow_bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.netflow_bytes ELSE 0 END),
    netflow_sum(u.netflow_parts),
    :now
FROM (
    SELECT a.site_name, f.src_client_id AS client_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections,
           n.bytes AS netflow_bytes, n.parts AS netflow_parts
    FROM flow AS f
    JOIN domain_attribution AS a ON a.flow_id = f.id
    LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
      AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
    UNION ALL
    SELECT p.site_name, p.src_client_id, p.action, p.bytes, p.connections,
           p.netflow_bytes, p.netflow_parts
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
      AND p.site_name IS NOT NULL
) AS u
GROUP BY u.site_name, u.client_id;


-- The peer of a flow is the address at the other end from its inside client: the
-- destination when the client is the source, the source otherwise. A flow with an
-- end that is this firewall contributes nothing here, nor to the client and owner
-- families regrouped from this one: this firewall is never a client and never a
-- peer. A second leg contributes nothing either: its connection is counted on its
-- first leg.
-- statement: insert_peer
INSERT INTO peer_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT
    :slot_start,
    :slot_end,
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
    sum(u.netflow_bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.netflow_bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.netflow_bytes ELSE 0 END),
    netflow_sum(u.netflow_parts),
    :now
FROM (
    SELECT f.local_client_id AS client_id, f.traffic_direction,
           CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address
                ELSE f.src_address END AS peer_address,
           f.action, f.packet_bytes AS bytes, 1 AS connections,
           n.bytes AS netflow_bytes, n.parts AS netflow_parts
    FROM classified_flow AS f
    LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
      AND f.local_client_id IS NOT NULL
      AND f.traffic_scope <> 'this_firewall'
      AND f.exit_leg_device IS NULL
    UNION ALL
    SELECT p.local_client_id, p.traffic_direction, p.peer_address, p.action, p.bytes, p.connections,
           p.netflow_bytes, p.netflow_parts
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
      AND p.local_client_id IS NOT NULL
      AND p.traffic_direction NOT IN ('to_this_firewall', 'from_this_firewall')
      AND p.exit_leg_device IS NULL
) AS u
GROUP BY u.client_id, u.traffic_direction, u.peer_address;


-- ---------------------------------------------------------------------------
-- Composition: a day from its hours, a week and a month from their days. @child@
-- is the finer period: 1h for 24h, 24h for 7d and 30d. The finer slots tile the
-- slot exactly -- a day is 24 hours, a week 7 days, a month its days -- so every
-- summable figure is their sum, and the finer slots are kept as long as the week
-- and the month holding them (internal/store/purge.sql), so a slot is composed
-- from all of them whatever the retention.
-- ---------------------------------------------------------------------------

-- statement: compose_volume
INSERT INTO volume_aggregate_@period@ (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, v.exit_leg_device, sum(v.bytes), sum(v.allowed_bytes),
       sum(v.blocked_bytes), sum(v.unknown_bytes), sum(v.allowed_connections),
       sum(v.blocked_connections), sum(v.unknown_connections), sum(v.netflow_bytes), sum(v.netflow_allowed_bytes), sum(v.netflow_blocked_bytes), sum(v.netflow_unknown_bytes), netflow_sum(v.netflow_parts), :now
FROM volume_aggregate_@child@ AS v
WHERE v.period_start_at >= :slot_start
  AND v.period_start_at < :slot_end
GROUP BY v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction, v.exit_leg_device;


-- statement: compose_rule
INSERT INTO rule_volume_aggregate_@period@ (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id,
       sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections), sum(r.netflow_bytes), sum(r.netflow_allowed_bytes), sum(r.netflow_blocked_bytes), sum(r.netflow_unknown_bytes), netflow_sum(r.netflow_parts),
       :now
FROM rule_volume_aggregate_@child@ AS r
WHERE r.period_start_at >= :slot_start
  AND r.period_start_at < :slot_end
GROUP BY r.rule_id, r.src_interface_id, r.dst_interface_id;


-- statement: compose_domain
INSERT INTO domain_volume_aggregate_@period@ (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, d.site_name, d.client_id, sum(d.bytes),
       sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections), sum(d.netflow_bytes), sum(d.netflow_allowed_bytes), sum(d.netflow_blocked_bytes), sum(d.netflow_unknown_bytes), netflow_sum(d.netflow_parts),
       :now
FROM domain_volume_aggregate_@child@ AS d
WHERE d.period_start_at >= :slot_start
  AND d.period_start_at < :slot_end
GROUP BY d.site_name, d.client_id;


-- statement: compose_peer
INSERT INTO peer_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, p.client_id, p.traffic_direction, p.peer_address,
       sum(p.bytes), sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections), sum(p.netflow_bytes), sum(p.netflow_allowed_bytes), sum(p.netflow_blocked_bytes), sum(p.netflow_unknown_bytes), netflow_sum(p.netflow_parts),
       :now
FROM peer_volume_aggregate_@child@ AS p
WHERE p.period_start_at >= :slot_start
  AND p.period_start_at < :slot_end
GROUP BY p.client_id, p.traffic_direction, p.peer_address;


-- ---------------------------------------------------------------------------
-- Regrouping, the same for every period. A client's slot is its peer rows of the
-- same slot, and distinct_peers is how many there are: the peer is in the peer
-- family's key, so that is an exact distinct count and never a sum or a maximum
-- of finer ones. An owner's slot is its clients' rows of the same slot, and
-- client_count is how many distinct clients there are, for the same reason. The
-- scope of a client row follows from its direction: between interfaces is
-- east-west, anything else north-south, as for a flow.
-- ---------------------------------------------------------------------------

-- statement: regroup_client
INSERT INTO client_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, p.client_id, p.traffic_direction, sum(p.bytes),
       sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections),
       count(DISTINCT p.peer_address), sum(p.netflow_bytes), sum(p.netflow_allowed_bytes), sum(p.netflow_blocked_bytes), sum(p.netflow_unknown_bytes), netflow_sum(p.netflow_parts), :now
FROM peer_volume_aggregate_@period@ AS p
WHERE p.period_start_at = :slot_start
GROUP BY p.client_id, p.traffic_direction;


-- statement: regroup_owner
INSERT INTO owner_volume_aggregate_@period@ (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, netflow_bytes, netflow_allowed_bytes, netflow_blocked_bytes,
    netflow_unknown_bytes, netflow_parts, computed_at)
SELECT :slot_start, :slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
            ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), sum(c.netflow_bytes), sum(c.netflow_allowed_bytes), sum(c.netflow_blocked_bytes), sum(c.netflow_unknown_bytes), netflow_sum(c.netflow_parts), :now
FROM client_volume_aggregate_@period@ AS c
JOIN client AS k ON k.id = c.client_id
WHERE c.period_start_at = :slot_start
GROUP BY k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
              ELSE 'north_south' END;


-- The retention horizon of the flows, NULL when retention is unlimited. A slot
-- the purge would remove outright -- one whose own end, and for an hour or a day
-- the end of its ISO week and of its calendar month, all lie before it -- is not
-- rewritten unless it is current (internal/store/aggregate.go).
-- statement: flow_horizon
SELECT :now - CAST(value AS BIGINT)
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0;

