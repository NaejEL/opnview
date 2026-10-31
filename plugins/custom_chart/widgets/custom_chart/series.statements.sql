-- The traffic of the window per bucket (ADR 0021, step 4), for a chart of series: the pf plugin's
-- classified flows in buckets aligned to UTC multiples of :width seconds, and the interface family's
-- 1 h slots of the rollups plugin for the whole hours beyond the flow horizon, read by the figures
-- library's series area (host.lib.figures.series.traffic). Each statement's measured twin answers the
-- same rows in a declared measure.

-- statement: widget_traffic_buckets
SELECT f.observed_at - f.observed_at % :width AS bucket_start, f.traffic_direction AS traffic_direction,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM plugin_pf.classified_flow AS f
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
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

-- statement: widget_traffic_buckets_measured
SELECT f.observed_at - f.observed_at % :width AS bucket_start, f.traffic_direction AS traffic_direction,
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
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
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

-- statement: widget_traffic_hour_slots
SELECT period_start_at AS hour, src_interface_id, dst_interface_id, traffic_direction,
       sum(bytes) AS bytes, sum(allowed_bytes) AS allowed_bytes, sum(blocked_bytes) AS blocked_bytes,
       sum(unknown_bytes) AS unknown_bytes, sum(allowed_connections) AS allowed_connections,
       sum(blocked_connections) AS blocked_connections, sum(unknown_connections) AS unknown_connections
FROM plugin_rollups.volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;

-- statement: widget_traffic_hour_slots_measured
SELECT period_start_at AS hour, src_interface_id, dst_interface_id, traffic_direction,
       measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)) AS bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0) AS allowed_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1) AS blocked_bytes,
       measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2) AS unknown_bytes,
       sum(allowed_connections) AS allowed_connections, sum(blocked_connections) AS blocked_connections,
       sum(unknown_connections) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM plugin_rollups.volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;
