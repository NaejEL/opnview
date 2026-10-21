# 0020 — The migration of the core tables, in waves

Status: Proposed
Proposed by the orchestrator, 18 October 2026, from the foreign-key graph of `schema.sql`, the views, the Go writers and the plugin readers, before any table moves

## Context

ADR 0019 decided the owners. The primitives exist (ADR 0018, granted writes,
the extended table declaration). What remains is the order, because the
tables hold each other:

- **The installation model is referenced by everything.** `client`,
  `interface`, `rule`, `owner` and `provider` are referenced by `flow`,
  `dhcp_lease`, `dns_resolution`, `security_event`, `measurement_sample`,
  `state_snapshot`, the 24 rollup tables, `purged_flow_hour`, `geo_asn`,
  `collection_gap` and the provider bookkeeping. `client` references
  `interface` and `owner`.
- **`flow` is referenced** by `domain_attribution` and read by the views
  `classified_flow`, `blocked_event` and `blocked_decision`; `blocked_decision`
  also reads `dns_resolution`, `security_event`, `interface_address` and
  `blocklist`.
- **The store applies `schema.sql` when it opens, before any plugin loads.**
  A core table that references a plugin's table cannot be created then, since
  the plugin's table does not exist yet.
- **Who names the tables unqualified today:** the Go writers
  (`store/flows/write.go` for every core kind, `network.go`, `owners`,
  `purge.sql`, `attribution`, `aggregates`), the widget engine's Go and SQL
  (`classified_flow` 16 places, `interface` 15, `flow` 14, `client` 9), and
  the plugins (`interface` 26 files, `flow` 26, `provider` 23, `client` 20,
  `rule` 12, `geo_asn` 11, `dns_resolution` 9, `security_event` 9).

Two consequences fall out: the installation model moves first, as one piece;
and the Go that still writes these tables, all of it scheduled for deletion
by ADR 0017, must keep working unchanged until its own turn, or every wave
becomes a rewrite of Go that is about to be deleted.

## Decision

### The bridge: a search path for the Go that will be deleted

While a Go writer or reader of a moved table still exists, opnview's own
connection and the read role resolve unqualified names through a **search
path that includes the plugin schemas of the moved tables**, in this order:
`opnview, plugin_installation, plugin_pf, plugin_leases, …, public`. The Go
SQL is not edited; it finds `flow` in `plugin_pf`. The bridge is a transition
device, named as such in `docs/architecture.md`, and it ends when the last
Go SQL naming a moved table is deleted (wave 5), at which point the search
path is the core schema alone again.

**Plugins never use the bridge.** Every plugin SQL file names a moved table by
its qualified name from the wave that moves it, in one mechanical rename per
wave, covered by the plugins' own tests and goldens. The plugin role's search
path stays `opnview, public`, so an unqualified name of a moved table in a
plugin's statement refuses the plugin at load, by name.

### Foreign keys from a core table to a moved table are dropped for the transition

A core table that still lives in `schema.sql` and references a moved table
(the rollups referencing `client`, `domain_attribution` referencing `flow`,
`dhcp_lease` referencing `client` and `interface` until its own wave) loses
that `REFERENCES` clause in the wave that moves its target, and regains it,
as `references` in its own declaration, in the wave that moves it. During the
transition the Go writers, which already resolve these ids before writing,
keep the integrity the constraint enforced. Each dropped constraint is listed
in this record's progress table so none is forgotten.

### The waves

