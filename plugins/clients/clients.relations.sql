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
