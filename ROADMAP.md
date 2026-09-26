# Roadmap — opnview

Inter-interface visibility for a VLAN-segmented network behind an OPNsense
firewall. Eight steps, **explicit validation after each one**. We never move to
the next step without agreement. If an assumption turns out to be wrong, we say
so and amend this roadmap rather than quietly working around it.

## Rules that apply to every step

- **Zero hardcoded configuration.** No interface name, VLAN name, addressing
  plan or interface count in the code, the templates or the default values.
  Everything is discovered at runtime through the API.
- **No secrets in the repository.** The OPNsense URL, API key/secret and
  MaxMind licence key are entered in the interface, never in a file to edit
  before starting.
- **`opnview` has its own accounts.** On first start the user creates a login
  and a password; everything else, including the firewall's URL and key, is
  configured from the interface once signed in.
  - A **password** is hashed with Argon2id, a per-user salt, and the algorithm
    parameters stored beside the hash so they can be raised later. It is never
    read back.
  - The **OPNsense secret and the MaxMind key are not passwords.** They are
    sent to those services on every call, so they are encrypted at rest with
    authenticated encryption and decrypted to be used. Hashing them would make
    the product unable to work.
  - The **encryption key is a file beside the database**, created on first
    start with restrictive permissions. **The limit is stated rather than
    dressed up:** this protects a database that is copied, backed up, sent by
    mistake or committed by accident, because the key file does not travel with
    it. It does not protect against someone who already has the machine, who
    reads both. No scheme can, while the service starts and collects without a
    human — which it must, or collection stops at every reboot until somebody
    signs in. That trade was taken deliberately. The README's limitations
    section says so in those words.
- **Read-only against the firewall.** No writes, no file read on the host:
  the authenticated REST API and nothing else. Where a firewall setting has to
  be turned on for a source to exist — the resolver query log, for instance —
  it is a manual step the user performs, documented in the README. The
  application never performs it.
- **Two outbound calls, not one more**: the firewall API (local network) and
  the MaxMind database download. No telemetry, no version check, no resource
  loaded from a CDN.
- **The observation limit is documented, not printed**: the application only
  sees what crosses the router, so every figure is a lower bound.
- **Degrade, never guess.** Suricata may be absent, installed but stopped, or
  running on only some interfaces. Which interfaces its alerts cover is a fact
  about the data, documented here; the interface does not recite it.
- **One service, installed and updated by a single command, with no external
  data store to provision. Storage engines are embedded libraries, not
  servers.** This replaces the earlier "single Go binary" rule, which was an
  inherited assumption rather than a reasoned constraint and was on the point
  of deciding the telemetry storage engine for no stated reason. What the old
  rule actually protected is the install story, and that is what the new one
  keeps. **Permitted**: SQLite, unchanged and still the store for everything
  the current schema holds; DuckDB, which is a library rather than a server and
  which OPNsense itself already ships behind the Unbound query report; and a
  time-series store written in pure Go, on the same test. **Forbidden**:
  PostgreSQL, TimescaleDB, InfluxDB, VictoriaMetrics — not on their merits, but
  because each would make someone installing a network-visibility tool into an
  unprivileged LXC administer a separate database. Several storage engines is
  not several processes: the install, update, systemd and backup stories are
  unchanged. `README.md` and `CLAUDE.md` point at this bullet rather than
  restating it.
- **The vocabulary is OPNsense's, and that of every product `opnview` reads. It
  never invents a term for something those products already name.** When the
  right term is not known it is researched — in
  `docs/opnsense-api-survey.md`, on `docs.opnsense.org`, or in the product's own
  documentation — or the maintainer is asked. It is never guessed and never
  embroidered to save a lookup. Where a product genuinely has no word for
  something `opnview` needs, that is stated explicitly, the term is chosen
  deliberately, and the reason is recorded next to it.

  **This rule exists because it was broken silently and nobody noticed for
  three steps.** `segment` was invented in the very first commit, before any
  research existed, and was never reconciled with what OPNsense calls the
  thing: an **interface**. The table gave itself away — `interface_identifier`,
  `device_name`, `discovered_description`, three columns describing an
  interface under a name OPNsense does not use — while
  `docs/opnsense-api-survey.md` had been carrying the real vocabulary
  (`get_interface_names`, `descr`, `link_type`) since step 1. `device` was worse
  still: it meant the network device on one table and a client machine on
  another, so one word carried two meanings and neither matched OPNsense's use
  of it. Both are fixed; the term-by-term mapping, with the source establishing
  each, is in `docs/data-model.md` under *Vocabulary*.

