-- opnview — supporting queries the model document cites, outside the seven
-- screen queries of sql/queries/screens.sql.
--
-- Same conventions: each query is preceded by "-- diagnostic: <Name>", is a
-- single statement terminated by a semicolon, and is executed by
-- sql/schema-checks.sh. Bound parameters:
--   :window_start  inclusive lower bound, UTC epoch seconds
--   :window_end    exclusive upper bound, UTC epoch seconds

-- diagnostic: Attribution rate per device
-- How often a flow leaving a device could be given a site name. The rate is
-- the honest figure the roadmap requires the UI to expose: site names are
-- inferred from resolver-lookup correlation and nothing else on OPNsense 26.7,
-- so a device using encrypted DNS, or a cached name, shows up here as poorly
-- attributed rather than mis-attributed.
SELECT
    f.src_device_id                                                  AS device_id,
    count(*)                                                         AS flow_count,
    sum(CASE WHEN a.flow_id IS NULL THEN 0 ELSE 1 END)               AS attributed_count,
    round(100.0 * sum(CASE WHEN a.flow_id IS NULL THEN 0 ELSE 1 END) / count(*), 2)
                                                                     AS attribution_rate_percent,
    avg(a.correlation_delay_seconds)                                 AS mean_correlation_delay_seconds,
    max(a.correlation_delay_seconds)                                 AS max_correlation_delay_seconds
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND f.src_device_id IS NOT NULL
GROUP BY f.src_device_id
ORDER BY attribution_rate_percent ASC, device_id ASC;

-- diagnostic: Aggregate coverage per period
-- One row per pre-computed period, with how many slots it holds and how fresh
-- they are. A period whose slot count is zero, or whose computed_at has fallen
-- behind the refresh contract in docs/data-model.md, is stale.
SELECT '1h' AS period, count(*) AS slot_count, min(period_start_at) AS earliest_period_start_at,
       max(period_end_at) AS latest_period_end_at, max(computed_at) AS last_computed_at,
       sum(bytes) AS observed_bytes
FROM volume_aggregate_1h
UNION ALL
SELECT '24h', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_24h
UNION ALL
SELECT '7d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_7d
UNION ALL
SELECT '30d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_30d;

-- diagnostic: Source availability
-- The current state of all five sources, with the probe that determined it.
-- Every screen reads this: an unavailable source is a condition of its own and
-- is never rendered as an absence of traffic, alerts, devices or names.
SELECT
    source        AS source,
    state         AS state,
    probe         AS probe,
    detail        AS detail,
    checked_at    AS checked_at
FROM source_availability
ORDER BY source;
