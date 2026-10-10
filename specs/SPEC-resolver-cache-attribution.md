# SPEC — Site names from the resolver's cache, host names from local data, the processor figure, exact-path cleanup, and `synchronous=NORMAL`

Status: APPROVED
Cycle kind: standard

All decisions below were taken by the maintainer on 9 October 2026.

## Context

The code base is at `8a9e870`: step 5A, plus the live corrections of
`specs/SPEC-step-5a-live-corrections.md` and
`specs/SPEC-test-budget-and-two-live-defects.md`. This cycle carries out two
decisions that the live-corrections spec deferred:

- **decision 8**: attribution from the resolver cache, with the exact evidence
  first, the existing timing rule as the fallback, and the method recorded on
  each attribution;
- **decision 10**: host names that have no lease, resolved through Unbound
  local data.

It also carries three items the maintainer added.

### What exists today

- **Attribution** (`internal/store/attribute.go`, `internal/store/derive.sql`,
  `docs/data-model.md` *`domain_attribution`*).
  - A flow is attributed only when the eligible lookups by the same client in
    `[observed_at − attribution_max_delay_seconds, observed_at]` name exactly
    one distinct domain. The window defaults to 5 s.
  - The rule is strict because `/api/unbound/overview/search_queries` returns
    no answer address (survey, *Data source 5*).
