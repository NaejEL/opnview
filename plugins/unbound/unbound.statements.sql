-- The unbound plugin's statements, run by its SQL-only derivation `resolve` after every dns_lookup,
-- dhcp_lease and resolver_local_data pass (ADR 0020 wave 4): the lists the refused lookups name, and
-- the machine behind a host name the query report logged as a lookup's client.

-- Every list a stored lookup names and no row holds yet: one row per distinct name. The purpose is
-- the user's and is never derived from the name.
-- statement: insert_blocklists
INSERT INTO plugin_unbound.blocklist (name, first_seen_at, last_seen_at)
SELECT r.blocklist_name, min(r.looked_up_at), max(r.looked_up_at)
FROM plugin_unbound.dns_resolution AS r
WHERE r.blocklist_id IS NULL
  AND r.blocklist_name <> ''
  AND NOT EXISTS (SELECT 1 FROM plugin_unbound.blocklist AS b WHERE b.name = r.blocklist_name)
GROUP BY r.blocklist_name
ON CONFLICT (name) DO NOTHING;


-- A refused lookup is pointed at its list, and the list's sightings span the lookups naming it.
-- statement: link_blocklists
WITH linked AS (
    UPDATE plugin_unbound.dns_resolution AS r
    SET blocklist_id = b.id
    FROM plugin_unbound.blocklist AS b
    WHERE r.blocklist_id IS NULL
      AND r.blocklist_name <> ''
      AND b.name = r.blocklist_name
    RETURNING b.id AS blocklist_id, r.looked_up_at)
UPDATE plugin_unbound.blocklist AS b
SET first_seen_at = LEAST(b.first_seen_at, s.first_at),
    last_seen_at = GREATEST(b.last_seen_at, s.last_at)
FROM (SELECT blocklist_id, min(looked_up_at) AS first_at, max(looked_up_at) AS last_at
      FROM linked GROUP BY blocklist_id) AS s
WHERE b.id = s.blocklist_id;


