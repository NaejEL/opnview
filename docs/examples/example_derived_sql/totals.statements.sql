-- The derivation "total" runs this statement, as the plugin's role, in one transaction: it may write
-- in plugin_example_derived_sql and nowhere else. :since and :now are the run's; this statement
-- recomputes every meter and needs neither.
-- statement: total_readings
INSERT INTO plugin_example_derived_sql.totals (meter, readings, total)
SELECT meter, count(*), sum(value)
FROM plugin_example_derived_sql.readings
GROUP BY meter
ON CONFLICT (meter) DO UPDATE SET readings = excluded.readings, total = excluded.total;
