-- opnview — the read statements the widget code in measure runs: what decides whether a volume answer
-- can be given in a declared measure (PA5 of the plugin-API amendment to
-- specs/SPEC-backend-completion-plugins.md; docs/widget-api.md, "Volumes and the observation-point
-- limit").
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql in
-- internal/widget for the parameter conventions). sql/schema-checks.sh plans each statement at both
-- seed sizes. They read the registry and the window intervals a measure's derivation recorded, and name
-- no measure, no state, no exporter and no endpoint: which measure is asked is the request's.


-- The active providers of the kind a measure needs: none means the measure cannot answer.
-- statement: widget_measure_providers
SELECT id FROM provider WHERE kind = CAST(:kind AS TEXT) AND is_active ORDER BY id;


-- A provider's recorded window intervals of a measure, oldest first (schema.sql, measure_interval).
-- statement: widget_measure_intervals
SELECT i.starts_at, i.ends_at, i.state
FROM measure_interval AS i
WHERE i.measure = CAST(:measure AS TEXT)
  AND i.provider_id = :provider_id
ORDER BY i.starts_at;
