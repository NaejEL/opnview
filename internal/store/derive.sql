-- opnview — the derivation statements: classification, site-name attribution
-- and the aggregate refresh.
--
-- One file, embedded into the binary by internal/store (statements.go) and read
-- by sql/schema-checks.sh, which records the EXPLAIN QUERY PLAN of every
-- statement below at both seed sizes and fails if one scans a growing table. So
-- the text that runs is the text that is checked, and there is no second copy.
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
--   :link_local     1 when the address being placed is an IPv6 link-local one
--   :full           1 for a full placement, 0 for an incremental one
--   :evidence       the evidence fingerprint an address was placed from
--   :hostname, :label, :at  a host name, its first label, and an instant
--   :resolution     a dns_resolution.client_resolution value
--
-- No address, CIDR, interface identifier, device name or description appears
-- below, and nothing classifies anything by what it is called.

-- ---------------------------------------------------------------------------
-- CLASSIFICATION. Membership of an address comes from on-link evidence only,
-- read in this order of strength by internal/store/classify.go: a lease, a
-- neighbour or lease identity, an interface network holding it, a sighting.
-- Evidence on an upstream interface is not evidence: such an address is outside.
--
-- An IPv6 link-local address is the same on every interface, so evidence for one
-- counts only on an interface whose operator rule says it does
-- (interface.link_local_evidence): :link_local is 1 for such an address, and
-- every statement below then reads only those interfaces.
-- ---------------------------------------------------------------------------

-- statement: membership_lease
SELECT l.interface_id
FROM dhcp_lease AS l
JOIN interface AS i ON i.id = l.interface_id
WHERE l.address = :address
  AND i.is_upstream = 0
  AND (:link_local = 0 OR i.link_local_evidence = 1)
ORDER BY l.observed_at DESC, l.interface_id
LIMIT 1;

-- statement: membership_identity
SELECT c.interface_id
FROM client AS c
JOIN interface AS i ON i.id = c.interface_id
WHERE c.last_address = :address
  AND c.identity_kind IN ('dhcp_client_id', 'mac')
  AND i.is_upstream = 0
  AND (:link_local = 0 OR i.link_local_evidence = 1)
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
WHERE i.is_upstream = 0
  AND n.removed_at IS NULL
  AND (n.origin = 'operator'
       OR n.last_detected_at = (SELECT max(m.last_detected_at) FROM interface_network AS m
                                WHERE m.interface_id = n.interface_id))
ORDER BY n.interface_id, n.network_address, n.prefix_length;

-- A sighting: a record logged INBOUND on an interface names its source as on that
-- interface's side, and one logged OUTBOUND names its destination. Only the most
-- recent sighting of each kind is read.
-- statement: membership_sighting_source
SELECT i.id, f.observed_at
FROM flow AS f
JOIN interface AS i ON i.device = f.interface_device
WHERE f.src_address = :address
  AND f.direction = 'in'
  AND i.is_upstream = 0
  AND (:link_local = 0 OR i.link_local_evidence = 1)
ORDER BY f.observed_at DESC, i.id
LIMIT 1;

-- statement: membership_sighting_destination
SELECT i.id, f.observed_at
FROM flow AS f
JOIN interface AS i ON i.device = f.interface_device
WHERE f.dst_address = :address
  AND f.direction = 'out'
  AND i.is_upstream = 0
  AND (:link_local = 0 OR i.link_local_evidence = 1)
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
       OR c.interface_id NOT IN (SELECT id FROM interface WHERE is_upstream = 1))
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

-- statement: unclassified_end_at_address
SELECT 1 FROM flow WHERE src_address = :address AND src_client_id IS NULL
UNION ALL
SELECT 1 FROM flow WHERE dst_address = :address AND dst_client_id IS NULL
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
WHERE address_classification.evidence IS NOT excluded.evidence;