-- A HOST NAME LOGGED AS A LOOKUP'S CLIENT. The query report gives the name a reverse lookup of the
-- querying address returned instead of the address, when there was one. Each such lookup is examined
-- once, as of its own instant, against the evidence held: the leases whose host name has the same
-- first label -- compared without regard to case and with a trailing dot removed on both sides --
-- valid at the instant (exactly one address: lease_hostname; several: ambiguous_hostname); failing a
-- lease, the resolver's local data naming the host (one address: local_data_hostname; one IPv4 and one
-- IPv6 address under one name, the dual-stack rule: the address of the lookup's querying family, or
-- dual_stack_hostname when the family is unknown; several otherwise: ambiguous_hostname); nothing:
-- unknown_hostname. The resolver's own name is this firewall. A lookup whose resolution changed is
-- placed again from the address it now names: its interface and client are cleared and it is
-- pending for the clients plugin. The mark hostname_examined_at keeps a lookup from being examined
-- twice; a lookup ingested after the passes this run follows waits for the next.
-- statement: resolve_hostnames
WITH due AS (
    SELECT r.id, r.client_hostname, r.looked_up_at, r.client_family, r.client_resolution,
           lower(CASE WHEN strpos(rtrim(r.client_hostname, '.'), '.') > 0
                      THEN substr(rtrim(r.client_hostname, '.'), 1, strpos(rtrim(r.client_hostname, '.'), '.') - 1)
                      ELSE rtrim(r.client_hostname, '.') END) AS label
    FROM plugin_unbound.dns_resolution AS r
    WHERE r.client_hostname IS NOT NULL
      AND r.client_resolution IN ('ambiguous_hostname', 'unknown_hostname', 'dual_stack_hostname')
      AND r.hostname_examined_at IS NULL
      AND r.ingested_at < CAST(:now AS BIGINT)
),
leased AS (
    SELECT d.id, count(DISTINCT l.address) AS n, min(l.address) AS address
    FROM due AS d
    JOIN plugin_leases.dhcp_lease AS l
      ON l.hostname_label = d.label
     AND (l.starts_at IS NULL OR l.starts_at <= d.looked_up_at)
     AND (l.expires_at IS NULL OR l.expires_at >= d.looked_up_at)
    GROUP BY d.id
),
local AS (
    SELECT DISTINCT d.id, o.address,
           family(CAST(o.address AS inet)) AS fam,
           lower(rtrim(CASE WHEN o.rrtype = 'PTR' THEN o.value ELSE o.owner_name END, '.')) AS name
    FROM due AS d
    JOIN plugin_unbound.resource_record_observation AS o
      ON o.held_in = 'local_data'
     AND o.host_label = d.label
     AND o.covered_until_at >= d.looked_up_at
     AND o.covered_from_at <= d.looked_up_at
),
local_summary AS (
    SELECT l.id,
           count(DISTINCT l.address) AS n,
           min(l.address) AS address,
           EXISTS (SELECT 1 FROM local AS a JOIN local AS b ON a.id = l.id AND b.id = l.id
                   AND a.fam <> b.fam AND a.name = b.name) AS dual_stack
    FROM local AS l
    GROUP BY l.id
),
decided AS (
    SELECT d.id, d.client_resolution AS before,
           CASE WHEN lower(rtrim(d.client_hostname, '.')) = 'localhost' THEN 'this_firewall_hostname'
                WHEN le.n = 1 THEN 'lease_hostname'
                WHEN le.n >= 2 THEN 'ambiguous_hostname'
                WHEN lo.n IS NULL THEN 'unknown_hostname'
                WHEN lo.n = 1 THEN 'local_data_hostname'
                WHEN lo.n = 2 AND lo.dual_stack AND d.client_family IS NULL THEN 'dual_stack_hostname'
                WHEN lo.n = 2 AND lo.dual_stack
                     AND EXISTS (SELECT 1 FROM local AS f WHERE f.id = d.id AND f.fam = d.client_family)
                     THEN 'local_data_hostname'
                WHEN lo.n = 2 AND lo.dual_stack THEN 'dual_stack_hostname'
                ELSE 'ambiguous_hostname' END AS resolution,
           CASE WHEN lower(rtrim(d.client_hostname, '.')) = 'localhost' THEN d.client_hostname
                WHEN le.n = 1 THEN le.address
                WHEN le.n >= 2 THEN d.client_hostname
                WHEN lo.n = 1 THEN lo.address
                WHEN lo.n = 2 AND lo.dual_stack AND d.client_family IS NOT NULL
                     THEN coalesce((SELECT min(f.address) FROM local AS f WHERE f.id = d.id AND f.fam = d.client_family),
                                   d.client_hostname)
                ELSE d.client_hostname END AS address
    FROM due AS d
    LEFT JOIN leased AS le ON le.id = d.id
    LEFT JOIN local_summary AS lo ON lo.id = d.id
)
UPDATE plugin_unbound.dns_resolution AS r
SET client_address = n.address,
    client_resolution = n.resolution,
    client_id = CASE WHEN n.before = n.resolution THEN r.client_id END,
    interface_id = CASE WHEN n.before = n.resolution THEN r.interface_id END,
    interface_lookup_state = CASE WHEN n.before = n.resolution THEN r.interface_lookup_state ELSE 'pending' END,
    hostname_examined_at = CAST(:now AS BIGINT)
FROM decided AS n
WHERE r.id = n.id;


-- ---------------------------------------------------------------------------
-- SITE-NAME ATTRIBUTION, the derivation `attribute` (ADR 0020 wave 4; the Go of
-- internal/store/attribution rewritten set-based, with the same result). The query report carries
-- no answer address and the resolver's cache does, so an attribution is exact evidence first -- a
-- lookup whose name the cache shows answering with the flow's destination over the whole interval
-- from the lookup to the flow, the CNAME chain climbed from the answer's name to the name a client
-- looked up, eleven links at most -- and, failing that, a lookup and a flow by the same client, close
-- in time, when every eligible lookup of the client in the window asked one name and that name's
-- answers, if any of the destination's family are known, include the destination. A flow is eligible
-- when its destination is outside -- not inside, and not this firewall -- and it is not the second
-- record of a connection whose first record is counted. "The same client" is the same client id
-- where both rows carry one, the same address otherwise. The flows decided again are those of the
-- window reaching back the longer of the two delays before the last run, and those the clients
-- plugin changed since. Each run works in attribution_work, cleared first.
--
-- Parameters: :since, :now, :attribution_max_delay (the timing delay), :cache_answer_delay.
-- ---------------------------------------------------------------------------

