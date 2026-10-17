-- statement: widget_example_latency_targets
SELECT target, max(latency_ms) AS worst_ms, count(*) AS samples
FROM plugin_example_latency.probe
WHERE measured_at >= :from AND measured_at < :to
GROUP BY target
ORDER BY worst_ms DESC, target;
