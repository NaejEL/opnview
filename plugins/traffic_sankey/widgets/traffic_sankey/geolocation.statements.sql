-- The stored geolocation of addresses (ADR 0021, step 4), as the maxmind plugin holds it, read by the
-- figures library's services area (host.lib.figures.services.geolocation).

-- statement: widget_geolocation
SELECT address, lookup_state, country_code, country_name, latitude, longitude, asn, operator, dataset_build_at
FROM plugin_maxmind.geo_asn
WHERE address IN (SELECT j.value FROM jsonb_array_elements_text(CAST(:addresses AS jsonb)) AS j(value));
