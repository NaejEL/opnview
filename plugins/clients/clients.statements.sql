-- The classification and the pairing, as one SQL-only derivation (ADR 0019, ADR 0020 wave 3): the
-- statements run in the manifest's order, in one transaction, as the plugin's role, after every pass
-- that brings evidence. They are the Go of internal/store/flows (classify.go, pair.go) rewritten set-
-- based, and they keep its properties: the result depends on the evidence held and not on the order
-- it arrived in, a run over unchanged evidence writes nothing, and the addresses are placed in sorted
-- order, which is the order a new address-level identity is minted in.
--
-- Parameters: :since (the last successful run, NULL before the first), :now, :held_margin (the
-- discovery interval), :leg_pairing_window, :setting_address_idle_window_seconds.

-- THE LEASE IDENTITIES. A lease names the machine that holds it: by the client identifier the server
-- reported -- the DHCP client id, the DUID or the IAID, the first present -- then by the hardware
-- address, and otherwise at the address level, on the interface the leases plugin resolved. The client
-- is minted or brought up to date -- an interface, a hardware address, a host name or a vendor it did
-- not have, its last address, its last sighting -- and the lease is pointed at it (ADR 0020 wave 4; the
-- Go lease pass did the same before storing each lease). Only the leases the pass stored or brought
-- up to date are looked at; of several reporting one identity in a pass, the last stored wins.
-- statement: mint_lease_clients
WITH leases AS (
    SELECT l.id, l.address, l.interface_id, l.mac, l.hostname, l.vendor_hint,
           CASE WHEN coalesce(nullif(l.dhcp_client_id, ''), nullif(l.duid, ''), nullif(l.iaid, '')) IS NOT NULL
                    THEN 'dhcp_client_id'
                WHEN l.mac IS NOT NULL THEN 'mac'
                ELSE 'address_in_interface'
           END AS identity_kind,
           CASE WHEN coalesce(nullif(l.dhcp_client_id, ''), nullif(l.duid, ''), nullif(l.iaid, '')) IS NOT NULL
                    THEN coalesce(nullif(l.dhcp_client_id, ''), nullif(l.duid, ''), nullif(l.iaid, ''))
                WHEN l.mac IS NOT NULL THEN l.mac
                ELSE coalesce(CAST(l.interface_id AS TEXT), 'unknown') || '|' || l.address || '|'
                     || ((CAST(:now AS BIGINT) / 86400) * 86400)
           END AS identity_key
    FROM plugin_leases.dhcp_lease AS l
    WHERE CAST(:since AS BIGINT) IS NULL OR l.observed_at >= CAST(:since AS BIGINT)
),
minted AS (
    INSERT INTO plugin_installation.client (identity_kind, identity_key, interface_id, mac, hostname, vendor_hint,
                                            last_address, first_seen_at, last_seen_at)
    SELECT DISTINCT ON (identity_kind, identity_key) identity_kind, identity_key, interface_id, mac, hostname,
           vendor_hint, address, CAST(:now AS BIGINT), CAST(:now AS BIGINT)
    FROM leases
    ORDER BY identity_kind, identity_key, id DESC
    ON CONFLICT (identity_kind, identity_key) DO UPDATE SET
        interface_id = coalesce(excluded.interface_id, client.interface_id),
        mac          = coalesce(excluded.mac, client.mac),
        hostname     = coalesce(excluded.hostname, client.hostname),
        vendor_hint  = coalesce(excluded.vendor_hint, client.vendor_hint),
        last_address = coalesce(excluded.last_address, client.last_address),
        last_seen_at = excluded.last_seen_at
    RETURNING id, identity_kind, identity_key)
UPDATE plugin_leases.dhcp_lease AS l
SET client_id = m.id
FROM leases AS x
JOIN minted AS m ON m.identity_kind = x.identity_kind AND m.identity_key = x.identity_key
WHERE l.id = x.id
  AND l.client_id IS DISTINCT FROM m.id;

-- THE ON-LINK FINGERPRINT. What the classification reads of the interfaces -- which are upstream,
-- which admit link-local evidence -- and the networks that count (decision D1: every operator network,
-- and every detected one not removed that discovery still reports). When it changed, every address
-- is placed again; it is recorded here so the next run sees it unchanged.
-- statement: onlink_fingerprint
INSERT INTO plugin_clients.onlink (singleton, fingerprint, changed)
SELECT 1, coalesce(f.text, ''), true
FROM (SELECT string_agg(part, '/' ORDER BY part) AS text
      FROM (SELECT 'i:' || i.id || ':' || i.is_upstream || ':' || i.link_local_evidence AS part
            FROM plugin_installation.interface AS i
            UNION ALL
            SELECT 'n:' || n.interface_id || ':' || n.network_address || '/' || n.prefix_length || ':' || n.origin
            FROM plugin_installation.interface_network AS n
            JOIN plugin_installation.interface AS i ON i.id = n.interface_id
            WHERE NOT i.is_upstream
              AND n.removed_at IS NULL
              AND (n.origin = 'operator'
                   OR n.last_detected_at = (SELECT max(m.last_detected_at) FROM plugin_installation.interface_network AS m
                                            WHERE m.interface_id = n.interface_id))) AS parts) AS f
