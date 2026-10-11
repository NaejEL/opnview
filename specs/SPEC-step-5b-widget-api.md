# SPEC — Step 5B: the widget HTTP API, the accepted backend proposals, and dual-stack host overrides

Status: APPROVED
Cycle kind: standard

The cycle bundles three groups, as the maintainer asked. It produces JSON over HTTP and no HTML, CSS, JavaScript, template, catalogue string or rendering, so it is `standard`. A separate interface cycle runs in parallel. Where a group needs interface work, this spec says what the API offers and leaves the rendering to that cycle.

## Context

**Where the repository stands.** `main` is at `a1e2707` ("Name sites from the Unbound cache, and read the processor from its stream"). The resolver-cache cycle and its two follow-ups are committed. Step 5 is "in progress" in `ROADMAP.md`: 5A is implemented and 5B, the widget HTTP API, is still to do.

**What 5B consumes, and what already exists.**

- **The store read layer.**
  - `internal/store/read.go` and `internal/store/samples.go` provide `ReadVolumeWindow`, `ReadVolumeSeries`, `ReadConnectionTree`, `ReadAttributionRate`, `ReadSiteTotals`, `ReadOwnerTotals`, `ReadInterfaceClients`, `ReadBlockedDecisionCounts`, `ReadRuleLogging`, `ReadPublicAddresses`, `ReadCurrentRates`, `ReadClientCurrentRate`, `ReadSampledBytes` and `ReadInterfaceThroughput`.
  - `publicsuffix.Refresher.Group` groups site names by registrable domain.
  - Their contracts are in `docs/data-model.md`, *What step 5B reads — the store's contracts*.
  - Every statement they run is in `internal/store/read.sql`, and `sql/schema-checks.sh` plans each one at the 100 000- and 1 000 000-row seeds.
  - Each read function fetches its statement text and runs it **unprepared** on every call. The derivation paths use `preparedTx` and `statementCache` (`internal/store/exec.go`); the read paths have no statement cache.
- **One database connection.** `internal/store/store.go` calls `SetMaxOpenConns(1)`. Every widget read would therefore queue behind collection writes and derivations, and the reverse.
- **The HTTP surface** (`internal/web/web.go`, `middleware.go`).
  - Routes are a table in which each row declares an access level: `public`, `authenticated` or `key_file`.
  - `TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest` enumerates the table.
  - An unauthenticated request is answered with a 303 redirect to the setup or sign-in page.
  - The CSRF check runs on every POST and reads a form field, `csrf_token`.
  - There is no widget endpoint. The package comment says step 5 adds one "by adding files and route rows".
- **What 5A deferred to 5B** (`specs/SPEC-correlation-classification-aggregation.md`, *Deferred to 5B*):
  - one authenticated route per widget type;
  - parameter parsing against the catalogue;
  - reference resolution with an unresolved state, and reporting of unknown keys;
  - availability, coverage, gaps and freshness carried in each response;
  - explicit remainders on truncation;
  - reconciliation of the map, the tree and the Sankey at response level;
  - `no_domains` withheld at the API;
  - the stored per-record attribution method;
  - six questions, carried below as open questions.
- **Decisions already taken for 5B** (`specs/SPEC-resolver-cache-attribution.md`, decisions 5 and 7, and C3):
  - The attribution method is exposed at the API as a per-record field, with the values `resolver_cache_answer` or `lookup_timing`.
  - Under `aggregate_mode = no_domains`, attributions, domain aggregates and resolver records are still stored, and they are withheld at the API.
- **What the API must serve.**
  - `ROADMAP.md` step 5: one endpoint per widget type. It takes the widget's declared parameters, with the period as a widget parameter, and returns the data together with the source-availability state.
  - A multi-series endpoint returns a list of series, each series carrying its own availability.
  - `docs/widget-catalogue.md` declares 29 presets (Part 2, *Preset index*). Their parameter keys are the vocabulary, and their bindings name the providers each preset depends on.
  - Part 1 gives each shape's data requirements: four conditions, never-silent truncation, the mandatory unplaced counter on the map, children summing to their parent in the tree, and the "Other" band in the Sankey.
  - `docs/dashboard-format.md` defines:
    - four parameter value shapes;
    - a reference object `{kind, by, value, label}`, whose `label` is never matched on;
    - unknown keys kept and reported;
    - unknown values falling back to the default with a notice;
    - unknown widget types rendered as unresolved rather than failing.
- **What the mockup places.** `docs/mockups/canvas-mockup.html` places 24 catalogue types and one deliberately unknown type, `client_activity_timeline` (listed in `UNKNOWN_TYPES` and reported as `unknown_widget_type`).
  - Five catalogue types are not placed: `destination_countries`, `security_alerts_over_time`, `alerts_by_client_and_interface`, `aggregate_freshness` and `unresolved_joins`.
  - The 5A spec's table *Data the mockup displays* maps 45 mockup data items to their store sources. Items #4, #8 at 2 s, and #39 are **cannot**.

**Group B: approved backend proposals.**

1. **The mutating-command guard.** `mutatingCommands` (`internal/opnsense/client.go`) refuses exact command names only: `set`, `add`, `del`, `toggle`, `reconfigure`, `start`, `stop`, `restart`, `clear`, `drop_alert_log`, `del_lease`, `dnsbl` and `reconfigure_general`. A prefixed name such as `add_item`, `set_rule`, `del_host_override`, `toggle_item` or `update_…`, or `reset`, passes it.
2. **The exact-attribution search.** `attribution_name_lookups` (`internal/store/derive.sql`, line 639) reads every lookup of a name through `idx_dns_resolution_domain_name (lower(rtrim(domain,'.')), looked_up_at)`, and only afterwards filters by client.
   - Its client predicate is an `OR` of two branches: by `client_id`, and by `client_address` when either side has no `client_id`.