-- The client an end keeps. An outside end keeps none. A strong identity already
-- on the row is kept; otherwise the strongest identity at the address replaces
-- whatever is there, which is how a lease naming an address seen only in flows
-- re-points those flows; otherwise an address-level client already on the row is
-- kept, and an empty end takes the address-level client of the address.
--
-- With :full = 0 -- the evidence for the address has not changed since it was last
-- placed -- only the rows with an unplaced end are read: an end the collector
-- stored has no interface yet, and a placed inside end always has one. Every
-- statement writes a row only when a value changes, so a pass that changes
-- nothing writes nothing.
-- statement: classify_flow_source
UPDATE flow
SET src_interface_id = :interface_id,
    src_client_id = CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN src_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = flow.src_client_id)
                 <> 'address_in_interface' THEN src_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN src_client_id IS NOT NULL THEN src_client_id
        ELSE :address_client_id
    END,
    traffic_scope = CASE WHEN :interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                         THEN 'east_west' ELSE 'north_south' END
WHERE src_address = :address
  AND (:full = 1 OR src_interface_id IS NULL)
  AND (src_interface_id IS NOT :interface_id
       OR src_client_id IS NOT (CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN src_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = flow.src_client_id)
                 <> 'address_in_interface' THEN src_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN src_client_id IS NOT NULL THEN src_client_id
        ELSE :address_client_id
    END))
RETURNING observed_at;

-- statement: classify_flow_destination
UPDATE flow
SET dst_interface_id = :interface_id,
    dst_client_id = CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN dst_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = flow.dst_client_id)
                 <> 'address_in_interface' THEN dst_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN dst_client_id IS NOT NULL THEN dst_client_id
        ELSE :address_client_id
    END,
    traffic_scope = CASE WHEN src_interface_id IS NOT NULL AND :interface_id IS NOT NULL
                         THEN 'east_west' ELSE 'north_south' END
WHERE dst_address = :address
  AND (:full = 1 OR dst_interface_id IS NULL)
  AND (dst_interface_id IS NOT :interface_id
       OR dst_client_id IS NOT (CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN dst_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = flow.dst_client_id)
                 <> 'address_in_interface' THEN dst_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN dst_client_id IS NOT NULL THEN dst_client_id
        ELSE :address_client_id
    END))
RETURNING observed_at;

-- statement: classify_event_source
UPDATE security_event
SET src_interface_id = :interface_id,
    src_client_id = CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN src_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = security_event.src_client_id)
                 <> 'address_in_interface' THEN src_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN src_client_id IS NOT NULL THEN src_client_id
        ELSE :address_client_id
    END
WHERE src_address = :address
  AND (:full = 1 OR src_interface_id IS NULL)
  AND (src_interface_id IS NOT :interface_id
       OR src_client_id IS NOT (CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN src_client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = security_event.src_client_id)
                 <> 'address_in_interface' THEN src_client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN src_client_id IS NOT NULL THEN src_client_id
        ELSE :address_client_id
    END));

-- A lookup is never given a client of its own: an address with no client stays
-- with none, and the lookup still carries its interface.
-- statement: classify_lookup
UPDATE dns_resolution
SET interface_id = :interface_id,
    interface_lookup_state = CASE WHEN :interface_id IS NULL THEN 'not_found' ELSE 'resolved' END,
    client_id = CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = dns_resolution.client_id)
                 <> 'address_in_interface' THEN client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN client_id IS NOT NULL THEN client_id
        ELSE :address_client_id
    END
