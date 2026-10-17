-- opnview — the read statements the widget code in measure runs: what decides whether a volume answer
-- can be of the `netflow` measure (PS22 of the NetFlow join amendment to
-- specs/SPEC-backend-completion-plugins.md; docs/widget-api.md, "Volumes and the observation-point
-- limit").
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql in
-- internal/widget for the parameter conventions). sql/schema-checks.sh plans each statement at both
-- seed sizes. They read what the collector stored -- the active flow_record provider, the capture
-- configurations it read -- and name no exporter, endpoint or
-- model field: which provider feeds the kind is its plugin's.


-- The active providers of the flow_record kind: none means NetFlow is not configured.
-- statement: widget_flow_record_providers
SELECT id FROM provider WHERE kind = 'flow_record' AND is_active = 1 ORDER BY id;


-- A provider's capture configurations, oldest first, with how many interfaces each captures on in
-- either direction: the intervals bounded by read instants of PS18, kept by NJ6 of the amendment "the
-- NetFlow join with every interface captured".
-- statement: widget_capture_configurations
SELECT c.id, c.first_read_at, c.last_read_at, c.fingerprint IS NOT NULL AS readable,
       coalesce(c.enabled, 0) AS enabled
FROM capture_configuration AS c
WHERE c.provider_id = :provider_id
ORDER BY c.first_read_at;


-- The captured interfaces of a provider's capture configurations.
-- statement: widget_capture_interfaces
SELECT i.capture_configuration_id, i.identifier, i.ingress, i.egress
FROM capture_interface AS i
JOIN capture_configuration AS c ON c.id = i.capture_configuration_id
WHERE c.provider_id = :provider_id
ORDER BY i.capture_configuration_id, i.identifier;

