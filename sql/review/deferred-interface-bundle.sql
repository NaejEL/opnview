-- opnview -- the review fixture of specs/SPEC-deferred-interface-bundle.md.
--
-- PROVENANCE: SYNTHESISED, EVERY ROW. No value here was read off any network. The
-- addresses are the documentation ranges -- RFC 5737 for IPv4 (192.0.2.0/24,
-- 198.51.100.0/24, 203.0.113.0/24) and RFC 3849 for IPv6 (2001:db8::/32) -- and the
-- interface descriptions, identifiers and devices are generic labels of the kind an
-- installation types, belonging to no installation. The details are sentences
-- written to read like a collector's.
--
-- WHAT IT IS FOR. The interface review of the deferred interface bundle: the
-- Interfaces and Sources surfaces, the collection surface's settings cards and the
-- Public Suffix List card, in every state those surfaces name. It is loaded by
-- sql/review/serve-deferred-interface-bundle.sh into a SCRATCH data directory inside
-- the development container, never into the `data` volume and never into a live
-- service's database. It is not product code and nothing in the product reads it.
--
-- IT IS APPLIED TWICE AND IS IDEMPOTENT. Once before the service starts, so the loop
-- intervals below are in force from the first turn; once after the service's start-up
-- passes, which, with no firewall configured, record every source as unavailable --
-- the second application puts the states the review needs back. Every statement
-- either upserts or rewrites what it owns.
--
-- THE STATES IT COVERS:
--   interfaces    one upstream interface (no control); an inside interface with a
--                 detected network of each family; one with a confirmed network;
--                 one with a removed network; one with a stale detection that no
--                 longer counts beside an operator network; one with no network at
--                 all, and the link-local rule set on one. An overlap stands: the Lab
--                 carries an operator network inside the LAN's, listed on both
--                 cards; any further overlap the reviewer adds is listed the same way.
--   sources       healthy, installed and not collecting (with the OPNsense page
--                 that turns it on, under the firewall URL below), unavailable (a
--                 401 and a 403 on two providers), not yet probed, and state
--                 unknown (one availability row deleted); providers read and not
--                 read; a selection on and one off; five outages by the rule.
--   gaps          one of every reason the schema allows inside the last 24 hours,
--                 and one older than that which must not be listed.
--   settings      every loop the service runs on its own is put to a day, so no
--                 pass overwrites these rows during the review; the intervals the
--                 collection surface shows are left at their defaults.

PRAGMA foreign_keys = ON;

BEGIN IMMEDIATE;

-- The loops that would rewrite the availability rows during the review: discovery
-- and the probe round, the sampler, the purge and the MaxMind refresh and lookup.
-- None of these intervals is drawn on any surface of this cycle.
INSERT INTO setting (key, value, updated_at) VALUES
    ('refresh_interval_discovery_seconds', '86400', CAST(strftime('%s', 'now') AS INTEGER)),
    ('poll_interval_measurement_seconds', '86400', CAST(strftime('%s', 'now') AS INTEGER)),
    ('purge_interval_seconds', '86400', CAST(strftime('%s', 'now') AS INTEGER)),
    ('refresh_interval_geoip_seconds', '86400', CAST(strftime('%s', 'now') AS INTEGER)),
    ('poll_interval_geo_lookup_seconds', '86400', CAST(strftime('%s', 'now') AS INTEGER)),
    ('source_selection_dhcp_lease_isc', 'on', CAST(strftime('%s', 'now') AS INTEGER)),
    ('source_selection_dns_lookup_dnsmasq', 'off', CAST(strftime('%s', 'now') AS INTEGER)),
    -- A firewall URL with no API key and no secret: the client holds no credentials,
    -- so nothing contacts it, and the Sources surface can draw the OPNsense page that
    -- turns an installed source on. An RFC 2606 name.
    ('opnsense_base_url', 'https://firewall.example.net', CAST(strftime('%s', 'now') AS INTEGER))
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- The interfaces. One upstream, five inside.
INSERT INTO interface (identifier, device, description, status, enabled, link_type, link_kind,
                       vlan_tag, is_upstream, first_seen_at, last_seen_at) VALUES
    ('wan',  'igb0',     'WAN',     'up', '1', 'ethernet', 'physical', NULL, 1,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER)),
    ('lan',  'igb1',     'LAN',     'up', '1', 'ethernet', 'physical', NULL, 0,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER)),
    ('opt1', 'vlan0.20', 'IoT',     'up', '1', 'vlan',     'vlan',     20,   0,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER)),
    ('opt2', 'vlan0.30', 'Guests',  'up', '1', 'vlan',     'vlan',     30,   0,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER)),
    ('opt3', 'vlan0.40', 'Lab',     'up', '1', 'vlan',     'vlan',     40,   0,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER)),
    ('opt4', 'vlan0.50', 'Cameras', 'up', '1', 'vlan',     'vlan',     50,   0,
        CAST(strftime('%s', 'now') AS INTEGER) - 864000, CAST(strftime('%s', 'now') AS INTEGER))