ON CONFLICT (singleton) DO UPDATE SET
    changed = plugin_clients.onlink.fingerprint IS DISTINCT FROM excluded.fingerprint,
    fingerprint = excluded.fingerprint;


-- statement: clear_placement
DELETE FROM plugin_clients.placement;


-- THE ADDRESSES OF THE RUN: every address a row stored since the last run names, every lease's, every
-- client seen since, every lookup still pending; and every address at all when the on-link evidence
-- changed or no run succeeded yet.
-- statement: collect_addresses
INSERT INTO plugin_clients.placement (address)
SELECT DISTINCT a.address
FROM (SELECT f.src_address AS address FROM plugin_pf.flow AS f
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR f.ingested_at >= CAST(:since AS BIGINT)
      UNION ALL
      SELECT f.dst_address FROM plugin_pf.flow AS f
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR f.ingested_at >= CAST(:since AS BIGINT)
      UNION ALL
      SELECT l.address FROM plugin_leases.dhcp_lease AS l
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR l.observed_at >= CAST(:since AS BIGINT) - CAST(:setting_address_idle_window_seconds AS BIGINT)
      UNION ALL
      SELECT c.last_address FROM plugin_installation.client AS c
      WHERE c.last_address IS NOT NULL
        AND ((SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
             OR c.last_seen_at >= CAST(:since AS BIGINT))
      UNION ALL
      SELECT e.src_address FROM plugin_suricata.security_event AS e
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR e.ingested_at >= CAST(:since AS BIGINT)
      UNION ALL
      SELECT e.dst_address FROM plugin_suricata.security_event AS e
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR e.ingested_at >= CAST(:since AS BIGINT)
      UNION ALL
      SELECT r.client_address FROM plugin_unbound.dns_resolution AS r
      WHERE (SELECT changed FROM plugin_clients.onlink) OR CAST(:since AS BIGINT) IS NULL
         OR r.ingested_at >= CAST(:since AS BIGINT) OR r.interface_lookup_state = 'pending') AS a
WHERE a.address IS NOT NULL AND a.address <> '';


-- What each address is: the one address it names, whether it is link-local, whether it is delivered
-- to the firewall itself at every instant, and every interval this firewall held it (the fingerprint
-- reads the holdings, deliberately without their last instant, which moves at every discovery).
-- statement: mark_addresses
UPDATE plugin_clients.placement AS p
SET address_inet = plugin_clients.to_inet(p.address),
    link_local = plugin_clients.is_link_local(p.address),
    loopback = plugin_clients.is_local_delivery(p.address),
    holdings = (SELECT string_agg(h.interface_id || ':' || h.first_seen_at, ',' ORDER BY h.interface_id, h.first_seen_at)
                FROM plugin_installation.this_firewall_address AS h
                WHERE h.address = p.address);


-- MEMBERSHIP, FROM ON-LINK EVIDENCE AND FROM NOTHING ELSE, in order of strength: a lease naming the
-- address on an interface; a client identity at the address on an interface; an interface network
-- containing the address, an operator network before a detected one and the longest prefix within
-- each, a tie to the lowest interface id; the most recent sighting, inbound naming its source and
-- outbound its destination. Evidence on an upstream interface is not evidence, and a link-local
-- address is evidence only on an interface whose link_local_evidence rule is set, and is never placed
-- by a network.
-- statement: place_evidence
UPDATE plugin_clients.placement AS p
SET interface_id = e.interface_id,
    evidence_kind = e.kind
FROM (SELECT q.address,
             coalesce(q.lease_interface, q.identity_interface, q.network_interface, q.sighting_interface) AS interface_id,
             CASE
                 WHEN q.lease_interface IS NOT NULL THEN 'lease'
                 WHEN q.identity_interface IS NOT NULL THEN 'identity'
                 WHEN q.network_interface IS NOT NULL THEN 'network'
                 WHEN q.sighting_interface IS NOT NULL THEN 'sighting'
                 ELSE 'none'
             END AS kind
      FROM (SELECT p2.address,
                   (SELECT l.interface_id
                    FROM plugin_leases.dhcp_lease AS l
                    JOIN plugin_installation.interface AS i ON i.id = l.interface_id
                    WHERE l.address = p2.address
                      AND NOT i.is_upstream
                      AND (NOT p2.link_local OR i.link_local_evidence)
                    ORDER BY l.observed_at DESC, l.interface_id
                    LIMIT 1) AS lease_interface,
                   (SELECT c.interface_id
                    FROM plugin_installation.client AS c
                    JOIN plugin_installation.interface AS i ON i.id = c.interface_id
                    WHERE c.last_address = p2.address
                      AND c.identity_kind IN ('dhcp_client_id', 'mac')
                      AND NOT i.is_upstream
                      AND (NOT p2.link_local OR i.link_local_evidence)
                    ORDER BY c.last_seen_at DESC, c.id
                    LIMIT 1) AS identity_interface,
                   (CASE WHEN p2.link_local OR p2.address_inet IS NULL THEN NULL
                         ELSE (SELECT n.interface_id
                               FROM plugin_installation.interface_network AS n
                               JOIN plugin_installation.interface AS i ON i.id = n.interface_id
                               WHERE NOT i.is_upstream
                                 AND n.removed_at IS NULL
                                 AND (n.origin = 'operator'
                                      OR n.last_detected_at = (SELECT max(m.last_detected_at)
                                                               FROM plugin_installation.interface_network AS m
                                                               WHERE m.interface_id = n.interface_id))
                                 AND family(p2.address_inet) = n.address_family
                                 AND p2.address_inet << CAST(n.network_address || '/' || n.prefix_length AS inet)
                               ORDER BY (n.origin = 'operator') DESC, n.prefix_length DESC, n.interface_id
                               LIMIT 1) END) AS network_interface,
                   (SELECT s.interface_id
                    FROM ((SELECT i.id AS interface_id, f.observed_at AS seen_at
                           FROM plugin_pf.flow AS f
                           CROSS JOIN LATERAL (SELECT j.id
                                               FROM plugin_installation.interface AS j
                                               WHERE j.device = f.interface_device
                                                 AND NOT j.is_upstream
                                                 AND (NOT p2.link_local OR j.link_local_evidence)
                                               OFFSET 0) AS i
                           WHERE f.src_address = p2.address AND f.direction = 'in'
                           ORDER BY f.observed_at DESC, i.id
                           LIMIT 1)
                          UNION ALL
                          (SELECT i.id, f.observed_at
                           FROM plugin_pf.flow AS f
                           CROSS JOIN LATERAL (SELECT j.id
                                               FROM plugin_installation.interface AS j
                                               WHERE j.device = f.interface_device
                                                 AND NOT j.is_upstream
                                                 AND (NOT p2.link_local OR j.link_local_evidence)
                                               OFFSET 0) AS i
                           WHERE f.dst_address = p2.address AND f.direction = 'out'
                           ORDER BY f.observed_at DESC, i.id
                           LIMIT 1)) AS s
                    ORDER BY s.seen_at DESC, s.interface_id
                    LIMIT 1) AS sighting_interface
            FROM plugin_clients.placement AS p2) AS q) AS e
WHERE e.address = p.address;


-- THIS FIREWALL COMES FIRST: an address the firewall held at some instant, or delivered to itself at
-- every instant, has its rows decided one by one below, as of their own instants. The strongest
-- identity at an inside address: a lease's client identifier, then a MAC; a client behind an upstream
-- interface is never one.
-- statement: place_identities
UPDATE plugin_clients.placement AS p
SET firewall = p.loopback OR p.holdings IS NOT NULL,
    identity_client_id = CASE WHEN p.interface_id IS NULL THEN NULL ELSE
        (SELECT c.id
         FROM plugin_installation.client AS c
         WHERE c.last_address = p.address
           AND c.identity_kind IN ('dhcp_client_id', 'mac')
           AND (c.interface_id IS NULL
                OR c.interface_id NOT IN (SELECT id FROM plugin_installation.interface WHERE is_upstream))
         ORDER BY CASE c.identity_kind WHEN 'dhcp_client_id' THEN 0 ELSE 1 END, c.last_seen_at DESC, c.id DESC
         LIMIT 1) END;


-- THE FINGERPRINT is every input of the outcome: which evidence placed the address, on which interface,
-- the strongest identity there, and when this firewall held it, with the margin those holdings are
-- read with. An address is placed in full when it is unchanged from its record, or never recorded;
-- otherwise only its unplaced rows are placed.
-- statement: place_fingerprints
UPDATE plugin_clients.placement AS p
SET evidence = p.evidence_kind || '|' || coalesce(CAST(p.interface_id AS TEXT), '-') || '|'
               || coalesce(CAST(p.identity_client_id AS TEXT), '-')
               || CASE WHEN p.firewall
                       THEN '|this_firewall:' || CASE WHEN p.loopback THEN 1 ELSE 0 END || ':' || coalesce(p.holdings, '')
                            || ':' || CAST(:held_margin AS BIGINT)
                       ELSE '' END,
    placed_in_full = (SELECT r.evidence FROM plugin_clients.address_classification AS r WHERE r.address = p.address)
                     IS DISTINCT FROM
                     (p.evidence_kind || '|' || coalesce(CAST(p.interface_id AS TEXT), '-') || '|'
                      || coalesce(CAST(p.identity_client_id AS TEXT), '-')
                      || CASE WHEN p.firewall
                              THEN '|this_firewall:' || CASE WHEN p.loopback THEN 1 ELSE 0 END || ':' || coalesce(p.holdings, '')
                                   || ':' || CAST(:held_margin AS BIGINT)
                              ELSE '' END);


-- THE ADDRESS-LEVEL CLIENT of an inside address with no strong identity: the one seen there within the
-- idle window, or a new one when a flow end at the address has no client at all and is not this
-- firewall at the record's instant -- a client nobody refers to would only inflate the count of
-- machines known behind the interface.
-- statement: place_address_clients
UPDATE plugin_clients.placement AS p
SET address_client_id = a.existing,
    needs_mint = a.existing IS NULL AND a.needed
FROM (SELECT p2.address,
             (SELECT c.id
              FROM plugin_installation.client AS c
              WHERE c.last_address = p2.address
                AND c.identity_kind = 'address_in_interface'
                AND c.interface_id = p2.interface_id
                AND c.last_seen_at >= CAST(:now AS BIGINT) - CAST(:setting_address_idle_window_seconds AS BIGINT)
              ORDER BY c.last_seen_at DESC, c.id DESC
              LIMIT 1) AS existing,
             EXISTS (SELECT 1
                     FROM plugin_pf.flow AS f
                     WHERE f.src_address = p2.address
                       AND f.src_client_id IS NULL
                       AND NOT p2.loopback
                       AND NOT EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                                       WHERE h.address = f.src_address
                                         AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                                         AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at))
                     UNION ALL
                     SELECT 1
                     FROM plugin_pf.flow AS f
                     WHERE f.dst_address = p2.address
                       AND f.dst_client_id IS NULL
                       AND NOT p2.loopback
                       AND NOT EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                                       WHERE h.address = f.dst_address
                                         AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                                         AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at))) AS needed
      FROM plugin_clients.placement AS p2
      WHERE p2.interface_id IS NOT NULL AND p2.identity_client_id IS NULL) AS a
