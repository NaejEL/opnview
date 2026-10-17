-- opnview — the read statements the reference candidates in candidates.go run: what each reference
-- kind of docs/dashboard-format.md, "The reference object", can name on this installation, for the
-- dropdowns of the repair surface and the no-code form (GET /api/references/{kind},
-- specs/SPEC-step-7-frontend.md, A8).
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql in
-- internal/widget for the parameter conventions). sql/schema-checks.sh plans each statement at both
-- seed sizes, and none may scan a growing table:
--
--   * interface, rule, owner and provider are BOUNDED tables (docs/data-model.md), read whole;
--   * client is GROWING, so it is read a page at a time, keyset on its primary key: every page is
--     an index search on `id > :after` under a LIMIT;
--   * the countries and the operators are the distinct values of geo_asn's indexed country_code
--     and asn, and the site names the distinct values of dns_resolution's indexed name expression
--     (the same comparison widget_ref_site resolves with). Both tables are GROWING, so the distinct
--     values are walked a page at a time by a recursive skip over the index: each step is one
--     index search for the next value after the last one, under a LIMIT 1, and the walk stops at
--     :page values. No step reads a row that is not the next distinct value.
--
-- Parameters:
--   :after    the last key of the previous page (an id, a code, an AS number or a name); the
--             first page passes a value below every key
--   :page     the number of keys a page holds


-- statement: widget_candidate_interfaces
SELECT id, identifier, device, user_label, description
FROM interface
ORDER BY id;


-- statement: widget_candidate_rules
SELECT id, pf_label, description
FROM rule
ORDER BY id;


-- statement: widget_candidate_owners
SELECT id, display_name
FROM owner
ORDER BY display_name, id;


-- statement: widget_candidate_providers
SELECT id, kind, provider_key, display_name
FROM provider
ORDER BY kind, provider_key;


-- One page of clients, keyset on the primary key.
-- statement: widget_candidate_clients_page
SELECT id, identity_kind, identity_key, hostname, last_address, unstable_identity
FROM client
WHERE id > :after
ORDER BY id
LIMIT :page;


-- One page of the distinct country codes after :after, walked over idx_geo_asn_country_code, each
-- with a country name a geolocated address carries (an index search on the code, LIMIT 1).
-- statement: widget_candidate_countries_page
WITH RECURSIVE codes (code, step) AS (
    (SELECT g.country_code, 1
     FROM geo_asn AS g
     WHERE g.country_code > :after
     ORDER BY g.country_code
     LIMIT 1)
    UNION ALL
    SELECT (SELECT g.country_code
            FROM geo_asn AS g
            WHERE g.country_code > codes.code
            ORDER BY g.country_code
            LIMIT 1),
           codes.step + 1
    FROM codes
    WHERE codes.code IS NOT NULL AND codes.step < :page
)
SELECT codes.code,
       (SELECT g.country_name
        FROM geo_asn AS g
        WHERE g.country_code = codes.code AND g.country_name IS NOT NULL
        LIMIT 1) AS country_name
FROM codes
WHERE codes.code IS NOT NULL
ORDER BY codes.code;


-- One page of the distinct AS numbers after :after, walked over idx_geo_asn_asn, each with an
-- operator name a geolocated address carries (an index search on the AS number, LIMIT 1).
-- statement: widget_candidate_operators_page
WITH RECURSIVE numbers (asn, step) AS (
    (SELECT g.asn, 1
     FROM geo_asn AS g
     WHERE g.asn > :after
     ORDER BY g.asn
     LIMIT 1)
    UNION ALL
    SELECT (SELECT g.asn
            FROM geo_asn AS g
            WHERE g.asn > numbers.asn
            ORDER BY g.asn
            LIMIT 1),
           numbers.step + 1
    FROM numbers
    WHERE numbers.asn IS NOT NULL AND numbers.step < :page
)
SELECT numbers.asn,
       (SELECT g.operator
        FROM geo_asn AS g
        WHERE g.asn = numbers.asn AND g.operator IS NOT NULL
        LIMIT 1) AS operator
FROM numbers
WHERE numbers.asn IS NOT NULL
ORDER BY numbers.asn;


-- One page of the distinct site names after :after: the names lookups asked for, compared as
-- widget_ref_site compares them (without regard to case, a trailing dot removed), walked over
-- idx_dns_resolution_domain_name, whose leading key is that very expression.
-- statement: widget_candidate_sites_page
WITH RECURSIVE names (name, step) AS (
    (SELECT lower(rtrim(d.domain, '.')), 1
     FROM dns_resolution AS d
     WHERE lower(rtrim(d.domain, '.')) > :after
     ORDER BY lower(rtrim(d.domain, '.'))
     LIMIT 1)
    UNION ALL
    SELECT (SELECT lower(rtrim(d.domain, '.'))
            FROM dns_resolution AS d
            WHERE lower(rtrim(d.domain, '.')) > names.name
            ORDER BY lower(rtrim(d.domain, '.'))
            LIMIT 1),
           names.step + 1
    FROM names
    WHERE names.name IS NOT NULL AND names.step < :page
)
SELECT names.name
FROM names
WHERE names.name IS NOT NULL
ORDER BY names.name;
