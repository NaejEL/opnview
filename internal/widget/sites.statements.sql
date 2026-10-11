-- opnview — the read statements the widget code in sites runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- Site names over the eligible flows of a window, per site, source client, source interface,
-- method and destination operator, so every figure a site-name preset shows is a sum of
-- these rows. Eligible is read_attribution_rate's rule, as for widget_address_flows below, so the
-- site rows and the address rows make the window's eligible flows.
-- statement: widget_site_flows
SELECT a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
       g.operator, count(*), sum(f.packet_bytes), sum(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds)
FROM flow AS f
JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
         g.operator;


-- The domain family's 1 h slots, for the whole hours before the earliest flow kept: the
-- site totals there, without a method, which only the purged attributions carried.
-- statement: widget_site_slots
SELECT site_name, client_id, sum(bytes),
       sum(allowed_connections + blocked_connections + unknown_connections)
FROM domain_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY site_name, client_id;



-- The window's eligible flows no site name was inferred for, per destination address, source
-- client, source interface and the geolocation of the address: the address rows that stand beside
-- the site rows, so a destination with no name is shown by its address, its operator and its
-- country and never folded into one unnamed bucket (C2 of the backend-completion cycle). Eligible is
-- read_attribution_rate's rule, so the site rows and these rows make the window's eligible flows. A
-- destination never looked up carries the lookup state 'no_row'. It reads the flows by their
-- columns, whichever source wrote them.
-- statement: widget_address_flows
SELECT f.dst_address, f.src_client_id, f.src_interface_id, coalesce(g.lookup_state, 'no_row'),
       g.country_code, g.asn, g.operator, count(*), sum(f.packet_bytes)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND a.flow_id IS NULL
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.dst_address, f.src_client_id, f.src_interface_id, g.lookup_state, g.country_code, g.asn,
         g.operator;


-- ---------------------------------------------------------------------------
-- The same three reads of the `netflow` measure (measure.go): the same rows and flows, with the bytes
-- the NetFlow export reported for their connections in place of the logged bytes, then the packets
-- and the connections measured, partial, unmeasured and not joinable. A flow's figure is its
-- connection's measured volume; these read first legs only, so a connection is counted once.
-- ---------------------------------------------------------------------------

-- statement: widget_site_flows_netflow
SELECT a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
       g.operator, count(*), sum(n.bytes), sum(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       sum(n.packets),
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END)
FROM flow AS f
JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
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
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
         g.operator;


-- statement: widget_site_slots_netflow
SELECT site_name, client_id, sum(netflow_bytes),
       sum(allowed_connections + blocked_connections + unknown_connections),
       sum(netflow_packets), sum(netflow_measured_connections), sum(netflow_partial_connections),
       sum(netflow_unmeasured_connections), sum(netflow_not_joinable_connections)
FROM domain_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY site_name, client_id;


-- statement: widget_address_flows_netflow
SELECT f.dst_address, f.src_client_id, f.src_interface_id, coalesce(g.lookup_state, 'no_row'),
       g.country_code, g.asn, g.operator, count(*), sum(n.bytes),
       sum(n.packets),
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
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
  AND a.flow_id IS NULL
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.dst_address, f.src_client_id, f.src_interface_id, g.lookup_state, g.country_code, g.asn,
         g.operator;