WHERE client_address = :address
  AND (:full = 1 OR interface_lookup_state = 'pending')
  AND (interface_id IS NOT :interface_id
       OR interface_lookup_state IS NOT (CASE WHEN :interface_id IS NULL THEN 'not_found'
                                              ELSE 'resolved' END)
       OR client_id IS NOT (CASE
        WHEN :interface_id IS NULL THEN NULL
        WHEN client_id IS NOT NULL
             AND (SELECT k.identity_kind FROM client AS k WHERE k.id = dns_resolution.client_id)
                 <> 'address_in_interface' THEN client_id
        WHEN :identity_client_id IS NOT NULL THEN :identity_client_id
        WHEN client_id IS NOT NULL THEN client_id
        ELSE :address_client_id
    END))
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
       OR interface_id IN (SELECT id FROM interface WHERE is_upstream = 1))
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
WHERE interface_id IN (SELECT id FROM interface WHERE is_upstream = 1)
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
-- internal/store/purge.sql computes it. The purge places them first, so its purged
-- part never freezes a flow as it was stored -- with neither end placed, and therefore
-- north-south -- when the evidence held can place it. An outside end is unplaced for
-- ever and is returned too; placing it again changes nothing.
-- statement: purge_due_addresses
SELECT src_address
FROM flow
WHERE observed_at < (SELECT :now - CAST(value AS INTEGER)
                     FROM setting
                     WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0)
  AND src_interface_id IS NULL
UNION
SELECT dst_address
FROM flow
WHERE observed_at < (SELECT :now - CAST(value AS INTEGER)
                     FROM setting
                     WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0)
  AND dst_interface_id IS NULL;

-- ---------------------------------------------------------------------------
-- SITE-NAME ATTRIBUTION. The resolver log carries no answer address, so an
-- attribution is a lookup and a flow by the same client, close in time. A flow
-- is eligible when its destination is outside.
-- ---------------------------------------------------------------------------

-- statement: attribution_candidates
SELECT id, observed_at, src_client_id, src_address
FROM flow
WHERE observed_at >= :from
  AND observed_at <= :to
  AND dst_interface_id IS NULL
ORDER BY id;

-- "The same client": the same client_id where both rows carry one, the same
-- address otherwise. Eligible: a passed lookup answered by recursion, from
-- cache, or from local data such as a host override.
-- statement: attribution_lookups
SELECT r.id, r.domain, r.looked_up_at
FROM dns_resolution AS r
WHERE r.client_id = :client_id
  AND r.looked_up_at >= :start
  AND r.looked_up_at <= :end
  AND r.action = 'pass'
  AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND r.client_resolution IN ('logged_address', 'lease_hostname')
UNION ALL
SELECT r.id, r.domain, r.looked_up_at
FROM dns_resolution AS r
WHERE r.client_address = :address
  AND r.looked_up_at >= :start
  AND r.looked_up_at <= :end
  AND r.action = 'pass'
  AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND r.client_resolution IN ('logged_address', 'lease_hostname')
  AND (r.client_id IS NULL OR :client_id IS NULL);

-- ---------------------------------------------------------------------------
-- A HOST NAME LOGGED AS A LOOKUP'S CLIENT. The resolver's query report gives the
-- name a reverse lookup of the querying address returned instead of the address,
-- when there was one. It is resolved through the DHCP leases valid at the
-- lookup's instant: a lease under that name, or under the name's first label --
-- a reverse lookup answers with the fully qualified name, and a lease carries the
-- host's own label -- that had started, if its start is known, and had not
-- expired, if its expiry is known. The caller takes the address only when there is
-- exactly one.
-- ---------------------------------------------------------------------------

-- The lookups whose logged host name resolved to no single address when they were
-- read, ingested since :since -- the start of the previous lease pass. A lookup read
-- before the lease that names its host is resolved again by the next lease pass, as
-- of its own instant; one an earlier pass already tried is not tried again, so the
-- work of a pass is the lookups read since the last one and does not grow with the
-- unresolved lookups that accumulate.
-- statement: unresolved_hostname_lookups
SELECT id, client_hostname, looked_up_at
FROM dns_resolution
WHERE client_resolution IN ('ambiguous_hostname', 'unknown_hostname')
  AND ingested_at >= :since
ORDER BY id;

