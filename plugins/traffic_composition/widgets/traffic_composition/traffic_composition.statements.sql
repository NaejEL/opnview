-- opnview — the read statements the traffic_composition widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns. The compositions by protocol, service port and blocking reason are the core's
-- (host.flows.parts, internal/widget/flows.statements.sql).


-- Composition by interface restricted to some clients, which no volume row can answer
-- because the interface family carries no client: read from the window's flows, each connection
-- once (no second leg), in a traffic scope ('all' for every one), restricted to some inside
-- interfaces and some clients (:interface_ids and :client_ids, a JSON list, NULL for no
-- restriction), per direction for a composition split by it.
-- statement: widget_traffic_composition_interface
SELECT f.local_interface_id AS member,
       f.traffic_direction AS traffic_direction,
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
GROUP BY f.local_interface_id, f.traffic_direction;


-- The refusals of the window per engine kind, over the blocked_decision view: a composition by
-- blocking reason counts the DNS engines' and the detection engine's beside its rings, since a
-- refused lookup never became a packet and a detection is not a logged record.
-- statement: widget_traffic_composition_refusals
SELECT d.engine_kind AS engine_kind, count(*) AS refusals
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
GROUP BY d.engine_kind;


-- The composition by interface restricted to some clients in the `netflow` measure, run in its place
-- when the answer is given in it (host.query; docs/widget-api.md, "Volumes and the observation-point
-- limit"): the bytes the NetFlow export reported for the connections, then their packets and
-- coverage classes, under the names summable figures carry them (host.figures).
-- statement: widget_traffic_composition_interface_netflow
SELECT f.local_interface_id AS member,
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
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY f.local_interface_id, f.traffic_direction;
