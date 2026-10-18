-- opnview — the read statements the security_alerts_over_time widget's script runs.
--
-- This file ADAPTS `-- screen: Alerts` of sql/queries/screens.sql (docs/widget-catalogue.md,
-- "Security alerts over time"): the same left joins of security_event to the provider registry and
-- to the per-provider rule-info cache, the event's own severity taking precedence over the cache's,
-- counted per bucket of the window rather than listed per event.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. Every column is named with AS.
--   :from, :to          the window, [from, to)
--   :width              the bucket width, in seconds; buckets are UTC-aligned multiples of it
--   :provider_ids       the providers asked for, as a JSON id list, or NULL for every one
--   :client_ids         the clients asked for, at either end, or NULL
--   :interface_ids      the interfaces asked for, at either end, or NULL


-- The events of the window per bucket, provider and severity source, counted. The severity is
-- resolved by the script: the event's own, then the cache's answer, else unknown with its reason.
-- statement: widget_security_alerts_buckets
SELECT e.occurred_at - e.occurred_at % :width AS bucket_start,
       e.provider_id AS provider_id,
       p.kind AS provider_kind,
       p.provider_key AS provider_key,
       p.display_name AS display_name,
       e.normalised_severity AS event_severity,
       r.lookup_state AS lookup_state,
       r.normalised_severity AS rule_severity,
       count(*) AS events
FROM security_event AS e
LEFT JOIN provider AS p ON p.id = e.provider_id
LEFT JOIN provider_rule_info AS r ON r.provider_id = e.provider_id AND r.rule_identity = e.rule_identity
WHERE e.occurred_at >= :from
  AND e.occurred_at < :to
  AND (CAST(:provider_ids AS jsonb) IS NULL
       OR e.provider_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:provider_ids AS jsonb)) AS j(value)))
  AND (CAST(:client_ids AS jsonb) IS NULL
       OR e.src_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value))
       OR e.dst_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR e.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value))
       OR e.dst_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY e.occurred_at - e.occurred_at % :width, e.provider_id, p.kind, p.provider_key, p.display_name,
         e.normalised_severity, r.lookup_state, r.normalised_severity;
