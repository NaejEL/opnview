-- opnview — the statements run by internal/store/dualstack.go.
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line
-- before each statement, a terminating semicolon, named parameters (the glossary is
-- at the head of classify.statements.sql), @period@ / @child@ tokens for the period
-- templates. sql/schema-checks.sh plans each statement at both seed sizes.
-- No address, CIDR, interface identifier, device name or description appears below.


-- The local data naming a host at an instant: the addresses of the A and AAAA records
-- whose owner, and of the PTR records whose target, has the host name's first label,
-- each with the full names it was held under -- the owner of an A or AAAA record, the
-- target of a PTR record -- compared as DNS names are, without regard to case and with
-- a trailing dot removed. The caller takes the address when there is exactly one, and,
-- when there are one IPv4 and one IPv6 address under one name, the address of the
-- lookup's querying family (the dual-stack rule). Three rows are enough to tell those
-- cases from every other.
-- statement: local_data_addresses_for_hostname
SELECT o.address,
       jsonb_agg(DISTINCT lower(rtrim(CASE WHEN o.rrtype = 'PTR' THEN o.value
                                                  ELSE o.owner_name END, '.'))) AS names
FROM resource_record_observation AS o
WHERE o.held_in = 'local_data'
  AND o.host_label = :label
  AND o.covered_until_at >= :at
  AND o.covered_from_at <= :at
GROUP BY o.address
ORDER BY o.address
LIMIT 3;

