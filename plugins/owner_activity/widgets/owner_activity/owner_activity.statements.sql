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


-- The same timeline in a declared measure, CAST(:measure AS TEXT),, run in its place when the answer is given in it
-- (host.query; docs/widget-api.md, "Volumes and the observation-point limit"): the measure's bytes for
-- the connections, then the measure's parts, measure_parts, under the names
-- summable figures carry them (host.figures).
-- statement: widget_owner_activity_buckets_measured
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       sum(measure_total_bytes(n.parts, CAST(:measure AS TEXT))) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN measure_total_bytes(n.parts, CAST(:measure AS TEXT)) ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(n.parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM classified_flow AS f
JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN LATERAL flow_measures(f.id, NULL, NULL, f.action) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3;
