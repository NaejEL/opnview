-- The attribution rate of the window (ADR 0021, step 4): how much of the outbound traffic could be
-- named at all, as a whole and per client, read by the figures library's attribution area
-- (host.lib.figures.attribution). Eligible is the sites' rule. The rate is defined only when a
-- resolver source answered for the window: a lookup stored inside it, or a dns_lookup availability
-- found reachable by a probe inside it.

-- statement: widget_attribution_window
SELECT count(*) AS eligible_flows, count(a.flow_id) AS named_flows,
       avg(a.correlation_delay_seconds) AS mean_delay, max(a.correlation_delay_seconds) AS max_delay,
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0) AS by_cache_answer,
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0) AS by_lookup_timing
FROM plugin_pf.flow AS f
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg');

-- statement: widget_attribution_lookup_coverage
SELECT (SELECT count(*) FROM (SELECT 1 FROM plugin_unbound.dns_resolution AS r
                              WHERE r.looked_up_at >= :from AND r.looked_up_at <= :to
                              LIMIT 1) AS l)
     + (SELECT count(*)
        FROM source_availability AS s
        JOIN provider AS p ON p.id = s.provider_id
        WHERE p.kind = 'dns_lookup' AND s.state = 'reachable'
          AND s.checked_at >= :from AND s.checked_at <= :to) AS covered;

-- statement: widget_attribution_cache_coverage
SELECT count(*) AS covered
FROM (SELECT 1
      FROM plugin_unbound.resource_record_observation AS o
      WHERE o.covered_until_at >= :from AND o.covered_from_at <= :to AND o.held_in = 'cache'
      LIMIT 1) AS c;

-- statement: widget_attribution_rate_by_client
SELECT f.src_client_id AS client_id, count(*) AS eligible_flows, count(a.flow_id) AS named_flows,
       avg(a.correlation_delay_seconds) AS mean_delay, max(a.correlation_delay_seconds) AS max_delay,
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0) AS by_cache_answer,
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0) AS by_lookup_timing,
       sum(f.packet_bytes) AS eligible_bytes
FROM plugin_pf.flow AS f
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;

-- statement: widget_attribution_rate_by_client_measured
SELECT f.src_client_id AS client_id, count(*) AS eligible_flows, count(a.flow_id) AS named_flows,
       avg(a.correlation_delay_seconds) AS mean_delay, max(a.correlation_delay_seconds) AS max_delay,
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0) AS by_cache_answer,
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0) AS by_lookup_timing,
       sum(n.bytes) AS eligible_bytes,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_total(n.parts, n.not_known_index, n.part_width) AS measure_parts
FROM plugin_pf.flow AS f
LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;