3. **The pre-deployment data directory.** `sql/schema-checks.sh` reads `OPNVIEW_DATA_DIR` (default `/data`).
   - Nothing refuses a relative value, or one that Git Bash has rewritten (MSYS path conversion turns `/data` into a Windows path).
   - Neither `OPNVIEW_DATA_DIR` nor `MSYS_NO_PATHCONV=1` is documented in `ROADMAP.md`, *Development and test environment*.
4. **The CNAME rule is not in the data model.** "No CNAME is followed into local data" is the decision of `specs/SPEC-resolver-cache-follow-ups.md`, scope 1. It is stated in code (`MaxCNAMELinks`, `records.go`) and in a diagnostic comment, but `docs/data-model.md` does not state it next to decision 6.
5. **The null-ttl classification sits in the wrong file.** The rule "a cache row whose `ttl` is JSON null is an Unbound message-cache reference" lives in the product-neutral `internal/collect/resourcerecord.go` (lines 80–86, and the tally fields at 40–51). It is a fact about Unbound's `dump_cache` and OPNsense's wrapper.py, not about DNS.
6. **About 10 live cache records "whose value is not valid for their type".** The live availability detail of the Unbound cache read reports them (`badValue`, `resourcerecord.go`). Nobody has established what they are.
7. **Two overlong comment lines.** `internal/opnsense/endpoints.go`, line 428, and `sql/queries/diagnostics.sql`, line 815, are the only comment lines over 100 characters in those files.
8. **Older step-5A proposals.**
   - The `destination_net` field of `search_rule` is recorded as *not stored* (`docs/data-model.md`, line 2987).
   - `ReclassifyEverything` (`internal/collect/derive.go`, line 334) pairs the whole history in one `Pair` call.
   - `sql/queries/screens.sql` reads `flow` and `volume_aggregate_24h` without excluding second legs (`exit_leg_device`), so a paired connection counts twice in Overview, Matrix, Interface and Map.
   - The read paths have no statement cache (see above).
   - `leg_pairing_window_seconds` accepts any whole number ≥ 0, with no upper bound (`internal/config/config.go`, line 500).
   - `UpsertBlocklist` (`internal/store/write.go`, line 496) sets `last_seen_at` to the lookup's ingestion instant and overwrites it unconditionally.
   - The lease pass writes `reachable` availability before ingesting (`internal/collect/dhcplease.go`, lines 247–260). A store failure in `ingestLease` then leaves the availability saying `reachable`.
   - The cost of `ReclassifyEverything` has never been measured.

**Group C: dual-stack host overrides.**

- `ResolveLoggedHostname` (`internal/store/attribute.go`, line 486) resolves a logged querier name through the leases first, then through local data.
- `local_data_addresses_for_hostname` returns distinct addresses, and any count above 1 gives `ambiguous_hostname`.
- A host override with both an A and an AAAA record is therefore ambiguous, although it names one machine. This is AC16 of `specs/SPEC-resolver-cache-attribution.md`: "Two addresses give `ambiguous_hostname`."
- The logged name is what `scripts/unbound/logger.py` got from a reverse lookup (`socket.gethostbyaddr`) of the querying address (survey, *Data source 5*). The querying address's family is therefore a fact that exists; the question is whether `opnview` can learn it.
- `/api/unbound/overview/search_queries` accepts a `client` parameter, "an IP address" (survey, line 568).

## Scope

### Group A — the widget HTTP API (5B)

**A1. One read-only endpoint per widget type, behind a registry.**

- Each served preset (see A2) has one authenticated `GET` route. The route reads only the local database.
- Endpoints are registered through a registry: adding a preset's endpoint means adding one file and one registry row, touching nothing else (`ROADMAP.md`, *Modularity*).
- The routes join the `internal/web` route table at `AccessAuthenticated`. The access-set test is updated by name.
- The path pattern is fixed by open question 3. It is recorded once, in the API document (A15).

**A2. Which presets are served.** The default, subject to open question 1:

- every preset in the catalogue's *Preset index* except the three alert presets — `security_alerts_over_time`, `alerts_by_signature` and `alerts_by_client_and_interface` — which belong to step 6 (severity resolution, alert aggregation, G7 and G8);
- that is 26 types, including the five the mockup does not place.

A type that is not served answers `unknown_widget_type` (A16). No placeholder endpoint is built for a type that is not served.

**A3. The request carries the widget's `parameters` object exactly as `docs/dashboard-format.md` defines it**: scalars, arrays of scalars, reference objects, and the ordered array of objects for `series`. There is no second encoding of the parameters. The carrier is fixed by open question 2.

**A4. Parameter handling, per the catalogue and the dashboard format.**

1. **Keys.** The accepted keys of a type are exactly the backticked keys of its catalogue **Parameters** paragraph.
   - An unknown key is reported in the response and ignored.
   - An unknown key never causes an error status.
2. **Values.** A value outside a key's declared vocabulary or type takes the default and is reported as an invalid value.
   - A value of the right type that the API applies is never silently clamped.
3. **Defaults.** Every absent key takes a default declared once, per open question 5.
   - The catalogue's stated defaults (`show_unplaced` true, `show_other` true, `include_blocked` true, `include_unassigned` true, `show_attribution_rate` true, `show_last_sampled` true) are kept.
4. **References.** A reference is resolved against the tables of the dashboard format's *The reference object*, by exact match on the declared `by` key.
   - `label` is never matched on.
   - A `kind` or `by` outside the vocabulary is unresolved.
   - An unresolved reference makes the widget return **no data**, never zeros. It also returns the unresolved reference's path (for example `series[1].interface`), `kind`, `by`, `value` and `label`.
   - In a `series` list, an unresolved reference affects its own series only.
5. **Data parameters and rendering parameters.** Each parameter of each served type is classified as either:
   - **data-affecting** — the API applies it; or
   - **rendering-only** — the API accepts it, echoes it and does not apply it (for example `compact`, `stacked`, `legend`, `layout` and `cluster`).

   The classification is recorded in the API document (A15). Every data-affecting parameter changes the response when its value changes (`ROADMAP.md` step 7, "Every control offered changes what is drawn").

