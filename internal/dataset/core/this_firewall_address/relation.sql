-- This firewall's own addresses, as discovery holds them (PA3 of the plugin-API amendment): every address
-- the firewall held, the interface it belonged to, and when opnview saw it held -- the this_firewall_address
-- view of the schema, whose term is OPNsense's "This Firewall" (pf's `self`; docs/data-model.md,
-- *Vocabulary*). An address was held at an instant t when first_seen_at - margin <= t and
-- (is_current = 1 or t <= last_seen_at + margin), margin being the discovery interval, as the view's own
-- comment defines it. Never a gateway: those are other machines.
--
-- Discovery is no provider of a kind, so the dataset's freshness names `discovery`, which no provider row
-- carries: its condition reads state_unknown.
SELECT a.address,
       i.identifier AS interface,
       a.first_seen_at,
       a.last_seen_at,
       a.is_current
FROM this_firewall_address AS a
JOIN interface AS i ON i.id = a.interface_id
