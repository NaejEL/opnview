-- opnview — the read statements the live_strip widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name, and every column is named with AS, because the
-- script reads a row as an object of its columns. sql/schema-checks.sh plans each statement at both
-- seed sizes. A bucket with no row is returned by none of these; the strip reads it as a measured
-- zero only where its kind's collection covered it.


-- The allowed decisions of the filter log per bucket: one per connection, the exit leg of a
-- connection logged on two interfaces left out, as every traffic series counts them.
-- statement: widget_live_strip_allowed
SELECT f.observed_at - f.observed_at % :width AS bucket_start, count(*) AS decisions
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.action = 'pass'
GROUP BY 1;


-- The refusals of the filter log and of the resolvers per bucket and engine kind, as the
-- blocked_decision view names them once each.
-- statement: widget_live_strip_refusals
SELECT d.occurred_at - d.occurred_at % :width AS bucket_start, d.engine_kind AS engine_kind,
       count(*) AS decisions
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND d.source_table IN ('flow', 'dns_resolution')
GROUP BY 1, 2;


-- The earliest record of each kind the strip counts, which bounds what the store covers.
-- statement: widget_live_strip_earliest
SELECT (SELECT min(observed_at) FROM flow) AS earliest_flow,
       (SELECT min(looked_up_at) FROM dns_resolution) AS earliest_lookup;
