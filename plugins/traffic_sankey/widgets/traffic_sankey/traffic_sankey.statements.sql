-- opnview — the read statements the traffic_sankey widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The Sankey with clients on the left: per client, the far interface or the outside peer.
-- statement: widget_sankey_client_flows
SELECT f.local_client_id AS client_id,
       f.traffic_direction AS traffic_direction,
       f.src_interface_id AS src_interface_id,
       f.dst_interface_id AS dst_interface_id,
       f.peer_address AS peer_address,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       count(*) AS connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
         f.peer_address;


-- The same in a declared measure, the measure bound as a parameter, run in its place when the answer is given in it (host.query;
-- docs/widget-api.md, "Volumes and the observation-point limit"): the measure's bytes
-- for the connections, then the measure's parts, measure_parts, under the names summable
-- figures carry them (host.figures).
-- statement: widget_sankey_client_flows_measured
SELECT f.local_client_id AS client_id,
       f.traffic_direction AS traffic_direction,
       f.src_interface_id AS src_interface_id,
       f.dst_interface_id AS dst_interface_id,
       f.peer_address AS peer_address,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       count(*) AS connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM plugin_pf.classified_flow AS f
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
         f.peer_address;
