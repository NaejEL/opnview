-- opnview — the statements of the rule-info cache: which rule identities a security_event
-- provider is asked about, and what its answers write.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name.
--   :provider_id    the provider whose events and whose cache are read
--   :stale_before   a cache row fetched before this instant is due again (the re-check age)
--   :bound          at most this many identities are returned (the per-pass bound)
--   :rule_identity, :lookup_state, :normalised_severity, :provider_severity, :category,
--   :rule_source, :fetched_at  one answer


-- The rule identities one provider's events carry that its cache holds no row for, or a row
-- fetched before :stale_before, in identity order and at most :bound of them. The distinct
-- identities are walked one index seek at a time on idx_security_event_rule_occurred_at --
-- each step the smallest identity after the previous one -- so the walk reads one index entry
-- per distinct identity rather than every event, and each is checked against the cache by its
-- primary key.
-- statement: rule_identities_to_fetch
WITH RECURSIVE identities (rule_identity) AS (
    SELECT (SELECT min(e.rule_identity) FROM security_event AS e WHERE e.provider_id = :provider_id)
    UNION ALL
    SELECT (SELECT min(e.rule_identity) FROM security_event AS e
            WHERE e.provider_id = :provider_id AND e.rule_identity > i.rule_identity)
    FROM identities AS i
    WHERE i.rule_identity IS NOT NULL
)
SELECT i.rule_identity
FROM identities AS i
WHERE i.rule_identity IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM provider_rule_info AS r
                  WHERE r.provider_id = :provider_id
                    AND r.rule_identity = i.rule_identity
                    AND r.fetched_at >= :stale_before)
ORDER BY i.rule_identity
LIMIT :bound;


-- One answer: what the provider said of one rule identity, replacing what the cache held.
-- statement: rule_info_upsert
INSERT INTO provider_rule_info (provider_id, rule_identity, lookup_state, normalised_severity,
                                provider_severity, category, rule_source, fetched_at)
VALUES (:provider_id, :rule_identity, :lookup_state, :normalised_severity, :provider_severity,
        :category, :rule_source, :fetched_at)
ON CONFLICT (provider_id, rule_identity) DO UPDATE SET
    lookup_state        = excluded.lookup_state,
    normalised_severity = excluded.normalised_severity,
    provider_severity   = excluded.provider_severity,
    category            = excluded.category,
    rule_source         = excluded.rule_source,
    fetched_at          = excluded.fetched_at;