- **Everything is written in English** — code, docs, UI, commits. See
  `CLAUDE.md`.

## Data sources

Five, all read through the OPNsense REST API:

|Source|Feeds|Required|
|---|---|---|
|Filter logs|matrix, blocked traffic|yes|
|Suricata `eve.json` (`alert` only)|alerts|no|
|NetFlow / Insight|volumes per address pair, daily|yes|
|DHCP leases|hostnames and MACs|yes|
|Resolver DNS lookups (Unbound or Dnsmasq)|site names (sole source)|yes|

The Suricata and resolver rows read differently from the original plan: see
the step-1 findings below.

## Development and test environment

No line of this project is built or tested on the host OS, and **no Go
toolchain is installed on the host**. The toolchain lives in a Debian
`trixie-slim` container defined by `Dockerfile` and `docker-compose.yml`, with
the Go version pinned and checksum-verified. The production target is an
unprivileged Debian LXC, and a Windows host masks exactly the bug classes
`opnview` will hit: file mode and umask on the SQLite file and its `-wal` /
`-shm` companions, filesystem case sensitivity, `SIGTERM` handling for the
future systemd unit, and normalising the OPNsense filter-log timestamps, which
carry neither year nor timezone.

The four project commands — `gofmt -l .`, `go vet ./...`, `go build ./...`,
`go test ./...` — run through a single canonical invocation, issued from the
repository root:

```
docker compose run --rm checks
```

That string is **identical in PowerShell and in bash**: it carries no quoting,
and it needs no knowledge of the host `PATH`. `docker compose run --rm dev
bash` opens an interactive shell in the same environment, and
`.devcontainer/devcontainer.json` reuses the same `dev` service so VS Code
"Reopen in Container" gives the editor that toolchain too. The Go module cache,
the Go build cache and the SQLite data directory live in named volumes mounted
outside the bind-mounted working tree, so a database and its WAL companions
never land in the repository and never suffer Windows bind-mount locking.

**What this environment does not prove.** Docker provides the Debian userland
and the Linux kernel, and nothing more. It does **not** exercise, and must
never be read as evidence for:

1. the systemd unit;
2. service start ordering;
3. unprivileged-LXC uid mapping;
4. `ct/install.sh`;
5. update by re-running the installer without data loss.

All five belong to step 8 and can only be validated on a real LXC. A green
container run says nothing about deployment.

## Steps

|#|Step|Deliverable|Status|
|---|---|---|---|
|1|OPNsense API exploration|Verified findings document|Done|
|2|Data model and SQLite schema|Schema + model document|Done|
|3|Static HTML mockup — overview|HTML file for a representative canvas, fake data|To do|
|4|Backend: collection and storage|Collectors + persistence + tests|To do|
|5|Backend: correlation, classification, matrix|Aggregations + HTTP API + tests|To do|
|6|Backend: alerts and client correlation|Alert model + API + tests|To do|
|7|Full frontend|Canvases, widgets, dashboard import/export|To do|
|8|Install, packaging, documentation|`ct/install.sh`, compose, README|To do|

### Step 1 — OPNsense API exploration

No code. Verify **against the official documentation** which endpoints really
expose the five sources, and in what shape. For each one: exact endpoints,
shape of the data, retention on the firewall, polling frequency it can sustain
without being loaded. If a source is not reachable through the API: say so, and
propose an alternative — never work around it by reading files on the firewall.

- Filter logs — interface, action, rule, source, destination, ports.
- NetFlow / Insight — volumes per address pair.
- DHCP leases — hostnames and MACs.
- Resolver DNS lookups — domain names per client. The resolver may be Unbound
  or Dnsmasq, and the DHCP server likewise: state how to detect which is
  active.
