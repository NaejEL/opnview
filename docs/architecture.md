# Architecture — the provider seam

`opnview` reads material from a firewall and stores it in one PostgreSQL database with the
TimescaleDB extension (*Storage*, below).
This document names the seam between "the material" and "who supplied it", and
stops there. It is a seam document, not a plugin specification.

The authority on the schema is `internal/store/schema.sql`; on the sources,
`docs/opnsense-api-survey.md`; on the entities, `docs/data-model.md`.

## Eleven kinds

A **kind** is a contract `opnview` implements: a shape of material, with a
normalised destination in the schema. Exactly eleven exist, and the `provider`
table constrains its `kind` column to them. The resolver-cache cycle added the last
two (`specs/SPEC-resolver-cache-attribution.md`, decision 1).

<!-- provider-kinds:begin -->
```
dhcp_lease
dns_lookup
firewall_log
flow_volume
ip_geolocation
measurement_sample
reconciled_state
registrable_domain
resolver_cache
resolver_local_data
security_event
```
<!-- provider-kinds:end -->

| Kind | What it supplies | Normalised into |
|---|---|---|
| `firewall_log` | one record per logged packet decision | `flow`, and `blocked_event` over it |
| `security_event` | one record per detection | `security_event`, with `provider_rule_info` for severity |
| `flow_volume` | per-pair byte and packet counters | `pair_volume_observation`, **derived from `flow` and collected from nowhere** |
| `dhcp_lease` | one record per lease generation | `dhcp_lease`, naming the server that issued each one, feeding `client` identity |
| `dns_lookup` | one record per resolver lookup | `dns_resolution`, feeding `domain_attribution` |
| `ip_geolocation` | country, coordinates, ASN and operator per address | `geo_asn` |
| `measurement_sample` | one numeric reading of one subject at one instant | `measurement_sample` |
| `reconciled_state` | the complete set of things of one type, as of one instant | `state_snapshot` and `state_item`, with `state_item_departure` over them |
| `registrable_domain` | the registrable domain of a site name, computed from the Public Suffix List | **no row**: one list file in the plugin's data directory, read by the built-in `publicsuffix` plugin to group site names at read time |
| `resolver_cache` | the records a resolver holds in its cache — the answer addresses of the names it resolved, which the query report of `dns_lookup` does not carry | `resource_record_observation` (held in `cache`), feeding `domain_attribution`'s `resolver_cache_answer` method |
| `resolver_local_data` | the records a resolver serves from its own local data, host overrides among them | `resource_record_observation` (held in `local_data`), feeding host-name resolution (`dns_resolution.client_resolution = 'local_data_hostname'`) and the exact evidence of a lookup answered from Local-data |

**`measurement_sample` and `reconciled_state` are the survey's finding rather than a design instinct.** Of the
~34 data-producing sources the plugin ecosystem exposes, **eight of the ten that
fit an existing shape fit `measurement_sample`** — per-peer transfer counters,
frontend and backend counters, per-interface volume, UPS, SMART and sensor
readings — and it was not a kind: a table with no `provider_key`, no availability
row and no place in `provider.kind`. **Ten of the eleven that fit nothing at all
are one single shape**, the reconciled set. Counting only the log-shaped kinds,
two sources in the whole ecosystem add to them. See
`docs/opnsense-api-survey.md`, *What the plugin ecosystem actually exposes*.

`flow_volume` keeps its registry row and has no implementation, deliberately:
its destination is **computed from `flow`** by step 5, because the only per-pair
endpoint carries neither a port nor a protocol and the filter log carries both
exactly. `reconciled_state` has **no registry row at all**: a row is a claim that
an implementation exists, and no connector for a state-shaped source is written
yet.

`registrable_domain` (`public_suffix` until the plugin cycle) supplies **no row of
observed data**. Its built-in implementation, the `publicsuffix` plugin, holds the
Public Suffix List, downloaded from publicsuffix.org — the third and last of the
outbound calls the project allows — and it is the rule set the registrable domain
of a site name is computed from, at read time. It is a kind rather than a helper because it has everything else a kind has: a provider,
an availability row that says whether a list is held, and failures to record (a
download that failed is a `collection_gap` with the reason `download_failed`,
while the list held since the last refresh stays in use). It is exclusive, because
two lists would be two answers to one question.

