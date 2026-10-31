-- The site names of the window (ADR 0021, step 4): the attributed flows of the pf and unbound plugins
-- inside the flow horizon, the domain family's 1 h slots of the rollups plugin beyond it, the eligible
-- flows no name was inferred for, read by the figures library's sites area
-- (host.lib.figures.sites). Each flow statement's measured twin answers the same rows in a declared
-- measure. Eligible is the attribution rate's rule: a flow a client sent to an outside destination,
-- neither end this firewall, first legs only.

-- statement: widget_site_flows
SELECT a.site_name AS site, f.src_client_id AS client_id, f.src_interface_id AS interface_id, a.method,
       g.country_code, g.asn, g.operator, count(*) AS flows, sum(f.packet_bytes) AS bytes,
       sum(a.correlation_delay_seconds) AS delay_sum, max(a.correlation_delay_seconds) AS delay_max
FROM plugin_pf.flow AS f
JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn, g.operator;

-- statement: widget_site_flows_measured
SELECT a.site_name AS site, f.src_client_id AS client_id, f.src_interface_id AS interface_id, a.method,
       g.country_code, g.asn, g.operator, count(*) AS flows, sum(n.bytes) AS bytes,
       sum(a.correlation_delay_seconds) AS delay_sum, max(a.correlation_delay_seconds) AS delay_max,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM plugin_pf.flow AS f
JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn, g.operator;

-- statement: widget_site_slots
SELECT site_name AS site, client_id, sum(bytes) AS bytes,
       sum(allowed_connections + blocked_connections + unknown_connections) AS flows
FROM plugin_rollups.domain_volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY site_name, client_id;

-- statement: widget_site_slots_measured
SELECT site_name AS site, client_id,
       measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)) AS bytes,
       sum(allowed_connections + blocked_connections + unknown_connections) AS flows,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM plugin_rollups.domain_volume_aggregate_1h
WHERE period_start_at >= :from AND period_start_at < :to
GROUP BY site_name, client_id;

-- statement: widget_address_flows
SELECT f.dst_address AS address, f.src_client_id AS client_id, f.src_interface_id AS interface_id,
       coalesce(g.lookup_state, 'no_row') AS lookup_state, g.country_code, g.asn, g.operator,
       count(*) AS flows, sum(f.packet_bytes) AS bytes
FROM plugin_pf.flow AS f
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND a.flow_id IS NULL
GROUP BY f.dst_address, f.src_client_id, f.src_interface_id, g.lookup_state, g.country_code, g.asn, g.operator;

-- statement: widget_address_flows_measured
SELECT f.dst_address AS address, f.src_client_id AS client_id, f.src_interface_id AS interface_id,
       coalesce(g.lookup_state, 'no_row') AS lookup_state, g.country_code, g.asn, g.operator,
       count(*) AS flows, sum(n.bytes) AS bytes,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM plugin_pf.flow AS f
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.dst_address
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND a.flow_id IS NULL
GROUP BY f.dst_address, f.src_client_id, f.src_interface_id, g.lookup_state, g.country_code, g.asn, g.operator;

-- How far back the retention purge has purged: before it, the flows are gone and the slots answer.
-- statement: widget_sites_purged_before
SELECT purged_before FROM retention_purge WHERE id = 1;

-- The interface a client of a slot row was last seen on.
-- statement: widget_site_client_interface
SELECT interface_id FROM plugin_installation.client WHERE id = :client_id;
