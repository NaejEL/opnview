-- opnview — the read statements the unresolved_joins widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- ---------------------------------------------------------------------------
-- Unresolved joins.
-- ---------------------------------------------------------------------------

-- The flows of the window whose device matched no interface, whose rid matched no rule, and all
-- of them.
-- statement: widget_unresolved_flow_joins
SELECT coalesce(sum(CASE WHEN f.interface_lookup_state <> 'resolved' THEN 1 ELSE 0 END), 0) AS interface_misses,
       coalesce(sum(CASE WHEN f.rid IS NOT NULL AND f.rule_lookup_state <> 'resolved'
                         THEN 1 ELSE 0 END), 0) AS rule_misses,
       count(*) AS flows
FROM plugin_pf.flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value))
       OR f.dst_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)));


-- The newest flows behind the interface or the rule counter.
-- statement: widget_unresolved_flow_records
SELECT f.id AS id,
       f.observed_at AS observed_at,
       f.interface_device AS interface_device,
       f.interface_lookup_state AS interface_lookup_state,
       f.rid AS rid,
       f.rule_lookup_state AS rule_lookup_state
FROM plugin_pf.flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND ((:counter = 'interface' AND f.interface_lookup_state <> 'resolved')
       OR (:counter = 'rule' AND f.rid IS NOT NULL AND f.rule_lookup_state <> 'resolved'))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value))
       OR f.dst_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
ORDER BY f.observed_at DESC, f.id DESC
LIMIT :limit;


-- The clients of the window, with the identity level each is known by.
-- statement: widget_window_clients
SELECT DISTINCT f.local_client_id AS client_id,
       c.identity_kind AS identity_kind,
       c.last_address AS last_address,
       c.interface_id AS interface_id
FROM plugin_pf.classified_flow AS f
JOIN plugin_installation.client AS c ON c.id = f.local_client_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
ORDER BY f.local_client_id;


-- The outside peers of the window and the state of their geolocation.
-- statement: widget_window_peers
SELECT DISTINCT f.peer_address AS peer_address,
       CASE WHEN g.address IS NULL THEN 'no_row' ELSE g.lookup_state END AS lookup_state
FROM plugin_pf.classified_flow AS f
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.peer_address IS NOT NULL
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
ORDER BY f.peer_address;
