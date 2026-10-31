-- Every client's volume over the window (ADR 0021, step 4): the client and peer families' 1 h slots of
-- the rollups plugin for the whole hours, the pf plugin's classified flows at either edge, read by the
-- figures library's clients area (host.lib.figures.clients.volumes).

-- statement: widget_client_volume_slots
SELECT client_id, traffic_direction,
       sum(bytes) AS bytes, sum(allowed_bytes) AS allowed_bytes, sum(blocked_bytes) AS blocked_bytes,
       sum(unknown_bytes) AS unknown_bytes, sum(allowed_connections) AS allowed_connections,
       sum(blocked_connections) AS blocked_connections, sum(unknown_connections) AS unknown_connections
FROM plugin_rollups.client_volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY client_id, traffic_direction;

-- statement: widget_client_volume_slots_measured
SELECT client_id, traffic_direction,
       measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)) AS bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0) AS allowed_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1) AS blocked_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2) AS unknown_bytes,
       sum(allowed_connections) AS allowed_connections, sum(blocked_connections) AS blocked_connections,
       sum(unknown_connections) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM plugin_rollups.client_volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY client_id, traffic_direction;

-- statement: widget_client_volume_flows
SELECT f.local_client_id AS client_id, f.traffic_direction,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY f.local_client_id, f.traffic_direction;

-- statement: widget_client_volume_flows_measured
SELECT f.local_client_id AS client_id, f.traffic_direction,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM plugin_pf.classified_flow AS f
LEFT JOIN LATERAL flow_measure(f.id, f.pair_outcome, f.paired_flow_id, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY f.local_client_id, f.traffic_direction;

-- statement: widget_client_peer_slots
SELECT DISTINCT client_id, peer_address AS peer
FROM plugin_rollups.peer_volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to;

-- statement: widget_client_peer_flows
SELECT DISTINCT f.local_client_id AS client_id,
       CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address ELSE f.src_address END AS peer
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL;

-- statement: widget_client_earliest_slot
SELECT min(period_start_at) AS earliest FROM plugin_rollups.volume_aggregate_1h;