WHERE a.address = p.address;


-- Minted in address order, so the ids are the ones a one-at-a-time pass would have given.
-- statement: mint_clients
WITH minted AS (
    INSERT INTO plugin_installation.client (identity_kind, identity_key, interface_id, last_address, first_seen_at, last_seen_at)
    SELECT 'address_in_interface',
           p.interface_id || '|' || p.address || '|' || ((CAST(:now AS BIGINT) / 86400) * 86400),
           p.interface_id, p.address, CAST(:now AS BIGINT), CAST(:now AS BIGINT)
    FROM plugin_clients.placement AS p
    WHERE p.needs_mint
    ORDER BY p.address
    ON CONFLICT (identity_kind, identity_key) DO UPDATE SET
        interface_id = coalesce(excluded.interface_id, client.interface_id),
        last_address = coalesce(excluded.last_address, client.last_address),
        last_seen_at = excluded.last_seen_at
    RETURNING id, last_address)
UPDATE plugin_clients.placement AS p
SET address_client_id = m.id
FROM minted AS m
WHERE m.last_address = p.address;


-- THE ENDS. An end whose address this firewall held at the row's instant, or that is delivered to it,
-- is this firewall: it records the interface the address belonged to (the most recently seen holding,
-- then the lowest interface id; none for a loopback address) and no client. Every other end keeps the
-- client it had: an outside end keeps none; a strong identity already on the row is kept; otherwise the
-- strongest identity at the address replaces whatever is there; otherwise an address-level client
-- already on the row is kept, and an empty end takes the address-level client of the address. In full,
-- every row naming the address; otherwise only the rows with an unplaced end. A row is written only
-- when a value changes, and a flow written is stamped changed_at for the refresh.
-- statement: place_flow_sources
UPDATE plugin_pf.flow AS flow
SET src_interface_id = n.interface_id,
    src_client_id = n.client_id,
    src_is_this_firewall = n.this_firewall,
    traffic_scope = CASE
        WHEN n.this_firewall OR flow.dst_is_this_firewall THEN 'this_firewall'
        WHEN n.interface_id IS NOT NULL AND flow.dst_interface_id IS NOT NULL THEN 'east_west'
        ELSE 'north_south'
    END,
    changed_at = CAST(:now AS BIGINT)
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE p.placed_interface_id END AS interface_id,
             CASE
                 WHEN p.this_firewall OR p.placed_interface_id IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM plugin_installation.client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN p.identity_client_id IS NOT NULL THEN p.identity_client_id
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE p.address_client_id
             END AS client_id
      FROM (SELECT f.id,
                   f.src_client_id AS client_id,
                   pl.interface_id AS placed_interface_id,
                   pl.identity_client_id,
                   pl.address_client_id,
                   (pl.loopback
                    OR EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                               WHERE h.address = f.src_address
                                 AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                                 AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at)))
                       AS this_firewall,
                   (SELECT h.interface_id FROM plugin_installation.this_firewall_address AS h
                    WHERE h.address = f.src_address
                      AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                      AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM plugin_pf.flow AS f
            JOIN plugin_clients.placement AS pl ON pl.address = f.src_address
            WHERE pl.placed_in_full OR f.src_interface_id IS NULL) AS p) AS n
