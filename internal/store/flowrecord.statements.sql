-- opnview — the statements run by internal/store/flowrecord.go: the NetFlow join and the measured
-- volume of the logged connections (PS17-PS20 of the NetFlow join amendment to
-- specs/SPEC-backend-completion-plugins.md; docs/data-model.md, "NetFlow volumes").
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters. The parameters of this file:
--   :provider_id     the flow_record provider
--   :before          a start_at bound: records starting before it
--   :device, :tuple_key  one group: an Observation Point's device and an unordered tuple
--   :address_a, :address_b  the two addresses of a record, in its order
--   :ip_version, :latest  a leg's version, and the latest observed_at a candidate may carry
--   :identity, :start_at  one flow record
--   :join_state, :joined_flow_id, :orientation, :unmatched_reason, :decided_at  its decision
--   :from, :to       an observed_at window, both ends included
--   :ids             flow ids, as decimal text separated by commas
--   :due             the filter log's read-through instant, by which an open measured volume is due
-- No address, CIDR, interface identifier, device name or description appears below.


-- The groups holding a record still to decide that started before a bound: one Observation Point's
-- device and one unordered tuple each.
-- statement: flow_record_pending_groups
SELECT DISTINCT r.observation_point_device, r.tuple_key
FROM flow_record AS r
WHERE r.provider_id = :provider_id
  AND r.join_state = 'pending'
  AND r.start_at < :before
ORDER BY r.observation_point_device, r.tuple_key;


-- Every record of one group with an Observation Point, oldest first: the join decides a group whole,
-- so its result does not depend on the order the records arrived in.
-- statement: flow_record_group
SELECT r.identity, r.start_at, r.start_millisecond, r.end_at, r.end_millisecond, r.src_address,
       r.dst_address, r.src_port, r.dst_port, r.protocol, r.tcp_flags, r.direction, r.join_state,
       r.joined_flow_id, r.orientation, r.unmatched_reason, r.decided_at, r.observation_point_identifier,
       r.ip_version, r.octets, r.packets
FROM flow_record AS r
WHERE r.observation_point_device = :device
  AND r.tuple_key = :tuple_key
  AND r.provider_id = :provider_id
  AND r.capture_state = 'observation_point'
ORDER BY r.start_at, r.start_millisecond, r.identity;


-- The legs a record may join (PS19 rule 2): the filter-log records logged on the Observation Point's
-- device, of the record's version, whose two addresses are the record's in either orientation --
-- compared as addresses, never as text -- logged at or before the latest instant it may join.
-- statement: flow_record_join_legs
SELECT f.id, f.observed_at, f.src_address, f.dst_address, f.src_port, f.dst_port, f.protocol,
       f.direction, f.action
FROM flow AS f
WHERE f.interface_device = :device
  AND ((canonical_address(f.src_address) = canonical_address(:address_a)
        AND canonical_address(f.dst_address) = canonical_address(:address_b))
    OR (canonical_address(f.src_address) = canonical_address(:address_b)
        AND canonical_address(f.dst_address) = canonical_address(:address_a)))
  AND f.ip_version = :ip_version
  AND f.observed_at <= :latest
ORDER BY f.observed_at, f.id;


-- One record's decision, written only when it differs from what is stored: a second derivation of
-- the same rows writes nothing.
-- statement: flow_record_decide
UPDATE flow_record
SET join_state = :join_state,
    joined_flow_id = :joined_flow_id,
    orientation = :orientation,
    unmatched_reason = :unmatched_reason,
    decided_at = :decided_at
WHERE identity = :identity
  AND start_at = :start_at
  AND (join_state IS DISTINCT FROM :join_state
       OR joined_flow_id IS DISTINCT FROM :joined_flow_id
       OR orientation IS DISTINCT FROM :orientation
       OR unmatched_reason IS DISTINCT FROM :unmatched_reason);


-- The connections whose first (or single) leg was logged in a window, each with its second leg when
-- the pairing found one: what a connection's measured volume is computed from (PS20).
-- statement: measured_volume_connections_between
SELECT f.id, f.observed_at, f.interface_device, f.direction, f.action, s.id, s.interface_device,
       s.direction, s.action
FROM flow AS f
LEFT JOIN flow AS s ON f.pair_outcome = 'first_leg' AND s.id = f.paired_flow_id
WHERE f.observed_at >= :from
  AND f.observed_at <= :to
  AND f.pair_outcome IS DISTINCT FROM 'second_leg'
ORDER BY f.id;


-- The same, for named legs: a first or single leg names itself, a second leg its first.
-- statement: measured_volume_connections_of
SELECT f.id, f.observed_at, f.interface_device, f.direction, f.action, s.id, s.interface_device,
       s.direction, s.action
FROM flow AS f
LEFT JOIN flow AS s ON f.pair_outcome = 'first_leg' AND s.id = f.paired_flow_id
WHERE f.id IN (SELECT CASE WHEN n.pair_outcome = 'second_leg' THEN n.paired_flow_id ELSE n.id END
               FROM flow AS n
               WHERE n.id = ANY (CAST(string_to_array(:ids, ',') AS BIGINT[])))
  AND f.pair_outcome IS DISTINCT FROM 'second_leg'
ORDER BY f.id;


-- The records joined to some legs: the figures of their connections.
-- statement: measured_volume_joined_records
SELECT r.joined_flow_id, r.orientation, r.octets, r.packets, r.start_at, r.end_at, r.tcp_flags, r.protocol
FROM flow_record AS r
WHERE r.joined_flow_id = ANY (CAST(string_to_array(:ids, ',') AS BIGINT[]))
ORDER BY r.joined_flow_id, r.start_at, r.identity;


-- The connections whose measured volume is due to change by time alone: open, and due by the filter
-- log's read-through instant :due.
-- statement: measured_volume_open
SELECT DISTINCT v.connection_flow_id
FROM connection_measured_volume AS v
WHERE v.final = 0
  AND v.due_at <= :due
ORDER BY v.connection_flow_id;
