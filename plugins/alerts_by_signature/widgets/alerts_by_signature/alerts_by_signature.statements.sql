-- opnview — the read statements the alerts_by_signature widget's script runs.
--
-- This file ADAPTS `-- screen: Alerts` of sql/queries/screens.sql (docs/widget-catalogue.md,
-- "Alerts by signature"): the same left joins of security_event to the provider registry and to the
-- per-provider rule-info cache, with the event's own severity taking precedence over the cache's, read
-- per (provider, rule identity) and per end rather than per event, with the destination end
-- (gap G7, closed in step 6) and the device the event was observed on.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. Every column is named with AS, because the
-- script reads a row as an object of its columns.
--   :from, :to          the window, [from, to)
--   :provider_ids       the providers asked for, as a JSON id list, or NULL for every one
--   :client_ids         the clients asked for, at either end, or NULL
--   :interface_ids      the interfaces asked for, at either end, or NULL


-- The events of the window per provider, rule identity, signature text, severity source, end and
-- observed device, with each group's count and newest instant. The severity is not resolved here:
-- the script resolves it from the event's own value, then the cache's answer, and names the reason
-- when there is none.
-- statement: widget_alerts_by_signature_rows
SELECT e.provider_id AS provider_id,
       p.kind AS provider_kind,
       p.provider_key AS provider_key,
       p.display_name AS display_name,
       e.rule_identity AS rule_identity,
       e.signature AS signature,
       e.normalised_severity AS event_severity,
       r.lookup_state AS lookup_state,
       r.normalised_severity AS rule_severity,
       r.provider_severity AS provider_severity,
       r.category AS category,
       r.rule_source AS rule_source,
       e.src_client_id AS src_client_id,
       e.src_interface_id AS src_interface_id,
       CAST(e.src_is_this_firewall AS INTEGER) AS src_is_this_firewall,
       e.dst_client_id AS dst_client_id,
       e.dst_interface_id AS dst_interface_id,
       CAST(e.dst_is_this_firewall AS INTEGER) AS dst_is_this_firewall,
       e.in_interface_device AS in_interface_device,
       m.interface_id AS in_interface_id,
       count(*) AS events,
       max(e.occurred_at) AS newest
FROM plugin_suricata.security_event AS e
LEFT JOIN provider AS p ON p.id = e.provider_id
LEFT JOIN plugin_suricata.provider_rule_info AS r ON r.provider_id = e.provider_id AND r.rule_identity = e.rule_identity
LEFT JOIN plugin_installation.interface_map AS m ON m.device = e.in_interface_device
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
GROUP BY e.provider_id, p.kind, p.provider_key, p.display_name, e.rule_identity, e.signature,
         e.normalised_severity, r.lookup_state, r.normalised_severity, r.provider_severity, r.category,
         r.rule_source, e.src_client_id, e.src_interface_id, e.src_is_this_firewall, e.dst_client_id,
         e.dst_interface_id, e.dst_is_this_firewall, e.in_interface_device, m.interface_id;