WHERE flow.id = n.id
  AND (flow.src_interface_id IS DISTINCT FROM n.interface_id
       OR flow.src_client_id IS DISTINCT FROM n.client_id
       OR flow.src_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- statement: place_flow_destinations
UPDATE plugin_pf.flow AS flow
SET dst_interface_id = n.interface_id,
    dst_client_id = n.client_id,
    dst_is_this_firewall = n.this_firewall,
    traffic_scope = CASE
        WHEN flow.src_is_this_firewall OR n.this_firewall THEN 'this_firewall'
        WHEN flow.src_interface_id IS NOT NULL AND n.interface_id IS NOT NULL THEN 'east_west'
        ELSE 'north_south'
    END,
    changed_at = CAST(:now AS BIGINT)
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE p.placed_interface_id END AS interface_id,
             CASE
                 WHEN p.this_firewall OR p.placed_interface_id IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM plugin_installation.client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN p.identity_client_id IS NOT NULL THEN p.identity_client_id
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE p.address_client_id
             END AS client_id
      FROM (SELECT f.id,
                   f.dst_client_id AS client_id,
                   pl.interface_id AS placed_interface_id,
                   pl.identity_client_id,
                   pl.address_client_id,
                   (pl.loopback
                    OR EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                               WHERE h.address = f.dst_address
                                 AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                                 AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at)))
                       AS this_firewall,
                   (SELECT h.interface_id FROM plugin_installation.this_firewall_address AS h
                    WHERE h.address = f.dst_address
                      AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= f.observed_at
                      AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= f.observed_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM plugin_pf.flow AS f
            JOIN plugin_clients.placement AS pl ON pl.address = f.dst_address
            WHERE pl.placed_in_full OR f.dst_interface_id IS NULL) AS p) AS n
