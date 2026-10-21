-- opnview — the read statements the firewall_health_overview widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The latest sample of one subject's measure at or before an instant.
-- statement: widget_measurement_latest
SELECT sampled_at AS sampled_at,
       value AS value,
       unit AS unit
FROM plugin_insight.measurement_sample
WHERE subject_kind = :subject_kind
  AND subject_key = :subject_key
  AND measure = :measure
  AND sampled_at <= :to
ORDER BY sampled_at DESC
LIMIT 1;
