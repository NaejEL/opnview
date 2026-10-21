-- opnview — the read statements the unified_blocked_feed widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.


-- ---------------------------------------------------------------------------
-- Refusals.
-- ---------------------------------------------------------------------------

-- The unified blocked feed: the refusals of the window, newest first, over the
-- blocked_decision view, which places each refusal of the four engines once; an address target
-- carries its port and protocol, read from the record it came from (a refused lookup has none).
-- statement: widget_blocked_feed
SELECT d.source_table AS source_table,
       d.source_id AS source_id,
       d.occurred_at AS occurred_at,
       d.engine_kind AS engine_kind,
       d.engine_reference AS engine_reference,
       d.client_id AS client_id,
       d.interface_id AS interface_id,
       d.target AS target,
       d.target_kind AS target_kind,
       d.target_is_this_firewall AS target_is_this_firewall,
       coalesce(f.dst_port, e.dst_port) AS port,
       coalesce(f.protocol, e.protocol) AS protocol
FROM plugin_refusals.blocked_decision AS d
LEFT JOIN plugin_pf.flow AS f ON d.source_table = 'flow' AND f.id = d.source_id
LEFT JOIN security_event AS e ON d.source_table = 'security_event' AND e.id = d.source_id
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (CAST(:engine_kinds AS jsonb) IS NULL OR d.engine_kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:engine_kinds AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL OR d.client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR d.interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
ORDER BY d.occurred_at DESC, d.source_table, d.source_id DESC
LIMIT :limit;


-- The refusals of the window per engine kind.
-- statement: widget_blocked_feed_counts
SELECT d.engine_kind AS engine_kind, count(*) AS refusals
FROM plugin_refusals.blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (CAST(:client_ids AS jsonb) IS NULL OR d.client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR d.interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY d.engine_kind;


-- The unified blocked feed grouped by client, and by target, over the whole window.
-- statement: widget_blocked_feed_by_client
SELECT d.client_id AS client_id, count(*) AS refusals
FROM plugin_refusals.blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (CAST(:engine_kinds AS jsonb) IS NULL OR d.engine_kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:engine_kinds AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL OR d.client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR d.interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY d.client_id;


-- statement: widget_blocked_feed_by_target
SELECT d.target AS target, d.target_kind AS target_kind, count(*) AS refusals
FROM plugin_refusals.blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (CAST(:engine_kinds AS jsonb) IS NULL OR d.engine_kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:engine_kinds AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL OR d.client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR d.interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY d.target, d.target_kind;


-- The rule a refusal's pf label names: the core's widget_ref_rule_pf_label, under the widget's own
-- name.
-- statement: widget_feed_rule_by_pf_label
SELECT id AS id FROM plugin_installation.rule WHERE pf_label = :value;
