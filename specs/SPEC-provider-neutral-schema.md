# SPEC — Make the step-2 schema provider-neutral

Status: APPROVED

## Context

Step 2 landed an executable schema at commit `62aea26`. It describes exactly
one provider per source, and two of its tables are shaped by one vendor's
transport details:

- `source_availability.source` carries
  `CHECK (source IN ('filter_log', 'suricata_eve', 'netflow_insight',
  'dhcp_leases', 'resolver_dns'))` — a closed enum, so registering a provider
  is a migration.
- `alert` is not an alert table but an eve.json table: it keys uniqueness on
  `(file_id, file_pos)`, an ingestion-cursor concern, and carries
  `signature_id INTEGER`, which a provider whose rules are named rather than
  numbered cannot use.
- `ids_rule_info` inherits both problems, keyed `signature_id INTEGER PRIMARY
  KEY`.
- `geo_asn.maxmind_build_at` puts a vendor name in the schema.

**Why now.** No database exists anywhere: the schema has never run outside the
checks. Changing a `CHECK` or splitting a table today is editing a file; doing
it once a Proxmox CT holds six months of history is a migration with data
carry-over.

**Why not more than this.** Step 1 designed the whole site-name story around
Suricata `tls` / `dns` events that turn out not to exist (survey, gaps 1 and
2). An abstraction designed before seeing the data is wrong. This cycle defines
the seam — *a provider contributes events of a given kind and declares its own
availability* — and does **not** invent the universal shape of a security
event. No CrowdSec column, no Zenarmor column, no speculative field for a
provider nobody has surveyed. When a second provider is actually installed, it
gets surveyed as OPNsense was.

**What is already modular.** The resolver (Unbound / Dnsmasq) and the DHCP
server (Kea / Dnsmasq / ISC) are already several implementations detected at
runtime and normalised into `dns_resolution` and `device`. Their adapters are
not reworked by this cycle. The filter log is not abstracted: there is exactly
one pf log and no second implementation exists.

Two facts established by the Planner and relied on below: `eve_ingest_cursor`
**already exists** as a separate table, keyed `UNIQUE (file_id, byte_offset)`,
so nothing needs creating; and there is a genuine contradiction between "a
normalised severity in the neutral core" and the step-2 AC20, which asserts by
`pragma_table_info` that the event table carries **no** severity column at all.
Decision 2 below resolves it.

## Decisions taken with the maintainer

1. **The neutralised event table is `security_event`**, matching the kind name,
   and **there is no provider-named side table**. The only thing a Suricata
   detail table would have carried is `file_id` / `file_pos` — an ingestion
   coordinate, not event data — and that coordinate already lives in
   `eve_ingest_cursor`. The core carries `provider_event_key TEXT`, which the
   collector composes (for Suricata, from the file id and offset), and
   `UNIQUE (provider_id, provider_event_key)` is the idempotence guarantee.
   One guarantee correctly placed beats two guarantees one of which forces a
   vendor name into the schema.
2. **Severity: a nullable normalised column on the core, NULL for Suricata.**
   The Alerts query still resolves severity from the `get_rule_info/<sid>`
   cache exactly as today. The step-2 AC20 is restated as: no
   Suricata-attributed row carries an inline severity value. The door is open
   for a provider that ships severity inside its event; nothing about today's
   behaviour changes.
3. **The normalised severity vocabulary is ordered text**: `critical`, `high`,
   `medium`, `low`, `informational`, with the mapping from Suricata's numeric
   1..4 documented. Neutral by construction rather than one vendor's scale
   promoted to universal.
4. **The registry holds one row per implementation, not one per kind.** Unbound
   and Dnsmasq are two providers of the DNS-lookup kind; Kea, Dnsmasq and ISC
   are three of the lease kind. The registry becomes real rather than notional,
   and "two providers of one kind" is exercised by genuine rows instead of a
   synthetic one. Step 1's detection already distinguishes them.
5. **An explicit active marker**, separate from reachability: a machine may
   have two implementations of a kind installed and reachable, and the model
   must say which one `opnview` actually reads. Constrained so that **at most
   one provider per kind is active**, enforced by a partial unique index. On a
   freshly migrated database no provider is active: activeness is determined by
   step-4 detection, not by a migration.
