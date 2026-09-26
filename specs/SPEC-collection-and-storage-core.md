# SPEC — Step 4A: the collection and storage core

Status: APPROVED
Cycle kind: standard

## What this cycle is

`ROADMAP.md` step 4 is split into three. This spec is the first.

| Cycle | Kind | Contents |
|---|---|---|
| **4A — this spec** | standard | module layout; migration runner; SQLite store; OPNsense client; runtime discovery; availability probes and provider activation; the five collectors; the scheduler; the `eve.json` cursor; sampled measurement (per-pair volume and firewall health); the retention purge; the gap table; the IPv6 seed; the first tests |
| 4B | interface | the first account, sign-in, and the settings surface where the firewall URL, the API key/secret, the MaxMind key and the theme are entered |
| 4C | standard | MaxMind GeoLite2 acquisition and refresh, and the `geo_asn` provider |

**4A cannot collect from a real firewall.** Credentials enter the product
through the interface, which is 4B, so 4A is exercised entirely against fakes.
The roadmap's "real collection against an OPNsense" validation belongs to the
end of 4B. This is a consequence of the maintainer's decision that `opnview`
has its own accounts and is configured from its own interface; it is stated here
so nobody writes an acceptance criterion that pretends otherwise.

## Decisions taken

Four by the maintainer, on 2026-09-26:

1. **SQLite driver: `modernc.org/sqlite`, pure Go.** `CGO_ENABLED` stays 0, so
   step 8 needs no compiler on the LXC. Its transitive dependency set is read
   from the module graph and reported **in full, before it is added** — the
   global build rule, and the reason it exists.
2. **No credential ingress in 4A.** No environment variable, no `configure`
   subcommand, no file. 4A runs against fakes.
3. **Secrets at rest**, settled now so 4B does not re-litigate it, and recorded
   in `ROADMAP.md`: Argon2id for a password, authenticated encryption for the
   OPNsense secret and the MaxMind key, the key in a file beside the database.
   The limit is stated rather than dressed up — it protects a copied or leaked
   database, not a compromised host, and nothing can while the service starts
   without a human. **4A stores none of them and creates no table for them**:
   see decision 4.
4. **A collection gap is a row in the schema**, and there are no migrations.
   Nothing is deployed, nobody has data, so numbered files, a `schema_version`
   table and a runner are machinery for a problem that does not exist. The
   schema is one file, edited in place. **Migrations begin the day the product
   runs somewhere with data worth keeping** — that file becomes the baseline and
   the first real migration is the one after it.

   The accounts and secrets tables the account decision implies are not added
   here either: 4A has no sign-in and no credential ingress, so they would be
   machinery built to receive a capability that does not exist. They arrive with
   4B, in the same schema file.

Eleven settled without the maintainer, each from a rule already written down:

5. **The retention purge runs in 4A.** `sql/purge.sql` and
   `setting.retention_seconds` exist; without the loop the database grows
   without bound from the first day. One scheduler loop.
6. **Provider selection reads the firewall's own configuration** — the enabled
   flags and configured ranges — never a preference order invented here. When
   two implementations of one kind are both reachable and the firewall's own
   configuration does not separate them, neither is activated and the ambiguity
   is recorded.
7. **`interface.status` and `interface.enabled` are stored verbatim.** The
   schema is already shaped for it. Normalising them against a vocabulary
   OPNsense has not published would be inventing one.
8. **Dnsmasq leases are a first-class source.** `/api/dnsmasq/leases/search`
   returns structured JSON — `hwaddr`, `address`, `hostname`, `client_id`,
   `expire`, the vendor, and the interface under all three of its names. The
   survey's claim that Dnsmasq needs a free-text parser is wrong for leases,
   verified against a live firewall on 2026-09-27. ISC dhcpd is gone (404).
   **Dnsmasq as a resolver query source** is still detected and reported rather
   than parsed: that grammar remains unseen.
9. **The five poll intervals are constants**, each written beside the survey
   section that establishes it, overridable by an optional `setting` row.
10. **The IPv6 seed takes a bound parameter**, so the existing counts and their
    determinism comparisons stay legible.
11. **The database path is a required `--data-dir`.** No default that would
    pre-empt step 8's LXC layout.
12. **ARP and NDP are client-identity sources and are collected.**
    `/api/diagnostics/interface/get_arp` and `get_ndp` carry MAC, address,
    interface and vendor. On a firewall whose DHCP is unreadable they are the
    only identity there is, and the survey named neither.
