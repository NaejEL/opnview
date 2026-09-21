# Data model and SQLite schema

Step 2 of `ROADMAP.md`. What each entity represents, which step-1 finding
shaped it, which OPNsense endpoint and API field feed it, how it is identified,
how retention treats it, and which indexes it carries.

The authority on the sources is `docs/opnsense-api-survey.md`, pinned to
OPNsense 26.7.3. Every endpoint named below is cited there, with its URL; the
citation is not repeated here. The authority on the schema is
`migrations/*.sql`, and the authority on the queries is
`sql/queries/screens.sql`. This document explains them; it does not restate
them, so the two cannot drift.

Everything is verified by one command, from the repository root:

```
docker compose run --rm schema-checks
```

## What this schema is not

**Observation-point limit.** Every byte, packet and connection count in this
schema comes from a point on the firewall: the filter log records packets that
crossed a rule with logging on, and NetFlow is captured on the firewall's own
interfaces. Traffic between two devices inside one segment never reaches the
router and appears nowhere in this schema. Every volume figure is therefore a
lower bound, and every screen that displays one must say so.

**Site names are inferred.** Every row of `domain_attribution` is an inference
from resolver correlation: a resolver lookup made by a client shortly before a
flow from that client to the address the lookup returned. There is no second
method on OPNsense 26.7. Suricata exposes no `dns` event type, and its `tls`
and `http` events, although they can be written to `eve.json`, cannot be read
back through the API (survey, gaps 1 and 2). Client-side caching, shared
content networks, DNS-over-TLS and DNS-over-HTTPS each break the correlation,
and a device using an external encrypted resolver can only be named by address,
country and operator.

**Provisional columns.** The survey carries `UNVERIFIED:` markers on the ISC
lease contract, on the grammar of the Unbound and Dnsmasq query-log lines, and
on the eve-log volume estimates. `dhcp_lease.backend = 'isc'` and
`dns_resolution.resolver = 'dnsmasq'` rest on those markers and are provisional
until step 4 confirms them against a live firewall.

## Conventions

**Instants.** Every column holding an instant is an `INTEGER` UTC epoch in
seconds and its name ends in `_at`. A `CHECK` rejects a negative value and any
value beyond 2100-01-01, which is what a millisecond timestamp mistakenly
stored as seconds looks like. The three input shapes the survey documents are
normalised by the collector at the boundary:

| Input shape | Where it comes from | Normalised into |
|---|---|---|
| Unix epoch seconds | resolver `time`, NetFlow `last_seen`, lease `expire` | stored as-is |
| ISO 8601 string | Suricata `timestamp` in `eve.json` | parsed, converted to UTC |
| Year-less, zone-less syslog string | filter-log `__timestamp__` | year inferred from the firewall's current time, read in the same API session; the assumption is recorded |

**Identifiers.** No interface name, VLAN name, segment name, address or CIDR
appears as a literal anywhere in the migrations, their defaults, their
constraints, their indexes, the seven screen queries or the seed. There is no
`LIKE`, `GLOB` or `REGEXP` predicate in the DDL at all, so nothing can classify
a segment, a device or a rule by what it is called.

**Zero assumed counts.** Nothing in the schema or in the queries presumes a
number of segments, devices, interfaces or address families. The checks prove
it by re-running every query against a second seed built with different counts.

## Entities

Exactly these entities exist. Blocked events are a view; everything else is a
table.

<!-- entity-list:begin -->
```
alert
blocked_event
device
dhcp_lease
dns_resolution
domain_attribution
eve_ingest_cursor
flow
geo_asn
ids_rule_info
interface_map
pair_volume_observation
rule
schema_version
segment
setting
source_availability
volume_aggregate_1h
volume_aggregate_24h
volume_aggregate_30d
volume_aggregate_7d
```
<!-- entity-list:end -->

**There is no TLS or HTTP observation entity**, and there will not be one until
a release of OPNsense makes those records readable. The schema carries nothing
no source can fill.

### Growing and bounded

