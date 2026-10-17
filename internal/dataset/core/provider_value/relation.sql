-- The values the ACTIVE providers export beside their functions (PA6 of the plugin-API amendment): what a
-- kind's contract names under `values` -- the firewall_log contract's pf state timeouts, dns_lookup's page
-- bound -- as the active implementation's script exported them, so a plugin reads a fact another plugin
-- states rather than restating it. One row per value: the kind, the provider key, the value's name, and
-- the value under the column of its type -- a whole number in integer_value and number_value, any other
-- number in number_value, a text in text_value.
--
-- The rows are rewritten at every load and probe round, not by a provider's pass, so the freshness names
-- `discovery`, which no provider row carries: the condition reads state_unknown.
SELECT p.kind,
       p.provider_key,
       v.name,
       CASE WHEN jsonb_typeof(v.value) = 'number' AND CAST(v.value AS TEXT) ~ '^-?[0-9]+$'
            THEN CAST(CAST(v.value AS TEXT) AS BIGINT) END AS integer_value,
       CASE WHEN jsonb_typeof(v.value) = 'number' THEN CAST(CAST(v.value AS TEXT) AS DOUBLE PRECISION) END
           AS number_value,
       CASE WHEN jsonb_typeof(v.value) = 'string' THEN v.value #>> '{}' END AS text_value
FROM provider_value AS v
JOIN provider AS p ON p.id = v.provider_id
WHERE p.is_active
