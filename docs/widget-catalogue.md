# Widget catalogue — first pass

This document is the **first pass** at `opnview`'s widget catalogue. It is
deliberately extendable: a widget is a named, parameterised unit of display,
and adding one is an addition to this list rather than a change to the shape of
the product. Nothing here is closed, and the catalogue is expected to grow as
the maintainer's questions sharpen.

**It replaces the seven-screen structure of `ROADMAP.md` step 7.** Step 7 names
seven fixed screens — Overview, Matrix, Segment, Device, Blocked, Alerts, Map —
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

Every widget is a `###` heading whose body carries exactly six fields, in this
order:

- **Question** — the question a user is asking when they place this widget.
- **Shows** — what is drawn, and the UI copy the widget is required to carry.
- **Parameters** — what the user can set, and what an exported dashboard file
  therefore has to record.
- **Data** — the `table.column` pairs in `migrations/*.sql` that feed it, or the
  missing-from-model marker and what would have to be added.
- **Existing query** — which of the seven queries in `sql/queries/screens.sql`
  it reuses, adapts or replaces, or why none applies.
- **Empty state** — what is rendered when there is nothing to draw,
  distinguishing the three conditions the model carries.

**Marker convention.** The missing-from-model marker is the greppable string
that opens each gap in a **Data** field below. It appears **exactly once per
gap**, in the first widget entry where that gap arises; every later widget
depending on the same gap refers to it by its identifier (`G1` … `G10`) without
repeating the marker. The marker is therefore written out **only** inside a
**Data** field — never in the prose of this section, never in the *Gaps found*
table — so that a plain `grep -c` for it returns exactly the number of gaps,
which is exactly the row count of that table. It is named rather than spelled
here for that reason, and it is spelled in full ten times below.

**Illustrative values.** Where an example address is needed below, it comes from
the documentation ranges — `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`
and `2001:db8::/32` — and is labelled as an example. **No interface name, VLAN
name, segment name, CIDR or address of any real network appears anywhere in this
document.** Every such value is discovered at runtime through the OPNsense API
(`docs/opnsense-api-survey.md`, *Runtime discovery*) and is never hardcoded, in
code, in a template, in a default value or in a widget definition.

**Sources.** Every widget below rests on the authenticated OPNsense REST API and
on nothing else. No entry proposes reading a file on the firewall, opening an
SSH session, or any access other than that API, and no entry proposes `opnview`
changing a setting on the firewall: where a source has to be switched on, that
is a manual operation the user performs in the OPNsense web UI, documented in
the README at step 8.

## Two standing UI statements

Two sentences recur in the **Shows** field below, because two facts have to be
on screen wherever the numbers they qualify are on screen.

1. **The observation-point limit.** `opnview` sees only what crosses the
   router. Traffic between two devices inside one segment never reaches the
   firewall and is invisible to all five sources, so **every byte, packet and
   connection figure is a lower bound**. Required copy, or wording carrying the
   same content: *"Counts only traffic that crossed the firewall. Traffic
   between two devices inside the same segment is not visible here, so this is a
   lower bound."*

2. **Site names are inferred.** On OPNsense 26.7 a site name comes from
   correlating a resolver lookup with a flow that followed it, and from nothing
   else: Suricata exposes no `dns` event type and its `tls` / `http` events
   cannot be read back (`docs/opnsense-api-survey.md`, *Gaps and alternatives*,
   gaps 1 and 2). Required copy, or wording carrying the same content: *"Site
   names are inferred by correlating resolver lookups with the traffic that
   followed them. They are not observed. A device using encrypted DNS, or a
   cached name, will be under-attributed."*

---

## Traffic and volume

### Segment traffic matrix

**Question** — Which segment talks to which, how much, and how much of it was
blocked?

**Shows** — A source-segment × destination-segment grid. Each cell carries the
observed byte volume, the allowed connection count, the blocked connection
count and the distinct rule descriptions that matched; a cell is clickable and
opens the underlying records. The right-hand column is north-south traffic,
where the destination sits in no discovered segment. Segment labels are the
user's own label where one has been set and the firewall's discovered
description otherwise — **never a name-based classification**: a segment's
nature is never inferred from what it is called. Required UI copy: the
observation-point limit sentence, verbatim or equivalent, beneath the grid.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `scope`
(`all` | `east_west` | `north_south`); `measure` (`bytes` | `allowed` |
`blocked`); `segments` (an optional list of segment references limiting the
rows and columns; empty means every discovered segment); `show_rules`
(boolean).

**Data** — `flow.src_segment_id`, `flow.dst_segment_id`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at`, `flow.rule_id`,
`flow.rule_lookup_state`; `rule.description`; `segment.user_label`,
`segment.discovered_description`. For the 7 d and 30 d periods the same shape is
read from `volume_aggregate_7d.bytes`,
`volume_aggregate_7d.allowed_connections`,
`volume_aggregate_7d.blocked_connections`,
`volume_aggregate_7d.src_segment_id`, `volume_aggregate_7d.dst_segment_id`,
`volume_aggregate_7d.period_start_at` and the `_30d` twins. Source: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; segment discovery,
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

**Question** — How much traffic left the network, entered it, and stayed between
segments, over time?

**Shows** — A soft area chart with one band per selected scope — outbound,
inbound, inter-segment — over the chosen period, with the numbers brought
forward as totals above the chart. Outbound and inbound derive from
`flow.direction`; inter-segment is `traffic_scope = 'east_west'`. Required UI
copy: the observation-point limit sentence.

**Parameters** — `period`; `scopes` (any subset of `outbound`, `inbound`,
`inter_segment`); `measure` (`bytes` | `connections`); `segments` (optional
list of segment references); `stacked` (boolean).

**Data** — `flow.observed_at`, `flow.direction`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.src_segment_id`,
`flow.dst_segment_id`. For the long periods: `MISSING FROM MODEL:` **G3 — the
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

### Segment volume ranking

**Question** — Which VLAN or segment is generating the traffic?

**Shows** — A horizontal ranking of segments by observed volume for the period,
each bar carrying the byte total, the device count behind it and the blocked
connection count, with the east-west and north-south shares shown separately
within the bar. Labels come from the user's own segment label, falling back to
the firewall's discovered description. Required UI copy: the observation-point
limit sentence.

**Parameters** — `period`; `measure` (`bytes` | `connections` | `devices`);
`scope` (`all` | `east_west` | `north_south`); `limit` (how many segments to
show); `include_unlabelled` (boolean — whether segments the user has not
labelled are listed).

**Data** — `flow.src_segment_id`, `flow.packet_bytes`, `flow.action`,
`flow.traffic_scope`, `flow.observed_at`, `flow.src_device_id`;
`segment.user_label`, `segment.discovered_description`, `segment.link_kind`,
`segment.is_tunnel`, `segment.vlan_tag`. Long periods:
`volume_aggregate_7d.src_segment_id`, `.bytes`, `.allowed_connections`,
`.blocked_connections` and the `_30d` twins. Source: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; segment discovery,
*Runtime discovery* (i).

