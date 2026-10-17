-- The unmatched records (NJ4 rule 9 and NJ8 of the amendment "the NetFlow join with every interface
-- captured"), per hour of their start: flow records that joined no leg and are no copy, keyed by their
-- ingress interface and their egress interface -- each an identifier, or the role of an index that names
-- none -- the IANA protocol number, the IP version and the reason. They are no logged connection's, and
-- no other dataset or family counts them.
SELECT u.period_start_at,
       u.ingress_interface,
       u.egress_interface,
       u.protocol,
       u.ip_version,
       u.reason,
       u.bytes,
       u.packets,
       u.records
FROM unmatched_record_aggregate_1h AS u
WHERE u.period_start_at >= :from
  AND u.period_start_at < :to
