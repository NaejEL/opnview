# SPEC — OPNsense REST API survey (step 1, documentation-only)

Status: APPROVED

## Context

The repository contains no application code: only `README.md`, `ROADMAP.md`,
`CLAUDE.md`, `LICENSE`, `.gitignore`, and the software-factory scaffolding
under `.claude/`, `ci/` and `.vscode/`. There is no `go.mod`, no `docs/`
directory, and `specs/` holds only `.gitkeep`.

`ROADMAP.md` step 1 ("OPNsense API exploration") requires a verified findings
document before any code is written, and makes Suricata `eve.json` the open
question of the step. Steps 2 and 4 — the SQLite schema, the collection
scheduler frequencies, the durable `eve.json` ingestion cursor, the detection
of resolver flavour, DHCP flavour and Suricata state — all depend on facts this
survey must establish. Without it the Builder of step 4 would have to guess
endpoint paths, which the project rules forbid.

This cycle therefore produces **documentation only**.

## Decisions taken with the maintainer

1. **Pinned version.** The survey is written for **OPNsense 26.7.3_11-amd64**
   (FreeBSD 15.1-RELEASE-p3, OpenSSL 3.5.8), the version the maintainer runs.
   The document states this version in *Scope and method* and flags any claim
   known to differ on earlier releases.
2. **Admissible sources.** `docs.opnsense.org` **and** the official OPNsense
   GitHub organisation (`github.com/opnsense/...` — model/form XML, controller
   sources) are both acceptable authoritative citations. A GitHub citation must
   point at the file, on a branch or tag matching the pinned release series.
3. **ROADMAP.md.** This cycle **does** flip the step-1 row of the step table in
   `ROADMAP.md` from `To do` to `Done`. That single-cell edit is the only change
   permitted outside `docs/`.
4. **No live instance.** The survey is purely documentary; no call is made
   against a real firewall. Empirical confirmation happens at step 4.
5. **Volume estimate.** Section 4(c) gives a **table parameterised by device
   count** (10 / 50 / 200 devices), not a single figure.
6. **DoT/DoH blind spot** is recorded in *Gaps and alternatives*.
7. **Length.** A tight reference document, roughly 300–500 lines: endpoint
   tables and short prose. Field names are given; full example JSON payloads are
   not required and are discouraged.

## Scope

Create one new file, `docs/opnsense-api-survey.md`, creating `docs/` in the
process; and edit exactly one cell of `ROADMAP.md` (the step-1 status).

No Go package, module, endpoint, table or screen is created in this cycle.

The document is written in English, in Markdown, with the following top-level
(`##`) sections, in this order:

1. **Scope and method** — the pinned OPNsense version; how each claim was
   established (documentation page, model/form XML or controller source in the
   official OPNsense GitHub organisation, or the built-in API reference); and
   the honesty rule: anything not confirmed by an official source is labelled
   unverified rather than asserted.
2. **Authentication and conventions** — API key/secret authentication, base URL
   shape, the `/api/<module>/<controller>/<command>` convention, HTTP status
   and error-body behaviour, and which endpoints are `GET` versus `POST`-with-a-
   body search calls. Every `POST` must be flagged explicitly as a read-only
   search call, so step 4 does not trip the "no writes to the firewall" rule.
3. **Data source 1 — Filter logs**
4. **Data source 2 — Suricata `eve.json`**
5. **Data source 3 — NetFlow / Insight**
6. **Data source 4 — DHCP leases**
7. **Data source 5 — Resolver DNS lookups**
8. **Runtime discovery** — interfaces with descriptions and subnets; firewall
   rules with labels and identifiers; resolver flavour (Unbound vs Dnsmasq);
   active DHCP server; Suricata state (absent / installed but stopped /
   running, and on which interfaces).
9. **Polling plan** — one consolidated table.
10. **Gaps and alternatives** — everything not reachable through the REST API,
    stated plainly, each with a proposed alternative that stays inside the
    project rules.
11. **Impact on the data model and the collectors** — what steps 2 and 4 must
    take from this survey.
12. **Project commands** — see AC18.
13. **References** — the full list of cited URLs.

Each of sections 3 to 7 carries the same fixed sub-structure, so the document
can be checked mechanically:

- **Endpoints** — exact paths, HTTP method, parameters (pagination, cursor,
  offset, `searchPhrase`, timestamp filters).
- **Response shape** — the field names actually returned and their meaning, at
  the level of detail needed to write a Go struct in step 4.
- **Retention on the firewall** — how long the data survives, what governs it
  (log rotation, ring buffer, database size, configuration setting), and
  whether the application must keep its own history to outlive it.