A growing table accumulates one row per observation and is unbounded in time. A
bounded table holds one row per object that exists on the firewall, or per
configuration key, and its size is set by the installation rather than by how
long opnview has been running. The distinction is load-bearing: the query-plan
criterion forbids a full scan of any growing table, and permits one of a
bounded table.

<!-- growing-tables:begin -->
```
alert
device
dhcp_lease
dns_resolution
domain_attribution
flow
geo_asn
pair_volume_observation
volume_aggregate_1h
volume_aggregate_24h
volume_aggregate_30d
volume_aggregate_7d
```
<!-- growing-tables:end -->

<!-- bounded-tables:begin -->
```
eve_ingest_cursor
ids_rule_info
interface_map
rule
schema_version
segment
setting
source_availability
```
<!-- bounded-tables:end -->

Why each bounded table is bounded, and may therefore be scanned:

| Table | Bound |
|---|---|
| `segment` | one row per discovered interface; a firewall has tens, not millions |
| `interface_map` | one row per raw device name the firewall reports |
| `rule` | one row per rule in the running ruleset |
| `ids_rule_info` | one row per signature id seen at least once; a rule set holds tens of thousands at most, and the table only ever holds the ones actually observed |
| `source_availability` | exactly five rows, one per source, for the life of the database |
| `eve_ingest_cursor` | one watermark per rotated `eve.json` file; the firewall keeps the current file plus four archives |
| `setting` | one row per configuration key |
| `schema_version` | one row per applied migration |

`device` is classified as growing, not bounded: an address reissued to another
machine creates a new identity rather than updating an existing one, so the
table accumulates over time even on a network with a fixed number of machines.
It is purged accordingly, under the guard described in its own section, and no
screen query is allowed to scan it.

## Per entity

### `segment` — a named zone, tunnels included

One discovered interface. **Source:** `/api/interfaces/overview/interfaces_info`
(runtime discovery (i)) — `identifier`, `description`, `device`, `link_type`,
`vlan_tag`, `addr4` and `addr6`.

**Identity:** `interface_identifier`, the configuration key, unique. The raw
device name is unique too and is what the filter log reports.

**Manual labelling is separate from discovery.** `discovered_description` holds
what the firewall reports; `user_label` holds the maintainer's own name and is
`NULL` until one is set. Relabelling never overwrites discovery and a refresh
of discovery never overwrites a label; both are returned together by the Matrix
and Alerts queries through `coalesce(user_label, discovered_description)`.

**Tunnels carry their own attribute.** `discovered_link_type` is the raw value
the API reports. `link_kind` is opnview's normalisation of it — `physical`,
`vlan`, `tunnel` or `other` — and `is_tunnel` is a generated column that is 1
exactly when `link_kind` is `tunnel`. Nothing in that chain reads a name: a
segment called anything at all is a tunnel if and only if its discovered link
type says so.

**Retention:** never purged. A segment that disappears from discovery keeps its
row so historical flows stay joinable.

**Indexes:** the primary key, plus the two uniqueness constraints on
`interface_identifier` and `device_name`.

### `interface_map` — join key one

The filter log reports a raw device name, not the user description
(survey, data source 1: "raw device name, **not** the user description").
**Source:** `/api/diagnostics/interface/get_interface_names`, runtime discovery
(i).

**Identity:** `device_name`, the primary key.

**Not found is a state, not a missing row.** A flow whose raw device name is
absent from this table carries `interface_lookup_state = 'not_found'`, and the
Device and Blocked screens return it with that state and a null description.

**Retention:** never purged. **Indexes:** the primary key.

### `rule` — join key two

**Source:** `/api/firewall/filter/search_rule`, runtime discovery (ii) —
`uuid`, `description`, `action`, `direction`, `is_automatic`. For legacy and
auto-generated rules the `uuid` carries the pf label, which is the same token
the filter log exposes as `rid`; the column is therefore named `pf_label` and
`rid` joins to it for both kinds.

**Identity:** `pf_label`, unique.

**Not found is a state.** A `rid` whose rule no longer exists is normal. The
flow keeps the raw `rid`, sets `rule_id` to `NULL` and
`rule_lookup_state = 'not_found'`, and the Blocked screen renders it as an
unknown rule.

