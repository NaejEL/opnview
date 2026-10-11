-- opnview — the statements run by internal/store/samples.go.
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
-- (interface_endpoint) and the per-peer figures (interface_endpoint_pair).
SELECT subject_kind, subject_key, measure, value, sampled_at
FROM measurement_sample
WHERE sampled_at >= :since
  AND sampled_at <= :now
  AND subject_kind IN ('interface_endpoint', 'interface_endpoint_pair')
  AND measure IN ('rate_bits_in', 'rate_bits_out');


-- statement: read_sampled_pair_bytes
-- The bytes seen in samples, per device, from the totals of each local address:
-- a local address's outbound bytes exist only as that total.
SELECT substr(subject_key, 1, strpos(subject_key, ' ') - 1),
       sampled_at,
       sum(value)
FROM measurement_sample
WHERE sampled_at >= :from
  AND sampled_at < :to
  AND subject_kind = 'interface_endpoint'
  AND measure IN ('cumulative_bytes_in', 'cumulative_bytes_out')
GROUP BY 1, 2;


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


-- statement: read_is_firewall_address
SELECT 1
FROM interface_address
WHERE address = :address
  AND source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')
LIMIT 1;

