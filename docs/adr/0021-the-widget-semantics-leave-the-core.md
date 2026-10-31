# 0021 — The widget semantics leave the core: a shared JavaScript library, the rollups as a derivation, the end of the bridge

Status: Accepted
Decided by: the maintainer, 31 October 2026 ("Accepté, enchaîne les six étapes"), on the orchestrator's proposal of the same day, written from the live validation of ADR 0020's five waves, the widget host's use across the 30 built-in widgets, and the Go that still reads and writes the rollups. The two questions left to him at the end are decided with the first step that meets them, as recorded in the progress table.

## Context

ADR 0017 ordered the cut: the deletions, the write primitive, one source at a
time, **then the widget semantics to a JavaScript library (step d)**, then the
surfaces. Steps (a) to (c) are done: ADR 0020's five waves moved every source's
table into its plugin, validated on the maintainer's firewall on 31 October 2026.

Wave 5 stopped at the tables. The plan's row read "the 24 rollups and
`purged_flow_hour`, moved to a derivation of the plugin that draws them, or
deleted where the widgets read raw flows", with the bridge removed and
`purge.sql` empty. Neither could happen, for one reason: **the rollups are
read by the Go of the widget host, not by one plugin.** The audit's sentence
"only one plugin reads the rollups; every traffic widget reads raw flows" was
true of the plugins' own SQL and false of the host: `host.volume`,
`host.sites`, `host.clients`, `host.buckets` and the `host.figures` that build
on them are Go statements over the rollups (`store/reads/read.statements.sql`,
`widget/traffic/buckets.statements.sql`, `widget/traffic/clientvolume.statements.sql`,
`widget/figures/sites.statements.sql`), and the refresh that fills them is
Go too (`store/aggregates`, 391 lines and 608 of SQL). Cutting the refresh
would leave nine plugins' figures without a writer; removing the bridge would
break the readers. So the tables moved, and the Go kept reaching them
unqualified through the bridge, as ADR 0020's progress table records.

What the widgets depend on today, counted over the plugins' JavaScript:

