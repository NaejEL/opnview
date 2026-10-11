-- The intrusion-detection alerts, one row each, counted as blocked when the engine's action was
-- blocked, as widget_security_buckets counts them.
SELECT e.occurred_at,
       e.signature,
       e.event_action,
       e.normalised_severity,
       e.protocol,
       e.src_client_id,
       i.identifier AS src_interface,
       e.dst_port,
       1 AS events,
       CASE WHEN e.event_action = 'blocked' THEN 1 ELSE 0 END AS blocked
FROM security_event AS e
LEFT JOIN interface AS i ON i.id = e.src_interface_id
WHERE e.occurred_at >= :from
  AND e.occurred_at < :to
