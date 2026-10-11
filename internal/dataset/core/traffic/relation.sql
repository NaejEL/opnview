-- The filter log's flows, one row each: the logged packet length as its bytes, and the connection
-- it records split by its action. The second leg of a paired connection is the same connection seen
-- on its second interface, so it is left out here as every volume figure leaves it out
-- (docs/data-model.md, "Pairing"). The interfaces are named by their OPNsense identifier.
SELECT f.observed_at,
       si.identifier AS src_interface,
       di.identifier AS dst_interface,
       f.src_client_id,
       f.dst_client_id,
       f.rule_id,
       f.protocol,
       f.ip_version,
       f.action,
       f.direction,
       f.dst_port,
       f.packet_bytes AS bytes,
       1 AS connections,
       CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END AS allowed,
       CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked
FROM flow AS f
LEFT JOIN interface AS si ON si.id = f.src_interface_id
LEFT JOIN interface AS di ON di.id = f.dst_interface_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.pair_outcome IS DISTINCT FROM 'second_leg'