6. **One idempotence guarantee, on the core**: `UNIQUE (provider_id,
   provider_event_key)`. The transport coordinate is not duplicated onto the
   event; `eve_ingest_cursor` keeps the separate watermark and rotation
   concern, which is a different guarantee about a different thing, and
   `docs/data-model.md` must state that difference so step 4 does not merge
   them.
7. **`eve_ingest_cursor` keeps its name.** Renaming a table for a provider that
   has not been surveyed is exactly the speculation this cycle forbids. It is
   Suricata-specific by nature and stays so until a second ingesting provider
   exists.
8. **The geo/ASN provider gets an availability row**, and
   `docs/data-model.md` states the distinction: that row describes the
   *dataset's* reachability, while `geo_asn.lookup_state` describes a single
   address lookup.
9. **Migrations are edited in place.** `migrations/0001_core.sql` and
   `migrations/0002_aggregates_and_defaults.sql` only; no `0003` that `ALTER`s
   tables created in `0001`. There is no deployed database to protect, and a
   schema whose final shape requires reading three files would be a permanent
   cost paid to protect nothing.
10. **Step-2 AC23 ("exactly five sources are modelled") is restated**, not
    weakened, as: every registered provider holds exactly one availability
    state drawn from `reachable` / `present_but_disabled` / `unavailable`, with
    a timestamp and a probe, an invalid state is rejected by a constraint, and
    every provider that exists today is registered. Stronger in one direction;
    it no longer pins a count, which it cannot once the registry is data.
11. **No provider name appears in any table or column name, without
    exception.** Not `maxmind`, not `suricata`, not any future one. A provider
    is a row in the registry and a foreign key, never an identifier in the
    DDL. `eve_ingest_cursor` is the single exception and is itself provisional
    (decision 7): it names a file format, not a product, and is rewritten when
    a second ingesting provider is surveyed.
12. **The attribute rule, for provider-specific data that does not fit the
    core.** A typed, indexed column for anything a screen **aggregates or
    filters on**. A JSON column, read only on a detail screen and never
    aggregated, filtered or joined, for an attribute that is only ever
    displayed. Never an entity-attribute-value table: it turns one row read
    into N reads plus a pivot, loses types and constraints, and destroys index
    selectivity on exactly the predicates the screens use — it would make these
    queries slower, not faster. Never a provider-named table.
13. **Covering indexes for the screen queries**, and a scale check to prove
    they hold. Column count is close to irrelevant to SQLite query speed; index
    selectivity and whether the index answers the query without fetching the
    row are what matter. Where a screen query aggregates a measure over an
    indexed range, the measure belongs in the index so the plan reads the index
    alone. And the 100 000-row baseline is a test size, not a production one: a
    single scale run at 1 000 000 rows in the largest growing table must show
    the plans do not change shape.

## Scope

No Go code. This cycle is SQL and documentation. The Go module is untouched.

### Files to modify

1. **`migrations/0001_core.sql`** — the registry table; `source_availability`
   referencing it; `alert` neutralised into `security_event`; `ids_rule_info` keyed per provider with a TEXT rule
   identity; `geo_asn` with a vendor-neutral dataset build-date column and a
   provider reference. Indexes renamed and recreated over the new columns so the
   screen query plans are preserved.
2. **`migrations/0002_aggregates_and_defaults.sql`** — seeds the registry with
   the providers that exist today, one row per implementation, none active, and
   creates exactly one availability row per registry row in the not-yet-probed
   `unavailable` state. An absent row is something a screen cannot tell apart
   from a healthy provider.
3. **`sql/queries/screens.sql`** — the Alerts query joins the core to the
   rule-info cache on `(provider, rule identity)`; Device and Map read the
   renamed build-date column. Seven screens, seven queries, unchanged semantics.
4. **`sql/queries/diagnostics.sql`** — the availability diagnostic returns one
   row per registered provider with its kind, key, state, probe and whether it
   is the active provider for its kind.