**A5. One response envelope for every type.** Its top-level fields, named once in the API document:

- the type;
- the **effective** parameters, after defaults;
- the requested window and the window actually **covered** (from `VolumeWindow.Covered` or its equivalent), or "not covered";
- the gaps overlapping the window;
- availability per provider the preset binds;
- freshness of every aggregate period read;
- unresolved references, unknown keys and invalid values;
- what was withheld under the aggregate mode (A10);
- the data.

Every figure the store could not compute is absent, carrying a named state, and never `0`. The store's three rules in `read.go`, lines 15–22, apply at the API.

**A6. Availability, in the catalogue's four conditions.**

- For each provider kind in a preset's **Binding**, the response carries every registered provider of that kind:
  - its `kind` and `provider_key`;
  - whether it is active;
  - its `source_availability` `state`, `probe`, `checked_at` and `detail`;
  - and the condition it maps to: *not yet probed*, *unavailable*, *present but disabled*, or *reachable*.
- A provider with no availability row is reported as *state unknown*, never as healthy.
- *Reachable and no rows* is distinguishable from the other three without inspecting the data.
- A multi-series request (`custom_chart`, and `traffic_over_time_by_scope` with several scopes) carries availability, coverage and gaps **per series**.
- The API emits **condition codes, not sentences**. The wording in the catalogue's Part 3 belongs to the interface cycle's string catalogue.

**A7. Horizon and freshness.**

- Inside the `flow` horizon (`retention_seconds`), a preset reads `flow` or `classified_flow`. Beyond it, it reads the aggregates (catalogue Part 4, item 1).
- A figure that cannot be answered beyond the horizon is reported with a named state for that figure, not omitted silently. The rules of a matrix cell beyond the horizon are one example, if `rule_volume_aggregate_*` cannot place them per cell.
- A period read from aggregates whose `computed_at` lags the refresh contract (`docs/data-model.md`) is flagged **stale** in the response. This is the same test *Aggregate freshness* applies.

**A8. Truncation and reconciliation at response level.**

- **Truncation.** Every `limit`, `slices`, `max_nodes`, `min_share`, `limit_per_level`, `clients_per_card`, `sites_per_client`, `min_flows`, `min_count` or `min_bytes` that drops members returns an explicit remainder carrying its count and its figures.
- **Map.** Placed volume plus unplaced volume equals the window total for the selected scope and action. The unplaced counter is always present, even at zero, and is split by `miss`, `pending` and never looked up.
  - The blocked map also returns the count of DNS-blocked lookups in the period, which cannot be placed.
- **Tree.** Children sum to their parent at every level, on both sides, with the remainder child (the rule of `docs/mockups/check-tree-reconciliation.js`).
- **Sankey.** Ribbons plus the "Other" band equal the total. A node whose naming source failed lands in "Other".
- **Cards.** The residual **Unassigned** group is returned whenever it is non-empty and `include_unassigned` is true. When `include_unassigned` is false, the response states that the totals exclude it.
- **Tables.** Rows whose join failed are first-class rows that name the failure, for example rule not found, address not matched to a client, or unresolved.
- **Ranked bars.** Discovered members with no traffic are returned with zero bars only where a measurement says zero.
- **Paired connections.** Every reconciliation counts a paired connection once (`exit_leg_device IS NULL`). An interface's own figures add its second legs, as `ReadVolumeWindow.ExitLegs` does.

**A9. Site names.**

- Every record carrying a site name also carries its `method` (`resolver_cache_answer` or `lookup_timing`) and its `correlation_delay_seconds`.
- `top_sites` and `sites_by_client` also carry the count per method.
- `attribution_rate_per_client` returns per client:
  - eligible flows, named flows, named by each method, and the rate;
  - mean and maximum delay;
  - `CacheCovered` and `ResolverCovered`.
- The rate is **undefined** when `ReadAttributionRate` says so, and is never `0` in that case.
- Registrable-domain grouping returns an explicit unavailable state before the first successful Public Suffix List download.
- When the resolver's ring-buffer ceiling bounds a period (catalogue Part 3, fact 1), this is exposed as a flag on every resolver-fed response.

**A10. `aggregate_mode = no_domains` withholds domain names at the API.**

- Under `no_domains`, no response contains a `domain_attribution.site_name`, a `dns_resolution.domain`, a registrable domain or a `resource_record_observation` name.
- Every withheld field or level is listed in the response's withheld field.
- Counts and volumes that carry no name are unchanged.
- The granularity (whole widget or field) and the exact set of withheld fields follow open question 6.
- `destination_countries`, `destination_operators` and the other presets that carry no domain are served unchanged.

**A11. What a volume means.**

- Every byte figure says which measure it is: logged bytes, the sum of logged packet lengths; or sampled bytes, from `ReadSampledBytes`.
- Logged and sampled figures are never summed together.
- *Interface throughput* says that its counters are the interface's own.
- How the observation-point limit is carried in the payload follows open question 7.

**A12. Reads.**

- Existing store functions are reused wherever they answer the question.
- Every new read statement goes in `internal/store/read.sql` (or a sibling file planned the same way) and is planned by `sql/schema-checks.sh` at both seed sizes. None plans a `SCAN` of a growing table.
- The new reads expected, at least:
  - per-client rankings with distinct destinations;
  - client traffic detail with lease issuers;
  - the matrix with rule descriptions per cell;
  - the blocked feed records over `blocked_decision`;
  - blocked DNS lookups by list and purpose;
  - countries and operators with peers and contributing interfaces;
  - Sankey pairs;
  - sites by client;
  - owner cards with client chips and their current rate or "no data in this period";
  - source availability;
  - aggregate freshness;
  - the unresolved-join counters;
  - the latest sample and a bucketed series per measurement subject and measure, aggregated `mean`, `min`, `max`, `sum` or `last`;
  - for *Public address*, whether each held address lies in a special-purpose range of the IANA registries, cited, so the response says whether the installation is behind an upstream NAT. No address literal is used beyond those registries.