**Retention:** never purged. **Indexes:** the primary key and the uniqueness
constraint on `pf_label`.

### `device` — one machine

**Sources:** the lease endpoints of data source 4 —
`/api/kea/leases4/search`, `/api/dnsmasq/leases/search` and the best-effort ISC
plugin endpoint — for `hostname`, `hwaddr` (or `mac` on the legacy plugin),
`client_id`, `duid`, `iaid` and `mac_info`; and, for a machine never seen in a
lease, `/api/diagnostics/firewall/log`, which carries no MAC at all.

**Identity is a cascade**, most stable first, recorded in `identity_kind` with
the value that level produced in `identity_key`. The pair is unique, so two
observations merge only when the same level yields the same value.

| Level | `identity_kind` | `identity_key` | When it applies |
|---|---|---|---|
| 1 | `dhcp_client_id` | the lease `client_id`, `duid` or `iaid` | a lease provides one |
| 2 | `mac` | the normalised MAC | a MAC is known and no client identity is |
| 3 | `address_in_segment` | segment, address and the validity start of that address | neither of the above; the only level available to a machine seen only in flows |

Level 3 carries the validity start deliberately: an address reissued to another
machine after a lease expiry produces a different `identity_key`, so the two
machines stay two rows and never merge into one phantom device.

**Randomised MAC.** `mac_is_randomised` and `unstable_identity` are generated
columns that test the IEEE locally-administered bit: the second hex digit of
the MAC, in `2`, `6`, `a` or `e`. The digits `0`, `4`, `8` and `c` are globally
administered — real burned-in addresses — and are **not** marked unstable. The
roadmap's original wording, "second hex digit even", additionally captured
those four and would have marked genuine devices as unstable identities; the
IEEE rule is what the schema implements. The `mac` column is constrained to
seventeen lowercase characters so the digit test is total. Two randomised
observations with different MACs remain two rows: they are two identity keys.

**Retention:** purged by `last_seen_at`, but only once every observation that
named the device has itself been purged. The purge guards the delete with a
`NOT EXISTS` over `flow`, `alert`, `dhcp_lease` and `dns_resolution`, because
removing an identity a surviving flow still points at would leave that flow
unable to name a machine.

**Indexes:** the primary key, `UNIQUE (identity_kind, identity_key)`, and
`idx_device_segment` for listing a segment's machines.

### `dhcp_lease` — one observed lease generation

**Source:** data source 4, the three lease endpoints. The MAC field name is the
only normalisation needed: `hwaddr` on Kea and Dnsmasq, `mac` on the legacy ISC
plugin.

**Identity:** `(address, starts_at, backend)`, unique — one row per lease
generation, so a reissue is a second row rather than an overwrite.

**Retention:** purged by `observed_at`.

**Indexes:** `idx_dhcp_lease_observed_at` for the purge and for recency,
`idx_dhcp_lease_device` for a device's lease history.

### `flow` — one filter-log record

**Source:** `/api/diagnostics/firewall/log`, data source 1 — `interface`,
`action`, `rid`, `label`, `src`, `dst`, `srcport`, `dstport`, `protoname`,
`protonum`, `length`, `ipversion`, `dir` and `__timestamp__`, deduplicated on
`__digest__`, which is stored as `log_digest`.

**Identity:** `log_digest`, unique. The endpoint echoes back the record
matching the digest a poll supplied, so the uniqueness constraint is what makes
the echo harmless.

**East-west and north-south** are carried by `traffic_scope`, a generated
column that is `east_west` exactly when both `src_segment_id` and
`dst_segment_id` are set, and `north_south` otherwise. It derives from segment
membership and from nothing else — no address, no CIDR, no name, no assumed
addressing plan.

**Retention:** purged by `observed_at`. Purging a flow cascades to its
attribution.

**Indexes:**

