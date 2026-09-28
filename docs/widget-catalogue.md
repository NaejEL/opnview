# Widget catalogue — first pass

This document is the **first pass** at `opnview`'s widget catalogue. It is
deliberately extendable: a widget is a named, parameterised unit of display,
and adding one is an addition to this list rather than a change to the shape of
the product. Nothing here is closed, and the catalogue is expected to grow as
the maintainer's questions sharpen.

**It replaces the seven-screen structure of `ROADMAP.md` step 7.** Step 7 names
seven fixed screens — Overview, Matrix, Interface, Client, Blocked, Alerts, Map —
reachable through a menu. The product as decided has no fixed screens: the user
creates named canvases and fills them with the widgets below. There is
therefore **no section in this document organising widgets into a screen list**,
and none is wanted. What survives of step 7 is its *query* work: the seven
representative queries in `sql/queries/screens.sql` remain authoritative, and
every widget states which of them it reuses, adapts or replaces, so that step 5
does not rediscover the mapping.

The five settled product decisions this catalogue is written against are
restated in `docs/ui-references.md`, section *The product as decided*: named
canvases rather than fixed screens; everything describable as code in JSON
and/or YAML; portable dashboards with explained empty states; the industrial
palette by default with five named options; and the theme following the
operating system on first launch. Nothing below reopens or contradicts one.

## How to read an entry

Every widget is a `###` heading whose body carries exactly seven fields, in this
order:

- **Type** — the widget's identifier: the `snake_case` token a dashboard file
  puts in `widget.type`, an HTTP API path segment or query value names it by,
  and an export writes back. **This document is the vocabulary**, and every
  consumer reads it here rather than deriving one from the heading:
  `docs/dashboard-format.md` says so explicitly for `widget.type`. The
  identifiers are stable — renaming one breaks every dashboard file that used
  it, so a rename is a compatibility event and not an edit — and each is unique
  across the catalogue.
- **Question** — the question a user is asking when they place this widget.
- **Shows** — what is drawn, and the UI copy the widget is required to carry.
- **Parameters** — what the user can set, and what an exported dashboard file
  therefore has to record.
- **Data** — the `table.column` pairs in `internal/store/schema.sql` that feed it, or the
  missing-from-model marker and what would have to be added.
- **Existing query** — which of the seven queries in `sql/queries/screens.sql`
  it reuses, adapts or replaces, or why none applies.
- **Empty state** — what is rendered when there is nothing to draw,
  distinguishing the three conditions the model carries.

**Marker convention.** The missing-from-model marker is the greppable string
that opens each gap in a **Data** field below. It appears **exactly once per
gap**, in the first widget entry where that gap arises; every later widget
depending on the same gap refers to it by its identifier (`G2` … `G13`) without
repeating the marker. The marker is therefore written out **only** inside a
**Data** field — never in the prose of this section, never in the *Gaps found*
table — so that a plain `grep -c` for it returns exactly the number of **open**
gaps, which is exactly the row count of that table. It is named rather than
spelled here for that reason, and it is spelled in full eleven times below.

**A closed gap loses its marker and keeps its identifier.** When a gap is
closed, the marker is removed from the **Data** field that carried it, that
field states the columns that now exist instead, and the gap moves to the *Gaps
closed* table. **The identifier is retired and never reused**, so a reference to
`G1` in an older document still means what it meant when it was written. That is
why the open identifiers below are not contiguous, and the discontinuity is the
record rather than an error.

**Illustrative values.** Where an example address is needed below, it comes from
the documentation ranges — `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`
and `2001:db8::/32` — and is labelled as an example. **No interface name, VLAN
name, interface name, CIDR or address of any real network appears anywhere in this
document.** Every such value is discovered at runtime through the OPNsense API
(`docs/opnsense-api-survey.md`, *Runtime discovery*) and is never hardcoded, in
code, in a template, in a default value or in a widget definition.

**Sources.** Every widget below rests on the authenticated OPNsense REST API and
on nothing else. No entry proposes reading a file on the firewall, opening an
SSH session, or any access other than that API, and no entry proposes `opnview`
changing a setting on the firewall: where a source has to be switched on, that
is a manual operation the user performs in the OPNsense web UI, documented in
the README at step 8.

## Two standing facts — documented, never printed on screen

They bind the *model*: they govern what a figure means. They are **not** UI copy,
and any **Shows** clause below asking for them is withdrawn.

1. **The observation-point limit.** `opnview` sees only what crosses the
   router. Traffic between two clients behind one interface never reaches the
   firewall and is invisible to all five sources, so **every byte, packet and
   connection figure is a lower bound**.

2. **Site names are inferred.** On OPNsense 26.7 a site name comes from
   correlating a resolver lookup with a flow that followed it, and from nothing
   else: Suricata exposes no `dns` event type and its `tls` / `http` events
   cannot be read back (`docs/opnsense-api-survey.md`, *Gaps and alternatives*,
   gaps 1 and 2).

---

## Traffic and volume

### Interface traffic matrix

**Type** — `interface_traffic_matrix`

**Question** — Which interface talks to which, how much, and how much of it was
blocked?

**Shows** — A source-interface × destination-interface grid. Each cell carries the
observed byte volume, the allowed connection count, the blocked connection
count and the distinct rule descriptions that matched; a cell is clickable and
opens the underlying records. The right-hand column is north-south traffic,
where the destination sits in no discovered interface. Interface labels are the
user's own label where one has been set and the firewall's discovered
description otherwise — **never a name-based classification**: an interface's
nature is never inferred from what it is called.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `scope`
(`all` | `east_west` | `north_south`); `measure` (`bytes` | `allowed` |
`blocked`); `interfaces` (an optional list of interface references limiting the
rows and columns; empty means every discovered interface); `show_rules`
(boolean).

**Data** — `flow.src_interface_id`, `flow.dst_interface_id`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at`, `flow.rule_id`,
`flow.rule_lookup_state`; `rule.description`; `interface.user_label`,
`interface.description`. For the 7 d and 30 d periods the same shape is
read from `volume_aggregate_7d.bytes`,
`volume_aggregate_7d.allowed_connections`,
`volume_aggregate_7d.blocked_connections`,
`volume_aggregate_7d.src_interface_id`, `volume_aggregate_7d.dst_interface_id`,
`volume_aggregate_7d.period_start_at` and the `_30d` twins. Source: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; interface discovery,
*Runtime discovery* (i); rule discovery, *Runtime discovery* (ii).

**Existing query** — Reuses `-- screen: Matrix` unchanged for the `1h` and `24h`
periods. Adapts it for `7d` and `30d`, where the same projection is read from
the pre-computed aggregate rather than from `flow`, because `flow` is bounded by
the `retention_seconds` row of `setting` while the aggregates are the primary volume store.

**Empty state** — Three distinct conditions, none of them an error screen and
none of them a displayed zero. *Source unavailable* (`source_availability.state`
is `unavailable` for the active `firewall_log` provider): *"The firewall log is
not reachable, so no traffic can be shown. This is not an absence of traffic."*
*Source present but disabled* (`present_but_disabled`): *"Local logging is
switched off on the firewall, so no filter-log records exist to count. Turn on
System > Settings > Logging to populate this widget."* *Reachable and no rows*:
*"The firewall log is healthy and returned no records for this period."* In the
third case the grid is drawn with its axes and empty cells, so the shape of the
network is still legible.

### Traffic over time by scope

**Type** — `traffic_over_time_by_scope`

**Question** — How much traffic left the network, entered it, and stayed between
interfaces, over time?

**Shows** — A soft area chart with one band per selected scope — outbound,
inbound, inter-interface — over the chosen period, with the numbers brought
forward as totals above the chart. Outbound and inbound derive from
`flow.direction`; inter-interface is `traffic_scope = 'east_west'`. Required UI
copy: the observation-point limit sentence.

**Parameters** — `period`; `scopes` (any subset of `outbound`, `inbound`,
`inter_interface`); `measure` (`bytes` | `connections`); `interfaces` (optional
list of interface references); `stacked` (boolean).

**Data** — `flow.observed_at`, `flow.direction`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.src_interface_id`,
`flow.dst_interface_id`. For the long periods: `MISSING FROM MODEL:` **G3 — the
volume aggregates carry no direction.** `volume_aggregate_1h` … `_30d` carry
`traffic_scope` but no `direction` column, so outbound and inbound cannot be
told apart in any pre-computed period; only `east_west` versus `north_south`
can. What would have to be added: a `direction` column on the four aggregate
tables, constrained to `in` / `out` / `unknown` like `flow.direction`, and
included in the uniqueness index of each slot. Source: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*.

**Existing query** — Adapts `-- screen: Overview`. That query groups by
`traffic_scope` alone and returns one row per scope for the whole window; this
widget needs the same aggregation bucketed by time and additionally split by
`flow.direction`, so the projection is the Overview query's with a bucket
expression added to the `GROUP BY`.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
no traffic can be charted. This is not an absence of traffic."* *Source present
but disabled*: *"Local logging is switched off on the firewall. Turn it on under
System > Settings > Logging to populate this chart."* *Reachable and no rows*:
*"No traffic crossed the firewall in this period."* The axes are drawn in every
case; no zero line is synthesised for an unavailable source.

### Interface volume ranking

**Type** — `interface_volume_ranking`

**Question** — Which VLAN or interface is generating the traffic?

**Shows** — A horizontal ranking of interfaces by observed volume for the period,
each bar carrying the byte total, the client count behind it and the blocked
connection count, with the east-west and north-south shares shown separately
within the bar. Labels come from the user's own interface label, falling back to
the firewall's discovered description.

**Parameters** — `period`; `measure` (`bytes` | `connections` | `clients`);
`scope` (`all` | `east_west` | `north_south`); `limit` (how many interfaces to
show); `include_unlabelled` (boolean — whether interfaces the user has not
labelled are listed).

**Data** — `flow.src_interface_id`, `flow.packet_bytes`, `flow.action`,
`flow.traffic_scope`, `flow.observed_at`, `flow.src_client_id`;
`interface.user_label`, `interface.description`, `interface.link_kind`,
`interface.is_tunnel`, `interface.vlan_tag`. Long periods:
`volume_aggregate_7d.src_interface_id`, `.bytes`, `.allowed_connections`,
`.blocked_connections` and the `_30d` twins. Source: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; interface discovery,
*Runtime discovery* (i).

**Existing query** — Adapts `-- screen: Matrix`, collapsing its
destination-interface dimension so one row per source interface remains. It does
**not** reuse `-- screen: Interface`, which is scoped to a single interface by
`:interface_id` and enumerates clients rather than interfaces.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
interfaces cannot be ranked."* *Source present but disabled*: *"Local logging is
switched off, so there are no filter-log records to rank interfaces by."*
*Reachable and no rows*: *"No interface carried traffic across the firewall in this
period."* When interfaces are discovered but silent, they are listed with an empty
bar rather than omitted, so a silent interface is visibly silent.

### Client volume ranking

**Type** — `client_volume_ranking`

**Question** — Which machine, by MAC or by address, is generating the traffic?

**Shows** — A ranked list of clients with hostname where a lease supplied one,
the MAC where one is known, the last observed address, the interface, the byte
total, the blocked connection count and the count of distinct destinations. A
client whose MAC carries the IEEE locally-administered bit is badged **unstable
identity**, with the explanation that a randomised MAC does not identify a
machine across sessions and has deliberately not been merged into a phantom
client.

**Parameters** — `period`; `interfaces` (optional list of interface references);
`identity` (`any` | `dhcp_client_id` | `mac` | `address_in_interface` — which
identity levels to include); `limit`; `include_unstable` (boolean); `measure`
(`bytes` | `connections` | `destinations`).

**Data** — `flow.src_client_id`, `flow.src_interface_id`, `flow.packet_bytes`,
`flow.action`, `flow.observed_at`, `flow.dst_address`; `client.hostname`,
`client.mac`, `client.mac_is_randomised`, `client.unstable_identity`,
`client.identity_kind`, `client.identity_key`, `client.last_address`,
`client.interface_id`, `client.vendor_hint`, `client.owner_id`. The ranking is
**per machine, and that is a different question from the per-person one** — the
per-person question has its own entry, *Per-person activity*, and its own
aggregate family; that was gap G11 and it is closed. This widget stays per
machine deliberately, because "which device is loudest" and "which person is
loudest" are both worth asking and answering one with the other would be a
substitution rather than an answer.

For any period longer than the `flow` retention horizon:
`MISSING FROM MODEL:` **G4 — there is no per-client
volume aggregate.** The four `volume_aggregate_*` tables are keyed on
`(period_start_at, src_interface_id, dst_interface_id, peer_address)` and carry no
client dimension at all, so a 7 d or 30 d per-client total can only be computed
by scanning `flow`, which the `retention_seconds` row of `setting` bounds. What would have to
be added: a parallel `client_volume_aggregate_<period>` family keyed on
`(period_start_at, src_client_id)` with `bytes`, `allowed_connections`,
`blocked_connections` and `distinct_destinations`, refreshed on the same
contract as the interface aggregates. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; client identity
from leases, *Data source 4 — DHCP leases*.