**Existing query** — Adapts `-- screen: Matrix`, collapsing its
destination-segment dimension so one row per source segment remains. It does
**not** reuse `-- screen: Segment`, which is scoped to a single segment by
`:segment_id` and enumerates devices rather than segments.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
segments cannot be ranked."* *Source present but disabled*: *"Local logging is
switched off, so there are no filter-log records to rank segments by."*
*Reachable and no rows*: *"No segment carried traffic across the firewall in this
period."* When segments are discovered but silent, they are listed with an empty
bar rather than omitted, so a silent segment is visibly silent.

### Device volume ranking

**Question** — Which machine, by MAC or by address, is generating the traffic?

**Shows** — A ranked list of devices with hostname where a lease supplied one,
the MAC where one is known, the last observed address, the segment, the byte
total, the blocked connection count and the count of distinct destinations. A
device whose MAC carries the IEEE locally-administered bit is badged **unstable
identity**, with the explanation that a randomised MAC does not identify a
machine across sessions and has deliberately not been merged into a phantom
device. Required UI copy: the observation-point limit sentence.

**Parameters** — `period`; `segments` (optional list of segment references);
`identity` (`any` | `dhcp_client_id` | `mac` | `address_in_segment` — which
identity levels to include); `limit`; `include_unstable` (boolean); `measure`
(`bytes` | `connections` | `destinations`).

**Data** — `flow.src_device_id`, `flow.src_segment_id`, `flow.packet_bytes`,
`flow.action`, `flow.observed_at`, `flow.dst_address`; `device.hostname`,
`device.mac`, `device.mac_is_randomised`, `device.unstable_identity`,
`device.identity_kind`, `device.identity_key`, `device.last_address`,
`device.segment_id`, `device.vendor_hint`. For any period longer than the
`flow` retention horizon: `MISSING FROM MODEL:` **G4 — there is no per-device
volume aggregate.** The four `volume_aggregate_*` tables are keyed on
`(period_start_at, src_segment_id, dst_segment_id, peer_address)` and carry no
device dimension at all, so a 7 d or 30 d per-device total can only be computed
by scanning `flow`, which the `retention_seconds` row of `setting` bounds. What would have to
be added: a parallel `device_volume_aggregate_<period>` family keyed on
`(period_start_at, src_device_id)` with `bytes`, `allowed_connections`,
`blocked_connections` and `distinct_destinations`, refreshed on the same
contract as the segment aggregates. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; device identity
from leases, *Data source 4 — DHCP leases*.

**Existing query** — Adapts `-- screen: Segment`. That query is the right
projection — device, hostname, address, identity kind, unstable-identity flag,
volume, blocked count, distinct destinations — but is pinned to one segment by
`f.src_segment_id = :segment_id`. This widget generalises the predicate to a set
of segments, or to all of them.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
devices cannot be ranked by traffic."* If the `dhcp_lease` provider is
additionally unavailable, the widget adds: *"No DHCP source is reachable, so
devices are named by address only."* *Source present but disabled*: *"Local
logging is switched off, so no per-device traffic has been recorded."*
*Reachable and no rows*: *"No device sent traffic across the firewall in this
period."*

### Device traffic detail

**Question** — Where did this one machine go, and what was allowed?

**Shows** — One device's outbound records over the period: timestamp,
destination address and port, protocol, action, traffic scope, byte count,
country, operator, and the inferred site name where a resolver lookup could be
correlated with the flow. A record with no attribution is shown with a null site
name and its address, country and operator — never omitted, because an
unattributed destination is a fact rather than a gap in the table. The
correlation delay is shown per attributed row, so a wide delay reads as a weak
attribution. Required UI copy: the observation-point limit sentence and the
site-names-are-inferred sentence.

**Parameters** — `device` (a device reference — required); `period`; `action`
(`all` | `allowed` | `blocked`); `scope` (`all` | `east_west` |
`north_south`); `columns` (which of the available columns to show); `limit`.

**Data** — `flow.id`, `flow.observed_at`, `flow.dst_address`, `flow.dst_port`,
`flow.protocol`, `flow.action`, `flow.traffic_scope`, `flow.packet_bytes`,
`flow.src_device_id`, `flow.interface_device`, `flow.interface_lookup_state`;
`domain_attribution.site_name`,
`domain_attribution.correlation_delay_seconds`; `geo_asn.lookup_state`,
`geo_asn.country_code`, `geo_asn.operator`, `geo_asn.dataset_build_at`;
`interface_map.description`. Sources: filter logs,
`docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*; resolver lookups,
*Data source 5 — Resolver DNS lookups*; geo and ASN enrichment, the second of the
two outbound calls the project allows.

**Existing query** — Reuses `-- screen: Device` unchanged. The bound parameter
`:device_id` is exactly this widget's `device` parameter after local resolution.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
this device's traffic cannot be listed."* *Source present but disabled*: *"Local
logging is switched off, so no records exist for this device."* *Reachable and
no rows*: *"This device sent no traffic across the firewall in this period.
Traffic it exchanged with machines inside its own segment would not appear
here."* Separately, when the `dns_lookup` provider is unavailable or disabled the
site-name column is rendered as a labelled column-level notice — *"No resolver
source is available, so no site name can be inferred for any record"* — rather
than as an empty column.

---

## Sites and names

### Top sites

**Question** — Which sites is this network actually using?

**Shows** — A ranked list of inferred site names with the number of flows
attributed to each, the number of distinct devices that reached them, the byte
volume, and the country and operator behind the address. Each row expands to the
devices behind it. Required UI copy: the observation-point limit sentence and
the site-names-are-inferred sentence, plus the network-wide attribution rate as
a headline figure, so the list is read in the knowledge of how much traffic it
does *not* cover.

**Parameters** — `period`; `segments` (optional); `devices` (optional);
`limit`; `min_flows` (suppress names seen fewer than N times);
`group_by_registrable_domain` (boolean — whether `a.example` and `b.example`
collapse).

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`,
`domain_attribution.dns_resolution_id`,
`domain_attribution.correlation_delay_seconds`; `flow.observed_at`,
`flow.src_device_id`, `flow.src_segment_id`, `flow.packet_bytes`,
`flow.dst_address`; `dns_resolution.domain`, `dns_resolution.client_address`,
`dns_resolution.looked_up_at`; `geo_asn.country_code`, `geo_asn.operator`. For
any period longer than the `flow` retention horizon: `MISSING FROM MODEL:`
**G5 — there is no per-domain volume aggregate.** `domain_attribution` is
per-flow and is purged with its parents, so a 30 d "top sites" list cannot
outlive the `retention_seconds` row of `setting`. What would have to be added: a
`domain_volume_aggregate_<period>` family keyed on
`(period_start_at, site_name, src_device_id)` carrying `flow_count`, `bytes` and
`distinct_devices`, refreshed on the same contract as the segment aggregates.
Sources: resolver lookups, `docs/opnsense-api-survey.md`, *Data source 5 —
Resolver DNS lookups*; filter logs, *Data source 1 — Filter logs*.