ON CONFLICT (identifier) DO UPDATE SET last_seen_at = excluded.last_seen_at;

-- The link-local rule, set on the LAN only.
UPDATE interface SET link_local_evidence = CASE identifier WHEN 'lan' THEN 1 ELSE 0 END;

-- The networks. Rewritten whole, so the second application restores any edit the
-- start-up passes could not have made and leaves the reviewer a known starting state.
DELETE FROM interface_network;
INSERT INTO interface_network (interface_id, network_address, prefix_length, address_family, origin,
                               first_detected_at, last_detected_at, removed_at, updated_at)
SELECT i.id, n.address, n.bits, n.family, n.origin, n.detected, n.detected, n.removed,
       CAST(strftime('%s', 'now') AS INTEGER)
FROM (
    -- LAN: one detected network of each family, both counting.
    SELECT 'lan' AS identifier, '192.0.2.0' AS address, 24 AS bits, 4 AS family,
           'detected' AS origin, CAST(strftime('%s', 'now') AS INTEGER) AS detected, NULL AS removed
    UNION ALL SELECT 'lan', '2001:db8:10::', 64, 6, 'detected',
           CAST(strftime('%s', 'now') AS INTEGER), NULL
    -- IoT: a detected network the operator confirmed.
    UNION ALL SELECT 'opt1', '198.51.100.0', 26, 4, 'operator',
           CAST(strftime('%s', 'now') AS INTEGER), NULL
    UNION ALL SELECT 'opt1', '2001:db8:20::', 64, 6, 'detected',
           CAST(strftime('%s', 'now') AS INTEGER), NULL
    -- Guests: a detected network the operator removed, still listed.
    UNION ALL SELECT 'opt2', '198.51.100.64', 26, 4, 'detected',
           CAST(strftime('%s', 'now') AS INTEGER), CAST(strftime('%s', 'now') AS INTEGER) - 3600
    -- Lab: a stale detection that no longer counts, beside a current one.
    UNION ALL SELECT 'opt3', '198.51.100.128', 27, 4, 'detected',
           CAST(strftime('%s', 'now') AS INTEGER) - 86400, NULL
    UNION ALL SELECT 'opt3', '198.51.100.160', 27, 4, 'detected',
           CAST(strftime('%s', 'now') AS INTEGER), NULL
) AS n
JOIN interface AS i ON i.identifier = n.identifier;

-- Two operator networks on the Lab, which nothing detected; the IPv4 one lies inside
-- the LAN's detected network, so the overlap is listed on both cards.
INSERT INTO interface_network (interface_id, network_address, prefix_length, address_family, origin,
                               updated_at)
SELECT i.id, n.address, n.bits, n.family, 'operator', CAST(strftime('%s', 'now') AS INTEGER)
FROM interface AS i,
     (SELECT '2001:db8:40::' AS address, 64 AS bits, 6 AS family
      UNION ALL SELECT '192.0.2.128', 25, 4) AS n
WHERE i.identifier = 'opt3';

-- The availability of every source, in every condition the preset names.
UPDATE source_availability SET state = 'reachable', probe = 'GET /api/diagnostics/firewall/log',
    detail = NULL, checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 40
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'firewall_log' AND provider_key = 'pf');

UPDATE source_availability SET state = 'present_but_disabled', probe = 'GET /api/ids/service/status',
    detail = 'status: stopped', checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'security_event' AND provider_key = 'suricata');

UPDATE source_availability SET state = 'reachable', probe = 'GET /api/kea/service/status',
    detail = 'running', checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'dhcp_lease' AND provider_key = 'kea');

UPDATE source_availability SET state = 'unavailable', probe = 'GET /api/dnsmasq/service/status',
    detail = 'HTTP 401 Unauthorized: the API key lacks this privilege',
    checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'dhcp_lease' AND provider_key = 'dnsmasq');

UPDATE source_availability SET state = 'unavailable', probe = 'GET /api/dhcpv4/service/status',
    detail = 'HTTP 404 Not Found: the plugin is not installed',
    checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'dhcp_lease' AND provider_key = 'isc');