**Existing query** — Adapts `-- screen: Interface`. That query is the right
projection — client, hostname, address, identity kind, unstable-identity flag,
volume, blocked count, distinct destinations — but is pinned to one interface by
`f.src_interface_id = :interface_id`. This widget generalises the predicate to a set
of interfaces, or to all of them.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
clients cannot be ranked by traffic."* If the `dhcp_lease` provider is
additionally unavailable, the widget adds: *"No DHCP source is reachable, so
clients are named by address only."* *Source present but disabled*: *"Local
logging is switched off, so no per-client traffic has been recorded."*
*Reachable and no rows*: *"No client sent traffic across the firewall in this
period."*

### Client traffic detail

**Type** — `client_traffic_detail`

**Question** — Where did this one machine go, and what was allowed?

**Shows** — One client's outbound records over the period: timestamp,
destination address and port, protocol, action, traffic scope, byte count,
country, operator, and the inferred site name where a resolver lookup could be
correlated with the flow. A record with no attribution is shown with a null site
name and its address, country and operator — never omitted, because an
unattributed destination is a fact rather than a gap in the table. The
correlation delay is shown per attributed row, so a wide delay reads as a weak
attribution.

**Parameters** — `client` (a client reference — required); `period`; `action`
(`all` | `allowed` | `blocked`); `scope` (`all` | `east_west` |
`north_south`); `columns` (which of the available columns to show); `limit`.

**Data** — `flow.id`, `flow.observed_at`, `flow.dst_address`, `flow.dst_port`,
`flow.protocol`, `flow.action`, `flow.traffic_scope`, `flow.packet_bytes`,
`flow.src_client_id`, `flow.interface_device`, `flow.interface_lookup_state`;
`domain_attribution.site_name`,
`domain_attribution.correlation_delay_seconds`; `geo_asn.lookup_state`,
`geo_asn.country_code`, `geo_asn.operator`, `geo_asn.dataset_build_at`;
`interface_map.description`. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; resolver lookups,
*Data source 5 — Resolver DNS lookups*; geo and ASN enrichment, the second of the
two outbound calls the project allows.

**Existing query** — Reuses `-- screen: Client` unchanged. The bound parameter
`:client_id` is exactly this widget's `client` parameter after local resolution.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
this client's traffic cannot be listed."* *Source present but disabled*: *"Local
logging is switched off, so no records exist for this client."* *Reachable and
no rows*: *"This client sent no traffic across the firewall in this period.
Traffic it exchanged with machines behind its own interface would not appear
here."* Separately, when the `dns_lookup` provider is unavailable or disabled the
site-name column is rendered as a labelled column-level notice — *"No resolver
source is available, so no site name can be inferred for any record"* — rather
than as an empty column.

### Traffic composition, now

**Type** — `traffic_composition`

**Question** — What is the traffic made of right now, and in what proportion?

**Shows** — A donut with a ranked legend beside it, the period's total in the
hole, and beneath it a rolling strip of the recent past. The strip is drawn **at
the resolution the samples actually have** — every measured point as a point,
newest at the right, oldest falling off the left — and applies no smoothing, for
the reason recorded in *Custom chart*: `docs/ui-references.md` names Grafana's
granularity as the reference precisely because it does not pretend to a
resolution the data lacks.

**The collapsed slice is mandatory and is named.** Everything past the slice
limit is one explicit **Other** slice carrying its own count of what it merged
— *"Other (N further groups)"* — and it is clickable, opening the full ranked
list. A donut that silently dropped its tail would misstate every proportion
drawn beside it, which is the whole point of drawing proportions.

**The grouping dimension is always on screen**, in the card's subtitle, because
"38 % of traffic" means four different things depending on whether the slices
are clients, interfaces, protocols or service ports.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `group_by`
(`client` | `interface` | `protocol` | `service_port`); `measure`
(`bytes` | `connections`); `slices` (how many named slices before the collapsed
one); `interfaces` (optional list of interface references); `clients`
(optional); `scope` (`all` | `east_west` | `north_south`); `show_other`
(boolean, default true and not recommended to disable).

**Data** — `flow.packet_bytes`, `flow.observed_at`, `flow.protocol`,
`flow.dst_port`, `flow.src_client_id`, `flow.src_interface_id`, `flow.action`,
`flow.traffic_scope`; `client.hostname`, `client.last_address`;
`interface.user_label`, `interface.description`. For the 7 d and 30 d periods
the client and interface groupings read `volume_aggregate_7d.bytes`,
`.allowed_connections`, `.blocked_connections`, `.src_interface_id`,
`.peer_address`, `.period_start_at` and the `_30d` twins. The `service_port`
grouping may additionally read `pair_volume_observation.service_port`,
`.protocol`, `.octets` and `.day_start_at`, with the caveat the model already
records: that column is `min(src_port, dst_port)` as Insight computes it, a
heuristic rather than the real destination port, so the widget labels that
grouping as approximate and prefers `flow.dst_port` wherever the period is
inside the `flow` horizon. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; per-pair volume,
*Data source 3 — NetFlow / Insight*.

There is a fifth grouping this widget does **not** offer.
`MISSING FROM MODEL:` **G12 — nothing in the model classifies a flow as an
application.** The schema knows a protocol number and a port, and stops there:
`flow.protocol`, `flow.dst_port` and `pair_volume_observation.service_port` are
the whole of it, and neither `domain_attribution` nor `security_event` carries
an application name. So the dimension ntopng and Zenarmor both lead with —
*"which app is this"* — cannot be offered, and this widget offers four groupings
rather than five. Two ways exist to close it and **neither is implemented
here**, deliberately. The first is a **port-and-SNI heuristic**: guess the
application from the destination port, and from the TLS server name where one is
visible. It is cheap, it is right for the easy cases, and it is wrong at exactly
the edges that matter — a service on a non-standard port, a CDN fronting a dozen
products behind one name, anything tunnelled over 443, and every client using
encrypted DNS, which per *Data source 5* is already a documented blind spot. A
heuristic labelled as a fact is the kind of lie this project refuses, and a
heuristic labelled as a guess is a column no user can act on; so it is written
down here and not built. The second is **deep packet inspection**, which is what
those products actually do — and OPNsense exposes no DPI classification to a
read-only API client. Suricata's own application-layer parsers feed detection
rather than the alert feed, and the `tls` and `http` events that would carry a
server name **cannot be read back at all** (`docs/opnsense-api-survey.md`, *Gaps
and alternatives*, gap 2). Closing G12 properly therefore needs a source that
does not exist yet, not a column.

**Existing query** — Adapts `-- screen: Overview` for the totals and
`-- screen: Interface` for the per-client grouping; both already aggregate
`packet_bytes` over an indexed time range, and the change is the `GROUP BY`
expression and a `LIMIT` with a remainder row. The 7 d and 30 d periods read a
`volume_aggregate_*` table directly, as *Interface traffic matrix* does.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
the composition of traffic cannot be shown. This is not an absence of
traffic."* *Source present but disabled*: *"Local logging is switched off on the
firewall, so there are no records to break down. Turn on System > Settings >
Logging to populate this widget."* *Reachable and no rows*: *"The firewall log
is healthy and recorded no traffic in this period."* In the third case the
donut's ring is drawn empty with the total reading zero, so the widget reads as
measured rather than broken.

### Connection tree

**Type** — `connection_tree`

**Question** — Which client is talking to what, and along which path?

**Shows** — An expandable tree, one level per hop of the question: interface,
then client, then destination — named by its site name where one was inferred
and by its operator and country otherwise — then the service port and protocol.
Every node carries its own rolled-up volume and connection count, and **a
node's children sum exactly to the node**: an expansion that did not reconcile
would teach the user to distrust every figure above it, so a level that is
truncated by `limit_per_level` carries an explicit remainder child rather than
quietly losing its tail. Blocked connections are drawn in the tree with their
own mark rather than filtered out, so a branch that exists only because
something was refused is visible as such.

**Parameters** — `period`; `root` (`interface` | `client` | `owner` — which
level the tree starts at); `depth` (how many levels are expanded on load);
`interfaces` (optional list of interface references); `clients` (optional);
`owners` (optional list of owner references); `measure`
(`bytes` | `connections`); `limit_per_level`; `sort`
(`volume` | `connections` | `name` | `recency`); `include_blocked` (boolean,
default true).

**Data** — `flow.src_interface_id`, `flow.dst_interface_id`,
`flow.src_client_id`, `flow.dst_address`, `flow.dst_port`, `flow.protocol`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at`, `flow.traffic_scope`;
`client.hostname`, `client.last_address`, `client.owner_id`,
`client.mac_is_randomised`; `owner.display_name`; `interface.user_label`,
`interface.description`; `domain_attribution.site_name`;
`geo_asn.country_code`, `geo_asn.country_name`, `geo_asn.asn`,
`geo_asn.operator`, `geo_asn.lookup_state`. A destination the geolocation cache
could not place keeps its branch and is labelled with its `lookup_state`, never
dropped. Rooting the tree at a person uses `client.owner_id`, and beyond the
`flow` horizon the owner level reads `owner_volume_aggregate_*` — see
*People*, below. Sources: filter logs, `docs/opnsense-api-survey.md`, *Data
source 1 — Filter logs*; names, *Data source 5 — Resolver DNS lookups*.

**Existing query** — Composes `-- screen: Interface` and `-- screen: Client`.
The first already returns an interface's clients with their volume and their
distinct destinations, which is the tree's first two levels; the second returns
one client's flows with destination, country, operator and site name, which is
the third and fourth. The widget issues one query per expanded level rather
than one recursive query, so an unexpanded branch costs nothing.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
no connections can be listed. This is not an absence of connections."* *Source
present but disabled*: *"Local logging is switched off on the firewall, so no
connection record exists. Note that a rule with logging disabled produces no
record even when the log itself is healthy."* *Reachable and no rows*: *"No
connection crossed the firewall from this root in this period."* When the
`dns_lookup` provider is unavailable the destination level still renders, by
operator and country, with a level-wide notice that no site name could be
inferred for any of them — never an empty level.

---

## People

The per-person view the `owner` entity was added for. Everything else in this
catalogue ranks and lists **machines**; this section is the one that asks the
question in the form the maintainer asked it — *Bob's phone, tablet and laptop
as one Bob* — and it is the widget half of what used to be gap **G11**.

### Per-person activity

**Type** — `owner_activity`

**Question** — What did one person's machines do, taken together?

**Shows** — One card per person, each naming the person, their total volume,
their allowed and blocked connection counts, the machines behind the figure as a
row of chips, and a timeline over the period. A chip carries its machine's own
state, so a machine that sent nothing reads as *no data in this period* rather
than as a zero the reader might take for an absence of the machine. A card is
clickable and opens the underlying records for that person.

**The unassigned card is mandatory and is not a footnote.** Ownership is
assigned by hand and most machines on a network belong to nobody in particular,
so a per-person view that showed only the claimed machines would under-report
the network while looking complete. The unassigned card is rendered like any
other, named **Unassigned**, and carries the same figures. `include_unassigned`
can hide it, and when it is hidden the widget says so in the card header —
*"Unassigned machines are hidden; totals below exclude them"* — because a
narrowed total that does not announce itself is a wrong total.

**Ownership is never inferred, and the widget says so where it matters.** A
machine appears under a person because somebody said it does, and for no other
reason: not a hostname, not a MAC prefix, not a vendor hint. The empty state of
a fresh installation therefore invites the user to create people rather than
proposing any.

**Parameters** — `period`; `owners` (an optional list of **owner** references —
the reference kind `docs/dashboard-format.md` adds for exactly this widget;
empty means every person); `include_unassigned` (boolean, default true);
`measure` (`bytes` | `allowed` | `blocked`); `scope`
(`all` | `east_west` | `north_south`); `sort`
(`volume` | `blocked` | `name` | `machines`); `clients_per_card` (how many chips
before a collapsed remainder chip); `show_timeline` (boolean).

**Data** — `owner.display_name`, `owner.id`; `client.owner_id`,
`client.hostname`, `client.last_address`, `client.interface_id`,
`client.last_seen_at`; `flow.src_client_id`, `flow.packet_bytes`,
`flow.action`, `flow.observed_at`, `flow.traffic_scope`. For periods beyond the
`flow` horizon — which is the `retention_seconds` row of `setting`, not a
constant — the same shape is read from `owner_volume_aggregate_7d.owner_id`,
`.bytes`, `.allowed_connections`, `.blocked_connections`, `.client_count`,
`.traffic_scope`, `.period_start_at`, `.computed_at` and the `_1h`, `_24h` and
`_30d` twins. **The model half of G11 is closed**: those four tables exist, they
are keyed on the person with a NULL `owner_id` carrying the unassigned bucket,
and `docs/data-model.md` states their refresh contract. One column has a rule
attached: `client_count` is the distinct machines that contributed to **one
slot** and must not be summed across slots, so a card covering several slots
counts its machines from `client` rather than by adding that column up. Source:
none, for the person — `owner` is the one entity in the schema fed by no
endpoint, because the firewall has no notion of a human being; the traffic
behind it comes from filter logs, `docs/opnsense-api-survey.md`, *Data source 1
— Filter logs*.

**Existing query** — Composes `-- diagnostic: Clients per owner`, which already
returns the assigned and unassigned buckets with the guarantee that their client
counts add up to every row of `client`, with the aggregation shape of
`-- screen: Interface`. Beyond the `flow` horizon it reads a
`owner_volume_aggregate_*` table directly and reconciles against
`-- diagnostic: Owner aggregate coverage per period` for freshness.

**Empty state** — Four conditions here rather than three, because this widget
has a failure the others do not. *No person exists yet*: *"Nobody has been
created yet. `opnview` cannot tell who owns a machine — the firewall does not
know, and guessing from a hostname would be wrong — so people are created here
and machines are assigned by hand."* with a control to create one. *Source
unavailable*: *"The firewall log is not reachable, so per-person activity cannot
be shown. This is not an absence of activity."* *Source present but disabled*:
*"Local logging is switched off on the firewall, so there are no records to
attribute to anybody."* *Reachable and no rows*: *"No machine attributed to
anybody sent traffic across the firewall in this period."* In the last case the
cards are still drawn, with their machines listed and their figures at zero
beside an explicit *measured, and nothing happened* label — which is a different
statement from *we could not look*, and the two are never rendered the same.

---
---

## Sites and names

### Top sites

**Type** — `top_sites`

**Question** — Which sites is this network actually using?

**Shows** — A ranked list of inferred site names with the number of flows
attributed to each, the number of distinct clients that reached them, the byte
volume, and the country and operator behind the address. Each row expands to the
clients behind it.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `min_flows` (suppress names seen fewer than N times);
`group_by_registrable_domain` (boolean — whether `a.example` and `b.example`
collapse).

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`,
`domain_attribution.dns_resolution_id`,
`domain_attribution.correlation_delay_seconds`; `flow.observed_at`,
`flow.src_client_id`, `flow.src_interface_id`, `flow.packet_bytes`,
`flow.dst_address`; `dns_resolution.domain`, `dns_resolution.client_address`,
`dns_resolution.looked_up_at`; `geo_asn.country_code`, `geo_asn.operator`. For
any period longer than the `flow` retention horizon: `MISSING FROM MODEL:`
**G5 — there is no per-domain volume aggregate.** `domain_attribution` is
per-flow and is purged with its parents, so a 30 d "top sites" list cannot
outlive the `retention_seconds` row of `setting`. What would have to be added: a
`domain_volume_aggregate_<period>` family keyed on
`(period_start_at, site_name, src_client_id)` carrying `flow_count`, `bytes` and
`distinct_clients`, refreshed on the same contract as the interface aggregates.
Sources: resolver lookups, `docs/opnsense-api-survey.md`, *Data source 5 —
Resolver DNS lookups*; filter logs, *Data source 1 — Filter logs*.