WHERE flow.id = n.id
  AND (flow.dst_interface_id IS DISTINCT FROM n.interface_id
       OR flow.dst_client_id IS DISTINCT FROM n.client_id
       OR flow.dst_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- statement: place_event_sources
UPDATE plugin_suricata.security_event AS security_event
SET src_interface_id = n.interface_id,
    src_client_id = n.client_id,
    src_is_this_firewall = n.this_firewall
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE p.placed_interface_id END AS interface_id,
             CASE
                 WHEN p.this_firewall OR p.placed_interface_id IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM plugin_installation.client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN p.identity_client_id IS NOT NULL THEN p.identity_client_id
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE p.address_client_id
             END AS client_id
      FROM (SELECT e.id,
                   e.src_client_id AS client_id,
                   pl.interface_id AS placed_interface_id,
                   pl.identity_client_id,
                   pl.address_client_id,
                   (pl.loopback
                    OR EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                               WHERE h.address = e.src_address
                                 AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= e.occurred_at
                                 AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= e.occurred_at)))
                       AS this_firewall,
                   (SELECT h.interface_id FROM plugin_installation.this_firewall_address AS h
                    WHERE h.address = e.src_address
                      AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= e.occurred_at
                      AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= e.occurred_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM plugin_suricata.security_event AS e
            JOIN plugin_clients.placement AS pl ON pl.address = e.src_address
            WHERE pl.placed_in_full OR e.src_interface_id IS NULL) AS p) AS n
