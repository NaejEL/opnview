-- opnview — the read statements the widget code in attribution_rate_per_client runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- The attribution rate per client: read_attribution_rate grouped by client, with the
-- client's eligible bytes for the by-volume order. Its counts equal the diagnostic
-- "Attribution rate per client" over the same window.
-- statement: widget_attribution_rate_by_client
SELECT f.src_client_id,
       count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0),
       sum(f.packet_bytes)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;



-- The same read in a declared measure, CAST(:measure AS TEXT), (measure.go): the client's eligible bytes are the measure's bytes for
-- its eligible connections, then the measure's parts, measure_parts
-- (internal/store/schema.sql). First legs only, so a connection counts once.
-- statement: widget_attribution_rate_by_client_measured
SELECT f.src_client_id,
       count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0),
       sum(n.bytes),
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;
