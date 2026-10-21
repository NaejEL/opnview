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
FROM plugin_pf.flow AS f
LEFT JOIN plugin_installation.interface_map AS m ON m.device = f.interface_device
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
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
FROM plugin_pf.flow AS f
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
FROM plugin_leases.dhcp_lease AS l
JOIN provider AS p ON p.id = l.provider_id
WHERE l.client_id = :client_id
GROUP BY p.kind, p.provider_key, l.backend, l.address
ORDER BY max(l.observed_at) DESC, l.address;


-- ---------------------------------------------------------------------------
-- The two reads above in a declared measure, CAST(:measure AS TEXT), run in their place when the answer is given in
-- it (host.query; docs/widget-api.md, "Volumes and the observation-point limit"). A record's bytes are
-- its connection's in the measure, both directions, read through flow_measures (internal/store/schema.sql)
-- as every measured twin reads them; each record also carries its action as an action column
-- (allowed_connections, blocked_connections, unknown_connections, 1 for its own action), its parts as
-- measure_parts -- its connection's packets, coverage class, direction states and markers, in the part of
-- its action -- and, per direction -- forward and reverse, as its first leg logged them -- the bytes, the
-- packets, the state and the markers. A connection the measure knew nothing of at its start has no
-- direction row: its directions are null.
-- ---------------------------------------------------------------------------

-- statement: widget_client_traffic_detail_records_measured
SELECT f.id AS flow_id,
       f.observed_at AS observed_at,
       f.dst_address AS destination,
       f.dst_port AS port,
       f.protocol AS protocol,
       f.action AS action,
       f.traffic_scope AS traffic_scope,
       measure_total_bytes(n.parts, CAST(:measure AS TEXT)) AS bytes,
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
       CAST(:measure AS TEXT) AS measure,
       CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END AS allowed_connections,
       CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked_connections,
       CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END AS unknown_connections,
       n.parts -> CAST(:measure AS TEXT) AS measure_parts,
       fw.bytes AS forward_bytes,
       fw.packets AS forward_packets,
       fw.state AS forward_state,
       measure_markers(CAST(:measure AS TEXT), fw.markers) AS forward_markers,
       rv.bytes AS reverse_bytes,
       rv.packets AS reverse_packets,
       rv.state AS reverse_state,
       measure_markers(CAST(:measure AS TEXT), rv.markers) AS reverse_markers
FROM plugin_pf.flow AS f
LEFT JOIN plugin_installation.interface_map AS m ON m.device = f.interface_device
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN LATERAL flow_measures(f.id, NULL, NULL, f.action) AS n ON true
LEFT JOIN connection_measure AS fw ON fw.measure = CAST(:measure AS TEXT) AND fw.connection_flow_id = f.id
                                AND fw.direction = 'forward'
LEFT JOIN connection_measure AS rv ON rv.measure = CAST(:measure AS TEXT) AND rv.connection_flow_id = f.id
                                AND rv.direction = 'reverse'
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


-- statement: widget_client_traffic_detail_record_totals_measured
SELECT count(*) AS records,
       coalesce(sum(measure_total_bytes(n.parts, CAST(:measure AS TEXT))), 0) AS bytes,
       CAST(:measure AS TEXT) AS measure,
       coalesce(sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END), 0) AS allowed_connections,
       coalesce(sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END), 0) AS blocked_connections,
       coalesce(sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END), 0) AS unknown_connections,
       measure_parts_sum(n.parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM plugin_pf.flow AS f
LEFT JOIN LATERAL flow_measures(f.id, NULL, NULL, f.action) AS n ON true
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope);
