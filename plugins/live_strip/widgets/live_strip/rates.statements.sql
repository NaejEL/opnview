-- The current rates (ADR 0021, step 4): the readings of both sampled subjects of the insight plugin --
-- the totals of each local address and the per-peer figures, fine and baseline, each with the live
-- cadence of a fine sample -- and what names their local addresses, read by the figures library's
-- rates area (host.lib.figures.rates). A subject is read by its parts, the device and the addresses
-- its provider reported, never by its key.

-- statement: widget_rate_rows
SELECT subject_kind, interface_device, local_address, coalesce(peer_address, '') AS peer, measure, value,
       sampled_at, live_cadence_seconds
FROM plugin_insight.measurement_sample
WHERE sampled_at >= :since AND sampled_at <= :now
  AND subject_kind IN ('interface_endpoint', 'interface_endpoint_pair')
  AND measure IN ('rate_bits_in', 'rate_bits_out')
  AND interface_device IS NOT NULL
  AND local_address IS NOT NULL
  AND (subject_kind = 'interface_endpoint' OR peer_address IS NOT NULL);

-- Whether an address is inside: on an interface, by the clients plugin's membership.
-- statement: widget_rate_inside
SELECT plugin_clients.interface_of(:address) IS NOT NULL AS inside;

-- The strongest identity at an address: a lease's client identifier, then a MAC; a client behind an
-- upstream interface is never one.
-- statement: widget_rate_client_at_address
SELECT c.id
FROM plugin_installation.client AS c
WHERE c.last_address = :address
  AND c.identity_kind IN ('dhcp_client_id', 'mac')
  AND (c.interface_id IS NULL
       OR c.interface_id NOT IN (SELECT id FROM plugin_installation.interface WHERE is_upstream))
ORDER BY CASE c.identity_kind WHEN 'dhcp_client_id' THEN 0 ELSE 1 END, c.last_seen_at DESC, c.id DESC
LIMIT 1;

-- statement: widget_rate_address_level_client
SELECT id
FROM plugin_installation.client
WHERE last_address = :address AND identity_kind = 'address_in_interface'
ORDER BY last_seen_at DESC, id DESC
LIMIT 1;
