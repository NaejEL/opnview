-- The address tests the classification needs, as the Go that preceded it decided them (ADR 0020
-- wave 3): an address as a flow stores it is text, possibly with an IPv6 zone and possibly
-- IPv4-mapped, and the three tests below read it as the one address it names.

-- to_inet — the address an address text names, with its zone dropped and an IPv4-mapped address
-- unmapped; NULL for a text that is not an address.
CREATE OR REPLACE FUNCTION plugin_clients.to_inet(address TEXT) RETURNS inet
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE
        WHEN NOT pg_input_is_valid(split_part(address, '%', 1), 'inet') THEN NULL
        WHEN CAST(split_part(address, '%', 1) AS inet) << CAST('::ffff:0.0.0.0/96' AS inet)
            THEN CAST('0.0.0.0' AS inet) + (CAST(split_part(address, '%', 1) AS inet) - CAST('::ffff:0.0.0.0' AS inet))
        ELSE CAST(split_part(address, '%', 1) AS inet)
    END
$$;

-- is_link_local — an IPv6 link-local unicast address (RFC 4291, section 2.5.6); an IPv4-mapped one is not.
CREATE OR REPLACE FUNCTION plugin_clients.is_link_local(address TEXT) RETURNS boolean
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT coalesce(family(plugin_clients.to_inet(address)) = 6
                    AND plugin_clients.to_inet(address) << CAST('fe80::/10' AS inet), false)
$$;

-- is_local_delivery — an address delivered to the firewall itself whatever interface holds it: the
-- loopback, the name `localhost`, the IPv4 limited broadcast (RFC 919) and every multicast address
-- (RFC 5771, RFC 4291).
CREATE OR REPLACE FUNCTION plugin_clients.is_local_delivery(address TEXT) RETURNS boolean
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE
        WHEN lower(rtrim(address, '.')) = 'localhost' THEN true
        WHEN plugin_clients.to_inet(address) IS NULL THEN false
        ELSE plugin_clients.to_inet(address) << CAST('127.0.0.0/8' AS inet)
             OR plugin_clients.to_inet(address) = CAST('::1' AS inet)
             OR plugin_clients.to_inet(address) = CAST('255.255.255.255' AS inet)
             OR plugin_clients.to_inet(address) << CAST('224.0.0.0/4' AS inet)
             OR plugin_clients.to_inet(address) << CAST('ff00::/8' AS inet)
    END
$$;


-- interface_of -- the interface an address belongs to, from on-link evidence alone, in order of
-- strength, as the Go membership read decided it before ADR 0021 (internal/store/flows/membership.go):
-- a lease, a client identity, an interface network -- an operator network before a detected one, the
-- longest prefix within each, the lowest interface id on a tie -- then the most recent sighting, a
-- record logged inbound naming its source and one logged outbound its destination. Evidence on an
-- upstream interface is not evidence, and an IPv6 link-local address is evidence only where the
-- operator says so. NULL for an address no evidence places: outside. The sampled rates split inside
-- from outside with it (plugins/figures, the rates area).
CREATE OR REPLACE FUNCTION plugin_clients.interface_of(address TEXT) RETURNS BIGINT
LANGUAGE sql STABLE STRICT AS $$
    WITH link AS (SELECT plugin_clients.is_link_local(address) AS local),
    lease AS (
        SELECT l.interface_id
        FROM plugin_leases.dhcp_lease AS l
        JOIN plugin_installation.interface AS i ON i.id = l.interface_id, link
        WHERE l.address = address AND NOT i.is_upstream AND (NOT link.local OR i.link_local_evidence)
        ORDER BY l.observed_at DESC, l.interface_id
        LIMIT 1),
    identity AS (
        SELECT c.interface_id
        FROM plugin_installation.client AS c
        JOIN plugin_installation.interface AS i ON i.id = c.interface_id, link
        WHERE c.last_address = address AND c.identity_kind IN ('dhcp_client_id', 'mac')
          AND NOT i.is_upstream AND (NOT link.local OR i.link_local_evidence)
        ORDER BY c.last_seen_at DESC, c.id
        LIMIT 1),
    network AS (
        SELECT n.interface_id
        FROM plugin_installation.interface_network AS n
        JOIN plugin_installation.interface AS i ON i.id = n.interface_id, link
        WHERE NOT link.local
          AND plugin_clients.to_inet(address) IS NOT NULL
          AND NOT i.is_upstream
          AND n.removed_at IS NULL
          AND (n.origin = 'operator'
               OR n.last_detected_at = (SELECT max(m.last_detected_at) FROM plugin_installation.interface_network AS m
                                        WHERE m.interface_id = n.interface_id))
          AND plugin_clients.to_inet(address) <<= CAST(n.network_address || '/' || n.prefix_length AS inet)
        ORDER BY CASE WHEN n.origin = 'operator' THEN 0 ELSE 1 END, n.prefix_length DESC, n.interface_id
        LIMIT 1),
    sighting AS (
        SELECT s.id
        FROM (SELECT i.id, f.observed_at
              FROM plugin_pf.flow AS f
              CROSS JOIN LATERAL (SELECT j.id FROM plugin_installation.interface AS j, link
                                  WHERE j.device = f.interface_device AND NOT j.is_upstream
                                    AND (NOT link.local OR j.link_local_evidence)
                                  OFFSET 0) AS i
              WHERE f.src_address = address AND f.direction = 'in'
              UNION ALL
              SELECT i.id, f.observed_at
              FROM plugin_pf.flow AS f
              CROSS JOIN LATERAL (SELECT j.id FROM plugin_installation.interface AS j, link
                                  WHERE j.device = f.interface_device AND NOT j.is_upstream
                                    AND (NOT link.local OR j.link_local_evidence)
                                  OFFSET 0) AS i
              WHERE f.dst_address = address AND f.direction = 'out') AS s
        ORDER BY s.observed_at DESC, s.id
        LIMIT 1)
    SELECT coalesce((SELECT interface_id FROM lease), (SELECT interface_id FROM identity),
                    (SELECT interface_id FROM network), (SELECT id FROM sighting))
$$;
