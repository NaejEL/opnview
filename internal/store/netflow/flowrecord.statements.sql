-- opnview — the statements run by internal/store/netflow: the NetFlow join and the measured volume of the
-- logged connections (PS17 of the NetFlow join amendment to specs/SPEC-backend-completion-plugins.md, and
-- NJ4 and NJ5 of its amendment "the NetFlow join with every interface captured"; docs/data-model.md,
-- "NetFlow volumes").
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters. The parameters of this file:
--   :provider_id     the flow_record provider
--   :before          a start_at bound: records starting before it
--   :tuple_key       one group: an unordered tuple
--   :devices         the devices a group's legs are searched on, joined by chr(31)
--   :address_a, :address_b  the two addresses of a record, in its order
--   :ip_version, :latest  a leg's version, and the latest observed_at a candidate may carry
--   :identity, :start_at  one flow record
--   :join_state, :joined_flow_id, :copy_kind, :copy_flow_id, :orientation, :unmatched_reason,
--   :decided_at      its decision
--   :from, :to       an observed_at window, both ends included
--   :ids             flow ids, as decimal text separated by commas
--   :due             the filter log's read-through instant, by which an open measured volume is due
-- No address, CIDR, interface identifier, device name or description appears below.


-- The groups holding a record still to decide that started before a bound: one unordered tuple each.
-- statement: flow_record_pending_groups
SELECT DISTINCT r.tuple_key
FROM flow_record AS r
WHERE r.provider_id = :provider_id
  AND r.join_state = 'pending'
  AND r.start_at < :before
ORDER BY r.tuple_key;


-- Every record of one group, oldest first: the join decides a group whole, so its result does not
-- depend on the order the records arrived in.
-- statement: flow_record_group
SELECT r.identity, r.start_at, r.start_millisecond, r.end_at, r.end_millisecond, r.src_address,
       r.dst_address, r.src_port, r.dst_port, r.protocol, r.ip_version, r.tcp_flags, r.octets, r.packets,
       r.ingress_interface, r.egress_interface, r.ingress_role, r.ingress_identifier, r.ingress_device,
       r.egress_role, r.egress_identifier, r.egress_device, r.join_state, r.joined_flow_id, r.copy_kind,
       r.copy_flow_id, r.orientation, r.unmatched_reason, r.decided_at
FROM flow_record AS r
WHERE r.tuple_key = :tuple_key
  AND r.provider_id = :provider_id
ORDER BY r.start_at, r.start_millisecond, r.identity;


-- The legs a group's records may join or match (NJ4): the filter-log records logged on the devices its
-- records name, of its version, whose two addresses are the records' in either orientation --
-- compared as addresses, never as text -- logged at or before the latest instant one may join. One
-- index search of idx_flow_join per device.
-- statement: flow_record_join_legs
SELECT f.id, f.observed_at, f.interface_device, f.src_address, f.dst_address, f.src_port, f.dst_port,
       f.protocol, f.direction, f.action, f.pair_outcome
FROM flow AS f
WHERE f.interface_device = ANY (string_to_array(:devices, chr(31)))
  AND ((canonical_address(f.src_address) = canonical_address(:address_a)
        AND canonical_address(f.dst_address) = canonical_address(:address_b))
    OR (canonical_address(f.src_address) = canonical_address(:address_b)
        AND canonical_address(f.dst_address) = canonical_address(:address_a)))
  AND f.ip_version = :ip_version
  AND f.observed_at <= :latest
ORDER BY f.observed_at, f.id;


-- One record's decision. A translated copy keeps counted_copy while it stays the copy of the same leg
-- in the same orientation; every other change clears it, for the measured volume to set again.
-- statement: flow_record_decide
UPDATE flow_record
SET join_state = :join_state,
    joined_flow_id = :joined_flow_id,
    copy_kind = :copy_kind,
    copy_flow_id = :copy_flow_id,
    orientation = :orientation,
    unmatched_reason = :unmatched_reason,
    decided_at = :decided_at,
    counted_copy = CASE WHEN copy_flow_id IS NOT DISTINCT FROM :copy_flow_id
                             AND orientation IS NOT DISTINCT FROM :orientation
                        THEN counted_copy ELSE 0 END
WHERE identity = :identity
  AND start_at = :start_at
  AND (join_state IS DISTINCT FROM :join_state
       OR joined_flow_id IS DISTINCT FROM :joined_flow_id
       OR copy_kind IS DISTINCT FROM :copy_kind
       OR copy_flow_id IS DISTINCT FROM :copy_flow_id
       OR orientation IS DISTINCT FROM :orientation
       OR unmatched_reason IS DISTINCT FROM :unmatched_reason);


-- The connections whose first (or single) leg was logged in a window, each with its second leg when
-- the pairing found one: what a connection's measured volume is computed from (NJ5).
-- statement: measured_volume_connections_between
SELECT f.id, f.observed_at, f.interface_device, f.direction, f.action, f.src_address, f.dst_address,
       f.src_port, f.dst_port, f.dst_is_this_firewall, s.id, s.interface_device, s.direction, s.action,
       s.src_address, s.dst_address, s.src_port, s.dst_port
FROM flow AS f
LEFT JOIN flow AS s ON f.pair_outcome = 'first_leg' AND s.id = f.paired_flow_id
WHERE f.observed_at >= :from
  AND f.observed_at <= :to
  AND f.pair_outcome IS DISTINCT FROM 'second_leg'
ORDER BY f.id;


-- The same, for named legs: a first or single leg names itself, a second leg its first.
-- statement: measured_volume_connections_of
SELECT f.id, f.observed_at, f.interface_device, f.direction, f.action, f.src_address, f.dst_address,
       f.src_port, f.dst_port, f.dst_is_this_firewall, s.id, s.interface_device, s.direction, s.action,
       s.src_address, s.dst_address, s.src_port, s.dst_port
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


-- The translated copies matched to some second legs: what NJ5's sole-copy rule counts.
-- statement: measured_volume_copy_records
SELECT r.copy_flow_id, r.orientation, r.octets, r.packets, r.start_at, r.end_at, r.tcp_flags, r.protocol
FROM flow_record AS r
WHERE r.copy_flow_id = ANY (CAST(string_to_array(:ids, ',') AS BIGINT[]))
ORDER BY r.copy_flow_id, r.start_at, r.identity;


-- The connections whose measured volume is due to change by time alone: open, and due by the filter
-- log's read-through instant :due.
-- statement: measured_volume_open
SELECT DISTINCT v.connection_flow_id
FROM connection_measured_volume AS v
WHERE v.final = 0
  AND v.due_at <= :due
ORDER BY v.connection_flow_id;