- **Provenance.** `domain_attribution` deliberately has **no method column**.
  Five places assert this, and each must be amended by name:
  - 5A Scope D3;
  - `docs/data-model.md`, line 47 ("There is no second method on OPNsense
    26.7") and the `domain_attribution` section;
  - `sql/schema-checks.sh`, line 669 (`AC18 domain_attribution carries no
    provenance or method column`);
  - README, *Site names are inferred, not observed*;
  - 5A *Deferred to 5B* ("a constant attribution-method field").
- **The resolver seam.** It is the `dns_lookup` kind (`internal/collect/provider.go`,
  `registry.go`), implemented by `dnslookup_unbound.go` (readable) and
  `dnslookup_dnsmasq.go`. Availability is one `source_availability` row per
  provider.
- **Host names.** `ResolveLeaseHostname` and `ResolveLoggedHostnames` resolve a
  logged host name through DHCP leases only. A host with no lease (H5) stays
  `unknown_hostname`.
- **Endpoints.** The registry (`internal/opnsense/endpoints.go`) does not hold
  `/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata`
  or `/api/diagnostics/cpu_usage/stream`. 5A AC4 limits new firewall endpoints
  to `/api/routes/gateway/status` and `/api/diagnostics/system/systemSwap`.
- **The processor reading** (`internal/collect/flowvolume_insight.go`,
  lines 68–69 and 407–416).
  - It reads `/api/diagnostics/activity/getActivity` and looks for invented
    keys (`unverifiedCPUKeys = {"cpu","cpu_usage","used","total"}`).
  - Its fixture, `testdata/activity.json` (`{"cpu": 17.5}`), is synthesised.
  - It has never produced a value on a live firewall.
- **The glob delete.** `sql/schema-checks.sh`, line 295, runs `rm -f
  "$DATA_DIR"/schema-checks-*.db …` on the shared `/data` volume. Its 13
  database paths are declared at lines 52–68.
- **Synchronous mode.** `internal/store/store.go` opens SQLite with WAL but
  sets no `synchronous`. The driver default in WAL mode is `FULL`.

### Live facts

Measured by the orchestrator, read-only, on 9 October 2026. Shapes and
proportions only.

1. **`GET /api/unbound/diagnostics/dumpcache`** answers HTTP 200 in under a
   second.
   - Shape: `{"status":"ok","data":[{"host","ttl","type","rrtype","value"}, …]}`.
   - `ttl` is a string of seconds remaining.
   - `type` is the DNS **class** (`IN`).
   - By rrtype: A about a third, AAAA about a third, NS about a quarter, then
     RRSIG, CNAME, SOA, NSEC3, DS, HTTPS and NSEC.
   - On `opnsense/core` master it is `Unbound/Api/DiagnosticsController.php`
     `dumpcacheAction`, running configd `unbound dumpcache`. The sibling
     `listlocaldataAction` runs `unbound listlocaldata`.
2. **Attribution rate today.** About a fifth to under a third of eligible flows
   are named. The main loss is several distinct domains looked up by the same
   client inside the 5 s window.
3. **Processor endpoints.**
   - `systemResources` returns only memory.
   - `GET /api/diagnostics/cpu_usage/stream` is a server-sent event stream.
     Each event is `event: message` / `data: {"total","user","nice","sys","intr","idle"}`,
     about one per second, and the stream never ends.
   - `GET /api/diagnostics/activity/get_activity` returns
     `{"headers":[…],"details":[…]}`. One line of `headers` is top's `CPU:`
     text, and `details` holds the whole process list.
   - `GET /api/diagnostics/cpu_usage/get_c_p_u_type` returns the processor
     model.
4. **Deletion.** The maintainer's rule is that deletions are by exact path
   only.
5. **`PRAGMA synchronous=NORMAL`** is approved for the live database in WAL
   mode. Measured in the container, a commit drops from about 5 ms to about
   13 µs.

## Decisions (9 October 2026)

1. **The cache read is a new provider kind,** with provider rows `unbound` and
   `dnsmasq` and its own availability rows.
   - The `provider.kind` `CHECK` and `docs/architecture.md` are amended.
   - The kind's name is researched first; Unbound's own word is "cache"
     (`unbound-control dump_cache`).
2. **The exact window** is the evidence's own validity: a lookup whose
   answer's coverage interval, along the whole chain, contains both the lookup
   and the flow. It is capped by a new validated setting with a **default of
   3600 s**.
3. **Contradicting evidence.** When cache evidence covers the single timing
   candidate's lookup but does not contain the flow's destination, **no
   attribution** is made.
4. **Intervals.** The cache is read every **60 s** and local data every
   **300 s**. Both are validated settings.
5. **Names.**
   - Method values: `resolver_cache_answer` and `lookup_timing`.
   - New `client_resolution` value: `local_data_hostname`.

   These are opnview's own terms, recorded in *Vocabulary* with the reason.
6. **Local-data A/AAAA records count as exact address evidence** for lookups
   whose `answer_source` is `Local-data`.
7. **`no_domains`.** The new data is stored as lookups are, and withheld at the
   API (5B), as 5A D4 decided.
8. **Processor source:** `/api/diagnostics/cpu_usage/stream`, read with a
   bounded read, subject to the verification in A.
9. **The `mktemp` directory removals** (`rm -rf "$WORK"`,
   `rm -rf "$WORK/statements"`) are kept. They are exact paths the script
   created, with no glob, and a comment says why.

## Scope

Everything applies to the working tree on top of `8a9e870`.

### Process rules for the whole cycle

- **Language and vocabulary.**
  - Everything is written in English.
  - Every term comes from OPNsense, Unbound (`unbound-control(8)`,
    `unbound.conf(5)`), the DNS RFCs or SQLite, and is researched and cited.
    Otherwise it is opnview's own and is recorded in *Vocabulary*, with the
    count sentence updated.
  - Field names stay as the endpoint names them. The dump's `type` is the
    class and is never relabelled as the record type.
- **Deletion and git.**
  - Never delete with a wildcard or glob. Delete only an exact path, after
    showing it to the maintainer.
  - Never run a destructive command in the background.
  - No git command moves or discards work.
- **Committing.** Nothing is committed before the live validation passes and
  the maintainer accepts it.
- **No installation data in the repository.** No host name, domain, address,
  processor model or count from the installation enters a committed file.
  Fixtures are synthesised:
  - names from RFC 2606 / RFC 6761, addresses from RFC 5737 / RFC 3849;
  - counts taken as parameters, and both IP versions;
  - a `__provenance__` block saying they are synthesised.
- **Sources.** Every OPNsense fact cites `opnsense/core` **26.7.3**: the
  controller, the configd action file, the script, and the `ACL.xml` page. The
  citation goes in the code, the `Endpoint` registry entry and the survey.
- **Modularity** (`ROADMAP.md`). Everything Unbound-specific lives in Unbound
  implementation files behind a seam. A resolver without the capability
  reports a named state. Adding a resolver means adding one file and one
  registry row.
- **Schema.** `schema.sql` is edited in place (live-corrections decision 12).
  The orchestrator dumps the live database first and shows the exact paths.
- **The test budget.** The everyday `checks` suite stays under 60 s. No test
  is moved to `predeployment` without the maintainer's agreement.

### A. Verify the new endpoints first, and record them

Each finding goes in `docs/opnsense-api-survey.md`. Cover
`/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata`,
`/api/diagnostics/cpu_usage/stream` and the registered
`/api/diagnostics/activity/getActivity`:

1. the controller action at 26.7.3, and its HTTP method;
2. the configd action and its script:
   - whether it wraps `unbound-control dump_cache` / `list_local_data`;
   - its output shape and any filtering;
3. the ACL privilege as named in `ACL.xml`;
4. every other action on the same controller. Any mutating action, such as a
   cache flush, is added to `mutatingCommands` in `internal/opnsense/client.go`;
5. for the stream:
   - that it is server-sent events;
   - its script and cadence;
   - its JSON field names;
   - anything the client must do differently (`Accept` header, the overall
     timeout, reading before EOF);
6. the live shape, from facts 1 and 3.

An unconfirmed claim is marked `UNVERIFIED:` and nothing is built on it. **If
`dumpcache` or `listlocaldata` does not exist at 26.7.3 under that path, stop
and report.** Never work around a missing endpoint.

### B. Cache observations, behind the resolver seam

1. **Seam.** The Unbound implementation reads `dumpcache`. The Dnsmasq
   implementation reports the named state "this resolver offers no cache read
   through the API", unless A finds one. A cache failure never overwrites the
   query report's availability, and the reverse holds too.
2. **Polling.** The dump is read once per poll, on the validated interval
   setting (decision 4), which is configurable on the collection surface.
3. **What is stored.**
   - Only `A`, `AAAA` and `CNAME` records. Every other rrtype is counted, not
     stored.
   - The availability detail gives the counts by rrtype, the response size and
     the time taken.
   - Names are compared case-insensitively, with any trailing dot removed.
   - A record with a non-integer `ttl`, or a `value` that is not an address
     valid for its rrtype, is skipped and counted.
4. **Bounded growth.**
   - A record `(host, rrtype, value)` that stays across consecutive polls is
     one stored observation, whose interval is extended.
   - A record that reappears after its interval ended starts a new
     observation.
   - The table is listed as a growing table.
5. **The coverage interval** is opnview's derivation, documented and named in
   *Vocabulary*.
   - It runs from the **last successful poll before the first poll that saw
     the record** to the **latest `poll instant + ttl`**.
   - Across a failed poll, or after a restart, the lower bound is that earlier
     successful poll.
   - With no earlier poll, it is the poll's own instant.
6. **CNAME chains.**
   - Evidence for domain `D` at instant `T` follows `CNAME` observations from
     `D`, every link covering `T`, to `A`/`AAAA` observations covering `T`.
   - The chain length is bounded, cited from Unbound or RFC 1034 §3.6.2, or
     else an opnview constant with its reason.
   - A loop yields nothing.
   - **`site_name` is always the domain the client looked up**, never a CNAME
     target.
7. **Late arrival.** Storing observations re-derives the attribution of every
   flow they could change: the derivation window is widened over their
   coverage. The result is independent of the order in which data arrives.

### C. The attribution rule, amended

This amends 5A D1/D3 and `docs/data-model.md` *`domain_attribution`* by name.

1. **Exact evidence first.**
   - A flow, eligible as today, with destination `X` and client `C` has an
     exact candidate `D` when:
     - an eligible lookup of `D` by `C` lies inside the exact window
       (decision 2); and
     - `X` is in `D`'s address evidence at that lookup's instant.
   - For a lookup with `answer_source` `Local-data`, local-data records count
     as evidence too (decision 6).
   - The flow is attributed with `resolver_cache_answer` **exactly when its
     exact candidates name exactly one distinct domain**. Two or more give no
     attribution.
2. **Timing as the fallback.**
   - With no exact candidate, the 5A timing rule applies unchanged and records
     `lookup_timing`.
   - It is suppressed when cache evidence covers the single timing candidate's
     lookup and excludes `X` (decision 3).
3. **The method is recorded on each attribution.** This reverses 5A D3, and the
   reversal is stated in the docs and in the `schema.sql` comment.
   - `domain_attribution` gains a method column, constrained by a `CHECK`.
   - An exact attribution names the address observation it matched.
   - The lookup foreign key and `correlation_delay_seconds` stay.
   - The `schema-checks.sh` absence assertion is replaced, by name, with
     presence, `CHECK` and seed assertions.
4. **The rate per method.**
   - `ReadAttributionRate` and the diagnostic `Attribution rate per client`
     report, per client, the count named by each method, the total named and
     the eligible count.
   - "Undefined" keeps its 5A meaning. The result also says whether cache
     evidence was available over the window.
5. **Re-pointing and ineligibility** apply to both methods. A second
   derivation writes nothing.
6. **`no_domains` and retention.**
   - `no_domains` follows decision 7.
   - The purge removes observations whose coverage ended before the horizon,
     except those a surviving attribution references, and `foreign_key_check`
     is clean afterwards.
   - `purge.sql`, *Purge* and the growing-table list cover the new tables.
7. **Privacy.**
   - README *Data* and *Limitations* say the cache read records **every name
     the firewall's resolver resolved**, governed by retention and the
     aggregate mode.
   - README *Site names* is amended: a second method exists, and it is still
     an inference.

### D. Host names with no lease, through local data

This part applies only if A verifies `listlocaldata`.

1. **Reading.**
   - The Unbound implementation reads local data on its interval (decision 4).
   - It keeps `A`, `AAAA` and `PTR` records, stored so that "what local data
     held at instant T" can be answered.
   - It reuses `state_snapshot` / `state_item` where they fit.
2. **Resolution.**
   - A host name no lease resolves is resolved through the local data valid
     at the lookup's instant.
   - Matching follows the same first-label, case and trailing-dot rules as
     leases. A match counts only when exactly one address answers.
   - The outcome is `local_data_hostname`.
   - The H6 retry and its partial index cover it, across restarts.
3. **Diagnostic.** `Unresolved host names by cause` gains a "resolved by local
   data" bin. "No lease names the host" becomes "neither a lease nor local data
   names the host".

### E. The processor figure

1. Remove the unverified path: `unverifiedCPUKeys` and the `cpu` key in
   `activity.json`.
2. Read `/api/diagnostics/cpu_usage/stream` (decision 8) through
   `internal/opnsense`:
   - read at most a bounded number of events within a bounded time, both
     documented with their reason, then close the stream;
   - name each of these as its own absence, never a 0: no event in time, an
     undecodable event, a denial (naming the 26.7.3 privilege), and a 404;
   - use a fixture of the verified shape, with synthesised values.
3. Keep `MeasureCPUUseRatio`.
   - Its derivation from the source fields is documented.
   - `docs/data-model.md` *`measurement_sample`* and the survey are corrected:
     the old path was `getActivity`.

### F. `sql/schema-checks.sh` deletes by exact path

1. The glob at line 295 is replaced by 39 exact paths: each of the 13 declared
   database variables, with its `-wal` and `-shm` companions.
2. Any new database this cycle adds is declared and covered the same way.
3. The two `mktemp` removals are kept, with a comment (decision 9).

### G. `PRAGMA synchronous = NORMAL`

1. The live database is opened with `synchronous(NORMAL)` beside WAL.
2. The trade is documented, citing sqlite.org (`pragma.html#pragma_synchronous`,
   `wal.html`), in `store.go`, README *Limitations* and `docs/data-model.md`
   *Conventions*:
   - transactions committed shortly before a power loss or an OS crash may be
     rolled back on recovery;
   - the database is not corrupted;
   - an application crash alone loses nothing;
   - approved by the maintainer on 9 October 2026.

### H. Documents

- **`docs/opnsense-api-survey.md`:** items A1–A6, and *Data source 5*
  corrected — the resolver now has an answer-address source.
- **`docs/data-model.md`:**
  - *What this schema is not*;
  - *Vocabulary* and its count;
  - the entity and growing/bounded lists;
  - `dns_resolution`, `domain_attribution`, the new entities and
    `measurement_sample`;
  - *API field coverage*, *Purge* and `setting`;
  - the `ReadAttributionRate` contract.
- **`docs/architecture.md`:** the new provider kind.
- **`docs/widget-catalogue.md`:**
  - preamble point 2: site names still come only from the resolver, with or
    without the cache's address evidence;
  - *Attribution rate per client*: columns per method;
  - the site-name presets carry the method per record (rendered in step 7).
- **`ROADMAP.md`:** step table row 5, the step-5 checklist (proportions only),
  and the *Data sources* resolver row.
- **5A spec, by name:**
  - AC4 is amended to admit `dumpcache`, `listlocaldata` and
    `cpu_usage/stream`;
  - *Deferred to 5B*: the constant method field becomes the stored per-record
    method.
- **`README.md`:** C7 and G2.

## Acceptance criteria

Every criterion runs through `docker compose run --rm checks`, whose `go test`
stays **under 60 s**, and `docker compose run --rm pre-deployment`.
`shellcheck -S error sql/schema-checks.sh` passes. Each new test is shown
failing on `8a9e870` first. No test is weakened, skipped or moved.

### Verification and endpoints

- [ ] AC1 — A1–A6 are recorded for every endpoint read, with 26.7.3 URLs.
  Unconfirmed claims are marked `UNVERIFIED:`, and no code reads them.
- [ ] AC2 — New registry entries pass the existing endpoint tests. Mutating
  Unbound diagnostics actions are in `mutatingCommands`, with a test.
- [ ] AC3 — 5A AC4 is amended by name. The registry diff against `8a9e870`
  adds exactly the named endpoints, and removes `getActivity`.
  *Amended by `specs/SPEC-resolver-cache-closing.md`, scope 2:* the diff adds
  the three named endpoints — `dumpcache`, `listlocaldata` and
  `cpu_usage/stream` — and removes `/api/diagnostics/activity/getActivity`,
  which carries no processor figure and is read for nothing else.
- [ ] AC4 — Per new endpoint, each absence has its own wording and stores
  nothing: authentication failed (401, naming no privilege); denied (403,
  naming the `ACL.xml` privilege); 404; undecodable;
  and, for the cache, `status` not `ok`. Dnsmasq reports the named "no cache
  read" state. Cache and query-report availability are independent.

### Cache observations

- [ ] AC5 — On a synthesised dump with every rrtype:
  - only `A`, `AAAA` and `CNAME` are stored;
  - the detail gives the rrtype counts and the response size;
  - a bad `ttl` or `value` is skipped and counted;
  - `type` is never read as the record type.
- [ ] AC6 — Growth:
  - N polls of an unchanged dump store as many observations as one poll;
  - coverage is extended at each poll;
  - absence followed by reappearance starts a new observation.
- [ ] AC7 — The coverage lower bound is the previous successful poll, across a
  failed poll and after a restart. The upper bound is the latest `poll + ttl`.
- [ ] AC8 — CNAME chains:
  - `D` → CNAME → CNAME → `A X` gives `X` for `D` while every link covers the
    instant, and not outside;
  - a loop gives nothing, and so does a chain over the bound;
  - `site_name` is `D`, verbatim.

### Attribution

- [ ] AC9 — Synthesised outcomes, one test each:
  - three domains looked up in the window, one resolving to the destination →
    attributed with `resolver_cache_answer`, where 5A gave no row;
  - two exact candidates with distinct domains → no row;
  - one exact candidate → `resolver_cache_answer`, with its evidence named;
  - no cache evidence and one timing candidate → `lookup_timing`;
  - contradicting evidence → no row (decision 3);
  - a blocked lookup, an east-west flow, a flow to this firewall → no row
    under either method.
- [ ] AC10 — Order independence:
  - all six orders of flows, lookups and observations give identical rows;
  - an observation stored a pass later changes the attribution at that pass;
  - a second derivation writes nothing.
- [ ] AC11 — No invented domain: every `site_name` equals its referenced
  lookup's `domain`, and that lookup's client is the flow's source client.
- [ ] AC12 — The method `CHECK` refuses unknown values. `schema-checks` asserts
  the column (replacing the 5A absence check, by name) and both methods
  seeded at both sizes.
- [ ] AC13 — `ReadAttributionRate` and the diagnostic give the same per-method
  counts on the seed, and those counts sum to the named total. One test per
  state: no resolver (undefined), query report only, query report and cache.
- [ ] AC14 — `no_domains` follows decision 7, with a test. The purge removes
  observations that ended before the horizon, keeps those an attribution
  references, and leaves the foreign keys clean.
- [ ] AC15 — No new statement plans a `SCAN` of a growing table at either seed
  size. The seed fills every new table and column in both IP versions.

### Local data (if verified)

- [ ] AC16 — An `unknown_hostname` lookup that local data names with exactly
  one address resolves to `local_data_hostname`, when the data arrives later
  and across a restart. Two addresses give `ambiguous_hostname`. The retry
  reads only its partial index.
- [ ] AC17 — The local-data bin exists, and `schema-checks` executes it on the
  seed.

### Processor, cleanup, synchronous

- [ ] AC18 — Processor reading:
  - a grep finds no `unverifiedCPUKeys` and no `cpu` key in `activity.json`;
  - the reading equals the documented derivation on a fixture of the verified
    shape;
  - each absence has its own wording and no row;
  - the stream read returns within its bound when the server streams forever,
    and when it sends nothing.
- [ ] AC19 — Exact-path deletion:
  - `grep -nE '(rm|unlink)[^#]*\*' sql/ ci/` returns nothing;
  - exactly the declared databases and their companions are removed;
  - an undeclared database in `$DATA_DIR` survives a run.
- [ ] AC20 — A test reads `PRAGMA synchronous` = 1 and `journal_mode` = `wal`.
  The trade is documented in `store.go`, the README and the data model, citing
  sqlite.org.

### Documents

- [ ] AC21 — Section H is present. *Vocabulary* has a row for every new term,
  and its count matches. The entity and growing lists match `sqlite_master`.
  The installation-data grep, run by the orchestrator, returns nothing.

## Live validation — before anything is committed

The orchestrator runs these read-only, before (on the current deployment, as
the baseline, before the dump and rebuild) and after (the new build having run
25 minutes or more, same logging, a window of the same length). Outputs are
never committed.

**V1 — Attribution rate.** Expected: **up**. Also run grouped by method
afterwards.

```sql
SELECT count(*) AS eligible, sum(a.flow_id IS NOT NULL) AS named,
       round(100.0 * sum(a.flow_id IS NOT NULL) / count(*), 1) AS rate_percent
FROM flow f LEFT JOIN domain_attribution a ON a.flow_id = f.id
WHERE f.observed_at >= :start AND f.observed_at < :end
  AND f.src_client_id IS NOT NULL AND f.dst_interface_id IS NULL
  AND f.src_is_this_firewall = 0 AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg');
```

**V2 — No invented domain.** Expected: **0**.

```sql
SELECT count(*) FROM domain_attribution a
JOIN dns_resolution r ON r.id = a.dns_resolution_id
JOIN flow f ON f.id = a.flow_id
WHERE a.site_name <> r.domain OR r.looked_up_at > f.observed_at
   OR NOT (CASE WHEN f.src_client_id IS NOT NULL AND r.client_id IS NOT NULL
                THEN r.client_id = f.src_client_id
                ELSE r.client_address = f.src_address END);
```

The other checks are named queries in `sql/queries/diagnostics.sql`
(`-- diagnostic: live_…`), executed by `schema-checks` on the seed:

| # | Query | Expected |
|---|---|---|
| V3 | `live_single_exact_candidate_unattributed` | 0 |
| V4 | `live_single_timing_candidate_unattributed` (no exact candidate, one timing candidate, no contradiction) | 0 |
| V5 | `live_exact_evidence_mismatch` | 0 |
| V6 | `live_cache_evidence_coverage`: the share of eligible flows whose destination is in an observation covering the flow | Informational |
| V7 | Cache-read detail (size, time, rrtype counts) and observations stored per hour | Grows with distinct records, not with polls |
| V8 | `client_resolution` counts, then *Unresolved host names by cause* | `unknown_hostname` down |
| V9 | Processor samples over the window, or the named absence | Samples, or a named cause |
| V10 | `live_this_firewall_unmarked`, `live_paired_counted_twice` | Still 0 |

The validation passes when:

- V2, V3, V4, V5 and V10 are 0;
- V1 rose;
- V9 shows samples or a named cause;
- the maintainer accepts V6–V8.

## Out of scope

- Routes, templates and rendering (5B, step 7).
- Suricata and step 6.
- Any change on the firewall.
- Caches of resolvers other than Unbound, beyond their named state.
- Inferring a site name from a CNAME target, a PTR record or a registrable
  domain.
- Migrations (step 8).
- `CLAUDE.md`.

## Risks

1. **Records the polls miss.** A record inserted and expired between two
   polls is never seen. V6 measures this.
2. **Dump cost on the firewall.** It grows with the size of the cache. The
   detail records the size and the time taken.
3. **Cache eviction before TTL** over-states the upper bound. Shared CDN
   addresses correctly produce no row.
4. **The coverage lower bound** stretches across failed polls. AC7 pins this.
5. **The stream never ends.** The read must be strictly bounded, and the
   client's overall timeout must not fight it.
6. **The 26.7.3 sources are unverified until A.**
7. **The rebuild needs a dump, and the baseline first.**
8. **The test budget.** The six-order and coverage tests must stay inside
   60 s.
9. **Privacy.** The README must state what the cache read records before this
   ships.