WHERE security_event.id = n.id
  AND (security_event.src_interface_id IS DISTINCT FROM n.interface_id
       OR security_event.src_client_id IS DISTINCT FROM n.client_id
       OR security_event.src_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- statement: place_event_destinations
UPDATE plugin_suricata.security_event AS security_event
SET dst_interface_id = n.interface_id,
    dst_client_id = n.client_id,
    dst_is_this_firewall = n.this_firewall
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE p.placed_interface_id END AS interface_id,
             CASE
                 WHEN p.this_firewall OR p.placed_interface_id IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM plugin_installation.client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN p.identity_client_id IS NOT NULL THEN p.identity_client_id
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE p.address_client_id
             END AS client_id
      FROM (SELECT e.id,
                   e.dst_client_id AS client_id,
                   pl.interface_id AS placed_interface_id,
                   pl.identity_client_id,
                   pl.address_client_id,
                   (pl.loopback
                    OR EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                               WHERE h.address = e.dst_address
                                 AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= e.occurred_at
                                 AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= e.occurred_at)))
                       AS this_firewall,
                   (SELECT h.interface_id FROM plugin_installation.this_firewall_address AS h
                    WHERE h.address = e.dst_address
                      AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= e.occurred_at
                      AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= e.occurred_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM plugin_suricata.security_event AS e
            JOIN plugin_clients.placement AS pl ON pl.address = e.dst_address
            WHERE pl.placed_in_full OR e.dst_interface_id IS NULL) AS p) AS n
WHERE security_event.id = n.id
  AND (security_event.dst_interface_id IS DISTINCT FROM n.interface_id
       OR security_event.dst_client_id IS DISTINCT FROM n.client_id
       OR security_event.dst_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- A lookup is never given a client of its own: an address with no client stays with none, and the
-- lookup still carries its interface. A lookup whose querier is this firewall carries the interface the
-- address belonged to and no client.
-- statement: place_lookups
UPDATE plugin_unbound.dns_resolution AS dns_resolution
SET interface_id = n.interface_id,
    interface_lookup_state = CASE WHEN n.interface_id IS NULL THEN 'not_found' ELSE 'resolved' END,
    client_id = n.client_id,
    client_is_this_firewall = n.this_firewall
FROM (SELECT p.id,
             p.this_firewall,
             CASE WHEN p.this_firewall THEN p.firewall_interface_id ELSE p.placed_interface_id END AS interface_id,
             CASE
                 WHEN p.this_firewall OR p.placed_interface_id IS NULL THEN NULL
                 WHEN p.client_id IS NOT NULL
                      AND (SELECT k.identity_kind FROM plugin_installation.client AS k WHERE k.id = p.client_id)
                          <> 'address_in_interface' THEN p.client_id
                 WHEN p.identity_client_id IS NOT NULL THEN p.identity_client_id
                 WHEN p.client_id IS NOT NULL THEN p.client_id
                 ELSE p.address_client_id
             END AS client_id
      FROM (SELECT r.id,
                   r.client_id,
                   pl.interface_id AS placed_interface_id,
                   pl.identity_client_id,
                   pl.address_client_id,
                   (pl.loopback
                    OR EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
                               WHERE h.address = r.client_address
                                 AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= r.looked_up_at
                                 AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= r.looked_up_at)))
                       AS this_firewall,
                   (SELECT h.interface_id FROM plugin_installation.this_firewall_address AS h
                    WHERE h.address = r.client_address
                      AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= r.looked_up_at
                      AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= r.looked_up_at)
                    ORDER BY h.last_seen_at DESC, h.interface_id
                    LIMIT 1) AS firewall_interface_id
            FROM plugin_unbound.dns_resolution AS r
            JOIN plugin_clients.placement AS pl ON pl.address = r.client_address
            WHERE pl.placed_in_full OR r.interface_lookup_state = 'pending') AS p) AS n
WHERE dns_resolution.id = n.id
  AND (dns_resolution.interface_id IS DISTINCT FROM n.interface_id
       OR dns_resolution.interface_lookup_state IS DISTINCT FROM (CASE WHEN n.interface_id IS NULL
                                                                       THEN 'not_found' ELSE 'resolved' END)
       OR dns_resolution.client_id IS DISTINCT FROM n.client_id
       OR dns_resolution.client_is_this_firewall IS DISTINCT FROM n.this_firewall);


-- The purged part follows its flows: when the strongest identity at an address replaces the
-- address-level client its flows named, the purged part naming that client is re-pointed too.
-- statement: repoint_purged_local
UPDATE opnview.purged_flow_hour AS purged
SET local_client_id = p.identity_client_id
FROM plugin_clients.placement AS p
WHERE p.placed_in_full
  AND p.identity_client_id IS NOT NULL
  AND purged.local_client_id IN (SELECT c.id FROM plugin_installation.client AS c
                                 WHERE c.last_address = p.address AND c.identity_kind = 'address_in_interface');


