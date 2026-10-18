# Architecture — the provider seam

`opnview` reads material from a firewall and stores it in one PostgreSQL database with the
TimescaleDB extension (*Storage*, below).
This document names the seam between "the material" and "who supplied it", and
stops there. It is a seam document, not a plugin specification.

The authority on the schema is `internal/store/schema.sql`; on the sources,
`docs/opnsense-api-survey.md`; on the entities, `docs/data-model.md`.

## Twelve kinds

A **kind** is a contract `opnview` implements: a shape of material, with a
normalised destination in the schema. Exactly twelve core kinds exist, one kind file each; a plugin may
bring kinds of its own, and the `provider` table refuses a `kind` that is not a
snake_case name. The resolver-cache cycle added `resolver_cache` and
`resolver_local_data` (`specs/SPEC-resolver-cache-attribution.md`, decision 1). The
NetFlow addendum's `flow_record` (`specs/SPEC-backend-completion-plugins.md`, PS17) is
no longer a core kind: the plugin-API amendment of 17 October 2026 (PA9) made it the
`netflow` plugin's own, with its tables, its join and its measure; and its PA12 made the ARP and
NDP tables, once read beside the leases, the core kind `neighbour`, implemented by the built-in
`neighbours` plugin.

<!-- provider-kinds:begin -->
```
dhcp_lease
dns_lookup
firewall_log
flow_volume
ip_geolocation
measurement_sample
neighbour
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
| `neighbour` | one entry of the firewall's ARP or NDP table: a MAC, an address and the interface it was learned on | **no row of its own**: a MAC-level `client` through the identity cascade, never this firewall and never a neighbour of an upstream interface |
| `reconciled_state` | the complete set of things of one type, as of one instant | `state_snapshot` and `state_item`, with `state_item_departure` over them |
| `registrable_domain` | the registrable domain of a site name, computed from the Public Suffix List | **no row**: one list file in the plugin's data directory, read by the built-in `publicsuffix` plugin to group site names at read time |
| `resolver_cache` | the records a resolver holds in its cache — the answer addresses of the names it resolved, which the query report of `dns_lookup` does not carry | `resource_record_observation` (held in `cache`), feeding `domain_attribution`'s `resolver_cache_answer` method |
| `resolver_local_data` | the records a resolver serves from its own local data, host overrides among them | `resource_record_observation` (held in `local_data`), feeding host-name resolution (`dns_resolution.client_resolution = 'local_data_hostname'`) and the exact evidence of a lookup answered from Local-data |

The twelve above are the core kinds. A built-in plugin may also declare a kind of its own, writing into its own tables: `pf_state_rate` (the `pf_states` plugin's pf state table rates) and `unbound_query_log` (the `unbound` plugin's query log received over syslog). Their contracts are their manifests' tables (`docs/plugins.md`, *Kinds of the plugin's own*).

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
rendering class, its no_domains rule, and its `renderer`, the file the front draws it with), its statement
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

The server side of the interface is modular in the same way. A **surface** is **a directory**
`internal/web/surfaces/<name>/` holding its handlers, its catalogue files
(`locale/<language>.json`) and a `surface.json` marker, and registering itself from an `init()`
with `web.RegisterSurface`. **A surface draws no page.** It answers JSON and closed codes, and
the words a reader sees for those codes are its catalogue's, drawn by the front (below). The
Go-rendered pages, their templates, the layout and the stylesheet of the steps before 7 are gone;
their paths are routes of the shell, so a bookmark still lands on the same view. The core in
`internal/web` owns what is shared -- the server, the route table and its access levels, the
session and CSRF middleware, the string catalogue and the helpers several surfaces use -- and hands
a surface exactly what it may use through the `web.Host` interface. The surfaces that ship are
listed in *Map of the tree*: among them `account` (setup, sign-in, sign-out, the key-file reset),
`settings`, `collection`, `interfaces`, `sources`, `people`, `labels`, `widgetapi`,
`dashboards`, `references` and `frontend`, which serves the front itself.

Everything that varies with the set of surfaces is built from the registered ones at start: the
route table and the catalogue (the core's file merged with each surface's). The only file that
names the surfaces is `internal/web/surfaces/surfaces_gen.go`, written by `go generate` from the
directories (`internal/discover`) and checked by a test that fails, naming the command, when it is
stale. A surface that cannot be served -- a malformed or clashing catalogue file, a taken name or
route, no access level -- is left out with a named reason and the server starts without it.
`docs/extending.md` is the procedure, with a worked example the tests run.

## The front — the fourth seam

The interface a browser draws is **the front**: one shell document and the scripts that draw every
view in the browser from the JSON routes (`specs/SPEC-step-7-frontend.md`, A1 to A9). It is the
fourth seam, and the one a third party extends without a Go toolchain at all.

- **The built-in front** is the top-level `front/` tree, embedded in the binary the way `plugins/`
  is (`front.go`): the shell, the core scripts and the stylesheet, the views, the twelve shape
  renderers, the six theme files, the colour-token set, the basemap and the default dashboard. A
  built-in widget's renderer is a file of its own widget directory, `plugins/<plugin>/widgets/<type>/`:
  the widget is one directory, manifest, statements, script and renderer together.
- **The data directory's parts** are read at start: a theme is `<data-dir>/themes/<id>.json`, a
  language `<data-dir>/languages/<code>.json`, and a widget's renderer a file of its widget's
  directory -- a plugin's, an operator widget's, or a front-only widget's, which has a renderer and
  no query. The built-in wins every collision; a part that breaks a rule is refused with a closed
  code, listed by `GET /api/widgets`, and the service starts without it. A part added to a running
  service takes effect at the next restart. `docs/front-plugins.md` is the contract.
- **The index serves only what it declares.** At start `internal/front` builds an in-memory index
  of every file the browser may be served -- the core's assets, the generated palette stylesheet,
  each theme, each renderer entry the manifests name -- and the `frontend` surface answers
  `/front/{path}` from it alone. A widget's server-side script, a statement file, a manifest or any
  other file of a plugin or of the data directory is never served, and a path that escapes the index
  is impossible by construction rather than filtered. Every asset carries a policy that refuses any
  other origin; the front contacts no host but its own server.
- **Strings, appearance, dashboards.** The front asks for every word by key
  (`GET /api/strings/{language}`), keeps the palette, the mode and the language as installation
  settings (`/api/appearance`), and stores its canvases as dashboard documents
  (`docs/dashboard-format.md`) through the `dashboards` surface. `docs/front-api.md`
  is the reference.
- **Tested without a browser.** `internal/front/frontjs` runs the front's own files -- the bytes a
  browser is served -- in the interpreter the server already embeds, over a minimal document, so the
  reconciler, the views, the document module and every renderer are tested by `go test`.

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

## Push sources — inbound, beside the broker

Decision 15 of `specs/SPEC-backend-completion-plugins.md`: besides asking, `opnview` can receive what
the firewall sends once the user configures it to -- syslog (RFC 5424, RFC 3164, over UDP and TCP)
and NetFlow v9 / IPFIX (over UDP). Receiving is inbound: a listener calls nothing, so the broker
stays the only path **to** the firewall's API, and the outbound rule is unchanged.

| Part | Where | Why it is core |
|---|---|---|
| Listeners, routing, bounded queues, batching, gaps, senders, liveness | `internal/push` | protocol code against public standards, naming no protocol |
| One decoder per protocol: the syslog header and RFC 6587 framing; NetFlow v9 / IPFIX templates | `internal/push/protocols/<protocol>/`, discovered | a protocol is a seam inside the core: one directory holding its `protocol.json`, linked by the generated `protocols_gen.go` |
| The IANA IPFIX Information Elements registry | `internal/push/ipfixregistry` | a standard, held as data, like `internal/specialpurpose` |
| What a program's message says, and which columns a record fills | the plugins' `push` entries | a program's format is its plugin's: the core names none |
| One path per instant for a core kind fed by polling and by push | `internal/collect`, `pushpaths.go` | the kind's coordinator owns its identity and its gaps |

**The path of one message.** A listener accepts a datagram or a frame only from the firewall's own
addresses; its protocol's decoder decodes it **once**; routing looks its subscribers up by route key
(a syslog program) or asks the subscribers that index nothing (NetFlow); each subscriber's records
wait in a bounded queue and are handed in batches to its sink -- a plugin's table, or a core kind's
coordinator -- which extracts only its own columns. A drop is a gap of the subscriber's provider.

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
Every database runs with `timescaledb.license=apache`, the Apache edition alone -- the test
databases and the installation's (`docker-compose.serve.yml`) -- so a TSL feature reached by
mistake fails the suite, and `sql/schema-checks.sh` asserts the edition the database runs with. The
decision of 12 October 2026 to enable the Timescale License was superseded on 16 October 2026: no
continuous aggregate is exactly equivalent to the table it would replace (*Storage fit*), and an
unused licence would be a placeholder.

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

**Statements and values, PostgreSQL's own** (PA15 of the amendment of 17 October 2026). The connection
rewrites no statement and converts no value: each text is prepared once per connection by pgx's
statement cache, and PostgreSQL types its parameters. A statement written in Go numbers its
parameters (`$1`); a statements file -- the store's, a widget's, a plugin's -- names them `:name`,
the authoring form `docs/plugins.md` documents, and is translated to `$n` once, when it is registered
(`internal/store/sqldb`, `translate.go`). A flag is a `BOOLEAN` column. A plugin's or widget's
statement reads a whole `numeric` as an integer through the plugin host's conversion
(`internal/store/reads`, `cells.go`); a core statement casts what it reads. A pass writes its rows in
a number of statements independent of their count, and the refresh after it rewrites each period's
slots together (PA17).

**The schema** is one file, `internal/store/schema.sql`, applied in place in one transaction on
every start. The core's objects live in the schema `opnview`; each plugin's tables in its own
schema `plugin_<plugin>`; tables set aside by a changed plugin declaration in `opnview_aside`.

## Decisions in force

The open plugin API (the amendment of 17 October 2026 to `specs/SPEC-backend-completion-plugins.md`,
PA1 to PA21) holds the following, decided by the orchestrator under the maintainer's instruction to
deliver end to end; the spec carries each decision and its reason.

- **A third party adds a feature in JSON, JavaScript and SQL alone.** A plugin declares its kinds,
  datasets, derivations, measures, push entries, settings pages and widgets (`docs/plugins.md`), and
  `internal/plugin/testdata/plugins/example_reporter` proves it with no Go file and no shipped file
  edited. The plugin API version is 2, and a version-1 plugin is refused.
- **Kinds are data.** A core kind is a file of `internal/collect/provider/kinds/`; its `concurrent`,
  `push` and `bindable` attributes, cadences and page sizes are read from it, and the DDL seeds no
  product row: the built-ins' declarations write the registry at load.
- **NetFlow is the `netflow` plugin's.** Its records, index map, capture configuration, join and
  measure are the plugin's tables and JavaScript (`plugins/netflow`); the core keeps only the v9 / IPFIX
  decoder, for speed, naming no OPNsense concept. A second exporter is a plugin of its own.
- **Measures are declared.** `volume_measure` takes `logged`, `best_available` and every declared
  measure; `best_available` answers the first, in name order, whose strict rule covers the window. No
  core table, column, statement name or enum value names a measure, and the observation-point limit
  holds for every one.
- **A backend's fields are its plugin's.** Lease states, resolver actions and Insight subjects are
  decoded in each plugin's script; the identity cascade, the "this firewall is never a client" rule and
  the upstream exclusion stay core. The ARP and NDP tables are the `neighbours` plugin, kind
  `neighbour`; the rolling strip is a widget plugin.
- **Booleans are `BOOLEAN`**, and the APIs keep serving a field documented as 0/1 as 0/1, by its
  declared column type. A plugin's and a widget's statements keep `:name` and the whole-number reading
  of a `numeric`, translated and converted by the plugin host, never by the connection.

**No Go file names a product** (PA20). `internal/modularity/producttokens_test.go` cuts every
identifier and string literal of every non-test Go file under `cmd/` and `internal/` into words and
fails on a product's name, an OPNsense convention, or a literal beginning `/api/`. Its allow-list,
each entry with its reason:

| Where | May hold | Why |
|---|---|---|
| `internal/opnsense/` | `/api/`, `interfaces_info` | the firewall client: discovery and the clock read OPNsense's own endpoints, which every installation has whatever its plugins |
| `internal/collect/discovery/` | `/api/`, `interfaces_info` | discovery reads the interfaces and the firewall's own addresses, the core's view of the firewall that no plugin owns |
| `internal/broker/` | `/api/` | the firewall data broker: it serves plugins' calls by path, checks a path is an OPNsense API path, and names the core's own reads |
| `internal/push/protocols/netflow/` | `netflow`, `ipfix` | the NetFlow v9 / IPFIX decoder: the protocol's own name, decoded in Go for speed (PA7), naming no OPNsense concept |
| `internal/push/ipfixregistry/` | `netflow`, `ipfix` | the IANA IPFIX information-element registry the decoder reads: the protocol's own name |
| `internal/push/protocols/protocols_gen.go` | `netflow` | the generated discovery of the push protocols imports each protocol's directory, the NetFlow decoder's among them, by its path |
| `internal/web/surfaces/settings/verify.go` | `interfaces_info` | the credential check calls discovery's first endpoint once, the one every installation answers whatever its plugins |
| `cmd/opnview/measurecmd.go` | `interfaces_info` | the cost-measuring command lists the interface names through discovery's endpoint, as discovery reads them |
| `internal/web/surfaces/` | `/api/` | opnview's own HTTP API routes, which share the `/api/` prefix with OPNsense's: none is a firewall path |
| `internal/web/webtest/` | `/api/` | the web test support calls opnview's own HTTP API routes |
| `internal/widget/service.go` | `/api/` | the route prefix of opnview's own widget API |
| `cmd/opnview/review.go` | `/api/` | the review command reads opnview's own widget API |

The token list may only grow; an entry is added to the allow-list, and here, with its reason.

## Map of the tree

Every Go package directory of `cmd/` and `internal/`, with what it is for. A package directory
holds at most fifteen non-test source files, Go and SQL counted together, and a `doc.go` whose
package comment says the same thing at more length; `internal/layout` holds the tests that keep
this list, the limit and the comments true. Where a package is composed of parts (the store, the
collector), the parts are the directories below it and the package itself composes them.

- `cmd/opnview` — the single entry point: flags, wiring, signals, exit codes, and the `plugin test` and `measure` subcommands.
- `internal/auth` — password hashing and verification, account creation, sessions and the one-time setup token.
- `internal/broker` — the only path from opnview to the firewall API: every call, from the mirror or the firewall.
- `internal/broker/answer` — a firewall answer as the firewall wrote it, and which of its fields were read.
- `internal/broker/infer` — what a firewall answer is, learned from the answers themselves.
- `internal/broker/mirror` — the learned mirror of the firewall API.
- `internal/broker/schedule` — when each mirrored datum is refreshed, and in what order calls leave.
- `internal/buildinfo` — the few facts the program knows about itself.
- `internal/collect` — the collector: composes its parts, runs the probe round, orders the purge and lists the scheduler's tasks.
- `internal/collect/collecttest` — what the collector's tests share: the stopped clock and the probe harness.
- `internal/collect/core` — the state every part of the collector shares, and what every part uses.
- `internal/collect/derive` — the derivation every pass ends with: classification, pairing, attribution and the aggregate refresh.
- `internal/collect/discovery` — runtime discovery of the firewall's interfaces, rules, version and clock.
- `internal/collect/identity` — the arithmetic of the client identity cascade.
- `internal/collect/kinds/declared` — the coordinator of the kinds plugins declare, and of the reconciled state.
- `internal/collect/kinds/filterlog` — the coordinator of the firewall_log kind, pulled and pushed.
- `internal/collect/kinds/leases` — the coordinator of the dhcp_lease kind, and of the neighbour table.
- `internal/collect/kinds/lookups` — the coordinator of the dns_lookup kind.
- `internal/collect/kinds/measurements` — the coordinator of the measurement_sample kind, baseline and live.
- `internal/collect/kinds/resolverrecords` — the coordinators of the resolver_cache and resolver_local_data kinds.
- `internal/collect/kinds/securityevents` — the coordinator of the security_event kind.
- `internal/collect/provider` — every kind's declaration as data (`kinds/<kind>.json`: contract and attributes), the provider set every implementation is registered in, and the session an implementation is handed.
- `internal/collect/providertest` — the fake firewall every provider test runs against.
- `internal/collect/services` — the service kinds, geolocation and the registrable-domain list, as their providers last answered.
- `internal/config` — the only reader of configuration.
- `internal/dashboard` — the authoritative structural validator of the dashboard file format.
- `internal/dataset` — the data catalogue: every dataset, its columns, its filters and its queries.
- `internal/decode` — what an OPNsense endpoint actually said, turned into the shapes opnview reads.
- `internal/discover` — the generator, and the check, of the one file linking a family of directories into the binary.
- `internal/ianaprotocol` — the keywords of IANA's Assigned Internet Protocol Numbers.
- `internal/front` — the front's index: the shell, the assets served, the themes, the languages and the widget registry.
- `internal/front/frontjs` — runs the front's own files in the interpreter over a minimal document, for the front's tests.
- `internal/layout` — the tests that keep this map, the fifteen-file limit and the package comments true.
- `internal/live` — the live-interest lease.
- `internal/live/strip` — the rolling strip.
- `internal/measurecost` — the LV-C measuring tool.
- `internal/modularity` — the tests that keep the modular rule true: an implementation is a directory, never a hand-kept list.
- `internal/opnsense` — the only package that constructs an HTTP request: the client and the endpoints it may call.
- `internal/plugin` — the plugin host: discovery, loading and validation, the kinds' adapters, settings, and the calls a plugin's own tests make.
- `internal/plugin/contract` — the plugin manifest and the refusal codes.
- `internal/plugin/fetch` — the plugin host's facility for fetching over HTTP(S), and a plugin's files.
- `internal/plugin/harness` — the runner of a plugin's own tests, `opnview plugin test`'s and the Go suite's.
- `internal/plugin/listing` — what the API surfaces read of the loaded plugins.
- `internal/plugin/mmdbwrite` — small MaxMind DB files and their archives, for a plugin's tests.
- `internal/plugin/scripthost` — the bindings of a plugin's JavaScript to the host.
- `internal/push` — what the firewall sends to opnview: the listeners, the routing and the batching.
- `internal/push/ipfixregistry` — the IANA IPFIX Information Elements registry.
- `internal/push/protocols` — the generated link of every push protocol into the binary.
- `internal/push/protocols/datagram` — the `datagram` push protocol: one UDP datagram per message, decoded by a plugin's script.
- `internal/push/protocols/line` — the `line` push protocol: one newline-framed line per message, over TCP or UDP, decoded by a plugin's script.
- `internal/push/protocols/netflow` — the NetFlow push protocol, version 9 and IPFIX.
- `internal/push/protocols/syslog` — the syslog push protocol.
- `internal/secret` — the key file beside the database, and the authenticated encryption of the credentials.
- `internal/sizing` — a suggested page size and poll interval for each paged read, from a measured record rate.
- `internal/specialpurpose` — the IANA special-purpose address registries.
- `internal/store` — the store: opens the database, applies the schema and composes the parts below.
- `internal/store/accounts` — accounts, sessions, encrypted credentials and setting rows.
- `internal/store/aggregates` — the aggregate families and their refresh, and the service's downtime.
- `internal/store/attribution` — DNS resource records, the attribution of flows to names, hostnames and the record rate.
- `internal/store/dashboards` — the stored dashboards.
- `internal/store/flows` — the typed writes, classification, networks, this firewall's addresses and the pairing of legs.
- `internal/store/geo` — geolocation and autonomous-system rows.
- `internal/store/owners` — owners and their assignments, interface labels and blocklists.
- `internal/store/plugindecl` — the declaration of a table a plugin owns.
- `internal/store/plugintables` — the tables plugins own: ensured, written, read and purged.
- `internal/store/purge` — the retention purge.
- `internal/store/push` — the push sources' gaps, listeners and source periods.
- `internal/store/reads` — the read paths, on the read-only connections.
- `internal/store/samples` — measurement samples and the current rates read from them.
- `internal/store/sqldb` — the handle every part of the store runs on: pools, driver, statement registry, query check.
- `internal/store/storetest` — what the store's tests share: a database of its own for each test, and the test network.
- `internal/store/vocab` — the store's closed vocabularies and row types.
- `internal/suffixlist` — the Public Suffix List format, and a name's registrable domain.
- `internal/testtiming` — where the everyday suite's time goes, per package, recorded while a CI-only switch is on; test binaries hand it to the store (`store.Timing`), so the product binary does not link it.
- `internal/web` — the HTTP core: the server, the route table, sessions and CSRF, the string catalogue and the surfaces' seam.
- `internal/web/messages` — the catalogue keys the core chooses, and the keys composed from a registry row.
- `internal/web/settingfields` — the fields of the settings forms.
- `internal/web/surfaces` — the generated link of every surface into the binary.
- `internal/web/surfaces/account` — the surface that creates the first account and signs readers in and out.
- `internal/web/surfaces/collection` — the collection surface.
- `internal/web/surfaces/dashboards` — the API of the stored dashboards.
- `internal/web/surfaces/datasets` — the data catalogue's API.
- `internal/web/surfaces/events` — the event stream (Server-Sent Events) announcing what was stored, which the front re-asks widgets on.
- `internal/web/surfaces/firewallcatalogue` — the learned API catalogue's API.
- `internal/web/surfaces/frontend` — the shell, the front's assets, the string catalogue, the appearance and the widget registry.
- `internal/web/surfaces/interfaces` — the interfaces surface.
- `internal/web/surfaces/labels` — the JSON API of the labels interfaces are given and the purposes blocklists are given.
- `internal/web/surfaces/live` — the live figures that are not a widget's.
- `internal/web/surfaces/people` — the API of the people the user creates and their machines.
- `internal/web/surfaces/plugins` — the plugin listing and the plugin settings.
- `internal/web/surfaces/references` — the candidates of each reference kind, and the resolution of reference objects.
- `internal/web/surfaces/settings` — the settings surface, where credentials enter the product.
- `internal/web/surfaces/sources` — the sources surface.
- `internal/web/surfaces/widgetapi` — the widget API.
- `internal/web/webtest` — what the web surfaces' tests share: the harness, sessions, pages and paths.
- `internal/web/webtest/accounttest` — the account surface's test helpers.
- `internal/web/webtest/collectiontest` — the collection surface's test helpers.
- `internal/web/webtest/examples/hello` — the worked example of adding a surface.
- `internal/web/webtest/interfacestest` — the interfaces surface's test helpers.
- `internal/web/webtest/settingstest` — the settings surface's test helpers.
- `internal/web/webtest/sourcestest` — the sources surface's test helpers.
- `internal/widget` — the widget API: one read-only endpoint per served preset, the loaders of widget directories, and the answer.
- `internal/widget/datasets` — the data catalogue served to widgets.
- `internal/widget/envelope` — the response every widget answers with.
- `internal/widget/figures` — the figures several widgets share: sites, attribution, rates, samples, peers.
- `internal/widget/hostareas/installation` — the script host's areas reading the installation's own state: interfaces, rates, samples, datasets and the widget's statements.
- `internal/widget/hostareas/traffic` — the script host's areas reading the logged traffic of a window: flows, volumes, series, sites, attribution, addresses and peers.
- `internal/widget/hostareas/values` — the script host's areas that work on values a script holds.
- `internal/widget/params` — a widget's manifest and parameters.
- `internal/widget/request` — one widget request being served.
- `internal/widget/script` — the running of a widget's JavaScript.
- `internal/widget/shapes` — what a shape draws from declared rows.
- `internal/widget/traffic` — the logged traffic widgets read.
- `internal/widget/widgets` — the link of the built-in widgets, and their tests.

## What this is not

`opnview` is one Go binary. A kind is implemented by a **plugin**: a directory holding a
manifest and, where the manifest cannot say it, a JavaScript script the binary's embedded
interpreter runs (`docs/plugins.md`). The built-in plugins are embedded in the binary; a plugin
dropped into the data directory is read at start. There is still **no dynamic loading of code**:
no shared object, no external process, no Go file in a plugin directory. The registry described
above is a table of rows saying which implementations exist and which one is read -- data about
providers, not a mechanism for loading them.
