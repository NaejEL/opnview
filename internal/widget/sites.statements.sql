-- opnview — the read statements the widget code in sites runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- Site names over the flows of a window, per site, source client, source interface,
-- method and destination operator, so every figure a site-name preset shows is a sum of
-- these rows.
-- statement: widget_site_flows
SELECT a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
       g.operator, count(*), sum(f.packet_bytes), sum(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds)
FROM flow AS f
JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
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

