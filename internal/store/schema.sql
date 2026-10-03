-- opnview — the schema. One file, applied in place.
--
-- THERE ARE NO MIGRATIONS, AND THAT IS A DECISION RATHER THAN AN OMISSION.
-- Nothing is deployed and nobody has data, so numbered files, a
-- `schema_version` table and a runner would be machinery for a problem that
-- does not exist. This file is edited in place, and it is applied on every
-- start: every statement below is idempotent, so applying it to a database
-- that already carries it changes nothing. Migrations begin the day the
-- product runs somewhere with data worth keeping — this file becomes the
-- baseline and the first real migration is the one after it.
--
-- It is embedded into the binary by internal/store, which is the only code
-- that applies it. sql/schema-checks.sh applies the same file to a throwaway
-- database under /data and asserts every criterion the data model carries.
--
-- Every entity here is fed by a provider of one of the eight kinds surveyed in
-- docs/opnsense-api-survey.md, or by the runtime discovery described in that
-- document's "Runtime discovery" section. docs/data-model.md maps each table
-- and each column to its endpoint and API field, and docs/architecture.md
-- names the eight kinds and the seam a provider plugs into.
--
-- No provider name appears as a table name or as a column name anywhere in
-- this file. A provider is a row in the `provider` registry and a foreign key
-- to it; its name is a value, never an identifier. The single exception is
-- `eve_ingest_cursor`, which names a file format rather than a product and is
-- provisional until a second ingesting provider is surveyed.
--
-- Conventions applied throughout, and asserted by sql/schema-checks.sh:
--   * Every column holding an instant is an INTEGER UTC epoch in seconds and
--     its name ends in "_at". The three input shapes (epoch seconds, ISO
--     strings, year-less syslog strings) are normalised by the collector at
--     the boundary; see docs/data-model.md.
--   * No interface name, VLAN name, interface name, address or CIDR appears as
--     a literal. Every such value is discovered at runtime.
--   * No predicate anywhere matches on a description, a label or a name: a
--     interface's nature is never inferred from what it is called.
--
-- The upper timestamp bound below is 2100-01-01T00:00:00Z. It rejects a
-- millisecond value mistakenly stored as seconds, and a negative instant.

