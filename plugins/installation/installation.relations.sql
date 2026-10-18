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

-- blocked_decision — every refusal, whichever engine decided it, under one vocabulary. The purpose is
-- the user's, from blocklist.purpose, and never derived from a list's name. engine_reference is what
-- decided inside the engine: the pf label (rid), the list's name, or the rule identity. target is what
-- was refused: the destination address, or the domain. target_is_this_firewall is 1 when that address
-- is one the firewall itself holds, which is how a feed says "-> this firewall".
--
-- It lives here, with the installation model, because it joins three sources' tables that no single
-- source owns (ADR 0020, wave 1); flow, dns_resolution, security_event and blocklist are read from the
-- core schema until their waves move them, and then by their qualified names.
CREATE OR REPLACE VIEW plugin_installation.blocked_decision AS
SELECT
    'flow'                                                  AS source_table,
    f.id                                                    AS source_id,
    f.observed_at                                           AS occurred_at,
    CASE
        WHEN f.log_reason = 'match' THEN 'firewall_rule'
        WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                              'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                              'state-mismatch', 'state-insert', 'state-limit',
                              'src-limit', 'synproxy') THEN 'firewall_no_rule'
        ELSE 'firewall_reason_not_recorded'
    END                                                     AS engine_kind,
    f.rid                                                   AS engine_reference,
    f.src_client_id                                         AS client_id,
    f.src_interface_id                                      AS interface_id,
    f.dst_address                                           AS target,
    'address'                                               AS target_kind,
    CAST(CAST(EXISTS (SELECT 1 FROM plugin_installation.interface_address AS a
            WHERE a.address = f.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
                                                            AS target_is_this_firewall
FROM opnview.flow AS f
WHERE f.action IN ('block', 'reject')
UNION ALL
SELECT
    'dns_resolution',
    r.id,
    r.looked_up_at,
    CASE
        WHEN r.blocklist_id IS NULL THEN 'dns_list_not_recorded'
        WHEN b.purpose IS NULL THEN 'dns_unassigned_list'
        WHEN b.purpose = 'advertising' THEN 'dns_advertising_list'
        WHEN b.purpose = 'tracking' THEN 'dns_tracking_list'
        WHEN b.purpose = 'threat' THEN 'dns_threat_list'
        WHEN b.purpose = 'parental' THEN 'dns_parental_list'
        ELSE 'dns_other_list'
    END,
    b.name,
    r.client_id,
    r.interface_id,
    r.domain,
    'domain',
    0
FROM opnview.dns_resolution AS r
LEFT JOIN opnview.blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
UNION ALL
SELECT
    'security_event',
    e.id,
    e.occurred_at,
    'security_engine',
    e.rule_identity,
    e.src_client_id,
    e.src_interface_id,
    e.dst_address,
    'address',
    CAST(CAST(EXISTS (SELECT 1 FROM plugin_installation.interface_address AS a
            WHERE a.address = e.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
FROM opnview.security_event AS e
WHERE e.event_action = 'blocked';