UPDATE source_availability SET state = 'reachable', probe = 'GET /api/unbound/overview/is_enabled',
    detail = NULL, checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 55
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'dns_lookup' AND provider_key = 'unbound');

UPDATE source_availability SET state = 'unavailable', probe = 'GET /api/dnsmasq/settings/get',
    detail = 'HTTP 403 Forbidden: the API key lacks this privilege',
    checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'dns_lookup' AND provider_key = 'dnsmasq');

UPDATE source_availability SET state = 'unavailable', probe = 'no_licence_key',
    detail = NULL, checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 3000
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'geo_asn' AND provider_key = 'maxmind_geolite2');

UPDATE source_availability SET state = 'reachable', probe = 'GET /api/diagnostics/traffic/top',
    detail = NULL, checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 120
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'measurement_sample' AND provider_key = 'insight');

UPDATE source_availability SET state = 'unavailable', probe = 'download_failed',
    detail = 'publicsuffix: fetching the list: connection refused',
    checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 900
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'public_suffix' AND provider_key = 'public_suffix_list');

UPDATE source_availability SET state = 'reachable', probe = 'GET /api/unbound/diagnostics/dumpcache',
    detail = '2214 records, 412 KiB, read in 0.31 s', checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 50
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'resolver_cache' AND provider_key = 'unbound');

UPDATE source_availability SET state = 'unavailable', probe = 'GET /api/dnsmasq/settings/get',
    detail = 'Dnsmasq offers no cache read through the API',
    checked_at = CAST(strftime('%s', 'now') AS INTEGER) - 280
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'resolver_cache' AND provider_key = 'dnsmasq');

-- flow_volume/insight and resolver_local_data/dnsmasq stay never probed.
UPDATE source_availability SET state = 'unavailable', probe = 'not_yet_probed', detail = NULL, checked_at = 0
WHERE provider_id IN (SELECT id FROM provider
                      WHERE (kind = 'flow_volume' AND provider_key = 'insight')
                         OR (kind = 'resolver_local_data' AND provider_key = 'dnsmasq'));

-- resolver_local_data/unbound has no availability row at all: "state unknown".
DELETE FROM source_availability
WHERE provider_id = (SELECT id FROM provider WHERE kind = 'resolver_local_data' AND provider_key = 'unbound');

-- Which providers opnview reads. ONLY THE SAMPLER IS ACTIVE, deliberately: an active
-- provider's pass runs on its kind's interval, and with no firewall configured it
-- records its source unavailable, overwriting the state above within seconds. The
-- sampler's interval is put to a day at the top of this file, so its row holds; the
-- sized kinds keep their default intervals, which the collection surface shows.
UPDATE provider SET is_active = CASE
    WHEN kind = 'measurement_sample' AND provider_key = 'insight' THEN 1
    ELSE 0 END;

-- The gaps: one of every reason inside the last day, and one outside it.
DELETE FROM collection_gap;
INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at, reason, detail, detected_at)
SELECT p.id, g.start_at, g.end_at, g.reason, g.detail, g.end_at
FROM (
    SELECT 'firewall_log' AS kind, 'pf' AS provider_key,
           CAST(strftime('%s', 'now') AS INTEGER) - 1260 AS start_at,
           CAST(strftime('%s', 'now') AS INTEGER) - 1200 AS end_at,
           'digest_outside_returned_window' AS reason,
           'the oldest line returned is newer than the newest stored' AS detail
    UNION ALL SELECT 'security_event', 'suricata',
           CAST(strftime('%s', 'now') AS INTEGER) - 14400, CAST(strftime('%s', 'now') AS INTEGER) - 10800,
           'eve_rotation_lost', 'eve.json.1 was rotated away before it was read'
    UNION ALL SELECT 'dns_lookup', 'unbound',
           CAST(strftime('%s', 'now') AS INTEGER) - 3700, CAST(strftime('%s', 'now') AS INTEGER) - 3290,
           'resolver_window_not_honoured', NULL
    UNION ALL SELECT 'public_suffix', 'public_suffix_list',
           CAST(strftime('%s', 'now') AS INTEGER) - 87300, CAST(strftime('%s', 'now') AS INTEGER) - 900,
           'download_failed', 'connection refused'
    UNION ALL SELECT 'firewall_log', 'pf',
           CAST(strftime('%s', 'now') AS INTEGER) - 172900, CAST(strftime('%s', 'now') AS INTEGER) - 172800,
           'digest_outside_returned_window', 'older than a day: not listed'
) AS g
JOIN provider AS p ON p.kind = g.kind AND p.provider_key = g.provider_key;

COMMIT;
