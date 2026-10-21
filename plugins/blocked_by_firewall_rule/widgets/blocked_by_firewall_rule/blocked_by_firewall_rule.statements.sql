-- opnview — the read statements the blocked_by_firewall_rule widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- Blocked firewall records per rule (per rid when no rule matched it) and per device the
-- record was logged on, with the interfaces and clients each one reached.
-- statement: widget_blocked_by_rule
SELECT f.rule_id AS rule_id,
       f.rid AS rid,
       r.description AS description,
       r.pf_label AS pf_label,
       CAST(r.is_automatic AS INTEGER) AS is_automatic,
       f.interface_device AS interface_device,
       m.description AS map_description,
       m.interface_id AS map_interface_id,
       f.src_interface_id AS src_interface_id,
       f.src_client_id AS src_client_id,
       count(*) AS records,
       max(f.observed_at) AS newest
FROM plugin_pf.flow AS f
LEFT JOIN plugin_installation.rule AS r ON r.id = f.rule_id
LEFT JOIN plugin_installation.interface_map AS m ON m.device = f.interface_device
WHERE f.action IN ('block', 'reject')
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (CAST(:client_ids AS jsonb) IS NULL OR f.src_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY f.rule_id, f.rid, r.description, r.pf_label, r.is_automatic, f.interface_device,
         m.description, m.interface_id, f.src_interface_id, f.src_client_id;


-- Each rule's blocked records in buckets: the inline timeline of Blocked by firewall rule.
-- statement: widget_blocked_rule_buckets
SELECT f.rule_id AS rule_id,
       f.rid AS rid,
       f.observed_at - f.observed_at % :width AS bucket_start,
       count(*) AS records
FROM plugin_pf.flow AS f
WHERE f.action IN ('block', 'reject')
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (CAST(:client_ids AS jsonb) IS NULL OR f.src_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY f.rule_id, f.rid, f.observed_at - f.observed_at % :width;
