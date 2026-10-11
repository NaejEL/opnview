-- The unmatched records (PS19 rule 7 and PS21 of the NetFlow join amendment), per hour of their start:
-- flow records with an Observation Point that joined no leg, keyed by that interface, the direction
-- at it, the IANA protocol number, the IP version and the reason. They are no logged connection's,
-- and no other dataset or family counts them.
SELECT u.period_start_at,
       u.observation_point_identifier AS observation_point,
       u.direction,
       u.protocol,
       u.ip_version,
       u.reason,
       u.bytes,
       u.packets,
       u.records
FROM unmatched_record_aggregate_1h AS u
WHERE u.period_start_at >= :from
  AND u.period_start_at < :to
