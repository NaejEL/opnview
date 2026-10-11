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
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
         f.peer_address;


-- The same in the `netflow` measure, run in its place when the answer is given in it (host.query;
-- docs/widget-api.md, "Volumes and the observation-point limit"): the bytes the NetFlow export
-- reported for the connections, then their packets and coverage classes, under the names summable
-- figures carry them (host.figures).
-- statement: widget_sankey_client_flows_netflow
SELECT f.local_client_id AS client_id,
       f.traffic_direction AS traffic_direction,
       f.src_interface_id AS src_interface_id,
       f.dst_interface_id AS dst_interface_id,
       f.peer_address AS peer_address,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       count(*) AS connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       'netflow' AS measure,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes), 0) AS bytes,
           coalesce(sum(v.packets), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = f.id
) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
         f.peer_address;