- **Sustainable polling frequency** — a recommended interval with the reasoning
  behind it.
- **Degradation** — what the API does when the source is absent, disabled or
  empty, and how the application detects that case.
- **Sources** — the documentation or repository URL(s) backing that subsection.

Section 4 (Suricata `eve.json`) additionally answers, under three titled
sub-headings, the three questions the roadmap declares open:

- **(a) Reachability and incremental consumption** — whether `eve.json` is
  exposed through the API at all and in what form (log-query endpoint,
  streaming endpoint, paginated search); and whether it can be consumed
  incrementally through a cursor, offset or timestamp filter, or only re-read
  whole. If incremental consumption is not natively supported, the document
  says so and describes the de-duplication strategy the application must
  implement itself.
- **(b) Enabling the `dns`, `tls` and `http` event types** — whether the web UI
  exposes them, whether a configuration file must be edited, and whether the
  setting survives a firewall upgrade. Written as numbered user-facing steps,
  since it will be copied into the README at step 8.
- **(c) Volume** — a table parameterised by device count (10 / 50 / 200)
  giving GB/day and GB/month with `dns` and `tls` enabled, the assumptions
  behind it (events per device per day, average event size), the retention
  window the firewall disk could hold, a conclusion on whether continuous
  consumption keeps the log off that disk, and what happens if the application
  is offline for a period.

## Acceptance criteria

- [ ] AC1 — `docs/opnsense-api-survey.md` exists, is valid Markdown, and is
      written entirely in English; no French word appears in it.
- [ ] AC2 — The document contains, as `##` headings, all thirteen sections
      listed in *Scope*, in that order.
- [ ] AC3 — Each of the five data-source sections contains all six required
      sub-headings: **Endpoints**, **Response shape**, **Retention on the
      firewall**, **Sustainable polling frequency**, **Degradation**,
      **Sources**. A missing sub-heading in any one section fails this
      criterion.
- [ ] AC4 — The filter-log section's *Response shape* names the field carrying
      each of: interface, action, rule identity, source address, destination
      address, source port, destination port, protocol, timestamp — or states
      explicitly, per missing field, that the API does not return it and what
      the consequence is.
- [ ] AC5 — The NetFlow / Insight section documents at least one endpoint
      returning per-address-pair volume data, names the byte and packet fields,
      and states the time resolution and the aggregation periods the firewall
      itself keeps.
- [ ] AC6 — The DHCP section documents lease retrieval including the hostname
      field and the MAC address field, and states which DHCP backends its
      endpoint(s) cover; if different backends need different endpoints, each
      backend has its own endpoint path documented.
- [ ] AC7 — The resolver section documents how per-client DNS lookups are
      retrieved for **both** Unbound and Dnsmasq, or states explicitly, for
      whichever is not reachable, that it is not reachable and what the fallback
      is.
- [ ] AC8 — Section 4 contains the three sub-headings (a), (b), (c), and each
      gives a definite answer rather than deferring it. (a) states in one
      unambiguous sentence whether incremental consumption is natively possible
      and, if not, what the application must do instead. (b) gives a numbered,
      reproducible user procedure and states whether the setting survives an
      upgrade. (c) contains the device-count table (10 / 50 / 200 rows) with
      GB/day and GB/month, and the assumptions it rests on.
- [ ] AC9 — Section 4 states in writing that enabling the extra Suricata event
      types is a manual operation performed by the user and that `opnview`
      never performs it, consistent with the read-only rule in `ROADMAP.md`.
- [ ] AC10 — The *Runtime discovery* section documents, each with its own
      endpoint path and response fields: (i) enumerating interfaces with their
      user-given description and subnet; (ii) enumerating firewall rules with
      their label/description and identifier; (iii) determining whether the
      resolver is Unbound or Dnsmasq; (iv) determining which DHCP server is
      active; (v) distinguishing Suricata absent, installed but stopped, and
      running — including how to obtain the list of interfaces it actually runs
      on.
- [ ] AC11 — No interface name, VLAN name, segment name, CIDR or IP address
      from any specific network appears as a configuration value. Illustrative
      payload fragments are permitted only when labelled as examples and
      accompanied by a statement that these values are discovered at runtime and
      never hardcoded.
- [ ] AC12 — Every endpoint path documented anywhere in the file is followed, in
      the same section, by at least one citation as a full `https://` URL to an
      official OPNsense source (`docs.opnsense.org` or the official OPNsense
      GitHub organisation). An endpoint with no citation fails this criterion.
