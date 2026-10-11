-- opnview — the statements run by internal/store/thisfirewall.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- Every holding of an address by this firewall, for the evidence fingerprint
-- (internal/store/classify.go): which interface, and since when. last_seen_at and
-- is_current are deliberately not returned. The first moves at every discovery,
-- and the fingerprint must change only when the holding does. The second turns 0
-- when discovery stops reporting the address, which ends the holding at
-- last_seen_at plus the margin; every row already placed as this firewall while it
-- was current lies before the discovery that noticed, and so within one discovery
-- interval -- the margin -- of last_seen_at, where it is still held.
-- statement: firewall_address_holdings
SELECT h.interface_id, h.first_seen_at
FROM this_firewall_address AS h
WHERE h.address = :address
ORDER BY h.interface_id, h.first_seen_at;


-- Whether this firewall held an address at one instant, as the this_firewall_address
-- view defines "held at an instant", :held_margin the discovery interval. It is what
-- the neighbour pass asks of an ARP or NDP entry before it creates a client
-- (internal/collect/dhcplease.go): the tables list the firewall's own interface
-- entries, and an entry at an address the firewall holds is this firewall.
-- statement: this_firewall_held_at
SELECT 1
FROM this_firewall_address AS h
WHERE h.address = :address
  AND h.first_seen_at - :held_margin <= :at
  AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= :at)
LIMIT 1;

