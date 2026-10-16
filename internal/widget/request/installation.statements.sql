-- opnview — the read statements the widget code in installation runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- ---------------------------------------------------------------------------
-- The installation's own rows, each read by key.
-- ---------------------------------------------------------------------------

-- The label an interface is named by is the user's own where one is set and the discovered
-- description otherwise; whether the user set one is the last column. Neither is ever read
-- to decide what an interface is.
-- statement: widget_interfaces
SELECT id, identifier, device, description, user_label, link_kind, vlan_tag, is_upstream,
       coalesce(nullif(user_label, ''), description),
       CASE WHEN nullif(user_label, '') IS NULL THEN 0 ELSE 1 END
FROM interface
ORDER BY id;


-- statement: widget_owners
SELECT id, display_name FROM owner ORDER BY id;


-- statement: widget_client
SELECT id, identity_kind, identity_key, interface_id, mac, hostname, last_address, owner_id,
       unstable_identity, last_seen_at, owner_assigned_at
FROM client
WHERE id = :client_id;


-- statement: widget_rule
SELECT id, pf_label, description, action, is_automatic FROM rule WHERE id = :rule_id;


-- The geolocations of a set of addresses, :addresses a JSON array of them, read together: every
-- outside peer a request places, at the first geolocation a script asks for (installation.go,
-- Geolocation), so a widget placing a thousand peers reads them in one statement rather than one
-- statement a peer. The set is bounded by the caller (geolocationBatch), and each address is a
-- search of geo_asn's primary key.
-- statement: widget_geo
SELECT address, lookup_state, country_code, country_name, latitude, longitude, asn, operator,
       dataset_build_at
FROM geo_asn
WHERE address IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:addresses AS jsonb)) AS j(value));

