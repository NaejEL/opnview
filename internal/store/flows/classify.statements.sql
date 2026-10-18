-- opnview — the classification statements, and the parameter glossary that every
-- *.statements.sql file of internal/store uses.
--
-- A statements file is embedded into the binary by internal/store (statements.go),
-- registered through store.RegisterStatements, and found by sql/schema-checks.sh by
-- walking the tree for "-- statement: <name>" markers, which records the EXPLAIN
-- QUERY PLAN of every statement at both seed sizes and fails if one scans a growing
-- table. The text that runs is the text that is checked, and there is no second
-- copy.
--
--
-- Each statement is preceded by a line "-- statement: <name>" and ends with a
-- semicolon. A statement whose table names carry the token @period@ is a
-- template: it is run once per aggregate period, with the token replaced by 1h,
-- 24h, 7d or 30d. Every parameter is named:
--   :address        one address, as flow stores it
--   :interface_id   the interface an address belongs to, or NULL when it is outside
--   :identity_client_id  the strongest client identity at the address, or NULL
--   :address_client_id   the address-level client at the address, or NULL
--   :not_before     the earliest last_seen_at an address-level client may carry
--   :since          an ingested_at bound
--   :slot_start, :slot_end  one calendar slot, [start, end)
--   :from, :to      an observed_at window, both ends included
--   :start, :end    a looked_up_at window, both ends included
--   :client_id, :flow_id, :lookup_id, :site_name, :delay, :now
--   :method, :observation_id  an attribution's method and the address observation
--                   it matched, or NULL
--   :name           a domain name compared as the resolver's records store it
--   :provider_id, :held_in, :owner_name, :rrtype, :value, :address, :host_label,
--   :seen_at, :covered_from, :until, :previous_at, :id  one resolver record of one
--                   poll, and the poll's place among its provider's polls
--   :link_local     1 when the address being placed is an IPv6 link-local one
--   :full           1 for a full placement, 0 for an incremental one
--   :evidence       the evidence fingerprint an address was placed from
--   :label, :at     a host name's first label, and an instant
--   :resolution     a dns_resolution.client_resolution value
--   :loopback       1 when the address being placed is a loopback address or
--                   `localhost`, which is this firewall by definition
--   :held_margin    the discovery interval, which "held at an instant" allows on
--                   either side of a sighting (the this_firewall_address view)
--   :window         the leg-pairing window, in seconds
--   :device, :dst_address, :dst_port, :protocol, :ip_version, :ip_id, :tcp_seq,
--   :src_address, :src_port, :src_is_this_firewall  one record's pairing fields
--
-- No address, CIDR, interface identifier, device name or description appears
-- below, and nothing classifies anything by what it is called.


-- statement: membership_lease
SELECT l.interface_id
FROM dhcp_lease AS l
JOIN interface AS i ON i.id = l.interface_id
WHERE l.address = :address
  AND NOT i.is_upstream
  AND (:link_local = 0 OR i.link_local_evidence)
ORDER BY l.observed_at DESC, l.interface_id
LIMIT 1;


-- statement: membership_identity
SELECT c.interface_id
FROM client AS c
JOIN interface AS i ON i.id = c.interface_id
WHERE c.last_address = :address
  AND c.identity_kind IN ('dhcp_client_id', 'mac')
  AND NOT i.is_upstream
  AND (:link_local = 0 OR i.link_local_evidence)
ORDER BY c.last_seen_at DESC, c.id
LIMIT 1;


-- The interface networks that count, decision D1: every network the operator
-- set, and every detected one the operator did not remove that discovery still
-- reports -- its last_detected_at is its interface's latest detection. origin is
-- returned because an operator network wins over a detected one.
-- statement: membership_networks
SELECT n.interface_id, n.network_address, n.prefix_length, n.origin
FROM interface_network AS n
JOIN interface AS i ON i.id = n.interface_id
WHERE NOT i.is_upstream
  AND n.removed_at IS NULL
  AND (n.origin = 'operator'
       OR n.last_detected_at = (SELECT max(m.last_detected_at) FROM interface_network AS m
                                WHERE m.interface_id = n.interface_id))
ORDER BY n.interface_id, n.network_address, n.prefix_length;


