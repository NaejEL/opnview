-- How far each provider's source has been read (schema.sql, provider_read_through): every record its source
-- held before read_through is stored, or lies in a gap the provider recorded. The filter log's is the instant
-- its last pass that stored everything it read began asking, or the newest record a push path delivered in
-- order; a derivation joining another source to the logged legs waits for it, so a leg not read yet is never
-- taken for one that does not exist. "Read-through" is opnview's own term: no product names it.
--
-- The rows are written by the passes of every kind, so no one provider dates the dataset: the freshness names
-- `discovery`, which no provider row carries, and the condition reads state_unknown.
SELECT p.kind,
       p.provider_key,
       r.read_through
FROM provider_read_through AS r
JOIN provider AS p ON p.id = r.provider_id