5. **`sql/seed.sql`** — exercises the new structures: the registry with today's
   providers, one active per kind, security events attributed to a provider, a
   rule-info cache keyed per provider, at least one **non-numeric** rule
   identity, and a cache miss.
6. **`sql/purge.sql`** — purges `security_event` by `occurred_at`; the detail
   follows by cascade; registry and rule-info cache stay bounded and unpurged.
7. **`sql/schema-checks.sh`** — every assertion naming `alert`,
   `ids_rule_info`, `signature_id`, `maxmind_build_at` or "five sources" is
   restated against the new structures **without weakening**, plus the new
   criteria below.
8. **`docs/data-model.md`** — entity list, growing/bounded lists, per-entity
   sections, purge section and the seven-query table brought into step.
9. **`ROADMAP.md`** — only if a statement in it becomes false; otherwise
   untouched.

### Files to create

10. **`docs/architecture.md`** — short. Names the ports and the six kinds,
    states that a provider declares its own availability, that availability is a
    modelled state rather than an absence of rows, that at most one provider per
    kind is active, and plainly that the shape of a future provider's events
    will be surveyed rather than guessed. A seam document, not a plugin
    specification.

## Acceptance criteria

Every criterion is verified by executing a command in the development
container. Every database a check creates lives under `/data`.

**Regression — nothing gets weaker**

- [ ] AC1 — `docker compose run --rm schema-checks` exits `0`, reports
      `checks failed : 0`, and its passed count is **greater than or equal to**
      the count on `main` before this cycle. That pre-change count is captured
      by running the command before the first edit and quoted in the final
      report. A criterion deleted rather than restated fails this.
- [ ] AC2 — `docker compose run --rm checks` exits `0`.
- [ ] AC3 — All 45 acceptance criteria of
      `specs/SPEC-data-model-sqlite-schema.md` still hold, verified the same
      way. Those naming a renamed object are restated against the new name only;
      none is removed, relaxed or turned into an inspection. The restated AC20
      asserts that no Suricata-attributed row carries an inline severity value;
      the restated AC23 is decision 10 above.
- [ ] AC4 — The seven screen queries each return at least one row against the
      default seed **and** the alternative seed, and `EXPLAIN QUERY PLAN` for
      each shows no `SCAN` of any table `docs/data-model.md` lists as growing.
      All seven plans recorded verbatim.
- [ ] AC5 — The index-drop demonstration still degrades: dropping the index the
      document names load-bearing for the Alerts query turns that plan into a
      scan of `security_event`, on a throwaway copy. Overview and Map unchanged.
- [ ] AC6 — `PRAGMA integrity_check` returns `ok` and `PRAGMA foreign_key_check`
      no rows, before and after the purge; the purge still removes every event
      older than the horizon, none newer, and leaves no orphan detail row.

**The registry**

- [ ] AC7 — No table carries a `CHECK` enumerating source names: the
      `sqlite_master` SQL contains none of `'filter_log'`, `'suricata_eve'`,
      `'netflow_insight'`, `'dhcp_leases'`, `'resolver_dns'` inside a
      constraint. Registering a provider is an `INSERT`, not a migration.
- [ ] AC8 — Registering a new provider of a kind that already has one succeeds
      with no DDL: inserting a second security-event provider, its availability
      row and events attributed to it exits `0`, and the Alerts query afterwards
      returns rows attributed to **both**, each naming its own provider.
      Executed on a copy of the seeded database.
- [ ] AC9 — A registry row whose `kind` is outside the six documented kinds is
      rejected by a constraint (failing insert), and `SELECT DISTINCT kind` in
      the live database diffs empty against the six the document lists.
- [ ] AC10 — Every availability row references an existing registry row
      (foreign key demonstrated by a failing insert), and on a freshly migrated,
      unseeded database every registry row has exactly one availability row in
      the not-yet-probed `unavailable` state. A count mismatch either way fails.
- [ ] AC11 — The registry holds **several providers for at least two kinds** —
      the DNS-lookup and lease kinds — reflecting the implementations step 1
      established, and the document lists them with the survey citation for each.
