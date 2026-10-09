-- opnview — supporting queries the model document cites, outside the seven
-- screen queries of sql/queries/screens.sql.
--
-- Same conventions: each query is preceded by "-- diagnostic: <Name>", is a
-- single statement terminated by a semicolon, and is executed by
-- sql/schema-checks.sh. Bound parameters:
--   :window_start  inclusive lower bound, UTC epoch seconds
--   :window_end    exclusive upper bound, UTC epoch seconds

-- diagnostic: Attribution rate per client
-- How often a flow leaving a client could be given a site name. The rate is
-- the honest figure the roadmap requires the UI to expose: site names are
-- inferred from resolver-lookup correlation and nothing else on OPNsense 26.7,
-- so a client using encrypted DNS, or a cached name, shows up here as poorly
-- attributed rather than mis-attributed.
--
-- The eligible flows are those store.ReadAttributionRate counts, and no others:
-- the flows with an OUTSIDE destination. An east-west flow is never attributed
-- (docs/data-model.md, the correlation rule), so counting it would lower the rate
-- for a reason that has nothing to do with naming. Whether the rate is DEFINED --
-- whether a resolver source answered for the window at all -- is the store
-- function's to say; this diagnostic reports the counts.
SELECT
    f.src_client_id                                                  AS client_id,
    count(*)                                                         AS flow_count,
    sum(CASE WHEN a.flow_id IS NULL THEN 0 ELSE 1 END)               AS attributed_count,
    round(100.0 * sum(CASE WHEN a.flow_id IS NULL THEN 0 ELSE 1 END) / count(*), 2)
                                                                     AS attribution_rate_percent,
    avg(a.correlation_delay_seconds)                                 AS mean_correlation_delay_seconds,
    max(a.correlation_delay_seconds)                                 AS max_correlation_delay_seconds
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND f.src_client_id IS NOT NULL
  AND f.dst_interface_id IS NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id
ORDER BY attribution_rate_percent ASC, client_id ASC;

-- diagnostic: Aggregate coverage per period
-- One row per pre-computed period, with how many slots it holds and how fresh
-- they are. A period whose slot count is zero, or whose computed_at has fallen
-- behind the refresh contract in docs/data-model.md, is stale.
SELECT '1h' AS period, count(*) AS slot_count, min(period_start_at) AS earliest_period_start_at,
       max(period_end_at) AS latest_period_end_at, max(computed_at) AS last_computed_at,
       sum(bytes) AS observed_bytes
FROM volume_aggregate_1h
UNION ALL
SELECT '24h', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_24h
UNION ALL
SELECT '7d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_7d
UNION ALL
SELECT '30d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_30d;

-- diagnostic: Source availability
-- One row per REGISTERED PROVIDER — its kind, its key, its availability state,
-- the probe that determined it, and whether it is the provider opnview
-- actually reads for that kind. Every screen reads this: an unavailable
-- provider is a condition of its own and is never rendered as an absence of
-- traffic, security events, clients or names.
--
-- Availability and activeness are two different facts. Two implementations of
-- one kind may both be 'reachable'; at most one of them is active.
SELECT
    p.kind        AS kind,
    p.provider_key AS provider_key,
    p.display_name AS display_name,
    a.state       AS state,
    a.probe       AS probe,
    a.detail      AS detail,
    a.checked_at  AS checked_at,
    p.is_active   AS is_active
FROM provider AS p
LEFT JOIN source_availability AS a ON a.provider_id = p.id
ORDER BY p.kind, p.provider_key;

-- diagnostic: Clients per owner
-- The per-person view, and the honest consequence of it. Ownership is assigned
-- by the user and never inferred (internal/store/schema.sql, the owner table),
-- so most clients on a normal network have no owner at all. They are returned
-- here as an explicit 'unassigned' bucket rather than dropped: a per-person
-- screen that quietly omitted every unowned machine would under-report the
-- network, which is the failure mode this query exists to prevent.
--
-- The join is a LEFT JOIN from client, deliberately. Driving it from owner
-- would return only the machines somebody has claimed.
SELECT
    c.owner_id                                              AS owner_id,
    o.display_name                                          AS owner_display_name,
    CASE WHEN c.owner_id IS NULL THEN 'unassigned'
         ELSE 'assigned' END                                AS ownership_state,
    count(*)                                                AS client_count,
    count(DISTINCT c.interface_id)                          AS interface_count,
    min(c.first_seen_at)                                    AS first_seen_at,
    max(c.last_seen_at)                                     AS last_seen_at
