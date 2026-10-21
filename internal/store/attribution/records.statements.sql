-- opnview — the statements run by internal/store/attribution/records.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- Every record of one name covering an instant: one step of the descent from a
-- looked-up name to its addresses.
-- statement: resource_records_of_name
SELECT o.id, o.held_in, o.owner_name, o.rrtype, o.value, o.address,
       o.covered_from_at, o.covered_until_at, o.first_seen_at
FROM resource_record_observation AS o
WHERE o.owner_name = :name
  AND o.covered_until_at >= :at
  AND o.covered_from_at <= :at;
