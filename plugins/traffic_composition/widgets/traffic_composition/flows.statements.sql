-- The flows of the window by their columns (ADR 0021, step 4): the parts every composition by
-- protocol, service port or blocking reason is summed from, and the recent part of a composition,
-- read from the pf plugin's classified flows by the figures library's flows area
-- (host.lib.figures.flows). Each statement's measured twin answers the same rows in a declared
-- measure. The decision of a record is 'allowed' for a pass, the engine kind of a refused record by
-- the blocked_decision view's rule, and 'unknown' for an action nobody read.

-- statement: widget_flow_parts
SELECT f.traffic_direction AS traffic_direction, f.protocol AS protocol, f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4;

-- statement: widget_flow_parts_measured
SELECT f.traffic_direction AS traffic_direction, f.protocol AS protocol, f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
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
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4;

-- statement: widget_flow_recent_parts
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       f.traffic_direction AS traffic_direction, f.protocol AS protocol, f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       f.local_interface_id AS interface_id, f.local_client_id AS client_id,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4, 5, 6, 7;

-- statement: widget_flow_recent_parts_measured
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       f.traffic_direction AS traffic_direction, f.protocol AS protocol, f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       f.local_interface_id AS interface_id, f.local_client_id AS client_id,
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
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4, 5, 6, 7;