**Existing query** — **Replaces** nothing and reuses nothing: no query in
`sql/queries/screens.sql` aggregates site names. `-- screen: Client` returns
`a.site_name` per record for one client, which is the only place a site name
appears, and it neither groups nor ranks. This is one of the two gaps the spec
records as already known (see *Gaps found*, G2).

**Empty state** — *Source unavailable* (`dns_lookup` provider `unavailable`):
*"No resolver source is reachable, so no site name can be inferred. This is not
an absence of browsing."* *Source present but disabled*
(`present_but_disabled`): *"The resolver is running, but query reporting is
switched off. Turn on Services > Unbound DNS > Reporting — or, for a Dnsmasq
resolver, query logging — to populate this list. `opnview` never changes that
setting for you."* *Reachable and no rows*: *"The resolver reported no lookups
that could be correlated with traffic in this period."* A fourth condition is
reported distinctly and is **not** an absence of data: *"The resolver query
window was not honoured on the last poll, so lookups may be missing. This is a
collection fault, not a quiet network."* (`docs/opnsense-api-survey.md`, *Gaps
and alternatives*, gap 10.)

### Sites by client

**Type** — `sites_by_client`

**Question** — Which site was used, and by whom?

**Shows** — A two-level view answering the maintainer's question in one widget:
clients down the side, their top inferred site names across, each cell carrying
the flow count and the byte volume. A client with no attributed flows is listed
with an explicit *"no site name could be inferred"* row rather than dropped, and
its attribution rate is shown beside it.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`sites_per_client` (how many names per row); `min_flows`;
`show_attribution_rate` (boolean, default true).

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`;
`flow.src_client_id`, `flow.src_interface_id`, `flow.observed_at`,
`flow.packet_bytes`; `client.hostname`, `client.last_address`,
`client.identity_kind`, `client.unstable_identity`; `dns_resolution.domain`,
`dns_resolution.client_id`. Long periods depend on gap G5. A per-interface
breakdown of *lookups* rather than of *flows* depends on gap G6, below.
Sources: resolver lookups, `docs/opnsense-api-survey.md`, *Data source 5 —
Resolver DNS lookups*; client identity from leases, *Data source 4 — DHCP
leases*.

**Existing query** — **Replaces** the site-name half of `-- screen: Client`.
That query answers "this client's records, with a name where one exists"; this
widget answers "which names, by which client", which is a different
aggregation over the same join and has no counterpart in
`sql/queries/screens.sql` (gap G2).

**Empty state** — *Source unavailable*: *"No resolver source is reachable, so no
site can be attributed to any client."* *Source present but disabled*: *"The
resolver is running with reporting switched off, so no lookups are being
recorded. The procedure is in the README; `opnview` never changes the setting."*
*Reachable and no rows*: *"No lookup could be correlated with traffic from these
clients in this period."* Clients are still listed in all three cases, so the
widget degrades to "these machines, no names" rather than to blankness.

### Attribution rate per client

**Type** — `attribution_rate_per_client`

**Question** — How much of this client's traffic can be named at all, and how
much should I therefore distrust the site lists?

**Shows** — One row per client with the flow count, the attributed count, the
attribution rate as a percentage, and the mean and maximum correlation delay.
Rows are ordered worst-first, so the clients whose site lists are least
trustworthy are the ones the user sees. A client at or near zero is annotated
with the likely cause — encrypted DNS (DNS-over-TLS or DNS-over-HTTPS), a
client-side cache, or a resolver other than the firewall's — quoting
`docs/opnsense-api-survey.md`, *Gaps and alternatives*, gap 8.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `sort` (`worst_first` | `best_first` | `by_volume`).

**Data** — `flow.src_client_id`, `flow.observed_at`, `flow.id`;
`domain_attribution.flow_id`,
`domain_attribution.correlation_delay_seconds`; `client.hostname`,
`client.last_address`, `client.identity_kind`. Sources: resolver lookups,
`docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*.

**Existing query** — Reuses no screen query, and uses the diagnostic query
`-- diagnostic: Attribution rate per client` in `sql/queries/diagnostics.sql`
unchanged. That query is executed by `sql/schema-checks.sh` today, so this
widget rests on a projection the schema checks already exercise.

**Empty state** — *Source unavailable*: *"No resolver source is reachable, so
the attribution rate is undefined rather than zero. `opnview` will not show 0 %
for a source that is not there."* *Source present but disabled*: *"Resolver
query reporting is switched off, so nothing can be attributed and the rate is
undefined."* *Reachable and no rows*: *"No traffic was observed for these clients
in this period, so there is nothing to attribute."* The distinction between an
undefined rate and a zero rate is the whole point of this widget and must not be
collapsed.

---

## Destinations, geography and operators

### Passed-traffic world map

**Type** — `passed_traffic_world_map`

**Question** — Where in the world did the traffic that was **allowed** actually
go?

**Shows** — A world map of destinations that traffic reached, as **point marks
on a de-saturated basemap**, sized by volume, with the detail — address or
operator, country, bytes, connection count, contributing interfaces — in a popup
rather than on the canvas. This rendering is not a free choice: it is the
maintainer's recorded preference in `docs/ui-references.md`, *The maintainer's
recorded preferences*, taken from ntopng — **no choropleth, no arcs, the map as
background and the data as foreground**.

**This is one of a pair.** Blocked traffic has its own widget rather than a
second layer here. The decision is the maintainer's, and it follows the
principle `docs/data-model.md` already applies to east-west and north-south
traffic: **two readings, presented separately, never blended into one figure.**
A layered map would also decide the canvas for the user, which is precisely what
the named-canvases model exists to avoid — somebody watching egress places this
one, somebody watching threats places the other, somebody who wants both places
both, at whatever sizes they choose.

**The unplaced-volume statement is mandatory and is the honest core of this
widget.** `geo_asn.lookup_state` models a miss as a state rather than as a
missing row, so a destination the geolocation dataset could not place **must not
silently vanish from the map**. It is accounted for in a counter fixed beside
the map — *"N connections and X bytes could not be placed on the map: M
addresses have no geolocation answer, K have not been looked up yet"* — which is
clickable and opens the list of those addresses. The counter is shown even when
it is zero, so its absence never means "we forgot to check". A map that quietly
drops what it cannot place lies about the total, and this widget is forbidden
from doing that.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `interfaces` (optional
list of interface references; empty means every discovered interface); `scope`
(`north_south` — the default and only meaningful value for a world map, since
east-west traffic has no remote endpoint to place — or `all`, which additionally
counts east-west volume into the unplaced counter so the totals reconcile);
`size_by` (`bytes` | `connections`); `min_bytes`; `show_unplaced` (boolean,
default true and not recommended to disable); `cluster` (boolean — whether
nearby marks merge at low zoom, with the merged count shown inside the mark).

**Data** — `volume_aggregate_24h.peer_address`, `.bytes`,
`.allowed_connections`, `.src_interface_id`, `.traffic_scope`,
`.period_start_at`, `.computed_at`, and the `_1h`, `_7d` and `_30d` twins;
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`, `geo_asn.asn`,
`geo_asn.operator`, `geo_asn.dataset_build_at`; `interface.user_label`,
`interface.description`. The allowed side needs no gap: the aggregates
already carry `allowed_connections` separately from `blocked_connections`, so
one query over one table serves this widget. Sources: per-pair volume,
`docs/opnsense-api-survey.md`, *Data source 3 — NetFlow / Insight*; the
allowed/blocked split, *Data source 1 — Filter logs*; coordinates, country and
operator from the MaxMind GeoLite2 City and ASN databases, the second of the two
outbound calls the project allows.

**Existing query** — Adapts `-- screen: Map`. Two changes, both required. First,
that query reads `volume_aggregate_24h` by name, whereas this widget's `period`
parameter makes the aggregate table a function of the period, so the same text
is issued against the `_1h`, `_7d` or `_30d` twin. Second — and this is the
change the unplaced counter depends on — **its join to `geo_asn` is an inner
`JOIN`, which silently drops any peer address with no cache row at all.** For
this widget it becomes a `LEFT JOIN`, with a null right-hand side counted as
`pending` and a `lookup_state` of `miss` counted as unplaced, so the mapped
volume plus the unplaced volume equals the total.

**Empty state** — *Source unavailable*: if the `geo_asn` provider is
`unavailable`, *"No geolocation database is available, so destinations cannot be
placed on a map. The traffic still happened — its volume is unaffected and is
shown in the countries and operators widgets as unresolved."* If the
`flow_volume` provider is `unavailable`, *"NetFlow/Insight is not reachable, so
per-destination volume cannot be read. This is not an absence of traffic."*
*Source present but disabled*: *"NetFlow is enabled but local collection is
switched off on the firewall, so Insight holds no data. Reporting > NetFlow is
where a user turns that on; `opnview` never changes it."* *Reachable and no
rows*: *"No allowed traffic reached an address outside a discovered interface in
this period."* Without a MaxMind licence key the map is disabled with that
stated plainly, and every other widget keeps working.

### Blocked-traffic world map

**Type** — `blocked_traffic_world_map`

**Question** — Where in the world was the traffic that was **stopped** trying to
go, or coming from?

**Shows** — The same rendering as its passed-traffic twin — point marks on a
de-saturated basemap, sized by volume or by count, detail in the popup — over
blocked traffic only. Two engines contribute and the popup names which: the
**firewall**, whose blocked records carry a real remote address; and the
**security engine**, whose events carry a peer address. **The DNS engines
contribute nothing to this map, and the widget says so**, because a lookup the
resolver refused never resolved to an address and there is therefore nothing to
place — a fact about the world rather than a gap in the model.

**The unplaced statement is mandatory here too**, and for the same reason: a
blocked destination the geolocation dataset could not place is accounted for in
a fixed counter beside the map — *"N blocked connections could not be placed: M
addresses have no geolocation answer, K have not been looked up yet"* — shown
even at zero. In addition, and specific to this widget, a second fixed counter
states *"N DNS lookups were blocked in this period and cannot appear on a map,
because a refused lookup resolves to no address"*, linking to the
*Blocked DNS lookups* widget. Between them the two counters make the map's total
reconcile with the unified blocked feed's total, which is the test of whether
this widget is telling the truth.

**Parameters** — `period`; `engines` (any subset of `firewall_rule`,
`security_engine`; the two DNS engines are deliberately not offered here and the
widget explains why); `interfaces` (optional); `endpoint` (`destination` — where
blocked traffic was heading — or `source` — where blocked inbound traffic came
from; a genuine choice, because the interesting end differs between egress
policy and inbound attack); `size_by` (`connections` | `bytes` | `events`);
`min_count`; `show_unplaced` (boolean, default true); `cluster` (boolean).

**Data** — For the firewall engine: `volume_aggregate_24h.peer_address`,
`.blocked_connections`, `.src_interface_id`, `.period_start_at` and the period
twins, for the aggregated view; and, for the record-level view within the
`flow` retention horizon, `flow.action`, `flow.observed_at`, `flow.src_address`,
`flow.dst_address`, `flow.dst_port`, `flow.src_interface_id`, `flow.direction`,
`flow.packet_bytes` through the `blocked_event` view. For the security engine:
`security_event.occurred_at`, `security_event.src_address`,
`security_event.dst_address`, `security_event.event_action`,
`security_event.src_interface_id`, `security_event.provider_id`,
`security_event.rule_identity`, `security_event.signature`. For both:
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`,
`geo_asn.operator`, `geo_asn.dataset_build_at`. Two known gaps bear on this
widget and neither is new: placing a security event by the interface it targeted
rather than the one it came from depends on gap G7, and the coverage statement
depends on gap G8. No further gap is needed — the aggregates already separate
`blocked_connections` from `allowed_connections`, and `security_event` already
carries `dst_address`. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; security events,
*Data source 2 — Suricata `eve.json`*; per-pair volume, *Data source 3 — NetFlow
/ Insight*; geolocation from the MaxMind databases.

