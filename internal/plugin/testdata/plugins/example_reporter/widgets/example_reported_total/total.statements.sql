-- The window's volume between the interfaces and the outside, from the hour slots: the logged figures.
-- statement: widget_example_reported_total
SELECT coalesce(sum(bytes), 0) AS bytes, coalesce(sum(allowed_bytes), 0) AS allowed_bytes,
       coalesce(sum(blocked_bytes), 0) AS blocked_bytes, coalesce(sum(unknown_bytes), 0) AS unknown_bytes,
       coalesce(sum(allowed_connections), 0) AS allowed_connections,
       coalesce(sum(blocked_connections), 0) AS blocked_connections,
       coalesce(sum(unknown_connections), 0) AS unknown_connections
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
  AND traffic_scope <> 'this_firewall';


-- Its twin, run in its place when the answer is given in a declared measure: the measure's bytes in place
-- of the logged ones, the measure, and the measure's parts.
-- statement: widget_example_reported_total_measured
SELECT coalesce(measure_total_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT)), 0) AS bytes,
       coalesce(measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 0), 0) AS allowed_bytes,
       coalesce(measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 1), 0) AS blocked_bytes,
       coalesce(measure_bytes(measure_parts_sum(measure_parts), CAST(:measure AS TEXT), 2), 0) AS unknown_bytes,
       coalesce(sum(allowed_connections), 0) AS allowed_connections,
       coalesce(sum(blocked_connections), 0) AS blocked_connections,
       coalesce(sum(unknown_connections), 0) AS unknown_connections,
       CAST(:measure AS TEXT) AS measure,
       measure_parts_sum(measure_parts) -> CAST(:measure AS TEXT) AS measure_parts
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
  AND traffic_scope <> 'this_firewall';
