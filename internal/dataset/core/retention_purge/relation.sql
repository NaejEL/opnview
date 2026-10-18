-- How far back the retention purge has removed the logged legs (schema.sql, retention_purge): every flow
-- observed before purged_before is gone, so a record older than it may have had a leg that is no longer held.
-- No row before the first purge. A derivation reads it so that a leg it cannot find is never claimed absent
-- when the purge may have taken it -- the netflow plugin's beyond_flow_horizon reason.
--
-- The purge is no provider's pass: the freshness names `discovery`, which no provider row carries, and the
-- condition reads state_unknown.
SELECT r.purged_before,
       r.purged_at
FROM retention_purge AS r
