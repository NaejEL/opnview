-- When each registered provider's current availability state began, as opnview's own checks bound
-- it (coverage row 39 of docs/widget-api.md): after state_since_after_at, by state_since_by_at, both
-- NULL until a change of state has been observed. Run by ReadAvailabilitySince (since.go).
-- statement: widget_availability_since
SELECT p.kind, p.provider_key, a.provider_id IS NOT NULL, a.state_since_after_at, a.state_since_by_at
FROM provider AS p
LEFT JOIN source_availability AS a ON a.provider_id = p.id
ORDER BY p.kind, p.provider_key;