FROM client AS c
LEFT JOIN owner AS o ON o.id = c.owner_id
GROUP BY c.owner_id, o.display_name, ownership_state
ORDER BY ownership_state, owner_display_name, owner_id;

-- diagnostic: Blocked lookups by list
-- Which list refused what, and what the user said that list is for. The name
-- is observed -- /api/unbound/overview/search_queries returns it verbatim on
-- every row -- and the purpose is assigned by hand, so a list nobody has
-- classified comes back with a NULL purpose and an explicit 'unassigned'
-- state rather than being guessed at or dropped.
--
-- The join is a LEFT JOIN from dns_resolution, deliberately. A blocked lookup
-- the resolver did not attribute to any list has a NULL blocklist_id, and it
-- is returned here as its own 'list not recorded' row: it is a lookup that was
-- refused, and collapsing it into a named list or omitting it would both be
-- lies about what was observed.
SELECT
    r.blocklist_id                                          AS blocklist_id,
    b.name                                                  AS blocklist_name,
    CASE WHEN r.blocklist_id IS NULL THEN 'list not recorded'
         ELSE 'list recorded' END                           AS attribution_state,
    CASE WHEN r.blocklist_id IS NULL THEN 'not applicable'
         WHEN b.purpose IS NULL THEN 'unassigned'
         ELSE b.purpose END                                 AS purpose,
    count(*)                                                AS lookup_count,
    count(DISTINCT r.domain)                                AS distinct_domains,
    count(DISTINCT r.client_id)                             AS distinct_clients,
    min(r.looked_up_at)                                     AS first_looked_up_at,
    max(r.looked_up_at)                                     AS last_looked_up_at
FROM dns_resolution AS r
LEFT JOIN blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
  AND r.looked_up_at >= :window_start
  AND r.looked_up_at < :window_end
GROUP BY r.blocklist_id, b.name, attribution_state, purpose
ORDER BY lookup_count DESC, blocklist_id;

-- diagnostic: Owner aggregate coverage per period
-- One row per pre-computed per-owner period, with how many slots it holds, how
-- fresh they are, and how many of those slots are the unassigned bucket. The
-- last column is the honest one: ownership is assigned by hand, so most of the
-- network belongs to nobody in particular and a per-person aggregate carrying
-- no unassigned slot would mean the unowned clients had been dropped.
SELECT '1h' AS period, count(*) AS slot_count, min(period_start_at) AS earliest_period_start_at,
       max(period_end_at) AS latest_period_end_at, max(computed_at) AS last_computed_at,
       sum(bytes) AS observed_bytes,
       sum(CASE WHEN owner_id IS NULL THEN 1 ELSE 0 END) AS unassigned_slot_count
FROM owner_volume_aggregate_1h
UNION ALL
SELECT '24h', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes),
       sum(CASE WHEN owner_id IS NULL THEN 1 ELSE 0 END)
FROM owner_volume_aggregate_24h
UNION ALL
SELECT '7d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes),
       sum(CASE WHEN owner_id IS NULL THEN 1 ELSE 0 END)
FROM owner_volume_aggregate_7d
UNION ALL
SELECT '30d', count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes),
       sum(CASE WHEN owner_id IS NULL THEN 1 ELSE 0 END)
FROM owner_volume_aggregate_30d;