-- A new resolution of one such lookup. It is placed again from the address it now
-- names, so its interface and client are cleared and it is pending.
-- statement: resolve_hostname_lookup
UPDATE dns_resolution
SET client_address = :address,
    client_resolution = :resolution,
    client_id = NULL,
    interface_id = NULL,
    interface_lookup_state = 'pending'
WHERE id = :lookup_id
  AND client_resolution IS NOT :resolution;

-- statement: lease_addresses_for_hostname
SELECT DISTINCT l.address
FROM dhcp_lease AS l
WHERE l.hostname COLLATE NOCASE IN (:hostname, :label)
  AND (l.starts_at IS NULL OR l.starts_at <= :at)
  AND (l.expires_at IS NULL OR l.expires_at >= :at)
ORDER BY l.address
LIMIT 2;

-- statement: attribution_upsert
INSERT INTO domain_attribution (flow_id, dns_resolution_id, site_name,
                                correlation_delay_seconds, attributed_at)
VALUES (:flow_id, :lookup_id, :site_name, :delay, :now)
ON CONFLICT (flow_id) DO UPDATE SET
    dns_resolution_id = excluded.dns_resolution_id,
    site_name = excluded.site_name,
    correlation_delay_seconds = excluded.correlation_delay_seconds,
    attributed_at = excluded.attributed_at
WHERE domain_attribution.dns_resolution_id IS NOT excluded.dns_resolution_id
   OR domain_attribution.site_name IS NOT excluded.site_name
   OR domain_attribution.correlation_delay_seconds IS NOT excluded.correlation_delay_seconds;

-- statement: attribution_delete
DELETE FROM domain_attribution WHERE flow_id = :flow_id;

-- ---------------------------------------------------------------------------
-- THE REFRESH. A slot is rewritten whole: its rows are deleted and recomputed,
-- for every family at once.
--
-- HOW EACH PERIOD IS COMPUTED, and why no slot is ever wrong under a short
-- retention:
--   * an HOUR is computed from the flows observed inside it PLUS its purged part,
--     the purged_flow_hour rows the retention purge wrote for the flows it
--     removed from that hour. So an hour holding the horizon -- the current hour
--     under a retention shorter than an hour -- keeps what it held for the purged
--     flows, and still takes in a flow ingested late into it and a
--     reclassification of a flow still present;
--   * a DAY is composed from its 24 hours;
--   * a WEEK and a MONTH are rolled up from their days, which tile them exactly.
-- The client family of every slot is regrouped from the peer family of the same
-- slot, and the owner family from the client family of the same slot, so the
-- two distinct figures -- distinct_peers and client_count -- are counted, never
-- summed, in every period.
-- ---------------------------------------------------------------------------

-- The hours a refresh considers: every hour holding a flow ingested since the
-- previous refresh, and every hour a purge has written a purged part for since then.
-- The second half is the belt and braces of the purge's ordering (docs/data-model.md,
-- "Purge"): the purge moves the flows of an hour into its purged part, and once they
-- have left `flow` nothing else would select that hour, so a purge that ever ran
-- before a refresh had read those flows would leave the hour without their bytes.
-- statement: dirty_hours
SELECT (observed_at / 3600) * 3600
FROM flow
WHERE ingested_at >= :since
UNION
SELECT hour_start_at
FROM purged_flow_hour
WHERE purged_at >= :since;

-- The newest instant at which anything inside a slot's window changed: a flow ingested
-- into it, or a purge writing a purged part for one of its hours. A slot whose
-- computed_at is not later than that instant is stale (internal/store/aggregate.go).
-- statement: slot_newest_ingested
SELECT max(newest)
FROM (SELECT max(ingested_at) AS newest
      FROM flow
      WHERE observed_at >= :slot_start
        AND observed_at < :slot_end
      UNION ALL
      SELECT max(purged_at)
      FROM purged_flow_hour
      WHERE hour_start_at >= :slot_start
        AND hour_start_at < :slot_end);

