-- opnview — the statements run by internal/store/dashboards/dashboards.go: the stored dashboards
-- (specs/SPEC-step-7-frontend.md, A7).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters. sql/schema-checks.sh plans each statement
-- at both seed sizes. The table is bounded by the user's own dashboards, a handful of rows.
--
-- THE DOCUMENT IS WRITTEN AND READ AS TEXT. It is cast to json on the way in, which checks its
-- syntax and keeps its text as given, and back to text on the way out, so a document reads back
-- byte for byte, member order and unknown keys included.
--
-- Parameters:
--   :id          a dashboard's id; NULL to restore a dashboard under a new one
--   :ids         a JSON array of dashboard ids, in their new order
--   :position    a dashboard's position
--   :document    a document's JSON text
--   :revision    a revision
--   :origin      created, imported or default
--   :source_name the imported file's name, or NULL
--   :now         the instant of the write, a UTC epoch
--   :created_at  :updated_at   the instants of a restored dashboard
--   :key         the setting that marks the default dashboard as offered


-- Every dashboard, in order.
-- statement: dashboards_list
SELECT id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at
FROM dashboard
ORDER BY position, id;

-- One dashboard.
-- statement: dashboard_get
SELECT id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at
FROM dashboard
WHERE id = :id;

-- One dashboard, locked for the transaction that saves or deletes it.
-- statement: dashboard_lock
SELECT id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at
FROM dashboard
WHERE id = :id
FOR UPDATE;

-- A new dashboard, after every other one, at its first revision.
-- statement: dashboard_create
INSERT INTO dashboard (position, document, revision, origin, source_name, created_at, updated_at)
VALUES ((SELECT coalesce(max(position) + 1, 0) FROM dashboard), CAST(CAST(:document AS TEXT) AS JSON), 1,
        :origin, :source_name, :now, :now)
RETURNING id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at;

-- A saved document: the next revision.
-- statement: dashboard_save
UPDATE dashboard
SET document = CAST(CAST(:document AS TEXT) AS JSON), revision = revision + 1,
    updated_at = greatest(CAST(:now AS BIGINT), created_at)
WHERE id = :id
RETURNING id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at;

-- Whether an id is held by a dashboard.
-- statement: dashboard_id_taken
SELECT EXISTS (SELECT 1 FROM dashboard WHERE id = :id);

-- A deleted dashboard recreated as it was: under its own id when :id is given, else under a new
-- one the row-id trigger assigns.
-- statement: dashboard_restore
INSERT INTO dashboard (id, position, document, revision, origin, source_name, created_at, updated_at)
VALUES (CAST(:id AS BIGINT), :position, CAST(CAST(:document AS TEXT) AS JSON), :revision, :origin,
        :source_name, :created_at, :updated_at)
RETURNING id, position, CAST(document AS TEXT), revision, origin, source_name, created_at, updated_at;

-- A dashboard deleted.
-- statement: dashboard_delete
DELETE FROM dashboard WHERE id = :id;

-- Every dashboard id.
-- statement: dashboard_ids
SELECT id FROM dashboard ORDER BY id;

-- The dashboards in a new order: each id of :ids takes its index as its position.
-- statement: dashboards_order
UPDATE dashboard AS d
SET position = ordered.ordinal - 1
FROM jsonb_array_elements_text(CAST(:ids AS JSONB)) WITH ORDINALITY AS ordered (id, ordinal)
WHERE d.id = CAST(ordered.id AS BIGINT);

-- The default dashboard's mark, claimed once: a row answers only to the first claim, ever.
-- statement: dashboard_default_claim
INSERT INTO setting (key, value, updated_at) VALUES (:key, 'true', :now)
ON CONFLICT (key) DO NOTHING
RETURNING key;

-- How many dashboards are stored.
-- statement: dashboard_count
SELECT count(*) FROM dashboard;
