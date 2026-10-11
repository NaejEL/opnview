-- The firewall's and its gateways' sampled readings, one row per sample. A reading's unit is in
-- its own row, since one measure column holds every measure: a query that aggregates value
-- filters on measure first.
SELECT s.sampled_at,
       s.subject_kind,
       s.subject_key,
       s.measure,
       s.unit,
       s.value
FROM measurement_sample AS s
WHERE s.subject_kind IN ('firewall', 'gateway')
  AND s.sampled_at >= :from
  AND s.sampled_at < :to