13. **The per-pair collector samples; it does not read an aggregate.**
    `/api/diagnostics/traffic/top/<interface names>` is a live rate snapshot and
    `netflow/aggregate` does not exist, so `opnview` builds its own history.
    A verified trap: the endpoint takes interface **names** (`lan`, `opt1`), and
    device names return `[]` with HTTP 200 — a wrong argument looks exactly like
    an absence of traffic.
14. **Sampled measurement is a first-class collector, and one table carries
    it.** The API answers for CPU, memory, temperature, disk, uptime and
    per-interface packet and byte counters — so gaps **G9** and **G10** are
    closed on the API side; only the schema lacked a place to put a gauge. And
    decision 13 established that per-pair volume has to be sampled too, because
    no aggregate exists upstream. Both are the same shape: a value, a subject, a
    unit, an instant. One table serves both, written once, and the firewall
    health canvas stops being an empty quarter of the product's navigation.
15. **The filter log's digest is a de-duplication key, not a cursor.** Passing
    `digest=` changes nothing, measured twice. The log is fast enough that the
    poll must be sized per installation rather than fixed: pull the most recent
    page, discard what `__digest__` says was seen, and record a gap when the
    oldest line returned is newer than the newest line stored.

## The package layout

Six packages, `internal/buildinfo` included. Nothing exists for a later step.

| Package | Responsibility |
|---|---|
| `cmd/opnview` | the single entry point: flags, wiring, signals to a cancelled context, exit codes |
| `internal/config` | the only reader of configuration: the `setting` table and the defaults |
| `internal/store` | opening SQLite, applying the embedded schema, and the typed write paths |
| `internal/opnsense` | the HTTP client, the endpoint registry with its survey citations, and the refusal to issue a mutating command |
| `internal/collect` | discovery, availability probes, provider activation, the five collectors, the cursor, the scheduler, the purge |
| `internal/buildinfo` | unchanged |

Not created: `internal/web`, `internal/api`, `internal/geo`, `internal/auth`,
`internal/correlate`, `internal/aggregate`, and any interface seam beyond what
the five collectors concretely need.

## Acceptance criteria

Every one is verifiable inside the container, by
`docker compose run --rm checks`, `docker compose run --rm schema-checks`, or
inspection of a named file. **None assumes a live firewall or a host Go.**

### Toolchain, layout, vocabulary

- [ ] **AC1** — `checks` exits 0 (`gofmt -l .` silent, vet, build, test) and
      `schema-checks` exits 0 with no assertion removed or relaxed.
- [ ] **AC2** — `go list ./...` prints exactly the six packages above.
- [ ] **AC3** — `go.mod` still declares `go 1.27.0` with no `toolchain`
      directive, so `GOTOOLCHAIN=local` cannot trigger a download.
- [ ] **AC4** — after a full run, `git status --porcelain` prints nothing: no
      test writes a database or a `-wal`/`-shm` file into the tree. Tests use
      `t.TempDir()`.
- [ ] **AC5** — `grep -rniE '\bsegments?\b' cmd internal` returns nothing;
      `device` names only the network device and `client` only the machine; every
      identifier taken from an endpoint carries that endpoint's field name
      unchanged; every string is English.
- [ ] **AC6** — the dependency report: `go list -m all` in the cycle's hand-back,
      every module named with the reason it is present, and the driver's
      transitive set read before it was added rather than discovered from errors.

### The client and the endpoints

- [ ] **AC11** — every endpoint path in the registry appears verbatim in
      `docs/opnsense-api-survey.md`, asserted by a test, each entry carrying the
      survey section and upstream URL that establishes it. An invented endpoint
      cannot compile and pass.
- [ ] **AC12** — the client refuses, before issuing anything, any mutating
      command (`set`, `add`, `del`, `toggle`, `reconfigure`, `start`, `stop`,
      `restart`, `clear`, `drop_alert_log`, `del_lease`); the fake transport sees
      no request. A recording fake asserts every path ever requested is in the
      registry.
- [ ] **AC13** — the resolver collector issues `search_queries` as a POST with
      `Content-Type: application/json` and `timeStart`/`timeEnd` as JSON
      integers; a test fails on a query string, a form body or a string bound.
