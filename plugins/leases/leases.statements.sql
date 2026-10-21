-- The leases plugin's statements, run by its SQL-only derivation `resolve` after every dhcp_lease pass
-- (ADR 0020 wave 4). The sources write each lease with the interface as their server names it -- a
-- configuration key, a device, or neither -- and this plugin names the installation's interface.

-- A lease given on a named interface takes that interface's id: by the configuration key the server
-- reported, else by the device. A lease the server gave no interface for keeps none; the clients plugin
-- places it from the networks and the sightings as it places any address. Only the leases this pass
-- stored or brought up to date are looked at.
-- statement: resolve_interfaces
UPDATE plugin_leases.dhcp_lease AS l
SET interface_id = i.id
FROM plugin_installation.interface AS i
WHERE l.interface_id IS NULL
  AND (CAST(:since AS BIGINT) IS NULL OR l.observed_at >= CAST(:since AS BIGINT))
  AND ((l.interface_identifier IS NOT NULL AND i.identifier = l.interface_identifier)
       OR (l.interface_identifier IS NULL AND l.interface_device IS NOT NULL AND i.device = l.interface_device));


-- The installation's retention, applied to the leases as the core purge applied it: a lease observed
-- longer ago than the retention is removed; a retention of zero keeps everything.
-- statement: purge_leases
DELETE FROM plugin_leases.dhcp_lease
WHERE CAST(:retention AS BIGINT) > 0
  AND observed_at < CAST(:now AS BIGINT) - CAST(:retention AS BIGINT);
