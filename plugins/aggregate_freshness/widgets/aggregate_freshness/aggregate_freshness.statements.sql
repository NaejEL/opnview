-- opnview — the read statements the aggregate_freshness widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line, binds its parameters by name, and reads the period a template (@period@) is run at.
-- sql/schema-checks.sh plans each statement at both seed sizes. Every column is named with AS,
-- because the script reads a row as an object of its columns.


-- Aggregate freshness, per pre-computed period, over the slots starting in [from, to).
-- statement: widget_period_coverage
SELECT count(*) AS slots,
       min(period_start_at) AS earliest_period_start_at,
       max(period_end_at) AS latest_period_end_at,
       max(computed_at) AS last_computed_at,
       sum(bytes) AS bytes
FROM volume_aggregate_@period@
WHERE period_start_at >= :from
  AND period_start_at < :to;


-- The slots of the same span whose computation is behind their flows: a flow of the slot was
-- ingested at or after the slot's last computation (the refresh contract's recognition rule), or
-- an hour of the slot a change of ownership invalidated and no refresh has rewritten yet
-- (owner_reassigned_hour).
-- statement: widget_period_stale_slots
SELECT count(*) AS stale_slots
FROM (SELECT period_start_at, period_end_at, max(computed_at) AS computed
      FROM volume_aggregate_@period@
      WHERE period_start_at >= :from
        AND period_start_at < :to
      GROUP BY period_start_at, period_end_at) AS computed_slot
WHERE EXISTS (SELECT 1 FROM flow AS f
              WHERE f.observed_at >= computed_slot.period_start_at
                AND f.observed_at < computed_slot.period_end_at
                AND f.ingested_at >= computed_slot.computed)
   OR EXISTS (SELECT 1 FROM owner_reassigned_hour AS r
              WHERE r.hour_start_at >= computed_slot.period_start_at
                AND r.hour_start_at < computed_slot.period_end_at);
