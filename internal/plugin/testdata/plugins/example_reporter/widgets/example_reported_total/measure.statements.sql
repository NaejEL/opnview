-- opnview — the window's reading of a declared measure, which the figures library makes to decide
-- whether the measure can answer the window (plugins/figures/figures.js, exports.measure; ADR 0021,
-- step 5). Same conventions as every *.statements.sql file. The statements read the registry and the
-- window intervals a measure's derivation recorded, and name no measure, no state, no exporter and no
-- endpoint: which measure is asked is the request's.

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