- [ ] **AC14** — **the resolver window is ignored by the firewall, and the
      collector knows it.** Verified 2026-09-27: a 5-minute and a 24-hour request
      both returned the same ~410-second span, `total` is always `1000`, and
      pages walk further back. The collector treats the endpoint as a ring buffer
      of the most recent lookups, pages it, and **never presents what it read as
      coverage of the window it asked for**. A fake reproducing that behaviour
      must not make the collector claim a complete window; a gap row is written
      when the oldest row paged is newer than the newest row stored.
- [ ] **AC15** — HTTP 200 with an empty body, HTTP 200 with
      `{"result":"failed"}`, 404 and 401/403 each produce a distinct outcome and
      none is recorded as "no data". Table-driven, per source.
- [ ] **AC16** — for each source, a fake in the state the survey documents as
      *present but disabled* moves exactly one `source_availability` row to the
      state the survey names, with `probe` and `checked_at` set, and writes no
      data row. **An unavailable source is reported, never worked around.**
- [ ] **AC17** — the response fixtures are labelled in their own files as
      **synthesised from the survey**, naming the section each field list comes
      from, and are not presented as captures from a firewall.

### Timestamps, identity, de-duplication

- [ ] **AC18** — timestamp normalisation is table-driven over the three shapes
      the data model names, including the December-to-January boundary for the
      year-less filter-log form, and passes with `TZ` set to a non-UTC zone.
- [ ] **AC19** — ingesting the same page twice produces the same row count as
      once, for `flow` (including the echoed digest record), `security_event` on
      `(provider_id, provider_event_key)`, and `pair_volume_observation` where
      the two directions collapse with `endpoint_low <= endpoint_high`.
      **`dns_resolution` cannot use `lookup_uuid`**: it is `null` on every row
      the firewall returns, verified 2026-09-27. The key is composed from the
      row's own content, and the builder states what it chose and why.
- [ ] **AC20** — every `flow` row carries `traffic_scope` derived from interface
      membership alone; a device not in `interface_map` is written with
      `interface_lookup_state = 'not_found'` rather than dropped, and likewise a
      `rid` matching no rule.
- [ ] **AC21** — the `eve.json` cursor resumes without loss or double-count
      across shifting offsets, and a rotation whose watermarked file is gone is
      recorded as `rotation_state = 'lost'` **and** as a gap row, not as silence.

### The gap table

- [ ] **AC32** — the schema gains one table for a sampled measurement — subject,
      measure, unit, value, instant — and it is the only one added for this.
      Both the per-pair sampler and the firewall-health sampler write to it, and
      a test proves a reading of each kind round-trips.
- [ ] **AC33** — the health sampler reads CPU, memory, temperature, disk, uptime
      and per-interface counters, and a source that does not answer on a given
      firewall is recorded in `source_availability` rather than written as a
      zero. A test drives a fake in which temperature is absent and asserts no
      row is written for it and one availability row moves.
- [ ] **AC34** — the per-pair sampler calls
      `/api/diagnostics/traffic/top/<interface names>` with **names**, and a test
      fails if it ever sends a device name — the argument whose wrong form
      returns `[]` with HTTP 200 and is indistinguishable from silence.
- [ ] **AC22** — the schema gains one table recording a collection gap: the
      source, the interval missing, when it was detected, and why. No other
      table is added — nothing for a capability 4A does not have.
      `sql/schema-checks.sh` gains assertions for it and still exits 0.
- [ ] **AC23** — a filter-log pass whose digest is not in the returned window
      writes a gap row naming the interval it could not cover; so does a lost eve
      rotation. Neither is smoothed over and neither is written as a zero.

### Zero hardcoded configuration, two outbound calls

- [ ] **AC24** — no interface identifier, description, VLAN name, VLAN tag, IP
      address, CIDR or interface count is a literal in `cmd/` or `internal/`, and
      nothing branches on the *text* of a name, description or label. Nothing
      derives an interface's nature, a client's owner or a blocklist's purpose
      from what it is called.
- [ ] **AC25** — **one outbound destination in 4A, enforced at one chokepoint.**
      `internal/opnsense` is the only package constructing an HTTP request. A
      test installs a transport failing any host that is not the configured
      firewall, runs discovery and all five collectors, and sees no failure. A
      second test greps for `http.Get`, `http.Post`, `http.DefaultClient` and
      absolute `http(s)://` literals: the count is zero.
