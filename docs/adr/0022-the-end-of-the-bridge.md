# 0022 — The end of the bridge: the core's last reads and writes of the plugins' tables

Status: Proposed
Decided by: nobody yet. Written by the orchestrator on 1 November 2026, at the end of ADR 0021's step 6, which the maintainer reduced that day ("Étape 6 réduite maintenant") because the bridge could not end with that record's steps alone. The maintainer accepts, amends or refuses this record; nothing below is built before he does.

## Context

ADR 0020 moved every source's table into its plugin behind a **bridge**: a
search path that lets the core's own connection and the read role resolve an
unqualified name through the plugin schemas, "a transition device, named as
such, that ends when the last Go SQL naming a moved table is deleted". ADR 0021
moved the widget semantics, the refresh and the purge out of the core, and its
decision 6 ends the bridge "when no Go statement names a plugin's table".

On 1 November 2026 that condition is not met, and no accepted record moves
what is left. An inventory of the core's SQL that still names a plugin's table
through the bridge, by file, with what it is:

| Core file | What it does with the plugins' tables |
|---|---|
| `internal/store/flows/write.go` | **Writes the installation model and the flows**: `UpsertInterface`, `UpsertInterfaceAddress`, `UpsertInterfaceMapEntry`, `UpsertRule`, `UpsertClient(s)`, `InsertFlow(s)` into `plugin_pf.flow`, `StoredFlowDigests`, `NewestFlowObservedAt`, `LatestInterfaceDiscoveryAt`; the filter-log kind's push path and the discovery pass call them. |
| `internal/store/flows/network.go`, `prefixes.statements.sql`, `thisfirewall.statements.sql` | The interface networks (detected, operator-set, confirmed, removed), the on-link fingerprint, this firewall's addresses. |
| `internal/store/owners/owners.go`, `owners.statements.sql` | **The user's own writes**: the people, the assignments of clients to them (with the rollups' `owner_reassigned_hour`), the interface labels, the blocklist purposes; the `/api/owners`, `/api/clients` and labels surfaces call them. |
| `internal/widget/params/refs.statements.sql`, `candidates.statements.sql` | **The references and the candidates** of the widget parameters: an interface by identifier, device or label, a client by identity or hostname, a rule, an owner, a provider, a country, an operator, a site; the pickers' candidate pages. |
| `internal/dataset/core/*/relation.sql` | **The core datasets** of the data catalogue: `leg`, `traffic`, `dns`, `security_event`, `interface`, `firewall_metric`, `interface_throughput`, `this_firewall_address` (and `collection_gap`, `provider_value`, `read_through`, `retention_purge`, which read core tables alone). The netflow and insight plugins' derivations read some of them (`leg`, `interface`, `this_firewall_address`). |
| `internal/widget/request/service.statements.sql`, `traffic/buckets.statements.sql` | The engine's `widget_earliest_*` reads per bound kind, the hour freshness (`volume_aggregate_1h`, `flow`, `owner_reassigned_hour`), the purged-before read. |
| `internal/store/push/push.go` | The newest flow of a push path's digests; this firewall's current addresses. |
| `internal/store/reads/surfaces.go`, `internal/web/webtest/collectiontest`, `internal/collect/collecttest` | The interfaces listing of a surface; test harnesses. |

Two defects of the rebuild mechanism met on the same day bear on this (ADR
0021, row 6a): a table another plugin's table references cannot be set
aside, so a declaration change of the installation model is a refusal of the
installation plugin today; and the recorded declaration is the Go struct's
JSON, so a Go field changes every table's text unless left out when empty.

## Decision

Nothing is decided. The record proposes, for the maintainer to accept, amend
or refuse, one direction per row of the inventory, in the order that frees the
most with the least:

1. **The engine's reads become the widgets' own statements**, as ADR 0021's
   step 4 did for every other read: `widget_earliest_<kind>` declared by each
   widget bound to the kind, the hour freshness by the widgets that read the
   rollups, the purged-before read by the widgets that read the flows; the
   core's `coverFlows`, `hourFreshness` and the condition-from-earliest engine
   rule move to the figures library. No decision of substance: the same
   pattern, five more statements.

2. **The references and the candidates become declarations of the
   installation, maxmind and unbound plugins**: a plugin declares, in its
   manifest, the kinds of reference it resolves (`interface`, `client`,
   `rule`, `owner`; `country`, `operator`; `site`) and the statement of its own
   that resolves one and the one that lists candidates, and the core's
   `/api/references` glue runs them by kind. ADR 0017 left this "MOVE or KEEP
   as `/api/references` glue"; the glue stays, the SQL leaves.

3. **The core datasets become the plugins' datasets**: every table a plugin
   declares is already a dataset of the catalogue (ADR 0011); the eight
   relations under `internal/dataset/core` become relation files of the
   plugins whose tables they read (`leg` and `traffic` of pf, `dns` of
   unbound, `security_event` of suricata, `interface` and
   `this_firewall_address` of installation, `firewall_metric` and
   `interface_throughput` of insight), declared as datasets under the names
   the catalogue already serves, so no widget and no derivation changes.

4. **The user's own writes become the installation plugin's write
   statements**: the owners, the assignments, the labels and the purposes as
   statements the plugin declares, run by the surfaces through the write
   primitive (ADR 0018) with the plugin's refusal codes carried as they are
   today. The `owner_reassigned_hour` write moves with them to a statement of
   the rollups plugin the installation plugin's statement calls, or to a
   derivation of the rollups plugin over `client.owner_assigned_at`.

5. **The installation model and the flows are written by derivations of the
   installation and pf plugins over the broker's mirror** — the largest
   piece, and the one that empties `store/flows`: discovery (`interface`,
   `interface_address`, `interface_map`, `interface_network`, `rule`) as
   SQL-only derivations over the mirrored endpoints, the flows as the pf
   kind's push output table written by the loader (ADR 0018's push primitive,
   as the netflow records are), the clients as the clients plugin already
   mints them. This needs the mirror to serve the discovery endpoints as
   datasets, which ADR 0017 keeps (`mirror/`, KEEP).

6. **The bridge ends** when 1–5 are built: `sqldb.SetBridgeSchemas` goes,
   `builtintables.go` goes, the test template holds the plugins' tables as
   the loader creates them, ADR 0020's wave 5 closes.

7. **The rebuild of a referenced table** is decided apart: either the set-aside
   drops the dependent foreign keys with the key and the referencing tables
   are rebuilt in the same start, or a declaration change of a referenced
   table is refused by name with the reason, as it is today by accident.

## Consequences

- Steps 1 to 3 are pattern work with parity goldens; 4 touches the API's
  refusal codes, which the front reads; 5 is the one that needs design, and
  a live validation of discovery and of the push path on the maintainer's
  firewall before `main`.
- Until 6, the bridge stays and ADR 0020 stays Proposed for its wave 5.
- Each step is validated on the maintainer's firewall as ADR 0021's were, and
  recorded in a progress table here once he accepts the record.

## Progress

| Date | Step | Done |
|---|---|---|