**Existing query** — **Replaces** nothing and reuses nothing: no query in
`sql/queries/screens.sql` aggregates site names. `-- screen: Device` returns
`a.site_name` per record for one device, which is the only place a site name
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

### Sites by device

**Question** — Which site was used, and by whom?

**Shows** — A two-level view answering the maintainer's question in one widget:
devices down the side, their top inferred site names across, each cell carrying
the flow count and the byte volume. A device with no attributed flows is listed
with an explicit *"no site name could be inferred"* row rather than dropped, and
its attribution rate is shown beside it. Required UI copy: the
observation-point limit sentence and the site-names-are-inferred sentence.

**Parameters** — `period`; `segments` (optional); `devices` (optional);
`sites_per_device` (how many names per row); `min_flows`;
`show_attribution_rate` (boolean, default true).

**Data** — `domain_attribution.site_name`, `domain_attribution.flow_id`;
`flow.src_device_id`, `flow.src_segment_id`, `flow.observed_at`,
`flow.packet_bytes`; `device.hostname`, `device.last_address`,
`device.identity_kind`, `device.unstable_identity`; `dns_resolution.domain`,
`dns_resolution.device_id`. Long periods depend on gap G5. A per-segment
breakdown of *lookups* rather than of *flows* depends on gap G6, below.
Sources: resolver lookups, `docs/opnsense-api-survey.md`, *Data source 5 —
Resolver DNS lookups*; device identity from leases, *Data source 4 — DHCP
leases*.

**Existing query** — **Replaces** the site-name half of `-- screen: Device`.
That query answers "this device's records, with a name where one exists"; this
widget answers "which names, by which device", which is a different
aggregation over the same join and has no counterpart in
`sql/queries/screens.sql` (gap G2).

**Empty state** — *Source unavailable*: *"No resolver source is reachable, so no
site can be attributed to any device."* *Source present but disabled*: *"The
resolver is running with reporting switched off, so no lookups are being
recorded. The procedure is in the README; `opnview` never changes the setting."*
*Reachable and no rows*: *"No lookup could be correlated with traffic from these
devices in this period."* Devices are still listed in all three cases, so the
widget degrades to "these machines, no names" rather than to blankness.

### Attribution rate per device

**Question** — How much of this device's traffic can be named at all, and how
much should I therefore distrust the site lists?

**Shows** — One row per device with the flow count, the attributed count, the
attribution rate as a percentage, and the mean and maximum correlation delay.
Rows are ordered worst-first, so the devices whose site lists are least
trustworthy are the ones the user sees. A device at or near zero is annotated
with the likely cause — encrypted DNS (DNS-over-TLS or DNS-over-HTTPS), a
client-side cache, or a resolver other than the firewall's — quoting
`docs/opnsense-api-survey.md`, *Gaps and alternatives*, gap 8. Required UI copy:
the site-names-are-inferred sentence, and the honest statement that a low rate
means *under-attributed*, never *mis-attributed*: `opnview` never invents a
domain and falls back to address, country and operator.

**Parameters** — `period`; `segments` (optional); `devices` (optional);
`limit`; `sort` (`worst_first` | `best_first` | `by_volume`).

**Data** — `flow.src_device_id`, `flow.observed_at`, `flow.id`;
`domain_attribution.flow_id`,
`domain_attribution.correlation_delay_seconds`; `device.hostname`,
`device.last_address`, `device.identity_kind`. Sources: resolver lookups,
`docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*.

**Existing query** — Reuses no screen query, and uses the diagnostic query
`-- diagnostic: Attribution rate per device` in `sql/queries/diagnostics.sql`
unchanged. That query is executed by `sql/schema-checks.sh` today, so this
widget rests on a projection the schema checks already exercise.

**Empty state** — *Source unavailable*: *"No resolver source is reachable, so
the attribution rate is undefined rather than zero. `opnview` will not show 0 %
for a source that is not there."* *Source present but disabled*: *"Resolver
query reporting is switched off, so nothing can be attributed and the rate is
undefined."* *Reachable and no rows*: *"No traffic was observed for these devices
in this period, so there is nothing to attribute."* The distinction between an
undefined rate and a zero rate is the whole point of this widget and must not be
collapsed.

---

## Destinations, geography and operators

### Passed-traffic world map

**Question** — Where in the world did the traffic that was **allowed** actually
go?

**Shows** — A world map of destinations that traffic reached, as **point marks
on a de-saturated basemap**, sized by volume, with the detail — address or
operator, country, bytes, connection count, contributing segments — in a popup
rather than on the canvas. This rendering is not a free choice: it is the
maintainer's recorded preference in `docs/ui-references.md`, *The maintainer's
recorded preferences*, taken from ntopng — **no choropleth, no arcs, the map as
background and the data as foreground**. Required UI copy: the
observation-point limit sentence, and an **unplaced-volume statement**, below.

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

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `segments` (optional
list of segment references; empty means every discovered segment); `scope`
(`north_south` — the default and only meaningful value for a world map, since
east-west traffic has no remote endpoint to place — or `all`, which additionally
counts east-west volume into the unplaced counter so the totals reconcile);
`size_by` (`bytes` | `connections`); `min_bytes`; `show_unplaced` (boolean,
default true and not recommended to disable); `cluster` (boolean — whether
nearby marks merge at low zoom, with the merged count shown inside the mark).

**Data** — `volume_aggregate_24h.peer_address`, `.bytes`,
`.allowed_connections`, `.src_segment_id`, `.traffic_scope`,
`.period_start_at`, `.computed_at`, and the `_1h`, `_7d` and `_30d` twins;
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`, `geo_asn.asn`,
`geo_asn.operator`, `geo_asn.dataset_build_at`; `segment.user_label`,
`segment.discovered_description`. The allowed side needs no gap: the aggregates
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
rows*: *"No allowed traffic reached an address outside a discovered segment in
this period."* Without a MaxMind licence key the map is disabled with that
stated plainly, and every other widget keeps working.

### Blocked-traffic world map

**Question** — Where in the world was the traffic that was **stopped** trying to
go, or coming from?

**Shows** — The same rendering as its passed-traffic twin — point marks on a
de-saturated basemap, sized by volume or by count, detail in the popup — over
blocked traffic only. Two engines contribute and the popup names which: the
**firewall**, whose blocked records carry a real remote address; and the
**security engine**, whose events carry a peer address. **The DNS engines
contribute nothing to this map, and the widget says so**, because a lookup the
resolver refused never resolved to an address and there is therefore nothing to
place — a fact about the world rather than a gap in the model. Required UI copy:
the observation-point limit sentence; the unplaced-volume statement, below; the
DNS statement just made; and, when the security engine contributes, the
per-segment coverage statement (gap G8).

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
widget explains why); `segments` (optional); `endpoint` (`destination` — where
blocked traffic was heading — or `source` — where blocked inbound traffic came
from; a genuine choice, because the interesting end differs between egress
policy and inbound attack); `size_by` (`connections` | `bytes` | `events`);
`min_count`; `show_unplaced` (boolean, default true); `cluster` (boolean).

