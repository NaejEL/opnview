-- opnview — the statements run by internal/store/attribution/attribute.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- SITE-NAME ATTRIBUTION. The query report carries no answer address and the
-- resolver's cache does, so an attribution is exact evidence first -- a lookup
-- whose name the cache shows answering with the flow's destination over the whole
-- interval from the lookup to the flow -- and, failing that, a lookup and a flow by
-- the same client, close in time (internal/store/attribution/attribute.go holds the rule). A
-- flow is eligible when its destination is outside -- not inside, and not this
-- firewall -- and it is not the second record of a connection whose first record
-- is counted (flow.pair_outcome 'second_leg').
-- ---------------------------------------------------------------------------

-- statement: attribution_candidates
SELECT id, observed_at, src_client_id, src_address, dst_address
FROM flow
WHERE observed_at >= :from
  AND observed_at <= :to
  AND dst_interface_id IS NULL
  AND NOT dst_is_this_firewall
  AND (pair_outcome IS NULL OR pair_outcome <> 'second_leg')
ORDER BY id;


-- The attributed flows of the window that are no longer eligible: a destination
-- placed inside or recognised as this firewall since, or a record paired as a
-- second leg since. Their attribution is removed.
-- statement: attribution_ineligible
SELECT f.id, f.observed_at
FROM flow AS f
JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at <= :to
  AND (f.dst_interface_id IS NOT NULL
       OR f.dst_is_this_firewall
       OR f.pair_outcome = 'second_leg')
ORDER BY f.id;


-- "The same client": the same client_id where both rows carry one, the same
-- address otherwise. Eligible: a passed lookup answered by recursion, from
-- cache, or from local data such as a host override, whose client named one
-- address -- logged as one, or a host name a lease or the local data resolved.
-- statement: attribution_lookups
SELECT r.id, r.domain, r.looked_up_at, r.lookup_key, r.answer_source
FROM dns_resolution AS r
WHERE r.client_id = :client_id
  AND r.looked_up_at >= :start
  AND r.looked_up_at <= :end
  AND r.action = 'pass'
  AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
UNION ALL
SELECT r.id, r.domain, r.looked_up_at, r.lookup_key, r.answer_source
FROM dns_resolution AS r
WHERE r.client_address = :address
  AND r.looked_up_at >= :start
  AND r.looked_up_at <= :end
  AND r.action = 'pass'
  AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
  AND (r.client_id IS NULL OR CAST(:client_id AS BIGINT) IS NULL);


-- ---------------------------------------------------------------------------
-- THE RESOLVER'S RECORDS, the evidence the resolver-cache attribution reads. A
-- record covers an instant when its coverage interval holds it. Names are
-- compared as the records store them (store.DNSName): lower-cased, no trailing dot.
-- ---------------------------------------------------------------------------

-- The A and AAAA records holding one address and covering an instant: the names a
-- flow's destination was an answer of, in the cache or in the local data.
-- statement: resource_records_of_address
SELECT o.id, o.held_in, o.owner_name, o.rrtype, o.value, o.address,
       o.covered_from_at, o.covered_until_at, o.first_seen_at
FROM resource_record_observation AS o
WHERE o.address = :address
  AND o.rrtype IN ('A', 'AAAA')
  AND o.covered_until_at >= :at
  AND o.covered_from_at <= :at;


-- The cache's CNAME records pointing at one name and covering an instant: one link
-- of the climb from an answer's name to the name a client looked up.
-- statement: resource_record_cname_owners
SELECT o.id, o.held_in, o.owner_name, o.rrtype, o.value, o.address,
       o.covered_from_at, o.covered_until_at, o.first_seen_at
FROM resource_record_observation AS o
WHERE o.rrtype = 'CNAME'
  AND o.value = :name
  AND o.covered_until_at >= :at
  AND o.covered_from_at <= :at;


-- The eligible lookups of one name by one client inside a window: what an answer's
-- name is matched against. The name is compared through the expression the indexes
-- below index.
--
-- TWO BRANCHES, EACH AN INDEX SEARCH BOUNDED BY NAME AND CLIENT (B2 of
-- specs/SPEC-step-5b-widget-api.md). The client is the lookup's client_id when the flow
-- has one, and its address when either side has no client_id. Written as one OR, the
-- planner could bound only the name and filtered every lookup of a popular name by
-- client afterwards; split into two branches, the first searches
-- idx_dns_resolution_name_client_id on (name, client_id, looked_up_at) and the second
-- idx_dns_resolution_name_client_address on (name, client_address, looked_up_at). The
-- address branch got an index of its own rather than a rewrite on the existing
-- (client_address, looked_up_at) index, because that one is bounded by client and not
-- by name: a client's every lookup in the window would be read to find one name. The
-- branches are disjoint -- the first needs r.client_id = :client_id, so a non-NULL
-- client_id on both sides, and the second needs one of them NULL -- so UNION ALL
-- returns each lookup once.
-- statement: attribution_name_lookups
SELECT r.id, r.domain, r.looked_up_at, r.lookup_key, r.answer_source
FROM dns_resolution AS r
WHERE lower(rtrim(r.domain, '.')) = :name
  AND r.client_id = :client_id
  AND r.looked_up_at >= :start
  AND r.looked_up_at <= :end
  AND r.action = 'pass'
  AND r.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND r.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname')
UNION ALL
SELECT a.id, a.domain, a.looked_up_at, a.lookup_key, a.answer_source
FROM dns_resolution AS a
WHERE lower(rtrim(a.domain, '.')) = :name
  AND a.client_address = :address
  AND a.looked_up_at >= :start
  AND a.looked_up_at <= :end
  AND (a.client_id IS NULL OR CAST(:client_id AS BIGINT) IS NULL)
  AND a.action = 'pass'
  AND a.answer_source IN ('Recursion', 'Cache', 'Local-data')
  AND a.client_resolution IN ('logged_address', 'lease_hostname', 'local_data_hostname');


-- statement: attribution_upsert
INSERT INTO domain_attribution (flow_id, dns_resolution_id, site_name,
                                correlation_delay_seconds, attributed_at, method,
                                address_observation_id)
VALUES (:flow_id, :lookup_id, :site_name, :delay, :now, :method, :observation_id)
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
   OR domain_attribution.address_observation_id IS DISTINCT FROM excluded.address_observation_id;


-- statement: attribution_delete
DELETE FROM domain_attribution WHERE flow_id = :flow_id;