| Host area | Uses | Plugins | What it is in Go |
|---|---|---|---|
| `host.figures` | 219 | 25 | Volume and Count figures, their sum, their measure and `measure_parts`, the coverage judgement |
| `host.params`, `host.refs`, `host.request`, `host.envelope`, `host.query`, `host.datasets`, `host.settings`, `host.json` | 330 | all | The generic host: the request, its parameters and references, the envelope, the widget's own statements, the catalogue. **KEEP** (ADR 0017) |
| `host.volume`, `host.sites`, `host.peers`, `host.clients`, `host.flows`, `host.series`, `host.attribution`, `host.samples`, `host.rates` | 112 | 14 | Reads of the rollups, the flows, the attributions, the samples and the rates, in the measure the core chose |
| `host.installation` | 27 | 15 | Reads of the installation model (interfaces, clients, rules, the firewall's own addresses) |
| `host.buckets`, `host.periods`, `host.mapfilters`, `host.matrixplacement`, `host.specialpurpose`, `host.services` | 52 | 11 | Arithmetic: time buckets, period slots, map filters, matrix placement, special-purpose addresses, the registrable domain |

Beside the areas, the core decides the **measure** of every Volume figure
(`volume_measure`, `best_available`, the `<name>_measured` twin a statement
declares, the run of a widget twice: `widget/request/measure.go` and
`speculation.go`, 814 lines). The Go of all this, non-test: about 5 700 lines
in `internal/widget/{traffic,figures,request,params/series}` and
`hostareas/{traffic,values}`, 930 in `store/reads/read.go`, 570 in
`store/aggregates`, and 1 400 lines of their SQL.

Two things the plugin API does not have today, and both are needed: **a
JavaScript library one plugin ships and other plugins use** (ADR 0017 names
it and nothing implements it: no `require`, no `libraries` key), and **a
derivation that runs before a required plugin's purge**, so the purged part of
an hour is written from the flows the purge is about to remove.

## Decision

1. **A plugin may ship a JavaScript library, and another plugin may use it.**
   A manifest lists `"libraries": ["<file>.js", …]`; the loader evaluates each
   once per runtime and binds it under `host.lib.<plugin>`. A plugin that
   `requires` the shipping plugin may call `host.lib.<plugin>.<function>`;
   one that does not is refused by name (`library_not_required`). A library
   is pure JavaScript: it is handed `host` by the caller and has no host of
   its own. This is the one new primitive of the loader, generic, naming no
   product.

2. **A built-in plugin `figures` carries the arithmetic the host does
   today**, as that library: the figures and their sum, counts, the measure
   and `measure_parts` kept through a sum, the coverage judgement, time
   buckets, period slots, map filters, matrix placement. The widget plugins
   `require` it and call `host.lib.figures.*` where they called
   `host.figures.*`, `host.buckets.*`, `host.periods.*`, `host.mapfilters.*`
   and `host.matrixplacement.*`. The special-purpose address table and the
   IANA protocol numbers become JSON the library reads (ADR 0017).

3. **Every data area becomes SQL of the plugin that owns the data**, read
   through `host.query` by the widget that draws it: the reads of the
   installation model are relations of `installation`; the reads of the
   rollups, the volume windows and the freshness are relations of `rollups`;
   the attribution rate is `unbound`'s; the samples and the rates are
   `insight`'s; the flows' detail is `pf`'s. A widget's own statement names
   them qualified, and the widget `requires` the owner. `host.volume`,
   `host.sites`, `host.peers`, `host.clients`, `host.flows`, `host.series`,
   `host.attribution`, `host.samples`, `host.rates` and `host.installation`
   are deleted with their Go once no built-in widget calls them.

4. **The refresh is the rollups plugin's SQL-only derivation**, `refresh`,
   after the `firewall_log` pass, after `pf/pair`, `clients/place` and
   `unbound/attribute`, and on a cadence: the same statements, the same
   staleness rule on `computed_at`, the same purged part, moved from
   `store/aggregates` as the attribution was from `store/attribution`, with
   a parity golden recorded from the Go before it is deleted. The daily
   per-address-pair volumes are a second derivation, `pair_volume`. The
   purged part is written by a derivation declared `before_purge` of
   `pf.flow`: **a new declaration key** of the derivation, run by the purge
   runner inside the purge's transaction before the required plugin's table
   is purged, generic and naming no product. The rollups and the purged part
   then declare their `references` to the installation model, which the
   declaration gains an `on_delete: cascade` for, and ADR 0020's dropped
   foreign keys are all restored.

5. **The measure is chosen in JavaScript.** `volume_measure` stays a
   parameter the core reserves and hands to every widget declaring
   `"volumes": true`; which measure can answer the window, the fall-back to
   `logged`, the twin statement run in the measure's place and the judgement
   of what the script built move to `host.lib.figures.measure(host, …)`, and
   `widget/request/measure.go` and `speculation.go` are deleted. The
   `connection_measure` and `measure_interval` tables, the measure registry
   and `flow_measure(...)` stay where ADR 0017 keeps them until a later record
   moves the measures with the plugins that declare them.

6. **Then the bridge ends.** When no Go statement names a plugin's table,
   the search path of opnview's own connection and of the read role is the
   core schema alone, `purge.sql` holds no plugin table, and ADR 0020's
   progress table closes wave 5.

## Order of the work, each step validated on the maintainer's firewall

| Step | What | Proof |
|---|---|---|
| 1 | The library primitive; the `figures` plugin with the arithmetic areas (buckets, periods, map filters, matrix placement, special-purpose, protocols); the 11 plugins that use them | `checks` green; the widget API answers the same bytes for the default dashboard, recorded before and after on the live database |
| 2 | The installation reads as `installation` relations; the 15 plugins | same |
| 3 | The refresh and the pair volumes as `rollups` derivations with their parity golden; `before_purge`; the foreign keys restored; `store/aggregates` and `collect/derive` deleted | row-by-row diff of the 26 tables on the live database, refresh by refresh, for a day |
| 4 | The data areas, one owner at a time: `rollups` (volume, sites, clients, buckets' figures), `pf` (flows), `unbound` (attribution), `insight` (samples, rates), `series`; the 14 plugins; `store/reads/read.go` and `widget/{traffic,figures}` deleted | the default dashboard's bytes, per widget, before and after |
| 5 | The measure choice in the library; `measure.go`, `speculation.go` deleted; the example reporter's third-party test kept green | the `netflow` and `logged` answers of `traffic_composition`, `owner_activity` and `traffic_sankey` byte for byte |
| 6 | The bridge removed; `purge.sql` empty; ADR 0020 wave 5 closed; ADR 0017's remaining deletions that waited on this (`shapes`, `widget/loader.go`, the script flat machinery) | `checks` green; the modularity allow-list shrinks |

## Progress

| Date | Step | Done |
|---|---|---|
| 31 October 2026 | 1, the library and the arithmetic areas | The primitive: a manifest key `libraries`, each file compiled at load and bound under `host.lib.<plugin>` in the widget runtime, the plugin script host and the harness (`t.lib`); a plugin may ship libraries and nothing else; refusals `library_missing`, `library_does_not_compile`, and `library_not_required` as the TypeError a script meets. The built-in `figures` ships `figures.js`: `buckets`, `periods`, `mapfilters`, `matrixplacement` and `specialpurpose` (the two IANA registries as data in the file, an address parser of its own), with its own tests; fifteen widget plugins require it and call it where they called the five host areas, whose Go is deleted with `internal/specialpurpose` and the two shape helpers. **Two departures from the step as written, both deferred rather than decided**: `host.figures` itself stays in Go until step 5, because its Volume, Count and `cover` record on the request what the measure engine judges (decision 5), and moving the arithmetic without the judgement would split one thing in two; and the IANA protocol numbers stay the script host's `opnview.protocolNumber`, used by the netflow derivation, until that plugin requires a library. **Proof**: every widget plugin's golden tests answer the same bytes as before, unchanged; the live comparison is the maintainer's review on screen, since the widget API answers only a signed-in session. |
| 31 October 2026 | 2, the installation reads | Four views of `plugins/installation` -- `interface_label` (the label resolved and `labelled`), `client_name` (`unstable_identity` as a boolean), `owner_name`, `rule_label` -- in the column order a response writes; the figures library's `installation` area reads them once per request through the widget's own one-line statements (`widget_installation_interfaces`, `widget_installation_client`, `widget_installation_owners`, `widget_installation_rule`, named apart from the core's own statements, which share one registry), which each of the fifteen widget plugins that named a member now declares in an `installation.statements.sql` of its own, requiring installation and figures. The host area `host.installation` is deleted. **Kept for now**: the Go reads of `widget/request/installation.go` and their statements, which the sites, attribution and rates areas still call; they go with those areas at step 4. **Proof**: the fifteen plugins' goldens unchanged; the installation plugin's test of the four views. |
| 31 October 2026 | 3, the refresh as the rollups plugin's derivation | `plugins/rollups` declares the SQL-only derivation `refresh` -- after the `firewall_log` pass (a push batch of flows is announced as a pass now), after `unbound/attribute`, and every minute -- 67 statements over a work table of candidate slots: the dirty hours by `ingested_at` and `purged_at` since the last watermark, the forced ones (`changed_at`, `connection_measure.computed_at`, the reassigned hours, the current slot), the purgeable pruned, the stale rule per period, then every family rewritten per period, the day's pair volume, the `pg_notify` the events surface relays, and the watermark advanced in `refresh_state`. **A core parameter `:ingest_watermark`** hands a derivation the oldest in-flight ingestion stamp, which the Go's refresh kept for itself; `owner_reassigned_hour` is the plugin's table, read and forgotten by the refresh, still written by the owners' Go through the bridge. **Parity proven** on a fixture of two days of flows, a purged part, an attribution and the owner family, recorded from the Go as `internal/plugin/testdata/rollups_parity.golden` (893 rows, every family in every period) before `store/aggregates`' refresh, `collect/derive`'s derivation, the owners' reassigned-hour reads and `ChangedFlowInstants` were deleted; a second run rewrites the current slots alone. **Three departures, deferred**: the rollups do not `require` clients, since clients requires rollups for its grant on the purged part and the loader refuses a cycle, so `clients/place` reaches the refresh through `changed_at` within the cadence rather than as a trigger; the foreign keys to the installation model wait for the cascade in the declaration (step 6); and `before_purge` with the purged part waits for the purge to be the plugins' own (step 6), the core purge writing the purged part through the bridge meanwhile. |

| 31 October 2026 | 4a, the volume window and the client volumes | `host.volume` and `host.clients` are the figures library's `volume` and `clients` areas, over statements each widget declares in `volume.statements.sql` and `clients.statements.sql`: the 1 h slots of `plugin_rollups` for the whole hours, `plugin_pf.classified_flow` at either edge, each with its measured twin, and the earliest slot; the library assembles the window as the Go did -- the rows summed by key through `host.figures.add`, sorted by bytes then key, the coverage from the earliest flow or slot with the left edge rule, the hour freshness recorded through `host.envelope` -- and the client volumes with their directions as a sorted map and their distinct peers. Ten widget plugins require pf and rollups. Deleted: the two host areas, `traffic.VolumeWindow`, `traffic/clientvolume` and its statements, `ReadVolumeWindow` and the `read_volume_*` statements of the store. **Proof**: the ten plugins' goldens unchanged. Found on the way, and fixed: loading a plugin cost one transaction per statement planned and one role grant per table, which the fifteen widget plugins now requiring rollups turned into a suite past its minute; a plugin's statements are planned in one transaction and remembered per process, and a plugin's role is given its schema once per start. |

## Consequences

- About 8 500 lines of Go and 1 400 of core SQL leave; the widget plugins
  gain the JavaScript that was the host's, and a third party's widget can do
  everything a built-in one does from the documentation alone, which today
  it cannot: nine host areas it depends on are Go it cannot extend.
- Two primitives are added to the loader, both generic: the library, and
  `before_purge`. Nothing else in Go names a widget, a figure or a measure
  once step 6 is done, which is what ADR 0002 demands.
- The cost is the parity work: every step compares the widget API's bytes on
  the maintainer's live database, recorded before, as ADR 0020 did for the
  attribution. It is a one-off comparison per step, in the dev container,
  never a gate in the suite (ADR 0012).
- A widget serving volumes becomes longer: it calls the library for what the
  core did silently. The library keeps the calls as short as the host's.
- Until step 6 the bridge stays, and ADR 0020 stays Proposed for its wave 5.
- **For the maintainer to decide**, besides accepting or refusing this
  record: whether the `before_purge` hook is the right shape for the purged
  part, or whether the purged part should go with the rollups' own retention
  (the rollups keep an hour's figures whatever the flows' retention; the
  purged part exists only so a slot straddling the horizon stays right);
  and whether `host.lib.<plugin>` or a `require("<plugin>")` in the script is
  the form he wants.
