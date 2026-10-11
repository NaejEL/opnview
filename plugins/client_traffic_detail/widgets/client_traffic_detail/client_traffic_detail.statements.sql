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