**`resolver_cache` and `resolver_local_data` are two kinds and not one more
method of `dns_lookup`**, because each has its own material, its own cadence and its
own availability: a resolver whose cache cannot be read still reports its lookups,
and the reverse, and a screen has to be able to say which of the two is missing.
"Cache" and "local data" are Unbound's own words (unbound-control(8), `dump_cache`
and `list_local_data`; unbound.conf(5), `local-data:`). Unbound implements both;
Dnsmasq is registered for both and reports the named state "this resolver offers no
cache read through the API" (or "no local-data read"), because OPNsense 26.7.3
exposes neither for it. Adding a resolver is one implementation file and one
registry row per kind.

A **provider** is one implementation of one kind. It is a row in the `provider`
registry, identified by `(kind, provider_key)`. Several providers of one kind
exist today: Unbound and Dnsmasq both supply `dns_lookup`, `resolver_cache` and
`resolver_local_data`; Kea, Dnsmasq and ISC dhcpd all supply `dhcp_lease`.

**No provider name appears in the schema as an identifier.** Not as a table
name, not as a column name. A provider name is a value — a registry row, or a
string in a row that references one. The single exception is
`eve_ingest_cursor`, which names a file format rather than a product, and which
is rewritten when a second ingesting provider is surveyed.

## A provider declares its own availability

Every registered provider carries exactly one row in `source_availability`,
holding `reachable`, `present_but_disabled` or `unavailable`, the instant that
was determined, and the probe that determined it.

**Availability is a modelled state, not an absence of rows.** No source on this
firewall can be told apart from silence by an empty result alone (survey, gap
11). A provider that is not installed, not running, or running with reporting
switched off is therefore recorded as being in that state and rendered as that
condition — never as "no traffic", "no alerts", "no clients" or "no names". A
provider with no availability row would be indistinguishable from a healthy
one, so the row exists from the first apply of the schema onwards, in the not-yet-probed
`unavailable` state.

## Activeness, and what is still exclusive

Reachability and use are two different facts. A machine may have two
implementations of a kind installed and reachable at once, and the model must
say which one `opnview` actually read. That is `provider.is_active`.

**One active provider per kind was the wrong universal rule.** People run
several detection engines side by side, and the how-to corpus is people stacking
them (survey, *Four findings that bear on the collectors already written*), so
three concurrent providers of the detection kind is the normal installation.
The partial unique index over `kind WHERE is_active = 1` is gone. What replaces
it is derived from the schema rather than chosen kind by kind:

> **A kind admits several concurrently active providers exactly when the
> identity of its destination rows includes the provider.**

Where the identity includes it, two active providers can neither collide nor
double-count: every row says who reported it, so one fact reported twice is two
attributed rows and a screen can show them per provider or side by side. Where
it does not, the second provider's rows are indistinguishable from the first's
by origin, so it would either collide with them or silently double a figure
nobody could decompose.

| Kind | Destination identity | Several active? |
|---|---|---|
| `firewall_log` | `flow.log_digest` | no |
| `security_event` | `(provider_id, provider_event_key)` | **yes** |
| `flow_volume` | `(day_start_at, endpoints, port, protocol)` | no |
| `dhcp_lease` | `(address, generation_key, provider_id)` | **yes** |
| `dns_lookup` | `dns_resolution.lookup_key` | no |
| `ip_geolocation` | `geo_asn.address` | no |
| `measurement_sample` | `(subject, measure, sampled_at, provider)` | **yes** |
| `reconciled_state` | `(provider_id, set_key, captured_at)` | **yes** |
| `registrable_domain` | one list file on disk, no row | no |
| `resolver_cache` | `(provider_id, owner_name, rrtype, value, first_seen_at)` of `resource_record_observation` | **yes** |
| `resolver_local_data` | the same, of the same table | **yes** |

