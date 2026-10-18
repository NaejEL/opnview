-- Every sample of the window, each target's in ascending order.
-- statement: widget_example_latency_samples
SELECT target, latency_ms
FROM plugin_example_latency.probe
WHERE measured_at >= :from AND measured_at < :to
ORDER BY target, latency_ms;
