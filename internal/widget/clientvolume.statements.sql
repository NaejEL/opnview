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
-- the bytes the NetFlow export reported for them, then the packets and the connections measured,
-- partial, unmeasured and not joinable. A flow's figure is its connection's measured volume, counted
-- on its first leg's row as the aggregate refresh counts it (internal/store, insert_volume).
-- statement: widget_client_volume_slots_netflow
SELECT client_id, traffic_direction,
       sum(netflow_bytes), sum(netflow_allowed_bytes), sum(netflow_blocked_bytes), sum(netflow_unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       sum(netflow_packets), sum(netflow_measured_connections), sum(netflow_partial_connections),
       sum(netflow_unmeasured_connections), sum(netflow_not_joinable_connections)
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
       sum(n.packets),
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END)
FROM classified_flow AS f
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes) FILTER (WHERE f.pair_outcome IS DISTINCT FROM 'second_leg'
                                          OR v.measured_flow_id = f.id), 0) AS bytes,
           coalesce(sum(v.packets) FILTER (WHERE f.pair_outcome IS DISTINCT FROM 'second_leg'
                                            OR v.measured_flow_id = f.id), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = CASE WHEN f.pair_outcome = 'second_leg' THEN f.paired_flow_id
                                      ELSE f.id END
) AS n ON true
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

