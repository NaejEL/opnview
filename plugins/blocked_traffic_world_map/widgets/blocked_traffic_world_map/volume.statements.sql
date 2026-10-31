-- The rolling window of volume (ADR 0021, step 4): the 1 h slots of the rollups plugin for the whole
-- hours, the pf plugin's classified flows at either edge, read by the figures library's volume area
-- (host.lib.figures.volume.window). Each statement's measured twin answers the same rows in a declared
-- measure: the measure's bytes in place of the logged ones, and its parts.

-- statement: widget_volume_slots
SELECT src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device,
       sum(bytes) AS bytes, sum(allowed_bytes) AS allowed_bytes, sum(blocked_bytes) AS blocked_bytes,
       sum(unknown_bytes) AS unknown_bytes, sum(allowed_connections) AS allowed_connections,
       sum(blocked_connections) AS blocked_connections, sum(unknown_connections) AS unknown_connections
FROM plugin_rollups.volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device;

-- statement: widget_volume_slots_measured
SELECT src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0)
           + measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1)
           + measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2) AS bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0) AS allowed_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1) AS blocked_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2) AS unknown_bytes,
       sum(allowed_connections) AS allowed_connections, sum(blocked_connections) AS blocked_connections,
       sum(unknown_connections) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM plugin_rollups.volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device;

-- statement: widget_volume_flows
SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope, f.traffic_direction, f.exit_leg_device,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from AND f.observed_at < :to
GROUP BY f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope, f.traffic_direction, f.exit_leg_device;

-- statement: widget_volume_flows_measured
SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope, f.traffic_direction, f.exit_leg_device,
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
GROUP BY f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope, f.traffic_direction, f.exit_leg_device;

-- statement: widget_volume_earliest_slot
SELECT min(period_start_at) AS earliest FROM plugin_rollups.volume_aggregate_1h;
