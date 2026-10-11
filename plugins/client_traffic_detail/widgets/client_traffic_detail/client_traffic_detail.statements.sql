-- opnview — the read statements the client_traffic_detail widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- One client's records, newest first, with what enriches them: the geolocation of the
-- destination, the site name and its method, and the interface the record was logged on.
-- statement: widget_client_traffic_detail_records
SELECT f.id AS flow_id,
       f.observed_at AS observed_at,
       f.dst_address AS destination,
       f.dst_port AS port,
       f.protocol AS protocol,
       f.action AS action,
       f.traffic_scope AS traffic_scope,
       f.packet_bytes AS bytes,
       f.interface_device AS interface_device,
       m.description AS interface_description,
       g.lookup_state AS lookup_state,
       g.country_code AS country_code,
       g.asn AS asn,
       g.operator AS operator,
       g.dataset_build_at AS dataset_build_at,
       a.site_name AS site_name,
       a.method AS method,
       a.correlation_delay_seconds AS correlation_delay_seconds
FROM flow AS f
LEFT JOIN interface_map AS m ON m.device = f.interface_device
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope)
ORDER BY f.observed_at DESC, f.id DESC
LIMIT :limit;


-- How many records the same filter holds, and their bytes: the whole the table shows a part of.
-- statement: widget_client_traffic_detail_record_totals
SELECT count(*) AS records,
       coalesce(sum(f.packet_bytes), 0) AS bytes
FROM flow AS f
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope);


-- Which server issued each of a client's leases: one row per provider and address, with
-- the latest generation seen.
-- statement: widget_client_traffic_detail_leases
SELECT p.kind AS provider_kind,
       p.provider_key AS provider_key,
       l.backend AS backend,
       l.address AS address,
       max(l.observed_at) AS last_observed_at,
       max(l.expires_at) AS expires_at
FROM dhcp_lease AS l
JOIN provider AS p ON p.id = l.provider_id
WHERE l.client_id = :client_id
GROUP BY p.kind, p.provider_key, l.backend, l.address
ORDER BY max(l.observed_at) DESC, l.address;


-- ---------------------------------------------------------------------------
-- The two reads above in the `netflow` measure, run in their place when the answer is given in it
-- (host.query; docs/widget-api.md, "Volumes and the observation-point limit"). A record's bytes are
-- its connection's measured volume, both directions, from connection_measured_volume; each record
-- also carries its direction as an action column (allowed_connections, blocked_connections,
-- unknown_connections, 1 for its own action), the coverage class of its connection
-- (measured_connections, partial_connections, unmeasured_connections, not_joinable_connections, 1
-- for its class) and, per direction -- forward and reverse, as its first leg logged them -- the
-- bytes, the packets, the state and the leg the figure was measured at. A connection that started
-- under no single Observation Point has no direction row: its directions are null.
-- ---------------------------------------------------------------------------

-- statement: widget_client_traffic_detail_records_netflow
SELECT f.id AS flow_id,
       f.observed_at AS observed_at,
       f.dst_address AS destination,
       f.dst_port AS port,
       f.protocol AS protocol,
       f.action AS action,
       f.traffic_scope AS traffic_scope,
       coalesce(fw.bytes, 0) + coalesce(rv.bytes, 0) AS bytes,
       f.interface_device AS interface_device,
       m.description AS interface_description,
       g.lookup_state AS lookup_state,
       g.country_code AS country_code,
       g.asn AS asn,
       g.operator AS operator,
       g.dataset_build_at AS dataset_build_at,
       a.site_name AS site_name,
       a.method AS method,
       a.correlation_delay_seconds AS correlation_delay_seconds,
       'netflow' AS measure,
       coalesce(fw.packets, 0) + coalesce(rv.packets, 0) AS packets,
       CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END AS allowed_connections,
       CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked_connections,
       CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END AS unknown_connections,
       CASE WHEN fw.state = 'measured' AND rv.state = 'measured' THEN 1 ELSE 0 END AS measured_connections,
       CASE WHEN (fw.state IS NOT NULL OR rv.state IS NOT NULL)
                 AND NOT (fw.state = 'measured' AND rv.state = 'measured')
                 AND coalesce(fw.records, 0) + coalesce(rv.records, 0) > 0 THEN 1 ELSE 0 END AS partial_connections,
       CASE WHEN (fw.state IS NOT NULL OR rv.state IS NOT NULL)
                 AND coalesce(fw.records, 0) + coalesce(rv.records, 0) = 0 THEN 1 ELSE 0 END AS unmeasured_connections,
       CASE WHEN fw.state IS NULL AND rv.state IS NULL THEN 1 ELSE 0 END AS not_joinable_connections,
       fw.bytes AS forward_bytes,
       fw.packets AS forward_packets,
       fw.state AS forward_state,
       fw.measured_at_leg AS forward_measured_at_leg,
       rv.bytes AS reverse_bytes,
       rv.packets AS reverse_packets,
       rv.state AS reverse_state,
       rv.measured_at_leg AS reverse_measured_at_leg
FROM flow AS f
LEFT JOIN interface_map AS m ON m.device = f.interface_device
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN connection_measured_volume AS fw ON fw.connection_flow_id = f.id AND fw.direction = 'forward'
LEFT JOIN connection_measured_volume AS rv ON rv.connection_flow_id = f.id AND rv.direction = 'reverse'
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope)
ORDER BY f.observed_at DESC, f.id DESC
LIMIT :limit;


-- statement: widget_client_traffic_detail_record_totals_netflow
SELECT count(*) AS records,
       coalesce(sum(n.bytes), 0) AS bytes,
       'netflow' AS measure,
       coalesce(sum(n.packets), 0) AS packets,
       coalesce(sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END), 0) AS allowed_connections,
       coalesce(sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END), 0) AS blocked_connections,
       coalesce(sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END), 0) AS unknown_connections,
       coalesce(sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END), 0) AS measured_connections,
       coalesce(sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END), 0) AS partial_connections,
       coalesce(sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END), 0) AS unmeasured_connections,
       coalesce(sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END), 0) AS not_joinable_connections
FROM flow AS f
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
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope);