-- statement: attribution_clear
DELETE FROM plugin_unbound.attribution_work;


-- statement: attribution_candidates
INSERT INTO plugin_unbound.attribution_work (flow_id, observed_at, client_id, src_address, dst_address)
SELECT f.id, f.observed_at, f.src_client_id, f.src_address, coalesce(opnview.canonical_address(f.dst_address), f.dst_address)
FROM plugin_pf.flow AS f
WHERE (CAST(:since AS BIGINT) IS NULL
       OR f.observed_at >= CAST(:since AS BIGINT) - GREATEST(CAST(:attribution_max_delay AS BIGINT), CAST(:cache_answer_delay AS BIGINT))
       OR f.changed_at >= CAST(:since AS BIGINT))
  AND f.observed_at <= CAST(:now AS BIGINT)
  AND f.dst_interface_id IS NULL
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg');


-- The attributed flows of the window that are no longer eligible: a destination placed inside or
-- recognised as this firewall since, or a record paired as a second leg since.
-- statement: attribution_drop_ineligible
WITH dropped AS (
    DELETE FROM plugin_unbound.domain_attribution AS a
    USING plugin_pf.flow AS f
    WHERE a.flow_id = f.id
      AND (CAST(:since AS BIGINT) IS NULL
           OR f.observed_at >= CAST(:since AS BIGINT) - GREATEST(CAST(:attribution_max_delay AS BIGINT), CAST(:cache_answer_delay AS BIGINT))
           OR f.changed_at >= CAST(:since AS BIGINT))
      AND f.observed_at <= CAST(:now AS BIGINT)
      AND (f.dst_interface_id IS NOT NULL OR f.dst_is_this_firewall OR f.pair_outcome = 'second_leg')
    RETURNING f.id)
UPDATE plugin_pf.flow AS f
SET changed_at = CAST(:now AS BIGINT)
FROM dropped AS d
WHERE f.id = d.id;