-- statement: repoint_purged_source
UPDATE opnview.purged_flow_hour AS purged
SET src_client_id = p.identity_client_id
FROM plugin_clients.placement AS p
WHERE p.placed_in_full
  AND p.identity_client_id IS NOT NULL
  AND purged.src_client_id IN (SELECT c.id FROM plugin_installation.client AS c
                               WHERE c.last_address = p.address AND c.identity_kind = 'address_in_interface');


-- statement: record_classifications
INSERT INTO plugin_clients.address_classification (address, evidence, classified_at)
SELECT p.address, p.evidence, CAST(:now AS BIGINT)
FROM plugin_clients.placement AS p
WHERE p.placed_in_full AND p.evidence IS NOT NULL
ON CONFLICT (address) DO UPDATE SET
    evidence = excluded.evidence,
    classified_at = excluded.classified_at
WHERE plugin_clients.address_classification.evidence IS DISTINCT FROM excluded.evidence;


-- A client that nothing refers to any more, that nobody attributed to a person, and that is either
-- address-level -- every flow it named moved to a better identity, or turned out to be outside -- or
-- sits behind an upstream interface: at the addresses placed in full.
-- statement: purge_unreferenced_at_addresses
DELETE FROM plugin_installation.client AS client
WHERE client.last_address IN (SELECT p.address FROM plugin_clients.placement AS p WHERE p.placed_in_full)
  AND client.owner_id IS NULL
  AND (client.identity_kind = 'address_in_interface'
       OR client.interface_id IN (SELECT id FROM plugin_installation.interface WHERE is_upstream))
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_leases.dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_unbound.dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.src_client_id = client.id);


-- And everywhere, once every address was placed again because the on-link evidence changed.
-- statement: purge_unreferenced_everywhere
DELETE FROM plugin_installation.client AS client
WHERE (SELECT changed FROM plugin_clients.onlink)
  AND client.owner_id IS NULL
  AND (client.identity_kind = 'address_in_interface'
       OR client.interface_id IN (SELECT id FROM plugin_installation.interface WHERE is_upstream))
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_leases.dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_unbound.dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.src_client_id = client.id);


-- The clients an earlier build minted at this firewall's own addresses from the neighbour tables: one
-- whose last address this firewall held when it was last seen there goes once nothing names it and
-- nobody assigned it to a person.
-- statement: purge_this_firewall_clients
DELETE FROM plugin_installation.client AS client
WHERE client.owner_id IS NULL
  AND EXISTS (SELECT 1 FROM plugin_installation.this_firewall_address AS h
              WHERE h.address = client.last_address
                AND h.first_seen_at - CAST(:held_margin AS BIGINT) <= client.last_seen_at
                AND (h.is_current = 1 OR h.last_seen_at + CAST(:held_margin AS BIGINT) >= client.last_seen_at))
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_pf.flow AS f WHERE f.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.src_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_suricata.security_event AS e WHERE e.dst_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_leases.dhcp_lease AS l WHERE l.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM plugin_unbound.dns_resolution AS r WHERE r.client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.local_client_id = client.id)
  AND NOT EXISTS (SELECT 1 FROM opnview.purged_flow_hour AS h WHERE h.src_client_id = client.id);


-- TWO LEGS OF ONE CONNECTION. pf logs the packet that creates a state on every interface a rule with
-- `log` creates one on, so a connection crossing the firewall is logged `in` on the interface it
-- arrived on and `out` on the one it left by. An `out` record is the SECOND LEG of an `in` record on
-- another interface when both carry the same protocol, IP version, destination address and
-- destination port, every logged field verified to survive the crossing unchanged (ip_id, tcp_seq),
-- their instants lie within the window, the `in` record passed, and each is the other's only
-- candidate. Under outbound NAT the second leg's source is this firewall; without it the sources and
-- their ports are equal. An unpaired `out` record on an upstream interface sourced by this firewall is
-- 'not_paired': opnview never claims it is the firewall's own traffic rather than a client's NATed
-- connection whose first record it cannot see.
--
-- The span decided again is every `out` record within the window of a record stored since the last
-- run or placed by this one, and the decision reads the records' logged fields and never their pairing
-- columns, so it is the same whichever run takes it. A partner a record no longer has is cleared unless
-- another record of the span now pairs with it. One statement, one snapshot, one write per changed row.
-- statement: pair_records
WITH bounds AS (
    SELECT min(f.observed_at) - CAST(:leg_pairing_window AS BIGINT) AS low,
           max(f.observed_at) + CAST(:leg_pairing_window AS BIGINT) AS high
    FROM plugin_pf.flow AS f
    WHERE CAST(:since AS BIGINT) IS NULL
       OR f.ingested_at >= CAST(:since AS BIGINT)
       OR f.changed_at = CAST(:now AS BIGINT)),