- **Suricata `eve.json`** — the open question of this step. Three things to
  settle:
  1. **Reachability.** Is `eve.json` exposed through the API, and in what
     form — a log-query endpoint, a streaming endpoint, a paginated search?
     Can it be consumed incrementally (cursor, offset, timestamp filter)
     rather than re-read whole?
  2. **Enabling the event types.** OPNsense logs only alerts by default.
     Determine whether the web UI exposes the `dns` / `tls` / `http` event
     types, whether it requires editing a configuration file, and whether the
     setting survives firewall upgrades. The procedure goes in the README:
     the user will have to follow it.
  3. **Volume.** Logging every DNS lookup and every TLS handshake of a whole
     network produces far more than alerts. Estimate it, and confirm that the
     application can consume the log continuously rather than letting it pile
     up on the firewall, whose disk is not sized for that.

Also in scope: discovering interfaces with their descriptions and subnets, and
discovering rules with their labels.

**Validation**: the report is credible and every endpoint is sourced.

**Outcome**: `docs/opnsense-api-survey.md`, pinned to OPNsense 26.7.3, every
endpoint cited. Three findings invalidate assumptions made above, and are
carried into the steps that follow:

1. **Suricata `dns` events do not exist.** The IDS model exposes only `http`
   and `tls` under `eveLog`; there is no `dns` output type to enable, in the
   UI or anywhere else.
2. **`tls` and `http` events can be written but not read back.** The only API
   endpoint that opens `eve.json` returns records carrying a top-level `alert`
   key and silently discards every other event type, and the generic log
   endpoint cannot open a file not named `.log`. Suricata is therefore an
   **alert source only** — it cannot name sites.
3. **Per-address-pair volume exists only at daily resolution, kept 62 days.**
   No sub-daily per-pair data exists on the firewall, so the 1 h and 24 h
   matrix periods cannot be served from Insight.

A fourth finding is a trap rather than a limit: the Unbound query endpoint
honours its time window only when the bounds arrive as JSON integers in the
request body. Any other call form returns the 1000 most recent records with
HTTP 200 and no diagnostic — silent data loss unless the collector asserts
that the rows it gets back fall inside the window it asked for.

### Step 2 — Data model and SQLite schema

Entities: interface (a VLAN, a physical link or a tunnel the firewall
terminates), client, owner, flow, blocked event,
DNS resolution, rule, domain attribution, alert, geo/ASN. There is no
TLS/HTTP observation entity: no source feeds it (step 1, finding 2), and the
schema carries nothing it cannot fill.

- East-west / north-south classification carried by the model.
- Manual interface labelling by the user, never inferred from a name.
- **Ownership likewise: assigned by the user, never inferred.** A person is an
  `owner` row and a machine points at one. The firewall does not know who owns
  what, so nothing derives an owner from a hostname, a MAC prefix or a vendor
  hint. A client with no owner assigned is the normal case and stays visible in
  any per-person view, in an explicit unassigned bucket.
- Randomised MAC (second hex digit even) marked as an unstable identity, not
  merged into phantom clients.
- **Every site-name attribution is inferred**, from resolver-lookup
  correlation — there is no second method to tell it apart from (step 1,
  finding 2). No provenance flag: a field with one possible value states
  nothing. What the model does carry is the lookup the name came from and the
  delay between that lookup and the flow, so an attribution can be judged.
- Alerts: signature, severity, source, destination, timestamp, joined to the
  client and interface models.
- Configurable retention, from a few hours to unlimited, with purge.
- Aggregate mode: volumes, interfaces, countries, operators — without domain
  names.
- Pre-computed aggregates for the 1 h / 24 h / 7 d / 30 d periods. These are
  the **primary store, not a cache over Insight**: per-pair data on the
  firewall is daily-only, and per-source data at 300 s is kept one hour.
- **Source availability is a modelled state**, not an absence of rows: per
  source, reachable / present-but-disabled / unavailable, with a timestamp and
  the probe that determined it. Every screen reads it.
- A durable ingestion cursor for `eve.json`: a per-`file_id` byte-offset
  watermark over an alert-only feed, with rotation detection, so a restart
  neither loses events nor double-counts them.

**Validation**: the schema answers all seven screens without pathological
queries.