-- THE EXACT EVIDENCE. The A and AAAA records holding the destination and covering the flow's instant
-- are the terminals; the cache's CNAME records pointing at a terminal's name, and at those names in
-- turn, climb to every name the answer was reached from. For each name, the eligible lookups of that
-- name by the client inside the cache window -- from the earliest instant a path covers -- are
-- matched to the earliest observation whose coverage holds the lookup's instant (the local data only
-- for a lookup answered from local data); the latest lookup wins the name, its earliest observation
-- the tie. A flow with exactly one such name is named by it; with several, it is not named at all.
-- statement: attribution_exact_count
WITH RECURSIVE
terminal AS (
    SELECT w.flow_id, o.owner_name AS name, o.covered_from_at AS from_at, o.id AS observation_id, o.held_in,
           o.first_seen_at, o.owner_name AS observation_owner
    FROM plugin_unbound.attribution_work AS w
    JOIN plugin_unbound.resource_record_observation AS o
      ON o.address = w.dst_address AND o.rrtype IN ('A', 'AAAA')
     AND o.covered_until_at >= w.observed_at AND o.covered_from_at <= w.observed_at
),
climb AS (
    SELECT t.flow_id, t.name, t.from_at, t.observation_id, t.held_in, t.first_seen_at, t.observation_owner, 0 AS links
    FROM terminal AS t
    UNION ALL
    SELECT c.flow_id, p.owner_name, GREATEST(c.from_at, p.covered_from_at), c.observation_id, 'cache',
           c.first_seen_at, c.observation_owner, c.links + 1
    FROM climb AS c
    JOIN plugin_unbound.attribution_work AS w ON w.flow_id = c.flow_id
    JOIN plugin_unbound.resource_record_observation AS p
      ON p.rrtype = 'CNAME' AND p.held_in = 'cache' AND p.value = c.name
     AND p.covered_until_at >= w.observed_at AND p.covered_from_at <= w.observed_at
    WHERE c.held_in = 'cache' AND c.links < 11
),
path AS (
    SELECT DISTINCT flow_id, name, from_at, observation_id, held_in, first_seen_at, observation_owner FROM climb
),
earliest AS (SELECT flow_id, name, min(from_at) AS from_at FROM path GROUP BY flow_id, name),
named_lookup AS (
    SELECT e.flow_id, e.name, r.id AS lookup_id, r.looked_up_at, r.lookup_key, r.answer_source, r.domain
    FROM earliest AS e
    JOIN plugin_unbound.attribution_work AS w ON w.flow_id = e.flow_id
    JOIN plugin_unbound.dns_resolution AS r
      ON lower(rtrim(r.domain, '.')) = e.name
     AND ((w.client_id IS NOT NULL AND r.client_id = w.client_id)
          OR (r.client_address = w.src_address AND (r.client_id IS NULL OR w.client_id IS NULL)))
     AND r.looked_up_at >= GREATEST(w.observed_at - CAST(:cache_answer_delay AS BIGINT), e.from_at)
     AND r.looked_up_at <= w.observed_at
     AND r.action = 'pass'
     AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
     AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
),
matched AS (
    SELECT nl.flow_id, nl.name, nl.lookup_id, nl.looked_up_at, nl.lookup_key, nl.domain, p.observation_id,
           row_number() OVER (PARTITION BY nl.flow_id, nl.name, nl.lookup_id
                              ORDER BY p.first_seen_at, p.held_in, p.observation_owner) AS observation_rank
    FROM named_lookup AS nl
    JOIN path AS p ON p.flow_id = nl.flow_id AND p.name = nl.name
     AND p.from_at <= nl.looked_up_at AND (p.held_in = 'cache' OR nl.answer_source = 'Local-data')
),
best AS (
    SELECT m.flow_id, m.name, m.lookup_id, m.looked_up_at, m.domain, m.observation_id,
           row_number() OVER (PARTITION BY m.flow_id, m.name ORDER BY m.looked_up_at DESC, m.lookup_key DESC) AS lookup_rank
    FROM matched AS m
    WHERE m.observation_rank = 1
),
per_flow AS (SELECT flow_id, count(*) AS names FROM best WHERE lookup_rank = 1 GROUP BY flow_id)
UPDATE plugin_unbound.attribution_work AS w
SET exact_names = pf.names
FROM per_flow AS pf
WHERE w.flow_id = pf.flow_id;


