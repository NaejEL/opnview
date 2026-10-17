-- opnview — the statements run by internal/store/reads/read.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.

-- statement: read_volume_slots
SELECT src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction,
       exit_leg_device,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction,
         exit_leg_device;


-- statement: read_volume_flows
SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope,
       f.traffic_direction, f.exit_leg_device,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
GROUP BY f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope,
         f.traffic_direction, f.exit_leg_device;


-- ---------------------------------------------------------------------------
-- The same two reads of the `netflow` measure (PS22 of the NetFlow join amendment): the same rows
-- and the same connections, with the bytes the NetFlow export reported for them in place of the
-- logged bytes, then the thirty-six columns of netflow_parts (schema.sql): per action, the packets,
-- the coverage classes and the direction states met. The slots carry them in their netflow_*
-- columns; at the edges a flow's figure is its connection's, read through connection_netflow and
-- counted as the aggregate refresh counts it
-- (aggregate.statements.sql, insert_volume): on the first leg's row, and on the second leg's row
-- too where the figure was measured at that leg. The two measures are never summed.
-- ---------------------------------------------------------------------------

-- statement: read_volume_slots_netflow
SELECT src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction,
       exit_leg_device,
       sum(netflow_bytes), sum(netflow_allowed_bytes), sum(netflow_blocked_bytes), sum(netflow_unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections),
       (netflow_sum(netflow_parts)).*
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction,
         exit_leg_device;


-- statement: read_volume_flows_netflow
SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope,
       f.traffic_direction, f.exit_leg_device,
       sum(n.bytes),
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END),
       (netflow_sum(n.parts)).*
FROM classified_flow AS f
LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
GROUP BY f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope,
         f.traffic_direction, f.exit_leg_device;


-- statement: read_earliest_slot
SELECT min(period_start_at) FROM volume_aggregate_1h;


-- statement: read_earliest_flow
SELECT min(observed_at) FROM flow;


-- The intervals the filter log is known not to cover, overlapping a window.
-- statement: read_firewall_log_gaps
SELECT g.interval_start_at, g.interval_end_at
FROM collection_gap AS g
WHERE g.provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')
  AND g.interval_start_at < :to
  AND g.interval_end_at >= :from;


-- ---------------------------------------------------------------------------
-- The connection tree: every flow of a window, grouped by every key a level of
-- either side reads, so each flow lands in exactly one node per level.
-- ---------------------------------------------------------------------------

-- statement: read_connection_tree
SELECT f.local_interface_id,
       f.local_client_id,
       c.owner_id,
       f.traffic_direction,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END,
       g.asn,
       a.site_name,
       count(*),
       sum(f.packet_bytes),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END)
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7;


-- ---------------------------------------------------------------------------
-- Site names: the attribution rate, and per-site totals from the domain family.
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


-- statement: read_site_totals
SELECT site_name,
       sum(bytes),
       sum(allowed_connections + blocked_connections + unknown_connections),
       count(DISTINCT client_id)
FROM domain_volume_aggregate_@period@
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY site_name;


-- ---------------------------------------------------------------------------
-- People and machines over several slots. Summable figures are summed; the
-- distinct clients of an owner are counted from the client family, whose key
-- carries the client, so a client active in two slots counts once.
-- ---------------------------------------------------------------------------

-- statement: read_owner_totals
SELECT owner_id,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM owner_volume_aggregate_@period@
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY owner_id;


-- statement: read_owner_client_counts
SELECT c.owner_id, count(DISTINCT v.client_id)
FROM client_volume_aggregate_@period@ AS v
JOIN client AS c ON c.id = v.client_id
WHERE v.period_start_at >= :from
  AND v.period_start_at < :to
GROUP BY c.owner_id;


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

-- statement: read_blocked_decision_counts
SELECT engine_kind, count(*)
FROM blocked_decision
WHERE occurred_at >= :from
  AND occurred_at < :to
GROUP BY engine_kind;


-- statement: read_rules
SELECT id, interface, legacy, logs_matches FROM rule ORDER BY id;


-- statement: read_interfaces
SELECT id, identifier, device, is_upstream FROM interface ORDER BY id;


-- statement: read_interface_addresses
SELECT source_field, address, prefix_length, address_family, first_seen_at, last_seen_at
FROM interface_address
WHERE interface_id = :interface_id
ORDER BY last_seen_at DESC, first_seen_at DESC, id;

