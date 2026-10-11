-- opnview — the statements run by internal/store/owners.go: the people the user creates, the
-- machines the user assigns to them, the labels the user gives interfaces and the purposes the
-- user gives blocklists (specs/SPEC-backend-completion-plugins.md, C1).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters. sql/schema-checks.sh plans each statement
-- at both seed sizes.
--
-- EVERY WRITE NAMES ITS ROWS BY ID. Nothing below matches a client, a person, an interface or a
-- list by a hostname, a MAC, a vendor hint, an address or a name: ownership, labels and purposes
-- are assigned, never inferred (docs/data-model.md, "client", "owner", "blocklist").
--
-- Parameters:
--   :id       the id of the person, the interface or the list the statement acts on
--   :ids      a JSON array of client ids
--   :name     a person's display name, already trimmed
--   :now      the instant of the write, a UTC epoch
--   :at       an owner_assigned_at to restore, a UTC epoch


-- ---------------------------------------------------------------------------
-- People.
-- ---------------------------------------------------------------------------

-- Every person, each with the number of machines assigned to them; idx_client_owner answers each
-- count by an index search.
-- statement: owners_list
SELECT o.id, o.display_name, o.created_at, o.updated_at,
       (SELECT count(*) FROM client AS c WHERE c.owner_id = o.id) AS client_count
FROM owner AS o
ORDER BY o.display_name, o.id;

-- One person, locked for the transaction that changes them.
-- statement: owner_lock
SELECT id, display_name, created_at, updated_at
FROM owner
WHERE id = :id
FOR UPDATE;

-- Whether a display name is taken, by another person than :id (0 for a new person).
-- statement: owner_name_taken
SELECT count(*)
FROM owner
WHERE display_name = :name
  AND id <> :id;

-- statement: owner_insert
INSERT INTO owner (display_name, created_at, updated_at)
VALUES (:name, :now, :now)
RETURNING id;

-- statement: owner_rename
UPDATE owner
SET display_name = :name,
    updated_at = :now
WHERE id = :id;

-- statement: owner_delete
DELETE FROM owner
WHERE id = :id;

-- The machines of one person, locked, with the instant each was assigned.
-- statement: owner_assignments
SELECT id, owner_assigned_at
FROM client
WHERE owner_id = :id
ORDER BY id
FOR UPDATE;

-- statement: owner_clear_assignments
UPDATE client
SET owner_id = NULL,
    owner_assigned_at = NULL
WHERE owner_id = :id;

-- Every slot of every period the owner family holds for one person, as the hour instant that
-- forces it: a deleted person's rows go, and these slots are rewritten without them.
-- statement: owner_record_slots
INSERT INTO owner_reassigned_hour (hour_start_at, recorded_at)
SELECT DISTINCT s.period_start_at, CAST(:now AS BIGINT)
FROM (SELECT period_start_at FROM owner_volume_aggregate_1h WHERE owner_id = :id
      UNION
      SELECT period_start_at FROM owner_volume_aggregate_24h WHERE owner_id = :id
      UNION
      SELECT period_start_at FROM owner_volume_aggregate_7d WHERE owner_id = :id
      UNION
      SELECT period_start_at FROM owner_volume_aggregate_30d WHERE owner_id = :id) AS s;

-- statement: owner_delete_slots_1h
DELETE FROM owner_volume_aggregate_1h WHERE owner_id = :id;

-- statement: owner_delete_slots_24h
DELETE FROM owner_volume_aggregate_24h WHERE owner_id = :id;

-- statement: owner_delete_slots_7d
DELETE FROM owner_volume_aggregate_7d WHERE owner_id = :id;

-- statement: owner_delete_slots_30d
DELETE FROM owner_volume_aggregate_30d WHERE owner_id = :id;


-- ---------------------------------------------------------------------------
-- Assignments.
-- ---------------------------------------------------------------------------

-- The named clients, locked, with their current owner; a client the list names that does not exist
-- is absent from the answer.
-- statement: clients_lock
SELECT id, owner_id, owner_assigned_at
FROM client
WHERE id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:ids AS jsonb)) AS j(value))
ORDER BY id
FOR UPDATE;

-- Every kept 1 h slot holding the named clients' traffic, recorded for the next refresh. The
-- client family keeps a slot beyond the flow horizon, so the purged part is found as well.
-- statement: clients_record_hours
INSERT INTO owner_reassigned_hour (hour_start_at, recorded_at)
SELECT DISTINCT period_start_at, CAST(:now AS BIGINT)
FROM client_volume_aggregate_1h
WHERE client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:ids AS jsonb)) AS j(value));

-- statement: client_assign
UPDATE client
SET owner_id = :owner_id,
    owner_assigned_at = :at
WHERE id = :id;

-- statement: client_unassign
UPDATE client
SET owner_id = NULL,
    owner_assigned_at = NULL
WHERE id = :id;


-- ---------------------------------------------------------------------------
-- The refresh's side: the hours a change of ownership invalidated (aggregate.go).
-- ---------------------------------------------------------------------------

-- statement: reassigned_hours
SELECT id, hour_start_at
FROM owner_reassigned_hour
ORDER BY id;

-- statement: reassigned_hours_forget
DELETE FROM owner_reassigned_hour
WHERE id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:ids AS jsonb)) AS j(value));


-- ---------------------------------------------------------------------------
-- The client listing: every machine, with or without traffic.
-- ---------------------------------------------------------------------------

-- statement: clients_list
SELECT c.id, c.identity_kind, c.identity_key, c.hostname, c.mac, c.last_address,
       c.interface_id, i.identifier, i.description, i.user_label,
       c.unstable_identity, c.owner_id, o.display_name, c.owner_assigned_at,
       c.first_seen_at, c.last_seen_at
FROM client AS c
LEFT JOIN interface AS i ON i.id = c.interface_id
LEFT JOIN owner AS o ON o.id = c.owner_id
ORDER BY c.id;


-- ---------------------------------------------------------------------------
-- Labels and purposes.
-- ---------------------------------------------------------------------------

-- statement: interface_set_user_label
UPDATE interface
SET user_label = :label
WHERE id = :id;

-- statement: interface_label_read
SELECT id, identifier, description, user_label
FROM interface
WHERE id = :id;

-- statement: blocklist_set_purpose
UPDATE blocklist
SET purpose = :purpose,
    purpose_assigned_at = :at
WHERE id = :id;

-- statement: blocklists_list
SELECT id, name, purpose, purpose_assigned_at, first_seen_at, last_seen_at
FROM blocklist
ORDER BY name, id;

-- statement: blocklist_read
SELECT id, name, purpose, purpose_assigned_at, first_seen_at, last_seen_at
FROM blocklist
WHERE id = :id;
