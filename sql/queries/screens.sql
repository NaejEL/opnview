-- opnview — the seven representative screen queries, one per roadmap screen.
--
-- This file is authoritative. docs/data-model.md explains each query and names
-- the index that makes its plan an index search rather than a table scan, but
-- the text that runs is the text here.
--
-- Each query is preceded by a line of the form "-- screen: <Name>" and is a
-- single statement terminated by a semicolon. sql/schema-checks.sh splits the
-- file on those markers, runs every query against the seeded database, records
-- its EXPLAIN QUERY PLAN and asserts that no growing table is scanned.
--
-- Bound parameters, supplied by the caller:
--   :window_start  inclusive lower bound of the period, UTC epoch seconds
--   :window_end    exclusive upper bound of the period, UTC epoch seconds
--   :segment_id    the segment the Segment screen is showing
--   :device_id     the device the Device screen is showing
--
-- No address, CIDR, interface name, VLAN name or segment name appears below,
-- and no query assumes a segment count or an address family.
--
-- Observation-point limit: every byte figure below counts only traffic that
-- crossed the firewall. Traffic between two devices inside one segment is
-- invisible to all five sources, so every volume is a lower bound and the
-- screen must say so.

-- screen: Overview
-- Segments, totals for the period, and the east-west versus north-south
-- split. The classification comes from segment membership alone: a flow is
-- east-west exactly when both of its endpoints sit in a discovered segment.
SELECT
    f.traffic_scope                                         AS traffic_scope,
    count(*)                                                AS flow_count,
    sum(CASE WHEN f.action = 'block' THEN 1 ELSE 0 END)     AS blocked_count,
    sum(CASE WHEN f.action <> 'block' THEN 1 ELSE 0 END)    AS allowed_count,
    sum(f.packet_bytes)                                     AS observed_bytes,
    count(DISTINCT f.src_segment_id)                        AS source_segment_count,
    count(DISTINCT f.src_device_id)                         AS source_device_count
FROM flow AS f
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
GROUP BY f.traffic_scope
ORDER BY observed_bytes DESC;

-- screen: Matrix
-- Source segment by destination segment: volume, allowed connections, blocked
-- connections and the rules that matched. A destination segment of NULL is the
-- north-south column.
SELECT
    f.src_segment_id                                                AS src_segment_id,
    coalesce(ssrc.user_label, ssrc.discovered_description)          AS src_segment_label,
    f.dst_segment_id                                                AS dst_segment_id,
    coalesce(sdst.user_label, sdst.discovered_description)          AS dst_segment_label,
    f.traffic_scope                                                 AS traffic_scope,
    sum(f.packet_bytes)                                             AS observed_bytes,
    sum(CASE WHEN f.action <> 'block' THEN 1 ELSE 0 END)            AS allowed_connections,
    sum(CASE WHEN f.action = 'block' THEN 1 ELSE 0 END)             AS blocked_connections,
    group_concat(DISTINCT r.description)                            AS matching_rules,
    sum(CASE WHEN f.rule_lookup_state = 'not_found' THEN 1 ELSE 0 END) AS unknown_rule_connections
FROM flow AS f
LEFT JOIN rule AS r ON r.id = f.rule_id
LEFT JOIN segment AS ssrc ON ssrc.id = f.src_segment_id
LEFT JOIN segment AS sdst ON sdst.id = f.dst_segment_id
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND f.src_segment_id IS NOT NULL
GROUP BY f.src_segment_id, src_segment_label, f.dst_segment_id, dst_segment_label, f.traffic_scope
ORDER BY observed_bytes DESC;

-- screen: Segment
-- The devices of one segment, with their destinations and their denials.
SELECT
    f.src_device_id                                         AS device_id,
    d.hostname                                              AS hostname,
    d.last_address                                          AS last_address,
    d.identity_kind                                         AS identity_kind,
    d.unstable_identity                                     AS unstable_identity,
    count(*)                                                AS flow_count,
    sum(f.packet_bytes)                                     AS observed_bytes,
    sum(CASE WHEN f.action = 'block' THEN 1 ELSE 0 END)     AS blocked_count,
    count(DISTINCT f.dst_address)                           AS distinct_destinations,
    sum(CASE WHEN f.traffic_scope = 'east_west' THEN 1 ELSE 0 END) AS east_west_count,
    max(f.observed_at)                                      AS last_seen_at
FROM flow AS f
LEFT JOIN device AS d ON d.id = f.src_device_id
WHERE f.src_segment_id = :segment_id
  AND f.observed_at >= :window_start
  AND f.observed_at < :window_end
GROUP BY f.src_device_id, d.hostname, d.last_address, d.identity_kind, d.unstable_identity
ORDER BY observed_bytes DESC;

-- screen: Device
-- One device's traffic: destination, country, operator, and the site name when
-- one could be inferred. A flow with no attribution is returned with a null
-- site name and its address, country and operator — never omitted.
SELECT
    f.id                                                    AS flow_id,
    f.observed_at                                           AS observed_at,
    f.dst_address                                           AS dst_address,
    f.dst_port                                              AS dst_port,
    f.protocol                                              AS protocol,
    f.action                                                AS action,
    f.traffic_scope                                         AS traffic_scope,
    f.packet_bytes                                          AS packet_bytes,
    g.lookup_state                                          AS geo_lookup_state,
    g.country_code                                          AS country_code,
    g.operator                                              AS operator,
    g.dataset_build_at                                      AS dataset_build_at,
    a.site_name                                             AS site_name,
    a.correlation_delay_seconds                             AS correlation_delay_seconds,
    f.interface_lookup_state                                AS interface_lookup_state,
    CASE WHEN f.interface_lookup_state = 'resolved' THEN im.description END AS interface_description
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
LEFT JOIN interface_map AS im ON im.device_name = f.interface_device
WHERE f.src_device_id = :device_id
  AND f.observed_at >= :window_start
  AND f.observed_at < :window_end
