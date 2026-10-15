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


-- The last successful poll of one provider's records.
-- statement: resource_record_read_at
SELECT read_at FROM resource_record_read WHERE provider_id = :provider_id;


-- The observation a record seen again extends: the one the previous successful poll
-- saw.
-- statement: resource_record_current
SELECT o.id, o.covered_until_at
FROM resource_record_observation AS o
WHERE o.provider_id = :provider_id
  AND o.owner_name = :owner_name
  AND o.rrtype = :rrtype
  AND o.value = :value
  AND o.last_seen_at = :previous_at
ORDER BY o.first_seen_at DESC
LIMIT 1;


-- statement: resource_record_extend
UPDATE resource_record_observation
SET last_seen_at = GREATEST(last_seen_at, :seen_at),
    covered_until_at = GREATEST(covered_until_at, :until)
WHERE id = :id;


-- statement: resource_record_insert
INSERT INTO resource_record_observation (provider_id, held_in, owner_name, rrtype, value,
                                         address, host_label, first_seen_at, last_seen_at,
                                         covered_from_at, covered_until_at)
VALUES (:provider_id, :held_in, :owner_name, :rrtype, :value, :address, :host_label,
        :seen_at, :seen_at, :covered_from, :until)
ON CONFLICT (provider_id, owner_name, rrtype, value, first_seen_at) DO UPDATE SET
    last_seen_at = GREATEST(resource_record_observation.last_seen_at, excluded.last_seen_at),
    covered_until_at = GREATEST(resource_record_observation.covered_until_at, excluded.covered_until_at);


-- statement: resource_record_read_upsert
INSERT INTO resource_record_read (provider_id, read_at)
VALUES (:provider_id, :seen_at)
ON CONFLICT (provider_id) DO UPDATE SET read_at = GREATEST(resource_record_read.read_at, excluded.read_at);

