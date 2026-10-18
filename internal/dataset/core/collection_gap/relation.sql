-- The intervals each provider knows it did not cover (schema.sql, collection_gap): a page that did not reach
-- back far enough, a push entry that was not alive, a NetFlow exporter's sequence jump. One row per gap: the
-- provider's kind and key, the interval, both ends included, and the reason, a word of the closed list of
-- the table. A derivation reads it to tell a leg or a record that was never read from one that does not
-- exist -- the netflow plugin's export_gap state and its filter_log_gap reason.
--
-- The rows are written by the passes and the push entries of every kind, so no one provider dates the
-- dataset: the freshness names `discovery`, which no provider row carries, and the condition reads
-- state_unknown.
SELECT p.kind,
       p.provider_key,
       g.interval_start_at,
       g.interval_end_at,
       g.reason
FROM collection_gap AS g
JOIN provider AS p ON p.id = g.provider_id
