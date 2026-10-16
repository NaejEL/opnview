# SPEC — Backend completion: JavaScript plugins for every source, service and widget, then owners, unnamed destinations, live rate, ports, the mockup audit, a readable core layout and live validation

Status: APPROVED
Cycle kind: standard

**Why `standard`.** This cycle writes Go, SQL, JSON contracts, documents and **server-side** JavaScript. That JavaScript runs inside the binary, in the plugin interpreter. It reads the firewall, other services or the database, and produces records or JSON. None of it is shipped to a browser. The cycle writes no HTML, CSS, browser JavaScript, template, theme or catalogue string the interface renders, and it changes no rendered output (AC X3 checks this byte for byte). Whatever a user will eventually see (the plugin list, plugin settings, the People page, the live strip, every settings page) is served as JSON and rendered by the step-7 frontend. Where this spec touches a binding interface document (`docs/widget-catalogue.md`), it amends a data contract by name and changes no aesthetic rule.

## Context

**The maintainer's requirement, in his words (translated from French):** "We finish the backend, everything except the Suricata plugin, before attacking the frontend."

**His corrections and decisions of 10 October 2026, which now shape the whole cycle:**

1. **Order.** Plugins come first. Then the gaps. Then live validation on his firewall. Then the commit. Nothing is written twice.
2. **Plugins are handled like widgets, with no gas factory.**
   - A third party drops a directory into `<data-dir>/plugins/<name>/`. Nobody forks, opens a PR, recompiles or merges anything.
   - There is no sandbox, no allowlist and no activation step: "if people install junk that breaks their firewall, that is not my problem".
   - A plugin that fails to load is logged with its reason and skipped. The built-in wins on a name collision.
   - POST read endpoints are allowed. Firewall calls go through opnview's client, whose refusal of mutating commands is unchanged, with nothing added on top.
3. **A plugin carries its own data schema.** It is not limited to the existing kinds ("I won't guess every use case"). Mapping to an existing kind stays possible.
4. **Every built-in source is rewritten as a JavaScript plugin.** Only the core stays in Go: store, classification, aggregation, the widget API engine, web, the plugin host and the OPNsense client.
   - Behaviour stays identical.
   - The existing tests are the behavioural spec. They run against the plugins, and none is weakened.
5. **Widgets are plugins too,** in the same format and the same directory model. The 26 built-in widgets' Go hooks are rewritten in that format. Their JSON stays byte-identical, and the shared shape engine stays in the Go core. One plugin directory may provide a source, a widget, or both.
6. **Every external service is a plugin kind. The Go core talks only to OPNsense.**
   - MaxMind and the Public Suffix List become built-in plugins of service kinds. Their downloads are made by the plugins themselves, through a facility of the plugin host, never by a Go package.
   - The core's outbound rule becomes "the firewall, nothing else". The built-in plugins' downloads belong to those plugins. A third-party plugin's destinations are its author's business.
7. **Live rate.**
   - A continuous, persisted baseline sampler.
   - The finest cadence the firewall sustains while the interface is open, measured on the live device as the finest cadence adding at most 2 processor percentage points.
   - Fine samples are persisted with a short retention. There are no gaps.
   - The pf state table is used for rate by port and protocol if it verifies against `opnsense/core` 26.7.3 and its cost is acceptable.
   - The live part of a response has a median budget of 100 ms at the 1 000 000-row seed.
8. **The rest.**
   - `top_sites` and `sites_by_client` stay withheld whole under `no_domains`.
   - The mockup's `traffic_composition` keys `group_by: blocking_reason`, `split_by: direction` and `window_seconds`, and `line_interpolation`, are adopted. `destinations_per_owner` is not.
   - Canvas and dashboard persistence get a separate spec before step 7.
   - An assigned client is kept beyond retention.
   - Undoing a person's deletion restores the original `owner_assigned_at`.
9. **The pre-deployment suite runs in under 3 minutes,** enforced like the 60 s everyday budget.
10. **A readable Go core layout.**
    - `internal/store` holds 77 files flat, `internal/collect` 50, `internal/widget` 41 and `internal/web/webtest` 18.
    - The maintainer: "we have folders with dozens of files in them; nobody can understand anything".
    - Packages are split by responsibility, with an enforced rule. This is a move after the plugin work.
11. **The frontend is a separate JavaScript application** (like the step-3 mockup), embedded in the binary and built in step 7. In the end `internal/web` holds only the JSON API, authentication and static-file serving.
    - The Go-rendered pages stay in service until step 7 replaces them.
    - This cycle invests nothing in them, and exposes every capability as JSON.
12. **Process.**
    - Test suites run once, when the work is ready to commit.
    - Only a blocking defect sends work back: wrong behaviour, a broken binding rule, or a data leak.
    - One verifier, no ergonomist.

**`CLAUDE.md`, binding, quoted:**

> "Modular at every layer, not only the frontend. A kind of thing is a seam and an implementation is a file behind it, selected at runtime by what the installation has … Adding a second implementation is adding a file and a registry row and touching nothing else."

> "Somebody who has never read the source must be able to add any of the three easily and naturally, from the documentation alone; if adding one means editing a file that ships with the product, the rule is not met."

**Where the repository stands (read for this revision).**

- `main` is at `a1e2707`. The working tree holds two uncommitted pieces of work, neither validated live:
  - step 5B (`specs/SPEC-step-5b-widget-api.md`, APPROVED);
  - the restructuring that split providers into `internal/collect/<kind>/<impl>/` with a `provider.json` each, linked by a generated `internal/collect/providers_gen.go` (`go generate`).
- **Twelve compiled provider implementations:**
  - `firewalllog/pf`;
  - `securityevent/suricata`;
  - `dhcplease/{kea,dnsmasq,isc}`;
  - `dnslookup/{unbound,dnsmasq}`;
  - `resolvercache/{unbound,dnsmasq}`;
  - `resolverlocaldata/{unbound,dnsmasq}`;
  - `measurement/insight`.

  Two shared product helpers sit beside them: `unboundctl` and `dnsmasqctl`.
- **The port a provider is given is `provider.Session`** (`internal/collect/provider/session.go`). It grants:
  - `Call` and `Stream` to the firewall;
  - `ServiceState`, `ReadObject` and `ReadCollection`;
  - `Normaliser`, the year-less timestamp normaliser with the firewall's measured UTC offset;
  - `Now`.

  It deliberately withholds the store, discovery, availability writes and activation. Each kind's `Source` interface says what an implementation returns:
  - `Probe` returning a `Result` with state, outcome, probe, detail and `Separable`;
  - `Records`, `Leases`, `Lookups` (with `PageBound` and `RequestedSpanSeconds`), `Cache`, `LocalData`, `Rotation` with `Alerts`, and `Sample`, which is handed the discovery snapshot.

  Cursors, gap detection, identity, pairing and derivation are the kind coordinators' business, in `internal/collect/*.go`.
- **Widgets.** 26 built-in widgets live in `internal/widget/widgets/<type>/`, each with `manifest.json`, `.sql` and a Go hook (`RegisterHook`), linked by a generated `hooks_gen.go`.
  - Hooks read through shared helpers of `internal/widget`: `SiteRows`, `RegistrableGrouping`, `SiteAccumulator`, attribution, buckets, series, rates, chips and peers.
  - Operator widgets in `<data-dir>/widgets/<type>/` are declarative only (manifest plus query).
  - Operator queries are checked to be a single read statement (`internal/store/querycheck.go`). Nothing refuses a query reading `account`, `session`, `encrypted_credential` or `setting`.
- **External services are compiled Go network clients:**
  - `internal/maxmind`: GeoLite2 download, refresh, MMDB read, and `Locator` placing flow addresses;
  - `internal/publicsuffix`: list download, conditional refresh, the held previous good copy, and grouping.

  `internal/opnsense/source_policy_test.go` admits exactly three packages building HTTP requests. `provider/contract.go` marks `geo_asn` and `public_suffix` as core-only kinds.
- **Web.** The Go-rendered surfaces are `account`, `settings`, `collection`, `interfaces` and `sources`, plus `widgetapi` (JSON), in `internal/web/surfaces/<name>/`, linked by `surfaces_gen.go`. Provider display strings live in the shipped `internal/web/locale/en.json`, for example `provider.dns_lookup.unbound`.
- **What a builder already added from the previous text of this spec,** with this spec's verdict on each:
  - **`github.com/dop251/goja` in `go.mod`**, with `regexp2`, `go-sourcemap` and `pprof` beside it. *Compatible*: it is the embedded pure-Go interpreter the maintainer named. It is listed `// indirect`; once imported it must be a direct requirement, and its transitive audit recorded (AC A14).
  - **`internal/opnsense/plugin_endpoints.go`** checks a declared endpoint at load: path shape, `/api/` prefix, no `..`, `MutatingCommand`, GET or POST only, and no conflict with a compiled entry. *Compatible.* Declaring the endpoints a plugin reads is the client's existing rule ("a path that is not in the registry cannot be issued"), moved to load time. It is not an operator allowlist: nobody approves anything.
  - **`internal/store/plugintables.go`** provides namespaced tables `plugin_<plugin>__<table>`, `CREATE … IF NOT EXISTS` at load, one-transaction `WritePluginPass` with `plugin_state`, and per-table retention in the purge. *Compatible, with one defect to change:*
    - on a schema change it refuses to load the plugin (`ErrPluginTableChanged`), so an updated plugin stops working until somebody edits the database by hand;
    - it compares only column names and types, so a change of nullability, primary key or index goes undetected.

    Section A4 replaces this.
  - **`internal/collect/provider/contract.go`** holds every core kind's record contract as data, the functions a script exports per kind, and `DeclareKind` for a plugin's own kind. *Compatible*, except `coreOnlyKinds`: `geo_asn` and `public_suffix` become plugin kinds (B2). `flow_volume` stays core-only, being derived.
  - **`internal/collect/pluginkind`** and **`internal/collect/reconciledstate`**. *Compatible.*
  - **`schema.sql`** gained:
    - `plugin_state`;
    - the outcomes `script_failed`, `script_timeout` and `records_invalid`;
    - `state_since_after_at` / `state_since_by_at` on `source_availability`;
    - a kind `CHECK` reduced to snake_case shape.

    *Compatible.*
- **Leftovers:**
  - `.claude/worktrees/agent-*` (six directories) are still present in the tree;
  - `docs/extending.md` describes `go generate`;
  - `docs/architecture.md`, *What this is not*, still says "There is no plugin system".

**Other backend gaps, established earlier and re-checked.**

1. **Owners.**
   - `client.owner_id` / `owner_assigned_at` and `owner (display_name UNIQUE)` exist, and nothing writes them.
   - `interface.user_label` and `blocklist.purpose` have no writer either.
   - No endpoint lists every client.
2. **Unnamed destinations.**
   - `top_sites` and `sites_by_client` omit them.
   - `connection_tree` lumps them under `site:none`.
   - About 34 % of live connections are named.
   - ROADMAP step 5: "Never invent a domain: fall back to IP, country and operator."
3. **Live rate.**
   - Rates come from `measurement_sample` at 300 s.
   - The sources are `traffic/top` (iftop for about 2 s per interface) and `traffic/interface` counters.
   - The mockup strip ticks every 2 s.
4. **Ports and protocols.**
   - `service_port` slices carry the port without the protocol.
   - The `connection_tree` "service port and protocol" level is not served.
5. **Mockup coverage.** Rows 4, 8 and 39 of `docs/widget-api.md`, *Coverage of the mockup*, are **cannot**, and there are declared departures.
6. **Nothing from 5B, the resolver-cache cycle or the 5A corrections has run on the maintainer's firewall.**

## Scope

The work is done in this order. A later phase never starts before the earlier one passes its own tests. The suites run once, at the end (*Rules*).

**Phase A.** The plugin host and format.
**Phase B.** Every built-in rewritten as a plugin: sources, services and widgets, plus the hard-wiring audit.
**Phase C.** The gaps: live rate and pf states as plugins; owners, unnamed destinations, ports and the JSON settings API in the core; the mockup audit.
**Phase D.** The core layout move.
**Phase E.** The pre-deployment budget.

Then `checks` and `pre-deployment` run once, then verification, then live validation, then the commit.

**Baseline snapshot.** Before Phase B removes any Go implementation, the orchestrator records a read-only snapshot of the working tree outside the repository. That snapshot is the "before" of every byte-identity and test-preservation criterion.

- It is taken with no git command that moves or discards work.
- A test or hook file the in-flight builder has already altered is compared with its version in the git index, and the builder lists every such file.

### Phase A: the plugin host

**A1. What a plugin is.**

- A plugin is a directory holding a manifest and the files it names:
  - JavaScript scripts;
  - SQL query files;
  - string files (catalogue keys with English source text);
  - optionally `testdata/`.
- **Locations:**
  - third party: `<data-dir>/plugins/<name>/`;
  - built-in: a repository directory embedded in the binary (proposed: `plugins/<name>/` at the repository root; the Builder states the path in `docs/plugins.md`).
- **Both go through the same loader and the same validator.**
- **What a plugin provides.** One plugin may provide any combination of:
  - **providers**, one or more, each of a kind: a core kind with a record contract, a service kind (B2), or a kind the plugin declares with its own tables (A4);
  - **widgets**, one or more (B3);
  - **tables** it owns (A4);
  - **settings** it declares (A6).
- **Product-centric grouping is expected.** For example, one `unbound` plugin provides `dns_lookup`, `resolver_cache` and `resolver_local_data`. Provider identity stays `(kind, provider_key)` as today.
- **The manifest declares:**
  - its name, a snake_case word;
  - its version;
  - the plugin API version it targets;
  - its providers, each with:
    - kind, `provider_key` and display-name catalogue key;
    - the firewall endpoints it reads (path, method, body encoding, argument kind, stream flag, and for built-ins the survey citation);
    - its cadence and lower bound where the kind has no core cadence;
    - optionally a live pass (C3);
  - its other outbound destinations, if any (A7);
  - its tables (A4) and its settings (A6);
  - its widgets (B3).
- **Format.** JSON and/or YAML, the Builder's choice, stated in `docs/plugins.md`. Its schema is versioned with the plugin API.

**A2. The interpreter.**

- Scripts run in the embedded pure-Go interpreter already in `go.mod` (goja). It needs no cgo, no compilation step and no binary per architecture.
- **Failure isolation, not a sandbox.** These are robustness, not security against the plugin:
  - a script exception, a Go panic inside a host function, or an exceeded per-call time bound becomes a closed outcome (`script_failed`, `script_timeout`) on that provider's availability row, or a condition on that widget's response;
  - the service, the kind's other providers and other plugins carry on.
- Records failing their contract are refused whole (`records_invalid`). Nothing of that answer is stored.
- **What the host does not grant.** No filesystem, network, environment or process access is granted beyond the host functions of A3, A6 and A7. The interpreter offers none by itself. No further isolation is built.

**A3. The host API a source provider sees.** It is derived from `provider.Session` and the kind contracts. Each item names the Go capability it replaces:

- **Firewall calls.**
  - The capabilities: one call (with form or JSON body, pagination and positional path arguments); read an object; read a collection under named wrapper keys; probe a service's status; read a bounded event stream.
  - Each answer carries the closed outcome, the status code and the detail the client classified today (`ResponseOutcome`).
  - Calls go only to the configured firewall base URL, only for endpoints the manifest declares, and through the existing client and its `MutatingCommand` refusal, unchanged.
- **Time.**
  - The current instant as the UTC epoch.
  - The timestamp normaliser: the year-less shape with the firewall's measured offset. The measurement itself stays core.
- **Discovery snapshot.** The interface identifiers and devices, handed as an argument exactly where the kind contract says so: the `measurement_sample` sample and live passes, and own-kind collect passes.
- **Kind settings.** The operator settings a kind passes today (page size, `page_bound`) arrive as call arguments, as now.
- **Record emission.**
  - For a core kind, the records of its contract (`contract.go`). The host validates them, and the core coordinator does what it does today: identity, gap detection, pairing, classification, attribution, aggregation.
  - For an own kind, rows for its own tables (A4).
- **Availability.** Every function returns a `result`: state, closed outcome, probe and detail, and `separable`. That is the plugin's report. The host writes availability; the plugin never does.
- **Durable state** (cursors and watermarks) as key/value pairs, read at the start of a pass. They are committed in the same transaction as the pass's rows, so a restart neither loses nor repeats a record.
- **Gaps.**
  - For core kinds, gap detection stays in the coordinator, unchanged.
  - An own-kind provider may report a gap interval with a reason code its manifest declares. It is stored with the provider, distinguishable from core reasons, and returned in the API's gap listings. The schema shape is the Builder's.
- **Its own tables:** upsert rows, and run a read-only query bounded in time against **its own** tables only.
- **Its own declared settings,** read-only (A6).
- **Logging** to the service log, prefixed with the plugin name.
- **Scheduling.** A plugin never sleeps or schedules.
  - A provider of a core kind runs inside that kind's coordinator pass at the kind's existing interval setting.
  - An own-kind provider runs at its manifest cadence, overridable by a namespaced setting, never below its declared lower bound.
  - The live pass is described in C3.

**A4. A plugin's own data schema.**

- **Declaration.** A plugin declares tables: columns (integer, real, text; nullable or not; carries a domain name or not), primary key, indexes, an optional `*_at` time column, and a retention.
  - Names are `plugin_<plugin>__<table>`, as `plugintables.go` builds them.
  - No core table can collide with that pattern.
- **Created by opnview** at load, applied at every start like the schema.
- **Purged** in the retention purge's transaction:
  - by the table's own retention, or by `retention_seconds` when it declares zero;
  - a table with no time column is never purged, and `docs/plugins.md` says such a table must be bounded by its key.
- **Readable by widget queries,** with the same checks as any query (B3, A8).
- **Domain-carrying columns.** A query reading a column declared as carrying a domain name is withheld under `aggregate_mode = no_domains` (`withheld_by_aggregate_mode`).
- **Freshness.** A widget bound to an own kind reports, as freshness, the instant of its provider's last successful pass.
- **Not aggregated by the core.** Core aggregates are untouched. A plugin wanting summaries declares its own tables.
- **Not part of the core schema checks** unless the plugin is built-in. `sql/schema-checks.sh` creates the tables every built-in plugin declares and plans every built-in widget query reading them.
- **Schema change (decided here):**
  - opnview records each plugin table's full declaration (columns, types, nullability, primary key, indexes, time column).
  - At load, a declaration that differs in **any** of these from the recorded one is not refused. The existing table is **renamed aside** under a name that can never be read as a live plugin table, the new table is created empty, and the plugin's durable state is reset.
  - The listing reports the event (`schema_rebuilt`) with the aside table's name and row count.
  - An aside table is purged by its recorded retention and dropped when empty. One with no time column is kept and listed.
  - This follows the project's rule: no migrations, rebuild rather than migrate, and nothing silently lost.

**A5. Loading, collisions, listing.**

- **Discovery at start**, like operator widgets. A plugin added to a running service is picked up at the next start. This is stated in `docs/plugins.md`.
- **A plugin is refused at load, with a named closed reason, logged, and skipped** when it has:
  - a malformed manifest;
  - an unsupported API version;
  - a name not in snake_case;
  - an unknown core kind for a contract it claims;
  - a declared kind colliding with a core kind;
  - an endpoint refused by `plugin_endpoints.go` (every code there);
  - a mapping to a field the contract lacks, or one missing a required field (detected at the first pass, as `records_invalid`);
  - a script that does not compile;
  - an invalid table declaration;
  - a widget refused by the widget loader's existing codes;
  - a collision.
- **Built-in wins on any collision:**
  - plugin name;
  - `(kind, provider_key)`;
  - declared kind name;
  - widget type.

  The third-party plugin is reported with the code naming the collision.
- **No activation step.** A loaded plugin's providers enter the existing runtime selection unchanged: the probe round's `separable`, the operator's `Selection` setting, and the per-kind rules on how many providers may be active.
- **Plugin listing, JSON, authenticated.** Every discovered plugin, built-ins included, with:
  - name, version and API version;
  - origin (built-in or data directory);
  - state (loaded, refused, failed), with reason code;
  - each provider's kind and key, and the endpoints and outbound destinations it declares;
  - each table with its row count, and any aside tables;
  - each widget type;
  - each declared setting (value, default, bounds; a secret shows only whether it is set).

  It carries no credential, ciphertext, session token or CSRF token.

**A6. Settings a plugin declares.**

- A plugin may declare settings: key, type (integer, number, boolean, text, enum, **secret**), default, bounds, and a catalogue key for the label. They are stored namespaced by the plugin.
- A secret is stored encrypted with the existing credential mechanism (`encrypted_credential`). No endpoint ever returns it.
- A plugin reads its settings through the host.
- They are written through an authenticated, CSRF-safe JSON endpoint that validates them against the declaration. They are listed in the plugin listing.
- **The built-in MaxMind and Public Suffix List plugins declare the setting keys the current settings surface already uses.** The Go-rendered settings page and the first-run wizard therefore keep working unchanged until step 7 replaces them, and LV-0 carries the values over.

**A7. Fetching over HTTP(S) and caching in the plugin's data directory.** This is the facility the built-in service plugins use, and any third-party plugin may use it.

- **Declaration.** A plugin declares, in its manifest, the outbound destinations it contacts (scheme and host).
  - The host refuses a request to an undeclared host, so the plugin listing never misstates what a plugin contacts. This is the author's own declaration, not an operator allowlist.
  - Whether a third-party destination is acceptable is its author's business.
- **The facility:**
  - fetches one URL with GET, with request headers the plugin sets (credentials from its secret settings included);
  - is **conditional**: it sends the validators (`ETag` / `Last-Modified`) of the cached copy, and a 304 keeps it;
  - **streams the body to a file** in the plugin's own data directory (`<data-dir>/plugin-data/<name>/` or similar; the Builder states the path), never into script memory, with a size bound the plugin declares;
  - writes to a temporary file and **replaces the previous good copy only when the new one is complete and passes the plugin's check**, so a failed or malformed download leaves the previous copy in use;
  - offers, as generic file-format helpers: gzip and tar extraction of a named entry, a SHA-256 digest, reading a text file line by line, and opening a **MaxMind DB (MMDB) format** file for lookups;
  - reports a closed outcome the plugin maps to its availability. The existing `download_refused`, `download_limit_reached`, `download_failed`, `download_malformed`, `no_licence_key`, `licence_key_undecryptable` and `no_account_id` are reused.
- **Why MMDB is a host helper.** MMDB is an open format that DB-IP and IPinfo also publish, so it is not one product's code. Parsing it in script would make every lookup pass far slower.
- **File access.** A plugin reads files only in its own directory and its own data directory.
- **The core makes no request to any host but the firewall.** No Go package other than the OPNsense client and this host facility builds an HTTP request. The facility is used only on a plugin's behalf.

**A8. Documentation.** `docs/plugins.md` is new. A third party writes a source plugin, a service plugin or a widget plugin from it alone. It holds:

- the manifest schema;
- every kind's contract, generated from or tested against `contract.go`;
- the A3, A6 and A7 host API, and the widget host API of B3;
- the table rules of A4;
- the refusal and outcome codes;
- the collision rule;
- the versioning policy;
- complete worked examples: a source plugin of a core kind; a source plugin with its own kind and tables; a widget with JavaScript logic over that table; a service plugin downloading a file.

**A Suricata-style alert example is not needed any more:** the built-in `suricata` plugin is itself the example. `docs/extending.md` is folded into `docs/plugins.md` or reduced to a pointer. `docs/architecture.md` (*What this is not*, *The widget registry*) and `docs/data-model.md` *Vocabulary* are amended by name. "Plugin" is opnview's own term and is distinguished from an OPNsense plugin package such as `os-ddclient`.

### Phase B: every built-in becomes a plugin

**B1. Firewall sources.**

- **The twelve implementations become built-in plugins grouped by product:**

  | Plugin | Providers (kind / provider_key) |
  |---|---|
  | `pf` | `firewall_log` / `pf` |
  | `suricata` | `security_event` / `suricata` |
  | `kea` | `dhcp_lease` / `kea` |
  | `isc` | `dhcp_lease` / `isc` |
  | `dnsmasq` | `dhcp_lease`, `dns_lookup`, `resolver_cache`, `resolver_local_data` / `dnsmasq` |
  | `unbound` | `dns_lookup`, `resolver_cache`, `resolver_local_data` / `unbound` |
  | `insight` | `measurement_sample` / `insight` |

- **Provider keys and display names are unchanged,** so registry rows, `Selection` settings and availability keep their identity.
- **Product logic moves into the plugin's script,** byte-for-byte in behaviour. This covers:
  - the `unboundctl` and `dnsmasqctl` helpers;
  - lease decoding and state normalisation;
  - action normalisation;
  - the unverified field-name candidate lists;
  - the device-name guard of `traffic/top`;
  - the Unbound deduplication key;
  - the `PageBound` / `RequestedSpanSeconds` values;
  - the availability detail wording.
- A plugin is self-contained. It never imports another plugin's file, so a third party can copy one as a template.
- **The Suricata plugin reproduces today's collector only.** Step 6 (alert ingestion as a shipped feature, severity resolution, G7, G8) stays out of scope.
- **Discovery stays core:** interfaces, rules, ARP/NDP, the system time and offset (`internal/collect/discovery.go`). See B4.
- **Provider display strings** (`provider.*` in `internal/web/locale/en.json`) move into each built-in plugin's string file. What the Go-rendered pages print stays byte-identical (X3).

**B2. External services as plugin kinds.**

- **Two service kinds are opened to plugins,** each with a contract in `contract.go` and `docs/plugins.md`:
  - **`ip_geolocation`** (replaces the `geo_asn` registry kind; MaxMind's own term for its GeoLite2 products is "IP geolocation"). It has:
    - a refresh function: acquire or update the dataset through A7;
    - a lookup function: for a batch of public addresses, each address's country code, ASN and AS organisation, or a named miss;
    - the dataset's build identity.
  - **`registrable_domain`** ("registrable domain" is the Public Suffix List's and the URL Standard's term). It has:
    - a refresh function;
    - a grouping function: for a batch of names, each name's registrable domain or a named state;
    - the list's version identity.
- **The core keeps:**
  - which addresses still need placing (`AddressesToGeolocate`, the build-keyed miss logic, the 20 000-per-pass bound, never looking up a non-public address — `internal/specialpurpose`);
  - storing answers in `geo_asn`;
  - the widget-side use of groupings;
  - the cadences, which keep their setting keys.
- **Re-placement.** A change of the active provider or of its build identity re-places addresses exactly as a new build does today.
- **One active provider per service kind,** chosen by the existing `Selection`, defaulting to the built-in. This is decided here because a figure must not be placed by two datasets at once.
- **MaxMind and the Public Suffix List become built-in plugins `maxmind` and `publicsuffix`.**
  - Their downloads, conditional refreshes, licence-key handling and held previous good copy are done through A7, with the same outcomes and the same refresh cadences as today.
  - **`internal/maxmind` and `internal/publicsuffix` are removed as Go network clients.** Any non-network code they hold that the core still needs moves into the core under a name that says what it is.
- **`source_policy_test.go` is rewritten:**
  - exactly two places build HTTP requests: the OPNsense client and the A7 facility;
  - the built-in plugin set declares exactly two non-firewall destinations: the MaxMind download host and `publicsuffix.org`.

**B3. Widgets as plugins.**

- **A widget plugin is:**
  - a manifest (today's widget manifest, unchanged in meaning);
  - one or more SQL queries;
  - optional JavaScript for logic a shape cannot express;
  - the renderer slot, reserved until step 7.

  It lives in a plugin directory, and one directory may hold sources and widgets. `<data-dir>/widgets/<type>/` keeps working through the same loader.
- **The 26 built-in Go hooks are rewritten in that format.**
  - `RegisterHook`, `hooks_gen.go` and its `go:generate` directive are removed.
  - Every JSON payload documented in `docs/widget-api.md` and asserted by the existing widget tests stays **byte-identical**, except the fields Phase C changes by name.
- **What the core keeps (the shared engine):**
  - parameter parsing;
  - references;
  - windows;
  - the envelope (availability, gaps, freshness, unknown, invalid and missing parameters, conditions, withheld);
  - the declarative shape engine.

  The shared figures several hooks use today are exposed to widget scripts as **documented host functions**, not reimplemented per widget:
  - site rows;
  - address rows (C2);
  - attribution rate;
  - registrable grouping through B2;
  - buckets and series;
  - rates and live figures (C3);
  - the remainder and lower-bound figures.
- **The widget host API grants:**
  - the parsed parameters and resolved references;
  - running the widget's own declared queries on the read-only pool with bound parameters and a time bound;
  - the shared host functions above;
  - setting states and conditions;
  - declaring withheld fields under `no_domains`.

  It grants **no firewall call** (5B boundary) and no outbound fetch.
- **Every widget query** (built-in, plugin or operator) is refused, with a closed code, when it reads `account`, `session`, `encrypted_credential`, or a `setting` row holding a credential or secret. This is a data-leak guard on the HTTP API, not a sandbox of plugins: decision 12 makes a data leak blocking.
- **Responses keep their 5B budgets** at the 1 000 000-row seed with scripts in the request path (AC B9).

**B4. Audit of hard-wired products and services (done for this spec; the Builder records it in `docs/architecture.md`).**

| Found | Where | Treatment |
|---|---|---|
| Twelve firewall-source implementations and two product helpers | `internal/collect/<kind>/<impl>/`, `unboundctl`, `dnsmasqctl`, `providers_gen.go` | Built-in plugins (B1); generation removed |
| MaxMind GeoLite2 | `internal/maxmind`, `store/geo.go`, settings surface keys | `ip_geolocation` kind, built-in `maxmind` plugin (B2) |
| Public Suffix List | `internal/publicsuffix`, `widget/sites.go` grouping | `registrable_domain` kind, built-in `publicsuffix` plugin (B2) |
| 26 widget hooks | `internal/widget/widgets/<type>/*.go`, `hooks_gen.go` | Widget plugins (B3) |
| Provider display strings | `internal/web/locale/en.json` | Carried by each plugin (B1) |
| Product-shaped closed codes (`eve_rotation_lost`, `no_licence_key`, `download_*`) | `schema.sql` | Kept as a shared closed vocabulary any plugin may report; own-kind gap reasons are plugin-declared (A3) |
| The OPNsense client: transport, auth, TLS trust, mutating guard, timestamp formats | `internal/opnsense`, `internal/decode` | **Not a seam. Stays core.** opnview is defined as observability for OPNsense (`CLAUDE.md`, ROADMAP), its vocabulary is OPNsense's, and every source plugin is written against OPNsense's API. A second firewall product would be a different product, not a second implementation |
| Discovery (interfaces, rules, ARP/NDP, system time) | `internal/collect/discovery.go` | **Stays core,** for the same reason. It is named in `docs/architecture.md` as the one OPNsense-specific code outside plugins and the client |
| IANA special-purpose registries | `internal/specialpurpose` | Stays core: a standard, no service, no outbound call, no plausible second implementation |
| SQLite (modernc), goja, Argon2id | store, plugin host, auth | Stay core: an embedded engine (ROADMAP), the interpreter, and a mandated algorithm |
| Test fixtures naming built-in providers | `internal/web/webtest/collection.go` | Acceptable: test data naming built-ins |

**B5. The existing tests are the behavioural spec.**

- Before any Go implementation is removed, the Builder records an **inventory** from the baseline snapshot as a committed test-data file: every test function in the implementation packages, the kind coordinators, `internal/maxmind`, `internal/publicsuffix` and `internal/widget/widgets`, each with its package and file.
- After the rewrite, every inventoried test exists by name and runs against the plugin loaded through the plugin loader.
- Its assertions are unchanged, except the lines that obtain the result.
- **Retired tests.** A test whose subject is the compiled mechanism itself (`providers_gen.go` or `hooks_gen.go` staleness, the provider overlay test, `testdata/examplefake`) is retired. The inventory names it together with the test that replaces it (A-series proof tests).

**B6. Removal.**

- The Go implementation directories, `providers_gen.go` and its directive, `hooks_gen.go` and its directive, and `internal/maxmind` / `internal/publicsuffix` as network clients are removed.
- The builder's in-progress `provider.Manifest` / `provider.json` mechanism is replaced by the plugin manifest.
- `go generate` stays only where the core links its own Go code: the web surfaces until step 7.

### Phase C: the gaps

**C1. Owners, labels and purposes (Go core, JSON only).**

All endpoints are authenticated, CSRF-safe and documented in a section *Write endpoints* of `docs/widget-api.md`. Paths are the Builder's.

- **Owners:**
  - list, each with its client count;
  - create (`display_name` non-empty after trimming, unique as the schema's `UNIQUE` defines it);
  - rename (same rules);
  - delete. Every assignment is cleared and the answer enumerates the cleared assignments (client, `owner_assigned_at`).
- **Undo a deletion.** Given the deleted person's name and the cleared assignments, it recreates the person and restores each assignment **with its original `owner_assigned_at`**. A client assigned to someone else in the meantime is reported and left as it is.
- **Assignment:** assign one or several clients (sets `owner_assigned_at` to now), and unassign them.
- **Client listing:** every client, with or without traffic, with:
  - id;
  - `identity_kind` and `identity_key`;
  - hostname;
  - MAC;
  - last address;
  - interface;
  - `unstable_identity`;
  - owner and `owner_assigned_at`;
  - `last_seen_at`.
- **Labels and purposes:**
  - set or clear `interface.user_label`;
  - set or clear `blocklist.purpose` within its `CHECK` vocabulary, with `purpose_assigned_at`.
- **Rules carried from the data model:**
  - Ownership is assigned, never inferred: an endpoint changes exactly the clients it names.
  - A client with an unstable identity may be assigned and is listed as such.
  - A reassignment forces recomputation of the owner family for every kept slot holding the client's traffic (`docs/data-model.md`, refresh contract rule 3). Until then `freshness` reports those slots stale.
  - **An assigned client is never removed by the retention purge.**
  - Nothing here contacts the firewall.
  - Answers are JSON with closed codes and no English sentence.

**C2. Unnamed destinations by address, operator and country (Go core, served through the B3 host functions).**

- **The rule.** Wherever a widget names a destination at site level, a destination with no inferred name is shown as **its address with its operator (ASN, organisation) and country**, or with its `lookup_state` when unplaced. It is never one "unnamed" bucket.
- **The computation lives in the core** (address rows beside site rows) and is exposed to widget plugins.
- **`top_sites` and `sites_by_client`:**
  - rows carry a kind, `site` or `address`;
  - address rows are ranked with site rows and truncated by the same `limit` / `sites_per_client` and `min_flows`;
  - a partly named address contributes its named flows to site rows and the rest to its address row.
- **`connection_tree`:** the level below the operator holds site nodes and address nodes, and `site:none` disappears. Owner-rooted views follow the same rule.
- **`client_traffic_detail`** is already compliant: verified, not changed.
- **`traffic_sankey`:** each `other.unnamed` bin lists its addresses with figures summing to the bin.
- **Beyond the flow horizon:** exact figures from a stored aggregate, or the named state `unnamed_destinations_beyond_flow_horizon`. Never an estimate.
- **Under `no_domains`:**
  - in `connection_tree`, owner-rooted views and `client_traffic_detail`, every destination is shown in address form, named ones included, so the response does not depend on whether a name was ever inferred;
  - `top_sites` and `sites_by_client` stay withheld whole.
- **Documents amended by name:** `docs/widget-catalogue.md` (*Top sites*, *Sites by client*, *Connection tree*) and `docs/widget-api.md` (the `no_domains` table).

**C3. Live rate (a plugin capability, read by the core's widget functions).**

- **C3.0, measured before building** (live validation step LV-C0, run by the orchestrator read-only on the live firewall before this part is written):
  - the wall time of `traffic/top` per call and per interface count;
  - the wall time of `traffic/interface`;
  - the firewall's processor use from the existing `cpu_usage/stream` samples, with no extra sampling for 15 minutes, then at each candidate cadence for 15 minutes;
  - for the pf state table (C4), if its endpoint verifies, the state count, the response size and time, and the processor cost.

  The result is recorded in `docs/opnsense-api-survey.md` as proportions and timings only.
- **Two modes, one sampler, in the built-in `insight` plugin:**
  - **Baseline.** The existing `sample` pass at `measurement_interval` always runs and is persisted, whether anyone is watching or not.
  - **Live.** The manifest declares a live pass reading only the live endpoints: `traffic/interface` counters, `traffic/top`, and `traffic/stream/<interval>` only if it verifies (below). It has a live cadence setting:
    - its **default is the finest cadence LV-C0 found adding at most 2 processor percentage points** over the idle baseline, justified in a comment citing the measurement;
    - its lower bound is the span of one `traffic/top` capture.

    The host runs the live pass while **live interest** holds.
- **Live interest.**
  - Any authenticated request for a live figure (a widget response carrying a live part, or the rolling series) renews a lease of `live_interest_seconds`.
  - Its default is 30 s: it survives a missed 2 s poll, and fine sampling stops within half a minute of the last viewer.
- **Persistence:**
  - every sample of either mode is persisted, so no sample leaves a gap;
  - fine samples are distinguishable from baseline samples (the schema shape is the Builder's) and purged after `live_sample_retention_seconds`;
  - baseline samples follow the normal retention;
  - when a baseline pass and a live pass fall due together, one round of the live endpoints serves both;
  - logged and sampled figures are never summed.

  How fine samples are "rolled into the baseline" is open question 1. Scope follows its recommendation unless the maintainer answers otherwise.
- **Candidate endpoint.** `/api/diagnostics/traffic/stream/<interval>` (`TrafficController::streamAction`) is `UNVERIFIED:`. It is read from `opnsense/core` 26.7.3 and recorded in the survey before any code uses it.
- **The widget API never calls the firewall.** Every live figure is read from stored samples.
- **Exposed fields:**
  - `current_rates` in `traffic_composition`;
  - `owner_activity` chips;
  - `client_traffic_detail.current_rate`.

  Each carries `measure`, `sampled_at`, the span one sample covers, `cadence_seconds` and `lower_bound`, and a named stale state when older than the cadence allows.
- **The rolling strip (coverage row 8: servable).** A trailing series at the live cadence:
  - throughput by direction, from the samples;
  - decisions per engine per bucket (allowed, firewall rule, firewall no rule, DNS refusals), from filter-log timestamps and resolver lookup timestamps.

  Each decision series carries `complete_until`. Buckets after it are gaps, never zeros.
- **Budget.** The live part of a response has a **median of 100 ms at the 1 000 000-row seed**.

**C4. Ports and protocols.**

- **Logged volume** by protocol and by service port is served end to end:
  - `service_port` slices carry protocol **and** port (IANA identifies a service by both), so 53/udp and 53/tcp are two slices;
  - `port:not_recorded` stays.
- **Every catalogue binding naming `flow.dst_port` or `flow.protocol` is served:**
  - `client_traffic_detail` records;
  - `traffic_composition`;
  - the `connection_tree` "service port and protocol" level;
  - the blocked feeds where the catalogue names them.
- **Rate by port and protocol from the pf state table:**
  - the endpoint is researched in 26.7.3 source and recorded;
  - **built as a plugin only if it verifies and LV-C0 found its cost within the same 2-point threshold** at a cadence the Builder states;
  - its figures carry `measure`, `sampled_at`, `cadence_seconds` and `lower_bound`, and are never summed with logged volumes;
  - if it is not built, `docs/widget-api.md` states that the rate by port or protocol is unavailable, citing the `traffic_top.py` iftop invocation without `-P` and the LV-C0 or source finding, and coverage row 4 cites it.
- **`traffic_composition` adopts the mockup keys:**
  - `group_by: blocking_reason`;
  - `split_by: direction`;
  - `window_seconds`;
  - `line_interpolation` (rendering class).

  The catalogue is amended by name. `destinations_per_owner` is not adopted: `connection_tree` rooted at the owner serves it.

**C5. The JSON API covers every current page.**

- **No investment in the Go templates.** No new page, template feature, catalogue machinery or theme machinery.
- **Every capability of this cycle is JSON only:**
  - owners;
  - plugin listing;
  - plugin settings;
  - live rate;
  - labels;
  - purposes.
- **Every action and every displayed figure of the Go-rendered surfaces gets a JSON equivalent,** so step 7 can replace them without new backend work. The surfaces are `account`, `settings`, `collection`, `interfaces` and `sources`. Their content:
  - **account:** first-account setup, sign-in, sign-out, key-file password reset;
  - **settings:** the firewall URL, key and secret with TLS trust verification; the MaxMind settings (now the `maxmind` plugin's declared settings); the Public Suffix List refresh; the theme preference value;
  - **collection:** per-kind polling intervals, measured record rate, suggestion, and the collection settings including `aggregate_mode`;
  - **interfaces:** networks per interface and the link-local rule;
  - **sources:** availability, recent gaps, the outage indicator.
- **Same rules as the pages.** Each JSON endpoint applies the same validation and the same authentication and CSRF rules.
- **Documentation and enforcement.** The mapping (page route → JSON route) is a table in `docs/widget-api.md`, *Settings API*, and a test enforces it (C-AC9).

**C6. The mockup gap audit.**

- `docs/widget-api.md`, *Coverage of the mockup*, is rewritten. Every element the mockup displays is either served (type and field) or carries an impossibility reason citing 26.7.3. That covers all 45 rows and the elements outside them.
- Re-examinations:
  - row 4: C4;
  - row 8: C3, servable;
  - row 39: **servable as a bounded interval**, from `state_since_after_at` / `state_since_by_at`, which come from opnview's own probes and are never stated as an exact instant.
- Rows 2, 3, 18, 20 and 24 move to C3. Rows 19, 26 and 27–29 move to C2. Row 45, the labels and the purposes move to C1.
- `client_activity_timeline` stays `unknown_widget_type`. Alert presets stay step 6. Canvases have their own spec.

### Phase D: a readable Go core layout (a move after Phase C; no behaviour change)

- **The rule, enforced by a test over `cmd/`, `internal/` and the built-in plugin tree:**
  - no package directory holds more than **15 non-test source files** (`.go` and `.sql`);
  - every package has a `doc.go` whose package comment says what the package is for.

  Test files are not counted, because Go keeps them beside the package. `testdata/` is not counted.
- **The proposed target, by responsibility.** The Builder may rename packages if the rule and the map hold.
  - **`internal/store`:** opening, the schema, the statement registry, the read pool, the query check, closed vocabularies. Responsibilities move out to:
    - `store/accounts` (accounts, sessions, credentials, settings rows);
    - `store/flows` (write, records, pairing, chunking, classification, dual-stack, this-firewall, networks);
    - `store/attribution` (attribution, record rate);
    - `store/geo`;
    - `store/aggregates` (aggregates, periods, downtime);
    - `store/samples` (measurement samples, reconciled state);
    - `store/purge`;
    - `store/reads` (reads, widget rows, surfaces);
    - `store/plugintables`.
  - **`internal/collect`:** the collector and scheduler. Responsibilities move out to:
    - `collect/discovery`;
    - `collect/identity`;
    - `collect/kinds/<kind>` (one coordinator per kind).
  - **`internal/plugin`:** the host. It holds:
    - `plugin/loader`;
    - `plugin/runtime` (interpreter bindings);
    - `plugin/contract` (from `contract.go`);
    - `plugin/fetch` (A7).
  - **`internal/widget`:** the engine. Responsibilities move out to:
    - `widget/params`;
    - `widget/envelope`;
    - `widget/figures` (sites, addresses, attribution, buckets, series, rates);
    - `widget/shapes`.
  - **`internal/web/webtest`:** split by surface.
- **Moves preserve history.** No identifier is renamed except where a split forces an export. Every such export is listed in the hand-back.
- **`docs/architecture.md` gains a *Map of the tree*:** one line per package directory saying what it is for. A test asserts that the map and the tree list the same directories.

### Phase E: the pre-deployment budget

- **The budget.** `ci/pre-deployment.sh` measures its total wall time, prints it beside the CPU count, and fails above **180 s**. The value is one constant, written once, like `TEST_BUDGET_SECONDS`.
- **`-race`** runs only on packages selected by a **stated mechanical rule**, not a hand list. The rule includes:
  - every package that starts a goroutine;
  - every package that uses `sync` or `sync/atomic`;
  - every package that serves HTTP handlers;
  - the plugin host.

  The script prints the selected and excluded packages.
- **The 1 000 000-row seed** is built **once** per run and reused by every check that needs it: plans, widget budgets, live budget, reclassify cost. Widget timings run on the large seed only.
- **Reporting.** `ROADMAP.md`, *Development and test environment*, states the budget and the rule. If 180 s cannot be met without dropping a check, the Builder reports the figures rather than dropping it.

### Phase F: documentation clean-up (last phase; documents the final state once)

Phase F starts when Phases A to E are complete. It runs before the suites' single run and the live validation. The live-validation figures (LV-C0, proportions) are then written into the condensed documents, not into documents about to be rewritten.

**F.1 Why.** The tree holds about 11,200 lines of documents and about 5,100 lines of specs. Several restate one another, and some contradict the code this cycle produces:
- `docs/architecture.md`, *What this is not*;
- `docs/extending.md` (`go generate`);
- server-rendered pages described as the product's interface.

Agents read whatever is in the tree as current. A superseded instruction kept beside its replacement is how the project drifted. The rule from here on: **the code and a small set of documents are the truth, and git history is the archive.**

**F.2 The target document set.** Each document is kept because a named reader needs it and no other document answers that need.

| Document | Reader and purpose | Target size | Action |
|---|---|---|---|
| `README.md` | Somebody installing opnview: install, first run, the setup wizard, what it sees, the observation-point limit, the read-only API key advice (Risk 6), one paragraph on dropping a plugin directory | ≤ 200 lines (from 295) | condensed |
| `CLAUDE.md` | Agents: the project rules | ≤ 135 lines (no growth) | **not edited by the Builder.** The Builder hands back a proposed diff (corrected references, the restated outbound rule), which the orchestrator applies with the maintainer's agreement |
| `ROADMAP.md` | Everybody: the rules that apply to every step, the development and test environment, and the remaining steps (6, 7, 8) | ≤ 300 lines (from 782) | condensed. *Rules that apply to every step* is kept in meaning, with every rule present (the outbound rule as restated in *Rules*). **Steps 3 and 7 as amended, which `CLAUDE.md` names as binding, keep every requirement.** Text may be reworded only where a requirement is unchanged. Completed steps 1, 2, 4 and 5 become one paragraph each: what was delivered, and the commit |
| `docs/architecture.md` | A contributor: *Map of the tree* (D2), data flow, the plugin host, the B4 audit table, and a new section ***Decisions in force*** | ≤ 500 lines | rewritten. *Decisions in force* holds one entry per maintainer decision still governing the code: the decision, its date, the test that enforces it where one does. It replaces every code citation of a spec |
| `docs/plugins.md` | A plugin author (source, service or widget): the whole guide, contracts, host API, worked examples | ≤ 1,200 lines | absorbs `docs/extending.md` and `docs/widget-authoring.md` |
| `docs/widget-api.md` | The step-7 application and any API client: the HTTP contract, *Write endpoints*, *Settings API* (C-AC9), the plugin listing, *Coverage of the mockup* | ≤ 900 lines | grows with this cycle's endpoints, nothing else |
| `docs/data-model.md` | A contributor touching the store: *Vocabulary* (full), the table families and their invariants, the refresh contract, and the figures pre-deployment records there (the `ReclassifyEverything` wall time) | ≤ 900 lines (from 3,535) | condensed. **Per-column descriptions live once, in `schema.sql`'s comments**, and are removed from this document, because two copies of the schema drift |
| `docs/opnsense-api-survey.md` | Whoever touches a firewall read: every OPNsense fact the code or a built-in plugin relies on, each with its 26.7.3 source URL; the step-1 findings that shaped the design (Suricata's `dns`/`tls`/`http` not readable through the API); each `UNVERIFIED:` item | ≤ 700 lines (from 1,447) | condensed. Every section whose title is cited by an endpoint's survey citation (compiled registry or built-in manifest) keeps its exact title. Probing narrative and history are removed |
| `docs/ui-references.md` | **Binding** (`CLAUDE.md`) | ≤ 500 lines (from 839) | reduced, preserving binding content, see F.3 |
| `docs/widget-catalogue.md` | **Binding** | ≤ 1,100 lines (from 1,825) | reduced, preserving binding content, see F.3 |
| `docs/dashboard-format.md` | **Binding** | unchanged (770) | kept, see F.3 |
| `docs/mockups/canvas-mockup.html` and its `check-*.js` scripts | The step-7 reference | unchanged | kept |
| `docs/ui-references/screenshots/*` | Referenced by `ui-references.md` | unchanged | kept: no screenshot is deleted in this cycle |

Target total for the Markdown documents above, excluding `CLAUDE.md` and the unchanged `dashboard-format.md`: **≤ 6,500 lines**, from about 10,300.

**F.3 The three binding interface documents.** Their paths do not change, because `CLAUDE.md` names them.
- **`docs/ui-references.md`:**
  - **Kept verbatim:** the section *The maintainer's recorded preferences* and the palette values reproduced from https://lequellec.xyz.
  - **Condensed:** the survey of prior art becomes one table with one row per reference (product, screenshot path, what is taken, what is rejected, and why), so every screenshot stays referenced.
  - **Removed:** content that describes the server-rendered Go pages as the product's interface, which decision 11 supersedes.
- **`docs/widget-catalogue.md`:**
  - **Kept:**
    - the twelve shapes with every field (what is drawn, data requirements, parameters, empty and absent states, truncation);
    - every preset with its **Data** binding and parameter table (tested against the manifests by the 5B parameter-table test and C-AC8);
    - *Why twelve*, as its derivation summary;
    - the *Gaps found* table with every gap still open.
  - **Removed:**
    - narratives of gaps already closed (a closed gap keeps one row: identifier, closed, and where it is served);
    - implementation notes that restate a manifest.
- **`docs/dashboard-format.md`:** unchanged except for sentences contradicted by decision 11. The format is not yet implemented (a separate spec, before step 7), and condensing a contract nobody can yet test against code would lose content silently.

**F.4 Specs: deleted from the tree, not archived.** Every spec whose work is delivered is deleted. Git history keeps it (`git log --follow -- specs/<file>`).
- **Why not archive.**
  - An archive directory is still in the tree. It is still read and grep-matched by agents, and keeps presenting superseded requirements as instructions. This spec's own earlier text, which required a sandbox, an allowlist and an activation step, is the example.
  - An archive also keeps the 185 code citations pointing at change requests rather than at the current rule.
  - What a delivered spec still governs is a **decision in force**, and it moves to `docs/architecture.md` *Decisions in force* or to the document that owns the subject.
- **Before a spec is deleted,** the Builder checks every requirement of it against the code. Any requirement found undelivered is moved, unchanged in meaning, into `ROADMAP.md` under the step that will deliver it, and listed in the hand-back.
- **Every citation of a spec is rewritten** to cite the surviving document and section: Go and SQL comments, `sql/schema-checks.sh`, `ci/`, the `Dockerfile`, `.vscode/tasks.json`, `ROADMAP.md`, `docs/`. A check label in `sql/schema-checks.sh` (for example `AC7`) may stay as a label when its message states the assertion in words. A citation of a deleted file may not.
- **This spec** is deleted by the orchestrator in this cycle's commit, after the maintainer accepts the live validation. Its decisions in force are recorded by F.2 before that.
- `specs/.gitkeep` stays, so the factory scripts' directory exists.

**F.5 Exact deletions, for the maintainer's approval.** The Builder deletes only these paths, each by exact path, shown first. No wildcard.

Documents (content folded into `docs/plugins.md`):
- `docs/extending.md`
- `docs/widget-authoring.md`

Specs (delivered; decisions in force moved per F.4):
- `specs/SPEC-accounts-and-settings.md`
- `specs/SPEC-canvas-mockup.md`
- `specs/SPEC-collection-and-storage-core.md`
- `specs/SPEC-correlation-classification-aggregation.md`
- `specs/SPEC-deferred-interface-bundle.md`
- `specs/SPEC-maxmind-geolite2.md`
- `specs/SPEC-purge-serialisation.md`
- `specs/SPEC-resolver-cache-attribution.md`
- `specs/SPEC-resolver-cache-closing.md`
- `specs/SPEC-resolver-cache-follow-ups.md`
- `specs/SPEC-sizing-derived-from-usage.md`
- `specs/SPEC-step-5a-corrections.md`
- `specs/SPEC-step-5a-live-corrections.md`
- `specs/SPEC-step-5b-widget-api.md`
- `specs/SPEC-test-budget-and-two-live-defects.md`
- `specs/SPEC-two-missing-kinds.md`

Deleted by the orchestrator in the commit, not by the Builder:
- `specs/SPEC-backend-completion-plugins.md`

Any further deletion the Builder finds necessary is proposed in the hand-back and not performed.

**F.6 Checks that keep the documents honest.** Phase F adds a document-consistency test to the everyday suite. F-AC4 and F-AC5 state what it covers.

## Acceptance criteria

### Phase A: plugin host

- [ ] **A1. Source proof test.** A test writes, as files only into a temporary data directory, a plugin of an **own kind** with two tables and a script. The plugin is discovered, its tables are created, and it runs at its cadence against the fake firewall. Its rows and durable state are committed in one transaction. A restart neither loses nor duplicates a row. No repository file is created or changed (file list and hashes compared before and after).
- [ ] **A2. Core-kind proof test.** The same, for a dropped plugin providing a second `dhcp_lease` provider. Its leases go through the identity cascade exactly as a built-in's do, and it has a registry row and an availability row.
- [ ] **A3. Refusal codes.** One test per A5 refusal reason, including each code of `plugin_endpoints.go`. Each is logged, listed with its closed code, and skipped, while the service and every other plugin keep running.
- [ ] **A4. Collisions.** A dropped plugin colliding with a built-in on plugin name, `(kind, provider_key)`, declared kind or widget type is reported with the collision code. The built-in is the one loaded.
- [ ] **A5. Runtime failures.** One test each for a script exception, a host-function panic, a timeout and invalid records. The provider's availability carries `script_failed`, `script_timeout` or `records_invalid`. No row of that answer is stored. Other providers collect in the same run.
- [ ] **A6. Schema change.** A plugin restarted with a declaration differing only in nullability, then only in primary key, then only in an index, then in columns, is loaded each time. Its old table is renamed aside, the new one is empty, its state is reset, and the listing reports `schema_rebuilt` with the aside table and its row count. An aside table with a time column is purged by its retention and dropped when empty.
- [ ] **A7. Plugin tables in the core.**
  - A plugin table is purged by its own retention, or by `retention_seconds` when it declares zero.
  - A widget query reads it.
  - A query reading a domain-carrying column answers `withheld_by_aggregate_mode` with `data: null` under `no_domains`, and answers data under `full`.
  - Freshness reports the provider's last successful pass.
- [ ] **A8. Settings.** A declared setting is listed, written through the JSON endpoint within its bounds, and refused outside them with a closed code naming the key. A secret is stored encrypted, read by the plugin, and absent from every response (string search over the listing and settings payloads for the plaintext and the ciphertext).
- [ ] **A9. Fetch facility.** Against a local test server:
  - a first fetch writes the file;
  - a conditional fetch receiving 304 keeps it;
  - a failed fetch keeps the previous good copy in use;
  - a malformed download, refused by the plugin's check, keeps the previous good copy;
  - a request to an undeclared host is refused before any connection;
  - the body is written to the plugin's data directory, and no file is written outside it.
- [ ] **A10. Firewall only, for the core.** The rewritten source-policy test passes: only the OPNsense client and the A7 facility build HTTP requests. A counting transport over a full run of the core and every built-in plugin, on fixtures, sees only:
  - the firewall base URL;
  - the MaxMind download host;
  - `publicsuffix.org`.

  The last two are requested only by the `maxmind` and `publicsuffix` plugins.
- [ ] **A11. Listing.** It returns every plugin with the A5 fields. Without a session it answers 401 with a JSON condition. No response contains a credential, ciphertext, session token or CSRF token.
- [ ] **A12. Derived enumerations.** No list of kinds, provider keys or widget types used at runtime is hand-maintained. A plugin added in A1, A2 or B8 appears in `source_availability`'s default `kinds`, in the selection keys and in the widget registry without any edit.
- [ ] **A13. Record contracts.** Each kind's contract is defined once (`contract.go`), and a test asserts that `docs/plugins.md`'s tables equal it.
- [ ] **A14. Interpreter dependency.**
  - `go.mod` lists goja as a direct requirement, and `go mod tidy` produces no diff.
  - `docs/plugins.md` records goja's full transitive dependency list.
  - `CGO_ENABLED=0 go build ./...` succeeds.
  - No warning is suppressed.
- [ ] **A15. Documented examples run.** Each worked example of `docs/plugins.md`, copied verbatim by a test into a temporary data directory, loads and produces the result the document states.
- [ ] **A16. Documents.**
  - `docs/architecture.md` no longer says there is no plugin system.
  - Neither `docs/extending.md` nor `docs/plugins.md` describes a step requiring `go generate`, a rebuild, Go code or a PR for a third party.
  - `docs/data-model.md` *Vocabulary* defines "plugin" against OPNsense's use of the word.

### Phase B: built-ins as plugins

- [ ] **B1. No compiled implementation remains.**
  - No Go package implements a firewall-source provider, an `ip_geolocation` provider, a `registrable_domain` provider or a widget hook.
  - `providers_gen.go`, `hooks_gen.go` and their directives are gone.
  - `internal/maxmind` and `internal/publicsuffix` build no HTTP request.
- [ ] **B2. Same loader.** Every built-in plugin loads through the same loader as A1, and the listing shows all of them as loaded with origin built-in. The listing holds the B1 table's seven firewall-source plugins plus `maxmind`, `publicsuffix` and the 26 widgets.
- [ ] **B3. Behavioural inventory.** The B5 inventory file exists. A test asserts that every inventoried test name exists and is not skipped, and that every retired test is named with its replacement. The verifier compares each test's assertions with the baseline snapshot and finds none removed or loosened.
- [ ] **B4. Identity preserved.** Registry rows `(kind, provider_key)` and display names of every built-in provider are unchanged, except `geo_asn` → `ip_geolocation` and `public_suffix` → `registrable_domain`, which `docs/data-model.md` records by name.
- [ ] **B5. Widget byte identity.** For every built-in widget type, at its defaults, at `24h` and `30d`, and at every parameter combination the existing widget tests exercise, the response body on the fixture database is byte-identical to the baseline snapshot's, except fields Phase C changes by name (each listed). The comparison is a test or a committed script output the verifier reruns.
- [ ] **B6. Widget proof test.** A widget with JavaScript logic is written as files into a temporary data directory: a manifest, a query over the A1 plugin's table, and a script computing a figure the declarative engine cannot. `GET /api/widget/{type}` answers with the full 5B envelope, with its parameters parsed, defaulted and reported (unknown, invalid, missing) exactly as a built-in's, with no recompile.
- [ ] **B7. Credential guard.** A widget query (built-in, plugin or operator) reading `account`, `session`, `encrypted_credential` or a credential or secret `setting` row is refused with a closed code. One test per table.
- [ ] **B8. Service kinds.**
  - A dropped `ip_geolocation` plugin reading a local MMDB file from its own directory places addresses when selected. Switching back to `maxmind` re-places them.
  - A dropped `registrable_domain` plugin groups names when selected.
  - Neither needs any edit to a shipped file.
- [ ] **B9. Budgets with scripts in the path.** Every widget response budget of 5B holds at the 1 000 000-row seed with the JavaScript widgets. Pre-deployment reports each widget's median beside its budget.
- [ ] **B10. Audit recorded.** `docs/architecture.md` carries the B4 table, including the statement that the OPNsense client and discovery are not seams, and why.

### Phase C: gaps

- [ ] **C-AC1. Owners.** Create, rename, delete, undo, assign, unassign and the client listing each have a test.
  - A duplicate or empty `display_name` is refused with a closed condition, and no row changes.
  - Delete returns every cleared assignment.
  - Undo restores each assignment with its **original** `owner_assigned_at`, and reports, without changing, a client assigned elsewhere in the meantime.
- [ ] **C-AC2. Assigned, never inferred.** Assigning a client changes no other client's `owner_id`, including clients sharing its hostname, vendor or MAC prefix (a fixture built to tempt a match). A client with `unstable_identity = 1` can be assigned and is listed as such.
- [ ] **C-AC3. Kept beyond retention.** A client whose last flow is older than the retention horizon and which has an owner survives the purge. An unassigned one in the same position is purged as before.
- [ ] **C-AC4. Auth and CSRF.** Every write endpoint of this cycle (owners, labels, purposes, plugin settings, the C5 settings API):
  - refuses no session (401, JSON condition);
  - refuses a missing or wrong CSRF proof or a cross-origin `Origin` (403, JSON condition);
  - changes no row when it refuses.

  No `GET` changes state. `TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest` covers the new routes.
- [ ] **C-AC5. Reassignment.** After a reassignment and the next derivation, `owner_activity` at `1h`, `24h`, `7d` and `30d` attributes the client's volume to the new owner. Owners plus unassigned sum to the value before. Before the derivation, the affected slots are reported stale.
- [ ] **C-AC6. Labels and purposes.** Both are visible in the next `interface_volume_ranking` and `blocked_dns_lookups` response respectively. An out-of-vocabulary purpose is refused with a closed condition.
- [ ] **C-AC7. Unnamed destinations.** On a fixture with named, unnamed, partly named and unplaced destinations:
  - for `top_sites` at any scope, rows plus remainder plus below-`min_flows` equal the `attribution.eligible_flows`, and each address row carries the address and `country_code`, `asn` and `operator`, or a `lookup_state`;
  - no response of `top_sites`, `sites_by_client`, `connection_tree` (every root) or `owner_activity` holds a node standing for all unnamed destinations together;
  - each `traffic_sankey` `other.unnamed` bin lists addresses summing to the bin;
  - under `no_domains`, `connection_tree`, the owner-rooted views and `client_traffic_detail` are byte-identical to the same fixture with every `domain_attribution` row removed, and `top_sites` / `sites_by_client` are withheld whole;
  - beyond a short-retention horizon, figures are exact or carry `unnamed_destinations_beyond_flow_horizon`.
- [ ] **C-AC8. Ports.**
  - A fixture with 53/udp and 53/tcp yields two `service_port` slices, and null ports fall in `port:not_recorded`.
  - A test parses each catalogue preset's **Data** paragraph. For every preset naming `flow.dst_port` or `flow.protocol`, a response carries both values where the catalogue binds them, the `connection_tree` level included.
  - The four adopted `traffic_composition` keys are in the manifest and the catalogue, and each `data`-class key changes the response.
- [ ] **C-AC9. Settings API coverage.** A test enumerates every route of the `account`, `settings`, `collection`, `interfaces` and `sources` surfaces. For each state-changing route there is a JSON route in `docs/widget-api.md` *Settings API* that, given the same input, makes the same change and refuses the same invalid inputs (tested per route). For each page, every figure it renders is present in a JSON response (tested by comparing values on a fixture).
- [ ] **C-AC10. Live endpoints verified.** Every endpoint the live pass and the pf-state plugin call is in `docs/opnsense-api-survey.md` with a 26.7.3 source URL. A candidate that does not verify is recorded as such, and no code calls it. A source the firewall does not answer yields a closed availability outcome.
- [ ] **C-AC11. Live cadence and interest.** With a fake clock:
  - the live pass runs only while a lease renewed by a live-figure request holds, and stops within `live_interest_seconds` of the last one;
  - the baseline pass runs throughout;
  - with N parallel readers, at most one round of live calls happens per live cadence;
  - the widget API makes zero firewall calls.

  The cadence, lease and retention are `setting` rows. An out-of-range value is refused at load, naming the key. The cadence default's comment cites the LV-C0 figures.
- [ ] **C-AC12. Live persistence.**
  - Fine and baseline samples are both stored and distinguishable.
  - Fine samples older than `live_sample_retention_seconds` are purged and baseline samples are not.
  - A window spanning a live period and an idle period has no bucket without a sample at the baseline cadence.
  - No response sums logged and sampled figures.
- [ ] **C-AC13. Live fields and strip.**
  - Every live figure carries `measure`, `sampled_at`, span, `cadence_seconds` and `lower_bound`; one older than its bound yields the named stale state.
  - Each decision series carries `complete_until`, and later buckets are gaps, never zeros.
  - Buckets sum to the decisions of the fixture's flows and lookups.
- [ ] **C-AC14. Live budget.** Pre-deployment reports the median response time of the live part at the 1 000 000-row seed, and it is at most 100 ms.
- [ ] **C-AC15. pf states.**
  - Either the pf-state plugin exists, its endpoint is cited, and its figures pass C-AC13's field check;
  - or `docs/widget-api.md` states the limit, citing the source or the LV-C0 finding, and coverage row 4 cites it.
- [ ] **C-AC16. Audit.**
  - The coverage table has one row per mockup element. Each names a type and field or a 26.7.3-cited impossibility. Rows 4, 8 and 39 carry a re-examination.
  - For row 39, an availability response carries per provider the current state's `since` bounds. A fixture transition between two probes yields exactly those two instants, and a state never seen changing yields a named absence.

### Phase D: layout

- [ ] **D1.** A test fails when any package directory under `cmd/`, `internal/` or the built-in plugin tree holds more than 15 non-test `.go` and `.sql` files, or lacks a `doc.go` with a package comment.
- [ ] **D2.** `docs/architecture.md` *Map of the tree* lists every package directory with its purpose. A test asserts that the map and the tree list the same set of directories.
- [ ] **D3.** After the move, B5's byte-identity comparison and the whole suite pass unchanged. The hand-back lists every identifier exported to make a split possible.

### Phase E and the whole cycle

- [ ] **E1.** `ci/pre-deployment.sh` prints its wall time beside the CPU count and fails above its 180 s constant. Its output shows:
  - the `-race` package selection, with its rule;
  - one build of the 1 000 000-row seed;
  - widget timings on that seed only.
- [ ] **X1.** The literal checks (no interface name, device, VLAN name, address or CIDR literal) pass over every new file, plugin scripts, examples and fixtures included. Fixtures carry a `__provenance__` block.
- [ ] **X2.** Every criterion of 5B (AC1–AC40, AD1–AD3), the resolver-cache specs, the live corrections and the test budget still passes. `docker compose run --rm checks` stays within 60 s. `docker compose run --rm pre-deployment` passes within 180 s.
- [ ] **X3.** Every page of the Go-rendered surfaces renders byte-identical HTML on the fixture database before (baseline snapshot) and after. No template, theme or catalogue machinery file gains a feature (diff inspection).
- [ ] **X4.** No file or directory named `zz_*`, and nothing under `.claude/worktrees/`, is tracked or staged. No scratch output remains in the tree.

### Phase F: documentation

- [ ] **F-AC1. Sizes.** Each document of the F.2 table is within its target, and the total is ≤ 6,500 lines (line counts printed by the consistency test or a committed script the verifier reruns).
- [ ] **F-AC2. Deletions.** Exactly the F.5 Builder paths are deleted, and no other tracked file. `git status` shows each as a deletion, not a move to another tracked path.
- [ ] **F-AC3. Binding content preserved.**
  - *The maintainer's recorded preferences* and the palette values of `docs/ui-references.md` are byte-identical to the baseline snapshot.
  - Every screenshot under `docs/ui-references/screenshots/` is referenced by `docs/ui-references.md`.
  - `docs/widget-catalogue.md` still holds the twelve shapes with every field, every preset with its **Data** paragraph and parameter table (the 5B parameter-table test and C-AC8 pass unchanged), and every open gap identifier of the baseline's *Gaps found* table.
  - `docs/dashboard-format.md` differs from the baseline only in sentences listed in the hand-back as contradicted by decision 11.
  - `ROADMAP.md` steps 3 and 7 keep every requirement: the verifier checks each baseline requirement sentence against the new text, listed in the hand-back.
- [ ] **F-AC4. No orphan reference.** The consistency test fails when any Markdown file, Go or SQL comment, shell script, `Dockerfile` or `.vscode/tasks.json` holds:
  - a relative link or backticked repository path that does not exist;
  - a citation `<document>, *<Section>*` whose section heading does not exist in that document;
  - a path to a file under `specs/` other than `specs/.gitkeep` and this spec. This spec is allowed until the commit; the orchestrator's deletion in the commit removes the last citations of it, which the verifier re-greps.
- [ ] **F-AC5. No document contradicting the code.** The consistency test asserts:
  - every API path in `docs/widget-api.md` is a registered route, and every route is documented;
  - every firewall endpoint path in `docs/` or `README.md` is in the compiled registry or a built-in plugin manifest;
  - every setting key named in a document exists in `internal/config` or a built-in plugin's declared settings;
  - every closed code listed in a document (availability outcomes, gap reasons, plugin refusal codes, widget conditions, settings refusals) equals the code's set;
  - every kind, provider key and widget type named in `docs/plugins.md` or `docs/widget-api.md` exists;
  - `docs/architecture.md` *Map of the tree* equals the tree (D2).

  Prose claims the test cannot parse are checked by the verifier against the code. Each finding is a defect only if it is wrong behaviour, a broken binding rule or a data leak (*Rules*).
- [ ] **F-AC6. Decisions in force.** Every maintainer decision recorded in a deleted spec that still governs code appears in `docs/architecture.md` *Decisions in force* or in the owning document, with its date. The hand-back maps each deleted spec to where each of its decisions in force now lives, or states "none in force".
- [ ] **F-AC7. Undelivered requirements.** The hand-back lists every requirement found undelivered in a deleted spec and where it now lives in `ROADMAP.md`, or states that none was found.
- [ ] **F-AC8. `CLAUDE.md` untouched by the Builder.** `CLAUDE.md` is unchanged in the Builder's work. The hand-back carries the proposed diff, and after it every reference in `CLAUDE.md` resolves (F-AC4).
- [ ] **F-AC9. Phase order.** No file under `docs/` changes after Phase F except to record the live-validation figures. The verifier compares the tree at the end of Phase F with the tree at commit.

## Live validation (before anything is committed)

The orchestrator runs this read-only against the firewall, after `checks` and `pre-deployment` pass, **except LV-C0, which runs before Phase C3/C4 is built**. Outputs are never committed, and documents record proportions and timings only.

**LV-C0: firewall-side cost, before building C3 and C4.** The measurements listed in C3.0, reported to the maintainer.

- The live cadence default is the finest cadence adding at most 2 processor percentage points.
- The pf-state plugin is built only if its cost meets the same threshold at a cadence stated with the figures.

**LV-0: database rebuild.** This amends `ROADMAP.md` step 5, *Rebuilding the database after step 5B*, by name.

1. Take the 5B baseline queries on the old build's database (5B live validation, and V1–V10).
2. Stop the service.
3. On the old database, list every user-input table and its row count, and show the list to the maintainer:
   - `account`;
   - `encrypted_credential`;
   - `setting`;
   - `owner`;
   - `interface.user_label`;
   - `blocklist.purpose`;
   - operator-set `interface_network` rows.
4. **Move** `opnview.db` and, if present, its `-wal` and `-shm` companions aside, to exact paths shown first. The move is the backup. Nothing is deleted, and the key file stays in place.
5. Start the new build so it creates the schema, then stop it.
6. Carry over `account`, `encrypted_credential` and `setting`, plus the rows the maintainer chose at step 3, by exact statements shown first. MaxMind settings keep their keys (A6).
7. Start the service. Sessions are not carried over.

**LV-P: plugins on live data.**

- **Parallel run.** For 15 minutes the old build (live) and the new build run concurrently against the firewall. The new build runs in the dev container with its own scratch data directory, never the live volume. Per provider, the rows each stored for the same window are compared:
  - filter-log records by digest;
  - lease generations;
  - lookups by deduplication key;
  - cache and local-data observations;
  - measurement readings by subject and measure;
  - geolocation answers for the same addresses;
  - registrable groupings for the same names.

  Every difference is explained by window-edge timing and named, or it is a defect.
- The listing shows every built-in as loaded.
- Each built-in plugin's pass wall time and the service's processor use are reported beside the old build's.
- A third-party example plugin is dropped into the scratch data directory only, never into the live volume. It is discovered and loaded or refused as expected.

**LV-W: per-widget checks.** After at least 25 minutes of collection, for every served widget type at defaults, `1h` and `24h`, record:

- **correct**, **empty** or **wrong**;
- the figures compared;
- the independent query used (5B L1–L4, L10 and their kin).

**The bar:** no widget is wrong, and every empty widget has a named reason that is true.

**Regression checks:**

- 5B L1–L14 and AD4;
- V1–V10 of `specs/SPEC-resolver-cache-attribution.md`: V2, V3, V4, V5 and V10 are 0, and V9 shows samples or a named cause.

**LV-O: owners** (with the maintainer's agreement):

- create one test person;
- assign two clients, one with an unstable identity if any exists;
- check `owner_activity` after the next derivation;
- delete the person and check the cleared assignments;
- undo, and check the original `owner_assigned_at` values;
- delete again.

**LV-N: unnamed destinations.**

- C-AC7's equality holds on live data for `24h`.
- The share of eligible flows shown by address is recorded.

**LV-I: the open `iftop` items** of ROADMAP step 5, each answered or named as still open with the reason:

- whether a client talking between interfaces is counted on both;
- the ten-line cap;
- base 1024 against base 1000;
- the span of one sample.

**LV-R: live mode.**

- With a client renewing live interest, live samples appear at the configured cadence, and the strip's `complete_until` trails real time by at most the filter-log poll interval plus one cadence.
- After interest stops, live sampling stops within `live_interest_seconds`.
- The processor use stays within the LV-C0 figure.

**The validation passes when:**

- LV-W's bar holds;
- every zero check holds;
- LV-P, LV-O and LV-N hold;
- the maintainer accepts LV-C0, LV-I and LV-R.

## Out of scope

- **Step 7, and with it the end-state of `internal/web`.** The frontend is a separate JavaScript application, embedded in the binary and built in step 7. `internal/web` then holds only the JSON API, authentication and static-file serving, with no server-rendered HTML. The current Go-rendered pages (setup, sign-in, settings, collection, interfaces, sources) stay in service until step 7 replaces them, and are then deleted.
- **Everything the step-7 application covers:**
  - widget renderers;
  - the theme and language files and their pickers;
  - the plugin management screen;
  - the plugin settings screen;
  - the People page.

  This cycle provides their JSON only.
- **The Suricata feature of step 6:** alert ingestion as a shipped feature, severity resolution, G7, G8. The `suricata` plugin reproduces today's collector only.
- Canvas and dashboard persistence, import and export: a separate spec, before step 7.
- **Any write to the firewall.** Making the firewall client or discovery a seam (B4).
- **Hot reload of plugins:** discovery is at start.
- Migrations (step 8). `schema.sql` is edited in place, and LV-0 handles the one installation.
- G12 (application classification). Inferring names from CNAME, PTR or registrable domain.

## Risks

1. **Size.** This is the largest cycle so far: three rewrites (sources, services, widgets), a host, the gaps and a layout move.
   - The phases are ordered so each one is testable on its own.
   - The Builder reports any unfinished phase rather than thinning it.
   - The verifier checks phases in order.
2. **Interpreter cost.**
   - goja is markedly slower than Go. JavaScript now sits in two hot paths:
     - the filter-log pass every 10 s (page decoding);
     - every widget request.
   - B9 and C-AC14 measure the second at the 1 000 000-row seed. LV-P measures the first live.
   - A goja runtime is not safe for concurrent use, so concurrent widget requests need a pool of runtimes, with the memory that costs. Compiled programs should be cached per process to keep the 60 s everyday budget.
3. **Test preservation is a manual comparison.** "No assertion loosened" (B3) needs the baseline snapshot. If the in-flight builder already changed files before the snapshot, the comparison falls back to the git index for them, which is weaker. The Builder must list those files.
4. **Byte identity across a language change.** Go's and JavaScript's number formatting, map ordering and integer range differ:
   - the epoch and the byte counters exceed 2^53 only in pathological cases;
   - JSON key order and float printing are the real traps.

   B5 catches them. The fix is in the host's serialisation, not in loosening the comparison.
5. **The undeclared-host refusal (A7)** may read to the maintainer as an allowlist. It is the author's own declaration, kept so the listing never misstates what a plugin contacts. It costs one manifest line.
6. **Declared endpoints.** A plugin reading a sensitive GET endpoint (a configuration backup, for example) is not stopped by the mutating guard. This is accepted by decision. The README must say the API key should hold read privileges only.
7. **Service plugins on the request path.** Registrable grouping is called while serving widgets. A slow or failing `registrable_domain` plugin must degrade to the existing `registrable_domain_grouping_unavailable` state, never block a response.
8. **Live mode cost.** If LV-C0 finds no cadence below today's 300 s within the 2-point threshold (for example, sequential iftop captures across many interfaces), the live mode's finest cadence is that measured value, and the 2 s strip is served at whatever cadence it is.
9. **Phase D export churn.** Splitting `internal/store` across packages forces unexported helpers to become exported. Each one is listed. Package cycles (purge touches everything) may force a different split than the one proposed, which is acceptable if D1 and D2 hold.
10. **The 180 s pre-deployment budget** is unmeasured against the current suite. Narrowing `-race` reduces race coverage to the packages the rule selects.
11. **Live-validation writes** (LV-O) touch the live database through the product's API, with the maintainer's agreement. The LV-P parallel run doubles read load on the firewall for 15 minutes.
12. **Two writers on binding documents.** The catalogue paragraphs amended in C2 and C4 are named exactly, so a parallel interface cycle can reconcile them.

13. **Lost rationale.** Deleting specs removes the reasoning behind decisions from the tree. *Decisions in force* (F-AC6) keeps every decision that governs code. The reasoning stays in git history, which is reachable but not browsed. A future reader asking "why" must use `git log`.
14. **Comment churn on top of the layout move.** Rewriting 185 citations in 84 files enlarges the Phase D diff, and the verifier's review of the move with it. Phase F runs after Phase D's byte-identity check (D3), so the two are reviewed separately.
15. **Condensing binding documents can drop a rule silently.** F-AC3 pins the parts tested against code or quoted verbatim. The rest of the step 3 and step 7 text depends on the verifier's sentence-by-sentence check, which is the weakest criterion of the phase.
16. **Existing tests read documents.** These tests read the documents and must keep passing, never be loosened:
    - survey section names checked against endpoint citations;
    - catalogue parameter tables checked against manifests;
    - `docs/plugins.md` contract tables (A13);
    - `docs/data-model.md` receiving the reclassify figure.

    They constrain the condensation: a section a test reads keeps its exact title.
17. **The size targets are judgement.** If a target cannot be met without losing content a test or a binding rule needs, the Builder reports the figure and the content that forced it, rather than cutting it.
## Rules

- **Test suites run once.** `docker compose run --rm checks` then `docker compose run --rm pre-deployment` run once, when the work is ready to commit, not after each change. The verifier runs them once per round.
  - The one exception: after LV-C0 sets the live-cadence default, the one-line change is followed by one run of `checks`.
- **Only a blocking defect sends work back:** wrong behaviour, a broken binding rule, a data leak. Cosmetic and documentation findings are listed and never open a new round.
- **One verifier per round, no ergonomist** (standard cycle).
- **No commit before the live validation passes and the maintainer accepts it.** No git command that moves or discards work.
- **Deletion.** Exact paths only, shown first. No wildcard or background destructive command. The live `data` volume's `opnview.db*` is touched only by LV-0's shown steps.
- **The outbound rule, restated.** The Go core contacts the firewall and nothing else. The built-in `maxmind` and `publicsuffix` plugins download from the MaxMind download host and `publicsuffix.org` respectively, so the product as shipped still contacts exactly the three destinations it does today. A third-party plugin's declared destinations are its author's business.
  - `ROADMAP.md`'s *Rules that apply to every step* bullet is amended by the Builder.
  - The matching sentence in `CLAUDE.md` is updated by the orchestrator with the maintainer's agreement, not by the Builder.
- **Hard rules carried from the project:**
  - no hardcoded configuration;
  - no installation data in any file;
  - read-only against the firewall;
  - OPNsense's and each product's vocabulary, with new terms recorded in `docs/data-model.md` *Vocabulary*;
  - English only;
  - every OPNsense fact cites `opnsense/core` 26.7.3, and an unverified one is marked `UNVERIFIED:` with nothing built on it;
  - degrade with a named state, never a zero;
  - no warning suppressed;
  - a full transitive audit for any new dependency.

## Open questions

1. **What "fine samples rolled into the baseline" means.** The maintainer's live-rate decision says fine samples are persisted with a short retention "and rolled into the baseline". Two readings:
   - **(a) Promotion only.** The baseline sampler runs without interruption at its own cadence. When it falls due during live mode, one round of calls serves both, so no extra firewall call is made and the baseline series has no hole. When the fine samples are purged, nothing more is kept from them.
   - **(b) Promotion plus summary rows.** Before fine samples are purged, each baseline interval they cover also keeps a summary row per subject and measure (count, mean and peak of the fine samples), marked as derived. That way the peaks seen while someone watched survive beyond the short retention.

   **Recommendation: (a).** Interface counters are cumulative, so the baseline already yields exact average rates over each interval. The `traffic/top` per-client figures are 2 s snapshots, whose mean and peak over a few minutes of viewing add little to the long-term history, while (b) adds a second kind of row every reader must keep apart from the baseline. Scope C3 follows (a) unless the maintainer chooses (b).
**Decided (maintainer, 10 October 2026):** open question 1 is answered (a), promotion only. No summary rows are kept.


---

**This amendment is part of the spec and overrides any earlier text it replaces.** Section 14 lists the earlier sentences that are obsolete. The Builder treats the earlier text as amended accordingly; the orchestrator folds the amendment into the body during Phase F.

# Amendment to `specs/SPEC-backend-completion-plugins.md`: the storage engine moves to PostgreSQL + TimescaleDB (maintainer decision, 11 October 2026)

**Fact check of the brief.** I read the repository and checked each stated reason.

- **The single writer: confirmed.** `internal/store/store.go` uses `SetMaxOpenConns(1)`, WAL, `busy_timeout(5000)` and `secure_delete(FAST)`. It also carries a workaround: the table count is read *before* the schema transaction, because otherwise SQLite answers `SQLITE_BUSY` and ignores the busy timeout.
- **The read pool: confirmed.** `readpool.go` holds 4 `query_only` connections with a hand-made statement cache. `querycheck.go` detects writes by scanning SQLite `EXPLAIN` opcodes.
- **"database is locked" handling: partly confirmed.** It is handled through `busy_timeout`, plus `isMissingSchemaObject`, which matches SQLite error text. There is no explicit "database is locked" branch.
- **The purge/derive mutexes: they exist, but they are not SQLite artefacts.** `purgeMutex` and `deriveMutex` in `internal/collect/collect.go` enforce the *ordering* rule of `docs/data-model.md` (*Refresh contract*, and "The refresh and the retention purge are ordered"). That rule is a logical invariant. It holds under any engine with concurrent transactions, so switching engines does not remove it by itself. The amendment below says this rather than promising its removal.
- **Hand-built aggregates: confirmed.** There are 6 families × 4 periods = 24 tables, plus `purged_flow_hour`, refreshed by `aggregate.statements.sql`.
  - The families key on mutable attributes: classification, attribution, client re-pointing and owner.
  - They carry exact distinct counts (`distinct_peers`, `client_count`).
  - They depend on a purged part.

  All three limit where continuous aggregates can fit. The amendment gates their use on equivalence.
- **One namespace for plugin tables: confirmed.** `plugintables.go` names every table `plugin_<p>__<t>` in the single namespace.
- **Two constraints that are new for hypertables.**
  - `flow.log_digest` is `UNIQUE`. A unique index on a hypertable must include its time column.
  - `domain_attribution.flow_id` references `flow(id)`, a foreign key into a would-be hypertable.
- **State of work.** Phase A and much of B are in the tree (`internal/plugin/`, `plugins/*/plugin.json`). No built-in plugin declares tables. Phase C (live rate) is not started.
- **The SQLite surface.**
  - 26 `.statements.sql` files.
  - `sql/schema-checks.sh` holds 404 `check`/`expect_sql_*` assertions on the `sqlite3` CLI.
  - About 60 test files call `.DB()` with raw SQLite SQL. These include `webtest/dbscan.go` (`sqlite_master`, `pragma_table_info`), `account_test.go` (`PRAGMA secure_delete`) and `store_test.go` (the `-wal`/`-shm` companions).
- **Current versions and pins.** `go.mod` pins `modernc.org/sqlite v1.59.0`. The `Dockerfile` installs `sqlite3` and `gcc`.
- **No README install section exists today.** The amendment specifies a new one.

---

## 1. Context: insertion after decision 12 ("Process"), as decision 13

> 13. **The storage engine is PostgreSQL with the TimescaleDB extension** (maintainer, 11 October 2026). It replaces SQLite for everything, and it replaces the `ROADMAP.md` rule that storage engines be embedded libraries and that forbade PostgreSQL and TimescaleDB. The orchestrator wrote that rule, not the maintainer. The reasons:
>     - SQLite's single writer forced a single read-write connection, a separate read-only pool and busy-timeout handling.
>     - The hour/day/week/month rollups are hand-built SQL.
>     - Plugin tables share one file and one namespace.
>     - The maintainer wants plugins with their own data schema, a fine live rate, event-driven features and time series.
>
>     The install story is kept: one command installs, one command updates, nothing is provisioned by hand. Phase P carries the switch.
>
>     **A second requirement of the same day:** any widget can list the datasets that the core and every plugin expose, and a generic widget binds x, y, filters, period and aggregation to any of them with no code (P8).

## 2. Context, *Where the repository stands*: append these bullets

> - **Storage today (read for decision 13):**
>   - `modernc.org/sqlite` (pure Go);
>   - one read-write connection (`store.go`: WAL, `synchronous=NORMAL`, `busy_timeout`, `secure_delete(FAST)`, a WAL checkpoint on close);
>   - four `query_only` read connections with a statement cache (`readpool.go`);
>   - a widget-query check that plans the text and searches SQLite `EXPLAIN` opcodes for writes (`querycheck.go`);
>   - `schema.sql` (about 3 300 lines, applied in one transaction at every start, no migrations);
>   - `purge.sql`;
>   - 26 `*.statements.sql` files under `internal/store` and `internal/widget`;
>   - 24 aggregate tables (6 families × 4 calendar periods) plus `purged_flow_hour`, refreshed by hand under the *Refresh contract* of `docs/data-model.md`;
>   - `sql/schema-checks.sh`: 404 assertions, query plans and the 100 000 / 1 000 000-row seeds on the `sqlite3` CLI.
> - **The ordering mutexes.** `purgeMutex` and `deriveMutex` (`internal/collect/collect.go`) enforce the refresh contract's ordering of the purge against a pass's store-and-derive. That ordering is a logical rule, not an engine workaround.

## 3. Context: verdicts amended

- On the `internal/store/plugintables.go` bullet, replace "*Compatible, with one defect to change:*" with:
  > *Compatible in contract; re-targeted by Phase P6 (one PostgreSQL schema per plugin). The defect below is fixed by A4 as amended.*
- In the B4 audit table, replace the row `| SQLite (modernc), goja, Argon2id | store, plugin host, auth | Stay core: … |` with:
  > `| PostgreSQL + TimescaleDB (via a pure-Go driver), goja, Argon2id | store, plugin host, auth | Stay core: the installation's store (ROADMAP, storage rule as replaced 11 October 2026), the interpreter, and a mandated algorithm. The store is not a seam: a second engine would be a second product to test |`

## 4. Scope: replace the phase-order paragraph

Replace from "**Phase A.** The plugin host and format." through "**Phase E.** The pre-deployment budget." with:

> **Phase A.** The plugin host and format.
> **Phase P.** The storage engine: PostgreSQL + TimescaleDB, the per-plugin schemas, the data catalogue.
> **Phase B.** Every built-in rewritten as a plugin: sources, services and widgets, plus the hard-wiring audit.
> **Phase C.** The gaps: live rate and pf states as plugins; owners, unnamed destinations, ports and the JSON settings API in the core; the mockup audit.
> **Phase D.** The core layout move.
> **Phase E.** The pre-deployment budget.
> **Phase F.** Documentation clean-up.
>
> **Where Phase P sits, and why.** Phase A and part of Phase B are already in the tree. No built-in plugin declares a table, and Phase C has not started.
> - Phase P runs **next**, from the current tree. It re-targets everything that touches storage, including the Phase A pieces already written: `plugintables.go`, durable plugin state, plugin settings, and the A-series tests that create plugin tables.
> - It must be complete, and its own criteria must pass, **before**:
>   - any built-in plugin declares a table;
>   - any remaining Phase B storage work;
>   - all of Phase C, whose live samples (C3) are written straight into the new store.
> - Phase A and Phase B items not yet finished continue on PostgreSQL after it.
>
> **SQLite reference outputs, recorded before Phase P.** Before any store code changes, the Builder runs the baseline snapshot's code (on SQLite, in the pre-cycle development image) on the fixture database. It records the outputs as committed test data:
> - every widget response of B5's comparison set;
> - the result rows of every query in `sql/queries/*.sql`;
> - a dump of every aggregate slot of every family and period.
>
> These are the "before" of P-AC6 and of B5. The baseline code cannot run once the development image no longer carries SQLite.

## 5. Scope: new section, inserted after Phase A (before "### Phase B")

### Phase P: the storage engine

**P1. Engine, versions, licence, telemetry.**
- **The store is PostgreSQL with the TimescaleDB extension.** One database per installation. `opnview` is its only client.
- **Pinned versions.**
  - The PostgreSQL major and the TimescaleDB release are each pinned by exact version and by image digest, verified against the publisher's release notes, and written once (one constant per pin, like `GO_VERSION`).
  - The TimescaleDB release is the latest that supports the pinned PostgreSQL major on the day of pinning. The Builder records both, with the source URLs, in `docs/architecture.md`.
  - A pin is changed deliberately, never floated.
- **The licence, stated in `README.md` (*Licence*) and `docs/architecture.md`:**
  - TimescaleDB's core (hypertables, `time_bucket`, chunk dropping) is Apache-2.0.
  - Continuous aggregates, and any compression or background policy, are under the **Timescale License (TSL)**. The TSL permits this self-hosted use and forbids offering TimescaleDB itself as a hosted database service.
  - `opnview` stays MIT. It neither links nor redistributes TimescaleDB; the installation pulls its image.
  - The Builder verifies this split against the licence matrix of the pinned release, cites it, and lists every TSL feature `opnview` actually uses.
- **Telemetry is off.** TimescaleDB's telemetry and version check would break the outbound rule ("no telemetry, no version check").
  - Every compose service running the database sets it off.
  - `opnview` reads the setting at start and **refuses to start**, with a named error, when it is not off.
- **The driver** is pure Go, so `CGO_ENABLED=0` still holds. `modernc.org/sqlite` leaves `go.mod`.
  - The driver becomes a direct requirement, `go mod tidy` produces no diff, and its full transitive dependency list is recorded in `docs/architecture.md`.
  - No warning is suppressed.
  - Library choice is the Builder's, subject to this rule.

**P2. Installation shape.**
- **Development and test (`docker-compose.yml`).**
  - A test database service (TimescaleDB image, pinned as P1) is added, and `checks`, `pre-deployment` and `schema-checks` depend on it through a health check.
  - The canonical strings `docker compose run --rm checks` and `docker compose run --rm pre-deployment` are unchanged.
  - For `checks`, the test database keeps its data on a tmpfs. Durability-off settings (`fsync`, `synchronous_commit`, `full_page_writes`) are permitted **only** on that test service, each with a comment saying why.
  - `pre-deployment` runs against a disk-backed instance with the production durability settings, so the whole suite meets a real disk once before a deployment, as today.
- **Running the service (`docker-compose.serve.yml`).**
  - `serve` gains the database service with a named volume for its data.
  - The `opnview` data directory (`/data`) keeps the key file and the plugin data directories. It no longer holds a database file.
- **The connection.**
  - `opnview` receives its database connection as a **required start parameter with no default**, exactly like `--data-dir`. It refuses to start without it, naming the parameter.
  - The compose files supply it.
  - The database is reached over a Unix socket or the installation's private container network. **The database service publishes no port.**
  - Any database credential is generated at first start into the installation's own storage (a secret file or volume, mode 0600), never written into a tracked file.
  - The test service may use trust authentication on its private network. That is stated in a comment, and it is the only place it is allowed.
- **Start.**
  - `opnview` waits for the database for a bounded time, logging each attempt, then exits non-zero naming the database.
  - At open it takes a **session advisory lock**. A second `opnview` against the same database refuses to start with a named error. This turns "one process" into a fact the in-process ordering mutexes (P7) can rely on.
- **Backup.**
  - **A backup is two things:** a `pg_dump` of the database (the exact command, run through compose, documented in `README.md`) and the key file in the data directory.
  - Restore goes into the same pinned TimescaleDB release, following the restore procedure TimescaleDB documents for that release.
  - The README says that a dump without the key file restores the history but not the stored credentials, as the key-file limitation already says.
- **Step 8 (an always-on host or LXC).** The single-command story is restated in `ROADMAP.md` step 8 (text in §12 below):
  - one command installs Docker Engine and its compose plugin if absent, writes the compose file and the generated database credential into the installation directory, and starts both containers;
  - re-running it updates: it pulls the pinned images and restarts, and the database volume and data directory survive;
  - an unprivileged Proxmox LXC needs nesting enabled for Docker, and the README says so.

  Building `ct/install.sh` stays step 8 work.
- **The outbound rule, restated.**
  - The Go core makes network requests to the firewall and to nothing else.
  - **The database connection is not an outbound call.** It goes to the installation's own database, configured by the installer, and to no other host.
  - Image pulls at install or update time are installation-time downloads, as the `Dockerfile` already says of its own build-time fetches. They are not among the product's outbound calls.

**P3. The core schema in PostgreSQL.**
- `schema.sql` is rewritten in PostgreSQL's dialect, **keeping every table, column, constraint, index, view and comment in meaning.** Per-column descriptions stay in its comments (F.2).
- **Every SQLite-specific construct is translated, not dropped:**
  - `INTEGER PRIMARY KEY` row ids become generated identities;
  - `ifnull`-wrapped unique indexes become `NULLS NOT DISTINCT` or equivalent expressions;
  - `strftime`, `GLOB`, `instr`, the `json_*` functions, `INSERT OR …`, `CREATE VIEW IF NOT EXISTS` and every `PRAGMA` are translated;
  - every upsert keeps its conflict target.
- **Two semantics are pinned, because they change results silently:**
  - **Text ordering is byte order**, as SQLite's `BINARY` collation gives it (the C collation or an equivalent);
  - **`LIKE` keeps SQLite's ASCII case-insensitivity** wherever a statement relied on it. The Builder lists every `LIKE` and its translation.
- **The JSON API keeps epoch seconds.** Every instant in every response stays an integer number of seconds since the epoch, byte-identical to today, whatever column type stores it. The constraint that drives the type choice: calendar-month buckets in TimescaleDB need a timestamp type. The choice is the Builder's.
- **Namespace.** Core objects live in a dedicated schema, not `public`. No plugin schema can share its name (P6).
- **Applied at every start, in one transaction where PostgreSQL allows it.** PostgreSQL's DDL is transactional. Any statement TimescaleDB refuses inside a transaction (for example, creating a continuous aggregate) runs after the transaction, idempotently, and the Builder lists those statements.
  - **Re-applying changes nothing:** no schema object and no row count in any table (`schema-checks` AC1, restated).
  - `ErrSchemaOutdated` keeps its meaning, detected from PostgreSQL's error codes for an undefined table or column rather than from message text.
- **No migrations,** as before (*Out of scope*). P10 says what happens at deployment.
- **Every SQL statement file** (`purge.sql`, every `*.statements.sql`, `sql/queries/*.sql`, `sql/seed.sql`) is translated with the same rules.
  - The authoring syntax for named parameters in widget and plugin queries (`:name`) stays what `docs/plugins.md` documents. The core translates it.
  - Operator and plugin queries are documented as **PostgreSQL** SQL.

**P4. Hypertables and retention by chunk.**
- **Hypertables.**
  - A growing table whose rows carry an instant (candidates: `flow`, `dns_resolution`, `measurement_sample`, `security_event`, `resource_record_observation`, `dhcp_lease`) becomes a hypertable on that instant **only if every constraint it carries keeps its meaning**.
  - The ones to verify are `flow.log_digest UNIQUE` (a hypertable's unique index must include the time column) and the foreign key `domain_attribution.flow_id → flow(id)`. Both are checked against the pinned release's documentation.
  - A table that cannot keep its constraints stays a plain table, and the reason is recorded.
- **Retention by chunk, driven by opnview's clock.**
  - The purge keeps its signature, `Purge(ctx, now)`, and its horizon semantics are **exact**:
    - whole chunks entirely past the horizon are dropped;
    - the chunk that straddles the horizon is purged row by row;
    - everything the purge writes first (the purged part, `retention_purge.purged_before`, the assigned-client exemption of C1) is written exactly as today, in the same transaction or ordering.
  - TimescaleDB background retention jobs are **not** used for any table whose purge is ordered with derivation, because they run on the server's clock, outside that ordering, and invisible to the tests' fake clock.
  - Fine live samples (C3), which share a table with baseline samples unless the Builder separates them, keep their own exact horizon (C-AC12).
- **Not in this cycle: columnstore compression.** Reclassification rewrites rows across the whole retention (`ReclassifyEverything`). See *Out of scope*.
- **The fit table.** `docs/data-model.md` gains a section ***Storage fit***. One row per growing table records:
  - hypertable yes or no, and why;
  - the retention mechanism;
  - the test that proves the purge semantics unchanged.

**P5. Aggregates: continuous where equivalent, hand-refreshed where not.**
- **A family and period becomes a TimescaleDB continuous aggregate only where it reproduces exactly what the existing tests assert.** That means:
  - a connection logged on two interfaces is **counted once** (the `in` record), its second leg counting only under its `exit_leg_device` in the volume family and in the rule family;
  - **UTC calendar slots**: the hour, the day, the ISO week starting Monday 00:00 UTC, and the calendar month of 28 to 31 days, with `period_end_at` exact;
  - **gaps are never zeros**, and a period predating collection shows its true coverage;
  - the purged part of an hour under a retention shorter than an hour;
  - late ingestion into a closed slot;
  - reclassification and identity re-pointing moving a flow out of one slot into another;
  - owner reassignment (*Refresh contract*, rule 3);
  - exact `distinct_peers` and `client_count` in every period.
- **The derivation drives refresh.** A continuous aggregate is refreshed by the derivation, over the slots the refresh contract selects (`refresh_continuous_aggregate` over an explicit window), **not** by a background policy, so rules 1 to 6 and the watermark semantics of `computed_at` hold. What `aggregate_freshness` and every widget's `freshness` report keeps its meaning.
- **A family that fails any of the above stays a hand-refreshed table in PostgreSQL, and that is an acceptable outcome.**
  - The families most at risk: the owner family (keyed on `client.owner_id`, a joined mutable attribute that continuous-aggregate invalidation does not track), the domain family (attribution), and every family's exact distinct counts.
- **A family that is a pure regrouping of another by a mutable attribute** (the owner family, regrouped from the client family by the client's current owner) **may be computed at read time instead of stored**, provided:
  - the widget responses stay byte-identical (B5, P-AC6);
  - C-AC5 holds;
  - the 5B budgets hold.
- **Recording the decisions.** *Storage fit* records, per family and period, which of the three it became (continuous aggregate, hand-refreshed, read time), why, and the test proving equivalence.

**P6. One PostgreSQL schema per plugin.** This replaces, in A4, "Names are `plugin_<plugin>__<table>`, as `plugintables.go` builds them" and "No core table can collide with that pattern".
- **The schema.** Each plugin that declares tables gets its own PostgreSQL schema, named from the plugin name under a prefix that no core schema carries. Its tables keep their declared names inside it.
  - Two plugins can never collide.
  - A plugin's objects are listed, dumped and dropped as one namespace.
- **The declaration (A4) gains, per column:**
  - a **role**: `time`, `dimension` or `measure`;
  - for a measure, an optional **unit**. A unit obeys the shape rule `measurement_sample.unit` already enforces (non-empty, lower-case, no space). The core's own terms are those of `internal/store/rows.go`, and a plugin's term is an ordinary value beside them, as for `measurement_sample`.

  These feed the data catalogue (P8).
- **A plugin table with a time column is created as a hypertable** on it.
  - A declared primary key or unique index that omits the time column is **refused at load** with a closed code the Builder names and `docs/plugins.md` lists (TimescaleDB requires the time column in every unique index).
  - Retention follows P4: exact horizon, by chunk then row. A7's criterion is unchanged.
- **Schema change keeps A4's rule:** rebuild, never refuse, nothing silently lost.
  - The previous table is **moved into a reserved aside schema**. That schema is never on any search path, never in the data catalogue, and never readable by a widget query.
  - The new table is created empty, the plugin's durable state is reset, and the listing reports `schema_rebuilt` with the aside table and its row count.
  - Aside tables are purged by their recorded retention and dropped when empty. One with no time column is kept and listed.
- **No plugin is ever dropped silently.** A plugin no longer present at start keeps its schema and tables. The listing reports them as orphaned with their row counts. Removal is a deliberate operator act, documented in `docs/plugins.md` as a single SQL command.
- **A plugin's durable state and settings stay in core tables** keyed by plugin, as today (`plugin_state`, namespaced settings, `encrypted_credential`).

**P7. Concurrency.**
- **Removed, being SQLite engine workarounds:**
  - the single read-write connection (`SetMaxOpenConns(1)`) and its rationale;
  - WAL, `synchronous`, `busy_timeout`, `secure_delete`, `query_only` and every other `PRAGMA`;
  - the WAL checkpoint on close and every handling of `-wal`/`-shm` companions;
  - the "count before the transaction" workaround in `applySchema`;
  - matching SQLite error text;
  - the separate read pool (`readpool.go`) and its hand-made statement cache. A pooled handle replaces both, with driver-level statement caching. AC31 of 5B ("a second call prepares nothing") is restated against that caching;
  - `IntegrityCheck` (`PRAGMA integrity_check`). It is retired, with its replacement or reason named in the B5 inventory.
- **Kept, being logical rules:**
  - the ordering of the purge against a pass's store-and-derive (`purgeMutex`);
  - the serialisation of derivations (`deriveMutex`);
  - the in-flight ingestion watermark.

  Each may become a transaction-scoped PostgreSQL advisory lock only if the Builder shows the same ordering. `purge_ordering_test.go` and the refresh-contract tests pass with unchanged assertions either way. The single-instance lock (P2) makes the in-process form sound.
- **Read connections and the data-leak guard.** Widget queries, plugin read queries and catalogue queries (P8) run on connections that:
  - log in as a **read role** with `SELECT` only on the readable core objects and on plugin schemas, and with **no privilege on `account`, `session`, `encrypted_credential`, or on any setting holding a credential or secret** (by splitting those rows out or by row-level security; the Builder's choice);
  - are not members of any role that holds those privileges, so no `SELECT` can raise its own privileges;
  - run every statement in a **read-only transaction** under a statement timeout.

  The load-time closed refusal of B7 stays. The privilege is a second layer, replacing `query_only` and the `EXPLAIN`-opcode scan, which is retired.
- **LISTEN/NOTIFY for event-driven work.**
  - The core publishes a notification on commit of:
    - every collection pass (kind, `provider_key`);
    - every plugin pass (plugin, provider);
    - every aggregate refresh (the periods refreshed).
  - The channels and payloads are a closed list, documented in `docs/plugins.md`.
  - **A provider of an own kind may declare, in its manifest, that its pass is also triggered by named core events**, in addition to its cadence. A triggered pass never runs more often than its declared lower bound and never concurrently with itself.
  - Pushing notifications to a browser is step 7.

**P8. The data catalogue API.** This is the maintainer's requirement of 11 October. All endpoints are JSON, authenticated, and documented in `docs/widget-api.md` under a new section *Data catalogue*. Paths are the Builder's; the ones below are proposed.
- **Listing (`GET /api/datasets`).** Every dataset the core and every loaded plugin expose. Each entry carries:
  - a namespaced name (core, or plugin plus table);
  - its origin;
  - a catalogue key for its label;
  - its time column, if any;
  - its retention horizon;
  - the provider whose last successful pass is its freshness;
  - whether it is an **event** dataset (a covered bucket with no row is a true zero) or a **sample** dataset (a bucket with no row is a gap);
  - its columns, each with: name, type, role (`time`, `dimension`, `measure`), unit, nullable, carries-domain, and the aggregations it admits.
- **What the listing never contains:** an aside table, `account`, `session`, `encrypted_credential`, a credential or secret setting, `plugin_state`.
- **Core datasets.** The core declares its datasets as data, in the same declaration format plugins use. At minimum it declares the five sources of `custom_chart` (`traffic`, `firewall_metric`, `interface_throughput`, `dns`, `security_event`, `docs/widget-api.md`, *Series of `custom_chart`*), with the measures that section lists. Every plugin table is a dataset automatically.
- **The query (`GET /api/datasets/{name}/query`).** Parameters are encoded the way `GET /api/widget/{type}` encodes `custom_chart`'s `series`. A query names:
  - **`period`**, using the widget API's period and window vocabulary and parsing;
  - **`x`**, either the time column with a `bucket` (`auto`, or seconds), or one dimension column;
  - **`y`**, one or more measure columns, each with an aggregation from a closed list (`sum`, `mean`, `min`, `max`, `last`, `count`, plus `count_distinct` for dimensions), refused where the column's declaration does not admit it;
  - **`filters`**, each a column, an operator from a closed list (`eq`, `ne`, `in`, `not_in`, `lt`, `le`, `gt`, `ge`, `is_null`, `is_not_null`) and a value typed against the column;
  - optional **`split_by`**, one dimension, with a `limit` and a remainder entry;
  - **`order`** and **`limit`** for a dimension `x`.
- **Safety.**
  - The server compiles a query into one parameterised `SELECT`.
  - **Identifiers come only from the dataset's declaration**, quoted. **Values are only ever bound.**
  - It runs on the P7 read connections.
  - **No SQL text is accepted from the browser,** in any parameter.
- **The response.**
  - It uses the 5B envelope: availability, coverage, gaps, freshness, conditions, `withheld`, and parameters echoed with unknown, invalid and missing reported.
  - Time buckets are aligned to UTC. A bucket of an hour, a day, a week or a month is aligned on the volume aggregates' period boundaries (ISO week, calendar month), as `docs/widget-catalogue.md`, *Custom chart*, requires.
  - A bucket not covered by collection (a collection gap, downtime, an unavailable provider), or an empty bucket of a sample dataset, is a **gap and never a zero**.
- **Under `no_domains`,** a query reading, filtering or splitting on a carries-domain column answers `withheld_by_aggregate_mode` with `data: null`.
- **Closed refusal codes,** named by the Builder and listed in the document: unknown dataset, unknown column, aggregation not admitted, operator not admitted, value type mismatch, too many buckets, too many series, too many rows.
- **Bounds.** Each is a constant written once and documented. Proposed:
  - at most 1 000 buckets per series;
  - at most 20 series (split values) plus the remainder;
  - at most 1 000 rows for a dimension `x`;
  - a statement timeout of 5 s.

  The Builder may change a value with a measured justification in the hand-back.
- **Budgets.** These are the 5B budgets: a median of at most 250 ms (24 h) and 500 ms (30 d) at the 100 000-row seed, and 1 s and 2 s at the 1 000 000-row seed. They are measured by the pre-deployment command for one default query per core dataset.
- **`custom_chart` generalised.** Its series `source` accepts any dataset name, `measure`/`metric` any measure column of it, and `aggregation` that column's admitted aggregations.
  - The five existing sources answer byte-identically (B5).
  - `docs/widget-catalogue.md`, *Custom chart* (**Data**, **Parameters**), and `docs/widget-api.md`, *Series of `custom_chart`*, are amended by name. These are data contracts, and no aesthetic rule changes.
  - Open question 2 governs any binding beyond `time_series`.

**P9. Tests and checks on PostgreSQL.**
- **The everyday suite stays within its 60 s `go test` budget** (`TEST_BUDGET_SECONDS`, unchanged), with the tests still parallel.
  - Isolation is per test: a fresh database cloned from a template built once per run, or a per-test namespace. The Builder chooses by measurement and reports the figure.
  - TimescaleDB background workers are disabled in test databases (they also block cloning from a template). A test of anything a job would do invokes it explicitly on the test's clock.
- **The pre-deployment command stays within its 180 s budget (Phase E)** against the disk-backed instance. The 1 000 000-row seed is built once per run with set-based generation, then `ANALYZE`d before any plan check.
- **`sql/schema-checks.sh` is re-targeted to `psql`.** Every assertion keeps its label and its meaning:
  - **`expect_sql_failure`** matches the SQLSTATE class of an integrity-constraint violation (class 23) instead of the text "constraint failed". It still refuses a parse error or an undefined object as a false pass.
  - **Plan checks** use PostgreSQL's `EXPLAIN` (machine-readable format) on the scale seed after `ANALYZE`. Each asserts the same thing as before: the named index is used, and no sequential scan of a growing table occurs where the SQLite check asserted a search.
  - **Checks about the SQLite engine itself** (WAL companions, database files removed by exact path, RC-AC19) are restated against PostgreSQL: databases created by the run are dropped by exact name, an undeclared database beside them survives, and no wildcard drop exists.
  - Checks whose subject no longer exists (`PRAGMA`s) are retired, each listed in the hand-back with its reason.
- **Test SQL is translated by dialect only.** Raw SQL in Go tests (about 60 files call `.DB()`) is translated with no change of meaning.
  - `webtest`'s database scan (`DatabaseContains`, `DatabaseBytesExcept`) covers **every schema**, plugin and aside schemas included.
  - Engine-specific tests are retired in the B5 inventory with a named replacement or reason. Examples: `secure_delete` in `TestOverwritingACredentialLeavesNoPriorCiphertext`, the `-wal`/`-shm` companions in `store_test.go`, the busy-lock case of `schemaoutdated_test.go`. In the first, "exactly one row remains" stays.
- **The `Dockerfile`.**
  - `sqlite3` is replaced by the PostgreSQL client tools (`psql`, `pg_dump`) of the pinned major.
  - Its comments about the SQLite driver and cgo are updated.
  - No host tool is added.

**P10. Deployment: rebuild, never migrate.**
- The project still has no migrations. Deploying this cycle **rebuilds** the live installation's database in PostgreSQL. LV-0 (amended below) carries the user-input rows over, and the SQLite file is kept as the backup.
- A plugin whose declared schema changes is handled by P6. The core schema still changes in place until step 8 introduces migrations.

## 6. Scope: amendments inside existing phases

- **A3, *Durable state*.** Append: "PostgreSQL transaction, together with the pass's rows (P6)."
- **A4.**
  - Replace the bullet "Names are `plugin_<plugin>__<table>`… / No core table can collide with that pattern." with: "**Names and namespace:** P6."
  - Replace "Purged in the retention purge's transaction" with "Purged by the retention purge, with the exact horizon of P4."
  - In "Not part of the core schema checks…", replace "creates the tables" with "creates, through `psql`, the schemas and tables".
  - The *Schema change* bullets stay, with "renamed aside" read as "moved into the aside schema (P6)".
  - The **Declaration** bullet gains "role and unit per column (P6)" and "a unique key must include the time column (P6)".
- **B3, *The widget host API grants*.** Replace "running the widget's own declared queries on the read-only pool" with "running the widget's own declared queries on the read connections (P7)". Under *What the core keeps*, add "the data catalogue's query compiler (P8)".
- **B7.** Append: "Enforced twice: the load-time refusal, and the read role's privileges (P7)."
- **C3, *Persistence*.** Append: "Samples are stored in PostgreSQL (Phase P). Phase C starts only after Phase P passes."
- **D, `internal/store` target.** Replace "opening, the schema, the statement registry, the read pool, the query check, closed vocabularies" with "opening, the schema, the statement registry, the read role and connections, the query check, closed vocabularies". Add `store/catalogue` (dataset declarations and the P8 query compiler).
- **E.** Append: "The PostgreSQL test instance's start is not inside the measured wall time of `go test`, and it is inside the 180 s of the pre-deployment command."
- **F.2 table, `README.md` row.** Add "the install section (§13 of this amendment), backup (`pg_dump` plus key file), the storage paragraph" to the purpose. The size target stays.
- **F.2 table, `ROADMAP.md` row.** Add "the storage rule as replaced (§12); *Development and test environment* restated for the PostgreSQL test instance".
- **F.2 table, `docs/data-model.md` row.** Add "*Storage fit* (P4, P5)".
- **F.2 table, `docs/architecture.md` row.** Add "*Decisions in force* carries decision 13 with its date".

## 7. Acceptance criteria: new section inserted before "### Phase A: plugin host"

### Phase P: storage engine

- [ ] **P-AC1. Engine and pins.**
  - The PostgreSQL major and the TimescaleDB release are each pinned once, by version and digest, in every compose service that runs them.
  - `docs/architecture.md` cites their release notes and the licence matrix, and lists every TSL feature in use.
  - `go.mod` holds no SQLite driver. The PostgreSQL driver is direct, `go mod tidy` produces no diff, and `CGO_ENABLED=0 go build ./...` succeeds.
  - The driver's transitive list is recorded. No warning is suppressed.
- [ ] **P-AC2. Telemetry off, enforced.**
  - On the test, pre-deployment and serve database services, the telemetry setting reads off.
  - A test starting `opnview` against a database where it is not off sees a refusal to start, with the named error.
- [ ] **P-AC3. No published database port, no credential in the tree.**
  - No compose file publishes a port for a database service.
  - A test or check searches every tracked file and finds no database password literal. The test service's trust authentication is the one documented exception.
  - `opnview` started without the connection parameter exits naming it.
- [ ] **P-AC4. Single instance.** A second `opnview` started against the same database exits non-zero with the named error, and the first keeps collecting.
- [ ] **P-AC5. Schema idempotence.**
  - The schema applies to an empty database.
  - A second apply changes no schema object and no row count in any table, plugin schemas included.
  - A database lacking a column the schema reads yields `ErrSchemaOutdated`, detected by error code. A refused connection does not.
- [ ] **P-AC6. Engine equivalence.** On the fixture database, the following are equal to the SQLite reference outputs recorded before Phase P:
  - every widget response of B5's set, byte for byte;
  - the result rows of every query in `sql/queries/*.sql`, as typed rows;
  - every aggregate slot of every family and period.

  A difference is a defect unless it is a field Phase C changes by name.
- [ ] **P-AC7. Pinned semantics.**
  - A fixture with mixed-case and non-ASCII names orders exactly as byte order in every response that sorts text.
  - Every `LIKE` the hand-back lists keeps SQLite's ASCII case-insensitivity where the statement relied on it, with one test per such statement.
- [ ] **P-AC8. Purge semantics unchanged.**
  - Every existing purge, purged-part and retention test passes with unchanged assertions, including a retention shorter than an hour, `purged_before` refusing late inserts, and an assigned client surviving.
  - A test on a hypertable with several chunks shows whole chunks past the horizon dropped, the straddling chunk purged to the exact second, and nothing younger than the horizon removed.
  - No TimescaleDB background job runs in any table's purge path ordered with derivation.
- [ ] **P-AC9. Aggregate equivalence.** Every existing aggregate, refresh-contract and corrections test passes with unchanged assertions. In addition, an equivalence test computes every family slot independently from raw flows and compares it with the stored or computed slot, at `1h`, `24h`, `7d` and `30d`, over a fixture spanning:
  - a 28-, 29-, 30- and 31-day month;
  - an ISO week crossing a year boundary;
  - a paired connection;
  - a retention shorter than an hour;
  - a late ingestion;
  - a reclassification;
  - an identity re-pointing;
  - an owner reassignment.

  Gaps answer as gaps, never zeros.
- [ ] **P-AC10. Storage fit recorded.** `docs/data-model.md` *Storage fit* has one row per growing table and per aggregate family and period, each naming its mechanism, the reason, and the test from P-AC8 or P-AC9 that proves it.
- [ ] **P-AC11. Plugin schemas.**
  - A dropped plugin declaring two tables gets its own schema containing exactly those tables. Two plugins declaring the same table name do not collide.
  - The table with a time column is a hypertable.
  - A declaration whose unique key omits the time column is refused at load with the named code, and the service carries on.
  - A6 and A7 pass on PostgreSQL. A6's aside tables live in the aside schema, absent from the catalogue and unreadable by a widget query (refused at load).
  - A plugin removed from disk is listed as orphaned with its row counts, and its schema is untouched.
- [ ] **P-AC12. The read role.** A widget, plugin or catalogue query reading `account`, `session`, `encrypted_credential` or a credential or secret setting:
  - is refused at load with B7's code;
  - fails with a permission error when the same text is executed directly on the read connections (one test per object);
  - cannot gain privilege through a function call inside a `SELECT`.

  A write attempted on the read connections fails.
- [ ] **P-AC13. Concurrency machinery.**
  - No source file sets a SQLite `PRAGMA` or a busy timeout, or handles `-wal`/`-shm` files. `readpool.go` and the `EXPLAIN`-opcode scan are gone.
  - The ordering tests (`purge_ordering_test.go` and the refresh-contract race tests) pass with unchanged assertions.
  - 5B AC31 is restated and passes: a second call of a read function prepares nothing new on that connection.
- [ ] **P-AC14. Notifications.**
  - A committed collection pass, a plugin pass and an aggregate refresh each emit the documented notification, and a rolled-back one emits none.
  - An own-kind provider declaring an event trigger runs after the event, never more often than its lower bound, and never concurrently with itself (fake clock).
- [ ] **P-AC15. Catalogue listing.** `GET /api/datasets`:
  - lists the five `custom_chart` sources and every plugin table, with every P8 field;
  - lists no aside table and none of the excluded objects (string search over the payload);
  - answers 401 with a JSON condition without a session.
- [ ] **P-AC16. Catalogue query, generic.**
  - A test drops a plugin with an own table declaring a time column, a dimension and a measure with a unit, and queries it with time `x`, then dimension `x`, with a filter, a `split_by` and each admitted aggregation. **No file other than the plugin's directory is created or changed** (hashes compared).
  - The results equal an independent SQL computation on the fixture.
- [ ] **P-AC17. Catalogue safety.**
  - A parameter carrying SQL text, an undeclared column, a quoted identifier, a semicolon or a comment marker is refused with the closed code, and no statement reaches the database (statement counter).
  - Every refusal code of P8 has a test.
  - Every bound is enforced with its code.
- [ ] **P-AC18. Catalogue gaps and alignment.**
  - For a sample dataset, a bucket with no row is a gap.
  - For an event dataset, a covered bucket with no row is zero, and an uncovered one (a collection gap, downtime, an unavailable provider) is a gap.
  - Hour, day, ISO-week and calendar-month buckets start on the volume aggregates' period boundaries.
  - Under `no_domains`, a query touching a carries-domain column is `withheld_by_aggregate_mode` with `data: null`.
- [ ] **P-AC19. `custom_chart` generalised.** A `custom_chart` series whose `source` is the P-AC16 plugin dataset answers with the full 5B envelope. The five existing sources stay byte-identical (B5). The catalogue's *Custom chart* paragraphs and `docs/widget-api.md` match the code (the 5B parameter-table test).
- [ ] **P-AC20. Budgets.**
  - `checks` stays within `TEST_BUDGET_SECONDS` (60 s), printing its wall time beside the CPU count.
  - `pre-deployment` stays within 180 s, and its output shows the PostgreSQL scale seed built once, every 5B widget budget, and every catalogue budget of P8, each median beside its limit.
- [ ] **P-AC21. Schema checks re-targeted.**
  - `sql/schema-checks.sh` runs on `psql` and passes.
  - Its assertion labels are a superset of the baseline's, minus those the hand-back retires, each retirement with its reason.
  - `expect_sql_failure` accepts only SQLSTATE class 23.
  - Every plan check asserts index use on the analysed scale seed.
  - ShellCheck passes.
- [ ] **P-AC22. Backup round trip.**
  - The pre-deployment command dumps the 100 000-row seed database with `pg_dump`, restores it into a fresh database following the pinned release's documented restore procedure, and compares row counts per table across every schema. They are equal.
  - The README's backup command is the one the check runs.
- [ ] **P-AC23. Secret scan.** `webtest`'s database scan covers every schema, plugin and aside schemas included. AC16's existing tests (plaintext and ciphertext absent from responses; one credential row after an overwrite) pass on PostgreSQL.

## 8. Acceptance criteria: amendments to existing ones

- **A6.** Replace "its old table is renamed aside" with "its old table is moved into the aside schema (P6)".
- **A7.** Append: "on PostgreSQL, with the exact horizon of P4."
- **B7.** Append: "and P-AC12 passes."
- **B9.** Append: "on PostgreSQL."
- **X2.** Append: "The SQLite-era criteria whose subject was the engine are restated or retired as P9 lists, each in the hand-back."
- **F-AC5.** Add to the list: "every dataset, notification channel and refusal code named in `docs/widget-api.md` *Data catalogue* or `docs/plugins.md` exists in code, and the reverse."

## 9. Live validation: replace **LV-0** whole, and add **LV-DB**

**LV-0: database rebuild into PostgreSQL.** This amends `ROADMAP.md` step 5, *Rebuilding the database after step 5B*, by name. Every command is shown to the maintainer first, and every path is exact.

1. Take the 5B baseline queries on the old build's SQLite database (5B live validation, and V1–V10).
2. **Before the development image is rebuilt,** tag the pre-cycle image under an exact name, so its `sqlite3` stays available.
3. Stop the old service.
4. On the old database, list every user-input table and its row count, and show the list to the maintainer:
   - `account`;
   - `encrypted_credential`;
   - `setting`;
   - `owner`;
   - `interface.user_label`;
   - `blocklist.purpose`;
   - operator-set `interface_network` rows.
5. **Export** exactly those rows, plus any the maintainer chose, to files at exact paths outside the repository, using the tagged image's `sqlite3`.
6. **Move** `opnview.db` and, if present, its `-wal` and `-shm` companions aside, to exact paths shown first. **The move is the backup and the rollback:** running the old build with the file moved back restores the previous installation. The key file stays in the data directory.
7. Start the PostgreSQL service on a **new** named volume, start the new build once so it creates the schema, then stop `opnview` and leave the database running.
8. **Import** the exported rows by exact statements shown first. Then advance every identity sequence past the highest imported id, and show per table that the counts equal step 4.
   - MaxMind settings keep their keys (A6).
   - Sessions are not carried over.
9. Start the service. Take the first `pg_dump` with the README's command, and keep it beside the SQLite backup.

**LV-DB: the database on the live host,** reported to the maintainer:
- after at least 25 minutes of collection, the resident memory of the database and of `opnview`, and the database's disk use;
- the same figures for the old build;
- the host's processor share for both.

The README's *Install* section records these as the measured footprint, as proportions and sizes only.

**The validation passes when**, as before, plus: LV-0's counts match and LV-DB is accepted by the maintainer.

**LV-P** stays. Its new build runs against a scratch PostgreSQL database in the dev stack, never the live volume.

## 10. Rules: amendments

- **Deletion bullet.** Replace "The live `data` volume's `opnview.db*` is touched only by LV-0's shown steps." with:
  > "The live `data` volume's `opnview.db*` is touched only by LV-0's shown steps, and is moved, never deleted, in this cycle. The live PostgreSQL volume is created by LV-0 and is never dropped in this cycle."
- **The outbound rule, restated.** Append:
  > "The database connection is not an outbound call: it goes to the installation's own PostgreSQL, configured by the installer, over a Unix socket or the installation's private container network, through no published port."
- **Hard rules carried from the project.** Append:
  - "PostgreSQL and TimescaleDB versions are pinned, and changed deliberately;"
  - "TimescaleDB telemetry is off, and its absence is enforced at start;"
  - "no SQL text from the browser;"
  - "no background database job on a path the refresh contract orders."

## 11. Out of scope, and Risks

**Out of scope: replace** "Migrations (step 8). `schema.sql` is edited in place, and LV-0 handles the one installation." **with:**
> - Migrations (step 8). `schema.sql` is edited in place, a plugin's changed table is rebuilt (P6), and LV-0 rebuilds the one installation's database into PostgreSQL.
> - `ct/install.sh` and the deployment compose file for an always-on host: step 8, against the install story of §12.
> - Columnstore compression. Reclassification rewrites rows across the whole retention. A later measurement decides.
> - Continuous aggregates declared by a plugin. A plugin wanting summaries declares its own tables (A4), and the catalogue aggregates at query time.
> - Pushing notifications to a browser (step 7).
> - A second storage engine of any kind.

**Risks: append**

18. **Rewrite size.**
    - Every SQL statement changes dialect: `schema.sql` (about 3 300 lines), `purge.sql`, 26 statement files, `sql/queries`, `sql/seed.sql`, 404 schema checks and the raw SQL in about 60 test files.
    - The silent traps:
      - `INTEGER PRIMARY KEY` row ids;
      - `strftime` calendar arithmetic;
      - `GLOB`;
      - the `json_*` functions;
      - `INSERT OR …` and the upsert conflict targets;
      - unique indexes over `ifnull(…)`;
      - partial indexes;
      - `PRAGMA`s;
      - `LIKE` case sensitivity;
      - text collation;
      - `sum`/`avg` over integers returning `numeric` (different JSON formatting);
      - integer overflow erroring instead of turning into a real number.
    - P-AC6 and P-AC7 catch them. The fix is in the statement, never in loosening a comparison.
19. **Test-time budget.**
    - Today's 60 s holds at about 12 s on SQLite with a tmpfs. Every test that opens a store now pays a database clone and a network round trip per statement.
    - Template cloning is blocked while TimescaleDB background workers hold the template open.
    - If 60 s cannot be met without moving tests behind the `predeployment` tag, the Builder reports the figures rather than moving them silently.
20. **Continuous-aggregate fit.**
    - Invalidation tracks changes to the hypertable's own rows, not to joined tables such as `client.owner_id`.
    - Distinct counts do not compose across hierarchical aggregates.
    - Calendar months need a timestamp-typed time column.
    - Creating a continuous aggregate may not run inside a transaction.
    - Possibly no family qualifies. P5 makes that an acceptable outcome rather than a reason to loosen equivalence.
21. **Hypertable constraints.** `flow.log_digest UNIQUE` and the foreign key into `flow` may keep `flow` a plain table. Whether a foreign key referencing a hypertable is supported depends on the pinned release, and is verified, not assumed.
22. **Clocks.** TimescaleDB jobs and `now()` run on the server's clock, not opnview's `Clock`. Any background policy would escape the fake-clock tests and the purge/derive ordering. That is why P4 and P5 drive retention and refresh from opnview.
23. **Licence.** Continuous aggregates (and any policy or compression) are TSL. If the maintainer ever wants an Apache-only database image, every TSL feature in use must have a fallback. P-AC1 lists them so that cost stays visible.
24. **Resources on small hosts.** A PostgreSQL + TimescaleDB container idles at a few hundred MB of memory, against a single process before. Its default memory settings may not suit a small LXC. LV-DB measures it, and the README states it.
25. **Docker in an unprivileged LXC** needs nesting, which some Proxmox administrators disable. Step 8's installer must detect and name this.
26. **Updates of the database itself.**
    - A TimescaleDB release bump requires the extension update procedure that release documents.
    - A PostgreSQL major bump requires a dump and restore, or `pg_upgrade`.
    - Both are step-8 update-story items. Pins keep them deliberate.
27. **Credential residue.** A replaced credential row leaves its previous version in the table's storage until PostgreSQL vacuums it, in the write-ahead log segments, and in any earlier `pg_dump`. This replaces the SQLite WAL residue, and the README says so in the same words of limit: encrypted under the key file, a superseded secret only.
28. **Reference outputs.** P-AC6 needs SQLite outputs recorded before Phase P. If the in-flight builder already changed widget or store code before they are recorded, the reference falls back to the baseline snapshot's code run in the tagged pre-cycle image, which must stay available until verification ends.

## 12. ROADMAP.md: replacement texts

**Replace the whole bullet** "**One service, installed and updated by a single command, with no external data store to provision. Storage engines are embedded libraries, not servers.** … `README.md` and `CLAUDE.md` point at this bullet rather than restating it." **with:**

> - **One service and its database, installed and updated by a single command. The user provisions nothing by hand.** The store is **PostgreSQL with the TimescaleDB extension** (the maintainer's decision of 11 October 2026). It runs beside `opnview` as part of the same installation: the installer creates, configures and starts it, and the user never creates a database, a role or a password.
>
>   This replaces an earlier rule, written by the orchestrator rather than the maintainer, that storage engines be embedded libraries and that forbade PostgreSQL and TimescaleDB. What that rule protected was the install story, and the install story is kept:
>   - one command installs;
>   - re-running it updates;
>   - one documented command backs up: `pg_dump`, plus the key file in the data directory.
>
>   What changed is the judgement on the cost. SQLite's single writer, the hand-built calendar rollups and one shared file for every plugin's tables cost more than a second process does.
>
>   **Versions are pinned:** the PostgreSQL major and the TimescaleDB release, by exact version and image digest, and they are changed deliberately.
>
>   **Licence:** TimescaleDB's core is Apache-2.0. The features `opnview` uses beyond it (continuous aggregates, listed in `docs/architecture.md`) are under the Timescale License, which permits this self-hosted use. `opnview` stays MIT and neither links nor redistributes TimescaleDB.
>
>   **TimescaleDB's telemetry is off,** and `opnview` refuses to start if it is not.
>
>   **Not permitted:**
>   - a second storage engine or data store, without a maintainer decision;
>   - a database reachable through a published port;
>   - a database credential in the repository.
>
>   `README.md` and `CLAUDE.md` point at this bullet rather than restating it.

**Append to the "Three outbound calls" bullet** (as restated by this spec's *Rules*):
> "The connection to the installation's own database is not an outbound call: it goes to the PostgreSQL the installer set up, over a Unix socket or the installation's private container network, and to no other host. Images pulled at install or update time are installation-time downloads, not calls the product makes."

**In *Development and test environment*:**
- Replace the sentence naming "file mode and umask on the SQLite file and its `-wal` / `-shm` companions" with "file mode and umask on the data directory and the key file".
- Replace the tmpfs paragraph's SQLite fsync justification with "the everyday suite's PostgreSQL test instance keeps its data on a tmpfs with durability off, which nothing the suite asserts depends on; the pre-deployment command runs against a disk-backed instance with production settings".
- Replace "the SQLite data directory live in named volumes" with "the database's data live in named volumes".

**Step 8 row and section.** Deliverable: "`ct/install.sh`, compose (opnview + PostgreSQL/TimescaleDB), README". Add to its section:
> "The single command installs Docker Engine and its compose plugin when absent (an unprivileged Proxmox LXC needs nesting enabled), writes the compose file and a generated database credential into the installation directory, and starts both containers. Re-running it updates: it pulls the pinned images and restarts, keeping the database volume and the data directory. Database version bumps follow the procedure each release documents."

The step-2 title "Data model and SQLite schema" is condensed by Phase F, and should read "Data model and schema".

## 13. README.md and CLAUDE.md

**README, replace lines 26–31** ("**One service, installed and updated by a single command, with no external data store to provision.** … read-only.") **with:**
> **One service and its database, installed and updated by a single command, with nothing to set up by hand.** The database is PostgreSQL with TimescaleDB, run beside `opnview` by the installation itself. See the storage rule in [ROADMAP.md](ROADMAP.md), under *Rules that apply to every step*. The frontend is served by the service. Nothing is installed on the firewall and nothing is changed on it: everything goes through its REST API, read-only.

**README, new section *Install*, placed after *Status*:**
> ## Install
>
> `opnview` runs as two containers: the service, and its PostgreSQL + TimescaleDB database. The database publishes no port and is reachable only by `opnview`. Nothing is installed on the firewall.
>
> Until the installer ships (step 8), run it from source:
> `<the serve invocation, bash and PowerShell, as docker-compose.serve.yml states it>`.
>
> Step 8 replaces this with one command for an always-on Debian host or a Proxmox LXC. The LXC needs nesting enabled, which Docker requires. The command:
> - installs Docker Engine and its compose plugin if they are absent;
> - writes the compose file and a generated database password into the installation directory;
> - starts both containers.
>
> Re-running it updates: it pulls the pinned images and restarts, and your database and data directory are kept.
>
> Then open the address it prints. The console prints a one-time setup token, and the setup surface demands it.
>
> **Backup is two things:** the database, with `<the pg_dump command P-AC22 runs>`, and the key file in the data directory. The dump without the key file restores your history but not your stored credentials. Restore into the same TimescaleDB version.
>
> **Footprint**, measured on a live installation: `<LV-DB figures>`.
>
> Installing pulls the two pinned container images. That is a download made by the installation, not a call `opnview` makes (see *What leaves your installation*).

**README, other paragraphs.**
- **Remove** "A power cut can roll back the last moments of collected history…". Production keeps PostgreSQL's default durability. If the Builder measures that the collector needs relaxed durability, that is reported as a maintainer decision, not taken.
- **Replace** "Replacing a credential leaves the previous one in the write-ahead log until a checkpoint…" with the same limit stated for PostgreSQL (Risk 27).
- **In the key-file paragraph,** replace "a file beside the database" with "a file in `opnview`'s data directory, outside the database". Replace "back up the key file with the database" with "back up the key file with the database dump".
- **In *Licence*,** add the TimescaleDB licence sentence of P1.

**CLAUDE.md, proposed wording.** This is for the orchestrator to apply with the maintainer's agreement; the Builder does not edit it.
- **Project, second paragraph.** Replace "**One service, installed and updated by a single command, with no external data store to provision; storage engines are embedded libraries, not servers** — the permitted and forbidden engines are listed in `ROADMAP.md` under *Rules that apply to every step*, and that bullet is the single place the rule is stated. Local SQLite, frontend embedded in the binary and served by it." with:
  > "**One service and its database, installed and updated by a single command; the user provisions nothing by hand.** The store is PostgreSQL with TimescaleDB, run by the installation beside the service. The rule, its pinned versions, its licence and what it forbids are in `ROADMAP.md` under *Rules that apply to every step*, and that bullet is the single place the rule is stated. Frontend embedded in the binary and served by it."
- **Development section.** Replace "Never run `go`, `gofmt` or `sqlite3` on the host." with:
  > "Never run `go`, `gofmt`, `psql` or `pg_dump` on the host; the test database is a compose service the two commands start themselves."

## 14. Existing spec sentences that become obsolete (to delete or replace as indicated above)

1. Context, the `plugintables.go` verdict: "*Compatible, with one defect to change:*" (replaced by §3).
2. B4 table row: "| SQLite (modernc), goja, Argon2id | store, plugin host, auth | Stay core: an embedded engine (ROADMAP), the interpreter, and a mandated algorithm |" (replaced by §3).
3. Scope, the phase list "**Phase A.** … **Phase E.** The pre-deployment budget." (replaced by §4).
4. A4: "Names are `plugin_<plugin>__<table>`, as `plugintables.go` builds them." and "No core table can collide with that pattern." (replaced by P6).
5. A4: "**Purged** in the retention purge's transaction:" (replaced, §6).
6. A4: "The existing table is **renamed aside** under a name that can never be read as a live plugin table" (read as the aside schema, §6).
7. B3: "running the widget's own declared queries on the read-only pool with bound parameters and a time bound" (replaced, §6).
8. Phase D: "`internal/store`: opening, the schema, the statement registry, the read pool, the query check, closed vocabularies." (replaced, §6).
9. Out of scope: "Migrations (step 8). `schema.sql` is edited in place, and LV-0 handles the one installation." (replaced, §11).
10. LV-0, steps 1–7 as written ("**Move** `opnview.db` … Start the new build so it creates the schema, then stop it. … Carry over … by exact statements shown first.") (replaced whole, §9).
11. Rules: "The live `data` volume's `opnview.db*` is touched only by LV-0's shown steps." (replaced, §10).
12. A6 criterion: "Its old table is renamed aside" (replaced, §8).

## 15. Open questions (additions)

The existing open question 1 stays answered (a).

2. **A generic dataset binding for shapes other than `time_series`.** P8's query already supports a dimension on `x` (ranked by a measure). Today only `custom_chart` (`time_series` with no binding) can take a dataset without code. Options:
   - **(a)** This cycle opens datasets to `custom_chart` only. Whether `ranked_bars`, `table` and `figure` get an unbound counterpart is decided with step 7, against `docs/widget-catalogue.md`.
   - **(b)** This cycle also adds unbound counterparts of `ranked_bars` and `table` to the binding catalogue (new types, data contracts only).

   **Recommendation: (a).** The query endpoint serves both kinds of `x` now, so step 7 needs no backend work either way. Adding types to a binding interface document is better decided where they will be drawn. Scope follows (a) unless the maintainer chooses (b).

No other question. The install shape (docker compose), the engine and the aggregate fallback were decided by the maintainer or follow from P5's equivalence gate.

---


---

**This amendment is part of the spec and overrides any earlier text it replaces. Its section 12 lists the earlier sentences that are obsolete.**

# Amendment to `specs/SPEC-backend-completion-plugins.md`: the firewall data broker and the learned API mirror (maintainer decisions, 11 October 2026)

**Placement.** Append this after the PostgreSQL + TimescaleDB amendment, with its own `---` separator and the same preamble: "This amendment is part of the spec and overrides any earlier text it replaces. Section 12 lists the earlier sentences that are obsolete." **Cycle kind stays `standard`.** Everything below is Go, SQL, JSON contracts, server-side JavaScript and documents. The API catalogue is served as JSON only.

**What I read and found (fact check of the brief).**

- **Firewall callers today.** `Client.Call` / `Client.Stream` are called directly from five places:
  - `internal/collect/provider/session.go` (`Host`: `Call`, `Stream`, `ServiceState`, `ReadObject`, `ReadCollection`);
  - `internal/collect/discovery.go` (`interfaces_info`, `get_interface_names`, `search_rule`, `systemTime`);
  - `internal/collect/dhcplease.go:386`;
  - `internal/plugin/script.go` and `declarative.go`, through the `Session`;
  - `internal/web/surfaces/settings/verify.go`, which verifies credentials before they are saved.

  Nothing coalesces, caches or rate-limits these calls. Each core kind runs its own loop at its own interval (`internal/collect/scheduler.go`, `runLoop`).
- **The plugin host hands a script the raw body.** `host.call` answers with `body` (a string) and `json()` (`script.go`, `responseObject`). The built-in plugins decode that body with `opnview.decode.rows(body)` and the other readers. The host has no way to know which fields a script reads.
- **Streams are read in bounded chunks today.** `host.stream(name, {events, within_seconds})` opens a connection, reads N events and closes it (`stream.go`). Whether a response is a stream is a manifest flag (`stream: true`), not a detection.
- **The read-only check is the mutating-command list, at load and at every call.** `plugin_endpoints.go` lets any POST through and says in a comment: "No allowlist is added on top … adds nothing else". Every POST endpoint the built-ins use today passes the new read rule: `search_rule`, `query_alerts`, `leases4/search`, `dnsmasq/leases/search`, `search_queries`. `carp_status` ends in `status`. On a POST it changes the CARP state, so the mutating list must keep being checked first.
- **Survey facts the inference must respect, not trust:**
  - `search_queries` ignores `timeStart`/`timeEnd` unless all of `client`, `timeStart` and `timeEnd` arrive as JSON integers, and its `uuid` is `null` on every row;
  - the filter log's `digest` parameter has no effect;
  - Suricata's resumable cursor is the pair `(fileid, filepos)`, and `current`/`rowCount` paging is not stable for resuming;
  - grid searches take `current`, `rowCount` (`-1` meaning all rows), `searchPhrase` and `sort`;
  - `traffic/top` is one iftop capture of about 2 s per interface, per call;
  - `stream_log` is closed by the server after about 60 s;
  - the first event of `cpu_usage/stream` after each connection is the average since boot, not a current reading.
- **`traffic/stream` is `UNVERIFIED:`** (C3). Nothing below is built on it. Its tests use a fake stream.

---

## 1. Context: insertion after decision 13, as decision 14

> 14. **A firewall data broker and a learned API mirror** (maintainer, 11 October 2026, arbitrated on a visual page).
>     - **The plugin names the endpoint; the core makes the call.** A plugin author chooses the OPNsense endpoint and its parameters exactly as the API takes them, and states how old an answer may be and still be good enough (its **max age**). Plugins never hold the API key and never reach the firewall directly.
>     - **The core learns what it reads.** At the first request for an endpoint, the core calls it, observes the request and the response, infers a schema and stores the answer in a PostgreSQL schema `opnsense`: **the mirror**.
>       - Only fields a plugin actually reads are stored.
>       - The schema evolves as plugins read more fields.
>       - Whether a datum is a snapshot or a record stream, its identity key, its cursor and the role of its parameters are inferred. A plugin may correct any inference in its manifest.
>     - **Serving.** An answer already in the mirror and fresh enough is served from PostgreSQL with no firewall call. Otherwise **one** call refreshes it for every requester waiting.
>     - **Scheduling.** The scheduler serves the nearest deadline first, with a reserved share of the call budget for real-time data, ageing so that background data is never starved, global ceilings, and exponential back-off.
>     - **Read-only is a core guarantee enforced by the broker for every plugin:** GET, and POST to read actions only. This adds no sandbox, no plugin allowlist and no activation step.
>     - **Continuous event streams.** One subscription per stream, fanned out to every subscriber, recorded into a TimescaleDB hypertable when its fields are read. A plugin may declare that a stream replaces a polled endpoint.
>     - **A learned API catalogue,** readable through the JSON API, that also feeds the data catalogue (P8).
>     - **External downloads never enter the firewall queue.**
>
>     This decision supersedes the sentence of decision 2: "Firewall calls go through opnview's client, whose refusal of mutating commands is unchanged, with nothing added on top". The refusal is unchanged, and the broker's read rule is added on top of it.

## 2. Context, *Where the repository stands*: append these bullets

> - **Firewall calls today (read for decision 14).**
>   - `Client.Call` and `Client.Stream` are called directly from the provider `Host` (`internal/collect/provider/session.go`), from discovery (`internal/collect/discovery.go`), from `internal/collect/dhcplease.go`, from the plugin host through the `Session`, and from the credential check of the settings surface (`internal/web/surfaces/settings/verify.go`).
>   - Each core kind runs its own loop at its own interval (`internal/collect/scheduler.go`).
>   - Nothing coalesces two requests for the same answer, caches an answer, or bounds the call rate toward the firewall.
> - **The plugin host's firewall answer** is `{endpoint, outcome, ok, status_code, detail, body, body_bytes, availability_outcome, error, json()}`, with the body as raw text (`internal/plugin/script.go`). Streams are read as bounded chunks (`host.stream`, `internal/opnsense/stream.go`). Whether an endpoint streams is a manifest flag.

## 3. Scope: phase order

In the phase list as replaced by the PostgreSQL amendment (§4 there), insert between **Phase P** and **Phase B**:

> **Phase A, part 2.** The firewall data broker, the learned API mirror, the deadline scheduler, event-stream subscriptions and the learned API catalogue (A9–A18). It is built **after Phase P**, because the mirror lives in PostgreSQL. It is complete, and its criteria (BR-AC) pass, **before**:
> - the remaining Phase B work;
> - all of Phase C.
>
> The built-in plugins already written are re-routed through it (A18).

## 4. Scope: new section, inserted after Phase P (after P10, before "### Phase B")

### Phase A, part 2: the firewall data broker and the learned API mirror

**A9. The components.** These are three responsibilities of the Go core. The package names are the Builder's, under Phase D's rule.

- **The broker** is the **only** path from opnview to the firewall API.
  - It receives every request: from plugins, from discovery, from the probe round, from the core kind coordinators.
  - It applies the read-only rule (A13).
  - It answers from the mirror when the mirror is fresh enough (A11). Otherwise it queues one call with the scheduler.
  - It hands the answer to every requester waiting for it.
  - It is the only caller of `Client.Call` and `Client.Stream`.
  - **The one exception is the settings surface's credential check** (`verify.go`). That check tests *candidate* credentials that are not yet saved, against a firewall that may not be the configured one. Its answer must never enter the mirror. It stays a direct call, and it is listed as the exception in `docs/architecture.md`.
- **The mirror** is the PostgreSQL schema `opnsense`, together with its metadata. It stores what the broker observed (A10), serves it (A11) and records the learned API catalogue (A15).
- **The scheduler** decides when each mirrored datum is refreshed and in what order calls leave (A12). Concurrently, it holds open the event-stream subscriptions (A14).
- **Vocabulary.** OPNsense has no term for "broker", "mirror", "datum", "max age" or "freshness floor".
  - These are opnview's own terms, recorded in `docs/data-model.md` *Vocabulary* with the reason.
  - "Event stream" and "server-sent events" are the WHATWG HTML standard's terms and are used as such.
  - A **datum** is one endpoint with one combination of identifying parameters (A10).

**A10. The learned mirror.**

- **First request.** The first request for a datum is a firewall call. The broker records the request (path, method, body encoding, positional arguments, query, body) and the response (status, content type, outcome, latency, body).
- **Only fields actually read are stored.**
  - A plugin receives the parsed answer as an **observed object**: reads are recorded per field path (for example `rows[].hwaddr`, or `total`). Whether the object is a JavaScript `Proxy` or a host-side dynamic object is the Builder's choice.
  - The broker records which field paths each plugin read, per plugin version and per datum.
  - A field no loaded plugin has read is not stored.
  - When a plugin reads a field for the first time, the column is added and filled from the next call (see *No missing field because of the mirror*).
  - A plugin that reads the raw text of the body, or serialises or enumerates a whole object, has read every field below that point. The broker records exactly that, and the catalogue reports the datum as `read_whole`.
- **No missing field because of the mirror.** A plugin must never see a field as absent only because the mirror did not hold it.
  - When an answer served from the mirror is asked for a field the mirror does not hold, the broker obtains that field's value from a firewall call made at that moment, at most one per datum.
  - The values the plugin sees must stay consistent row by row. A value is never taken from a different row of a different answer.
  - The mechanism is the Builder's. One option is to re-run the plugin's function against the fresh answer, since a call's records are committed only after it returns. The Builder states the choice.
- **Inferred types.**
  - The types are IP address, MAC address, timestamp, number, text and boolean.
  - Timestamps are recognised only in the shapes `internal/decode` reads, with the firewall's measured offset.
  - Nested objects are flattened, or become child tables. Arrays become rows of a child table, and their order is kept.
  - **An object whose keys are values** (its key set differs between answers, or its keys are identifiers such as interface names) is stored as rows keyed by that key, **never as one column per key**. This keeps installation data such as interface names out of the schema.
  - A value whose type is ambiguous, or that changes type, is stored as JSONB.
- **Lossless round trip.** Every field served from the mirror is identical, in JSON type and in lexical value, to the firewall's answer that filled it. This includes:
  - numeric strings with leading zeros;
  - placeholder strings such as `~`;
  - `null` against an absent field;
  - the empty string;
  - integers beyond 2^53;
  - float formatting;
  - row order.

  The inferred type serves the catalogue and queries. It never changes what a plugin reads.
- **Schema evolution, never a rebuild.**
  - A new field read becomes a new column.
  - A type conflict converts the column to JSONB without loss.
  - Every change records a new schema version, with its instant and its cause.
  - A column no loaded plugin reads any more stops being filled and is kept, with its rows, until retention. Nothing is dropped silently.
  - This is the core's own mirror. A4's rebuild rule for **declared** plugin tables is unchanged.
- **Nature.** Each datum is inferred to be one of two natures:
  - **a snapshot** (leases, interfaces): the current state plus a change history, a history row being written when a stored field of a row changes;
  - **a record stream** (logs, DNS lookups): a **cursor** is detected, either a timestamp or a monotonic id, and reads are incremental.
- **Identity key.** The key is inferred from MAC, IP or id fields.
  - A candidate key must be non-null on every row and unique within one answer. A field that is `null` on every row (Unbound's `uuid`, for example) is never a key.
  - A snapshot with no key is stored as whole generations.
  - A record stream with no key is de-duplicated on the stored content of its rows.
- **Parameters.** Parameters are recognised from their names and values:
  - the start and end of a time window;
  - the page and the page size (the grid-search set `current` / `rowCount`, with `rowCount = -1` meaning all rows);
  - everything else.
  - **Window and page parameters do not identify a datum.** The other parameters do, so each combination of them is one subset of the mirror.
  - **The core handles pagination.** It walks pages until the answer is complete, within the bound the request states.
  - **A recognised window is verified, never trusted.** If a returned row's cursor lies outside the requested window, the parameter is recorded in the catalogue as `not_honoured` and is no longer relied on. The datum is then read in full and filtered on the cursor. This is the survey's finding for `search_queries` when `client` is absent and for the filter log's `digest`.
- **Gaps.**
  - In a record stream, if the oldest row of an answer is newer than the newest row stored, a **gap** is recorded for that interval, never bridged.
  - A failed call is recorded as an unanswered interval, never as an empty answer.
- **Corrections in the manifest.** A plugin may correct any inference for an endpoint it declares:
  - the nature;
  - the key, which may be composite (for example `hwaddr`);
  - the cursor, which may be composite (for example `fileid, filepos`) or a timestamp;
  - the role of each parameter;
  - the type of a field;
  - whether a field carries a domain name;
  - for a stream, how many events to discard after each connection;
  - a stream's declared equivalence (A14).

  A correction always overrides inference. Two loaded plugins correcting the same datum differently: the built-in wins, and otherwise the plugin that loaded first wins. The other is listed with the code `mirror_correction_conflict`, and it still loads. **The core never needs to know an endpoint in advance.**
- **Domain names.** A mirrored text column is marked as carrying a domain name:
  - when a reading plugin's correction says so; or
  - when an observed value has host-name shape, unless a correction says it does not.

  Under `aggregate_mode = no_domains`, the data catalogue withholds such a column (P8). The API catalogue never contains values (A15).
- **The firewall changes.** When the configured base URL changes, nothing in the mirror is fresh any more, and nothing is served as fresh. History is kept, marked with the firewall it came from (a non-secret identity of the base URL; the Builder's choice). It is never mixed into the new firewall's current state.
- **Retention.**
  - A mirror table follows `retention_seconds`, with the exact horizon of P4, unless a mirror retention setting overrides it.
  - A record stream with a timestamp cursor is a hypertable on that cursor where P4's constraint rule allows it. Otherwise it stays a plain table, and *Storage fit* records the reason.
- **A failed mirror write never fails a request.** The firewall's answer is still served, the failure is logged, and the datum is treated as not mirrored.

**A11. Serving from the mirror.**

- **A request names a datum and a max age.** If the mirror holds that datum's answer and it was fetched within the max age, the answer is served from PostgreSQL. **No call is made.**
- **Otherwise exactly one call is made** for that datum. Every requester waiting for it, from any plugin or from the core, receives that one answer. This is single-flight per datum, on top of the mirror.
- **Every answer carries how it was served:**
  - `served_from` (`mirror` or `firewall`);
  - `fetched_at`;
  - `age_seconds`.
- **An unsuccessful outcome counts for freshness without replacing data.**
  - A non-OK outcome (`not_found`, `forbidden`, `server_error`, `transport_failure`, …) is remembered for freshness, so N plugins asking an absent endpoint cause one call.
  - It never replaces mirrored rows.
  - It is served with its outcome and its `fetched_at`. A3's availability mapping is unchanged.
- **Reading a record stream after a cursor.** A plugin may ask for the rows after a cursor value it holds. Those rows are served from the mirror, together with any gap that falls inside the range.

**A12. The scheduler: deadline-first, with a real-time reserve.**

- **Demand.**
  - Every request registers or renews its requester's demand for a datum at the requester's max age.
  - A demand lapses when it has not been renewed for a span the Builder states, at least twice its max age.
  - A datum with no live demand is not refreshed. **An endpoint's cadence follows its most demanding reader, and drops when nobody reads it.**
- **Deadline.** A datum's deadline is its last fetch plus the shortest max age among its live demands.
  - The scheduler refreshes data ahead of their deadlines, nearest deadline first, so that a reader's pass normally finds the mirror fresh.
  - **The broker never has two calls in flight for one datum.**
- **Freshness floor.** Each endpoint has a floor, the shortest refresh interval it is ever given. The floor is the largest of:
  - the endpoint's observed median latency;
  - a floor the built-in manifest declares from the LV-C measurement (`traffic/top` can never go below its capture of about 2 s);
  - a floor a third-party manifest declares;
  - the operator's override setting.

  A request asking for less is served at the floor. Its answer's `age_seconds` says so, and the catalogue lists the endpoint's floor.
- **The real-time reserve.**
  - A share of the call budget is reserved for real-time data. The share is a setting, default **70 %**. Real-time data is a datum whose shortest live max age is in the real-time class (open question 4): current throughput, connected devices, active addresses and domains with their rate.
  - Background data (interface names, leases, logs) uses the rest.
  - The reserve is **guaranteed, not a cap**: while no real-time datum is due, background data may use it.
- **Ageing.** A background datum gains priority the longer it waits past its deadline, so none is starved. **Its lateness under sustained real-time load is bounded**, and the Builder states the bound as a function of the settings.
- **Ceilings.** Two settings:
  - a maximum number of calls in flight toward the firewall;
  - a maximum number of calls per second.

  The proposed defaults are 2 and 5. The Builder may change them, with the LV-B figures, in the hand-back.
- **Back-off.**
  - A datum whose calls end in `server_error`, `transport_failure` or a time-out backs off exponentially, up to a setting.
  - When failures or time-outs span several endpoints (the firewall is saturated or unreachable), the whole queue backs off.
  - Back-off ends at the first successful answer.
  - `forbidden` and `not_found` are states, reported through availability and retried at the reader's own cadence. Back-off does not apply to them.
- **What the existing loops become.**
  - The per-kind loops of `scheduler.go` remain the plugins' **passes**. A core kind's interval setting becomes its readers' max age.
  - Discovery and the probe round are core readers whose max age is the discovery interval.
  - A plugin still never sleeps or schedules (A3).
  - All the setting keys above are new rows of the `setting` table. The proposed names are in the hand-back, and an out-of-range value is refused at load, naming the key.

**A13. Read-only, enforced by the broker for every plugin.**

- **What the broker forwards:**
  - **GET**;
  - **POST** only to a read action: a command (the fourth path element, compared in its snake_case spelling as `MutatingCommand` does) that **starts with `search`, `get`, `query` or `list`, or ends with `status`**.
- **Everything else is refused before a request object exists.** The refusal is logged with the plugin's name, the path and a closed code (`write_refused`), and counted in the catalogue.
- **`MutatingCommand` stays on top and is checked first.** So `carp_status`, which ends in `status` and changes the CARP state on a POST, is still refused.
- **Two places, one rule.** `plugin_endpoints.go` also refuses, at load, a declared POST that is not a read action, with a new code that the A5 listing reports. The broker checks every request at run time, the core's own included.
- **This is not the gas factory the maintainer rejected.** There is no sandbox, no allowlist of plugins and no activation step, and plugins stay free in everything else.
- **The README keeps its advice** that the API key should hold read privileges only (Risk 6).
- **Plugins never hold the credentials.** No host function answers a credential, a base URL or an authorisation header.

**A14. Continuous event streams.**

- **Detection.** A response with `Content-Type: text/event-stream` is detected as a stream **from its headers, before any body is read**, so a stream never holds a call until the client's time-out.
  - The manifest's `stream` flag becomes optional. Where it is present and disagrees with what was observed, the catalogue reports the mismatch.
  - The catalogue records, per stream: its endpoint, its event shape, the observed cadence of events, and the observed server cut-off (for example about 60 s for `stream_log`).
- **One subscription per stream and parameters.**
  - Its events are fanned out to every subscribed plugin.
  - It opens at the first subscriber and closes at the last, **unless it feeds a table**. A stream feeds a table when a loaded plugin reads its fields, and it is then kept open while that plugin is loaded.
- **Reconnection.**
  - A dropped subscription reconnects with back-off.
  - The interval with no events is recorded as a **gap, never zeros**.
  - The events a manifest declares to discard after each connection are discarded. The built-in `insight` plugin declares one for `cpu_usage/stream`, whose first event is the average since boot (survey, *Processor stream*).
- **Recording.** When at least one plugin reads fields of a stream:
  - those fields go into a TimescaleDB hypertable in the `opnsense` schema, timestamped at reception;
  - the detail is kept for a short retention (a setting; the Builder states the default with the disk figure measured on the scale seed);
  - **continuous aggregates per minute and per hour** keep, for each numeric field read, its minimum, maximum, mean, last value and event count, for `retention_seconds`;
  - aggregate refresh and detail retention are driven by opnview on its own clock, not by a TimescaleDB background policy, which keeps the fake-clock tests and the rule of P4 and P5;
  - P1's list of TSL features in use gains these aggregates.
- **Plugins read streams without waiting.**
  - A plugin's pass reads the events received since a cursor it holds, from the subscription's buffer or from the hypertable.
  - A provider of a kind of its own may be triggered by a core notification that names the stream (the P7 mechanism, with its lower bound).
  - The bounded `host.stream` read of today is answered from the shared subscription.
- **Declared equivalence.**
  - A plugin may **declare** in its manifest that a stream replaces a polled endpoint (for example throughput).
  - While the stream runs, that plugin's demand for the polled endpoint is suspended. Other readers of the endpoint keep their own demand.
  - When the stream drops, the demand resumes from the next scheduling decision, and the plugin is told which source answered.
  - **Equivalence is declared, never guessed.**
  - Nothing is built on `traffic/stream` while it is `UNVERIFIED:` (C3). The proof uses a fake stream.

**A15. The learned API catalogue.**

- **Content.** It records:
  - every endpoint seen, with method and body encoding;
  - every recognised parameter with its role, and whether it is honoured;
  - per datum, the inferred nature, key, cursor and schema, with every schema version;
  - the corrections applied, and who applied them;
  - the observed change frequency;
  - call cost (median and 95th-percentile latency, response size);
  - the freshness floor;
  - the streams, with their shapes, cadence and cut-off;
  - the plugins reading each datum, with their max ages and the field paths they read;
  - the mirror hit ratio;
  - the counts of refusals (`write_refused`).
- **Access.**
  - It is served as JSON, authenticated, and documented in `docs/widget-api.md` under a new section *API catalogue*. The path is the Builder's.
  - Plugins read it through `host.catalogue()`.
  - **It never contains a field value, a credential, the base URL or an authorisation header.** A third-party author reads it before writing a line.
  - An endpoint that a third-party plugin reads with no `upstream_url` is listed as `uncited`.
- **It feeds the data catalogue (P8).**
  - Every mirror table and stream hypertable is a dataset with origin `opnsense`.
  - Roles are inferred from types: timestamp → `time`, number → `measure`, everything else → `dimension`. They can be corrected in the manifest.
  - Stream hypertables and snapshots are **sample** datasets. Record streams are **event** datasets, whose covered buckets are those outside recorded gaps.
  - The `no_domains` rule of P8 applies through A10's domain-name marking.

**A16. External downloads never enter the firewall queue.** The MaxMind and Public Suffix List downloads, and any third-party plugin's internet calls, go through the A7 fetch facility only. They take no broker slot, count against no firewall ceiling, and never enter the mirror.

**A17. The plugin host API changes** (A3 amended; `docs/plugins.md` *The host* is rewritten to match).

- **`host.call`, `host.readCollection`, `host.readObject` and `host.serviceState` go through the broker.**
  - Each accepts a `max_age_seconds`. Its default is the endpoint's manifest default, then the kind's interval for a core kind, then the provider's cadence for a kind of its own.
  - Each accepts an optional `after` cursor value for a record stream.
  - Each answers with an observed object (A10), plus `served_from`, `fetched_at`, `age_seconds` and the gaps inside the range.
  - Every field the answer carries today keeps its name and meaning. Reading `body` or calling `json()` counts as reading every field (A10).
- **Streams:**
  - a subscription to an event stream, renewed by use, that lapses like a demand;
  - reading the events received after a cursor, with the gaps;
  - `host.stream`'s bounded read, kept, answered from the shared subscription.
- **`host.catalogue()`** gives read access to the learned API catalogue.
- **New manifest keys for an endpoint:** a default `max_age_seconds`, an optional floor, the A10 corrections, the A14 equivalence, and the number of events to discard after a connection. `stream` becomes optional. The manifest schema stays at plugin API version 1 only if every existing manifest loads unchanged. Otherwise the version is raised under the versioning policy, and the hand-back says which.

**A18. Built-ins and the core go through the broker.**

- Every built-in plugin (`pf`, `suricata`, `kea`, `isc`, `dnsmasq`, `unbound`, `insight`, and the widgets' plugins, which make no firewall call), discovery, the probe round and the core coordinators reach the firewall only through the broker.
- **Built-in plugins read through the observed object rather than raw body text,** so the mirror stores only what they use. Wherever a helper of `opnview.decode` takes a body, it accepts the observed object.
- **Behaviour stays identical:** the B5 inventory and byte identity hold. The only difference allowed is the timing of when an answer was fetched, within each reader's max age.
- **Corrections carried by the built-in manifests.** These are the inferences the survey contradicts or makes ambiguous, each citing its survey section:
  - the filter log's key `__digest__` and cursor `__timestamp__`, with `digest` declared as not a cursor parameter;
  - Suricata's composite cursor `(fileid, filepos)`;
  - Unbound's content-composed de-duplication key and its window rule;
  - the lease keys;
  - `cpu_usage/stream`'s discarded first event.

## 5. Scope: amendments inside existing sections

- **A1, the manifest.** Replace "the firewall endpoints it reads (path, method, body encoding, argument kind, stream flag, and for built-ins the survey citation)" with "the firewall endpoints it reads (path, method, body encoding, argument kind, an optional stream flag, a default max age, an optional freshness floor, mirror corrections and stream equivalences (A17), and for built-ins the survey citation)".
- **A3, *Firewall calls*.**
  - Replace "Calls go only to the configured firewall base URL, only for endpoints the manifest declares, and through the existing client and its `MutatingCommand` refusal, unchanged." with "Calls go only through the broker (A9) to the configured firewall base URL, only for endpoints the manifest declares, under the broker's read-only rule (A13) with the client's `MutatingCommand` refusal checked first. Each request states a max age, and its answer may be served from the mirror (A11)."
  - Replace "read a bounded event stream" with "subscribe to an event stream and read its events after a cursor (A14)".
- **A3, *Scheduling*.** Append: "A pass's firewall requests are served by the broker under A12's scheduler. The kind's interval is the pass's max age."
- **A5, refusals.** Add to the list: "a declared POST that is not a read action (A13)", and "a mirror correction naming a field path or parameter in a malformed way". A conflicting correction is listed, not refused (A10).
- **A5, listing.** Add per provider: the endpoints it declared, the data it actually read (from the catalogue), its max ages, and its `write_refused` count.
- **A8, `docs/plugins.md`.** Add:
  - the broker, the max age and how answers are served;
  - the observed object and the read-every-field rule;
  - inference and corrections;
  - streams and equivalence;
  - the read-only rule;
  - the API catalogue;
  - a worked example: a plugin naming an endpoint with a max age and one correction, sharing a datum with a built-in.
- **B4, the audit table.** Add the row: `| Firewall access: queueing, caching, scheduling, read-only rule | internal/collect, internal/plugin (direct Client calls) | The broker, the mirror and the scheduler (A9–A14). Core, not a seam: a single gate is what makes the call budget and the read-only guarantee enforceable |`.
- **C3, *Live*.** Append to "The host runs the live pass while live interest holds.": "Its requests carry the live cadence as their max age, fall in the real-time class (A12), and are coalesced by the broker. The live cadence is never below the endpoint's freshness floor."
- **C3, *Candidate endpoint*.** Append: "If it verifies, the `insight` plugin may declare it as replacing `traffic/interface` polling (A14)."
- **P8, *Core datasets*.** Append: "The mirror's tables and stream hypertables are datasets with origin `opnsense` (A15)."
- **P6.** Append: "The `opnsense` schema is the core's. No plugin schema can take that name, because plugin schemas carry their prefix."
- **P7, *Read connections*.** Append: "The read role holds `SELECT` on the mirror's data tables and none on its broker metadata beyond what the catalogue serves."
- **Phase D, map.** Add the broker, the mirror and the scheduler as packages named by responsibility, under the 15-file rule. One proposal: `internal/broker`, `internal/broker/mirror`, `internal/broker/schedule`, `internal/broker/catalogue`.
- **Phase E.** Append: "The broker budgets of BR-AC17 are measured on the 1 000 000-row seed instance and printed beside their limits."
- **F.2 table.**
  - `docs/plugins.md` row: add "the broker host API, inference and corrections, streams, the read-only rule". The size target stays. If it cannot be met, the Builder reports it (Risk 17).
  - `docs/widget-api.md` row: add "*API catalogue*".
  - `docs/architecture.md` row: add "the broker, the mirror and the scheduler in the data flow; the `verify.go` exception; *Decisions in force* carries decision 14".
  - `docs/opnsense-api-survey.md` row: add "the LV-C freshness floors and LV-B figures, as proportions and timings".
  - `docs/data-model.md` row: add "*Vocabulary*: broker, mirror, datum, max age, freshness floor; *Storage fit*: the mirror's tables".

## 6. Acceptance criteria: new section inserted after "### Phase A: plugin host" (before "### Phase B")

### Phase A, part 2: broker, mirror, scheduler

Every test runs against the fake firewall with a **counting transport** and a fake clock, on the PostgreSQL test instance.

- [ ] **BR-AC1. One gate.** A test lists every non-test caller of `Client.Call` and `Client.Stream` in `cmd/` and `internal/`. The list is exactly the broker and `internal/web/surfaces/settings/verify.go`. A test shows that a verification call leaves no row in the mirror.
- [ ] **BR-AC2. Two plugins share one call.** Two plugins written to a temporary data directory request the same datum at the same instant (max ages 30 s and 60 s). The firewall sees **one** call, and both receive identical values for the fields they read.
- [ ] **BR-AC3. Served from the mirror when fresh.** A third request for the datum within its max age is served with `served_from: mirror` and **zero** firewall calls. A request past its max age causes exactly one call.
- [ ] **BR-AC4. Only read fields are stored; a new field is added.**
  - The fixture answer has fields `a`, `b` and `c`, and the plugin reads `a` and `b`. The mirror table has columns for `a` and `b` only, and the PostgreSQL catalogue shows no column for `c`.
  - The plugin is replaced by a version that also reads `c`, against a fresh mirror answer. The plugin sees `c` with the firewall's value, at most one extra call is made, the column `c` exists and is filled, and a new schema version is recorded.
  - A plugin reading `body` makes the datum `read_whole`.
- [ ] **BR-AC5. Lossless round trip.** A fixture answer holds:
  - a numeric string with leading zeros;
  - `~`;
  - `null` and an absent field;
  - an empty string;
  - an integer above 2^53;
  - a float printed with trailing zeros;
  - a nested object;
  - an array of five rows;
  - a value-keyed object.

  Served from the mirror, every field the plugin reads is identical in JSON type and lexical value to the firewall's answer, with row order kept. The value-keyed object is stored as rows, not as one column per key (the PostgreSQL catalogue is inspected).
- [ ] **BR-AC6. Type change.** A field answered as a number, then as text, ends in a JSONB column holding both values without loss, with a schema version recorded.
- [ ] **BR-AC7. Nature, key and cursor.**
  - On the recorded `testdata` answers of every built-in endpoint, after the built-in manifests' corrections, the nature, key and cursor equal a committed table. Each row of the table cites the survey section that establishes it.
  - A `null`-on-every-row field is never inferred as a key.
  - A correction overrides an inference.
  - Conflicting corrections by two third-party plugins: the first loaded applies, the other is listed with `mirror_correction_conflict`, and both load.
- [ ] **BR-AC8. The cursor gives incremental reads.** For a record stream with an honoured window:
  - after the first fetch, each refresh asks only for the window after the last stored cursor (the request bodies are asserted);
  - a plugin asking with `after` receives only newer rows;
  - an answer whose oldest row is newer than the newest stored records a gap interval, and the gap is returned with the rows.
- [ ] **BR-AC9. A window that is not honoured.** When the fake answers rows outside the requested window, the parameter is recorded as `not_honoured`, later refreshes read in full and filter on the cursor, and no row outside a requested range is served.
- [ ] **BR-AC10. Pagination.** A paged datum (`current` / `rowCount`) of 3.5 pages is walked by the core to completion within the request's bound. Two requests that differ only in page parameters share one datum.
- [ ] **BR-AC11. Deadline-first, and a starved background datum is still served.**
  - Three data with different deadlines are refreshed in deadline order.
  - Under real-time demand that saturates the per-second ceiling for ten minutes of fake time, a background datum (max age 300 s) is still refreshed, with lateness within the bound the Builder states.
- [ ] **BR-AC12. The real-time reserve under load.**
  - With background demand saturating the ceilings, a real-time datum is refreshed at its max age (or its floor), and real-time calls take at least the reserved share of calls (70 % by default, then 50 % when the setting changes).
  - With no real-time demand, background calls use the whole budget.
  - Concurrency and calls per second never exceed their settings (the transport records the maxima).
- [ ] **BR-AC13. Cadence follows demand.** The refresh interval of a datum equals the shortest live max age. When that reader stops, it relaxes to the next reader's within the lapse span, and to no refresh at all when no reader remains. A request below the floor is served at the floor, and its `age_seconds` shows it.
- [ ] **BR-AC14. Back-off.**
  - Repeated `server_error` answers on one datum space its calls exponentially up to the maximum setting, and the first `ok` restores normal cadence.
  - Time-outs across three endpoints back off the whole queue.
  - `not_found` does not trigger back-off and is recorded as availability.
- [ ] **BR-AC15. A write attempt is refused, and no call reaches the firewall.** A plugin issues:
  - a POST to a command that is not a read action;
  - a POST to `carp_status`;
  - a PUT;
  - a DELETE;
  - a GET to a command in `MutatingCommand`.

  Each is refused with its closed code and logged with the plugin's name, and **the counting transport records zero requests**. A manifest declaring such a POST is refused at load with the new code. Every POST endpoint of the built-ins passes the rule.
- [ ] **BR-AC16. Credentials never reach a plugin.** A string search over everything every host function answers in the full built-in test run finds neither the API key, the secret, the base URL nor an `Authorization` value.
- [ ] **BR-AC17. Broker budgets** (proposed constants, each written once; the Builder may change one with a measured justification in the hand-back). Measured by `pre-deployment` and printed beside their limits:
  - serving a fresh 1 000-row datum to a plugin reading 10 fields per row: median host-call time **≤ 25 ms**;
  - the broker's overhead on a call to a zero-latency fake answering 1 000 rows (observation, inference, mirror write): median **≤ 50 ms**;
  - a pass reading 1 000 rows × 10 fields through the observed object: at most **2×** the same pass over a plain object.

  `checks` stays within 60 s.
- [ ] **BR-AC18. One subscription shared.** Three plugins subscribe to the same stream with the same parameters. The fake firewall sees **one** open connection, and every plugin receives every event. The connection closes after the last unsubscribes, unless a plugin reads fields of the stream, in which case it stays open while that plugin is loaded.
- [ ] **BR-AC19. A stream cut is recorded as a gap.** The fake closes the stream after 60 s of fake time and refuses the first reconnection. The subscription reconnects with back-off, and the interval with no events is a gap in the hypertable, in its minute and hour aggregates, and in the data catalogue's answer, **never a zero**. The declared discarded first event is not stored.
- [ ] **BR-AC20. Stream recording.**
  - Fields read go into a hypertable timestamped at reception.
  - Detail older than its retention is removed with P4's exact horizon.
  - Minute and hour aggregates equal an independent computation from the detail.
  - No TimescaleDB background job runs.
- [ ] **BR-AC21. A declared stream replaces polling, and polling resumes when it drops.**
  - A plugin declares that a fake stream replaces a polled endpoint. While the stream runs, that plugin causes **zero** calls to the polled endpoint, and another plugin's demand for it is still served.
  - When the stream drops, polling resumes from the next scheduling decision, and the plugin is told which source answered.
  - A plugin that declares nothing never has its polling replaced.
- [ ] **BR-AC22. Detection by content type.** An endpoint not flagged as a stream that answers `text/event-stream` is detected from the headers. The call returns within the stream bound, not the client's time-out, and the catalogue records the stream.
- [ ] **BR-AC23. The API catalogue.**
  - It lists every field A15 names for the BR-AC2 to BR-AC21 fixtures.
  - It answers 401 with a JSON condition without a session.
  - A string search finds no field value, credential or base URL.
  - `host.catalogue()` returns the same content.
  - The P8 data catalogue lists the mirror tables, with inferred roles.
  - Under `no_domains`, a query touching a mirrored column that carries a domain name answers `withheld_by_aggregate_mode`.
- [ ] **BR-AC24. Downloads stay out of the queue.** With the broker saturated, a fetch through A7 completes on its own timing. No fetch appears in the broker's counters or in the mirror.
- [ ] **BR-AC25. The firewall changes.** After the base URL changes, no answer is served from the mirror as fresh, and the first request causes a call. The previous firewall's history is not part of the new firewall's current state.
- [ ] **BR-AC26. The source is verified and an unavailable one is reported.**
  - Every endpoint the core or a built-in plugin reads carries a 26.7.3 citation in the survey (the existing citation test, extended).
  - An endpoint answering `not_found` or `forbidden` yields that closed outcome to every reader, and the broker tries no other path in its place.
  - A third-party endpoint without a citation is listed as `uncited`.
- [ ] **BR-AC27. Built-ins through the broker.**
  - The B5 inventory tests and byte identity pass with every built-in plugin routed through the broker.
  - No built-in plugin script reads `body` or calls `json()` (a test greps the built-in tree), unless it is listed in the hand-back with the reason.

## 7. Acceptance criteria: amendments to existing ones

- **A3.** Add: "including the new A13 load-time code".
- **A10.** Append: "and every firewall request the counting transport sees came through the broker (BR-AC1)".
- **A11.** Append: "each provider's observed data, max ages and `write_refused` count".
- **C-AC11.** Replace "with N parallel readers, at most one round of live calls happens per live cadence" with "with N parallel readers, at most one round of live calls happens per live cadence, by the broker's single-flight and mirror (BR-AC2, BR-AC3)".
- **F-AC5.** Add: "every setting key, refusal code and catalogue field named in `docs/plugins.md` or `docs/widget-api.md` *API catalogue* exists in code, and the reverse".
- **X2.** Append: "and BR-AC1 to BR-AC27".

## 8. Live validation: amendments

**LV-C0 becomes LV-C, extended.** In addition to C3.0, for every endpoint a built-in reads with a max age of 60 s or less (filter log, `traffic/top`, `traffic/interface`, `cpu_usage/stream`, resolver lookups, security events, resolver cache):

- the call latency (median and 95th percentile) and the response size;
- the firewall's processor use at candidate refresh intervals, with the finest interval adding at most 2 processor percentage points over the idle baseline recorded as the endpoint's **freshness floor**. `traffic/top` is never below its capture span.

The floors are written into the built-in manifests as declared floors, each citing the measurement, and into the survey as proportions and timings. **No write attempt is ever sent to the live firewall**, not even to test a refusal: BR-AC15 is proved on fakes only.

**New LV-B: the broker on live data**, during LV-P's 15-minute parallel run:

- the firewall calls per endpoint made by the new build (the broker's counter), against the old build's calls for the same window. The old figure is computed from its fixed intervals and stated as computed;
- the mirror hit ratio, per endpoint and overall;
- the medians of BR-AC17 measured live;
- the learned catalogue shown to the maintainer: per built-in datum, the inferred nature, key, cursor, honoured parameters and floor. Every inference that differs from the BR-AC7 table is either a correction added to a built-in manifest or a defect;
- the `stream_log` and `cpu_usage/stream` subscriptions: one connection each whatever the number of subscribers, and the server cut-off recorded as gaps followed by reconnection.

**The validation passes when**, as before, and in addition: the maintainer accepts LV-C's floors and LV-B's figures, and no LV-B inference difference is left unexplained.

## 9. Rules, Out of scope, Risks

**Rules, *Hard rules carried from the project*: append**

- "every firewall request goes through the broker, except the credential check of candidate credentials;"
- "GET, and POST to read actions, only, with `MutatingCommand` checked first;"
- "a mirrored field is served exactly as the firewall answered it;"
- "a stream cut, a failed call and an unanswered interval are gaps, never zeros."

**Out of scope: append**

- Runtime self-calibration of freshness floors from the firewall's processor use (open question 6).
- A mirror shared between installations, or an export of the mirror.
- Writing anything to the firewall, including to test the refusal.

**Risks: append**

29. **Inference errors.** Some OPNsense parameters are ignored silently (`search_queries` without `client`, the filter log's `digest`), keys can be `null` everywhere, and timestamps come in three shapes, one without a year. A wrong inference serves wrong rows. A10's verification of windows, BR-AC7's committed table, the built-in corrections and LV-B's review contain this. A third-party datum relies on its author's corrections.
30. **Schema churn.** Value-keyed objects, polymorphic fields and plugin updates can add columns or convert them to JSONB repeatedly. The value-keyed rule and versioned schemas contain this. A churn rate the Builder finds pathological is reported in the catalogue, not hidden.
31. **Proxy overhead in goja.** A `Proxy` trap per property read is far slower than a plain read, and the filter-log pass reads hundreds of rows every 10 s. BR-AC17's 2× bound may force a host-side dynamic object instead of a JavaScript `Proxy`, or a per-path read cache.
32. **No missing field: consistency.** Supplying a newly read field from a fresh call while the rest of the answer came from the mirror can mismatch rows, a lease that disappeared for example. A10 forbids reading a value from another row. A re-run of the plugin's function is the safest mechanism, provided a call has no side effect before its records are committed. The fetch facility's file writes are the exception to check.
33. **Mirror growth.** Built-in data is stored twice: as raw read fields in the mirror and as derived rows in the core tables. Stream detail grows at its event rate. The mirror retention setting, chunk retention and LV-DB's disk figure bound this, and *Storage fit* records it.
34. **Explosion of parameter combinations.** Every combination of identifying parameters is its own datum. `traffic/top/<interface list>` with different lists, or a plugin varying `searchPhrase`, multiplies data and calls. Window and page parameters do not identify a datum. Other combinations are counted in the catalogue, and a plugin creating unbounded combinations is visible there.
35. **The ceiling against a long call.** A `traffic/top` call over many interfaces holds a concurrency slot for several seconds. With the default of 2, it can delay the filter log. LV-B measures this, and the Builder may change the defaults.
36. **Reference timing.** Behaviour identity (B5, LV-P) now allows answers up to one max age old where today's passes called at that instant. Gap and window logic in the coordinators must read `fetched_at`, not the pass's instant, or a gap can be misdated.
37. **LV-C duration.** Measuring the processor at candidate intervals for seven endpoints at 15 minutes each takes hours of read load on the live firewall. The orchestrator may measure the endpoints in parallel only if their costs are proven separable.

## 10. Phase P interaction (summary for the Builder)

- **The `opnsense` schema** is a core schema created with the core schema, under P3's rules, idempotent (P-AC5). Its tables are created and altered at run time by the mirror, not by `schema.sql`. They are covered by `webtest`'s database scan (P-AC23) and by `pg_dump` (P-AC22).
- **Hypertables and retention** follow P4, with the exact horizon, by chunk then row. Continuous aggregates on stream hypertables are refreshed by opnview (P5's reasoning, Risk 22).
- **LV-0** creates an empty mirror. Nothing is carried over, because the mirror is learned again within the first passes.

## 11. ROADMAP.md and README.md

- **`ROADMAP.md`, *Rules that apply to every step*, read-only bullet.** Append: "Read-only is enforced by the broker for every plugin: GET, and POST to read actions (commands starting with `search`, `get`, `query` or `list`, or ending with `status`), with the list of mutating commands checked first."
- **`README.md`.** Keep the read-only API key advice, and add one sentence saying that every plugin's firewall access goes through opnview's read-only broker.

## 12. Existing spec sentences that become obsolete

1. Context, decision 2: "POST read endpoints are allowed. Firewall calls go through opnview's client, whose refusal of mutating commands is unchanged, with nothing added on top." Superseded by decision 14 (§1). The `MutatingCommand` refusal stays unchanged, and the A13 read rule is added on top.
2. A1: "the firewall endpoints it reads (path, method, body encoding, argument kind, stream flag, and for built-ins the survey citation)" (replaced, §5).
3. A3: "Calls go only to the configured firewall base URL, only for endpoints the manifest declares, and through the existing client and its `MutatingCommand` refusal, unchanged." (replaced, §5).
4. A3: "read a bounded event stream" (replaced, §5).
5. The scope phase list as replaced by the PostgreSQL amendment §4 (amended by §3: Phase A, part 2 inserted after Phase P).
6. C-AC11: "with N parallel readers, at most one round of live calls happens per live cadence" (amended, §7).
7. LV-C0 as written (extended into LV-C, §8). The C3.0 references to "LV-C0" read as LV-C.
8. Outside the spec, for the Builder to correct:
   - the `internal/opnsense/plugin_endpoints.go` comment "No allowlist is added on top: … keeps the client's refusal of known mutating commands as it is and adds nothing else";
   - `docs/plugins.md` *Endpoints*, "`method`: `GET` or `POST`. A POST that only reads, such as a grid search, is allowed." and "`stream`: `true` for a server-sent event stream";
   - `docs/plugins.md` *The host*, the `host.call` and `host.stream` rows;
   - the `provider.Session` comment that `Call` is "the ONLY outbound capability the port grants besides Stream".

## 13. Open questions (additions)

Open questions 1 and 2 stay as they are.

3. **Must a plugin still declare its endpoints in the manifest?** Decision 1 says the plugin names the endpoint. Today it names it in `plugin.json`, checked at load.
   - **(a)** Keep the manifest declaration mandatory. The load-time refusal, the listing and the read rule at load stay, and the script calls endpoints by declared name.
   - **(b)** Also allow naming any path at run time from the script. The broker's A13 check is then the only gate, and the listing learns endpoints from the catalogue.

   **Recommendation: (a).** A refused write shows in the listing before any pass runs, a third-party author's endpoints are readable without running the plugin, and none of the decisions requires run-time naming. Scope follows (a) unless the maintainer chooses (b).
4. **What puts a datum in the real-time class?**
   - **(a)** A threshold on the shortest live max age, a setting with a proposed default of 5 s. This keeps the 10 s filter log in the background, as the maintainer listed it.
   - **(b)** The plugin marks a request as real-time.
   - **(c)** A request made under C3's live interest is real-time.

   **Recommendation: (a).** It follows from the max age the plugin already states, needs no new declaration, and the operator can tune it. Scope follows (a).
5. **Does a widget bound to a mirrored dataset keep that datum refreshed?** Through the data catalogue (P8), a generic widget can bind to `opnsense.*` data that no loaded plugin reads any more. The widget API never calls the firewall.
   - **(a)** A data-catalogue query on a mirrored dataset renews a demand lease, with C3's `live_interest_seconds`, at the shortest max age its last plugin reader used. The broker refreshes the datum while someone looks at it.
   - **(b)** Widgets create no demand. Data nobody reads goes stale, and `freshness` says so.

   **Recommendation: (a).** It reuses the lease the maintainer already decided for live figures, and keeps the widget API free of firewall calls. Scope follows (a).
6. **Freshness floors on firewalls other than the maintainer's.** LV-C measures the floors on one device. A weaker firewall may need longer ones, and zero hardcoded configuration forbids assuming that device's figures for everybody.
   - **(a)** The built-in floors are the LV-C figures, overridable by setting, with back-off as the protection.
   - **(b)** In addition, the broker calibrates floors at run time from `cpu_usage/stream`, applying the same +2-point rule to each installation.

   **Recommendation: (a) this cycle, (b) as a later step.** (b) needs its own measurement design and would delay the cycle. Back-off already protects a saturated firewall. Scope follows (a).
7. **Secrets in firewall answers.** A plugin may read an endpoint whose answer carries a secret: a configuration export, or a key field (Risk 6). The mirror then stores it, and the data catalogue serves it to the authenticated user.
   - **(a)** Accept it, as Risk 6 does. The API key's read privileges bound what can be read, and the README says so.
   - **(b)** In addition, never store a field whose name matches a closed list of secret markers (`password`, `secret`, `psk`, `private_key`, `token`, `hash`), and report it in the catalogue.

   **Recommendation: (a).** Any list of markers is a guess and gives false assurance. The maintainer already accepted Risk 6, and the data stays inside the installation's own authenticated API. Scope follows (a) unless the maintainer chooses (b).

No other question. The scheduler's shape, the reserve, the read rule, streams and the catalogue were decided by the maintainer. The rest follows from them or from Phase P.

---

**Decided (maintainer, 11 October 2026), broker amendment open questions:**
- Q3: (a), endpoints stay declared in the manifest.
- Q4: (a), real-time means a short requested freshness, at or below a setting with a default of 5 s.
- Q5: (a), a displayed widget keeps its mirror datum refreshed while someone looks at it.
- Q6: (a), the floors measured on the maintainer's firewall are defaults, overridable by setting, with back-off as protection. Automatic calibration comes later.
- Q7: (a), mirror columns are exposed like any other field. The API key's read-only privileges bound what can be read.
- Q8 (plugin compatibility across OPNsense versions): (a), A19 adopted, extended by the maintainer:
  - **Version selection is declarative and optional.** In its manifest, a plugin may map an OPNsense version range to **routes**: path, method, parameters, and field-name mapping, as A19 describes. It may also map a range to **a script**: a whole different script file for that range. Both may be combined.
  - **A plugin that declares no version mapping has no version handling at all.** It runs as it is on every version, and nothing is required of its author.
  - **The engine does the switching.** It reads the firewall's version and selects the matching routes or script, again when the version changes. The author never writes `if version > x` or `switch(version)`. A version that no declared range covers is a named availability outcome, never a guess.
  - The same mechanism applies to an OPNsense plugin's own endpoints, for example os-zenarmor, whose version may differ from the core's. A range may name the version of the OPNsense core or of the named OPNsense plugin. How the engine reads an installed plugin's version is to be found in `opnsense/core` 26.7.3 and recorded in the survey, `UNVERIFIED:` until then.

### A19. Firewall versions: one plugin, several OPNsense versions, no version tests in the script (part of Phase A, part 2)

This section belongs with A9 to A18 of the broker amendment. Q8 above extends it.

- **The core reads the firewall's OPNsense version** at discovery and on every discovery pass, so an upgrade is seen without a restart. It also reads the version of each installed OPNsense plugin a range refers to.
  - The endpoints that give these versions are to be found in `opnsense/core` 26.7.3 and recorded in the survey before use. Until then they are `UNVERIFIED:` and nothing is built on them.
  - The version is shown in the plugin listing, in the catalogue's schema versions, and in `host.firewall`, which is read-only.
- **An endpoint name in a manifest is stable; its variants carry the differences.**
  - Each variant has a version range, plus its own path, method, body, parameters and corrections.
  - A field-name mapping presents the answer to the script under the stable names.
  - A variant may add an adapter (a script function) where a rename is not enough.
  - A range may instead select a whole script file.
  - The core selects what matches the running versions. The script's logic is written once, against the stable names.
  - **Everything here is optional.** A plugin that declares no variant runs unchanged on every version.
- **Refusals and states:**
  - overlapping ranges are refused at load, with a closed code;
  - a running version that no declared range covers yields a closed availability outcome naming the versions, for example `firewall_version_unsupported`. The engine never guesses the closest range.
- **The mirror stays a mirror of the API.** A different path is a different datum. Schema versions record which OPNsense version produced which shape.
- **Tests.** A plugin's test data holds recorded answers per variant, and its JS tests run each variant and assert the same records.
- **Docs.** `docs/plugins.md` gains a worked example: one endpoint renamed between two versions, with one field renamed and one restructured by an adapter, plus a range that selects another script.
- **Acceptance (BR24):**
  - a plugin with two variants serves the same records from each variant's recorded answers;
  - a version outside every range yields the closed outcome;
  - overlapping ranges are refused at load;
  - a version change seen at discovery switches the variant with no restart;
  - a range selecting a script runs that script;
  - a plugin with no variants is unaffected;
  - each version endpoint is cited, or `UNVERIFIED:` and unused.

**Decided (maintainer, 12 October 2026), after the Phase P / A-part-2 checkpoint:**
- **TimescaleDB licence:** the Timescale License features are **enabled** (`timescaledb.license=timescale`): continuous aggregates, compression and native retention policies. Self-hosted use is free under that licence. Where a continuous aggregate gives exactly the result the existing aggregate tests assert, it replaces the hand-built aggregate; otherwise the hand-built one stays. This is done in Phase E, with the performance work, and README and ROADMAP state the licence.
- **Mirror write cost:** the mirror stamps the newest generation per datum instead of rewriting unchanged rows. The BR-AC17 broker overhead budget stays at **50 ms**.
- **OPNsense plugin versions:** version ranges may name the OPNsense **core** version only. `/api/core/firmware/info` runs `pkg update` and is never called. A range naming an OPNsense plugin stays refused (`package_version_unavailable`) until a side-effect-free read is found and cited.
- **Order of the rest:**
  - B3a, the widget script host;
  - B3b, the 26 widgets ported in parallel batches, with the existing golden JSON as the oracle;
  - then C, D, E (including the licence switch and the open performance findings: five widgets over budget at 100k/30d, `purge_due_addresses` reading the whole index, the slow collect tests, pre-deployment within 3 minutes);
  - then F.
- `docker-compose.serve.yml` gains the pinned TimescaleDB service (no published port, generated password, `OPNVIEW_DATABASE`) before the live validation.
- **Mirror datasets' `seen_at` (12 October):** the read role is granted `SELECT (id, fetched_at)` on `opnsense.mirror_datum`. That gives an exact "last fetched" without rewriting any row. It is a narrow, documented exception to A15.
- **Domain withholding (12 October):** under `aggregate_mode = no_domains`, the core withholds every column marked carries-domain from every widget answer, built-in and third-party alike. Authors have nothing to do.


---

**This amendment is part of the spec and overrides any earlier text it replaces. Section 11 lists the earlier sentences that are obsolete.**

# Amendment to `specs/SPEC-backend-completion-plugins.md`: push sources, syslog and NetFlow (maintainer decision, 12 October 2026)

**Placement.** Append this after the last "Decided (maintainer, 12 October 2026)" block, with its own `---` separator and the preamble above. **The cycle kind stays `standard`.** Everything below is Go, SQL, JSON contracts, server-side JavaScript, compose files and documents. The push catalogue entries are served as JSON only.

**What I read and found (fact check of the brief).**

- **The design source.** `ROADMAP.md`, *Planned: push sources — syslog and NetFlow (maintainer, 12 October 2026; not yet scheduled)*, lists seven points "the specification of this step must settle". Each one is a requirement below:
  - the type vocabulary;
  - the optional JavaScript parse function;
  - sender acceptance, RFC 6587 and bounded buffers;
  - timestamps;
  - ifIndex mapping;
  - one catalogue with declared replacement;
  - one decoding pass for several subscribers.
- **Nothing in the tree receives pushed data.** No listener, no syslog parser and no NetFlow decoder exist under `cmd/` or `internal/`. Every firewall datum enters through the broker (`internal/broker/broker.go`, `gate.go`), which only *calls* the API. **Push is inbound, so the broker's rule "the only path from opnview to the firewall API" (A9) stays true.** A push listener is not a firewall call and is not a caller of `Client.Call` / `Client.Stream` (BR-AC1 is unchanged).
- **Declared equivalence exists, but only for event streams.** It is the A14 mechanism: `replaces` in an endpoint declaration (`docs/plugins.md`, *Endpoints*), implemented in `internal/broker/streams.go`, `replacing()`. It suspends one plugin's **demand** for a polled endpoint while a subscribed stream is connected.
  - It does **not** settle how the records the two sources deliver are kept from being counted twice in a core kind's tables. For streams nothing needed it: no built-in stream feeds a record kind.
  - For push it is the critical point. `pf` pulled through `/api/diagnostics/firewall/log` and `pf` pushed through syslog `filterlog` would both deliver `firewall_log` records into `flow`, the pairing and every aggregate family.
- **Record identities differ between the two paths.** `firewall_log_record.digest` is today the backend's `__digest__`, "the MD5 of a previously seen raw log line" (survey, *Data source 1*, *Endpoints*), computed over the line as the firewall's local file holds it. A line received over remote syslog carries a header written for that destination, so whether a pushed line can reproduce the same digest is `UNVERIFIED:` and probably false. **The no-double-count rule below therefore cannot rest on a shared record identity. It rests on an exclusive source per instant (PS9).**
- **Plugin table types today** are `integer`, `real` and `text`. The retention (time) column is an integer of UTC epoch seconds (`docs/plugins.md`, *Tables*). The broker's inferred types are `ip_address`, `mac_address`, `timestamp`, `number`, `text`, `boolean` and `json` (`internal/broker/infer/infer.go`). The mirror maps them to `numeric`, `boolean`, `jsonb` and `text` (`internal/broker/mirror/tables.go`). **The roadmap's "plugin type vocabulary (`ip`, `mac`, `instant`, `integer`…)" does not exist yet.** PS4 defines it once, for push output tables and every A4 declaration.
- **Manifest vocabulary.** The maintainer's sketch names the declaration `type: stream, provider: syslog|netflow`. Both words already mean something else in the manifest:
  - `stream` is the A14 server-sent event stream (`docs/plugins.md`, *Endpoints*, `stream`);
  - `provider` is the registry's `(kind, provider_key)` (A1, *Providers*).

  Under `CLAUDE.md`'s vocabulary rule, one key cannot carry two meanings. PS3 therefore names the declaration `push` (the roadmap's own word for the step), keeps `program` (syslog-ng's and OPNsense's term for the RFC 3164 TAG / RFC 5424 APP-NAME), and uses the protocol names `syslog` and `netflow`. The maintainer's structure is kept whole, and only two key names change. This is recorded as a deliberate departure.
- **Compose.** `docker-compose.serve.yml` publishes exactly one port, `${OPNVIEW_LISTEN:?…}`, with **no default**, "because an address written into this repository would be one installation's configuration shipped to every other". The serve container runs as an unprivileged UID (`OPNVIEW_UID`, default 1000), so it cannot bind a port below 1024 without an added capability. Syslog's registered port (514/udp) is below 1024.
- **OPNsense facts the brief asked me to verify.** I could not read `opnsense/core` 26.7.3 for this amendment: I had no network access. `docs/opnsense-api-survey.md` has **no section** on remote syslog targets, on NetFlow export destinations and versions, on Suricata's syslog output, or on Unbound query logging to syslog. *Data source 3* states only that "a firewall exporting to an external collector reports `netflow: 1, local: 0`", and gives the Insight aggregates' properties.
  - **Every OPNsense fact below is therefore `UNVERIFIED:`.** Each is listed as a **candidate to read**, with the source file I expect to establish it, and no source is presented as a citation.
  - PS1 makes reading them the first task of the phase, and PS-AC1 forbids building on any of them until it is done.

---

## 1. Context: insertion after decision 14 (and after the "Decided" blocks), as decision 15

> 15. **Push sources: syslog and NetFlow** (maintainer, 12 October 2026). As well as pulling through the broker, plugins may receive data **pushed** by the firewall:
>     - syslog, RFC 3164 and RFC 5424, over UDP and TCP;
>     - NetFlow v9 and IPFIX, over UDP.
>
>     The user configures OPNsense to send to `opnview`. `opnview` never writes to the firewall.
>     - **The Go core stays agnostic.** It holds the listeners, one generic decoder per protocol (the syslog header; NetFlow v9 / IPFIX template decoding into typed fields) and routing by subscription, with bounded buffers and bulk inserts.
>     - **It holds no knowledge of any program's message format.**
>     - A syslog line no plugin subscribes to is dropped at the header.
>     - **A plugin declares, in its manifest:**
>       - a push source;
>       - a header filter;
>       - for syslog, a named-group regular expression, or a JavaScript parse function where a regular expression cannot express the message;
>       - an output table whose typed columns become a TimescaleDB hypertable on its time column, with no JSONB.
>     - **The pushed data counts once.** Pushed sources appear in both catalogues. A plugin may declare that a pushed source replaces one of its polled endpoints. **A connection is never counted twice in the core's tables when both paths deliver it.**
>     - **Order.** This is built after Phase C's C1–C5 and **before the mockup audit (C6)**, because push sources change the audit's verdict.

## 2. Context, *Where the repository stands*: append these bullets

> - **Push (read for decision 15).** Nothing in the tree receives pushed data: no listener, no syslog or NetFlow decoder. Every firewall datum enters through the broker, which only calls the API. Declared equivalence exists for server-sent event streams only (A14, `internal/broker/streams.go`). It suspends a plugin's demand for a polled endpoint, and nothing in it keeps a core kind's records from being counted twice when two paths deliver them.
> - **Plugin table types** are `integer`, `real` and `text`, with an integer epoch time column (`docs/plugins.md`, *Tables*). No `ip`, `mac` or `instant` type exists.
> - **The serve compose file** publishes one port, `OPNVIEW_LISTEN`, with no default, and runs as an unprivileged UID.

## 3. Scope: phase order

**In the phase list** (as replaced by the PostgreSQL amendment §4 and amended by the broker amendment §3), replace the **Phase C** line with:

> **Phase C.** The gaps: live rate and pf states as plugins; owners, unnamed destinations, ports and the JSON settings API in the core (C1–C5).
> **Phase C, part 2.** Push sources: syslog and NetFlow (PS1–PS16).
> **Phase C, part 3.** The mockup audit (C6), which re-examines every row with push sources available.

**In the "Decided (maintainer, 12 October 2026)" block, *Order of the rest*,** replace "then C, D, E (including …)" with:

> then C1–C5, then push sources (Phase C, part 2), then the mockup audit C6, then D and E (including the licence switch and the open performance findings: …)

Phase C, part 2 starts only when C1–C5 pass their own criteria. C6 starts only when PS-AC1 to PS-AC27 pass.

## 4. Scope: new section, inserted after C5 and before "**C6. The mockup gap audit.**"

### Phase C, part 2: push sources

**PS1. Verify the firewall side first** (before any push code). The orchestrator or the Builder reads `opnsense/core` 26.7.3 and records, in a new section of `docs/opnsense-api-survey.md` titled ***Push sources: remote syslog and NetFlow export, read from source for push sources***, each fact below with its 26.7.3 source URL. A fact the source does not establish stays `UNVERIFIED:`, and nothing is built on it.

The candidates below are **not citations**: they are the files expected to answer.

| # | What must be established | Candidate source (to read, `UNVERIFIED:` until read) |
|---|---|---|
| S1 | Remote syslog targets: their menu path, and the fields a target has. Transport (expected: UDP, TCP and TLS over IPv4 or IPv6), application / program filter, level, facility, host, port, and an RFC 5424 option | `src/opnsense/mvc/app/models/OPNsense/Syslog/Syslog.xml` and its form XML |
| S2 | What syslog-ng actually sends to a target:<br>- header format with and without the RFC 5424 option;<br>- timestamp format and zone;<br>- whether the hostname is present;<br>- **TCP framing** (RFC 6587 octet counting, or non-transparent newline framing) | `src/opnsense/service/templates/OPNsense/Syslog/syslog-ng-destinations.conf` (or the template that renders destinations) |
| S3 | The program names under which `filterlog`, Unbound, Suricata and the DHCP servers log, and whether a target can select them | the same model's program list; each service's syslog-ng source template |
| S4 | The `filterlog` message: its CSV, and every variant by IP version and protocol. The survey's *Data source 1*, *Response shape*, gives the field set as parsed by the API | the backend's filter-log parser that produces `/api/diagnostics/firewall/log` |
| S5 | NetFlow export:<br>- the versions offered (expected: v5 and v9; FreeBSD `ng_netflow(4)` is not known to emit IPFIX);<br>- destinations (form, how many, IPv4 and/or IPv6);<br>- capture interfaces and any egress-only option;<br>- active and inactive timeouts;<br>- whether local collection (`flowd`) is itself a destination | `src/opnsense/mvc/app/models/OPNsense/Diagnostics/Netflow.xml`; the script or `plugins.inc.d` file that starts `ng_netflow` |
| S6 | What `ifIndex` an exported record carries (the FreeBSD interface index), and **which read endpoint maps an index to a device or interface identifier**, if any | candidates: `/api/diagnostics/networkinsight/get_interfaces` (in the survey's endpoint table with no response shape), `/api/interfaces/overview/interfaces_info` |
| S7 | Suricata: whether the IDS model offers syslog output of alerts and/or of eve records, under which program, which event types it carries (alert only, or `tls` and `http` too), and in what format | `src/opnsense/mvc/app/models/OPNsense/IDS/IDS.xml`; `src/opnsense/service/templates/OPNsense/IDS/suricata.yaml` (already cited by the survey for eve-log types) |
| S8 | Unbound: whether query and reply logging to syslog can be enabled from the web UI, and what one logged line contains (client, name, type, verdict, blocklist, rcode) | `src/opnsense/mvc/app/models/OPNsense/Unbound/Unbound.xml`; the Unbound configuration template |
| S9 | Read-only endpoints from which `opnview` can **detect** that a remote syslog target or a NetFlow destination is configured, so a missing export is reported rather than guessed | the Syslog and Netflow controllers' `get` / `search` actions |

- **What the section must also say.** Each finding is recorded with its consequence for the built-in variants (PS12). The section also records the user's procedure for each export, to the precision S1 and S5 establish. That procedure is the source of the README text in §10.
- **No write.** `opnview` never calls any `set`, `add`, `del`, `reconfigure` or `setconfig` action of these controllers (`setconfig` is already on the mutating list). Enabling an export is the user's manual step.

**PS2. The core: listeners, decoders and routing, protocol-agnostic in what they route.**

- **Listeners.**
  - A syslog listener on UDP and one on TCP, and a NetFlow / IPFIX listener on UDP.
  - Each listener is enabled only by its own **start parameter, which has no default**: the same rule as `--listen`, because a port is one installation's configuration.
  - A listener with no parameter is off. Every push declaration then reports the availability state `listener_not_configured`, never a silence presented as no data.
- **One generic decoder per protocol,** behind one internal seam, so that adding a protocol is adding a decoder file and a registry row. Batching, routing, storage, gaps and the catalogues name no protocol.
  - **Syslog header (RFC 5424, RFC 3164).** It decodes:
    - PRI into facility and severity;
    - the timestamp;
    - the hostname;
    - the program (RFC 3164 TAG / RFC 5424 APP-NAME);
    - the process id (PROCID / the TAG's `[pid]`);
    - for RFC 5424, MSGID and STRUCTURED-DATA;
    - then the message.

    It accepts RFC 3164 with or without a hostname, and RFC 3164 with an ISO 8601 timestamp (a syslog-ng variant; the survey's *Data source 1* shows the firewall writes either form). A line that does not parse is counted as `header_unparseable` and dropped, never stored.
  - **NetFlow v9 (RFC 3954) and IPFIX (RFC 7011).** It decodes:
    - the packet header;
    - templates and options templates, scoped per exporter address and observation domain (v9 source ID);
    - template withdrawal and redefinition;
    - variable-length fields (IPFIX), padding and enterprise-specific fields.

    Each field is typed from the **IANA IPFIX Information Elements registry**. That registry is a standard, held as data in the core with its source cited, like `internal/specialpurpose`. Fields are named by the registry's names (for example `sourceIPv4Address`, `octetDeltaCount`), which v9's field type numbers share. An enterprise field with no registry entry is exposed as an octet string under a name built from its enterprise number and its id.
  - **Data records before their template** are held within a bound and decoded when the template arrives. Past the bound they are dropped, counted and recorded as a gap (`netflow_template_missing`).
  - **Other NetFlow versions** (v5, if S5 shows OPNsense offers it) are counted and reported with the named state `netflow_version_unsupported`, never decoded by guess.
- **TCP syslog framing.** RFC 6587 defines two framings: octet counting and non-transparent (newline). Which one OPNsense sends is S2, `UNVERIFIED:`.
  - The listener accepts **both**, detected per connection from the first byte of each frame, as RFC 6587 §3.4 permits a receiver.
  - A frame above a size bound (a constant, written once and documented, at least RFC 5425's minimum of 2048 octets) is counted, and its connection is closed.
  - Connections are bounded in number and have an idle time-out.
- **Routing by subscription. One decoding pass is shared by several subscribers.**
  - The header (or the NetFlow template decoding) runs **once** per message.
  - Subscribers are selected by an index on the header filter (for syslog, the program), so a message no subscriber wants costs one header decode and one lookup. No regular expression or script runs for it, and it is counted and dropped.
  - Each subscriber then extracts only its own declared columns and writes only to its own declared table or kind. Two subscribers of the same program or template never share a row.
- **The core holds no product knowledge.** No Go source outside tests under the push packages names a program (`filterlog`, `unbound`, `suricata`, …), a message regular expression, or a field of any program's message (PS-AC2).

**PS3. The manifest declaration** (`push`, a new optional list of the plugin manifest, under A17's versioning rule). Each entry declares:

- **`protocol`:** `syslog` or `netflow`.
- **A header filter.**
  - For syslog: `program`, one or more exact names, optionally narrowed by facility and severity.
  - For NetFlow: the Information Elements the subscriber requires, by registry name. A record lacking a required element is skipped for that subscriber and counted. An optional filter on decoded fields may be added, using P8's closed operator list.
- **For syslog, how the message becomes a row:**
  - a **named-group regular expression**, whose groups name the output columns. It is matched by a linear-time engine (RE2 syntax, which Go's standard library offers), because the input is network data and a backtracking engine can be driven into catastrophic time. The syntax is stated in `docs/plugins.md`;
  - or a **parse function** in the plugin's script, for messages a regular expression cannot express (`filterlog`'s CSV with its variants).
- **Where rows go,** one of two, never both:
  - **`output_table`:** one of the plugin's own declared tables (A4, P6), for a kind of the plugin's own;
  - **`kind`:** a core kind whose provider the same plugin declares, such as `firewall_log`. The rows are then **records of that kind's contract** (`contract.go`), delivered to the kind's coordinator exactly as pulled records are (PS9).
- **Optional `replaces`:** the name of one of the same plugin's declared endpoints, which this push source replaces while it is alive (PS10).
- **Optional `liveness_seconds`** (PS10), bounded by a constant floor and overridable by a namespaced setting.
- **Versions.** Push entries take part in A19's version ranges like endpoints, including a range that selects another script. The `filterlog` CSV may differ between OPNsense versions.

**PS4. The plugin type vocabulary** (A4 amended; one vocabulary for every plugin table, push or not). Column types are words of a closed list, never SQL type strings:

| Plugin type | PostgreSQL type | Notes |
|---|---|---|
| `integer` | `bigint` | A NetFlow unsigned 64-bit value above the signed range is refused for that field (`push_value_invalid`), never wrapped |
| `real` | `double precision` | |
| `text` | `text` | |
| `boolean` | `boolean` | |
| `ip` | `inet` | IPv4 or IPv6 |
| `mac` | `macaddr` | |
| `instant` | `timestamptz` | Keeps sub-second precision where the source has it |

- **There is no JSON type.** No push output table has a JSONB column, and each captured property is its own column, for compression and continuous aggregates.
- **Registry mapping.** The IANA abstract data types map onto this list (`unsigned*` and `signed*` → `integer`, `float*` → `real`, `ipv4Address` and `ipv6Address` → `ip`, `macAddress` → `mac`, `dateTime*` → `instant`, `string` → `text`, `boolean` → `boolean`, `octetArray` → `text` in lower-case hex). The table is in `docs/plugins.md`.
- **A value that does not convert** to its column's type is null when the column is nullable, and otherwise refuses the row. It is counted as `push_value_invalid` and never coerced.
- **A push output table's time column must be of type `instant`.** Calendar-month buckets need a timestamp type (P3), and the hypertable is chunked on it.
- **Existing declarations stay valid.** Integer-epoch time columns remain valid for other plugin tables, and every built-in declaration loads unchanged.
- **The JSON API keeps epoch seconds** (P3). Every `instant` leaving any response (data catalogue, widget, listing) is an integer number of UTC epoch seconds.
- **Data catalogue roles** derive from the vocabulary: `instant` → `time`; `integer` and `real` → `measure`; everything else → `dimension`. They can be corrected in the declaration (A4 role, unit).

**PS5. Timestamps.**

- **RFC 3164**, which has no year and no zone, is normalised by the existing year-less normaliser with the firewall's **measured UTC offset**, the same function `host.normaliser().syslog` exposes, including the year rollover. The measurement stays core.
- **RFC 5424 and ISO variants** apply the offset they carry. A timestamp carrying no offset gets the measured one.
- **NetFlow v9.** A record's start and end come from `FIRST_SWITCHED` / `LAST_SWITCHED`, relative to `sysUptime`, converted through the packet header's `unix_secs` and `sysUptime`: the export time.
- **IPFIX** uses the record's absolute time elements, or its delta elements against the header's export time.
- **The reception instant** is recorded by the core for liveness, ordering and gaps. It is never substituted for a header instant that parsed.

**PS6. Senders: only the configured firewall.**

- **The accepted set is derived at run time, never written down.** It holds:
  - the addresses the configured base URL's host resolves to;
  - every address discovery reports for the firewall's own interfaces (`interface_address`, the `this_firewall_address` view).

  It is recomputed when the base URL changes or discovery refreshes, with no restart.
- **Anything from outside the set is refused.** A UDP datagram, or a TCP connection, from an address outside the set is refused before decoding: nothing of it is stored.
  - Each refusal is counted per source address.
  - Refused addresses are listed in the push entry of the API catalogue (an authenticated JSON, PS13), so a container network that rewrites source addresses is visible at once (Risk 39).
- **What this does and does not prove.** It is a source-address check. It refuses datagrams from any other sender. It does not authenticate the firewall: a forged source address on the path is not detectable at this layer (Risk 38). `docs/plugins.md` and the README state it in those words.

**PS7. Bounded buffers, drops as gaps, bulk inserts.**

- **Every queue between the socket and the database is bounded:**
  - the socket receive buffer;
  - the decode queue;
  - each subscriber's batch;
  - the NetFlow pre-template hold;
  - the count of templates per exporter.

  Each bound is a constant written once, or a setting, and documented.
- **A drop is counted and recorded as a gap, never silently.** Each gap has a reason from a closed list. The names are proposals; the Builder fixes them and lists them in the closed vocabulary and in `docs/plugins.md`:

  | Reason | What it records |
  |---|---|
  | `push_buffer_overflow` | `opnview`'s own queues |
  | `push_receive_overflow` | the operating system's receive-queue drop counter, where the platform reports one |
  | `netflow_sequence_gap` | a jump in the v9 or IPFIX sequence number per exporter and observation domain |
  | `netflow_template_missing` | data records whose template never arrived within the bound |
  | `push_store_failed` | a batch that could not be committed after a bounded retry |
  | `push_listener_down` | the interval during which `opnview` was not listening, including its own downtime between runs |

  - **The gap's interval** spans the reception instants of the first and last lost message, merged within a window the Builder states. A sequence gap spans the export times on either side of it.
  - **For a core kind** the gap enters the kind's gap listing. **For an own kind** it is an A3 provider gap.
  - **Under P8,** a bucket inside a gap is a gap, never a zero.
- **Inserts are batched per destination table** and flushed at whichever bound comes first, rows or time, each a constant written once. A batch is one transaction. A trickle is committed within the time bound.
- **Undetectable loss.** Loss on the network between the firewall and `opnview` is undetectable for syslog over UDP: it carries no sequence number. The data catalogue entry of every push-fed dataset therefore carries **`loss_detectable`**:
  - true for TCP syslog and for NetFlow / IPFIX;
  - false for UDP syslog.

  Open question 9 governs what this means for replacing a polled core kind.

**PS8. The parse function** (syslog only).

- **Called on a batch.** It is called with a bounded batch of messages, each with its decoded header, never once per message. Each call runs in a fresh interpreter (*The script*), which a per-message call could not afford.
- **What it returns, per message:**
  - for an `output_table` entry, one row of the declared columns, or nothing (skipped and counted);
  - for a `kind` entry, one record of the kind's contract, or nothing.
- **Failures.**
  - A thrown exception or a time-out fails the messages of that call. They are counted (`script_failed` / `script_timeout` on the provider's availability, as in A2) and recorded as a gap. The listener carries on.
  - Records failing the contract are refused (`records_invalid`), as for pulled records.
- **The regular expression stays the fast path.** A declaration using it makes no script call.

**PS9. A core kind fed by both paths: an exclusive source per instant, and nothing counted twice.** This is the rule that keeps `flow`, the pairing and every aggregate family exact. It applies to any provider of a core kind that declares a push entry with `kind` and a `replaces` naming one of its polled endpoints.

- **One provider, two paths.** The pushed and the pulled records belong to **the same provider** `(kind, provider_key)`. Registry identity, `Selection` and availability are unchanged (B4). The per-kind rule on how many providers may be active is unchanged.
- **Source segments.**
  - The coordinator records, per provider, consecutive **source segments** `(path: pull | push, from_at, to_at)` that never overlap. Every instant of the provider's history belongs to at most one segment.
  - A record is stored only if its event instant (for `firewall_log`, `observed_at`) falls in a segment of the path that delivered it.
  - A record delivered by the path not contributing at its instant is not stored, and is counted as `superseded`.
  - The segments are listed with the provider's availability and gaps in the JSON API.
- **Handover from pull to push.**
  - When the push source becomes alive (PS10), polling continues until both paths have delivered an **overlap**.
  - The boundary is then set at a whole second that the pulled path is known to have read completely and that the push path was alive throughout.
  - Pulled records after the boundary and pushed records up to it are `superseded`.
  - Only then is polling suspended (A14's demand suspension).
- **Handover from push to pull.**
  - When the push source stops being alive, the boundary is the last whole second the push path is known to have delivered completely.
  - Polling resumes at once, and pulled records up to the boundary are `superseded`.
  - A pulled path that cannot reach back to the boundary records a gap from the boundary to the oldest record it returned (the filter log's existing gap rule, clipped).
- **Gaps are clipped by coverage.** A gap either path detects is clipped by the intervals the other path covered. A gap never spans an interval whose records were stored.
- **Pushed record identity is namespaced.** It can never equal a pulled one: for `firewall_log`, the pushed `digest` is computed from the pushed message under a prefix the pulled digests cannot carry, and `flow.log_digest UNIQUE` holds.
- **Everything downstream is unchanged.** Identity, pairing, classification, attribution and aggregation run on stored records exactly as today, whichever path delivered them. A connection whose two legs were delivered by different paths across a boundary is paired once.
- **Only a declared replacement may share a provider.** A push entry with `kind` and no `replaces` is refused at load (`push_kind_without_replacement`). Without an exclusive segment, two paths of one provider would double-count, so the rule is enforced rather than documented.

**PS10. Liveness and declared replacement** (A14 extended to push).

- **Alive.** A push source is alive while accepted messages matching its filter have arrived within its liveness span. The span is:
  - the declaration's `liveness_seconds`;
  - else the Builder's stated default;
  - bounded below by a constant;
  - overridable by a namespaced setting.
- **Effect on polling.**
  - While alive and past the PS9 overlap, the declaring plugin's demand for the replaced endpoint is suspended. Other readers of that endpoint keep their own demand (BR-AC21).
  - When it stops being alive, the demand resumes from the next scheduling decision, and the plugin is told which path answered.
- **Hysteresis.** Liveness has a stated hysteresis, so a source near its span does not flip at every message.
- **Replacement is declared, never guessed.** A built-in may declare `replaces` only under the parity rule of PS12.

**PS11. NetFlow interface indexes.**

- **Mapping.** A record's input and output `ifIndex` are mapped to OPNsense interfaces (identifier and device, the `docs/data-model.md` *Vocabulary* terms) through a **read through the broker**: a declared datum with a max age, the endpoint S6 establishes.
- **The mapping is the one in force at reception.** It is stored with the record beside the raw index, so a later renumbering does not rewrite history.
- **Unmapped indexes.** If S6 establishes no endpoint, or an index is unknown, the interface columns are null, the raw index is kept, and the condition is counted and reported as `interface_index_unmapped`. The core never matches an index to an interface by position, name or count.

**PS12. Built-in plugins: push variants, and how they coexist with the polled ones.**

- **The parity rule for `replaces`.** A built-in push entry may declare `replaces` only when a test passes: the same recorded source lines, delivered once through the pulled fixture and once through the pushed fixture, yield contract records equal **in every field of the kind's contract**, except:
  - the record identity (PS9);
  - any field listed in the plugin's test data with the reason it differs.

  A variant that fails parity feeds an own table, or is not built, and declares no replacement.

| Plugin | Push variant | Condition | Coexistence |
|---|---|---|---|
| `pf` | syslog, program `filterlog` (S3), parse function over the CSV (S4); `kind: firewall_log`, provider `pf` | S2, S3 and S4 verified | `replaces` the filter-log endpoint under PS9, if parity holds. The backend's `label` is not a contract field. The pulled `__digest__` is replaced by the namespaced pushed identity |
| `suricata` | syslog alerts (S7); `kind: security_event`, provider `suricata` | S7 shows alerts can be sent to syslog in a parseable form | `replaces` `query_alerts` under PS9, if parity holds. Only today's collector is reproduced (B1); `tls` / `http` events are open question 12 |
| `unbound` | syslog query lines (S8) | S8 shows the UI enables them | `replaces` `search_queries` **only if parity holds**. If the logged line lacks the verdict or the blocklist, the `dns_lookup` contract's `action` would degrade to `unknown`, which fails parity. The variant then writes its own table and declares no replacement |
| `netflow` (new built-in) | NetFlow v9 / IPFIX; its own kind and its own hypertable of flow records: the observing interface (PS11), the flow direction where exported, endpoints, ports, protocol, octets, packets, start, end | S5 verified | **Declares no replacement.** No polled endpoint measures the same thing. Insight's aggregates are pro-rated, direction-doubled and port-approximated (survey, *Data source 3*, *Response shape*), and `traffic/top` is a 2 s iftop snapshot. **Its rows never enter `flow`,** which stays the filter log's |

- **DHCP lease servers get no push variant.** A logged DHCP exchange is an event, not the lease state the `dhcp_lease` kind reconciles.
- **The `netflow` dataset's double-count trap is stated where a reader meets it.** A connection crossing two captured interfaces is exported once per interface, as Insight's details are. The table therefore records the observing interface on every row. `docs/plugins.md` and the dataset's catalogue label say that a sum across interfaces counts such a connection once per interface. No built-in widget serves such a sum.
- **The observation-point limit applies** to every NetFlow figure: traffic inside one interface's network never reaches the exporter. It is documented in `docs/plugins.md` and the README as the limit already is.

**PS13. Both catalogues.**

- **The API catalogue (A15)** gains one push entry per listener and per declaration. It carries:
  - protocol and transport;
  - listener state;
  - the accepted-sender set's size and the refused senders with their counts;
  - programs or templates seen, with message counts;
  - subscribers and the columns each captures;
  - rates, drop counters and gaps;
  - liveness;
  - replacements and the current source segment.

  It never carries a message, a field value, a credential or the base URL. It is served at the existing path and through `host.catalogue()`.
- **The data catalogue (P8).** Every push output table is a dataset like any plugin table, with:
  - its roles and units;
  - nature `event`;
  - `loss_detectable` (PS7);
  - coverage equal to the intervals in which the source was alive, minus its recorded gaps.

  A covered bucket with no row is a true zero, and an uncovered one is a gap.

**PS14. Ports and compose.**

- **Start parameters.** The three listeners take start parameters with no default, like `--listen`.
- **The serve file.** `docker-compose.serve.yml` publishes the syslog UDP and TCP ports and the NetFlow UDP port **from variables with no default**, the protocol on each mapping explicit (`/udp`, `/tcp`), host port equal to container port, as `OPNVIEW_LISTEN` does.
  - The canonical serve invocation keeps working **unchanged with none of them set**. How optional mappings are expressed (a second override file, or profiles) is the Builder's, stated in the file's header comment. Compose cannot omit a `ports:` entry whose variable is unset.
- **Nothing for `checks` and `pre-deployment`.** `docker-compose.yml`'s `checks` and `pre-deployment` acquire no new variable. Tests bind ephemeral loopback ports and never a fixed one.
- **The README states:**
  - that the serve container runs unprivileged, so a port below 1024 needs a capability the file does not grant, and a port of 1024 or more is advised (OPNsense's target takes any port, S1);
  - that `docker compose run` needs `--service-ports`.

**PS15. Tests and budgets.**

- **Go tests**, for the core decoders, framing, routing, batching, gaps, senders and PS9.
  - **Fixtures.** Recorded syslog lines and NetFlow packets, rewritten to documentation addresses (RFC 5737, RFC 3849) and documentation MAC addresses, with every device and interface name replaced as the existing fixtures do, so X1 holds. Each carries `__provenance__`. A binary packet is stored as hex inside that JSON wrapper.
  - **Fuzzing.** One Go fuzz target each for the syslog header, TCP framing and the NetFlow / IPFIX decoder. The seed corpus runs as ordinary tests in `checks`. `pre-deployment` fuzzes each for a bounded time the Builder states, inside the 180 s budget.
- **JavaScript tests for plugins** (`opnview plugin test`). The test world gains functions that deliver recorded syslog lines (with transport and sender) and recorded NetFlow packets **through the core's real decoder and router**, and read what reached the plugin's table or kind. A plugin's tests never call its parse function directly to prove ingestion.
- **Budgets.** These are constants written once, measured by `pre-deployment` and printed beside their limits. The Builder may change one with a measured justification in the hand-back. The proposals:

  | Measure | Limit |
  |---|---|
  | Syslog header decode plus routing of a line nobody subscribes to | median ≤ 10 µs per line |
  | Regular-expression path, listener to committed hypertable rows | ≥ 10 000 messages/s sustained for 10 s with zero drops at default bounds |
  | Parse-function path (a `filterlog`-shaped fixture) | ≥ 2 000 messages/s for 10 s with zero drops |
  | NetFlow v9 decode plus routing | ≥ 50 000 flow records/s |
  | NetFlow, listener to committed rows | ≥ 10 000 records/s for 10 s with zero drops |
  | Reception to commit under a trickle | 95th percentile ≤ 2 s |

  The push measurements together add at most 45 s to `pre-deployment`, which stays within 180 s. `checks` stays within 60 s: everyday tests use small fixtures and no throughput run.

**PS16. Documentation.**

- **`docs/plugins.md`** gains a section *Push sources* covering:
  - the declaration;
  - the type vocabulary and the IANA mapping;
  - RE2 syntax;
  - the parse function;
  - senders, liveness and replacement;
  - PS9's segments;
  - gaps and `loss_detectable`;
  - the new refusal codes;
  - three worked examples run by A15's test:
    - a syslog program into an own table with a regular expression;
    - a parse function;
    - a NetFlow subscriber.
- **The other documents:**
  - `docs/data-model.md` *Vocabulary* gains the terms below.
  - `docs/architecture.md` gains the B4 row (§5).
  - The README gains *Push sources* (§10).

  | Term | Its status |
  |---|---|
  | push source; source segment; liveness | `opnview`'s own, with the reason |
  | collector, exporter (RFC 7011), originator (RFC 5424) | the RFCs' own |
  | `program` | syslog-ng's and OPNsense's |

## 5. Scope: amendments inside existing sections

- **A1, the manifest.** Add "its push sources (PS3)" to what the manifest declares.
- **A3.** Add a bullet: "**Pushed data.** A push declaration's parse function receives batches of decoded messages (PS8). Pushed records of a core kind enter that kind's coordinator under PS9."
- **A4.**
  - Replace "columns (integer, real, text; …)" with "columns of the plugin type vocabulary (PS4)".
  - Add: "a push output table's time column is an `instant`, and it holds no JSON type".
- **A5, refusals.** Add:
  - an unknown push protocol;
  - a regular expression that does not compile under RE2, or a named group that is not a column;
  - a column type outside PS4;
  - a push output table without an `instant` time column;
  - a declaration naming both or neither of `output_table` and `kind`;
  - `push_kind_without_replacement`;
  - `replaces` naming an undeclared endpoint;
  - an Information Element name outside the registry;
  - a liveness span below its floor.

  Each has a code the Builder names and `docs/plugins.md` lists.
- **A5, listing.** Add per plugin: its push declarations, each with listener state, counters, liveness and current source segment.
- **A14, *Declared equivalence*.** Append: "A push source may also be declared as replacing a polled endpoint (PS10). For a core kind, PS9's source segments keep the two paths from counting a record twice."
- **A15.** Add "the push entries of PS13" to its content.
- **B4, the audit table.** Add the row:

  > `| Push listeners and decoders (syslog header, NetFlow v9 / IPFIX templates, IANA IE registry) | the push packages | Core, protocol code against public standards, like internal/specialpurpose. A protocol is a seam inside the core: one decoder file and one registry row. Program formats are plugins |`
- **C3, *Rolling strip*.** Append: "Where a pushed `firewall_log` source contributes, the decision series' `complete_until` trails reception rather than the poll interval."
- **C6.** Append:
  - "Every row is re-examined with push sources available, rows 4 (rate by port and protocol) and 8 (live rate) first. A row servable only when the user configures an export names the dataset or field, the export (survey, *Push sources*), and the named state served when it is absent."
  - "Rows touching site names record the S7 finding."
- **P8, *Core datasets*.** Append: "Push output tables are datasets with `loss_detectable` and coverage from liveness minus gaps (PS13)."
- **Phase D, map.** Add the push packages, named by responsibility under the 15-file rule. One proposal:
  - `internal/push` (listeners, routing, batching);
  - `internal/push/syslog`;
  - `internal/push/netflow`;
  - `internal/push/ipfixregistry`.
- **Phase E.** Append: "PS15's throughput and fuzz runs are inside the 180 s, printed beside their limits."
- **F.2 table.**
  - `README.md`: add "*Push sources*: configuring the exports on OPNsense, ports, the sender check's limit".
  - `docs/opnsense-api-survey.md`: add "*Push sources: remote syslog and NetFlow export, read from source for push sources*".
  - `docs/plugins.md`: add "*Push sources*". The size target stays, and Risk 17 applies.
  - `docs/data-model.md`: add "the push terms of PS16; *Storage fit*: push hypertables".
  - `docs/architecture.md`: add "the push data flow beside the broker; *Decisions in force* carries decision 15".

## 6. Acceptance criteria: new section inserted after C-AC15 and before C-AC16

### Phase C, part 2: push sources

Every test runs on the PostgreSQL test instance with a fake clock, loopback listeners on ephemeral ports, a fake firewall and the counting transport.

- [ ] **PS-AC1. The firewall side is verified, and an absent export is reported.**
  - The survey section of PS1 exists with S1–S9, each carrying a 26.7.3 source URL or marked `UNVERIFIED:`.
  - A test asserts that every built-in push declaration names a survey section that exists, and that no built-in declaration rests on an item still `UNVERIFIED:`.
  - With a listener configured and nothing arriving, every declaration reports a named availability state (and, if S9 verifies, says whether the firewall has a target configured), never data.
  - Nothing in the code calls a mutating action of the Syslog or Netflow controllers (BR-AC15 extended).
- [ ] **PS-AC2. The core is agnostic.**
  - A test greps every non-test Go file of the push packages and finds no program name of any built-in plugin, no regular expression over a message, and no message field name.
  - Adding a test protocol decoder is one file and one registry row, with no edit to routing, batching or catalogue code (shown by a test-only decoder).
- [ ] **PS-AC3. Syslog header.** Table-driven tests decode, to facility, severity, instant, hostname, program, process id, MSGID, structured data and message:
  - RFC 3164 with and without a hostname, with and without `[pid]`, and with an ISO timestamp;
  - RFC 5424 with structured data, NILVALUEs, a BOM, fractional seconds and offsets.

  Malformed lines are counted as `header_unparseable` and store nothing.
- [ ] **PS-AC4. Timestamps.**
  - RFC 3164 instants equal the year-less normaliser's answers with the measured offset, including a line dated 31 December received on 1 January (fake clock).
  - RFC 5424 offsets are applied as given.
  - NetFlow v9 start and end equal `unix_secs` and `sysUptime` arithmetic on a fixture. IPFIX absolute and delta time elements give the same instants.
  - Every instant in a JSON response is an integer of epoch seconds.
- [ ] **PS-AC5. TCP framing.**
  - Octet-counted and newline-framed connections are both decoded, including frames split across reads and several frames in one read.
  - A frame above the bound closes its connection and is counted.
  - The connection bound and the idle time-out are enforced.
- [ ] **PS-AC6. NetFlow v9 and IPFIX.** Fixtures cover:
  - templates and options templates per exporter and domain;
  - data before its template (held, then decoded; past the bound, counted as `netflow_template_missing` with a gap);
  - IPFIX template withdrawal and redefinition;
  - variable-length fields, padding and enterprise fields;
  - sequence-number jumps (a `netflow_sequence_gap` gap with the right interval);
  - a v5 packet (`netflow_version_unsupported`, nothing decoded).

  Every decoded field carries its IANA name and the PS4 type of its abstract data type.
- [ ] **PS-AC7. Fuzzing.** The three fuzz targets' seed corpora pass in `checks`. `pre-deployment` runs each for its stated time, with no panic and peak memory under a stated bound.
- [ ] **PS-AC8. Senders.**
  - A datagram and a TCP connection from a loopback address outside the accepted set store nothing, and are counted per address in the catalogue.
  - The same from an address in the set are stored.
  - Changing the base URL or the discovered interface addresses changes the set with no restart.
  - No address literal appears in the code (X1).
- [ ] **PS-AC9. Routing and one decoding pass.**
  - A line whose program has no subscriber causes zero regex matches, zero script calls and zero rows (counters).
  - Two plugins subscribed to the same program cause one header decode per line, and each table holds only its own declared columns.
  - Two NetFlow subscribers cause one template decode per packet.
- [ ] **PS-AC10. The declaration.**
  - Each PS3 and §5 A5 refusal has a test, is listed with its code, and leaves the service and the other plugins running.
  - A valid declaration creates its output table, in the plugin's schema, as a hypertable on its `instant` column. Catalog inspection shows each PS4 type mapped as the table says, and no `json` or `jsonb` column.
- [ ] **PS-AC11. Values.** A value that does not convert is null in a nullable column, or refuses the row in a non-nullable one, and is counted as `push_value_invalid`. An unsigned 64-bit value above the `bigint` range is refused, not wrapped.
- [ ] **PS-AC12. The parse function.**
  - For N messages and a batch bound B, the script is called at most ⌈N/B⌉ times plus the flushes by time (counter).
  - The regular-expression path makes zero script calls.
  - An exception and a time-out each fail only that call's messages, counted and recorded as a gap, and the next batch is processed.
  - A contract-violating record is refused (`records_invalid`).
- [ ] **PS-AC13. Buffers, drops, batching.**
  - With a database fake that stalls, the queues fill. The drops are counted, a gap of reason `push_buffer_overflow` spans their reception instants, and memory stays under the stated bound.
  - A failing commit is retried within its bound and then recorded as `push_store_failed`.
  - Insert statements per table are at most ⌈rows/batch⌉ plus the flushes by time.
  - A trickle commits within the time bound.
  - The interval during which the listener was down, including `opnview`'s own restart, is a `push_listener_down` gap.
- [ ] **PS-AC14. No double count: the critical criterion.** One fixture of `filterlog` lines is delivered through both the fake pulled endpoint and the syslog listener, across a handover from pull to push and back. Every boundary second holds records delivered by both paths, and some by only one.
  - Every record delivered by both paths is stored **exactly once**. No record delivered by both is lost.
  - A record delivered only by the non-contributing path is not stored, and is counted as `superseded`.
  - The source segments never overlap and cover the fixture's span with no hole other than recorded gaps.
  - The `flow` table, `pair_outcome` (a connection with one leg pulled and one pushed is paired once), and every aggregate family and period at `1h`, `24h`, `7d` and `30d` equal an independent computation from the fixture's distinct records (P-AC9's method).
  - A pull gap overlapping a push-covered interval is clipped to the uncovered part.
  - Pushed digests never equal a pulled digest, and `flow.log_digest UNIQUE` is never violated.
  - A declaration with `kind` and no `replaces` is refused at load.
- [ ] **PS-AC15. Liveness and replacement.**
  - While a push source is alive and past its overlap, the declaring plugin makes **zero** calls to the replaced endpoint (counting transport), and another plugin's demand for it is still served.
  - After the liveness span plus its hysteresis, with no message (fake clock), polling resumes at the next scheduling decision, and the plugin is told which path answered.
  - A source near its span does not flip at every message.
  - BR-AC21 still passes.
- [ ] **PS-AC16. Parity.**
  - For each built-in push entry declaring `replaces`, the parity test passes on recorded fixtures, listing every excepted field with its reason.
  - A test asserts that no built-in declares `replaces` without a passing parity test, and that the `netflow` plugin declares none.
- [ ] **PS-AC17. Interface indexes.**
  - The index-to-interface mapping is read through the broker (BR-AC1 still lists only the broker and `verify.go`) at its max age.
  - Each fixture record stores the raw index and the mapped identifier.
  - An unknown index stores null, is counted and is reported as `interface_index_unmapped`.
  - A mapping change applies only to records received after it.
  - If S6 is still `UNVERIFIED:`, the mapping is absent and every record reports `interface_index_unmapped`, with nothing inferred.
- [ ] **PS-AC18. Catalogues.**
  - The API catalogue lists every PS13 push field for the fixtures, answers 401 with a JSON condition without a session, and holds no message text, field value, credential or base URL (string search). `host.catalogue()` returns the same.
  - `GET /api/datasets` lists every push output table with its roles, units, nature `event` and `loss_detectable` (false for a UDP syslog fixture, true for TCP and NetFlow).
  - A catalogue query answers a bucket inside a push gap, or outside liveness, as a gap, and a covered empty bucket as zero.
- [ ] **PS-AC19. Built-in variants.**
  - Each built-in push variant that PS1's findings allow exists, as PS12 states, with JavaScript tests in its `tests/` delivering recorded fixtures through the real decoder.
  - Each variant PS1's findings rule out is listed in the hand-back with the survey item that rules it out.
  - The `netflow` plugin's table records the observing interface on every row, and its catalogue label states the per-interface counting.
- [ ] **PS-AC20. Fixtures.** Every new fixture passes X1 and carries `__provenance__`. No recorded line or packet holds an installation address, MAC, device or interface name.
- [ ] **PS-AC21. Listeners and compose.**
  - The binary started without a push parameter opens no push socket (the process's sockets inspected), and its declarations report `listener_not_configured`.
  - `docker compose run --rm checks` and `pre-deployment` need no new variable.
  - The canonical serve invocation runs with no push variable set.
  - The documented push invocation publishes exactly the configured UDP and TCP ports, each with its protocol.
  - No compose file holds a push port number as a default.
- [ ] **PS-AC22. Nothing changes when push is not configured.** With no listener, B5's byte identity, BR-AC1 to BR-AC27 and every existing test pass unchanged.
- [ ] **PS-AC23. Budgets.**
  - `pre-deployment` prints every PS15 measure beside its limit and fails above it. The push measures add at most 45 s, and the whole command stays within 180 s.
  - `checks` stays within 60 s.
- [ ] **PS-AC24. Documents.**
  - `docs/plugins.md` *Push sources* exists. Its three worked examples load and produce what it states (A15's test).
  - Its refusal codes, gap reasons and type table equal the code's (F-AC5).
  - `docs/data-model.md` *Vocabulary* defines the PS16 terms, with their status.
  - The README *Push sources* section exists, and its OPNsense steps match the survey's PS1 section.
- [ ] **PS-AC25. Never a write to the firewall.** No code path issues a request to configure an export. The README instructs the user to do it.
- [ ] **PS-AC26. Inbound is not outbound.** A10's counting transport over a full run with push fixtures sees the same destinations as before. The listeners make no outbound connection (process inspection or test).
- [ ] **PS-AC27. Versions.** A push entry under two A19 version ranges selects its declared parse script per firewall version, and an uncovered version yields `firewall_version_unsupported` (BR24's method).

## 7. Acceptance criteria: amendments to existing ones

- **C-AC16 (audit).**
  - Append: "and every row records its re-examination with push sources (C6 as amended). A row servable only with an export names the export, the dataset or field, and the state served without it."
  - Its verification happens after PS-AC1 to PS-AC27 pass.
- **A3.** Add: "including the push refusals of §5".
- **A13.** Add: "and the type-vocabulary table of `docs/plugins.md` equals PS4's mapping in code".
- **F-AC5.** Add: "every push refusal code, gap reason, type and catalogue field named in `docs/plugins.md` *Push sources* exists in code, and the reverse".
- **X2.** Append: "and PS-AC1 to PS-AC27".

## 8. Live validation: additions

These run after LV-P and LV-B, read-only toward the API. **The user configures the exports on OPNsense. `opnview` and the orchestrator never do.** The scratch build in the dev stack publishes the push ports; the live build does not.

- **LV-PS0, the firewall-side cost.** Using LV-C's method (`cpu_usage/stream`, 15 minutes each), the firewall's processor use is measured in three states:
  - no export;
  - the remote syslog target enabled for the subscribed programs;
  - the NetFlow export enabled to `opnview`.

  The figures are reported to the maintainer and recorded as proportions in the survey's PS1 section.
- **LV-SY, syslog against polling, over 15 minutes.** The scratch build runs with `pf` pushed over **TCP** and, in a second window, over **UDP**, beside a pull-only reference (the old build, or the scratch build with push disabled).
  - **Counts.** Per minute, the filter-log records stored by each are compared. Every difference is named: pull's `limit` gaps (recorded), window edges, `superseded` counts, or a push gap with its reason. Otherwise it is a defect.
  - **Peak and drops.** The peak message rate is recorded, and the drop counters are zero.
  - **Handover.** The orchestrator stops and restarts **the scratch build's listener**, never the firewall's target. Across both handovers, `flow` and the aggregates of the affected hours equal the pull-only reference's for records both saw, and the source segments are shown.
  - **The other built-ins** (`suricata`, `unbound`) are compared the same way where PS1 allowed them.
- **LV-NF, NetFlow over 15 minutes.**
  - Per captured interface, NetFlow octets against `traffic/interface` counter deltas over the same window, as a proportion. The difference is explained by the observation point, the timeouts (S5) or a recorded gap, or it is a defect.
  - The sequence gaps, template refreshes and records/s.
  - The share of records whose `ifIndex` was mapped. Every unmapped index must be named.
- **LV-SND, senders.** The refused-sender list after both windows. If the firewall's datagrams were refused because the container network rewrote the source address, open question 10 governs, and the validation stops there until the maintainer answers.
- **LV-AUD.** The C6 audit's push re-examinations are shown to the maintainer with LV-SY's and LV-NF's figures.

**The validation passes when**, as before, plus:

- LV-SY's and LV-NF's differences are all named;
- the drop counters stay zero at the live peak;
- the maintainer accepts LV-PS0 and LV-AUD.

## 9. Rules, Out of scope, Risks

**Rules, *Hard rules carried from the project*: append**

- "a push listener accepts only the configured firewall's addresses, derived at run time;"
- "a pushed record and a pulled record of one provider never both count for the same instant (PS9);"
- "a drop, a sequence gap or a missing template is a gap, never a zero;"
- "no JSONB column in a push output table;"
- "listeners are inbound and make no outbound call; the outbound rule is unchanged."

**Out of scope: append**

- Syslog over TLS (RFC 5425), and any authentication of the sender beyond the source-address check.
- NetFlow v5, sFlow and any other export protocol.
- Using pushed Suricata `tls` / `http` records for site names (open question 12).
- Joining NetFlow volumes to logged connections in the core's tables (open question 11).
- Continuous aggregates and compression on push output tables. These are Phase E's licence work under the 12 October decision, and a plugin still declares no continuous aggregate.
- Recovering a gap by re-reading the firewall's own log for the gap interval.
- Configuring, or checking by a write, anything on the firewall.

**Risks: append**

38. **Address spoofing.** The sender check refuses other senders, but on the same network segment a forged source address passes it. Neither UDP syslog nor NetFlow authenticates. What is achievable is stated in the README rather than overstated.
39. **Source addresses rewritten by the container network.** Some Docker configurations rewrite the source of published UDP (Docker Desktop, IPv6 with the userland proxy). Every datagram would then be refused. PS6's refused-sender listing makes this visible, and LV-SND checks it on the maintainer's host.
40. **Undetectable UDP syslog loss.** No sequence number exists, so `loss_detectable` is false and replacing a core kind over UDP trades a verifiable gap rule for an unverifiable one (open question 9).
41. **The handover boundary.** Seconds-resolution timestamps, clock offset and log write latency make "a second read completely" a judgement the Builder must make exact. PS-AC14 tests it, and LV-SY checks it live.
42. **Script cost on the hot path.** A busy ruleset can log thousands of `filterlog` lines per second, and goja runs each batch in a fresh interpreter. PS15's 2 000/s floor may be below a busy firewall's peak. LV-SY records the live peak; if it exceeds the measured capacity, that is a defect to report, not a reason to drop the batch rule.
43. **Assumed OPNsense behaviour.** Everything in PS1 is `UNVERIFIED:` today. In particular:
    - IPFIX may be unobtainable from OPNsense core (`ng_netflow`), leaving the IPFIX decoder proven on fixtures only;
    - the syslog TCP framing is unknown;
    - Suricata and Unbound may not log what parity needs.

    PS12's conditions absorb this. A ruled-out variant is reported, not built.
44. **Double counting inside NetFlow.** One flow is exported per captured interface (PS12). A third-party author summing across interfaces double-counts, and only documentation stands between them and the error.
45. **Ports below 1024.** The unprivileged container cannot bind syslog's registered port. A user who sets 514 sees a bind failure, which the README anticipates.
46. **Firewall load from the exports.** Remote logging and `ng_netflow` cost processor time on the firewall. LV-PS0 measures it.
47. **Delay of the audit.** C6 now waits for push sources, so the step-7 interface is specified later.

## 10. ROADMAP.md, README.md and CLAUDE.md

- **`ROADMAP.md`.** Replace the heading "### Planned: push sources — syslog and NetFlow (maintainer, 12 October 2026; not yet scheduled)" with "### Push sources: syslog and NetFlow (maintainer, 12 October 2026)". Replace its sentence "Points the specification of this step must settle:" with "The specification settles them in Phase C, part 2 of the backend-completion cycle". Phase F condenses the section.
- **`ROADMAP.md`, *Rules that apply to every step*, outbound bullet.** Append: "Push listeners are inbound: they receive what the user configured the firewall to send, accept only the firewall's own addresses, and make no outbound call."
- **`README.md`, the sentence of the PostgreSQL amendment §13** "Nothing is installed on the firewall and nothing is changed on it: everything goes through its REST API, read-only." Replace it with:

  > "Nothing is installed on the firewall and `opnview` changes nothing on it: it reads the REST API, read-only, and can receive the firewall's syslog and NetFlow exports if you configure them on the firewall yourself."
- **`README.md`, new section *Push sources*.** It says:
  - why to configure the exports, and what each adds;
  - the OPNsense steps for a remote syslog target and a NetFlow destination, taken from the survey's PS1 section (TCP advised for syslog, with the reason);
  - the ports and their variables, and the 1024 note;
  - the sender check and its limit (Risk 38);
  - that polling resumes on its own when an export stops.
- **`CLAUDE.md`, proposed wording** (for the orchestrator to apply with the maintainer's agreement; the Builder does not edit it). In *The user interface*, after "It reads five sources through the OPNsense REST API: …", add:

  > "It can also receive the firewall's syslog and NetFlow exports, configured by the user on OPNsense; `opnview` still writes nothing to the firewall."

  The sentence "Site names are inferred from resolver lookups, unconditionally." stays unless open question 12 is answered (b).

## 11. Existing spec and repository sentences that become obsolete

1. The 12 October "Decided" block, *Order of the rest*: "then C, D, E (including …)" (replaced, §3).
2. The scope phase list's **Phase C** line, as amended by the PostgreSQL and broker amendments (replaced, §3: Phase C, parts 2 and 3).
3. A4: "columns (integer, real, text; nullable or not; …)" (replaced by PS4, §5).
4. `docs/plugins.md`, *Tables*: "`type` (`integer`, `real` or `text`)" and "`retention` | The integer column holding each row's instant, in UTC epoch seconds" (for the Builder to correct to PS4).
5. `docs/plugins.md`, *Endpoints*, `replaces`: "For a stream: the name of another endpoint …". It is no longer stream-only: a push declaration carries it too (PS3).
6. `ROADMAP.md`, the heading "(…; not yet scheduled)" and "Points the specification of this step must settle:" (replaced, §10).
7. The README sentence "everything goes through its REST API, read-only" from the PostgreSQL amendment §13 (replaced, §10).
8. The maintainer's sketch keys `type: stream` and `provider: syslog|netflow`. They are named `push` and `protocol` (PS3), because `stream` (A14) and `provider` (A1) already carry other meanings. This is a deliberate departure, recorded here.

Not obsolete, stated for clarity:

- A9's "the broker is the only path from opnview to the firewall API": push is inbound.
- BR-AC1.
- The outbound rule.

## 12. Open questions (additions)

Open questions 1 to 8 stay as answered.

9. **May UDP syslog replace a polled core kind?** UDP syslog carries no sequence number, so loss on the wire is invisible (`loss_detectable: false`), whereas the polled filter log detects its own gaps.
   - **(a)** A push entry with `kind` and `replaces` takes over only over TCP. Over UDP, a core-kind push source is received, counted and shown in the catalogue, and polling continues.
   - **(b)** UDP may replace polling too. The source segments it contributes are marked `loss_detectable: false`, and every gap listing over them says so.
   - **(c)** UDP may replace polling, with no distinction.

   **Recommendation: (a).** It keeps the project's rule that a loss is a gap and never silent. TCP is offered by OPNsense's target (S1, to verify), and UDP still serves own-kind datasets, where `loss_detectable` is carried. Scope follows (a) unless the maintainer chooses otherwise.
10. **Accepted senders when the container network rewrites source addresses.**
    - **(a)** Only addresses derived at run time (PS6), with refused senders listed. A rewriting network is a deployment to correct (host networking, or a Docker setting), documented in the README.
    - **(b)** In addition, an operator setting listing further accepted addresses, entered through the settings JSON API and never in a file.

    **Recommendation: (a), and (b) only if LV-SND shows the maintainer's own host rewriting addresses.** (b) reopens what "only the configured firewall" means, and should be added on evidence. Scope follows (a).
11. **Should NetFlow volumes become a core figure?**
    - **(a)** No. The `netflow` dataset is served through the data catalogue and `custom_chart` only, and C6 cites it where it makes a row servable.
    - **(b)** Yes. Join NetFlow records to logged connections (`flow`), giving each connection its real volume. That is a new correlation design, with its own double-count rules, in a separate spec.

    **Recommendation: (a) this cycle, (b) as its own spec if the audit shows it is wanted.** Scope follows (a).
12. **Pushed Suricata `tls` / `http` records and site names.** The survey's finding that `tls` events "cannot be read back through the API" was the reason site names are inferred unconditionally. If S7 shows those events can be sent to syslog, observed attribution becomes possible.
    - **(a)** Record S7's finding and C6's re-examination only. Observed attribution is a separate spec, in line with "everything except the Suricata plugin".
    - **(b)** Build it in this phase: store SNI, attribute flows with method `observed`, recorded per record and surfaced as the site-name rule requires.

    **Recommendation: (a).** It touches attribution, the data model and a binding `CLAUDE.md` statement, and the maintainer excluded Suricata work from this cycle. Scope follows (a) unless the maintainer chooses (b).

No other question. The listener design, routing, the type vocabulary, the parse function, senders, timestamps, ifIndex mapping, the catalogues and shared decoding were decided by the maintainer or settled by the roadmap's points. The no-double-count rule (PS9) follows from the requirement that nothing counts twice and from the unverifiable shared identity. Ports follow the existing no-default rule.

---

**Status of this amendment: APPROVED by the maintainer on 12 October 2026.**

**Decided (maintainer, 12 October 2026), push-sources open questions:**
- Q9: (a). A pushed source replaces polling of a core kind **over TCP only**. Over UDP it is received and stored for plugins, while polling continues for the core kinds.
- Q10: (a). Only the configured firewall's addresses are accepted. A network that rewrites source addresses is a deployment to correct, as documented in the README. An operator setting for extra addresses is added only if live validation shows it is needed.
- Q11: **(b), in this cycle.** NetFlow volumes are joined to the core's logged connections, so each connection gets its real volume. It is a new correlation design with its own no-double-count rules, specified by the planner before it is built.
- Q12: **(b), in this cycle.** If Suricata `tls`/`http` events can be sent to syslog (to be verified against 26.7.3, S7), SNI and HTTP host are stored and flows are attributed with method `observed`. The method is recorded per record and surfaced as the site-name rule requires. This changes the `CLAUDE.md` statement that site names come only from resolver lookups. The orchestrator proposes that edit to the maintainer.


**This is the text to append after the "Decided (maintainer, 12 October 2026), push-sources open questions" block.** It covers Q11(b) and Q12(b) in this cycle. The proposed CLAUDE.md diff is in §J, for you to apply once the maintainer agrees.

---

**Addendum to the push-sources amendment: what Q11(b) and Q12(b) add to this cycle (maintainer, 12 October 2026)**

**Placement.** Append this after the push-sources "Decided" block, with its own `---` separator and the same preamble line as the earlier amendments. Each section below names its insertion point inside the push-sources amendment. **Cycle kind stays `standard`.** Everything below is Go, SQL, JSON contracts and server-side JavaScript. Every name-bearing or volume-bearing change reaches a screen only through the widget JSON, which step 7 renders.

**What I read and found (fact check of the brief).**

- **`flow` is one filter-log record, a *leg*.**
  - `packet_bytes` is the length of the one packet that created a state (`docs/data-model.md`, *`flow`*, "Logged bytes are not conversation bytes").
  - The six aggregate families × four periods, `purged_flow_hour` and `pair_volume_observation` carry **logged** figures only.
  - A paired connection counts once, on its first leg. Its second leg counts only under `exit_leg_device` and in the rule family (*The aggregate families*; *Two legs of one connection*).
- **The widget API's measure vocabulary has three values:** `logged`, `sampled` and `interface_counter` (`internal/widget/envelope.go`; `docs/widget-api.md`, *Volumes and the observation-point limit*). `Volume.lower_bound` is documented as "always true".
- **`domain_attribution` cannot hold an observed name today.**
  - `method` has a `CHECK (method IN ('resolver_cache_answer', 'lookup_timing'))`.
  - `dns_resolution_id` is `NOT NULL`.
  - `correlation_delay_seconds` is `CHECK (>= 0)`.
  - `address_observation_id` is required exactly for `resolver_cache_answer` (`internal/store/schema.sql`, lines 1617–1631).
  - `widget.MethodCounts` is a closed struct of `resolver_cache_answer`, `lookup_timing` and `not_recorded_beyond_flow_horizon` (`internal/widget/sites.go`).
- **The "Insight-based polled volume path" does not read Insight's aggregates.** The `insight` plugin samples `traffic/top` (measure `sampled`) and `traffic/interface` (measure `interface_counter`). The `networkinsight` / `netflow` aggregate paths answered 404 on 26.7.3_11 (`plugins/insight/insight.js` header; survey, *Wrong paths*). **No polled path delivers per-record NetFlow.**
- **`flow_volume` is a core-only kind, "derived by opnview from the filter log"** (`internal/collect/provider/contract.go`, `coreOnlyKinds`). It is not reused here.
- **Two parts of the push amendment conflict with Q11(b) and Q12(b).**
  - PS12 routes NetFlow into the `netflow` plugin's **own** table. The core cannot join a plugin's own table, because it reads contracts, not plugin schemas (PS2: the core is agnostic).
  - PS9's last bullet refuses any `kind` entry without `replaces`, which would refuse a push-only core kind.
  - Both are amended below.
- **Suricata on the maintainer's firewall** runs on two internal interfaces and not on WAN (survey, *Suricata, on that firewall*). Observed names on that installation would therefore match first legs, before NAT.
- **No OPNsense, FreeBSD or Suricata fact used below has been read for this addendum.** I had no network access. Every such fact is a candidate row S10–S14 under PS1, marked `UNVERIFIED:`. Nothing is built on any of them until PS1 records them.
- **The no-Go-under-`plugins/` rule** stated in your brief is not written in the spec text I read. It is restated in §G so it is.

---

## A. Context: append to decision 15 (push amendment §1), as its last sub-bullet

> - **15a. NetFlow volumes and observed site names, in this cycle** (maintainer, 12 October 2026; open questions 11 and 12 answered (b)).
>   - NetFlow records are joined to the logged connections. Each connection gets its measured volume as a second measure, `netflow`, stored beside the logged one and never summed with it.
>   - If OPNsense can send Suricata's `tls` and `http` eve events to syslog (S13), their TLS server name and HTTP host are stored. Flows are then attributed with a new method, `observed`, which takes precedence over the two inferred methods. The method is recorded per record and exposed per record.
>   - If S13 is negative, that part is not built, and the reason is recorded.

## B. Scope: phase order (push amendment §3)

Replace the **Phase C, part 2** line with:

> **Phase C, part 2.** Push sources: syslog and NetFlow (PS1–PS16), then the NetFlow volume join (PS17–PS23), then observed server names (PS24–PS30), each part starting only when the previous one passes its own criteria.

- Replace "C6 starts only when PS-AC1 to PS-AC27 pass" with "C6 starts only when PS-AC1 to PS-AC47 pass".
- In §7, X2: replace "PS-AC1 to PS-AC27" with "PS-AC1 to PS-AC47".

## C. Scope: PS1 table, append rows S10–S14

**Insertion point:** after row S9 of PS1's table.

| # | What must be established | Candidate source (to read, `UNVERIFIED:` until read) |
|---|---|---|
| S10 | The NetFlow v9 template `ng_netflow` emits as OPNsense starts it:<br>- each Information Element: addresses v4/v6, ports, protocol, `tcpControlBits`, octets, packets, first/last switched, input/output interface index, flow direction, any sampling element or options template, and how ICMP type/code is encoded;<br>- whether IPv6 flows are exported;<br>- the active and inactive timeouts and cache size OPNsense sets | FreeBSD 15.1 `ng_netflow(4)`, `sys/netgraph/netflow/netflow_v9.c`; the OPNsense script that starts `ng_netflow` (S5's) |
| S11 | Where each capture point sits relative to pf on the same interface:<br>- on ingress and on egress, whether the exported addresses and ports are the form pf **logs on that interface** (before or after translation);<br>- whether packets pf blocks are exported;<br>- what the WAN "egress only" option (if S5 finds one) captures | `ng_netflow(4)`, `ng_ether(4)`, the OPNsense hook-up script; pf.conf(5) 15.1 |
| S12 | pf's state timeouts as OPNsense 26.7.3 configures them: TCP states, UDP, ICMP, other, and adaptive timeouts. Also the read-only endpoint, if any, exposing the values in force | pf.conf(5) 15.1, `set timeout`; `src/etc/inc/filter.inc`; the *Firewall > Settings > Advanced* model |
| S13 | Suricata eve over syslog (extends S7):<br>- the IDS model field that enables it;<br>- its program (identity) and facility;<br>- whether it carries the same event types as `eve.json`, `tls` and `http` in particular when their toggles are on;<br>- the message format (one eve JSON object per message);<br>- whether `tls.sni` and `http.hostname` are present without extended logging | `src/opnsense/mvc/app/models/OPNsense/IDS/IDS.xml`; `src/opnsense/service/templates/OPNsense/IDS/suricata.yaml` |
| S14 | How Suricata sees packets on an interface, per mode (IDS pcap, IPS netmap):<br>- the value of `in_iface` (device name, any host-ring suffix);<br>- whether its addresses and ports are the form pf logs on that device;<br>- whether `src_ip` / `src_port` of `tls` and `http` events are the connection's initiator | The eve documentation of the Suricata release 26.7.3 ships; OPNsense's IDS manual and template |

**Gates.**
- If S11 shows that a capture point exports a **different form** of the packet from the one pf logs on the same device, the PS19 rule is not built for that capture point, and the hand-back reports it. No translation is inferred.
- If S14 shows the same mismatch, the same applies to PS27.
- If S13 is negative, PS24 applies.

## D. Scope: new sections

**Insertion point:** after PS16, before "## 5. Scope: amendments inside existing sections".

### Phase C, part 2 (continued): NetFlow volumes joined to logged connections

**PS17. The core kind `flow_record`.**
- **What it is.** A push-only core kind: one record of a flow, as an exporter reported it. The term is RFC 3954's and RFC 7011's *Flow Record*. `docs/data-model.md` *Vocabulary* says explicitly that `flow` (one filter-log record) and `flow_record` are different things.
- **Its contract** is in `contract.go` and in `docs/plugins.md`, identical (A13). It carries:
  - the exporter address and the observation domain;
  - the **Observation Point** (RFC 7011): the interface mapped under PS11 with the raw indexes kept, and the record's direction at it (`ingress`, `egress`, or `not_exported`);
  - IP version and IANA protocol number;
  - source and destination address and port (absent for a protocol without ports);
  - ICMP type and code where S10 establishes their encoding;
  - cumulative TCP flags;
  - octets and packets;
  - start and end instants, at the precision exported;
  - the sampling interval (absent when not declared);
  - the reception instant.
- **Identity.** Unique per exported record and stable if a packet is delivered twice. The Builder states its composition.
- **Storage.** A core hypertable on the start instant, under P4's rule. Its retention is a setting whose default equals the filter-log flow retention. The purge works by chunk, then by row (P4).
- **The kind's provider**, the built-in `netflow` plugin (PS12 amended), declares:
  - a push entry `protocol: netflow`, `kind: flow_record`;
  - a **declarative mapping** from IANA Information Element names to contract fields. This is a new PS3 option for NetFlow entries: a contract field names one IE, or an ordered list whose first present IE wins.
  - No script runs per record.
- **Protocol comparison.** Protocol is compared by IANA number. If `flow` stores only the filter log's `protoname`, the Builder adds the number, or maps the name through the IANA *Assigned Internet Protocol Numbers* keywords held as data with their source cited. It never compares by guess.

**PS18. Capture coverage is read, never inferred from data.**
- **Configured capture.** Through the broker, at a max age, the `netflow` plugin reports a coverage record of the `flow_record` contract: per interface, whether ingress and egress are captured. The endpoints are those S5 and S9 establish.
- **When a direction counts as covered.** An interface-direction is **covered** at instant *t* when all three hold:
  - it is configured as captured;
  - the push source was alive at *t* (PS10);
  - no NetFlow gap (PS7) meets *t*.
- **Configuration unreadable.** If S9 establishes no read endpoint, coverage is `capture_configuration_unknown`. It is never deduced from which records happened to arrive.

**PS19. The join: a flow record to the leg logged at its Observation Point.**

1. **Candidates.**
   - **Same point, same family.** Candidate legs are the `flow` rows logged on the record's observing device, with the same IP version and protocol.
   - **Same tuple, either orientation.** The record's (source, source port, destination, destination port) equals the leg's logged tuple (**forward**), or the leg's tuple swapped (**reverse**). *Forward* and *reverse* are RFC 5103's terms.
   - **Address comparison.** Addresses are compared as canonical address values, never as text.
   - **Protocols without ports** (ICMP, ICMPv6, any record carrying no port) join on the two addresses alone. This weaker key is documented.
2. **Time.** The record joins the candidate with the latest `observed_at` ≤ record start + `netflow_join_tolerance_seconds`.
   - That setting is a positive whole number, set through the settings JSON API (C5).
   - Its default is the Builder's, justified by the resolutions involved: the filter log's seconds, NetFlow's milliseconds and the normaliser's offset. LV-NJ confirms it.
3. **Continuity.** A record belongs to a leg's connection only while that connection's pf state could still exist.
   - The gap between the leg (or its previous joined record in the same direction) and the record's start never exceeds pf's state timeout for that protocol and state. Those timeouts are the values in force when readable through the broker, else pf's defaults as S12 records them.
   - A TCP record whose cumulative flags include SYN joins only a leg logged within the tolerance of its start.
4. **Ambiguity is never resolved by guessing.** Two candidates with the same `observed_at` → the record joins neither.
5. **Every action joins.** Legs with action `pass`, `block`, `reject` and `unknown` all join. The bytes of refused packets that the capture exported are the refused leg's measured figure. If S11 shows that blocked packets are not exported, refused legs carry the direction state `not_exported_when_refused`.
6. **Unmatched records.** A record with no candidate is an **unmatched record**, with one reason from a closed list. The names below are proposals; the Builder fixes them.

   | Reason | The record |
   |---|---|
   | `no_logged_leg` | has no leg on its observing device |
   | `ambiguous` | had two candidates at once (rule 4) |
   | `interface_unmapped` | carries an index PS11 could not map |
   | `beyond_flow_horizon` | started before `retention_purge.purged_before`, or its leg was purged |

7. **Decided from stored rows.**
   - The result does not depend on arrival order. It is re-derived when a record or a leg arrives in its window, and when the pairing changes a leg's outcome. A second derivation writes nothing.
   - A record is decided only once the filter-log ingestion watermark (the in-flight watermark of the refresh contract, rule 4) has passed its start plus the tolerance. Before that it is pending, never unmatched.
8. **NAT needs no rule of its own.** Each leg is matched in its logged form on its own device.
   - Under outbound NAT, the second leg (upstream `out`, translated source) joins the upstream device's records in translated form.
   - The first leg joins the inside device's records untranslated.
   - IPv6, and traffic between two interfaces, match with equal tuples on both devices.
   - This holds only under S11's gate.

**PS20. A connection's measured volume: each direction once, at one Observation Point.**
- **What a connection is.** A first leg with its second leg, or a single leg (`not_paired`, or no pair outcome).
- **One Observation Point per direction.** For each direction, forward and reverse as the first leg logged it, the connection's measured figure is the sum of that direction's joined records at **exactly one** Observation Point:
  - the first leg's device, when it was covered for that direction over the connection's span (PS18);
  - otherwise the second leg's device, when it was covered;
  - otherwise none.

  **The records of one direction at two devices are never summed.**
- **Stored per connection:** per direction, the bytes, packets, record count, device, and which leg it came from (`first_leg` or `second_leg`). The measure identity is `netflow`, stored **beside** the logged figures. `packet_bytes` and every logged figure are unchanged everywhere.
- **Per-direction state.** A closed vocabulary; the names are proposals.

  | State | The direction |
  |---|---|
  | `measured` | ended under PS19's continuity rule, with its bytes recorded |
  | `in_progress` | still within continuity; its figure grows |
  | `awaiting_export` | covered, but no record yet, within S10's active timeout plus the tolerance |
  | `missing` | covered, past that bound, with no record |
  | `not_captured` | no covered device |
  | `capture_configuration_unknown` | PS18 could not read the capture configuration |
  | `export_gap` | a NetFlow gap meets its span at the chosen device |
  | `sampled` | a joined record declares a sampling interval above 1 |
  | `not_exported_when_refused` | the leg was refused and S11 shows refused packets are not exported |

- **Sampling is never extrapolated.** Bytes are stored as exported, and no figure is scaled.
- **A second leg's own records** (both directions at its own device) are that interface's own measured figure. They count under `exit_leg_device` exactly as logged volume does, and in no total.

**PS21. Aggregates carry both measures and never sum them.**
- **New columns.** Every family and period, and the purged part, gain `netflow` columns beside the logged ones:
  - bytes, split allowed / blocked / unknown by the leg's action, with a `CHECK` that the split sums;
  - packets;
  - a count of the connections in each coverage class (both directions measured; partial; none).
- **Counting is unchanged.** Connections count once in totals. Second legs count only under their `exit_leg_device` row and in the rule family.
- **Slot.** A connection's measured volume counts in the slot of its first leg's `observed_at` (open question 14).
  - A join that changes a connection's figure forces that slot, as a changed attribution does (refresh contract, rule 3).
  - Until the slot is recomputed, `freshness` counts it stale.
- **Purge.** The purged part keeps the `netflow` figures of the flows it removes. An hour holding the horizon keeps them.
- **The unmatched family.** A new family over the four periods holds the unmatched records (the name is the Builder's).
  - Key: observing interface, direction at it, protocol, IP version, reason.
  - Figures: bytes, packets, records.
  - It is added to no other family and to no total of any other family.
- **What does not change.** `pair_volume_observation` stays logged-only and says so.
- **P5's equivalence gate** applies to every new column.

**PS22. Widget API: one measure per answer, a stated default, and `lower_bound` restated.**
- **The measure vocabulary gains `netflow`:** "bytes and packets the firewall's NetFlow export reported for the logged connections, each direction counted once at one Observation Point."
- **A new parameter.** Every type serving a `Volume` or a byte `Count` takes `volume_measure`, with values `logged` | `netflow` | `best_available`. Its default is set by open question 13; scope follows the recommendation, `best_available`.
  - **One measure per answer.** An answer never mixes measures. Every figure's `measure` states the one used.
  - **How `best_available` chooses.** It answers `netflow` when the capture coverage (PS18) of every interface carrying a counted connection spans the whole window with no gap. Otherwise it answers `logged`, with one of two named states:
    - `netflow_not_configured`: no active provider of `flow_record`, or its listener is off;
    - `netflow_not_covering_window`.
- **A `netflow` Volume** carries a coverage object: connections per coverage class (PS21). A `logged` Volume gains no field.
- **`lower_bound`.**
  - Always `true` for `logged`.
  - For `netflow`, `true` on every aggregate figure: unlogged connections are in the unmatched figures and in no keyed aggregate, and traffic inside one interface is invisible.
  - `false` only on a single `client_traffic_detail` record whose two directions are `measured`, with no gap meeting its span and no sampling.
  - `docs/widget-api.md`, *Volumes and the observation-point limit*, is amended by name, and so is the `Volume` comment in `envelope.go`.
- **`client_traffic_detail` records,** under `netflow`, carry per direction the bytes, packets, state and the leg it was measured at.
- **The data catalogue.**
  - `custom_chart`'s `traffic` source gains the measures `netflow_bytes` and `netflow_packets`.
  - The catalogue gains the datasets `flow_record` (nature `event`, `loss_detectable` true) and the unmatched family.
  - Both documents are amended by name: these are data contracts, and no aesthetic rule changes.
- **No path sums two measures.** No code path sums `logged`, `netflow`, `sampled` or `interface_counter` with one another.

**PS23. How this relates to the polled volume path.**
- **Separate measures, no replacement.** The polled path is the `insight` plugin's `sampled` and `interface_counter` measures. `flow_record` has no polled provider, so it has no PS9 source segment and no `replaces`. The `netflow` plugin still declares none (PS-AC16).
- **Never substituted.** The four measures are never summed or substituted for one another. A window without NetFlow coverage is never filled from samples or counters.
- **README.** It says whether OPNsense's local collection (`local: 1`, Insight) and an export to `opnview` can run together, as S5 establishes.

### Phase C, part 2 (continued): observed server names

**PS24. The gate.**
- **If S13 shows** that `tls` / `http` eve events cannot be sent to syslog, or that they carry neither the server name nor the host, PS25–PS30 are **not built**: no kind, no method value, no column, no API field, no placeholder. The hand-back cites the survey row.
- **If S13 is positive but S14 fails its gate,** the same applies to PS27 onward.

**PS25. The core kind `server_name_observation`.**
- **The term is `opnview`'s own,** recorded in *Vocabulary* with the reason:
  - TLS's `server_name` extension (RFC 6066, §3) and HTTP's `Host` field (RFC 9110, §7.2) both carry the name the client asked the server for;
  - Suricata names them `tls.sni` and `http.hostname`;
  - no product names the pair.
- **Contract fields:**
  - the observed instant, at sub-second precision;
  - the device, from `in_iface` by exact match under S14's finding, with no fuzzy matching;
  - IP version and protocol;
  - addresses and ports as reported;
  - `name_source`: `tls_server_name` or `http_host`;
  - `server_name`, verbatim;
  - an identity composed from the message content;
  - Suricata's `flow_id`, kept as provenance only, never as identity.
- **Validation.**
  - A `Host` value loses its port, per RFC 9110's `uri-host [ ":" port ]`.
  - An empty value, an IP literal, or a value that is not a syntactically valid DNS name is not a server name. It is counted and not stored.
- **Storage.** A hypertable on the instant. Its retention is a setting whose default equals the flow retention. An observation an attribution names is kept as long as that attribution, as `address_observation_id` is kept today.
- **Domain withholding.** `server_name` is marked carries-domain.

**PS26. The built-in plugin.**
- **A separate plugin.** It is a new built-in plugin, proposed name `suricata_server_names`, separate from `suricata`, so step 6's alert work never touches it.
- **Its push entry:** `protocol: syslog`, the program S13 establishes, `kind: server_name_observation`.
  - A parse function (JSON) emits one record per `tls` event carrying a server name and one per `http` event carrying a host.
  - Every other event type is skipped and counted.
  - **No `replaces`:** `query_alerts` drops these event types, so no polled path exists.
- **Shared decoding.** If the `suricata` alert variant (PS12) subscribes to the same program, the header is decoded once (PS2).
- **Availability.** It reads, through the broker, `/api/ids/service/status` and `/api/ids/settings/get` (both already in the survey): whether Suricata runs, the interfaces it covers (`ids.general.interfaces`), and the eve `tls` / `http` and eve-syslog toggles (S7, S13). Its named states (proposals) are:
  - `listener_not_configured`;
  - `ids_not_installed`;
  - `ids_stopped`;
  - `eve_tls_and_http_disabled`;
  - `eve_syslog_not_enabled`;
  - `no_server_name_received`.
- **Transport.** Open question 17. Scope follows its recommendation: the entry is accepted over TCP only.
- **It never writes `security_event`.**

**PS27. Joining an observation to a connection.**
- **Candidates.** Legs on the observation's device, with the same IP version and protocol, and a forward-equal tuple. A reverse tuple is accepted only if S14 shows Suricata reports the responder first for some event type, and the orientation is recorded.
- **Time.** The leg's `observed_at` must lie in [`observed_at` − tolerance, `observed_at` + `observed_name_max_delay_seconds`].
  - That setting's default is the Builder's, justified by S14, and confirmed by LV-SN.
  - The latest leg wins. A tie is `ambiguous` and joins nothing.
- **Second legs.** An observation joined to a second leg names its **first leg's** connection.
- **Eligibility.** It is today's rule: an outside destination, not to this firewall, not a second leg. An observation of an ineligible connection is kept, counted, and names nothing.
- **Several observations of one connection.** Names are compared normalised: lower-cased, with any trailing dot removed (RFC 4343).
  - If exactly one distinct name remains, the connection is attributed `observed`, with the earliest observation's name verbatim (its identity breaks a tie).
  - If two or more remain, there is no observed attribution. The case is recorded as `observed_names_conflict`, and the inferred methods apply as today.
- **Order and repetition.** Arrival order does not change the result, the same is true of late arrival, and a second derivation writes nothing, as for attribution today.

**PS28. The method `observed`: precedence and conflicts.**
- **The new value.** `domain_attribution.method` gains `observed`.
- **Precedence.** `observed` beats `resolver_cache_answer`, which beats `lookup_timing`. An observation arriving after an inferred attribution replaces it and forces its slot.
- **Evidence, pinned per method by `CHECK`s.**
  - `observed` requires its observation reference and no lookup.
  - The inferred methods require their lookup as today.
  - The rule "an attribution cannot exist without the resolver lookup it came from" becomes "…without the evidence its method names".
  - The meaning of `correlation_delay_seconds` is stated per method. No negative delay is stored without a sign convention written in the schema comment.
- **The inferred outcome is kept, not discarded.** For every observed attribution, the inferred rule is still evaluated and its outcome stored beside it:
  - the inferred name and method, or none;
  - an agreement value from a closed vocabulary (proposals): `agrees`, `disagrees`, `no_inference`.
- **`site_name`** is the server name verbatim. It is never a CNAME target and never invented.
- **Under `no_domains`,** storage is unchanged and every name (observed, inferred-beside and conflicting) is withheld at the API like every site name.
- **The domain family is unchanged.** It still keeps no method, so `site_methods_not_recorded_beyond_flow_horizon` still applies.

**PS29. The API, under the site-name rule.**
- **Method counts.** `MethodCounts` gains `observed`, and the methods sum to the named flows. Method counts gain `observed` in `top_sites`, `sites_by_client` and `connection_tree`.
- **`client_traffic_detail.records[]`.** `method` takes `observed`. Each record gains the agreement field, withheld with `method` under `no_domains`.
- **`attribution_rate_per_client`** gains:
  - the observed count;
  - the agreement counts;
  - an `ObservedCovered` figure beside `CacheCovered`: whether a server-name source was alive and covering the client's interfaces over the window.
- **Degraded states.** When no server-name provider is active or reachable, answers keep today's inferred figures and carry that provider's availability with its named state. A panel is never empty without a reason.
- **Documents amended by name** (data contracts only; "rendered in step 7" stays):
  - `docs/widget-catalogue.md`: Part 1's site-name paragraph, *Client traffic detail*, *Top sites*, *Sites by client* and *Attribution rate per client*;
  - `docs/widget-api.md`: the `no_domains` table.

**PS30. Diagnostics.** `store.ReadAttributionRate` and `-- diagnostic: Attribution rate per client` keep one definition, and both gain the observed and agreement counts.

### PS15 and PS16 additions

**PS15.** Add these budget rows (proposals; the Builder may change them with a measured justification):

| Measure | Limit |
|---|---|
| Flow-record join, at the 1 000 000-row seed | ≥ 10 000 records/s |
| Observation join | ≥ 2 000 observations/s |
| Attribution pass with observations | no more than 10 % slower than the same pass without them |

If the push measures no longer fit the 45 s allowance, the Builder reports the figure. The 180 s and 60 s budgets stand.

**PS16, Vocabulary.**

| Term | Its status |
|---|---|
| `flow_record`; Observation Point | RFC 3954 / RFC 7011 |
| forward, reverse | RFC 5103 |
| measured volume; the `netflow` measure; unmatched record; `server_name_observation`; the `observed` method; inferred agreement | `opnview`'s own, each with its reason |

## E. Scope: amendments inside the push amendment and existing sections

- **PS3.** Add, for a NetFlow entry with `kind`, the optional declarative IE-to-field mapping (PS17).
- **PS9, last bullet.** Replace it with: "A push entry with `kind` whose provider **also delivers that kind through a polled endpoint** must declare `replaces`, or it is refused at load (`push_kind_without_replacement`). A push-only core kind (`flow_record`, `server_name_observation`) declares none."
- **PS12, `netflow` row.**
  - Replace "its own kind and its own hypertable of flow records" with "the core kind `flow_record` (PS17)".
  - "Its rows never enter `flow`" stays.
  - Append to the double-count bullet: "The core's measured volume never sums one direction across two Observation Points (PS20)."
- **PS12, `suricata` row.** Replace "`tls` / `http` events are open question 12" with "`tls` / `http` events are PS24–PS30's, in a separate plugin".
- **PS13.** The data catalogue lists `flow_record`, the unmatched family and `server_name_observation`. The last two carry `loss_detectable`; `server_name` is carries-domain.
- **B5 / X3 byte identity, and PS-AC22.** The comparison admits exactly these differences:
  - the echoed `volume_measure` key in `parameters`;
  - the key `methods.observed`, with value 0 when there is none;
  - the agreement and `ObservedCovered` fields.

  All of them exist only when PS24's gate passes, except `volume_measure`.
- **C6.** Append: "Rows naming volume or site names record the `netflow` and `observed` availability and their named states."

## F. Acceptance criteria

**Insertion point:** after PS-AC27. Every test runs on the PostgreSQL test instance with a fake clock and recorded fixtures through the real decoder and router.

- [ ] **PS-AC28. Firewall facts are verified first.**
  - Rows S10–S14 exist in the survey section, each with a 26.7.3 (or FreeBSD 15.1 / Suricata) source URL or marked `UNVERIFIED:`.
  - A test asserts that the `flow_record` entry and the server-name plugin rest on no `UNVERIFIED:` row.
  - Any gate of §C that fails is listed in the hand-back, with the part not built.
- [ ] **PS-AC29. The `flow_record` kind.**
  - Its contract in code equals `docs/plugins.md` (A13).
  - The `netflow` plugin feeds it through the IE mapping, with zero script calls per record (counter).
  - `push_kind_without_replacement` still refuses a `kind` entry whose provider also polls that kind, and admits a push-only kind (one test each).
  - The table is a hypertable on its start instant (catalogue inspection), or the reason it is not is recorded under P4.
- [ ] **PS-AC30. NAT.** The fixture is one IPv4 connection: logged `in` on an inside device untranslated, and `out` on the upstream device with translated source and port. Records: forward at the inside device's ingress, reverse at the upstream device's ingress (translated destination), and forward at the upstream device's egress.
  - Each record joins the expected leg.
  - The connection's forward figure equals the inside record **alone**, and its reverse figure the upstream reverse record.
  - The second leg's own figure equals its device's two records.
  - The six families count the connection once.
  - Every logged column is unchanged.
- [ ] **PS-AC31. Split records and continuity.**
  - One TCP connection exported as several forward and several reverse records (an active-timeout split, and an idle gap within pf's timeout): every record joins and the sums are exact.
  - A later record on the same tuple carrying SYN beyond the tolerance joins no old leg: `no_logged_leg`, or the new leg when one is logged.
  - A UDP record after a gap longer than the S12 timeout joins nothing.
- [ ] **PS-AC32. Unmatched records.**
  - A record with no leg lands in the unmatched family with reason `no_logged_leg`. It is in no other family and in no logged or `netflow` total.
  - Two legs in the same second give `ambiguous`.
  - An unmapped index gives `interface_unmapped`.
  - A record for a purged leg gives `beyond_flow_horizon`.
- [ ] **PS-AC33. IPv6 and ICMP.**
  - An IPv6 connection across two interfaces joins, with one source writing a non-canonical address text.
  - ICMP echo legs join on their addresses.
  - An ICMP record meeting two same-second ICMP legs is `ambiguous`.
- [ ] **PS-AC34. Arrival order.**
  - The same fixture ingested records-first, legs-first, interleaved, and with the pairing decided after, gives identical joins, connection figures and aggregates.
  - A second derivation writes nothing (counter).
  - No record is declared unmatched before the ingestion watermark passes its start plus the tolerance.
- [ ] **PS-AC35. Counted once, never summed.**
  - At `1h`, `24h`, `7d` and `30d`, every family's logged columns equal the baseline (P-AC9's method).
  - The `netflow` columns equal an independent per-connection computation, one Observation Point per direction.
  - After a purge of the hour, its `netflow` figures are unchanged.
  - A test proves that no `Volume` or `Count` is built from two measures.
- [ ] **PS-AC36. Coverage and states.**
  - With capture configured on the upstream device only, both directions come from the second leg and are recorded as such.
  - With the configuration unreadable, the state is `capture_configuration_unknown`.
  - A sequence gap gives `export_gap`.
  - A record sampled at 1:N gives `sampled`, with bytes not scaled.
- [ ] **PS-AC37. The widget measure.**
  - Each volume type accepts the three `volume_measure` values.
  - With no `flow_record` provider, `best_available` answers byte-identically to `logged` and to the baseline, except for the echoed key, with state `netflow_not_configured`.
  - With full coverage it answers `netflow`. With partial coverage it answers `logged` with `netflow_not_covering_window`.
  - Every figure in one answer carries the same measure.
  - `lower_bound` is false only on a fully measured `client_traffic_detail` record, and true on every aggregate `netflow` figure.
- [ ] **PS-AC38. The other measures are untouched.**
  - The `netflow` plugin declares no `replaces`.
  - The `insight` plugin's `sampled` and `interface_counter` golden answers are unchanged.
  - No code path combines them with `netflow`.
- [ ] **PS-AC39. The server-name gate.** If S13 or S14 is negative, these tests pass:
  - a grep finds no `observed` value in the method `CHECK`;
  - no `server_name_observation` table exists;
  - no API key named by PS29 exists;
  - the hand-back cites the survey row.
- [ ] **PS-AC40. Server-name ingestion,** from recorded eve-JSON syslog fixtures through the real decoder.
  - The fixtures are: `tls` with a server name, `http` with a host, a host with a port, an IP-literal host, `alert`, `flow`, and malformed JSON.
  - Exactly one observation is stored per valid `tls` / `http` name, with the port stripped.
  - The IP literal is refused and counted. Other types are skipped and counted, and malformed JSON is counted under PS8's codes.
  - The plugin's tests live in its `tests/`.
- [ ] **PS-AC41. The observation join.** Fixtures:
  - Suricata on the inside device: the observation joins the first leg.
  - Suricata on the upstream device: the observation joins the second leg, translated, and names the first leg's connection.
  - Both devices: one attribution backed by two observations.
  - Outside the delay: no join. A tie: `ambiguous`.
  - Two distinct names: no `observed`, `observed_names_conflict` recorded, and the inferred method applied.
- [ ] **PS-AC42. Precedence and conflicts.**
  - A `lookup_timing` flow that later receives an observation becomes `observed`, its slot is forced (`freshness` shows it stale until refreshed), and the agreement is recorded.
  - A `resolver_cache_answer` flow with a disagreeing observation gets the observed name, the agreement `disagrees`, and its inferred name stays queryable.
  - Inserts violating the per-method evidence `CHECK`s fail with SQLSTATE class 23.
- [ ] **PS-AC43. The API.**
  - `methods` include `observed` and sum to the named flows.
  - `client_traffic_detail` records carry `observed` and the agreement field.
  - `attribution_rate_per_client` carries the per-method and agreement counts and `ObservedCovered`.
  - Under `no_domains`, every new name-bearing field is withheld and listed in `withheld`, and a string search finds no fixture server name in any answer.
  - With Suricata absent, stopped, with eve `tls` / `http` off, or with the listener off, each answer carries its named state and the inferred figures. None returns `data: null` without a condition.
- [ ] **PS-AC44. Retention.**
  - `flow_record` and `server_name_observation` are purged at their horizons on the fake clock, by chunk then by row.
  - An observation an attribution names survives the purge.
- [ ] **PS-AC45. Budgets.** `pre-deployment` prints the new PS15 rows beside their limits and fails above them. `checks` stays within 60 s.
- [ ] **PS-AC46. Documents.**
  - The *Vocabulary* terms of PS16 are present.
  - `docs/widget-api.md` lists `netflow` in the measure table, with the `lower_bound` rule.
  - The catalogue paragraphs named in PS29 are amended.
  - The README states the export steps.
  - F-AC5 holds for every new code, state and reason.
- [ ] **PS-AC47. The core stays agnostic.**
  - PS-AC2's grep is extended: the join and attribution code names no IE name and no Suricata field (`sni`, `hostname`, `eve`), and reads contract fields only.
  - No `.go` file exists under `plugins/`.

**Amendments to existing criteria.**
- **PS-AC14**, last bullet: append "whose provider also polls that kind".
- **PS-AC19**: replace "The `netflow` plugin's table" with "The `flow_record` table".
- **PS-AC22**: append "except the differences §E admits".

## G. Live validation, Rules, Out of scope, Risks

**Live validation: append to push amendment §8.**

- **LV-NJ, the NetFlow join, over 15 minutes, with a v9 export the user configured.** Reported to the maintainer:
  - the share of records joined, and unmatched by reason, per interface;
  - the share of connections per direction state;
  - the distribution of record start minus leg `observed_at`, which confirms the tolerance;
  - the ratio of `netflow` to logged bytes per interface;
  - one NAT connection traced by hand from its legs through its records to its figures;
  - per device, joined plus unmatched octets against the `traffic/interface` deltas, extending LV-NF.

  Every `ambiguous` and `no_logged_leg` sample must be explained, for example a rule that does not log, or the firewall's own traffic.
- **LV-SN, observed names, over 15 minutes, with eve `tls` / `http` and syslog enabled by the maintainer.** Reported:
  - the share of eligible connections on covered interfaces named `observed`;
  - the **agreement rate of each inferred method against `observed`**, the first measurement of the heuristic's accuracy;
  - every conflict, explained;
  - the distribution of observation minus flow delay;
  - an uncovered interface's flows falling back to inferred names.

  LV-PS0 gains a fourth state: eve `tls` / `http` over syslog on.
- **Real fixtures.** Recorded packets and lines from both windows, rewritten under X1 with `__provenance__`, are added as fixtures before the commit. Until then the fixtures are synthesised from S10 and S13, and say so.
- **The validation passes when** every LV-NJ and LV-SN difference is named, and the maintainer has accepted the agreement figures and the default of open question 13.

**Rules, *Hard rules*: append.**
- "A connection's measured volume takes each direction from exactly one Observation Point."
- "`logged`, `netflow`, `sampled` and `interface_counter` are never summed or substituted."
- "An observed name is joined, never guessed: no fuzzy device or name matching."
- "No `.go` file under `plugins/`."

**Out of scope: delete** "Using pushed Suricata `tls` / `http` records for site names (open question 12)." and "Joining NetFlow volumes to logged connections in the core's tables (open question 11)." **Append:**
- record-time-resolved `netflow` figures in the aggregates (they are served from the `flow_record` dataset only);
- classifying the ends of unmatched records (open question 15);
- `netflow` in `pair_volume_observation`;
- extrapolating sampled records;
- RFC 5103 bidirectional records;
- QUIC server names, JA3 / JA4, certificate fields;
- correlating Suricata alerts with flows (step 6);
- every part of the step-6 alert feature.

**Risks: append.**

48. **Size, and the delay of the audit.**
    - This addendum is a cycle's worth of work in its own right: two core kinds, a join derivation, new columns in 24 aggregate tables and the purged part, a new family, an attribution schema and rule change, and widget contracts.
    - It sits **before the mockup audit C6**, so step 7 is pushed further back again.
    - The Builder reports an unfinished part rather than thinning it. The join is built and passes its own criteria before the names part.
49. **Unverified capture semantics.** S10–S12 are unread. If the capture point does not export the form pf logs on a device, the join for that device cannot be built at all.
50. **Hot-table cost.** The join needs an index on `flow` covering device, key and instant, on the largest table. LV-NJ and PS15 measure the write and read cost.
51. **Record volume.** NetFlow can exceed the filter-log rate by orders of magnitude, in storage and in join throughput.
52. **Start-slot attribution** (open question 14). A long transfer's bytes land in the hour it started.
53. **Port reuse inside pf's timeout under a rule that does not log** can attach a new connection's bytes to an old leg. Continuity bounds it, and LV-NJ measures it.
54. **Server names are partial.** Encrypted Client Hello, QUIC / HTTP/3 and DoH carry no observable name, so `observed` coverage shrinks as those spread.
55. **The agreement figures may show the inferred heuristic is weak.** That is a maintainer decision to come, not this cycle's.
56. **Byte identity exceptions widen B5** by the exact keys §E lists.

## H. ROADMAP.md and README.md

- **`ROADMAP.md`, *Push sources*.** Append: "NetFlow records are joined to the logged connections as a second measure, and Suricata's TLS server names and HTTP hosts, received over syslog, give an `observed` attribution method (Phase C, part 2)."
- **`README.md`, lines 120–125** ("Suricata therefore contributes alerts and nothing else … says per name which of the two methods inferred it.") are rewritten by the Builder in F.2:
  - three methods, with `observed` named and its conditions stated;
  - the eve `tls` / `http` and syslog steps, taken from S13;
  - the `netflow` measure and its coverage.

  If PS24's gate fails, only the NetFlow part is written.

## I. Existing sentences that become obsolete

Append to push amendment §11. Items 7–11 apply only if PS24's gate passes.

1. Push amendment §9, *Out of scope*: the two bullets on open questions 11 and 12 (deleted, §G).
2. Push amendment §12: Q11's "Recommendation: (a) … Scope follows (a)." and Q12's "Recommendation: (a) … Scope follows (a) unless the maintainer chooses (b)." Both are superseded by the Decided block and this addendum.
3. PS12's `netflow` row ("its own kind and its own hypertable"), its `suricata` row ("open question 12"), and PS9's last bullet (all §E).
4. PS-AC14's last bullet, PS-AC19's "`netflow` plugin's table", and PS-AC22 (§F).
5. §3's and X2's "PS-AC1 to PS-AC27" (§B).
6. Push amendment §10: "The sentence 'Site names are inferred from resolver lookups, unconditionally.' stays unless open question 12 is answered (b)." and its proposed `CLAUDE.md` sentence. Both are replaced by §J.
7. `docs/data-model.md`, *What this schema is not*:
   - "Every volume figure is therefore a lower bound" (each figure now states its own bound, PS22);
   - the whole **Site names are inferred.** paragraph, including "so the resolver is still the only source of site names".
8. `docs/data-model.md`, *`domain_attribution`*:
   - "constrained by a `CHECK` to `resolver_cache_answer` or `lookup_timing`";
   - "an attribution cannot exist without the resolver lookup it came from".

   Also the *Vocabulary* row `domain_attribution.method`, and *The aggregate families*: "The figures are **logged bytes** … and never conversation volume" (true of the logged columns only).
9. `docs/widget-api.md`, lines 139–153 ("every byte, packet and connection figure is a lower bound"; the three-row measure table), and `envelope.go`'s "lower_bound is always true".
10. `docs/widget-catalogue.md`:
    - lines 72–77, "Suricata exposes no `dns` event type and its `tls` / `http` events cannot be read back…";
    - line 682, "the resolver cache's answer or lookup timing";
    - line 927, "the two summing to the attributed count".
11. "The sole source of site names" in four places:
    - `docs/plugins.md`, line 1098;
    - `plugins/unbound/plugin.json`, `description`;
    - `internal/store/schema.sql`, line 1255;
    - `README.md`, lines 120–125.
12. `CLAUDE.md`, lines 103–111 (§J; applied by the orchestrator).

**Not obsolete, stated for clarity:**
- "No figure computed from `flow` may be presented as the volume of a conversation." `netflow` figures are not computed from `flow` alone.
- Survey *Data source 2* (b), "no REST endpoint can read those records". It stays true, and gains a pointer to the push section.

## J. `CLAUDE.md`: the proposed diff, for the orchestrator to apply with the maintainer's agreement

This replaces the CLAUDE.md wording proposed in push amendment §10.

**Variant A, applied if PS24's gate passes:**

```diff
-It reads five sources through the OPNsense REST API: filter logs, the Suricata
-`eve.json` event log (optional — IDS alerts only), NetFlow/Insight, DHCP
-leases, and resolver DNS lookups (the sole source of site names).
-
-Suricata was planned as the primary source of site names. The step-1 API
-survey established that it cannot be: OPNsense exposes no `dns` event type,
-and `tls` / `http` events, though they can be written to `eve.json`, cannot be
-read back through the API. See `docs/opnsense-api-survey.md`. Site names are
-inferred from resolver lookups, unconditionally.
+It reads five sources through the OPNsense REST API: filter logs, the Suricata
+`eve.json` event log (optional — IDS alerts only), NetFlow/Insight, DHCP
+leases, and resolver DNS lookups. It can also receive the firewall's syslog and
+NetFlow exports, configured by the user on OPNsense; `opnview` still writes
+nothing to the firewall. NetFlow volumes, when exported, are joined to the
+logged connections as a second measure (`netflow`), stated on every figure and
+never summed with the logged one.
+
+Suricata was planned as the primary source of site names. The step-1 API
+survey established that the API cannot serve them: OPNsense exposes no `dns`
+event type, and `tls` / `http` events, though they can be written to
+`eve.json`, cannot be read back through the API. They can be received over
+syslog once the user enables them and a remote syslog target. See
+`docs/opnsense-api-survey.md`. Site names are therefore **observed** from
+Suricata's TLS server name and HTTP host where Suricata runs and exports those
+events, and **inferred** from resolver lookups everywhere else. The method is
+recorded on every attribution and exposed with the name.
```

**Variant B, applied if PS24's gate fails.** Keep the second paragraph unchanged, and replace only the first:

```diff
-leases, and resolver DNS lookups (the sole source of site names).
+leases, and resolver DNS lookups (the sole source of site names). It can also
+receive the firewall's syslog and NetFlow exports, configured by the user on
+OPNsense; `opnview` still writes nothing to the firewall. NetFlow volumes, when
+exported, are joined to the logged connections as a second measure (`netflow`),
+stated on every figure and never summed with the logged one.
```

In Variant B, append to the second paragraph: "Nor can they be received over syslog (survey, S13)."

## K. Open questions (additions)

13. **The default `volume_measure`.**
    - **(a)** `logged`, with `netflow` opt-in per widget.
    - **(b)** `best_available`: `netflow` when coverage spans the window, else `logged` with a named state.
    - **(c)** Always `netflow`, with partial coverage stated.

    **Recommendation: (b).** It gives the measured volume whenever that is honest, never mixes the two, and answers byte-identically when NetFlow is absent. Scope follows (b).
14. **Which slot a connection's measured bytes count in.**
    - **(a)** The slot of its first leg, the connection start.
    - **(b)** Each record's start slot.

    **Recommendation: (a).** It keeps count-once, the purged part and the refresh contract unchanged. A long transfer lands in its start hour, and time-resolved throughput is served from the `flow_record` dataset. Scope follows (a).
15. **How to key unmatched records.**
    - **(a)** By interface, direction, protocol, IP version and reason only.
    - **(b)** Also classified to an inside client and outside peer, which would give "unlogged traffic per client" but adds the records to the classification walk.

    **Recommendation: (a) this cycle.** Scope follows (a).
16. **The kind name `flow_record`.** It is the RFC term, but a reader may confuse it with `flow`, one filter-log record.
    - **(a)** Keep `flow_record`, with the *Vocabulary* note.
    - **(b)** Another name, which the maintainer would choose.

    **Recommendation: (a).** The project's rule is the products' own vocabulary. Scope follows (a).
17. **Transport for syslog-fed core kinds that replace nothing** (`server_name_observation`).
    - **(a)** TCP only, extending Q9's principle that a core kind needs a transport whose loss is detectable. NetFlow is exempt because its sequence numbers detect loss.
    - **(b)** UDP accepted too, with observed coverage marked `loss_detectable: false`.

    **Recommendation: (a).** Under UDP loss, "covered but unnamed" would be a false claim in `ObservedCovered`. Scope follows (a).

**Everything else** (the join rule, continuity, per-direction choice, unmatched reasons, precedence, conflicts, API exposure, `lower_bound`) follows from the maintainer's answers and the existing count-once and site-name rules. The tolerances are the Builder's, justified, and confirmed live.

---

**Status of this addendum: APPROVED by the maintainer on 12 October 2026.**

---

**Decided (maintainer, 12 October 2026), addendum open questions:**
- Q13: (b), `best_available`.
- Q14: (a), the start slot.
- Q15: (a), unmatched records keyed by interface, direction, protocol, IP version and reason only.
- Q16: (a), the kind is named `flow_record`.
- Q17: (a), the syslog feeding `server_name_observation` is accepted over TCP only.

The CLAUDE.md variant matching the PS24 finding is applied by the orchestrator once that finding is made, with the maintainer's prior agreement.

---

# Amendment: NetFlow join limited to a single Observation Point (replaces PS17–PS23)

Cycle kind: standard

**Status of this amendment: APPROVED, 11 October 2026.**

**Placement.** This amendment follows "Decided (maintainer, 12 October 2026), addendum open questions". It replaces addendum §D's PS17–PS23 completely. It also amends the addendum's §B, §E, §F and §G and push amendment §8 where named below. Anything it does not name stays as approved.

## Context

The push builder stopped before PS17–PS23. The survey's verified findings (`docs/opnsense-api-survey.md`, *Push sources*) contradict two assumptions those sections relied on:

- **S11.** Every `ng_netflow` node exports through one `samplicate` socket under one `source_id`. The observing interface is not exported. No direction element exists.
  - With two or more captured interfaces, a forwarded, untranslated packet arrives as two records that are identical in every field.
  - The per-node sequence numbers interleave. The decoder already flags this as `netflow_sequence_interleaved` and then asserts no gap (`internal/push/protocols/netflow/decoder.go`; `docs/plugins.md`, *A NetFlow exporter whose counters interleave*).
- **S10.** The template carries `INPUT_SNMP` / `OUTPUT_SNMP`, which are IANA `ingressInterface` (IE 10) and `egressInterface` (IE 14). It carries no `flowDirection` (IE 61), no sampling element, and no ICMP type or code. Its time resolution is one second.
- **S5.** There is one node per captured interface. A node is either `conf=11` (ingress and egress, "once per node") or `conf=10` (egress only).
- **S6.** The input index is the receiving interface's kernel index (`rcvif`), because OPNsense never sends `setifindex`. The output index is the route lookup's interface, not necessarily the interface the packet left by.
- **S11.** Ingress is captured before pf, so packets pf then blocks are exported. Egress is captured after pf, so packets blocked on the way out are not exported, and their addresses are as translated.

PS19's "legs on the record's observing device" and PS20's "one Observation Point per direction, first leg's device else second leg's" therefore cannot be evaluated once two interfaces are captured.

What exists today:

- `internal/push` and its NetFlow v9 decoder;
- `internal/collect/pushindexes.go`, the PS11 index mapping through `interfaces_info/1`;
- `internal/plugin/push.go` and `pushkind.go`, the push declaration and the core-kind adapters (`firewall_log` only).

There is no `plugins/netflow/` directory and no `flow_record` kind.

**S13 is negative**, so PS24–PS30 are not built. PS-AC39 (the gate test) applies, and so does CLAUDE.md Variant B, which is already applied.

## A. Decisions recorded (append to decision 15, after 15a)

> - **15b. The NetFlow join requires exactly one captured interface** (maintainer, 11 October 2026).
>   - **One captured interface.** When the NetFlow capture configuration read through the broker names exactly one interface, that interface is the Observation Point (RFC 7011) of every record, and the join runs.
>   - **Any other configuration.** With zero interfaces, two or more, or a configuration that cannot be read, `flow_record` rows are still received and stored, nothing is joined, and every volume answer under `netflow` or `best_available` names the reason.
>   - **Coverage source.** Coverage is read from the configuration, never inferred from the records (PS18).
> - **15c. Suricata alerts over syslog (S7) are deferred to step 6** (maintainer, 11 October 2026). No `suricata` push variant is built in this cycle. S7's finding stays recorded in the survey.

## B. Replacement text for PS17–PS23

### Phase C, part 2 (continued): NetFlow volumes joined to logged connections

**PS17. The core kind `flow_record`.**

- **What it is.** A push-only core kind: one record of a flow as an exporter reported it.
  - The term is the *Flow Record* of RFC 3954 and RFC 7011.
  - `docs/data-model.md` *Vocabulary* says explicitly that `flow` (one filter-log record) and `flow_record` are different things (Q16).
- **The contract.** It is in `contract.go` and in `docs/plugins.md`, and the two are identical (A13). It has two record types.
  - **The flow record**, which carries:
    - the exporter address and the observation domain (v9 `source_id`);
    - the raw `ingressInterface` and `egressInterface` indexes;
    - IP version and IANA protocol number;
    - source and destination address and port (absent for a protocol without ports);
    - cumulative TCP flags;
    - octets and packets;
    - start and end instants, at the precision exported;
    - the reception instant.

    Removed from the approved contract: the ICMP type and code, the sampling interval and the exported direction. S10 shows the only exporter sends none of them, and a field no exporter fills is not built (the maintainer's no-placeholder rule). An exporter that sends them is a later amendment.
  - **The capture configuration record**, which carries:
    - per interface, its OPNsense interface identifier and whether ingress and egress are captured;
    - the active and inactive timeouts;
    - the export version;
    - the read instant.

    Or it carries a single *unreadable* marker with the broker's outcome. It is delivered by a polled read (PS18).
- **Core-written columns.** At reception, the core also stores on each flow record:
  - the Observation Point in force: the mapped interface (identifier and device) when PS18 establishes one, and null otherwise;
  - the record's direction at it (PS19).

  Neither is a contract field the provider fills.
- **Identity.** Each exported record has exactly one identity, and the identity must:
  - store a datagram delivered twice only once;
  - keep two records of one datagram as two rows, even when they are identical in every field;
  - never let a record received after the exporter's sequence restarted (the node recreated) collide with an earlier one;
  - distinguish datagrams of different nodes. With two or more captured interfaces their sequence numbers interleave under one `source_id` and can coincide (S11).

  The Builder states the composition and documents the one case it cannot separate: two datagrams identical octet for octet are one delivery. With two or more captured interfaces, the duplicate records S11 describes are two rows, as exported, and are never deduplicated by content.
- **Storage.** A core hypertable on the start instant, under P4's rule. Retention is a setting whose default equals the filter-log flow retention. The purge works by chunk, then by row (P4).
- **The provider.** The built-in `netflow` plugin is a directory under `plugins/`, holding no `.go` file. It declares:
  - a push entry with `protocol: netflow` and `kind: flow_record`, plus a declarative mapping from IANA Information Element names to contract fields (a new PS3 option: a field names one IE, or an ordered list whose first present IE wins). No script runs per record;
  - a polled endpoint, read through the broker at a max age, which delivers the capture configuration record (PS18).
- **Provider rules.**
  - PS9's double-count rule concerns one record type delivered by both paths. It must not refuse a provider that pushes flow records and polls its capture configuration. The Builder states how the load check tells the two apart.
  - A second exporter implementation is a second plugin directory with its own mapping and its own configuration read. The core join, coverage and states are unchanged by it (PS-AC48).
- **Protocol comparison.** Protocol is compared by IANA number. If `flow` stores only the filter log's `protoname`, the Builder adds the number, or maps the name through the IANA *Assigned Internet Protocol Numbers* keywords held as data with their source cited.

**PS18. The Observation Point is read from the capture configuration, never inferred from data.**

- **What is read.** The `netflow` plugin reads `GET /api/diagnostics/netflow/getconfig` (S9) through the broker.
  - It maps the `OPNsense.Netflow` model's `capture.interfaces` and `capture.egress_only` (S5) onto the contract's per-interface ingress and egress flags.
  - S5's node creation sets the semantics: `conf=11` is ingress and egress, `conf=10` is egress only.
  - The Builder records in the survey's S5 entry, with the `rc.d/netflow` line it rests on, whether an interface listed in `egress_only` must also be listed in `interfaces` or creates a node on its own.
  - The response shape is verified against the 26.7.3 `NetflowController` and the model, and cited in the plugin's header comment.
- **The captured-interface count** is the number of interfaces with at least one captured direction.
- **Configuration in force.**
  - The core keeps the history of capture configurations as intervals bounded by read instants.
  - Between the last read showing one configuration and the first read showing a different one, the configuration is `capture_configuration_unknown`.
  - A record takes the configuration in force at its start instant. A record is never re-joined retroactively when a later configuration differs.
- **When the join runs.** It runs for a record only when the configuration in force names exactly one captured interface X, and PS11 maps X to a kernel index. That interface X is the record's Observation Point. Otherwise the record is stored with a null Observation Point and its interval carries one named state (PS22):

  | Configuration in force | State |
  |---|---|
  | capture names no interface, or the NetFlow export is disabled (`is_enabled`, S9) | `netflow_not_configured` (PS22's existing state, extended to this case) |
  | two or more interfaces captured | `observation_point_not_exported`, with `captured_interfaces: N` |
  | the read failed, or lies between two differing reads | `capture_configuration_unknown` |
  | one interface, but PS11 cannot map it to an index | `interface_index_unmapped` (PS11's existing name) |

- **The configuration contradicting the stream.** If the decoder raises `netflow_sequence_interleaved` for the exporter and domain while the configuration names one interface, the two disagree.
  - The join is suspended from that point until a later configuration read, and the interval's state is `capture_configuration_unknown`.
  - This is a refusal to trust a configuration the stream contradicts. It never deduces coverage from records.
- **Loss detectability follows the configuration.**
  - With one captured interface, the node's sequence is the domain's sequence. `netflow_sequence_gap` applies, and `flow_record` is `loss_detectable: true` over those intervals.
  - With two or more, no sequence gap is asserted over the interval, whether or not the decoder has already seen a backward jump, and `loss_detectable` is false for it.
  - The data catalogue reports `loss_detectable` per interval for `flow_record`.
- **When a direction counts as covered.** An interface-direction of X is covered at instant *t* when all three hold:
  - it is captured in the configuration in force;
  - the push source was alive at *t* (PS10);
  - no NetFlow gap (PS7) meets *t*.

**PS19. The join: a record to the leg logged on its Observation Point.**

1. **Direction at X.** It comes from the configuration and the record's `ingressInterface`, never from `egressInterface`. `egressInterface` is the route lookup's interface (S6), which policy routing makes differ from the interface the packet actually left by.
   - If X captures egress only, the record is `egress`.
   - Otherwise, if `ingressInterface` equals X's kernel index, the record is `ingress`. By S6 the index is the receiving interface. By S5 a node accounts a packet once, so a packet both received and sent on X is accounted at its ingress.
   - Otherwise the record is `egress`.
   - **Verification before building.** The Builder confirms, at the S6 source, the value `ingressInterface` takes for a packet with no receiving interface (one the firewall originated). The Builder also confirms that `conf=11`'s "once per node" means the packet is accounted at its first pass. Both go into S6 and S5 as sub-facts. If either stays `UNVERIFIED:`, PS-AC28 lists it, and the join is not built until it is verified.
2. **Candidates.**
   - **Same point, same family.** Candidates are the `flow` rows logged on X's device, with the same IP version and protocol.
   - **Same tuple, either orientation.** The record's (source, source port, destination, destination port) equals the leg's logged tuple (**forward**) or its swap (**reverse**), RFC 5103's terms.
   - **Consistent direction.** The record's direction at X, its orientation and the leg's pf `dir` agree:

     | Record at X | Orientation | Leg on X must be logged |
     |---|---|---|
     | `ingress` | forward | `in` |
     | `ingress` | reverse | `out` |
     | `egress` | forward | `out` |
     | `egress` | reverse | `in` |

     A leg logged `in` on X is a packet received on X: its forward packets arrive on X and its replies leave by X. A leg logged `out` is the opposite.
   - **Addresses** are compared as canonical address values, never as text.
   - **Protocols without ports.** ICMP, ICMPv6, and any record with ports 0 (S10: ICMP and non-first fragments) join on the two addresses alone. This weaker key is documented.
3. **Time.** The record joins the candidate with the latest `observed_at` ≤ record start + `netflow_join_tolerance_seconds`.
   - The setting is a positive whole number, set through the settings JSON API (C5).
   - Its default is the Builder's, justified by the filter log's whole seconds, NetFlow's whole seconds (S10) and the normaliser's offset. LV-NJ confirms it.
4. **Continuity.** A record belongs to a leg's connection only while that connection's pf state could still exist.
   - The gap from the leg, or from its previous joined record in the same direction, to the record's start never exceeds pf's default state timeout for that protocol and state, as S12 records it. S12 shows the values in force are not readable.
   - A TCP record whose cumulative flags include SYN joins only a leg logged within the tolerance of its start.
5. **Ambiguity is never resolved by guessing.** Two candidates with the same `observed_at` mean the record joins neither.
6. **Every action joins.** `pass`, `block`, `reject` and `unknown` legs all join. By S11:
   - for a refused `in` leg, the forward packets were captured at ingress before pf. They are its measured figure;
   - for a refused `out` leg, the forward packets were blocked before the egress capture, so its forward direction is `not_exported_when_refused`.
7. **Unmatched records.** These apply only to records with an Observation Point. The names are proposals; the Builder fixes them.

   | Reason | The record |
   |---|---|
   | `no_logged_leg` | has no consistent candidate on X |
   | `ambiguous` | had two candidates at once (rule 5) |
   | `beyond_flow_horizon` | started before `retention_purge.purged_before`, or its leg was purged |

   PS11's `interface_unmapped` is no longer an unmatched reason: an unmapped X stops the join for the interval (PS18). Records with no Observation Point are not unmatched records. They are served only through the `flow_record` dataset (PS22).
8. **Decided from stored rows.** Unchanged from the approved text:
   - the result does not depend on arrival order, and a second derivation writes nothing;
   - before the filter-log ingestion watermark passes the record's start plus the tolerance, the record is pending, never unmatched.
9. **NAT needs no rule of its own.** Each leg is matched in its logged form on X.
   - The join never infers a translation.
   - Egress records carry the translated form (S11), and ingress records carry the form before pf.
   - Whether pflog writes a translated packet in its translated form is S11's `UNVERIFIED:` sub-fact (open question 1). If it does not, the affected records are `no_logged_leg`, never attached to another leg, and LV-NJ measures the share.

**PS20. A connection's measured volume: each direction once, at the single Observation Point.**

- **What a connection is.** A first leg with its second leg, or a single leg (`not_paired`, or no pair outcome).
- **The figure.** For each direction (forward and reverse, as the first leg logged it), the measured figure is the sum of that direction's records joined to a leg of the connection that was logged on X.
  - Only one Observation Point exists, and a node accounts each packet once (S5), so no direction can be counted at two points or twice at one.
  - The first-leg-device-else-second-leg-device choice of the approved PS20 is removed: it existed only to choose between Observation Points.
- **Which leg.** When X carries the first leg, the figures come from it. When X carries the second leg (X is the upstream interface of a NAT connection, for example), they come from the second leg. When X carries neither, the direction is `not_captured`.
- **Stored per connection,** per direction:
  - bytes, packets and record count;
  - the Observation Point's interface, because the captured interface can change over the retention period;
  - the leg the figure was measured at (`first_leg` or `second_leg`).

  The measure identity is `netflow`, stored beside the logged figures. `packet_bytes` and every logged figure are unchanged everywhere.
- **Per-direction state.** A closed vocabulary; the names are proposals.

  | State | The direction |
  |---|---|
  | `measured` | ended under PS19's continuity rule, with its bytes recorded |
  | `in_progress` | still within continuity; its figure grows |
  | `awaiting_export` | covered, with no record yet, within the active timeout in force (PS18's configuration record) plus the tolerance |
  | `missing` | covered, past that bound, with no record |
  | `not_captured` | no leg on X, or X does not capture that direction (egress only) |
  | `export_gap` | a NetFlow gap meets its span |
  | `not_exported_when_refused` | rule 6 |
  | `observation_point_not_exported`, `capture_configuration_unknown`, `netflow_not_configured`, `interface_index_unmapped` | PS18's interval state at the connection's start |

  `sampled` is removed, because S10 shows no sampling is exported.
- **Exit-leg figures.** When the figure was measured at the second leg, the same figure also counts under that leg's `exit_leg_device` row, exactly as a second leg's logged volume does, and in no total.

**PS21. Aggregates carry both measures and never sum them.** The approved text stands, with these changes:

- The coverage classes counted per row are:
  - both directions measured;
  - partial;
  - none;
  - not joinable: no single Observation Point at the connection's start.
- The unmatched family's key is: Observation Point interface, direction at it, protocol, IP version, reason (Q15 (a)). It holds only records that had an Observation Point.

Everything else in PS21 stands: count-once, the start slot (Q14 (a)), the forced slot on a changed figure, the purge rule, `pair_volume_observation` staying logged-only, and P5's gate.

**PS22. Widget API: one measure per answer, a stated default, and `lower_bound` restated.** The approved text stands, with these changes:

- **The `netflow` measure's definition** becomes: "bytes and packets the firewall's NetFlow export reported for the logged connections, each direction counted once, at the single Observation Point the capture configuration establishes."
- **`best_available`** answers `netflow` only when every connection the answer counts has every direction covered at X over the whole window, under a single-Observation-Point configuration throughout. Otherwise it answers `logged`, byte-identical to `logged` apart from the echoed key, with one state:
  - `netflow_not_configured` (extended by PS18);
  - `observation_point_not_exported`, with `captured_interfaces`;
  - `capture_configuration_unknown`;
  - `interface_index_unmapped`;
  - `netflow_not_covering_window`, when the window mixes intervals or holds connections without a leg on X. Its detail lists the interval states met.

  With one Observation Point, an answer whose scope includes connections without a leg on X (traffic between two interfaces that are not captured) falls back to `logged`. This is the intended consequence, stated in `docs/widget-api.md`.
- **An explicit `volume_measure=netflow`** that cannot be answered carries no figure and carries the same named state. It never carries logged figures labelled `netflow` (PS23).
- **`lower_bound`.**
  - Always `true` for `logged` and for every aggregate `netflow` figure.
  - `false` only on a single `client_traffic_detail` record whose two directions are `measured` with no gap meeting its span.
- **The `flow_record` dataset.**
  - The data catalogue serves it in every configuration, with its per-interval `loss_detectable` and Observation Point.
  - Its label states that, with two or more captured interfaces, a forwarded packet is exported once per interface as identical records whose interface is not known, so a sum counts such traffic once per captured interface.
  - No built-in widget serves such a sum.
- **Measure rules.** Unchanged: one measure per answer, the `custom_chart` measures `netflow_bytes` and `netflow_packets`, and no path summing two measures.

**PS23. How this relates to the polled volume path.** The approved text stands.

- The four measures are never summed or substituted.
- `flow_record` has no polled provider of records, no PS9 source segment and no `replaces`.
- The README states two things:
  - local collection (Insight) and an export to `opnview` run together (S5);
  - **the capture configuration is shared between them**, so restricting capture to one interface for the join also narrows what Insight collects on the firewall. This is the user's trade-off, described rather than advised.

## C. Amendments elsewhere

- **Addendum §B and X2.** Replace "PS-AC1 to PS-AC47" with "PS-AC1 to PS-AC52", both in the C6 start condition and in X2.
- **Addendum §C, gates.** Replace the first gate with: "PS19 rule 9 and open question 1 govern the translated form. No translation is inferred."
- **PS7, the `loss_detectable` bullet.** Append: "For `flow_record`, it is carried per interval and follows the capture configuration (PS18)."
- **PS12, `netflow` row.** Replace the double-count bullet with: "With two or more captured interfaces, a forwarded packet is exported once per captured interface as records identical in every field, and the interface is not exported (S11). A sum across records then counts it once per interface, and no row can say which interface saw it. With one captured interface, the row records it as its Observation Point. The core's measured volume is computed only then (PS18, PS20)."
- **PS12, `suricata` row.** Replace its push-variant cell and its coexistence cell with: "Deferred to step 6 (decision 15c). S7 shows alerts can be sent over syslog as eve JSON, program `suricata`. Nothing is built in this cycle."
- **PS13.** The data catalogue lists `flow_record` with its per-interval `loss_detectable` and the unmatched family. `server_name_observation` is not listed (S13).
- **Addendum §E, C6.** Replace "Rows naming volume or site names record the `netflow` and `observed` availability and their named states" with: "Rows naming volume record the `netflow` availability and its named states, including `observation_point_not_exported` and the single-Observation-Point condition. Rows naming site names record S13's negative finding."
- **Addendum §E, B5 / X3.** The admitted differences are reduced to the echoed `volume_measure` key, because PS24's gate failed.

## D. Acceptance criteria

Every test runs on the PostgreSQL test instance with a fake clock and small recorded fixtures through the real decoder and router.

**Replaced or amended:**

- [ ] **PS-AC28.** It stands. It also lists the S5 and S6 sub-facts of PS18 and PS19 rule 1, each with its 26.7.3 or `stable/26.7` URL, or as `UNVERIFIED:` with the part not built. It lists S11's pflog-form sub-fact under open question 1's answer.
- [ ] **PS-AC29. The `flow_record` kind.**
  - The contract in code equals `docs/plugins.md` (A13), and carries no ICMP type or code, sampling or exported-direction field.
  - `plugins/netflow/` feeds it through the IE mapping with zero script calls per record (counter).
  - The plugin's polled capture-configuration endpoint loads without `push_kind_without_replacement`. A provider that delivers the same record type over both paths without `replaces` is still refused (one test each).
  - The table is a hypertable on its start instant (catalogue inspection).
- [ ] **PS-AC30. NAT, two single-interface fixtures.** One IPv4 connection: logged `in` on an inside device untranslated, and `out` on the upstream device with translated source and port.
  - **(a) Capture on the inside device only.** The ingress forward record and the egress reverse record join the first leg. Both directions are `measured` at `first_leg`.
  - **(b) Capture on the upstream device only.** The egress forward record (translated source) and the ingress reverse record (translated destination) join the second leg. Both directions are `measured` at `second_leg`, and the same figure appears under `exit_leg_device`.
  - **In both,** the six families count the connection once, and every logged column is unchanged.
- [ ] **PS-AC31.** It stands, with "the S12 timeout" read as "S12's pf default".
- [ ] **PS-AC32. Unmatched records.**
  - `no_logged_leg`, `ambiguous` and `beyond_flow_horizon` each have a fixture.
  - Such a record is in the unmatched family and in no other family or total.
  - A record under a two-interface configuration is in no family at all.
- [ ] **PS-AC33.** It stands, with one change: the IPv6 connection crosses two interfaces, one of which is captured.
- [ ] **PS-AC35.** It stands, with "one Observation Point per direction" read as "at the single Observation Point".
- [ ] **PS-AC36. Coverage and states.**
  - With X egress-only, the forward direction of an `in` leg is `not_captured` and its reverse is `measured`.
  - An unreadable configuration gives `capture_configuration_unknown`.
  - A sequence gap gives `export_gap`.
  - A refused `out` leg's forward direction gives `not_exported_when_refused`, and a refused `in` leg's forward direction is `measured`.
  - A configuration change between two reads gives `capture_configuration_unknown` over the interval between them, and records before it keep their earlier Observation Point.
- [ ] **PS-AC37. The widget measure.** It stands, plus:
  - under a two-interface configuration, `best_available` answers byte-identically to `logged`, apart from the echoed key, with `observation_point_not_exported` and `captured_interfaces: 2`;
  - explicit `netflow` under the same configuration carries no figure and the same state.
- [ ] **PS-AC47.** It stands, and its grep extends to the core join, coverage and state code. That code names no OPNsense endpoint, model field or option (`getconfig`, `capture.interfaces`, `egress_only`, `conf`) and no IE name. It reads contract fields only.
- [ ] **PS-AC19.** Replace "The `flow_record` table records the observing interface on every row" with: "The `flow_record` table records the raw indexes on every row, and the Observation Point exactly on rows received under a single-interface configuration. The hand-back lists the `suricata` push variant as deferred to step 6 (decision 15c), not as ruled out."

**New:**

- [ ] **PS-AC48. Modularity: a second exporter implementation is a directory.**
  - A test-only second provider of `flow_record` lives under test data as a plugin directory: a manifest, its IE mapping, and a capture-configuration read against the fake firewall.
  - It loads, feeds `flow_record`, and its records join and produce the same connection figures as the built-in's on the same fixture.
  - No Go file, no file under `plugins/` and no core registry is edited for it. The test asserts the loader found it from the directory alone.
  - This is CLAUDE.md's "Modular at every layer" check.
- [ ] **PS-AC49. More or fewer than one captured interface: stored, never joined.**
  - Under configurations naming zero, two and three interfaces, every delivered record is stored with a null Observation Point.
  - S11's identical-record pair is stored as two rows.
  - No join, connection figure, unmatched row or aggregate `netflow` column changes (counters and table comparison).
  - The interval carries `netflow_not_configured` or `observation_point_not_exported` with the right count.
  - No `netflow_sequence_gap` is recorded over a two-interface interval, even for a forward sequence jump, and `loss_detectable` is false for it.
- [ ] **PS-AC50. Direction at X.**
  - A table-driven test derives `ingress` and `egress` from the configuration and `ingressInterface` for every PS19 rule-1 case, including egress-only, and a record whose `egressInterface` differs from the interface of a policy-routed leg.
  - It shows that `egressInterface` never changes the result.
  - A record whose derived direction contradicts the only candidate's `dir` and orientation joins nothing.
- [ ] **PS-AC51. Identity.**
  - A datagram delivered twice stores its records once.
  - Two identical records within one datagram store two rows.
  - Two datagrams from two nodes with the same sequence number and export time store all their records.
  - A sequence restart stores new rows.
  - The configuration-contradiction rule (PS18) suspends the join on an interleaving fixture under a one-interface configuration.
- [ ] **PS-AC52. The OPNsense source is verified, and an unavailable source is reported.**
  - The `netflow` plugin's header cites the 26.7.3 controller and model for `getconfig` and `is_enabled`, with the response fields it reads.
  - A 404, a 403 or an unparseable response gives `capture_configuration_unknown`, with the broker outcome in the availability. Nothing is guessed from records.
  - No mutating `netflow` action is called (BR-AC15).
- [ ] **PS-AC45, budget (amended).**
  - The join throughput at the 1 000 000-row seed runs in `pre-deployment` only.
  - Everyday tests use fixtures of at most a few hundred records.
  - `docker compose run --rm checks` stays within its 60 s `go test` budget, and the hand-back prints the wall time the new tests add.

## E. Live validation (amends push amendment §8 and addendum §G)

- **LV-NF.** Replace its first bullet with this text. The per-interface comparison of NetFlow octets against `traffic/interface` deltas runs with one captured interface. With two or more, only the exporter's total is reported, beside the sum of the captured interfaces' deltas, and S11's duplicate records are named as the expected difference.
- **LV-NJ.**
  - It runs over two windows of 15 minutes, each with one captured interface the maintainer configures: first an inside interface, then the upstream interface.
  - A short third window with two captured interfaces confirms `observation_point_not_exported` and the absence of joins and false sequence gaps.
  - Its bullets stand, with "per interface" read as "for the captured interface".
  - The NAT trace is taken from the upstream window. It reports the share of `no_logged_leg` among translated records, which answers S11's pflog-form sub-fact live.
  - The README's Insight trade-off (PS23) is shown to the maintainer before the reconfiguration.
- **LV-SY.** Delete "`suricata`" from *The other built-ins* (decision 15c).
- **LV-SN.** Not run (S13).

## F. Out of scope (append)

- A join under two or more captured interfaces, and any inference of a record's interface or direction from its content, timing or sequence.
- Re-joining stored records when the capture configuration changes.
- The `suricata` push variant (step 6, decision 15c).
- Contract fields no exporter sends: ICMP type and code, sampling, and exported direction.

## G. Risks (replace 44 and 49; append 57–60)

44. **Double counting in the raw dataset.** With two or more captured interfaces, identical records are stored as exported. Only the dataset's label and `docs/plugins.md` stand between a third-party author and a doubled sum. The core never sums them.
49. **Unverified capture semantics.** These are S11's pflog form for translated packets, and the S5 and S6 sub-facts of PS19 rule 1. Their failure lowers the joined share; it never causes a wrong attachment (PS19 rule 9). LV-NJ measures it.
57. **Coverage is narrow by design.** One captured interface means only connections with a leg on it get a `netflow` figure, and `best_available` falls back to `logged` for wider answers. Most installations capture several interfaces, and on them the join is never available.
58. **Shared configuration.** Restricting capture to one interface also narrows OPNsense's own Insight collection (S5). This is the user's trade-off, stated in the README.
59. **pf timeouts not readable (S12).** Continuity uses pf's defaults. A rule override or `set optimization conservative` (UDP up to 900 s) can cut a long, idle UDP connection's later records to `no_logged_leg`. LV-NJ measures it.
60. **Configuration read lag.** Between two reads, a change leaves an interval of `capture_configuration_unknown`, so its length is the max age of the read.

## H. Sentences that become obsolete (append to addendum §I)

1. Addendum PS17–PS23 as approved, replaced whole by §B. In particular:
   - PS19 rule 1's "legs logged on the record's observing device";
   - PS20's "One Observation Point per direction … first leg's device … otherwise the second leg's device";
   - the states `sampled` and the unmatched reason `interface_unmapped`;
   - the contract's ICMP type and code, sampling interval and exported direction.
2. PS-AC30, PS-AC32 and PS-AC36 as approved (§D).
3. `docs/plugins.md`, *A NetFlow subscriber*: "A connection crossing two captured interfaces is exported once per interface". It should say "as identical records whose interface is not exported (S11)".
4. Addendum §E's C6 sentence naming `observed` (§C).

## Open questions

1. **Building on S11's unverified sub-fact (which form pflog writes for a translated packet).** The spec's rule is that nothing is built on an `UNVERIFIED:` item, and PS-AC28's test enforces it.
   - **(a)** Build the join anyway. The sub-fact affects only how many translated records join, never which leg they join (PS19 rule 9), and LV-NJ's upstream window measures it. PS-AC28's test exempts this one sub-fact by name.
   - **(b)** Build the join, but refuse it while X has outbound NAT or port forwards. This needs a NAT-configuration read that no survey row covers yet.
   - **(c)** Do not build the join until the sub-fact is read from source, which may not be possible (the survey found no statement).

   **Recommendation: (a).**

**Decided (maintainer, 11 October 2026):** open question 1: (a), the join is built; PS-AC28's test exempts S11's pflog-form sub-fact by name, and LV-NJ's upstream window measures it.

**Decided (maintainer, 11 October 2026), after the NetFlow join build:**
- PS22 state placement: (b). A `best_available` answer that falls back to `logged` is byte-identical to `logged` apart from the echoed `volume_measure` key **and** the named state in `states`; both are admitted differences (B5 / X3 amended accordingly).
- The broker data race (`internal/broker/broker.go` `fromMirror` reading `datum.AttemptedAt` against `mirror/write.go` `Store`) is fixed in this cycle.
- The join-throughput row at the 1 000 000-row seed (PS-AC45, PS15) is not added in this cycle.
- The builder's choices are kept: the reverse direction of a refused connection ends `missing`; a leg stored after the read-through has passed a record's start plus the tolerance does not reopen that record's decision; TCP continuity before any acknowledgement uses pf's `tcp.first` (120 s).

**Decided (maintainer, 11 October 2026), after the PS22 build:**
- A state's detail (`captured_interfaces: N` for `observation_point_not_exported`, the interval states met for `netflow_not_covering_window`) goes in a new envelope member `state_details`, keyed by state name, present only when such a state is present. `states` stays a list of strings. It is a third admitted difference under B5 / X3, only when the state is present.
- The builder's interpretations are kept: `aggregate_freshness` is not a volume type; an explicit `netflow` with partial coverage answers the netflow figures with `netflow_not_covering_window`; `netflow_not_served_by_type` is `opnview`'s own term, documented. "Covered" is judged exactly once the finer coverage classes below exist.
- Built in this cycle:
  - **Shared fragment.** The per-leg counting over `connection_measured_volume` is one SQL view or function in `schema.sql`, used by the aggregate refresh and every `_netflow` twin.
  - **Twin validation at load.** A widget whose `<name>_netflow` twin binds different parameters than `<name>` is refused at load with a named code.
  - **Finer coverage classes.** Aggregates store connection counts per direction state, so `best_available` judges "covered" exactly as PS22 states (`in_progress`, `awaiting_export`, `missing` covered; `not_captured` not).
  - **Coverage per action.** The allowed / blocked / unknown netflow parts carry their own packets and coverage counts, so the map widgets serve a coverage object.

**Decided (maintainer, 11 October 2026), after the C6 audit:** the following gaps are closed before step 7.
- **Rows 18, E7.** `owner_activity` serves, per person card, the current rate and the interface list, inside its own plugin folder (from `host.rates.byClient()`), including the machines folded past `clients_per_card`.
- **Row E8.** `traffic_composition` serves the members folded into `other`, inside its own plugin folder.
- **Row 24.** The per-peer current rate, from the stored `traffic/top` per-pair readings, is exposed by the core once (host function or core dataset directory), so any widget plugin can serve it; the destination widgets serve it.
- **Row 4.** A provider plugin reading the pf state table (`query_states`, read-only, per the survey) serves the current rate by protocol and port. It is built now and **active on installation** (maintainer, 11 October 2026). LV-C0 measures its cost afterwards; if it exceeds 2 points (C4), the maintainer decides whether to turn it off.
- The *Coverage of the mockup* push-class table moves to `docs/plugins.md` *Push sources* in Phase F.

**Decided (maintainer, 11 October 2026), after the coverage refinements:** `export_gap` and `not_exported_when_refused` are not covered. No migration, backfill or old-schema compatibility work is done: the project is pre-POC and the database is rebuilt before deployment.

**Decided (maintainer, 11 October 2026), after the gap build:**
- The push "source segment" is renamed **`source_period`** (table `provider_source_period`, and every identifier, comment, doc and state name derived from it). It is `opnview`'s own term — no product names the span during which one delivery path (pull or push) delivers a provider's records — recorded with that reason in `docs/data-model.md` *Vocabulary*. "Period" reads "from one instant to another".
- The literal test address in `internal/plugin/testpush.go` moves to a fixture, so `internal/opnsense`'s source policy passes.
- `internal/collect/scheduler_test.go` derives the plugin loops from the loaded built-ins; the hand list keeps the core loops only.
- A new host function `host.datasets.latest(name, filter)` lets a widget read another plugin's dataset by its catalogue name, without SQL. `traffic_composition` uses it to fill `current_rates.by_member` for the protocol and service-port groupings from `pf_states.rate` when that dataset is present, and keeps the named absence otherwise.
- The `pf_states` mirror reduction is decided after LV-C.

**Decided (maintainer, 15 October 2026), after the dataset-reads build:**
- The rate of a protocol or service-port member uses the **same shape as the client and interface chips** (`in_*` / `out_*`, `out_measured`, `cadence_seconds`), not a separate forward/reverse shape. The mapping from pf's state directions to that shape is stated in `docs/plugins.md` and `docs/widget-api.md`; a field that cannot be filled is null with `out_measured` false, and the docs say so.
- `host.datasets.latest`'s absence state is renamed `unknown_dataset`, aligned with the data catalogue API's refusal code.
- The plugin JS test world keeps the core catalogue only (no built-in datasets).

**Decided (maintainer, 16 October 2026), after the Phase D build:** all five of the builder's proposals are accepted and built in Phase E.
- **Room under the 15-file rule.** The three directories at exactly 15 files (`internal/plugin`, `internal/web`, `internal/widget/hostareas/readers`) are brought below it: the plugin test harness moves to `plugin/harness`, the message-key files of `internal/web` move to their own package, and `hostareas/readers` is split in two.
- **Import aliases removed.** The aliases created by name shadowing (`requestpkg`, `contractpkg`, `paramspkg`, `envelopepkg`, `datasetspkg`, `attributionpkg`, `readspkg`, `pluginpkg`, `listingpkg`) go away by renaming the shadowing local variables.
- **`plugin/runtime` is renamed** so that it no longer collides with the standard library's `runtime` (for example `scripthost`).
- **Forced exports narrowed.** Each part's tests move into the part's own directory, so that the names exported only for tests left in the composing package become unexported again; the shared test fixtures (notably the store's `testNetwork`) are split as needed.
- **The move tooling is kept in the repository under `ci/`.**

---

**This amendment is part of the spec and overrides any earlier text it replaces; section 7 lists the earlier sentences it makes obsolete.**

# Amendment: Phase E, performance and the two budgets (maintainer decisions of 12 and 16 October 2026)

Status: APPROVED

Cycle kind: standard

**Placement.** This amendment **replaces the whole of "### Phase E: the pre-deployment budget"** (lines 620–631) and **criterion E1** (line 843). X1–X4 stay as approved. The cycle kind stays `standard`: Go, SQL, shell, compose and documents only, no screen changes.

**Modularity.** Adding a plugin is adding a directory and touching nothing else (CLAUDE.md, *The user interface*; ROADMAP.md, *Rules that apply to every step*). Every mechanism of this phase works from what it discovers, and none keeps a list a new plugin would have to edit: the `-race` selection, the geolocation batching, the plan sweep, the widget budgets and the plugin-table classification. E29 tests this.

## 1. Context: what the repository holds today (planner's read-only findings)

**The everyday suite.**
- `TEST_BUDGET_SECONDS=60` is set at `ci/checks.sh:30`, and the timed command is `go test -count=1 ./...` (line 65). The suite measured 95 s on 16 CPUs.
- Each database test clones a template:
  - `internal/store/testdatabase.go:180-206` runs `CREATE DATABASE … TEMPLATE … STRATEGY FILE_COPY` (line 188);
  - when the template is busy (55006) it retries 20 times with no backoff (193–205);
  - the database is dropped `WITH (FORCE)`, together with its role (106–121).
- Every `Open` re-applies the whole schema, even on a clone that already holds it:
  - `store.go:156` calls `applySchema` (`schema.go:21-53`), which runs the 4,528 lines of `schema.sql`, including the `DO` blocks that alter 24 aggregate tables (`schema.sql:3138-3181`);
  - every test helper goes through `Open`: `storetest.Open`, `collect/fake_test.go:105-117`, `webtest/harness.go:251` and `store/store_test.go:15-28`.
- Every `Open` also creates or alters a cluster-wide login role and re-grants it (`sqldb/readers.go:56-130`).
- `waitForDatabase` (`store.go:171-195`) retries a failed ping every second for up to 60 s, and logs nothing in tests:
  - `db-test` sets `max_connections=500` (`docker-compose.yml:119-120`);
  - the main pool is unbounded (`sqldb/driver.go:54-61`) and the read pool allows 8 (`readers.go:38`).
- The slow packages already call `t.Parallel()`, and `collect` has no `time.Sleep`. The per-package figures include contention.
- `internal/opnsense`'s source-policy tests walk every Go file of `cmd/` and `internal/` serially (`source_policy_test.go:360-406`).
- `internal/store/calendar_test.go:1` is tagged `predeployment`, which contradicts `ROADMAP.md:210` ("No test is moved today").

**Pre-deployment.**
- `PRE_DEPLOYMENT_BUDGET_SECONDS=180` exists already (`ci/pre-deployment.sh:57, 103-111`).
- `-race` still runs over `./...` (line 74).
- The three fuzz targets run one after another, 10 s each (85–93).
- `ci/budget-self-test.sh:27` re-runs the whole of `ci/checks.sh` with a probe that sleeps 61 s.
- The 1M-row seed is built once per run (`sql/schema-checks.sh:625`). It was not ready about 310 s into the scale section in the 11 October logs.

**Widget budgets at 100k/30d** (11 October run). Five types exceed 500 ms:

| Widget | 100k/30d | 1M/30d |
|---|---|---|
| `destination_countries` | 1450.9 ms | 980.8 ms |
| `destination_operators` | 1101.6 ms | 993.0 ms |
| `passed_traffic_world_map` | 1022.7 ms | 1257.5 ms |
| `traffic_sankey` | 991.4 ms | 1165.4 ms |
| `blocked_traffic_world_map` | 668.1 ms | 666.2 ms |

All five stay within 2 s at 1M/30d. The cost tracks the number of distinct peers, not the number of flows.

Hypothesis, not yet profiled: `host.services.geolocation` (`request/installation.go:192-228`) runs one statement per peer address, an N+1 read.

**NetFlow.**
- `sql/seed.sql` fills none of these tables: `flow_record`, `flow_record_condition`, `connection_measured_volume`, `capture_configuration`, `capture_interface`, `unmatched_record_aggregate_*`, `provider_source_period`, `push_listener`.
- Nothing checks that `connection_netflow` (`schema.sql:4059-4133`) is inlined.
- `netflow_parts_add` is PL/pgSQL with 36 fields (`schema.sql:3057-3109`).
- The core datasets' `relation.sql` and `earliest.sql` carry no `-- statement:` marker, so the plan sweep never plans them.

**Schema-check coverage.**
- `CORE_TABLES` and `CORE_ENTITIES` read only `schemaname = 'opnview'` (`schema-checks.sh:753-756`). So none of these is ever covered:
  - the `opnsense.mirror_*` tables;
  - the broker's stream tables;
  - the plugin tables (`pf_states.rate`, `unbound.query_log`).
- `provider_source_period` is classified bounded (`docs/data-model.md:422, 459`).

**`purge_due_addresses`** (`internal/store/flows/classify.statements.sql:514-527`). Candidate causes, read from the statement:
1. A scalar-subquery horizon, which hides its selectivity from the planner.
2. No index leads with `dst_interface_id`.
3. `idx_flow_src_interface_observed_at` lacks `src_address`, so every row costs a heap fetch.
4. Its generic plan matters, because statements run under pgx's statement cache (`sqldb/driver.go:55`).

**TimescaleDB.**
- The 12 October decision enables the Timescale License and uses continuous aggregates wherever they are exactly equivalent (line 2295).
- The tree still says Apache only: `docker-compose.yml:117-118, 147-148`, `ROADMAP.md:100-101`, `docs/data-model.md:474-476, 529`, `broker/recording.go:20-23`.
- `flow` cannot become a hypertable (`docs/data-model.md:487`).

**Phase D leftovers.**
- `internal/plugin`, `internal/web` and `internal/widget/hostareas/readers` each hold exactly 15 files.
- The nine import aliases appear in 14 files.
- `internal/plugin/runtime` is imported by 17 files.
- The move tooling is a separate module (`module mover`, go 1.24, `golang.org/x/tools v0.30.0`) holding compiled binaries. Its replay script hard-codes `/scratch` paths and calls Perl fix-ups.

## 2. Scope: "### Phase E: performance and the two budgets" (replaces Phase E whole)

### E.1 The everyday suite under 60 s

1. **Measure first.** A CI-only switch, off by default, makes the shared test helpers record, per package:
   - clone time;
   - `Open`: schema apply and read-role grant, separately;
   - every `waitForDatabase` retry;
   - drop time;
   - time in test bodies.

   The breakdown covers the nine slow packages. It is taken twice, with the whole suite running and with each package alone. The hand-back reports both breakdowns before and after the change.
2. **Fix by mechanism, in the shared helpers.** Never fix by a list of tests. Each point below is measured, and its decision is reported:
   - **A clone must not pay the full schema apply again.** The product records a digest of `schema.sql` in the database and skips the apply when it matches (decided, Q2). Tests therefore run the shipped code path.
   - **The cluster-wide role created per test.**
   - **`FILE_COPY` against `WAL_LOG` cloning.** Cite the PostgreSQL 17 documentation on `FILE_COPY`'s checkpoints.
   - **A bounded backoff on the 55006 retry.**
   - **Databases and connections in flight against `max_connections`.** Any `-p` or `-parallel` value written into `ci/checks.sh` is justified there by the measured figures.
   - **Every `waitForDatabase` retry during tests** becomes a reported event, not a silent sleep.
3. **Forbidden, whatever it saves:**
   - weakening, deleting or skipping an assertion;
   - adding `t.Skip`, `testing.Short()` or a build tag to a test;
   - moving a test to pre-deployment, unless it is a `-race` check or a 1M-row check;
   - suppressing a warning.

   A per-test edit is allowed only where a test bypasses the shared helpers. Each such edit is listed in the hand-back with its reason.
4. **Real applies are still exercised.** `TestApplyingTheSchemaTwiceChangesNothing`, and every test of `Open` on an empty or outdated database, still runs a real full apply.
5. **`internal/store/calendar_test.go` returns to the everyday suite** once E.1 leaves room for it (decided, Q7). If it does not fit, the figures are reported.

### E.2 Pre-deployment under 180 s

1. `PRE_DEPLOYMENT_BUDGET_SECONDS` stays the one constant. The command prints each step's wall time and the total, beside the CPU count.
2. **`-race` by a stated rule, computed by a `ci/` program, never a hand list.** A package is selected when any of its `.go` files, test files included:
   - (i) holds a `go` statement;
   - (ii) imports `sync` or `sync/atomic`;
   - (iii) serves HTTP: it imports `net/http/httptest`, declares a `ServeHTTP` method, or references `http.Handler`, `http.HandlerFunc`, `http.ServeMux` or `http.Server`;
   - (iv) or lies under `internal/plugin/`.

   Every package is printed as selected or excluded, with the clause that decided it.
3. Every package runs exactly once with the `predeployment` tag: the selected ones under `-race`, the others without it.
4. **The budget self-test** still asserts that `checks.sh` exits non-zero and names the budget. A CI-only variable restricts `checks.sh`'s `go test` to the probe package during the self-test, and the self-test runs concurrently with other steps (decided, Q3).
5. Independent steps may run concurrently: vet, fuzzing, the self-test's sleep and the seed builds. Each keeps its own output block and its own exit code.
6. **The 1M-row seed is built once per run.** That one build serves the plans, the widget budgets, the live budget and the reclassification cost (on a copy). The build prints its breakdown: schema apply, generation of each table, index maintenance, and `VACUUM (ANALYZE)`.
   - The seed is first built cold on every run, which must fit in 180 s.
   - If the breakdown shows it cannot fit, the seed is reused across runs while a digest of `schema.sql`, `seed.sql`, the seed parameters and the image digest matches. A run whose digest changed builds the seed cold and prints that build time apart; the 180 s budget then applies to runs whose seed already exists. This is preferred to dropping any check (decided, Q4).
7. If 180 s still cannot be met without dropping a check, the Builder reports the per-step figures and drops nothing.

### E.3 The five widgets over budget

1. Profile the five types at 100k/30d: statements per response, SQL time and script time.
2. If the N+1 geolocation read is confirmed, the core reads the geolocations of every peer a request places in one bounded statement.
   - The statement lives in a `.statements.sql` file, so the sweep plans it.
   - It fills the request cache, so `host.services.geolocation` keeps its contract.
   - Third-party widgets that place peers benefit without being edited.
3. Anything else the profile names is fixed in the core host areas, never by special-casing one widget type.
4. Every golden output stays byte-identical.
5. Widget budgets run on **both seeds**: 100k at 250/500 ms and 1M at 1 s/2 s (decided, Q1).

### E.4 Continuous aggregates, only where exactly equivalent

1. `docs/data-model.md`, *Storage fit*, records a verdict and a reason for every candidate:
   - the 24 family tables;
   - `unmatched_record_aggregate_*`;
   - the broker's `#minute` and `#hour` stream aggregates.

   Every TimescaleDB 2.30.2 restriction a reason relies on is cited from that version's documentation.
2. The expected reasons, which the Builder must verify and not take as given:
   - **Families:** `flow` cannot become a hypertable.
   - **`unmatched_record_aggregate_*`:** it is accumulated from the join's decisions and outlives the purge of its records.
   - **Stream aggregates:**
     - `last` is insertion-ordered, with ties at one-second resolution;
     - columns are added at run time;
     - the aggregates are retained longer than the detail rows;
     - each event is visible immediately;
     - refreshes may not run inside a transaction, which is to be verified.
3. A continuous aggregate is used only where an equivalence test passes: every slot equals the hand-built table across late ingestion, reclassification, purge and the existing fixtures.
4. **If none qualifies, the installation stays on Apache-2.0.** The 12 October licence decision is then recorded as superseded, with the reason: enabling an unused licence would be a placeholder (decided, Q5).

### E.5 `purge_due_addresses`

1. Record `EXPLAIN (ANALYZE, BUFFERS)` at the 1M seed, under the custom and the generic plan, at three horizons: none due, about 1 % due, and all due. State which cause holds.
2. Fix the statement, or add or extend an index, so that both branches are bounded by an index condition and the work scales with the number of due rows. The result set stays identical. A new index is justified in `schema.sql` like the existing `flow` indexes, with its write cost stated.

### E.6 NetFlow plans at the 1M seed

1. **`sql/seed.sql` fills these tables**, at every size, in proportion to `flow_rows`, with no address literal:
   - `capture_configuration` and `capture_interface`: one Observation Point, plus one interval with two captured interfaces;
   - `flow_record` and `flow_record_condition`;
   - `connection_measured_volume`: every state allowed by its CHECK;
   - `unmatched_record_aggregate_*`;
   - `provider_source_period`: one pull period and one push period;
   - `push_listener`;
   - the `netflow_*` columns of the families and of `purged_flow_hour`.
2. **Plan checks**, for every `_netflow` twin, `connection_netflow` and `netflow_sum`, at both seeds:
   - no `Function Scan` of `connection_netflow`;
   - `connection_measured_volume` is read through its primary key;
   - no growing table is scanned;
   - the same set of indexes is used at 100k and at 1M.
3. **Widget budgets.** `widgetbudget` also times the `netflow` answer of every type whose preset offers a volume measure, with the same budgets and the same empty-answer rule. The cost of `netflow_sum` is reported.

### E.7 Schema checks over the tables added since Phase C

1. `sql/schema-checks.sh` covers, in its entity, classification, foreign-key, seed-fill, plan and purge sections:
   - every core table added since Phase C;
   - the `opnsense` schema's bookkeeping tables;
   - the built-in plugins' tables.

   The plugin tables are created by a `ci/` program that runs the plugin loader.
2. A plugin table's class (growing or bounded) is derived from its manifest: a time column and a retention make it growing. It is never taken from a list in `docs/data-model.md`.
3. The plan sweep also plans every core dataset's `relation.sql` and `earliest.sql`, at both seeds, with `:from` and `:to` bound.
4. After a purge, no row older than its horizon remains in any core growing table added since Phase C.
5. **`provider_source_period`.** The Builder first states which code path writes a row (decided, Q8):
   - if a row is written on each listener restart, the table is reclassified as growing and purged at the flows' horizon, keeping the open period;
   - otherwise it stays bounded, and the bound is documented.

### E.8 The five Phase D proposals (decision of 16 October 2026)

1. **Room under the 15-file rule.**
   - The plugin test harness moves to `internal/plugin/harness`.
   - The message-key files of `internal/web` move to their own package.
   - `internal/widget/hostareas/readers` is split in two, by responsibility.
2. **The nine import aliases are removed** by renaming the shadowing locals. No new alias replaces them.
3. **`internal/plugin/runtime` is renamed** to a name that collides with no standard-library package (`scripthost` unless that collides). The map of the tree follows.
4. **Each part's tests move into the part's own directory.** Every identifier that Phase D exported only for tests becomes unexported, or is kept with a named non-test caller. Shared fixtures such as `testNetwork` (`store/network_test.go:61`) are split as needed.
5. **The move tooling becomes its own module at `ci/mover/`**, with its own `go.mod` (decided, Q6).
   - `golang.org/x/tools` is pinned after the complete transitive dependency audit. The product's `go.mod` does not change.
   - Kept: the commands' sources, a `doc.go` per command, and one example plan.
   - A fixture test runs the splitter on a small module under `testdata/` and asserts that the result builds and matches the expected tree.
   - Not kept: compiled binaries, absolute paths, and the Perl fix-ups and replay scripts.
   - `ci/checks.sh` builds, vets and tests the module inside the timed span.
6. **As in Phase D:** the moves preserve history; B5's comparison, the widget goldens and X3's snapshot stay byte-identical.

### E.9 Order and reporting

The work runs in this order:
1. E.8;
2. E.1;
3. E.7 and E.6, seed first;
4. E.5;
5. E.3;
6. E.4;
7. E.2.

The suite runs once, before commit. The hand-back carries:
- the before-and-after tables of E.1.1, E.2.1, E.2.6 and E.3.1;
- every figure that §8 lists as unmeasured.

## 3. Acceptance criteria: replaces E1 under "### Phase E and the whole cycle" (X1–X4 unchanged)

- [ ] **E1.** `docker compose run --rm pre-deployment`:
  - prints each step's wall time and the total, beside the CPU count;
  - fails above `PRE_DEPLOYMENT_BUDGET_SECONDS`, which is written once;
  - passes within 180 s on 16 CPUs, or the hand-back carries the per-step figures as E.2.7 requires.
- [ ] **E2.** `docker compose run --rm checks` reports a `go test` wall time of at most 60 s on 16 CPUs and exits 0.
- [ ] **E3.** The E.1.1 switch prints, per package, the clone, schema-apply, role, wait-retry, drop and body totals. The hand-back gives them before and after, for the nine packages, with the whole suite running and with each package alone.
- [ ] **E4.** A green everyday run records zero `waitForDatabase` retries. A test that forces a refused connection shows the retry is reported, not silently slept.
- [ ] **E5.** A test inventory (`go test -json -count=1 ./...`) is taken before Phase E, and every name in it passes afterwards. The hand-back maps each test moved under E.8.4 to its new path.
- [ ] **E6.** The Phase E diff adds no `t.Skip`, no `testing.Short()` and no `//go:build` line to any test.
  - The only `predeployment`-tagged tests are those `ROADMAP.md` lists. `calendar_test.go` is back in the everyday suite, or the hand-back reports why it does not fit.
  - Every removed `_test.go` line that holds `t.Error`, `t.Errorf`, `t.Fatal`, `t.Fatalf` or a comparison is listed beside the line that replaces it.
- [ ] **E7.** The `-race` selection is computed by a `ci/` program from E.2.2's four clauses. Pre-deployment prints every package with its deciding clause. A test shows that a fixture package becomes selected when a `go` statement is added to it, and that a fixture package with no trigger is excluded.
- [ ] **E8.** Pre-deployment runs every package of `./...` exactly once with the `predeployment` tag, the selected ones with `-race`. The two printed sets together equal `go list ./...`.
- [ ] **E9.** `ci/budget-self-test.sh` still fails unless `checks.sh` exits non-zero **and** prints "over the test budget of 60 s". During the self-test, `go test` is restricted to the probe package.
- [ ] **E10.** One run's output shows:
  - exactly one 1M-row build, or a reuse whose digest is printed;
  - the build's breakdown;
  - every consumer that read it: plans, widget budgets, live budget, and reclassification on a copy.
- [ ] **E11.** `widgetbudget` prints no `OVER BUDGET` line on either seed. Each of the five types in §1 has a 30d median of at most 500 ms at 100k.
- [ ] **E12.** For each of the five types, a test answers the same request over two fixtures that differ only in the number of distinct outside peers, and the statements per response are equal. The batched read is a named statement that the S5A-AC35 sweep plans, and it scans no growing table.
- [ ] **E13.** Every widget golden, B5's comparison and X3's snapshot pass with their recorded files byte-unchanged.
- [ ] **E14.** *Storage fit* gives a verdict and a reason for every E.4 candidate, citing the TimescaleDB 2.30.2 documentation. No continuous aggregate exists without a passing equivalence test.
- [ ] **E15.** The licence setting is the same everywhere it appears: `docker-compose.yml`, the serve compose file, `ROADMAP.md`, `README.md` and `docs/architecture.md`. It is Apache-2.0 unless a continuous aggregate qualified. `sql/schema-checks.sh` asserts the value the database is running with.
- [ ] **E16.** `purge_due_addresses` returns the same set at both seeds and at the three horizons.
  - At 1M with nothing due, `EXPLAIN (ANALYZE, BUFFERS)` reads at most 100 shared buffers, under both the custom and the generic plan.
  - Each branch's access to `flow` carries an index condition on `observed_at`.
  - The cause is stated in the statement's comment.
- [ ] **E17.** At both seed sizes, every table E.6.1 names holds rows, in both address families where the table carries an address. The seed holds no address literal (V6-AC2).
- [ ] **E18.** For every `_netflow` twin, at both seeds:
  - no `Function Scan` on `connection_netflow`;
  - `connection_measured_volume` is read through its primary key;
  - no growing table is scanned;
  - the same set of indexes is used at 100k and at 1M.

  A teeth check catches a twin that calls a non-inlinable copy of the function.
- [ ] **E19.** `widgetbudget` prints a `netflow` line for every type offering a volume measure. Every such line is within budget and non-empty.
- [ ] **E20.** Every core dataset's `relation.sql` and `earliest.sql` is planned at both seeds, and none scans a growing table.
- [ ] **E21.** The AC10 classification covers every table of the `opnview` and `opnsense` schemas and every built-in plugin table, and plugin classes come from their manifests. A test plugin directory holding a table with a time column is classified and planned without editing any shipped file. `provider_source_period`'s class follows E.7.5, with the writing code path stated.
- [ ] **E22.** After the schema checks' purge, no row older than its horizon remains in `flow_record`, `flow_record_condition`, `connection_measured_volume` or `unmatched_record_aggregate_*`.
- [ ] **E23.** `internal/plugin`, `internal/web` and each part of the former `hostareas/readers` hold fewer than 15 files. D1 and D2 pass, and the map lists the new packages.
- [ ] **E24.** No file imports a package under any of the nine aliases. The set of import aliases after Phase E is a subset of the set before.
- [ ] **E25.** No directory named `runtime` exists under `internal/`, and nothing imports `…/internal/plugin/runtime`.
- [ ] **E26.** Every identifier in Phase D's list of test-forced exports is unexported, or is kept with a named non-test caller listed in the hand-back.
- [ ] **E27.** `ci/mover/` is its own module, with:
  - `x/tools` pinned after the audit, and no change to the product's `go.mod`;
  - a `doc.go` per command;
  - a fixture test that passes;
  - no binary and no absolute path;
  - a build, vet and test run by `ci/checks.sh` inside the timed span.
- [ ] **E28.** `ROADMAP.md`, *Development and test environment*, states:
  - both budgets;
  - the `-race` rule;
  - what the self-test runs;
  - the seed rule, reuse included;
  - the exact list of tests moved to pre-deployment.
- [ ] **E29.** A test copies a fixture plugin into a temporary plugin tree. The fixture holds a widget that places peers and a table with a time column. The test shows the plugin is covered by:
  - the geolocation batching;
  - the manifest-derived classification;
  - the plan sweep;
  - the `-race` rule where it applies.

  No shipped file is edited.
- [ ] **E30.** No new OPNsense data source is read: the diff adds no endpoint to the registry or to any manifest.

## 4. Out of scope

- Any change to what a widget, dataset or screen answers, beyond the admitted differences already recorded.
- Raising any budget.
- Indexes, except under E.5.
- The join-throughput row at the 1M seed (declined 11 October 2026).
- Migration, backfill or old-schema compatibility (decided 11 October 2026).
- The live service (port 8080) and the `data` volume.
- Phase F, except the sentences listed in §6.

## 5. Risks

1. The per-package cost reading is a hypothesis. If contention dominates, the concurrency bound becomes the lever.
2. Skipping the schema apply would make the apply-twice test vacuous unless that test forces a real apply (E.1.4).
3. A cold 1M build may not fit in 180 s alongside `-race`. Q4's fallback covers this.
4. The `-race` rule may select almost every package. The figure is reported, not tuned away.
5. Seeding the NetFlow tables may expose existing failures. These are defects to fix, not checks to relax.
6. `golang.org/x/tools` needs the full transitive dependency audit, and has not been tested here with Go 1.27.
7. `provider_source_period` may grow without bound (E.7.5).
8. The TimescaleDB restrictions in E.4 come from memory and must be verified against the 2.30.2 documentation.

## 6. Documents

- `ROADMAP.md:198-214` is rewritten per E28.
- `ROADMAP.md:100-101` keeps the Apache-only sentence, adding the date and the reason, unless a continuous aggregate qualified.
- `docs/data-model.md`, *Storage fit*, gains the E.4 verdicts and the `opnsense` tables.
- `docs/architecture.md`, *Map of the tree*, follows E.8.

## 7. Obsolete sentences

1. Spec lines 620–631, the old Phase E.
2. Spec line 843, the old E1.
3. "Widget timings run on the large seed only": both seeds are timed now (Q1).
4. `ROADMAP.md:210`, "No test is moved today".
5. Line 2295's licence sentence, if no continuous aggregate qualifies (Q5).

## 8. Figures not yet measured (hand-back items)

- Today's everyday breakdown.
- Pre-deployment's per-step figures.
- The 1M seed's build time.
- Widget timings after the plugin port.
- `purge_due_addresses`'s plan.
- Whether `widgetbudget` fails today on the NetFlow datasets.
- The cost of `netflow_sum`.
- Whether `connection_netflow` is inlined.

## 9. Decided (maintainer, 16 October 2026): the planner's open questions

1. Widget budgets on both seeds: 100k at 250/500 ms, 1M at 1 s/2 s.
2. A digest of `schema.sql` is recorded in the product, and the apply is skipped when it matches. A real apply is still tested.
3. During the budget self-test, `go test` is restricted to the probe package, and the self-test runs concurrently with other steps.
4. The 1M seed is built cold, and is reused by digest only if a cold build cannot fit.
5. If no continuous aggregate qualifies, stay on Apache-2.0 and supersede the 12 October licence decision.
6. The move tooling is its own module, `ci/mover/`.
7. `calendar_test.go` returns to the everyday suite.
8. `provider_source_period` is classified from its writing code path: growing and purged if a row is written per restart, bounded and documented otherwise.

---

**This amendment is part of the spec and overrides any earlier text it replaces; section H lists the earlier sentences it makes obsolete.**

# Amendment: the NetFlow join with every interface captured (replaces decision 15b and the single-Observation-Point rule)

Status: APPROVED

Cycle kind: standard

**Placement.** This amendment follows the Phase E amendment and is built **after Phase E is complete**. It replaces the parts of the NetFlow join amendment (lines 3601–4011) that §H names; anything it does not name stays as approved.

**Cycle kind.** `standard`: Go, SQL, plugin JavaScript, fixtures and documents. Nothing on screen changes; the widget API changes only as a data contract (states and coverage fields).

**Modularity.** CLAUDE.md, *The user interface*: "Modular at every layer". Adding an exporter implementation is adding a plugin directory and touching nothing else. NJ9 and NJ-AC17 test this.

## Context

**What the real data shows.** The maintainer's firewall was captured on 16 October 2026: OPNsense 26.7.3_11, all seven interfaces captured, NetFlow v9, 11,193 records and 13,352 `filterlog` lines. The analysis is in `scratchpad/capture-analysis/REPORT.md` (session scratchpad). It contradicts what decision 15b assumed:

- **The records name their interfaces.** `INPUT_SNMP` and `OUTPUT_SNMP` (IANA `ingressInterface` IE 10 and `egressInterface` IE 14) are filled. `INPUT_SNMP = 0` appears only on firewall-originated records (S6a). Local delivery carries the loopback's index as `OUTPUT_SNMP` (REPORT Q1, Q2).
- **No cross-node identical twin was observed** (S11's "two records identical in every field"). The 26 records that are identical in every field lie within one node and one datagram: they are distinct real one-packet records (Q3).
- **VLAN parent copies exist.** A parent interface's node re-exports VLAN ingress with `INPUT_SNMP` = the parent (584 records). Of 131 VLAN-ingress tuples, 7 have no parent copy and 5 have a copy with different totals (Q3).
- **Translated traffic is exported twice:** a pre-NAT copy from the ingress node and a post-NAT copy from the upstream egress node, equal in bytes in 2,521 of 2,540 cases.
- **pflog logs the post-NAT form on the upstream `out` leg** (3,376 of 3,376). This settles S11's `UNVERIFIED:` sub-fact (Q4).
- **Upstream ingress is not captured**, which fits an egress-only configuration; `getconfig` has not confirmed it (Q4).
- **`wg0` has no node, and one VLAN exported nothing of its own** (Q2, Q7).
- **REPORT's Rule B joins exactly one leg** for 82.2 % of records and 92.3 % of bytes, excluding the expected copies. It prevents about 48.5 MB of double counting and rejects nothing legitimate (Q5).

**What the repository holds today.**
- **The single-Observation-Point join is built:**
  - `internal/collect/derive/flowrecord.go`: `StateAt`, `DirectionAt`, `consistent`, groups keyed by `observation_point_device`;
  - `internal/collect/derive/measuredvolume.go`: a figure only under `CaptureObservationPoint`, otherwise `not_captured`;
  - `internal/store/netflow/flowrecord.go`: the five capture states; `CaptureStateAt` returns `observation_point_not_exported` for two or more interfaces;
  - `internal/store/schema.sql`: `flow_record`'s `capture_state`, `captured_interfaces`, `observation_point_*`, `direction` and `not_joinable` (lines 3939–3964); `idx_flow_record_group` (3978); `flow_record_condition` (3852–3872); `connection_measured_volume.observation_point_*` (4020–4021); the unmatched family keyed on `observation_point_identifier` (3215, 3226);
  - `internal/widget/request/measure.go`: the window state, `observation_point_not_exported` and `state_details.captured_interfaces`.
- **The decoder** (`internal/push/protocols/netflow/decoder.go:51-54, 577-625`) flags `netflow_sequence_interleaved` and then asserts no loss.
- **The `netflow` plugin** (`plugins/netflow/plugin.json`, `netflow.js`):
  - it already maps `ingressInterface` and `egressInterface`;
  - it reads `getconfig` and `is_enabled`;
  - its description (`plugin.json:6`) and its header (`netflow.js:28-31`) state the one-interface rule.
- **The index map** (`internal/collect/derive/pushindexes.go`) reads `interfaces_info/1` `statistics.index` per device. Nothing establishes the loopback's index or a VLAN's parent. `interfaces_info` carries `vlan_tag`, but the survey has recorded no parent field.
- **Firewall addresses** are held by discovery (`interface_address`, source `interfaces_info`).
- **Phase E's E.6** (lines 4179–4194) seeds "one Observation Point, plus one interval with two captured interfaces" and `flow_record_condition`, and E22 purges `flow_record_condition`.

**Why the rule changes.** The maintainer wants every interface captured, so that Insight keeps everything, **and** NetFlow volumes joined to the logged connections. Decision 15b forbade having both; the capture shows they are compatible.

**What does not change.** The **observation-point limit** of `docs/widget-api.md` and `docs/widget-catalogue.md` (traffic within one interface stays invisible, and the limit is documented rather than printed) is unrelated to RFC 7011's *Observation Point* and does not change.

## A. Decision recorded (appended to decision 15, after 15c)

> - **15d. The NetFlow join works with every interface captured** (maintainer, 16 October 2026; supersedes 15b).
>   - Each record is joined from its own `ingressInterface` and `egressInterface`, mapped at reception, under NJ4 (REPORT's Rule B).
>   - No Observation Point is assigned to a record: OPNsense exports none (S11).
>   - Translated copies and VLAN parent copies are stored and never counted, except a translated copy under NJ5's sole-copy rule.
>   - Records are never deduplicated by content.
>   - Each direction of a connection is counted once.
>   - What the rule cannot measure carries a named state per direction, served per figure.
>   - Coverage is read from the capture configuration, never inferred from records.

## B. Scope

### NJ1. Firewall facts first (survey, *Push sources*)

Each fact is read from source and recorded in the survey with its 26.7.3 or `stable/26.7` URL, or else marked `UNVERIFIED:`. Nothing in NJ2–NJ8 is built on an `UNVERIFIED:` fact, except through the fallback named for it.

| # | What must be established | Fallback while `UNVERIFIED:` |
|---|---|---|
| S5c | Which interfaces get an `ng_netflow` node when they are listed in `capture.interfaces`; REPORT's hypothesis is that only Ethernet attachments get one, so `wg` gets none. Also which field of a read-only endpoint tells the two kinds apart. | The plugin reports such an interface's export as `unknown`; its directions get `capture_configuration_unknown` (NJ5). |
| S6b | How the loopback's kernel index is read: a row of `interfaces_info/1` with its `statistics.index`, identified by a reported attribute such as an interface flag, never by device name. | Rule 2(c), and rule 4's "0 equals L", are not built. A record whose egress index is unmapped is unmatched with `interface_index_unmapped`, and the hand-back reports it. |
| S6c | Where a VLAN's parent is read (an `interfaces_info` field, or `/api/interfaces/vlan_settings/search_item`, with VLAN model fields such as `if` / `vlanif`), and what OPNsense calls that field, which settles the term "parent". | Parent copies cannot be classified; they end `no_logged_leg`, and LV-NJ2 reports their share. |

**Corrections recorded from the 16 October capture.**
- S11's pflog sub-fact is settled as "the post-NAT form on the `out` leg" (3,376 of 3,376). The `UNVERIFIED:` marker and PS-AC28's named exemption are removed.
- The other S5, S6 and S11 corrections are listed in NJ11.

### NJ2. Contract and stored columns

- **The flow-record contract (PS17).** Its provider-filled fields do not change: `ingress_interface` and `egress_interface` were already raw indexes.
- **Core-written columns of `flow_record`.** They are replaced. The core no longer writes `capture_state`, `captured_interfaces`, `observation_point_identifier`, `observation_point_device` or `direction`. It writes instead:
  - the **mapped ingress and egress interface**, each as identifier and device, from the index map in force at reception (NJ3);
  - for each of the two, a **role** from a closed list (the names are proposals): `interface`, `no_receiving_interface` (index 0, S6a), `local_delivery` (the loopback index, S6b) or `unmapped`;
  - **`join_state`**: `pending`, `joined`, `copy` or `unmatched`. `not_joinable` is removed, because every record can be joined;
  - **`copy_kind`**: `translated_copy` or `parent_copy`, present exactly when `join_state = copy`;
  - a **`counted_copy` marker** on a translated copy counted under NJ5's sole-copy rule;
  - **`unmatched_reason`**, from NJ4 rule 9's closed list;
  - **`loss_detectable`** (NJ7);
  - **`tuple_key`**, unchanged.
- **The group key** no longer holds a device. `idx_flow_record_group` becomes `(tuple_key, start_at)`, unless the Builder shows a better index with plans at both seeds.
- **The capture-configuration record (contract).** It keeps, per interface, the identifier, `ingress` and `egress`, and gains:
  - **`exported`**, for each captured direction: whether the exporter actually exports what is configured. The value is `yes`, `no` with a named reason (proposal: `no_export_point`), or `unknown` (the S5c fallback);
  - **`parent_of`**: the identifiers of the interfaces whose received packets this interface's exporter also exports with this interface as `ingressInterface` (S6c).

  The plugin fills both. The core never names the mechanism behind them.
- **`capture_interface`** gains the matching columns.
- **`flow_record_condition`** is dropped (NJ6).
- **`connection_measured_volume`**:
  - loses `observation_point_identifier` and `observation_point_device`;
  - loses `measured_at_leg` and `measured_flow_id`, unless they still carry information under decided questions 3 and 4;
  - gains the `counted_copy` marker per direction;
  - its `state` CHECK takes NJ5's list.
- **No migration.** The project is pre-POC and the database is rebuilt. No migration, backfill or compatibility code is written (decided 11 October 2026).

### NJ3. The index map in force at reception

- **When a record is mapped.** Its two indexes are mapped when it is received, against the latest `interfaces_info/1` read at or before reception: PS11, through the broker, at discovery's max age.
- **Mapped values are fixed.** They are stored and never re-mapped.
- **Exporter restart.** When the exporter's `sysUptime` goes backwards, the index map is read again before the next record is mapped, because a reboot can renumber cloned interfaces.
- **Roles.** Index 0 has the role `no_receiving_interface`. The loopback index (S6b) has the role `local_delivery`. Any other index absent from the map is `unmapped`.

### NJ4. The join (REPORT's Rule B)

**Definitions.**
- *dev(i)* is the interface that a record's mapped index *i* names.
- *L* is the loopback index.
- A **leg** is a `flow` row.
- A **second leg** is a leg whose pairing outcome is `second_leg`.

**Unchanged.** Forward and reverse (RFC 5103), address comparison by value, the portless key, the time rule (latest `observed_at` ≤ start + `netflow_join_tolerance_seconds`), the SYN rule, continuity (S12 defaults), ambiguity and "every action joins" are PS19 rules 2–6 as approved. Only the device and the pf `dir` of the candidate legs change.

1. **`ingressInterface` = 0** (firewall-originated). The record joins forward to the `out` leg on dev(egress), or in reverse to the `in` leg on dev(egress).
2. **`ingressInterface` ≠ 0.** The record joins:
   - (a) forward to the `in` leg on dev(ingress);
   - (b) in reverse to the `in` leg on dev(egress);
   - (c) if `egressInterface` = L, in reverse to the `out` leg on dev(ingress).
3. **Choosing among candidates.** The candidates of every applicable case are pooled, and PS19's time, ambiguity, SYN and continuity rules choose among them. **A second leg is never a candidate.**
4. **The reverse-join check.**
   - A reverse join under rule 1 or rule 2(b) is accepted only if a forward record joined to the same leg has an `egressInterface` equal to this record's `ingressInterface`, with 0 and L counted as equal.
   - Until such a forward record exists, the reverse record stays `pending`, while the filter-log read-through ≤ the leg's `observed_at` + the active timeout in force + the tolerance. Without a known timeout, pf's `tcp.established` is used.
   - After that bound, the record is decided by rule 6, or else unmatched with `reverse_unconfirmed`.
   - A forward record joined later reopens the pending reverse records of that leg (PS19 rule 8).
5. **Translated copy.** A record with `ingressInterface` ≠ 0 and `egressInterface` ≠ L that joins no candidate is a `translated_copy` when either holds:
   - its tuple equals, in either orientation, the logged tuple of a second leg on dev(egress) or dev(ingress);
   - its source or destination is an address that discovery holds for the firewall.

   It is stored with `join_state = copy`, counted only under NJ5's sole-copy rule, and never reported `no_logged_leg`.
6. **Parent copy.** A record whose dev(ingress) declares a non-empty `parent_of` is a `parent_copy` when either holds:
   - (i) it joins no candidate on dev(ingress), but would join under rule 2 if dev(ingress) were replaced by an interface of its `parent_of`;
   - (ii) rule 4 rejected its reverse join, and the egress interface of the accepted forward record is in its `parent_of`.

   It is stored, never counted, and never reported `no_logged_leg`.
7. **No deduplication.** Records are never deduplicated by content. Identical records are distinct rows (PS17 identity), and each is counted when joined.
8. **Everything is core.** Every rule reads contract fields, legs, the index map, the capture configuration and discovery's addresses. None names a device, an interface, a VLAN or an address.
9. **Unmatched reasons**, a closed list (the names are proposals):
   - `no_logged_leg`;
   - `ambiguous`;
   - `beyond_flow_horizon`;
   - `interface_index_unmapped` (reinstated: a record whose needed index is `unmapped`);
   - `reverse_unconfirmed`.

### NJ5. Counting each direction once, and the per-direction states

- **A direction's figure.** A connection's direction figure is the sum of the `joined` records joined, in that orientation, to its first (or single) leg, plus the sole-copy records below. No record contributes to two connections, nor twice to one.
- **Exporters of a direction.** Each direction has an **entry** (the interface its packets arrive by, or local) and an **exit** (the interface they leave by, or local). They come from the configuration in force at the connection's start and from the connection's legs:

  | Connection | Forward | Reverse |
  |---|---|---|
  | first leg `in` on A, paired second leg `out` on B | A → B | B → A |
  | single `in` leg on A, destination an address of the firewall | A → local | local → A |
  | single `in` leg on A, any other destination | A → unknown | unknown → A |
  | single `out` leg on A (firewall-originated) | local → A | A → local |

  - **Candidates.** The exporters that can see a direction are the entry interface's ingress and the exit interface's egress, wherever that direction is `exported = yes`.
  - **Translated connections.** On a translated connection (the second leg's logged tuple differs from the first leg's), an exporter on the second leg's interface sees the translated form, so its records are copies and it is excluded.
  - The exporters that remain are the **counted exporters**.
- **Sole-copy rule (decided, question 4).**
  - When a direction has **no** counted exporter, and an exporter on the second leg's interface exports the **translated** form of that direction, its translated copies are counted for that direction and marked `counted_copy`. This happens for example from `wg0` to outside.
  - The choice is made from the configuration alone, so the copy cannot double.
  - Parent copies are never counted.
- **The state of a direction.**
  - **Exactly one counted exporter, or the sole-copy rule applies:** the approved states `measured`, `in_progress`, `awaiting_export`, `missing` and `export_gap`.
  - **Two counted exporters:** untranslated forwarding between two exported interfaces, or an unknown entry or exit while another interface exports that side. The state is `forwarding_export_unsettled`, with no bytes, not covered (decided, question 1). No controlled test follows; LV-NJ2 item 7 only counts the state.
  - **None, and no sole copy:** the first cause that applies, looking at the entry side, then the exit side:

    | State | Cause |
    |---|---|
    | `not_exported_when_refused` | a refused `out` leg: pf dropped the direction before the egress capture (approved meaning) |
    | `ingress_not_captured` | the entry interface is captured egress-only, for example an egress-only upstream's inbound blocks, or the replies of firewall-originated traffic |
    | `parent_copies_only` | the entry interface exports no ingress of its own, but an exported interface lists it in `parent_of` |
    | `interface_not_exported` | configured as captured but reported `exported = no` |
    | `interface_not_captured` | not in the capture configuration |
    | `capture_configuration_unknown` | the configuration, or an interface's `exported`, is unknown |

    The state names are proposals.
- **Coverage.** `not_captured` is removed. **Covered** stays `measured`, `in_progress`, `awaiting_export` or `missing`. Every other state is not covered.
- **No readable configuration.** A connection that started under `netflow_not_configured` or `capture_configuration_unknown` has no row. Its class, today's `not_joinable`, is renamed (proposal: `capture_not_known`), because records now join regardless.
- **The exit row (decided, question 3).** A second leg's `exit_leg_device` row carries, under `netflow`, the connection's figure in both directions when the second leg passed, and `not_exported_when_refused` forward when it was refused. These are the same packets, mirroring how a second leg's logged bytes count there.

### NJ6. What becomes of the Observation Point

**Removed** (no speculative placeholders):
- the record's Observation Point and direction;
- the `observation_point`, `observation_point_not_exported` and `interface_index_unmapped` capture states, as interval states;
- `captured_interfaces`;
- `DirectionAt`, the direction-consistency table, and `StateAt`'s one-interface branch;
- the configuration-contradiction rule;
- `flow_record_condition`;
- the decoder's `ResetSequences` call on a configuration change, unless NJ7 still uses it;
- `state_details.captured_interfaces`;
- `widget_flow_record_unmapped`;
- the "single Observation Point throughout the window" condition;
- PS-AC28's pflog exemption;
- the README's "restricting capture to one interface narrows Insight" trade-off;
- the `flow_record` dataset label about "identical records whose interface is not known".

**Kept:**
- `capture_configuration` and `capture_interface` as runs of reads, `CaptureHistory`, and the rule "in force over its run, unknown between differing runs";
- `netflow_not_configured`, `capture_configuration_unknown` and `netflow_not_covering_window` with its `interval_states` detail;
- the per-direction-state coverage counts and the four coverage classes (one renamed);
- `connection_netflow` as the single reader;
- `netflow_parts`, extended (NJ8);
- RFC 7011's *Observation Point* in *Vocabulary*, restated as "not exported by OPNsense and not assigned by `opnview`".

### NJ7. Loss detection under several exporting nodes (decided, question 2)

- The decoder follows several sequence counters under one `source_id`. A datagram continues the counter whose expected next sequence equals its own; otherwise it opens a new counter.
- A `netflow_sequence_gap` is asserted only when the open counters exceed the number of interfaces that the configuration in force reports as exporting. The core passes in this bound from the contract; the decoder names no OPNsense concept.
- `loss_detectable` is 1 over the intervals where the bound is known.

### NJ8. Aggregates and widget API

- **PS21.**
  - The unmatched family's key becomes: slot, the ingress interface identifier or role, the egress interface identifier or role, protocol, IP version and reason.
  - Copies are in no family, except counted copies, which count in their connection's figures.
  - `netflow_parts` gains, per part, a `<part>_direction_<state>_connections` field for each NJ5 state, and loses `not_captured`. The Builder states the new field count.
- **PS22.**
  - **The `netflow` measure's definition** becomes: "bytes and packets the firewall's NetFlow export reported for the logged connections, each direction counted once, from the records in the first leg's form, or from the translated form where no exporter sees the first leg's form; copies are otherwise never counted".
  - **`best_available` stays strict (decided, question 5).** It answers `netflow` only when the window has a readable, enabled configuration throughout and every counted connection is covered. Otherwise it answers `logged`, with `netflow_not_configured`, `capture_configuration_unknown` or `netflow_not_covering_window`. LV-NJ2 item 5 measures the fallback rate, and the maintainer revisits the rule with that figure.
  - **Coverage object.** The coverage object on every `netflow` figure serves the counts per NJ5 state, so that no unmeasurable direction is silently zero.
  - **The `flow_record` dataset** serves `join_state`, `copy_kind`, `counted_copy` and the mapped interfaces. Its label says that a sum over records which are not `joined` counts copies.

### NJ9. Modularity

- **Core.** The join, the states, coverage and counting are core (`internal/collect/derive`, `internal/store/netflow`).
- **The plugin supplies contract data only.**
  - `plugin.json` keeps its IE mapping unchanged.
  - `netflow.js` reports per-interface `exported` and `parent_of`, from the S5c and S6c reads. It declares these in its manifest's `endpoints`, with `survey_section` and `upstream_url`.
- **A second exporter** is a directory with its own mapping and its own configuration read. Nothing under `internal/` or in any shipped plugin is edited.
- **Description and header.** The plugin's `description` and its header state the all-interface rule.

### NJ10. Interaction with Phase E (built after it)

- **E.6.1's seed list is replaced for:**
  - `capture_configuration` and `capture_interface`: several captured interfaces, including one egress-only interface, one `exported = no`, one with `parent_of`, and one unreadable run;
  - `flow_record`: every `join_state`, both `copy_kind`s, a `counted_copy`, every unmatched reason and every role;
  - `connection_measured_volume`: every NJ5 state;
  - `unmatched_record_aggregate_*`, under the new key.

  `flow_record_condition` is removed from the list.
- **Phase E checks rerun.** E17, E18 (including the teeth check, with the extended `netflow_parts`), E19 and E22 (without `flow_record_condition`) are rerun and pass at both seeds.
- **Join statements.** The join's statements are planned by the sweep at both seeds, and none scans a growing table.
- **Budgets.** No budget is raised.

### NJ11. Survey and document corrections

- **Survey (`docs/opnsense-api-survey.md`).**
  - **S5:** "one `ng_netflow` node per captured interface" is qualified by S5c. Add that a VLAN parent's node re-exports VLAN ingress with its own index (observed 16 October 2026).
  - **S6:** add S6b and S6c. S6a is confirmed by data.
  - **S10:** confirmed (templates 256/259, `OUT_*` = 0, 1 s resolution, no options template).
  - **S11:**
    - replace "two records identical in every field … Only an installation capturing one interface knows its observation point" with the observed facts;
    - mark the pflog sub-fact settled;
    - record the unexplained absences (blocked ICMP to the firewall, 0 of 40; some inter-VLAN TCP, 0 of 49) as open, without the maintainer's device names.
  - **The summary-table rows** for S5, S6 and S11 are updated.
  - ***The user's procedure*:** delete "capture exactly one interface … narrows what Insight collects". State instead:
    - capture the interfaces to measure;
    - an egress-only interface's inbound traffic is `ingress_not_captured`;
    - an interface the exporter cannot capture is `interface_not_exported`.

    These consequences are described, not advised.
- **`docs/data-model.md`.**
  - *NetFlow volumes* is rewritten.
  - *Vocabulary*: rows 184, 187 and 188 change. New rows, each with its reason: *translated copy*, *parent copy* (term per S6c), *counted copy*, and the index roles.
  - *Storage fit*: row 503 changes.
- **Other documents.**
  - `docs/widget-api.md` and `docs/plugins.md` (*A NetFlow subscriber*, the `flow_record` contract, `netflowState()`) are amended where named.
  - `README.md`: the NetFlow paragraph.

### NJ12. Fixtures

- **Source.** Every fixture derives from REPORT's shapes. It is fed through the real v9 decoder and the real filter-log parser, uses templates 256/259 with REPORT Q1's field list, and has `source_id` 0.
- **Rewriting under X1.**
  - Addresses: documentation addresses only (RFC 5737, RFC 3849).
  - Interface identifiers, devices and kernel indexes differ from the capture's.
  - Nothing from the maintainer's network appears: no address, MAC, identifier, device or index.
- **Provenance.** Each fixture carries `__provenance__`, naming the REPORT section it follows and what was rewritten or synthesised.

| # | Shape | Expected |
|---|---|---|
| F1 | Outbound NAT from a VLAN: a pre-NAT ingress record; a parent copy; a post-NAT egress copy (`ingressInterface` = the VLAN); the reply as the VLAN's egress copy (`ingressInterface` = upstream, destination de-NATed); no parent copy of the reply | forward and reverse `measured`; one `parent_copy`; one `translated_copy` |
| F2 | The same from an untagged interface | as F1, without the parent copy |
| F3 | Client → firewall on a VLAN: forward `egressInterface` = L; a parent copy (parent → L); reply with `ingressInterface` = 0 | 2(a) and rule-1 reverse joined; rule 4 with 0 = L; one `parent_copy` |
| F4 | Firewall → internal host on a VLAN: forward `ingressInterface` = 0; reply `egressInterface` = L; a parent copy of the reply | rule-1 forward and 2(c) joined; one `parent_copy` |
| F5 | Firewall → outside, with an egress-only upstream | forward `measured`; reverse `ingress_not_captured` |
| F6 | `exported = no` interface → VLAN host: forward as the VLAN's egress copy; reply, plus its parent copy | rule 4 accepts the reply and rejects the parent copy (`parent_copy` by 6(ii)) |
| F7 | `exported = no` interface → outside via NAT: only a post-NAT copy | sole-copy rule: the forward is counted from the translated copy and marked `counted_copy` |
| F8 | Inbound blocked on an egress-only upstream | forward `ingress_not_captured`; reverse `missing` (approved builder choice) |
| F9 | Two identical one-packet records (FIN/RST) in one datagram, and a pair identical across two datagrams | four rows; each joined record is counted |
| F10 | A VLAN not captured whose parent is captured | `parent_copies_only` |
| F11 | Untranslated forwarding between two exported interfaces, with 0, 1 and 2 records per packet | `forwarding_export_unsettled`, no bytes |
| F12 | A firewall-originated refused `out` leg | `not_exported_when_refused` |
| F13 | An index absent from the map | `interface_index_unmapped` |
| F14 | A reply with no forward record past the bound | `reverse_unconfirmed` |
| F15 | IPv6 firewall-originated traffic | rule 1 joins |
| F16 | Datagrams from five interleaved counters, one datagram dropped | one `netflow_sequence_gap` |
| F17 | A pre-de-NAT ingress record on an upstream captured on ingress (**synthesised; not observed**) | `translated_copy`, never joined to the second leg |

## C. Acceptance criteria

Every test runs on the PostgreSQL test instance, with a fake clock and NJ12's fixtures, through the real decoder and router.

- [ ] **NJ-AC1. Facts first.**
  - S5c, S6b and S6c exist in the survey, each with a 26.7.3 or `stable/26.7` URL. Where one is still `UNVERIFIED:`, its NJ1 fallback is built and named in the hand-back.
  - S11's pflog sub-fact carries no `UNVERIFIED:`.
  - PS-AC28's test exempts nothing by name.
- [ ] **NJ-AC2. OPNsense sources verified, and an unavailable source reported.**
  - Every endpoint that the `netflow` plugin adds for S5c and S6c is in its manifest with `survey_section` and `upstream_url`, and is cited in its header with the response fields it reads.
  - A 404, 403 or unparseable answer gives `exported = unknown` or `capture_configuration_unknown`, with the broker outcome in the availability. Nothing is guessed from records.
  - No mutating `netflow` action is called (BR-AC15).
- [ ] **NJ-AC3. Contract.**
  - The contract in code equals `docs/plugins.md` (A13), including `exported` and `parent_of`.
  - A grep finds no `observation_point` in `contract.go`, `schema.sql`, `internal/`, `plugins/` or `docs/`, outside *Vocabulary*'s RFC row.
- [ ] **NJ-AC4. Index map.**
  - Records are mapped with the map in force at reception, and a later map change does not re-map stored rows.
  - A `sysUptime` that goes backwards forces a read before the next mapping (request counter).
  - Index 0 and L get their roles; any other unknown index gets `unmapped`.
- [ ] **NJ-AC5. Rule B, table-driven.**
  - Each of rule 1 (both orientations), 2(a), 2(b) and 2(c) has a fixture that joins the expected leg.
  - A record that meets only a second leg never joins it.
  - Candidates pooled at the same instant give `ambiguous`.
- [ ] **NJ-AC6. Reverse check.**
  - F6's parent copy is rejected and classed `parent_copy`.
  - F3 accepts with 0 = L.
  - F14 stays `pending` until the bound, then becomes `reverse_unconfirmed`.
  - F1–F6 give identical rows whether ingested forward-first, reverse-first or interleaved (PS-AC34's method), and a second derivation writes nothing.
- [ ] **NJ-AC7. Copies.**
  - F1, F2 and F17 classify translated copies by each of the two signals separately.
  - F1, F3, F4 and F10 classify parent copies.
  - Uncounted copies appear in no family, no total, no `connection_measured_volume` figure and no unmatched slot.
  - F7's counted copy is counted exactly once and carries `counted_copy` in the row, in the dataset and in the figure's coverage object.
- [ ] **NJ-AC8. No deduplication.** F9 stores four rows, and the connection's figure sums every joined record.
- [ ] **NJ-AC9. Each direction counted once.**
  - For every fixture, each direction's bytes and packets equal an independent computation over the records that the fixture's provenance marks as counted.
  - A test fails if any record contributes to two directions, to two connections, or twice to one direction.
- [ ] **NJ-AC10. Named states, never silently zero.**
  - Each NJ5 state is produced by its fixture (F5, F8, F10, F11, F12, and `exported = no`).
  - None of these directions reports `measured` or `missing`.
  - Each state appears in the figure's coverage object of a `client_traffic_detail` answer and of an aggregate answer.
- [ ] **NJ-AC11. Unsettled forwarding.** F11's three variants each give `forwarding_export_unsettled`, with zero bytes and not covered.
- [ ] **NJ-AC12. Aggregates.**
  - At `1h`, `24h`, `7d` and `30d`, the logged columns equal the baseline (P-AC9), and the `netflow` columns equal an independent computation.
  - The unmatched family uses the new key.
  - The purge keeps the purged part's `netflow` figures.
  - The exit row follows NJ5's decided rule.
- [ ] **NJ-AC13. One reader, inlined.**
  - `connection_netflow` remains the only reader of `connection_measured_volume` (`TestNoStatementCopiesThePerLegCounting`).
  - E18's inlining check passes with the extended `netflow_parts`.
- [ ] **NJ-AC14. Widget measure.**
  - Under a seven-interface configuration where every counted connection is covered, `best_available` answers `netflow`.
  - With F5 in scope, it answers `logged` with `netflow_not_covering_window`, byte-identical to `logged` apart from the admitted differences.
  - An explicit `netflow` request serves the coverage object with the NJ5 counts.
  - A grep finds no `observation_point_not_exported` and no `captured_interfaces` in code, schema or docs.
- [ ] **NJ-AC15. Loss detection.**
  - F16 records one `netflow_sequence_gap` for the dropped datagram, and none for the five interleaved counters alone.
  - `loss_detectable` is 1 over those intervals.
- [ ] **NJ-AC16. Dead code removed.**
  - `flow_record_condition`, `DirectionAt`, the one-interface branch, `widget_flow_record_unmapped` and the `not_joinable` join state no longer exist (grep and catalogue inspection).
  - Each removal is listed in the hand-back.
- [ ] **NJ-AC17. Modularity.**
  - PS-AC48 passes with a test-only second provider, in its own directory, that reports `exported` and `parent_of`, with no shipped file edited.
  - PS-AC47's grep extends to the NJ4 and NJ5 code: no OPNsense endpoint, model field, netgraph term, IE name, device name or address literal.
- [ ] **NJ-AC18. Fixtures.**
  - Every NJ12 fixture passes X1 and carries `__provenance__`.
  - A test asserts that no fixture holds an address, identifier, device or kernel index from REPORT.md's configuration table or its Q2 table.
- [ ] **NJ-AC19. REPORT reproduced.**
  - The Builder runs the built join locally over the 16 October capture: `scratchpad/capture/netflow.bin` and `syslog.log`, with REPORT Q2's mapping supplied as a test-only index map.
  - The capture files are **never committed**.
  - The hand-back prints the outcome table (records and bytes for one leg, ambiguous, each copy kind and each unmatched reason) beside REPORT Q5's, and explains every difference.
- [ ] **NJ-AC20. Phase E holds.** E17, E18, E19 and E22, as NJ10 amends them, pass at both seeds, and the join statements are planned.
- [ ] **NJ-AC21. Budget.** `docker compose run --rm checks` stays within its 60 s `go test` budget. The hand-back prints the wall time that the new tests add.
- [ ] **NJ-AC22. Documents.**
  - NJ11's corrections are present.
  - The new *Vocabulary* rows exist, each with its reason.
  - F-AC5 holds for every new state, reason, role and copy kind.

## D. Live validation: LV-NJ2 (replaces LV-NJ)

LV-NJ2 runs after deployment, on the maintainer's firewall, with every interface captured as on 16 October 2026. **Nothing goes to `main` before it passes.**

1. **The configuration read.** The plugin's `getconfig` read through the broker is reported:
   - which interfaces are captured, and which are egress-only, notably the upstream;
   - the status of `wg0` and of the VLAN that exported nothing of its own;
   - `exported` and `parent_of` as the plugin derived them.
2. **The index map.** The `interfaces_info` map, including the loopback index, is compared with an empirical 5-tuple mapping recomputed by REPORT's method. Every high-confidence row agrees, or the difference is explained.
3. **Window length.** The window lasts at least 40 minutes, longer than the 1800 s active timeout.
4. **Outcome table.** Records and bytes, per outcome, per copy kind, per unmatched reason and per interface, are reported beside REPORT Q5. Excluding copies, the joined share is at least the baseline minus 5 points in both records and bytes (baseline 82.2 % / 92.3 %), or each shortfall is explained.
5. **Fallback rate.** The share of connection directions in each NJ5 state is reported, and so is the share of `best_available` answers that fall back to `logged` over the default canvas. The maintainer revisits question 5 with this figure.
6. **Double-count audit.**
   - Counted bytes per interface, against the `traffic/interface` deltas.
   - A query proving that no direction draws records of two copy kinds or from two exporters.
   - One NAT connection and one VLAN connection traced by hand, from legs to records to figures.
7. **Untranslated forwarding: observed only, no controlled test (maintainer, 16 October 2026).** On the maintainer's network, transfers between internal hosts are rare and cross an unmanaged switch, so they never reach the firewall. No test is staged. The report gives the count of `forwarding_export_unsettled` directions seen during the window, which is expected to be zero or near zero.
8. **Samples explained.** Every sample of `ambiguous`, `no_logged_leg`, `reverse_unconfirmed` and `missing` is explained, including REPORT's unexplained absences.

**LV-NJ2 passes** when every item is reported, every difference is named, and the maintainer has accepted the figures.

## E. Out of scope

- QinQ and nested VLANs, LAGG members, and a parent of a parent.
- Inferring an interface or a node from a record's content, timing or sequence counter.
- Deduplicating records by content.
- The rdr (port-forward) form on the upstream `in` leg. LV-NJ2 measures it.
- A join for exporters other than OPNsense's: only the seam is built.
- Re-joining stored records when the index map or the configuration changes.
- Writing anything to the firewall.

## F. Risks

1. **Missing ingress records count as covered.** REPORT could not explain some missing ingress records; they end `missing`, which counts as covered. If they are systematic, `best_available` overstates coverage. LV-NJ2 item 8 measures this.
2. **Policy routing.** `egressInterface` is the interface the route lookup chose. Under policy routing or gateway groups, the reverse check may reject legitimate replies (`reverse_unconfirmed`). LV-NJ2 reports the share.
3. **Inter-VLAN traffic.** Untranslated inter-VLAN traffic carries no `netflow` figure. No controlled test settles it: on the maintainer's network such traffic stays behind an unmanaged switch. An installation where it does cross the firewall sees it as `forwarding_export_unsettled`, stated rather than guessed.
4. **Unreliable parent copies.** They are never counted, so a VLAN without its own export stays unmeasured.
5. **F17 is synthesised.** A captured upstream ingress was never observed. If the maintainer later captures it, LV-NJ2 must run again.
6. **Renumbering without a restart.** Index renumbering is caught only by the `sysUptime` rule (NJ3).
7. **Join cost.** Candidate legs are read across several devices per group. Plans are checked at both seeds, but throughput at 1M is not measured (declined 11 October 2026).
8. **Missing NJ1 sources.** If the NJ1 sources do not exist, the fallbacks lower coverage, never correctness.

## G. ROADMAP.md, README.md and CLAUDE.md

- **`ROADMAP.md`, *Push sources*:** unchanged in substance.
- **README:** Phase F rewrites the NetFlow paragraph. Every interface may be captured; it names the states and the egress-only consequence; the Insight trade-off is removed.
- **`CLAUDE.md`:** its NetFlow sentence stays true, so no diff.

## H. Sentences that become obsolete

**In `specs/SPEC-backend-completion-plugins.md`:**
1. Decision 15b, lines 3635–3638.
2. Line 3621, "cannot be evaluated once two interfaces are captured".
3. PS17:
   - the "Core-written columns", lines 3669–3673;
   - the last sentence of line 3680.
4. PS18:
   - lines 3702–3709;
   - lines 3711–3713;
   - lines 3714–3717 (replaced by NJ7);
   - lines 3718–3721 (replaced by NJ5).
5. PS19:
   - rule 1, lines 3725–3729;
   - rule 2's "Same point" and its consistency table, lines 3731–3742;
   - rule 7's list and note, lines 3755–3763;
   - rule 9, lines 3767–3770.
6. PS20, lines 3772–3799 (replaced by NJ5).
7. PS21, lines 3807 and 3808.
8. PS22:
   - line 3814;
   - lines 3815–3822;
   - lines 3828–3830.
9. PS23, line 3839.
10. §C, lines 3846 and 3849.
11. §D:
    - PS-AC30(b), line 3866;
    - PS-AC32's last bullet, line 3872;
    - PS-AC36, lines 3876 and 3880;
    - PS-AC37, lines 3882–3883;
    - PS-AC19, line 3885;
    - PS-AC49, lines 3894–3899;
    - PS-AC50, lines 3900–3903;
    - PS-AC51's last bullet, line 3909;
    - PS-AC28's exemption, lines 3858 and 3967.
12. §E, line 3921, and LV-NJ, lines 3923–3927 (replaced by LV-NJ2).
13. §F, line 3933.
14. §G, risks 44, 49, 57 and 58 (lines 3940–3943).
15. The decided blocks:
    - line 3976's `captured_interfaces: N`;
    - line 3982's "no single Observation Point".
16. Phase E:
    - line 4182, the seed's "one Observation Point, plus one interval with two captured interfaces";
    - line 4183's `flow_record_condition`;
    - E22's `flow_record_condition`, line 4283.

**In `docs/opnsense-api-survey.md`:** lines 1367, 1368, 1373, 1478–1480, 1614–1629 and 1676–1678.

**In `docs/data-model.md`:** lines 184, 187, 188, 503, 2970–2975, 2985–2990, 2996–3016, 3026–3037, 3051–3052 and 3067–3068.

**In `docs/widget-api.md`:** lines 65, 116, 154, 166–167, 184, 188–193, 202–205, 216, 221, 337, 343 and 999–1000.

**In `docs/plugins.md`:** lines 1105, 1160–1161, 1171, 1436, 1487, 2061 and 2139–2150.

**In code:**
- `plugins/netflow/plugin.json:6`;
- `plugins/netflow/netflow.js:28-31`;
- `internal/store/netflow/flowrecord.go:23-31, 174-195`;
- `internal/collect/derive/flowrecord.go:28-33, 194-245, 285-296`;
- `internal/collect/derive/measuredvolume.go:11-20, 105-125`;
- `internal/widget/request/measure.go:37-57, 245-249, 286-289, 346-375`;
- `internal/store/schema.sql:3215, 3226, 3852-3872, 3894-3912, 3939-3964, 3978-3979, 3984-4005, 4020-4021, 4066-4068`;
- `internal/push/protocols/netflow/decoder.go:51-54`.

## I. Decided (maintainer, 16 October 2026): the planner's open questions

1. **Untranslated forwarding between two exported interfaces:** built now as `forwarding_export_unsettled`, with no bytes and not covered. There is no controlled test and no follow-up amendment: on the maintainer's network this traffic crosses an unmanaged switch and never reaches the firewall (maintainer, 16 October 2026).
2. **Loss detection:** several sequence counters are followed under one `source_id`. A gap is asserted when the counters exceed the number of exporting interfaces (NJ7).
3. **The exit row:** it carries the connection's `netflow` figure in both directions when the second leg passed, and `not_exported_when_refused` forward when it was refused.
4. **Copy counting:** when no exporter sees the first leg's form of a direction, its translated copy is counted, chosen from the configuration and marked `counted_copy`. Parent copies are never counted.
5. **`best_available`:** unchanged and strict this cycle. LV-NJ2 item 5 measures the fallback rate, and the maintainer revisits the rule with that figure.
