-- opnview — the read statements step 5B's widget API consumes.
--
-- Embedded into the binary by internal/store (statements.go, read.go) and read
-- by sql/schema-checks.sh, which records the EXPLAIN QUERY PLAN of every
-- statement at both seed sizes and fails if one scans a growing table. The same
-- conventions as derive.sql: a "-- statement: <name>" line before each, a
-- semicolon after, @period@ for the aggregate period, named parameters only:
--   :from, :to       a window, [from, to) unless a statement says otherwise
--   :since, :now     a sampling bound
--   :interface_id, :client_id, :device, :measure, :sampled_at
--
-- Observation-point limit: every volume below counts only traffic that crossed
-- the firewall; traffic between two clients behind one interface is in none of
-- them.

-- ---------------------------------------------------------------------------
-- Volumes over a rolling window: whole hours from the 1 h slots, the partial
-- hours at either edge from `flow`.
-- ---------------------------------------------------------------------------

-- statement: read_volume_slots
SELECT src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction;

-- statement: read_volume_flows
SELECT f.src_interface_id, f.dst_interface_id, f.peer_address, f.traffic_scope,
       f.traffic_direction,
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
         f.traffic_direction;

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
GROUP BY 1, 2, 3, 4, 5, 6, 7;

-- ---------------------------------------------------------------------------
-- Site names: the attribution rate, and per-site totals from the domain family.
-- ---------------------------------------------------------------------------

-- The eligible flows: those a client sent to an outside destination. A flow with
-- no source client -- both ends outside, an unsolicited packet to the firewall's
-- own address -- was sent by nobody the resolver could have named a site for.
-- The diagnostic "Attribution rate per client" counts the same flows.
-- statement: read_attribution_rate
SELECT count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND (:client_id IS NULL OR f.src_client_id = :client_id);

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

-- ---------------------------------------------------------------------------
-- Samples: the current per-pair rates, the bytes seen in samples, and the
-- per-interface counters.
-- ---------------------------------------------------------------------------

-- statement: read_pair_rates
-- The rates of both sampled subjects: the totals of each local address
-- (interface_endpoint) and the per-peer figures (interface_endpoint_pair).
SELECT subject_kind, subject_key, measure, value, sampled_at
FROM measurement_sample
WHERE sampled_at >= :since
  AND sampled_at <= :now
  AND subject_kind IN ('interface_endpoint', 'interface_endpoint_pair')
  AND measure IN ('rate_bits_in', 'rate_bits_out');

-- statement: read_sampled_pair_bytes
-- The bytes seen in samples, per device, from the totals of each local address:
-- a local address's outbound bytes exist only as that total.
SELECT substr(subject_key, 1, instr(subject_key, ' ') - 1),
       sampled_at,
       sum(value)
FROM measurement_sample
WHERE sampled_at >= :from
  AND sampled_at < :to
  AND subject_kind = 'interface_endpoint'
  AND measure IN ('cumulative_bytes_in', 'cumulative_bytes_out')
GROUP BY 1, 2;

-- statement: read_interface_counter
SELECT sampled_at, value
FROM measurement_sample
WHERE subject_kind = 'interface'
  AND subject_key = :device
  AND measure = :measure
  AND sampled_at >= :from
  AND sampled_at < :to
ORDER BY sampled_at;

-- statement: read_address_level_client
SELECT id
FROM client
WHERE last_address = :address
  AND identity_kind = 'address_in_interface'
ORDER BY last_seen_at DESC, id DESC
LIMIT 1;

-- statement: read_client_owner
SELECT owner_id FROM client WHERE id = :client_id;

-- statement: read_is_firewall_address
SELECT 1
FROM interface_address
WHERE address = :address
  AND source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')
LIMIT 1;