-- diagnostic: Leases per client and issuing server
-- One client's leases, each naming the DHCP server that issued it.
--
-- It exists because the dhcp_lease kind admits several concurrently active
-- providers: one server issues on one VLAN and another on a second, so a machine
-- can legitimately hold two leases at once and "which server gave this machine
-- its address" becomes a real question. A machine leased by both servers is ONE
-- client with TWO leases -- the identity cascade keys on the DHCP client
-- identifier and then on the MAC, neither scoped to an interface -- and this
-- query is what makes that visible rather than merely true.
--
-- The server's name is READ, not reconstructed: provider.display_name comes off
-- the join, so nothing parses a composite key or infers a server from a backend
-- token. The backend is returned beside it because they are different facts --
-- which server issued the lease, and which response shape it was read from.
--
-- The lease's own interface is returned rather than the client's: a client that
-- holds leases on two VLANs has one client.interface_id, the most recently
-- observed, and the per-lease value is the one that is true of each lease.
--
-- A lease whose validity start is null is a backend that does not report one,
-- which is a state and not a gap; the generation key beside it says what the row's
-- identity actually rests on.
SELECT
    l.observed_at                                AS observed_at,
    p.provider_key                               AS provider_key,
    p.display_name                               AS issuing_server,
    l.backend                                    AS backend,
    l.address                                    AS address,
    l.mac                                        AS mac,
    l.hostname                                   AS hostname,
    l.lease_state                                AS lease_state,
    l.starts_at                                  AS starts_at,
    l.generation_key                             AS generation_key,
    l.expires_at                                 AS expires_at,
    l.interface_id                               AS interface_id,
    coalesce(s.user_label, s.description)        AS interface_label
FROM dhcp_lease AS l
JOIN provider AS p ON p.id = l.provider_id
LEFT JOIN interface AS s ON s.id = l.interface_id
WHERE l.client_id = :client_id
ORDER BY l.observed_at DESC, p.provider_key;

