-- this_firewall_address — every address this firewall held, the interface it belonged to, and when
-- opnview saw it held. A row of interface_address is ONE HOLDING: from the first discovery that read
-- the value to the last that read it without a break. is_current is 1 when the row was read by the
-- latest discovery of its interface: an address the firewall still holds is held from its first
-- sighting onward, so a record made after the last discovery is not orphaned by a discovery that has
-- not run yet. The classification reads it to recognise an end as this firewall as of the record's
-- own instant; the pairing reads it for a second leg sourced by this firewall.
CREATE OR REPLACE VIEW plugin_installation.this_firewall_address AS
SELECT
    a.address                                                    AS address,
    a.interface_id                                               AS interface_id,
    a.first_seen_at                                              AS first_seen_at,
    a.last_seen_at                                               AS last_seen_at,
    CASE WHEN a.last_seen_at >= i.last_seen_at THEN 1 ELSE 0 END AS is_current
FROM plugin_installation.interface_address AS a
JOIN plugin_installation.interface AS i ON i.id = a.interface_id
WHERE a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6');



-- ---------------------------------------------------------------------------
-- How a response names the installation's members (ADR 0021, step 2): what the widget host read
-- in Go as host.installation, as four views a widget's own statement reads. Every label is the
-- user's own where one is set and the firewall's discovered description otherwise; nothing is
-- classified by what it is called. The columns stand in the order a response writes them.

-- interface_label: every interface with its label resolved -- label is the user's label where one
-- is set, the discovered description otherwise, and labelled says the user set one.
CREATE OR REPLACE VIEW plugin_installation.interface_label AS
SELECT
    i.id                                                 AS id,
    i.identifier                                         AS identifier,
    i.device                                             AS device,
    i.description                                        AS description,
    i.user_label                                         AS user_label,
    coalesce(nullif(i.user_label, ''), i.description)    AS label,
    nullif(i.user_label, '') IS NOT NULL                 AS labelled,
    i.link_kind                                          AS link_kind,
    i.vlan_tag                                           AS vlan_tag,
    i.is_upstream                                        AS upstream
FROM plugin_installation.interface AS i;

-- client_name: one client as a response names it; unstable_identity is the IEEE
-- locally-administered bit of the MAC, as a boolean.
CREATE OR REPLACE VIEW plugin_installation.client_name AS
SELECT
    c.id                    AS id,
    c.identity_kind         AS identity_kind,
    c.identity_key          AS identity_key,
    c.interface_id          AS interface_id,
    c.mac                   AS mac,
    c.hostname              AS hostname,
    c.last_address          AS last_address,
    c.owner_id              AS owner_id,
    c.owner_assigned_at     AS owner_assigned_at,
    c.unstable_identity = 1 AS unstable_identity,
    c.last_seen_at          AS last_seen_at
FROM plugin_installation.client AS c;

-- owner_name: every person, as somebody typed them in.
CREATE OR REPLACE VIEW plugin_installation.owner_name AS
SELECT o.id AS id, o.display_name AS display_name
FROM plugin_installation.owner AS o;

-- rule_label: a rule as the firewall described it.
CREATE OR REPLACE VIEW plugin_installation.rule_label AS
SELECT r.id AS id, r.pf_label AS pf_label, r.description AS description, r.action AS action,
       r.is_automatic AS automatic
FROM plugin_installation.rule AS r;