- The read paths use one statement cache (B8d).

**A13. Boundaries.**

- No widget handler calls the firewall, MaxMind or the Public Suffix List. The three permitted outbound calls stay three.
- No response carries a credential, a ciphertext, a session token or a CSRF token.
- No response sets CORS headers.
- No user-facing sentence is a literal in the API. Condition codes and field names are English identifiers.

**A14. Response budgets.**

- Each served type, at its default parameters, for periods `24h` and `30d`, is timed against the 100 000- and 1 000 000-row seeds in the pre-deployment command (`predeployment` build tag or `sql/schema-checks.sh`). The figures are reported, and a figure over its budget fails the run. Budgets follow open question 9.
- The everyday suite tests the API on small parameterised fixtures carrying both IP versions, and stays under 60 s.
- Request contexts propagate cancellation to the queries.

**A15. Documents.**

- **The API contract, documented once** (location per open question 3): path, carrier, envelope fields, condition codes, the per-type parameter table (data-affecting or rendering-only, with defaults), and the reference resolution rules. The parallel interface cycle reads this document. It cites the catalogue and the dashboard format rather than restating them.
- **A coverage table** mapping each of the 45 rows of the 5A table *Data the mockup displays* to the type and response field that carries it, or to its **cannot** reason.
- **`docs/data-model.md`, *What step 5B reads*:** the new read functions and their contracts.
- **`docs/architecture.md`:** the endpoint registry as a seam.
- **`ROADMAP.md`:** the step-5 row and checklist, in proportions only.
- **5A spec, *Deferred to 5B*, amended by name:** each item marked done, or moved, with this spec cited.

**A16. Unknown type and malformed requests.**

- A type with no endpoint answers JSON naming the condition `unknown_widget_type`, with status 404, never an HTML page.
- A request whose parameters cannot be parsed at all answers JSON naming the condition, with status 400.
- An unauthenticated or expired session is answered per open question 4.

### Group B — accepted backend proposals

**B1. The mutating-command guard refuses by verb prefix.**

- `MutatingCommand` keeps its existing exact names. It also refuses any command whose name begins with the verbs `add`, `set`, `del`, `toggle` or `update`, followed by `_` (snake_case) or by an upper-case letter (camelCase, since the OPNsense router accepts both spellings). It refuses `reset` both exactly and prefixed the same way.
- It must not refuse read-only names that merely share letters, for example `settings`, `address…`, `delay…`, `search…`, `get…`, `status`, `dumpcache`, `listlocaldata`, `searchLease` or `get_c_p_u_type`.
- Positional parameters after the command remain caller data.
- **One test per controller the registry reaches** (each distinct `module/controller`). It lists that controller's actions at `opnsense/core` 26.7.3, cited by URL. It asserts that every mutating action is refused and that every action the registry calls is not.
- `TestNoRegistryEndpointIsAMutatingCommand` still passes.

**B2. An index bounding the exact-attribution search per client.**

- `attribution_name_lookups` becomes an index search bounded by name **and** client on **both** branches of its client predicate (by `client_id`, and by `client_address`).
- The approved index is `(lower(rtrim(domain,'.')), client_id, looked_up_at)`. The address branch needs either a matching address index or a rewrite into two bounded branches; the Builder chooses and records the reason.
- `schema-checks` asserts the plan at both seed sizes. The index's write cost on `dns_resolution`, a growing table, is noted in `docs/data-model.md`.

**B3. `ci/pre-deployment.sh` refuses a bad data directory.**

