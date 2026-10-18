-- opnview — the read statements the alerts_by_client_and_interface widget's script runs.
--
-- This file ADAPTS `-- screen: Alerts` of sql/queries/screens.sql (docs/widget-catalogue.md,
-- "Alerts by client and interface"): the same left joins of security_event to the provider registry
-- and to the per-provider rule-info cache, the event's own severity taking precedence over the
-- cache's, read per end -- the source and, since step 6 closed gap G7, the destination -- rather than
-- per event.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. Every column is named with AS.
--   :from, :to          the window, [from, to)
--   :interface_ids      the interfaces asked for, at either end, or NULL


-- The events of the window per provider, rule, severity source and pair of ends, counted. An event
-- with no client and no end at this firewall carries its source address, which labels its row; every
-- other event carries none, so the groups stay as few as the ends.
-- statement: widget_alerts_by_client_rows
SELECT e.provider_id AS provider_id,
       p.kind AS provider_kind,
       p.provider_key AS provider_key,
       e.rule_identity AS rule_identity,
       e.signature AS signature,
       e.normalised_severity AS event_severity,
       r.lookup_state AS lookup_state,
       r.normalised_severity AS rule_severity,
       e.src_client_id AS src_client_id,
       e.src_interface_id AS src_interface_id,
       CAST(e.src_is_this_firewall AS INTEGER) AS src_is_this_firewall,
       e.dst_client_id AS dst_client_id,
       e.dst_interface_id AS dst_interface_id,
       CAST(e.dst_is_this_firewall AS INTEGER) AS dst_is_this_firewall,
       CASE WHEN e.src_client_id IS NULL AND e.dst_client_id IS NULL
                 AND NOT e.src_is_this_firewall AND NOT e.dst_is_this_firewall
            THEN e.src_address END AS unmatched_address,
       count(*) AS events,
       max(e.occurred_at) AS newest
FROM security_event AS e
LEFT JOIN provider AS p ON p.id = e.provider_id
LEFT JOIN provider_rule_info AS r ON r.provider_id = e.provider_id AND r.rule_identity = e.rule_identity
WHERE e.occurred_at >= :from
  AND e.occurred_at < :to
  AND (CAST(:interface_ids AS jsonb) IS NULL
       OR e.src_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value))
       OR e.dst_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
GROUP BY e.provider_id, p.kind, p.provider_key, e.rule_identity, e.signature, e.normalised_severity,
         r.lookup_state, r.normalised_severity, e.src_client_id, e.src_interface_id, e.src_is_this_firewall,
         e.dst_client_id, e.dst_interface_id, e.dst_is_this_firewall,
         CASE WHEN e.src_client_id IS NULL AND e.dst_client_id IS NULL
                   AND NOT e.src_is_this_firewall AND NOT e.dst_is_this_firewall
              THEN e.src_address END;