-- statement: slot_computed_at
SELECT max(computed_at)
FROM volume_aggregate_@period@
WHERE period_start_at = :slot_start;

-- statement: delete_volume
DELETE FROM volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- statement: delete_rule
DELETE FROM rule_volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- statement: delete_domain
DELETE FROM domain_volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- statement: delete_peer
DELETE FROM peer_volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- statement: delete_client
DELETE FROM client_volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- statement: delete_owner
DELETE FROM owner_volume_aggregate_@period@ WHERE period_start_at = :slot_start;

-- ---------------------------------------------------------------------------
-- The hour: the flows still present, plus the purged part. Each statement reads
-- one UNION ALL of the two, with the same columns -- bytes and connections are
-- one flow's packet_bytes and 1, or a purged row's sums -- and groups it on the
-- family's key.
-- ---------------------------------------------------------------------------

-- statement: insert_volume
INSERT INTO volume_aggregate_1h (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, computed_at)
SELECT
    :slot_start,
    :slot_end,
    u.src_interface_id,
    u.dst_interface_id,
    u.peer_address,
    CASE WHEN u.src_interface_id IS NOT NULL AND u.dst_interface_id IS NOT NULL
         THEN 'east_west' ELSE 'north_south' END,
    u.traffic_direction,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_direction,
           f.action, f.packet_bytes AS bytes, 1 AS connections
    FROM classified_flow AS f
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    UNION ALL
    SELECT p.src_interface_id, p.dst_interface_id,
           CASE WHEN p.traffic_direction = 'inter_interface' THEN NULL ELSE p.peer_address END,
           p.traffic_direction, p.action, p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
) AS u
GROUP BY u.src_interface_id, u.dst_interface_id, u.peer_address, u.traffic_direction;

-- statement: insert_rule
INSERT INTO rule_volume_aggregate_1h (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT
    :slot_start,
    :slot_end,
    u.rule_id,
    u.src_interface_id,
    u.dst_interface_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT f.rule_id, f.src_interface_id, f.dst_interface_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections
    FROM flow AS f
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    UNION ALL
    SELECT p.rule_id, p.src_interface_id, p.dst_interface_id, p.action, p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
) AS u
GROUP BY u.rule_id, u.src_interface_id, u.dst_interface_id;

-- statement: insert_domain
INSERT INTO domain_volume_aggregate_1h (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, computed_at)
SELECT
    :slot_start,
    :slot_end,
    u.site_name,
    u.client_id,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT a.site_name, f.src_client_id AS client_id, f.action,
           f.packet_bytes AS bytes, 1 AS connections
    FROM flow AS f
    JOIN domain_attribution AS a ON a.flow_id = f.id
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    UNION ALL
    SELECT p.site_name, p.src_client_id, p.action, p.bytes, p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
      AND p.site_name IS NOT NULL
) AS u
GROUP BY u.site_name, u.client_id;