- Before it runs any step, it refuses an `OPNVIEW_DATA_DIR` that:
  - is not absolute;
  - carries a drive letter, a `:` or a `\`;
  - or otherwise shows Git-Bash path conversion.

  The message names the variable and the fix.
- `ROADMAP.md`, *Development and test environment*, documents `OPNVIEW_DATA_DIR` (its default, what reads it, why it must be a container path) and `MSYS_NO_PATHCONV=1` for Git Bash.
- Whether `sql/schema-checks.sh` gets the same guard follows open question 16.

**B4. `docs/data-model.md`: no CNAME is followed into local data.** A paragraph next to decision 6 (*`domain_attribution`*) states the rule of `specs/SPEC-resolver-cache-follow-ups.md`, scope 1, with its reason: Unbound answers a local-data name from local data, not through a cached CNAME. It names the three places that read it: `exactCandidates`, `answerAddresses` and `live_timing_fallback_other_family_cached`.

**B5. The null-ttl classification moves into the Unbound reader.**

- The rule that a JSON-null `ttl` is a message-cache reference, with its citation (`daemon/cachedump.c` `dump_msg_ref`; wrapper.py 26.7.3, lines 65–68), moves from `resourcerecord.go` to `resolvercache_unbound.go`.
- `resourcerecord.go` keeps only DNS-generic rules, and states no Unbound fact.
- Behaviour is unchanged: the availability detail of a fixture holding message-cache references is identical before and after.

**B6. The roughly 10 live records "whose value is not valid for their type".**

- The Builder profiles a live dump read-only. The orchestrator captures it through the deployed service's credentials to an exact path outside the repository, shown to the maintainer, and never commits it.
- The outcome is one of two:
  - **(a)** a parsing defect, which is fixed, with a synthesised fixture row of each affected shape; or
  - **(b)** genuine anomalies. The skip count is then split by named category in the availability detail, for example "an A record whose value is not an IPv4 address" or "a CNAME with an empty target".
- In either case `badValue` stops being one undifferentiated count. Only categories and proportions are recorded in the survey.

**B7. The two overlong comment lines** are rewrapped. No comment line in `internal/opnsense/endpoints.go` or `sql/queries/diagnostics.sql` exceeds 100 characters.

**B8. The older step-5A proposals.**

- **a. `destination_net`.** Each pf rule's `destination_net` from `search_rule` is stored verbatim on `rule`. The *not stored* verdict at `docs/data-model.md`, line 2987, is reversed by name, with the reason from open question 12. It is never evaluated: `opnview` matches no flow against it.
- **b. Chunked pairing.** `ReclassifyEverything` pairs in bounded windows rather than one `Pair` over the whole history. The bound is a documented constant with its reason, not a new setting. The result is identical to the unchunked pairing.
- **c. `screens.sql` excludes second legs.** Every query in `sql/queries/screens.sql` that sums volume or counts connections reads `classified_flow` or filters `exit_leg_device IS NULL`. This covers Overview, Matrix and Interface, and Map over `volume_aggregate_24h`. The queries stay authoritative and are not deleted.
- **d. One statement cache for the read paths.** Each read statement is prepared once and reused across calls, compatible with the single connection and with any read connection open question 10 introduces.
- **e. An upper bound on `leg_pairing_window_seconds`,** validated by `internal/config`, refused above the bound with a message naming the setting. The value follows open question 13.
- **f. `UpsertBlocklist` `last_seen_at`.** The semantics follow open question 14. Under the recommendation, `first_seen_at` and `last_seen_at` are the earliest and latest `looked_up_at` of a lookup naming the list, and never move backwards on re-ingestion.
- **g. Store-side failures in the lease pass reach the availability.** A failure in `ingestLease` after a successful read leaves the backend's availability naming the store failure. It is not left as `reachable` with the read's detail. It also counts as a failed pass for the host-name retry, as a read failure does.
- **h. The cost of `ReclassifyEverything`,** measured at the 1 000 000-row seed in the pre-deployment command, reported, and recorded in `docs/data-model.md`. Whether it carries a budget follows open question 17.

### Group C — dual-stack host overrides

**C1. Verify first.** From `opnsense/core` 26.7.3 (`scripts/unbound/logger.py`, `scripts/unbound/stats.py`, `Unbound/Api/OverviewController.php`) and Unbound's documented behaviour, establish which read-only evidence can tell the family of the querying address behind a logged host name. At least these candidates are examined:

- **(i)** whether `search_queries` with its `client` parameter filters on the stored querying address. If so, a lookup can be found again under each candidate address, through the endpoint already registered and with no new endpoint.
- **(ii)** whether local-data PTR records name the host for one family only at the instant.

Each finding is cited in the survey. An unconfirmed claim is marked `UNVERIFIED:` and nothing is built on it.

**C2. The rule.** When the candidate addresses of a logged host name are one A and one AAAA of the **same** name, and verified evidence establishes the querying family:

- the lookup resolves to the address of that family, with outcome `local_data_hostname`;
- the evidence used is recorded per lookup or in the availability detail, per open question 15.

When the family cannot be established, the lookup stays `ambiguous_hostname`, or the distinct state that open question 15 may add. Nothing is guessed.

Two or more addresses of one family remain ambiguous, and leases are still consulted first.

**C3. AC16 of `specs/SPEC-resolver-cache-attribution.md` is amended by name.** "Two addresses give `ambiguous_hostname`" becomes: "two addresses of one family, or one A and one AAAA whose querying family cannot be established, give `ambiguous_hostname`; one A and one AAAA with the family established give `local_data_hostname` with the address of that family."

The retry and its partial index still cover the case, across restarts.

**C4. `docs/data-model.md`, *`dns_resolution`*,** and the diagnostic *Unresolved host names by cause* gain the dual-stack case as its own bin.

## Acceptance criteria

Every criterion runs through `docker compose run --rm checks`, whose `go test` stays **under 60 s** with the CPU count reported, and through `docker compose run --rm pre-deployment`, whose wall time is reported. Each new test is shown failing on `a1e2707` first, where it can fail there. No test is weakened, skipped or moved to `predeployment` without the maintainer's agreement. Fixtures take their interface and client counts as parameters, carry both IP versions, and are synthesised (RFC 2606, 5737 and 3849).

### Group A — API

- [ ] AC1 — A test parses `docs/widget-catalogue.md` (`### ` headings, `**Type**` and `**Parameters**`, as `check-form-covers-catalogue.js` does). The set of registered types equals the preset index minus the types open question 1 excludes. For every registered type, every catalogue key is accepted without being reported unknown.
- [ ] AC2 — The registry is a seam. A test registers a fake type from a test-only file and row and reaches it over HTTP, with no other file changed. `docs/architecture.md` names the seam.
- [ ] AC3 — Every widget route is `AccessAuthenticated`. `TestEveryRouteIsPublicByDecisionOrRefusesAnUnauthenticatedRequest` covers them, and a request with no session or an expired session gets the answer decided by open question 4 and **no widget data**.
- [ ] AC4 — An unknown parameter key is reported and ignored, and the status is not an error. An out-of-vocabulary value is reported and the default applies. A malformed parameters payload answers 400 with a JSON condition. An unknown type answers 404 with JSON `unknown_widget_type`, and so does `client_activity_timeline`.
- [ ] AC5 — For each reference kind (`interface`, `client`, `rule`, `owner`, `provider`, `country`, `operator`, `site`) and each accepted `by`:
  - a match resolves;
  - a non-match returns no data plus the path, `kind`, `by`, `value` and `label`;
  - a reference whose `label` matches a local row and whose `value` does not stays unresolved;
  - in a `custom_chart` with two series, an unresolved reference in one series leaves the other series' data intact.
- [ ] AC6 — For every data-affecting parameter of every served type, a test shows that two values give two different responses on a fixture built to make them differ. Every rendering-only parameter is echoed in the effective parameters. The API document's parameter table matches the code (a test compares them).
- [ ] AC7 — One test per condition, per provider kind a served type binds: not yet probed, unavailable, present but disabled, and reachable with no rows. Each yields its own condition code with no zero figure, and *reachable, no rows* yields measured zeros where the shape draws zeros. A provider with no availability row yields *state unknown*.
- [ ] AC8 — A multi-series request with one series' provider unavailable returns that series with its own unavailable condition and the other series with data.
- [ ] AC9 — Coverage and gaps:
  - a window starting before the earliest stored data returns `covered` narrower than `requested`;
  - a `firewall_log` `collection_gap` inside the window is listed;
  - a bucketed series marks gap buckets as gaps and never as zero;
  - its buckets sum to the window total.