- [ ] AC12 — At most one provider per kind is active, enforced by a constraint:
      marking a second provider of the same kind active fails. On a freshly
      migrated, unseeded database **no** provider is active; the seed makes
      exactly one active per kind, and a query returns them.
- [ ] AC13 — One query in `sql/queries/diagnostics.sql` returns, for every
      registered provider, its kind, key, state, probe and active flag —
      executed, one row per registry row.

**The security-event core**

- [ ] AC14 — `security_event` carries no ingestion-transport column:
      `pragma_table_info` returns no column whose name contains `file`, `pos` or
      `offset`. No provider-named side table exists anywhere in
      `sqlite_master`.
- [ ] AC15 — The provider's own event key is the uniqueness key: replaying
      `INSERT OR IGNORE` of every existing core row leaves the event count
      unchanged, and a duplicate `(provider_id, provider_event_key)` insert is
      rejected by a constraint. `eve_ingest_cursor` keeps its own separate
      watermark guarantee, and `docs/data-model.md` states why the two are
      different guarantees about different things.
- [ ] AC16 — The rule identity is TEXT on both `security_event` and the
      rule-info cache, asserted by `pragma_table_info`, and the seed contains at
      least one event whose rule identity is **non-numeric**, returned by the
      Alerts query with its severity resolved from the cache.
- [ ] AC17 — Suricata severity behaviour is unchanged: every seeded
      Suricata-attributed event carries a NULL normalised severity, and the
      Alerts query returns `severity_state = 'resolved'` with a non-null
      severity for a rule identity present in the cache and
      `severity_state = 'unknown'` with a null severity for one that is not —
      both counts at least 1, from the query text in `screens.sql`.
- [ ] AC18 — The rule-info cache is keyed per provider: the same rule identity
      under two providers yields two rows, both retrievable, and the Alerts
      query attributes each event to its own provider's entry.
- [ ] AC19 — The normalised severity is constrained to `critical`, `high`,
      `medium`, `low`, `informational`: a value outside it is rejected by a
      constraint (failing insert). `docs/data-model.md` states the vocabulary,
      its order, and the mapping from Suricata's numeric 1..4.

**Geo/ASN**

- [ ] AC20 — **No provider name appears in any table or column name, with no
      exception.** A query over `sqlite_master` joined to `pragma_table_info`,
      covering both table names and column names, returns nothing for
      `*maxmind*`, `*crowdsec*`, `*zenarmor*` or `*suricata*`. The only
      permitted occurrence of a provider name anywhere in the database is as a
      **value** — a registry row, or a seeded string — never as an identifier.
- [ ] AC21 — Stale-dataset visibility survives: every `geo_asn` row with
      `lookup_state = 'resolved'` carries a non-null dataset build date and
      names the provider that supplied it, enforced by a `CHECK` demonstrated by
      a failing insert; a seeded `miss` row still exists and is still returned
      by the Map query.

**Migration discipline, documentation, hygiene**

- [ ] AC22 — `migrations/` holds exactly two `.sql` files, `schema_version`
      holds exactly two matching rows after a fresh apply, both directions
      diffed; applying twice is a no-op with nothing on stderr; no `0003*` file
      exists.
- [ ] AC23 — The entity list in `docs/data-model.md` and live `sqlite_master`
      are identical in both directions, and every table is classified growing or
      bounded, including those this cycle creates.
- [ ] AC24 — `docs/architecture.md` exists and, asserted by the harness, names
      the six kinds, states that a provider declares its own availability, that
      availability is a modelled state rather than an absence of rows, that at
      most one provider per kind is active, and that a future provider's event
      shape will be surveyed rather than guessed. It describes no plugin,
      manifest, dynamic-loading or external-process mechanism.
- [ ] AC25 — Every source-fed table and column still carries, in
      `docs/data-model.md`, the OPNsense endpoint and API field it comes from,
      with the citation already present in `docs/opnsense-api-survey.md`. The
      endpoints the restructured tables consume are cited in the DDL comment of
      the consuming table, and the document restates that an unavailable
      provider is recorded as an availability state and never rendered as an
      absence of data.
- [ ] AC26 — No speculative provider anywhere: a grep over `migrations/`,
      `sql/` and `docs/` finds no column, `CHECK` value, seeded row or
      documented field named for a provider not installed and surveyed today.
