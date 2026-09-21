-- opnview migration 0001 — core entities.
--
-- Every entity here is fed by one of the five OPNsense sources surveyed in
-- docs/opnsense-api-survey.md, or by the runtime discovery described in that
-- document's "Runtime discovery" section. docs/data-model.md maps each table
-- and each column to its endpoint and API field.
--
-- Conventions applied throughout, and asserted by sql/schema-checks.sh:
--   * Every column holding an instant is an INTEGER UTC epoch in seconds and
--     its name ends in "_at". The three input shapes (epoch seconds, ISO
--     strings, year-less syslog strings) are normalised by the collector at
--     the boundary; see docs/data-model.md.
--   * No interface name, VLAN name, segment name, address or CIDR appears as
--     a literal. Every such value is discovered at runtime.
--   * No predicate anywhere matches on a description, a label or a name: a
--     segment's nature is never inferred from what it is called.
--
-- The upper timestamp bound below is 2100-01-01T00:00:00Z. It rejects a
-- millisecond value mistakenly stored as seconds, and a negative instant.

CREATE TABLE IF NOT EXISTS schema_version (
    filename   TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL CHECK (applied_at >= 0 AND applied_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Configuration. Bounded: one row per setting key.
-- ---------------------------------------------------------------------------
CREATE TABLE setting (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Segment — a named zone, tunnels included.
-- Source: /api/interfaces/overview/interfaces_info (identifier, description,
-- device, link_type, vlan_tag). Survey: "Runtime discovery" (i).
--
-- discovered_description is what the firewall reports; user_label is the
-- maintainer's own name for the zone. They are distinct columns so that
-- relabelling never overwrites discovery and discovery never overwrites a
-- label. link_kind is the normalised class of discovered_link_type; is_tunnel
-- derives from link_kind alone and from nothing that carries a name.
-- ---------------------------------------------------------------------------
CREATE TABLE segment (
    id                     INTEGER PRIMARY KEY,
    interface_identifier   TEXT NOT NULL UNIQUE,
    device_name            TEXT NOT NULL UNIQUE,
    discovered_description TEXT NOT NULL,
    user_label             TEXT,
    discovered_link_type   TEXT NOT NULL,
    link_kind              TEXT NOT NULL
                           CHECK (link_kind IN ('physical', 'vlan', 'tunnel', 'other')),
    is_tunnel              INTEGER GENERATED ALWAYS AS
                           (CASE WHEN link_kind = 'tunnel' THEN 1 ELSE 0 END) VIRTUAL,
    vlan_tag               INTEGER,
    address_family         INTEGER CHECK (address_family IS NULL OR address_family IN (4, 6)),
    first_seen_at          INTEGER NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at           INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Interface map — first-class join key number one: the filter log's raw
-- device name to the user-given interface description.
-- Source: /api/diagnostics/interface/get_interface_names. Survey: "Runtime
-- discovery" (i). A device name absent from this table is the modelled
-- "not found" state carried on flow.interface_lookup_state.
-- ---------------------------------------------------------------------------
CREATE TABLE interface_map (
    device_name   TEXT PRIMARY KEY,
    description   TEXT NOT NULL,
    segment_id    INTEGER REFERENCES segment (id),
    discovered_at INTEGER NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Rule — first-class join key number two: the filter log's rid / the rule
-- uuid, to the rule description.
-- Source: /api/firewall/filter/search_rule (uuid, description, action,
-- direction, is_automatic). Survey: "Runtime discovery" (ii). A rid matching
-- no row here is normal (the rule was removed) and is carried on
-- flow.rule_lookup_state.
-- ---------------------------------------------------------------------------
CREATE TABLE rule (
    id            INTEGER PRIMARY KEY,
    pf_label      TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL,
    action        TEXT NOT NULL
                  CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction     TEXT CHECK (direction IS NULL OR direction IN ('in', 'out', 'any')),
    is_automatic  INTEGER NOT NULL DEFAULT 0 CHECK (is_automatic IN (0, 1)),
    discovered_at INTEGER NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Device — one physical machine, identified by the documented cascade, most
-- stable first: DHCP client identity, then MAC, then address within its
-- segment over a validity interval.
-- Sources: the lease endpoints of data source 4 (hostname, hwaddr/mac,
-- client_id, duid, iaid, mac_info) and, for a device never seen in a lease,
-- the filter log of data source 1, which carries no MAC at all.
--
-- identity_key holds the value the chosen cascade level produced; the pair
-- (identity_kind, identity_key) is unique, so two observations only merge when
-- the same level yields the same value. An address reissued after a lease
-- expiry produces a different identity_key, because the key of that level
-- carries the validity start.
--
-- A randomised MAC is one whose second hex digit carries the IEEE
-- locally-administered bit: 2, 6, A or E. The digits 0, 4, 8 and C are
-- globally administered — burned-in addresses — and are NOT randomised. The
-- mac column is constrained to 17 lowercase characters so the digit test is
-- total.
-- ---------------------------------------------------------------------------
CREATE TABLE device (
    id                INTEGER PRIMARY KEY,
    identity_kind     TEXT NOT NULL
                      CHECK (identity_kind IN ('dhcp_client_id', 'mac', 'address_in_segment')),
    identity_key      TEXT NOT NULL,
    segment_id        INTEGER REFERENCES segment (id),
    mac               TEXT CHECK (mac IS NULL OR (length(mac) = 17 AND mac = lower(mac))),
    hostname          TEXT,
    vendor_hint       TEXT,
    last_address      TEXT,
    mac_is_randomised INTEGER GENERATED ALWAYS AS (
                          CASE
                              WHEN mac IS NULL THEN NULL
                              WHEN instr('26ae', substr(mac, 2, 1)) > 0 THEN 1
                              ELSE 0
                          END) VIRTUAL,
    unstable_identity INTEGER GENERATED ALWAYS AS (
                          CASE
                              WHEN mac IS NOT NULL AND instr('26ae', substr(mac, 2, 1)) > 0 THEN 1
                              ELSE 0
                          END) VIRTUAL,
    first_seen_at     INTEGER NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at      INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    UNIQUE (identity_kind, identity_key)
);

CREATE INDEX idx_device_segment ON device (segment_id, id);

-- ---------------------------------------------------------------------------
-- DHCP lease — one observed lease generation.
-- Source: /api/kea/leases4/search, /api/dnsmasq/leases/search, and the
-- best-effort ISC plugin endpoint. Survey: data source 4. The MAC field is
-- hwaddr on the two current backends and mac on the legacy plugin; it is
-- normalised here.
-- ---------------------------------------------------------------------------
CREATE TABLE dhcp_lease (
    id          INTEGER PRIMARY KEY,
    device_id   INTEGER REFERENCES device (id),
    backend     TEXT NOT NULL CHECK (backend IN ('kea', 'dnsmasq', 'isc')),
    address     TEXT NOT NULL,
    mac         TEXT CHECK (mac IS NULL OR (length(mac) = 17 AND mac = lower(mac))),
    hostname    TEXT,
    client_id   TEXT,
    duid        TEXT,
    iaid        TEXT,
    vendor_hint TEXT,
    lease_state TEXT NOT NULL
                CHECK (lease_state IN ('active', 'expired', 'reserved', 'unknown')),
    segment_id  INTEGER REFERENCES segment (id),
    starts_at   INTEGER NOT NULL CHECK (starts_at >= 0 AND starts_at < 4102444800),
    expires_at  INTEGER CHECK (expires_at IS NULL OR (expires_at >= 0 AND expires_at < 4102444800)),
    observed_at INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    UNIQUE (address, starts_at, backend)
);

CREATE INDEX idx_dhcp_lease_observed_at ON dhcp_lease (observed_at);
CREATE INDEX idx_dhcp_lease_device ON dhcp_lease (device_id, starts_at);

-- ---------------------------------------------------------------------------
-- Flow — one filter-log record, allowed or blocked. The blocked events are a
-- projection of this table, never a second ingestion path: every blocked
-- record is the same log line as an allowed one with a different action.
-- Source: /api/diagnostics/firewall/log. Survey: data source 1. The record is
-- deduplicated on __digest__, stored as log_digest.
--
-- traffic_scope is east-west exactly when both endpoints sit in a discovered
-- segment, and north-south otherwise. It derives from segment membership and
-- from nothing else — no address, no CIDR, no name.
-- ---------------------------------------------------------------------------
CREATE TABLE flow (
    id                     INTEGER PRIMARY KEY,
    log_digest             TEXT NOT NULL UNIQUE,
    observed_at            INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    ingested_at            INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    interface_device       TEXT NOT NULL,
    interface_lookup_state TEXT NOT NULL
                           CHECK (interface_lookup_state IN ('resolved', 'not_found', 'pending')),
    src_segment_id         INTEGER REFERENCES segment (id),
    dst_segment_id         INTEGER REFERENCES segment (id),
    src_device_id          INTEGER REFERENCES device (id),
    dst_device_id          INTEGER REFERENCES device (id),
    src_address            TEXT NOT NULL,
    dst_address            TEXT NOT NULL,
    src_port               INTEGER CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_port               INTEGER CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol               TEXT NOT NULL,
    ip_version             INTEGER NOT NULL CHECK (ip_version IN (4, 6)),
    action                 TEXT NOT NULL
                           CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction              TEXT NOT NULL CHECK (direction IN ('in', 'out', 'unknown')),
    packet_bytes           INTEGER NOT NULL CHECK (packet_bytes >= 0),
    rid                    TEXT,
    rule_id                INTEGER REFERENCES rule (id),
    rule_lookup_state      TEXT NOT NULL
                           CHECK (rule_lookup_state IN ('resolved', 'not_found', 'pending')),
    traffic_scope          TEXT GENERATED ALWAYS AS (
                               CASE
                                   WHEN src_segment_id IS NOT NULL AND dst_segment_id IS NOT NULL
                                       THEN 'east_west'
                                   ELSE 'north_south'
                               END) VIRTUAL
);

CREATE INDEX idx_flow_observed_at ON flow (observed_at);
CREATE INDEX idx_flow_src_segment_observed_at ON flow (src_segment_id, observed_at);
CREATE INDEX idx_flow_src_device_observed_at ON flow (src_device_id, observed_at);
CREATE INDEX idx_flow_blocked_observed_at ON flow (observed_at) WHERE action = 'block';

CREATE VIEW blocked_event AS
SELECT
    id AS flow_id,
    observed_at,
    ingested_at,
    interface_device,
    interface_lookup_state,
    src_segment_id,
    dst_segment_id,
    src_device_id,
    dst_device_id,
    src_address,
    dst_address,
    src_port,
    dst_port,
    protocol,
    ip_version,
    direction,
    packet_bytes,
    rid,
    rule_id,
    rule_lookup_state,
    traffic_scope
FROM flow
WHERE action = 'block';

-- ---------------------------------------------------------------------------
-- DNS resolution — one resolver lookup, the sole source of site names on
-- OPNsense 26.7.
-- Source: /api/unbound/overview/search_queries (client, domain, time, action,
-- source, rcode, uuid), or the free-text dnsmasq query log. Survey: data
-- source 5. The uuid deduplicates.
-- ---------------------------------------------------------------------------
CREATE TABLE dns_resolution (
    id             INTEGER PRIMARY KEY,
    lookup_uuid    TEXT NOT NULL UNIQUE,
    client_address TEXT NOT NULL,
    device_id      INTEGER REFERENCES device (id),
    domain         TEXT NOT NULL,
    resolver       TEXT NOT NULL CHECK (resolver IN ('unbound', 'dnsmasq')),
    action         TEXT NOT NULL CHECK (action IN ('pass', 'block', 'drop', 'unknown')),
    answer_source  TEXT,
    rcode          TEXT,
    looked_up_at   INTEGER NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    ingested_at    INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800)
);

CREATE INDEX idx_dns_resolution_looked_up_at ON dns_resolution (looked_up_at);
CREATE INDEX idx_dns_resolution_client ON dns_resolution (client_address, looked_up_at);

-- ---------------------------------------------------------------------------
-- Domain attribution — the site name carried by a flow, and the lookup it was
-- inferred from.
--
-- There is deliberately NO provenance or method column. On 26.7 every
-- attribution is inferred from resolver-lookup correlation and there is no
-- second method to tell it apart from: Suricata exposes no dns event type and
-- its tls / http events cannot be read back (survey, gaps 1 and 2). A field
-- with one possible value states nothing. What is carried instead is the
-- lookup itself — a mandatory foreign key, so an attribution cannot exist
-- without it — and the delay between that lookup and the flow, so the
-- attribution can be judged.
-- ---------------------------------------------------------------------------
CREATE TABLE domain_attribution (
    flow_id                   INTEGER PRIMARY KEY
                              REFERENCES flow (id) ON DELETE CASCADE,
    dns_resolution_id         INTEGER NOT NULL
                              REFERENCES dns_resolution (id) ON DELETE CASCADE,
    site_name                 TEXT NOT NULL,
    correlation_delay_seconds INTEGER NOT NULL CHECK (correlation_delay_seconds >= 0),
    attributed_at             INTEGER NOT NULL CHECK (attributed_at >= 0 AND attributed_at < 4102444800)
);

CREATE INDEX idx_domain_attribution_resolution ON domain_attribution (dns_resolution_id);

-- ---------------------------------------------------------------------------
-- Geo / ASN — the MaxMind lookup for one address.
-- Keyed per address rather than per prefix: MaxMind answers with a prefix but
-- SQLite has no natural longest-prefix join. maxmind_build_at is the build
-- date of the database that answered, so a stale enrichment is visible. A
-- cache miss is a row whose lookup_state is 'miss', never an absent row.
-- Source: the MaxMind GeoLite2 City and ASN databases, the second and last of
-- the two outbound calls the project allows. Acquisition is step 4.
-- ---------------------------------------------------------------------------
CREATE TABLE geo_asn (
    address          TEXT PRIMARY KEY,
    lookup_state     TEXT NOT NULL CHECK (lookup_state IN ('resolved', 'miss', 'pending')),
    country_code     TEXT,
    country_name     TEXT,
    latitude         REAL,
    longitude        REAL,
    asn              INTEGER,
    operator         TEXT,
    maxmind_build_at INTEGER
                     CHECK (maxmind_build_at IS NULL
                            OR (maxmind_build_at >= 0 AND maxmind_build_at < 4102444800)),
    looked_up_at     INTEGER NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    CHECK (lookup_state <> 'resolved'
           OR (country_code IS NOT NULL AND maxmind_build_at IS NOT NULL))
);

CREATE INDEX idx_geo_asn_looked_up_at ON geo_asn (looked_up_at);

-- ---------------------------------------------------------------------------
-- IDS rule info cache — severity and category per signature id.
-- Source: /api/ids/settings/get_rule_info/<sid>. Survey: data source 2,
-- response shape, and gap 3: query_alerts overwrites the nested alert object
-- with the signature string, so severity and category are not in the alert
-- record and must be resolved separately and cached. A signature with no row
-- here is a cache miss, and the Alerts screen renders it as an explicit
-- unknown severity rather than dropping the alert.
-- ---------------------------------------------------------------------------
CREATE TABLE ids_rule_info (
    signature_id INTEGER PRIMARY KEY,
    severity     INTEGER NOT NULL CHECK (severity >= 1),
    category     TEXT,
    rule_source  TEXT,
    fetched_at   INTEGER NOT NULL CHECK (fetched_at >= 0 AND fetched_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Alert — one Suricata eve.json alert record. Suricata is an alert source and
-- nothing else on 26.7.
-- Source: /api/ids/service/query_alerts (timestamp, src_ip, src_port,
-- dest_ip, dest_port, proto, in_iface, alert as signature text, alert_sid,
-- alert_action, fileid, filepos). Survey: data source 2.
-- The pair (file_id, file_pos) is the durable identity, so replaying an
-- ingestion inserts nothing new. There is deliberately no severity column:
-- severity lives in ids_rule_info.
-- ---------------------------------------------------------------------------
CREATE TABLE alert (
    id                  INTEGER PRIMARY KEY,
    file_id             TEXT NOT NULL,
    file_pos            INTEGER NOT NULL CHECK (file_pos >= 0),
    occurred_at         INTEGER NOT NULL CHECK (occurred_at >= 0 AND occurred_at < 4102444800),
    ingested_at         INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    signature_id        INTEGER NOT NULL,
    signature           TEXT NOT NULL,
    alert_action        TEXT NOT NULL CHECK (alert_action IN ('allowed', 'blocked', 'unknown')),
    src_address         TEXT NOT NULL,
    src_port            INTEGER CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_address         TEXT NOT NULL,
    dst_port            INTEGER CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol            TEXT,
    in_interface_device TEXT,
    src_device_id       INTEGER REFERENCES device (id),
    src_segment_id      INTEGER REFERENCES segment (id),
    flow_ref            INTEGER,
    UNIQUE (file_id, file_pos)
);

CREATE INDEX idx_alert_occurred_at ON alert (occurred_at);
CREATE INDEX idx_alert_device_occurred_at ON alert (src_device_id, occurred_at);
CREATE INDEX idx_alert_signature ON alert (signature_id, occurred_at);

-- ---------------------------------------------------------------------------
-- eve.json ingestion cursor — a per-file byte-offset watermark over an
-- alert-only feed, with rotation detection.
-- Source: /api/ids/service/query_alerts (fileid, filepos) and
-- /api/ids/service/get_alert_logs (filename, size, modified, sequence).
-- Survey: data source 2 (a). Paging is offset-from-end-of-file and therefore
-- unstable, so (file_id, byte_offset) is the only durable resume point; it is
-- unique, and a duplicate insert fails.
-- ---------------------------------------------------------------------------
CREATE TABLE eve_ingest_cursor (
    id             INTEGER PRIMARY KEY,
    file_id        TEXT NOT NULL,
    byte_offset    INTEGER NOT NULL CHECK (byte_offset >= 0),
    file_sequence  INTEGER,
    rotation_state TEXT NOT NULL CHECK (rotation_state IN ('current', 'rotated', 'lost')),
    observed_at    INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    UNIQUE (file_id, byte_offset)
);

-- ---------------------------------------------------------------------------
-- Source availability — the modelled health of each of the five sources.
-- No source can be told apart from silence by an empty result alone (survey,
-- gap 11), so each one carries a state, the instant it was determined, and
-- the probe that determined it. Every screen reads this table; an unavailable
-- source is rendered as its own condition, never as an absence of data.
-- ---------------------------------------------------------------------------
CREATE TABLE source_availability (
    source     TEXT PRIMARY KEY
               CHECK (source IN ('filter_log', 'suricata_eve', 'netflow_insight',
                                 'dhcp_leases', 'resolver_dns')),
    state      TEXT NOT NULL
               CHECK (state IN ('reachable', 'present_but_disabled', 'unavailable')),
    probe      TEXT NOT NULL,
    detail     TEXT,
    checked_at INTEGER NOT NULL CHECK (checked_at >= 0 AND checked_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Pair volume observation — the daily per-address-pair volume Insight keeps,
-- de-duplicated.
-- Source: /api/diagnostics/networkinsight/top/FlowSourceAddrDetails/... with
-- a field list containing src_addr and dst_addr; the row carries total (the
-- octets or packets measure) and last_seen. Survey: data source 3.
--
-- Insight writes each flow once per interface AND once per direction, with
-- source and destination swapped on the outbound direction, so naive
-- summation double-counts. The de-duplication key is therefore explicit and
-- direction-free: (day_start_at, endpoint_low, endpoint_high, service_port,
-- protocol), where endpoint_low and endpoint_high are the two addresses in
-- lexicographic order. The interface and direction of the observation that
-- won the insert are kept for provenance only and are NOT part of the key.
-- Collectors insert with ON CONFLICT DO NOTHING.
--
-- service_port is min(src_port, dst_port) as computed by Insight — a heuristic
-- and not the real destination port, which the filter log carries exactly.
-- octets and packets are REAL because Insight pro-rates flows spanning a slice
-- boundary.
-- ---------------------------------------------------------------------------
CREATE TABLE pair_volume_observation (
    id                        INTEGER PRIMARY KEY,
    day_start_at              INTEGER NOT NULL CHECK (day_start_at >= 0 AND day_start_at < 4102444800),
    endpoint_low              TEXT NOT NULL,
    endpoint_high             TEXT NOT NULL,
    service_port              INTEGER NOT NULL
                              CHECK (service_port >= 0 AND service_port <= 65535),
    protocol                  TEXT NOT NULL,
    octets                    REAL NOT NULL CHECK (octets >= 0),
    packets                   REAL NOT NULL CHECK (packets >= 0),
    observed_direction        TEXT NOT NULL CHECK (observed_direction IN ('in', 'out', 'unknown')),
    observed_interface_device TEXT,
    last_seen_at              INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    ingested_at               INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    CHECK (endpoint_low <= endpoint_high),
    UNIQUE (day_start_at, endpoint_low, endpoint_high, service_port, protocol)
);

CREATE INDEX idx_pair_volume_day ON pair_volume_observation (day_start_at);

INSERT INTO schema_version (filename, applied_at)
VALUES ('0001_core.sql', CAST(strftime('%s', 'now') AS INTEGER));