-- diagnostic: Record rate per kind
-- How many records each paged read writes, measured on the rows opnview holds:
-- the count in the window, the span it covers on the source clock and on the
-- ingested clock, the busiest 60-second bucket, and the collection gaps
-- detected in the window, one row per gap reason. It is the measurement the
-- collection surface sizes a page and an interval on, character for
-- character: internal/store/recordrate.go builds each branch, and
-- internal/sizing holds this file to it, so a suggested pair can be checked by
-- running this query against the same database.
--
-- A kind with no record still yields its row, with a NULL span and peak: no
-- record is a state, not a rate of nought. The ingested span of the lease
-- table is NULL because that table records no ingested instant.
--
-- Rebuilt after the loss of 2 October 2026 from the compiled measurement of
-- that evening; this comment is rewritten.
SELECT
    k.kind AS kind,
    :window_start AS window_start_at,
    :window_end AS window_end_at,
    (SELECT CAST(value AS INTEGER) FROM setting
      WHERE key = 'retention_seconds')
        AS retention_seconds,
    (SELECT count(*) FROM flow
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS record_count,
    (SELECT min(observed_at) FROM flow
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS covered_from_at,
    (SELECT max(observed_at) FROM flow
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS covered_to_at,
    (SELECT min(ingested_at) FROM flow
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS ingested_from_at,
    (SELECT max(ingested_at) FROM flow
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS ingested_to_at,
    (SELECT max(bucket_records) FROM
      (SELECT count(*) AS bucket_records FROM flow
        WHERE observed_at >= :window_start AND observed_at < :window_end
        GROUP BY observed_at / 60))
        AS peak_bucket_records,
    g.reason AS gap_reason,
    count(g.id) AS gap_count,
    coalesce(sum(g.interval_end_at - g.interval_start_at), 0) AS missed_seconds
FROM (SELECT 'firewall_log' AS kind) AS k
LEFT JOIN provider AS p ON p.kind = k.kind
LEFT JOIN collection_gap AS g ON g.provider_id = p.id
     AND g.detected_at >= :window_start AND g.detected_at < :window_end
GROUP BY k.kind, g.reason
UNION ALL
SELECT
    k.kind AS kind,
    :window_start AS window_start_at,
    :window_end AS window_end_at,
    (SELECT CAST(value AS INTEGER) FROM setting
      WHERE key = 'retention_seconds')
        AS retention_seconds,
    (SELECT count(*) FROM security_event
      WHERE occurred_at >= :window_start AND occurred_at < :window_end)
        AS record_count,
    (SELECT min(occurred_at) FROM security_event
      WHERE occurred_at >= :window_start AND occurred_at < :window_end)
        AS covered_from_at,
    (SELECT max(occurred_at) FROM security_event
      WHERE occurred_at >= :window_start AND occurred_at < :window_end)
        AS covered_to_at,
    (SELECT min(ingested_at) FROM security_event
      WHERE occurred_at >= :window_start AND occurred_at < :window_end)
        AS ingested_from_at,
    (SELECT max(ingested_at) FROM security_event
      WHERE occurred_at >= :window_start AND occurred_at < :window_end)
        AS ingested_to_at,
    (SELECT max(bucket_records) FROM
      (SELECT count(*) AS bucket_records FROM security_event
        WHERE occurred_at >= :window_start AND occurred_at < :window_end
        GROUP BY occurred_at / 60))
        AS peak_bucket_records,
    g.reason AS gap_reason,
    count(g.id) AS gap_count,
    coalesce(sum(g.interval_end_at - g.interval_start_at), 0) AS missed_seconds
FROM (SELECT 'security_event' AS kind) AS k
LEFT JOIN provider AS p ON p.kind = k.kind
LEFT JOIN collection_gap AS g ON g.provider_id = p.id
     AND g.detected_at >= :window_start AND g.detected_at < :window_end
GROUP BY k.kind, g.reason
UNION ALL
SELECT
    k.kind AS kind,
    :window_start AS window_start_at,
    :window_end AS window_end_at,
    (SELECT CAST(value AS INTEGER) FROM setting
      WHERE key = 'retention_seconds')
        AS retention_seconds,
    (SELECT count(*) FROM dhcp_lease
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS record_count,
    (SELECT min(observed_at) FROM dhcp_lease
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS covered_from_at,
    (SELECT max(observed_at) FROM dhcp_lease
      WHERE observed_at >= :window_start AND observed_at < :window_end)
        AS covered_to_at,
    NULL
        AS ingested_from_at,
    NULL
        AS ingested_to_at,
    (SELECT max(bucket_records) FROM
      (SELECT count(*) AS bucket_records FROM dhcp_lease
        WHERE observed_at >= :window_start AND observed_at < :window_end
        GROUP BY observed_at / 60))
        AS peak_bucket_records,
    g.reason AS gap_reason,
    count(g.id) AS gap_count,
    coalesce(sum(g.interval_end_at - g.interval_start_at), 0) AS missed_seconds
FROM (SELECT 'dhcp_lease' AS kind) AS k
LEFT JOIN provider AS p ON p.kind = k.kind
LEFT JOIN collection_gap AS g ON g.provider_id = p.id
     AND g.detected_at >= :window_start AND g.detected_at < :window_end
GROUP BY k.kind, g.reason
UNION ALL
SELECT
    k.kind AS kind,
    :window_start AS window_start_at,
    :window_end AS window_end_at,
    (SELECT CAST(value AS INTEGER) FROM setting
      WHERE key = 'retention_seconds')
        AS retention_seconds,
    (SELECT count(*) FROM dns_resolution
      WHERE looked_up_at >= :window_start AND looked_up_at < :window_end)
        AS record_count,
    (SELECT min(looked_up_at) FROM dns_resolution
      WHERE looked_up_at >= :window_start AND looked_up_at < :window_end)
        AS covered_from_at,
    (SELECT max(looked_up_at) FROM dns_resolution
      WHERE looked_up_at >= :window_start AND looked_up_at < :window_end)
        AS covered_to_at,
    (SELECT min(ingested_at) FROM dns_resolution
      WHERE looked_up_at >= :window_start AND looked_up_at < :window_end)
        AS ingested_from_at,
    (SELECT max(ingested_at) FROM dns_resolution
      WHERE looked_up_at >= :window_start AND looked_up_at < :window_end)
        AS ingested_to_at,
    (SELECT max(bucket_records) FROM
      (SELECT count(*) AS bucket_records FROM dns_resolution
        WHERE looked_up_at >= :window_start AND looked_up_at < :window_end
        GROUP BY looked_up_at / 60))
        AS peak_bucket_records,
    g.reason AS gap_reason,
    count(g.id) AS gap_count,
    coalesce(sum(g.interval_end_at - g.interval_start_at), 0) AS missed_seconds
FROM (SELECT 'dns_lookup' AS kind) AS k
LEFT JOIN provider AS p ON p.kind = k.kind
LEFT JOIN collection_gap AS g ON g.provider_id = p.id
     AND g.detected_at >= :window_start AND g.detected_at < :window_end
GROUP BY k.kind, g.reason;

-- diagnostic: Unresolved host names by cause
-- Every lookup whose logged host name still names no machine -- client_resolution
-- 'unknown_hostname' -- in the window, sorted into the first cause that applies, in
-- this order (step-5A live corrections, item 3.1). The leases are compared as of the
-- lookup's instant where the cause says so:
--   this_firewall    the name is `localhost`, which names this firewall
--   exact_match      a lease carries the logged name exactly, case aside, at the
--                    lookup's instant
--   logged_label     a lease carries the logged name's first label, at that instant
--   both_labels      a lease's first label is the logged name's first label, at that
--                    instant
--   trailing_dot     the names are equal once a trailing dot is removed, at that
--                    instant
--   not_at_instant   a lease names the host by its first label, but none was valid at
--                    the lookup's instant
--   no_lease         no lease names the host at all
-- The first five are what the matching of the step-5A live corrections resolves, so a
-- database it has run on holds lookups in the last two only, once a lease pass has
-- examined them. The query reads the leases once per lookup, which is why it is a
-- diagnostic and not a statement the collector runs.
SELECT cause, count(*) AS lookups
FROM (
    SELECT
        CASE
            WHEN lower(rtrim(r.client_hostname, '.')) = 'localhost' THEN 'this_firewall'
            WHEN EXISTS (SELECT 1 FROM dhcp_lease AS l
                         WHERE lower(l.hostname) = lower(r.client_hostname)
                           AND (l.starts_at IS NULL OR l.starts_at <= r.looked_up_at)
                           AND (l.expires_at IS NULL OR l.expires_at >= r.looked_up_at))
                THEN 'exact_match'
            WHEN EXISTS (SELECT 1 FROM dhcp_lease AS l
                         WHERE lower(l.hostname) = lower(CASE
                                   WHEN instr(r.client_hostname, '.') > 0
                                       THEN substr(r.client_hostname, 1, instr(r.client_hostname, '.') - 1)
                                   ELSE r.client_hostname END)
                           AND (l.starts_at IS NULL OR l.starts_at <= r.looked_up_at)
                           AND (l.expires_at IS NULL OR l.expires_at >= r.looked_up_at))
                THEN 'logged_label'
            WHEN EXISTS (SELECT 1 FROM dhcp_lease AS l
                         WHERE l.hostname_label = lower(CASE
                                   WHEN instr(r.client_hostname, '.') > 0
                                       THEN substr(r.client_hostname, 1, instr(r.client_hostname, '.') - 1)
                                   ELSE r.client_hostname END)
                           AND (l.starts_at IS NULL OR l.starts_at <= r.looked_up_at)
                           AND (l.expires_at IS NULL OR l.expires_at >= r.looked_up_at))
                THEN 'both_labels'
            WHEN EXISTS (SELECT 1 FROM dhcp_lease AS l
                         WHERE lower(rtrim(l.hostname, '.')) = lower(rtrim(r.client_hostname, '.'))
                           AND (l.starts_at IS NULL OR l.starts_at <= r.looked_up_at)
                           AND (l.expires_at IS NULL OR l.expires_at >= r.looked_up_at))
                THEN 'trailing_dot'
            WHEN EXISTS (SELECT 1 FROM dhcp_lease AS l
                         WHERE l.hostname_label = lower(CASE
                                   WHEN instr(rtrim(r.client_hostname, '.'), '.') > 0
                                       THEN substr(rtrim(r.client_hostname, '.'), 1,
                                                   instr(rtrim(r.client_hostname, '.'), '.') - 1)
                                   ELSE rtrim(r.client_hostname, '.') END))
                THEN 'not_at_instant'
            ELSE 'no_lease'
        END AS cause
    FROM dns_resolution AS r
    WHERE r.client_resolution = 'unknown_hostname'
      AND r.looked_up_at >= :window_start
      AND r.looked_up_at < :window_end
)
GROUP BY cause
ORDER BY cause;

-- diagnostic: live_this_firewall_unmarked
-- Live validation L2 of the step-5A live corrections: the flows of the window with an
-- end whose address this firewall held at the flow's instant -- the
-- this_firewall_address view's definition, with the margin of the discovery interval
-- setting, 300 s when the row is absent -- that are NOT recorded as this firewall.
-- Expected 0. A loopback end is recognised from the address form by the code and is
-- not restated here.
SELECT count(*) AS unmarked_flows
FROM flow AS f
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND ((f.src_is_this_firewall = 0
        AND EXISTS (SELECT 1 FROM this_firewall_address AS h
                    WHERE h.address = f.src_address
                      AND h.first_seen_at - coalesce((SELECT CAST(s.value AS INTEGER) FROM setting AS s
                            WHERE s.key = 'refresh_interval_discovery_seconds'), 300) <= f.observed_at
                      AND (h.is_current = 1
                           OR h.last_seen_at + coalesce((SELECT CAST(s.value AS INTEGER) FROM setting AS s
                            WHERE s.key = 'refresh_interval_discovery_seconds'), 300) >= f.observed_at)))
    OR (f.dst_is_this_firewall = 0
        AND EXISTS (SELECT 1 FROM this_firewall_address AS h
                    WHERE h.address = f.dst_address
                      AND h.first_seen_at - coalesce((SELECT CAST(s.value AS INTEGER) FROM setting AS s
                            WHERE s.key = 'refresh_interval_discovery_seconds'), 300) <= f.observed_at
                      AND (h.is_current = 1
                           OR h.last_seen_at + coalesce((SELECT CAST(s.value AS INTEGER) FROM setting AS s
                            WHERE s.key = 'refresh_interval_discovery_seconds'), 300) >= f.observed_at))));

-- diagnostic: live_anonymous_north_south
-- Live validation L3: the flows of the window counted north-south with neither end
-- placed -- no interface on either end, neither end this firewall -- and which are not
-- the second leg of a counted connection. On the live firewall 5A put most of its flows
-- here, the firewall's own recursion among them; expected about 0. What remains is
-- traffic between two outside addresses, which a firewall forwards only exceptionally.
SELECT count(*) AS anonymous_flows,
       coalesce(sum(f.packet_bytes), 0) AS anonymous_bytes
FROM flow AS f
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND f.traffic_scope = 'north_south'
  AND f.src_interface_id IS NULL
  AND f.dst_interface_id IS NULL
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg');

-- diagnostic: live_upstream_firewall_source_outcomes
-- Live validation L11: every `out` record of the window on an upstream interface whose
-- source is this firewall, by pairing outcome, with, for the second legs, how many have
-- no first leg naming them back. Every record carries exactly one outcome, so a `none`
-- row is a defect, and `partner_missing` is expected to be 0.
SELECT coalesce(f.pair_outcome, 'none') AS outcome,
       count(*) AS records,
       sum(CASE WHEN f.pair_outcome = 'second_leg'
                     AND NOT EXISTS (SELECT 1 FROM flow AS p
                                     WHERE p.id = f.paired_flow_id
                                       AND p.pair_outcome = 'first_leg'
                                       AND p.paired_flow_id = f.id)
                THEN 1 ELSE 0 END) AS partner_missing
FROM flow AS f
JOIN interface AS i ON i.device = f.interface_device
WHERE f.observed_at >= :window_start
  AND f.observed_at < :window_end
  AND f.direction = 'out'
  AND f.src_is_this_firewall = 1
  AND i.is_upstream = 1
GROUP BY 1
ORDER BY 1;

-- diagnostic: live_paired_counted_twice
-- Live validation L12: the connections of the window counted twice. A paired
-- connection is counted once, on its first leg (decision 2): its second leg carries
-- the device it was logged on in classified_flow.exit_leg_device, which keeps it out of
-- every total, and it carries no attribution. So a second leg with no exit-leg device, a
-- second leg with an attribution, and a first leg whose partner is not its second leg
-- are each a connection counted twice or wrongly, and the sum is expected to be 0.
SELECT (SELECT count(*) FROM classified_flow AS f
        WHERE f.observed_at >= :window_start
          AND f.observed_at < :window_end
          AND f.pair_outcome = 'second_leg'
          AND f.exit_leg_device IS NULL)
     + (SELECT count(*) FROM flow AS f
        JOIN domain_attribution AS a ON a.flow_id = f.id
        WHERE f.observed_at >= :window_start
          AND f.observed_at < :window_end
          AND f.pair_outcome = 'second_leg')
     + (SELECT count(*) FROM flow AS f
        WHERE f.observed_at >= :window_start
          AND f.observed_at < :window_end
          AND f.pair_outcome = 'first_leg'
          AND NOT EXISTS (SELECT 1 FROM flow AS p
                          WHERE p.id = f.paired_flow_id
                            AND p.pair_outcome = 'second_leg'
                            AND p.paired_flow_id = f.id))
       AS counted_twice;