-- The peer of a flow is the address at the other end from its inside client: the
-- destination when the client is the source, the source otherwise.
-- statement: insert_peer
INSERT INTO peer_volume_aggregate_1h (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT
    :slot_start,
    :slot_end,
    u.client_id,
    u.traffic_direction,
    u.peer_address,
    sum(u.bytes),
    sum(CASE WHEN u.action = 'pass' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.bytes ELSE 0 END),
    sum(CASE WHEN u.action = 'pass' THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action IN ('block', 'reject') THEN u.connections ELSE 0 END),
    sum(CASE WHEN u.action = 'unknown' THEN u.connections ELSE 0 END),
    :now
FROM (
    SELECT f.local_client_id AS client_id, f.traffic_direction,
           CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address
                ELSE f.src_address END AS peer_address,
           f.action, f.packet_bytes AS bytes, 1 AS connections
    FROM classified_flow AS f
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
      AND f.local_client_id IS NOT NULL
    UNION ALL
    SELECT p.local_client_id, p.traffic_direction, p.peer_address, p.action, p.bytes,
           p.connections
    FROM purged_flow_hour AS p
    WHERE p.hour_start_at = :slot_start
      AND p.local_client_id IS NOT NULL
) AS u
GROUP BY u.client_id, u.traffic_direction, u.peer_address;

-- ---------------------------------------------------------------------------
-- Composition: a day from its hours, a week and a month from their days. @child@
-- is the finer period: 1h for 24h, 24h for 7d and 30d. The finer slots tile the
-- slot exactly -- a day is 24 hours, a week 7 days, a month its days -- so every
-- summable figure is their sum, and the finer slots are kept as long as the week
-- and the month holding them (internal/store/purge.sql), so a slot is composed
-- from all of them whatever the retention.
-- ---------------------------------------------------------------------------

-- statement: compose_volume
INSERT INTO volume_aggregate_@period@ (period_start_at, period_end_at, src_interface_id,
    dst_interface_id, peer_address, traffic_scope, traffic_direction, bytes,
    allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections, blocked_connections,
    unknown_connections, computed_at)
SELECT :slot_start, :slot_end, v.src_interface_id, v.dst_interface_id, v.peer_address,
       v.traffic_scope, v.traffic_direction, sum(v.bytes), sum(v.allowed_bytes),
       sum(v.blocked_bytes), sum(v.unknown_bytes), sum(v.allowed_connections),
       sum(v.blocked_connections), sum(v.unknown_connections), :now
FROM volume_aggregate_@child@ AS v
WHERE v.period_start_at >= :slot_start
  AND v.period_start_at < :slot_end
GROUP BY v.src_interface_id, v.dst_interface_id, v.peer_address, v.traffic_scope,
         v.traffic_direction;

-- statement: compose_rule
INSERT INTO rule_volume_aggregate_@period@ (period_start_at, period_end_at, rule_id,
    src_interface_id, dst_interface_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT :slot_start, :slot_end, r.rule_id, r.src_interface_id, r.dst_interface_id,
       sum(r.bytes), sum(r.allowed_bytes), sum(r.blocked_bytes), sum(r.unknown_bytes),
       sum(r.allowed_connections), sum(r.blocked_connections), sum(r.unknown_connections),
       :now
FROM rule_volume_aggregate_@child@ AS r
WHERE r.period_start_at >= :slot_start
  AND r.period_start_at < :slot_end
GROUP BY r.rule_id, r.src_interface_id, r.dst_interface_id;

-- statement: compose_domain
INSERT INTO domain_volume_aggregate_@period@ (period_start_at, period_end_at, site_name,
    client_id, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, computed_at)
SELECT :slot_start, :slot_end, d.site_name, d.client_id, sum(d.bytes),
       sum(d.allowed_bytes), sum(d.blocked_bytes), sum(d.unknown_bytes),
       sum(d.allowed_connections), sum(d.blocked_connections), sum(d.unknown_connections),
       :now
FROM domain_volume_aggregate_@child@ AS d
WHERE d.period_start_at >= :slot_start
  AND d.period_start_at < :slot_end
GROUP BY d.site_name, d.client_id;

-- statement: compose_peer
INSERT INTO peer_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, peer_address, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
    allowed_connections, blocked_connections, unknown_connections, computed_at)
SELECT :slot_start, :slot_end, p.client_id, p.traffic_direction, p.peer_address,
       sum(p.bytes), sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections),
       :now
FROM peer_volume_aggregate_@child@ AS p
WHERE p.period_start_at >= :slot_start
  AND p.period_start_at < :slot_end
GROUP BY p.client_id, p.traffic_direction, p.peer_address;

