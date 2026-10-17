-- opnview — the statements run by internal/store/aggregates/aggregate.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- THE REFRESH. A slot is rewritten whole: its rows are deleted and recomputed,
-- for every family at once: the hours together (aggregatehours.statements.sql), then the days, the
-- weeks and the months, each period's slots together (aggregateslots.statements.sql).
--
-- HOW EACH PERIOD IS COMPUTED, and why no slot is ever wrong under a short
-- retention:
--   * an HOUR is computed from the flows observed inside it PLUS its purged part,
--     the purged_flow_hour rows the retention purge wrote for the flows it
--     removed from that hour. So an hour holding the horizon -- the current hour
--     under a retention shorter than an hour -- keeps what it held for the purged
--     flows, and still takes in a flow ingested late into it and a
--     reclassification of a flow still present;
--   * a DAY is composed from its 24 hours;
--   * a WEEK and a MONTH are rolled up from their days, which tile them exactly.
-- The client family of every slot is regrouped from the peer family of the same
-- slot, and the owner family from the client family of the same slot, so the
-- two distinct figures -- distinct_peers and client_count -- are counted, never
-- summed, in every period.
-- ---------------------------------------------------------------------------

-- The hours a refresh considers: every hour holding a flow ingested since the
-- previous refresh, and every hour a purge has written a purged part for since then.
-- The second half is the belt and braces of the purge's ordering (docs/data-model.md,
-- "Purge"): the purge moves the flows of an hour into its purged part, and once they
-- have left `flow` nothing else would select that hour, so a purge that ever ran
-- before a refresh had read those flows would leave the hour without their bytes.
-- statement: dirty_hours
SELECT (observed_at / 3600) * 3600
FROM flow
WHERE ingested_at >= :since
UNION
SELECT hour_start_at
FROM purged_flow_hour
WHERE purged_at >= :since;


-- The retention horizon of the flows, NULL when retention is unlimited. A slot
-- the purge would remove outright -- one whose own end, and for an hour or a day
-- the end of its ISO week and of its calendar month, all lie before it -- is not
-- rewritten unless it is current (internal/store/aggregates/aggregate.go).
-- statement: flow_horizon
SELECT :now - CAST(value AS BIGINT)
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0;

