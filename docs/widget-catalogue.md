# Widget catalogue — shapes and presets

**A widget is a shape, and the data is a parameter.** The rule is stated once, in
`ROADMAP.md` under *Rules that apply to every step*, and this document is its
inventory rather than a second copy of it. What follows is **two inventories**:
the **shapes**, which are what gets built, and the **presets**, which are a
shape, a binding and a title chosen because they answer a question worth
answering. A preset is not a thing to build.

The binding documents for any interface work are `docs/ui-references.md`,
`docs/widget-catalogue.md` (this file), `docs/dashboard-format.md`, and
`ROADMAP.md` steps 3 and 7 as amended. The five settled product decisions are in
`docs/ui-references.md`, *The product as decided*, and are not restated here.

## What reads this document, and the format that follows from it

Three consumers, and the third is a program.

1. `docs/dashboard-format.md` takes `widget.type` from a preset's **Type** field.
   **The identifiers are stable**: renaming one breaks every dashboard file that
   used it, so a rename is a compatibility event and not an edit.
2. `ROADMAP.md` step 7 makes a preset's fields the **widget manifest's schema**.
3. `docs/mockups/check-form-covers-catalogue.js` **parses this file**: one record
   per `### ` heading, the identifier from the first backticked token of the
   `**Type**` line, the parameter keys from the backticked tokens of the
   `**Parameters**` paragraph. It compares that set in both directions against
   the mockup and its transcription.

So the format is load-bearing. **Presets are `### ` headings carrying `**Type**`
and `**Parameters**`. Shapes are `#### ` headings carrying `**Shape**` and
`**Shape parameters**`** — deliberately different field names, so a shape is
never read as a widget type. **No parameter key changed when this document was
restructured**, and enumerated values were kept, because that two-way comparison
is what stops the mockup and the catalogue drifting apart.

## Conventions

**Illustrative values.** Where an example address is needed it comes from the
documentation ranges — `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`,
`2001:db8::/32` — and is labelled as an example. **No interface name, VLAN name,
CIDR or address of any real network appears anywhere in this document.** Every
such value is discovered at runtime (`docs/opnsense-api-survey.md`, *Runtime
discovery*) and is never hardcoded, in code, in a template, in a default value or
in a preset.

**Sources.** Every preset rests on the authenticated OPNsense REST API and on
nothing else, plus the MaxMind download. No entry proposes reading a file on the
firewall, an SSH session, or `opnview` changing a setting: where a source has to
be switched on, that is a manual operation the user performs, documented in the
README at step 8.

**The missing-from-model marker.** It appears **exactly once per open gap, in the
*Gaps found* table, and nowhere else**. A `grep -c` therefore returns the number
of open gaps, which is the row count of that table's open rows. *This reverses
the earlier convention*, which put the marker in each **Data** field and banned
it from the table; with bindings gathered in one place, scattering it no longer
serves the count. **A closed gap loses its marker and keeps its identifier**, and
**the identifier is retired and never reused** — which is why the open ones are
not contiguous.

## Two standing facts — documented, never printed on screen

They bind the *model*: they govern what a figure means. They are **not** UI copy.

1. **The observation-point limit.** `opnview` sees only what crosses the router.
   Traffic between two clients behind one interface never reaches the firewall
   and is invisible to all five sources, so **every byte, packet and connection
   figure is a lower bound**. One preset is exempt and says so:
   *Interface throughput*.
2. **Site names are inferred.** On OPNsense 26.7 a site name comes from
   correlating a resolver lookup with a flow that followed it, and from nothing
   else: Suricata exposes no `dns` event type and its `tls` / `http` events
   cannot be read back (`docs/opnsense-api-survey.md`, *Gaps and alternatives*,
   gaps 1 and 2).

---

# Part 1 — The shapes

**Twelve.** Each one is a drawing with a data requirement; the subject is a
parameter. The count is derived from what the presets in Part 2 actually need,
not assumed: the nine shapes `ROADMAP.md` names draw 24 of the 29 presets, and
the other five force `feed` and `cards` and make `repeat` a shape parameter. The
derivation, and the check against sources not yet built, are in *Why twelve* at
the end of this part.

## How to read a shape

Five fields: **Shape** (the identifier), **Draws**, **Requires of its data**,
**Shape parameters**, **Empty and absent**. Two rules apply to every shape and
are stated here once rather than twelve times.

**Truncation is never silent.** Any shape with a limit renders what it dropped as
an explicit remainder carrying its own count — an **Other** slice, an *N further*
band, a remainder child, a remainder chip, a remainder row. A shape that
truncated quietly would misstate every proportion drawn beside it.

**A figure the shape cannot show is not shown as a fragment.** No truncated
address, no clipped label, no drawing whose names are dropped: a shortened
address reads as a different address (`ROADMAP.md` step 7, *What the step-3
mockup proved*).

**Four conditions, not three.** Every shape distinguishes *not yet probed*,
*source unavailable*, *source present but disabled*, and *reachable and no rows*.
The **sentences** are a property of the source and are in Part 3; what the shape
decides is the **rendering**, below. A zero is drawn only for the fourth
condition, and only when a measurement actually says zero.

**Repeat.** Every shape accepts `repeat`: draw one instance per member of a
**runtime-discovered** key set — per discovered interface, per discovered
temperature sensor, per mounted filesystem. It exists because the members and
their number cannot be known when the preset is written, and a fixed tile count
would be hardcoded configuration.

**Inline series.** `figure`, `donut`, `table` and `cards` accept an inline series
(a sparkline) drawn at the resolution the samples actually have, newest at the
right, with no smoothing — `docs/ui-references.md`, *The maintainer's recorded
preferences*, *Charts, histograms, pie charts*.

#### One figure

**Shape** — `figure`

**Draws** — One number, its unit, its label, an optional inline series, an
optional threshold colouring, an optional comparison with the preceding period.

**Requires of its data** — A single scalar, with a unit.

**Shape parameters** — `period`, unit and format, `thresholds` (**no default
threshold is ever assumed**: a sensible temperature or disk figure depends on the
hardware, and assuming one would be an assumption about the installation),
inline series on or off, last-sampled stamp on or off, `repeat`, `layout`
(`tiles` | `rows`) when repeated, `compact`.

**Empty and absent** — The tile keeps its label and renders an explicit **dash**,
never a zero: a zero CPU percentage and an unknown CPU percentage are different
facts. Where a reading's response key was not found, the state is *absent*, not
zero (`docs/data-model.md`, `measurement_sample`).

#### Ranked bars

**Shape** — `ranked_bars`

**Draws** — Labelled magnitudes as horizontal bars, longest first, each bar
carrying its primary figure and optional secondary figures and badges, and
optionally split within the bar into named parts.

**Requires of its data** — A set of labelled magnitudes over one dimension.

**Shape parameters** — `period`, `measure`, `limit`, `sort`, within-bar split on
or off, include-empty-members on or off, `repeat`.

**Empty and absent** — Discovered members that are silent are **listed with an
empty bar rather than omitted**, so a silent member is visibly silent. For the
first three conditions no bars are synthesised.

#### Time series

**Shape** — `time_series`

**Draws** — One or more series of instants as lines, areas, bars or points, on
shared or paired axes, optionally stacked or banded by a category.

**Requires of its data** — A series of instants, each a value at a time, per
series.

**Shape parameters** — `period`, `bucket`, `measure`, `series` (an ordered list —
see `docs/dashboard-format.md`, *Parameters*, fourth value shape), `axes`,
`stacked`, `legend`, `tooltip` (`single` | `all_series`), `annotations`,
per-series `render` and `aggregation`, `repeat` / `per_interface_axis`.

**Empty and absent** — **Axes are drawn in every condition.** A missing series is
a **labelled gap**, never a line at zero and never a straight line between the
two surrounding points: a gap in a sampled series means `opnview` was not
collecting. **Degradation is per series** — a chart of four series with one
missing must not look like a chart of three. No curve smoothing; where a series
is aggregated to fit the pixels available the widget **says which aggregation it
applied**.

#### Table

**Shape** — `table`

**Draws** — Rows and columns, sortable, with optional expandable rows, optional
inline series in a cell, and first-class rows for values that did not resolve.

**Requires of its data** — Rows and columns, every column with a declared type
and unit.

**Shape parameters** — `period`, `columns`, `limit`, `sort`, `min_count` /
`min_flows` style thresholds, expansion on or off, `compact`, `repeat`.

**Empty and absent** — **Rows whose join failed are first-class rows**, labelled
with what failed — *rule no longer exists*, *address not matched to a client*,
*unresolved* — and never dropped, because dropping them under-reports. A column
whose source is unavailable renders as a **labelled column-level notice**, not as
an empty column. `ROADMAP.md` step 3 bans walls of dense tables: a table is a
reading of a bounded set, not a log dump.

#### Matrix

**Shape** — `matrix`

**Draws** — A grid of pairs: one axis of row keys, one axis of column keys,
each cell carrying one or more figures, optionally colour-scaled. Cells are
clickable and open the underlying records. The two axes may draw on **different**
vocabularies (clients against site names) and the column set may be per-row.

**Requires of its data** — A grid of pairs: two key dimensions and a measure per
pair.

**Shape parameters** — `period`, `measure`, `sort`, per-axis `limit`, colour
scale on or off, `repeat`.

**Empty and absent** — In *reachable and no rows* **the grid is drawn with its
axes and empty cells**, so the shape of the network is still legible. A row with
no pairs at all keeps its row and says so explicitly rather than being dropped.
A heatmap is this shape with a colour scale; no separate shape exists for one.

#### Map

**Shape** — `map`

