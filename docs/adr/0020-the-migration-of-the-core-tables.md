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
| | 3 to 5 | not started |

**Dropped foreign keys to restore**, by the wave that moves the referencing
table: `flow` → interface, client, rule (wave 2); `dhcp_lease` → client,
interface (wave 4); `dns_resolution` → client, interface (wave 4);
`security_event` → client, interface (wave 4); `purged_flow_hour` → client,
interface, rule and the 24 rollups → client, interface, rule, owner, with
`ON DELETE CASCADE` (wave 5).
