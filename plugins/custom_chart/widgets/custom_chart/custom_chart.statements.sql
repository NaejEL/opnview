-- opnview — the read statements the custom_chart widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The resolver lookups of a window per bucket start: all of them, and the blocked ones.
-- statement: widget_dns_buckets
SELECT r.looked_up_at - r.looked_up_at % :width AS bucket_start,
       count(*) AS lookups,
       sum(CASE WHEN r.action IN ('block', 'drop') THEN 1 ELSE 0 END) AS blocked
FROM dns_resolution AS r
WHERE r.looked_up_at >= :from
  AND r.looked_up_at < :to
  AND (CAST(:resolver AS TEXT) IS NULL OR r.resolver = :resolver)
  AND (CAST(:client_id AS BIGINT) IS NULL OR r.client_id = :client_id)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR r.interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY 1;


-- The security events of a window per bucket start: all of them, and the blocked ones.
-- statement: widget_security_buckets
SELECT e.occurred_at - e.occurred_at % :width AS bucket_start,
       count(*) AS events,
       sum(CASE WHEN e.event_action = 'blocked' THEN 1 ELSE 0 END) AS blocked
FROM plugin_suricata.security_event AS e
WHERE e.occurred_at >= :from
  AND e.occurred_at < :to
  AND (CAST(:provider_id AS BIGINT) IS NULL OR e.provider_id = :provider_id)
  AND (CAST(:client_id AS BIGINT) IS NULL OR e.src_client_id = :client_id)
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR e.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY 1;
