-- opnview — the statements run by internal/store/samples/samples.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- ---------------------------------------------------------------------------
-- Samples: the current per-pair rates, the bytes seen in samples, and the
-- per-interface counters.
-- ---------------------------------------------------------------------------

-- statement: read_pair_rates
-- The rates of both sampled subjects: the totals of each local address
-- (interface_endpoint) and the per-peer figures (interface_endpoint_pair), fine
-- and baseline, each with the live cadence of a fine sample (null for a baseline one). A
-- subject is read by its parts, the device and the addresses its provider reported, never by
-- its key; a reading missing a part its kind has names no subject the rates can place.
SELECT subject_kind, interface_device, local_address, coalesce(peer_address, ''), measure, value,
       sampled_at, live_cadence_seconds
FROM measurement_sample
WHERE sampled_at >= :since
  AND sampled_at <= :now
  AND subject_kind IN ('interface_endpoint', 'interface_endpoint_pair')
  AND measure IN ('rate_bits_in', 'rate_bits_out')
  AND interface_device IS NOT NULL
  AND local_address IS NOT NULL
  AND (subject_kind = 'interface_endpoint' OR peer_address IS NOT NULL);


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