| Index | Serves |
|---|---|
| `idx_flow_observed_at` | the Overview and Matrix queries, and any period-wide read |
| `idx_flow_src_segment_observed_at` | the Segment query |
| `idx_flow_src_device_observed_at` | the Device query |
| `idx_flow_blocked_observed_at` | the Blocked query; a partial index on the blocking action only, so it stays small |

### `blocked_event` — a view, not a table

Every blocked record is the same filter-log line as an allowed one with a
different `action`. Making it a second table would mean a second ingestion path
and a second copy of the data. It is therefore a view over `flow`, paired with
the partial index above so that reading it is an index search rather than a
scan of every flow ever recorded.

Code that expects a table will not find one. This is deliberate and is the only
entity in the list that is not a table.

### `dns_resolution` — one resolver lookup

**Source:** `/api/unbound/overview/search_queries`, data source 5 — `client`,
`domain`, `time`, `action`, `source`, `rcode` and `uuid`. The call must be a
JSON-body read-only POST with integer `timeStart` and `timeEnd`: any other call
form silently returns the 1000 most recent records with HTTP 200 and no
diagnostic. For a Dnsmasq resolver there is no structured endpoint at all and
the rows come from parsing the free-text `line` of the generic log endpoint,
whose grammar the survey marks `UNVERIFIED:`.

**Identity:** `lookup_uuid`, unique — the row `uuid` the endpoint returns, which
is what deduplicates a re-requested window.

**Retention:** purged by `looked_up_at`. Purging a lookup cascades to every
attribution inferred from it, because an attribution with no lookup behind it
could not be judged.

**Indexes:** `idx_dns_resolution_looked_up_at` for the purge,
`idx_dns_resolution_client` for the per-client window the collector asks for.

### `domain_attribution` — the site name a flow was given

**Identity:** `flow_id`, the primary key. One flow carries at most one site
name.

**There is no provenance or method column, deliberately.** Its absence is
asserted by the checks against the column list. On 26.7 every attribution is
inferred from resolver correlation and there is no second method to tell it
apart from, so a provenance field would have exactly one possible value and
would state nothing. What the model carries instead is stronger: the lookup
itself, as a mandatory foreign key — an attribution cannot exist without the
resolver lookup it came from, and inserting one fails — and
`correlation_delay_seconds`, the delay between that lookup and the flow, stored
and queryable, so a wide delay can be treated as a weak attribution.

**A flow with no attribution is a first-class case**, not an omission: the
Device query returns it with its address, country and operator and a null site
name.

**Attribution rate.** The per-device rate is the query
`-- diagnostic: Attribution rate per device` in `sql/queries/diagnostics.sql`.
It is executed by the checks, and it is the figure the UI must expose per
device.

**Retention:** removed by the cascade of either parent, and by its own
`attributed_at`.

**Indexes:** the primary key, plus `idx_domain_attribution_resolution` so the
cascade from a purged lookup is an index search.

### `geo_asn` — the MaxMind enrichment of one address

**Source:** the MaxMind GeoLite2 City and ASN databases, the second of the two
outbound calls the project allows. Acquisition, refresh and licence handling
are step 4; only the model is in scope here.

**Identity:** `address`, the primary key. Keyed per address rather than per
prefix: MaxMind answers with a prefix, and SQLite has no natural
longest-prefix join.

**A cache miss is a modelled state.** `lookup_state` is `resolved`, `miss` or
`pending`, and the Map query returns a miss with its state rather than dropping
the volume attached to it. `maxmind_build_at` records the build date of the
database that answered, so a stale enrichment is visible instead of silently
wrong; a `CHECK` makes it mandatory on a resolved row.

**Retention:** purged by `looked_up_at`. **Indexes:** the primary key and
`idx_geo_asn_looked_up_at`.

### `alert` — one Suricata alert

**Source:** `/api/ids/service/query_alerts`, data source 2 — `timestamp`,
`src_ip`, `src_port`, `dest_ip`, `dest_port`, `proto`, `in_iface`, `alert_sid`,
`alert_action`, `fileid`, `filepos`, and the `alert` key, which the backend has
overwritten with the signature text.

**Identity:** `(file_id, file_pos)`, unique. Paging is offset-from-end-of-file
and therefore unstable, so the byte offset inside its file is the only durable
identity; a replayed ingestion inserts nothing new.

