# 0017 — The package-by-package verdict on the existing Go

Status: Accepted
Proposed by the orchestrator, 18 October 2026, from five read-only audits of the code (no document was read); the three questions at the end decided by the maintainer the same day

**The maintainer's decisions of 18 October 2026:**

1. **The learned mirror stays, as ADR 0004 decides it.** In his words: it is a
   central feature, so that an API is not spammed for information already held.
   The broker rows marked DECIDE below are KEEP.
2. **The write primitive is built first**: declared write statements, views and
   SQL functions, shared tables (see the store section).
3. **The order of the cut** is the proposed one: the DELETE column, then the
   write primitive, then one source at a time starting with pf, then the widget
   semantics to a JavaScript library, then the surfaces. Each step on a `wip/`
   branch, validated on his firewall before `main`.

## Context

ADR 0002 says the Go is the broker, the plugin loader and host, the scheduler,
and the glue that cannot be elsewhere. On 18 October 2026 the non-test Go was
85 000 lines. This record lists every package with what it does, who depends on
it, and one of three verdicts: **KEEP** (it is core), **MOVE** (it becomes a
plugin's JavaScript and SQL, and the core gains at most a generic primitive),
**DELETE** (nothing needs it). The maintainer accepts, amends or refuses each
line; nothing is cut before. Line counts are non-test Go.

**Estimate if every verdict below is accepted:** about 27 000 lines of Go
remain (broker 2 300, scheduler 850, loader and host 7 500, harness 1 850, push
hub and decoders 4 300, store glue 4 500, dataset 1 800, web glue 4 000, widget
kernel 2 300, front 1 200, accounts/secret/config/opnsense 2 600), and about
58 000 lines leave, most of them to plugins' SQL and JavaScript.

## What the plugins depend on today (the contract that must survive a move)

Host functions called by the 42 plugins' JavaScript, with the Go that answers
them. A MOVE of that Go must keep the function or replace it by `host.query`
over the plugin's own SQL.

| Host function | Uses | Implemented by | Verdict on the implementation |
|---|---|---|---|
| `host.call`, `host.stream`, `host.events`, `host.serviceState`, `host.readObject`, `host.readCollection` | 40 | broker, provider contract | KEEP |
| `host.query.rows/row`, `host.query(sql)` | all widgets | store/reads (widgetrows, queryreads) | KEEP |
| `host.params.*`, `host.refs.*`, `host.request.*`, `host.envelope.*`, `host.json.*` | 200+ | widget/params, request, envelope | KEEP (generic part) |
| `host.datasets.latest/rows` | 5 | dataset | KEEP |
| `host.settings`, `host.secret_states`, `host.fetch`, `host.files.*` | 20 | plugin/settings, plugin/fetch | KEEP |
| `host.figures.*` | 150+ | widget/hostareas/values | MOVE to a JS helper library plugins `require` |
| `host.volume.*`, `host.sites.*`, `host.peers.*`, `host.series.*`, `host.clients.*`, `host.attribution.*`, `host.flows.*`, `host.samples.*`, `host.rates.*` | 110 | widget/figures, widget/traffic, store/reads (read.go), store/samples | MOVE: each becomes a `widget_*` statement of the plugin that draws it, plus JS |
| `host.installation.*` | 26 | widget/request/installation.go | MOVE to shared SQL read through `host.query` |
| `host.mapfilters.*`, `host.matrixplacement.*`, `host.periods.*`, `host.buckets.*` | 20 | widget/shapes, hostareas/values | MOVE to JS |
| `host.specialpurpose.classify`, `opnview.protocolNumber` | 4 | specialpurpose, ianaprotocol | MOVE to a JSON/CSV table read by JS |
| `host.services.registrable/geolocation` | 7 | suffixlist, store/geo | KEEP suffixlist as a host helper; geo table becomes the maxmind plugin's dataset |
| `host.catalogue()` | 0 | broker/catalogue | SIMPLIFY with the mirror |

No plugin references any opnview HTTP route; only the front does.

## Broker and scheduler (`internal/broker`, 7 782)

| Part | Lines | Verdict | Why |
|---|---|---|---|
| broker.go, readonly.go, gate.go, versions.go, settings.go, pages.go, streams.go (subscription) | 2 300 | KEEP | The broker proper: one call per datum, read-only rule, served_from/age, SSE fan-out. |
| schedule/schedule.go | 706 | KEEP | The scheduler of ADR 0005, already as specified: deadlines, 70 % reserve, 2 in flight, 5/s, backoff. |
| mirror/, infer/, answer/, recording.go, datasets.go, catalogue.go, and `observed.go` + the rerun loop in scripthost | 5 100 | KEEP (maintainer, 18 October 2026) | The learned mirror of ADR 0004, as built: learned tables per endpoint, JSONB promotion, per-field read tracking, reruns of a script when a field is missing. The audit noted its cost (26 % of the two core packages); the maintainer confirmed it is a central feature: an API is not called again for information already held. |

## Loader and host (`internal/plugin`, 11 498)

| Part | Lines | Verdict | Why |
|---|---|---|---|
| loader.go, contract/, implementation.go, live.go, settings.go, brokered.go, versions.go, listing/ | 3 700 | KEEP, shrink | The loader. About 40 % of loader.go registers widgets, measures, datasets and pages; it stays only for what survives below. |
| scripthost/ (goja embedding, host object, conversions) | 2 300 | KEEP | The JS host. Engine: goja; one runtime per call, 2 min interrupt. |
| fetch/ (HTTP to declared hosts, files, MMDB and PSL readers) | 720 | KEEP | Host facility of ADR 0007. The MMDB/PSL helpers are product-specific and named as such. |
| harness/, harnesscalls.go, mmdbwrite/ | 1 850 | KEEP | `opnview plugin test`: fake firewall, fresh database, fake web. |
| push.go, pushkind.go, pushsink.go | 1 304 | MOVE to `internal/push` | Receiving is a source concern; only the JS parse call belongs to the host. |
| derivation.go, measures.go, datasetaccess.go | 855 | KEEP the derivation loop as the generic "run this exported function on this cadence or notification"; MOVE the measure registry with the measures | A derivation is the primitive plugins need to own classification and attribution (see store). |
| declarative.go | 431 | DELETE | A second provider language beside JavaScript. |

## Collection (`internal/collect`, 10 172 + 1 900 lines of kind JSON)

| Part | Lines | Verdict | Why |
|---|---|---|---|
| core/tasks.go (the loop runner) + root scheduler.go, derivations.go, notifications.go, heartbeat.go + core/brokered.go, clock, availability | 850 | KEEP | The pass scheduler: one goroutine per task, run-at-start, wake channel, min gap. 138 lines are the runner; the rest is the task list, which shrinks to "one loop per (plugin, function, cadence)". |
| provider/ (the kind contract, `Session`, `Host`, registries) | 1 974 | KEEP | The loader's contract. The 12 kind JSON files move under `plugins/` as data. |
| kinds/declared | 279 | KEEP | The one generic pass for plugin-declared kinds. |
| kinds/filterlog, leases, lookups, neighbours, resolverrecords, securityevents, measurements | 2 835 | MOVE | Per-kind coordinators: "read answer fields, store rows, record gap". Each becomes the owning plugin's JS and SQL (pf, kea/isc/dnsmasq, unbound, neighbours, suricata, insight). |
| derive/ + identity/ | 424 | MOVE / DELETE | The built-in derivation sequence (classify, pair, attribute, refresh) becomes plugin derivations; identity is two wrappers. |
| discovery/ | 507 | MOVE | Reads five OPNsense endpoints, classifies interfaces and rules. A plugin of its own; the broker keeps a vocabulary/version hook. |
| services/ | 428 | MOVE | The geolocation and PSL refresh loops: plugin derivations on a cadence. |
| core/sources.go (DecideActive), hostnamesources.go, pushhandlers.go | 500 | MOVE | Activation policy ("two exclusive sources → neither") is SQL/JS over the provider registry. |
| collecttest/, providertest/, purgeconcurrency/ | 1 618 | DELETE | Test helpers of the coordinators. |

## Push (`internal/push`, 4 226) and small packages

| Package | Lines | Verdict | Why |
|---|---|---|---|
| push (hub, listeners, queues, gaps, liveness) | 2 055 | KEEP | Generic inbound glue; names no product. |
| push/protocols (NetFlow v9 / IPFIX, syslog RFC 5424/3164/6587, line, datagram) + ipfixregistry | 2 170 | KEEP | RFC decoders over IANA data, like a JSON parser. Verified: no OPNsense logic inside. |
| decode: field.go, mac.go | 416 | KEEP | JSON tolerance used by broker and host. |
| decode: timestamp.go, uptime.go | 280 | MOVE | OPNsense-measured timestamp shapes (filter log, `systemTime`), BSD uptime: JS helpers of pf and insight. |
| discover (go generate for protocol dirs) | 296 | KEEP while protocols are Go directories | |
| suffixlist | 335 | KEEP | Host helper (PSL algorithm); JS would cost per-request speed. |
| live | 101 | KEEP | The live-interest lease; no dependency. |
| measurecost + `measure-cost` command | 546 | DELETE | Development measuring tool. |
| specialpurpose, ianaprotocol | 206 | MOVE | IANA tables as JSON/CSV read by JS. |
| sizing | 418 | DELETE with the collection surface | Pure arithmetic for one surface. |
| testtiming + storetest/timing.go | 112 | DELETE | Timing instrumentation of the deleted CI tool. |
| modularity (tests only) | 4 + 928 test | KEEP | The mechanical guard of ADR 0001 and 0002. |
| buildinfo, front.go, plugins.go | 99 | KEEP | Embed glue. |

## Store (`internal/store`, 17 043 + 7 394 lines of SQL) and dataset

| Part | Lines | Verdict | Why |
|---|---|---|---|
| store root: open, wait, collation and telemetry refusal, advisory lock, schema apply, registries | 450 | KEEP | The database connection of ADR 0008. |
| store/testdatabase.go | 602 | MOVE beside storetest | Test infrastructure compiled into the product package. |
| sqldb (pool, named statements, `:name` translation, read role, LISTEN/NOTIFY, plan check) | 1 669 | KEEP | Generic; names no product. |
| plugindecl + plugintables (declared tables, hypertables, aside, insert, pass, derivation write, purge) | 1 576 | KEEP, extend | The plugin-table primitive. **Missing, and needed for every MOVE below**: (1) a declared **write statement** (a `*.statements.sql` the manifest lists, run in one transaction on the plugin's schema, on a cadence or a notification); (2) plugin-declared **views and SQL functions** (today `classified_flow`, `blocked_decision`, `canonical_address()` live in `schema.sql` and 21 plugin files read them); (3) a way for a plugin to declare a table other plugins join (`flow`, `client`, `interface`, `rule`, `provider` are read by 38 to 44 plugin files each). About 300 to 500 lines by analogy with the derivation writer. |
| vocab: measure, samplemetric, kinds, period | 750 | KEEP | Loader registries filled from manifests. |
| vocab: rows.go, outcome.go | 900 | MOVE | The Go structs of every core kind's row, pf's reason list, Unbound's answer sources, iftop's 2 s. |
| reads: widgetrows, catalogue, cells, queryreads | 645 | KEEP | "Run a declared read statement on the read role": what `host.query` is. |
| reads: read.go, surfaces.go | 1 150 | MOVE | Volume windows, attribution rate, interface clients, rule logging, public addresses: `widget_*` statements of the plugins that draw them. |
| flows (every core-kind INSERT, classification, leg pairing, This-Firewall, interface networks, rule info) | 2 887 + 802 SQL | MOVE | pf owns `flow`, pairing and classification as derivations; kea/isc/dnsmasq own `dhcp_lease`; suricata `security_event`; unbound `dns_resolution`. Needs primitives (1) to (3). |
| attribution (Unbound cache attribution, CNAME chasing, dual-stack rule, record rate) | 1 639 + 309 SQL | MOVE | A derivation of the unbound plugin. |
| owners (people, assignments, labels, blocklist purposes) | 843 + 199 SQL | MOVE | A user-input feature; needs the write primitive and a plugin-declared HTTP surface (see web). |
| aggregates: aggregate.go (24 rollup tables) | 391 + 608 SQL | MOVE or DELETE | Only one plugin reads the rollups; every traffic widget reads raw flows. |
| aggregates: downtime.go | 163 | KEEP | Heartbeat record, the scheduler's. |
| samples (traffic/top rate semantics) | 656 | MOVE | SQL and JS of the plugin providing `measurement_sample`. |
| accounts, dashboards, push | 898 | KEEP | API glue of ADR 0006 and 0016, push liveness. |
| purge: runner / purge.sql | 127 / 642 SQL | KEEP runner; purge.sql empties as tables become plugin-owned | |
| geo | 129 | MOVE | The maxmind plugin's table and dataset. |
| schema.sql | 4 495 SQL | SPLIT | One file declaring every product table; each table goes to its owning plugin's manifest. No core table is a hypertable today. |
| dataset (catalogue, grammar, compiler, latest, rows) | 1 818 | KEEP | ADR 0011's engine. The 12 `core/` datasets move to the plugins owning their tables. |

## Widgets and front (`internal/widget` 14 137, `internal/front` 1 760, dashboard 315)

Facts: all 30 built-in widgets are script widgets with their own SQL and
renderer; 0 use the declarative shape engine; no renderer reaches a core shape
renderer. A generic path "declared statements → JS transform → JSON" is 1 800
to 2 300 lines of Go.

| Part | Lines | Verdict | Why |
|---|---|---|---|
| widget root: service, pluginwidget (validate, compile, register and plan-check statements), renderer check | 1 180 | KEEP | The widget loader and route. |
| widget/loader.go (operator declarative widgets) | 207 | DELETE | With shapes. |
| script: script.go, host.go | 555 | KEEP | Fresh goja runtime per request, 20 s bound. |
| script: script_flat, script_hostobject, script_json, script_jsonvalue | 1 533 | DELETE | Speed and golden-parity machinery; accept `JSON.stringify`. |
| request: request.go, since.go | 750 | KEEP | The per-request envelope. |
| request: measure.go, speculation.go (runs a widget twice, NetFlow vs logged) | 814 | MOVE | A policy about one source; the widget's own JS. |
| request: installation.go | 321 | MOVE | Shared SQL read through `host.query`. |
| params: manifest, validate, params, providerkinds | 925 | KEEP | Parameter parsing against the manifest. |
| params: refs, candidates, resolveobject | 548 | MOVE or KEEP as `/api/references` glue | |
| params: series.go | 321 | MOVE | Exists for `custom_chart` alone. |
| envelope | 402 | KEEP (250), MOVE the figure structs | The API contract. |
| shapes (declarative engine, 12 drawers) | 1 133 | DELETE | Zero widgets use it. |
| figures, traffic (+ 707 SQL) | 2 007 | MOVE | The product semantics: sites, peers, rates, directions, bins. SQL to the drawing plugins, aggregation to a JS library. |
| hostareas | 2 652 | KEEP 700 (query, datasets, params, refs, request, envelope, json, settings); MOVE 1 950 | |
| datasets | 749 | KEEP, fold into `internal/dataset` | `datasetseries.go` moves with series. |
| front (asset index, themes, languages, renderer delivery, `/api/widgets`) | 1 170 | KEEP | "Serve what plugins declare." |
| front/frontjs | 590 | DELETE | Harness of the deleted front tests. |
| dashboard (JSON validator) | 315 | KEEP | Small, pure. |

## Web, config, auth, cmd (`internal/web` 9 057, others 4 500)

| Part | Lines | Verdict | Why |
|---|---|---|---|
| web core (server, middleware, CSRF, sessions, string catalogue) | 1 620 | KEEP | The JSON API server. |
| surfaces: account, settings, plugins, widgetapi, datasets, firewallcatalogue, frontend, events, dashboards | 2 690 | KEEP | Accounts, encrypted credentials, what plugins declare, the front's persistence. `events` hard-codes four channel names; a verbatim relay would be 150 lines. |
| surfaces: collection | 1 160 | DELETE with the Go collectors | Every line is knowledge of them; their settings become plugin settings. |
| surfaces: live | 212 | MOVE | The insight plugin's settings. |
| surfaces: sources | 399 | MOVE | A widget over store tables. |
| surfaces: interfaces, labels, people | 1 106 | MOVE | Store CRUD. **Blocker**: plugins cannot declare a write endpoint today; this needs a primitive (a plugin-declared HTTP surface, or writes through declared statements) before these move. |
| surfaces: references | 239 | MOVE with widget/params refs | |
| webtest | 1 530 | DELETE or keep as test support | Not in the binary. |
| config: credentials, live, corekeys, appearance | 340 | KEEP | |
| config: config.go (collector settings), liverate.go | 634 | MOVE with the collectors | Names the insight plugin. |
| auth, secret, buildinfo | 767 | KEEP | |
| opnsense (client, outcomes, TLS pinning, SSE reader, endpoint admission) | 1 552 | KEEP | The broker's transport. Five source-specific compiled endpoints in `endpoints.go` are re-homed to the plugins that already declare the same paths. |
| cmd/opnview: serve, `plugin test`, push wiring | 770 | KEEP | |
| cmd/opnview: measurecmd.go, review.go | 377 | DELETE | Development tooling. |

## The three questions, as put to the maintainer and answered (see the top)

1. **The learned mirror** (5 100 lines): kept.
2. **The write primitive** for plugins: declared write statements, views and
   functions, and shared tables. Built first; without it the store, collection
   and the three CRUD surfaces cannot move.
3. **Order of the cut:** (a) the DELETE column first (about 6 000 lines, no
   plugin loses anything); (b) the write primitive; (c) one source at a time,
   pf first, each validated live; (d) the widget semantics to a JS library;
   (e) the surfaces.

## Progress of the cut

| Date | Step | Done |
|---|---|---|
| 18 October 2026 | (a) DELETE, first part | `measure-cost` command and `internal/measurecost`, the `review` build, `internal/front/frontjs`, `internal/testtiming` and the store's timing seam, `internal/layout`, the settings reader of the deleted command; the modularity allow-list shrunk by two entries. Suite green in 7 s. |
| 18 October 2026 | (b) the write primitive | ADR 0018 built: per-plugin PostgreSQL role, declared write statements planned under it at load, SQL-only derivations, `statements` returned by script derivations, relation files (views and functions), `requires` and the load order, `after_derivations`, `t.query` and `t.derive` in the harness, the worked examples `example_derived_sql` and `example_requires` with no JavaScript, plugin API version 3. |
| 18 October 2026 | (c) preparation: granted writes | ADR 0019 accepted; `writable_by` built: column-level and table-level grants on another plugin's role, re-granted and revoked at every start, refused by name when a granted plugin's statement steps outside. The owners of `flow`, `client` and the classification are decided (installation, pf, clients, leases); the move itself follows, one plugin per commit, validated by the diff on a copy of the live database. |
| 18 October 2026 | (c) preparation: the table declaration | Extended so a plugin table can be what `flow` and `client` are: identity, generated columns, checks, foreign keys to a required plugin's table, partial indexes. Three tests. |
| | (a) DELETE, remaining | `widget/shapes` and `widget/loader.go` (needs `host.mapfilters` and `host.matrixplacement` moved to JavaScript first: 10 plugin uses); the script flat/hostobject/json machinery (changes JSON formatting, the plugins' golden tests must be re-recorded); `plugin/declarative.go` (used by three worked examples under `docs/examples/`, which go with it); `surfaces/collection` and `sizing` (with the Go collectors, step c). |

## Consequences

Each line of this record becomes one change on a `wip/` branch,
validated on the maintainer's firewall before `main`. The host-function table
above is the regression list for every move.
