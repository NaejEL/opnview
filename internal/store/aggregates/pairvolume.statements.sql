-- opnview — the statements run by internal/store/aggregates/aggregate.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- The daily per-pair volume, derived from the flows of one UTC day whenever the
-- day's slot is rewritten, for every day of a refresh at once (:slots, as in
-- aggregateslots.statements.sql). A second leg is left out: its connection is counted on its first
-- leg (decision 2 of the step-5A live corrections). This and the retention purge are the only writers of
-- pair_volume_observation. It has no purged part: the purge removes a day's rows
-- once the day starts before the horizon, so the flows a rewrite of such a day
-- can no longer read are the ones whose rows the purge removes anyway.
-- statement: delete_pair_volume
DELETE FROM pair_volume_observation
WHERE day_start_at IN (SELECT CAST(j.value ->> 0 AS BIGINT) FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value));


-- statement: insert_pair_volume
INSERT INTO pair_volume_observation (day_start_at, endpoint_low, endpoint_high,
    service_port, protocol, octets, packets, observed_direction,
    observed_interface_device, last_seen_at, ingested_at)
SELECT p.day_start_at, p.endpoint_low, p.endpoint_high, p.service_port, p.protocol, p.octets, p.packets,
       first.direction, first.interface_device, p.last_seen_at, :now
FROM (
    SELECT
        s.slot_start                             AS day_start_at,
        LEAST(f.src_address, f.dst_address)      AS endpoint_low,
        GREATEST(f.src_address, f.dst_address)   AS endpoint_high,
        coalesce(f.dst_port, 0)                  AS service_port,
        f.protocol                               AS protocol,
        sum(f.packet_bytes)                      AS octets,
        count(*)                                 AS packets,
        min(f.id)                                AS first_flow_id,
        max(f.observed_at)                       AS last_seen_at
    FROM (SELECT CAST(j.value ->> 0 AS BIGINT) AS slot_start, CAST(j.value ->> 1 AS BIGINT) AS slot_end
          FROM jsonb_array_elements(CAST(:slots AS jsonb)) AS j(value)) AS s
    JOIN flow AS f ON f.observed_at >= s.slot_start AND f.observed_at < s.slot_end
    WHERE f.observed_at >= :from AND f.observed_at < :to
      AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
    GROUP BY 1, 2, 3, 4, 5
) AS p
JOIN flow AS first ON first.id = p.first_flow_id;
