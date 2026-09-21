# Roadmap — opnview

Inter-segment visibility for a VLAN-segmented network behind an OPNsense
firewall. Eight steps, **explicit validation after each one**. We never move to
the next step without agreement. If an assumption turns out to be wrong, we say
so and amend this roadmap rather than quietly working around it.

## Rules that apply to every step

- **Zero hardcoded configuration.** No interface name, VLAN name, addressing
  plan or segment count in the code, the templates or the default values.
  Everything is discovered at runtime through the API.
- **No secrets in the repository.** The OPNsense URL, API key/secret and
  MaxMind licence key are entered in the web setup wizard, never in a file to
  edit before starting.
- **Read-only against the firewall.** No writes, no file read on the host:
  the authenticated REST API and nothing else. Where a firewall setting has to
  be turned on for a source to exist — the resolver query log, for instance —
  it is a manual step the user performs, documented in the README. The
  application never performs it.
- **Two outbound calls, not one more**: the firewall API (local network) and
  the MaxMind database download. No telemetry, no version check, no resource
  loaded from a CDN.
- **The observation limit is displayed, not hidden**: the application only
  sees what crosses the router.
- **Degrade, never guess.** Suricata may be absent, installed but stopped, or
  running on only some interfaces. The UI states which segments its alerts
  cover and which they do not.
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
|2|Data model and SQLite schema|Schema + model document|To do|
|3|Static HTML mockup — overview|HTML file, fake data|To do|
|4|Backend: collection and storage|Collectors + persistence + tests|To do|
|5|Backend: correlation, classification, matrix|Aggregations + HTTP API + tests|To do|
|6|Backend: alerts and device correlation|Alert model + API + tests|To do|
|7|Full frontend|7 screens served by the binary|To do|
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

Entities: segment (named zone, tunnels included), device, flow, blocked event,
DNS resolution, rule, domain attribution, alert, geo/ASN. There is no
TLS/HTTP observation entity: no source feeds it (step 1, finding 2), and the
schema carries nothing it cannot fill.

- East-west / north-south classification carried by the model.
- Manual segment labelling by the user, never inferred from a name.
- Randomised MAC (second hex digit even) marked as an unstable identity, not
  merged into phantom devices.
- **Every site-name attribution is inferred**, from resolver-lookup
  correlation — there is no second method to tell it apart from (step 1,
  finding 2). No provenance flag: a field with one possible value states
  nothing. What the model does carry is the lookup the name came from and the
  delay between that lookup and the flow, so an attribution can be judged.
- Alerts: signature, severity, source, destination, timestamp, joined to the
  device and segment models.
- Configurable retention, from a few hours to unlimited, with purge.
- Aggregate mode: volumes, segments, countries, operators — without domain
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

### Step 3 — Static HTML mockup of the overview

A single file, fake data, **mandatory stop for validation before writing the
frontend**.

UniFi Network as the aesthetic reference: light background by default, rounded
cards, generous spacing, a single accent colour, soft area charts, numbers
brought forward. Dark mode available, never the default. Banned:
"cyber-defence" aesthetics, walls of dense tables, empty panels with no
explanation.

The overview now carries recent alerts alongside recent blocked traffic, and
must degrade cleanly when Suricata is absent — an explanatory state, not an
empty panel.

**Validation**: explicit sign-off on the mockup.

### Step 4 — Backend: collection and storage

Go, single binary. Prerequisite: the containerised toolchain described under
**Development and test environment** above — nothing is installed on the host.

- OPNsense API client: authentication, pagination, graceful degradation when a
  source is missing.
- Runtime discovery of interfaces, subnets and rules.
- Detection of what is actually available: resolver flavour, DHCP flavour,
  Suricata present / stopped / running on which interfaces.
- Collection scheduler at the frequencies established in step 1.
- Continuous incremental ingestion of `eve.json` with a durable cursor.
- SQLite persistence, own history independent of the firewall's retention.
- First-run setup wizard: URL, API key/secret, MaxMind key.
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
  documented unconditionally, and an attribution rate is exposed per device.
  Never invent a domain: fall back to IP, country and operator.
- Device resolution through DHCP leases.
- Geo and ASN enrichment.
- HTTP API for the screens, period selector everywhere.

**Validation**: the numbers are correct, and the heuristic's attribution rate
is honest.

### Step 6 — Backend: alerts and device correlation

- Ingest Suricata alerts: signature, source, destination, timestamp. The API
  flattens the nested alert object to the signature string, so severity and
  category are resolved separately through `get_rule_info/<sid>` and cached.
- Join them to the device and segment models, so an alert names a machine
  rather than an address.
- Aggregations for the Alerts screen: over time, by device, by segment, by
  signature.
- Behave correctly with no Suricata at all: the feature is absent and says so,
  it does not fail.

**Validation**: an alert raised on the firewall shows up attributed to the
right device and segment.

### Step 7 — Full frontend

The seven screens, served by the binary, no exotic build chain, fonts and
scripts included in the binary.

1. Overview — segments, matrix, east-west vs north-south, recent blocked
   traffic and recent alerts.
2. Matrix — full screen, clickable, filterable; each cell opens the detail
   (IPs, ports, rules, timestamps).
3. Segment — devices, destinations, denials.
4. Device — volume over time, domains, countries, operators, denials, alerts.
5. Blocked — timeline, by rule, by source.
6. Alerts — timeline, by device, by segment, by signature.
7. Map — destinations by country and operator, detail on click.

Explicit in-UI statements: observation-point limit, site names inferred by
correlation and the limits of that heuristic, the attribution rate, which
segments Suricata's alerts cover, randomised MACs.

**Validation**: full walkthrough of the seven screens on real data.

### Step 8 — Install, packaging, documentation

- `ct/install.sh` aligned with `community-scripts/ProxmoxVE` conventions:
  unprivileged Debian LXC, sensible defaults, advanced mode (CPU, RAM, disk,
  storage, bridge, VLAN, DHCP or static IP), systemd service, URL printed at
  the end, update by re-running the same command without data loss. Asks
  **nothing** about OPNsense or MaxMind.
- `docker-compose.yml` for non-Proxmox users.
- README: problem solved, screenshots, one-line install, how to obtain the
  OPNsense API and MaxMind keys, **how to enable the resolver query log**,
  what an internal-interface IDS is and is not for, an
  honest limitations section, a factual section on the data collected and what
  can be inferred from it, the two outbound calls. Licensed under MIT
  (`LICENSE`).

**Validation**: successful install from scratch on a PVE node.

## Superseded decisions

Recorded so they do not resurface.

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
- **Six screens became seven**: the Alerts screen is new, and the Device
  screen gains its alerts.
- **Seven steps became eight**: alerts and device correlation are their own
  backend step, between the matrix and the frontend. Step numbers shifted
  accordingly — the frontend is now step 7 and packaging step 8.