-- ---------------------------------------------------------------------------
-- Configuration. Bounded: one row per setting key.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS setting (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- account — one opnview login. Bounded: one row per account.
--
-- `account` and `session` are opnview's OWN terms, recorded as such in
-- docs/data-model.md, Vocabulary: OPNsense has users and API keys, and neither
-- names a login into a different product. Nothing here is read from an
-- endpoint.
--
-- THE ALGORITHM PARAMETERS ARE COLUMNS, AND THAT IS THE WHOLE DESIGN.
-- ROADMAP.md, "Rules that apply to every step", asks for Argon2id, a per-user
-- salt, and the parameters stored beside the hash so they can be raised later.
-- Raising them is therefore a change to the DEFAULTS the code writes for a NEW
-- record, and every record already written still verifies against the
-- parameters it was written with. That is parameter agility, and it is a column
-- layout rather than a registry: one algorithm is mandated, so there is no kind
-- with two implementations here and nothing to select at runtime.
--
-- password_algorithm is NOT constrained to a value list. A CHECK enumerating
-- 'argon2id' would have to be edited the day a second label exists, and this
-- file follows the same rule the provider registry follows: an implementation
-- name is data, never an identifier and never a CHECK.
--
-- The password itself is never stored and never read back. The digest is the
-- Argon2id output over (password, salt, parameters) and nothing else.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS account (
    id                   INTEGER PRIMARY KEY,
    login                TEXT NOT NULL UNIQUE CHECK (length(login) > 0),
    password_algorithm   TEXT NOT NULL CHECK (length(password_algorithm) > 0),
    password_memory_kib  INTEGER NOT NULL CHECK (password_memory_kib > 0),
    password_iterations  INTEGER NOT NULL CHECK (password_iterations > 0),
    password_parallelism INTEGER NOT NULL CHECK (password_parallelism > 0),
    password_salt        BLOB NOT NULL CHECK (length(password_salt) >= 16),
    password_digest      BLOB NOT NULL CHECK (length(password_digest) >= 16),
    created_at           INTEGER NOT NULL
                         CHECK (created_at >= 0 AND created_at < 4102444800),
    password_set_at      INTEGER NOT NULL
                         CHECK (password_set_at >= 0 AND password_set_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- session — one signed-in browser. Bounded: one row per session issued and not
-- yet expired and collected.
--
-- THE PRIMARY KEY IS A DIGEST, NOT THE TOKEN. The token exists in the cookie
-- and nowhere else: a database that is copied, backed up or sent by mistake —
-- the case the key file's own limitation is written against — hands over no
-- usable session. A lookup hashes what the cookie carried and compares digests.
--
-- TWO EXPIRIES, BOTH ENFORCED. `expires_at` is the ABSOLUTE cap, written once
-- when the session is issued and never moved. The SLIDING IDLE WINDOW is
-- `last_seen_at` plus a duration internal/auth carries, evaluated on every
-- request: it is not a column, because a stored copy of it would be a second
-- truth that a change to the window could not reach. An expired session is
-- refused and never renewed.
--
-- csrf_token is the session's own. A submission carrying another session's
-- token fails the comparison, which is what makes AC11's second half testable.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS session (
    token_digest TEXT PRIMARY KEY,
    account_id   INTEGER NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    csrf_token   TEXT NOT NULL CHECK (length(csrf_token) > 0),
    created_at   INTEGER NOT NULL CHECK (created_at >= 0 AND created_at < 4102444800),
    last_seen_at INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    expires_at   INTEGER NOT NULL CHECK (expires_at >= 0 AND expires_at < 4102444800)
);

CREATE INDEX IF NOT EXISTS idx_session_account
    ON session (account_id);
CREATE INDEX IF NOT EXISTS idx_session_expires_at
    ON session (expires_at);

-- ---------------------------------------------------------------------------
-- encrypted_credential — one credential opnview has to send to somebody else.
-- Bounded: one row per named credential.
--
-- WHY THIS IS NOT A `setting` ROW. `setting.value` is a single TEXT NOT NULL,
-- and a ciphertext is not a value: it needs the nonce it was sealed with, the
-- label of the algorithm that sealed it, and the identifier of the key that did
-- it, or it cannot be opened and a key that has been replaced cannot be told
-- apart from a ciphertext that has been tampered with. Those are four columns,
-- so this is a table.
--
-- WHY ENCRYPTION AND NOT HASHING. ROADMAP.md, "Rules that apply to every step":
-- these are not passwords. They are sent to the firewall and to MaxMind on
-- every call, so hashing them would make the product unable to work. They are
-- encrypted at rest with authenticated encryption and decrypted to be used.
--
-- The key is a FILE BESIDE THE DATABASE, never a row in it. The limit of that
-- is stated in README.md's limitations section in ROADMAP.md's own words, and
-- it is not restated on any screen.
--
-- algorithm and key_id are not constrained to value lists, for the same reason
-- password_algorithm is not: a future scheme is a second label, not a second
-- copy of the product.
--
-- `name` is opnview's own vocabulary for which credential this is. The two this
-- cycle writes are the OPNsense API secret and the MaxMind licence key — the
-- exact two ROADMAP.md names. The OPNsense API KEY is not one of them and is a
-- `setting` row: it is the Basic-auth username half of the pair, which the
-- roadmap does not list among the secrets, and putting it here would make a
-- third ciphertext where the settled design names two.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS encrypted_credential (
    name       TEXT PRIMARY KEY,
    algorithm  TEXT NOT NULL CHECK (length(algorithm) > 0),
    key_id     TEXT NOT NULL CHECK (length(key_id) > 0),
    nonce      BLOB NOT NULL CHECK (length(nonce) > 0),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Provider registry — one row per implementation that can feed opnview,
-- grouped by the kind of material it supplies. Bounded: one row per
-- implementation known to the project.
--
-- Registering an implementation is an INSERT, never a migration: nothing here
-- enumerates provider names in a CHECK. What is constrained is the `kind`, the
-- eight seams docs/architecture.md names, because a kind is a contract opnview
-- implements and not data a deployment supplies.
--
-- Endpoints, one citation per kind, all in docs/opnsense-api-survey.md:
--   firewall_log    /api/diagnostics/firewall/log            (data source 1)
--   security_event  /api/ids/service/query_alerts            (data source 2)
--   flow_volume     /api/diagnostics/networkinsight/...      (data source 3)
--   dhcp_lease      /api/kea/leases4/search,
--                   /api/dnsmasq/leases/search,
--                   /api/dhcpv4/leases/searchLease           (data source 4)
--   dns_lookup      /api/unbound/overview/search_queries,
--                   /api/diagnostics/log/core/dnsmasq        (data source 5)
--   geo_asn         the MaxMind GeoLite2 City and ASN databases, the second of
--                   the two outbound calls the project allows
--   measurement_sample
--                   /api/diagnostics/system/systemResources,
--                   /api/diagnostics/system/systemTemperature,
--                   /api/diagnostics/system/systemTime,
--                   /api/diagnostics/system/systemDisk,
--                   /api/diagnostics/activity/getActivity,
--                   /api/diagnostics/traffic/interface and
--                   /api/diagnostics/traffic/top/<interface names>
--                   ("The telemetry the data model calls gaps G9 and G10
--                   exists", and "The per-pair data is a live snapshot, not
--                   history")
--   reconciled_state
--                   no endpoint is read for it yet. It is the shape ten of the
--                   eleven surveyed sources that fit no kind at all produce:
--                   the current ban list of an intrusion-prevention engine, the
--                   peers of a tunnel, LLDP neighbours, FRR routes,
--                   certificates, Monit services, UPnP mappings and Tor
--                   circuits ("Shapes the model has no room for"). The kind
--                   exists so that such a source has a destination; no
--                   connector for any of them is written here.
--
-- is_active separates "opnview reads this one" from "this one is reachable".
-- A machine may have two implementations of a kind installed and running; the
-- model must say which one the data came from. On a freshly applied database
-- none is active: activeness is decided by the probe round, never by this file.
--
-- AND IT IS DECIDED BY THE OPERATOR BEFORE THE PROBE ROUND. This comment used to
-- stop at the line above, and it claimed a separation the code did not implement:
-- the round derived "opnview reads this one" from "this one is reachable" every
-- five minutes and nobody could overrule it. So one failed probe dropped a working
-- source, a network that was merely QUIET at that instant was indistinguishable
-- from an absent one, and two implementations of an exclusive kind both answering
-- made the round read NEITHER with no way for a human to break the tie. The operator's decision
-- now comes first and is a `setting` row per source — off is never read, on is
-- always read, auto is the probe's call and remains the default. See
-- internal/config, Selection. Availability is untouched by it: what the firewall
-- reported stays recorded exactly as reported, because selection is a decision and
-- not a claim about the firewall.
--
-- ACTIVENESS IS EXCLUSIVE PER KIND ONLY WHERE TWO PROVIDERS WOULD BE
-- INDISTINGUISHABLE, and that rule replaces the one-active-provider-per-kind
-- index this file used to carry. People run several intrusion-detection and
-- filtering engines at once, and the how-to corpus is people stacking them
-- (survey, "Four findings that bear on the collectors already written"), so
-- universal exclusivity was wrong. What replaces it is derived from this schema rather
-- than chosen kind by kind:
--
--   A KIND ADMITS SEVERAL CONCURRENTLY ACTIVE PROVIDERS EXACTLY WHEN THE
--   IDENTITY OF ITS DESTINATION ROWS INCLUDES THE PROVIDER.
--
-- Where the identity includes it, two active providers can neither collide nor
-- double-count: every row says who reported it, so one fact reported twice is
-- two attributed rows and a screen can show them per provider or side by side.
-- Where it does not, the second provider's rows are indistinguishable from the
-- first's by origin, so it would either collide with them or silently double a
-- figure nobody could decompose. Kind by kind:
--
--   kind                destination identity                        several?
--   firewall_log        flow.log_digest                             no
--   security_event      (provider_id, provider_event_key)           YES
--   flow_volume         (day_start_at, endpoints, port, protocol)   no
--   dhcp_lease          (address, generation_key, provider_id)      YES
--   dns_lookup          dns_resolution.lookup_key                   no
--   geo_asn             geo_asn.address                             no
--   measurement_sample  (subject, measure, sampled_at, provider)    YES
--   reconciled_state    (provider_id, set_key, captured_at)         YES
--
-- dhcp_lease IS CONCURRENT, AND THE DEPLOYMENT IS THE ORDINARY ONE: one server
-- issuing on one VLAN and another on a second, two scopes with no overlap. The
-- objection to it was that two lease providers would put one machine on the
-- screen twice under two names, and the client identity cascade is what answers
-- it -- the cascade keys on the DHCP client identifier first and on the MAC
-- second, neither of which is scoped to an interface, so a machine leased on two
-- VLANs by two servers resolves to ONE client holding TWO leases, which is the
-- truth. The duplication the objection feared needs two servers issuing on the
-- SAME scope, and that is a misconfiguration of the firewall rather than a shape
-- this model should contort itself to absorb.
--
-- Its identity therefore names the PROVIDER and not the backend. `backend` is a
-- normalised vocabulary of response shapes, not a provider key: two providers
-- could report the same backend -- a second implementation reading a Kea running
-- somewhere else would -- and the rule above is stated in terms of the provider,
-- so keying on the backend would have satisfied the letter of it and not the
-- substance. No `ifnull` wrapper is needed here, unlike measurement_sample: a
-- lease with no provider is not reachable, because a lease is issued by a server,
-- and the column is NOT NULL.
--
-- geo_asn is exclusive because its row is one cache entry per address and a
-- second provider would overwrite the first's answer rather than add to it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider (
    id            INTEGER PRIMARY KEY,
    kind          TEXT NOT NULL
                  CHECK (kind IN ('firewall_log', 'security_event', 'flow_volume',
                                  'dhcp_lease', 'dns_lookup', 'geo_asn',
                                  'measurement_sample', 'reconciled_state')),
    provider_key  TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 0 CHECK (is_active IN (0, 1)),
    registered_at INTEGER NOT NULL CHECK (registered_at >= 0 AND registered_at < 4102444800),
    UNIQUE (kind, provider_key)
);

-- The index that admitted one active provider per kind, dropped by name.
--
-- It is named here rather than merely deleted because this file is applied on
-- every start: an object the file no longer declares would otherwise survive in
-- a database that already carries it, and the old index would then reject the
-- second concurrent activation that is now the normal case.
-- Naming a removal is the only form of removal an idempotent single-file schema
-- can express, and it is not a migration runner arriving by the back door.
DROP INDEX IF EXISTS uq_provider_active_per_kind;

-- At most one active provider per EXCLUSIVE kind, by the rule above. A partial
-- index, so the many inactive rows of one kind do not collide with each other,
-- and so the four kinds whose rows carry their provider are left alone.
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_active_per_exclusive_kind
    ON provider (kind)
    WHERE is_active = 1
      AND kind NOT IN ('security_event', 'dhcp_lease', 'measurement_sample',
                       'reconciled_state');

-- ---------------------------------------------------------------------------
-- Interface — one interface as OPNsense defines it: a VLAN, a physical link or
-- a tunnel the firewall terminates, carrying rules and counters of its own.
-- The word is OPNsense's, not opnview's: the endpoint is named
-- /api/interfaces/overview/interfaces_info and the menu is Interfaces >
-- Assignments. Every discovered column below is that endpoint's own field name.
-- Source: /api/interfaces/overview/interfaces_info (identifier, description,
-- device, status, enabled, link_type, vlan_tag). Survey: "Runtime discovery"
-- (i).
--
--   identifier   the configuration key the firewall stores this interface
--                under; API field `identifier`
--   device       the network device the interface runs on; API field `device`,
--                the same token /api/diagnostics/interface/get_interface_names
--                keys its map by and the filter log reports in its `interface`
--                field. In this schema `device` means the network device and
--                nothing else; the machine on the network is a `client`.
--   description  the description entered by the user in the OPNsense UI; API
--                field `description`, falling back to the upper-cased
--                identifier
--   status       the link state the API reports; API field `status`
--   enabled      the administrative state the API reports; API field `enabled`
--   link_type    the raw link type the API reports; API field `link_type`
--
-- status and enabled are TEXT and are stored VERBATIM, with no CHECK and no
-- normalisation. They are stored rather than dropped because an interface with
-- no traffic and an interface that is down or switched off are three different
-- facts that look identical on a screen, and these are the only columns that
-- tell them apart -- the same distinction rule.logs_matches carries for a rule
-- that does not log. They are TEXT because the survey establishes the two
-- fields and NOT their encoding: writing a CHECK or a 0/1 column here would be
-- inventing a vocabulary OPNsense has not published. Normalising them is a
-- step-4 decision to be taken against a live firewall, and recorded then.
--
-- user_label and link_kind have no OPNsense equivalent and are opnview's own,
-- deliberately: OPNsense carries a single description field, so a label the
-- maintainer sets inside opnview needs a column the firewall cannot overwrite,
-- and OPNsense publishes no normalised class of link type. Relabelling never
-- overwrites discovery and a refresh of discovery never overwrites a label.
-- link_kind is the normalised class of link_type; is_tunnel derives from
-- link_kind alone and from nothing that carries a name.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS interface (
    id             INTEGER PRIMARY KEY,
    identifier     TEXT NOT NULL UNIQUE,
    device         TEXT NOT NULL UNIQUE,
    description    TEXT NOT NULL,
    user_label     TEXT,
    status         TEXT,
    enabled        TEXT,
    link_type      TEXT NOT NULL,
    link_kind      TEXT NOT NULL
                   CHECK (link_kind IN ('physical', 'vlan', 'tunnel', 'other')),
    is_tunnel      INTEGER GENERATED ALWAYS AS
                   (CASE WHEN link_kind = 'tunnel' THEN 1 ELSE 0 END) VIRTUAL,
    vlan_tag       INTEGER,
    address_family INTEGER CHECK (address_family IS NULL OR address_family IN (4, 6)),
    first_seen_at  INTEGER NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at   INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Owner — a person the maintainer has told opnview about, so that one person's
-- several machines read as one person rather than as three unrelated cards.
--
-- THIS TERM IS OPNVIEW'S OWN, and deliberately so. Neither OPNsense nor any
-- product opnview reads has a notion of a person: a lease carries a hostname, a
-- MAC and a client identifier, and none of them names a human being. The word
-- is taken from the question the entity answers — whose machine is this — not
-- invented for something a product already names.
--
-- OWNERSHIP IS ASSIGNED BY THE USER AND IS NEVER INFERRED. Nothing derives an
-- owner from a hostname, a MAC prefix, a vendor hint, an address or any other
-- observed value: there is no DEFAULT on client.owner_id, no generated column,
-- no trigger, and no predicate anywhere in this schema that matches on a name.
-- It is the same rule interface.user_label already obeys, for the same reason.
--
-- A CLIENT WITH NO OWNER IS NORMAL, not an error and not a row to hide. Most
-- machines on a network belong to nobody in particular, and a per-person view
-- must therefore carry an explicit unassigned bucket rather than silently
-- dropping every client the user has not got round to attributing. The
-- diagnostic query "Clients per owner" in sql/queries/diagnostics.sql is that
-- guarantee, written down and executed.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS owner (
    id           INTEGER PRIMARY KEY,
    display_name TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL CHECK (created_at >= 0 AND created_at < 4102444800),
    updated_at   INTEGER NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Interface map — first-class join key number one: the filter log's raw
-- device name to the user-given interface description.
-- Source: /api/diagnostics/interface/get_interface_names. Survey: "Runtime
-- discovery" (i). A device name absent from this table is the modelled
-- "not found" state carried on flow.interface_lookup_state.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS interface_map (
    device        TEXT PRIMARY KEY,
    description   TEXT NOT NULL,
    interface_id  INTEGER REFERENCES interface (id),
    discovered_at INTEGER NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Rule — first-class join key number two: the filter log's rid / the rule
-- uuid, to the rule description.
-- Source: /api/firewall/filter/search_rule (uuid, description, action,
-- direction, log, is_automatic). Survey: "Runtime discovery" (ii). A rid
-- matching no row here is normal (the rule was removed) and is carried on
-- flow.rule_lookup_state.
--
-- logs_matches is the endpoint's `log` field: whether the rule writes a line
-- to the filter log when a packet matches it. It is a SECOND observation-point
-- limit, and the reason it is stored rather than dropped: a rule that does not
-- log passes traffic opnview can never see, so "this interface shows nothing"
-- has two possible causes and only this column tells them apart. It is
-- nullable, because a rule discovered from a source that did not report the
-- flag must read as "not reported" rather than as "does not log".
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rule (
    id            INTEGER PRIMARY KEY,
    pf_label      TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL,
    action        TEXT NOT NULL
                  CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction     TEXT CHECK (direction IS NULL OR direction IN ('in', 'out', 'any')),
    logs_matches  INTEGER CHECK (logs_matches IS NULL OR logs_matches IN (0, 1)),
    is_automatic  INTEGER NOT NULL DEFAULT 0 CHECK (is_automatic IN (0, 1)),
    discovered_at INTEGER NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Client — one physical machine, identified by the documented cascade, most
-- stable first: DHCP client identity, then MAC, then address within its
-- interface over a validity interval.
-- Sources: the lease endpoints of data source 4 (hostname, hwaddr/mac,
-- client_id, duid, iaid, mac_info) and, for a client never seen in a lease,
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
CREATE TABLE IF NOT EXISTS client (
    id                INTEGER PRIMARY KEY,
    identity_kind     TEXT NOT NULL
                      CHECK (identity_kind IN ('dhcp_client_id', 'mac', 'address_in_interface')),
    identity_key      TEXT NOT NULL,
    interface_id      INTEGER REFERENCES interface (id),
    mac               TEXT CHECK (mac IS NULL OR (length(mac) = 17 AND mac = lower(mac))),
    hostname          TEXT,
    vendor_hint       TEXT,
    last_address      TEXT,
    -- Ownership, assigned by the user and never inferred. No DEFAULT, so a
    -- client is unowned until somebody says otherwise; owner_assigned_at
    -- records when that happened, and the CHECK below keeps the two from
    -- disagreeing.
    owner_id          INTEGER REFERENCES owner (id),
    owner_assigned_at INTEGER CHECK (owner_assigned_at IS NULL
                                     OR (owner_assigned_at >= 0 AND owner_assigned_at < 4102444800)),
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
    UNIQUE (identity_kind, identity_key),
    CHECK ((owner_id IS NULL) = (owner_assigned_at IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_client_interface ON client (interface_id, id);

-- An owner's machines. owner_id is NULL-bearing, and SQLite keeps the
-- unassigned clients at the head of this index rather than omitting them, so
-- the index serves the unassigned bucket as well as the assigned ones.
CREATE INDEX IF NOT EXISTS idx_client_owner ON client (owner_id, id);

-- ---------------------------------------------------------------------------
-- DHCP lease — one observed lease generation.
-- Source: /api/kea/leases4/search, /api/dnsmasq/leases/search, and the
-- best-effort ISC plugin endpoint. Survey: data source 4. The MAC field is
-- hwaddr on the two current backends and mac on the legacy plugin; it is
-- normalised here.
--
-- starts_at IS NULLABLE, AND generation_key IS WHAT MAKES A RE-POLL IDEMPOTENT.
-- The two were one column and meant two things. Kea reports `valid_lifetime`,
-- so `expire` minus it is a real validity start. Dnsmasq reports no start at
-- all, and a reserved lease reports neither a start nor an expiry; the earlier
-- schema stored the expiry, or the day of observation, in a NOT NULL starts_at
-- and was honest only because a comment beside it said so. A backend that
-- cannot know now says so by storing NULL, and the discriminator that keeps a
-- second poll of one lease from inserting a second row is its own column.
--
-- Identity is (address, generation_key, provider_id): one row per lease
-- generation per server. The server is in it because this kind admits several
-- concurrently active providers (see the registry above), so the SAME address can
-- legitimately be leased by two servers on two scopes, and those are two leases
-- rather than one contested row.
--
-- generation_key is composed by the collector and carries the name of what it
-- rests on, so a reader can tell a real start from a substitute without
-- consulting the backend: `start:<epoch>` when the backend reported a validity
-- start, `expiry:<epoch>` when it reported only an expiry, and
-- `observed_day:<epoch>` for a standing reservation, which is one row per day
-- rather than one per poll. The term is opnview's own and is recorded as such in
-- docs/data-model.md, Vocabulary; nothing in this file parses it or matches on
-- it, and it is opaque to every query.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dhcp_lease (
    id             INTEGER PRIMARY KEY,
    client_id      INTEGER REFERENCES client (id),
    -- provider_id NAMES THE SERVER THAT ISSUED THIS LEASE, and it is a fact a
    -- reader is entitled to rather than a component of a de-duplication key.
    -- This kind admits several concurrently active providers -- one server on one
    -- VLAN, another on a second -- so a client can hold leases from two servers
    -- at once, and "which server gave this machine its address" is then a real
    -- question with a per-lease answer. It joins to provider.display_name, so a
    -- screen prints the server's name without reconstructing it from an index or
    -- parsing a composite; the diagnostic query "Leases per client and issuing
    -- server" in sql/queries/diagnostics.sql is that read, written down and
    -- executed.
    --
    -- NOT NULL, because a lease is issued by a server: there is no provider-less
    -- lease to model, which is also why the identity below needs no ifnull
    -- wrapper where measurement_sample's does.
    provider_id    INTEGER NOT NULL REFERENCES provider (id),
    -- backend is the normalised vocabulary of RESPONSE SHAPES, and it is kept
    -- beside provider_id rather than replaced by it: it is what says which
    -- field-name normalisation produced the row. It is NOT an identity for the
    -- server, and it is not in the uniqueness constraint any more.
    backend        TEXT NOT NULL CHECK (backend IN ('kea', 'dnsmasq', 'isc')),
    address        TEXT NOT NULL,
    mac            TEXT CHECK (mac IS NULL OR (length(mac) = 17 AND mac = lower(mac))),
    hostname       TEXT,
    dhcp_client_id TEXT,
    duid           TEXT,
    iaid           TEXT,
    vendor_hint    TEXT,
    lease_state    TEXT NOT NULL
                   CHECK (lease_state IN ('active', 'expired', 'reserved', 'unknown')),
    interface_id   INTEGER REFERENCES interface (id),
    starts_at      INTEGER CHECK (starts_at IS NULL
                                  OR (starts_at >= 0 AND starts_at < 4102444800)),
    generation_key TEXT NOT NULL CHECK (length(generation_key) > 0),
    expires_at     INTEGER CHECK (expires_at IS NULL OR (expires_at >= 0 AND expires_at < 4102444800)),
    observed_at    INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    UNIQUE (address, generation_key, provider_id)
);

CREATE INDEX IF NOT EXISTS idx_dhcp_lease_observed_at ON dhcp_lease (observed_at);
-- A client's lease history, ordered by the instant opnview read the lease rather
-- than by the validity start: the start is now NULL on every backend that cannot
-- report one, and an index leading on a column that is null for a whole backend
-- would order that backend's leases arbitrarily.
CREATE INDEX IF NOT EXISTS idx_dhcp_lease_client ON dhcp_lease (client_id, observed_at);

-- ---------------------------------------------------------------------------
-- Flow — one filter-log record, allowed or blocked. The blocked events are a
-- projection of this table, never a second ingestion path: every blocked
-- record is the same log line as an allowed one with a different action.
-- Source: /api/diagnostics/firewall/log. Survey: data source 1. The record is
-- deduplicated on __digest__, stored as log_digest.
--
-- traffic_scope is east-west exactly when both endpoints sit in a discovered
-- interface, and north-south otherwise. It derives from interface membership and
-- from nothing else — no address, no CIDR, no name.
--
-- log_reason is the filter log's own `reason` field: WHY the packet was
-- logged, which is not the same question as what was done to it. A record
-- whose reason is not a rule match was not denied by a rule at all, and
-- rendering it as a rule denial would attribute a decision to a rule that made
-- none. The value is stored verbatim and is never parsed or matched on: the
-- survey establishes the field but not its value set, so no CHECK enumerates
-- one here — that would be inventing a vocabulary OPNsense has not published.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS flow (
    id                     INTEGER PRIMARY KEY,
    log_digest             TEXT NOT NULL UNIQUE,
    observed_at            INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    ingested_at            INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    interface_device       TEXT NOT NULL,
    interface_lookup_state TEXT NOT NULL
                           CHECK (interface_lookup_state IN ('resolved', 'not_found', 'pending')),
    src_interface_id       INTEGER REFERENCES interface (id),
    dst_interface_id       INTEGER REFERENCES interface (id),
    src_client_id          INTEGER REFERENCES client (id),
    dst_client_id          INTEGER REFERENCES client (id),
    src_address            TEXT NOT NULL,
    dst_address            TEXT NOT NULL,
    src_port               INTEGER CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_port               INTEGER CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol               TEXT NOT NULL,
    ip_version             INTEGER NOT NULL CHECK (ip_version IN (4, 6)),
    action                 TEXT NOT NULL
                           CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction              TEXT NOT NULL CHECK (direction IN ('in', 'out', 'unknown')),
    log_reason             TEXT,
    packet_bytes           INTEGER NOT NULL CHECK (packet_bytes >= 0),
    rid                    TEXT,
    rule_id                INTEGER REFERENCES rule (id),
    rule_lookup_state      TEXT NOT NULL
                           CHECK (rule_lookup_state IN ('resolved', 'not_found', 'pending')),
    -- traffic_scope is written by the collector and pinned by the CHECK below
    -- to exactly one expression over interface membership, so no row can carry a
    -- value that disagrees with its interfaces — the same guarantee a generated
    -- column gives, demonstrated by a failing insert in the checks.
    --
    -- It is a plain column rather than a generated one for a query-plan
    -- reason, and for no other: SQLite never reports an index as covering for
    -- a query that reads a generated column, virtual or stored, so making this
    -- one generated would cost the Overview, Matrix and Interface queries their
    -- covering plans — all three group by this value. The derivation is
    -- unchanged and is still interface membership alone: no address, no CIDR, no
    -- name, no assumed addressing plan.
    traffic_scope          TEXT NOT NULL,
    CHECK (traffic_scope = CASE
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south'
                           END)
);

-- Index shape. Column count is close to irrelevant to SQLite read speed; what
-- matters is selectivity and whether the index answers the query without
-- fetching the row. The two indexes serving an AGGREGATING screen query
-- therefore carry the measures that query sums or counts, so its plan reads
-- the index alone — "USING COVERING INDEX" in EXPLAIN QUERY PLAN. The two
-- serving a DETAIL listing are left narrow: a listing returns most of the row
-- anyway, so widening them would buy nothing and cost write throughput on
-- every ingested record. docs/data-model.md names, per query, which index
-- covers it and which measures were added for that purpose.
--
-- There is exactly one index leading on observed_at alone, deliberately: it is
-- the load-bearing index of the Overview and Matrix queries, and a second one
-- with the same leading column would hide its loss.
CREATE INDEX IF NOT EXISTS idx_flow_observed_at ON flow
    (observed_at, traffic_scope, action, packet_bytes,
     src_interface_id, dst_interface_id, src_client_id, rule_id, rule_lookup_state);
CREATE INDEX IF NOT EXISTS idx_flow_src_interface_observed_at ON flow
    (src_interface_id, observed_at, src_client_id, packet_bytes, action,
     traffic_scope, dst_address);
CREATE INDEX IF NOT EXISTS idx_flow_src_client_observed_at ON flow (src_client_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_flow_blocked_observed_at ON flow (observed_at) WHERE action = 'block';

CREATE VIEW IF NOT EXISTS blocked_event AS
SELECT
    id AS flow_id,
    observed_at,
    ingested_at,
    interface_device,
    interface_lookup_state,
    src_interface_id,
    dst_interface_id,
    src_client_id,
    dst_client_id,
    src_address,
    dst_address,
    src_port,
    dst_port,
    protocol,
    ip_version,
    direction,
    log_reason,
    packet_bytes,
    rid,
    rule_id,
    rule_lookup_state,
    traffic_scope
FROM flow
WHERE action = 'block';

-- ---------------------------------------------------------------------------
-- Blocklist — one named list the resolver refused a lookup against, and the
-- purpose a user assigned to it.
--
-- Source of the NAME: /api/unbound/overview/search_queries, which returns a
-- `blocklist` field on every row alongside `client`, `domain`, `time`,
-- `action`, `source`, `rcode`, `dnssec_status` and `uuid`. Survey: data
-- source 5, "Response shape". The name is stored verbatim, exactly as the
-- endpoint reported it, and is never parsed, normalised or matched on.
--
-- THE PURPOSE IS ASSIGNED BY THE USER AND IS NEVER INFERRED. The endpoint
-- reports what a list is CALLED and says nothing about what it is FOR: the
-- survey establishes the field and nothing beyond it, and a machine reading
-- "hagezi-pro" or "oisd-small" learns nothing about advertising versus threat
-- from those characters. Classifying a list by its name would be the same
-- defect as classifying an interface by its description, so purpose obeys
-- exactly the rule interface.user_label and client.owner_id already obey:
-- no DEFAULT, no generated expression, no trigger, and no predicate anywhere
-- in this schema that matches on the name. Only a statement somebody wrote
-- can fill it.
--
-- purpose IS NULL is the normal state of a list nobody has classified yet,
-- and a screen renders it as "purpose not assigned" rather than guessing or
-- hiding the rows. The vocabulary is opnview's own and is recorded as such in
-- docs/data-model.md, Vocabulary.
--
-- Bounded: one row per distinct list name the resolver has ever reported. A
-- resolver subscribes to tens of lists, not millions, and the table is never
-- purged: a purpose is user input, as an owner is, and purging a lookup must
-- not discard the classification work behind it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS blocklist (
    id                  INTEGER PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    purpose             TEXT CHECK (purpose IS NULL OR purpose IN
                        ('advertising', 'tracking', 'threat', 'parental', 'other')),
    purpose_assigned_at INTEGER CHECK (purpose_assigned_at IS NULL
                        OR (purpose_assigned_at >= 0 AND purpose_assigned_at < 4102444800)),
    first_seen_at       INTEGER NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at        INTEGER NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    CHECK ((purpose IS NULL) = (purpose_assigned_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- DNS resolution — one resolver lookup, the sole source of site names on
-- OPNsense 26.7.
-- Source: /api/unbound/overview/search_queries (client, domain, time, action,
-- source, rcode, dnssec_status, blocklist), or the free-text dnsmasq query log.
-- Survey: data source 5.
--
-- lookup_key IS THE IDENTITY, AND IT IS COMPOSED BY opnview RATHER THAN READ.
-- The endpoint returns a `uuid` field and it is NULL ON EVERY ROW, measured
-- against a live firewall on 2026-09-27, so the identity this table was first
-- designed around does not exist. The column was called lookup_uuid and is now
-- called lookup_key, because a name that says uuid while holding a hash is worse
-- than a comment that says it: the comment is at least next to the column, and
-- the name travels into every query somebody writes.
--
-- WHAT COMPOSES IT. For the Unbound provider: the string "content:" followed by
-- the SHA-256 of the resolver name, the lookup instant, and the row's own
-- client, domain, action, source, rcode, dnssec_status and blocklist fields,
-- joined by a unit separator. The endpoint is a ring buffer with no cursor and
-- no working window, so every pass re-reads rows already stored; without a
-- content-addressed identity each pass would insert them again and multiply
-- every per-client lookup count by the number of passes that saw it.
--
-- THE TRADE, RECORDED HERE BECAUSE IT IS A REAL LOSS. Two genuinely distinct
-- lookups by the same client, for the same domain, with the same verdict, in the
-- same second collapse into one row, so such a pair is under-counted by one.
-- That direction is chosen deliberately: the endpoint returns nothing that could
-- tell those two apart -- no identifier, no sub-second instant, no sequence --
-- so the only alternative is a key that is not stable across passes, which would
-- over-count every row on every poll instead of under-counting a rare
-- coincidence. An under-count of identical lookups is also the direction that
-- cannot invent traffic. The composition is opnview's own and the term is
-- recorded as such in docs/data-model.md, Vocabulary.
--
-- A provider that does supply a stable row identifier stores it here unchanged;
-- the column says "key" and not "digest" for that reason.
--
-- dnssec_status is the validation verdict the resolver reached for the lookup.
-- It is stored verbatim and is never parsed or matched on: the survey
-- establishes the field but not its value set, so no CHECK enumerates one --
-- that would be inventing a vocabulary OPNsense has not published. It is NULL
-- for a resolver that reports no verdict, the dnsmasq query log included,
-- which is "not reported" and not "unvalidated".
--
-- blocklist_id is the list that refused the lookup, resolved at ingest from
-- the row's `blocklist` field against the blocklist table above. It is NULL
-- when the endpoint reported no list -- which is every passed lookup, and also
-- a blocked one the resolver did not attribute. A screen renders the second
-- case as "blocked, list not recorded" rather than merging it into a bucket;
-- the action column is what tells the two apart.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dns_resolution (
    id             INTEGER PRIMARY KEY,
    lookup_key     TEXT NOT NULL UNIQUE,
    client_address TEXT NOT NULL,
    client_id      INTEGER REFERENCES client (id),
    domain         TEXT NOT NULL,
    resolver       TEXT NOT NULL CHECK (resolver IN ('unbound', 'dnsmasq')),
    action         TEXT NOT NULL CHECK (action IN ('pass', 'block', 'drop', 'unknown')),
    answer_source  TEXT,
    rcode          TEXT,
    dnssec_status  TEXT,
    blocklist_id   INTEGER REFERENCES blocklist (id),
    looked_up_at   INTEGER NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    ingested_at    INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800)
);

CREATE INDEX IF NOT EXISTS idx_dns_resolution_looked_up_at ON dns_resolution (looked_up_at);
CREATE INDEX IF NOT EXISTS idx_dns_resolution_client ON dns_resolution (client_address, looked_up_at);

-- "Which list refused what, over this period" is an index search rather than a
-- scan of a growing table. The NULL-bearing leading column keeps the
-- unattributed blocked lookups at the head of the index instead of omitting
-- them, so the "list not recorded" rows are served by it too.
CREATE INDEX IF NOT EXISTS idx_dns_resolution_blocklist ON dns_resolution (blocklist_id, looked_up_at);

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
CREATE TABLE IF NOT EXISTS domain_attribution (
    flow_id                   INTEGER PRIMARY KEY
                              REFERENCES flow (id) ON DELETE CASCADE,
    dns_resolution_id         INTEGER NOT NULL
                              REFERENCES dns_resolution (id) ON DELETE CASCADE,
    site_name                 TEXT NOT NULL,
    correlation_delay_seconds INTEGER NOT NULL CHECK (correlation_delay_seconds >= 0),
    attributed_at             INTEGER NOT NULL CHECK (attributed_at >= 0 AND attributed_at < 4102444800)
);

CREATE INDEX IF NOT EXISTS idx_domain_attribution_resolution ON domain_attribution (dns_resolution_id);

-- ---------------------------------------------------------------------------
-- Geo / ASN — the geo and ASN enrichment of one address.
-- Keyed per address rather than per prefix: the dataset answers with a prefix
-- but SQLite has no natural longest-prefix join. dataset_build_at is the build
-- date of the dataset that answered and provider_id names the provider that
-- supplied it, so a stale enrichment is visible and attributable. A cache miss
-- is a row whose lookup_state is 'miss', never an absent row; a 'pending' row
-- has not been looked up yet and names no provider.
-- Source: the geo_asn kind of the provider registry — today the MaxMind
-- GeoLite2 City and ASN databases, the second and last of the two outbound
-- calls the project allows. Acquisition is step 4.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS geo_asn (
    address          TEXT PRIMARY KEY,
    provider_id      INTEGER REFERENCES provider (id),
    lookup_state     TEXT NOT NULL CHECK (lookup_state IN ('resolved', 'miss', 'pending')),
    country_code     TEXT,
    country_name     TEXT,
    latitude         REAL,
    longitude        REAL,
    asn              INTEGER,
    operator         TEXT,
    dataset_build_at INTEGER
                     CHECK (dataset_build_at IS NULL
                            OR (dataset_build_at >= 0 AND dataset_build_at < 4102444800)),
    looked_up_at     INTEGER NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    CHECK (lookup_state <> 'resolved'
           OR (country_code IS NOT NULL
               AND dataset_build_at IS NOT NULL
               AND provider_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_geo_asn_looked_up_at ON geo_asn (looked_up_at);

-- ---------------------------------------------------------------------------
-- Rule-info cache — the normalised severity and category of one rule identity,
-- per provider.
-- Source: /api/ids/settings/get_rule_info/<sid> for the Suricata provider.
-- Survey: data source 2, response shape, and gap 3: query_alerts overwrites
-- the nested alert object with the signature string, so severity and category
-- are not in the event record and must be resolved separately and cached. A
-- rule identity with no row here is a cache miss, and the Alerts screen
-- renders it as an explicit unknown severity rather than dropping the event.
--
-- Keyed per provider: two providers may name a rule identically and mean
-- different rules, so the identity alone is not a key. The identity is TEXT,
-- because a provider whose rules are named rather than numbered cannot use an
-- integer.
--
-- normalised_severity is opnview's own ordered vocabulary; provider_severity
-- keeps the raw value the provider reported, verbatim, so the normalisation
-- stays auditable. docs/data-model.md gives the vocabulary, its order and the
-- mapping from the Suricata numeric scale.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_rule_info (
    provider_id         INTEGER NOT NULL REFERENCES provider (id),
    rule_identity       TEXT NOT NULL,
    normalised_severity TEXT NOT NULL
                        CHECK (normalised_severity IN ('critical', 'high', 'medium',
                                                       'low', 'informational')),
    provider_severity   TEXT,
    category            TEXT,
    rule_source         TEXT,
    fetched_at          INTEGER NOT NULL CHECK (fetched_at >= 0 AND fetched_at < 4102444800),
    PRIMARY KEY (provider_id, rule_identity)
);

-- ---------------------------------------------------------------------------
-- Security event — one event contributed by a provider of the security_event
-- kind. Today that is Suricata, which on 26.7 is an alert source and nothing
-- else; the table describes the kind, not the product.
-- Source: /api/ids/service/query_alerts (timestamp, src_ip, src_port,
-- dest_ip, dest_port, proto, in_iface, alert as signature text, alert_sid,
-- alert_action, fileid, filepos). Survey: data source 2.
--
-- Identity is (provider_id, provider_event_key): the key the provider itself
-- guarantees stable, composed by the collector — for Suricata, from the eve
-- file id and the byte offset inside it. That is ONE idempotence guarantee,
-- placed on the core. The ingestion coordinate is NOT duplicated here: it is
-- a transport concern and lives in eve_ingest_cursor, which carries a
-- different guarantee about a different thing (see docs/data-model.md).
--
-- normalised_severity is nullable and is NULL for every Suricata event,
-- because the API destroys the nested alert object before opnview can read it
-- (survey, gap 3): its severity is resolved through provider_rule_info. The
-- column exists for a provider that ships severity inside its event.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS security_event (
    id                  INTEGER PRIMARY KEY,
    provider_id         INTEGER NOT NULL REFERENCES provider (id),
    provider_event_key  TEXT NOT NULL,
    occurred_at         INTEGER NOT NULL CHECK (occurred_at >= 0 AND occurred_at < 4102444800),
    ingested_at         INTEGER NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    rule_identity       TEXT NOT NULL,
    signature           TEXT NOT NULL,
    event_action        TEXT NOT NULL CHECK (event_action IN ('allowed', 'blocked', 'unknown')),
    normalised_severity TEXT
                        CHECK (normalised_severity IS NULL
                               OR normalised_severity IN ('critical', 'high', 'medium',
                                                          'low', 'informational')),
    src_address         TEXT NOT NULL,
    src_port            INTEGER CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_address         TEXT NOT NULL,
    dst_port            INTEGER CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol            TEXT,
    in_interface_device TEXT,
    src_client_id       INTEGER REFERENCES client (id),
    src_interface_id    INTEGER REFERENCES interface (id),
    flow_ref            INTEGER,
    UNIQUE (provider_id, provider_event_key)
);

CREATE INDEX IF NOT EXISTS idx_security_event_occurred_at ON security_event (occurred_at);
CREATE INDEX IF NOT EXISTS idx_security_event_client_occurred_at ON security_event (src_client_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_security_event_rule_occurred_at
    ON security_event (provider_id, rule_identity, occurred_at);

-- ---------------------------------------------------------------------------
-- eve.json ingestion cursor — a per-file byte-offset watermark over an
-- alert-only feed, with rotation detection.
-- Source: /api/ids/service/query_alerts (fileid, filepos) and
-- /api/ids/service/get_alert_logs (filename, size, modified, sequence).
-- Survey: data source 2 (a). Paging is offset-from-end-of-file and therefore
-- unstable, so (file_id, byte_offset) is the only durable resume point; it is
-- unique, and a duplicate insert fails.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS eve_ingest_cursor (
    id             INTEGER PRIMARY KEY,
    file_id        TEXT NOT NULL,
    byte_offset    INTEGER NOT NULL CHECK (byte_offset >= 0),
    file_sequence  INTEGER,
    rotation_state TEXT NOT NULL CHECK (rotation_state IN ('current', 'rotated', 'lost')),
    observed_at    INTEGER NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    UNIQUE (file_id, byte_offset)
);

-- ---------------------------------------------------------------------------
-- Source availability — the modelled health of each registered provider, one
-- row per registry row.
-- No source can be told apart from silence by an empty result alone (survey,
-- gap 11), so each provider carries a state, the instant it was determined,
-- and the probe that determined it. Every screen reads this table; an
-- unavailable provider is rendered as its own condition, never as an absence
-- of data.
--
-- Availability is reachability, and nothing else. Which provider opnview
-- actually reads is provider.is_active: a machine may have two reachable
-- implementations of one kind. For the geo_asn kind the row describes the
-- reachability of the DATASET; geo_asn.lookup_state describes a single address
-- lookup against it. Two different questions, two different columns.
--
-- The provider set is data, so there is no CHECK enumerating provider names
-- here: registering one is an INSERT into `provider` plus an INSERT here.
-- Probe endpoints per kind are tabulated in docs/data-model.md, each cited in
-- docs/opnsense-api-survey.md.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS source_availability (
    provider_id INTEGER PRIMARY KEY REFERENCES provider (id),
    state       TEXT NOT NULL
                CHECK (state IN ('reachable', 'present_but_disabled', 'unavailable')),
    probe       TEXT NOT NULL,
    detail      TEXT,
    checked_at  INTEGER NOT NULL CHECK (checked_at >= 0 AND checked_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Pair volume observation — the daily per-address-pair volume, de-duplicated.
--
-- IT IS DERIVED, NOT COLLECTED, AND THAT IS THE MAINTAINER'S RULING. No
-- collector writes this table and none will: the only per-pair endpoint the
-- survey found carries neither a port nor a protocol, and `flow` carries both
-- exactly, from the filter log. So these rows are step 5's to COMPUTE from
-- `flow`, and nobody should look for the collector that fills them. The SAMPLED
-- per-pair volume, which is a different thing — a live rate snapshot rather than
-- a period total — stays in measurement_sample, where 4A already puts it.
--
-- The de-duplication key below is kept exactly as surveyed, because the
-- derivation must produce the same key: a per-pair figure summed over a day is
-- direction-free whether it came from the firewall's own aggregate or from
-- opnview's own records.
--
-- Origin of the shape: /api/diagnostics/networkinsight/top/FlowSourceAddrDetails/...
-- with a field list containing src_addr and dst_addr; the row carries total (the
-- octets or packets measure) and last_seen. Survey: data source 3.
--
-- Insight writes each flow once per interface AND once per direction, with
-- source and destination swapped on the outbound direction, so naive
-- summation double-counts. The de-duplication key is therefore explicit and
-- direction-free: (day_start_at, endpoint_low, endpoint_high, service_port,
-- protocol), where endpoint_low and endpoint_high are the two addresses in
-- lexicographic order. The interface and direction of the observation that
-- won the insert are kept for provenance only and are NOT part of the key.
-- The derivation inserts with ON CONFLICT DO NOTHING.
--
-- service_port is min(src_port, dst_port) as computed by Insight — a heuristic
-- and not the real destination port, which the filter log carries exactly.
-- octets and packets are REAL because Insight pro-rates flows spanning a slice
-- boundary.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pair_volume_observation (
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

CREATE INDEX IF NOT EXISTS idx_pair_volume_day ON pair_volume_observation (day_start_at);

CREATE TABLE IF NOT EXISTS volume_aggregate_1h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_1h_slot
    ON volume_aggregate_1h (period_start_at, src_interface_id,
                            ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_24h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_24h_slot
    ON volume_aggregate_24h (period_start_at, src_interface_id,
                             ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_7d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_7d_slot
    ON volume_aggregate_7d (period_start_at, src_interface_id,
                            ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_30d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    INTEGER NOT NULL REFERENCES interface (id),
    dst_interface_id    INTEGER REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_30d_slot
    ON volume_aggregate_30d (period_start_at, src_interface_id,
                             ifnull(dst_interface_id, -1), ifnull(peer_address, ''));

-- ---------------------------------------------------------------------------
-- The per-owner aggregates — the same four periods, keyed on the person
-- rather than on the interface pair.
--
-- Why they exist: the four tables above carry interfaces and peer addresses
-- and no owner dimension at all, so "how much did Bob's machines move this
-- month" had nothing to read once the question left the flow horizon, which is
-- bounded by the retention_seconds row of setting. That was gap G11 in
-- docs/widget-catalogue.md and this family closes it.
--
-- Keyed as the four above are: the slot is (period_start_at, owner, scope),
-- with the same ifnull wrapper on the NULL-bearing column, because SQLite
-- treats NULLs in a UNIQUE index as distinct. One difference is deliberate and
-- is not a drift: traffic_scope is part of the key here, where above it is
-- derivable from whether dst_interface_id is set. A per-owner slot has no
-- destination interface to derive it from, so it is keyed explicitly.
--
-- owner_id IS NULL IS THE UNASSIGNED BUCKET, AND IT IS NOT OPTIONAL. Ownership
-- is assigned by hand and most machines on a network belong to nobody in
-- particular, so a per-person aggregate that dropped the unowned clients would
-- under-report the network while looking complete. The column is therefore
-- NULL-bearing by design and the unassigned slot is an ordinary row --
-- the same guarantee the "Clients per owner" diagnostic already carries.
--
-- Nothing here infers an owner. These rows are computed from flow joined to
-- client.owner_id, which only a statement somebody wrote can fill.
--
-- client_count is the number of distinct clients that contributed to THIS
-- slot. It is a per-slot figure and MUST NOT be summed across slots: the same
-- machine active in two hours would be counted twice. Summing bytes, allowed
-- and blocked across slots is correct; summing client_count is not.
--
-- Refresh contract: identical to the four above, and written down once in
-- docs/data-model.md. A slot is recomputed when a flow inside its window is
-- ingested; the current slot is recomputed on every pass; computed_at against
-- the newest ingested_at inside the window is the staleness rule; a closed
-- slot is left alone. One addition specific to this family: reassigning a
-- client to another owner, or clearing its owner, invalidates every slot whose
-- window holds a flow from that client, because ownership is an attribute of
-- the person and not of the flow.
--
-- Observation-point limit applies here exactly as above: these are lower
-- bounds, and a per-person screen must say so.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_1h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_1h_slot
    ON owner_volume_aggregate_1h (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_24h (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_24h_slot
    ON owner_volume_aggregate_24h (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_7d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_7d_slot
    ON owner_volume_aggregate_7d (period_start_at, ifnull(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_30d (
    id                  INTEGER PRIMARY KEY,
    period_start_at     INTEGER NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       INTEGER NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            INTEGER REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               INTEGER NOT NULL CHECK (bytes >= 0),
    allowed_connections INTEGER NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections INTEGER NOT NULL CHECK (blocked_connections >= 0),
    client_count        INTEGER NOT NULL CHECK (client_count >= 0),
    computed_at         INTEGER NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_30d_slot
    ON owner_volume_aggregate_30d (period_start_at, ifnull(owner_id, -1), traffic_scope);

-- ---------------------------------------------------------------------------
-- Collection gap — one interval opnview knows it did not cover.
--
-- THE TERM IS OPNVIEW'S OWN. No product opnview reads has a notion of "the
-- data I failed to collect": every source returns what it has and says nothing
-- about what it did not return (survey, gap 11). The word is taken from the
-- question the row answers — what is missing from this history — and it is
-- recorded as opnview's own in docs/data-model.md, Vocabulary.
--
-- Why it is a row rather than a log line: a gap is read by a screen. A byte
-- total over a window that contains a gap is not a lower bound for the usual
-- physical reason, it is a lower bound because opnview was not looking, and
-- those are two different sentences to put next to a figure. The three
-- detections that write here are all measured findings rather than
-- suppositions:
--   * the filter log's __digest__ is not a server-side cursor, so a poll whose
--     newest stored line is older than the oldest line the page returned has
--     missed everything in between (survey, "The filter log's digest is not a
--     server-side cursor");
--   * an eve.json rotation that discarded the file a watermark pointed at is a
--     permanent loss (survey, data source 2 (c));
--   * the resolver query window is ignored outright — a 5-minute and a 24-hour
--     request return the same ~410-second span — so what came back is the most
--     recent 1000 lookups and never the window that was asked for (survey,
--     "The resolver window is not honoured at all").
--
-- reason is a CLOSED vocabulary and it is opnview's own, legitimately: these
-- are opnview's own detections, not values an endpoint reports, so enumerating
-- them invents nothing. detail carries the free text.
--
-- Growing: one row per detected gap. Purged by detected_at, because a gap
-- describing a window whose data would itself have been purged says nothing.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS collection_gap (
    id                INTEGER PRIMARY KEY,
    provider_id       INTEGER NOT NULL REFERENCES provider (id),
    interval_start_at INTEGER NOT NULL
                      CHECK (interval_start_at >= 0 AND interval_start_at < 4102444800),
    interval_end_at   INTEGER NOT NULL
                      CHECK (interval_end_at >= 0 AND interval_end_at < 4102444800),
    reason            TEXT NOT NULL
                      CHECK (reason IN ('digest_outside_returned_window',
                                        'eve_rotation_lost',
                                        'resolver_window_not_honoured')),
    detail            TEXT,
    detected_at       INTEGER NOT NULL CHECK (detected_at >= 0 AND detected_at < 4102444800),
    CHECK (interval_end_at >= interval_start_at)
);

CREATE INDEX IF NOT EXISTS idx_collection_gap_detected_at ON collection_gap (detected_at);
CREATE INDEX IF NOT EXISTS idx_collection_gap_interval
    ON collection_gap (provider_id, interval_start_at, interval_end_at);

-- ---------------------------------------------------------------------------
-- Measurement sample — one numeric reading of one subject at one instant.
--
-- IT IS A PROVIDER KIND, and it is the kind most of the ecosystem fits. Of the
-- ten surveyed sources that fit a shape this model already had, eight fit this
-- one (survey, "What does produce data, and in what shape"): WireGuard's
-- per-peer transfer counters, HAProxy's frontend and backend counters, vnStat's
-- per-interface volume, and the UPS, SMART and sensor readings of NUT, apcupsd
-- and LLDPd. It was a table with no kind, so none of them had a provider_key, an
-- availability row or a place in provider.kind; it has all three now.
--
-- THE TERM IS OPNVIEW'S OWN, and the reason is that two different needs turned
-- out to have one shape. OPNsense answers for CPU, memory, temperature, disk,
-- uptime and per-interface packet and byte counters
-- (/api/diagnostics/system/systemResources,
-- /api/diagnostics/system/systemTemperature,
-- /api/diagnostics/system/systemTime, /api/diagnostics/system/systemDisk,
-- /api/diagnostics/activity/getActivity, /api/diagnostics/traffic/interface —
-- survey, "The telemetry the data model calls gaps G9 and G10 exists"), and it
-- publishes no collective noun for them. Separately,
-- /api/diagnostics/traffic/top/<interface names> is a LIVE RATE SNAPSHOT and no
-- endpoint exposes a flow aggregate over a past window (survey, "The per-pair
-- data is a live snapshot, not history"), so per-pair volume has to be sampled
-- too. A gauge and a sampled pair volume are the same five facts: a subject, a
-- measure, a unit, a value and an instant. One table carries both.
--
-- THIS IS NOT AN ENTITY-ATTRIBUTE-VALUE TABLE, and the distinction matters
-- because docs/data-model.md forbids one. An EAV table stores attributes of
-- heterogeneous entities as untyped name/value pairs, losing every type and
-- every constraint. This table stores ONE kind of thing — a numeric reading
-- over time — with a typed REAL value, a mandatory unit, and a subject and a
-- measure that are declared terms rather than free text, and the screens that
-- read it filter on (subject_kind, subject_key, measure) over a range of sampled_at,
-- which is exactly what the index below serves. It is the shape every
-- time-series store uses, including the aggregate tables OPNsense itself keeps
-- under /var/netflow.
--
-- subject_kind and subject_key together name what was measured. The three terms
-- opnview's own sampler uses, and a provider may declare a fourth — HAProxy's
-- subject is a backend, and a UPS is neither an interface nor a pair:
--   'firewall'      the firewall itself. subject_key names the PART measured
--                   when the reading is of a part -- a temperature sensor, a
--                   mounted filesystem -- and is the empty string when the
--                   reading is of the whole machine, as uptime and load are.
--                   It never carries the firewall's URL, which is configuration
--                   held elsewhere. The value is a label and nothing branches
--                   on its text.
--   'interface'     subject_key is the network DEVICE name, the same token
--                   interface_map keys by, so the reading joins to an interface
--                   without a foreign key that a discovery refresh could break
--   'endpoint_pair' subject_key is the two addresses in lexicographic order
--                   joined by a space, the same canonical ordering
--                   pair_volume_observation enforces with endpoint_low and
--                   endpoint_high
--
-- THE THREE VOCABULARIES ARE OPNVIEW'S OWN AND ARE EXTENSIBLE BY A PROVIDER,
-- WITHOUT A SCHEMA CHANGE. They used to be three CHECKs enumerating what 4A's
-- own sampler reads, and that was correct for exactly one provider. The kind now
-- absorbs eight surveyed sources that share no vocabulary at all: a UPS reports
-- volts, SMART reports reallocated sectors, and HAProxy's subject is a backend
-- rather than an interface or a client (survey, "What does produce data, and in
-- what shape"). A closed CHECK would have made every one of them a schema
-- change, which is precisely the plugin-hostile design this promotion exists to
-- remove.
--
-- What is still enforced is the SHAPE of a term rather than its membership of a
-- list: a term is non-empty, lower-case and carries no space, so a typo or a
-- free-text sentence is rejected and two spellings of one measure cannot both
-- exist. What opnview's own sampler uses is the vocabulary listed above, and
-- that list is in internal/store/rows.go as named constants; a provider's own
-- term is an ordinary value beside them.
--
-- Identity is (subject_kind, subject_key, measure, sampled_at, provider):
-- re-reading the same instant is idempotent, which is what a sampler restarting
-- inside one interval needs, and the provider is part of it because this kind
-- admits several concurrently active providers — two of them reading one subject
-- at one instant are two readings, not a collision. It is a unique INDEX over
-- ifnull(provider_id, -1) rather than a table constraint, because SQLite treats
-- NULLs in a UNIQUE constraint as distinct and the firewall's own gauges carry a
-- null provider: a bare UNIQUE would have made every gauge non-idempotent. The
-- same ifnull wrapper the volume aggregates already use, for the same reason.
--
-- Growing: one row per reading. Purged by sampled_at.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS measurement_sample (
    id           INTEGER PRIMARY KEY,
    provider_id  INTEGER REFERENCES provider (id),
    subject_kind TEXT NOT NULL CHECK (length(subject_kind) > 0
                                      AND subject_kind = lower(subject_kind)
                                      AND instr(subject_kind, ' ') = 0),
    subject_key  TEXT NOT NULL,
    measure      TEXT NOT NULL CHECK (length(measure) > 0
                                      AND measure = lower(measure)
                                      AND instr(measure, ' ') = 0),
    unit         TEXT NOT NULL CHECK (length(unit) > 0
                                      AND unit = lower(unit)
                                      AND instr(unit, ' ') = 0),
    value        REAL NOT NULL,
    sampled_at   INTEGER NOT NULL CHECK (sampled_at >= 0 AND sampled_at < 4102444800)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_measurement_sample_reading
    ON measurement_sample (subject_kind, subject_key, measure, sampled_at,
                           ifnull(provider_id, -1));

-- "This subject's readings of this measure over this period" is an index
-- search rather than a scan of a growing table.
CREATE INDEX IF NOT EXISTS idx_measurement_sample_subject
    ON measurement_sample (subject_kind, subject_key, measure, sampled_at, value);
CREATE INDEX IF NOT EXISTS idx_measurement_sample_sampled_at
    ON measurement_sample (sampled_at);

-- ---------------------------------------------------------------------------
-- Reconciled state — the complete set of things of one type a provider reports,
-- as of one instant, plus the things in it.
--
-- THE TERM IS OPNVIEW'S OWN, and it names a SHAPE rather than a thing. No
-- product opnview reads has a word for "the current set, replaced wholesale on
-- each poll": each of them names only its own contents — decisions, peers,
-- neighbours, routes, circuits. The survey found that ten of the eleven sources
-- fitting no existing kind are this one shape (survey, "Shapes the model has no
-- room for"), so the word is taken from what those ten have in common: the set
-- is reconciled against the previous one rather than appended to.
--
-- WHY IT IS NOT ANOTHER APPEND-ONLY KIND. Every other kind here records events:
-- a row arrives, it is stored, it is never contradicted. These sources record
-- the opposite — the surveyed ban-list endpoint runs its own tool with no limit
-- and no `since`, so every poll re-dumps the whole list, and a decision carries
-- a TTL and no timestamp at all (survey, "What does produce data, and in what
-- shape"). Storing such a dump as events would
-- either insert the whole set again on every poll or, with de-duplication on
-- content, keep a thing that has gone for ever.
--
-- A DEPARTURE IS THE WHOLE POINT, AND IT IS WHAT THE INSTANT IS FOR. Because a
-- snapshot is asserted COMPLETE at captured_at, a key present in one snapshot
-- and absent from the next has LEFT — the ban expired, the peer was removed, the
-- neighbour went away — and the model can say so rather than silently keeping the
-- item or silently dropping it. The state_item_departure view below is that
-- statement, computed and not stored: a departure is a fact about two snapshots
-- and duplicating it into a column would let the column and the snapshots
-- disagree.
--
-- WHAT IT DELIBERATELY DOES NOT CARRY. No status, no category, no severity, no
-- enabled flag, no display name. This kind is the one piece of the model designed
-- from research rather than from a working collector, and this project has twice
-- been caught building against a document, so it holds exactly what the examined
-- sources need and nothing a screen might one day want: a set, an instant, an
-- identity within the set, the attributes, and an optional validity end.
--
-- Growing: one snapshot per poll per set. Purged by captured_at, and the items
-- go with their snapshot through ON DELETE CASCADE.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS state_snapshot (
    id          INTEGER PRIMARY KEY,
    provider_id INTEGER NOT NULL REFERENCES provider (id),
    -- set_key names WHICH set, because one provider commonly reports several:
    -- one surveyed engine reports decisions and alerts, and a tunnel reports
    -- its peers. It is a
    -- term the provider declares, constrained in shape and not in membership,
    -- for the same reason measurement_sample's vocabularies are. Nothing here
    -- parses it or branches on its text.
    set_key     TEXT NOT NULL CHECK (length(set_key) > 0
                                     AND set_key = lower(set_key)
                                     AND instr(set_key, ' ') = 0),
    -- captured_at is the instant at which the set was COMPLETE. It is the
    -- load-bearing column: without it a set is a bag of rows and a departure is
    -- undetectable.
    captured_at INTEGER NOT NULL CHECK (captured_at >= 0 AND captured_at < 4102444800),
    UNIQUE (provider_id, set_key, captured_at)
);

CREATE INDEX IF NOT EXISTS idx_state_snapshot_captured_at ON state_snapshot (captured_at);

-- "The latest snapshots of this set" is an index search rather than a scan of a
-- growing table, which is what the departure view walks.
CREATE INDEX IF NOT EXISTS idx_state_snapshot_set
    ON state_snapshot (provider_id, set_key, captured_at, id);

CREATE TABLE IF NOT EXISTS state_item (
    id             INTEGER PRIMARY KEY,
    snapshot_id    INTEGER NOT NULL REFERENCES state_snapshot (id) ON DELETE CASCADE,
    -- item_key is the identity WITHIN the set, supplied by the provider: the
    -- banned address, the peer's public key, the neighbour's chassis id. It is
    -- unique per snapshot, so one poll cannot report one thing twice, and it is
    -- the token a departure is computed on. It is TEXT because a provider whose
    -- things are named rather than numbered has to fit, and it is stored verbatim.
    item_key       TEXT NOT NULL CHECK (length(item_key) > 0),
    -- attributes is the provider's own fields, as a JSON object, and it obeys
    -- tier 2 of the attribute rule in docs/data-model.md: displayed on a detail
    -- screen, never aggregated, never filtered on, never joined. A field a screen
    -- needs to aggregate or filter on earns a typed column, after that provider's
    -- shape has been surveyed rather than guessed. NULL is a thing with no
    -- attributes at all, which is a normal state.
    attributes     TEXT CHECK (attributes IS NULL OR json_type(attributes) = 'object'),
    -- valid_until_at is the end of the thing's own validity when the provider
    -- states one — a surveyed ban decision carries a TTL and nothing else. It is NOT
    -- how a departure is detected: an item is gone when the next complete
    -- snapshot omits it, which is the only signal every one of these sources
    -- gives. An expiry that has passed while the item is still reported is the
    -- provider's business, and opnview reports both rather than choosing.
    valid_until_at INTEGER CHECK (valid_until_at IS NULL
                                  OR (valid_until_at >= 0 AND valid_until_at < 4102444800)),
    UNIQUE (snapshot_id, item_key)
);

-- The departure statement: what was in the previous complete snapshot of a set
-- and is not in the latest one.
--
-- It is the latest pair and not every pair, deliberately: "what has just left"
-- is the question a screen asks, and a view over every consecutive pair would
-- grow with the history for no reader. A set with only one snapshot yields no
-- rows, which is correct — nothing can be said to have left a set observed once.
CREATE VIEW IF NOT EXISTS state_item_departure AS
WITH latest AS (
    SELECT provider_id, set_key, max(captured_at) AS captured_at
    FROM state_snapshot
    GROUP BY provider_id, set_key
),
previous AS (
    SELECT s.provider_id, s.set_key, max(s.captured_at) AS captured_at
    FROM state_snapshot AS s
    JOIN latest AS l ON l.provider_id = s.provider_id AND l.set_key = s.set_key
    WHERE s.captured_at < l.captured_at
    GROUP BY s.provider_id, s.set_key
)
SELECT
    p.provider_id                AS provider_id,
    p.set_key                    AS set_key,
    i.item_key                   AS item_key,
    i.attributes                 AS attributes,
    i.valid_until_at             AS valid_until_at,
    p.captured_at                AS last_present_at,
    l.captured_at                AS absent_since_at
FROM previous AS p
JOIN latest AS l ON l.provider_id = p.provider_id AND l.set_key = p.set_key
JOIN state_snapshot AS ps ON ps.provider_id = p.provider_id
                         AND ps.set_key = p.set_key
                         AND ps.captured_at = p.captured_at
JOIN state_item AS i ON i.snapshot_id = ps.id
WHERE NOT EXISTS (
    SELECT 1
    FROM state_snapshot AS ls
    JOIN state_item AS li ON li.snapshot_id = ls.id
    WHERE ls.provider_id = p.provider_id
      AND ls.set_key = p.set_key
      AND ls.captured_at = l.captured_at
      AND li.item_key = i.item_key
);

-- ---------------------------------------------------------------------------
-- The defaults the model guarantees exist.
--
-- Every statement below is written so that applying this file again inserts
-- nothing: the schema is applied on every start, and a default that reappeared
-- would silently undo a setting the user changed.
-- ---------------------------------------------------------------------------

-- retention_seconds: the purge horizon, in seconds, so it can be set to a few
-- hours as easily as to months. 7776000 is 90 days, the documented default; 0
-- means unlimited and purges nothing. No retention duration appears anywhere
-- else: every query and every index reads the horizon from this row.
--
-- aggregate_mode: 'full' shows everything; 'no_domains' is the aggregate mode
-- the roadmap asks for — volumes, interfaces, countries and operators, with no
-- domain name read at all. The Map screen query is that query, and it reads
-- neither dns_resolution nor domain_attribution.
INSERT INTO setting (key, value, updated_at) VALUES
    ('retention_seconds', '7776000', CAST(strftime('%s', 'now') AS INTEGER)),
    ('aggregate_mode', 'full', CAST(strftime('%s', 'now') AS INTEGER))
ON CONFLICT (key) DO NOTHING;

-- The provider registry, seeded with the implementations that exist and have
-- been surveyed today — one row per IMPLEMENTATION, not one per kind. Unbound
-- and Dnsmasq are two providers of the dns_lookup kind; Kea, Dnsmasq and ISC
-- dhcpd are three of the dhcp_lease kind. Step-1 detection already tells them
-- apart, so "two providers of one kind" is exercised by real rows.
--
-- These names are DATA, not configuration. A firewall with no Suricata still
-- gets a Suricata row, in the 'unavailable' state: that is the modelled-state
-- design working, not a hardcoded assumption about the installation. Nothing
-- in the DDL, in a query or in an index tests any of these strings. ISC dhcpd
-- keeps its row although /api/dhcpv4/leases/searchLease answered 404 on the
-- firewall the survey probed: a 404 says the end-of-life plugin is not
-- installed THERE, which is the 'unavailable' state working, not a reason to
-- forget the implementation exists.
--
-- No provider is active. Activeness says which implementation opnview reads,
-- and it is decided by the probe round in internal/collect against a live
-- firewall; this file has no way to know and must not pretend to. Every
-- citation below is in docs/opnsense-api-survey.md, and docs/data-model.md
-- tabulates them.
INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at) VALUES
    ('firewall_log',   'pf',                'pf filter log',        0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('security_event', 'suricata',          'Suricata',             0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('flow_volume',    'insight',           'NetFlow / Insight',    0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'kea',               'Kea DHCP',             0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'dnsmasq',           'Dnsmasq DHCP',         0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dhcp_lease',     'isc',               'ISC dhcpd',            0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dns_lookup',     'unbound',           'Unbound',              0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('dns_lookup',     'dnsmasq',           'Dnsmasq resolver',     0, CAST(strftime('%s', 'now') AS INTEGER)),
    ('geo_asn',        'maxmind_geolite2',  'MaxMind GeoLite2',     0, CAST(strftime('%s', 'now') AS INTEGER)),
    -- The measurement_sample kind's first implementation is the sampler 4A
    -- already wrote: the live per-pair traffic snapshot and the firewall's own
    -- gauges. It was registered under flow_volume because the kind did not exist,
    -- and it moves here with the key it already has. The firewall's OWN gauges
    -- still carry a null provider_id, because the machine reporting on itself
    -- implements no external contract; what this row accounts for is the sampled
    -- per-pair volume, which is a source's material.
    ('measurement_sample', 'insight',       'NetFlow / Insight sampling',
                                                                    0, CAST(strftime('%s', 'now') AS INTEGER))
ON CONFLICT (kind, provider_key) DO NOTHING;

-- THE reconciled_state KIND HAS NO REGISTRY ROW, AND THAT IS NOT AN OMISSION.
-- Registering a provider means there is an implementation to probe: a row with
-- none would be probed by nothing, so its availability would read "not yet
-- probed" for ever and a screen would show a source nobody is looking at. The
-- kind exists so that the ten surveyed sources of that shape have a destination;
-- writing a connector for any of them is not this cycle's work, and inventing
-- rows for one of those products now would be exactly the speculative
-- registration this comment refuses. The kind is a contract; a row is a claim
-- that something implements it.
--
-- flow_volume keeps its row and has no implementation either, for the opposite
-- reason: pair_volume_observation is now DERIVED from flow (see its own section),
-- so nothing collects that kind. The row records that the implementation exists;
-- the netflow probe that used to answer for it did not disappear, it moved with
-- the sampler to the measurement_sample row above, which is the row whose
-- material it actually governs.

-- Availability is a modelled state, so exactly one row exists per registry row
-- from the first apply onwards. Until a probe has run, each provider is
-- 'unavailable' with the probe recorded as not yet run — never an absent row,
-- which a screen could not tell apart from a provider that is fine. The row
-- set is derived from the registry, so registering a provider and forgetting
-- its availability row is not possible here, and the NOT EXISTS guard is what
-- makes a second apply leave an already-probed row alone.
INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
SELECT p.id, 'unavailable', 'not_yet_probed', NULL, 0
FROM provider AS p
WHERE NOT EXISTS (SELECT 1 FROM source_availability a WHERE a.provider_id = p.id);
