-- opnview — the read statements the widget code in samples runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- statement: widget_earliest_sample
SELECT min(sampled_at) FROM measurement_sample;


-- ---------------------------------------------------------------------------
-- Samples.
-- ---------------------------------------------------------------------------

-- The subjects that reported a measure of some kinds over a window.
-- statement: widget_measurement_subjects
SELECT DISTINCT subject_kind, subject_key, measure, unit
FROM measurement_sample
WHERE sampled_at >= :from
  AND sampled_at <= :to
  AND subject_kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:subject_kinds AS jsonb)) AS j(value));


-- statement: widget_measurement_series
SELECT sampled_at, value
FROM measurement_sample
WHERE subject_kind = :subject_kind
  AND subject_key = :subject_key
  AND measure = :measure
  AND sampled_at >= :from
  AND sampled_at < :to
ORDER BY sampled_at;

