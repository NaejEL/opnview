-- opnview — the read statements the widget code in buckets runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- statement: widget_earliest_flow
SELECT min(observed_at) FROM flow;


-- ---------------------------------------------------------------------------
-- Series in buckets, aligned to UTC multiples of :width seconds from the epoch, read from
-- the records of the window. A bucket with no row is returned by none of these; the API
-- fills it with a measured zero only where the window is covered and no gap overlaps it.
-- ---------------------------------------------------------------------------

-- statement: widget_traffic_buckets
SELECT f.observed_at - f.observed_at % :width, f.traffic_direction,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_id IS NULL OR f.local_client_id = :client_id)
  AND (:rule_id IS NULL OR f.rule_id = :rule_id)
  AND (:country IS NULL OR g.country_code = :country)
  AND (:asn IS NULL OR g.asn = :asn)
  AND (:site IS NULL OR a.site_name = :site)
GROUP BY 1, 2;


-- The whole hours of a series beyond the flow horizon, from the interface family.
-- statement: widget_traffic_hour_slots
SELECT period_start_at, src_interface_id, dst_interface_id, traffic_direction,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;



-- ---------------------------------------------------------------------------
-- The same two reads of the `netflow` measure (measure.go): the same rows and connections, with the
-- bytes the NetFlow export reported for them, then the packets and the connections measured,
-- partial, unmeasured and not joinable. A flow's figure is its connection's measured volume, counted
-- on its first leg's row as the aggregate refresh counts it (internal/store, insert_volume).
-- ---------------------------------------------------------------------------

-- statement: widget_traffic_buckets_netflow
SELECT f.observed_at - f.observed_at % :width, f.traffic_direction,
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
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_id IS NULL OR f.local_client_id = :client_id)
  AND (:rule_id IS NULL OR f.rule_id = :rule_id)
  AND (:country IS NULL OR g.country_code = :country)
  AND (:asn IS NULL OR g.asn = :asn)
  AND (:site IS NULL OR a.site_name = :site)
GROUP BY 1, 2;


-- statement: widget_traffic_hour_slots_netflow
SELECT period_start_at, src_interface_id, dst_interface_id, traffic_direction,
       sum(netflow_bytes), sum(netflow_allowed_bytes), sum(netflow_blocked_bytes), sum(netflow_unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       sum(netflow_packets), sum(netflow_measured_connections), sum(netflow_partial_connections),
       sum(netflow_unmeasured_connections), sum(netflow_not_joinable_connections)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;
