-- The flow records an exporter sent, one row each, as stored (PS17 of the NetFlow join amendment, and NJ8
-- of its amendment "the NetFlow join with every interface captured"): every record, with what its two
-- indexes named at reception -- an interface's identifier, or the role of an index that names none --
-- loss_detectable, 1 over the intervals whose configuration bounded the exporter's sequence counters,
-- and its join state: joined, a copy (translated_copy or parent_copy, counted_copy 1 on a translated
-- copy a connection's figure counts), unmatched with its reason, or pending. A packet can be exported
-- more than once -- by the node of the interface it arrived on, by the node of the interface it left by
-- in its translated form, by its VLAN's parent's node -- so a sum over records that are not joined counts
-- copies.
SELECT r.start_at,
       r.end_at,
       r.exporter_address,
       r.observation_domain,
       r.ingress_interface,
       r.egress_interface,
       r.ingress_role,
       r.ingress_identifier,
       r.egress_role,
       r.egress_identifier,
       r.ip_version,
       r.protocol,
       r.src_address,
       r.dst_address,
       r.src_port,
       r.dst_port,
       r.loss_detectable,
       r.join_state,
       r.copy_kind,
       r.counted_copy,
       r.unmatched_reason,
       r.octets,
       r.packets,
       1 AS records
FROM flow_record AS r
WHERE r.start_at >= :from
  AND r.start_at < :to