ORDER BY f.observed_at DESC;

-- screen: Blocked
-- The blocked timeline, by rule and by source. blocked_event is the projection
-- of flow on the blocking actions; a rid that matches no known rule is
-- returned with rule_lookup_state 'not_found' and a null description, and a
-- raw interface name absent from the interface map likewise. Present, not
-- missing.
SELECT
    b.observed_at                                           AS observed_at,
    b.src_segment_id                                        AS src_segment_id,
    b.src_device_id                                         AS src_device_id,
    b.src_address                                           AS src_address,
    b.dst_address                                           AS dst_address,
    b.dst_port                                              AS dst_port,
    b.protocol                                              AS protocol,
    b.traffic_scope                                         AS traffic_scope,
    b.rid                                                   AS rid,
    b.rule_lookup_state                                     AS rule_lookup_state,
    CASE WHEN b.rule_lookup_state = 'resolved' THEN r.description END AS rule_description,
    b.interface_device                                      AS interface_device,
    b.interface_lookup_state                                AS interface_lookup_state,
    CASE WHEN b.interface_lookup_state = 'resolved' THEN im.description END AS interface_description
FROM blocked_event AS b
LEFT JOIN rule AS r ON r.id = b.rule_id
LEFT JOIN interface_map AS im ON im.device_name = b.interface_device
WHERE b.observed_at >= :window_start
  AND b.observed_at < :window_end
ORDER BY b.observed_at DESC;

-- screen: Alerts
-- Security events joined to the device and the segment, each naming the
-- provider that contributed it, with severity taken from the event itself when
-- the provider ships one and otherwise resolved through the per-provider
-- rule-info cache. A rule identity absent from that cache, and carrying no
-- inline severity, is returned with severity_state 'unknown' and a null
-- severity, not dropped. The rule identity is text, so a provider whose rules
-- are named rather than numbered is returned unchanged.
--
-- Every join is a LEFT JOIN, the provider one included: security_event is the
-- table the period predicate applies to, and it must drive the plan.
SELECT
    se.occurred_at                                          AS occurred_at,
    p.provider_key                                          AS provider_key,
    se.rule_identity                                        AS rule_identity,
    se.signature                                            AS signature,
    se.event_action                                         AS event_action,
    CASE WHEN coalesce(se.normalised_severity, ri.normalised_severity) IS NULL
         THEN 'unknown' ELSE 'resolved' END                 AS severity_state,
    coalesce(se.normalised_severity, ri.normalised_severity) AS severity,
    ri.category                                             AS category,
    se.src_address                                          AS src_address,
    se.src_port                                             AS src_port,
    se.dst_address                                          AS dst_address,
    se.dst_port                                             AS dst_port,
    se.protocol                                             AS protocol,
    se.src_device_id                                        AS device_id,
    d.hostname                                              AS hostname,
    se.src_segment_id                                       AS segment_id,
    coalesce(s.user_label, s.discovered_description)        AS segment_label
FROM security_event AS se
LEFT JOIN provider AS p ON p.id = se.provider_id
LEFT JOIN provider_rule_info AS ri
       ON ri.provider_id = se.provider_id AND ri.rule_identity = se.rule_identity
LEFT JOIN device AS d ON d.id = se.src_device_id
LEFT JOIN segment AS s ON s.id = se.src_segment_id
WHERE se.occurred_at >= :window_start
  AND se.occurred_at < :window_end
ORDER BY se.occurred_at DESC;

-- screen: Map
-- Destinations by country and by operator, per source segment, read from the
-- 24 h aggregate. This is also the aggregate-mode query the roadmap asks for:
-- volumes, segments, countries and operators, reading no table that holds a
-- domain name. A geo cache miss is a modelled row and is returned with its
-- lookup_state rather than silently dropping the volume.
SELECT
    g.lookup_state                                          AS geo_lookup_state,
    g.country_code                                          AS country_code,
    g.country_name                                          AS country_name,
    g.asn                                                   AS asn,
    g.operator                                              AS operator,
    v.src_segment_id                                        AS src_segment_id,
    coalesce(s.user_label, s.discovered_description)        AS segment_label,
    sum(v.bytes)                                            AS observed_bytes,
    sum(v.allowed_connections)                              AS allowed_connections,
    sum(v.blocked_connections)                              AS blocked_connections,
    count(DISTINCT v.peer_address)                          AS distinct_peers,
    max(v.computed_at)                                      AS aggregate_computed_at,
    max(g.dataset_build_at)                                 AS dataset_build_at
FROM volume_aggregate_24h AS v
JOIN geo_asn AS g ON g.address = v.peer_address
LEFT JOIN segment AS s ON s.id = v.src_segment_id
WHERE v.period_start_at >= :window_start
  AND v.period_start_at < :window_end
GROUP BY g.lookup_state, g.country_code, g.country_name, g.asn, g.operator,
         v.src_segment_id, segment_label
ORDER BY observed_bytes DESC;