**There is no severity column here.** The backend destroys the nested alert
object, so severity and category are not in the record (survey, gap 3). They
come from `ids_rule_info`.

**Retention:** purged by `occurred_at`.

**Indexes:** `idx_alert_occurred_at` for the timeline,
`idx_alert_device_occurred_at` for a device's alerts,
`idx_alert_signature` for the by-signature aggregation.

### `ids_rule_info` — the `get_rule_info` cache

**Source:** `/api/ids/settings/get_rule_info/<sid>`, resolved on a cache-miss
basis and stored, because the alert record cannot carry it.

**Identity:** `signature_id`, the primary key.

**A miss is modelled by absence of a row, and rendered explicitly.** The Alerts
query left-joins this table and returns `severity_state` as `resolved` or
`unknown`. An alert whose signature has never been resolved is returned with
`unknown` and a null severity, never dropped.

**Retention:** never purged; it is a cache of firewall metadata, not an
observation. **Indexes:** the primary key.

### `eve_ingest_cursor` — the durable ingestion watermark

**Source:** `/api/ids/service/query_alerts` (`fileid`, `filepos`) and
`/api/ids/service/get_alert_logs` (`filename`, `size`, `modified`, `sequence`).

**Identity:** `(file_id, byte_offset)`, unique — a duplicate insert fails, which
is what makes a restart unable to double-count.

**Rotation is modelled.** `rotation_state` is `current`, `rotated` or `lost`.
`lost` is the case the survey describes where rotation discarded the file the
watermark referred to: the gap is permanent and is recorded as such rather than
silently reset.

**Retention:** never purged. **Indexes:** the primary key and the uniqueness
constraint.

### `source_availability` — the health of each source

Exactly five rows, created by migration 0002 in the `unavailable` state with
the probe recorded as not yet run, because an absent row is something a screen
could not tell apart from a healthy source. No source can be distinguished from
silence by an empty result alone (survey, gap 11), so each row carries its
state, the instant it was determined and the probe that determined it. A
`CHECK` rejects any state outside `reachable`, `present_but_disabled` and
`unavailable`.

Per source, the probe and what an unavailable answer means. In every case the
condition is stored as an **availability state** and rendered as its own
condition; it is never rendered as an absence of data:

| `source` | Probe | What is recorded instead of "no data" |
|---|---|---|
| `filter_log` | `/api/diagnostics/firewall/log` | an empty or non-array body, 401 or 403 is `unavailable`; a healthy empty array is `reachable` and surfaced as "reachable but silent" |
| `suricata_eve` | `/api/ids/service/status` | 404, 401 or 403 is `unavailable`; a `stopped`, `disabled` or `unknown` status is `present_but_disabled` |
| `netflow_insight` | `/api/diagnostics/netflow/is_enabled`, then `get_metadata` | `local == 0` is `present_but_disabled`; a zero `last_sync` is `present_but_disabled` with the detail saying it is not yet aggregating |
| `dhcp_leases` | `/api/kea/service/status`, `/api/dnsmasq/service/status` | no backend running is `unavailable`, and devices fall back to being named by address |
| `resolver_dns` | `/api/unbound/overview/is_enabled` | reporting off is `present_but_disabled`; an unreachable resolver is `unavailable`; a window the endpoint did not honour is a collection fault, also `unavailable`, never "this client made no queries" |

**Retention:** never purged. **Indexes:** the primary key.

### `pair_volume_observation` — daily per-pair volume from Insight

**Source:** `/api/diagnostics/networkinsight/top/FlowSourceAddrDetails/...`
with a field list containing both `src_addr` and `dst_addr`, data source 3. The
row carries the requested key fields plus `total` — the octets or packets
measure — and `last_seen`.

**The de-duplication key is explicit, and this is the point of the entity.**
Insight writes each flow once per interface **and** once per direction, with
source and destination swapped on the outbound one, so naive summation
double-counts every volume. The unique key is therefore direction-free and
interface-free:

