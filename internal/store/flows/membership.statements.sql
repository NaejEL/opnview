-- opnview — the membership statements internal/store/flows/membership.go runs: the four kinds of
-- on-link evidence an address is placed by, as the sampled rates still read them. The classification
-- itself is the clients plugin's (plugins/clients/clients.statements.sql, ADR 0020 wave 3), where the
-- same four reads are set-based.
--
-- Parameters: :address one address, as flow stores it; :link_local 1 when it is an IPv6 link-local one.
-- No address, CIDR, interface identifier, device name or description appears below.


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


-- The interface networks that count, decision D1: every network the operator set, and every detected
-- one the operator did not remove that discovery still reports.
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


-- A sighting: a record logged INBOUND on an interface names its source as on that interface's side,
-- and one logged OUTBOUND names its destination. Only the most recent sighting of each kind is read.
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


-- The strongest identity at an address: a lease's client identifier, then a MAC. A client behind
-- an upstream interface is never one. The samples reader asks it of a measured local address
-- (internal/store/samples/samples.go) to name the client a reading is of.
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
