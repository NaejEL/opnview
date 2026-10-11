-- opnview — the read statements the owner_activity widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The machines somebody put under one person, by ascending id; the script reads each one through
-- host.installation.client.
-- statement: widget_owner_activity_clients_of_owner
SELECT id AS client_id
FROM client
WHERE owner_id = :owner_id
ORDER BY id;


-- Per-person activity's inline timeline: the window's flows in buckets, per owner of the
-- inside client (NULL is the unassigned group).
-- statement: widget_owner_activity_buckets
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
JOIN client AS c ON c.id = f.local_client_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3;


-- The same timeline in the `netflow` measure, run in its place when the answer is given in it
-- (host.query; docs/widget-api.md, "Volumes and the observation-point limit"): the bytes the NetFlow
-- export reported for the connections, then their packets and coverage classes, under the names
-- summable figures carry them (host.figures).
-- statement: widget_owner_activity_buckets_netflow
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections,
       'netflow' AS measure,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
JOIN client AS c ON c.id = f.local_client_id
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
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3;
