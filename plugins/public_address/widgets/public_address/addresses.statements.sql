-- The addresses an upstream interface held (ADR 0021, step 4), read by the figures library's
-- addresses area (host.lib.figures.addresses).

-- statement: widget_interface_addresses
SELECT source_field, address, prefix_length, address_family, first_seen_at, last_seen_at
FROM plugin_installation.interface_address
WHERE interface_id = :interface_id
ORDER BY last_seen_at DESC, first_seen_at DESC, id;