-- ---------------------------------------------------------------------------
-- Regrouping, the same for every period. A client's slot is its peer rows of the
-- same slot, and distinct_peers is how many there are: the peer is in the peer
-- family's key, so that is an exact distinct count and never a sum or a maximum
-- of finer ones. An owner's slot is its clients' rows of the same slot, and
-- client_count is how many distinct clients there are, for the same reason. The
-- scope of a client row follows from its direction: between interfaces is
-- east-west, anything else north-south, as for a flow.
-- ---------------------------------------------------------------------------

-- statement: regroup_client
INSERT INTO client_volume_aggregate_@period@ (period_start_at, period_end_at, client_id,
    traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, distinct_peers, computed_at)
SELECT :slot_start, :slot_end, p.client_id, p.traffic_direction, sum(p.bytes),
       sum(p.allowed_bytes), sum(p.blocked_bytes), sum(p.unknown_bytes),
       sum(p.allowed_connections), sum(p.blocked_connections), sum(p.unknown_connections),
       count(DISTINCT p.peer_address), :now
FROM peer_volume_aggregate_@period@ AS p
WHERE p.period_start_at = :slot_start
GROUP BY p.client_id, p.traffic_direction;

-- statement: regroup_owner
INSERT INTO owner_volume_aggregate_@period@ (period_start_at, period_end_at, owner_id,
    traffic_scope, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
    blocked_connections, unknown_connections, client_count, computed_at)
SELECT :slot_start, :slot_end, k.owner_id,
       CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
            ELSE 'north_south' END,
       sum(c.bytes), sum(c.allowed_bytes), sum(c.blocked_bytes), sum(c.unknown_bytes),
       sum(c.allowed_connections), sum(c.blocked_connections), sum(c.unknown_connections),
       count(DISTINCT c.client_id), :now
FROM client_volume_aggregate_@period@ AS c
JOIN client AS k ON k.id = c.client_id
WHERE c.period_start_at = :slot_start
GROUP BY k.owner_id,
         CASE WHEN c.traffic_direction = 'inter_interface' THEN 'east_west'
              ELSE 'north_south' END;

-- The retention horizon of the flows, NULL when retention is unlimited. A slot
-- the purge would remove outright -- one whose own end, and for an hour or a day
-- the end of its ISO week and of its calendar month, all lie before it -- is not
-- rewritten unless it is current (internal/store/aggregate.go).
-- statement: flow_horizon
SELECT :now - CAST(value AS INTEGER)
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0;

-- The daily per-pair volume, derived from the flows of one UTC day whenever the
-- day's slot is rewritten. This and the retention purge are the only writers of
-- pair_volume_observation. It has no purged part: the purge removes a day's rows
-- once the day starts before the horizon, so the flows a rewrite of such a day
-- can no longer read are the ones whose rows the purge removes anyway.
-- statement: delete_pair_volume
DELETE FROM pair_volume_observation WHERE day_start_at = :slot_start;

-- statement: insert_pair_volume
INSERT INTO pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
    service_port, protocol, octets, packets, observed_direction,
    observed_interface_device, last_seen_at, ingested_at)
SELECT
    :slot_start,
    p.endpoint_low,
    p.endpoint_high,
    p.service_port,
    p.protocol,
    p.octets,
    p.packets,
    first.direction,
    first.interface_device,
    p.last_seen_at,
    :now
FROM (
    SELECT
        min(f.src_address, f.dst_address)  AS endpoint_low,
        max(f.src_address, f.dst_address)  AS endpoint_high,
        coalesce(f.dst_port, 0)            AS service_port,
        f.protocol                         AS protocol,
        sum(f.packet_bytes)                AS octets,
        count(*)                           AS packets,
        min(f.id)                          AS first_flow_id,
        max(f.observed_at)                 AS last_seen_at
    FROM flow AS f
    WHERE f.observed_at >= :slot_start
      AND f.observed_at < :slot_end
    GROUP BY 1, 2, 3, 4
) AS p
JOIN flow AS first ON first.id = p.first_flow_id;