**Existing query** — **Two queries, not one, and that is the honest answer to
"can one query serve both maps".** The firewall branch adapts `-- screen: Map`
exactly as the passed-traffic twin does, reading `blocked_connections` instead
of `allowed_connections` and using the same `LEFT JOIN` to `geo_asn`. The
security-engine branch adapts `-- screen: Alerts`, which already returns
`se.src_address` and `se.dst_address` per event, adding a join to `geo_asn` on
the chosen endpoint and grouping by location. The two results are unioned at the
presentation layer with the engine carried as a column, so a mark can say which
engine put it there.

**Empty state** — The conditions differ from the passed-traffic map, and
collapsing them would be exactly the dishonesty this widget exists to avoid. *An
empty blocked map must never be read as "nothing was blocked" unless every
contributing engine is healthy.* *Source unavailable*: per engine — *"The
firewall log is not reachable, so rule-based blocks cannot be mapped. This is
not an absence of blocking."* and *"No security-event source is reachable, so
detection-based blocks cannot be mapped."* If the `geo_asn` provider is
unavailable, *"No geolocation database is available, so blocked destinations
cannot be placed. The blocks still happened and are listed in the unified
blocked feed."* *Source present but disabled*: *"Local logging is switched off on
the firewall, so no blocked record exists to map"*, and *"Suricata is installed
but not running, so nothing is being detected."* Each names the OPNsense page
where a user turns it on and states that `opnview` never performs that change.
*Reachable and no rows*: *"Every contributing engine is healthy and blocked
nothing that could be placed on a map in this period"* — with the DNS counter
still shown beside it, because DNS blocks may well be non-zero while this map is
empty, and a user must not conclude from a blank map that nothing was blocked.
Where the security engine contributes, all three states additionally carry the
per-interface coverage statement.

### Destination countries

**Type** — `destination_countries`

**Question** — Which countries, ranked, and how much of that was blocked?

**Shows** — A ranked table of countries with byte volume, allowed and blocked
connection counts, the number of distinct peers, the contributing interfaces, and
the build date of the geolocation dataset that answered, so a stale enrichment is
visible. An address whose lookup state is `miss` or `pending` is carried in an
explicit unresolved row.

**Parameters** — `period`; `interfaces` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `sort` (`bytes` | `blocked` | `peers`).

**Data** — `geo_asn.country_code`, `geo_asn.country_name`,
`geo_asn.lookup_state`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_interface_id`, `.period_start_at` and the period twins;
`interface.user_label`, `interface.description`. Sources as for the world
map.

**Existing query** — Reuses `-- screen: Map`, collapsing the operator and ASN
columns out of the `GROUP BY`. The aggregate-mode guarantee of that query holds
here unchanged: it reads neither `dns_resolution` nor `domain_attribution`, so
this widget is usable with the `aggregate_mode` row of `setting` set to `no_domains`.

**Empty state** — *Source unavailable*: *"No geolocation database is available,
so destinations cannot be grouped by country. The volumes still exist and are
shown unresolved."* *Source present but disabled*: *"NetFlow local collection is
switched off on the firewall, so there is no per-destination volume to group."*
*Reachable and no rows*: *"No traffic left a discovered interface in this period."*

### Destination operators

**Type** — `destination_operators`

**Question** — Which operators and autonomous systems is this network's traffic
actually reaching?

**Shows** — A ranked table of ASN and operator name with byte volume, allowed
and blocked connection counts, distinct peer count and the contributing
interfaces, plus the dataset build date. This is the widget that survives
encrypted DNS: when no site name can be inferred, the operator is usually still
knowable, and this is the honest answer to "where did it go" for a client whose
attribution rate is near zero.

**Parameters** — `period`; `interfaces` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `group` (`asn` | `operator`).

**Data** — `geo_asn.asn`, `geo_asn.operator`, `geo_asn.lookup_state`,
`geo_asn.country_code`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_interface_id`, `.period_start_at` and the period twins;
`interface.user_label`, `interface.description`. Sources as for the world
map.

**Existing query** — Reuses `-- screen: Map` with the country columns collapsed
out of the `GROUP BY`. Like the country widget it reads no table holding a
domain name, so it is available in `no_domains` aggregate mode.

**Empty state** — *Source unavailable*: *"No ASN database is available, so
operators cannot be named. Addresses and volumes are unaffected."* *Source
present but disabled*: *"NetFlow local collection is switched off, so there is no
per-destination volume to attribute to an operator."* *Reachable and no rows*:
*"No traffic reached an address outside a discovered interface in this period."*

---

### Traffic Sankey

**Type** — `traffic_sankey`

**Question** — Who talks to whom, at a glance — which interfaces send their volume
to which other interfaces, and to which operators outside?

**Shows** — A Sankey diagram: sources down the left, destinations down the
right, and a ribbon between each pair whose width is proportional to volume.
Every end is **labelled with a resolved name rather than a number** — a
interface's user label or discovered description on the left, and on the right
either another interface (east-west) or the destination operator's AS number
*and* operator name (north-south). Ribbons below the `min_share` threshold
collapse into a single explicit **"Other"** band carrying its own total, so a
long tail cannot make the diagram unreadable while also not being hidden.

This is a wanted widget rather than an admired picture: the maintainer named
Akvorado's ASN Sankey specifically, and `docs/ui-references.md`, *The
maintainer's recorded preferences*, records both the preference and what is
worth taking from it — **the resolved operator name on the diagram itself, and
an honest collapsed band for the tail**. It answers the same question as the
*Interface traffic matrix* and answers it differently: **the matrix is precise and
the Sankey is legible**, so both are offered and the user places whichever suits
the canvas.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `left` (`interface` —
today the only source dimension that resolves for every flow — or `client`, whose
limits the next field sets out); `right` (`interface` | `operator` | `country`); `scope` (`all` |
`east_west` | `north_south`); `interfaces` (optional list of interface references
limiting the left-hand side); `measure` (`bytes` | `connections`); `max_nodes`
(how many ribbons before the remainder collapses); `min_share` (the threshold
below which a ribbon joins the "Other" band); `show_blocked` (boolean — whether
ribbon colour additionally encodes the blocked share of each pair, which is
available because the aggregates carry both counts).