```
(day_start_at, endpoint_low, endpoint_high, service_port, protocol)
```

where `endpoint_low` and `endpoint_high` are the two addresses in lexicographic
order, enforced by a `CHECK`. Collectors insert with `ON CONFLICT DO NOTHING`;
the interface and direction of the observation that won are kept for provenance
only and are not part of the key. Step 4 must use this key, or volumes double.

Two further consequences of the survey, recorded so the columns are not
over-read: `service_port` is `min(src_port, dst_port)` as Insight computes it,
a heuristic rather than the real destination port — the filter log carries the
exact ports — and `octets` and `packets` are `REAL` because Insight pro-rates
flows that span a slice boundary.

**Retention:** purged by `day_start_at`. **Indexes:** the uniqueness
constraint, plus `idx_pair_volume_day`.

### `volume_aggregate_1h`, `_24h`, `_7d`, `_30d` — the primary volume store

Four materialised tables sharing one shape, one row per period slot per
(source segment, peer) pair.

**They are opnview's primary store, not a cache over Insight**, and the reason
is survey data source 3: per-address-pair volume exists on the firewall only at
**daily** resolution and only for **62 days**, and per-source totals at 300 s
resolution are kept **one hour**. The 1 h and 24 h periods the roadmap wants
cannot be served from Insight at all. All four tables are therefore computed
from opnview's own stored flows; no column assumes Insight supplies sub-daily
per-pair data.

They are tables rather than views for a second reason: a view over a growing
base table cannot satisfy the query-plan criterion, because every read of it
would rescan `flow`.

**Identity:** the slot, enforced by a unique index over
`(period_start_at, src_segment_id, ifnull(dst_segment_id, -1), ifnull(peer_address, ''))`.
The `ifnull` wrappers are necessary because SQLite treats NULLs in a unique
index as distinct, and both columns are NULL-bearing by design: a north-south
slot has no destination segment, and an east-west slot has no peer address.

**Refresh contract, which step 4 must honour.**

1. A period slot is recomputed when a flow is ingested whose `observed_at`
   falls inside it. The recomputation replaces the slot, keyed as above.
2. The current slot of each period — the one containing the present instant —
   is recomputed on every aggregation pass, because it is still filling.
3. `computed_at` is the freshness timestamp every row carries. A slot is stale
   when `computed_at` is older than the newest `ingested_at` among the flows
   inside its window. That comparison is the recognition rule; no separate
   dirty flag exists.
4. A closed slot is never recomputed once no flow inside its window has been
   ingested since its `computed_at`. Back-filled history therefore reopens only
   the slots it touches.
5. The 7 d and 30 d slots may lean on the firewall's daily per-pair aggregate
   for history predating opnview's own; the 1 h and 24 h slots may not, and
   must show their true coverage rather than a zero.

**Aggregate mode.** The roadmap's aggregate mode — volumes, segments, countries
and operators without domain names — is the Map query,
`-- screen: Map` in `sql/queries/screens.sql`. It reads
`volume_aggregate_24h`, `geo_asn` and `segment`, and no table that holds a
domain name. The checks assert that by inspecting the tables it references.

**Retention:** purged by `period_end_at`, so a slot survives as long as its
window lies inside the horizon. Every period remains queryable after a purge;
only slots entirely older than the horizon are removed.

**Indexes:** the unique slot index per table, whose leading column is
`period_start_at`, which is also what makes a period read an index search.

### `setting` — configuration

**Identity:** `key`, the primary key.

**Retention is configuration, not a constant.** `retention_seconds` holds the
purge horizon in seconds, so it is as easy to set to a few hours as to months.
It defaults to `7776000` — 90 days — and `0` means unlimited, which purges
nothing. No retention duration appears as a literal in any query, in any index,
or in `sql/purge.sql`: every statement reads the horizon from this row through
a scalar subquery that yields `NULL` when the horizon is unlimited, so every
comparison is `NULL` and nothing is deleted.

`aggregate_mode` is `full` or `no_domains`.

**Retention:** never purged. **Indexes:** the primary key.

### `schema_version` — the applied migrations