outs AS (
    SELECT o.id, o.observed_at, o.interface_device, o.src_address, o.src_port, o.dst_address, o.dst_port,
           o.protocol, o.ip_version, o.ip_id, o.tcp_seq, o.src_is_this_firewall, o.pair_outcome, o.paired_flow_id
    FROM plugin_pf.flow AS o, bounds AS b
    WHERE o.direction = 'out'
      AND o.observed_at >= b.low
      AND o.observed_at <= b.high),
candidates AS (
    SELECT o.id AS out_id, i.id AS in_id
    FROM outs AS o
    JOIN plugin_pf.flow AS i
      ON i.dst_address = o.dst_address
     AND i.observed_at >= o.observed_at - CAST(:leg_pairing_window AS BIGINT)
     AND i.observed_at <= o.observed_at + CAST(:leg_pairing_window AS BIGINT)
     AND i.direction = 'in'
     AND i.action = 'pass'
     AND i.interface_device <> o.interface_device
     AND i.protocol = o.protocol
     AND i.ip_version = o.ip_version
     AND i.dst_port IS NOT DISTINCT FROM o.dst_port
     AND i.ip_id IS NOT DISTINCT FROM o.ip_id
     AND i.tcp_seq IS NOT DISTINCT FROM o.tcp_seq
     AND NOT i.src_is_this_firewall
     AND (o.src_is_this_firewall OR (i.src_address = o.src_address AND i.src_port IS NOT DISTINCT FROM o.src_port))),
single AS (
    SELECT c.out_id, min(c.in_id) AS in_id
    FROM candidates AS c
    GROUP BY c.out_id
    HAVING count(*) = 1),
desired AS (
    SELECT s.out_id, s.in_id
    FROM single AS s
    JOIN plugin_pf.flow AS i ON i.id = s.in_id
    WHERE (SELECT count(*)
           FROM (SELECT 1
                 FROM plugin_pf.flow AS o2
                 WHERE o2.dst_address = i.dst_address
                   AND o2.observed_at >= i.observed_at - CAST(:leg_pairing_window AS BIGINT)
                   AND o2.observed_at <= i.observed_at + CAST(:leg_pairing_window AS BIGINT)
                   AND o2.direction = 'out'
                   AND o2.interface_device <> i.interface_device
                   AND o2.protocol = i.protocol
                   AND o2.ip_version = i.ip_version
                   AND o2.dst_port IS NOT DISTINCT FROM i.dst_port
                   AND o2.ip_id IS NOT DISTINCT FROM i.ip_id
                   AND o2.tcp_seq IS NOT DISTINCT FROM i.tcp_seq
                   AND (o2.src_is_this_firewall OR (o2.src_address = i.src_address AND o2.src_port IS NOT DISTINCT FROM i.src_port))
                 LIMIT 2) AS back) = 1),
targets AS (
    SELECT o.id,
           CASE
               WHEN d.in_id IS NOT NULL THEN 'second_leg'
               WHEN o.src_is_this_firewall
                    AND EXISTS (SELECT 1 FROM plugin_installation.interface AS u
                                WHERE u.device = o.interface_device AND u.is_upstream) THEN 'not_paired'
           END AS outcome,
           d.in_id AS partner
    FROM outs AS o
    LEFT JOIN desired AS d ON d.out_id = o.id
    UNION ALL
    SELECT d.in_id, 'first_leg', d.out_id
    FROM desired AS d
    UNION ALL
    SELECT o.paired_flow_id, NULL, NULL
    FROM outs AS o
    LEFT JOIN desired AS d ON d.out_id = o.id
    WHERE o.pair_outcome = 'second_leg'
      AND o.paired_flow_id IS NOT NULL
      AND d.in_id IS DISTINCT FROM o.paired_flow_id
      AND NOT EXISTS (SELECT 1 FROM desired AS x WHERE x.in_id = o.paired_flow_id))
UPDATE plugin_pf.flow AS flow
SET pair_outcome = t.outcome,
    paired_flow_id = t.partner,
    changed_at = CAST(:now AS BIGINT)
FROM targets AS t
WHERE flow.id = t.id
  AND (flow.pair_outcome IS DISTINCT FROM t.outcome OR flow.paired_flow_id IS DISTINCT FROM t.partner);