- [ ] AC10 — A period read from an aggregate whose `computed_at` violates the refresh contract is flagged stale, and a fresh one is not.
- [ ] AC11 — Reconciliation on fixtures with paired connections:
  - the matrix total equals `sum(packet_bytes)` over `classified_flow` with `exit_leg_device IS NULL`;
  - map placed plus unplaced equals the total, with the unplaced split present at zero;
  - every tree level sums to its parent on both sides;
  - Sankey ribbons plus "Other" equal the total;
  - owner cards plus Unassigned equal the owner-family total.
- [ ] AC12 — Each truncating parameter at a value smaller than the member count returns a remainder whose count and figures make the total whole.
- [ ] AC13 — Every site-name record carries `method` and the delay. The per-method counts of `attribution_rate_per_client` equal `ReadAttributionRate` and the diagnostic `Attribution rate per client` on the seed. With no resolver coverage the rate is undefined, not 0.
- [ ] AC14 — With `aggregate_mode = no_domains`:
  - no response from any served type, for every period, contains any `site_name`, `dns_resolution.domain`, registrable domain or resource-record name present in the fixture (string search over the serialised responses);
  - each withheld field or level is listed;
  - every count and volume equals its value under `full`.
- [ ] AC15 — Every byte figure names its measure. No figure sums logged and sampled bytes. The payload carries the observation-point information decided by open question 7.
- [ ] AC16 — A test with a counting transport shows that serving every served type makes **zero** requests to the firewall, to MaxMind and to the Public Suffix List. The source-policy test still counts three packages that build HTTP requests.
- [ ] AC17 — No new read statement plans a `SCAN` of a growing table at either seed size. `schema-checks` plans every statement the API runs.
- [ ] AC18 — Pre-deployment reports the response time of every served type at its defaults, for `24h` and `30d`, at both seed sizes. Every figure is within the budget of open question 9, and a figure over budget fails the run.
- [ ] AC19 — *Public address* returns, per upstream interface, the address history and whether each held address is in a special-purpose range, with the IANA registries cited. It never presents a special-purpose IPv4 address as public. The change instant is absent until a second address has been seen.
- [ ] AC20 — The documents of A15 exist. The mockup coverage table has one row for each of the 45 rows of the 5A table, and each row names a type and field or a **cannot** reason. *Deferred to 5B* in the 5A spec is amended by name.

### Group B

- [ ] AC21 — `MutatingCommand` refuses `add_x`, `addX`, `set_x`, `setX`, `del_x`, `delX`, `toggle_x`, `toggleX`, `update_x`, `updateX`, `reset`, `reset_x` and `resetX`, each also with a positional parameter. It refuses none of the read-only names listed in B1 nor any command the registry calls. There is one test per registry controller, each citing its 26.7.3 source. Removing the prefix rule makes those tests fail.
- [ ] AC22 — `attribution_name_lookups` plans as an index `SEARCH` bounded by name and client on both branches, at both seed sizes, asserted by `schema-checks`. Every attribution test of the resolver-cache specs still passes.
- [ ] AC23 — `OPNVIEW_DATA_DIR` set to `data`, `./data`, `C:/Program Files/Git/data` and `C:\data` each makes `ci/pre-deployment.sh` exit non-zero before `go vet` runs, naming the variable. `/data` passes. `shellcheck -S error` passes. The `ROADMAP.md` paragraph exists.
- [ ] AC24 — The B4 paragraph exists in `docs/data-model.md`, next to decision 6.
- [ ] AC25 — `resourcerecord.go` contains no reference to message-cache references, null ttls or wrapper.py. The availability detail of the dump fixture is byte-identical before and after B5.
- [ ] AC26 — B6 is resolved: either the parsing defect is fixed with a fixture row per shape, or the detail names each category. The test fails on `a1e2707`'s wording. Only categories are recorded in the survey.
- [ ] AC27 — `grep` finds no comment line over 100 characters in `internal/opnsense/endpoints.go` or `sql/queries/diagnostics.sql`.
- [ ] AC28 — `rule.destination_net` is stored from a `search_rule` fixture. The seed fills it at both sizes, and `docs/data-model.md` is amended by name.
- [ ] AC29 — Chunked and unchunked pairing give identical `pair_outcome` and `paired_flow_id` on a fixture spanning several chunks. A second run changes nothing.
- [ ] AC30 — In `schema-checks`, the Overview and Matrix totals of `screens.sql` equal the `ReadVolumeWindow` total on the seed, which contains paired connections. `live_paired_counted_twice` stays 0.
- [ ] AC31 — The read functions run prepared statements from one cache, which a test observes. Every `read_test.go` test still passes.
- [ ] AC32 — `leg_pairing_window_seconds` above its bound is refused naming the setting. The bound itself is accepted.
- [ ] AC33 — Re-ingesting an older lookup naming a list never moves `blocklist.last_seen_at` backwards, under the semantics of open question 14.
- [ ] AC34 — A store failure planted in `ingestLease` leaves the backend's availability naming the store failure, and the pass counts as failed for the host-name retry.
- [ ] AC35 — Pre-deployment reports the wall time of `ReclassifyEverything` at the 1 000 000-row seed, and `docs/data-model.md` records it.

### Group C

- [ ] AC36 — The C1 findings are in the survey with 26.7.3 URLs. Unconfirmed claims are marked `UNVERIFIED:`, and no code reads them.
- [ ] AC37 — One test per outcome, each also passing after a restart and when the evidence arrives later:
  - a name with one A and one AAAA and verified evidence of the IPv4 family resolves to the A address;
  - the same with IPv6 evidence resolves to the AAAA address;
  - with no evidence, it gives the state decided by open question 15;
  - two A records stay ambiguous;
  - a lease still wins.
- [ ] AC38 — AC16 of the resolver-cache spec is amended by name. The diagnostic bin exists and `schema-checks` executes it on the seed.