- [ ] AC13 — The *References* section lists every URL cited in the body, each
      with the title of the page or file it points to, and contains no URL that
      is not also cited in the body.
- [ ] AC14 — Every claim the author could not confirm against an official source
      is marked with the greppable marker `UNVERIFIED:`, and *Scope and method*
      explains what that marker means. A claim presented as fact without a
      citation and without the marker fails this criterion.
- [ ] AC15 — *Gaps and alternatives* is non-empty: it either lists the
      sources/fields not reachable through the REST API with a proposed
      alternative for each, or states explicitly that all five sources and all
      required fields proved reachable. It includes the DNS-over-TLS /
      DNS-over-HTTPS blind spot affecting data source 5. No proposed alternative
      anywhere in the document involves reading a file on the firewall, SSH, or
      any access other than the authenticated REST API.
- [ ] AC16 — *Polling plan* contains a table with one row per data source, each
      giving the recommended interval, the incremental mechanism (cursor,
      offset, timestamp filter, or full re-read with de-duplication) and a
      one-line justification. The intervals match the per-source *Sustainable
      polling frequency* subsections.
- [ ] AC17 — The document states, for each of the five sources, how the
      application detects that the source is unavailable, and that this must be
      reported as a distinct state — never silently reported as an absence of
      traffic.
- [ ] AC18 — The *Project commands* section states verbatim that `gofmt -l .`,
      `go vet ./...`, `go build ./...` and `go test ./...` are **N/A** for this
      cycle because the repository contains no Go module and this cycle creates
      none, and instructs the Verifier to record them as N/A rather than as
      failures. It also states the check that does apply: the acceptance
      criteria above are verified by reading `docs/opnsense-api-survey.md`.
- [ ] AC19 — `git status --porcelain` after the cycle shows exactly two paths:
      the added `docs/opnsense-api-survey.md` and the modified `ROADMAP.md`
      (plus this spec file itself). No `go.mod`, no `.go` file, no other file is
      created or modified.
- [ ] AC20 — The step-1 row of the step table in `ROADMAP.md` reads `Done` in
      its Status column, and no other line of `ROADMAP.md` is changed.
- [ ] AC21 — The document states the observation-point limit (the application
      only sees what crosses the router) where it bears on the volume figures
      derived from NetFlow and the filter logs.
- [ ] AC22 — *Scope and method* names OPNsense 26.7.3 as the pinned version.
- [ ] AC23 — The document's body is between 250 and 700 lines, consistent with
      the agreed "tight reference" format.

## Out of scope

- Any Go code, `go.mod`, package layout, struct definition or function.
- Any SQLite schema, migration or table design — that is step 2.
- Any HTTP endpoint, template, asset or screen — steps 5 and 7.
- Copying the Suricata procedure into `README.md` — that is step 8.
- Any live call against a real OPNsense instance.
- MaxMind, GeoLite2, geolocation and ASN enrichment — not an OPNsense source.
- Choosing the collection library, the HTTP client or any dependency.
- Modifying `README.md`, `CLAUDE.md` or the factory scaffolding.

## Risks

- **Documentation drift.** API paths and payload fields change between
  releases and the documentation is not always in step with the code. Mitigated
  by pinning 26.7.3 and by preferring model/controller sources on a matching
  branch when the prose documentation is silent.
- **Plugin-dependent endpoints.** NetFlow/Insight, Suricata and Dnsmasq are not
  all part of the base system in the same way; an endpoint documented as
  available may require a plugin the user has not installed. The *Degradation*
  subsections absorb this, but the survey may over-estimate availability.
- **`eve.json` may prove unreachable through the API**, or reachable only in a
  form that cannot be consumed incrementally. That would invalidate the durable
  ingestion cursor planned in step 2 and force a de-duplication design instead.
  The survey must surface this outcome rather than soften it.
- **The volume table is an estimate.** It rests on assumptions about per-device
  DNS/TLS activity that cannot be verified without a running installation; if
  wrong by an order of magnitude, the disk-sizing conclusion changes. The
  assumptions are therefore stated inline.
- **Unverifiable retention claims.** Retention is often governed by local
  configuration and disk size rather than by a documented constant; some entries
  may have to carry `UNVERIFIED:`.
- **Research network access.** Reading the OPNsense documentation and
  repositories at authoring time is research activity by the Builder, not an
  outbound call made by the application; the survey should say so explicitly so
  the Verifier does not read it as a third outbound call.
- **Verifier tooling.** The Verifier is normally instructed to run the Go
  commands and treat a missing toolchain as critical. This cycle depends on
  AC18 and on the orchestrator honouring the N/A instruction.
