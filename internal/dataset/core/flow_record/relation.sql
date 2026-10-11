-- The flow records an exporter sent, one row each, as stored (PS17 and PS22 of the NetFlow join
-- amendment): every record, whatever the capture configuration was, with the state of the
-- configuration in force at its start, its Observation Point and direction when it had one, and
-- loss_detectable, 1 over the intervals whose configuration made the exporter's sequence one
-- counter's. With two or more captured interfaces a forwarded packet is exported once per interface,
-- as identical records whose interface is not exported (survey, Push sources, S11): a sum over this
-- dataset then counts such traffic once per captured interface.
SELECT r.start_at,
       r.end_at,
       r.exporter_address,
       r.observation_domain,
       r.ingress_interface,
       r.egress_interface,
       r.ip_version,
       r.protocol,
       r.src_address,
       r.dst_address,
       r.src_port,
       r.dst_port,
       r.capture_state,
       r.captured_interfaces,
       r.observation_point_identifier AS observation_point,
       r.direction,
       r.loss_detectable,
       r.join_state,
       r.unmatched_reason,
       r.octets,
       r.packets,
       1 AS records
FROM flow_record AS r
WHERE r.start_at >= :from
  AND r.start_at < :to
