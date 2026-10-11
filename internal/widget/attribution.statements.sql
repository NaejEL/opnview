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
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;



-- The same read of the `netflow` measure (measure.go): the client's eligible bytes are the bytes the
-- NetFlow export reported for its eligible connections, then the packets and the connections
-- measured, partial, unmeasured and not joinable. First legs only, so a connection counts once.
-- statement: widget_attribution_rate_by_client_netflow
SELECT f.src_client_id,
       count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0),
       sum(n.bytes),
       sum(n.packets),
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END),
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
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
GROUP BY f.src_client_id;
