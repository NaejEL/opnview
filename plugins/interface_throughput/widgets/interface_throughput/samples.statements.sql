-- The firewall's sampled readings (ADR 0021, step 4), as the insight plugin holds them, read by the
-- figures library's samples area (host.lib.figures.samples).

-- statement: widget_measurement_subjects
SELECT DISTINCT subject_kind AS kind, subject_key AS key, measure, unit
FROM plugin_insight.measurement_sample
WHERE sampled_at >= :from AND sampled_at <= :to
  AND subject_kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:subject_kinds AS jsonb)) AS j(value));

-- statement: widget_measurement_series
SELECT sampled_at AS at, value
FROM plugin_insight.measurement_sample
WHERE subject_kind = :subject_kind AND subject_key = :subject_key AND measure = :measure
  AND sampled_at >= :from AND sampled_at < :to
ORDER BY sampled_at;

-- statement: widget_interface_counter
SELECT sampled_at AS at, value
FROM plugin_insight.measurement_sample
WHERE subject_kind = 'interface' AND subject_key = :device AND measure = :measure
  AND sampled_at >= :from AND sampled_at < :to
ORDER BY sampled_at;