**`dhcp_lease` is concurrent, and the deployment is the ordinary one:** one
server issuing on one VLAN and another on a second, two scopes with no overlap.
The objection to it was that two active lease providers would put one machine on
the screen twice under two names, and the **client identity cascade** is what
answers it — the cascade keys on the DHCP client identifier first and on the MAC
second, and *neither is scoped to an interface*, so a machine leased on two VLANs
by two servers resolves to **one client holding two leases**, which is the truth.
The duplication the objection feared needs two servers issuing on the *same*
scope, and that is a misconfiguration of the firewall rather than a shape this
model should contort itself to absorb.

Its identity names the **provider** and not the backend, and the distinction is
load-bearing rather than pedantic: `backend` is a normalised vocabulary of
response *shapes*, and two providers could report the same one — a second
implementation reading a Kea running elsewhere would — so keying on it satisfied
the letter of the rule above and not its substance. The rule is stated in terms of
the provider, and so is the key.

`ip_geolocation` is exclusive because its row, in `geo_asn`, is one cache entry per address, and a
second provider would overwrite the first's answer rather than add to it.
`dns_lookup` is exclusive because `dns_resolution.lookup_key` carries no provider:
a lookup that transited two resolvers would be two records nothing could tell
apart from two lookups.

It is enforced by a partial unique index over `kind` where `is_active = 1` **and
the kind is not one of the six marked yes above**, and mirrored in `internal/store/kinds.go` so
the probe round can activate every qualifying provider of a concurrent kind
instead of discovering the constraint by failing. The database is the authority,
and a test reads the index's own definition back and asserts the two agree.

On a freshly applied schema **no provider is active**. Activeness is determined
by runtime detection, never by the schema: a schema file has no way to know what
is installed and must not pretend to. For an exclusive kind, two candidates the
firewall's own configuration does not separate activate **neither**, and the
ambiguity is recorded on both rows; for a concurrent kind there is nothing to
decide, so every candidate is activated.

## A future provider's event shape will be surveyed, not guessed

`security_event` carries what one surveyed provider actually supplies, plus the
two things any provider must have: an identity of its own
(`provider_id`, `provider_event_key`) and a rule identity as text, so a provider
whose rules are named rather than numbered fits without a schema change. It carries
no column invented for a provider nobody has installed.

When a second provider of any kind is actually installed, **its event shape
will be surveyed exactly as the OPNsense API was surveyed at step 1**, and the
schema will be extended against observed data. An abstraction designed before
seeing the data is wrong; step 1 already produced one, for Suricata `tls` and
`dns` events that turn out not to exist.

The rule for provider-specific data that does not fit the core is in
`docs/data-model.md` under *The attribute rule*: a typed, indexed column for
anything a screen aggregates or filters on; a JSON column, displayed only, for
anything merely shown; never an entity-attribute-value table.

## The widget registry — the second seam
The widget API of step 5B (`docs/widget-api.md`) is a seam of the same kind, on the read
side. A served preset of `docs/widget-catalogue.md` is **one directory** of a widget plugin,
`plugins/<plugin>/widgets/<type>/`, holding its `manifest.json` (type, shape, the provider
kinds its binding names, its declared parameters with their defaults and their data or
rendering class, its no_domains rule, a `renderer` slot reserved for step 7), its statement
files and, for the logic a shape cannot express, its JavaScript, run by the widget host
(`docs/plugins.md`, *Widgets*). Every built-in widget is such a plugin, embedded in the binary
and found by walking the embedded tree, and a third party's is the same directory under
`<data-dir>/plugins/`; there is no registry list and no Go code per widget. **Operator widgets** are directories dropped in
`<data-dir>/widgets/<type>/`, validated at start (manifest schema, parameter vocabulary, a
single read-only query planned on the read pool) and served by the shape engine in
`internal/widget`, which holds once, per catalogue shape, the reconciliation, remainders,
buckets, conditions, envelope, references, no_domains withholding and lower bound. A widget
refused at load is logged with a named reason and never stops the service. The `widgetapi`
surface (`internal/web/surfaces/widgetapi`) builds one authenticated route per type from
`Service.Types()`. Adding a widget is adding a directory: `docs/extending.md` is the
procedure and `docs/widget-authoring.md` the reference; a test adds one by writing files
into a temporary operator directory and reaches it over HTTP.