**Data** — `volume_aggregate_24h.src_interface_id`, `.dst_interface_id`,
`.peer_address`, `.traffic_scope`, `.bytes`, `.allowed_connections`,
`.blocked_connections`, `.period_start_at`, `.computed_at`, and the `_1h`,
`_7d` and `_30d` twins; `interface.user_label`, `interface.description`,
`interface.link_kind`; `geo_asn.address`, `geo_asn.asn`, `geo_asn.operator`,
`geo_asn.country_code`, `geo_asn.lookup_state`. For a period inside the `flow`
retention horizon the same shape can be read directly from
`flow.src_interface_id`, `flow.dst_interface_id`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at` and `flow.dst_address`.

Two honest limits, neither of them a new gap. First, **`opnview` has no source
autonomous system**: unlike Akvorado, which sees both ends of a transit flow,
`opnview` observes a network whose local end is an interface and a client, so the
faithful analogue of an AS-to-AS Sankey is **interface-to-operator**, not
AS-to-AS. Second, setting `left` to `client` for any period longer than the
`flow` retention horizon depends on gap **G4** — the aggregates carry no client
dimension — so a 7 d or 30 d client-to-operator Sankey is unavailable until that
is added, and the widget says so in place rather than silently falling back to
interfaces. Sources: per-pair volume, `docs/opnsense-api-survey.md`, *Data source
3 — NetFlow / Insight*; the allowed and blocked counts, *Data source 1 — Filter
logs*; interface discovery, *Runtime discovery* (i); operator and country from the
MaxMind GeoLite2 ASN and City databases.

**Existing query** — Adapts `-- screen: Matrix` when `right` is `interface`: that
query already projects exactly the source-interface, destination-interface, volume
and allowed-versus-blocked tuple a Sankey needs, and the only change is that the
result is rendered as ribbons rather than as cells. Adapts `-- screen: Map` when
`right` is `operator` or `country`, which already groups peer volume by ASN,
operator and country per source interface — with the same `LEFT JOIN` correction
the two map widgets require, so that peers with no geolocation answer reach the
"Other" band instead of disappearing.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
no traffic relationships can be drawn. This is not an absence of traffic."* If
the `geo_asn` provider is unavailable while `right` is `operator` or `country`,
*"No ASN database is available, so destinations cannot be named. Switch the
right-hand side to interfaces, or add a MaxMind licence key."* *Source present but
disabled*: *"Local logging is switched off on the firewall, so there are no
records to relate. Turn it on under System > Settings > Logging; `opnview` never
changes that setting."* *Reachable and no rows*: *"No traffic crossed between a
discovered interface and anywhere else in this period."* In the third case the
discovered interfaces are still drawn as unconnected nodes down the left, so a
silent network reads as silent rather than as absent.

## Blocked and denied

### Unified blocked feed

**Type** — `unified_blocked_feed`

**Question** — What was blocked, and **what blocked it** — a DNS advertising
list, a DNS threat list, Suricata, or a firewall rule?

**Shows** — One reverse-chronological feed unifying the blocking decisions of
all four engines, each entry naming the engine, the client or client, the target
(a domain where the engine saw one, an address and port otherwise), and — the
load-bearing column — **which named list, signature or rule produced the
decision**. Entries are grouped and filterable by engine, so "show me only what
the DNS threat lists stopped" is one click. Where an engine cannot name what it
used, that is stated in the row rather than left blank.

**Parameters** — `period`; `engines` (any subset of `firewall_rule`,
`dns_advertising_list`, `dns_threat_list`, `security_engine`); `interfaces`
(optional); `clients` (optional); `limit`; `group_by` (`none` | `engine` |
`client` | `target`).

**Data** — For the firewall engine: `flow.action`, `flow.observed_at`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`, `flow.protocol`,
`flow.rid`, `flow.rule_id`, `flow.rule_lookup_state`, `flow.src_client_id`,
`flow.src_interface_id`, `flow.interface_device`,
`flow.interface_lookup_state`, and the `blocked_event` view over them;
`rule.description`, `rule.action`, `rule.pf_label`; `interface_map.description`.
For the security engine: `security_event.event_action`,
`security_event.occurred_at`, `security_event.signature`,
`security_event.rule_identity`, `security_event.src_address`,
`security_event.dst_address`, `security_event.src_client_id`,
`security_event.src_interface_id`, `security_event.provider_id`;
`provider_rule_info.normalised_severity`, `provider_rule_info.category`. For the
two DNS engines: `dns_resolution.action`, `dns_resolution.domain`,
`dns_resolution.client_address`, `dns_resolution.client_id`,
`dns_resolution.looked_up_at`, `dns_resolution.resolver`,
`dns_resolution.answer_source`, `dns_resolution.rcode`,
`dns_resolution.blocklist_id`; `blocklist.name`, `blocklist.purpose`,
`blocklist.purpose_assigned_at`. **The list that refused a lookup is now a
column** — that was gap G1 and it is closed; `blocklist.name` is the value
`/api/unbound/overview/search_queries` returns verbatim
(`docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*,
*Response shape*). Two consequences the feed must respect. First, the **purpose**
— advertising against threat, which is what the maintainer's question turns on —
is **assigned by the user and never inferred from the name**, so a list nobody
has classified is rendered *"purpose not assigned"* and is never sorted into a
category on the strength of what it is called. Second, a blocked lookup with a
NULL `blocklist_id` is *"blocked, list not recorded"*, a state of its own: the
resolver refused it and named nothing, which is not the same as the lookup
having passed. What remains open is the same widget's other half:
`MISSING FROM MODEL:` **G2 — there is no unified blocking vocabulary
across the four engines.** A block lives in three tables with three different
column names and three different value sets (`flow.action = 'block'`,
`dns_resolution.action IN ('block','drop')`,
`security_event.event_action = 'blocked'`), and nothing in the schema says which
*kind* of engine produced a given decision. What would have to be added: a
`blocked_decision` view unioning the three, projecting a common
`(occurred_at, engine_kind, engine_reference, client_id, interface_id, target,
target_kind)` shape, with `engine_kind` constrained to `firewall_rule`,
`dns_advertising_list`, `dns_threat_list` and `security_engine` — the
maintainer's four categories, made explicit rather than inferred by a caller.
Sources: filter logs, `docs/opnsense-api-survey.md`, *Data source 1 — Filter
logs*; security events, *Data source 2 — Suricata `eve.json`*; resolver lookups,
*Data source 5 — Resolver DNS lookups*.

**Existing query** — **Replaces** `-- screen: Blocked`. That query is the
firewall-rule engine alone: it reads the `blocked_event` view over `flow` and
joins `rule` and `interface_map`, and it has no knowledge of DNS or of security
events. This widget keeps that projection as one of its four branches and adds
the other three, which is a replacement rather than an adaptation.

**Empty state** — Per engine, because the four fail independently and collapsing
them would be dishonest. *Source unavailable*: for each engine whose provider is
`unavailable`, a labelled row — *"The firewall log is not reachable, so
rule-based blocks cannot be shown"*, *"No resolver source is reachable, so
DNS-based blocks cannot be shown"*, *"No security-event source is reachable, so
detection-based blocks cannot be shown"*. *Source present but disabled*:
likewise per engine — *"Local logging is switched off on the firewall"*, *"The
resolver is running with query reporting switched off"*, *"Suricata is installed
but not running"* — each naming the OPNsense page where the user turns it on,
and each stating that `opnview` never performs that change. *Reachable and no
rows*: *"Every configured blocking engine is healthy and blocked nothing in this
period."* Where Suricata contributes, the widget additionally states which
interfaces its coverage includes and which it does not; see G8.

### Blocked by firewall rule

**Type** — `blocked_by_firewall_rule`

**Question** — Which firewall rules are actually firing, and against whom?

**Shows** — A ranked list of rules by blocked-connection count, each row naming
the rule's description, its pf label, the interface it fired on, the interfaces
and clients it blocked, and a small timeline. A `rid` that matches no known rule
is a first-class row labelled **rule no longer exists** — normal, because a rule
can be removed after it logged — and never a missing row. An interface client
name absent from the interface map is labelled likewise.

**Parameters** — `period`; `interfaces` (optional); `clients` (optional);
`limit`; `include_automatic` (boolean — whether auto-generated rules are
listed); `sort` (`blocked` | `clients` | `recency`).

**Data** — `flow.action`, `flow.observed_at`, `flow.rid`, `flow.rule_id`,
`flow.rule_lookup_state`, `flow.src_interface_id`, `flow.src_client_id`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`,
`flow.interface_device`, `flow.interface_lookup_state`, and the
`blocked_event` view; `rule.description`, `rule.pf_label`, `rule.action`,
`rule.direction`, `rule.is_automatic`; `interface_map.description`,
`interface_map.interface_id`. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; rule discovery,
*Runtime discovery* (ii).

**Existing query** — Adapts `-- screen: Blocked`, which already returns exactly
these columns per record with both lookup states carried; this widget groups
that record stream by `rule_id` and by the unresolved-`rid` case.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
rule activity cannot be shown. This is not an absence of blocking."* *Source
present but disabled*: *"Local logging is switched off, so no blocked record
exists to attribute to a rule. Note that a rule with logging disabled will never
appear here even when the log itself is healthy."* *Reachable and no rows*: *"No
rule blocked a connection in this period."* Rules discovered but never fired are
listed with a zero count on request, so "which rules are dead" is answerable.

### Blocked DNS lookups

**Type** — `blocked_dns_lookups`

**Question** — Which domains did the resolver refuse, and which list did the
refusing?

**Shows** — A ranked list of blocked domains with the count, the clients that
asked, the interfaces they sit in, and the name of the blocklist that produced the
decision, with the advertising and threat categories separated. A lookup whose
list cannot be named is carried explicitly as *"blocked, list not recorded"*,
never merged into a generic bucket.

**Parameters** — `period`; `purposes` (any subset of `advertising`, `tracking`,
`threat`, `parental`, `other`, plus `unassigned` for a list nobody has
classified and `not_recorded` for a block the resolver attributed to no list —
the last two being states rather than purposes, and selectable precisely so they
cannot be quietly excluded); `blocklists` (optional — limit to named lists);
`interfaces` (optional); `clients` (optional); `limit`; `min_count`.

**Data** — `dns_resolution.domain`, `dns_resolution.action`,
`dns_resolution.client_address`, `dns_resolution.client_id`,
`dns_resolution.looked_up_at`, `dns_resolution.resolver`,
`dns_resolution.answer_source`, `dns_resolution.rcode`,
`dns_resolution.lookup_key`, `dns_resolution.blocklist_id`; `blocklist.name`,
`blocklist.purpose`; `client.hostname`, `client.last_address`. The list that
refused the lookup is `blocklist.name`, observed verbatim from the endpoint, and
what that list is **for** is `blocklist.purpose`, which a user assigned and no
code derived — that was gap G1 and it is closed. The advertising and threat
categories this widget separates are therefore separated by somebody's decision,
not by a substring of a list name, and a list with no assigned purpose keeps its
rows under an explicit *purpose not assigned* group. The query that does this is
`-- diagnostic: Blocked lookups by list` in `sql/queries/diagnostics.sql`.
Grouping by interface depends on: `MISSING FROM MODEL:` **G6 — `dns_resolution` carries no interface.** It has
`client_address` and a nullable `client_id`, so a lookup can only be placed in a
interface by joining through `client.interface_id`; a client that has never appeared
in a lease or a flow has no client row, and its lookups are therefore
unplaceable. What would have to be added: a nullable `interface_id` column on
`dns_resolution`, resolved at ingest from the client address against the
discovered interface addressing, plus an `interface_lookup_state` in the same
`resolved` / `not_found` / `pending` vocabulary `flow` already uses — so an
unplaceable lookup is a modelled state rather than a null. Sources: resolver
lookups, `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*.

**Existing query** — Reuses nothing. No query in `sql/queries/screens.sql` reads
`dns_resolution` at all: the only route to that table in the seven is through
`domain_attribution` in `-- screen: Client`, which selects attributed flows
rather than lookups and cannot see a blocked lookup, because a blocked lookup
produces no flow to attribute.

**Empty state** — *Source unavailable*: *"No resolver source is reachable, so
blocked lookups cannot be shown. This is not an absence of blocking."* *Source
present but disabled*: *"The resolver is running with query reporting switched
off, so blocked lookups are not being recorded. Turn on Services > Unbound DNS >
Reporting; `opnview` never changes that setting."* *Reachable and no rows*: *"The
resolver reported no blocked lookups in this period."* If the resolver is
Dnsmasq, the widget additionally states that the rows come from parsing a
free-text log rather than a structured endpoint, and that the grammar is not a
documented contract (`docs/opnsense-api-survey.md`, *Gaps and alternatives*,
gap 6).

---

## Security events

### Security alerts over time

**Type** — `security_alerts_over_time`

**Question** — What did the intrusion-detection engine see, and when?

**Shows** — A timeline of security events over the period, banded by normalised
severity, with the totals brought forward. Every entry names the provider that
contributed it. Severity is taken from the event when the provider ships one and
resolved through the per-provider rule-info cache otherwise; a rule identity
absent from that cache is drawn as an explicit **unknown severity** band rather
than dropped, because a detection whose severity we could not look up is still a
detection.

**Parameters** — `period`; `severities` (any subset of `critical`, `high`,
`medium`, `low`, `informational`, `unknown`); `interfaces` (optional); `clients`
(optional); `bucket` (the time granularity); `providers` (optional, by provider
kind and key).

**Data** — `security_event.occurred_at`, `security_event.normalised_severity`,
`security_event.rule_identity`, `security_event.signature`,
`security_event.event_action`, `security_event.src_client_id`,
`security_event.src_interface_id`, `security_event.provider_id`,
`security_event.in_interface_device`; `provider_rule_info.normalised_severity`,
`provider_rule_info.category`, `provider_rule_info.provider_severity`;
`provider.provider_key`, `provider.display_name`. The coverage statement
depends on: `MISSING FROM MODEL:` **G8 — the interfaces a security-event provider
covers are not modelled.** `/api/ids/settings/get` exposes
`ids.general.interfaces` as an object keyed by interface identifier with a
`selected` flag (`docs/opnsense-api-survey.md`, *Runtime discovery* (v)), and
nothing in the schema stores it: `source_availability` carries a free-text
`detail` and no per-interface relation. What would have to be added: a
`provider_interface_coverage` table keyed on `(provider_id, interface_id)` with an
`is_covered` flag and a `determined_at` timestamp, so a UI statement about which
interfaces are covered is a query rather than a string parse. Sources: security
events, `docs/opnsense-api-survey.md`, *Data source 2 — Suricata `eve.json`*;
severity resolution, *Gaps and alternatives*, gap 3.

**Existing query** — Adapts `-- screen: Alerts`. That query returns the record
stream with severity resolved and the `severity_state` discriminator already
computed; this widget buckets it by time and by severity. The requirement that
every join stay a `LEFT JOIN`, so that `security_event` drives the plan, carries
over unchanged.

**Empty state** — *Source unavailable*: *"No security-event source is reachable.
The intrusion-detection module is absent or not permitted, so this is not an
absence of threats."* *Source present but disabled*: *"Suricata is installed but
not running, so nothing is being detected. An empty chart here would be a lie."*
*Reachable and no rows*: *"Suricata is running and raised no alerts in this
period on the interfaces it covers."* All three states additionally carry the
coverage statement, because an alert count of zero means something very different
on a covered interface than on an uncovered one.

### Alerts by signature

**Type** — `alerts_by_signature`

**Question** — Which detections are firing, how often, and how serious are they?

**Shows** — A ranked list of rule identities with the signature text, the
normalised severity, the category, the count, the clients and interfaces
involved, and the provider that contributed them. The raw severity the provider
reported is shown alongside `opnview`'s normalised value, so the normalisation
stays auditable. A rule identity with no cache entry is shown with severity
**unknown** and the reason — the API destroys the nested alert object before
`opnview` can read it, so severity has to be fetched separately and this one has
not been fetched yet.

**Parameters** — `period`; `severities`; `interfaces` (optional); `clients`
(optional); `limit`; `providers` (optional); `sort` (`count` | `severity` |
`recency`).

**Data** — `security_event.rule_identity`, `security_event.signature`,
`security_event.occurred_at`, `security_event.event_action`,
`security_event.normalised_severity`, `security_event.src_client_id`,
`security_event.src_interface_id`, `security_event.provider_id`;
`provider_rule_info.normalised_severity`, `provider_rule_info.provider_severity`,
`provider_rule_info.category`, `provider_rule_info.rule_source`,
`provider_rule_info.fetched_at`; `provider.provider_key`,
`provider.display_name`; `interface.user_label`,
`interface.description`; `client.hostname`. Coverage statement depends
on gap G8. Sources: security events, `docs/opnsense-api-survey.md`, *Data source
2 — Suricata `eve.json`*; rule metadata, *Gaps and alternatives*, gap 3.

**Existing query** — Reuses `-- screen: Alerts`, grouped by
`(provider_id, rule_identity)`. The index
`idx_security_event_rule_occurred_at` on exactly that triple exists for this
aggregation.

**Empty state** — *Source unavailable*: *"No security-event source is reachable,
so no signature can be ranked."* *Source present but disabled*: *"Suricata is
installed but not running."* *Reachable and no rows*: *"No signature fired in
this period on the covered interfaces."* Plus the coverage statement in all three
cases.

### Alerts by client and interface

**Type** — `alerts_by_client_and_interface`

**Question** — Which machine, and in which interface, is the detection engine
complaining about?

**Shows** — Alerts attributed to a client and an interface rather than to an
address: one row per client with the alert count, the worst severity seen, the
interface, and the top signatures. A source address that could not be resolved to
a client is a first-class row labelled **address not matched to a client**, with
its address shown, because dropping it would under-report.

**Parameters** — `period`; `severities`; `interfaces` (optional); `limit`;
`sort` (`count` | `worst_severity`); `include_unmatched` (boolean, default
true).

**Data** — `security_event.src_client_id`, `security_event.src_interface_id`,
`security_event.src_address`, `security_event.dst_address`,
`security_event.occurred_at`, `security_event.rule_identity`,
`security_event.signature`, `security_event.normalised_severity`;
`provider_rule_info.normalised_severity`, `provider_rule_info.category`;
`client.hostname`, `client.last_address`, `client.unstable_identity`;
`interface.user_label`, `interface.description`. The destination side is
unavailable: `MISSING FROM MODEL:` **G7 — `security_event` carries no
destination interface or client.** It has `src_client_id` and `src_interface_id` and
no counterpart for `dst_address`, so "which interface was the target" cannot be
asked, and an alert about traffic *into* an interface is placed by its source only.
What would have to be added: `dst_client_id` and `dst_interface_id` columns
mirroring the source pair, resolved at ingest the same way, together with an
index on `(dst_interface_id, occurred_at)` so the inverse aggregation is an index
search. Coverage statement depends on gap G8. Sources: security events,
`docs/opnsense-api-survey.md`, *Data source 2 — Suricata `eve.json`*; client
identity, *Data source 4 — DHCP leases*.

**Existing query** — Reuses `-- screen: Alerts`, grouped by
`src_client_id` and `src_interface_id`. The index
`idx_security_event_client_occurred_at` exists for this aggregation.

**Empty state** — *Source unavailable*: *"No security-event source is reachable,
so no client can be implicated."* *Source present but disabled*: *"Suricata is
installed but not running, so no client has been flagged."* *Reachable and no
rows*: *"No covered client raised an alert in this period."* Plus the coverage
statement, naming the interfaces where a client could not raise an alert because
nothing is watching it.

---

## The installation itself

One entry, about the firewall's own identity on the network rather than about
the traffic crossing it. It sits outside *Firewall health and telemetry* on
purpose: unlike everything in that section, its endpoint **is** surveyed.

### Public address

**Type** — `public_address`

**Question** — What address does this installation present to the internet, on
which gateway, and when did it last change?

**Shows** — One row per gateway the firewall routes through, each naming the
gateway, the interface behind it, the address currently assigned to that
interface, its address family, and when `opnview` first saw that address on that
interface. A recent change is marked, and the previous address is shown beside
it, because *"it changed four hours ago"* is usually the answer a firewall
operator is actually looking for.

**The distinction this widget must not blur.** What the API reports is **the
address configured on the upstream interface**. On a firewall connected
directly to the internet that is the public address. On a firewall behind a
modem or router doing its own NAT, or behind carrier-grade NAT, it is a private
address and the real public address is **a different number the firewall cannot
see**. The widget states which case it is in — it can tell, because a private
range is arithmetic on the address rather than a guess about a name — and where
it cannot know, it says *"This is the address on the upstream interface. This
installation is behind another NAT, so the address the internet sees is not
visible from the firewall and `opnview` does not guess it."* It never labels a
private address as public, and it never fetches the answer from an external
echo service: the project allows exactly two outbound calls, the firewall API
and the MaxMind download, and a third one is not added for a convenience. The
observation-point limit sentence does **not** apply here and must not be shown;
this widget describes the firewall, not traffic crossing it.

**Parameters** — `families` (any subset of `ipv4`, `ipv6`); `gateways`
(optional — limit to named gateways; empty means all); `show_history` (boolean —
whether the previous addresses are listed beneath the current one);
`history_limit`.

**Data** — The endpoint is established, and this is the one entry in the
catalogue whose source was surveyed and whose storage was not.
`/api/interfaces/overview/interfaces_info` returns, per interface, `addr4` and
`addr6` in `address/prefix` form, the `ipv4[]` and `ipv6[]` arrays whose entries
each carry an `ipaddr` in the same form, and `gateways[]` — all cited in
`docs/opnsense-api-survey.md`, *Runtime discovery* (i). Which interface is the
upstream one follows from which carries a gateway, never from what it is called.
`/api/routes/gateway/status` may additionally name the gateways and their state,
and is listed in *Endpoint candidates, cited rather than asserted* below with
`UNVERIFIED:` against its response shape.

Then the model.
`MISSING FROM MODEL:` **G13 — the schema stores no interface address, and no
address history.** `interface` carries `identifier`, `device`, `description`,
`user_label`, `link_type`, `link_kind`, `vlan_tag`, `address_family` and the two
`_seen_at` instants, and **no address column at all**: addresses live on
observations (`flow.src_address`, `client.last_address`, `geo_asn.address`) and
never on the interface itself. So the current address has nowhere to be stored,
and the question *when did it last change* is worse than unstored — **no
endpoint answers it.** The API reports the address the interface has now and
keeps no history of the ones it had, so a change can only be known by `opnview`
having looked before and recorded what it saw. What would have to be added: an
`interface_address` table keyed on `(interface_id, address)` carrying the
prefix length, the address family, whether a gateway sits behind it, and
`first_seen_at` / `last_seen_at`, so a change is a new row rather than an
overwrite and the history is the table; plus a nullable `gateway_name`, since a
gateway is what makes an address the upstream one. Until then this widget can
show the current address and **must not show a change time**, because a
fabricated *"changed recently"* is exactly the zero-that-means-we-could-not-look
this catalogue forbids everywhere else. What would have to be **surveyed** is
narrower than G10: the response shape and field names of
`/api/routes/gateway/status`, and nothing else — the address fields themselves
are already established above.

**Existing query** — Reuses nothing. No query in `sql/queries/screens.sql` reads
an interface address, because no table holds one.

**Empty state** — *Source unavailable*: *"The firewall's interface list is not
reachable, so this installation's addresses cannot be read. This is not an
absence of connectivity."* *Source present but disabled*: does not arise —
interface discovery is not a feature a user switches off, and the widget does
not invent a third state to look symmetrical. *Reachable and no rows*: *"No
interface on this firewall carries a gateway, so no upstream address could be
identified."* Until G13 is closed the change column renders as *"not recorded —
`opnview` has kept no address history yet"*, which is a stated limitation rather
than a blank.

---

## Firewall health and telemetry

**This whole section is outside the step-1 survey.**
`docs/opnsense-api-survey.md` established five data sources — filter logs, the
Suricata `eve.json` event log, NetFlow/Insight, DHCP leases and resolver
lookups — and **none of them is system telemetry.** Every widget below is
therefore missing twice over: the schema has no table that can hold it, *and*
the survey has not established which endpoints supply it, at what shape, with
what retention or at what sustainable polling frequency. Both gaps are recorded
per widget, and the endpoint candidates below are **candidates**, cited or
marked, never asserted.

#### Why this data is a different shape from everything else in the model

The catalogue must say this plainly, because the temptation to reuse the
existing aggregates is strong and would be wrong.

Everything the model holds today is an **event record**: a filter-log line, a
detection, a lease, a lookup. Something happened, at an instant, and the row is
that instant. Retention is a horizon over `observed_at`, purge is a delete by
timestamp, and aggregation means counting or summing events into period buckets.

**Telemetry is not that.** It is a **regular-interval sample of a value**: a
number every N seconds, forever, whether or not anything happened. Three
consequences follow, and each one breaks an assumption the current schema makes.

1. **Downsampling replaces counting.** Summing CPU percentages is meaningless;
   what a longer period needs is a mean, a minimum and a maximum per bucket, and
   often a percentile. The four `volume_aggregate_*` tables sum bytes and count
   connections — the wrong operation entirely.
2. **A gap in the series is information.** A missing event row means nothing
   happened; a missing sample means **`opnview` was not collecting**, which the
   chart must render as a gap rather than as a zero or as a straight line
   between the two surrounding points. The existing tables have no way to say
   "no sample here".
3. **Retention wants a ladder, not a horizon.** Telemetry is cheap to keep at
   coarse resolution and expensive at fine, so the natural policy is a
   roll-up ladder — full resolution briefly, then hourly, then daily — rather
   than the single the `retention_seconds` row of `setting` cut-off the rest of the model
   uses.

**So the eventual step-2 amendment must not be a copy of what already exists.**
What is wanted is a distinct family: a `metric` registry naming each series and
its unit, and a `metric_sample` table keyed on `(metric_id, sampled_at)` with a
value and an explicit collection-gap marker, plus roll-up tables carrying
`min`, `max`, `mean` and `sample_count` per bucket rather than a sum.

#### Endpoint candidates, cited rather than asserted

A later cycle must survey these the way step 1 surveyed the five sources —
response shapes, retention on the firewall, sustainable polling frequency and
degradation behaviour are all unknown here. What *is* established is that the
paths exist:

| Telemetry | Candidate endpoint | Citation |
|---|---|---|
| Memory | `/api/diagnostics/system/memory` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Disk | `/api/diagnostics/system/system_disk` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Temperature | `/api/diagnostics/system/system_temperature` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Uptime and system time | `/api/diagnostics/system/system_time` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| General resources | `/api/diagnostics/system/system_resources` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Swap | `/api/diagnostics/system/system_swap` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| CPU | `/api/diagnostics/cpu_usage/stream` (GET), with `/api/diagnostics/cpu_usage/get_c_p_u_type` for the processor description | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Per-interface counters | `/api/diagnostics/interface/get_interface_statistics` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Per-interface live traffic | `/api/diagnostics/traffic/_interface` (GET) and `/api/diagnostics/traffic/stream` (GET) | https://docs.opnsense.org/development/api/core/diagnostics.html |
| Gateway latency and loss | `/api/routes/gateway/status` (GET) | https://docs.opnsense.org/development/api/core/routes.html |

Two honest caveats. **`UNVERIFIED:` the response shape, the units and the
retention of every endpoint in that table.** The generated API reference lists
paths and verbs and documents neither parameters nor response bodies — the same
limitation `docs/opnsense-api-survey.md` records in its own *Scope and method* —
so what each returns must be read from the controller and backend sources before
any of it is relied upon. And **`UNVERIFIED:` whether `/api/routes/gateway/status`
reports round-trip time and packet loss at all**; the documentation names the
endpoint and says nothing about its fields, so latency is a plausible rather
than an established source. Two endpoints above are named `stream`, which in
this API means Server-Sent Events; `docs/opnsense-api-survey.md`, *Data source 1
— Filter logs*, already found the filter-log stream unsuitable as a primary path
because the server closes it after about a minute, so a later survey should
assume nothing and check.

Everything here is **read-only**, like the rest of the product: these are `GET`
requests against the authenticated REST API, and no widget below proposes
changing a firewall setting, reading a file on the firewall, or any access other
than that API.

### Firewall health overview

**Type** — `firewall_health_overview`

**Question** — Is the firewall itself healthy right now?

**Shows** — A compact panel of current values with their trend: uptime, CPU
percentage, memory used against total, swap used, disk used per filesystem,
temperature per sensor, and per-gateway latency and loss. Each value carries a
sparkline of its recent history and its own threshold colouring, and each states
when it was last sampled — so a stale panel reads as stale rather than as
current.

**Parameters** — `metrics` (any subset of `uptime`, `cpu`, `memory`, `swap`,
`disk`, `temperature`, `gateway_latency`, `gateway_loss`); `period` (the window
the sparklines cover); `thresholds` (per metric, the warning and critical
values, defaulting to unset — **no default threshold is assumed**, because a
sensible temperature or disk figure depends on the hardware and assuming one
would be a hardcoded assumption about the installation); `layout` (`tiles` |
`rows`); `show_last_sampled` (boolean, default true).

**Data** — `measurement_sample`, one row per reading: a subject, a measure, a
unit, a value and an instant. **G9 and G10 are closed**, on 2026-09-27 and by
step 4A. The telemetry was probed against a live OPNsense 26.7.3_11 and every
endpoint answers — CPU, memory, temperature, disk, uptime and per-interface
packet and byte counters — so the gap was never in the API, only in a schema
that had nowhere to put a sampled gauge. The table serves the per-pair volume
sampler as well, because both are the same shape.

What remains open is narrower and is recorded in
`docs/opnsense-api-survey.md`: the telemetry responses' **field names** are not
established, so each reading tries a candidate list and is recorded as *absent*
when none answers — never as a zero. A widget must therefore expect a reading
to be missing on a given installation and say so, which is the same discipline
as any other degraded source.

**Existing query** — None of the seven queries in `sql/queries/screens.sql`
applies, and none can be adapted: all seven read event or aggregate tables that
this widget does not use, and the projection it needs — the latest sample per
metric, plus a bounded window of history — has no counterpart among them.

**Empty state** — *Source unavailable*: *"The firewall's status endpoints are
not reachable, so its health cannot be shown. This says nothing about whether
the firewall is healthy."* *Source present but disabled*: this condition exists
for telemetry in a form the other sources do not have — *"`opnview` is not
collecting firewall telemetry. Collection is off, or this build predates it."*
*Reachable and no rows*: *"Telemetry collection is running but has not taken its
first sample yet."* In all three the panel keeps its tiles with their labels and
an explicit dash rather than a zero, because a zero CPU percentage and an
unknown CPU percentage are different facts.

### Interface throughput

**Type** — `interface_throughput`

**Question** — How much traffic is each interface carrying, and is anything
saturated or erroring?

**Shows** — Per-interface bandwidth in and out over time, with the current rate
brought forward, plus error, drop and collision counters where the source
supplies them. Interfaces are labelled with the user-given description resolved
through the interface map, never with the raw device name alone. Required UI
copy: the sampling statement above, and a distinction that matters — **these are
the firewall's own interface counters, not the filter log**, so they include
traffic the filter log never recorded because no logging rule matched it. That
makes this the one traffic figure in the catalogue that is *not* a lower bound
for the same reason as the others, and the copy says so rather than pasting the
observation-point sentence: *"Counted at the interface, so this includes traffic
no logging rule matched. Traffic between two clients behind one interface still
never reaches the firewall and is still invisible."*

**Parameters** — `interfaces` (optional list of interface references; empty means
every discovered interface); `period`; `direction` (`both` | `in` | `out`);
`measure` (`bits_per_second` | `bytes` | `packets` | `errors`); `stacked`
(boolean); `per_interface_axis` (boolean — one shared axis, or one small chart
per interface).

**Data** — Depends on gaps **G9** and **G10**, above. The join back to a named
interface uses columns that do exist: `interface_map.device`,
`interface_map.description`, `interface_map.interface_id`;
`interface.user_label`, `interface.description`,
`interface.identifier`, `interface.device`. Candidate endpoints:
`/api/diagnostics/interface/get_interface_statistics` and
`/api/diagnostics/traffic/_interface`, both cited in the candidate table.
Interface discovery is already surveyed:
`docs/opnsense-api-survey.md`, *Runtime discovery* (i).

**Existing query** — None applies. `-- screen: Matrix` and `-- screen: Overview`
both aggregate `flow`, which counts only logged packet decisions; this widget
counts everything the interface saw, so reusing either would silently answer a
different question.

**Empty state** — *Source unavailable*: *"Interface counters are not reachable,
so throughput cannot be shown."* *Source present but disabled*: *"`opnview` is
not collecting interface telemetry."* *Reachable and no rows*: *"No sample has
been taken for these interfaces yet."* An interface that is discovered but
carrying nothing is drawn as a flat line at zero **only when a sample actually
says zero**; when there is no sample the line is a gap.

### Custom chart

**Type** — `custom_chart`

**Question** — Whatever the user wants to ask by putting two or more series on
the same chart.

**Shows** — A chart of **several user-chosen series on shared or paired axes**.
The maintainer's own example — temperature and CPU plotted against WAN bandwidth
on one chart — is an *example*, not a specification: this widget exists
precisely so the user builds whatever combination is useful to them, rather than
being given a fixed CPU widget and a fixed bandwidth widget and no way to relate
them. Series of different units get a secondary axis; series of the same unit
share one. Each series carries its own label, colour and rendering, and the
legend states the unit and the axis per series so a reader is never left
guessing which line belongs to which scale.

The charting behaviour is not a free choice either: `docs/ui-references.md`,
*The maintainer's recorded preferences*, records Grafana as the reference **for
its granularity — it does not over-smooth, and it shows the resolution the data
actually has**. So this widget draws through real points, applies no curve
smoothing, and where a series is aggregated to fit the pixels available it
**says which aggregation it applied** rather than doing it silently. Required UI
copy: the sampling statement for telemetry series, and the observation-point
limit sentence **only when at least one traffic series is present** — the copy
is a function of which series the user chose, not a fixed footer.

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

**This widget is the format's hardest test, and it is deliberately included as
one.** `parameters.series` is a *list of objects*, not a scalar or a list of
scalars, so a dashboard file has to be able to express it. `docs/dashboard-format.md`
carries a worked example showing exactly that — if a list of series cannot be
written in the format, the format is wrong and this entry is where that would
be discovered.

**Data** — Whatever its series name. A telemetry series depends on gaps **G9**
and **G10**. A traffic series reads columns that already exist —
`flow.observed_at`, `flow.packet_bytes`, `flow.action`, `flow.traffic_scope`,
`flow.src_interface_id`, `flow.direction`, and the `volume_aggregate_*` family's
`period_start_at`, `bytes`, `allowed_connections` and `blocked_connections`. A
detection series reads `security_event.occurred_at`,
`security_event.normalised_severity` and `security_event.src_interface_id`. A DNS
series reads `dns_resolution.looked_up_at` and `dns_resolution.action`. **Mixing
a telemetry series with a traffic series on one chart requires both families to
share a time axis and a bucketing rule**, which the roll-up design in G9 must
provide: bucket boundaries have to be the same as the volume aggregates' period
boundaries, or the two will not line up and the chart will lie about
simultaneity.

**Existing query** — Composes rather than reuses. A traffic series adapts
`-- screen: Overview` (bucketed by time) or reads a `volume_aggregate_*` table
directly; a detection series adapts `-- screen: Alerts`; a telemetry series has
no counterpart and depends on G9. The widget issues one query per series and
joins them on the time bucket at the presentation layer rather than in SQL,
because the series may come from tables with no join key in common.

**Empty state** — Per series, never collapsed, because a chart with four series
of which one is missing must not look like a chart with three series. *Source
unavailable*: the series is drawn as a labelled gap and the legend entry reads
*"unavailable — the source feeding this series is not reachable"*. *Source
present but disabled*: the legend entry reads *"not collected — this source is
installed but reporting is switched off"*, naming the OPNsense page a user turns
it on and stating that `opnview` never performs that change. *Reachable and no
rows*: the series is drawn as an explicit gap with *"no samples in this
period"*, and never as a line at zero. When **every** series is in one of the
first two states the whole widget renders its own empty state with the axes
still drawn, so the chart's shape is legible even with nothing in it.

## Health and honesty

### Source availability

**Type** — `source_availability`

**Question** — Which of my sources is actually working, and what am I therefore
not seeing?

**Shows** — One row per registered provider: its kind, its key, its display
name, its availability state, the probe that determined it, the detail the probe
returned, when it was checked, and whether it is the provider `opnview` actually
reads for that kind. Availability and activeness are shown as two separate
columns, because a machine may have two reachable implementations of one kind
and the model has to say which one the data came from. Every widget on every
canvas links here when it degrades, so an empty widget is always one click from
the reason it is empty.

**Parameters** — `kinds` (optional subset of the six provider kinds);
`show_inactive` (boolean, default true); `compact` (boolean).

**Data** — `provider.kind`, `provider.provider_key`, `provider.display_name`,
`provider.is_active`, `provider.registered_at`; `source_availability.state`,
`source_availability.probe`, `source_availability.detail`,
`source_availability.checked_at`. Sources: the probe endpoints per kind, all
cited in `docs/opnsense-api-survey.md` — *Data source 1 — Filter logs*
(*Degradation*), *Data source 2 — Suricata `eve.json`* (*Degradation*), *Data
source 3 — NetFlow / Insight* (*Degradation*), *Data source 4 — DHCP leases*
(*Degradation*) and *Data source 5 — Resolver DNS lookups* (*Degradation*).

**Existing query** — Reuses no screen query, and uses the diagnostic query
`-- diagnostic: Source availability` in `sql/queries/diagnostics.sql` unchanged.

**Empty state** — This widget has no empty state in the usual sense, and that is
deliberate: `source_availability` holds exactly one row per registry row from
the migration onwards, so the table is never empty. The three conditions are
therefore rendered as *values* rather than as states of this widget — a provider
in `unavailable` with probe `not_yet_probed` reads *"not yet probed"* rather than
*"unavailable"*, a provider in `present_but_disabled` reads *"installed, not
collecting"* with the OPNsense page that turns it on, and a `reachable` provider
that has returned nothing reads *"healthy"*. A provider row that somehow carried
no availability row at all would be rendered as *"state unknown"*, never as
healthy.

### Aggregate freshness

**Type** — `aggregate_freshness`

**Question** — Are the pre-computed numbers I am looking at actually current?

**Shows** — One row per pre-computed period — 1 h, 24 h, 7 d, 30 d — with the
slot count, the earliest and latest period covered, the last computation
timestamp and the total bytes held. A period whose slot count is zero, or whose
`computed_at` has fallen behind the refresh contract in `docs/data-model.md`, is
flagged **stale**, and every widget reading that period shows the same flag
inline rather than presenting a stale number as current.

**Parameters** — `periods` (any subset of `1h`, `24h`, `7d`, `30d`);
`show_bytes` (boolean); `compact` (boolean).

**Data** — `volume_aggregate_1h.period_start_at`, `.period_end_at`,
`.computed_at`, `.bytes`, and the `_24h`, `_7d`, `_30d` twins. Source: the
aggregates are `opnview`'s own primary store rather than a cache over the
firewall; the reason they must be is `docs/opnsense-api-survey.md`, *Data source
3 — NetFlow / Insight*, *Retention on the firewall*, which establishes that
per-address-pair volume exists only at daily resolution and only for 62 days.

**Existing query** — Reuses no screen query, and uses the diagnostic query
`-- diagnostic: Aggregate coverage per period` in `sql/queries/diagnostics.sql`
unchanged.

**Empty state** — *Source unavailable*: *"The flow-volume source is not
reachable, so the aggregates are not being refreshed. The figures below are as
of the timestamp shown and are not current."* *Source present but disabled*:
*"NetFlow local collection is switched off on the firewall, so no new volume is
arriving to aggregate."* *Reachable and no rows*: *"No aggregate slot exists for
this period yet. `opnview` computes these from its own history, so a fresh
installation has none until the first refresh completes."* A fresh installation
showing no slots is a normal condition, stated as such, and never a zero.

### Unresolved joins

**Type** — `unresolved_joins`

**Question** — How much of what I am looking at could not be joined to a name?

**Shows** — Four counters with their trends: flows whose interface device name
matched no entry in the interface map; flows whose `rid` matched no known rule;
clients identified only by address behind an interface rather than by a DHCP client
identity or a MAC; and addresses whose geolocation lookup is a `miss` or still
`pending`. Each counter links to the records behind it. This widget exists so the
quality of every other widget is visible rather than assumed.

**Parameters** — `period`; `counters` (any subset of `interface`, `rule`,
`client_identity`, `geo`); `interfaces` (optional); `show_records` (boolean).

**Data** — `flow.interface_lookup_state`, `flow.rule_lookup_state`,
`flow.interface_device`, `flow.rid`, `flow.observed_at`, `flow.src_interface_id`;
`interface_map.device`, `interface_map.description`; `rule.pf_label`;
`client.identity_kind`, `client.identity_key`, `client.mac`,
`client.unstable_identity`; `geo_asn.lookup_state`, `geo_asn.looked_up_at`,
`geo_asn.address`. Sources: the two first-class join keys,
`docs/opnsense-api-survey.md`, *Runtime discovery* (i) and (ii); geo enrichment
from the MaxMind databases.

**Existing query** — Adapts `-- screen: Matrix` for the rule counter, which
already projects `unknown_rule_connections` as
`sum(CASE WHEN f.rule_lookup_state = 'not_found' THEN 1 ELSE 0 END)`. The
interface, identity and geo counters have no counterpart among the seven and are
new projections over columns the schema already carries.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
join quality cannot be measured. A zero here would mean 'nothing to join', not
'everything joined'."* *Source present but disabled*: *"Local logging is switched
off, so there are no records whose joins could be checked."* *Reachable and no
rows*: *"Every record in this period joined cleanly."* The third case is the only
one that may be rendered as a zero, because there it genuinely is one.

---

## Coverage of the maintainer's named questions

Every question the maintainer named has at least one entry answering it. Where an
entry answers a question only partially, the gap blocking the rest is named.

| Named question | Entries answering it | Complete today? |
|---|---|---|
| Site used, and by whom | *Sites by client* (primary), *Top sites*, *Client traffic detail*, *Attribution rate per client* | Partial — needs G5 for periods beyond `flow` retention; the attribution rate is exposed unconditionally so the coverage is honest |
| World map of passed **and** blocked traffic | *Passed-traffic world map* and *Blocked-traffic world map* — **two widgets, by the maintainer's decision, not one map with two layers**; plus *Destination countries* and *Destination operators* | Yes for both maps — the aggregates already carry `allowed_connections` and `blocked_connections` separately, and `security_event.dst_address` covers the detection engine. One honest limit, stated in the blocked map rather than hidden: a **DNS**-blocked lookup resolves to no address and therefore cannot appear on any map, so it is carried as a counter beside it |
| Who talks to whom, legibly rather than precisely | *Traffic Sankey* (primary), *Interface traffic matrix* | Yes for interface-to-interface and interface-to-operator; a **client**-to-operator Sankey beyond the `flow` retention horizon needs G4 |
| Blocked content unified across DNS advertising lists, DNS threat lists, Suricata and firewall rules | *Unified blocked feed* (primary), *Blocked DNS lookups*, *Blocked by firewall rule*, *Alerts by signature* | Partial — **which list** is answerable now that `blocklist` exists (G1 closed), with the list's *purpose* assigned by the user rather than guessed from its name; what remains is G2, one vocabulary across the four engines |
| Traffic by VLAN | *Interface volume ranking* (primary), *Interface traffic matrix*, *Traffic over time by scope* | Yes — interface membership is modelled and never inferred from a name |
| Traffic by MAC or IP | *Client volume ranking* (primary), *Client traffic detail*, *Alerts by client and interface* | Partial — needs G4 for periods beyond `flow` retention |
| Outbound / inbound / inter-VLAN as selectable scopes | *Traffic over time by scope* (primary), *Interface traffic matrix*, *Interface volume ranking* | Partial — inter-VLAN is complete from `flow.traffic_scope`; outbound and inbound need G3 for any pre-computed period |
| **Firewall health and telemetry** — uptime, CPU, RAM, disk, temperature, per-interface bandwidth, latency. *Not one of the six originally named; added by the maintainer after reviewing this cycle's captures.* | *Firewall health overview* (primary), *Interface throughput*, *Custom chart* | **No — and not partially.** The model holds no telemetry (G9) and no telemetry source has been surveyed (G10). This is a new data category rather than a missing column |
| **The per-person view** — Bob's phone, tablet and laptop as one Bob rather than three cards, the way Firewalla presents a household. *Added by the maintainer with the `owner` entity.* | *Per-person activity* (primary), and the `connection_tree` rooted at a person | **Yes — G11 closed.** The model carries the person (`owner`, `client.owner_id`), the `owner_volume_aggregate_*` family carries the long periods, and the widget carries the explicit **unassigned** card that keeps the unowned majority of a network visible. One honest limit, stated in the widget rather than hidden: a person exists only because somebody typed them in, so a fresh installation's per-person view is empty until it is told who there is |
| **Which blocklist refused a lookup, and what that list is for** — *added by the maintainer with G1* | *Blocked DNS lookups* (primary), *Unified blocked feed* | Yes for the **name**, which the endpoint reports and `blocklist.name` stores verbatim. The **purpose** is answerable only for lists the user has classified, by design: the API does not report it and a name is not evidence, so an unclassified list reads *purpose not assigned* rather than being sorted into a category by a heuristic |
| **The installation's public address** — what it is, on which gateway, when it last changed — *added by the maintainer* | *Public address* | Partial — the address is readable from an endpoint the survey established, and **when it last changed is not**: no endpoint keeps a history, and the model has nowhere to store one either (G13). The widget shows the address and says plainly that it has no change history rather than inventing one |
| **Arbitrary combinations of series on one chart** — the maintainer's example being temperature and CPU against WAN bandwidth, explicitly as an example rather than a specification | *Custom chart* (primary) | Structurally yes, once G9 and G10 are closed: the widget's `series` parameter is a list, so the combination is authored by the user rather than enumerated here |

## Gaps closed

Two of the gaps this catalogue named have since been closed in the schema, and
they are recorded here rather than deleted, so that a reader meeting `G1` or
`G11` in an older document can find out what happened to it. **Their
identifiers are retired and are never reused.** Neither carries a
missing-from-model marker any more; the **Data** fields that used to carry them
now name the columns that exist.

| Id | Gap, as it was named | How it was closed |
|---|---|---|
| G1 | `dns_resolution` has no `blocklist` column, so "blocked by which list" is unanswerable | A `blocklist` table holding the list's **name**, stored verbatim from the `blocklist` field of `/api/unbound/overview/search_queries` (`docs/opnsense-api-survey.md`, *Data source 5*, *Response shape*), plus `dns_resolution.blocklist_id`. The list's **purpose** is a separate column on that table, **assigned by the user and never inferred**, because the endpoint reports what a list is called and nothing about what it is for — the same rule `interface.user_label` and `client.owner_id` obey. A list nobody has classified reads *purpose not assigned*; a blocked lookup naming no list reads *list not recorded*. See `docs/data-model.md`, the `blocklist` entity |
| G11 | No widget and no aggregate answers the per-person question | The `owner_volume_aggregate_1h` / `_24h` / `_7d` / `_30d` family, keyed on `(period_start_at, owner_id, traffic_scope)` with a NULL `owner_id` carrying the mandatory **unassigned** bucket; and the *Per-person activity* entry above, plus `connection_tree` rooted at a person. See `docs/data-model.md`, the per-person volume store |

## Gaps found

Eleven gaps remain open: two were known before this cycle, eight turned up by
mapping the widgets onto the schema, and two — G12 and G13 — arrived with the
entries added for the traffic-composition donut and the public address. Two
others, G1 and G11, were open when this list was first written and are now in
*Gaps closed* above, which is why the identifiers below are not contiguous.
**Each open gap carries exactly one missing-from-model marker in a Data field
above, so a grep for that marker returns eleven and this table has eleven
rows.** None is fixed here: this document names gaps and a later cycle
implements them.

G9 and G10 are of a different order from the rest. G2 through G8, G12 and G13,
are columns
and tables, and widget entries, missing from a model that otherwise has the
right shape. **G9 and G10 are a whole data category the model was never designed to hold and the step-1
survey never looked at** — firewall health and telemetry — and closing them means
a new table family, a seventh provider kind and a sixth surveyed data source,
not a migration adding columns.

G12 is of a third kind again, and the distinction matters because it decides
what closing it would mean. It is not a column the model forgot and not a
source nobody surveyed: it is a **classification that does not exist in the
data at all**. No amount of schema work produces an application name from a
port number, and the only two routes to one — a heuristic, or deep packet
inspection — are respectively dishonest and unavailable to a read-only API
client. It is recorded so that nobody closes it by guessing.

**G9 and G10 are deliberately left open here.** The maintainer has decided that
telemetry gets **its own research cycle, after the mockup**, covering the API
survey of the health endpoints and the storage-engine choice together — the
second of those being newly available because the "single Go binary" rule has
been replaced by one that admits embedded storage engines beside SQLite
(`docs/ui-references.md`, *Proposed ROADMAP amendment*, item 7). This catalogue's
job is therefore to name what is missing precisely enough to give that cycle a
starting point, and explicitly **not** to resolve it: the health widgets stay
marked, the endpoint table stays a list of candidates, and no storage decision is
taken here.

| Id | Gap | Known before this cycle? | Fix it needs | Cited to |
|---|---|---|---|---|
| G2 | No unified blocking vocabulary across the four engines; a block lives in three tables under three column names | **Yes** (as "no query aggregates the unified blocked view"; the mapping shows it is a model gap as well as a query gap) | A `blocked_decision` view over `flow`, `dns_resolution` and `security_event` projecting `(occurred_at, engine_kind, engine_reference, client_id, interface_id, target, target_kind)`, `engine_kind` constrained to the four categories | `docs/opnsense-api-survey.md`, *Data source 1*, *Data source 2*, *Data source 5*; and `sql/queries/screens.sql`, which has no such query |
| G3 | The four volume aggregates carry `traffic_scope` but no `direction`, so outbound and inbound cannot be told apart in any pre-computed period | No | A `direction` column on all four aggregate tables in the `in` / `out` / `unknown` vocabulary `flow.direction` uses, included in each slot's uniqueness index | `docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*, *Response shape* (`dir`) |
| G4 | No per-client volume aggregate; the aggregates are keyed on interfaces and peer addresses only | No | A `client_volume_aggregate_<period>` family keyed on `(period_start_at, src_client_id)` carrying bytes, allowed, blocked and distinct destinations | `docs/opnsense-api-survey.md`, *Data source 3 — NetFlow / Insight*, *Retention on the firewall* (why `opnview`'s own aggregates are the primary store) |
| G5 | No per-domain volume aggregate; `domain_attribution` is per flow and purged with its parents | No | A `domain_volume_aggregate_<period>` family keyed on `(period_start_at, site_name, src_client_id)` | `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, *Retention on the firewall* (the 7-day ceiling on the firewall side) |
| G6 | `dns_resolution` carries no interface, so a lookup from a client address with no client row cannot be placed | No | A nullable `interface_id` plus an `interface_lookup_state` in the `resolved` / `not_found` / `pending` vocabulary | `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, *Response shape* (`client`); *Runtime discovery* (i) for the addressing to resolve against |
| G7 | `security_event` carries no destination client or interface, so "which interface was the target" is unanswerable | No | `dst_client_id` and `dst_interface_id` mirroring the source pair, with an index on `(dst_interface_id, occurred_at)` | `docs/opnsense-api-survey.md`, *Data source 2 — Suricata `eve.json`*, *Response shape* (`dest_ip`) |
| G8 | The interfaces a security-event provider covers are not modelled; `source_availability.detail` is free text | No | A `provider_interface_coverage` table keyed on `(provider_id, interface_id)` with `is_covered` and `determined_at` | `docs/opnsense-api-survey.md`, *Runtime discovery* (v) (`ids.general.interfaces`, keys whose `selected` is truthy) |
| G9 | ~~The model holds no system telemetry at all~~ — **closed 2026-09-27 by step 4A.** `measurement_sample` stores a subject, a measure, a unit, a value and an instant, and carries both the firewall's gauges and the sampled per-pair volume | Closed | — | Probed against a live OPNsense 26.7.3_11; recorded in `docs/opnsense-api-survey.md`, *Verified against a live firewall* |
| G10 | ~~No source of system telemetry has been surveyed~~ — **closed 2026-09-27.** Every telemetry endpoint answers and the survey now records them. What stays open is smaller and is not a gap in the model: the responses' **field names** are unestablished, so a reading that does not answer is recorded as absent rather than as a zero | Closed | — | Same section of the survey |
| G12 | **Nothing in the model classifies a flow as an application.** The schema knows a protocol number and a port and stops there, so the dimension ntopng and Zenarmor both lead with — which app is this — cannot be offered by the composition donut or by anything else | No — it appeared with the *Traffic composition, now* entry | Neither of the two available routes, and that is the finding. A **port-and-SNI heuristic** is cheap and wrong at exactly the edges that matter (a service on a non-standard port, a CDN fronting a dozen products, anything tunnelled over 443), and a heuristic presented as a fact is the kind of lie this project refuses. **Deep packet inspection** is what those products actually do, and OPNsense exposes none of it to a read-only API client: Suricata's application-layer parsers feed detection rather than the alert feed, and the `tls` and `http` events that would carry a server name cannot be read back at all. Closing this needs a source that does not exist, not a column, and neither route is to be implemented | `docs/opnsense-api-survey.md`, *Gaps and alternatives*, gaps 2 and 8; and `flow.protocol`, `flow.dst_port`, `pair_volume_observation.service_port`, which are the whole of what the model knows about what a flow carries |
| G13 | **The schema stores no interface address, and no address history**, so the installation's own public address has nowhere to live and "when did it last change" is unanswerable twice over — the model cannot hold it and no endpoint reports it | No — it appeared with the *Public address* entry | An `interface_address` table keyed on `(interface_id, address)` with the prefix length, the address family, whether a gateway sits behind it, a nullable gateway name, and `first_seen_at` / `last_seen_at` — so a change is a new row and the history is the table. The change time can only ever come from `opnview` having looked before, because the API reports the address an interface has now and keeps no history of the ones it had. Separately, and not fixable at all: a firewall behind an upstream NAT cannot see the address the internet sees, and finding it would need a third outbound call the project does not allow | The address fields **are** established: `/api/interfaces/overview/interfaces_info` returns `addr4`, `addr6`, `ipv4[]`, `ipv6[]` and `gateways[]` (`docs/opnsense-api-survey.md`, *Runtime discovery* (i)). What would still have to be surveyed is narrow: the response shape of `/api/routes/gateway/status`, marked `UNVERIFIED:` in *Endpoint candidates, cited rather than asserted* |

**One candidate was raised and withdrawn**, recorded here so it is not
rediscovered: `security_event.flow_ref` is an unconstrained `INTEGER` with no
foreign key to `flow`. No widget in this catalogue reads it, so this document has
no standing to call it a gap, and it carries no identifier and no marker.

## What this catalogue does not decide

Named honestly rather than left implicit.

- **Which widgets ship first.** The catalogue is a first pass and declares
  itself extendable; the order of implementation is the maintainer's call.
- **How a widget is rendered.** No charting library, no frontend framework and
  no widget-rendering technology is chosen here; that is explicitly out of
  scope for this cycle.
- **The final wording of the UI copy.** The sentences quoted above fix the
  *content* each widget must carry — the observation-point limit, the inferred
  nature of site names, the three availability conditions — not the final
  phrasing.
- **Whether the catalogue covers the questions the maintainer actually has.**
  It covers the six he named plus what the mapping turned up. Whether that is
  the right set is for him to judge, not for this document to assert.