- [ ] AC27 — Zero hardcoded configuration survives: no dotted-quad, CIDR,
      interface name, VLAN name or segment name literal in the migrations, the
      queries, the seed, the purge, the harness or the documents — the existing
      grep extended to `docs/architecture.md`.
- [ ] AC28 — Everything added is in English; every `.sh` and `.sql` file is
      LF-terminated; `bash -n` and `shellcheck` on the harness exit `0`; and
      `git status --porcelain` lists only the files this spec names plus this
      spec file — no `*.db`, `*.db-wal`, `*.db-shm`, no scratch file.

**Performance at volume**

- [ ] AC29 — Covering indexes: for every screen query whose plan
      `docs/data-model.md` claims is covered, `EXPLAIN QUERY PLAN` prints
      `USING COVERING INDEX`. The document names, per query, which index covers
      it and which measures were added to it for that purpose. A query the
      document claims is covered but whose plan does not say so fails.
- [ ] AC30 — Scale check: one run seeds 1 000 000 rows in the largest growing
      table and re-runs the seven screen queries. Every plan keeps the same
      shape as at the 100 000-row baseline — no `SCAN` of a growing table
      appears, and no query that was covered stops being covered. The seven
      plans at scale are recorded verbatim alongside the baseline ones, and the
      wall-clock time of each query at scale is reported so a regression later
      has a number to compare against.
- [ ] AC31 — No entity-attribute-value structure was introduced: no table whose
      rows are `(entity, attribute name, value)` triples, asserted by
      inspection of the entity list, and `docs/data-model.md` states the
      attribute rule of decision 12 so the question does not reopen.
- [ ] AC32 — The `flow` table's 31 columns are reviewed and the document
      justifies each one that is not read by any of the seven screen queries,
      the purge or the aggregate refresh — or the column is removed. A column
      nothing reads and nothing explains fails this criterion.

## Out of scope

- Any Go code: no storage package, no migration runner, no provider interface
  in Go, no collector.
- Any plugin system: no dynamic loading, no external process, no manifest.
  `opnview` stays a single Go binary; this cycle only lets the data model
  describe more than one provider per kind.
- Designing the event shape of any provider not installed today.
- Abstracting the filter log; reworking the resolver and DHCP adapters.
- Provider **selection policy** — how step 4 decides which provider to make
  active. This cycle models the marker; setting it is step 4.
- Any UI, endpoint, template or screen; any live OPNsense instance; MaxMind
  acquisition.
- IPv6 seed coverage, already recorded against step 4.

## Risks

- **The regression surface is the whole of step 2.** Four of 21 entities change
  and five SQL files plus one document follow. A criterion restated too loosely
  would pass while the behaviour it protected is gone; AC1's pass-count floor
  and AC3's no-weakening clause are the only defence, and both depend on the
  Builder being honest about restatement. The Verifier must treat restatement
  as the primary thing to attack.
- **Severity normalisation is where this cycle can over-design.** The
  vocabulary is chosen from one surveyed provider. It is neutral by
  construction, but its adequacy for a second provider is unproven.
- **The registry seed is a literal list of provider names in a migration.** It
  is data, not configuration — a firewall with no Suricata still gets a Suricata
  row in the `unavailable` state. That is the modelled-state design working as
  intended, recorded here so it is not mistaken for a hardcoding defect.
- **Covering indexes cost write throughput and disk.** Widening an index to
  cover a query makes every insert into that table more expensive. The
  collectors of step 4 write continuously, so the trade is real; the scale run
  of AC30 reports query time but not ingest time, and that gap is deliberate —
  measuring ingest needs a collector, which does not exist yet.
- **The active marker pre-empts a step-4 policy.** Modelling it now is cheap;
  the risk is that step 4 discovers selection needs more than a flag.
- **By-signature aggregation at step 6 will now compare strings** rather than
  integers. Noted, not solved here.
- **Unverified survey claims persist**: `dhcp_lease.backend = 'isc'` and
  `dns_resolution.resolver = 'dnsmasq'` still rest on `UNVERIFIED:` markers.