-- A sighting: a record logged INBOUND on an interface names its source as on that
-- interface's side, and one logged OUTBOUND names its destination. Only the most
-- recent sighting of each kind is read.
--
-- The address's flows are read newest first through its own index, and each meets its interface as it
-- is read (the OFFSET 0 keeps the interface a probe of each flow rather than a side of the join). Joined
-- the other way, the planner read every flow logged on an interface, through idx_flow_join, and kept
-- those of the address: 88 ms on the 1 000 000-row seed, where this reads the address's few flows
-- (18 October 2026). The rows are the same.
-- statement: membership_sighting_source
SELECT i.id, f.observed_at
FROM flow AS f
CROSS JOIN LATERAL (SELECT j.id
                    FROM interface AS j
                    WHERE j.device = f.interface_device
                      AND NOT j.is_upstream
                      AND (:link_local = 0 OR j.link_local_evidence)
                    OFFSET 0) AS i
WHERE f.src_address = :address
  AND f.direction = 'in'
ORDER BY f.observed_at DESC, i.id
LIMIT 1;


-- statement: membership_sighting_destination
SELECT i.id, f.observed_at
FROM flow AS f
CROSS JOIN LATERAL (SELECT j.id
                    FROM interface AS j
                    WHERE j.device = f.interface_device
                      AND NOT j.is_upstream
                      AND (:link_local = 0 OR j.link_local_evidence)
                    OFFSET 0) AS i
WHERE f.dst_address = :address
  AND f.direction = 'out'
ORDER BY f.observed_at DESC, i.id
LIMIT 1;


-- The strongest identity at an address: a lease's client identifier, then a MAC.
-- A client behind an upstream interface is never one.
-- statement: client_identity_at_address
SELECT c.id
FROM client AS c
WHERE c.last_address = :address
  AND c.identity_kind IN ('dhcp_client_id', 'mac')
  AND (c.interface_id IS NULL
       OR c.interface_id NOT IN (SELECT id FROM interface WHERE is_upstream))
ORDER BY CASE c.identity_kind WHEN 'dhcp_client_id' THEN 0 ELSE 1 END,
         c.last_seen_at DESC, c.id DESC
LIMIT 1;


-- statement: client_address_level
SELECT c.id
FROM client AS c
WHERE c.last_address = :address
  AND c.identity_kind = 'address_in_interface'
  AND c.interface_id = :interface_id
  AND c.last_seen_at >= :not_before
ORDER BY c.last_seen_at DESC, c.id DESC
LIMIT 1;


-- A flow end at the address that has no client and is not this firewall at the
-- record's instant: what an address-level identity is minted for. An end that is
-- this firewall is never a client, so it never calls for one.
-- statement: unclassified_end_at_address
SELECT 1
FROM flow AS f
WHERE f.src_address = :address
  AND f.src_client_id IS NULL
  AND :loopback = 0
  AND NOT EXISTS (SELECT 1 FROM this_firewall_address AS h
                  WHERE h.address = f.src_address
                    AND h.first_seen_at - :held_margin <= f.observed_at
                    AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= f.observed_at))
UNION ALL
SELECT 1
FROM flow AS f
WHERE f.dst_address = :address
  AND f.dst_client_id IS NULL
  AND :loopback = 0
  AND NOT EXISTS (SELECT 1 FROM this_firewall_address AS h
                  WHERE h.address = f.dst_address
                    AND h.first_seen_at - :held_margin <= f.observed_at
                    AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= f.observed_at))
LIMIT 1;


-- The evidence an address was last placed from, and its record.
-- statement: classification_evidence
SELECT evidence FROM address_classification WHERE address = :address;


-- statement: classification_record
INSERT INTO address_classification (address, evidence, classified_at)
VALUES (:address, :evidence, :now)
ON CONFLICT (address) DO UPDATE SET
    evidence = excluded.evidence,
    classified_at = excluded.classified_at
WHERE address_classification.evidence IS DISTINCT FROM excluded.evidence;


