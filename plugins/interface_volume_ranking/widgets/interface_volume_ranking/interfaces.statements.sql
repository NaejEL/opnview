-- What the store counts per interface beyond its volume (ADR 0021, step 4), read by the figures
-- library's interfaces area (host.lib.figures.interfaces).

-- statement: widget_interface_seen_clients
SELECT f.local_interface_id AS interface_id, count(DISTINCT f.local_client_id) AS seen
FROM plugin_pf.classified_flow AS f
WHERE f.observed_at >= :from AND f.observed_at < :to AND f.local_client_id IS NOT NULL
GROUP BY f.local_interface_id;

-- statement: widget_interface_known_clients
SELECT count(*) AS known
FROM plugin_installation.client
WHERE interface_id = :interface_id AND last_seen_at >= :from;

-- statement: widget_rules_logging
SELECT id, interface, legacy, logs_matches FROM plugin_installation.rule ORDER BY id;
