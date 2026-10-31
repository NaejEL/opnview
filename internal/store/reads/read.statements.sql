-- opnview — the statements run by internal/store/reads/read.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.

-- ---------------------------------------------------------------------------
-- The clients an interface has seen.
-- ---------------------------------------------------------------------------

-- statement: read_interface_seen_clients
SELECT f.local_interface_id, count(DISTINCT f.local_client_id)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_interface_id;


-- statement: read_interface_known_clients
SELECT count(*)
FROM client
WHERE interface_id = :interface_id
  AND last_seen_at >= :from;


-- ---------------------------------------------------------------------------
-- The decisions, the rules and the installation itself.
-- ---------------------------------------------------------------------------

-- statement: read_rules
SELECT id, interface, legacy, logs_matches FROM rule ORDER BY id;


-- statement: read_interfaces
SELECT id, identifier, device, is_upstream FROM interface ORDER BY id;


-- statement: read_interface_addresses
SELECT source_field, address, prefix_length, address_family, first_seen_at, last_seen_at
FROM interface_address
WHERE interface_id = :interface_id
ORDER BY last_seen_at DESC, first_seen_at DESC, id;