- [ ] **AC26** — no URL, key or secret appears in the repository, a default, a
      fixture, a compose file or a committed log. Fakes use `httptest` and
      credentials generated in the test.

### Lifecycle and purge

- [ ] **AC27** — the five loops run at the surveyed cadences, each written beside
      the survey section justifying it; with an injected clock, a collector
      returning an error does not stop the other four.
- [ ] **AC28** — the purge loop applies `sql/purge.sql` against
      `setting.retention_seconds`, and a test proves rows older than the window
      are removed and newer ones are not.
- [ ] **AC29** — the run loop stops on context cancellation within a bounded
      deadline, closing the database cleanly (`PRAGMA integrity_check` = `ok`, no
      hot journal), and `cmd/opnview` wires `SIGTERM` and `SIGINT` to it.

### The IPv6 seed

- [ ] **AC30** — `sql/seed.sql` produces both families: `flow.ip_version` takes 4
      and 6, and at least one `dns_resolution`, one `pair_volume_observation`,
      one `client` and one `security_event` carries an IPv6 address — synthesised
      from a counter, with no address literal and no addressing-plan meaning.
- [ ] **AC31** — `sql/schema-checks.sh` asserts, for each of the seven screen
      queries, at least one row derived from IPv6 and one from IPv4, and that the
      no-scan query-plan criterion still holds with both present. The seed stays
      deterministic and the alternative-count run still passes.

## For maintainer validation — not acceptance criteria

No agent can perform these and none is written as a criterion an agent could
fake. They run at the end of **4B**, when credentials can enter:

1. Real collection: rows appearing in `flow`, `dhcp_lease`, `dns_resolution`,
   `security_event`, `pair_volume_observation`, `interface`, `interface_map`,
   `rule`.
2. The survey's `UNVERIFIED:` markers — the ISC lease contract, the Unbound and
   Dnsmasq query-log grammars, the eve-log volume estimates — confirmed or
   corrected in `docs/opnsense-api-survey.md`.
3. The real encodings of `interface.status` and `interface.enabled`, recorded
   then.
4. Whether a 10 s filter-log poll keeps the digest inside the returned window on
   the maintainer's ruleset, and how often a gap is recorded.
5. Which providers detection activates, and whether that is what he expected.

## Out of scope

- **The first account, sign-in and the settings surface** — 4B, `Cycle kind:
  interface`. No HTTP server, no template, no HTML, no CSS, no JavaScript in 4A.
- **MaxMind acquisition and the map's disabled state** — 4C. No `geo_asn` row is
  written in 4A and no second outbound destination exists in its code.
- **Correlation and aggregation** — `domain_attribution`, the aggregate
  families, the matrix, the HTTP API: step 5. 4A writes `flow.traffic_scope`
  because the column is `NOT NULL`, and computes no aggregate.
- **Severity and category resolution** through `get_rule_info/<sid>`: step 6.
  `security_event` rows carry no inline severity, as the schema checks assert.
- **Any schema change beyond the gap table.** A column 4A turns out to need is
  a finding to report and a decision to take, not something to add in passing.
- **Dnsmasq query-log parsing and the ISC lease endpoint** — detected and
  reported, per decision 8.
- **Everything the container cannot prove**: the systemd unit, start ordering,
  unprivileged-LXC uid mapping, `ct/install.sh`, update by re-running the
  installer. All step 8, and a green container run is evidence for none of them.

## Risks

1. **Fixtures are not evidence.** Every collector test runs against a fake built
   from a document. If the survey is wrong about a field, an envelope or a
   status code, the tests pass and the collection is wrong. AC17 keeps that
   honest; only 4B's validation closes it.
2. **The silently-truncated resolver window** is, in the survey's words, the most
   dangerous failure mode in it. AC13 and AC14 are the only thing between the
   product and ingesting the thousand-most-recent rows as though they were a
   window.
3. **The first third-party dependency.** `go.mod` becomes non-empty and `go.sum`
   appears. AC6 is the guard, and the global rule it comes from was written after
   a day lost to exactly this.
4. **Level-3 client identity** needs an address-validity start the filter log
   does not carry. Getting it wrong merges two machines into one phantom client.
   The builder proposes the substitute and states it in its report.
5. **`interface` is a Go keyword**, so the discovered entity cannot be a
   lowercase identifier of that name — and the substitute must not be a coinage.
   The vocabulary rule cost this project three steps.
