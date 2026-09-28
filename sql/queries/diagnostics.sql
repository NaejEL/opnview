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
