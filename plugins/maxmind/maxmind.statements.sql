-- The maxmind plugin's statements, run by its SQL-only derivation `mark` after every firewall_log pass
-- (ADR 0020 wave 4): which addresses need placing, never a non-public one.

-- Every public address a flow stored since the last run names and no row holds yet becomes a pending
-- row, named to this plugin's provider; and a row another provider placed becomes pending again under
-- this one. The script derivation `locate` reads the pending rows and the stale ones and places them.
-- A public address is a global unicast one outside the private, loopback, link-local, shared,
-- multicast and reserved ranges, as the Go placing pass decided it before it was deleted.
-- statement: mark_addresses
INSERT INTO plugin_maxmind.geo_asn (address, provider_id, lookup_state, looked_up_at)
SELECT DISTINCT seen.address,
       (SELECT p.id FROM opnview.provider AS p WHERE p.kind = 'ip_geolocation' AND p.provider_key = 'maxmind_geolite2'),
       'pending',
       CAST(:now AS BIGINT)
FROM (SELECT f.src_address AS address FROM plugin_pf.flow AS f
      WHERE CAST(:since AS BIGINT) IS NULL OR f.ingested_at >= CAST(:since AS BIGINT)
      UNION
      SELECT f.dst_address FROM plugin_pf.flow AS f
      WHERE CAST(:since AS BIGINT) IS NULL OR f.ingested_at >= CAST(:since AS BIGINT)) AS seen
WHERE plugin_clients.to_inet(seen.address) IS NOT NULL
  AND NOT (plugin_clients.to_inet(seen.address) << CAST('0.0.0.0/8' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('10.0.0.0/8' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('100.64.0.0/10' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('127.0.0.0/8' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('169.254.0.0/16' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('172.16.0.0/12' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('192.168.0.0/16' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('224.0.0.0/4' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('240.0.0.0/4' AS inet)
           OR plugin_clients.to_inet(seen.address) = CAST('255.255.255.255' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('::/128' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('::1/128' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('fc00::/7' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('fe80::/10' AS inet)
           OR plugin_clients.to_inet(seen.address) << CAST('ff00::/8' AS inet))
  AND NOT EXISTS (SELECT 1 FROM plugin_maxmind.geo_asn AS g WHERE g.address = seen.address)
ON CONFLICT (address) DO NOTHING;


-- A row placed by another provider is placed again by this one.
-- statement: mark_other_providers
UPDATE plugin_maxmind.geo_asn AS g
SET lookup_state = 'pending',
    provider_id = (SELECT p.id FROM opnview.provider AS p WHERE p.kind = 'ip_geolocation' AND p.provider_key = 'maxmind_geolite2'),
    country_code = NULL, country_name = NULL, latitude = NULL, longitude = NULL, asn = NULL, operator = NULL,
    dataset_build_at = NULL,
    looked_up_at = CAST(:now AS BIGINT)
WHERE g.provider_id IS DISTINCT FROM
      (SELECT p.id FROM opnview.provider AS p WHERE p.kind = 'ip_geolocation' AND p.provider_key = 'maxmind_geolite2');
