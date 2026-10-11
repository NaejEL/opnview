-- opnview — the read statements the traffic_composition widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.
--
-- Each statement reads the window's flows, each connection once (no second leg), in a traffic scope
-- ('all' for every one), restricted to some inside interfaces and some clients when the request
-- names them (:interface_ids and :client_ids, a JSON list, NULL for no restriction).


-- Composition by protocol.
-- statement: widget_traffic_composition_protocol
SELECT f.protocol AS member,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY f.protocol;


-- Composition by service port: the destination port, NULL where none is recorded.
-- statement: widget_traffic_composition_port
SELECT f.dst_port AS member,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY f.dst_port;


-- Composition by interface restricted to some clients, which no volume row can answer
-- because the interface family carries no client: read from the flows of the window.
-- statement: widget_traffic_composition_interface
SELECT f.local_interface_id AS member,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY f.local_interface_id;