| Wave | Plugin | Tables and relations | Also |
|---|---|---|---|
| 1 | `installation` (new, built-in) | `interface`, `interface_address`, `interface_map`, `interface_network`, `rule`, `client`, `owner`; the view `this_firewall_address` | the bridge; the rename in plugin SQL; the `REFERENCES` to these dropped from the core tables that stay |
| 2 | `pf` | `flow` with its indexes, checks and references; `classified_flow`, `blocked_event`; the pairing as a SQL-only derivation after each `firewall_log` pass; `writable_by: clients` on the end columns | `domain_attribution` and `purged_flow_hour` lose their `REFERENCES flow`; `blocked_decision` moves to the plugin that owns its last table (wave 4) and reads `plugin_pf.flow` meanwhile from the core |
| 3 | `clients` (new, built-in) | `address_classification`; the classification as SQL-only derivations after the passes of `firewall_log`, `dhcp_lease`, `neighbour`, `dns_lookup` and after `pf/pair` | **the live diff** (ADR 0019 §4) on a copy of the maintainer's database; `store/flows/classify.go` deleted |
| 4 | `leases` (new), `unbound`, `suricata`, `insight`, `maxmind` | `dhcp_lease` (granted to kea, dnsmasq, isc); `dns_resolution`, `blocklist`, `resource_record_*`, `domain_attribution`, the attribution derivation; `security_event`, `eve_ingest_cursor`, `provider_rule_info`, `blocked_decision`; `measurement_sample`, `state_snapshot`, `state_item`; `geo_asn` | `store/flows/write.go`, `attribution`, `samples`, `geo` deleted as each source's writer becomes the plugin's pass or push output |
| 5 | rollups | the 24 `*_volume_aggregate_*` tables and `purged_flow_hour`: moved to a derivation of the plugin that draws them, or deleted where the widgets read raw flows (ADR 0017) | the bridge removed; `purge.sql` empty |

### What each wave proves before it is committed

- `docker compose run --rm checks` green, the plugins' tests and goldens
  included.
- On a copy of the maintainer's database restored from `pg_dump`, migrated
  by starting opnview against it once: every moved table holds the same row
  count as before, and the widget API answers the same bytes for the default
  dashboard's widgets, recorded before and after. This is a one-off comparison
  made for the wave, in the dev container, never a gate added to the suite.
- Wave 3 adds the row-by-row diff of the classification and pairing columns
  and of `client`.

## Consequences

- The data a running installation holds is **rebuilt, not moved**: the
  maintainer's standing rule before the proof of concept is that the database
  is rebuilt, never migrated, and a wave creates its plugin's tables empty.
  The proofs below therefore compare row counts and widget answers on a copy
  that the new build has collected into, and wave 3 copies the raw rows into
  the new tables once, in the dev container, for its row-by-row diff.
- Five waves, five commits at least, each validated as above; wave 3 is the
  longest. Nothing reaches `main` before the maintainer has looked at the
  live service after each wave.
- The bridge is the one place the architecture is temporarily untrue, and it
  is written down here so that its removal is a step and not a discovery.

## Progress