### Whole cycle

- [ ] AC39 — The literal checks (no interface name, device, VLAN name, address or CIDR literal) pass over every new file, except the cited IANA special-purpose ranges, which are standards and not installation data. No new behaviour is hardcoded in place of a `setting` row.
- [ ] AC40 — Every criterion of the specs this cycle builds on still passes: 5A as amended, the live corrections, the test budget, and the three resolver-cache specs.

## Live validation — before anything is committed

The orchestrator runs these read-only. It takes a baseline on the current deployment before deploying, and runs the checks again after the new build has run at least 25 minutes. Checks that write (L5, L7, L11) run on a **copy** of the live database at an exact path shown to the maintainer first, inside the dev container, and never on the `data` volume's `opnview.db*`. Outputs are never committed, and only proportions are recorded in documents.

In every query, `:start` and `:end` are the `covered` bounds the API response reports.

- **L1 — Matrix total.** `interface_traffic_matrix`, `period=24h`, `scope=all`, `measure=bytes`: the sum of every cell, including the this-firewall row and column and the north-south column, equals:
  ```sql
  SELECT sum(packet_bytes) FROM classified_flow
  WHERE observed_at >= :start AND observed_at < :end AND exit_leg_device IS NULL;
  ```
- **L2 — Passed map.** `passed_traffic_world_map`, `period=24h`, `scope=all`: placed plus unplaced bytes equals L1's query with `AND action = 'pass'` added.
- **L3 — Tree.** `connection_tree`, `period=24h`, for each root: the inside root total equals the outside root total, which equals:
  ```sql
  SELECT count(*) FROM classified_flow
  WHERE observed_at >= :start AND observed_at < :end AND exit_leg_device IS NULL;
  ```
- **L4 — Attribution rate.** `attribution_rate_per_client`, `period=24h`: per client, the eligible, named and per-method counts equal `-- diagnostic: Attribution rate per client` over the same window.
- **L5 — `no_domains`** (on the copy, with `aggregate_mode` set to `no_domains` in the copy only):
  - every served type, for each period, returns no string equal to any value of `SELECT DISTINCT site_name FROM domain_attribution` or `SELECT DISTINCT domain FROM dns_resolution`;
  - every count equals its value under `full`.
- **L6 — Response time.** Every served type at its defaults, `24h` and `30d`, within the budget of open question 9. The times are reported.
- **L7 — Index** (on the copy):
  - `EXPLAIN QUERY PLAN` of `attribution_name_lookups` shows a `SEARCH` bounded by name and client on both branches;
  - an attribution pass over the last 24 h runs no slower than the baseline. Both times are reported.
- **L8 — Cache-read detail.** After deployment, the availability detail of the Unbound cache read either names each invalid-value category or reports none, per B6.
- **L9 — Host names.**
  ```sql
  SELECT client_resolution, count(*) FROM dns_resolution
  WHERE looked_up_at >= :start AND looked_up_at < :end GROUP BY 1;
  ```
  Expected: `ambiguous_hostname` down, or unchanged with C1's finding explaining why. Also, every `local_data_hostname` lookup's `client_address` is among the local-data addresses of its host label at its instant, which a planted-query check counts as 0 exceptions.
- **L10 — Screens.** The `-- screen: Overview` total `observed_bytes` over the L1 window equals L1.
- **L11 — Reclassification cost** (on the copy): the wall time of `ReclassifyEverything` is reported. A second run changes nothing.
- **L12 — Rules.** The share of `rule` rows with `destination_net` stored, compared with the rules the endpoint reports one for. Expected: all of them.
- **L13 — Blocklist.** Under the recommended semantics, expected 0:
  ```sql
  SELECT count(*) FROM blocklist b
  WHERE b.last_seen_at < (SELECT max(r.looked_up_at) FROM dns_resolution r
                          WHERE r.blocklist_id = b.id);
  ```
- **L14 — Regression.** V1–V10 of `specs/SPEC-resolver-cache-attribution.md` against the baseline: V2, V3, V4, V5 and V10 are still 0, and `live_paired_counted_twice` is still 0.

The validation passes when:

- L1–L4, L10, L13 and the zero checks of L5, L9 and L14 hold;
- L6 is within budget;
- the maintainer accepts L7, L8, L9, L11 and L12.

## Out of scope

- All interface work, which the parallel interface cycle owns:
  - templates, HTML, CSS, JavaScript, the canvas and the widget renderers;
  - the string catalogue, including the catalogue's Part 3 sentences;
  - every *Required statement* text;
  - how the observation limit, the method and the conditions are drawn.
- The three alert presets, severity resolution, and G7, G8 and G12 (step 6, or never for G12).
- Dashboard persistence, import, export and repair surfaces (step 7). The API resolves references; it does not store dashboards.
- Write endpoints: owner assignment, blocklist purpose, the aggregate-mode toggle, interface labels.
- Any new firewall endpoint, and any change on the firewall.
- Migrations (step 8). `schema.sql` is edited in place.
- Inferring a site name from a CNAME target, a PTR record or a registrable domain.

## Risks

1. **Scope.** There are 26 endpoints, about a dozen new read statements, eight proposals and a resolution rule. The Builder reports any part it could not finish rather than thinning it.
2. **One connection.** Widget reads queue behind derivations, `ReclassifyEverything` above all, and long reads delay collection. Response budgets may fail for this reason rather than because of the queries (open question 10).
3. **Test budget.** Many endpoint tests may push `checks` past 60 s. They need small fixtures, parallel tests and shared seeded databases.
4. **Two writers on binding documents.** The interface cycle may edit `docs/widget-catalogue.md` at the same time. This cycle therefore records defaults and classifications in the API document rather than in the catalogue, and reads the catalogue's parameter keys as they stand.
5. **Beyond-horizon answers.** Some figures, such as rules per matrix cell or distinct destinations per client, may not be derivable from the aggregates. They must surface as named states, and the maintainer may find them limiting.
6. **Group C may find no evidence.** If neither C1 candidate holds, the rule changes nothing and AC16's amendment only records why.
7. **Free-text `detail`.** Availability details are English literals written by the collectors. Passing them through the API puts untranslated text in front of the interface (open question 18).
8. **The B1 prefix rule is wider than the survey's list.** A read-only action with a mutating-looking prefix would become unreachable. The per-controller tests are the guard.
9. **The pairing-window bound** may refuse a value an operator already stored. The configuration load then fails loudly, which is the intended behaviour.

