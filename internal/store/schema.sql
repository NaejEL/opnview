-- opnview — the schema. One file, applied in place.
--
-- THERE ARE NO MIGRATIONS, AND THAT IS A DECISION RATHER THAN AN OMISSION.
-- The one deployment is the maintainer's live validation installation, whose
-- history is re-collectable from the firewall (ROADMAP.md, step 5, "Rebuilding
-- the database after step 5B"), so numbered files, a
-- `schema_version` table and a runner would be machinery for a problem that
-- does not exist. This file is edited in place, and it is applied on every
-- start: every statement below is idempotent, so applying it to a database
-- that already carries it changes nothing (and Open skips it when the
-- database records this file's digest, applied_schema). Migrations begin the day the
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
-- The engine and the namespace (decision 13 of
-- specs/SPEC-backend-completion-plugins.md). The store is PostgreSQL with the
-- TimescaleDB extension; every core object lives in the schema `opnview`, never
-- in `public`, so a plugin's schema (prefixed, plugintables.go) and the
-- learned mirror's (`opnsense`) never share a namespace with the core.
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE SCHEMA IF NOT EXISTS opnview;
SET LOCAL search_path TO opnview, public;

-- THE CALENDAR MONTH OF AN INSTANT, IN UTC. Every instant is stored as integer
-- epoch seconds (the JSON API serves them so, byte for byte), and the monthly
-- aggregates and the purge need the month an instant falls in: its first second
-- and the first second of the next month, 28 to 31 days later. Computed in UTC
-- whatever the server's time zone.
CREATE OR REPLACE FUNCTION month_start_at(at BIGINT) RETURNS BIGINT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$ SELECT CAST(extract(epoch FROM date_trunc('month', to_timestamp(at) AT TIME ZONE 'UTC')) AS BIGINT) $$;

CREATE OR REPLACE FUNCTION next_month_start_at(at BIGINT) RETURNS BIGINT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$ SELECT CAST(extract(epoch FROM date_trunc('month', to_timestamp(at) AT TIME ZONE 'UTC')
                                     + interval '1 month') AS BIGINT) $$;

-- ROW IDS ARE ASSIGNED AS THEY ALWAYS WERE: one more than the largest in use.
-- Every table whose rows are numbered by `id` keeps the numbering the store
-- was written against, and that every widget response carries -- a key such as
-- "interface:3" is that number. A PostgreSQL sequence would advance on every
-- upsert that conflicts and skip numbers a later insert then takes, so a run
-- would number its interfaces and clients differently from the same run a
-- moment earlier. A BEFORE INSERT trigger therefore gives a row without an id
-- the largest id of its table plus one, under a transaction-scoped advisory
-- lock on the table, so two concurrent writers can never take the same number:
-- the writers of one table are serialised, which is what the store was
-- written against. The lock's first key, 1, keeps it apart from the
-- instance lock (store.go). The trigger function is one per table, with
-- static SQL, so its plan is prepared once per connection.
CREATE OR REPLACE FUNCTION install_assign_id(target regclass) RETURNS void
LANGUAGE plpgsql AS $install$
DECLARE
    name text := (SELECT relname FROM pg_class WHERE oid = target);
BEGIN
    EXECUTE format($function$
        CREATE OR REPLACE FUNCTION %I() RETURNS trigger LANGUAGE plpgsql AS $body$
        BEGIN
            IF NEW.id IS NULL THEN
                PERFORM pg_advisory_xact_lock(1, %s);
                NEW.id := coalesce((SELECT max(id) FROM %s), 0) + 1;
            END IF;
            RETURN NEW;
        END
        $body$
    $function$, name || '_assign_id', target::oid::integer, target);
    EXECUTE format('CREATE OR REPLACE TRIGGER assign_id BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %I()',
                   target, name || '_assign_id');
END
$install$;

-- ---------------------------------------------------------------------------
-- Configuration. Bounded: one row per setting key.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS setting (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- applied_schema — the digest of the schema file last applied to this
-- database. Bounded: one row. Open applies this file only when the digest
-- recorded here is not the embedded file's (internal/store/schema.go), so a
-- restart of the same build, and a test database cloned from the template,
-- skip an apply that would change nothing; a database another build wrote is
-- applied as before.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS applied_schema (
    singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    digest     TEXT NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    applied_at BIGINT NOT NULL CHECK (applied_at >= 0 AND applied_at < 4102444800)
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
    id                   BIGINT PRIMARY KEY,
    login                TEXT NOT NULL UNIQUE CHECK (length(login) > 0),
    password_algorithm   TEXT NOT NULL CHECK (length(password_algorithm) > 0),
    password_memory_kib  BIGINT NOT NULL CHECK (password_memory_kib > 0),
    password_iterations  BIGINT NOT NULL CHECK (password_iterations > 0),
    password_parallelism BIGINT NOT NULL CHECK (password_parallelism > 0),
    password_salt        BYTEA NOT NULL CHECK (length(password_salt) >= 16),
    password_digest      BYTEA NOT NULL CHECK (length(password_digest) >= 16),
    created_at           BIGINT NOT NULL
                         CHECK (created_at >= 0 AND created_at < 4102444800),
    password_set_at      BIGINT NOT NULL
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
    account_id   BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    csrf_token   TEXT NOT NULL CHECK (length(csrf_token) > 0),
    created_at   BIGINT NOT NULL CHECK (created_at >= 0 AND created_at < 4102444800),
    last_seen_at BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    expires_at   BIGINT NOT NULL CHECK (expires_at >= 0 AND expires_at < 4102444800)
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
    nonce      BYTEA NOT NULL CHECK (length(nonce) > 0),
    ciphertext BYTEA NOT NULL CHECK (length(ciphertext) > 0),
    updated_at BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Provider registry — one row per implementation that can feed opnview,
-- grouped by the kind of material it supplies. Bounded: one row per
-- implementation known to the project.
--
-- Registering an implementation is an INSERT, never a migration: nothing here
-- enumerates provider names in a CHECK. And since the plugin cycle
-- (specs/SPEC-backend-completion-plugins.md, Decisions) nothing enumerates the
-- KINDS either: a plugin may declare a kind of its own, with tables of its own
-- (docs/plugins.md), and its provider row names that kind. What the CHECK keeps
-- is the shape of a kind name, a snake_case word. The core kinds -- the seams
-- listed below -- are defined once, with their record contracts, in
-- internal/collect/provider (contract.go), which docs/plugins.md is tested
-- against.
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
--   ip_geolocation  a dataset placing public addresses; the built-in provider
--                   is the maxmind plugin (MaxMind GeoLite2 City and ASN),
--                   which downloads through the plugin host's fetch facility
--                   (docs/plugins.md). Its answers are rows of geo_asn.
--   measurement_sample
--                   /api/diagnostics/system/systemResources,
--                   /api/diagnostics/system/systemTemperature,
--                   /api/diagnostics/system/systemTime,
--                   /api/diagnostics/system/systemDisk,
--                   /api/diagnostics/cpu_usage/stream (the processor; it
--                   replaced /api/diagnostics/activity/getActivity, which
--                   carries no numeric processor field),
--                   /api/diagnostics/traffic/interface and
--                   /api/diagnostics/traffic/top/<interface names>
--                   ("The telemetry the data model calls gaps G9 and G10
--                   exists", and "The per-pair data is a live snapshot, not
--                   history")
--   resolver_cache  /api/unbound/diagnostics/dumpcache ("Resolver cache and
--                   local data, read from source for the resolver-cache
--                   cycle"): the records the resolver holds in its cache and
--                   the answer addresses of the names it resolved, which the
--                   query report does not carry. "Cache" is Unbound's own word
--                   (unbound-control(8), dump_cache).
--   resolver_local_data
--                   /api/unbound/diagnostics/listlocaldata (same section): the
--                   records the resolver serves from its local data, host
--                   overrides among them. "Local data" is Unbound's own word
--                   (unbound.conf(5), local-data; unbound-control(8),
--                   list_local_data).
--   registrable_domain
--                   a list giving a site name its registrable domain; the
--                   built-in provider is the publicsuffix plugin (the Public
--                   Suffix List, from publicsuffix.org, through the fetch
--                   facility). It supplies no row of observed data. It is a
--                   kind because it has a provider, an availability and
--                   failures to record, exactly as ip_geolocation does.
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
--   ip_geolocation      geo_asn.address                             no
--   measurement_sample  (subject, measure, sampled_at, provider)    YES
--   reconciled_state    (provider_id, set_key, captured_at)         YES
--   resolver_cache      (provider_id, owner_name, rrtype, value,    YES
--                        first_seen_at) of resource_record_observation
--   resolver_local_data the same, of the same table                  YES
--   registrable_domain  one list file on disk, no row                no
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
-- substance. No `coalesce` wrapper is needed here, unlike measurement_sample: a
-- lease with no provider is not reachable, because a lease is issued by a server,
-- and the column is NOT NULL.
--
-- ip_geolocation is exclusive because its row is one cache entry per address and a
-- second provider would overwrite the first's answer rather than add to it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider (
    id            BIGINT PRIMARY KEY,
    kind          TEXT NOT NULL
                  CHECK (length(kind) BETWEEN 1 AND 64
                         AND translate(kind, 'abcdefghijklmnopqrstuvwxyz0123456789_', '') = ''),
    provider_key  TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    is_active     BIGINT NOT NULL DEFAULT 0 CHECK (is_active IN (0, 1)),
    registered_at BIGINT NOT NULL CHECK (registered_at >= 0 AND registered_at < 4102444800),
    UNIQUE (kind, provider_key)
);


-- At most one active provider per EXCLUSIVE kind, by the rule above. A partial
-- index, so the many inactive rows of one kind do not collide with each other,
-- and so the four kinds whose rows carry their provider are left alone.
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_active_per_exclusive_kind
    ON provider (kind)
    WHERE is_active = 1
      AND kind NOT IN ('security_event', 'dhcp_lease', 'measurement_sample',
                       'reconciled_state', 'resolver_cache', 'resolver_local_data');

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
    id             BIGINT PRIMARY KEY,
    identifier     TEXT NOT NULL UNIQUE,
    device         TEXT NOT NULL UNIQUE,
    description    TEXT NOT NULL,
    user_label     TEXT,
    status         TEXT,
    enabled        TEXT,
    link_type      TEXT NOT NULL,
    link_kind      TEXT NOT NULL
                   CHECK (link_kind IN ('physical', 'vlan', 'tunnel', 'other')),
    is_tunnel      BIGINT GENERATED ALWAYS AS
                   (CASE WHEN link_kind = 'tunnel' THEN 1 ELSE 0 END) STORED,
    vlan_tag       BIGINT,
    address_family BIGINT CHECK (address_family IS NULL OR address_family IN (4, 6)),
    -- is_upstream is 1 exactly when the last interfaces_info response reported a
    -- non-empty `gateways[]` for this interface, and 0 otherwise. The API field
    -- is the only input: no name, description, link type or address decides it.
    -- The term is the widget catalogue's "upstream interface"; OPNsense applies
    -- "upstream" to a GATEWAY (System > Gateways, "Upstream Gateway"), and an
    -- interface carrying a gateway is what reaches beyond the firewall.
    --
    -- An address reached through an upstream interface is OUTSIDE: it has no
    -- interface membership, and on-link evidence on an upstream interface is not
    -- evidence of membership (docs/data-model.md, "Classification").
    is_upstream    BIGINT NOT NULL DEFAULT 0 CHECK (is_upstream IN (0, 1)),
    -- link_local_evidence is the OPERATOR'S rule, and discovery never writes it:
    -- whether an IPv6 link-local address (RFC 4291, section 2.5.6) seen on this
    -- interface is evidence that the address belongs to it. A link-local prefix
    -- is the same on every interface, so an address inside it says nothing about
    -- where a machine sits unless the operator says it does. The default is no
    -- (0), and setting it on one interface makes such an address evidence on
    -- that interface only (docs/data-model.md, "Classification").
    link_local_evidence BIGINT NOT NULL DEFAULT 0 CHECK (link_local_evidence IN (0, 1)),
    first_seen_at  BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at   BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Interface address — the addresses an interface held, and the gateways behind
-- it, with the instants opnview first and last saw each one. Closes G13 in
-- part.
-- Source: /api/interfaces/overview/interfaces_info, fields `addr4` and `addr6`
-- (one string each, "address/prefix length", or empty), `ipv4[]` and `ipv6[]`
-- (every address the interface holds, each entry an object whose `ipaddr` is
-- "address/prefix length") and `gateways[]` (a list of gateway ADDRESSES, at
-- most one per address family), all read at opnsense/core 26.7.3,
-- Interfaces/Api/OverviewController.php. Survey: "Runtime discovery" (i).
--
-- source_field is the API field the row was read from, verbatim: 'addr4',
-- 'addr6', 'ipv4', 'ipv6' or 'gateways'. It is the endpoint's own vocabulary, so
-- nothing is invented to tell a primary address from a secondary one, or an
-- interface address from a gateway address. The primary address normally
-- appears twice, once under `addr4` and once in `ipv4[]`, and is stored under
-- both: each row says what one field reported.
--
-- A CHANGE IS A NEW ROW AND THE HISTORY IS THE TABLE. A row is ONE HOLDING: the
-- interface, the field, the address and its prefix length, from the first
-- discovery that read it to the last one that read it without a break. A refresh
-- that reads the same value, having read it at the previous discovery, moves
-- last_seen_at; a refresh that reads a different value inserts a row and leaves
-- the predecessor's last_seen_at where the last sighting put it; and a refresh
-- that reads a value again after a discovery that did not read it -- the address
-- was released and re-acquired -- inserts a NEW row from that instant, so the
-- interval between the two holdings is held by neither (step-5A live corrections:
-- "held at an instant", the this_firewall_address view, reads one row as one
-- holding). The previous discovery is the instant the interfaces carried before
-- this one wrote anything, read once at the start of the pass
-- (internal/store/flows/write.go, LatestInterfaceDiscoveryAt and UpsertInterfaceAddress),
-- so the outcome does not depend on the order the interfaces are listed in. "When did the public address last change" is therefore the first_seen_at
-- of the current row, and it can only come from opnview having looked before:
-- no endpoint keeps that history.
--
-- With ipv4[] and ipv6[] stored, G13 is closed (docs/widget-catalogue.md, "Gaps
-- closed"). The interface's NETWORKS -- the ranges classification reads as
-- membership evidence -- are not this table: they are interface_network below,
-- where a detected range and one the operator set are told apart.
--
-- Growing, slowly: one row per address an interface ever held. Purged by
-- last_seen_at, which never drops a current row because discovery moves it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS interface_address (
    id             BIGINT PRIMARY KEY,
    interface_id   BIGINT NOT NULL REFERENCES interface (id),
    source_field   TEXT NOT NULL
                   CHECK (source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6', 'gateways')),
    address        TEXT NOT NULL CHECK (length(address) > 0),
    prefix_length  BIGINT CHECK (prefix_length IS NULL
                                  OR (prefix_length >= 0 AND prefix_length <= 128)),
    address_family BIGINT NOT NULL CHECK (address_family IN (4, 6)),
    first_seen_at  BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at   BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    CHECK (last_seen_at >= first_seen_at),
    -- A gateway is an address with no prefix; an interface address carries one.
    CHECK ((source_field = 'gateways') = (prefix_length IS NULL))
);

-- One row per holding: the key carries first_seen_at, so a re-acquired address is a
-- holding of its own rather than its first holding's row.
CREATE UNIQUE INDEX IF NOT EXISTS uq_interface_address_holding
    ON interface_address (interface_id, source_field, address, coalesce(prefix_length, -1),
                          first_seen_at);
-- "This interface's current addresses" and "is this address one of the
-- firewall's own" are index searches rather than scans of a growing table.
CREATE INDEX IF NOT EXISTS idx_interface_address_interface
    ON interface_address (interface_id, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_interface_address_address
    ON interface_address (address, source_field);
CREATE INDEX IF NOT EXISTS idx_interface_address_last_seen_at
    ON interface_address (last_seen_at);

-- ---------------------------------------------------------------------------
-- Interface network — one on-link address range of an interface: the range
-- whose addresses classification takes to sit behind that interface.
--
-- THE TERM IS OPNSENSE'S. OPNsense calls the range an interface's address and
-- prefix length cover that interface's NETWORK: the firewall rule editor offers
-- "LAN net" for it, and the internal alias that holds it is named for "LAN
-- network" (https://docs.opnsense.org/manual/aliases.html, internal aliases).
-- opnview keeps the word, per interface, whatever the interface is called.
--
-- A NETWORK IS EITHER DETECTED OR SET BY THE OPERATOR, and origin says which:
--   'detected'  proposed by discovery from `addr4` / `addr6` and the entries of
--               `ipv4[]` / `ipv6[]` (their address masked to their prefix
--               length). An IPv6 link-local prefix is never proposed: it is the
--               same on every interface and places nothing; whether a
--               link-local address is evidence is interface.link_local_evidence.
--   'operator'  added by the operator, or a detected network the operator
--               confirmed.
-- removed_at is set when the operator removes a network. The row is kept, so a
-- detected network the operator removed is not proposed back into use by the
-- next discovery: discovery moves last_detected_at and never touches origin or
-- removed_at.
--
-- WHICH NETWORKS COUNT is the maintainer's decision D1 (specs/SPEC-step-5a-
-- corrections.md): a detected network counts until the operator overrides it;
-- an operator network always wins over a detected one; a removed network does
-- not count. A detected network counts only while discovery still reports it,
-- that is while its last_detected_at is the latest detection of its interface.
-- Nothing here is a default: no range is written by the schema or by code.
--
-- Bounded: one row per network an interface has carried, set by the
-- installation and the operator rather than by how long opnview has run. It is
-- never purged, because an operator row is user input.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS interface_network (
    id                BIGINT PRIMARY KEY,
    interface_id      BIGINT NOT NULL REFERENCES interface (id),
    network_address   TEXT NOT NULL CHECK (length(network_address) > 0),
    prefix_length     BIGINT NOT NULL CHECK (prefix_length >= 0 AND prefix_length <= 128),
    address_family    BIGINT NOT NULL CHECK (address_family IN (4, 6)),
    origin            TEXT NOT NULL CHECK (origin IN ('detected', 'operator')),
    first_detected_at BIGINT CHECK (first_detected_at IS NULL
                                     OR (first_detected_at >= 0 AND first_detected_at < 4102444800)),
    last_detected_at  BIGINT CHECK (last_detected_at IS NULL
                                     OR (last_detected_at >= 0 AND last_detected_at < 4102444800)),
    removed_at        BIGINT CHECK (removed_at IS NULL
                                     OR (removed_at >= 0 AND removed_at < 4102444800)),
    updated_at        BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800),
    CHECK ((first_detected_at IS NULL) = (last_detected_at IS NULL)),
    -- A network nobody detected was set by the operator.
    CHECK (origin = 'operator' OR first_detected_at IS NOT NULL),
    CHECK (address_family = 6 OR prefix_length <= 32),
    UNIQUE (interface_id, network_address, prefix_length)
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
    id           BIGINT PRIMARY KEY,
    display_name TEXT NOT NULL UNIQUE,
    created_at   BIGINT NOT NULL CHECK (created_at >= 0 AND created_at < 4102444800),
    updated_at   BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800)
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
    interface_id  BIGINT REFERENCES interface (id),
    discovered_at BIGINT NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800)
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
--
-- interface is the endpoint's `interface` field, verbatim, and legacy its
-- `legacy` field. The two are stored together because the first means two
-- different things depending on the second, read at opnsense/core 26.7.3
-- (Firewall/Api/FilterController.php): on a model rule it is the comma-separated
-- list of interface CONFIGURATION KEYS, and on a legacy or automatic rule the
-- controller has already rewritten each key into the interface's DESCRIPTION.
-- So a token is resolved to an interface only on a non-legacy rule, and only by
-- equality with interface.identifier, which is a key and not a name; a legacy
-- rule's interfaces are reported unresolved rather than matched on a
-- description. An empty value is a floating rule, which the controller treats
-- as matching on every interface. Stored for the "N rules do not log" figure
-- per interface; docs/data-model.md, `rule`.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rule (
    id            BIGINT PRIMARY KEY,
    pf_label      TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL,
    action        TEXT NOT NULL
                  CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction     TEXT CHECK (direction IS NULL OR direction IN ('in', 'out', 'any')),
    interface     TEXT,
    legacy        BIGINT CHECK (legacy IS NULL OR legacy IN (0, 1)),
    logs_matches  BIGINT CHECK (logs_matches IS NULL OR logs_matches IN (0, 1)),
    -- enabled is the endpoint's `enabled` field: "1" or "0" on a model rule, which the
    -- endpoint returns disabled or not, and always "1" on a legacy or automatic row,
    -- which it returns only when enabled (opnsense/core 26.7.3, Firewall/Filter.xml and
    -- scripts/filter/list_non_mvc_rules.php). NULL is not reported. Stored as
    -- coverage of the field: a rule a screen lists can say it is switched off, and
    -- nothing in the derivation reads it.
    enabled       BIGINT CHECK (enabled IS NULL OR enabled IN (0, 1)),
    is_automatic  BIGINT NOT NULL DEFAULT 0 CHECK (is_automatic IN (0, 1)),
    discovered_at BIGINT NOT NULL CHECK (discovered_at >= 0 AND discovered_at < 4102444800),
    -- destination_net is the endpoint's `destination_net` field, VERBATIM: an address,
    -- a network, an alias name, `any`, whatever the rule says (B8a of
    -- specs/SPEC-step-5b-widget-api.md, decision 12). It is stored so a table of rules
    -- can show what a rule was written against, and IT IS NEVER EVALUATED: opnview
    -- matches no flow against it and derives nothing from it. NULL is not reported.
    -- The schema is edited in place (decision 12 of the step-5A live corrections), so
    -- a database created before this column is rebuilt rather than migrated.
    destination_net TEXT
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
    id                BIGINT PRIMARY KEY,
    identity_kind     TEXT NOT NULL
                      CHECK (identity_kind IN ('dhcp_client_id', 'mac', 'address_in_interface')),
    identity_key      TEXT NOT NULL,
    interface_id      BIGINT REFERENCES interface (id),
    mac               TEXT CHECK (mac IS NULL OR (length(mac) = 17 AND mac = lower(mac))),
    hostname          TEXT,
    vendor_hint       TEXT,
    last_address      TEXT,
    -- Ownership, assigned by the user and never inferred. No DEFAULT, so a
    -- client is unowned until somebody says otherwise; owner_assigned_at
    -- records when that happened, and the CHECK below keeps the two from
    -- disagreeing.
    owner_id          BIGINT REFERENCES owner (id),
    owner_assigned_at BIGINT CHECK (owner_assigned_at IS NULL
                                     OR (owner_assigned_at >= 0 AND owner_assigned_at < 4102444800)),
    mac_is_randomised BIGINT GENERATED ALWAYS AS (
                          CASE
                              WHEN mac IS NULL THEN NULL
                              WHEN strpos('26ae', substr(mac, 2, 1)) > 0 THEN 1
                              ELSE 0
                          END) STORED,
    unstable_identity BIGINT GENERATED ALWAYS AS (
                          CASE
                              WHEN mac IS NOT NULL AND strpos('26ae', substr(mac, 2, 1)) > 0 THEN 1
                              ELSE 0
                          END) STORED,
    first_seen_at     BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at      BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    UNIQUE (identity_kind, identity_key),
    CHECK ((owner_id IS NULL) = (owner_assigned_at IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_client_interface ON client (interface_id, id);

-- "The machine at this address" is what classification and attribution ask for
-- every address a pass touches; an index search, never a scan of a growing table.
CREATE INDEX IF NOT EXISTS idx_client_last_address ON client (last_address, last_seen_at);

-- An owner's machines. owner_id is NULL-bearing, and a B-tree index keeps the
-- unassigned clients rather than omitting them, so the index serves the
-- unassigned bucket as well as the assigned ones.
CREATE INDEX IF NOT EXISTS idx_client_owner ON client (owner_id, id);

-- A widget reference to a client by host name (docs/dashboard-format.md, "The reference
-- object", `client` by `hostname`) is an exact match on this column, and the widget API's
-- statements may not scan a growing table (specs/SPEC-step-5b-widget-api.md, A12). The
-- write cost is one index entry per client row, which grows with the machines seen, not
-- with the traffic.
CREATE INDEX IF NOT EXISTS idx_client_hostname ON client (hostname);

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
    id             BIGINT PRIMARY KEY,
    client_id      BIGINT REFERENCES client (id),
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
    -- lease to model, which is also why the identity below needs no coalesce
    -- wrapper where measurement_sample's does.
    provider_id    BIGINT NOT NULL REFERENCES provider (id),
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
    interface_id   BIGINT REFERENCES interface (id),
    starts_at      BIGINT CHECK (starts_at IS NULL
                                  OR (starts_at >= 0 AND starts_at < 4102444800)),
    generation_key TEXT NOT NULL CHECK (length(generation_key) > 0),
    expires_at     BIGINT CHECK (expires_at IS NULL OR (expires_at >= 0 AND expires_at < 4102444800)),
    observed_at    BIGINT NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    -- hostname_label is the host name's FIRST LABEL, lower-cased, a trailing dot
    -- removed: what a host name the resolver logged as a lookup's client is matched
    -- on. The resolver logs the name a reverse lookup returned, which may be fully
    -- qualified, carry a trailing dot or be a bare label, and a lease may carry
    -- any of the three; comparing the first label of both sides matches all of
    -- them (step-5A live corrections, causes H1 and H2). lower() folds ASCII case
    -- only, which is the case DNS names fold (RFC 4343). Generated, so it cannot
    -- disagree with hostname; nothing writes it.
    hostname_label TEXT GENERATED ALWAYS AS (
                       lower(CASE
                                 WHEN strpos(rtrim(hostname, '.'), '.') > 0
                                     THEN substr(rtrim(hostname, '.'), 1,
                                                 strpos(rtrim(hostname, '.'), '.') - 1)
                                 ELSE rtrim(hostname, '.')
                             END)) STORED,
    UNIQUE (address, generation_key, provider_id)
);

CREATE INDEX IF NOT EXISTS idx_dhcp_lease_observed_at ON dhcp_lease (observed_at);
-- A client's lease history, ordered by the instant opnview read the lease rather
-- than by the validity start: the start is now NULL on every backend that cannot
-- report one, and an index leading on a column that is null for a whole backend
-- would order that backend's leases arbitrarily.
CREATE INDEX IF NOT EXISTS idx_dhcp_lease_client ON dhcp_lease (client_id, observed_at);
-- A host name the resolver logged as a lookup's client, resolved to the address
-- leased under it, by first label.
CREATE INDEX IF NOT EXISTS idx_dhcp_lease_hostname_label ON dhcp_lease (hostname_label, address);

-- ---------------------------------------------------------------------------
-- Flow — one filter-log record, allowed or blocked. The blocked events are a
-- projection of this table, never a second ingestion path: every blocked
-- record is the same log line as an allowed one with a different action.
-- Source: /api/diagnostics/firewall/log. Survey: data source 1. The record is
-- deduplicated on __digest__, stored as log_digest.
--
-- traffic_scope is this_firewall when an end is this firewall, east-west exactly
-- when both endpoints sit in a discovered interface, and north-south otherwise. It
-- derives from interface membership and this firewall's own addresses and from
-- nothing else — no address literal, no CIDR, no name.
--
-- MEMBERSHIP IS DECIDED FROM ON-LINK EVIDENCE ONLY, after the record is stored,
-- by internal/store's classification (docs/data-model.md, "Classification"): a
-- lease, a neighbour entry, an address inside a prefix an interface holds, or an
-- earlier sighting inside a non-upstream interface. An address with none is
-- OUTSIDE: its end carries no interface and no client, so a remote address never
-- becomes a client row. The classification is a function of the evidence held
-- and not of the order records arrived in, and re-running it changes nothing.
--
-- log_reason is the filter log's own `reason` field: WHY the packet was
-- logged, which is not the same question as what was done to it. It is stored
-- verbatim and no CHECK enumerates it, because a value outside the set below is
-- possible and must be kept rather than refused. THE SET IS NOW ESTABLISHED, from
-- the reason table of the `filterlog` daemon that writes the line (opnsense/ports
-- 26.7.3, opnsense/filterlog/files/filterlog.c), which is FreeBSD pf's PFRES_NAMES
-- (sys/netpfil/pf/pf.h) up to `synproxy`: match, bad-offset, fragment, short,
-- normalize, memory, bad-timestamp, congestion, ip-option, proto-cksum,
-- state-mismatch, state-insert, state-limit, src-limit, synproxy. Any other
-- reason is written by filterlog as `unknown(<n>)`. Code may match on the
-- established values and on no other: `match` is a rule decision, and every
-- other established value is a packet dropped for a reason no rule expresses.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS flow (
    id                     BIGINT PRIMARY KEY,
    log_digest             TEXT NOT NULL UNIQUE,
    observed_at            BIGINT NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
    ingested_at            BIGINT NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    interface_device       TEXT NOT NULL,
    interface_lookup_state TEXT NOT NULL
                           CHECK (interface_lookup_state IN ('resolved', 'not_found', 'pending')),
    src_interface_id       BIGINT REFERENCES interface (id),
    dst_interface_id       BIGINT REFERENCES interface (id),
    src_client_id          BIGINT REFERENCES client (id),
    dst_client_id          BIGINT REFERENCES client (id),
    src_address            TEXT NOT NULL,
    dst_address            TEXT NOT NULL,
    src_port               BIGINT CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_port               BIGINT CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol               TEXT NOT NULL,
    ip_version             BIGINT NOT NULL CHECK (ip_version IN (4, 6)),
    action                 TEXT NOT NULL
                           CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    direction              TEXT NOT NULL CHECK (direction IN ('in', 'out', 'unknown')),
    log_reason             TEXT,
    packet_bytes           BIGINT NOT NULL CHECK (packet_bytes >= 0),
    rid                    TEXT,
    rule_id                BIGINT REFERENCES rule (id),
    rule_lookup_state      TEXT NOT NULL
                           CHECK (rule_lookup_state IN ('resolved', 'not_found', 'pending')),
    -- src_is_this_firewall and dst_is_this_firewall say that an end is THIS
    -- FIREWALL, OPNsense's own term for the firewall's own addresses: the rule
    -- editor's "This Firewall" (opnsense/core 26.7.3, Firewall/FilterRule.php,
    -- `(self)` rendered as gettext("This Firewall"); used as a destination on
    -- https://docs.opnsense.org/manual/how-tos/caddy.html), which the ruleset
    -- emits to pf as `self`, "all addresses assigned to all interfaces"
    -- (pf.conf(5), FreeBSD 15.1-RELEASE). An end is this firewall when its address
    -- was held by the firewall at the record's instant -- interface_address under
    -- addr4, addr6, ipv4 or ipv6, see the this_firewall_address view -- or is a
    -- loopback address. Such an end records the interface the address belongs to
    -- (none for a loopback address) and never a client: the CHECKs below. It is
    -- never outside either. Classification sets both, docs/data-model.md,
    -- "Classification"; the collector stores every record with neither set.
    src_is_this_firewall   BIGINT NOT NULL DEFAULT 0 CHECK (src_is_this_firewall IN (0, 1)),
    dst_is_this_firewall   BIGINT NOT NULL DEFAULT 0 CHECK (dst_is_this_firewall IN (0, 1)),
    -- ip_id and tcp_seq are the filter log's `id` (IPv4 identification) and `seq`
    -- (TCP sequence number), the two logged fields verified to be carried
    -- unchanged by both legs of one connection under OPNsense's defaults (survey,
    -- "Two legs of one connection, read for the step-5A live corrections"). They
    -- are stored for one purpose: pairing the two records of one connection. NULL
    -- is a record that does not carry the field -- an IPv6 record has no `id`, a
    -- record that is not TCP no `seq`.
    ip_id                  BIGINT CHECK (ip_id IS NULL OR (ip_id >= 0 AND ip_id <= 65535)),
    tcp_seq                BIGINT CHECK (tcp_seq IS NULL OR (tcp_seq >= 0 AND tcp_seq <= 4294967295)),
    -- pair_outcome and paired_flow_id record what the pairing of the two records of
    -- one connection decided (docs/data-model.md, "Two legs of one connection"):
    --   'first_leg'   the `in` record of a client connection whose second record
    --                 was found; paired_flow_id names that second record
    --   'second_leg'  the `out` record on another interface that is the second
    --                 record of the connection paired_flow_id names. It is counted
    --                 on the interface it was logged on and in the rule family,
    --                 and in no other figure
    --   'not_paired'  an unpaired `out` record on an upstream interface sourced by
    --                 this firewall. It keeps its this-firewall end, but opnview
    --                 never claims it is the firewall's own traffic rather than a
    --                 client's NATed connection whose first record it cannot see
    --                 (decision 3 as amended on 7 October 2026), and no client is
    --                 assigned to it
    --   NULL          every other record
    -- The three values are opnview's own terms: pf and OPNsense log the records
    -- and have no word for one being the second of a pair. paired_flow_id carries
    -- no foreign key, deliberately: the retention purge can remove one record of a
    -- pair and keep the other, and the survivor still says what it was.
    pair_outcome           TEXT CHECK (pair_outcome IS NULL
                                       OR pair_outcome IN ('first_leg', 'second_leg', 'not_paired')),
    paired_flow_id         BIGINT,
    -- traffic_scope is written by the collector and pinned by the CHECK below
    -- to exactly one expression over interface membership and this firewall's
    -- ends, so no row can carry a value that disagrees with them — the same
    -- guarantee a generated column gives, demonstrated by a failing insert in the
    -- checks. 'this_firewall' is the third value, decision 6 of the step-5A live
    -- corrections: a flow with an end that is this firewall is neither between two
    -- interfaces nor across the edge.
    --
    -- It is a plain column rather than a generated one, a choice made under the
    -- previous engine, which never reported an index as covering a query that
    -- read a generated column; it is kept so the Overview, Matrix and Interface
    -- queries, which all three group by this value, read it like any other
    -- column of their covering indexes. The derivation is
    -- unchanged and is still interface membership alone: no address, no CIDR, no
    -- name, no assumed addressing plan.
    traffic_scope          TEXT NOT NULL,
    CHECK (traffic_scope = CASE
                               WHEN src_is_this_firewall = 1 OR dst_is_this_firewall = 1
                                   THEN 'this_firewall'
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south'
                           END),
    CHECK (src_is_this_firewall = 0 OR src_client_id IS NULL),
    CHECK (dst_is_this_firewall = 0 OR dst_client_id IS NULL),
    CHECK ((coalesce(pair_outcome, '') IN ('first_leg', 'second_leg')) = (paired_flow_id IS NOT NULL))
);

-- Index shape. Column count is close to irrelevant to read speed; what
-- matters is selectivity and whether the index answers the query without
-- fetching the row. The two indexes serving an AGGREGATING screen query
-- therefore carry the measures that query sums or counts, so its plan reads
-- the index alone -- an index-only scan in EXPLAIN. The two
-- serving a DETAIL listing are left narrow: a listing returns most of the row
-- anyway, so widening them would buy nothing and cost write throughput on
-- every ingested record. docs/data-model.md names, per query, which index
-- covers it and which measures were added for that purpose.
--
-- There is exactly one index leading on observed_at alone, deliberately: it is
-- the load-bearing index of the Overview and Matrix queries, and a second one
-- with the same leading column would hide its loss.
--
-- pair_outcome and interface_device close both, since B8c of
-- specs/SPEC-step-5b-widget-api.md: the screen queries read classified_flow and keep
-- the rows whose exit_leg_device is NULL, and exit_leg_device is computed from those
-- two columns, so without them every covered plan would fetch each row to tell a
-- second leg from a first.
CREATE INDEX IF NOT EXISTS idx_flow_observed_at ON flow
    (observed_at, traffic_scope, action, packet_bytes,
     src_interface_id, dst_interface_id, src_client_id, rule_id, rule_lookup_state,
     pair_outcome, interface_device);
CREATE INDEX IF NOT EXISTS idx_flow_src_interface_observed_at ON flow
    (src_interface_id, observed_at, src_client_id, packet_bytes, action,
     traffic_scope, dst_address, pair_outcome, interface_device);
CREATE INDEX IF NOT EXISTS idx_flow_src_client_observed_at ON flow (src_client_id, observed_at);
-- The partial index of the blocked_event view, over the refusals: a 'block' and a
-- 'reject' -- a reject is a refusal that answers. Its predicate is written exactly
-- as the view's, which is what lets the planner use it.
CREATE INDEX IF NOT EXISTS idx_flow_refused_observed_at ON flow (observed_at)
    WHERE action IN ('block', 'reject');

-- The four indexes step 5 adds, each for a statement in internal/store/derive.sql
-- that would otherwise scan the largest growing table:
--   idx_flow_src_address, idx_flow_dst_address  classification, which rewrites
--       every flow touching one address and reads the sightings of that address;
--   idx_flow_ingested_at  the refresh, which finds the slots holding a flow
--       ingested since the last refresh;
--   idx_flow_dst_client_observed_at  re-pointing a client identity and the
--       retention guard on a client, both of which look a client up as a
--       destination.
CREATE INDEX IF NOT EXISTS idx_flow_src_address ON flow (src_address, observed_at);
CREATE INDEX IF NOT EXISTS idx_flow_dst_address ON flow (dst_address, observed_at);
CREATE INDEX IF NOT EXISTS idx_flow_ingested_at ON flow (ingested_at, observed_at);
CREATE INDEX IF NOT EXISTS idx_flow_dst_client_observed_at ON flow (dst_client_id, observed_at);

-- ---------------------------------------------------------------------------
-- classified_flow — a flow, with the three facts every derivation reads from it
-- written ONCE, as expressions over its stored columns.
--
-- traffic_direction is opnview's own term, taken from the widget catalogue's
-- scopes: 'outbound' (the destination is outside), 'inbound' (the source is
-- outside) or 'inter_interface' (neither end is outside). It is NOT flow.direction,
-- which stays pf's per-interface `dir`: a packet leaving a client for the
-- Internet is `in` on the client's interface. When BOTH ends are outside -- an
-- unsolicited packet to the firewall's own upstream address, or one the firewall
-- sends from it -- membership cannot decide, and pf's `dir` on the interface the
-- record was logged on can: `out` is outbound, anything else inbound. That
-- tie-break is the only place `dir` enters, so the three directions partition the
-- flows exactly.
--
-- local_interface_id and local_client_id name the INSIDE end: the source when it
-- has an interface and is not this firewall, else the destination on the same
-- terms, else nothing. peer_address is the OUTSIDE end: the destination of an
-- outbound flow, the source of an inbound one, and NULL between two interfaces.
--
-- THIS FIREWALL IS NEITHER END KIND, and has two directions of its own
-- (decision 6 of the step-5A live corrections): 'from_this_firewall' when the
-- source is this firewall, 'to_this_firewall' when the destination is and the
-- source is not. Its interface is recorded on the flow, but it is not an inside
-- end -- it has no client -- and not an outside one: peer_address of such a flow
-- is the other end when that end is outside, and NULL otherwise.
--
-- exit_leg_device is the device a SECOND LEG was logged on (flow.pair_outcome
-- 'second_leg'), and NULL for every other record. A second leg is counted on that
-- interface's own figures and in the rule family, and in no other figure, so the
-- statements that count read it.
--
-- A view rather than columns: they are pure functions of the row, and a view
-- over one table is flattened by the planner, so every statement that reads it
-- keeps the flow indexes. It is replaced on every apply, so an edit to its text
-- reaches a database the previous text was applied to.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW classified_flow AS
SELECT
    id,
    observed_at,
    ingested_at,
    interface_device,
    src_interface_id,
    dst_interface_id,
    src_client_id,
    dst_client_id,
    src_address,
    dst_address,
    src_port,
    dst_port,
    protocol,
    action,
    direction,
    log_reason,
    packet_bytes,
    rid,
    rule_id,
    traffic_scope,
    src_is_this_firewall,
    dst_is_this_firewall,
    pair_outcome,
    paired_flow_id,
    -- rule_lookup_state and ip_version since B8c of specs/SPEC-step-5b-widget-api.md:
    -- the Matrix screen query, which reads this view, counts the connections whose rid
    -- matched no rule, and the checks count each address family.
    rule_lookup_state,
    ip_version,
    CASE
        WHEN src_is_this_firewall = 1 THEN 'from_this_firewall'
        WHEN dst_is_this_firewall = 1 THEN 'to_this_firewall'
        WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL THEN 'inter_interface'
        WHEN src_interface_id IS NOT NULL THEN 'outbound'
        WHEN dst_interface_id IS NOT NULL THEN 'inbound'
        WHEN direction = 'out' THEN 'outbound'
        ELSE 'inbound'
    END AS traffic_direction,
    CASE
        WHEN src_is_this_firewall = 0 AND src_interface_id IS NOT NULL THEN src_interface_id
        WHEN dst_is_this_firewall = 0 THEN dst_interface_id
    END AS local_interface_id,
    CASE
        WHEN src_is_this_firewall = 0 AND src_interface_id IS NOT NULL THEN src_client_id
        WHEN dst_is_this_firewall = 0 AND dst_interface_id IS NOT NULL THEN dst_client_id
    END AS local_client_id,
    CASE
        WHEN src_is_this_firewall = 1 AND dst_is_this_firewall = 1 THEN NULL
        WHEN src_is_this_firewall = 1 THEN CASE WHEN dst_interface_id IS NULL THEN dst_address END
        WHEN dst_is_this_firewall = 1 THEN CASE WHEN src_interface_id IS NULL THEN src_address END
        WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL THEN NULL
        WHEN src_interface_id IS NOT NULL THEN dst_address
        WHEN dst_interface_id IS NOT NULL THEN src_address
        WHEN direction = 'out' THEN dst_address
        ELSE src_address
    END AS peer_address,
    CASE WHEN pair_outcome = 'second_leg' THEN interface_device END AS exit_leg_device
FROM flow;

-- ---------------------------------------------------------------------------
-- this_firewall_address — every address this firewall held, the interface it
-- belonged to, and when opnview saw it held.
--
-- THE TERM IS OPNSENSE'S, "This Firewall" (see flow.src_is_this_firewall). The
-- addresses are interface_address under the four fields that report an address
-- the firewall itself holds -- addr4, addr6, ipv4 and ipv6 -- and never
-- `gateways`, which are other machines.
--
-- "HELD AT AN INSTANT" IS DEFINED HERE AND NOWHERE ELSE, as the derivation reads
-- it (internal/store/derive.sql, the :held_margin parameter): an address was held
-- at t when one of its rows has
--     first_seen_at - margin <= t
--     and (is_current = 1 or t <= last_seen_at + margin)
-- where margin is the discovery interval, because discovery reads the addresses
-- once per interval and an address can have been taken or released at any instant
-- between two reads. is_current is 1 when the row was read by the latest discovery
-- of its interface: an address the firewall still holds is held from its first
-- sighting onward, so a record made after the last discovery is not orphaned by a
-- discovery that has not run yet.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW this_firewall_address AS
SELECT
    a.address                                              AS address,
    a.interface_id                                         AS interface_id,
    a.first_seen_at                                        AS first_seen_at,
    a.last_seen_at                                         AS last_seen_at,
    CASE WHEN a.last_seen_at >= i.last_seen_at THEN 1 ELSE 0 END AS is_current
FROM interface_address AS a
JOIN interface AS i ON i.id = a.interface_id
WHERE a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6');

-- blocked_event — every refused filter-log record: action 'block' or 'reject'. A
-- record whose action could not be named ('unknown') is not a refusal and is not
-- here, and it is not counted as allowed anywhere either. Replaced on every apply,
-- for the reason blocked_decision is.
CREATE OR REPLACE VIEW blocked_event AS
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
    action,
    traffic_scope
FROM flow
WHERE action IN ('block', 'reject');

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
    id                  BIGINT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    purpose             TEXT CHECK (purpose IS NULL OR purpose IN
                        ('advertising', 'tracking', 'threat', 'parental', 'other')),
    purpose_assigned_at BIGINT CHECK (purpose_assigned_at IS NULL
                        OR (purpose_assigned_at >= 0 AND purpose_assigned_at < 4102444800)),
    first_seen_at       BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at        BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
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
--
-- interface_id and interface_lookup_state place the querying address on an
-- interface (G6). They are resolved by the same classification that places a
-- flow's ends, from the same on-link evidence, never from an address range
-- written anywhere: 'pending' until it has run, 'resolved' with the interface,
-- and 'not_found' when the address has no on-link evidence at all. So a lookup
-- from an address no client row names can still be placed.
--
-- answer_source holds the endpoint's `source` verbatim. Its values are
-- established at opnsense/core 26.7.3 (scripts/unbound/stats.py): Recursion,
-- Local, Local-data and Cache. Local-data is a host override or another
-- local-data entry; Local is a blocklist answer or a SERVFAIL.
-- ---------------------------------------------------------------------------
--
-- client_resolution says what the endpoint's `client` field held, because it is
-- not always an address. The resolver's query report replaces the querying
-- address by the name a reverse lookup of it returned, when there was one
-- (opnsense/core 26.7.3, scripts/unbound/stats.py, the `details` query, which
-- joins the logged client to the `client` table that scripts/unbound/logger.py
-- fills through socket.gethostbyaddr). So:
--   'logged_address'      `client` was an address; client_address is it and
--                         client_hostname is NULL.
--   'lease_hostname'      `client` was a host name, and exactly one address held
--                         a DHCP lease under that name at the lookup's instant;
--                         client_address is that address and client_hostname the
--                         logged name, verbatim.
--   'ambiguous_hostname'  a host name that more than one address held a lease
--                         under at that instant;
--   'unknown_hostname'    a host name neither a lease nor the resolver's local
--                         data named at that instant.
--   'local_data_hostname' a host name no lease named, and exactly one address
--                         answered for it in the resolver's local data held at
--                         the lookup's instant (resource_record_observation,
--                         held_in 'local_data': an A or AAAA record whose owner
--                         name, or a PTR record whose target, has the same first
--                         label); client_address is that address and
--                         client_hostname the logged name. The term is
--                         opnview's own (docs/data-model.md, Vocabulary).
--   'this_firewall_hostname'  a host name that names this firewall: `localhost`,
--                         the name a reverse lookup of a loopback address returns.
--                         It is a protocol constant (decision 11 of the step-5A
--                         live corrections), not configuration.
--   'dual_stack_hostname' a host name no lease named, for which the local data held
--                         exactly two addresses at the lookup's instant, one IPv4 and
--                         one IPv6, under one name -- a host override with an A and an
--                         AAAA record names one machine -- and the lookup's querying
--                         family was not reported, so which of the two asked is not
--                         known (group C of specs/SPEC-step-5b-widget-api.md, decision
--                         15). With the family reported, the lookup resolves to the
--                         address of that family as 'local_data_hostname'. The term
--                         is opnview's own (docs/data-model.md, Vocabulary).
-- In 'ambiguous_hostname', 'unknown_hostname', 'dual_stack_hostname' and
-- 'this_firewall_hostname' client_address holds the logged name verbatim, and in the
-- first three of them the lookup is placed nowhere and attributes nothing: a name
-- that does not resolve to one address does not say which machine asked.
--
-- client_family is the family of the querying address, 4 or 6, as the resolver's
-- query report gives it in the `family` field of each row: Unbound's own family of
-- the reply's address, `ip4` or `ip6` (opnsense/core 26.7.3,
-- scripts/unbound-dnsbl/lib/__init__.py, Query, from comm_reply.family, which
-- Unbound's pythonmod/interface.i maps from the socket address's ss_family). It is
-- the querying address's family even where the report replaced that address by a
-- host name, which makes it the evidence the dual-stack rule reads, recorded per
-- lookup. NULL when the row carried none: a Dnsmasq lookup, a `unix` socket, an
-- absent field.
--
-- A host name matches a lease by its FIRST LABEL on both sides, without regard
-- to case and with a trailing dot removed: dhcp_lease.hostname_label below.
-- The resolver's local data is matched by the same rule, on the first label of the
-- record's host name. hostname_examined_at is the instant a pass last resolved the
-- logged host name again; NULL until one has, which is what a lease pass or a
-- local-data pass reads -- including the first one after a restart -- so every
-- unresolved lookup is examined again once both sources in use have been read
-- after it was ingested, and none is scanned twice.
--
-- client_is_this_firewall is 1 when the querier is this firewall: its address
-- was held by the firewall at the lookup's instant, or it is a loopback address
-- or `localhost` (see flow.src_is_this_firewall). Such a lookup has no client.
CREATE TABLE IF NOT EXISTS dns_resolution (
    id             BIGINT PRIMARY KEY,
    lookup_key     TEXT NOT NULL UNIQUE,
    client_address TEXT NOT NULL,
    client_hostname TEXT,
    client_resolution TEXT NOT NULL DEFAULT 'logged_address'
                   CHECK (client_resolution IN ('logged_address', 'lease_hostname',
                                                'ambiguous_hostname', 'unknown_hostname',
                                                'this_firewall_hostname',
                                                'local_data_hostname',
                                                'dual_stack_hostname')),
    client_family  BIGINT CHECK (client_family IS NULL OR client_family IN (4, 6)),
    client_id      BIGINT REFERENCES client (id),
    client_is_this_firewall BIGINT NOT NULL DEFAULT 0 CHECK (client_is_this_firewall IN (0, 1)),
    hostname_examined_at BIGINT CHECK (hostname_examined_at IS NULL
                                        OR (hostname_examined_at >= 0 AND hostname_examined_at < 4102444800)),
    domain         TEXT NOT NULL,
    resolver       TEXT NOT NULL CHECK (resolver IN ('unbound', 'dnsmasq')),
    action         TEXT NOT NULL CHECK (action IN ('pass', 'block', 'drop', 'unknown')),
    answer_source  TEXT,
    rcode          TEXT,
    dnssec_status  TEXT,
    blocklist_id   BIGINT REFERENCES blocklist (id),
    looked_up_at   BIGINT NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    ingested_at    BIGINT NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    interface_id   BIGINT REFERENCES interface (id),
    interface_lookup_state TEXT NOT NULL DEFAULT 'pending'
                   CHECK (interface_lookup_state IN ('resolved', 'not_found', 'pending')),
    CHECK ((interface_lookup_state = 'resolved') = (interface_id IS NOT NULL)),
    CHECK ((client_resolution = 'logged_address') = (client_hostname IS NULL)),
    CHECK (client_is_this_firewall = 0 OR client_id IS NULL)
);

CREATE INDEX IF NOT EXISTS idx_dns_resolution_looked_up_at ON dns_resolution (looked_up_at);
CREATE INDEX IF NOT EXISTS idx_dns_resolution_client ON dns_resolution (client_address, looked_up_at);
-- The attribution's lookup by the same client, and re-pointing a client identity.
CREATE INDEX IF NOT EXISTS idx_dns_resolution_client_id ON dns_resolution (client_id, looked_up_at);
-- The lookups of one name, compared as the resolver's records are: without regard
-- to case and with a trailing dot removed (RFC 4343, section 3: DNS names compare
-- case-insensitively in ASCII). It is what the resolver-cache attribution searches,
-- from the names an answer address was held under to the lookups of those names. An
-- index on the expression rather than a second column: the expression is the
-- comparison, and a stored copy of the domain could disagree with the domain.
CREATE INDEX IF NOT EXISTS idx_dns_resolution_domain_name
    ON dns_resolution (lower(rtrim(domain, '.')), looked_up_at);
-- The same comparison bounded by the lookup's client as well, one index per branch of
-- the attribution's client predicate (statement attribution_name_lookups): by
-- client_id, and by the address for a lookup or a flow with no client_id. Without
-- them the search was bounded by name only and read every lookup of a popular name
-- in the window before filtering by client. Each costs one more index entry per
-- stored lookup on a growing table; docs/data-model.md, dns_resolution, records it.
CREATE INDEX IF NOT EXISTS idx_dns_resolution_name_client_id
    ON dns_resolution (lower(rtrim(domain, '.')), client_id, looked_up_at);
CREATE INDEX IF NOT EXISTS idx_dns_resolution_name_client_address
    ON dns_resolution (lower(rtrim(domain, '.')), client_address, looked_up_at);

-- "Which list refused what, over this period" is an index search rather than a
-- scan of a growing table. The NULL-bearing leading column keeps the
-- unattributed blocked lookups at the head of the index instead of omitting
-- them, so the "list not recorded" rows are served by it too.
CREATE INDEX IF NOT EXISTS idx_dns_resolution_blocklist ON dns_resolution (blocklist_id, looked_up_at);
-- The lookups whose logged host name did not resolve to one address and that no
-- lease pass has examined since they were ingested: what one lease pass resolves
-- again. A partial index, so it holds exactly the lookups still waiting and
-- shrinks as a pass examines them, rather than growing with every unresolved
-- lookup ever stored.
--
-- A 'dual_stack_hostname' lookup waits here too: a lease naming its host may be read
-- after it, and a lease wins over the local data. Its querying family is stored with
-- it (client_family), so the pass that examines it again decides it as ingest would
-- have. As every schema change before step 8 is, the predicate is edited in place and
-- reaches a database that is rebuilt (decision 12 of the step-5A live corrections).
CREATE INDEX IF NOT EXISTS idx_dns_resolution_hostname_unexamined
    ON dns_resolution (client_resolution, hostname_examined_at, ingested_at, id)
    WHERE client_resolution IN ('ambiguous_hostname', 'unknown_hostname', 'dual_stack_hostname')
      AND hostname_examined_at IS NULL;

-- ---------------------------------------------------------------------------
-- Resource record observation — one record the resolver held, in its cache or in
-- its local data, over the interval opnview's polls saw it.
--
-- Sources, both in docs/opnsense-api-survey.md, "Resolver cache and local data,
-- read from source for the resolver-cache cycle":
--   held_in 'cache'       /api/unbound/diagnostics/dumpcache, the resolver_cache
--                         kind: `unbound-control dump_cache`, one {host, ttl,
--                         type, rrtype, value} per record, `ttl` the seconds
--                         remaining. Only A, AAAA and CNAME records are stored;
--                         every other rrtype is counted in the read's
--                         availability detail and not stored.
--   held_in 'local_data'  /api/unbound/diagnostics/listlocaldata, the
--                         resolver_local_data kind: `unbound-control
--                         list_local_data`, one {name, ttl, type, rrtype, value}
--                         per record. A, AAAA and PTR records are stored.
-- The endpoint's `type` is the record's CLASS (IN), never its type, and is not
-- stored: every record either endpoint returns is of class IN. "Resource record",
-- "owner name", "RRtype" and "label" are DNS's own words (RFC 1034, section
-- 3.6; RFC 1035, section 3.2.2).
--
--   owner_name   the endpoint's `host` (cache) or `name` (local data), lower-cased
--                in ASCII with the trailing dot removed: DNS names compare
--                without regard to case (RFC 4343) and the dump writes absolute
--                names.
--   rrtype       the endpoint's `rrtype`, verbatim.
--   value        the endpoint's `value`: for A and AAAA the address in its
--                canonical text form (RFC 5952 for IPv6, which is the form
--                inet_ntop writes and the filter log reports); for CNAME and PTR
--                the target name, normalised as owner_name is. A record whose
--                value is not an address of its rrtype's family is skipped and
--                counted, never stored.
--   address      the address the record maps a name to: the value of an A or
--                AAAA record, and for a PTR record the address its owner name
--                encodes (RFC 1035, section 3.5; RFC 3596, section 2.5). NULL for
--                a CNAME, which maps a name to a name.
--   host_label   for local data only: the first label of the host name the
--                record names -- the owner of an A or AAAA record, the target of
--                a PTR record -- which is what a host name the query report
--                logged as a lookup's client is matched on, exactly as
--                dhcp_lease.hostname_label is. NULL for the cache.
--
-- THE COVERAGE INTERVAL, covered_from_at to covered_until_at, is opnview's own
-- derivation and its own term (docs/data-model.md, Vocabulary): the interval over
-- which the resolver can be taken to have held the record.
--   covered_from_at   the instant of the LAST SUCCESSFUL POLL BEFORE the first poll
--                     that saw the record, read from resource_record_read when
--                     that poll was stored. Across a failed poll or a restart it
--                     is that earlier successful poll; with none, the first
--                     poll's own instant. The record may have entered the cache
--                     at any moment after that poll, so the bound is the earliest
--                     the polls allow, not a claim that it was held then.
--   first_seen_at, last_seen_at  the first and the latest poll that saw it.
--   covered_until_at  for the cache, the latest poll's instant plus the `ttl` it
--                     reported: the resolver serves the record no longer than
--                     that. An eviction before the TTL makes this an
--                     over-statement, which is recorded as a limitation. For
--                     local data, which has no remaining TTL -- its `ttl` is the
--                     configured one -- the latest poll that saw it.
--
-- BOUNDED GROWTH. A record seen by two consecutive successful polls of its
-- provider is ONE observation whose interval is extended; one that reappears after
-- a poll that did not hold it starts a new observation. So the table grows with the
-- distinct records the resolver held and with their comings and goings, not with
-- the number of polls. It is a growing table, purged by covered_until_at, and an
-- observation an attribution still names is kept.
--
-- The identity includes the provider, so the two kinds admit several concurrently
-- active providers (see `provider`); held_in repeats what the provider's kind
-- says, because every search below starts from it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS resource_record_observation (
    id               BIGINT PRIMARY KEY,
    provider_id      BIGINT NOT NULL REFERENCES provider (id),
    held_in          TEXT NOT NULL CHECK (held_in IN ('cache', 'local_data')),
    owner_name       TEXT NOT NULL CHECK (length(owner_name) > 0
                                          AND owner_name = lower(owner_name)
                                          AND substr(owner_name, -1) <> '.'),
    rrtype           TEXT NOT NULL CHECK (rrtype IN ('A', 'AAAA', 'CNAME', 'PTR')),
    value            TEXT NOT NULL CHECK (length(value) > 0),
    address          TEXT,
    host_label       TEXT,
    first_seen_at    BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    last_seen_at     BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    covered_from_at  BIGINT NOT NULL CHECK (covered_from_at >= 0 AND covered_from_at < 4102444800),
    covered_until_at BIGINT NOT NULL CHECK (covered_until_at >= 0 AND covered_until_at < 4102444800),
    CHECK ((held_in = 'cache' AND rrtype IN ('A', 'AAAA', 'CNAME'))
           OR (held_in = 'local_data' AND rrtype IN ('A', 'AAAA', 'PTR'))),
    CHECK ((address IS NULL) = (rrtype = 'CNAME')),
    CHECK ((host_label IS NULL) = (held_in = 'cache')),
    CHECK (covered_from_at <= first_seen_at AND first_seen_at <= last_seen_at
           AND last_seen_at <= covered_until_at),
    UNIQUE (provider_id, owner_name, rrtype, value, first_seen_at)
);

-- The names an address was held under: where the resolver-cache attribution starts.
CREATE INDEX IF NOT EXISTS idx_resource_record_observation_address
    ON resource_record_observation (address, covered_until_at);
-- The names a CNAME pointed at a name from: the climb from an address to the names
-- a client may have looked up.
CREATE INDEX IF NOT EXISTS idx_resource_record_observation_cname_target
    ON resource_record_observation (value, covered_until_at)
    WHERE rrtype = 'CNAME';
-- The records of one name: the descent from a looked-up name to its addresses.
CREATE INDEX IF NOT EXISTS idx_resource_record_observation_owner
    ON resource_record_observation (owner_name, covered_until_at);
-- The local data naming a host: the resolution of a logged host name no lease names.
CREATE INDEX IF NOT EXISTS idx_resource_record_observation_host_label
    ON resource_record_observation (host_label, covered_until_at)
    WHERE held_in = 'local_data';
-- The purge, and whether any record was held over a window.
CREATE INDEX IF NOT EXISTS idx_resource_record_observation_covered_until
    ON resource_record_observation (covered_until_at);

-- Resource record read — the last successful poll of each provider of the two kinds
-- above. It is what makes the coverage interval's lower bound survive a failed poll
-- and a restart: a record first seen by a poll starts covered from the instant this
-- row held before that poll. Bounded: one row per provider of the two kinds.
CREATE TABLE IF NOT EXISTS resource_record_read (
    provider_id BIGINT PRIMARY KEY REFERENCES provider (id),
    read_at     BIGINT NOT NULL CHECK (read_at >= 0 AND read_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Domain attribution — the site name carried by a flow, the lookup it was
-- inferred from, and the method that inferred it.
--
-- THE METHOD IS RECORDED ON EACH ATTRIBUTION, which reverses decision D3 of
-- specs/SPEC-correlation-classification-aggregation.md. Until the resolver-cache
-- cycle there was one method and a column with one possible value stated
-- nothing; the resolver's cache now supplies the answer addresses the query
-- report lacks, so two methods exist and a reader must be able to tell them
-- apart (specs/SPEC-resolver-cache-attribution.md, decision 8 of the step-5A live
-- corrections). Both remain inferences.
--
-- THE RULE, written once in internal/store/attribution/attribute.go and derive.sql and stated
-- in docs/data-model.md. A flow is eligible when its destination is outside, it is
-- no second leg, and it has a source client; a lookup is eligible when it passed,
-- was answered by Recursion, Cache or Local-data, and names its client.
--   'resolver_cache_answer'  EXACT EVIDENCE FIRST. An eligible lookup of D by the
--       flow's client, at most attribution_max_cache_answer_delay_seconds before
--       the flow, is an exact candidate when the flow's destination is among D's
--       answer addresses over the whole interval from the lookup to the flow: the
--       cache's CNAME observations from D, each covering that interval, lead to an
--       A or AAAA observation of the destination covering it too -- and, for a
--       lookup answered from Local-data, a local-data A or AAAA record of D counts
--       as well. The flow is attributed exactly when its exact candidates name one
--       distinct domain; address_observation_id names the A or AAAA observation
--       matched.
--   'lookup_timing'  THE 5A RULE, AS THE FALLBACK, when there is no exact
--       candidate: the eligible lookups by the same client in [observed_at -
--       attribution_max_delay_seconds, observed_at] name exactly one distinct
--       domain. It is suppressed when the cache held answers of the destination's
--       address family for that domain at the lookup's instant and the destination
--       was not among them: evidence that contradicts it.
-- site_name is the domain the client looked up, verbatim -- never a CNAME target
-- -- and the referenced lookup is the latest of the matching ones.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS domain_attribution (
    flow_id                   BIGINT PRIMARY KEY
                              REFERENCES flow (id) ON DELETE CASCADE,
    dns_resolution_id         BIGINT NOT NULL
                              REFERENCES dns_resolution (id) ON DELETE CASCADE,
    site_name                 TEXT NOT NULL,
    correlation_delay_seconds BIGINT NOT NULL CHECK (correlation_delay_seconds >= 0),
    attributed_at             BIGINT NOT NULL CHECK (attributed_at >= 0 AND attributed_at < 4102444800),
    method                    TEXT NOT NULL
                              CHECK (method IN ('resolver_cache_answer', 'lookup_timing')),
    -- No ON DELETE: the purge keeps an observation an attribution names, and the
    -- foreign key is what makes forgetting that an error rather than a dangling id.
    address_observation_id    BIGINT REFERENCES resource_record_observation (id),
    CHECK ((method = 'resolver_cache_answer') = (address_observation_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_domain_attribution_resolution ON domain_attribution (dns_resolution_id);
-- Whether an observation is named by an attribution, which is what the purge asks.
CREATE INDEX IF NOT EXISTS idx_domain_attribution_address_observation
    ON domain_attribution (address_observation_id);

-- ---------------------------------------------------------------------------
-- Geo / ASN — the geo and ASN enrichment of one address.
-- Keyed per address rather than per prefix: the dataset answers with a prefix
-- but SQL has no natural longest-prefix join. dataset_build_at is the build
-- date of the dataset that answered and provider_id names the provider that
-- supplied it, so a stale enrichment is visible and attributable. A cache miss
-- is a row whose lookup_state is 'miss', never an absent row; a 'pending' row
-- has not been looked up yet and names no provider.
-- Source: the geo_asn kind of the provider registry — today the MaxMind
-- GeoLite2 City and ASN databases, the second of the three outbound
-- calls the project allows. Acquisition is step 4.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS geo_asn (
    address          TEXT PRIMARY KEY,
    provider_id      BIGINT REFERENCES provider (id),
    lookup_state     TEXT NOT NULL CHECK (lookup_state IN ('resolved', 'miss', 'pending')),
    country_code     TEXT,
    country_name     TEXT,
    latitude         DOUBLE PRECISION,
    longitude        DOUBLE PRECISION,
    asn              BIGINT,
    operator         TEXT,
    dataset_build_at BIGINT
                     CHECK (dataset_build_at IS NULL
                            OR (dataset_build_at >= 0 AND dataset_build_at < 4102444800)),
    looked_up_at     BIGINT NOT NULL CHECK (looked_up_at >= 0 AND looked_up_at < 4102444800),
    CHECK (lookup_state <> 'resolved'
           OR (country_code IS NOT NULL
               AND dataset_build_at IS NOT NULL
               AND provider_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_geo_asn_looked_up_at ON geo_asn (looked_up_at);

-- A widget reference to a country or to an operator (docs/dashboard-format.md, "The
-- reference object": `country` by `iso_3166_1_alpha_2`, `operator` by `asn`) resolves
-- against these columns by exact match, and the widget API may not scan a growing table
-- (specs/SPEC-step-5b-widget-api.md, A12). One entry each per geolocated address.
CREATE INDEX IF NOT EXISTS idx_geo_asn_country_code ON geo_asn (country_code);
CREATE INDEX IF NOT EXISTS idx_geo_asn_asn ON geo_asn (asn);

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
    provider_id         BIGINT NOT NULL REFERENCES provider (id),
    rule_identity       TEXT NOT NULL,
    normalised_severity TEXT NOT NULL
                        CHECK (normalised_severity IN ('critical', 'high', 'medium',
                                                       'low', 'informational')),
    provider_severity   TEXT,
    category            TEXT,
    rule_source         TEXT,
    fetched_at          BIGINT NOT NULL CHECK (fetched_at >= 0 AND fetched_at < 4102444800),
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
    id                  BIGINT PRIMARY KEY,
    provider_id         BIGINT NOT NULL REFERENCES provider (id),
    provider_event_key  TEXT NOT NULL,
    occurred_at         BIGINT NOT NULL CHECK (occurred_at >= 0 AND occurred_at < 4102444800),
    ingested_at         BIGINT NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    rule_identity       TEXT NOT NULL,
    signature           TEXT NOT NULL,
    event_action        TEXT NOT NULL CHECK (event_action IN ('allowed', 'blocked', 'unknown')),
    normalised_severity TEXT
                        CHECK (normalised_severity IS NULL
                               OR normalised_severity IN ('critical', 'high', 'medium',
                                                          'low', 'informational')),
    src_address         TEXT NOT NULL,
    src_port            BIGINT CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_address         TEXT NOT NULL,
    dst_port            BIGINT CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    protocol            TEXT,
    in_interface_device TEXT,
    src_client_id       BIGINT REFERENCES client (id),
    src_interface_id    BIGINT REFERENCES interface (id),
    -- 1 when the event's source is this firewall, as flow.src_is_this_firewall
    -- decides it for a record's end; such a source has no client.
    src_is_this_firewall BIGINT NOT NULL DEFAULT 0 CHECK (src_is_this_firewall IN (0, 1)),
    flow_ref            BIGINT,
    UNIQUE (provider_id, provider_event_key),
    CHECK (src_is_this_firewall = 0 OR src_client_id IS NULL)
);

CREATE INDEX IF NOT EXISTS idx_security_event_occurred_at ON security_event (occurred_at);
CREATE INDEX IF NOT EXISTS idx_security_event_client_occurred_at ON security_event (src_client_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_security_event_rule_occurred_at
    ON security_event (provider_id, rule_identity, occurred_at);
-- Classification rewrites an event's source end with the flows of that address.
CREATE INDEX IF NOT EXISTS idx_security_event_src_address
    ON security_event (src_address, occurred_at);

-- ---------------------------------------------------------------------------
-- blocked_decision — every refusal, whichever engine decided it, under one
-- vocabulary. Closes G2.
--
-- A refusal lives in three tables under three column names and three value sets:
-- flow.action IN ('block', 'reject'), dns_resolution.action IN ('block', 'drop') and
-- security_event.event_action = 'blocked'. This view projects each of them once,
-- keyed by (source_table, source_id), so every refusal appears exactly once.
--
-- engine_kind is opnview's own closed vocabulary, recorded in
-- docs/data-model.md, and every refusal has a place in it:
--   firewall_rule                a filter-log record whose reason is `match`
--   firewall_no_rule             one whose reason is another established pf
--                                reason: dropped for a reason no rule expresses
--   firewall_reason_not_recorded one whose reason is absent or outside the
--                                established set, so neither can be claimed
--   dns_advertising_list, dns_tracking_list, dns_threat_list,
--   dns_parental_list, dns_other_list
--                                a lookup refused by a list carrying that
--                                user-assigned purpose
--   dns_unassigned_list          refused by a list nobody has classified
--   dns_list_not_recorded        refused with no list named by the resolver
--   security_engine              a security event the engine blocked
-- The purpose is the user's, from blocklist.purpose, and never derived from a
-- list's name.
--
-- engine_reference is what decided inside the engine: the pf label (`rid`), the
-- list's name, or the rule identity. target is what was refused: the
-- destination address, or the domain. target_is_this_firewall is 1 when that
-- address is one the firewall itself holds (interface_address, addr4 or addr6),
-- which is how a feed says "-> this firewall".
-- ---------------------------------------------------------------------------
-- The view is replaced on every apply, so an edit to its text reaches a database
-- the previous text was applied to.
CREATE OR REPLACE VIEW blocked_decision AS
SELECT
    'flow'                                                  AS source_table,
    f.id                                                    AS source_id,
    f.observed_at                                           AS occurred_at,
    CASE
        WHEN f.log_reason = 'match' THEN 'firewall_rule'
        WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                              'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                              'state-mismatch', 'state-insert', 'state-limit',
                              'src-limit', 'synproxy') THEN 'firewall_no_rule'
        ELSE 'firewall_reason_not_recorded'
    END                                                     AS engine_kind,
    f.rid                                                   AS engine_reference,
    f.src_client_id                                         AS client_id,
    f.src_interface_id                                      AS interface_id,
    f.dst_address                                           AS target,
    'address'                                               AS target_kind,
    CAST(CAST(EXISTS (SELECT 1 FROM interface_address AS a
            WHERE a.address = f.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
                                                            AS target_is_this_firewall
FROM flow AS f
WHERE f.action IN ('block', 'reject')
UNION ALL
SELECT
    'dns_resolution',
    r.id,
    r.looked_up_at,
    CASE
        WHEN r.blocklist_id IS NULL THEN 'dns_list_not_recorded'
        WHEN b.purpose IS NULL THEN 'dns_unassigned_list'
        WHEN b.purpose = 'advertising' THEN 'dns_advertising_list'
        WHEN b.purpose = 'tracking' THEN 'dns_tracking_list'
        WHEN b.purpose = 'threat' THEN 'dns_threat_list'
        WHEN b.purpose = 'parental' THEN 'dns_parental_list'
        ELSE 'dns_other_list'
    END,
    b.name,
    r.client_id,
    r.interface_id,
    r.domain,
    'domain',
    0
FROM dns_resolution AS r
LEFT JOIN blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
UNION ALL
SELECT
    'security_event',
    e.id,
    e.occurred_at,
    'security_engine',
    e.rule_identity,
    e.src_client_id,
    e.src_interface_id,
    e.dst_address,
    'address',
    CAST(CAST(EXISTS (SELECT 1 FROM interface_address AS a
            WHERE a.address = e.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
FROM security_event AS e
WHERE e.event_action = 'blocked';

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
    id             BIGINT PRIMARY KEY,
    file_id        TEXT NOT NULL,
    byte_offset    BIGINT NOT NULL CHECK (byte_offset >= 0),
    file_sequence  BIGINT,
    rotation_state TEXT NOT NULL CHECK (rotation_state IN ('current', 'rotated', 'lost')),
    observed_at    BIGINT NOT NULL CHECK (observed_at >= 0 AND observed_at < 4102444800),
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
--
-- outcome NAMES WHAT THE LAST PROBE OR READ TURNED INTO, from a closed set, so
-- that no reader ever parses `detail` (SPEC-step-5b-widget-api.md, D1). The
-- state says whether the source can be read; the outcome says why, in a word a
-- program reads and an interface translates; the detail stays as diagnostic
-- text. The set is store.AvailabilityOutcomes, and docs/data-model.md states
-- each code's meaning.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS source_availability (
    provider_id BIGINT PRIMARY KEY REFERENCES provider (id),
    state       TEXT NOT NULL
                CHECK (state IN ('reachable', 'present_but_disabled', 'unavailable')),
    outcome     TEXT NOT NULL DEFAULT 'not_yet_probed'
                CHECK (outcome IN ('not_yet_probed', 'healthy', 'healthy_empty',
                                   'partial_readings', 'present_but_disabled',
                                   'service_not_running', 'authentication_failed',
                                   'denied', 'not_found', 'server_error', 'empty_body',
                                   'result_failed', 'undecodable', 'status_not_ok',
                                   'transport_failure', 'certificate_refused',
                                   'request_not_made', 'plugin_not_installed',
                                   'read_not_supported', 'no_cache_read',
                                   'no_local_data_read', 'log_grammar_not_recorded',
                                   'no_licence_key', 'licence_key_undecryptable',
                                   'no_account_id', 'download_refused',
                                   'download_limit_reached', 'download_failed',
                                   'download_malformed', 'store_failure',
                                   'script_failed', 'script_timeout',
                                   'records_invalid', 'firewall_version_unsupported',
                                   'listener_not_configured', 'no_message_received')),
    probe       TEXT NOT NULL,
    detail      TEXT,
    checked_at  BIGINT NOT NULL CHECK (checked_at >= 0 AND checked_at < 4102444800),
    -- WHEN THE CURRENT STATE BEGAN, AS opnview's OWN PROBES BOUND IT (coverage row
    -- 39 of docs/widget-api.md: "the detection engine stopped at 06:12"). opnview
    -- sees a provider only when it probes or reads it, so the instant a state began
    -- is never known exactly: it lies after the last check that saw the previous
    -- state and no later than the first check that saw this one. Those two checks
    -- are the bounds, written by Store.SetAvailability when a check finds a state
    -- other than the one recorded. Both stay NULL until a change has been
    -- observed -- the first check after the row was created changes "not yet
    -- probed" into a state and bounds nothing -- and a reader names that absence
    -- rather than printing an instant.
    state_since_after_at  BIGINT
                          CHECK (state_since_after_at >= 0 AND state_since_after_at < 4102444800),
    state_since_by_at     BIGINT
                          CHECK (state_since_by_at >= 0 AND state_since_by_at < 4102444800),
    CHECK ((state_since_after_at IS NULL) = (state_since_by_at IS NULL)),
    CHECK (state_since_after_at IS NULL OR state_since_after_at <= state_since_by_at)
);

-- ---------------------------------------------------------------------------
-- plugin_state — a plugin's durable values: a watermark, a cursor, the last
-- thing it read. Bounded: a handful of rows per plugin.
--
-- "Plugin" here is opnview's own term for a directory of manifest, data schema
-- and script that opnview loads (docs/plugins.md; docs/data-model.md,
-- Vocabulary), and NOT an OPNsense plugin package such as os-ddclient, which
-- OPNsense also calls a plugin. A plugin of a kind of its own writes these rows
-- in the same transaction as the rows of its own tables
-- (Store.WritePluginPass), so a restart neither loses nor repeats a record. The
-- tables a plugin declares are created at load, beside this file rather than in
-- it, as plugin_<plugin>__<table>.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plugin_state (
    plugin     TEXT NOT NULL CHECK (length(plugin) > 0),
    key        TEXT NOT NULL CHECK (length(key) > 0),
    value      TEXT NOT NULL,
    updated_at BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800),
    PRIMARY KEY (plugin, key)
);

-- ---------------------------------------------------------------------------
-- plugin_table — the full declaration of every table a plugin owns, as it was
-- when the table was created. Bounded: one row per plugin table.
--
-- It is what makes a plugin's schema change a REBUILD rather than a refusal or
-- a silent loss (Store.EnsurePluginTable): a declaration that differs from the
-- recorded one in its columns, types, nullability, primary key, indexes or time
-- column sets the existing table aside, under a name in plugin_aside_table, and
-- creates the new one empty. The retention is recorded too, so an aside table
-- is purged by the retention its rows were written under.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plugin_table (
    plugin            TEXT NOT NULL CHECK (length(plugin) > 0),
    table_name        TEXT NOT NULL CHECK (length(table_name) > 0),
    declaration       TEXT NOT NULL,
    time_column       TEXT,
    retention_seconds BIGINT NOT NULL CHECK (retention_seconds >= 0),
    declared_at       BIGINT NOT NULL CHECK (declared_at >= 0 AND declared_at < 4102444800),
    PRIMARY KEY (plugin, table_name)
);

-- plugin_aside_table — a plugin table set aside by a schema change, kept under a
-- name that can never be read as a live plugin table (it begins "aside_", not
-- "plugin_"). Bounded: an aside table with a time column is purged by its
-- recorded retention and dropped, with its row here, when it is empty; one with
-- no time column is kept and listed until somebody removes it.
CREATE TABLE IF NOT EXISTS plugin_aside_table (
    name              TEXT PRIMARY KEY CHECK (left(name, 6) = 'aside_'),
    plugin            TEXT NOT NULL CHECK (length(plugin) > 0),
    table_name        TEXT NOT NULL CHECK (length(table_name) > 0),
    time_column       TEXT,
    retention_seconds BIGINT NOT NULL CHECK (retention_seconds >= 0),
    set_aside_at      BIGINT NOT NULL CHECK (set_aside_at >= 0 AND set_aside_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Pair volume observation — the daily per-address-pair volume, de-duplicated.
--
-- IT IS DERIVED, NOT COLLECTED, AND THAT IS THE MAINTAINER'S RULING. No
-- collector writes this table and none will: the only per-pair endpoint the
-- survey found carries neither a port nor a protocol, and `flow` carries both
-- exactly, from the filter log. So these rows are step 5's to COMPUTE from
-- `flow`, and step 5 does: the statement insert_pair_volume of
-- internal/store/derive.sql rewrites one UTC day from the flows observed in it,
-- and nothing else writes the table but the retention purge. The SAMPLED
-- per-pair volume, which is a different thing -- bytes seen in two-second
-- samples rather than a period total -- stays in measurement_sample.
--
-- The de-duplication key below is kept as surveyed: a per-pair figure summed
-- over a day is direction-free whether it came from the firewall's own
-- aggregate or from opnview's own records, so the derivation groups on
-- (day_start_at, endpoint_low, endpoint_high, service_port, protocol), where
-- endpoint_low and endpoint_high are the two addresses in lexicographic order,
-- and both directions of one pair collapse to one row.
--
-- Origin of the shape: /api/diagnostics/networkinsight/top/FlowSourceAddrDetails/...
-- with a field list containing src_addr and dst_addr; the row carries total (the
-- octets or packets measure) and last_seen. Survey: data source 3.
--
-- service_port IS THE REAL DESTINATION PORT, flow.dst_port, and no longer
-- Insight's min(src_port, dst_port) heuristic: the derivation reads the filter
-- log, which carries the exact port. A protocol with no port -- every record
-- whose dst_port is NULL -- is stored with service_port 0. octets is the sum of
-- the logged packet lengths and packets the count of logged records, so both
-- are LOGGED figures and lower bounds, never conversation volumes; they stay
-- REAL because the shape was Insight's. observed_direction and
-- observed_interface_device are those of the earliest flow of the group and are
-- kept for provenance only.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pair_volume_observation (
    id                        BIGINT PRIMARY KEY,
    day_start_at              BIGINT NOT NULL CHECK (day_start_at >= 0 AND day_start_at < 4102444800),
    endpoint_low              TEXT NOT NULL,
    endpoint_high             TEXT NOT NULL,
    service_port              BIGINT NOT NULL
                              CHECK (service_port >= 0 AND service_port <= 65535),
    protocol                  TEXT NOT NULL,
    octets                    DOUBLE PRECISION NOT NULL CHECK (octets >= 0),
    packets                   DOUBLE PRECISION NOT NULL CHECK (packets >= 0),
    observed_direction        TEXT NOT NULL CHECK (observed_direction IN ('in', 'out', 'unknown')),
    observed_interface_device TEXT,
    last_seen_at              BIGINT NOT NULL CHECK (last_seen_at >= 0 AND last_seen_at < 4102444800),
    ingested_at               BIGINT NOT NULL CHECK (ingested_at >= 0 AND ingested_at < 4102444800),
    CHECK (endpoint_low <= endpoint_high),
    UNIQUE (day_start_at, endpoint_low, endpoint_high, service_port, protocol)
);

CREATE INDEX IF NOT EXISTS idx_pair_volume_day ON pair_volume_observation (day_start_at);

-- ---------------------------------------------------------------------------
-- The aggregate families. Six keys, four periods each, all computed from
-- opnview's own `flow` by internal/store/derive.sql and refreshed together, slot
-- by slot, on the contract docs/data-model.md writes down once.
--
-- THE FOUR PERIODS ARE CALENDAR SLOTS, ALIGNED TO UTC:
--   _1h   the hour
--   _24h  the day
--   _7d   the ISO week, starting Monday 00:00 UTC
--   _30d  the calendar MONTH, of 28, 29, 30 or 31 days
-- The `_30d` suffix holds months and the `_7d` suffix ISO weeks. The names are
-- the periods a widget asks for, and are kept; period_end_at says exactly where
-- each slot ends, so nothing has to multiply a day by thirty.
--
-- EVERY FAMILY CARRIES THE ALLOWED / BLOCKED / UNKNOWN SPLIT, of bytes and of
-- connections, and bytes is the sum of the three. "Allowed" is flow.action =
-- 'pass', "blocked" is 'block' or 'reject' -- a reject is a refusal that answers --
-- and "unknown" is a record whose action opnview could not name. An unknown
-- decision is counted as neither: a decision nobody read is not an allowance.
--
-- The byte figures are LOGGED BYTES, the sum of flow.packet_bytes. pf logs the
-- packet that establishes a state and not the rest of the connection
-- (pf.conf(5), `log`), so these are logged volume and never conversation volume.
--
-- A CONNECTION LOGGED ON TWO INTERFACES IS COUNTED ONCE, decision 2 of the
-- step-5A live corrections, which amends 5A Scope A2 and its OQ4: once, on the
-- client's `in` record. Its second record -- flow.pair_outcome 'second_leg' --
-- stays in `flow` and counts in the volume family only under its own
-- exit_leg_device, the interface it was logged on, which is that interface's own
-- figure; every other family leaves it out but the rule family, which counts
-- every record because each record is a match of its own rule.
--
-- Observation-point limit: traffic between two clients behind one interface
-- never reaches the firewall and is in none of these figures.
--
-- A slot is unique per period and key, through a unique index whose NULL-bearing
-- columns are wrapped in coalesce, because PostgreSQL treats NULLs in a unique
-- index as distinct.
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- volume_aggregate_* — per interface pair and outside peer: the primary volume
-- store, not a cache over Insight (survey, data source 3: per-pair data exists
-- on the firewall only daily, for 62 days).
--
-- src_interface_id IS NULLABLE: an inbound flow's source is outside and has no
-- interface, and the slot key wraps it in coalesce so that slot is unique too.
-- peer_address is classified_flow.peer_address, the outside end, and
-- traffic_direction the outbound / inbound / inter_interface classification, or
-- to_this_firewall / from_this_firewall when an end is this firewall, which is
-- therefore its own key and never part of the outside peers; the CHECKs pin both
-- to the interfaces the row carries. The direction in the key is what closes G3:
-- outbound and inbound are told apart in every period.
--
-- exit_leg_device is NULL on every row a volume total sums, and the device a
-- second leg was logged on otherwise: the second records of paired connections,
-- kept apart so they count on that interface's own figures and nowhere else.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL
                        CHECK (traffic_scope IN ('east_west', 'north_south', 'this_firewall')),
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface',
                                                     'to_this_firewall', 'from_this_firewall')),
    exit_leg_device     TEXT,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes),
    CHECK (traffic_scope = CASE
                               WHEN traffic_direction IN ('to_this_firewall', 'from_this_firewall')
                                   THEN 'this_firewall'
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south' END),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((traffic_direction = 'inter_interface')
               = (src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL))),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NULL OR dst_interface_id IS NOT NULL
           OR traffic_direction = 'outbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NOT NULL OR dst_interface_id IS NULL
           OR traffic_direction = 'inbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((peer_address IS NULL) = (traffic_direction = 'inter_interface')))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_1h_slot
    ON volume_aggregate_1h (period_start_at, coalesce(src_interface_id, -1),
                            coalesce(dst_interface_id, -1), coalesce(peer_address, ''),
                            traffic_direction, coalesce(exit_leg_device, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL
                        CHECK (traffic_scope IN ('east_west', 'north_south', 'this_firewall')),
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface',
                                                     'to_this_firewall', 'from_this_firewall')),
    exit_leg_device     TEXT,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes),
    CHECK (traffic_scope = CASE
                               WHEN traffic_direction IN ('to_this_firewall', 'from_this_firewall')
                                   THEN 'this_firewall'
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south' END),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((traffic_direction = 'inter_interface')
               = (src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL))),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NULL OR dst_interface_id IS NOT NULL
           OR traffic_direction = 'outbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NOT NULL OR dst_interface_id IS NULL
           OR traffic_direction = 'inbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((peer_address IS NULL) = (traffic_direction = 'inter_interface')))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_24h_slot
    ON volume_aggregate_24h (period_start_at, coalesce(src_interface_id, -1),
                            coalesce(dst_interface_id, -1), coalesce(peer_address, ''),
                            traffic_direction, coalesce(exit_leg_device, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL
                        CHECK (traffic_scope IN ('east_west', 'north_south', 'this_firewall')),
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface',
                                                     'to_this_firewall', 'from_this_firewall')),
    exit_leg_device     TEXT,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes),
    CHECK (traffic_scope = CASE
                               WHEN traffic_direction IN ('to_this_firewall', 'from_this_firewall')
                                   THEN 'this_firewall'
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south' END),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((traffic_direction = 'inter_interface')
               = (src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL))),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NULL OR dst_interface_id IS NOT NULL
           OR traffic_direction = 'outbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NOT NULL OR dst_interface_id IS NULL
           OR traffic_direction = 'inbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((peer_address IS NULL) = (traffic_direction = 'inter_interface')))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_7d_slot
    ON volume_aggregate_7d (period_start_at, coalesce(src_interface_id, -1),
                            coalesce(dst_interface_id, -1), coalesce(peer_address, ''),
                            traffic_direction, coalesce(exit_leg_device, ''));

CREATE TABLE IF NOT EXISTS volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    peer_address        TEXT,
    traffic_scope       TEXT NOT NULL
                        CHECK (traffic_scope IN ('east_west', 'north_south', 'this_firewall')),
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface',
                                                     'to_this_firewall', 'from_this_firewall')),
    exit_leg_device     TEXT,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes),
    CHECK (traffic_scope = CASE
                               WHEN traffic_direction IN ('to_this_firewall', 'from_this_firewall')
                                   THEN 'this_firewall'
                               WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
                                   THEN 'east_west'
                               ELSE 'north_south' END),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((traffic_direction = 'inter_interface')
               = (src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL))),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NULL OR dst_interface_id IS NOT NULL
           OR traffic_direction = 'outbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR src_interface_id IS NOT NULL OR dst_interface_id IS NULL
           OR traffic_direction = 'inbound'),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((peer_address IS NULL) = (traffic_direction = 'inter_interface')))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_volume_aggregate_30d_slot
    ON volume_aggregate_30d (period_start_at, coalesce(src_interface_id, -1),
                            coalesce(dst_interface_id, -1), coalesce(peer_address, ''),
                            traffic_direction, coalesce(exit_leg_device, ''));

-- ---------------------------------------------------------------------------
-- owner_volume_aggregate_* — the same four periods, keyed on the person rather
-- than on the interface pair. Closed G11.
--
-- A flow belongs to the owner of its INSIDE client, classified_flow's
-- local_client_id: the source when the source is inside, else the destination.
-- A flow with no inside client belongs to nobody and is in no row of this
-- family; it is still in the interface family above.
--
-- owner_id IS NULL IS THE UNASSIGNED BUCKET, AND IT IS NOT OPTIONAL. Ownership is
-- assigned by hand and most machines belong to nobody in particular, so a
-- per-person aggregate that dropped the unowned clients would under-report the
-- network while looking complete. Nothing here infers an owner.
--
-- client_count is the number of distinct clients that contributed to THIS slot.
-- It MUST NOT be summed across slots: the same machine active in two hours would
-- be counted twice. Across slots, distinct clients are counted from the client
-- family below, whose key carries the client.
--
-- Reassigning a client to another owner invalidates every slot holding one of its
-- flows, because ownership is an attribute of the person and not of the flow.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS owner_volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            BIGINT REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    client_count        BIGINT NOT NULL CHECK (client_count >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_1h_slot
    ON owner_volume_aggregate_1h (period_start_at, coalesce(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            BIGINT REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    client_count        BIGINT NOT NULL CHECK (client_count >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_24h_slot
    ON owner_volume_aggregate_24h (period_start_at, coalesce(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            BIGINT REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    client_count        BIGINT NOT NULL CHECK (client_count >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_7d_slot
    ON owner_volume_aggregate_7d (period_start_at, coalesce(owner_id, -1), traffic_scope);

CREATE TABLE IF NOT EXISTS owner_volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    owner_id            BIGINT REFERENCES owner (id),
    traffic_scope       TEXT NOT NULL CHECK (traffic_scope IN ('east_west', 'north_south')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    client_count        BIGINT NOT NULL CHECK (client_count >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_volume_aggregate_30d_slot
    ON owner_volume_aggregate_30d (period_start_at, coalesce(owner_id, -1), traffic_scope);

-- ---------------------------------------------------------------------------
-- client_volume_aggregate_* — per machine. Closes G4.
--
-- Keyed on (period, inside client, traffic direction). A flow lands under its
-- local_client_id, so a client's inbound traffic is its own as well as its
-- outbound traffic. distinct_peers counts the distinct addresses at the OTHER end
-- of the client's flows in THIS slot -- the catalogue's "distinct destinations"
-- for an outbound flow -- and, as with every distinct figure, is never summed across
-- slots.
--
-- A client row may be removed (the retention purge, or a superseded identity
-- whose flows were re-pointed): its slots go with it through ON DELETE CASCADE,
-- after the refresh has already re-keyed every slot its flows moved out of.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS client_volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    distinct_peers      BIGINT NOT NULL CHECK (distinct_peers >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_client_volume_aggregate_1h_slot
    ON client_volume_aggregate_1h (period_start_at, client_id, traffic_direction);
CREATE INDEX IF NOT EXISTS idx_client_volume_aggregate_1h_client
    ON client_volume_aggregate_1h (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS client_volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    distinct_peers      BIGINT NOT NULL CHECK (distinct_peers >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_client_volume_aggregate_24h_slot
    ON client_volume_aggregate_24h (period_start_at, client_id, traffic_direction);
CREATE INDEX IF NOT EXISTS idx_client_volume_aggregate_24h_client
    ON client_volume_aggregate_24h (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS client_volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    distinct_peers      BIGINT NOT NULL CHECK (distinct_peers >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_client_volume_aggregate_7d_slot
    ON client_volume_aggregate_7d (period_start_at, client_id, traffic_direction);
CREATE INDEX IF NOT EXISTS idx_client_volume_aggregate_7d_client
    ON client_volume_aggregate_7d (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS client_volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    distinct_peers      BIGINT NOT NULL CHECK (distinct_peers >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_client_volume_aggregate_30d_slot
    ON client_volume_aggregate_30d (period_start_at, client_id, traffic_direction);
CREATE INDEX IF NOT EXISTS idx_client_volume_aggregate_30d_client
    ON client_volume_aggregate_30d (client_id, period_start_at);

-- ---------------------------------------------------------------------------
-- peer_volume_aggregate_* — per machine and per address at the other end of its
-- flows. It is what makes every distinct_peers figure exact.
--
-- Keyed on (period, inside client, traffic direction, peer address). The client
-- is classified_flow's local_client_id, as in the client family; peer_address is
-- the address at the OTHER end from that client: the destination when the client
-- is the source, the source otherwise. For an outbound or an inbound flow that is
-- the volume family's peer_address, the outside end; between two interfaces it is
-- the other inside address, which the volume family does not key on. The term
-- "peer" is the one distinct_peers already used for that address.
--
-- A distinct count cannot be summed across slots, and the client family's
-- distinct_peers used to be counted in each slot from `flow` alone, so a day or
-- a month composed from finer slots could only carry an approximation of it.
-- With the peer in the key, the client family of EVERY slot is regrouped from
-- this family's same slot, and distinct_peers is the number of its rows: exact
-- in an hour, in a composed day and in a rolled-up week or month alike, and one
-- peer active in two hours of a day counts once in that day.
--
-- Its slots are composed exactly as the other families' are, its retention and
-- purge are theirs, and a client removed takes its slots with it through
-- ON DELETE CASCADE.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS peer_volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    peer_address        TEXT NOT NULL CHECK (length(peer_address) > 0),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_peer_volume_aggregate_1h_slot
    ON peer_volume_aggregate_1h (period_start_at, client_id, traffic_direction, peer_address);
CREATE INDEX IF NOT EXISTS idx_peer_volume_aggregate_1h_client
    ON peer_volume_aggregate_1h (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS peer_volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    peer_address        TEXT NOT NULL CHECK (length(peer_address) > 0),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_peer_volume_aggregate_24h_slot
    ON peer_volume_aggregate_24h (period_start_at, client_id, traffic_direction, peer_address);
CREATE INDEX IF NOT EXISTS idx_peer_volume_aggregate_24h_client
    ON peer_volume_aggregate_24h (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS peer_volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    peer_address        TEXT NOT NULL CHECK (length(peer_address) > 0),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_peer_volume_aggregate_7d_slot
    ON peer_volume_aggregate_7d (period_start_at, client_id, traffic_direction, peer_address);
CREATE INDEX IF NOT EXISTS idx_peer_volume_aggregate_7d_client
    ON peer_volume_aggregate_7d (client_id, period_start_at);

CREATE TABLE IF NOT EXISTS peer_volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    client_id           BIGINT NOT NULL REFERENCES client (id) ON DELETE CASCADE,
    traffic_direction   TEXT NOT NULL
                        CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface')),
    peer_address        TEXT NOT NULL CHECK (length(peer_address) > 0),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_peer_volume_aggregate_30d_slot
    ON peer_volume_aggregate_30d (period_start_at, client_id, traffic_direction, peer_address);
CREATE INDEX IF NOT EXISTS idx_peer_volume_aggregate_30d_client
    ON peer_volume_aggregate_30d (client_id, period_start_at);

-- ---------------------------------------------------------------------------
-- domain_volume_aggregate_* — per inferred site name and client. Closes G5.
--
-- Computed from the flows that carry a domain_attribution, so a site name
-- outlives both the flow and the lookup it was inferred from. site_name is the
-- attributed name verbatim; grouping it into a registrable domain is done at read
-- time from the Public Suffix List and never stored. client_id is the flow's
-- source client, the machine that made the lookup; NULL when the flow named
-- none, keyed through coalesce.
--
-- The flow count is allowed_connections + blocked_connections +
-- unknown_connections. Distinct clients
-- per site over several slots are counted as distinct client_id over the slot
-- rows, which is exact because the client is in the key; nothing is summed.
--
-- aggregate_mode = 'no_domains' does NOT stop this family being written: the mode
-- withholds names at the API (step 5B), and what is stored is unchanged.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS domain_volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    site_name           TEXT NOT NULL CHECK (length(site_name) > 0),
    client_id           BIGINT REFERENCES client (id) ON DELETE CASCADE,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_volume_aggregate_1h_slot
    ON domain_volume_aggregate_1h (period_start_at, site_name, coalesce(client_id, -1));

CREATE TABLE IF NOT EXISTS domain_volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    site_name           TEXT NOT NULL CHECK (length(site_name) > 0),
    client_id           BIGINT REFERENCES client (id) ON DELETE CASCADE,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_volume_aggregate_24h_slot
    ON domain_volume_aggregate_24h (period_start_at, site_name, coalesce(client_id, -1));

CREATE TABLE IF NOT EXISTS domain_volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    site_name           TEXT NOT NULL CHECK (length(site_name) > 0),
    client_id           BIGINT REFERENCES client (id) ON DELETE CASCADE,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_volume_aggregate_7d_slot
    ON domain_volume_aggregate_7d (period_start_at, site_name, coalesce(client_id, -1));

CREATE TABLE IF NOT EXISTS domain_volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    site_name           TEXT NOT NULL CHECK (length(site_name) > 0),
    client_id           BIGINT REFERENCES client (id) ON DELETE CASCADE,
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_domain_volume_aggregate_30d_slot
    ON domain_volume_aggregate_30d (period_start_at, site_name, coalesce(client_id, -1));

-- ---------------------------------------------------------------------------
-- rule_volume_aggregate_* — per rule and interface pair, so the matrix's rules
-- and a rule's recent history are answerable beyond the flow horizon.
--
-- rule_id is the rule the flow's rid resolved to; NULL is every flow whose rid
-- resolved to nothing or named none, kept as its own slot through coalesce so the
-- family sums to every flow. A rule is never purged, so a rule deleted on the
-- firewall after it was discovered keeps its slots and its description.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rule_volume_aggregate_1h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    rule_id             BIGINT REFERENCES rule (id),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_rule_volume_aggregate_1h_slot
    ON rule_volume_aggregate_1h (period_start_at, coalesce(rule_id, -1),
                                 coalesce(src_interface_id, -1), coalesce(dst_interface_id, -1));
CREATE INDEX IF NOT EXISTS idx_rule_volume_aggregate_1h_rule
    ON rule_volume_aggregate_1h (rule_id, period_start_at);

CREATE TABLE IF NOT EXISTS rule_volume_aggregate_24h (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    rule_id             BIGINT REFERENCES rule (id),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_rule_volume_aggregate_24h_slot
    ON rule_volume_aggregate_24h (period_start_at, coalesce(rule_id, -1),
                                 coalesce(src_interface_id, -1), coalesce(dst_interface_id, -1));
CREATE INDEX IF NOT EXISTS idx_rule_volume_aggregate_24h_rule
    ON rule_volume_aggregate_24h (rule_id, period_start_at);

CREATE TABLE IF NOT EXISTS rule_volume_aggregate_7d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    rule_id             BIGINT REFERENCES rule (id),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_rule_volume_aggregate_7d_slot
    ON rule_volume_aggregate_7d (period_start_at, coalesce(rule_id, -1),
                                 coalesce(src_interface_id, -1), coalesce(dst_interface_id, -1));
CREATE INDEX IF NOT EXISTS idx_rule_volume_aggregate_7d_rule
    ON rule_volume_aggregate_7d (rule_id, period_start_at);

CREATE TABLE IF NOT EXISTS rule_volume_aggregate_30d (
    id                  BIGINT PRIMARY KEY,
    period_start_at     BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at       BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    rule_id             BIGINT REFERENCES rule (id),
    src_interface_id    BIGINT REFERENCES interface (id),
    dst_interface_id    BIGINT REFERENCES interface (id),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    allowed_bytes       BIGINT NOT NULL CHECK (allowed_bytes >= 0),
    blocked_bytes       BIGINT NOT NULL CHECK (blocked_bytes >= 0),
    unknown_bytes       BIGINT NOT NULL CHECK (unknown_bytes >= 0),
    allowed_connections BIGINT NOT NULL CHECK (allowed_connections >= 0),
    blocked_connections BIGINT NOT NULL CHECK (blocked_connections >= 0),
    unknown_connections BIGINT NOT NULL CHECK (unknown_connections >= 0),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    CHECK (bytes = allowed_bytes + blocked_bytes + unknown_bytes)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_rule_volume_aggregate_30d_slot
    ON rule_volume_aggregate_30d (period_start_at, coalesce(rule_id, -1),
                                 coalesce(src_interface_id, -1), coalesce(dst_interface_id, -1));
CREATE INDEX IF NOT EXISTS idx_rule_volume_aggregate_30d_rule
    ON rule_volume_aggregate_30d (rule_id, period_start_at);

-- ---------------------------------------------------------------------------
-- purged_flow_hour — the PURGED PART of an hour: what the retention purge
-- removed from `flow`, summed per UTC hour on every key an aggregate family
-- reads.
--
-- WHY IT EXISTS. An hour slot is computed from the flows observed inside it.
-- When the retention horizon falls inside an hour -- the current hour itself,
-- under a retention shorter than an hour -- the purge removes part of that
-- hour's flows while the rest are still being ingested, reclassified or
-- attributed. Recomputing the hour from `flow` alone would then drop the purged
-- flows, and leaving it as it was would freeze it: a flow ingested late into it
-- would never be counted, and a reclassification would leave its old figures in
-- place. So the purge writes here, in the same transaction and BEFORE it
-- deletes them, the figures of the flows it removes, and every hour slot is
-- computed from the flows still present PLUS its purged part
-- (internal/store/derive.sql, "The refresh"). Days, weeks and months are
-- composed from hours and days, so they follow.
--
-- The key is the union of the families' keys, so each family regroups it as it
-- regroups `flow`:
--   src_interface_id, dst_interface_id  both ends' interfaces (volume, rule);
--   src_client_id                       the source client (domain);
--   local_client_id                     classified_flow's inside client (peer,
--                                       client, owner);
--   traffic_direction                   classified_flow's direction;
--   peer_address                        the address at the other end from the
--                                       inside end -- the peer family's peer --
--                                       and, for a flow with no inside end,
--                                       classified_flow's peer_address. The
--                                       volume family's peer is this address
--                                       except between interfaces, where it is
--                                       none;
--   rule_id, site_name, action          the rule, the attributed site name, the
--                                       decision;
--   exit_leg_device                     classified_flow's: a second leg counts on
--                                       its interface's figures and in the rule
--                                       family only.
-- bytes and connections are the summed packet_bytes and the number of records.
--
-- What the purged part keeps is the flows' classification when they were
-- purged, with one exception, the one the flows themselves obey: when a better
-- identity -- a lease, a MAC -- later names the address of an address-level
-- client, the purged part is re-pointed to it as the flows are
-- (internal/store/derive.sql, "repoint_purged_*"). An interface that changes
-- after the flows are gone does not reach them. A client is never purged while
-- the purged part names it (internal/store/purge/purge.sql), so the client, peer,
-- owner and domain families keep its bytes for as long as the hour is kept;
-- ON DELETE SET NULL is only the backstop of the foreign key.
--
-- No unique key: each purge appends the rows of the flows it removed. A flow is
-- added once because it is stored once: the purge records how far back it has
-- purged (retention_purge), and the flow insert refuses a record older than that,
-- inside the insert statement, so no purge committing during a collection pass can
-- let one in again, although the firewall's page keeps offering it.
--
-- Growing: purged exactly as the hour slots are -- once the hour, its ISO week
-- and its calendar month have all ended before the horizon.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS purged_flow_hour (
    id                BIGINT PRIMARY KEY,
    hour_start_at     BIGINT NOT NULL CHECK (hour_start_at >= 0 AND hour_start_at < 4102444800
                                              AND hour_start_at % 3600 = 0),
    src_interface_id  BIGINT REFERENCES interface (id),
    dst_interface_id  BIGINT REFERENCES interface (id),
    src_client_id     BIGINT REFERENCES client (id) ON DELETE SET NULL,
    local_client_id   BIGINT REFERENCES client (id) ON DELETE SET NULL,
    traffic_direction TEXT NOT NULL
                      CHECK (traffic_direction IN ('outbound', 'inbound', 'inter_interface',
                                                   'to_this_firewall', 'from_this_firewall')),
    -- NULL only for a flow with an end that is this firewall whose other end is
    -- not outside: classified_flow's peer_address is NULL there, and the volume
    -- family keys on that.
    peer_address      TEXT,
    -- classified_flow.exit_leg_device: the device a second leg was logged on.
    exit_leg_device   TEXT,
    rule_id           BIGINT REFERENCES rule (id),
    site_name         TEXT CHECK (site_name IS NULL OR length(site_name) > 0),
    action            TEXT NOT NULL CHECK (action IN ('pass', 'block', 'reject', 'unknown')),
    bytes             BIGINT NOT NULL CHECK (bytes >= 0),
    connections       BIGINT NOT NULL CHECK (connections > 0),
    purged_at         BIGINT NOT NULL CHECK (purged_at >= 0 AND purged_at < 4102444800),
    CHECK (traffic_direction IN ('to_this_firewall', 'from_this_firewall')
           OR ((traffic_direction = 'inter_interface')
               = (src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL))),
    CHECK (peer_address IS NOT NULL OR traffic_direction IN ('to_this_firewall', 'from_this_firewall'))
);

CREATE INDEX IF NOT EXISTS idx_purged_flow_hour_hour ON purged_flow_hour (hour_start_at);
-- The refresh selects every hour a purge has written a purged part for since the
-- previous refresh (internal/store/derive.sql, dirty_hours), by an index search
-- rather than a scan.
CREATE INDEX IF NOT EXISTS idx_purged_flow_hour_purged_at ON purged_flow_hour (purged_at, hour_start_at);
-- The two client columns, so removing a client sets them to NULL by an index
-- search rather than a scan.
CREATE INDEX IF NOT EXISTS idx_purged_flow_hour_src_client ON purged_flow_hour (src_client_id);
CREATE INDEX IF NOT EXISTS idx_purged_flow_hour_local_client ON purged_flow_hour (local_client_id);

-- ---------------------------------------------------------------------------
-- netflow_parts — the figures of the `netflow` measure that are counted per action
-- (the maintainer's decisions of 11 October 2026 after the PS22 build: finer
-- coverage classes, and coverage per action; NJ5 and NJ8 of the amendment "the
-- NetFlow join with every interface captured"). One value holds, for each of the
-- allowed (pass), blocked (block, reject) and unknown parts -- the parts the
-- logged figures are split into -- the eighteen figures of that part:
--
--   <part>_packets                         the measured packets
--   <part>_measured_connections            both directions measured     } the four
--   <part>_partial_connections             some measured bytes, not     } coverage
--                                          both directions measured     } classes,
--   <part>_unmeasured_connections          a readable configuration,    } summing to
--                                          no measured byte             } the part's
--   <part>_capture_not_known_connections   no readable, enabled capture } connections
--                                          configuration at the connection's start
--   <part>_direction_<state>_connections   the connections with at least one direction
--                                          in <state>, one of NJ5's twelve states:
--                                          measured, in_progress, awaiting_export,
--                                          missing, export_gap,
--                                          not_exported_when_refused,
--                                          forwarding_export_unsettled,
--                                          ingress_not_captured, parent_copies_only,
--                                          interface_not_exported,
--                                          interface_not_captured,
--                                          capture_configuration_unknown. A connection
--                                          whose two directions are in two states
--                                          counts under both, so these do not sum to
--                                          the connections: they say which states
--                                          were met, and how often.
--   <part>_counted_copy_connections        the connections with a direction counted
--                                          from translated copies (NJ5's sole-copy
--                                          rule)
--
-- Fifty-four fields in all. The widget measure judges from them whether every
-- connection an answer counts is covered (internal/widget/request/measure.go): a
-- connection is covered when its capture was known and every direction is
-- measured, in_progress, awaiting_export or missing (NJ5, "Coverage").
--
-- One value rather than fifty-four columns, so every statement that carries it --
-- the aggregate refresh, the purge and every `_netflow` twin -- names it once:
-- netflow_sum adds values field by field, and (netflow_sum(x)).* reads it back as
-- its fifty-four named columns. connection_netflow (below, after
-- connection_measured_volume) computes one connection's value.
--
-- The type is created once and never replaced: the project is rebuilt rather
-- than migrated before its first release (decided 11 October 2026), so a database
-- holding an earlier shape of the type is created afresh.
-- ---------------------------------------------------------------------------
DO $netflow_parts_type$
BEGIN
    IF to_regtype('netflow_parts') IS NULL THEN
        CREATE TYPE netflow_parts AS (
        allowed_packets                                             BIGINT,
        allowed_measured_connections                                BIGINT,
        allowed_partial_connections                                 BIGINT,
        allowed_unmeasured_connections                              BIGINT,
        allowed_capture_not_known_connections                       BIGINT,
        allowed_direction_measured_connections                      BIGINT,
        allowed_direction_in_progress_connections                   BIGINT,
        allowed_direction_awaiting_export_connections               BIGINT,
        allowed_direction_missing_connections                       BIGINT,
        allowed_direction_export_gap_connections                    BIGINT,
        allowed_direction_not_exported_when_refused_connections     BIGINT,
        allowed_direction_forwarding_export_unsettled_connections   BIGINT,
        allowed_direction_ingress_not_captured_connections          BIGINT,
        allowed_direction_parent_copies_only_connections            BIGINT,
        allowed_direction_interface_not_exported_connections        BIGINT,
        allowed_direction_interface_not_captured_connections        BIGINT,
        allowed_direction_capture_configuration_unknown_connections BIGINT,
        allowed_counted_copy_connections                            BIGINT,
        blocked_packets                                             BIGINT,
        blocked_measured_connections                                BIGINT,
        blocked_partial_connections                                 BIGINT,
        blocked_unmeasured_connections                              BIGINT,
        blocked_capture_not_known_connections                       BIGINT,
        blocked_direction_measured_connections                      BIGINT,
        blocked_direction_in_progress_connections                   BIGINT,
        blocked_direction_awaiting_export_connections               BIGINT,
        blocked_direction_missing_connections                       BIGINT,
        blocked_direction_export_gap_connections                    BIGINT,
        blocked_direction_not_exported_when_refused_connections     BIGINT,
        blocked_direction_forwarding_export_unsettled_connections   BIGINT,
        blocked_direction_ingress_not_captured_connections          BIGINT,
        blocked_direction_parent_copies_only_connections            BIGINT,
        blocked_direction_interface_not_exported_connections        BIGINT,
        blocked_direction_interface_not_captured_connections        BIGINT,
        blocked_direction_capture_configuration_unknown_connections BIGINT,
        blocked_counted_copy_connections                            BIGINT,
        unknown_packets                                             BIGINT,
        unknown_measured_connections                                BIGINT,
        unknown_partial_connections                                 BIGINT,
        unknown_unmeasured_connections                              BIGINT,
        unknown_capture_not_known_connections                       BIGINT,
        unknown_direction_measured_connections                      BIGINT,
        unknown_direction_in_progress_connections                   BIGINT,
        unknown_direction_awaiting_export_connections               BIGINT,
        unknown_direction_missing_connections                       BIGINT,
        unknown_direction_export_gap_connections                    BIGINT,
        unknown_direction_not_exported_when_refused_connections     BIGINT,
        unknown_direction_forwarding_export_unsettled_connections   BIGINT,
        unknown_direction_ingress_not_captured_connections          BIGINT,
        unknown_direction_parent_copies_only_connections            BIGINT,
        unknown_direction_interface_not_exported_connections        BIGINT,
        unknown_direction_interface_not_captured_connections        BIGINT,
        unknown_direction_capture_configuration_unknown_connections BIGINT,
        unknown_counted_copy_connections                            BIGINT
        );
    END IF;
END
$netflow_parts_type$;

-- netflow_parts_zero is the value holding nothing: the default of every column of the type.
CREATE OR REPLACE FUNCTION netflow_parts_zero() RETURNS netflow_parts
LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $netflow_parts_zero$
    SELECT CAST('(0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0)' AS netflow_parts)
$netflow_parts_zero$;

-- netflow_parts_add is two values added field by field.
CREATE OR REPLACE FUNCTION netflow_parts_add(total netflow_parts, part netflow_parts) RETURNS netflow_parts
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
AS $netflow_parts_add$
BEGIN
    RETURN CAST(ROW(
            total.allowed_packets + part.allowed_packets,
            total.allowed_measured_connections + part.allowed_measured_connections,
            total.allowed_partial_connections + part.allowed_partial_connections,
            total.allowed_unmeasured_connections + part.allowed_unmeasured_connections,
            total.allowed_capture_not_known_connections + part.allowed_capture_not_known_connections,
            total.allowed_direction_measured_connections + part.allowed_direction_measured_connections,
            total.allowed_direction_in_progress_connections + part.allowed_direction_in_progress_connections,
            total.allowed_direction_awaiting_export_connections + part.allowed_direction_awaiting_export_connections,
            total.allowed_direction_missing_connections + part.allowed_direction_missing_connections,
            total.allowed_direction_export_gap_connections + part.allowed_direction_export_gap_connections,
            total.allowed_direction_not_exported_when_refused_connections + part.allowed_direction_not_exported_when_refused_connections,
            total.allowed_direction_forwarding_export_unsettled_connections + part.allowed_direction_forwarding_export_unsettled_connections,
            total.allowed_direction_ingress_not_captured_connections + part.allowed_direction_ingress_not_captured_connections,
            total.allowed_direction_parent_copies_only_connections + part.allowed_direction_parent_copies_only_connections,
            total.allowed_direction_interface_not_exported_connections + part.allowed_direction_interface_not_exported_connections,
            total.allowed_direction_interface_not_captured_connections + part.allowed_direction_interface_not_captured_connections,
            total.allowed_direction_capture_configuration_unknown_connections + part.allowed_direction_capture_configuration_unknown_connections,
            total.allowed_counted_copy_connections + part.allowed_counted_copy_connections,
            total.blocked_packets + part.blocked_packets,
            total.blocked_measured_connections + part.blocked_measured_connections,
            total.blocked_partial_connections + part.blocked_partial_connections,
            total.blocked_unmeasured_connections + part.blocked_unmeasured_connections,
            total.blocked_capture_not_known_connections + part.blocked_capture_not_known_connections,
            total.blocked_direction_measured_connections + part.blocked_direction_measured_connections,
            total.blocked_direction_in_progress_connections + part.blocked_direction_in_progress_connections,
            total.blocked_direction_awaiting_export_connections + part.blocked_direction_awaiting_export_connections,
            total.blocked_direction_missing_connections + part.blocked_direction_missing_connections,
            total.blocked_direction_export_gap_connections + part.blocked_direction_export_gap_connections,
            total.blocked_direction_not_exported_when_refused_connections + part.blocked_direction_not_exported_when_refused_connections,
            total.blocked_direction_forwarding_export_unsettled_connections + part.blocked_direction_forwarding_export_unsettled_connections,
            total.blocked_direction_ingress_not_captured_connections + part.blocked_direction_ingress_not_captured_connections,
            total.blocked_direction_parent_copies_only_connections + part.blocked_direction_parent_copies_only_connections,
            total.blocked_direction_interface_not_exported_connections + part.blocked_direction_interface_not_exported_connections,
            total.blocked_direction_interface_not_captured_connections + part.blocked_direction_interface_not_captured_connections,
            total.blocked_direction_capture_configuration_unknown_connections + part.blocked_direction_capture_configuration_unknown_connections,
            total.blocked_counted_copy_connections + part.blocked_counted_copy_connections,
            total.unknown_packets + part.unknown_packets,
            total.unknown_measured_connections + part.unknown_measured_connections,
            total.unknown_partial_connections + part.unknown_partial_connections,
            total.unknown_unmeasured_connections + part.unknown_unmeasured_connections,
            total.unknown_capture_not_known_connections + part.unknown_capture_not_known_connections,
            total.unknown_direction_measured_connections + part.unknown_direction_measured_connections,
            total.unknown_direction_in_progress_connections + part.unknown_direction_in_progress_connections,
            total.unknown_direction_awaiting_export_connections + part.unknown_direction_awaiting_export_connections,
            total.unknown_direction_missing_connections + part.unknown_direction_missing_connections,
            total.unknown_direction_export_gap_connections + part.unknown_direction_export_gap_connections,
            total.unknown_direction_not_exported_when_refused_connections + part.unknown_direction_not_exported_when_refused_connections,
            total.unknown_direction_forwarding_export_unsettled_connections + part.unknown_direction_forwarding_export_unsettled_connections,
            total.unknown_direction_ingress_not_captured_connections + part.unknown_direction_ingress_not_captured_connections,
            total.unknown_direction_parent_copies_only_connections + part.unknown_direction_parent_copies_only_connections,
            total.unknown_direction_interface_not_exported_connections + part.unknown_direction_interface_not_exported_connections,
            total.unknown_direction_interface_not_captured_connections + part.unknown_direction_interface_not_captured_connections,
            total.unknown_direction_capture_configuration_unknown_connections + part.unknown_direction_capture_configuration_unknown_connections,
            total.unknown_counted_copy_connections + part.unknown_counted_copy_connections
        ) AS netflow_parts);
END
$netflow_parts_add$;

-- netflow_sum is the sum of values, field by field; over no row it is netflow_parts_zero().
CREATE OR REPLACE AGGREGATE netflow_sum(netflow_parts) (
    SFUNC = netflow_parts_add,
    STYPE = netflow_parts,
    COMBINEFUNC = netflow_parts_add,
    INITCOND = '(0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0)',
    PARALLEL = SAFE
);

-- ---------------------------------------------------------------------------
-- The `netflow` measure in the aggregate families (PS21 of the NetFlow join
-- amendment to specs/SPEC-backend-completion-plugins.md): every family and
-- period, and the purged part, carries beside its logged figures the measured
-- volume of the connections it counts -- connection_netflow -- and its
-- netflow_parts. THE TWO MEASURES ARE NEVER SUMMED: the logged columns are
-- unchanged, and no statement adds one to the other.
--
--   netflow_bytes   the measured bytes, split by the counting leg's action into
--                   netflow_allowed_bytes / _blocked_ / _unknown_, the CHECK
--                   pinning the split
--   netflow_parts   per action, the packets, the coverage classes and the
--                   direction states met (netflow_parts, above)
--
-- COUNTED AS THE LOGGED FIGURES ARE. A connection's figure counts in the slot of
-- its first leg, on its first leg's rows; a connection with a second leg also
-- counts it on that leg's rows -- under exit_leg_device in the volume family,
-- and in the rule family -- both directions when the second leg passed, and its
-- forward direction as not_exported_when_refused when it was refused (decided
-- question 3 of the amendment "the NetFlow join with every interface captured"):
-- the same packets, exactly as the second leg's logged bytes count there, and in
-- no total. A second leg's rows count its connection's class and states, as they
-- count the connection.
--
-- The columns are added in place, so a database the previous text was applied
-- to gains them, at zero, and the next refresh of a slot fills them.
-- ---------------------------------------------------------------------------
DO $netflow_columns$
DECLARE
    family TEXT;
    period TEXT;
    target TEXT;
    measure TEXT;
BEGIN
    FOREACH family IN ARRAY ARRAY['volume', 'rule', 'domain', 'peer', 'client', 'owner'] LOOP
        FOREACH period IN ARRAY ARRAY['1h', '24h', '7d', '30d'] LOOP
            target := family || '_volume_aggregate_' || period;
            IF family = 'volume' THEN
                target := 'volume_aggregate_' || period;
            END IF;
            FOREACH measure IN ARRAY ARRAY['netflow_bytes', 'netflow_allowed_bytes', 'netflow_blocked_bytes',
                                           'netflow_unknown_bytes'] LOOP
                EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS %I BIGINT NOT NULL DEFAULT 0 CHECK (%I >= 0)',
                               target, measure, measure);
            END LOOP;
            EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS netflow_parts netflow_parts NOT NULL '
                           'DEFAULT netflow_parts_zero()', target);
            IF NOT EXISTS (SELECT 1 FROM pg_constraint
                           WHERE conname = target || '_netflow_split'
                             AND conrelid = CAST(target AS regclass)) THEN
                EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (netflow_bytes = netflow_allowed_bytes '
                               '+ netflow_blocked_bytes + netflow_unknown_bytes)',
                               target, target || '_netflow_split');
            END IF;
        END LOOP;
    END LOOP;
    ALTER TABLE purged_flow_hour ADD COLUMN IF NOT EXISTS netflow_bytes BIGINT NOT NULL DEFAULT 0
        CHECK (netflow_bytes >= 0);
    ALTER TABLE purged_flow_hour ADD COLUMN IF NOT EXISTS netflow_parts netflow_parts NOT NULL
        DEFAULT netflow_parts_zero();
END
$netflow_columns$;

-- ---------------------------------------------------------------------------
-- unmatched_record_aggregate_* — the unmatched records (NJ4 rule 9 and NJ8 of the
-- amendment "the NetFlow join with every interface captured"): the flow records
-- that joined no leg and are no copy, per slot of their start and per ingress
-- interface, egress interface, IANA protocol number, IP version and reason.
-- ingress_interface and egress_interface are the mapped interface's identifier,
-- or, for an index that names no assigned interface, its role
-- (no_receiving_interface, local_delivery, unmapped, or interface for a device
-- no assignment names). "Unmatched record" is opnview's own term. Bytes, packets
-- and records only, added to no other family and to no total of any other: they
-- are no logged connection's. A copy is in no slot.
--
-- THE FAMILY IS ACCUMULATED, NOT RECOMPUTED. The join adds a record to its four
-- slots when it decides it unmatched, and takes it out again should a later
-- decision of its group join it, in the transaction that writes the decision;
-- so a slot keeps its records after the flow records themselves are purged.
-- updated_at is the last change. Purged by period_end_at, at the flows' horizon.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS unmatched_record_aggregate_1h (
    period_start_at   BIGINT NOT NULL CHECK (period_start_at >= 0 AND period_start_at < 4102444800),
    period_end_at     BIGINT NOT NULL CHECK (period_end_at >= 0 AND period_end_at < 4102444800),
    ingress_interface TEXT NOT NULL CHECK (length(ingress_interface) > 0),
    egress_interface  TEXT NOT NULL CHECK (length(egress_interface) > 0),
    protocol          BIGINT NOT NULL CHECK (protocol >= 0 AND protocol <= 255),
    ip_version        BIGINT NOT NULL CHECK (ip_version IN (4, 6)),
    reason            TEXT NOT NULL
                      CHECK (reason IN ('no_logged_leg', 'ambiguous', 'beyond_flow_horizon',
                                        'interface_index_unmapped', 'reverse_unconfirmed')),
    bytes             BIGINT NOT NULL CHECK (bytes >= 0),
    packets           BIGINT NOT NULL CHECK (packets >= 0),
    records           BIGINT NOT NULL CHECK (records >= 0),
    updated_at        BIGINT NOT NULL CHECK (updated_at >= 0 AND updated_at < 4102444800),
    CHECK (period_end_at > period_start_at),
    PRIMARY KEY (period_start_at, ingress_interface, egress_interface, protocol, ip_version, reason)
);

CREATE TABLE IF NOT EXISTS unmatched_record_aggregate_24h (
    LIKE unmatched_record_aggregate_1h INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS unmatched_record_aggregate_7d (
    LIKE unmatched_record_aggregate_1h INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS unmatched_record_aggregate_30d (
    LIKE unmatched_record_aggregate_1h INCLUDING ALL
);

CREATE INDEX IF NOT EXISTS idx_unmatched_record_aggregate_1h_end ON unmatched_record_aggregate_1h (period_end_at);
CREATE INDEX IF NOT EXISTS idx_unmatched_record_aggregate_24h_end ON unmatched_record_aggregate_24h (period_end_at);
CREATE INDEX IF NOT EXISTS idx_unmatched_record_aggregate_7d_end ON unmatched_record_aggregate_7d (period_end_at);
CREATE INDEX IF NOT EXISTS idx_unmatched_record_aggregate_30d_end ON unmatched_record_aggregate_30d (period_end_at);

-- ---------------------------------------------------------------------------
-- Retention purge — how far back the retention purge has purged: one row.
--
-- purged_before is the furthest horizon any purge has applied: every flow and
-- every lookup observed before it may be gone, its figures moved into the purged
-- part. It only moves forward, so lengthening the retention later does not make
-- a purged record storable again. The flow and lookup inserts read it inside the
-- insert statement and do not store a record older than it, so a purge that commits
-- in the middle of a collection pass is seen: the filter-log page and the
-- resolver's buffer keep offering records the purge already removed, and storing
-- one again would count it twice -- once in `flow`, once in the purged part of its
-- hour.
--
-- The term is the purge's own: docs/data-model.md, "Purge". Bounded: one row,
-- written by the purge, absent until the first purge with a finite retention.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS retention_purge (
    id            BIGINT PRIMARY KEY CHECK (id = 1),
    purged_before BIGINT NOT NULL CHECK (purged_before >= 0 AND purged_before < 4102444800),
    purged_at     BIGINT NOT NULL CHECK (purged_at >= 0 AND purged_at < 4102444800)
);

-- ---------------------------------------------------------------------------
-- Owner reassigned hour — an hour slot a change of ownership invalidated, waiting
-- for the next refresh of the aggregates.
--
-- Ownership is an attribute of the person, not of the flow, so assigning,
-- reassigning or unassigning a client, deleting a person or undoing that deletion
-- changes no flow's ingested_at, and the refresh contract's recognition rule
-- (docs/data-model.md, "Refresh contract", rules 3 and 4) cannot see it. The write
-- that changes ownership therefore records here, in its own transaction, every
-- kept 1 h slot holding the clients' traffic (read from the client family, so a
-- slot beyond the flow horizon is found as well as one inside it). The next refresh
-- reads these rows as forced instants -- rewriting the hour and the day, week and
-- month holding it -- and deletes exactly the rows it read once it has committed;
-- until then the widget API's freshness reports each such hour stale. A row is
-- written per hour per change, so a change made while a refresh runs is kept for
-- the next one.
--
-- Bounded: the rows live from a change of ownership to the next refresh, seconds
-- at most. Never purged: the refresh empties it.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS owner_reassigned_hour (
    id            BIGINT PRIMARY KEY,
    hour_start_at BIGINT NOT NULL CHECK (hour_start_at >= 0 AND hour_start_at < 4102444800
                                         AND hour_start_at % 3600 = 0),
    recorded_at   BIGINT NOT NULL CHECK (recorded_at >= 0 AND recorded_at < 4102444800)
);

CREATE INDEX IF NOT EXISTS idx_owner_reassigned_hour ON owner_reassigned_hour (hour_start_at);

-- ---------------------------------------------------------------------------
-- Address classification — the evidence each address was last placed from.
--
-- Classification places an address from on-link evidence (docs/data-model.md,
-- "Classification"). Redoing that over every row naming the address on every
-- pass would rewrite nothing and still read its whole history, so a pass is
-- INCREMENTAL: it places only the rows with an unplaced end, unless the evidence
-- for the address has changed since it was last placed, in which case every row
-- naming it is placed again. evidence is the fingerprint that decides it: the
-- kind of evidence that placed the address, the interface it placed it on, and
-- the strongest client identity at the address -- every input of the outcome
-- and nothing else. An address with no row here has never been placed, and is
-- placed in full.
--
-- The term "fingerprint" is the one the discovery pass already uses for the same
-- kind of record (internal/collect/discovery/discovery.go).
--
-- Growing: one row per address ever classified. Purged by classified_at; an
-- address whose row was purged is simply placed in full again.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS address_classification (
    address       TEXT PRIMARY KEY CHECK (length(address) > 0),
    evidence      TEXT NOT NULL CHECK (length(evidence) > 0),
    classified_at BIGINT NOT NULL CHECK (classified_at >= 0 AND classified_at < 4102444800)
);

CREATE INDEX IF NOT EXISTS idx_address_classification_classified_at
    ON address_classification (classified_at);

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
--     "The resolver window is not honoured at all");
--   * a download of a reference list opnview keeps on disk -- today the Public
--     Suffix List -- failed or delivered a file that does not parse. The
--     interval runs from the last successful refresh of the copy in use (or the
--     failure itself, when there was none) to the failure: it is how long the
--     copy in use has gone without being refreshed, and the copy stays in use.
--
-- reason is a CLOSED vocabulary and it is opnview's own, legitimately: these
-- are opnview's own detections, not values an endpoint reports, so enumerating
-- them invents nothing. detail carries the free text.
--
-- Growing: one row per detected gap. Purged by detected_at, because a gap
-- describing a window whose data would itself have been purged says nothing.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS collection_gap (
    id                BIGINT PRIMARY KEY,
    provider_id       BIGINT NOT NULL REFERENCES provider (id),
    interval_start_at BIGINT NOT NULL
                      CHECK (interval_start_at >= 0 AND interval_start_at < 4102444800),
    interval_end_at   BIGINT NOT NULL
                      CHECK (interval_end_at >= 0 AND interval_end_at < 4102444800),
    reason            TEXT NOT NULL
                      CHECK (reason IN ('digest_outside_returned_window',
                                        'eve_rotation_lost',
                                        'resolver_window_not_honoured',
                                        'download_failed',
                                        -- The push sources' (docs/plugins.md, "Push sources").
                                        'push_buffer_overflow',
                                        'push_store_failed',
                                        'push_listener_down',
                                        'push_script_failed',
                                        'push_not_alive',
                                        'netflow_sequence_gap',
                                        'netflow_template_missing')),
    detail            TEXT,
    detected_at       BIGINT NOT NULL CHECK (detected_at >= 0 AND detected_at < 4102444800),
    CHECK (interval_end_at >= interval_start_at)
);

CREATE INDEX IF NOT EXISTS idx_collection_gap_detected_at ON collection_gap (detected_at);
CREATE INDEX IF NOT EXISTS idx_collection_gap_interval
    ON collection_gap (provider_id, interval_start_at, interval_end_at);

-- ---------------------------------------------------------------------------
-- opnview's heartbeat, and the intervals during which opnview was not running
-- (SPEC-step-5b-widget-api.md, D2).
--
-- Source: none. While opnview is stopped, every source goes unread at once, and
-- nothing any endpoint returns says so: the next poll returns what the firewall
-- still holds and is silent about what rotated out meanwhile. So the service
-- records that it is alive, and on start it compares the last record with the
-- clock.
--
-- opnview_heartbeat is ONE ROW: the last instant the service was known to run,
-- and whether it stopped cleanly at that instant. A clean stop writes the stop
-- instant and sets clean_stop; a crash leaves the last periodic beat, which is
-- then the best bound opnview holds, at most one heartbeat interval early.
--
-- opnview_downtime IS A TABLE OF ITS OWN rather than a collection_gap reason.
-- collection_gap is per provider (provider_id NOT NULL, the foreign key every
-- "was this provider's window covered" search rests on), and opnview not running
-- is not one provider's gap but all of them: written there it would be one row
-- per provider per interval, or a NULL provider every reader of that table would
-- have to learn to skip. Here it is one row per interval, purged with the
-- retention by detected_at, as collection_gap is.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opnview_heartbeat (
    id         BIGINT PRIMARY KEY CHECK (id = 1),
    beat_at    BIGINT NOT NULL CHECK (beat_at >= 0 AND beat_at < 4102444800),
    clean_stop BIGINT NOT NULL DEFAULT 0 CHECK (clean_stop IN (0, 1))
);

CREATE TABLE IF NOT EXISTS opnview_downtime (
    id                BIGINT PRIMARY KEY,
    interval_start_at BIGINT NOT NULL
                      CHECK (interval_start_at >= 0 AND interval_start_at < 4102444800),
    interval_end_at   BIGINT NOT NULL
                      CHECK (interval_end_at >= 0 AND interval_end_at < 4102444800),
    -- 1 when the service recorded a clean stop at interval_start_at, 0 when the
    -- start is the last heartbeat of a process that ended without one.
    clean_stop        BIGINT NOT NULL CHECK (clean_stop IN (0, 1)),
    detected_at       BIGINT NOT NULL CHECK (detected_at >= 0 AND detected_at < 4102444800),
    CHECK (interval_end_at > interval_start_at)
);

CREATE INDEX IF NOT EXISTS idx_opnview_downtime_detected_at ON opnview_downtime (detected_at);
CREATE INDEX IF NOT EXISTS idx_opnview_downtime_interval
    ON opnview_downtime (interval_end_at, interval_start_at);

-- ---------------------------------------------------------------------------
-- push_listener -- when each push listener was last heard alive (PS7 of the push
-- amendment to specs/SPEC-backend-completion-plugins.md).
--
-- Source: opnview itself. A listener records the instant it is alive every
-- heartbeat and when it stops; when it opens again, the interval since that
-- instant -- opnview's own downtime between runs included -- is a gap of reason
-- push_listener_down of every declaration it feeds. One row per protocol and
-- transport, a handful in all; the row holds no address and no port, which are
-- the installation's start parameters.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS push_listener (
    protocol  TEXT NOT NULL CHECK (length(protocol) > 0),
    transport TEXT NOT NULL CHECK (transport IN ('udp', 'tcp')),
    alive_at  BIGINT NOT NULL CHECK (alive_at >= 0 AND alive_at < 4102444800),
    PRIMARY KEY (protocol, transport)
);

-- ---------------------------------------------------------------------------
-- provider_source_period -- which path delivered a provider's records at each
-- instant, when one provider of a core kind is fed both by polling and by a push
-- source that replaces it (PS9 of the push amendment).
--
-- Source: opnview itself. THE PERIODS NEVER OVERLAP: every instant of the
-- provider's history belongs to at most one, and a record is stored only if its
-- event instant falls in a period of the path that delivered it; a record of the
-- other path is superseded, counted and not stored. That is what keeps a
-- connection from counting twice in flow, the pairing and every aggregate family
-- when both paths deliver it: the two paths' record identities differ (a pushed
-- line carries a header written for the push target, survey S4), so nothing
-- could deduplicate them afterwards. to_at is NULL for the open period, and
-- from_at is 0 for the first.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_source_period (
    provider_id BIGINT NOT NULL REFERENCES provider (id),
    path        TEXT NOT NULL CHECK (path IN ('pull', 'push')),
    from_at     BIGINT NOT NULL CHECK (from_at >= 0 AND from_at < 4102444800),
    to_at       BIGINT CHECK (to_at >= 0 AND to_at < 4102444800),
    CHECK (to_at IS NULL OR to_at >= from_at),
    PRIMARY KEY (provider_id, from_at)
);

-- At most one open period per provider.
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_source_period_open
    ON provider_source_period (provider_id) WHERE to_at IS NULL;

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
-- subject_kind and subject_key together name what was measured. The five terms
-- opnview's own sampler uses, and a provider may declare another — HAProxy's
-- subject is a backend, and a UPS is neither an interface nor a pair:
--   'firewall'      the firewall itself. subject_key names the PART measured
--                   when the reading is of a part -- a temperature sensor, a
--                   mounted filesystem, a swap device -- and is the empty
--                   string when the reading is of the whole machine, as uptime
--                   and load are.
--                   It never carries the firewall's URL, which is configuration
--                   held elsewhere. The value is a label and nothing branches
--                   on its text.
--   'interface'     subject_key is the network DEVICE name, the same token
--                   interface_map keys by, so the reading joins to an interface
--                   without a foreign key that a discovery refresh could break
--   'interface_endpoint_pair'
--                   subject_key is the device the reading was taken on, the
--                   local address and the peer address, joined by spaces, in
--                   that order. traffic/top reports each local address with its
--                   peers on one interface, the peer's figures being INBOUND to
--                   the local address (survey, "What `traffic/top` measures,
--                   read from source for step 5"), so the order carries the
--                   direction and is never canonicalised away
--   'interface_endpoint'
--                   subject_key is the device and the local address, joined
--                   by a space: the record's own totals over all its peers.
--                   The local address's sending is reported ONLY as that
--                   total, so it is what a client's outbound figure comes from
--   'gateway'       subject_key is the gateway's name as System > Gateways
--                   reports it; the delay and loss dpinger measures
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
-- that list is in internal/store/vocab/rows.go as named constants; a provider's own
-- term is an ordinary value beside them.
--
-- Identity is (subject_kind, subject_key, measure, sampled_at, provider):
-- re-reading the same instant is idempotent, which is what a sampler restarting
-- inside one interval needs, and the provider is part of it because this kind
-- admits several concurrently active providers — two of them reading one subject
-- at one instant are two readings, not a collision. It is a unique INDEX over
-- coalesce(provider_id, -1) rather than a table constraint, because PostgreSQL
-- treats NULLs in a UNIQUE constraint as distinct and the firewall's own gauges carry a
-- null provider: a bare UNIQUE would have made every gauge non-idempotent. The
-- same coalesce wrapper the volume aggregates already use, for the same reason.
--
-- Growing: one row per reading. Purged by sampled_at.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS measurement_sample (
    id           BIGINT PRIMARY KEY,
    provider_id  BIGINT REFERENCES provider (id),
    subject_kind TEXT NOT NULL CHECK (length(subject_kind) > 0
                                      AND subject_kind = lower(subject_kind)
                                      AND strpos(subject_kind, ' ') = 0),
    subject_key  TEXT NOT NULL,
    measure      TEXT NOT NULL CHECK (length(measure) > 0
                                      AND measure = lower(measure)
                                      AND strpos(measure, ' ') = 0),
    unit         TEXT NOT NULL CHECK (length(unit) > 0
                                      AND unit = lower(unit)
                                      AND strpos(unit, ' ') = 0),
    value        DOUBLE PRECISION NOT NULL,
    sampled_at   BIGINT NOT NULL CHECK (sampled_at >= 0 AND sampled_at < 4102444800),
    -- A FINE SAMPLE carries the live cadence it was taken at; a BASELINE sample carries
    -- NULL (C3 of the backend-completion cycle, "Persistence"). A live pass runs while
    -- somebody looks at a live figure, and its samples are purged after
    -- live_sample_retention_seconds; the baseline pass runs at the measurement interval
    -- whether anyone looks or not, and its samples follow retention_seconds. When the
    -- baseline pass falls due during live mode it is served from the broker's mirror by
    -- the live pass's answer -- one round of calls for both -- and writes the same reading
    -- at the same instant: the conflict PROMOTES the fine row to a baseline one (this
    -- column set to NULL) rather than storing it twice, so the baseline series has no hole
    -- and no reading is ever counted twice (the maintainer's answer (a) to open question 1:
    -- promotion only, no summary rows).
    live_cadence_seconds INTEGER CHECK (live_cadence_seconds IS NULL OR live_cadence_seconds > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_measurement_sample_reading
    ON measurement_sample (subject_kind, subject_key, measure, sampled_at,
                           coalesce(provider_id, -1));

-- "This subject's readings of this measure over this period" is an index
-- search rather than a scan of a growing table.
CREATE INDEX IF NOT EXISTS idx_measurement_sample_subject
    ON measurement_sample (subject_kind, subject_key, measure, sampled_at, value);
CREATE INDEX IF NOT EXISTS idx_measurement_sample_sampled_at
    ON measurement_sample (sampled_at);
-- The fine-sample purge reads only the fine rows, which are few beside the baseline ones.
CREATE INDEX IF NOT EXISTS idx_measurement_sample_fine
    ON measurement_sample (sampled_at)
    WHERE live_cadence_seconds IS NOT NULL;

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
    id          BIGINT PRIMARY KEY,
    provider_id BIGINT NOT NULL REFERENCES provider (id),
    -- set_key names WHICH set, because one provider commonly reports several:
    -- one surveyed engine reports decisions and alerts, and a tunnel reports
    -- its peers. It is a
    -- term the provider declares, constrained in shape and not in membership,
    -- for the same reason measurement_sample's vocabularies are. Nothing here
    -- parses it or branches on its text.
    set_key     TEXT NOT NULL CHECK (length(set_key) > 0
                                     AND set_key = lower(set_key)
                                     AND strpos(set_key, ' ') = 0),
    -- captured_at is the instant at which the set was COMPLETE. It is the
    -- load-bearing column: without it a set is a bag of rows and a departure is
    -- undetectable.
    captured_at BIGINT NOT NULL CHECK (captured_at >= 0 AND captured_at < 4102444800),
    UNIQUE (provider_id, set_key, captured_at)
);

CREATE INDEX IF NOT EXISTS idx_state_snapshot_captured_at ON state_snapshot (captured_at);

-- "The latest snapshots of this set" is an index search rather than a scan of a
-- growing table, which is what the departure view walks.
CREATE INDEX IF NOT EXISTS idx_state_snapshot_set
    ON state_snapshot (provider_id, set_key, captured_at, id);

CREATE TABLE IF NOT EXISTS state_item (
    id             BIGINT PRIMARY KEY,
    snapshot_id    BIGINT NOT NULL REFERENCES state_snapshot (id) ON DELETE CASCADE,
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
    attributes     TEXT CHECK (attributes IS NULL OR attributes IS JSON OBJECT),
    -- valid_until_at is the end of the thing's own validity when the provider
    -- states one — a surveyed ban decision carries a TTL and nothing else. It is NOT
    -- how a departure is detected: an item is gone when the next complete
    -- snapshot omits it, which is the only signal every one of these sources
    -- gives. An expiry that has passed while the item is still reported is the
    -- provider's business, and opnview reports both rather than choosing.
    valid_until_at BIGINT CHECK (valid_until_at IS NULL
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
CREATE OR REPLACE VIEW state_item_departure AS
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
-- NetFlow: the flow records an exporter sends, its capture configuration, and
-- the volume they measure for the logged connections (PS17 of the NetFlow join
-- amendment to specs/SPEC-backend-completion-plugins.md, as the amendment "the
-- NetFlow join with every interface captured" replaces its join, NJ2 to NJ8;
-- docs/data-model.md, "NetFlow volumes").
--
-- THE TERMS. A flow_record is RFC 3954's and RFC 7011's Flow Record, and is not
-- a `flow`, which is one filter-log record. forward and reverse are RFC 5103's.
-- RFC 7011's Observation Point is neither exported by OPNsense nor assigned by
-- opnview. "Measured volume", the `netflow` measure, a translated copy, a parent
-- copy, a counted copy and the index roles are opnview's own: docs/data-model.md,
-- *Vocabulary*, records each with its reason.
--
-- EVERY RECORD IS JOINED FROM ITS OWN INDEXES. A record names the interface it
-- was received on and the interface its route chose (ingressInterface and
-- egressInterface, survey S6), mapped at reception; the join reads them and the
-- legs logged there (NJ4). Which interfaces export what is read from the capture
-- configuration, never inferred from the records.
-- ---------------------------------------------------------------------------

-- canonical_address — an address as one canonical text, for comparing an address
-- the filter log wrote (verbatim, flow.src_address) with one an exporter sent,
-- by value and never by text. NULL for a text that is not an address, tested with
-- pg_input_is_valid rather than caught, so no insert into `flow` opens a
-- subtransaction for it. IMMUTABLE -- an address's canonical text depends on
-- nothing but the address -- so the join's expression index below may use it.
CREATE OR REPLACE FUNCTION canonical_address(address TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
    SELECT CASE WHEN pg_input_is_valid(address, 'inet') THEN host(CAST(address AS inet)) END
$$;

-- The join's candidate search (NJ4): the legs logged on the devices a group's
-- records name whose two addresses are the records', in either orientation, at
-- or before an instant -- one index search per device. Risk 7 of the amendment
-- names its cost; the pre-deployment sweep plans it at both seeds.
CREATE INDEX IF NOT EXISTS idx_flow_join ON flow
    (interface_device, canonical_address(src_address), canonical_address(dst_address), observed_at);

-- ---------------------------------------------------------------------------
-- capture_configuration — the capture configurations a flow_record provider
-- read, as INTERVALS BOUNDED BY READ INSTANTS (PS18, kept by NJ6).
--
-- Source: the provider's polled read (docs/plugins.md, `flow_record`): what the
-- exporter's own configuration says it captures, through the broker. Which
-- endpoints a provider reads is its plugin's, never this schema's (PS-AC47).
--
-- One row per run of consecutive reads that found the same configuration: a read
-- equal to the latest row's extends its last_read_at, any other read opens a row.
-- A configuration is in force over [first_read_at, last_read_at] of its row. The
-- interval between two rows -- the last read showing one configuration and the
-- first showing another -- is capture_configuration_unknown, and so is every
-- unreadable row and every instant before the first. After the newest read, the
-- newest configuration is taken as in force until a read says otherwise; a record
-- once stored is never re-joined when one does.
--
-- fingerprint is the configuration as one canonical text, which is what "the
-- same configuration" compares; NULL exactly when the read failed, and outcome
-- then names what the broker answered (source_availability's closed codes).
-- Purged by last_read_at, the newest row of each provider always kept.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS capture_configuration (
    id                       BIGINT PRIMARY KEY,
    provider_id              BIGINT NOT NULL REFERENCES provider (id),
    first_read_at            BIGINT NOT NULL
                             CHECK (first_read_at >= 0 AND first_read_at < 4102444800),
    last_read_at             BIGINT NOT NULL
                             CHECK (last_read_at >= 0 AND last_read_at < 4102444800),
    fingerprint              TEXT,
    outcome                  TEXT,
    detail                   TEXT,
    enabled                  BIGINT CHECK (enabled IS NULL OR enabled IN (0, 1)),
    export_version           TEXT,
    active_timeout_seconds   BIGINT CHECK (active_timeout_seconds IS NULL OR active_timeout_seconds > 0),
    inactive_timeout_seconds BIGINT CHECK (inactive_timeout_seconds IS NULL OR inactive_timeout_seconds > 0),
    CHECK (last_read_at >= first_read_at),
    CHECK ((fingerprint IS NULL) = (enabled IS NULL)),
    CHECK ((fingerprint IS NULL) = (outcome IS NOT NULL)),
    UNIQUE (provider_id, first_read_at)
);

-- capture_interface — one interface of a readable configuration (NJ2, "the
-- capture-configuration record"):
--   identifier          the interface's OPNsense identifier, as discovery names it
--   ingress, egress     whether the configuration captures its received and its
--                       sent packets
--   exported            whether the exporter actually exports what is configured
--                       for it, for each captured direction: 'yes', 'no' (and
--                       not_exported_reason names why, for OPNsense
--                       no_export_point), or 'unknown' when the provider could not
--                       establish it
-- What the plugin read to fill exported is its own (NJ9): the core never names it.
CREATE TABLE IF NOT EXISTS capture_interface (
    capture_configuration_id BIGINT NOT NULL REFERENCES capture_configuration (id) ON DELETE CASCADE,
    identifier               TEXT NOT NULL CHECK (length(identifier) > 0),
    ingress                  BIGINT NOT NULL CHECK (ingress IN (0, 1)),
    egress                   BIGINT NOT NULL CHECK (egress IN (0, 1)),
    exported                 TEXT NOT NULL CHECK (exported IN ('yes', 'no', 'unknown')),
    not_exported_reason      TEXT CHECK (not_exported_reason IS NULL OR length(not_exported_reason) > 0),
    CHECK ((exported = 'no') = (not_exported_reason IS NOT NULL)),
    PRIMARY KEY (capture_configuration_id, identifier)
);

-- capture_interface_parent_of — an interface's parent_of (NJ2): the interfaces
-- whose received packets this interface's exporter also exports with this
-- interface as ingressInterface, one row each. For OPNsense, a VLAN's parent
-- interface (S6c); the core never names the mechanism.
CREATE TABLE IF NOT EXISTS capture_interface_parent_of (
    capture_configuration_id BIGINT NOT NULL,
    identifier               TEXT NOT NULL,
    of_identifier            TEXT NOT NULL CHECK (length(of_identifier) > 0),
    PRIMARY KEY (capture_configuration_id, identifier, of_identifier),
    FOREIGN KEY (capture_configuration_id, identifier)
        REFERENCES capture_interface (capture_configuration_id, identifier) ON DELETE CASCADE
);

-- ---------------------------------------------------------------------------
-- flow_record — one flow record as its exporter sent it (PS17), a hypertable
-- chunked by day on its start, under P4's rule: its one unique key holds the
-- start instant, so the hypertable keeps it.
--
-- Source: the push entry of the active flow_record provider, mapped from IANA
-- Information Elements with no script. Rows of a provider that is not active are
-- not stored.
--
-- identity is the core's: the SHA-256 of the exporter, the observation domain,
-- the SHA-256 of the datagram and the record's position in it (internal/plugin,
-- pushkind.go). A datagram delivered twice stores its records once; two records
-- identical in every field keep two rows, and are never deduplicated by content
-- (NJ4 rule 7). Two datagrams identical octet for octet are one delivery: the one
-- case the identity cannot separate.
--
-- start_at and end_at are the first and last packets' instants in whole seconds,
-- and *_millisecond the rest, at the precision exported (OPNsense's is whole
-- seconds, survey S10). ingress_interface and egress_interface are the raw
-- indexes; protocol is the IANA number.
--
-- THE CORE-WRITTEN COLUMNS, set at reception and never by a provider (NJ2, NJ3):
--   ingress_role, egress_role  what the index names, from the index map in force
--                      at reception: 'interface', 'no_receiving_interface'
--                      (index 0, S6a), 'local_delivery' (the loopback's index,
--                      S6b) or 'unmapped' (an index the map does not hold, or no
--                      index at all)
--   ingress_identifier, ingress_device, egress_identifier, egress_device  the
--                      mapped interface of an 'interface' role: its device always,
--                      its identifier when an assignment names it. Fixed at
--                      reception and never re-mapped
--   loss_detectable    1 when the configuration in force at the start bounded the
--                      exporter's sequence counters (NJ7), so a loss would have
--                      been recorded as a gap
--   tuple_key          the record's two ends, unordered, with its version and
--                      protocol: what the join groups records by
-- And the join's (NJ4), decided from stored rows only:
--   join_state         'pending' until the filter log has been read past the
--                      start plus the tolerance, or while a reverse join awaits
--                      its forward record (NJ4 rule 4); then 'joined', 'copy' or
--                      'unmatched'
--   joined_flow_id     the leg it joined, a first or single leg, never a second
--   copy_kind          translated_copy or parent_copy, exactly on a copy
--   copy_flow_id       on a translated copy matched by a second leg's logged
--                      tuple, that second leg: what NJ5's sole-copy rule counts it
--                      under
--   orientation        forward or reverse, to joined_flow_id or copy_flow_id
--   counted_copy       1 on a translated copy NJ5's sole-copy rule counts
--   unmatched_reason   no_logged_leg, ambiguous, beyond_flow_horizon,
--                      interface_index_unmapped or reverse_unconfirmed
-- joined_flow_id and copy_flow_id carry no foreign key, as flow.paired_flow_id
-- does not: the retention purge can remove the leg and keep the record.
-- Purged by start_at, at retention_flow_record_seconds (0 follows
-- retention_seconds).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS flow_record (
    provider_id        BIGINT NOT NULL REFERENCES provider (id),
    identity           TEXT NOT NULL CHECK (length(identity) = 64),
    start_at           BIGINT NOT NULL CHECK (start_at >= 0 AND start_at < 4102444800),
    start_millisecond  BIGINT NOT NULL CHECK (start_millisecond >= 0 AND start_millisecond < 1000),
    end_at             BIGINT NOT NULL CHECK (end_at >= 0 AND end_at < 4102444800),
    end_millisecond    BIGINT NOT NULL CHECK (end_millisecond >= 0 AND end_millisecond < 1000),
    received_at        BIGINT NOT NULL CHECK (received_at >= 0 AND received_at < 4102444800),
    exporter_address   TEXT NOT NULL,
    observation_domain BIGINT NOT NULL CHECK (observation_domain >= 0),
    ingress_interface  BIGINT CHECK (ingress_interface IS NULL OR ingress_interface >= 0),
    egress_interface   BIGINT CHECK (egress_interface IS NULL OR egress_interface >= 0),
    ip_version         BIGINT NOT NULL CHECK (ip_version IN (4, 6)),
    protocol           BIGINT NOT NULL CHECK (protocol >= 0 AND protocol <= 255),
    src_address        TEXT NOT NULL,
    dst_address        TEXT NOT NULL,
    src_port           BIGINT CHECK (src_port IS NULL OR (src_port >= 0 AND src_port <= 65535)),
    dst_port           BIGINT CHECK (dst_port IS NULL OR (dst_port >= 0 AND dst_port <= 65535)),
    tcp_flags          BIGINT CHECK (tcp_flags IS NULL OR tcp_flags >= 0),
    octets             BIGINT NOT NULL CHECK (octets >= 0),
    packets            BIGINT NOT NULL CHECK (packets >= 0),
    ingress_role       TEXT NOT NULL CHECK (ingress_role IN ('interface', 'no_receiving_interface',
                                                             'local_delivery', 'unmapped')),
    ingress_identifier TEXT CHECK (ingress_identifier IS NULL OR length(ingress_identifier) > 0),
    ingress_device     TEXT CHECK (ingress_device IS NULL OR length(ingress_device) > 0),
    egress_role        TEXT NOT NULL CHECK (egress_role IN ('interface', 'no_receiving_interface',
                                                            'local_delivery', 'unmapped')),
    egress_identifier  TEXT CHECK (egress_identifier IS NULL OR length(egress_identifier) > 0),
    egress_device      TEXT CHECK (egress_device IS NULL OR length(egress_device) > 0),
    loss_detectable    BIGINT NOT NULL CHECK (loss_detectable IN (0, 1)),
    tuple_key          TEXT NOT NULL,
    join_state         TEXT NOT NULL CHECK (join_state IN ('pending', 'joined', 'copy', 'unmatched')),
    joined_flow_id     BIGINT,
    copy_kind          TEXT CHECK (copy_kind IS NULL OR copy_kind IN ('translated_copy', 'parent_copy')),
    copy_flow_id       BIGINT,
    orientation        TEXT CHECK (orientation IS NULL OR orientation IN ('forward', 'reverse')),
    counted_copy       BIGINT NOT NULL DEFAULT 0 CHECK (counted_copy IN (0, 1)),
    unmatched_reason   TEXT CHECK (unmatched_reason IS NULL
                                   OR unmatched_reason IN ('no_logged_leg', 'ambiguous', 'beyond_flow_horizon',
                                                           'interface_index_unmapped', 'reverse_unconfirmed')),
    decided_at         BIGINT CHECK (decided_at IS NULL OR (decided_at >= 0 AND decided_at < 4102444800)),
    CHECK (end_at > start_at OR (end_at = start_at AND end_millisecond >= start_millisecond)),
    CHECK ((ingress_role = 'interface') = (ingress_device IS NOT NULL)),
    CHECK (ingress_identifier IS NULL OR ingress_role = 'interface'),
    CHECK ((egress_role = 'interface') = (egress_device IS NOT NULL)),
    CHECK (egress_identifier IS NULL OR egress_role = 'interface'),
    CHECK ((join_state = 'joined') = (joined_flow_id IS NOT NULL)),
    CHECK ((join_state = 'copy') = (copy_kind IS NOT NULL)),
    CHECK (copy_flow_id IS NULL OR copy_kind = 'translated_copy'),
    CHECK ((orientation IS NOT NULL) = (joined_flow_id IS NOT NULL OR copy_flow_id IS NOT NULL)),
    CHECK (counted_copy = 0 OR copy_flow_id IS NOT NULL),
    CHECK ((join_state = 'unmatched') = (unmatched_reason IS NOT NULL)),
    CHECK ((join_state IN ('joined', 'copy', 'unmatched')) = (decided_at IS NOT NULL)),
    UNIQUE (identity, start_at)
);

SELECT create_hypertable('flow_record', by_range('start_at', 86400), if_not_exists => true);

-- The join's two searches: the undecided records of a provider, oldest first,
-- and the records of one group -- one unordered tuple. No device leads the group
-- key: a group's records name their own interfaces (NJ2).
CREATE INDEX IF NOT EXISTS idx_flow_record_pending
    ON flow_record (provider_id, start_at) WHERE join_state = 'pending';
CREATE INDEX IF NOT EXISTS idx_flow_record_group
    ON flow_record (tuple_key, start_at);
-- A connection's measured volume reads the records joined to its first leg, and
-- the translated copies of its second leg.
CREATE INDEX IF NOT EXISTS idx_flow_record_joined
    ON flow_record (joined_flow_id) WHERE joined_flow_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_flow_record_copy
    ON flow_record (copy_flow_id) WHERE copy_flow_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- connection_measured_volume — a logged connection's volume as the exporter
-- counted it, per direction, each direction once (NJ5).
--
-- A connection is a first leg with its second leg, or a single leg; it is named
-- by connection_flow_id, its first (or single) leg. forward and reverse are the
-- directions the first leg logged. A row exists only for a connection that
-- started under a readable, enabled capture configuration; a connection that
-- started under none has no row -- its coverage class is capture_not_known.
--
--   state         measured, in_progress, awaiting_export, missing or export_gap
--                 where exactly one exporter counts the direction, or the
--                 sole-copy rule applies; forwarding_export_unsettled where two
--                 do; otherwise the first cause that applies:
--                 not_exported_when_refused, ingress_not_captured,
--                 parent_copies_only, interface_not_exported,
--                 interface_not_captured, capture_configuration_unknown
--   bytes, packets, records  the sum of the records joined to the first leg in
--                 that direction, or of the counted copies; 0 with no record and
--                 in every state that is not counted
--   counted_copy  1 when the figure is the translated copies' (NJ5's sole-copy
--                 rule)
--   final         1 once nothing can change it any more
-- The measure identity is `netflow`: packet_bytes and every logged figure are
-- unchanged, and nothing anywhere sums the two. Purged with its connection.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS connection_measured_volume (
    connection_flow_id  BIGINT NOT NULL,
    direction           TEXT NOT NULL CHECK (direction IN ('forward', 'reverse')),
    connection_start_at BIGINT NOT NULL
                        CHECK (connection_start_at >= 0 AND connection_start_at < 4102444800),
    state               TEXT NOT NULL
                        CHECK (state IN ('measured', 'in_progress', 'awaiting_export', 'missing', 'export_gap',
                                         'not_exported_when_refused', 'forwarding_export_unsettled',
                                         'ingress_not_captured', 'parent_copies_only', 'interface_not_exported',
                                         'interface_not_captured', 'capture_configuration_unknown')),
    bytes               BIGINT NOT NULL CHECK (bytes >= 0),
    packets             BIGINT NOT NULL CHECK (packets >= 0),
    records             BIGINT NOT NULL CHECK (records >= 0),
    counted_copy        BIGINT NOT NULL CHECK (counted_copy IN (0, 1)),
    final               BIGINT NOT NULL CHECK (final IN (0, 1)),
    -- due_at is the read-through instant from which the state may change by time alone -- an
    -- in-progress direction ending, an awaited one going missing -- and NULL once final: an open row is
    -- computed again when it falls due, or when a record, a leg or a gap it reads changes.
    due_at              BIGINT CHECK (due_at IS NULL OR (due_at >= 0 AND due_at < 4102444800)),
    computed_at         BIGINT NOT NULL CHECK (computed_at >= 0 AND computed_at < 4102444800),
    CHECK ((final = 1) = (due_at IS NULL)),
    CHECK (records > 0 OR (bytes = 0 AND packets = 0)),
    CHECK (state NOT IN ('measured', 'in_progress') OR records > 0),
    CHECK (state IN ('measured', 'in_progress', 'export_gap') OR records = 0),
    CHECK (counted_copy = 0 OR state IN ('measured', 'in_progress', 'awaiting_export', 'missing', 'export_gap')),
    PRIMARY KEY (connection_flow_id, direction)
);

CREATE INDEX IF NOT EXISTS idx_connection_measured_volume_start
    ON connection_measured_volume (connection_start_at);
CREATE INDEX IF NOT EXISTS idx_connection_measured_volume_due
    ON connection_measured_volume (due_at) WHERE final = 0;

-- ---------------------------------------------------------------------------
-- connection_netflow — THE ONE PLACE a logged flow's `netflow` figures are read
-- from connection_measured_volume (the maintainer's decision of 11 October 2026,
-- "shared fragment"). The aggregate refresh, the retention purge and every
-- `_netflow` twin -- the store's, the core widgets' and the widget plugins' --
-- join it to the flow they count, and none of them reads
-- connection_measured_volume itself:
--
--     LEFT JOIN LATERAL connection_netflow(f.id, f.pair_outcome, f.paired_flow_id,
--                                          f.action) AS n ON true
--
-- For the flow given -- its id, pair outcome, paired flow and action -- it answers
-- one row:
--   bytes  the measured bytes the flow's row counts: its connection's, both
--          directions. On a second leg the same, the connection's figure being
--          the same packets (decided question 3), except that the forward
--          direction of a refused second leg counts nothing and is counted as
--          not_exported_when_refused: pf dropped it before it could leave
--   parts  netflow_parts, with the flow's connection placed in the part of the
--          flow's action: its packets (counted as its bytes are), its coverage
--          class, the direction states it met and whether a direction was
--          counted from translated copies. A second leg's row counts its
--          connection's class and states, as it counts the connection.
-- A connection is the flow itself, or for a second leg the first leg it is
-- paired with. A connection with no row is capture_not_known: it started under
-- no readable, enabled capture configuration.
--
-- A plain SQL function returning a set, STABLE and not STRICT, so PostgreSQL
-- inlines it into the statement calling it: the plan is the one the statement
-- would have written out by hand.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION connection_netflow(leg_id BIGINT, leg_pair_outcome TEXT, leg_paired_flow_id BIGINT,
                                              leg_action TEXT)
RETURNS TABLE (bytes BIGINT, parts netflow_parts)
LANGUAGE sql STABLE PARALLEL SAFE
AS $connection_netflow$
    SELECT m.bytes,
           -- A connection with no row -- the common case where NetFlow is not configured -- is one
           -- constant per action: capture_not_known in the part of the flow's action, and nothing else.
           CASE WHEN m.rows = 0 THEN
               CASE WHEN leg_action = 'pass' THEN
                   CAST('(0,0,0,0,1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0)' AS netflow_parts)
                    WHEN leg_action IN ('block', 'reject') THEN
                   CAST('(0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0)' AS netflow_parts)
                    WHEN leg_action = 'unknown' THEN
                   CAST('(0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,0,0,0,0,0,0,0,0,0,0,0,0,0)' AS netflow_parts)
                    ELSE netflow_parts_zero() END
           ELSE CAST(ROW(
                m.allowed * m.packets,
                m.allowed * m.class_measured,
                m.allowed * m.class_partial,
                m.allowed * m.class_unmeasured,
                m.allowed * m.class_capture_not_known,
                m.allowed * m.state_measured,
                m.allowed * m.state_in_progress,
                m.allowed * m.state_awaiting_export,
                m.allowed * m.state_missing,
                m.allowed * m.state_export_gap,
                m.allowed * m.state_not_exported_when_refused,
                m.allowed * m.state_forwarding_export_unsettled,
                m.allowed * m.state_ingress_not_captured,
                m.allowed * m.state_parent_copies_only,
                m.allowed * m.state_interface_not_exported,
                m.allowed * m.state_interface_not_captured,
                m.allowed * m.state_capture_configuration_unknown,
                m.allowed * m.counted_copy,
                m.blocked * m.packets,
                m.blocked * m.class_measured,
                m.blocked * m.class_partial,
                m.blocked * m.class_unmeasured,
                m.blocked * m.class_capture_not_known,
                m.blocked * m.state_measured,
                m.blocked * m.state_in_progress,
                m.blocked * m.state_awaiting_export,
                m.blocked * m.state_missing,
                m.blocked * m.state_export_gap,
                m.blocked * m.state_not_exported_when_refused,
                m.blocked * m.state_forwarding_export_unsettled,
                m.blocked * m.state_ingress_not_captured,
                m.blocked * m.state_parent_copies_only,
                m.blocked * m.state_interface_not_exported,
                m.blocked * m.state_interface_not_captured,
                m.blocked * m.state_capture_configuration_unknown,
                m.blocked * m.counted_copy,
                m.unknown * m.packets,
                m.unknown * m.class_measured,
                m.unknown * m.class_partial,
                m.unknown * m.class_unmeasured,
                m.unknown * m.class_capture_not_known,
                m.unknown * m.state_measured,
                m.unknown * m.state_in_progress,
                m.unknown * m.state_awaiting_export,
                m.unknown * m.state_missing,
                m.unknown * m.state_export_gap,
                m.unknown * m.state_not_exported_when_refused,
                m.unknown * m.state_forwarding_export_unsettled,
                m.unknown * m.state_ingress_not_captured,
                m.unknown * m.state_parent_copies_only,
                m.unknown * m.state_interface_not_exported,
                m.unknown * m.state_interface_not_captured,
                m.unknown * m.state_capture_configuration_unknown,
                m.unknown * m.counted_copy
           ) AS netflow_parts) END
    FROM (
        SELECT CAST(coalesce(sum(e.bytes), 0) AS BIGINT) AS bytes,
               count(*) AS rows,
               CAST(coalesce(sum(e.packets), 0) AS BIGINT) AS packets,
               CASE WHEN leg_action = 'pass' THEN 1 ELSE 0 END AS allowed,
               CASE WHEN leg_action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked,
               CASE WHEN leg_action = 'unknown' THEN 1 ELSE 0 END AS unknown,
               CASE WHEN count(*) = 0 THEN 1 ELSE 0 END AS class_capture_not_known,
               CASE WHEN count(*) > 0 AND count(*) FILTER (WHERE e.state = 'measured') = 2
                    THEN 1 ELSE 0 END AS class_measured,
               CASE WHEN count(*) > 0 AND count(*) FILTER (WHERE e.state = 'measured') <> 2
                         AND count(*) FILTER (WHERE e.records > 0) = 0
                    THEN 1 ELSE 0 END AS class_unmeasured,
               CASE WHEN count(*) > 0 AND count(*) FILTER (WHERE e.state = 'measured') <> 2
                         AND count(*) FILTER (WHERE e.records > 0) > 0
                    THEN 1 ELSE 0 END AS class_partial,
               CASE WHEN count(*) FILTER (WHERE e.state = 'measured') > 0 THEN 1 ELSE 0 END AS state_measured,
               CASE WHEN count(*) FILTER (WHERE e.state = 'in_progress') > 0 THEN 1 ELSE 0 END AS state_in_progress,
               CASE WHEN count(*) FILTER (WHERE e.state = 'awaiting_export') > 0 THEN 1 ELSE 0 END AS state_awaiting_export,
               CASE WHEN count(*) FILTER (WHERE e.state = 'missing') > 0 THEN 1 ELSE 0 END AS state_missing,
               CASE WHEN count(*) FILTER (WHERE e.state = 'export_gap') > 0 THEN 1 ELSE 0 END AS state_export_gap,
               CASE WHEN count(*) FILTER (WHERE e.state = 'not_exported_when_refused') > 0 THEN 1 ELSE 0 END AS state_not_exported_when_refused,
               CASE WHEN count(*) FILTER (WHERE e.state = 'forwarding_export_unsettled') > 0 THEN 1 ELSE 0 END AS state_forwarding_export_unsettled,
               CASE WHEN count(*) FILTER (WHERE e.state = 'ingress_not_captured') > 0 THEN 1 ELSE 0 END AS state_ingress_not_captured,
               CASE WHEN count(*) FILTER (WHERE e.state = 'parent_copies_only') > 0 THEN 1 ELSE 0 END AS state_parent_copies_only,
               CASE WHEN count(*) FILTER (WHERE e.state = 'interface_not_exported') > 0 THEN 1 ELSE 0 END AS state_interface_not_exported,
               CASE WHEN count(*) FILTER (WHERE e.state = 'interface_not_captured') > 0 THEN 1 ELSE 0 END AS state_interface_not_captured,
               CASE WHEN count(*) FILTER (WHERE e.state = 'capture_configuration_unknown') > 0 THEN 1 ELSE 0 END AS state_capture_configuration_unknown,
               CASE WHEN count(*) FILTER (WHERE e.counted_copy = 1) > 0 THEN 1 ELSE 0 END AS counted_copy
        FROM (
            SELECT CASE WHEN r.refused_exit AND v.direction = 'forward' THEN 'not_exported_when_refused'
                        ELSE v.state END AS state,
                   CASE WHEN r.refused_exit AND v.direction = 'forward' THEN 0 ELSE v.bytes END AS bytes,
                   CASE WHEN r.refused_exit AND v.direction = 'forward' THEN 0 ELSE v.packets END AS packets,
                   CASE WHEN r.refused_exit AND v.direction = 'forward' THEN 0 ELSE v.records END AS records,
                   CASE WHEN r.refused_exit AND v.direction = 'forward' THEN 0 ELSE v.counted_copy END AS counted_copy
            FROM connection_measured_volume AS v
            CROSS JOIN (SELECT leg_pair_outcome IS NOT DISTINCT FROM 'second_leg'
                               AND leg_action IN ('block', 'reject') AS refused_exit) AS r
            WHERE v.connection_flow_id = CASE WHEN leg_pair_outcome = 'second_leg' THEN leg_paired_flow_id
                                              ELSE leg_id END
        ) AS e
    ) AS m
$connection_netflow$;

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
-- neither dns_resolution nor domain_attribution. It changes what is SHOWN and
-- never what is stored: attributions, the domain family and the resolver's
-- records in resource_record_observation are written in both modes, and the API
-- withholds them (decision 7 of specs/SPEC-resolver-cache-attribution.md, as 5A D4
-- decided for attributions).
--
-- attribution_max_delay_seconds: how long before a flow a resolver lookup may
-- have been made and still name it (docs/data-model.md, "Site-name
-- attribution"). 5 is the maintainer's default; it is a positive whole number,
-- and internal/config refuses anything else.
--
-- attribution_max_cache_answer_delay_seconds: how long before a flow, at most, a
-- lookup may have been made and still name it through the resolver's cache, when
-- the cache shows the flow's destination among the lookup's answers over the whole
-- interval (decision 2 of specs/SPEC-resolver-cache-attribution.md; docs/data-model.md,
-- "Site-name attribution"). The evidence's own validity is the window and this is its
-- cap. 3600 is the maintainer's default; it is a positive whole number, and
-- internal/config refuses anything else.
--
-- leg_pairing_window_seconds: how far apart, in seconds, the two records of one
-- connection may have been logged and still be paired (decision 4 of the step-5A
-- live corrections; docs/data-model.md, "Two legs of one connection"). 1 is the
-- maintainer's default; it is a whole number of seconds, 0 or more, and
-- internal/config refuses anything else.
--
-- lease_backend_failure_pass_limit: how many lease passes in a row one DHCP
-- backend may fail before a pass that read every other backend counts as complete,
-- so the unresolved host-name lookups are examined without it (defect L1 of
-- specs/SPEC-test-budget-and-two-live-defects.md; docs/data-model.md, "Host names
-- logged as the client"). 3 is the default, a quarter of an hour at the default
-- lease interval; it is a whole number of passes, 1 or more, and internal/config
-- refuses anything else.
--
-- heartbeat_interval_seconds and downtime_threshold_seconds: how often the
-- running service records that it is alive, and how long it must have been gone
-- for the interval to be recorded in opnview_downtime (SPEC-step-5b-widget-
-- api.md, D2). 60 and 300 are the defaults, justified in internal/config beside
-- DefaultHeartbeatIntervalSeconds; both are positive whole numbers of seconds,
-- the threshold longer than the interval, and internal/config refuses anything
-- else.
--
-- live_interest_seconds and live_sample_retention_seconds: how long a request for a
-- live figure keeps the live passes running, and how long their fine samples are
-- kept (C3 of the backend-completion cycle; docs/widget-api.md, "Live figures").
-- 30 and 3600 are the defaults, justified in internal/config/liverate.go; each is a
-- whole number of seconds within the bounds written there, and internal/config
-- refuses anything else, naming the key.
--
-- netflow_join_tolerance_seconds: how far after a leg was logged a flow record may
-- start and still join it (PS19 rule 3 of the NetFlow join amendment). 3 is
-- opnview's default, justified in internal/config/netflow.go from the three
-- one-second resolutions involved; a positive whole number, and internal/config
-- refuses anything else. LV-NJ confirms it.
--
-- retention_flow_record_seconds: how long a flow record is kept (PS17). 0, the
-- default, follows retention_seconds: the flow records are kept as long as the
-- filter log's flows they join.
-- The row-id triggers, one per table numbered by id (see install_assign_id).
SELECT install_assign_id(t::regclass) FROM unnest(ARRAY[
    'account',
    'provider',
    'interface',
    'interface_address',
    'interface_network',
    'owner',
    'rule',
    'client',
    'dhcp_lease',
    'flow',
    'blocklist',
    'dns_resolution',
    'resource_record_observation',
    'security_event',
    'eve_ingest_cursor',
    'pair_volume_observation',
    'volume_aggregate_1h',
    'volume_aggregate_24h',
    'volume_aggregate_7d',
    'volume_aggregate_30d',
    'owner_volume_aggregate_1h',
    'owner_volume_aggregate_24h',
    'owner_volume_aggregate_7d',
    'owner_volume_aggregate_30d',
    'client_volume_aggregate_1h',
    'client_volume_aggregate_24h',
    'client_volume_aggregate_7d',
    'client_volume_aggregate_30d',
    'peer_volume_aggregate_1h',
    'peer_volume_aggregate_24h',
    'peer_volume_aggregate_7d',
    'peer_volume_aggregate_30d',
    'domain_volume_aggregate_1h',
    'domain_volume_aggregate_24h',
    'domain_volume_aggregate_7d',
    'domain_volume_aggregate_30d',
    'rule_volume_aggregate_1h',
    'rule_volume_aggregate_24h',
    'rule_volume_aggregate_7d',
    'rule_volume_aggregate_30d',
    'purged_flow_hour',
    'collection_gap',
    'opnview_downtime',
    'measurement_sample',
    'state_snapshot',
    'state_item',
    'owner_reassigned_hour',
    'capture_configuration'
]) AS t;

INSERT INTO setting (key, value, updated_at) VALUES
    ('retention_seconds', '7776000', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('aggregate_mode', 'full', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('attribution_max_delay_seconds', '5', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('attribution_max_cache_answer_delay_seconds', '3600', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('leg_pairing_window_seconds', '1', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('lease_backend_failure_pass_limit', '3', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('heartbeat_interval_seconds', '60', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('downtime_threshold_seconds', '300', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('live_interest_seconds', '30', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('live_sample_retention_seconds', '3600', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('netflow_join_tolerance_seconds', '3', CAST(extract(epoch FROM now()) AS BIGINT)),
    ('retention_flow_record_seconds', '0', CAST(extract(epoch FROM now()) AS BIGINT))
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
-- THIS SEED IS THE HISTORICAL SET AND NOT A LIST TO KEEP UP. Every row below is
-- now also declared by a built-in plugin (plugins/<name>/plugin.json), under the
-- same kind and key, so a store whose seed is ignored ends with the same rows. A
-- plugin added later -- a directory dropped into <data-dir>/plugins/ -- declares
-- its kind, key and display name in its manifest, and the collector registers its
-- row (and the availability row below) at the first probe round with
-- Store.EnsureProvider, so adding one edits nothing in this file, a new kind
-- included.
--
-- No provider is active. Activeness says which implementation opnview reads,
-- and it is decided by the probe round in internal/collect against a live
-- firewall; this file has no way to know and must not pretend to. Every
-- citation below is in docs/opnsense-api-survey.md, and docs/data-model.md
-- tabulates them.
INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at) VALUES
    ('firewall_log',   'pf',                'pf filter log',        0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('security_event', 'suricata',          'Suricata',             0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('flow_volume',    'insight',           'NetFlow / Insight',    0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('dhcp_lease',     'kea',               'Kea DHCP',             0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('dhcp_lease',     'dnsmasq',           'Dnsmasq DHCP',         0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('dhcp_lease',     'isc',               'ISC dhcpd',            0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('dns_lookup',     'unbound',           'Unbound',              0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('dns_lookup',     'dnsmasq',           'Dnsmasq resolver',     0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('ip_geolocation', 'maxmind_geolite2',  'MaxMind GeoLite2',     0, CAST(extract(epoch FROM now()) AS BIGINT)),
    -- The measurement_sample kind's first implementation is the sampler 4A
    -- already wrote: the live per-pair traffic snapshot and the firewall's own
    -- gauges. It was registered under flow_volume because the kind did not exist,
    -- and it moves here with the key it already has. The firewall's OWN gauges
    -- still carry a null provider_id, because the machine reporting on itself
    -- implements no external contract; what this row accounts for is the sampled
    -- per-pair volume, which is a source's material.
    ('measurement_sample', 'insight',       'NetFlow / Insight sampling',
                                                                    0, CAST(extract(epoch FROM now()) AS BIGINT)),
    -- The Public Suffix List, downloaded from publicsuffix.org by the built-in
    -- publicsuffix plugin: the registrable_domain kind's built-in provider.
    ('registrable_domain', 'public_suffix_list', 'Public Suffix List', 0, CAST(extract(epoch FROM now()) AS BIGINT)),
    -- The resolver's cache and its local data, one row per resolver for each: Unbound
    -- answers both reads; Dnsmasq is registered so that its state -- it offers no
    -- cache or local-data read through the API -- is probed and named rather than
    -- absent.
    ('resolver_cache', 'unbound',           'Unbound cache',        0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('resolver_cache', 'dnsmasq',           'Dnsmasq cache',        0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('resolver_local_data', 'unbound',      'Unbound local data',   0, CAST(extract(epoch FROM now()) AS BIGINT)),
    ('resolver_local_data', 'dnsmasq',      'Dnsmasq local data',   0, CAST(extract(epoch FROM now()) AS BIGINT))
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
INSERT INTO source_availability (provider_id, state, outcome, probe, detail, checked_at)
SELECT p.id, 'unavailable', 'not_yet_probed', 'not_yet_probed', NULL, 0
FROM provider AS p
WHERE NOT EXISTS (SELECT 1 FROM source_availability a WHERE a.provider_id = p.id);

-- ---------------------------------------------------------------------------
-- THE LEARNED MIRROR OF THE FIREWALL API (A9 and A10 of the broker amendment to
-- specs/SPEC-backend-completion-plugins.md). The schema `opnsense` holds what the
-- broker observed of the firewall's answers: the data tables, which the mirror
-- creates and alters at run time from the fields its readers read, and the
-- metadata below, which this file creates. No endpoint, field or installation
-- value is written here: the tables are generic, and their rows are what the
-- broker learned. The read role holds SELECT on the data tables only, granted as
-- each is created; nothing below is readable by it.
-- ---------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS opnsense;

-- One firewall endpoint the broker has called: its method and body encoding, its
-- call cost, its streams and the broker's counters.
CREATE TABLE IF NOT EXISTS opnsense.mirror_endpoint (
    id                     BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    path                   TEXT NOT NULL CHECK (left(path, 5) = '/api/'),
    method                 TEXT NOT NULL CHECK (method IN ('GET', 'POST')),
    encoding               TEXT NOT NULL CHECK (encoding IN ('none', 'form', 'json')),
    first_seen_at          BIGINT NOT NULL CHECK (first_seen_at >= 0 AND first_seen_at < 4102444800),
    -- Whether the answer was seen to be an event stream, from its headers, and
    -- whether a manifest declared it one: the catalogue reports a disagreement.
    observed_stream        BOOLEAN NOT NULL DEFAULT false,
    declared_stream        BOOLEAN NOT NULL DEFAULT false,
    calls                  BIGINT NOT NULL DEFAULT 0 CHECK (calls >= 0),
    mirror_hits            BIGINT NOT NULL DEFAULT 0 CHECK (mirror_hits >= 0),
    write_refused          BIGINT NOT NULL DEFAULT 0 CHECK (write_refused >= 0),
    -- The last calls' latencies in milliseconds and answer sizes in bytes, newest
    -- last, at most 64 of each: the catalogue's median and 95th percentile.
    latencies_ms           JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(latencies_ms) = 'array'),
    sizes_bytes            JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(sizes_bytes) = 'array'),
    -- A stream's observed shape: the median interval between events and the
    -- longest a connection lasted before the server cut it.
    event_interval_ms      DOUBLE PRECISION,
    stream_cutoff_seconds  DOUBLE PRECISION,
    UNIQUE (path, method)
);

-- One request parameter of an endpoint, its role, who corrected it, and whether a
-- window parameter is honoured: NULL until a window was tried, false once a row
-- came back outside it.
CREATE TABLE IF NOT EXISTS opnsense.mirror_parameter (
    endpoint_id   BIGINT NOT NULL REFERENCES opnsense.mirror_endpoint (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (length(name) > 0),
    role          TEXT NOT NULL CHECK (role IN ('window_start', 'window_end', 'page', 'page_size',
                                                'identifying', 'ignored')),
    corrected_by  TEXT,
    honoured      BOOLEAN,
    PRIMARY KEY (endpoint_id, name)
);

-- One datum: one endpoint with one combination of identifying parameters, as
-- answered by one firewall. `firewall` is a non-secret identity of the configured
-- base URL (a digest, never the URL), so a changed firewall starts from nothing
-- fresh and its predecessor's history is never mixed into its state.
CREATE TABLE IF NOT EXISTS opnsense.mirror_datum (
    id                 BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    endpoint_id        BIGINT NOT NULL REFERENCES opnsense.mirror_endpoint (id) ON DELETE CASCADE,
    identity           JSONB NOT NULL,
    identity_key       TEXT NOT NULL,
    firewall           TEXT NOT NULL,
    nature             TEXT CHECK (nature IS NULL OR nature IN ('snapshot', 'record_stream')),
    row_set            TEXT,
    key                JSONB,
    cursor             JSONB,
    cursor_type        TEXT,
    cursor_descending  BOOLEAN NOT NULL DEFAULT false,
    dedupe_on_content  BOOLEAN NOT NULL DEFAULT false,
    generation         BIGINT NOT NULL DEFAULT 0,
    -- The last call that answered OK, and the last call whatever it answered: an
    -- unsuccessful outcome counts for freshness and never replaces rows.
    fetched_at         BIGINT CHECK (fetched_at IS NULL OR (fetched_at >= 0 AND fetched_at < 4102444800)),
    attempted_at       BIGINT CHECK (attempted_at IS NULL OR (attempted_at >= 0 AND attempted_at < 4102444800)),
    outcome            TEXT,
    status_code        BIGINT,
    detail             TEXT,
    -- The shape of the last answer's containers, and its page size.
    root_shape         JSONB,
    page_size          BIGINT,
    -- The last answer's body, kept only while a reader reads the datum whole.
    body               TEXT,
    read_whole         BOOLEAN NOT NULL DEFAULT false,
    -- Generations whose stored content changed: the observed change frequency.
    changes            BIGINT NOT NULL DEFAULT 0,
    UNIQUE (endpoint_id, identity_key, firewall)
);

-- The data tables the mirror created: one per endpoint and row set.
CREATE TABLE IF NOT EXISTS opnsense.mirror_table (
    name            TEXT PRIMARY KEY,
    endpoint_id     BIGINT NOT NULL REFERENCES opnsense.mirror_endpoint (id) ON DELETE CASCADE,
    row_set         TEXT NOT NULL,
    parent_row_set  TEXT,
    hypertable      BOOLEAN NOT NULL DEFAULT false,
    created_at      BIGINT NOT NULL CHECK (created_at >= 0 AND created_at < 4102444800),
    UNIQUE (endpoint_id, row_set)
);

-- The columns of the data tables: the field path each holds, its inferred or
-- corrected type, and whether it carries a domain name.
CREATE TABLE IF NOT EXISTS opnsense.mirror_column (
    table_name      TEXT NOT NULL REFERENCES opnsense.mirror_table (name) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    column_name     TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('ip_address', 'mac_address', 'timestamp', 'number', 'text',
                                                  'boolean', 'json')),
    carries_domain  BOOLEAN NOT NULL DEFAULT false,
    added_at        BIGINT NOT NULL CHECK (added_at >= 0 AND added_at < 4102444800),
    PRIMARY KEY (table_name, path),
    UNIQUE (table_name, column_name)
);

-- Every change of an endpoint's mirrored schema: its instant, its cause, and the
-- firewall version that produced the shape.
CREATE TABLE IF NOT EXISTS opnsense.mirror_schema_version (
    endpoint_id       BIGINT NOT NULL REFERENCES opnsense.mirror_endpoint (id) ON DELETE CASCADE,
    version           BIGINT NOT NULL CHECK (version >= 1),
    changed_at        BIGINT NOT NULL CHECK (changed_at >= 0 AND changed_at < 4102444800),
    cause             TEXT NOT NULL,
    firewall_version  TEXT,
    PRIMARY KEY (endpoint_id, version)
);

-- What each reader read of each datum: the field paths, per plugin and plugin
-- version, with its max age. A path read whole marks everything below it.
CREATE TABLE IF NOT EXISTS opnsense.mirror_read (
    datum_id         BIGINT NOT NULL REFERENCES opnsense.mirror_datum (id) ON DELETE CASCADE,
    reader           TEXT NOT NULL,
    reader_version   TEXT NOT NULL,
    path             TEXT NOT NULL,
    whole            BOOLEAN NOT NULL,
    max_age_seconds  DOUBLE PRECISION,
    first_read_at    BIGINT NOT NULL CHECK (first_read_at >= 0 AND first_read_at < 4102444800),
    last_read_at     BIGINT NOT NULL CHECK (last_read_at >= 0 AND last_read_at < 4102444800),
    PRIMARY KEY (datum_id, reader, reader_version, path, whole)
);

-- The corrections a plugin's manifest applies to an endpoint, and whether they
-- were applied or lost to another plugin's (mirror_correction_conflict).
CREATE TABLE IF NOT EXISTS opnsense.mirror_correction (
    endpoint_id  BIGINT NOT NULL REFERENCES opnsense.mirror_endpoint (id) ON DELETE CASCADE,
    reader       TEXT NOT NULL,
    corrections  JSONB NOT NULL,
    applied      BOOLEAN NOT NULL,
    recorded_at  BIGINT NOT NULL CHECK (recorded_at >= 0 AND recorded_at < 4102444800),
    PRIMARY KEY (endpoint_id, reader)
);

-- An interval a datum or a stream is known not to cover: a record stream whose
-- answer began after its newest stored row, a failed call, a stream cut. A gap is
-- recorded, never bridged, and never read as zeros.
CREATE TABLE IF NOT EXISTS opnsense.mirror_gap (
    id           BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    datum_id     BIGINT REFERENCES opnsense.mirror_datum (id) ON DELETE CASCADE,
    stream       TEXT,
    from_at      BIGINT NOT NULL CHECK (from_at >= 0 AND from_at < 4102444800),
    to_at        BIGINT NOT NULL CHECK (to_at >= from_at AND to_at < 4102444800),
    reason       TEXT NOT NULL CHECK (reason IN ('answer_newer_than_stored', 'call_failed', 'stream_cut')),
    recorded_at  BIGINT NOT NULL CHECK (recorded_at >= 0 AND recorded_at < 4102444800),
    CHECK ((datum_id IS NULL) <> (stream IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_mirror_gap_datum ON opnsense.mirror_gap (datum_id, from_at);
CREATE INDEX IF NOT EXISTS idx_mirror_gap_stream ON opnsense.mirror_gap (stream, from_at);