-- THIS FIREWALL COMES FIRST. An end whose address this firewall held at the
-- record's instant -- or a loopback address, :loopback -- is this firewall, before
-- any on-link evidence is read: it records the interface the address belonged to
-- (the most recently seen holding, then the lowest interface id; none for a
-- loopback address) and no client, and it is never outside. "Held at the instant"
-- is the this_firewall_address view's definition, with :held_margin the discovery
-- interval.
--
-- Every other end keeps the client it had, as before: an outside end keeps none. A
-- strong identity already on the row is kept; otherwise the strongest identity at
-- the address replaces whatever is there, which is how a lease naming an address
-- seen only in flows re-points those flows; otherwise an address-level client
-- already on the row is kept, and an empty end takes the address-level client of
-- the address.
--
-- With :full = 0 -- the evidence for the address has not changed since it was last
-- placed -- only the rows with an unplaced end are read: an end the collector
-- stored has no interface yet, and a placed inside end always has one. Every
-- statement writes a row only when a value changes, so a pass that changes
-- nothing writes nothing.
-- statement: classify_flow_source
UPDATE flow
SET src_interface_id = n.interface_id,
    src_client_id = n.client_id,
    src_is_this_firewall = n.this_firewall,
    traffic_scope = CASE
        WHEN n.this_firewall OR flow.dst_is_this_firewall THEN 'this_firewall'
        WHEN n.interface_id IS NOT NULL AND flow.dst_interface_id IS NOT NULL THEN 'east_west'
        ELSE 'north_south'
    END
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE :interface_id END
                 AS interface_id,
             CASE
                 WHEN p.this_firewall OR CAST(:interface_id AS BIGINT) IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN CAST(:identity_client_id AS BIGINT) IS NOT NULL THEN CAST(:identity_client_id AS BIGINT)
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE :address_client_id
             END AS client_id
      FROM (SELECT f.id,
                   f.src_client_id AS client_id,
                   CASE WHEN :loopback = 1
                             OR EXISTS (SELECT 1 FROM this_firewall_address AS h
                                        WHERE h.address = f.src_address
                                          AND h.first_seen_at - :held_margin <= f.observed_at
                                          AND (h.is_current = 1
                                               OR h.last_seen_at + :held_margin >= f.observed_at))
                        THEN true ELSE false END AS this_firewall,
                   (SELECT h.interface_id FROM this_firewall_address AS h
                    WHERE h.address = f.src_address
                      AND h.first_seen_at - :held_margin <= f.observed_at
                      AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= f.observed_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM flow AS f
            WHERE f.src_address = :address
              AND (:full = 1 OR f.src_interface_id IS NULL)) AS p) AS n
WHERE flow.id = n.id
  AND (flow.src_interface_id IS DISTINCT FROM n.interface_id
       OR flow.src_client_id IS DISTINCT FROM n.client_id
       OR flow.src_is_this_firewall IS DISTINCT FROM n.this_firewall)
RETURNING observed_at;


-- statement: classify_flow_destination
UPDATE flow
SET dst_interface_id = n.interface_id,
    dst_client_id = n.client_id,
    dst_is_this_firewall = n.this_firewall,
    traffic_scope = CASE
        WHEN flow.src_is_this_firewall OR n.this_firewall THEN 'this_firewall'
        WHEN flow.src_interface_id IS NOT NULL AND n.interface_id IS NOT NULL THEN 'east_west'
        ELSE 'north_south'
    END
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE :interface_id END
                 AS interface_id,
             CASE
                 WHEN p.this_firewall OR CAST(:interface_id AS BIGINT) IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN CAST(:identity_client_id AS BIGINT) IS NOT NULL THEN CAST(:identity_client_id AS BIGINT)
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE :address_client_id
             END AS client_id
      FROM (SELECT f.id,
                   f.dst_client_id AS client_id,
                   CASE WHEN :loopback = 1
                             OR EXISTS (SELECT 1 FROM this_firewall_address AS h
                                        WHERE h.address = f.dst_address
                                          AND h.first_seen_at - :held_margin <= f.observed_at
                                          AND (h.is_current = 1
                                               OR h.last_seen_at + :held_margin >= f.observed_at))
                        THEN true ELSE false END AS this_firewall,
                   (SELECT h.interface_id FROM this_firewall_address AS h
                    WHERE h.address = f.dst_address
                      AND h.first_seen_at - :held_margin <= f.observed_at
                      AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= f.observed_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM flow AS f
            WHERE f.dst_address = :address
              AND (:full = 1 OR f.dst_interface_id IS NULL)) AS p) AS n
WHERE flow.id = n.id
  AND (flow.dst_interface_id IS DISTINCT FROM n.interface_id
       OR flow.dst_client_id IS DISTINCT FROM n.client_id
       OR flow.dst_is_this_firewall IS DISTINCT FROM n.this_firewall)
RETURNING observed_at;


-- statement: classify_event_source
UPDATE security_event
SET src_interface_id = n.interface_id,
    src_client_id = n.client_id,
    src_is_this_firewall = n.this_firewall
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE :interface_id END
                 AS interface_id,
             CASE
                 WHEN p.this_firewall OR CAST(:interface_id AS BIGINT) IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN CAST(:identity_client_id AS BIGINT) IS NOT NULL THEN CAST(:identity_client_id AS BIGINT)
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE :address_client_id
             END AS client_id
      FROM (SELECT e.id,
                   e.src_client_id AS client_id,
                   CASE WHEN :loopback = 1
                             OR EXISTS (SELECT 1 FROM this_firewall_address AS h
                                        WHERE h.address = e.src_address
                                          AND h.first_seen_at - :held_margin <= e.occurred_at
                                          AND (h.is_current = 1
                                               OR h.last_seen_at + :held_margin >= e.occurred_at))
                        THEN true ELSE false END AS this_firewall,
                   (SELECT h.interface_id FROM this_firewall_address AS h
                    WHERE h.address = e.src_address
                      AND h.first_seen_at - :held_margin <= e.occurred_at
                      AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= e.occurred_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM security_event AS e
            WHERE e.src_address = :address
              AND (:full = 1 OR e.src_interface_id IS NULL)) AS p) AS n
