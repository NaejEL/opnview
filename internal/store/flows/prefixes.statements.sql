-- opnview — the statement internal/store/flows/prefixes.go runs.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before the
-- statement, a terminating semicolon, named parameters. No address, CIDR, interface identifier,
-- device name or description appears below.

-- The interface networks that count, decision D1: every network the operator set, and every detected
-- one the operator did not remove that discovery still reports.
-- statement: membership_networks
SELECT n.interface_id, n.network_address, n.prefix_length, n.origin
FROM interface_network AS n
JOIN interface AS i ON i.id = n.interface_id
WHERE NOT i.is_upstream
  AND n.removed_at IS NULL
  AND (n.origin = 'operator'
       OR n.last_detected_at = (SELECT max(m.last_detected_at) FROM interface_network AS m
                                WHERE m.interface_id = n.interface_id))
ORDER BY n.interface_id, n.network_address, n.prefix_length;
