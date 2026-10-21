-- opnview — the read statements the blocked_dns_lookups widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- Blocked lookups per name, list, client and interface, with the list's assigned purpose.
-- statement: widget_blocked_lookups
SELECT lower(rtrim(r.domain, '.')) AS domain,
       r.blocklist_id AS blocklist_id,
       b.name AS list_name,
       b.purpose AS purpose,
       r.client_id AS client_id,
       r.client_address AS client_address,
       r.interface_id AS interface_id,
       count(*) AS lookups,
       max(r.looked_up_at) AS newest
FROM plugin_unbound.dns_resolution AS r
LEFT JOIN plugin_unbound.blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
  AND r.looked_up_at >= :from
  AND r.looked_up_at < :to
GROUP BY lower(rtrim(r.domain, '.')), r.blocklist_id, b.name, b.purpose, r.client_id,
         r.client_address, r.interface_id;
