-- opnview — the statements run by internal/store/aggregates/downtime.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- The intervals during which opnview was not running (D2), overlapping a
-- window, oldest first. idx_opnview_downtime_interval bounds the search by
-- interval_end_at; the table holds one row per absence and is purged with the
-- retention, but it is planned like every growing table all the same.
-- ---------------------------------------------------------------------------

-- statement: read_not_running
SELECT interval_start_at, interval_end_at, clean_stop, detected_at
FROM opnview_downtime
WHERE interval_end_at > :from
  AND interval_start_at < :to
ORDER BY interval_start_at, id;