WHERE security_event.id = n.id
  AND (security_event.src_interface_id IS DISTINCT FROM n.interface_id
       OR security_event.src_client_id IS DISTINCT FROM n.client_id
       OR security_event.src_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- A lookup is never given a client of its own: an address with no client stays
-- with none, and the lookup still carries its interface. A lookup whose querier is
-- this firewall carries the interface the address belonged to and no client.
-- statement: classify_lookup
UPDATE dns_resolution
SET interface_id = n.interface_id,
    interface_lookup_state = CASE WHEN n.interface_id IS NULL THEN 'not_found' ELSE 'resolved' END,
    client_id = n.client_id,
    client_is_this_firewall = n.this_firewall
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE :interface_id END
                 AS interface_id,
             CASE
                 WHEN p.this_firewall OR CAST(:interface_id AS BIGINT) IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN CAST(:identity_client_id AS BIGINT) IS NOT NULL THEN CAST(:identity_client_id AS BIGINT)
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE :address_client_id
             END AS client_id
      FROM (SELECT r.id,
                   r.client_id,
                   CASE WHEN :loopback = 1
                             OR EXISTS (SELECT 1 FROM this_firewall_address AS h
                                        WHERE h.address = r.client_address
                                          AND h.first_seen_at - :held_margin <= r.looked_up_at
                                          AND (h.is_current = 1
                                               OR h.last_seen_at + :held_margin >= r.looked_up_at))
                        THEN true ELSE false END AS this_firewall,
                   (SELECT h.interface_id FROM this_firewall_address AS h
                    WHERE h.address = r.client_address
                      AND h.first_seen_at - :held_margin <= r.looked_up_at
                      AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= r.looked_up_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM dns_resolution AS r
            WHERE r.client_address = :address
              AND (:full = 1 OR r.interface_lookup_state = 'pending')) AS p) AS n
WHERE dns_resolution.id = n.id
  AND (dns_resolution.interface_id IS DISTINCT FROM n.interface_id
       OR dns_resolution.interface_lookup_state IS DISTINCT FROM (CASE WHEN n.interface_id IS NULL
                                                             THEN 'not_found' ELSE 'resolved' END)
       OR dns_resolution.client_id IS DISTINCT FROM n.client_id
       OR dns_resolution.client_is_this_firewall IS DISTINCT FROM n.this_firewall)
RETURNING looked_up_at;


-- The purged part follows its flows. When the strongest identity at an address
-- replaces the address-level client its flows named, the purged part naming that
-- client is re-pointed to the identity too, and each statement returns the hours it
-- moved, which the refresh must recompute.
-- statement: repoint_purged_local_client
UPDATE purged_flow_hour
SET local_client_id = :identity_client_id
WHERE local_client_id IN (SELECT c.id FROM client AS c
                          WHERE c.last_address = :address
                            AND c.identity_kind = 'address_in_interface')
RETURNING hour_start_at;


-- statement: repoint_purged_source_client
UPDATE purged_flow_hour
SET src_client_id = :identity_client_id
WHERE src_client_id IN (SELECT c.id FROM client AS c
                        WHERE c.last_address = :address
                          AND c.identity_kind = 'address_in_interface')
RETURNING hour_start_at;


-- A client that nothing refers to any more, that nobody attributed to a person,
-- and that is either address-level -- every flow it named moved to a better
-- identity, or turned out to be outside -- or sits behind an upstream interface.
-- statement: purge_unreferenced_clients_at_address
DELETE FROM client
WHERE last_address = :address
  AND owner_id IS NULL
  AND (identity_kind = 'address_in_interface'
       OR interface_id IN (SELECT id FROM interface WHERE is_upstream))
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.src_client_id = client.id);


-- statement: purge_unreferenced_address_level_clients
DELETE FROM client
WHERE identity_kind = 'address_in_interface'
  AND owner_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.src_client_id = client.id);


-- statement: purge_unreferenced_upstream_clients
DELETE FROM client
WHERE interface_id IN (SELECT id FROM interface WHERE is_upstream)
  AND owner_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.src_client_id = client.id);


