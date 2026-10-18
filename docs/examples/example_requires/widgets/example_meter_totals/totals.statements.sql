-- The view of the plugin this one requires, read by its qualified name.
-- statement: widget_example_meter_totals
SELECT meter, readings, total
FROM plugin_example_derived_sql.meter_totals
ORDER BY total DESC, meter
LIMIT :limit;