-- statement: attribution_exact_pick
WITH RECURSIVE
terminal AS (
    SELECT w.flow_id, o.owner_name AS name, o.covered_from_at AS from_at, o.id AS observation_id, o.held_in,
           o.first_seen_at, o.owner_name AS observation_owner
    FROM plugin_unbound.attribution_work AS w
    JOIN plugin_unbound.resource_record_observation AS o
      ON o.address = w.dst_address AND o.rrtype IN ('A', 'AAAA')
     AND o.covered_until_at >= w.observed_at AND o.covered_from_at <= w.observed_at
    WHERE w.exact_names = 1
),
climb AS (
    SELECT t.flow_id, t.name, t.from_at, t.observation_id, t.held_in, t.first_seen_at, t.observation_owner, 0 AS links
    FROM terminal AS t
    UNION ALL
    SELECT c.flow_id, p.owner_name, GREATEST(c.from_at, p.covered_from_at), c.observation_id, 'cache',
           c.first_seen_at, c.observation_owner, c.links + 1
    FROM climb AS c
    JOIN plugin_unbound.attribution_work AS w ON w.flow_id = c.flow_id
    JOIN plugin_unbound.resource_record_observation AS p
      ON p.rrtype = 'CNAME' AND p.held_in = 'cache' AND p.value = c.name
     AND p.covered_until_at >= w.observed_at AND p.covered_from_at <= w.observed_at
    WHERE c.held_in = 'cache' AND c.links < 11
),
path AS (
    SELECT DISTINCT flow_id, name, from_at, observation_id, held_in, first_seen_at, observation_owner FROM climb
),
earliest AS (SELECT flow_id, name, min(from_at) AS from_at FROM path GROUP BY flow_id, name),
named_lookup AS (
    SELECT e.flow_id, e.name, r.id AS lookup_id, r.looked_up_at, r.lookup_key, r.answer_source, r.domain
    FROM earliest AS e
    JOIN plugin_unbound.attribution_work AS w ON w.flow_id = e.flow_id
    JOIN plugin_unbound.dns_resolution AS r
      ON lower(rtrim(r.domain, '.')) = e.name
     AND ((w.client_id IS NOT NULL AND r.client_id = w.client_id)
          OR (r.client_address = w.src_address AND (r.client_id IS NULL OR w.client_id IS NULL)))
     AND r.looked_up_at >= GREATEST(w.observed_at - CAST(:cache_answer_delay AS BIGINT), e.from_at)
     AND r.looked_up_at <= w.observed_at
     AND r.action = 'pass'
     AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
     AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
),
matched AS (
    SELECT nl.flow_id, nl.name, nl.lookup_id, nl.looked_up_at, nl.lookup_key, nl.domain, p.observation_id,
           row_number() OVER (PARTITION BY nl.flow_id, nl.name, nl.lookup_id
                              ORDER BY p.first_seen_at, p.held_in, p.observation_owner) AS observation_rank
    FROM named_lookup AS nl
    JOIN path AS p ON p.flow_id = nl.flow_id AND p.name = nl.name
     AND p.from_at <= nl.looked_up_at AND (p.held_in = 'cache' OR nl.answer_source = 'Local-data')
),
best AS (
    SELECT m.flow_id, m.lookup_id, m.looked_up_at, m.domain, m.observation_id,
           row_number() OVER (PARTITION BY m.flow_id, m.name ORDER BY m.looked_up_at DESC, m.lookup_key DESC) AS lookup_rank
    FROM matched AS m
    WHERE m.observation_rank = 1
)
UPDATE plugin_unbound.attribution_work AS w
SET lookup_id = b.lookup_id, site_name = b.domain, looked_up_at = b.looked_up_at,
    method = 'resolver_cache_answer', observation_id = b.observation_id
FROM best AS b
WHERE w.flow_id = b.flow_id AND b.lookup_rank = 1;


-- THE TIMING EVIDENCE, for a flow with no exact name: the eligible lookups of the client in the
-- timing window asked exactly one name; its latest lookup names the flow, unless the name's answers
-- at the lookup's instant -- descending the cache's CNAME chain, the local data at the first link
-- only for a lookup answered from it -- hold addresses of the destination's family and not the
-- destination.
-- statement: attribution_timing
WITH RECURSIVE
lookups AS (
    SELECT w.flow_id, r.id, r.domain, r.looked_up_at, r.lookup_key, r.answer_source
    FROM plugin_unbound.attribution_work AS w
    JOIN plugin_unbound.dns_resolution AS r
      ON ((w.client_id IS NOT NULL AND r.client_id = w.client_id)
          OR (r.client_address = w.src_address AND (r.client_id IS NULL OR w.client_id IS NULL)))
     AND r.looked_up_at >= w.observed_at - CAST(:attribution_max_delay AS BIGINT)
     AND r.looked_up_at <= w.observed_at
     AND r.action = 'pass'
     AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
     AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
    WHERE w.exact_names = 0
),
single AS (SELECT flow_id FROM lookups GROUP BY flow_id HAVING count(DISTINCT domain) = 1),
latest AS (
    SELECT DISTINCT ON (l.flow_id) l.flow_id, l.id, l.domain, l.looked_up_at, l.answer_source
    FROM lookups AS l
    JOIN single AS s ON s.flow_id = l.flow_id
    ORDER BY l.flow_id, l.looked_up_at DESC, l.lookup_key DESC
),
descent AS (
    SELECT la.flow_id, lower(rtrim(la.domain, '.')) AS name, 0 AS links FROM latest AS la
    UNION ALL
    SELECT d.flow_id, o.value, d.links + 1
    FROM descent AS d
    JOIN latest AS la ON la.flow_id = d.flow_id
    JOIN plugin_unbound.resource_record_observation AS o
      ON o.owner_name = d.name AND o.rrtype = 'CNAME' AND o.held_in = 'cache'
     AND o.covered_until_at >= la.looked_up_at AND o.covered_from_at <= la.looked_up_at
    WHERE d.links < 11
),
answers AS (
    SELECT DISTINCT d.flow_id, o.address
    FROM descent AS d
    JOIN latest AS la ON la.flow_id = d.flow_id
    JOIN plugin_unbound.resource_record_observation AS o
      ON o.owner_name = d.name AND o.rrtype IN ('A', 'AAAA')
     AND o.covered_until_at >= la.looked_up_at AND o.covered_from_at <= la.looked_up_at
    WHERE o.address IS NOT NULL AND (o.held_in = 'cache' OR (la.answer_source = 'Local-data' AND d.links = 0))
),
family AS (
    SELECT w.flow_id,
           bool_or(family(CAST(a.address AS inet)) = family(CAST(w.dst_address AS inet))) AS same_family,
           bool_or(a.address = w.dst_address) AS includes
    FROM plugin_unbound.attribution_work AS w
    JOIN answers AS a ON a.flow_id = w.flow_id
    WHERE opnview.canonical_address(w.dst_address) IS NOT NULL
    GROUP BY w.flow_id
)
UPDATE plugin_unbound.attribution_work AS w
SET lookup_id = la.id, site_name = la.domain, looked_up_at = la.looked_up_at, method = 'lookup_timing',
    observation_id = NULL
