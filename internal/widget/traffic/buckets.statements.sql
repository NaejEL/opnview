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
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_id AS BIGINT) IS NULL OR f.local_client_id = :client_id)
  AND (CAST(:rule_id AS BIGINT) IS NULL OR f.rule_id = :rule_id)
  AND (CAST(:country AS TEXT) IS NULL OR g.country_code = :country)
  AND (CAST(:asn AS BIGINT) IS NULL OR g.asn = :asn)
  AND (CAST(:site AS TEXT) IS NULL OR a.site_name = :site)
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
-- The same two reads in a declared measure, CAST(:measure AS TEXT), (measure.go): the same rows and connections, with the
-- measure's bytes for them, then the measure's parts, measure_parts (internal/store,
-- schema.sql). A flow's figure is its connection's, read through flow_measures and counted
-- on its first leg's row as the aggregate refresh counts it (internal/store, insert_volume).
-- ---------------------------------------------------------------------------

-- statement: widget_traffic_buckets_measured
SELECT f.observed_at - f.observed_at % :width, f.traffic_direction,
       sum(measure_total_bytes(n.parts, CAST(:measure AS TEXT))),
       sum(CASE WHEN f.action = 'pass' THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END),
       measure_parts_sum(n.parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM classified_flow AS f
LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_id AS BIGINT) IS NULL OR f.local_client_id = :client_id)
  AND (CAST(:rule_id AS BIGINT) IS NULL OR f.rule_id = :rule_id)
  AND (CAST(:country AS TEXT) IS NULL OR g.country_code = :country)
  AND (CAST(:asn AS BIGINT) IS NULL OR g.asn = :asn)
  AND (CAST(:site AS TEXT) IS NULL OR a.site_name = :site)
GROUP BY 1, 2;


-- statement: widget_traffic_hour_slots_measured
SELECT period_start_at, src_interface_id, dst_interface_id, traffic_direction,
       measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)),
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0), measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1),
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;