## Rules

- **Language and vocabulary.** Everything is written in English, and every term is OPNsense's, a product's, an RFC's or the catalogue's. A new term is recorded in *Vocabulary* with its reason.
- **Deletion.** Delete only exact paths, shown to the maintainer first. Never use wildcards or globs, and never run a destructive command in the background.
- **Git.** No git command that moves or discards work. **No commit before the live validation passes and the maintainer accepts it.**
- **The live database.** Never touch the `data` volume's `opnview.db*`. Writing checks run on a copy at an exact path.
- **No installation data in any file.** Fixtures are synthesised with a `__provenance__` block. Proportions only go in documents.
- **Sources.** Every OPNsense fact cites `opnsense/core` 26.7.3. An unverified claim is marked `UNVERIFIED:`, and nothing is built on it.
- **Read-only against the firewall; exactly three outbound calls; no hardcoded configuration; degrade with a named state, never a zero.**
- **Test budget.** `checks` stays under 60 s. `pre-deployment` runs before deployment.


## Group D — structured availability outcomes, and opnview's own downtime

Added by the maintainer on 10 October 2026. The parallel interface cycle
(`specs/SPEC-deferred-interface-bundle.md`) renders `source_availability.detail`
verbatim for now. It switches to these codes in a later pass.

### D1. A closed outcome code on `source_availability`

- Add a column naming the outcome of the last probe or read from a closed set.
  The set is the Builder's to derive from what the collectors already
  distinguish. It includes at least:
  - authentication failed (401);
  - denied (403);
  - not found (404);
  - undecodable;
  - `status` not `ok`;
  - transport failure;
  - each named absence (for example "this resolver offers no cache read
    through the API", the end-of-life plugin not installed);
  - present but disabled;
  - not yet probed;
  - healthy.
- A `CHECK` holds the closed set.
- **Every collector writes the code with the detail.** No reader parses
  `detail`.
- `detail` stays as diagnostic text.
- The widget API's `source_availability` preset, and any condition the API
  derives from availability, carry the code. That code is what an interface
  translates.
- The vocabulary and the meaning of each code are documented in
  `docs/data-model.md`.

### D2. Intervals during which opnview was not running

- Record a heartbeat, and on start detect the interval since the last one.
  That interval is written as a `collection_gap` (or a dedicated row; Builder's
  choice, justified) with a reason of its own: opnview not running.
- The heartbeat granularity and the threshold are settings rows with
  justified defaults.
- The table stays bounded: one row per interval, purged with the retention.
- The reason has its documented meaning. The widget API's gap and health
  readings include it.

### Acceptance criteria (Group D)

- [ ] AD1 — Every collector writes a code for every outcome it records. A test
  per outcome asserts the code. A grep shows no reader of `detail`. The
  `CHECK` refuses a code outside the set.
- [ ] AD2 — 401 and 403 give different codes, and so does each named absence.
- [ ] AD3 — A stop and a restart after an interval longer than the threshold
  record exactly one downtime interval, bounded by the last heartbeat and the
  restart. A restart within the threshold records none. A clean shutdown and
  a crash are both covered.
- [ ] AD4 — **Live validation.** After deployment, every `source_availability`
  row on the live database carries a code consistent with its detail. The
  service is stopped and restarted after more than the threshold, and the
  interval appears.

## Decisions (maintainer, 10 October 2026)

The maintainer accepted every planner recommendation:

| # | Question | Decision |
|---|---|---|
| 1 | Presets | 26 endpoints; the three alert presets answer `unknown_widget_type` until step 6 |
| 2 | Parameters | One `GET` parameter holding the dashboard-format `parameters` object as JSON |
| 3 | Path | `/api/widget/{type}`, documented in a new `docs/widget-api.md` |
| 4 | Unauthenticated | 401 with a JSON condition (`no_session` / `session_expired`) |
| 5 | Defaults | One defaults table in the API document (as proposed) |
| 6 | `no_domains` | Field-level withholding; `top_sites` and `sites_by_client` withheld whole; blocked lookups' domains count as domain names |
| 7 | Observation limit | Each volume figure carries its measure identifier and `lower_bound: true`; the API document states the limit; nothing is printed on screen (`CLAUDE.md`) |
| 8 | Buckets | Sub-hour buckets (1 m, 5 m, 15 m), UTC-aligned, from `flow` inside the horizon |
| 9 | Budgets | At 1 000 000 rows: 1 s (`24h`) and 2 s (`30d`); at 100 000: 250 ms and 500 ms; measured in pre-deployment |
| 10 | Concurrency | A small pool of read-only (`query_only`) connections for the API, closed at shutdown |
| 11 | Bare shapes | None |
| 12 | `destination_net` | Stored verbatim, never evaluated now; the reason is the rule table |
| 13 | Pairing window | Upper bound 60 s |
| 14 | `last_seen_at` | The latest `looked_up_at` naming the list; `first_seen_at` the earliest; never moves backwards |
| 15 | Dual-stack | Every C1 candidate that verifies, plus a distinct state `dual_stack_hostname` |
| 16 | Guard | Applied to `sql/schema-checks.sh` too |
| 17 | Reclassify cost | Measured and recorded only |
| 18 | `detail` in the API | Passed through verbatim as diagnostic data, beside the D1 code |
| 19 | Live-validation copy | Authorised: copy `opnview.db` to an exact path in the dev container's scratch space, delete it by exact path afterwards |

Risk 2 is answered by decision 10. Risk 7 is answered by D1.
