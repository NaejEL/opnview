-- opnview — the read statements the widget code in refs runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- References, resolved by exact match on the declared key and on nothing else. The label
-- of a reference is never read by any of these.
-- statement: widget_ref_interface_identifier
SELECT id FROM interface WHERE identifier = :value;


-- statement: widget_ref_interface_device
SELECT id FROM interface WHERE device = :value;


-- statement: widget_ref_interface_user_label
SELECT id FROM interface WHERE user_label = :value;


-- statement: widget_ref_client_identity
SELECT id FROM client WHERE identity_kind = :identity_kind AND identity_key = :value;


-- A host name is not unique; the API refuses to pick one of several clients.
-- statement: widget_ref_client_hostname
SELECT id FROM client WHERE hostname = :value LIMIT 2;


-- statement: widget_ref_rule_pf_label
SELECT id FROM rule WHERE pf_label = :value;


-- statement: widget_ref_rule_description
SELECT id FROM rule WHERE description = :value LIMIT 2;


-- statement: widget_ref_owner_display_name
SELECT id FROM owner WHERE display_name = :value;


-- statement: widget_ref_provider
SELECT id FROM provider WHERE kind = :kind AND provider_key = :value;


-- statement: widget_ref_country
SELECT 1 FROM geo_asn WHERE country_code = :value LIMIT 1;


-- statement: widget_ref_operator
SELECT 1 FROM geo_asn WHERE asn = :asn LIMIT 1;


-- A site name is the name a lookup asked for, so it resolves when a stored lookup asked for
-- it, compared the way the attribution compares names.
-- statement: widget_ref_site
SELECT 1 FROM dns_resolution WHERE lower(rtrim(domain, '.')) = lower(rtrim(:value, '.')) LIMIT 1;