**Outcome**: the migrations, `sql/seed.sql`, the seven representative queries
in `sql/queries/screens.sql` and `docs/data-model.md`. Step 3's widget
catalogue subsequently identified gaps between the model and the
questions the widgets ask; they are tabulated in `docs/widget-catalogue.md`,
and each is a future migration. **Two have since been closed in the migrations
themselves**, which are edited in place because nothing is deployed: G1, the
blocklist that refused a resolver lookup — its **name** observed from the API
and its **purpose** assigned by the user, never inferred from the name — and
G11, the per-person aggregates that let a per-person question outlive the
`flow` horizon. **Eleven remain open**, two of them new: G12, that nothing in
the model classifies a flow as an application, and G13, that no interface
address and no address history is stored. G9 and G10 — firewall health and
telemetry — are a whole data category the model was never designed to hold and
the step-1 survey never looked at, and they are deliberately left to their own
research cycle after the mockup.

### Step 3 — Static HTML mockup of a representative canvas

A single file, fake data, **mandatory stop for validation before writing the
frontend**. Its subject is a **representative canvas** — a named board composed
from widgets of `docs/widget-catalogue.md` — and not a fixed overview screen,
because the product has no fixed screens.

**The visual language is angular, dense and high-contrast.** The maintainer's
industrial palette is the reference: a `0.25rem` corner radius, system fonts
only, and a layout of a fixed top bar, a collapsible sidebar and a centred main
column. Cloudflare Radar is the compositional reference — a left rail, a scoped
header carrying the selectors, and angular cards each with a one-line
explanation under its title. Homarr is the structural reference for how tiles
are arranged, moved and resized, **its rounding explicitly excepted**. Generous
spacing, a single accent colour and numbers brought forward are retained from
the original brief.

**Charts show the resolution the data actually has.** No curve smoothing, and
no silent downsampling that can hide a single-sample spike — where a series is
aggregated to fit the pixels available, the widget says so. Area fills survive
only as a means of separating stacked series, and the line on top of a fill is
drawn through its real points. A tooltip on a stacked chart lists the full
breakdown at the hovered instant rather than the top series only. Grafana is
the reference for this, and for the degree of per-series rendering control a
widget should expose.

**Theme.** On first launch the theme follows the operating system's preference.
The user can override it afterwards, and the override persists. The industrial
palette is the default; Tokyo Night, Dracula, Nord, Rosé Pine and Catppuccin
are offered as named options at the values published on the maintainer's own
site, https://lequellec.xyz, reproduced in `docs/ui-references.md` — that site,
not each palette's upstream project, is authoritative for these five names.
Nord's upstream project publishes no light variant, but the maintainer's site
defines one, so Nord is light-capable here and no fallback is to be built.

Banned, unchanged: "cyber-defence" aesthetics, walls of dense tables, and empty
panels with no explanation.

The canvas carries recent alerts alongside recent blocked traffic, and must
degrade cleanly when Suricata is absent — an explanatory state, not an empty
panel.

The binding references for this step are `docs/ui-references.md`,
`docs/widget-catalogue.md` and `docs/dashboard-format.md`.

**Validation**: explicit sign-off on the mockup.

### Step 4 — Backend: collection and storage

Go. One service, installed and updated by a single command, with no external
data store to provision — see the storage rule under **Rules that apply to
every step** above. Prerequisite: the containerised toolchain described under
**Development and test environment** above — nothing is installed on the host.

- OPNsense API client: authentication, pagination, graceful degradation when a
  source is missing.
- Runtime discovery of interfaces, subnets and rules.
- Detection of what is actually available: resolver flavour, DHCP flavour,
  Suricata present / stopped / running on which interfaces.
- Collection scheduler at the frequencies established in step 1.
- Continuous incremental ingestion of `eve.json` with a durable cursor.
- SQLite persistence, own history independent of the firewall's retention.
- **The migration runner wraps each migration file in its own transaction.**
  The step-2 DDL files carry no `BEGIN` / `COMMIT`: verified at step 2, a file
  interrupted halfway leaves a half-created schema with no `schema_version`
  row, and re-running it then aborts on an object that already exists — a
  database unrecoverable without manual intervention. The runner, not the SQL,
  owns that guarantee.