**Draws** — **Point marks on a de-saturated basemap**, sized by a measure, with
the detail in a popup rather than on the canvas, optionally clustered at low
zoom with the merged count shown inside the mark. **No choropleth and no arcs**:
the map is background, the data is foreground
(`docs/ui-references.md`, *The maintainer's recorded preferences*, *World map*).

**Requires of its data** — Points with coordinates, plus — mandatorily — the
volume that has **no** coordinates.

**Shape parameters** — `period`, `size_by`, `min_bytes` / `min_count`,
`cluster`, `show_unplaced` (default true and not recommended to disable).

**Empty and absent** — **The unplaced counter is mandatory and is shown even at
zero**, so its absence never means "we forgot to check": *"N connections and X
bytes could not be placed: M addresses have no geolocation answer, K have not
been looked up yet"*, clickable, opening that list. A destination the dataset
could not place **must not silently vanish**; mapped volume plus unplaced volume
equals the total. A map that drops what it cannot place lies about the total and
is forbidden from doing it.

#### Donut

**Shape** — `donut`

**Draws** — Parts of a whole as a ring with a ranked legend beside it, the total
in the hole, and an optional inline series beneath.

**Requires of its data** — Parts of a whole: labelled magnitudes over one
dimension that sum to a stated total.

**Shape parameters** — `period`, `measure`, `slices`, `show_other` (default true
and not recommended to disable), inline series on or off.

**Empty and absent** — **The grouping dimension is always on screen**, in the
card's subtitle: *38 % of traffic* means four different things depending on
whether the slices are clients, interfaces, protocols or ports. In *reachable and
no rows* the ring is drawn empty with the total reading zero, so the widget reads
as measured rather than broken.

#### Tree

**Shape** — `tree`

**Draws** — An expandable hierarchy rooted at a **chosen subject**, one level per
level of the question, every node carrying its own rolled-up figures.

**Requires of its data** — A hierarchy: an ordered list of levels, each level a
key resolvable from the one above.

**Shape parameters** — `root`, `depth`, `limit_per_level`, `measure`, `sort`,
include-refused on or off.

**Empty and absent** — **A node's children sum exactly to the node**: an
expansion that did not reconcile would teach the reader to distrust every figure
above it, so a level truncated by `limit_per_level` carries an explicit remainder
child. A level whose naming source is unavailable **still renders**, by whatever
does resolve, with a level-wide notice — never an empty level. The shape issues
one query per expanded level, so an unexpanded branch costs nothing. The root is
a subject the reader chooses; **rooting at the firewall says only that traffic
crossed the firewall, which is true of everything on the page** (`ROADMAP.md`
step 7). `root` accepts **one subject or several**, and Malcolm's two mirrored
trees from one root are one value of it rather than the shape's definition
(`docs/ui-references.md`, *Connection tree*). Data that is not a hierarchy at
all belongs on `graph`, not here.

#### Graph

**Shape** — `graph`

**Draws** — A node-link graph: nodes placed by the layout rather than by a
hierarchy, edges carrying a measure, node size and colour driven by a **named,
user-chosen weight**, and a **docked detail panel** rather than tooltips
(`docs/ui-references.md`, Arkime). It has **no single root**: a subject may be
selected and its neighbourhood emphasised, and de-selecting it leaves the graph
standing.

**Requires of its data** — A set of nodes with identities, and a set of edges
between them with a magnitude. Nothing hierarchical: an edge may close a cycle,
and a node may have several parents.

**Shape parameters** — `period`, `measure`, `weight` (which measure drives node
size), `max_nodes`, `min_share`, `focus` (an optional subject to emphasise),
`neighbourhood_depth` when focused, `layout`, `show_labels`, `detail_panel` on or
off.

**Empty and absent** — Nodes over `max_nodes` or under `min_share` collapse into
one explicit **Other** node carrying its own edge total, the same truncation rule
as every other shape. Discovered nodes with no edge in the period are still drawn
unconnected, so a silent part of the network reads as silent rather than as
absent. **Never animated particles on edges**: motion-as-volume carries
information at ten nodes and becomes texture at a hundred
(`docs/ui-references.md`, *Connection tree — rejected*). That note rejects a
**rendering**, not this shape.

#### Sankey

**Shape** — `sankey`

**Draws** — Flows between nodes: sources down the left, destinations down the
right, a ribbon per pair whose width is proportional to the measure. **Every end
is labelled with a resolved name rather than a number.**

**Requires of its data** — Flows between nodes: a left key, a right key and a
magnitude.

**Shape parameters** — `period`, `measure`, `left`, `right`, `max_nodes`,
`min_share`, blocked-share encoding on or off.

**Empty and absent** — Ribbons below `min_share` collapse into a single explicit
**Other** band carrying its own total, so a long tail can neither make the
diagram unreadable nor be hidden. In *reachable and no rows* the discovered nodes
are still drawn unconnected down the left, so a silent network reads as silent
rather than as absent. Nodes whose naming source failed reach the **Other** band
rather than disappearing.

#### Feed

**Shape** — `feed`

**Draws** — Records in reverse-chronological order as **entries rather than
cells**: heterogeneous rows, each naming its own kind, its subject, its target
and what produced it, groupable and filterable by any of those.

**Requires of its data** — Records carrying an instant, a kind and a subject;
their remaining fields need not be the same from one record to the next.

**Shape parameters** — `period`, `limit`, `group_by`, kind filter.

**Empty and absent** — **Per contributing source, never collapsed**: each source
in a degraded state gets its own labelled row inside the feed, so an empty feed
is never read as "nothing happened" unless every contributor is healthy. Where a
record's producer cannot be named, that is stated **in the row** rather than left
blank.

*This is not a table.* Its rows have no common column set, its order is the
instant, and its degraded states live inside the list.

#### Cards

**Shape** — `cards`

**Draws** — One card per group: the group's name, its figures, an optional inline
series, and its **members as chips, each carrying its own state**.

**Requires of its data** — Groups, each with figures and a member list.

**Shape parameters** — `period`, `measure`, `sort`, members-per-card before a
remainder chip, inline series on or off, include-the-residual-group on or off.

**Empty and absent** — **A chip carries its own member's state**, so a member
that reported nothing reads *no data in this period* rather than as a zero the
reader might take for the member's absence. **A residual group is rendered like
any other** — the unclaimed majority of a set is not a footnote — and when it is
hidden the card header says so, because a narrowed total that does not announce
itself is a wrong total. In *reachable and no rows* cards are still drawn, with
members listed and figures at zero beside an explicit *measured, and nothing
happened* label, which is a different statement from *we could not look*, and the
two are never rendered the same.

*This is not a table row.* A cell cannot carry a child entity's own degraded
state. `docs/ui-references.md`, *Per-device view*, records the preference for this
rendering.

## Why twelve

The count is derived, and the derivation is the argument.

| Shape | Presets on it |
|---|---|
| `figure` | 2 |
| `ranked_bars` | 3 |
| `time_series` | 4 |
| `table` | 11 |
| `matrix` | 2 |
| `map` | 2 |
| `donut` | 1 |
| `tree` | 1 |
| `sankey` | 1 |
| `feed` | 1 |
| `cards` | 1 |
| `graph` | 0 today |

Four findings, recorded because they are what the count rests on.

1. **`feed` and `cards` are the two shapes the nine-shape guess was missing**, for
   the reasons in their entries. Both are independently required by material
   outside this catalogue: the survey's application-layer request log and
   `state_item_departure` for the first, `docs/ui-references.md`'s per-device
   preference for the second.
2. **`graph` carries no preset today, and is a shape anyway.** It is the one
   entry here derived from a **requirement** rather than from an existing preset:
   *where does a chosen subject go preferentially* (`docs/ui-references.md`,
   *Connection tree*). The surveyed FRR neighbours and routes and LLDP neighbours
   are node-link data, and `tree` and `sankey` each force a structure onto them
   that the data does not have. A binding will come with the connector that
   brings the data; the shape is declared now so that connector does not arrive
   with data nothing can draw.
3. **`repeat` is a parameter, not a shape.** Two presets draw one instance per
   member of a set discovered at runtime. A twelfth shape called "tile group"
   would have hardcoded what the set is.
4. **`custom_chart` is not a preset at all.** It is `time_series` with no
   binding — the user names the series. It is listed in Part 2 for compatibility
   with `widget.type` and with `docs/dashboard-format.md`'s worked example, and
   it is the plainest evidence that the old structure mistook a shape for a
   subject.
5. **No shape was added for a heatmap, a gauge or a choropleth.** The first is
   `matrix` with a colour scale, the second `figure` with thresholds, and the
   third is refused by the maintainer's recorded map preference.

**An earlier revision of this section refused the `graph` shape, and the refusal
was wrong.** It read *"Vizceral: liked less, too cluttered"* as a rejection of
node-link graphs. It is a rejection of **animated particles on every edge** —
`docs/ui-references.md`, *Connection tree — rejected*, says so in its own
*Avoid* clause, and the row above it records that Malcolm and Arkime, both
node-link, are **liked**. The same document already carries the companion
correction: *"The single root was this document's reading, not his
instruction, and it produced a widget that says only that traffic crossed the
firewall."* Two readings of the maintainer's preferences, in the same table, both
narrowing a rendering note into a product limit. The shape exists; the rendering
note applies to how it is drawn.

**The check that matters, run against sources not yet built.** Reconciled state
(`state_snapshot` / `state_item`) draws as `table` for the current set and `feed`
over `state_item_departure` for what has left. Monotonic counters — the surveyed
per-peer `transfer-rx` / `transfer-tx` — draw as `time_series` with a rate
aggregation. Gauges from UPS, SMART and sensor plugins draw as `figure` with
`repeat`. Per-request HTTP records draw as `feed`, `ranked_bars` and `table`.
Percentiles, minima and maxima per bucket are `time_series` aggregations, not new
shapes. Node-link data — FRR neighbours and routes, LLDP neighbours — draws as `graph`.
**Nothing in `docs/opnsense-api-survey.md`, *What the plugin ecosystem actually
exposes*, is undrawable by these twelve.**

---

# Part 2 — The presets

A preset is **a shape, a binding and a title**, chosen because it answers a
question worth answering. Everything it needs beyond that is data.

## How to read a preset

- **Type** — the `snake_case` identifier a dashboard file puts in `widget.type`,
  an API path segment names it by, and an export writes back. **This document is
  the vocabulary.** Stable, unique, and unchanged by this restructure.
- **Shape** — which of the twelve draws it. A preset names exactly one.
- **Question** — the question a reader is asking when they place it.
- **Binding** — the entity, the dimensions it groups by, the measures, and the
  providers it depends on. What the shape needs, said in this subject's terms.
- **Data** — the `table.column` pairs in `internal/store/schema.sql` that feed
  it, and the surveyed source of each.
- **Parameters** — what the reader can set, and what an exported dashboard file
  therefore records. **Verbatim from the previous revision**; see *What reads
  this document*.
- **Required statement** — present only where a preset must carry a sentence
  neither the shape nor Part 3 supplies. Five presets of twenty-nine have one.
- **Gaps** — the open gaps it depends on, by identifier.

Anything else a preset seems to need is a shape rule (Part 1), source copy
(Part 3), or a model fact (Part 4).

**Every control offered changes what is drawn, or it is not offered**
(`ROADMAP.md` step 7). A parameter listed below that renders inert is a defect in
the build, not a decoration in this document, and
`docs/mockups/check-inert-parameters.js` exists to catch it.

## Preset index

| Type | Shape | Title | Query in `sql/queries/screens.sql` | Gaps |
|---|---|---|---|---|
| `interface_traffic_matrix` | `matrix` | Interface traffic matrix | reuses `-- screen: Matrix`; adapts for 7 d / 30 d | — |
| `traffic_over_time_by_scope` | `time_series` | Traffic over time by scope | adapts `-- screen: Overview` | G3 |
| `interface_volume_ranking` | `ranked_bars` | Interface volume ranking | adapts `-- screen: Matrix` | — |
| `client_volume_ranking` | `ranked_bars` | Client volume ranking | adapts `-- screen: Interface` | G4 |
| `client_traffic_detail` | `table` | Client traffic detail | reuses `-- screen: Client` | — |
| `traffic_composition` | `donut` | Traffic composition, now | adapts `-- screen: Overview` and `-- screen: Interface` | G12 |
| `connection_tree` | `tree` | Connection tree | composes `-- screen: Interface` and `-- screen: Client` | — |
| `owner_activity` | `cards` | Per-person activity | composes `-- diagnostic: Clients per owner` with `-- screen: Interface` | — |
| `top_sites` | `table` | Top sites | none applies | G5 |
| `sites_by_client` | `matrix` | Sites by client | replaces the site-name half of `-- screen: Client` | G5, G6 |
| `attribution_rate_per_client` | `table` | Attribution rate per client | `-- diagnostic: Attribution rate per client`, unchanged | — |
| `passed_traffic_world_map` | `map` | Passed-traffic world map | adapts `-- screen: Map` | — |
| `blocked_traffic_world_map` | `map` | Blocked-traffic world map | adapts `-- screen: Map` **and** `-- screen: Alerts` | G7, G8 |
| `destination_countries` | `table` | Destination countries | reuses `-- screen: Map` | — |
| `destination_operators` | `table` | Destination operators | reuses `-- screen: Map` | — |
| `traffic_sankey` | `sankey` | Traffic Sankey | adapts `-- screen: Matrix` or `-- screen: Map` | G4 |
| `unified_blocked_feed` | `feed` | Unified blocked feed | **replaces** `-- screen: Blocked` | G2, G8 |
| `blocked_by_firewall_rule` | `table` | Blocked by firewall rule | adapts `-- screen: Blocked` | — |
| `blocked_dns_lookups` | `table` | Blocked DNS lookups | `-- diagnostic: Blocked lookups by list` | G6 |
| `security_alerts_over_time` | `time_series` | Security alerts over time | adapts `-- screen: Alerts` | G8 |
| `alerts_by_signature` | `table` | Alerts by signature | reuses `-- screen: Alerts` | G8 |
| `alerts_by_client_and_interface` | `table` | Alerts by client and interface | reuses `-- screen: Alerts` | G7, G8 |
| `public_address` | `table` | Public address | none applies | G13 |
| `firewall_health_overview` | `figure` (repeated) | Firewall health overview | none applies | — |
| `interface_throughput` | `time_series` | Interface throughput | none applies | — |
| `custom_chart` | `time_series` | Custom chart | composes per series | — |
| `source_availability` | `table` | Source availability | `-- diagnostic: Source availability`, unchanged | — |
| `aggregate_freshness` | `table` | Aggregate freshness | `-- diagnostic: Aggregate coverage per period`, unchanged | — |
| `unresolved_joins` | `figure` (repeated) | Unresolved joins | adapts `-- screen: Matrix` for the rule counter | — |

**The seven queries in `sql/queries/screens.sql` remain authoritative and are not
deleted** (`ROADMAP.md` step 7). The column above is the mapping step 5 must not
rediscover.

## Traffic and volume

### Interface traffic matrix

**Type** — `interface_traffic_matrix`

**Shape** — `matrix`

**Question** — Which interface talks to which, how much, and how much of it was
blocked?

**Binding** — Source interface × destination interface. Each cell carries the
observed byte volume, the allowed connection count, the blocked connection count
and the distinct rule descriptions that matched. The right-hand column is
north-south traffic, where the destination sits in no discovered interface.
Interface labels are the user's own label where one is set and the firewall's
discovered description otherwise — **never a name-based classification**: an
interface's nature is never inferred from what it is called. Provider:
`firewall_log`.

**Data** — `flow.src_interface_id`, `flow.dst_interface_id`,
`flow.traffic_scope`, `flow.packet_bytes`, `flow.action`, `flow.observed_at`,
`flow.rule_id`, `flow.rule_lookup_state`; `rule.description`;
`interface.user_label`, `interface.description`. For 7 d and 30 d the same shape
is read from `volume_aggregate_7d.bytes`, `.allowed_connections`,
`.blocked_connections`, `.src_interface_id`, `.dst_interface_id`,
`.period_start_at` and the `_30d` twins. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; interface
discovery, *Runtime discovery* (i); rule discovery, *Runtime discovery* (ii).

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `scope`
(`all` | `east_west` | `north_south`); `measure` (`bytes` | `allowed` |
`blocked`); `interfaces` (an optional list of interface references limiting the
rows and columns; empty means every discovered interface); `show_rules`
(boolean).

### Traffic over time by scope

**Type** — `traffic_over_time_by_scope`

**Shape** — `time_series`

**Question** — How much traffic left the network, entered it, and stayed between
interfaces, over time?

**Binding** — One band per selected scope — outbound, inbound, inter-interface —
with the totals brought forward above the chart. Outbound and inbound derive from
`flow.direction`; inter-interface is `traffic_scope = 'east_west'`. Provider:
`firewall_log`.

**Data** — `flow.observed_at`, `flow.direction`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.src_interface_id`,
`flow.dst_interface_id`. Source: filter logs, *Data source 1 — Filter logs*.

**Parameters** — `period`; `scopes` (any subset of `outbound`, `inbound`,
`inter_interface`); `measure` (`bytes` | `connections`); `interfaces` (optional
list of interface references); `stacked` (boolean).

**Gaps** — **G3**: the volume aggregates carry no `direction`, so for any
pre-computed period outbound and inbound cannot be told apart and only
`east_west` versus `north_south` can. The widget says so in place.

### Interface volume ranking

**Type** — `interface_volume_ranking`

**Shape** — `ranked_bars`

**Question** — Which VLAN or interface is generating the traffic?

**Binding** — Interfaces by observed volume, each bar carrying the byte total,
the client count behind it and the blocked connection count, with the east-west
and north-south shares shown as parts within the bar. Labels from the user's own
label, falling back to the firewall's discovered description. Provider:
`firewall_log`.

**Data** — `flow.src_interface_id`, `flow.packet_bytes`, `flow.action`,
`flow.traffic_scope`, `flow.observed_at`, `flow.src_client_id`;
`interface.user_label`, `interface.description`, `interface.link_kind`,
`interface.is_tunnel`, `interface.vlan_tag`. Long periods:
`volume_aggregate_7d.src_interface_id`, `.bytes`, `.allowed_connections`,
`.blocked_connections` and the `_30d` twins. Sources: *Data source 1 — Filter
logs*; *Runtime discovery* (i).

**Parameters** — `period`; `measure` (`bytes` | `connections` | `clients`);
`scope` (`all` | `east_west` | `north_south`); `limit` (how many interfaces to
show); `include_unlabelled` (boolean — whether interfaces the user has not
labelled are listed).

### Client volume ranking

**Type** — `client_volume_ranking`

**Shape** — `ranked_bars`

**Question** — Which machine, by MAC or by address, is generating the traffic?

**Binding** — Clients by volume, with hostname where a lease supplied one, the
MAC where one is known, the last observed address, the interface, the byte total,
the blocked connection count and the count of distinct destinations. A client
whose MAC carries the IEEE locally-administered bit is badged **unstable
identity**: a randomised MAC does not identify a machine across sessions and has
deliberately not been merged into a phantom client. **The ranking is per machine,
and that is a different question from the per-person one** — *Per-person
activity* asks that one, and answering one with the other would be a substitution
rather than an answer. Providers: `firewall_log`, `dhcp_lease`.

**Data** — `flow.src_client_id`, `flow.src_interface_id`, `flow.packet_bytes`,
`flow.action`, `flow.observed_at`, `flow.dst_address`; `client.hostname`,
`client.mac`, `client.mac_is_randomised`, `client.unstable_identity`,
`client.identity_kind`, `client.identity_key`, `client.last_address`,
`client.interface_id`, `client.vendor_hint`, `client.owner_id`. Sources: *Data
source 1 — Filter logs*; client identity from leases, *Data source 4 — DHCP
leases*.

**Parameters** — `period`; `interfaces` (optional list of interface references);
`identity` (`any` | `dhcp_client_id` | `mac` | `address_in_interface` — which
identity levels to include); `limit`; `include_unstable` (boolean); `measure`
(`bytes` | `connections` | `destinations`).

**Gaps** — **G4**: no per-client volume aggregate, so a 7 d or 30 d per-client
total can only be computed by scanning `flow`, which the `retention_seconds` row
of `setting` bounds.

### Client traffic detail

**Type** — `client_traffic_detail`

**Shape** — `table`

**Question** — Where did this one machine go, and what was allowed?

**Binding** — One client's outbound records over the period, **and which server
issued each of its leases** — a client can hold leases from two DHCP servers at
once, so the source is a fact the reader is entitled to rather than a
de-duplication detail. Per record: timestamp, destination address and port,
protocol, action, traffic scope, byte count, country, operator, and the inferred
site name where a resolver lookup could be correlated. A record with no
attribution is shown with a null site name and its address, country and operator
— never omitted, because an unattributed destination is a fact rather than a gap
in the table. The correlation delay is shown per attributed row, so a wide delay
reads as a weak attribution. Providers: `firewall_log`, `dns_lookup`, `geo_asn`,
`dhcp_lease`.

**Data** — `flow.id`, `flow.observed_at`, `flow.dst_address`, `flow.dst_port`,
`flow.protocol`, `flow.action`, `flow.traffic_scope`, `flow.packet_bytes`,
`flow.src_client_id`, `flow.interface_device`, `flow.interface_lookup_state`;
`domain_attribution.site_name`, `domain_attribution.correlation_delay_seconds`;
`geo_asn.lookup_state`, `geo_asn.country_code`, `geo_asn.operator`,
`geo_asn.dataset_build_at`; `interface_map.description`. Sources: *Data source 1
— Filter logs*; *Data source 5 — Resolver DNS lookups*; geo and ASN enrichment,
the second of the two outbound calls the project allows.

**Parameters** — `client` (a client reference — required); `period`; `action`
(`all` | `allowed` | `blocked`); `scope` (`all` | `east_west` |
`north_south`); `columns` (which of the available columns to show); `limit`.

### Traffic composition, now

**Type** — `traffic_composition`

**Shape** — `donut`

**Question** — What is the traffic made of right now, and in what proportion?

**Binding** — Volume as parts of the period's total, grouped by one of four
dimensions, with the total in the hole and a rolling inline series beneath.
Providers: `firewall_log`; and `measurement_sample` for the `service_port`
grouping beyond the `flow` horizon, which is where the sampled per-pair volume
lands (`docs/data-model.md`, `measurement_sample`).

**Data** — `flow.packet_bytes`, `flow.observed_at`, `flow.protocol`,
`flow.dst_port`, `flow.src_client_id`, `flow.src_interface_id`, `flow.action`,
`flow.traffic_scope`; `client.hostname`, `client.last_address`;
`interface.user_label`, `interface.description`. For 7 d and 30 d the client and
interface groupings read `volume_aggregate_7d.bytes`, `.allowed_connections`,
`.blocked_connections`, `.src_interface_id`, `.peer_address`, `.period_start_at`
and the `_30d` twins. The `service_port` grouping may additionally read
`pair_volume_observation.service_port`, `.protocol`, `.octets` and
`.day_start_at`, **labelled approximate** for the reason in Part 4. Sources:
*Data source 1 — Filter logs*; per-pair volume, *Data source 3 — NetFlow /
Insight*.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `group_by`
(`client` | `interface` | `protocol` | `service_port`); `measure`
(`bytes` | `connections`); `slices` (how many named slices before the collapsed
one); `interfaces` (optional list of interface references); `clients`
(optional); `scope` (`all` | `east_west` | `north_south`); `show_other`
(boolean, default true and not recommended to disable).

**Gaps** — **G12**: nothing in the model classifies a flow as an application, so
this preset offers four groupings rather than five and **application is not one
of them**. Neither route to one is implemented, deliberately; see *Gaps found*.

### Connection tree

**Type** — `connection_tree`

**Shape** — `tree`

**Question** — Where does a chosen subject — an interface, a machine or a person
— go preferentially?

**Binding** — Levels: the chosen root, then client, then destination (named by
its site name where one was inferred and by its operator and country otherwise),
then service port and protocol. Blocked connections are drawn with their own mark
rather than filtered out, so a branch that exists only because something was
refused is visible as such. A destination the geolocation cache could not place
keeps its branch and is labelled with its `lookup_state`. Rooting at a person
uses `client.owner_id`, and beyond the `flow` horizon the person level reads
`owner_volume_aggregate_*`. Providers: `firewall_log`, `dns_lookup`, `geo_asn`.

**Data** — `flow.src_interface_id`, `flow.dst_interface_id`,
`flow.src_client_id`, `flow.dst_address`, `flow.dst_port`, `flow.protocol`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at`, `flow.traffic_scope`;
`client.hostname`, `client.last_address`, `client.owner_id`,
`client.mac_is_randomised`; `owner.display_name`; `interface.user_label`,
`interface.description`; `domain_attribution.site_name`;
`geo_asn.country_code`, `geo_asn.country_name`, `geo_asn.asn`,
`geo_asn.operator`, `geo_asn.lookup_state`. Sources: *Data source 1 — Filter
logs*; names, *Data source 5 — Resolver DNS lookups*.

**Parameters** — `period`; `root` (`interface` | `client` | `owner` — which
level the tree starts at); `depth` (how many levels are expanded on load);
`interfaces` (optional list of interface references); `clients` (optional);
`owners` (optional list of owner references); `measure`
(`bytes` | `connections`); `limit_per_level`; `sort`
(`volume` | `connections` | `name` | `recency`); `include_blocked` (boolean,
default true).

## People

### Per-person activity

**Type** — `owner_activity`

**Shape** — `cards`

**Question** — What did one person's machines do, taken together?

**Binding** — One card per person: the person, their total volume, their allowed
and blocked connection counts, their machines as chips, and an inline timeline.
A card is clickable and opens the underlying records. The residual group is the
**Unassigned** card. Providers: `firewall_log` for the traffic; **none for the
person** — `owner` is the one entity in the schema fed by no endpoint, because
the firewall has no notion of a human being.

**Data** — `owner.display_name`, `owner.id`; `client.owner_id`,
`client.hostname`, `client.last_address`, `client.interface_id`,
`client.last_seen_at`; `flow.src_client_id`, `flow.packet_bytes`,
`flow.action`, `flow.observed_at`, `flow.traffic_scope`. Beyond the `flow`
horizon — the `retention_seconds` row of `setting`, not a constant — the same
shape is read from `owner_volume_aggregate_7d.owner_id`, `.bytes`,
`.allowed_connections`, `.blocked_connections`, `.client_count`,
`.traffic_scope`, `.period_start_at`, `.computed_at` and the `_1h`, `_24h` and
`_30d` twins; a NULL `owner_id` carries the unassigned bucket. `client_count`
carries the rule in Part 4. Source: *Data source 1 — Filter logs*.

**Parameters** — `period`; `owners` (an optional list of **owner** references —
the reference kind `docs/dashboard-format.md` adds for exactly this widget;
empty means every person); `include_unassigned` (boolean, default true);
`measure` (`bytes` | `allowed` | `blocked`); `scope`
(`all` | `east_west` | `north_south`); `sort`
(`volume` | `blocked` | `name` | `machines`); `clients_per_card` (how many chips
before a collapsed remainder chip); `show_timeline` (boolean).

**Required statement** — **Ownership is never inferred.** A machine appears under
a person because somebody said it does, and for no other reason: not a hostname,
not a MAC prefix, not a vendor hint. So this preset has a fifth condition the
shape does not supply — **no person exists yet**: *"Nobody has been created yet.
`opnview` cannot tell who owns a machine — the firewall does not know, and
guessing from a hostname would be wrong — so people are created here and machines
are assigned by hand."*, with a control to create one. And when
`include_unassigned` is false the card header states *"Unassigned machines are
hidden; totals below exclude them"*.

## Sites and names

### Top sites

**Type** — `top_sites`

**Shape** — `table`

**Question** — Which sites is this network actually using?

**Binding** — Inferred site names ranked by attributed flows, with the number of
distinct clients that reached them, the byte volume, and the country and operator
behind the address. Each row expands to the clients behind it. Providers:
`dns_lookup`, `firewall_log`, `geo_asn`.

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`,
`domain_attribution.dns_resolution_id`,
`domain_attribution.correlation_delay_seconds`; `flow.observed_at`,
`flow.src_client_id`, `flow.src_interface_id`, `flow.packet_bytes`,
`flow.dst_address`; `dns_resolution.domain`, `dns_resolution.client_address`,
`dns_resolution.looked_up_at`; `geo_asn.country_code`, `geo_asn.operator`.
Sources: *Data source 5 — Resolver DNS lookups*; *Data source 1 — Filter logs*.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `min_flows` (suppress names seen fewer than N times);
`group_by_registrable_domain` (boolean — whether `a.example` and `b.example`
collapse).

**Gaps** — **G5**: no per-domain volume aggregate, so a 30 d top-sites list
cannot outlive the `retention_seconds` row of `setting`.

### Sites by client

**Type** — `sites_by_client`

**Shape** — `matrix`

**Question** — Which site was used, and by whom?

**Binding** — Clients down the side, their top inferred site names across, each
cell carrying the flow count and the byte volume. A client with no attributed
flows keeps its row with an explicit *"no site name could be inferred"* and its
attribution rate beside it. Providers: `dns_lookup`, `firewall_log`,
`dhcp_lease`.

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`;
`flow.src_client_id`, `flow.src_interface_id`, `flow.observed_at`,
`flow.packet_bytes`; `client.hostname`, `client.last_address`,
`client.identity_kind`, `client.unstable_identity`; `dns_resolution.domain`,
`dns_resolution.client_id`. Sources: *Data source 5 — Resolver DNS lookups*;
*Data source 4 — DHCP leases*.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`sites_per_client` (how many names per row); `min_flows`;
`show_attribution_rate` (boolean, default true).

**Gaps** — **G5** for long periods; **G6** for a per-interface breakdown of
*lookups* rather than of *flows*.

### Attribution rate per client

**Type** — `attribution_rate_per_client`

**Shape** — `table`

**Question** — How much of this client's traffic can be named at all, and how
much should I therefore distrust the site lists?

**Binding** — One row per client: flow count, attributed count, attribution rate
as a percentage, mean and maximum correlation delay. Ordered worst-first, so the
clients whose site lists are least trustworthy are the ones the reader sees. A
client at or near zero is annotated with the likely cause — encrypted DNS
(DNS-over-TLS or DNS-over-HTTPS), a client-side cache, or a resolver other than
the firewall's — citing `docs/opnsense-api-survey.md`, *Gaps and alternatives*,
gap 8. Provider: `dns_lookup`.

**Data** — `flow.src_client_id`, `flow.observed_at`, `flow.id`;
`domain_attribution.flow_id`, `domain_attribution.correlation_delay_seconds`;
`client.hostname`, `client.last_address`, `client.identity_kind`. Source: *Data
source 5 — Resolver DNS lookups*.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `sort` (`worst_first` | `best_first` | `by_volume`).

**Required statement** — **An undefined rate is not a zero rate, and the two are
never rendered the same.** With no resolver source, the rate reads *undefined*:
*"`opnview` will not show 0 % for a source that is not there."* Collapsing that
distinction defeats the whole preset.

## Destinations, geography and operators

### Passed-traffic world map

**Type** — `passed_traffic_world_map`

**Shape** — `map`

**Question** — Where in the world did the traffic that was **allowed** actually
go?

**Binding** — Destinations that allowed traffic reached, sized by volume, the
detail — address or operator, country, bytes, connection count, contributing
interfaces — in the popup. **This is one of a pair**: blocked traffic has its own
preset rather than a second layer here. The decision is the maintainer's and it
follows the principle `docs/data-model.md` applies to east-west and north-south —
two readings, presented separately, never blended — and a layered map would
decide the canvas for the reader, which the named-canvases model exists to avoid.
Providers: `firewall_log`, `geo_asn`.

**Data** — `volume_aggregate_24h.peer_address`, `.bytes`,
`.allowed_connections`, `.src_interface_id`, `.traffic_scope`,
`.period_start_at`, `.computed_at`, and the `_1h`, `_7d` and `_30d` twins;
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`, `geo_asn.asn`,
`geo_asn.operator`, `geo_asn.dataset_build_at`; `interface.user_label`,
`interface.description`. The allowed side needs no gap: the aggregates carry
`allowed_connections` separately from `blocked_connections`. Sources: *Data
source 3 — NetFlow / Insight*; the allowed/blocked split, *Data source 1 —
Filter logs*; coordinates, country and operator from the MaxMind GeoLite2 City
and ASN databases, the second of the two outbound calls the project allows.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `interfaces` (optional
list of interface references; empty means every discovered interface); `scope`
(`north_south` — the default and only meaningful value for a world map, since
east-west traffic has no remote endpoint to place — or `all`, which additionally
counts east-west volume into the unplaced counter so the totals reconcile);
`size_by` (`bytes` | `connections`); `min_bytes`; `show_unplaced` (boolean,
default true and not recommended to disable); `cluster` (boolean — whether
nearby marks merge at low zoom, with the merged count shown inside the mark).

### Blocked-traffic world map

**Type** — `blocked_traffic_world_map`

**Shape** — `map`

**Question** — Where in the world was the traffic that was **stopped** trying to
go, or coming from?

**Binding** — Blocked traffic only, from two engines, and the popup names which:
the **firewall**, whose blocked records carry a real remote address, and the
**security engine**, whose events carry a peer address. Providers:
`firewall_log`, `security_event`, `geo_asn`.

**Data** — Firewall engine: `volume_aggregate_24h.peer_address`,
`.blocked_connections`, `.src_interface_id`, `.period_start_at` and the period
twins for the aggregated view; and, within the `flow` retention horizon,
`flow.action`, `flow.observed_at`, `flow.src_address`, `flow.dst_address`,
`flow.dst_port`, `flow.src_interface_id`, `flow.direction`, `flow.packet_bytes`
through the `blocked_event` view. Security engine:
`security_event.occurred_at`, `security_event.src_address`,
`security_event.dst_address`, `security_event.event_action`,
`security_event.src_interface_id`, `security_event.provider_id`,
`security_event.rule_identity`, `security_event.signature`. Both:
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`,
`geo_asn.operator`, `geo_asn.dataset_build_at`. Sources: *Data source 1 — Filter
logs*; *Data source 2 — Suricata `eve.json`*; *Data source 3 — NetFlow /
Insight*; geolocation from the MaxMind databases.

**Parameters** — `period`; `engines` (any subset of `firewall_rule`,
`security_engine`; the two DNS engines are deliberately not offered here and the
widget explains why); `interfaces` (optional); `endpoint` (`destination` — where
blocked traffic was heading — or `source` — where blocked inbound traffic came
from; a genuine choice, because the interesting end differs between egress
policy and inbound attack); `size_by` (`connections` | `bytes` | `events`);
`min_count`; `show_unplaced` (boolean, default true); `cluster` (boolean).

**Required statement** — **The DNS engines contribute nothing to this map, and
the widget says so**, because a lookup the resolver refused resolved to no
address and there is therefore nothing to place: a fact about the world rather
than a gap in the model. A second fixed counter states *"N DNS lookups were
blocked in this period and cannot appear on a map, because a refused lookup
resolves to no address"*, linking to *Blocked DNS lookups*. Between the two
counters the map's total reconciles with the unified blocked feed's total, which
is the test of whether this preset is telling the truth — **an empty blocked map
must never be read as "nothing was blocked" unless every contributing engine is
healthy.**

**Gaps** — **G7** to place a security event by the interface it targeted rather
than the one it came from; **G8** for the coverage statement.

### Destination countries

**Type** — `destination_countries`

**Shape** — `table`

**Question** — Which countries, ranked, and how much of that was blocked?

**Binding** — Countries with byte volume, allowed and blocked connection counts,
distinct peer count, contributing interfaces, and the build date of the
geolocation dataset that answered, so a stale enrichment is visible. An address
whose lookup state is `miss` or `pending` is carried in an explicit unresolved
row. Providers: `firewall_log`, `geo_asn`. Reads no table holding a domain name,
so it is usable with the `aggregate_mode` row of `setting` set to `no_domains`.

**Data** — `geo_asn.country_code`, `geo_asn.country_name`,
`geo_asn.lookup_state`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_interface_id`, `.period_start_at` and the period twins;
`interface.user_label`, `interface.description`. Sources as for the world map.

**Parameters** — `period`; `interfaces` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `sort` (`bytes` | `blocked` | `peers`).

### Destination operators

**Type** — `destination_operators`

**Shape** — `table`

**Question** — Which operators and autonomous systems is this network's traffic
actually reaching?

**Binding** — ASN and operator name with byte volume, allowed and blocked
connection counts, distinct peer count, contributing interfaces and the dataset
build date. **This is the preset that survives encrypted DNS**: when no site name
can be inferred the operator is usually still knowable, and this is the honest
answer to "where did it go" for a client whose attribution rate is near zero.
Providers: `firewall_log`, `geo_asn`. Also `no_domains`-safe.

**Data** — `geo_asn.asn`, `geo_asn.operator`, `geo_asn.lookup_state`,
`geo_asn.country_code`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_interface_id`, `.period_start_at` and the period twins;
`interface.user_label`, `interface.description`. Sources as for the world map.

**Parameters** — `period`; `interfaces` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `group` (`asn` | `operator`).

### Traffic Sankey

**Type** — `traffic_sankey`

**Shape** — `sankey`

**Question** — Who talks to whom, at a glance — which interfaces send their
volume to which other interfaces, and to which operators outside?

**Binding** — Left: an interface's user label or discovered description. Right:
another interface (east-west), or the destination operator's AS number **and**
operator name, or the country (north-south). Answers the same question as
*Interface traffic matrix* differently — **the matrix is precise and the Sankey
is legible** — so both are offered and the reader places whichever suits the
canvas. `docs/ui-references.md`, *The maintainer's recorded preferences*, *Flow
visualisation*. Providers: `firewall_log`, `geo_asn`.

**Data** — `volume_aggregate_24h.src_interface_id`, `.dst_interface_id`,
`.peer_address`, `.traffic_scope`, `.bytes`, `.allowed_connections`,
`.blocked_connections`, `.period_start_at`, `.computed_at`, and the `_1h`,
`_7d` and `_30d` twins; `interface.user_label`, `interface.description`,
`interface.link_kind`; `geo_asn.address`, `geo_asn.asn`, `geo_asn.operator`,
`geo_asn.country_code`, `geo_asn.lookup_state`. Inside the `flow` retention
horizon the same shape reads `flow.src_interface_id`, `flow.dst_interface_id`,
`flow.traffic_scope`, `flow.packet_bytes`, `flow.action`, `flow.observed_at` and
`flow.dst_address`. Sources: *Data source 3 — NetFlow / Insight*; the allowed and
blocked counts, *Data source 1 — Filter logs*; *Runtime discovery* (i); operator
and country from the MaxMind databases.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `left` (`interface` —
today the only source dimension that resolves for every flow — or `client`, whose
limits the next field sets out); `right` (`interface` | `operator` | `country`); `scope` (`all` |
`east_west` | `north_south`); `interfaces` (optional list of interface references
limiting the left-hand side); `measure` (`bytes` | `connections`); `max_nodes`
(how many ribbons before the remainder collapses); `min_share` (the threshold
below which a ribbon joins the "Other" band); `show_blocked` (boolean — whether
ribbon colour additionally encodes the blocked share of each pair, which is
available because the aggregates carry both counts).

**Gaps** — **G4** for `left` = `client` beyond the `flow` retention horizon; the
widget says so in place rather than silently falling back to interfaces. One
limit that is not a gap: **`opnview` has no source autonomous system** — it
observes a network whose local end is an interface and a client, so the faithful
analogue of an AS-to-AS Sankey is **interface-to-operator**.

## Blocked and denied

### Unified blocked feed

**Type** — `unified_blocked_feed`

**Shape** — `feed`

**Question** — What was blocked, and **what blocked it** — a DNS advertising
list, a DNS threat list, Suricata, or a firewall rule?

**Binding** — One reverse-chronological feed over four engines, each entry naming
the engine, the client, the target (a domain where the engine saw one, an address
and port otherwise), and — the load-bearing field — **which named list,
signature or rule produced the decision**. Providers: `firewall_log`,
`dns_lookup`, `security_event`.

**Data** — Firewall engine: `flow.action`, `flow.observed_at`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`, `flow.protocol`,
`flow.rid`, `flow.rule_id`, `flow.rule_lookup_state`, `flow.src_client_id`,
`flow.src_interface_id`, `flow.interface_device`,
`flow.interface_lookup_state`, and the `blocked_event` view; `rule.description`,
`rule.action`, `rule.pf_label`; `interface_map.description`. Security engine:
`security_event.event_action`, `security_event.occurred_at`,
`security_event.signature`, `security_event.rule_identity`,
`security_event.src_address`, `security_event.dst_address`,
`security_event.src_client_id`, `security_event.src_interface_id`,
`security_event.provider_id`; `provider_rule_info.normalised_severity`,
`provider_rule_info.category`. DNS engines: `dns_resolution.action`,
`dns_resolution.domain`, `dns_resolution.client_address`,
`dns_resolution.client_id`, `dns_resolution.looked_up_at`,
`dns_resolution.resolver`, `dns_resolution.answer_source`,
`dns_resolution.rcode`, `dns_resolution.blocklist_id`; `blocklist.name`,
`blocklist.purpose`, `blocklist.purpose_assigned_at`. `blocklist.name` is the
value `/api/unbound/overview/search_queries` returns verbatim (*Data source 5*,
*Response shape*) — that was gap G1 and it is closed. Sources: *Data source 1*,
*Data source 2*, *Data source 5*.

**Parameters** — `period`; `engines` (any subset of `firewall_rule`,
`dns_advertising_list`, `dns_threat_list`, `security_engine`); `interfaces`
(optional); `clients` (optional); `limit`; `group_by` (`none` | `engine` |
`client` | `target`).

**Gaps** — **G2**: no unified blocking vocabulary across the four engines; the
feed unions three tables with three column names at the presentation layer until
a `blocked_decision` view exists. **G8** for the Suricata coverage statement.

**Two model rules this feed must respect** (both in Part 4): a blocklist's
**purpose** is assigned by the user and never inferred from its name, and a
blocked lookup with a NULL `blocklist_id` is *"blocked, list not recorded"* — a
state of its own.

### Blocked by firewall rule

**Type** — `blocked_by_firewall_rule`

**Shape** — `table`

**Question** — Which firewall rules are actually firing, and against whom?

**Binding** — Rules ranked by blocked-connection count, each row naming the
rule's description, its pf label, the interface it fired on, the interfaces and
clients it blocked, and an inline timeline. A `rid` that matches no known rule is
a first-class row labelled **rule no longer exists** — normal, because a rule can
be removed after it logged. Rules discovered but never fired are listed with a
zero count on request, so "which rules are dead" is answerable. Provider:
`firewall_log`.

**Data** — `flow.action`, `flow.observed_at`, `flow.rid`, `flow.rule_id`,
`flow.rule_lookup_state`, `flow.src_interface_id`, `flow.src_client_id`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`,
`flow.interface_device`, `flow.interface_lookup_state`, and the
`blocked_event` view; `rule.description`, `rule.pf_label`, `rule.action`,
`rule.direction`, `rule.is_automatic`; `interface_map.description`,
`interface_map.interface_id`. Sources: *Data source 1 — Filter logs*; *Runtime
discovery* (ii).

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `include_automatic` (boolean — whether auto-generated rules are
listed); `sort` (`blocked` | `clients` | `recency`).

### Blocked DNS lookups

**Type** — `blocked_dns_lookups`

**Shape** — `table`

**Question** — Which domains did the resolver refuse, and which list did the
refusing?

**Binding** — Blocked domains ranked by count, with the clients that asked, the
interfaces they sit in, and the name of the blocklist that produced the decision,
advertising and threat separated **by somebody's decision, not by a substring of
a list name**. A lookup whose list cannot be named is carried explicitly as
*"blocked, list not recorded"*. Provider: `dns_lookup`.

**Data** — `dns_resolution.domain`, `dns_resolution.action`,
`dns_resolution.client_address`, `dns_resolution.client_id`,
`dns_resolution.looked_up_at`, `dns_resolution.resolver`,
`dns_resolution.answer_source`, `dns_resolution.rcode`,
`dns_resolution.lookup_key`, `dns_resolution.blocklist_id`; `blocklist.name`,
`blocklist.purpose`; `client.hostname`, `client.last_address`. Query:
`-- diagnostic: Blocked lookups by list` in `sql/queries/diagnostics.sql`.
Source: *Data source 5 — Resolver DNS lookups*.

**Parameters** — `period`; `purposes` (any subset of `advertising`, `tracking`,
`threat`, `parental`, `other`, plus `unassigned` for a list nobody has
classified and `not_recorded` for a block the resolver attributed to no list —
the last two being states rather than purposes, and selectable precisely so they
cannot be quietly excluded); `blocklists` (optional — limit to named lists);
`interfaces` (optional); `clients` (optional); `limit`; `min_count`.

**Gaps** — **G6**: `dns_resolution` carries no interface, so grouping by
interface joins through `client.interface_id` and a client with no client row is
unplaceable.

## Security events

### Security alerts over time

**Type** — `security_alerts_over_time`

**Shape** — `time_series`

**Question** — What did the intrusion-detection engine see, and when?

**Binding** — Security events over the period, banded by normalised severity,
with totals brought forward and every entry naming the provider that contributed
it. Severity is taken from the event when the provider ships one and resolved
through the per-provider rule-info cache otherwise; a rule identity absent from
that cache is an explicit **unknown severity** band rather than a dropped event,
because a detection whose severity we could not look up is still a detection.
Provider: `security_event` — **and this kind admits several concurrently active
providers** (`docs/data-model.md`, *Providers*), so the provider is part of every
grouping.

**Data** — `security_event.occurred_at`, `security_event.normalised_severity`,
`security_event.rule_identity`, `security_event.signature`,
`security_event.event_action`, `security_event.src_client_id`,
`security_event.src_interface_id`, `security_event.provider_id`,
`security_event.in_interface_device`; `provider_rule_info.normalised_severity`,
`provider_rule_info.category`, `provider_rule_info.provider_severity`;
`provider.provider_key`, `provider.display_name`. Sources: *Data source 2 —
Suricata `eve.json`*; severity resolution, *Gaps and alternatives*, gap 3.

**Parameters** — `period`; `severities` (any subset of `critical`, `high`,
`medium`, `low`, `informational`, `unknown`); `interfaces` (optional); `clients`
(optional); `bucket` (the time granularity); `providers` (optional, by provider
kind and key).

**Gaps** — **G8**: the interfaces a security-event provider covers are not
modelled, and an alert count of zero means something very different on a covered
interface than on an uncovered one. All conditions carry the coverage statement.

### Alerts by signature

**Type** — `alerts_by_signature`

**Shape** — `table`

**Question** — Which detections are firing, how often, and how serious are they?

**Binding** — Rule identities ranked by count, with the signature text, the
normalised severity, the category, the clients and interfaces involved, and the
contributing provider. **The raw severity the provider reported is shown beside
`opnview`'s normalised value, so the normalisation stays auditable.** A rule
identity with no cache entry shows severity **unknown** with the reason — the API
flattens the nested alert object, so severity is fetched separately and this one
has not been fetched yet. Provider: `security_event`.

**Data** — `security_event.rule_identity`, `security_event.signature`,
`security_event.occurred_at`, `security_event.event_action`,
`security_event.normalised_severity`, `security_event.src_client_id`,
`security_event.src_interface_id`, `security_event.provider_id`;
`provider_rule_info.normalised_severity`, `provider_rule_info.provider_severity`,
`provider_rule_info.category`, `provider_rule_info.rule_source`,
`provider_rule_info.fetched_at`; `provider.provider_key`,
`provider.display_name`; `interface.user_label`, `interface.description`;
`client.hostname`. Grouped by `(provider_id, rule_identity)`; the index
`idx_security_event_rule_occurred_at` exists for it. Sources: *Data source 2*;
rule metadata, *Gaps and alternatives*, gap 3.

**Parameters** — `period`; `severities`; `interfaces` (optional); `clients`
(optional); `limit`; `providers` (optional); `sort` (`count` | `severity` |
`recency`).

**Gaps** — **G8**.

### Alerts by client and interface

**Type** — `alerts_by_client_and_interface`

**Shape** — `table`

**Question** — Which machine, and in which interface, is the detection engine
complaining about?

**Binding** — One row per client with the alert count, the worst severity seen,
the interface, and the top signatures — attributed to a client and an interface
rather than to an address. A source address that could not be resolved is a
first-class row labelled **address not matched to a client**, with its address
shown, because dropping it would under-report. Providers: `security_event`,
`dhcp_lease`.

**Data** — `security_event.src_client_id`, `security_event.src_interface_id`,
`security_event.src_address`, `security_event.dst_address`,
`security_event.occurred_at`, `security_event.rule_identity`,
`security_event.signature`, `security_event.normalised_severity`;
`provider_rule_info.normalised_severity`, `provider_rule_info.category`;
`client.hostname`, `client.last_address`, `client.unstable_identity`;
`interface.user_label`, `interface.description`. Grouped by `src_client_id` and
`src_interface_id`; `idx_security_event_client_occurred_at` exists for it.
Sources: *Data source 2*; client identity, *Data source 4 — DHCP leases*.

**Parameters** — `period`; `severities`; `interfaces` (optional); `limit`;
`sort` (`count` | `worst_severity`); `include_unmatched` (boolean, default
true).

**Gaps** — **G7**: no destination client or interface, so "which interface was
the target" cannot be asked and an alert about traffic *into* an interface is
placed by its source only. **G8** for coverage, which here names the interfaces
where a client **could not** raise an alert because nothing is watching it.

## The installation itself

### Public address

**Type** — `public_address`

**Shape** — `table`

**Question** — What address does this installation present to the internet, on
which gateway, and when did it last change?

**Binding** — One row per gateway the firewall routes through: the gateway, the
interface behind it, the address currently assigned to that interface, its
address family, and when `opnview` first saw that address there. **Which
interface is the upstream one follows from which carries a gateway, never from
what it is called.** Provider: interface discovery.

**Data** — `/api/interfaces/overview/interfaces_info` returns, per interface,
`addr4` and `addr6` in `address/prefix` form, the `ipv4[]` and `ipv6[]` arrays
whose entries each carry an `ipaddr`, and `gateways[]` — all cited in
`docs/opnsense-api-survey.md`, *Runtime discovery* (i).
`/api/routes/gateway/status` may additionally name the gateways and their state,
and its response shape is `UNVERIFIED:` in Part 5.

**Parameters** — `families` (any subset of `ipv4`, `ipv6`); `gateways`
(optional — limit to named gateways; empty means all); `show_history` (boolean —
whether the previous addresses are listed beneath the current one);
`history_limit`.

**Required statement** — **The distinction this preset must not blur.** What the
API reports is **the address configured on the upstream interface**. Directly
connected, that is the public address; behind a modem doing its own NAT, or
behind carrier-grade NAT, it is a private address and the real public address is
**a different number the firewall cannot see**. The preset states which case it
is in — it can tell, because a private range is arithmetic on the address rather
than a guess about a name — and otherwise says *"This is the address on the
upstream interface. This installation is behind another NAT, so the address the
internet sees is not visible from the firewall and `opnview` does not guess
it."* It never labels a private address as public, and **never fetches the answer
from an external echo service**: the project allows exactly two outbound calls.
Until G13 is closed the change column reads *"not recorded — `opnview` has kept
no address history yet"* and **must not show a change time**: a fabricated
*"changed recently"* is exactly the zero-that-means-we-could-not-look this
catalogue forbids. The observation-point limit does **not** apply here and must
not be shown; this preset describes the firewall, not traffic crossing it.

**Gaps** — **G13**.

## Firewall health and telemetry

**The model half is closed.** `measurement_sample` stores a subject, a measure, a
unit, a value and an instant, and carries both the firewall's gauges and the
sampled per-pair volume (`docs/data-model.md`, `measurement_sample`). Every
telemetry endpoint was probed against a live OPNsense 26.7.3_11 and answers
(`docs/opnsense-api-survey.md`, *The telemetry the data model calls gaps G9 and
G10 exists*). **G9 and G10 are closed**; the endpoint-candidate table survives in
Part 5 for the fields still unverified.

**What is still open is narrower and is a reading, not a gap.** The telemetry
responses' **field names** are not established, so each reading tries a candidate
list and is recorded as **absent** when none answers — never as a zero. Any
preset here must expect a reading to be missing on a given installation and say
so, which is the same discipline as any other degraded source.

**Why this data is a different shape from everything else in the model.** Kept
because the temptation to reuse the volume aggregates is strong and would be
wrong. Everything else the model holds is an **event record**; telemetry is a
**regular-interval sample of a value**. Three consequences: **downsampling
replaces counting** (summing CPU percentages is meaningless; a longer period
wants a mean, a minimum, a maximum, often a percentile); **a gap in the series is
information** — a missing sample means `opnview` was not collecting, which the
`time_series` shape renders as a gap rather than a zero or an interpolation; and
**retention wants a ladder, not a horizon**, because telemetry is cheap coarse
and expensive fine.

### Firewall health overview

**Type** — `firewall_health_overview`

**Shape** — `figure`, repeated over the discovered subjects

**Question** — Is the firewall itself healthy right now?

**Binding** — One tile per reading: uptime, CPU percentage, memory used against
total, swap used, disk used **per discovered filesystem**, temperature **per
discovered sensor**, and per-gateway latency and loss. Each tile carries an
inline series of its recent history, its own threshold colouring and its
last-sampled stamp, so a stale panel reads as stale rather than as current. The
repeat set is discovered at runtime: **the number of sensors and filesystems is
not knowable when this preset is written.** Provider: `measurement_sample`.

**Data** — `measurement_sample`, one row per reading, filtered on
`(subject_kind, subject_key, measure)` over a range of `sampled_at` —
`subject_kind` being `firewall`, `interface` or `endpoint_pair`. Sources:
`/api/diagnostics/system/systemResources`,
`/api/diagnostics/system/systemTemperature`,
`/api/diagnostics/system/systemTime`, `/api/diagnostics/system/systemDisk`,
`/api/diagnostics/activity/getActivity`, `/api/diagnostics/traffic/interface`
(`docs/opnsense-api-survey.md`, *Verified against a live firewall, 2026-09-27*).
Gateway latency and loss remain `UNVERIFIED:` — see Part 5.

**Parameters** — `metrics` (any subset of `uptime`, `cpu`, `memory`, `swap`,
`disk`, `temperature`, `gateway_latency`, `gateway_loss`); `period` (the window
the sparklines cover); `thresholds` (per metric, the warning and critical
values, defaulting to unset — **no default threshold is assumed**, because a
sensible temperature or disk figure depends on the hardware and assuming one
would be a hardcoded assumption about the installation); `layout` (`tiles` |
`rows`); `show_last_sampled` (boolean, default true).

### Interface throughput

**Type** — `interface_throughput`

**Shape** — `time_series`

**Question** — How much traffic is each interface carrying, and is anything
saturated or erroring?

**Binding** — Per-interface bandwidth in and out over time, the current rate
brought forward, plus error, drop and collision counters where the source
supplies them. Interfaces are labelled with the user-given description resolved
through the interface map, **never with the raw device name alone**. Provider:
`measurement_sample`.

**Data** — `measurement_sample` for the readings, with `subject_kind` =
`interface` and the **device** name as `subject_key`, which is the token
`interface_map` keys by. The join back to a named interface uses
`interface_map.device`, `interface_map.description`,
`interface_map.interface_id`; `interface.user_label`, `interface.description`,
`interface.identifier`, `interface.device`. Source:
`/api/diagnostics/traffic/interface`, which gives per-interface packet and byte
counters, errors, link state and line rate (*Verified against a live firewall*);
interface discovery, *Runtime discovery* (i).

**Parameters** — `interfaces` (optional list of interface references; empty means
every discovered interface); `period`; `direction` (`both` | `in` | `out`);
`measure` (`bits_per_second` | `bytes` | `packets` | `errors`); `stacked`
(boolean); `per_interface_axis` (boolean — one shared axis, or one small chart
per interface).

**Required statement** — **These are the firewall's own interface counters, not
the filter log**, so they include traffic the filter log never recorded because no
logging rule matched it. This is the one traffic figure in the catalogue that is
not a lower bound for the same reason as the others, and the copy says so rather
than pasting the observation-point sentence: *"Counted at the interface, so this
includes traffic no logging rule matched. Traffic between two clients behind one
interface still never reaches the firewall and is still invisible."* No screen
query applies and none can be adapted: `-- screen: Matrix` and
`-- screen: Overview` aggregate `flow`, which counts only logged packet
decisions, so reusing either would silently answer a different question.

### Custom chart

**Type** — `custom_chart`

**Shape** — `time_series`, **with no binding**

**Question** — Whatever the reader wants to ask by putting two or more series on
the same chart.

**Binding** — **None, and that is the point.** This entry is the `time_series`
shape exposed directly: the reader names the series. The maintainer's own example
— temperature and CPU against WAN bandwidth on one chart — is an *example*, not a
specification: the preset exists so the reader builds whatever combination is
useful rather than being given a fixed CPU widget, a fixed bandwidth widget and
no way to relate them. Providers: whichever the chosen series name.

**Data** — Whatever its series name. A telemetry series reads
`measurement_sample`. A traffic series reads `flow.observed_at`,
`flow.packet_bytes`, `flow.action`, `flow.traffic_scope`,
`flow.src_interface_id`, `flow.direction`, and the `volume_aggregate_*` family's
`period_start_at`, `bytes`, `allowed_connections`, `blocked_connections`. A
detection series reads `security_event.occurred_at`,
`security_event.normalised_severity`, `security_event.src_interface_id`. A DNS
series reads `dns_resolution.looked_up_at`, `dns_resolution.action`. **Mixing a
telemetry series with a traffic series on one chart requires both families to
share a time axis and a bucketing rule**: bucket boundaries have to be the volume
aggregates' period boundaries, or the chart will lie about simultaneity. One
query per series, joined on the time bucket at the presentation layer rather than
in SQL, because the series may come from tables with no join key in common.

**Parameters** — `period`; `series` — an ordered **list**, each entry naming
`source` (which catalogue data a series is drawn from), `metric` or `measure`,
an optional reference scoping it (an interface, a client, a rule, a
provider), `axis` (`left` | `right`), `label`, `render` (`line` | `area` |
`bars` | `points`) and `aggregation` (`mean` | `min` | `max` | `sum` | `last`,
for the bucket a pixel covers); `axes` (per axis, unit, scale and an optional
fixed range); `legend` (`table` | `inline` | `hidden`); `tooltip`
(`single` | `all_series` — the second being Grafana's full stacked breakdown at
the hovered instant, which the preferences section names as worth taking);
`annotations` (optional — overlay security events or blocked spikes as markers,
the idea taken from Datadog's event-overlay band).

**This entry is the dashboard format's hardest test and is deliberately kept as
one.** `parameters.series` is a *list of objects*, and
`docs/dashboard-format.md` carries a worked example showing exactly that: if a
list of series cannot be written in the format, the format is wrong and this is
where that would be discovered.

## Health and honesty

### Source availability

**Type** — `source_availability`

**Shape** — `table`

**Question** — Which of my sources is actually working, and what am I therefore
not seeing?

**Binding** — One row per registered provider: kind, key, display name,
availability state, the probe that determined it, the detail the probe returned,
when it was checked, and whether it is the provider `opnview` actually reads for
that kind. **Availability and activeness are two separate columns**, because a
machine may have two reachable implementations of one kind and the model has to
say which one the data came from — the survey found Unbound and Dnsmasq both
running as resolvers on the probed firewall, so this is not hypothetical. **Every
widget on every canvas links here when it degrades**, so an empty widget is
always one click from the reason it is empty.

**Data** — `provider.kind`, `provider.provider_key`, `provider.display_name`,
`provider.is_active`, `provider.registered_at`; `source_availability.state`,
`source_availability.probe`, `source_availability.detail`,
`source_availability.checked_at`. Query: `-- diagnostic: Source availability`,
unchanged. Sources: the probe endpoints per kind, all cited in
`docs/opnsense-api-survey.md` under each data source's *Degradation*.

**Parameters** — `kinds` (optional subset of the provider kinds);
`show_inactive` (boolean, default true); `compact` (boolean).

**Required statement** — This preset **has no empty state**, and that is
deliberate: `source_availability` holds one row per registry row from the first
start onwards, so the table is never empty. The conditions are rendered as
**values** — `unavailable` with probe `not_yet_probed` reads *"not yet probed"*,
`present_but_disabled` reads *"installed, not collecting"* with the OPNsense page
that turns it on, and a `reachable` provider that returned nothing reads
*"healthy"*. A provider row carrying no availability row at all reads *"state
unknown"*, **never healthy**.

### Aggregate freshness

**Type** — `aggregate_freshness`

**Shape** — `table`

**Question** — Are the pre-computed numbers I am looking at actually current?

**Binding** — One row per pre-computed period — 1 h, 24 h, 7 d, 30 d — with the
slot count, the earliest and latest period covered, the last computation
timestamp and the total bytes held. A period whose slot count is zero, or whose
`computed_at` has fallen behind the refresh contract in `docs/data-model.md`, is
flagged **stale**, and **every widget reading that period shows the same flag
inline** rather than presenting a stale number as current.

**Data** — `volume_aggregate_1h.period_start_at`, `.period_end_at`,
`.computed_at`, `.bytes`, and the `_24h`, `_7d`, `_30d` twins. Query:
`-- diagnostic: Aggregate coverage per period`, unchanged. Source: the
aggregates are `opnview`'s own primary store rather than a cache over the
firewall, for the reason in *Data source 3 — NetFlow / Insight*, *Retention on
the firewall*, reinforced by *The per-pair data is a live snapshot, not history*.

**Parameters** — `periods` (any subset of `1h`, `24h`, `7d`, `30d`);
`show_bytes` (boolean); `compact` (boolean).

### Unresolved joins

**Type** — `unresolved_joins`

**Shape** — `figure`, repeated over the selected counters

**Question** — How much of what I am looking at could not be joined to a name?

**Binding** — Four counters with their trends: flows whose interface device name
matched no entry in the interface map; flows whose `rid` matched no known rule;
clients identified only by address behind an interface rather than by a DHCP
client identity or a MAC; and addresses whose geolocation lookup is a `miss` or
still `pending`. Each counter links to the records behind it. **This preset
exists so the quality of every other widget is visible rather than assumed.**
Providers: `firewall_log`, `geo_asn`, `dhcp_lease`.

**Data** — `flow.interface_lookup_state`, `flow.rule_lookup_state`,
`flow.interface_device`, `flow.rid`, `flow.observed_at`,
`flow.src_interface_id`; `interface_map.device`, `interface_map.description`;
`rule.pf_label`; `client.identity_kind`, `client.identity_key`, `client.mac`,
`client.unstable_identity`; `geo_asn.lookup_state`, `geo_asn.looked_up_at`,
`geo_asn.address`. Sources: the two first-class join keys, *Runtime discovery*
(i) and (ii); geo enrichment from the MaxMind databases.

**Parameters** — `period`; `counters` (any subset of `interface`, `rule`,
`client_identity`, `geo`); `interfaces` (optional); `show_records` (boolean).

**Required statement** — *Reachable and no rows* here means *"Every record in
this period joined cleanly"* and **is the only condition in this preset that may
render as a zero**, because there it genuinely is one. With the firewall log
unreachable, *"A zero here would mean 'nothing to join', not 'everything
joined'."*

---

# Part 3 — Degradation copy, per source

**One table, because the sentences belong to the source and not to the widget.**
The previous revision carried these three times per entry, twenty-nine times
over; they varied by source, so they are stated here once and a preset inherits
whichever rows its **Binding** names as providers. What the shape decides is the
**rendering** (Part 1); what the preset adds is the five *Required statements* in
Part 2.

**The final wording is not fixed here.** What is fixed is the **content**: which
condition, what it does and does not imply, and — where a user can switch the
source on — the OPNsense page, **always with the statement that `opnview` never
performs that change**.

| Provider kind | Not yet probed | Unavailable | Present but disabled | Reachable, no rows |
|---|---|---|---|---|
| `firewall_log` | *"Not yet probed."* | *"The firewall log is not reachable, so no traffic can be shown. This is not an absence of traffic."* | *"Local logging is switched off on the firewall. Turn on System > Settings > Logging to populate this widget."* — and, where the reading is about rules: *"a rule with logging disabled produces no record even when the log itself is healthy."* | *"The firewall log is healthy and returned no records for this period."* |
| `dns_lookup` | *"Not yet probed."* | *"No resolver source is reachable, so no site name can be inferred. This is not an absence of browsing."* | *"The resolver is running, but query reporting is switched off. Turn on Services > Unbound DNS > Reporting — or, for a Dnsmasq resolver, query logging — to populate this list."* | *"The resolver reported no lookups that could be correlated with traffic in this period."* |
| `security_event` | *"Not yet probed."* | *"No security-event source is reachable. The intrusion-detection module is absent or not permitted, so this is not an absence of threats."* | *"Suricata is installed but not running, so nothing is being detected. An empty chart here would be a lie."* | *"Suricata is running and raised no alerts in this period on the interfaces it covers."* |
| `geo_asn` | *"Not yet probed."* | *"No geolocation database is available, so destinations cannot be placed on a map. The traffic still happened — its volume is unaffected and is shown unresolved."* Without a MaxMind licence key the map is disabled with that stated plainly, **and every other widget keeps working**. | does not arise | *"No address in this period had a geolocation answer to place."* |
| `dhcp_lease` | *"Not yet probed."* | *"No DHCP source is reachable, so clients are named by address only."* `UNVERIFIED:` — the survey has since found `get_arp` and `get_ndp`, so a MAC may still be knowable without a lease, and this copy may understate what is knowable. | *"No DHCP backend is serving leases."* | *"No lease was reported in this period."* |
| `flow_volume` | — | — | — | — | **Retired as a source a widget degrades against.** `netflow/aggregate` answers 404, `traffic/top` is a live snapshot sampled into `measurement_sample`, and `pair_volume_observation` is **derived from `flow`** — so nothing implements this kind and no reader can act on a message about it. The six presets that carried it now bind `firewall_log` plus `measurement_sample`, and the copy *"NetFlow local collection is switched off on the firewall"* is gone: it told the reader to switch on something `opnview` does not read. Maintainer's decision, 2026-09-28. |
| `measurement_sample` | *"Not yet probed."* | *"The firewall's status endpoints are not reachable, so its health cannot be shown. This says nothing about whether the firewall is healthy."* | *"`opnview` is not collecting firewall telemetry. Collection is off, or this build predates it."* | *"Telemetry collection is running but has not taken its first sample yet."* Per reading: **absent**, where no candidate response key answered — never a zero. |
| `reconciled_state` | **No registry row exists**, deliberately: a row is a claim that an implementation exists and can be probed, and a row with none would read *"not yet probed"* for ever (`docs/data-model.md`, *Providers*). No preset binds this kind yet; the `table` and `feed` shapes are ready for one. | — | — | — |

**Three source facts a preset must not smooth over.**

1. **The resolver reports a bounded window and nothing wider.** Measured: a
   5-minute window and a 24-hour window returned the same ~410-second span, and
   `total` is `1000` whatever is asked — `timeStart` / `timeEnd` do nothing
   (*Verified against a live firewall*, *The resolver window is not honoured at
   all*). So any resolver-fed reading over a period longer than that ring buffer
   is **under-reported by construction**, and the widget says so. *This replaces
   the previous revision's copy, which described a per-poll collection fault; the
   ceiling is permanent, not occasional.*
2. **A healthy IDS with a narrow ruleset reports nothing, and that is a true
   reading.** On the probed firewall five rulesets were enabled and all five were
   abuse.ch indicator feeds, which fire only on contact with infrastructure
   already known to be malicious; `query_alerts` returned `[]` for every window
   up to seven days. *Reachable and no rows* here is correct behaviour and must
   not be rendered as a fault.
3. **A blocklist can be large while blocking almost nothing.** A product built
   against the mockup's invented data expects a busy blocked-DNS view; a real
   installation may have a handful of rows. Neither the widget nor the collector
   may treat that as a fault.

---

# Part 4 — Bindings that are not obvious

Five facts a preset's **Binding** relies on and could get wrong. They are model
and query facts rather than widget facts; each is flagged for
`docs/data-model.md` or `sql/queries/` to own, and is restated here only because
a reader of a binding needs it.

1. **Inside the `flow` horizon, read `flow`; beyond it, read the aggregates.**
   The horizon is the `retention_seconds` row of `setting`, **not a constant**,
   and the aggregates are the **primary volume store rather than a cache**, because
   per-pair data on the firewall is daily-only and `traffic/top` is a live
   snapshot.
2. **`screen: Map`'s join to `geo_asn` is an inner `JOIN` and silently drops any
   peer address with no cache row.** Both map presets, and the Sankey with an
   operator or country right-hand side, require a `LEFT JOIN` with a null
   right-hand side counted as `pending` and a `lookup_state` of `miss` counted as
   unplaced — which is what makes mapped plus unplaced equal the total. *This is a
   defect in the query, not a preference of the widget.*
3. **`owner_volume_aggregate_*.client_count` is the distinct machines that
   contributed to one slot and must not be summed across slots.** A card covering
   several slots counts its machines from `client`.
4. **`pair_volume_observation.service_port` is `min(src_port, dst_port)` as
   Insight computes it** — a heuristic rather than the real destination port. The
   grouping that uses it is **labelled approximate** and prefers `flow.dst_port`
   wherever the period is inside the `flow` horizon.
5. **A blocklist's name is observed and its purpose is assigned.**
   `blocklist.name` is stored verbatim from the endpoint; `blocklist.purpose` is
   a user's classification and **no code derives it from the name**. A list nobody
   has classified reads *purpose not assigned* and is never sorted into a category
   on the strength of what it is called; a blocked lookup with a NULL
   `blocklist_id` reads *blocked, list not recorded*, which is not the same as the
   lookup having passed. This is the rule `interface.user_label` and
   `client.owner_id` already obey.

---

# Part 5 — Endpoints still to verify

What `docs/opnsense-api-survey.md` establishes is not repeated here; what remains
`UNVERIFIED:` is, because a preset rests on it.

| Reading | Endpoint | Status |
|---|---|---|
| Gateway latency and loss | `/api/routes/gateway/status` | `UNVERIFIED:` **whether it reports round-trip time and packet loss at all.** The generated reference names the path and documents no response body. *Firewall health overview*'s two gateway metrics and *Public address*'s gateway naming both rest on it. |
| Every telemetry reading | the six probed system paths | The paths answer. `UNVERIFIED:` the **field names**, the units and the retention, so each reading tries a candidate list marked in `internal/collect` and records **absent** when none answers. |
| DHCP via ISC | `/api/dhcpv4/leases/searchLease` | **404 on 26.7.3_11** — ISC is gone. The registry row stays `UNVERIFIED:`. |
| Rule metadata | `get_rule_info/<sid>` | **404** at the documented path. Step 6 depends on it and must find the real one; until then *Alerts by signature* renders severity **unknown** with the reason. |

Two paths in the earlier candidate table were named `stream`, which in this API
means Server-Sent Events. *Data source 1 — Filter logs* already found the
filter-log stream unsuitable as a primary path because the server closes it after
about a minute, so a later survey assumes nothing and checks. Everything here is
**read-only** `GET` against the authenticated REST API.

---

# Part 6 — Coverage of the maintainer's named questions

Every question the maintainer named has at least one preset answering it. Where a
preset answers only partially, the gap blocking the rest is named.

| Named question | Presets answering it | Complete today? |
|---|---|---|
| Site used, and by whom | *Sites by client* (primary), *Top sites*, *Client traffic detail*, *Attribution rate per client* | Partial — needs G5 beyond `flow` retention; the attribution rate is exposed unconditionally so the coverage is honest. And the resolver's own ring-buffer ceiling bounds any long period (Part 3) |
| World map of passed **and** blocked traffic | *Passed-traffic world map* and *Blocked-traffic world map* — **two presets, by the maintainer's decision, not one map with two layers**; plus *Destination countries* and *Destination operators* | Yes for both — the aggregates carry `allowed_connections` and `blocked_connections` separately and `security_event.dst_address` covers the detection engine. One honest limit, stated in the blocked map: a **DNS**-blocked lookup resolves to no address and cannot appear on any map, so it is a counter beside it |
| Who talks to whom, legibly rather than precisely | *Traffic Sankey* (primary), *Interface traffic matrix* | Yes for interface-to-interface and interface-to-operator; a **client**-to-operator Sankey beyond the `flow` horizon needs G4 |
| Blocked content unified across DNS advertising lists, DNS threat lists, Suricata and firewall rules | *Unified blocked feed* (primary), *Blocked DNS lookups*, *Blocked by firewall rule*, *Alerts by signature* | Partial — **which list** is answerable (G1 closed), with the purpose assigned rather than guessed; what remains is G2, one vocabulary across the four engines |
| Traffic by VLAN | *Interface volume ranking* (primary), *Interface traffic matrix*, *Traffic over time by scope* | Yes — interface membership is modelled and never inferred from a name |
| Traffic by MAC or IP | *Client volume ranking* (primary), *Client traffic detail*, *Alerts by client and interface* | Partial — needs G4 beyond `flow` retention |
| Outbound / inbound / inter-VLAN as selectable scopes | *Traffic over time by scope* (primary), *Interface traffic matrix*, *Interface volume ranking* | Partial — inter-VLAN is complete from `flow.traffic_scope`; outbound and inbound need G3 for any pre-computed period |
| **Firewall health and telemetry** — uptime, CPU, RAM, disk, temperature, per-interface bandwidth, latency | *Firewall health overview* (primary), *Interface throughput*, *Custom chart* | **Yes, with one reservation.** G9 and G10 are closed: `measurement_sample` holds a sampled gauge and every telemetry endpoint answers. The reservation is the response **field names**, so a reading may be **absent** on a given installation; gateway latency and loss remain `UNVERIFIED:` |
| **The per-person view** — Bob's phone, tablet and laptop as one Bob | *Per-person activity* (primary), and `connection_tree` rooted at a person | **Yes — G11 closed.** The model carries the person, `owner_volume_aggregate_*` carries the long periods, and the preset carries the **Unassigned** card that keeps the unowned majority visible. One honest limit: a person exists only because somebody typed them in |
| **Which blocklist refused a lookup, and what that list is for** | *Blocked DNS lookups* (primary), *Unified blocked feed* | Yes for the **name**, stored verbatim. The **purpose** is answerable only for lists the user has classified, by design |
| **The installation's public address** | *Public address* | Partial — the address is readable; **when it last changed is not**. No endpoint keeps a history and the model has nowhere to store one (G13). The preset shows the address and says plainly it has no change history |
| **Arbitrary combinations of series on one chart** | *Custom chart* (primary) | Yes — and it is not a preset but the `time_series` shape with the series list authored by the reader |

---

# Part 7 — Gaps

## Gaps closed

Recorded rather than deleted, so a reader meeting these identifiers in an older
document can find out what happened. **Their identifiers are retired and are
never reused**, and none carries a missing-from-model marker any more.

| Id | Gap, as it was named | How it was closed |
|---|---|---|
| G1 | `dns_resolution` has no `blocklist` column, so "blocked by which list" is unanswerable | A `blocklist` table holding the list's **name**, stored verbatim from the `blocklist` field of `/api/unbound/overview/search_queries`, plus `dns_resolution.blocklist_id`. The **purpose** is a separate column, **assigned by the user and never inferred**. See `docs/data-model.md`, the `blocklist` entity |
| G11 | No widget and no aggregate answers the per-person question | The `owner_volume_aggregate_1h` / `_24h` / `_7d` / `_30d` family keyed on `(period_start_at, owner_id, traffic_scope)` with a NULL `owner_id` carrying the mandatory **unassigned** bucket; and *Per-person activity* plus `connection_tree` rooted at a person |
| G9 | The model holds no system telemetry at all | **Closed 2026-09-27 by step 4A.** `measurement_sample` stores a subject, a measure, a unit, a value and an instant, and carries the firewall's gauges and the sampled per-pair volume alike. The gap was never in the API |
| G10 | No source of system telemetry has been surveyed | **Closed 2026-09-27.** Every telemetry endpoint answers, probed against a live OPNsense 26.7.3_11 and recorded in *Verified against a live firewall*. What stays open is not a gap in the model: the responses' **field names** are unestablished, so a reading that does not answer is recorded as **absent** rather than as a zero |

## Gaps found

**Nine gaps remain open.** Two were known before the cycle that first mapped the
widgets onto the schema, five turned up in that mapping, and two — G12 and G13 —
arrived with the composition donut and the public address. **Each open gap
carries exactly one missing-from-model marker, in its row of this table**, so a
grep for the marker returns nine and this table has nine open rows. None is fixed
here: this document names gaps and a later cycle implements them.

G12 is of a different order from the rest, and the distinction decides what
closing it would mean. G2 through G8 and G13 are columns, tables and views
missing from a model that otherwise has the right shape. **G12 is a
classification that does not exist in the data at all.** No amount of schema work
produces an application name from a port number, and the only two routes — a
heuristic, or deep packet inspection — are respectively dishonest and unavailable
to a read-only API client. It is recorded so that nobody closes it by guessing.

| Id | Gap | Fix it needs | Cited to |
|---|---|---|---|
| G2 | `MISSING FROM MODEL:` No unified blocking vocabulary across the four engines; a block lives in three tables under three column names and three value sets (`flow.action = 'block'`, `dns_resolution.action IN ('block','drop')`, `security_event.event_action = 'blocked'`), and nothing says which *kind* of engine decided | A `blocked_decision` view over `flow`, `dns_resolution` and `security_event` projecting `(occurred_at, engine_kind, engine_reference, client_id, interface_id, target, target_kind)`, `engine_kind` constrained to `firewall_rule`, `dns_advertising_list`, `dns_threat_list`, `security_engine` — the four categories made explicit rather than inferred by a caller | *Data source 1*, *Data source 2*, *Data source 5*; and `sql/queries/screens.sql`, which has no such query |
| G3 | `MISSING FROM MODEL:` The four volume aggregates carry `traffic_scope` but no `direction`, so outbound and inbound cannot be told apart in any pre-computed period | A `direction` column on all four aggregate tables in the `in` / `out` / `unknown` vocabulary `flow.direction` uses, included in each slot's uniqueness index | *Data source 1 — Filter logs*, *Response shape* (`dir`) |
| G4 | `MISSING FROM MODEL:` No per-client volume aggregate; the aggregates are keyed on `(period_start_at, src_interface_id, dst_interface_id, peer_address)` and carry no client dimension | A `client_volume_aggregate_<period>` family keyed on `(period_start_at, src_client_id)` with `bytes`, `allowed_connections`, `blocked_connections` and `distinct_destinations`, refreshed on the same contract as the interface aggregates | *Data source 3 — NetFlow / Insight*, *Retention on the firewall* |
| G5 | `MISSING FROM MODEL:` No per-domain volume aggregate; `domain_attribution` is per flow and is purged with its parents | A `domain_volume_aggregate_<period>` family keyed on `(period_start_at, site_name, src_client_id)` carrying `flow_count`, `bytes` and `distinct_clients` | *Data source 5 — Resolver DNS lookups*, *Retention on the firewall* |
| G6 | `MISSING FROM MODEL:` `dns_resolution` carries no interface, so a lookup from a client address with no client row cannot be placed | A nullable `interface_id` resolved at ingest against the discovered addressing, plus an `interface_lookup_state` in the `resolved` / `not_found` / `pending` vocabulary `flow` already uses | *Data source 5*, *Response shape* (`client`); *Runtime discovery* (i) |
| G7 | `MISSING FROM MODEL:` `security_event` carries no destination client or interface, so "which interface was the target" is unanswerable and an alert about traffic *into* an interface is placed by its source only | `dst_client_id` and `dst_interface_id` mirroring the source pair, resolved at ingest the same way, with an index on `(dst_interface_id, occurred_at)` | *Data source 2*, *Response shape* (`dest_ip`) |
| G8 | `MISSING FROM MODEL:` The interfaces a security-event provider covers are not modelled; `source_availability.detail` is free text | A `provider_interface_coverage` table keyed on `(provider_id, interface_id)` with `is_covered` and `determined_at`, so a UI statement about coverage is a query rather than a string parse | *Runtime discovery* (v) (`ids.general.interfaces`, keys whose `selected` is truthy) |
| G12 | `MISSING FROM MODEL:` **Nothing classifies a flow as an application.** The schema knows a protocol number and a port and stops there — `flow.protocol`, `flow.dst_port`, `pair_volume_observation.service_port` are the whole of it — so the dimension ntopng and Zenarmor both lead with cannot be offered | **Neither of the two available routes, and that is the finding.** A **port-and-SNI heuristic** is cheap, right for the easy cases and wrong at exactly the edges that matter — a service on a non-standard port, a CDN fronting a dozen products behind one name, anything tunnelled over 443, and every client using encrypted DNS. A heuristic labelled as a fact is the kind of lie this project refuses, and one labelled as a guess is a column nobody can act on. **Deep packet inspection** is what those products do, and OPNsense exposes none of it to a read-only API client: Suricata's application-layer parsers feed detection, and the `tls` and `http` events that would carry a server name cannot be read back. Closing this needs a source that does not exist, not a column, and **neither route is to be implemented** | *Gaps and alternatives*, gaps 2 and 8 |
| G13 | `MISSING FROM MODEL:` **No interface address and no address history.** `interface` carries `identifier`, `device`, `description`, `user_label`, `link_type`, `link_kind`, `vlan_tag`, `address_family` and the two `_seen_at` instants and **no address column**: addresses live on observations. So the current address has nowhere to be stored, and *when did it last change* is worse than unstored — **no endpoint answers it** | An `interface_address` table keyed on `(interface_id, address)` with the prefix length, the address family, whether a gateway sits behind it, a nullable `gateway_name`, and `first_seen_at` / `last_seen_at`, so a change is a new row and the history is the table. The change time can only ever come from `opnview` having looked before. Separately, and not fixable at all: a firewall behind an upstream NAT cannot see the address the internet sees, and finding it would need a third outbound call the project does not allow | The address fields **are** established — `addr4`, `addr6`, `ipv4[]`, `ipv6[]`, `gateways[]` (*Runtime discovery* (i)). What remains to survey is narrow: the response shape of `/api/routes/gateway/status`, marked `UNVERIFIED:` in Part 5 |

**One candidate was raised and withdrawn**, recorded so it is not rediscovered:
`security_event.flow_ref` is an unconstrained `INTEGER` with no foreign key to
`flow`. No preset reads it, so this document has no standing to call it a gap,
and it carries no identifier and no marker.