One row per applied migration file, inserted by the migration itself. The
runner skips a file already recorded, which is what makes re-applying the
migrations a no-op. The checks assert that the rows and the files in
`migrations/` match in both directions.

## Purge

`sql/purge.sql` is the documented purge. It takes `:now`, reads the horizon
from `setting`, and removes rows older than it from every growing table:
`flow`, `dns_resolution`, `domain_attribution`, `alert`, `dhcp_lease`,
`device`, `pair_volume_observation`, `geo_asn` and the four aggregates.
Bounded tables
are never purged, so a surviving observation always joins to a segment, a
device, a rule and an interface.

Afterwards, `PRAGMA foreign_key_check` returns no rows: `domain_attribution`
is removed by the `ON DELETE CASCADE` of both its parents, and nothing else
points at a purgeable row.

With `retention_seconds = 0` the purge removes nothing at all. Both directions
are asserted by row counts before and after.

## The seven screen queries

All seven live in `sql/queries/screens.sql`, each labelled with its screen
name, each executable as written, each taking `:window_start` and `:window_end`
and — for two of them — `:segment_id` or `:device_id`. None of them contains an
address, a CIDR, an interface name, a VLAN name or a segment name, and none
assumes a count of anything.

| Screen | What it answers | Load-bearing index |
|---|---|---|
| Overview | totals for the period and the east-west versus north-south split, classified from segment membership alone | `idx_flow_observed_at` |
| Matrix | source segment by destination segment: volume, allowed connections, blocked connections, the matching rules, and how many connections matched no known rule | `idx_flow_observed_at` |
| Segment | the devices of one segment, their volume, their denials, their distinct destinations | `idx_flow_src_segment_observed_at` |
| Device | one device's flows with destination, country, operator and the inferred site name, returning unattributed flows rather than hiding them | `idx_flow_src_device_observed_at` |
| Blocked | the blocked timeline with the rule and the interface, each carrying an explicit unknown state when the join key resolves to nothing | `idx_flow_blocked_observed_at`, the partial index |
| Alerts | alerts joined to device and segment, with severity from the `get_rule_info` cache and an explicit unknown state on a cache miss | `idx_alert_occurred_at` |
| Map | destinations by country and operator per source segment, read from the 24 h aggregate; also the aggregate-mode query, reading no domain name | `uq_volume_aggregate_24h_slot` |

Every plan is recorded verbatim by the checks. None of the seven scans a
growing table; the scans that do appear are of bounded tables, which is
permitted, and each such table's bound is given in the table above. Dropping a
load-bearing index is shown to turn its query's plan into a scan, so a good
plan is demonstrably the index rather than a coincidence of data size. The
demonstration is run on a throwaway copy of the seeded database, on the
Overview, Alerts and Map queries, dropping `idx_flow_observed_at`,
`idx_alert_occurred_at` and `uq_volume_aggregate_24h_slot` in turn.

One honest qualification. The Matrix query is not used for that demonstration,
because dropping `idx_flow_observed_at` does not turn its plan into a literal
scan: its `src_segment_id IS NOT NULL` clause lets SQLite fall back to a full
range of `idx_flow_src_segment_observed_at`, which is a whole-table read
wearing the word `SEARCH`. It shares its load-bearing index with the Overview
query, and that is where the drop is demonstrated.

## Verification

`sql/schema-checks.sh` applies the migrations to a fresh database under
`/data`, seeds it, runs every query and every plan, and asserts each acceptance
criterion of `specs/SPEC-data-model-sqlite-schema.md`. It exits non-zero on any
failure.

`sql/seed.sql` is deterministic, takes its row counts as parameters, and
produces at least 100 000 rows in `flow`. It contains no address literal: every
address is synthesised from a counter at generation time and carries no
addressing-plan meaning, because nothing in the schema or in the queries
interprets an address.

Databases live in the `data` named volume mounted at `/data`, outside the
bind-mounted working tree. SQLite in WAL mode on a Windows bind mount locks
pathologically, and a `.db`, `-wal` or `-shm` file must never land in the
repository.