FROM latest AS la
LEFT JOIN family AS f ON f.flow_id = la.flow_id
WHERE w.flow_id = la.flow_id
  AND NOT (coalesce(f.same_family, false) AND NOT coalesce(f.includes, false));


-- The decisions written: an attribution per named flow, replaced only when something of it differs,
-- and none for a flow decided unnamed. Every flow whose attribution changed is stamped for the refresh.
-- statement: attribution_write
WITH written AS (
    INSERT INTO plugin_unbound.domain_attribution (flow_id, dns_resolution_id, site_name, correlation_delay_seconds,
                                                   attributed_at, method, address_observation_id)
    SELECT w.flow_id, w.lookup_id, w.site_name, w.observed_at - w.looked_up_at, CAST(:now AS BIGINT), w.method,
           w.observation_id
    FROM plugin_unbound.attribution_work AS w
    WHERE w.lookup_id IS NOT NULL
    ON CONFLICT (flow_id) DO UPDATE SET
        dns_resolution_id = excluded.dns_resolution_id,
        site_name = excluded.site_name,
        correlation_delay_seconds = excluded.correlation_delay_seconds,
        attributed_at = excluded.attributed_at,
        method = excluded.method,
        address_observation_id = excluded.address_observation_id
    WHERE domain_attribution.dns_resolution_id IS DISTINCT FROM excluded.dns_resolution_id
       OR domain_attribution.site_name IS DISTINCT FROM excluded.site_name
       OR domain_attribution.correlation_delay_seconds IS DISTINCT FROM excluded.correlation_delay_seconds
       OR domain_attribution.method IS DISTINCT FROM excluded.method
       OR domain_attribution.address_observation_id IS DISTINCT FROM excluded.address_observation_id
    RETURNING flow_id)
UPDATE plugin_pf.flow AS f
SET changed_at = CAST(:now AS BIGINT)
FROM written AS x
WHERE f.id = x.flow_id;


-- statement: attribution_remove
WITH removed AS (
    DELETE FROM plugin_unbound.domain_attribution AS a
    USING plugin_unbound.attribution_work AS w
    WHERE a.flow_id = w.flow_id AND w.lookup_id IS NULL
    RETURNING a.flow_id)
UPDATE plugin_pf.flow AS f
SET changed_at = CAST(:now AS BIGINT)
FROM removed AS x
WHERE f.id = x.flow_id;


-- The work table is left empty between runs.
-- statement: attribution_stamp
DELETE FROM plugin_unbound.attribution_work;