- **Extend `sql/seed.sql` to IPv6.** It currently produces IPv4 rows only.
  Nothing in the schema interprets an address family today, so step 2 passed,
  but no IPv6 row has ever been exercised through the seven screen queries.
- First-run setup: creating the first account, then the firewall URL, the API
  key/secret, the MaxMind key and the theme — the theme defaulting to the
  operating system's preference and changeable at any time from the interface
  rather than only at setup. **This is where credentials enter the product, so
  nothing before it can collect from a real firewall.**
- MaxMind GeoLite2 City and ASN databases: downloaded on first start,
  refreshed automatically, never embedded. Without a key the map is disabled
  with a clear message and everything else keeps working.
- First `*_test.go` files, standard `testing` library.

**Validation**: real collection against an OPNsense, data in the database.

### Step 5 — Backend: correlation, classification, matrix

- Source × destination matrix: volume, allowed connections, blocked
  connections, matching rules by label. The 1 h and 24 h periods are computed
  from `opnview`'s own history; only 7 d and 30 d can lean on the firewall's
  daily per-pair aggregate.
- East-west / north-south classification, presented separately.
- **Site names, sole path on 26.7**: correlating resolver lookups with
  subsequent flows. Suricata cannot serve this (step 1, finding 2), so the
  heuristic is no longer a fallback — it is the only path. Its limits —
  client-side caching, shared CDNs, DNS-over-TLS and DNS-over-HTTPS — are
  documented unconditionally, and an attribution rate is exposed per client.
  Never invent a domain: fall back to IP, country and operator.
- Client resolution through DHCP leases.
- Geo and ASN enrichment.
- HTTP API for the widget catalogue: **one endpoint per widget type**, taking
  that widget's declared parameters — period, scope, interface, client and the
  rest — as documented in `docs/widget-catalogue.md`, and returning both the
  data and the source-availability state each widget must render. A canvas is
  served by issuing one request per widget. **The period is a parameter of a
  widget rather than a property of a screen**, because two widgets on one
  canvas will legitimately show different periods. A multi-series endpoint
  accepts a list of series and returns a list of series, each carrying its own
  availability state, so one missing source degrades one line rather than the
  whole chart.

**Validation**: the numbers are correct, and the heuristic's attribution rate
is honest.

### Step 6 — Backend: alerts and client correlation

- Ingest Suricata alerts: signature, source, destination, timestamp. The API
  flattens the nested alert object to the signature string, so severity and
  category are resolved separately through `get_rule_info/<sid>` and cached.
- Join them to the client and interface models, so an alert names a machine
  rather than an address.
- Aggregations for the Alerts screen: over time, by client, by interface, by
  signature.
- Behave correctly with no Suricata at all: the feature is absent and says so,
  it does not fail.

**Validation**: an alert raised on the firewall shows up attributed to the
right client and interface.

### Step 7 — Full frontend

The canvas frontend, served by the binary, no exotic build chain, fonts and
scripts included in the binary. **There is no fixed screen list and no view
reachable only through a menu inside a menu**: the user creates **named
canvases**, composes each from widgets, and moves between them with **tabs
rather than menus**.

#### The frontend is modular, and that is a requirement, not a preference

The step-3 mockup is a single file, which is acceptable for a mockup and is
**not** acceptable for the product. The real frontend is composed of parts a
person who is not us can add:

- **a language is a file** — every user-visible string comes from a catalogue,
  none is a literal in a template, a handler or a script, and the user switches
  language in the interface;
- **a theme is a file** — the palettes ship as data of the same kind a third
  party can add, not as blocks in a stylesheet;
- **a widget is a directory** — its manifest, its query and its renderer live
  together, and adding one is dropping a directory in, not editing the product.

The test this rule has to pass: **somebody who has never seen the source can add
a language, a theme or a widget easily and naturally**, from the documentation
alone. If adding one means editing a file that ships with the product, the rule
is not met.

Two consequences to design rather than discover:

- The service reads these parts **from a data directory at runtime**, beside the
  defaults embedded in the binary. That does not weaken *one service, installed
  and updated by a single command, with no external data store to provision* —
  the parts are files the service reads, not a store to provision — but the
  directory, its precedence over the embedded defaults and its reload behaviour
  are part of this step.
- `docs/widget-catalogue.md`'s six fields per entry stop being prose for a human
  and become **the widget manifest's schema**. That is what makes a widget a
  directory rather than a patch.

- The widget catalogue of `docs/widget-catalogue.md`, implemented as placeable
  widgets that can be added, moved and resized on a grid.
- **The connection tree answers where a chosen subject goes preferentially** —
  an interface, a client or an owner, picked by the reader, with what it reaches
  ranked. The step-3 mockup rooted it at the firewall instead, which is true of
  everything on the page and so tells the reader nothing; that root came from a
  research document's reading of a screenshot, not from any requirement.
- Named canvases with tabs, each carrying the widgets the user put on it.
- The dashboard file format of `docs/dashboard-format.md`, with import, export
  and the two repair surfaces — a highlighting JSON/YAML editor and a no-code
  selector populated from what this installation actually has.
- The theme system: the industrial palette by default, the five named palettes
  as options, the theme following the operating system on first launch with a
  persisting override — each palette a file of the same kind a third party adds.
- The string catalogue and the language picker, with English as the source
  language and no user-visible literal anywhere in the code.

**The seven queries in `sql/queries/screens.sql` remain authoritative and are
not deleted.** They are the query work this step was really carrying, and every
widget states which of them it reuses, adapts or replaces.

**No explanatory copy in the interface.** A title names, a figure states, a unit
qualifies, a degraded state is named in a word. Nothing explains the interface to
its reader.

#### What the step-3 mockup proved the interface has to be

Each of these was found by using that mockup, and each was a defect in it. They
are requirements here, not observations:

- **The page does not rebuild under the reader's hands.** A live view refreshes
  by reconciling, never by replacing, and never touches a subtree holding the
  focus or a selection. In the mockup a two-second rebuild meant a name could not
  be typed at ordinary speed, a MAC could not be selected and copied, and an
  unapplied edit was silently discarded — three critical failures, one cause.
- **Inspecting is not editing.** Every name and every figure — a client row, a
  rule, a domain, a matrix cell, a signature, a tree node — opens a detail, and
  that detail is a pivot rather than a dead end. In the mockup the only way to
  look at a machine was to repoint a widget's reference, which was permanent and
  had no undo.
- **Scope belongs to the canvas, beside the period.** Interface, client and
  owner, with a per-widget override that says so. Scoping through a gear, one
  widget at a time, is not a filter.
- **Navigation stays on screen.** Tabs and the rail do not scroll away from a
  page that is several screens tall.
- **A destructive action confirms, or can be undone.** Removing a person took
  four dated assignments with it, on one click, with neither.
- **Every control offered changes what is drawn**, or it is not offered. More
  than half the mockup's declared parameter surface was decoration.
- **A chooser may explain; a widget may not.** The copy rule above governs the
  canvas. A picker that lists twenty-four widget names and nothing else gives the
  reader nothing to choose on.
- **A figure the interface cannot show is not shown as a fragment.** No truncated
  address, no clipped label, no drawing whose names are dropped — a shortened
  address reads as a different address.

**Validation**: full walkthrough of a representative set of canvases on real
data.

### Step 8 — Install, packaging, documentation

- `ct/install.sh` aligned with `community-scripts/ProxmoxVE` conventions:
  unprivileged Debian LXC, sensible defaults, advanced mode (CPU, RAM, disk,
  storage, bridge, VLAN, DHCP or static IP), systemd service, URL printed at
  the end, update by re-running the same command without data loss. Asks
  **nothing** about OPNsense or MaxMind.
- `docker-compose.yml` for non-Proxmox users.
- README: problem solved, screenshots of **representative canvases with the
  dashboard files that produced them committed as examples** — which has a
  second benefit, since those files exercise import on every clone — one-line
  install, how to obtain the
  OPNsense API and MaxMind keys, **how to enable the resolver query log**,
  what an internal-interface IDS is and is not for, an
  honest limitations section, a factual section on the data collected and what
  can be inferred from it, the two outbound calls. Licensed under MIT
  (`LICENSE`).

