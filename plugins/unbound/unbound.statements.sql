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