A widget reads the database only through named statements, embedded beside the code that
runs them and registered by name (the core's in `internal/widget/*.statements.sql`, each
preset's in its own plugin directory, checked when the plugin loads), run by
`Store.WidgetRows` on the store's read-only connections (`internal/store/readpool.go`), so
`sql/schema-checks.sh` plans every statement the API runs, found by walking the tree, and no
widget can write. The three alert presets have no directory until step 6, and
answer `unknown_widget_type` like any type with none.

## The web surfaces — the third seam

The interface is modular in the same way. A **surface** is **a directory**
`internal/web/surfaces/<name>/` holding its handlers, its page templates (`template/`), its
catalogue files (`locale/<language>.json`) and a `surface.json` marker, and registering itself
from an `init()` with `web.RegisterSurface`. The core in `internal/web` owns what is shared --
the server, the route table and its access levels, the session and CSRF middleware, the layout,
the themes, the stylesheet, the string catalogue and the helpers several surfaces use -- and
hands a surface exactly what it may use through the `web.Host` interface. Six surfaces ship:
`account` (setup, sign-in, sign-out, the key-file reset), `settings`, `collection`,
`interfaces`, `sources` (which also contributes the top bar's outage indicator) and
`widgetapi`.

Everything that varies with the set of surfaces is built from the registered ones at start: the
route table, the pages, the tabs of the top bar, the indicators, the closed list of keys a
redirect may carry, and the catalogue (the core's file merged with each surface's). The only
file that names the surfaces is `internal/web/surfaces/surfaces_gen.go`, written by
`go generate` from the directories (`internal/discover`) and checked by a test that fails, naming
the command, when it is stale. A surface that cannot be served -- a missing or non-compiling
template, a malformed or clashing catalogue file, a taken name or route, no access level -- is
left out with a named reason and the server starts without it. `docs/extending.md` is the
procedure, with a worked example the tests run.

## The broker — the one path to the firewall

Every request to the firewall API goes through one package, `internal/broker` (A9 to A18 of the
broker amendment to `specs/SPEC-backend-completion-plugins.md`): the plugins', through the plugin
host; discovery's and the probe round's; and the core coordinators', through the provider host's
gate. It is the only caller of the client's `Call`, `Stream` and `Listen`. The one exception is the
settings surface's check of candidate credentials (`internal/web/surfaces/settings/verify.go`):
those credentials are not saved, the firewall they name may not be the configured one, and its
answer must never enter the mirror, so that check calls the client directly.

The broker is four parts, each a package named by its responsibility:

- `internal/broker` -- the gate: the read-only rule (`readonly.go`: a `POST` only to a read action,
  never a command in the client's mutating list, refused before anything is sent), one call per
  datum however many readers wait for it, the answer served from the mirror while it is younger
  than the reader's max age, the record of what each reader read, the stream subscriptions and
  their recording, and the API catalogue.
- `internal/broker/mirror` -- the `opnsense` schema: the fields read of each answer, under the
  API's own names, in tables learned from the answers; their history; the gaps; the purge.
- `internal/broker/infer` -- what an answer is: its nature, rows, key, cursor, field types,
  value-keyed objects and parameter roles, corrected by a manifest where it is wrong.
- `internal/broker/schedule` -- when a datum is refreshed and in what order calls leave: demand
  drives it, nearest deadline first, a reserve for real-time data, two ceilings (calls in flight,
  calls per second) and back-off.

The firewall's OPNsense version is read at every discovery pass and held by the broker: a plugin
mapping version ranges to its routes or scripts is served by the one its version selects
(`docs/plugins.md`, *OPNsense versions*). Downloads -- MaxMind, the Public Suffix List -- never go
through the broker: they are not the firewall API, and the fetch facility makes them on its own
timing.

## Storage

**The engine.** PostgreSQL 17 with the TimescaleDB extension, one database per installation,
`opnview` its only client (P1 of `specs/SPEC-backend-completion-plugins.md`). The pin is written
once, in `docker-compose.yml` (`x-timescaledb-image`), by tag and digest:
`timescale/timescaledb:2.30.2-pg17`, TimescaleDB 2.30.2 on PostgreSQL 17.11. Release notes:
<https://github.com/timescale/timescaledb/releases/tag/2.30.2> (it supports PostgreSQL 16, 17 and
18) and <https://www.postgresql.org/docs/release/17.11/>. PostgreSQL 17 rather than 18: the
development image is Debian trixie, whose archive carries the PostgreSQL 17 client tools
(`psql`, `pg_dump`) and not 18's, and the client tools must match the server's major for a dump
and its restore. The pin is changed deliberately, never floated.

**The licence.** TimescaleDB ships an Apache-2.0 edition and features under the Timescale License
(TSL), listed in its editions matrix (<https://docs.timescale.com/about/latest/timescaledb-editions/>).
**`opnview` uses no TSL feature.** What it uses -- hypertables (`create_hypertable`),
`show_chunks` and `drop_chunks` -- is Apache-2.0; it uses no continuous aggregate, no
compression or columnstore, no background job or policy (*Storage fit*, `docs/data-model.md`).
Every test database runs with `timescaledb.license=apache`, the Apache edition alone, so a TSL
feature reached by mistake fails the suite.

**Telemetry is off, and enforced.** Every database service runs with
`timescaledb.telemetry_level=off`, and `opnview` refuses to start against a database where it is
not (`store.ErrTelemetryOn`): telemetry would be an outbound call the project's rules forbid.

**Byte order.** A database is created with the libc `C` locale; `opnview` refuses one whose
collation, character classification or locale provider is anything else (`store.ErrCollation`),
because it orders text by its bytes and folds case on ASCII alone, as the previous engine did.

**One instance per database.** A session advisory lock held for the store's life makes a second
`opnview` against the same database exit with `store.ErrAlreadyRunning`.

**The read role.** Every widget, plugin and catalogue query runs as `<database>_reader`, a role
created at start with a password generated then and held in memory only, in read-only
transactions under a statement timeout, with no privilege on `account`, `session`,
`encrypted_credential` or `plugin_state` (`internal/store/readers.go`).

**The driver.** `github.com/jackc/pgx/v5` v5.11.0, pure Go, through `database/sql`, so `opnview`
builds with `CGO_ENABLED=0`. Its transitive modules, as the build resolves them:
`github.com/jackc/pgpassfile` v1.0.0, `github.com/jackc/pgservicefile`
v0.0.0-20240606120523-5a60cdf6a761, `github.com/jackc/puddle/v2` v2.2.2, `golang.org/x/sync`
v0.24.0 and `golang.org/x/text` v0.43.0 (both raised from pgx's own requirement, v0.17.0 and
v0.29.0, by the rest of the module graph -- upgrades, never downgrades). The test-only modules
pgx's own `go.mod` names (`testify` and its dependencies) are not linked into `opnview`.

**The schema** is one file, `internal/store/schema.sql`, applied in place in one transaction on
every start. The core's objects live in the schema `opnview`; each plugin's tables in its own
schema `plugin_<plugin>`; tables set aside by a changed plugin declaration in `opnview_aside`.

## What this is not

`opnview` is one Go binary. A kind is implemented by a **plugin**: a directory holding a
manifest and, where the manifest cannot say it, a JavaScript script the binary's embedded
interpreter runs (`docs/plugins.md`). The built-in plugins are embedded in the binary; a plugin
dropped into the data directory is read at start. There is still **no dynamic loading of code**:
no shared object, no external process, no Go file in a plugin directory. The registry described
above is a table of rows saying which implementations exist and which one is read -- data about
providers, not a mechanism for loading them.