| Date | Wave | Done |
|---|---|---|
| 18 October 2026 | 1, `installation` | The seven tables and the two views (`this_firewall_address`, and `blocked_decision`, which joins three sources' tables and lives with the model) declared in `plugins/installation`; the bridge; the read role and the plan checks of plugin statements set apart from it; the rename to qualified names in 37 plugin files, each of the 26 plugins now `requires` installation; the `REFERENCES` to the seven dropped from `flow`, `dhcp_lease`, `dns_resolution`, `security_event`, `purged_flow_hour` and the 24 rollups, with their `ON DELETE` actions; the test template holds the built-ins' tables and relations as a started service does; a column `default` and `nulls_not_distinct` on a unique index added to the declaration for `interface` and `interface_address`. Not yet: the proof on a copy of the maintainer's database, which needs his firewall. |
| 21 October 2026 | 2, `pf` | `flow` declared in `plugins/pf` with its identity, its eighteen checks (the pinned `traffic_scope` among them), its references to the installation model, its nine indexes, the join index on `canonical_address()` included (an index entry may be an expression now); `classified_flow` and `blocked_event` as pf's relations; `blocked_decision` moved from installation to a new relation plugin, `refusals`, which requires installation and pf, since the view reads `flow`; 32 plugin files renamed, 21 plugins require pf, 4 require refusals; `domain_attribution` loses its `REFERENCES flow`; the bridge gains `plugin_pf` and `plugin_refusals`; the harness loads a plugin's requirements transitively. **Re-cut**: the pairing stays in Go for this wave, and moves in wave 3 with the classification and the attribution, chained by `after_derivations`, so their order (pair, then classify, then attribute) is never broken between two commits. |
| 21 October 2026 | 3, `clients` | Two commits. First: the `clients` plugin, built-in, requires installation and pf; its tables `address_classification`, `placement` and `onlink`; its relations `to_inet`, `is_link_local`, `is_local_delivery`; its SQL-only derivation `place`, after the passes `firewall_log`, `dhcp_lease`, `neighbour`, `dns_lookup`, `security_event`, cadence 600 s, twenty-one statements in one transaction that classify the addresses of the evidence, mint clients, re-point flows, events, lookups and purged rows, then pair legs (`pair_records`, one statement); `client` writable by clients (ADR 0019), and the transition grants on `dns_resolution`, `security_event`, `purged_flow_hour` until waves 4 and 5; `pf.flow.changed_at`, stamped by the derivation, read by the Go refresh. Parity with the Go proven on a fixture and recorded as `internal/plugin/testdata/clients_parity.golden`. Second: `store/flows/classify.go`, `pair.go`, `pairchunk.go` and their statements deleted, with `PurgeDueAddresses`, the firewall-holdings fingerprint, and the `Addresses`, `Everything` and pairing fields of the derivation request; the passes ask the Go only for attribution and the refresh; `membership.go` keeps what the samples reader and discovery still call (`Membership`, `LoadPrefixes`, `client_identity_at_address`), which wave 4 revisits. Not yet: the live diff on a copy of the maintainer's database. |
| 21 October 2026 | 4a, `leases` | The first of wave 4's five owners. A new built-in, `leases`, owns `dhcp_lease` (identity, generated `generation_key` and `hostname_label`, references to the installation model, the unique key `(address, generation_key, provider_id)`, the checks); its SQL-only derivation `resolve`, after every `dhcp_lease` pass, names the interface a lease was given on from the key or the device the server reported and purges the leases past the installation's retention (`:retention`, a third core parameter). **The generic pass stores a core kind with no coordinator left**: `kinds/leases` is deleted, each lease source's script exports `collect(host, state, provider)` -- `provider` carrying the registry id and the kind's page size -- and answers rows for `leases.dhcp_lease`, a table the owner grants to **every provider of the kind** (`writable_by: [{"kind": "dhcp_lease"}]`, granted to each provider's role as it loads, so a third party's lease source needs no line of the owner's manifest); the scheduler runs one such loop per registered provider at the kind's own interval. The declarative read of a core kind targets a granted table, with the readings `provider_id` and `provider_key` (`docs/examples/example_leases`). The clients derivation mints the identity a lease names and points the lease at it (`mint_lease_clients`, first in `place`, which now runs after `leases/resolve`); `client_id` is granted to clients by column. A table numbered by an identity column and written by a pass is keyed by its first unique index, so a lease generation seen again is brought up to date (state, host name, observed_at) where the Go kept its first sighting. Deleted with the Go: `InsertDHCPLeases`, `vocab.DHCPLease`, `GenerationKeyOf`, the `lease_backend_failure_pass_limit` setting and its card (a backend that keeps failing no longer holds the host-name re-examination back: a lease pass that stored its rows lets the Go attribution examine the waiting lookups, until wave 4e moves it), `collect/identity`, `purgeconcurrency`; the role-catalogue writes run under the read role's advisory lock. Not declared: `REFERENCES provider`, a core bookkeeping table no wave moves; the loader's registry row is the id's source. |
| 21 October 2026 | 4b, `suricata` | `security_event` (identity, the unique key `(provider_id, provider_event_key)`, its seven indexes, its checks, references to the installation model, the end columns granted to `clients` by column -- the transition grant on the core table is gone) and `provider_rule_info` declared in `plugins/suricata`. The eve.json watermark is the provider's durable state (`file.<id>` = `offset:observed_at:rotation_state`), so `eve_ingest_cursor` is deleted rather than moved; a lost rotation is reported by the pass as a collection gap -- **a collect may answer `gaps`**, each under a reason the kind declares -- and marked lost once. The rule-info lookup is the plugin's script derivation `rule_info`, after each `security_event` pass and hourly, reading its own tables with `host.query`, asking the grid with `host.call`, bounded by two plugin settings that replace the kind's bounds (`rule_info_per_pass`, `rule_info_recheck_seconds`). Deleted: `kinds/securityevents`, `store/flows/ruleinfo.go` and its statements, the three writers and `EveWatermarks`, `vocab.SecurityEvent` and `EveCursor`, the declarative `rotation` case. Eight plugins reading the two tables name them qualified and require suricata. A row may leave out a column with a declared `default`. The bridge is no longer a list in Go: it is every built-in plugin's schema holding tables or relations, set by the store from the manifests. The core purge keeps deleting the events by `occurred_at` through the bridge until wave 5. |
| 21 October 2026 | 4c, `insight` | `measurement_sample` declared in `plugins/insight` (identity, the reading's unique key with `provider_id` nulls-not-distinct in place of the core's `coalesce` expression, the fine-sample partial index, the checks). The pass is `collect`, reading the interfaces from the core dataset `interface` through `host.datasets` instead of the Go's discovery snapshot, answering rows whose `provider_id` is null for the firewall's own gauges (`attributed: false`) and the outcome `partial_readings` itself. **The live pass is the collector's own loop** (`internal/collect/live.go`), generic over every provider declaring one: it calls the live function as `collect` is called, with `live_cadence_seconds` in the provider argument, and stores the rows it answers with `WriteLiveRows`, which never replaces a row already held -- a baseline pass that read the same capture keeps its row, and a baseline arriving after replaces the live row (the Go's conditional upsert, as two write modes); `latest` tells the live figures the newest instant. `reconciled_state`, a kind nothing ever provided, is deleted with `state_snapshot`, `state_item`, its view, its purge, its Go pass and its vocabulary. Deleted too: `kinds/measurements`, the sample and snapshot writers, the in-memory sample absences. A plugin loaded on its own reads the core datasets. Five plugins reading the sample table name it qualified and require insight. The core purge keeps purging the samples through the bridge until wave 5. |
| 21 October 2026 | 4d, `maxmind` | `geo_asn` declared in `plugins/maxmind` (the address as its key, the three indexes, a partial index on the pending rows, the checks). The placing pass is two derivations of the plugin: `mark`, SQL-only after each `firewall_log` pass, lists as pending every public address the flows name that no row holds -- the public ranges written in SQL with the clients plugin's `to_inet`, as the Go's `isPublic` decided them -- and re-marks what another provider placed; `locate`, a script derivation after `mark` and on its cadence, places the pending rows and the rows an older build placed from the databases on disk, through `host.query` on its own table and the script's own lookup, at most 20 000 a run. **A derivation may follow one of its own plugin's** (`after_derivations: ["maxmind/mark"]`). The kind keeps `status` and `refresh`, which the core's service loop still runs (the activation policy stays Go for now); its `lookup` function and its lookup cadence setting are gone with the Go placing pass and `store/geo`. Ten plugins reading or seeding the table name it qualified and require maxmind. The core purge keeps purging it through the bridge until wave 5. |
| 21 October 2026 | 4e, `unbound`, first commit | `blocklist`, `dns_resolution` (the placement columns granted to `clients` by column: the last transition grant is gone; `on_conflict: keep`, a new declaration key, since the ring buffer is read again at every pass and the rows were placed since), `resource_record_observation`, `resource_record_read` and `domain_attribution` declared in `plugins/unbound`. The three passes are `collect`: the query report walked page by page until a page brings nothing new, the new rows told apart with `host.query` on the plugin's own table, a rotated-out interval reported as a gap, the purge horizon read from the core dataset `retention_purge`; the cache and the local data normalised in the script (names, addresses, PTR owners, TTLs) and stored as observations extended or started against what the previous poll saw. The lists the refused lookups name and the machine behind a logged host name are the plugin's SQL-only derivation `resolve`, after the dns_lookup, dhcp_lease and resolver_local_data passes: a lookup is stored under the host name, unresolved, and examined once by the next run -- the leases first, the local data and its dual-stack rule next -- where the Go resolved it at insertion; the Go's rule that a failing lease backend holds the examination back is gone with it. Deleted: `kinds/lookups`, `kinds/resolverrecords`, `core/hostnamesources.go`, the `names_hosts` flag, the lookup and record writers, `vocab.DNSResolution`, the host-name resolution and the dual-stack rule of `store/attribution`. **The attribution itself is still the Go's**, `AttributeWith` reading and writing the plugin's tables through the bridge, until the second commit moves it; until then a flow is attributed again only when the clients derivation changed it, no longer when a record's coverage was extended. dnsmasq's three resolver kinds export `collect` saying why nothing is read. Eleven plugins name the tables qualified and require unbound. |
| | 4e second commit, 5 | not started: the attribution as unbound's derivation with a parity golden against the Go, then wave 5 |

**Dropped foreign keys to restore**, by the wave that moves the referencing
table: `flow` → interface, client, rule (wave 2); `dhcp_lease` → client,
interface (wave 4); `dns_resolution` → client, interface (wave 4);
`security_event` → client, interface (wave 4); `purged_flow_hour` → client,
interface, rule and the 24 rollups → client, interface, rule, owner, with
`ON DELETE CASCADE` (wave 5).
