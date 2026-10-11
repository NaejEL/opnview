-- opnview — the read statements of the rolling strip (internal/live/strip).
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes. A bucket with no row is returned by none of these; the strip
-- reads it as a measured zero only where its kind's collection covered it.


-- statement: widget_live_strip_allowed
-- The allowed decisions of the filter log per bucket: one per connection, the exit leg of a
-- connection logged on two interfaces left out, as every traffic series counts them.
SELECT f.observed_at - f.observed_at % :width, count(*)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.action = 'pass'
GROUP BY 1;


-- statement: widget_live_strip_refusals
-- The refusals of the filter log and of the resolvers per bucket and engine kind, as the
-- blocked_decision view names them once each.
SELECT d.occurred_at - d.occurred_at % :width, d.engine_kind, count(*)
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND d.source_table IN ('flow', 'dns_resolution')
GROUP BY 1, 2;


-- statement: widget_live_strip_earliest
-- The earliest record of each kind the strip counts, which bounds what the store covers.
SELECT (SELECT min(observed_at) FROM flow), (SELECT min(looked_up_at) FROM dns_resolution);
