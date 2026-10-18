-- A relation file holds CREATE OR REPLACE VIEW and CREATE OR REPLACE FUNCTION statements, every
-- object in the plugin's own schema. They are applied at every start, after the plugin's tables, and
-- the read role reads them: another plugin names this view in its SQL once it requires this plugin.
CREATE OR REPLACE VIEW plugin_example_derived_sql.meter_totals AS
SELECT meter, readings, total
FROM plugin_example_derived_sql.totals;

-- A function is STABLE or IMMUTABLE, never VOLATILE and never SECURITY DEFINER.
CREATE OR REPLACE FUNCTION plugin_example_derived_sql.mean_reading(meter_name TEXT) RETURNS NUMERIC
LANGUAGE sql STABLE AS $$
  SELECT CASE WHEN readings = 0 THEN NULL ELSE total::NUMERIC / readings END
  FROM plugin_example_derived_sql.totals WHERE meter = meter_name
$$;
