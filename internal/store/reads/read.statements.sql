-- opnview — the statements run by internal/store/reads/read.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.

-- ---------------------------------------------------------------------------
-- Site names: the attribution rate.
-- ---------------------------------------------------------------------------

-- The eligible flows: those a client sent to an outside destination. A flow with
-- no source client -- both ends outside -- was sent by nobody the resolver could
-- have named a site for. A flow from or to this firewall is not a client's, and a
-- second leg is the same connection as its first leg, which is the one counted.
-- The diagnostic "Attribution rate per client" counts the same flows.
-- The named flows are counted per method as well, and the two counts sum to the named
-- total because the method column admits exactly two values.
-- statement: read_attribution_rate
SELECT count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND NOT f.src_is_this_firewall
  AND NOT f.dst_is_this_firewall
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (CAST(:client_id AS BIGINT) IS NULL OR f.src_client_id = :client_id);


-- The evidence that a resolver source answered for [from, to]: a lookup stored
-- for an instant inside the window -- the source evidently answered for it -- or
-- a dns_lookup availability found reachable by a probe inside the window. The
-- availability row holds only the latest probe, so a past window is judged by its
-- lookups alone.
-- statement: read_lookup_coverage
SELECT (SELECT count(*) FROM (SELECT 1 FROM dns_resolution AS r
                              WHERE r.looked_up_at >= :from
                                AND r.looked_up_at <= :to
                              LIMIT 1))
     + (SELECT count(*)
        FROM source_availability AS s
        JOIN provider AS p ON p.id = s.provider_id
        WHERE p.kind = 'dns_lookup'
          AND s.state = 'reachable'
          AND s.checked_at >= :from
          AND s.checked_at <= :to);


-- The evidence that the resolver's cache was read for [from, to]: a record of the cache
-- whose coverage interval meets the window. A poll stores every record the cache held,
-- so a window no observation meets is one no successful poll covered -- or one in
-- which the cache held nothing, which a working resolver does not do.
-- statement: read_cache_coverage
SELECT count(*)
FROM (SELECT 1
      FROM resource_record_observation AS o
      WHERE o.covered_until_at >= :from
        AND o.covered_from_at <= :to
        AND o.held_in = 'cache'
      LIMIT 1);


-- ---------------------------------------------------------------------------
-- The clients an interface has seen.
-- ---------------------------------------------------------------------------

-- statement: read_interface_seen_clients
SELECT f.local_interface_id, count(DISTINCT f.local_client_id)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_interface_id;


-- statement: read_interface_known_clients
SELECT count(*)
FROM client
WHERE interface_id = :interface_id
  AND last_seen_at >= :from;


-- ---------------------------------------------------------------------------
-- The decisions, the rules and the installation itself.
-- ---------------------------------------------------------------------------

-- statement: read_rules
SELECT id, interface, legacy, logs_matches FROM rule ORDER BY id;


-- statement: read_interfaces
SELECT id, identifier, device, is_upstream FROM interface ORDER BY id;


-- statement: read_interface_addresses
SELECT source_field, address, prefix_length, address_family, first_seen_at, last_seen_at
FROM interface_address
WHERE interface_id = :interface_id
ORDER BY last_seen_at DESC, first_seen_at DESC, id;