-- THE CLIENTS AN EARLIER BUILD MINTED AT THIS FIREWALL'S OWN ADDRESSES FROM THE
-- NEIGHBOUR TABLES. get_arp and get_ndp list the firewall's own interface
-- entries, and before the neighbour pass recognised them each became a MAC-level
-- client at an address the firewall held (defect L2 of
-- specs/SPEC-test-budget-and-two-live-defects.md). Such a client is one whose last
-- address this firewall held when it was last seen there -- "held at an instant"
-- as the this_firewall_address view defines it, :held_margin the discovery
-- interval -- and it goes once nothing names it and nobody assigned it to an
-- owner, by the same guard as the two purges above. It runs once per address the
-- firewall ever held, which are few, each found by one index seek after the last
-- (next_this_firewall_address), so neither interface_address nor client is
-- scanned.
-- statement: next_this_firewall_address
SELECT min(address)
FROM interface_address
WHERE address > :address
  AND source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6');


-- statement: purge_unreferenced_this_firewall_clients_at_address
DELETE FROM client
WHERE last_address = :address
  AND EXISTS (SELECT 1 FROM this_firewall_address AS h
              WHERE h.address = :address
                AND h.first_seen_at - :held_margin <= client.last_seen_at
                AND (h.is_current = 1 OR h.last_seen_at + :held_margin >= client.last_seen_at))
  AND owner_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM purged_flow_hour AS p WHERE p.src_client_id = client.id);


-- Reclassifying everything walks the distinct addresses one index seek at a
-- time, each statement returning the next address after :address, so even the
-- whole-history pass reads an index rather than scanning a growing table.
-- statement: next_source_address
SELECT min(src_address) FROM flow WHERE src_address > :address;


-- statement: next_destination_address
SELECT min(dst_address) FROM flow WHERE dst_address > :address;


-- statement: next_lookup_address
SELECT min(client_address) FROM dns_resolution WHERE client_address > :address;


-- statement: next_event_address
SELECT min(src_address) FROM security_event WHERE src_address > :address;


-- The addresses of every unplaced end of a flow the retention purge is about to
-- remove: the flows observed before the horizon, read through the horizon exactly as
-- internal/store/purge/purge.sql computes it. The purge places them first, so its purged
-- part never freezes a flow as it was stored -- with neither end placed, and therefore
-- north-south -- when the evidence held can place it. An outside end is unplaced for
-- ever and is returned too; placing it again changes nothing.
--
-- ITS PLAN IS BOUNDED, AND NONE OF THE FOUR SUSPECTED CAUSES HOLDS (E16, measured on
-- 16 October 2026 with EXPLAIN (ANALYZE, BUFFERS) on the 1 000 000-row seed, under the
-- custom and the generic plan). The scalar-subquery horizon is an InitPlan parameter,
-- which PostgreSQL accepts as an index condition, so it hides nothing that matters here;
-- the first branch reads idx_flow_src_interface_observed_at with the index condition
-- (src_interface_id IS NULL AND observed_at < horizon), and the second reads
-- idx_flow_observed_at with (observed_at < horizon AND dst_interface_id IS NULL), the
-- index carrying dst_interface_id among its keys -- so no index leading on
-- dst_interface_id is needed. With nothing due, both plans read 8 shared buffers; with
-- about 1 % due, 1 024; with everything due, 101 956 -- the work follows the due rows,
-- and the heap fetch each due row costs for its address is the address the purge needs.
-- No index is added. sql/schema-checks.sh, section E16, asserts all of this at both
-- seeds and three horizons.
--
-- THE TWO BRANCHES ARE APPENDED, NOT UNITED (17 October 2026), and the caller drops the
-- repeated addresses (PurgeDueAddresses, internal/store/purge/purge.go). With UNION, the
-- plan the S5A-AC35 sweep reads -- every growing table planned at a trillion rows, nested
-- loops only -- sorted the second branch by reading idx_flow_dst_address whole, in
-- address order, to feed the deduplication, rather than sort what idx_flow_observed_at
-- returns: a scan of flow, at both seeds. Appended, each branch keeps its index
-- condition on observed_at under every plan, and the set returned is the same.
-- statement: purge_due_addresses
SELECT src_address
FROM flow
WHERE observed_at < (SELECT :now - CAST(value AS BIGINT)
                     FROM setting
                     WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0)
  AND src_interface_id IS NULL
UNION ALL
SELECT dst_address
FROM flow
WHERE observed_at < (SELECT :now - CAST(value AS BIGINT)
                     FROM setting
                     WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0)
  AND dst_interface_id IS NULL;

