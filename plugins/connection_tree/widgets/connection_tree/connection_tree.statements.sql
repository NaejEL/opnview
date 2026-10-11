-- opnview — the read statements the connection_tree widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- The connection tree's rows, with what the API needs beyond ReadConnectionTree: the
-- blocked bytes, so blocked connections can be left out whole, and the newest instant, so
-- a level can be sorted by recency.
-- statement: widget_connection_tree
SELECT f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END AS lookup_state,
       g.asn AS asn,
       a.site_name AS site_name,
       count(*) AS connections,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       max(f.observed_at) AS newest
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7;


-- An operator's name, from any geolocated address carrying its AS number.
-- statement: widget_operator_name
SELECT operator AS operator FROM geo_asn WHERE asn = :asn AND operator IS NOT NULL LIMIT 1;