**Data** — For the firewall engine: `volume_aggregate_24h.peer_address`,
`.blocked_connections`, `.src_segment_id`, `.period_start_at` and the period
twins, for the aggregated view; and, for the record-level view within the
`flow` retention horizon, `flow.action`, `flow.observed_at`, `flow.src_address`,
`flow.dst_address`, `flow.dst_port`, `flow.src_segment_id`, `flow.direction`,
`flow.packet_bytes` through the `blocked_event` view. For the security engine:
`security_event.occurred_at`, `security_event.src_address`,
`security_event.dst_address`, `security_event.event_action`,
`security_event.src_segment_id`, `security_event.provider_id`,
`security_event.rule_identity`, `security_event.signature`. For both:
`geo_asn.address`, `geo_asn.lookup_state`, `geo_asn.country_code`,
`geo_asn.country_name`, `geo_asn.latitude`, `geo_asn.longitude`,
`geo_asn.operator`, `geo_asn.dataset_build_at`. Two known gaps bear on this
widget and neither is new: placing a security event by the segment it targeted
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
per-segment coverage statement.

### Destination countries

**Question** — Which countries, ranked, and how much of that was blocked?

**Shows** — A ranked table of countries with byte volume, allowed and blocked
connection counts, the number of distinct peers, the contributing segments, and
the build date of the geolocation dataset that answered, so a stale enrichment is
visible. An address whose lookup state is `miss` or `pending` is carried in an
explicit unresolved row. Required UI copy: the observation-point limit sentence.

**Parameters** — `period`; `segments` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `sort` (`bytes` | `blocked` | `peers`).

