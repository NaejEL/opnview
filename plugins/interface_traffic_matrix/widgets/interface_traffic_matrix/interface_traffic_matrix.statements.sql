-- opnview — the read statements the interface_traffic_matrix widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The rules that matched, per source interface, destination interface and direction. Read from the
-- flows of the window alone: no aggregate family keeps a rule per matrix cell, so beyond the flow
-- horizon this has no answer, and the API says so by name.
-- statement: widget_matrix_rules
SELECT f.src_interface_id AS src_interface_id,
       f.dst_interface_id AS dst_interface_id,
       f.traffic_direction AS traffic_direction,
       f.rule_id AS rule_id,
       r.description AS description,
       f.rid AS rid,
       count(*) AS connections
FROM classified_flow AS f
LEFT JOIN plugin_installation.rule AS r ON r.id = f.rule_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY f.src_interface_id, f.dst_interface_id, f.traffic_direction, f.rule_id,
         r.description, f.rid;
