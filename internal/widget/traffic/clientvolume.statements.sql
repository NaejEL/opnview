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


-- The same two reads of the `netflow` measure (measure.go): the same rows and connections, with
-- the bytes the NetFlow export reported for them, then the columns of netflow_parts (internal/store,
-- schema.sql). A flow's figure is its connection's, read through connection_netflow and counted
-- on its first leg's row as the aggregate refresh counts it (internal/store, insert_volume).
-- statement: widget_client_volume_slots_netflow
SELECT client_id, traffic_direction,
       sum(netflow_bytes), sum(netflow_allowed_bytes), sum(netflow_blocked_bytes), sum(netflow_unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       (netflow_sum(netflow_parts)).*
FROM client_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY client_id, traffic_direction;


-- statement: widget_client_volume_flows_netflow
SELECT f.local_client_id, f.traffic_direction,
       sum(n.bytes),
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END),
       (netflow_sum(n.parts)).*
FROM classified_flow AS f
LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
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