**Validation**: successful install from scratch on a PVE node.

## Superseded decisions

Recorded so they do not resurface.

Correction of 2026-09-23, applied directly by the maintainer rather than
through a cycle, after inspection found the schema naming an OPNsense object
with a word OPNsense does not use:

- **`segment` is replaced by `interface`, everywhere.** The entity, its
  columns, the queries, the seed, the purge, the schema checks and every
  document. `segment` was invented before any research existed; OPNsense calls
  the thing an interface and always did.
- **`device` now means only the network device**, as OPNsense means it. The
  machine on the network is a `client`, the word Kea, Dnsmasq and Unbound all
  use for it. The DHCP option-61 identifier keeps its own name,
  `dhcp_lease.dhcp_client_id`, so it cannot be confused with the foreign key.
- **An `owner` entity is added**, because a per-person view needs a notion of a
  person and the model had none. It is `opnview`'s own term, deliberately: no
  product it reads has one.
- Nothing was deployed, so both renames were made **in the migrations in
  place**, exactly as the provider-neutral change was.

Revision of 2026-09-23, after the interface and dashboard-format research
(`docs/ui-references.md`, `docs/widget-catalogue.md`,
`docs/dashboard-format.md`):

- **The seven fixed screens are replaced by named canvases.** Step 7 listed
  Overview, Matrix, Interface, Client, Blocked, Alerts and Map as screens
  reachable through a menu. The product has no fixed screens: the user composes
  named canvases from widgets and moves between them with tabs. The seven
  queries in `sql/queries/screens.sql` are untouched and stay authoritative.
- **The rule that made a light theme the default, and dark merely available,
  is replaced.** The theme follows the operating system on first launch, the
  override persists, the industrial palette is the default and the five named
  palettes are options. The old rule contradicted the default palette's own
  code, which follows the OS and has no manual override at all.
- **UniFi Network is no longer the aesthetic reference, and cards are not
  rounded.** The language is angular: the industrial palette's `0.25rem`
  radius and system fonts, Cloudflare Radar for composition, Homarr for
  structure with its rounding excepted. The three bans survive unchanged.
- **The softened, smoothed chart treatment step 3 used to ask for is
  withdrawn.** The maintainer's charting reference is
  Grafana, and his reason is that it is the only one without the over-smoothed
  quality. Charts show the resolution the data actually has. Area fills survive
  only to separate stacked series, with the line through real points.
- **"HTTP API for the screens, period selector everywhere" is replaced** by one
  endpoint per widget type, with the period as a widget parameter.
- **"Single Go binary" is replaced** by one service installed and updated by a
  single command, with no external data store to provision and storage engines
  as embedded libraries. The permitted and forbidden lists are under *Rules
  that apply to every step*.

Revision of 2026-09-21, after the step-1 API survey
(`docs/opnsense-api-survey.md`, OPNsense 26.7.3):

- **Suricata is no longer a site-name source.** `dns` events do not exist and
  `tls` / `http` events cannot be read back through the API. The revision
  recorded below, which demoted the DNS × flow heuristic to a fallback, is
  itself superseded: that heuristic is the only site-name path on 26.7, and
  the README's limitations section applies to it unconditionally again.
  Suricata stays a data source, for alerts alone — the Alerts screen and the
  eight-step plan are unchanged.
- **The matrix cannot lean on Insight for its short periods.** Per-address-pair
  volume is daily-only, so `opnview`'s own history is the primary store rather
  than a cache.

Revision of 2026-09-21, after Suricata was added as a data source:

- **The DNS × flow heuristic is no longer the primary way to name sites.** It
  is demoted to a fallback for installations without Suricata. It stays
  implemented; it is no longer the main path, and the README's limitations
  section applies to it conditionally rather than unconditionally.
- **Four data sources became five**, and the five-source list above replaces
  the earlier one.
- **Six screens became seven**: the Alerts screen is new, and the Client
  screen gains its alerts.
- **Seven steps became eight**: alerts and client correlation are their own
  backend step, between the matrix and the frontend. Step numbers shifted
  accordingly — the frontend is now step 7 and packaging step 8.
