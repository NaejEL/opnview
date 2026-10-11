-- opnview — the read statements of the widget API (step 5B,
-- specs/SPEC-step-5b-widget-api.md).
--
-- Same conventions as every *.statements.sql file of the tree: a "-- statement: <name>" line
-- before each, a semicolon after, @period@ for the aggregate period, named parameters
-- only. Embedded by internal/widget (statements.go) and by each widget directory,
-- registered through store.RegisterStatements, and run through Store.WidgetRows on the
-- read-only connections (readpool.go). sql/schema-checks.sh finds every such file by
-- walking the tree and plans every statement at the 100 000- and 1 000 000-row seeds;
-- none may scan a growing table.
--
-- Sets of ids arrive as one JSON array text bound to a parameter -- :interface_ids,
-- :client_ids, :engine_kinds and the like -- and NULL means "no restriction". A set is
-- read through json_each, a table-valued function over the bound text rather than a
-- table, so a statement keeps one text whatever the size of the set.
--
-- A flow belongs to its LOCAL interface and client (classified_flow.local_interface_id
-- and .local_client_id), the end that is inside, as the aggregate families key it; and a
-- second leg (exit_leg_device set) is never counted a second time.
--
-- Observation-point limit: every volume below counts only traffic that crossed the
-- firewall; traffic between two clients behind one interface is in none of them.

-- statement: widget_earliest_lookup
SELECT min(looked_up_at) FROM dns_resolution;


-- The instant of the oldest security event kept, which a security_event reading is covered from.
-- The core reads it by name for every widget bound to the security_event kind (engine.go), and a
-- script reads it with host.envelope.earliest.
-- statement: widget_earliest_security_event
SELECT min(occurred_at) FROM security_event;


-- Every registered provider with its availability, or NULLs when the provider carries no
-- availability row: a screen reports that as "state unknown", never as healthy.
-- statement: widget_availability
SELECT p.id, p.kind, p.provider_key, p.display_name, p.is_active,
       a.state, a.probe, a.detail, a.checked_at, a.outcome
FROM provider AS p
LEFT JOIN source_availability AS a ON a.provider_id = p.id
ORDER BY p.kind, p.provider_key;


-- The collection gaps of the providers of some kinds, overlapping a window. The provider
-- is read first so each gap is an index search on (provider_id, interval_start_at).
-- statement: widget_collection_gaps
SELECT p.kind, p.provider_key, g.interval_start_at, g.interval_end_at, g.reason
FROM provider AS p
JOIN collection_gap AS g ON g.provider_id = p.id
WHERE p.kind IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:kinds AS jsonb)) AS j(value))
  AND g.interval_start_at < :to
  AND g.interval_end_at >= :from
ORDER BY g.interval_start_at, p.kind, p.provider_key;


-- statement: widget_setting
SELECT value FROM setting WHERE key = :key;


-- ---------------------------------------------------------------------------
-- Freshness. An hour is stale when no 1 h slot of it was computed strictly after the
-- newest ingested_at among its flows -- the recognition rule of the refresh contract,
-- docs/data-model.md, "The aggregate families" -- which also catches an hour holding
-- flows and no slot at all.
-- ---------------------------------------------------------------------------

-- statement: widget_hour_slots_computed
SELECT count(*), min(computed_at), max(computed_at)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to;


-- statement: widget_stale_hours
SELECT count(*)
FROM (SELECT f.observed_at - f.observed_at % 3600 AS hour_start_at, max(f.ingested_at) AS newest
      FROM flow AS f
      WHERE f.observed_at >= :from
        AND f.observed_at < :to
      GROUP BY f.observed_at - f.observed_at % 3600) AS h
WHERE NOT EXISTS (SELECT 1 FROM volume_aggregate_1h AS v
                  WHERE v.period_start_at = h.hour_start_at
                    AND v.computed_at > h.newest);


-- How far back the retention purge has purged: before it, the flows are gone.
-- statement: widget_purged_before
SELECT purged_before FROM retention_purge WHERE id = 1;

