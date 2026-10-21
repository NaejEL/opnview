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

