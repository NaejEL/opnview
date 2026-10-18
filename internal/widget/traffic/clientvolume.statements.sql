-- opnview — the read statements the widget code in clientvolume runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- ---------------------------------------------------------------------------
-- Volumes per client, by the assembly ReadVolumeWindow uses: whole hours from the client
-- and peer families' 1 h slots, the partial hours at the edges from the flows.
-- ---------------------------------------------------------------------------

-- statement: widget_client_volume_slots
SELECT client_id, traffic_direction,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM client_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY client_id, traffic_direction;


-- statement: widget_client_volume_flows
SELECT f.local_client_id, f.traffic_direction,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY f.local_client_id, f.traffic_direction;


-- The same two reads in a declared measure, CAST(:measure AS TEXT), (measure.go): the same rows and connections, with
-- the measure's bytes for them, then the measure's parts, measure_parts (internal/store,
-- schema.sql). A flow's figure is its connection's, read through flow_measure and counted
-- on its first leg's row as the aggregate refresh counts it (internal/store, insert_volume).
-- statement: widget_client_volume_slots_measured
SELECT client_id, traffic_direction,
       measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)),
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0), measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1),
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM client_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY client_id, traffic_direction;


-- statement: widget_client_volume_flows_measured
SELECT f.local_client_id, f.traffic_direction,
       sum(n.bytes),
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END),
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM classified_flow AS f
LEFT JOIN LATERAL flow_measure(f.id, f.pair_outcome, f.paired_flow_id, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY f.local_client_id, f.traffic_direction;


-- statement: widget_client_peer_slots
SELECT DISTINCT client_id, peer_address
FROM peer_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to;


-- The far end of a client's flow, as the peer family keys it (derive.sql).
-- statement: widget_client_peer_flows
SELECT DISTINCT f.local_client_id,
       CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address ELSE f.src_address END
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL;


-- The earliest 1 h slot of the interface family: with the earliest flow, where a window
-- assembled from slots and flows begins to be covered.
-- statement: widget_earliest_hour_slot
SELECT min(period_start_at) FROM volume_aggregate_1h;