**Data** — `geo_asn.country_code`, `geo_asn.country_name`,
`geo_asn.lookup_state`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_segment_id`, `.period_start_at` and the period twins;
`segment.user_label`, `segment.discovered_description`. Sources as for the world
map.

**Existing query** — Reuses `-- screen: Map`, collapsing the operator and ASN
columns out of the `GROUP BY`. The aggregate-mode guarantee of that query holds
here unchanged: it reads neither `dns_resolution` nor `domain_attribution`, so
this widget is usable with the `aggregate_mode` row of `setting` set to `no_domains`.

**Empty state** — *Source unavailable*: *"No geolocation database is available,
so destinations cannot be grouped by country. The volumes still exist and are
shown unresolved."* *Source present but disabled*: *"NetFlow local collection is
switched off on the firewall, so there is no per-destination volume to group."*
*Reachable and no rows*: *"No traffic left a discovered segment in this period."*

### Destination operators

**Question** — Which operators and autonomous systems is this network's traffic
actually reaching?

**Shows** — A ranked table of ASN and operator name with byte volume, allowed
and blocked connection counts, distinct peer count and the contributing
segments, plus the dataset build date. This is the widget that survives
encrypted DNS: when no site name can be inferred, the operator is usually still
knowable, and this is the honest answer to "where did it go" for a device whose
attribution rate is near zero. Required UI copy: the observation-point limit
sentence, and a note that an operator is not a site — a single operator commonly
fronts many unrelated sites.

**Parameters** — `period`; `segments` (optional); `limit`; `series` (any subset
of `allowed`, `blocked`); `group` (`asn` | `operator`).

**Data** — `geo_asn.asn`, `geo_asn.operator`, `geo_asn.lookup_state`,
`geo_asn.country_code`, `geo_asn.dataset_build_at`;
`volume_aggregate_24h.bytes`, `.allowed_connections`, `.blocked_connections`,
`.peer_address`, `.src_segment_id`, `.period_start_at` and the period twins;
`segment.user_label`, `segment.discovered_description`. Sources as for the world
map.

**Existing query** — Reuses `-- screen: Map` with the country columns collapsed
out of the `GROUP BY`. Like the country widget it reads no table holding a
domain name, so it is available in `no_domains` aggregate mode.

**Empty state** — *Source unavailable*: *"No ASN database is available, so
operators cannot be named. Addresses and volumes are unaffected."* *Source
present but disabled*: *"NetFlow local collection is switched off, so there is no
per-destination volume to attribute to an operator."* *Reachable and no rows*:
*"No traffic reached an address outside a discovered segment in this period."*

---

### Traffic Sankey

**Question** — Who talks to whom, at a glance — which segments send their volume
to which other segments, and to which operators outside?

**Shows** — A Sankey diagram: sources down the left, destinations down the
right, and a ribbon between each pair whose width is proportional to volume.
Every end is **labelled with a resolved name rather than a number** — a
segment's user label or discovered description on the left, and on the right
either another segment (east-west) or the destination operator's AS number
*and* operator name (north-south). Ribbons below the `min_share` threshold
collapse into a single explicit **"Other"** band carrying its own total, so a
long tail cannot make the diagram unreadable while also not being hidden.

This is a wanted widget rather than an admired picture: the maintainer named
Akvorado's ASN Sankey specifically, and `docs/ui-references.md`, *The
maintainer's recorded preferences*, records both the preference and what is
worth taking from it — **the resolved operator name on the diagram itself, and
an honest collapsed band for the tail**. It answers the same question as the
*Segment traffic matrix* and answers it differently: **the matrix is precise and
the Sankey is legible**, so both are offered and the user places whichever suits
the canvas. Required UI copy: the observation-point limit sentence.

**Parameters** — `period` (`1h` | `24h` | `7d` | `30d`); `left` (`segment` —
today the only source dimension that resolves for every flow — or `device`, whose
limits the next field sets out); `right` (`segment` | `operator` | `country`); `scope` (`all` |
`east_west` | `north_south`); `segments` (optional list of segment references
limiting the left-hand side); `measure` (`bytes` | `connections`); `max_nodes`
(how many ribbons before the remainder collapses); `min_share` (the threshold
below which a ribbon joins the "Other" band); `show_blocked` (boolean — whether
ribbon colour additionally encodes the blocked share of each pair, which is
available because the aggregates carry both counts).

**Data** — `volume_aggregate_24h.src_segment_id`, `.dst_segment_id`,
`.peer_address`, `.traffic_scope`, `.bytes`, `.allowed_connections`,
`.blocked_connections`, `.period_start_at`, `.computed_at`, and the `_1h`,
`_7d` and `_30d` twins; `segment.user_label`, `segment.discovered_description`,
`segment.link_kind`; `geo_asn.address`, `geo_asn.asn`, `geo_asn.operator`,
`geo_asn.country_code`, `geo_asn.lookup_state`. For a period inside the `flow`
retention horizon the same shape can be read directly from
`flow.src_segment_id`, `flow.dst_segment_id`, `flow.traffic_scope`,
`flow.packet_bytes`, `flow.action`, `flow.observed_at` and `flow.dst_address`.

Two honest limits, neither of them a new gap. First, **`opnview` has no source
autonomous system**: unlike Akvorado, which sees both ends of a transit flow,
`opnview` observes a network whose local end is a segment and a device, so the
faithful analogue of an AS-to-AS Sankey is **segment-to-operator**, not
AS-to-AS. Second, setting `left` to `device` for any period longer than the
`flow` retention horizon depends on gap **G4** — the aggregates carry no device
dimension — so a 7 d or 30 d device-to-operator Sankey is unavailable until that
is added, and the widget says so in place rather than silently falling back to
segments. Sources: per-pair volume, `docs/opnsense-api-survey.md`, *Data source
3 — NetFlow / Insight*; the allowed and blocked counts, *Data source 1 — Filter
logs*; segment discovery, *Runtime discovery* (i); operator and country from the
MaxMind GeoLite2 ASN and City databases.

**Existing query** — Adapts `-- screen: Matrix` when `right` is `segment`: that
query already projects exactly the source-segment, destination-segment, volume
and allowed-versus-blocked tuple a Sankey needs, and the only change is that the
result is rendered as ribbons rather than as cells. Adapts `-- screen: Map` when
`right` is `operator` or `country`, which already groups peer volume by ASN,
operator and country per source segment — with the same `LEFT JOIN` correction
the two map widgets require, so that peers with no geolocation answer reach the
"Other" band instead of disappearing.

**Empty state** — *Source unavailable*: *"The firewall log is not reachable, so
no traffic relationships can be drawn. This is not an absence of traffic."* If
the `geo_asn` provider is unavailable while `right` is `operator` or `country`,
*"No ASN database is available, so destinations cannot be named. Switch the
right-hand side to segments, or add a MaxMind licence key."* *Source present but
disabled*: *"Local logging is switched off on the firewall, so there are no
records to relate. Turn it on under System > Settings > Logging; `opnview` never
changes that setting."* *Reachable and no rows*: *"No traffic crossed between a
discovered segment and anywhere else in this period."* In the third case the
discovered segments are still drawn as unconnected nodes down the left, so a
silent network reads as silent rather than as absent.

## Blocked and denied

### Unified blocked feed

**Question** — What was blocked, and **what blocked it** — a DNS advertising
list, a DNS threat list, Suricata, or a firewall rule?

**Shows** — One reverse-chronological feed unifying the blocking decisions of
all four engines, each entry naming the engine, the device or client, the target
(a domain where the engine saw one, an address and port otherwise), and — the
load-bearing column — **which named list, signature or rule produced the
decision**. Entries are grouped and filterable by engine, so "show me only what
the DNS threat lists stopped" is one click. Where an engine cannot name what it
used, that is stated in the row rather than left blank. Required UI copy: the
observation-point limit sentence for the connection counts, and, when Suricata
contributes, the per-segment coverage statement described under *Security alerts
over time*.

**Parameters** — `period`; `engines` (any subset of `firewall_rule`,
`dns_advertising_list`, `dns_threat_list`, `security_engine`); `segments`
(optional); `devices` (optional); `limit`; `group_by` (`none` | `engine` |
`device` | `target`).

**Data** — For the firewall engine: `flow.action`, `flow.observed_at`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`, `flow.protocol`,
`flow.rid`, `flow.rule_id`, `flow.rule_lookup_state`, `flow.src_device_id`,
`flow.src_segment_id`, `flow.interface_device`,
`flow.interface_lookup_state`, and the `blocked_event` view over them;
`rule.description`, `rule.action`, `rule.pf_label`; `interface_map.description`.
For the security engine: `security_event.event_action`,
`security_event.occurred_at`, `security_event.signature`,
`security_event.rule_identity`, `security_event.src_address`,
`security_event.dst_address`, `security_event.src_device_id`,
`security_event.src_segment_id`, `security_event.provider_id`;
`provider_rule_info.normalised_severity`, `provider_rule_info.category`. For the
two DNS engines: `dns_resolution.action`, `dns_resolution.domain`,
`dns_resolution.client_address`, `dns_resolution.device_id`,
`dns_resolution.looked_up_at`, `dns_resolution.resolver`,
`dns_resolution.answer_source`, `dns_resolution.rcode` — and then the gap.
`MISSING FROM MODEL:` **G1 — `dns_resolution` has no `blocklist` column, so
"blocked by which list" is unanswerable today.**
`/api/unbound/overview/search_queries` returns a `blocklist` field in every row
(`docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, response
shape), and the schema discards it. What would have to be added: a `blocklist`
TEXT column on `dns_resolution`, populated verbatim from that field, plus a
`block_list_kind` discriminator — advertising, threat, custom, unknown — because
the endpoint reports the list's name and not its purpose, and the maintainer's
question distinguishes the two. A second marker covers the same widget's other
half: `MISSING FROM MODEL:` **G2 — there is no unified blocking vocabulary
across the four engines.** A block lives in three tables with three different
column names and three different value sets (`flow.action = 'block'`,
`dns_resolution.action IN ('block','drop')`,
`security_event.event_action = 'blocked'`), and nothing in the schema says which
*kind* of engine produced a given decision. What would have to be added: a
`blocked_decision` view unioning the three, projecting a common
`(occurred_at, engine_kind, engine_reference, device_id, segment_id, target,
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
segments its coverage includes and which it does not; see G8.

### Blocked by firewall rule

**Question** — Which firewall rules are actually firing, and against whom?

**Shows** — A ranked list of rules by blocked-connection count, each row naming
the rule's description, its pf label, the interface it fired on, the segments
and devices it blocked, and a small timeline. A `rid` that matches no known rule
is a first-class row labelled **rule no longer exists** — normal, because a rule
can be removed after it logged — and never a missing row. An interface device
name absent from the interface map is labelled likewise. Required UI copy: the
observation-point limit sentence.

**Parameters** — `period`; `segments` (optional); `devices` (optional);
`limit`; `include_automatic` (boolean — whether auto-generated rules are
listed); `sort` (`blocked` | `devices` | `recency`).

**Data** — `flow.action`, `flow.observed_at`, `flow.rid`, `flow.rule_id`,
`flow.rule_lookup_state`, `flow.src_segment_id`, `flow.src_device_id`,
`flow.src_address`, `flow.dst_address`, `flow.dst_port`,
`flow.interface_device`, `flow.interface_lookup_state`, and the
`blocked_event` view; `rule.description`, `rule.pf_label`, `rule.action`,
`rule.direction`, `rule.is_automatic`; `interface_map.description`,
`interface_map.segment_id`. Sources: filter logs,
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

**Question** — Which domains did the resolver refuse, and which list did the
refusing?

**Shows** — A ranked list of blocked domains with the count, the devices that
asked, the segments they sit in, and the name of the blocklist that produced the
decision, with the advertising and threat categories separated. A lookup whose
list cannot be named is carried explicitly as *"blocked, list not recorded"*,
never merged into a generic bucket. Required UI copy: the
site-names-are-inferred sentence does **not** apply here — a resolver lookup is
an *observed* domain rather than an inferred one, and the widget says so
explicitly, because that is the one place in the product where a domain is not a
guess.

**Parameters** — `period`; `categories` (any subset of `advertising`, `threat`,
`custom`, `unknown`); `segments` (optional); `devices` (optional); `limit`;
`min_count`.

**Data** — `dns_resolution.domain`, `dns_resolution.action`,
`dns_resolution.client_address`, `dns_resolution.device_id`,
`dns_resolution.looked_up_at`, `dns_resolution.resolver`,
`dns_resolution.answer_source`, `dns_resolution.rcode`,
`dns_resolution.lookup_uuid`; `device.hostname`, `device.last_address`. The
blocklist name and its category depend on gap G1. Grouping by segment depends
on: `MISSING FROM MODEL:` **G6 — `dns_resolution` carries no segment.** It has
`client_address` and a nullable `device_id`, so a lookup can only be placed in a
segment by joining through `device.segment_id`; a client that has never appeared
in a lease or a flow has no device row, and its lookups are therefore
unplaceable. What would have to be added: a nullable `segment_id` column on
`dns_resolution`, resolved at ingest from the client address against the
discovered segment addressing, plus a `segment_lookup_state` in the same
`resolved` / `not_found` / `pending` vocabulary `flow` already uses — so an
unplaceable lookup is a modelled state rather than a null. Sources: resolver
lookups, `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*.

**Existing query** — Reuses nothing. No query in `sql/queries/screens.sql` reads
`dns_resolution` at all: the only route to that table in the seven is through
`domain_attribution` in `-- screen: Device`, which selects attributed flows
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

**Question** — What did the intrusion-detection engine see, and when?

**Shows** — A timeline of security events over the period, banded by normalised
severity, with the totals brought forward. Every entry names the provider that
contributed it. Severity is taken from the event when the provider ships one and
resolved through the per-provider rule-info cache otherwise; a rule identity
absent from that cache is drawn as an explicit **unknown severity** band rather
than dropped, because a detection whose severity we could not look up is still a
detection. Required UI copy: a per-segment coverage statement — *"Detection
covers these segments: … It does not cover these: … An absence of alerts for an
uncovered segment means nothing."* — which is the `ROADMAP.md` degrade-never-guess
rule made concrete.

**Parameters** — `period`; `severities` (any subset of `critical`, `high`,
`medium`, `low`, `informational`, `unknown`); `segments` (optional); `devices`
(optional); `bucket` (the time granularity); `providers` (optional, by provider
kind and key).

**Data** — `security_event.occurred_at`, `security_event.normalised_severity`,
`security_event.rule_identity`, `security_event.signature`,
`security_event.event_action`, `security_event.src_device_id`,
`security_event.src_segment_id`, `security_event.provider_id`,
`security_event.in_interface_device`; `provider_rule_info.normalised_severity`,
`provider_rule_info.category`, `provider_rule_info.provider_severity`;
`provider.provider_key`, `provider.display_name`. The coverage statement
depends on: `MISSING FROM MODEL:` **G8 — the segments a security-event provider
covers are not modelled.** `/api/ids/settings/get` exposes
`ids.general.interfaces` as an object keyed by interface identifier with a
`selected` flag (`docs/opnsense-api-survey.md`, *Runtime discovery* (v)), and
nothing in the schema stores it: `source_availability` carries a free-text
`detail` and no per-segment relation. What would have to be added: a
`provider_segment_coverage` table keyed on `(provider_id, segment_id)` with an
`is_covered` flag and a `determined_at` timestamp, so a UI statement about which
segments are covered is a query rather than a string parse. Sources: security
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
period on the segments it covers."* All three states additionally carry the
coverage statement, because an alert count of zero means something very different
on a covered segment than on an uncovered one.

### Alerts by signature

**Question** — Which detections are firing, how often, and how serious are they?

**Shows** — A ranked list of rule identities with the signature text, the
normalised severity, the category, the count, the devices and segments
involved, and the provider that contributed them. The raw severity the provider
reported is shown alongside `opnview`'s normalised value, so the normalisation
stays auditable. A rule identity with no cache entry is shown with severity
**unknown** and the reason — the API destroys the nested alert object before
`opnview` can read it, so severity has to be fetched separately and this one has
not been fetched yet. Required UI copy: the per-segment coverage statement.

**Parameters** — `period`; `severities`; `segments` (optional); `devices`
(optional); `limit`; `providers` (optional); `sort` (`count` | `severity` |
`recency`).

**Data** — `security_event.rule_identity`, `security_event.signature`,
`security_event.occurred_at`, `security_event.event_action`,
`security_event.normalised_severity`, `security_event.src_device_id`,
`security_event.src_segment_id`, `security_event.provider_id`;
`provider_rule_info.normalised_severity`, `provider_rule_info.provider_severity`,
`provider_rule_info.category`, `provider_rule_info.rule_source`,
`provider_rule_info.fetched_at`; `provider.provider_key`,
`provider.display_name`; `segment.user_label`,
`segment.discovered_description`; `device.hostname`. Coverage statement depends
on gap G8. Sources: security events, `docs/opnsense-api-survey.md`, *Data source
2 — Suricata `eve.json`*; rule metadata, *Gaps and alternatives*, gap 3.

**Existing query** — Reuses `-- screen: Alerts`, grouped by
`(provider_id, rule_identity)`. The index
`idx_security_event_rule_occurred_at` on exactly that triple exists for this
aggregation.

**Empty state** — *Source unavailable*: *"No security-event source is reachable,
so no signature can be ranked."* *Source present but disabled*: *"Suricata is
installed but not running."* *Reachable and no rows*: *"No signature fired in
this period on the covered segments."* Plus the coverage statement in all three
cases.

### Alerts by device and segment

**Question** — Which machine, and in which segment, is the detection engine
complaining about?

**Shows** — Alerts attributed to a device and a segment rather than to an
address: one row per device with the alert count, the worst severity seen, the
segment, and the top signatures. A source address that could not be resolved to
a device is a first-class row labelled **address not matched to a device**, with
its address shown, because dropping it would under-report. Required UI copy: the
per-segment coverage statement, and a note that only the *source* side of an
alert is currently placed in a segment.

**Parameters** — `period`; `severities`; `segments` (optional); `limit`;
`sort` (`count` | `worst_severity`); `include_unmatched` (boolean, default
true).

**Data** — `security_event.src_device_id`, `security_event.src_segment_id`,
`security_event.src_address`, `security_event.dst_address`,
`security_event.occurred_at`, `security_event.rule_identity`,
`security_event.signature`, `security_event.normalised_severity`;
`provider_rule_info.normalised_severity`, `provider_rule_info.category`;
`device.hostname`, `device.last_address`, `device.unstable_identity`;
`segment.user_label`, `segment.discovered_description`. The destination side is
unavailable: `MISSING FROM MODEL:` **G7 — `security_event` carries no
destination segment or device.** It has `src_device_id` and `src_segment_id` and
no counterpart for `dst_address`, so "which segment was the target" cannot be
asked, and an alert about traffic *into* a segment is placed by its source only.
What would have to be added: `dst_device_id` and `dst_segment_id` columns
mirroring the source pair, resolved at ingest the same way, together with an
index on `(dst_segment_id, occurred_at)` so the inverse aggregation is an index
search. Coverage statement depends on gap G8. Sources: security events,
`docs/opnsense-api-survey.md`, *Data source 2 — Suricata `eve.json`*; device
identity, *Data source 4 — DHCP leases*.

**Existing query** — Reuses `-- screen: Alerts`, grouped by
`src_device_id` and `src_segment_id`. The index
`idx_security_event_device_occurred_at` exists for this aggregation.

**Empty state** — *Source unavailable*: *"No security-event source is reachable,
so no device can be implicated."* *Source present but disabled*: *"Suricata is
installed but not running, so no device has been flagged."* *Reachable and no
rows*: *"No covered device raised an alert in this period."* Plus the coverage
statement, naming the segments where a device could not raise an alert because
nothing is watching it.

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

**Question** — Is the firewall itself healthy right now?

**Shows** — A compact panel of current values with their trend: uptime, CPU
percentage, memory used against total, swap used, disk used per filesystem,
temperature per sensor, and per-gateway latency and loss. Each value carries a
sparkline of its recent history and its own threshold colouring, and each states
when it was last sampled — so a stale panel reads as stale rather than as
current. Required UI copy: **a sampling statement** — *"These are samples taken
every N seconds. A gap in a trend line means `opnview` was not collecting, not
that the value was zero."* The observation-point limit sentence does **not**
apply here and must not be shown: this widget describes the firewall itself
rather than traffic crossing it, and repeating a caveat where it is untrue
teaches the user to ignore it.

**Parameters** — `metrics` (any subset of `uptime`, `cpu`, `memory`, `swap`,
`disk`, `temperature`, `gateway_latency`, `gateway_loss`); `period` (the window
the sparklines cover); `thresholds` (per metric, the warning and critical
values, defaulting to unset — **no default threshold is assumed**, because a
sensible temperature or disk figure depends on the hardware and assuming one
would be a hardcoded assumption about the installation); `layout` (`tiles` |
`rows`); `show_last_sampled` (boolean, default true).

**Data** — `MISSING FROM MODEL:` **G9 — the model holds no system telemetry at
all.** There is no table in `migrations/*.sql` that can store a sampled value:
every existing table holds event records or derived volume, and the four
`volume_aggregate_*` tables sum and count rather than averaging, so they cannot
hold a percentage or a temperature. What would have to be added, per *Why this
data is a different shape* above: a `metric` registry of
`(id, provider_id, metric_key, display_name, unit, value_kind)` where
`value_kind` distinguishes a gauge from a counter; a `metric_sample` table keyed
on `(metric_id, sampled_at)` carrying `value REAL` and a collection-gap marker,
so a missing sample is a modelled state rather than an absent row; and a
roll-up family carrying `min`, `max`, `mean` and `sample_count` per bucket
rather than a sum. A second, independent gap applies to every widget in this
section: `MISSING FROM MODEL:` **G10 — no source of system telemetry has been
surveyed.** `docs/opnsense-api-survey.md` covers five sources and none of them
is telemetry, so the endpoints in the candidate table above have no established
response shape, retention, polling frequency or degradation behaviour. What
would have to be added: a sixth data source in that survey, and a seventh
provider kind in `docs/architecture.md` — today the `provider.kind` CHECK
constrains the column to exactly six values, so registering a telemetry provider
is a migration rather than an INSERT.

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
no logging rule matched. Traffic between two devices inside one segment still
never reaches the firewall and is still invisible."*

**Parameters** — `interfaces` (optional list of segment references; empty means
every discovered interface); `period`; `direction` (`both` | `in` | `out`);
`measure` (`bits_per_second` | `bytes` | `packets` | `errors`); `stacked`
(boolean); `per_interface_axis` (boolean — one shared axis, or one small chart
per interface).

**Data** — Depends on gaps **G9** and **G10**, above. The join back to a named
segment uses columns that do exist: `interface_map.device_name`,
`interface_map.description`, `interface_map.segment_id`;
`segment.user_label`, `segment.discovered_description`,
`segment.interface_identifier`, `segment.device_name`. Candidate endpoints:
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
an optional reference scoping it (a segment, a device, an interface, a
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
`flow.src_segment_id`, `flow.direction`, and the `volume_aggregate_*` family's
`period_start_at`, `bytes`, `allowed_connections` and `blocked_connections`. A
detection series reads `security_event.occurred_at`,
`security_event.normalised_severity` and `security_event.src_segment_id`. A DNS
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

**Question** — Are the pre-computed numbers I am looking at actually current?

**Shows** — One row per pre-computed period — 1 h, 24 h, 7 d, 30 d — with the
slot count, the earliest and latest period covered, the last computation
timestamp and the total bytes held. A period whose slot count is zero, or whose
`computed_at` has fallen behind the refresh contract in `docs/data-model.md`, is
flagged **stale**, and every widget reading that period shows the same flag
inline rather than presenting a stale number as current. Required UI copy: the
observation-point limit sentence, because the byte totals shown here are the same
lower bounds.

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

**Question** — How much of what I am looking at could not be joined to a name?

**Shows** — Four counters with their trends: flows whose interface device name
matched no entry in the interface map; flows whose `rid` matched no known rule;
devices identified only by address within a segment rather than by a DHCP client
identity or a MAC; and addresses whose geolocation lookup is a `miss` or still
`pending`. Each counter links to the records behind it. This widget exists so the
quality of every other widget is visible rather than assumed. Required UI copy: a
statement that an unresolved join is normal — a rule can be deleted after it
logged, an interface can be renamed, a device can appear before its lease is read
— and is a labelled condition rather than a defect.

**Parameters** — `period`; `counters` (any subset of `interface`, `rule`,
`device_identity`, `geo`); `segments` (optional); `show_records` (boolean).

**Data** — `flow.interface_lookup_state`, `flow.rule_lookup_state`,
`flow.interface_device`, `flow.rid`, `flow.observed_at`, `flow.src_segment_id`;
`interface_map.device_name`, `interface_map.description`; `rule.pf_label`;
`device.identity_kind`, `device.identity_key`, `device.mac`,
`device.unstable_identity`; `geo_asn.lookup_state`, `geo_asn.looked_up_at`,
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
| Site used, and by whom | *Sites by device* (primary), *Top sites*, *Device traffic detail*, *Attribution rate per device* | Partial — needs G5 for periods beyond `flow` retention; the attribution rate is exposed unconditionally so the coverage is honest |
| World map of passed **and** blocked traffic | *Passed-traffic world map* and *Blocked-traffic world map* — **two widgets, by the maintainer's decision, not one map with two layers**; plus *Destination countries* and *Destination operators* | Yes for both maps — the aggregates already carry `allowed_connections` and `blocked_connections` separately, and `security_event.dst_address` covers the detection engine. One honest limit, stated in the blocked map rather than hidden: a **DNS**-blocked lookup resolves to no address and therefore cannot appear on any map, so it is carried as a counter beside it |
| Who talks to whom, legibly rather than precisely | *Traffic Sankey* (primary), *Segment traffic matrix* | Yes for segment-to-segment and segment-to-operator; a **device**-to-operator Sankey beyond the `flow` retention horizon needs G4 |
| Blocked content unified across DNS advertising lists, DNS threat lists, Suricata and firewall rules | *Unified blocked feed* (primary), *Blocked DNS lookups*, *Blocked by firewall rule*, *Alerts by signature* | No — needs G1 (which list) and G2 (one vocabulary across the four engines) |
| Traffic by VLAN | *Segment volume ranking* (primary), *Segment traffic matrix*, *Traffic over time by scope* | Yes — segment membership is modelled and never inferred from a name |
| Traffic by MAC or IP | *Device volume ranking* (primary), *Device traffic detail*, *Alerts by device and segment* | Partial — needs G4 for periods beyond `flow` retention |
| Outbound / inbound / inter-VLAN as selectable scopes | *Traffic over time by scope* (primary), *Segment traffic matrix*, *Segment volume ranking* | Partial — inter-VLAN is complete from `flow.traffic_scope`; outbound and inbound need G3 for any pre-computed period |
| **Firewall health and telemetry** — uptime, CPU, RAM, disk, temperature, per-interface bandwidth, latency. *Not one of the six originally named; added by the maintainer after reviewing this cycle's captures.* | *Firewall health overview* (primary), *Interface throughput*, *Custom chart* | **No — and not partially.** The model holds no telemetry (G9) and no telemetry source has been surveyed (G10). This is a new data category rather than a missing column |
| **Arbitrary combinations of series on one chart** — the maintainer's example being temperature and CPU against WAN bandwidth, explicitly as an example rather than a specification | *Custom chart* (primary) | Structurally yes, once G9 and G10 are closed: the widget's `series` parameter is a list, so the combination is authored by the user rather than enumerated here |

## Gaps found

Ten gaps, two of them already known before this cycle and recorded here rather
than presented as discoveries, and eight turned up by mapping the widgets onto
the schema. **Each carries exactly one missing-from-model marker in a Data field
above, so a grep for that marker returns ten and this table has ten rows.** None
is fixed here: this cycle names gaps and a later cycle implements them, which is
what `specs/SPEC-ui-and-dashboard-format-research.md` puts out of scope.

The last two are of a different order from the rest. G1 through G8 are columns
and tables missing from a model that otherwise has the right shape. **G9 and G10
are a whole data category the model was never designed to hold and the step-1
survey never looked at** — firewall health and telemetry — and closing them means
a new table family, a seventh provider kind and a sixth surveyed data source,
not a migration adding columns.

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
| G1 | `dns_resolution` has no `blocklist` column, so "blocked by which list" is unanswerable | **Yes** | Add `blocklist` TEXT, populated verbatim from the endpoint's `blocklist` field, plus a `block_list_kind` discriminator (advertising / threat / custom / unknown), because the endpoint names the list and not its purpose | `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, *Response shape* |
| G2 | No unified blocking vocabulary across the four engines; a block lives in three tables under three column names | **Yes** (as "no query aggregates the unified blocked view"; the mapping shows it is a model gap as well as a query gap) | A `blocked_decision` view over `flow`, `dns_resolution` and `security_event` projecting `(occurred_at, engine_kind, engine_reference, device_id, segment_id, target, target_kind)`, `engine_kind` constrained to the four categories | `docs/opnsense-api-survey.md`, *Data source 1*, *Data source 2*, *Data source 5*; and `sql/queries/screens.sql`, which has no such query |
| G3 | The four volume aggregates carry `traffic_scope` but no `direction`, so outbound and inbound cannot be told apart in any pre-computed period | No | A `direction` column on all four aggregate tables in the `in` / `out` / `unknown` vocabulary `flow.direction` uses, included in each slot's uniqueness index | `docs/opnsense-api-survey.md`, *Data source 1 — Filter logs*, *Response shape* (`dir`) |
| G4 | No per-device volume aggregate; the aggregates are keyed on segments and peer addresses only | No | A `device_volume_aggregate_<period>` family keyed on `(period_start_at, src_device_id)` carrying bytes, allowed, blocked and distinct destinations | `docs/opnsense-api-survey.md`, *Data source 3 — NetFlow / Insight*, *Retention on the firewall* (why `opnview`'s own aggregates are the primary store) |
| G5 | No per-domain volume aggregate; `domain_attribution` is per flow and purged with its parents | No | A `domain_volume_aggregate_<period>` family keyed on `(period_start_at, site_name, src_device_id)` | `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, *Retention on the firewall* (the 7-day ceiling on the firewall side) |
| G6 | `dns_resolution` carries no segment, so a lookup from a client with no device row cannot be placed | No | A nullable `segment_id` plus a `segment_lookup_state` in the `resolved` / `not_found` / `pending` vocabulary | `docs/opnsense-api-survey.md`, *Data source 5 — Resolver DNS lookups*, *Response shape* (`client`); *Runtime discovery* (i) for the addressing to resolve against |
| G7 | `security_event` carries no destination device or segment, so "which segment was the target" is unanswerable | No | `dst_device_id` and `dst_segment_id` mirroring the source pair, with an index on `(dst_segment_id, occurred_at)` | `docs/opnsense-api-survey.md`, *Data source 2 — Suricata `eve.json`*, *Response shape* (`dest_ip`) |
| G8 | The segments a security-event provider covers are not modelled; `source_availability.detail` is free text | No | A `provider_segment_coverage` table keyed on `(provider_id, segment_id)` with `is_covered` and `determined_at` | `docs/opnsense-api-survey.md`, *Runtime discovery* (v) (`ids.general.interfaces`, keys whose `selected` is truthy) |
| G9 | **The model holds no system telemetry at all**; every table holds event records or summed volume, and none can store a sampled gauge such as a percentage or a temperature | No | A `metric` registry of `(id, provider_id, metric_key, display_name, unit, value_kind)`; a `metric_sample` table keyed on `(metric_id, sampled_at)` with a `value REAL` and an explicit collection-gap marker; and a roll-up family carrying `min`, `max`, `mean` and `sample_count` per bucket rather than a sum, on bucket boundaries matching the volume aggregates so the two can share a chart | No OPNsense citation is possible: the survey does not cover this material. The shape requirement is argued in *Why this data is a different shape from everything else in the model**, above |
| G10 | **No source of system telemetry has been surveyed.** `docs/opnsense-api-survey.md` establishes five sources and none of them is telemetry, so the endpoints that would feed G9 have no established response shape, retention, polling frequency or degradation behaviour | No | A sixth data source in the survey, done to the same standard as the five; and a seventh value in the `provider.kind` CHECK, since that column is constrained to exactly six kinds and registering a telemetry provider is therefore a migration rather than an INSERT | Endpoint **candidates** only, cited at https://docs.opnsense.org/development/api/core/diagnostics.html and https://docs.opnsense.org/development/api/core/routes.html; their response shapes are marked `UNVERIFIED:` in *Endpoint candidates, cited rather than asserted*, above |

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
