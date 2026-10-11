-- opnview — the read statements the blocked_traffic_world_map widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The blocked map's detection-engine half: blocked security events per peer end and interface.
-- statement: widget_security_blocked_peers
SELECT CASE WHEN :endpoint = 'source' THEN e.src_address ELSE e.dst_address END AS peer_address,
       e.src_interface_id AS interface_id,
       count(*) AS events
FROM security_event AS e
WHERE e.event_action = 'blocked'
  AND e.occurred_at >= :from
  AND e.occurred_at < :to
GROUP BY 1, 2;


-- The refusals of the window, per engine kind: the DNS engines' refusals are a counter of their
-- own beside the map, since a refused lookup resolves to no address. A copy of the store's
-- read_blocked_decision_counts.
-- statement: widget_blocked_decision_counts
SELECT engine_kind AS engine_kind, count(*) AS decisions
FROM blocked_decision
WHERE occurred_at >= :from
  AND occurred_at < :to
GROUP BY engine_kind;
