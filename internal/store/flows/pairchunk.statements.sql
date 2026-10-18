-- opnview — the statements run by internal/store/flows/pairchunk.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- TWO LEGS OF ONE CONNECTION. pf logs the packet that creates a state on every
-- interface a rule with `log` creates one on, so a connection crossing the
-- firewall is logged `in` on the interface it arrived on and `out` on the one it
-- left by (pf.conf(5), `log`; survey, "Two legs of one connection, read for the
-- step-5A live corrections"). An `out` record is the SECOND LEG of an `in`
-- record on another interface when both carry the same protocol, IP version,
-- destination address and destination port, every logged field verified to
-- survive the crossing unchanged (ip_id, tcp_seq), their instants lie within
-- :window seconds, the `in` record passed, and each is the other's only
-- candidate. The sources differ under outbound NAT, where the second leg's source
-- is this firewall, and are equal without it, with their ports.
--
-- The decision is a function of the stored records, so it does not depend on the
-- order they arrived in, it is taken again when either leg arrives later, and it
-- writes nothing when nothing changed (internal/store/flows/pair.go).
-- ---------------------------------------------------------------------------

-- The `out` records of a window, with their pairing fields and what is recorded.
-- statement: pairing_out_records
SELECT id, observed_at, interface_device, src_address, src_port, dst_address, dst_port,
       protocol, ip_version, ip_id, tcp_seq, src_is_this_firewall, pair_outcome, paired_flow_id
FROM flow
WHERE observed_at >= :from
  AND observed_at <= :to
  AND direction = 'out'
ORDER BY id;


-- The `in` records one `out` record can be the second leg of. Two are enough to
-- know it has more than one.
-- statement: pairing_in_candidates
-- plan-with: src_is_this_firewall=false
SELECT i.id
FROM flow AS i
WHERE i.dst_address = :dst_address
  AND i.observed_at >= CAST(:at AS BIGINT) - CAST(:window AS BIGINT)
  AND i.observed_at <= CAST(:at AS BIGINT) + CAST(:window AS BIGINT)
  AND i.direction = 'in'
  AND i.action = 'pass'
  AND i.interface_device <> :device
  AND i.protocol = :protocol
  AND i.ip_version = :ip_version
  AND i.dst_port IS NOT DISTINCT FROM :dst_port
  AND i.ip_id IS NOT DISTINCT FROM :ip_id
  AND i.tcp_seq IS NOT DISTINCT FROM :tcp_seq
  AND NOT i.src_is_this_firewall
  AND (:src_is_this_firewall OR (i.src_address = :src_address AND i.src_port IS NOT DISTINCT FROM :src_port))
ORDER BY i.id
LIMIT 2;


-- The `out` records one `in` record can be the first leg of, the same test seen
-- from the other side.
-- statement: pairing_out_candidates
SELECT o.id
FROM flow AS o
WHERE o.dst_address = :dst_address
  AND o.observed_at >= CAST(:at AS BIGINT) - CAST(:window AS BIGINT)
  AND o.observed_at <= CAST(:at AS BIGINT) + CAST(:window AS BIGINT)
  AND o.direction = 'out'
  AND o.interface_device <> :device
  AND o.protocol = :protocol
  AND o.ip_version = :ip_version
  AND o.dst_port IS NOT DISTINCT FROM :dst_port
  AND o.ip_id IS NOT DISTINCT FROM :ip_id
  AND o.tcp_seq IS NOT DISTINCT FROM :tcp_seq
  AND (o.src_is_this_firewall OR (o.src_address = :src_address AND o.src_port IS NOT DISTINCT FROM :src_port))
ORDER BY o.id
LIMIT 2;


-- The pairing fields of one record, read for the `in` side of a candidate.
-- statement: pairing_record_fields
SELECT id, observed_at, interface_device, src_address, src_port, dst_address, dst_port,
       protocol, ip_version, ip_id, tcp_seq, src_is_this_firewall, pair_outcome, paired_flow_id
FROM flow
WHERE id = :flow_id;


-- One record's outcome, written only when it changes.
-- statement: pairing_record
UPDATE flow
SET pair_outcome = :outcome,
    paired_flow_id = :partner
WHERE id = :flow_id
  AND (pair_outcome IS DISTINCT FROM :outcome OR paired_flow_id IS DISTINCT FROM :partner)
RETURNING observed_at;

