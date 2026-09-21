# Data model and SQLite schema

Step 2 of `ROADMAP.md`. What each entity represents, which step-1 finding
shaped it, which OPNsense endpoint and API field feed it, how it is identified,
how retention treats it, and which indexes it carries.

The authority on the sources is `docs/opnsense-api-survey.md`, pinned to
OPNsense 26.7.3. Every endpoint named below is cited there, with its URL; the
citation is not repeated here. The authority on the schema is
`migrations/*.sql`, and the authority on the queries is
`sql/queries/screens.sql`. This document explains them; it does not restate
them, so the two cannot drift. `docs/architecture.md` names the six provider
kinds and the seam a provider plugs into; this document is where each entity
is written down.

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
blocked_event
device
dhcp_lease
dns_resolution
domain_attribution
eve_ingest_cursor
flow
geo_asn
interface_map
pair_volume_observation
provider
provider_rule_info
rule
schema_version
security_event
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
device
dhcp_lease
dns_resolution
domain_attribution
flow
geo_asn
pair_volume_observation
security_event
volume_aggregate_1h
volume_aggregate_24h
volume_aggregate_30d
volume_aggregate_7d
```
<!-- growing-tables:end -->

<!-- bounded-tables:begin -->
```
eve_ingest_cursor
interface_map
provider
provider_rule_info
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
| `provider` | one row per implementation the project knows of; nine today, and a new one is an `INSERT`, not a stream |
| `provider_rule_info` | one row per (provider, rule identity) seen at least once; a rule set holds tens of thousands at most, and the table only ever holds the ones actually observed |
| `source_availability` | exactly one row per `provider` row, for the life of the database |
| `eve_ingest_cursor` | one watermark per rotated `eve.json` file; the firewall keeps the current file plus four archives |
| `setting` | one row per configuration key |
| `schema_version` | one row per applied migration |

`device` is classified as growing, not bounded: an address reissued to another
machine creates a new identity rather than updating an existing one, so the
table accumulates over time even on a network with a fixed number of machines.
It is purged accordingly, under the guard described in its own section, and no
screen query is allowed to scan it.

## Providers

`docs/architecture.md` holds the seam; this section holds the data. A
**kind** is a contract `opnview` implements, a **provider** is one
implementation of it, and the registry holds one row per implementation, not
one per kind.

Registering a provider is an `INSERT` into `provider` plus an `INSERT` into
`source_availability`. Nothing in the DDL enumerates provider names: the only
`CHECK` on the registry constrains `kind`, because a kind is code `opnview`
ships and not data a deployment supplies.

The nine rows migration 0002 seeds, with the survey section that established
each one:

| Kind | `provider_key` | Endpoint | Survey |
|---|---|---|---|
| `firewall_log` | `pf` | `/api/diagnostics/firewall/log` | data source 1 |
| `security_event` | `suricata` | `/api/ids/service/query_alerts` | data source 2 |
| `flow_volume` | `insight` | `/api/diagnostics/networkinsight/top/FlowSourceAddrDetails/...` | data source 3 |
| `dhcp_lease` | `kea` | `/api/kea/leases4/search` | data source 4 |
| `dhcp_lease` | `dnsmasq` | `/api/dnsmasq/leases/search` | data source 4 |
| `dhcp_lease` | `isc` | `/api/dhcpv4/leases/searchLease` | data source 4, `UNVERIFIED:` |
| `dns_lookup` | `unbound` | `/api/unbound/overview/search_queries` | data source 5 |
| `dns_lookup` | `dnsmasq` | `/api/diagnostics/log/core/dnsmasq` | data source 5, `UNVERIFIED:` |
| `geo_asn` | `maxmind_geolite2` | the GeoLite2 City and ASN databases | the second of the two outbound calls the project allows |

**Two kinds have several providers**, and that is what makes the registry real
rather than notional: `dns_lookup` has Unbound and Dnsmasq, `dhcp_lease` has
Kea, Dnsmasq and ISC dhcpd. Step-1 detection already tells them apart, and
`dhcp_lease.backend` and `dns_resolution.resolver` already record which one a
row came from.

**These names are data, not configuration.** A firewall with no Suricata still
gets a Suricata registry row, in the `unavailable` state. That is the
modelled-state design working as intended, and it is recorded here so it is not
mistaken for a hardcoding defect: nothing in the DDL, in a screen query, in an
index or in the purge tests any of these strings.

**No provider name is an identifier.** Not a table name, not a column name, no
exception. `eve_ingest_cursor` names a file format rather than a product and is
itself provisional; it is rewritten when a second ingesting provider is
surveyed.

### The attribute rule

For provider-specific data that does not fit the neutral core, in this order:

1. **A typed, indexed column** for anything a screen **aggregates or filters
   on**. Types and constraints are what make a predicate correct, and index
   selectivity is what makes it fast.
2. **A JSON column**, read only on a detail screen and never aggregated,
   filtered or joined, for an attribute that is only ever displayed.
3. **Never an entity-attribute-value table.** A table of
   `(entity, attribute name, value)` triples turns one row read into N reads
   plus a pivot, loses every type and every constraint, and destroys index
   selectivity on exactly the predicates the screens use. It would make these
   queries slower, not faster, and there is none in this schema.
4. **Never a provider-named table.**

### Severity: one ordered vocabulary

`opnview`'s normalised severity is ordered text, most severe first:

```
critical > high > medium > low > informational
```

Text rather than a number, and `opnview`'s own rather than one vendor's scale
promoted to universal. Both `security_event.normalised_severity` and
`provider_rule_info.normalised_severity` are constrained to it; a value outside
it is rejected by a `CHECK`.

The mapping from the Suricata numeric scale, which runs 1 (most severe) to 4:

| Suricata `severity` | `normalised_severity` |
|---|---|
| 1 | `critical` |
| 2 | `high` |
| 3 | `medium` |
| 4 | `low` |

`informational` has no Suricata equivalent. It is in the vocabulary for a
provider that reports one, and `provider_rule_info.provider_severity` keeps the
raw value the provider reported so the normalisation stays auditable. The
vocabulary's adequacy for a second provider is unproven until one is surveyed.

## Per entity

### `provider` — the registry

One row per implementation that can feed `opnview`, keyed
`(kind, provider_key)`. `kind` is constrained to the six above.

**`is_active` is not reachability.** It says which implementation `opnview`
actually reads for that kind; `source_availability.state` says which ones can
be reached. A machine may have two reachable implementations of one kind. At
most one provider per kind is active, enforced by the partial unique index
`uq_provider_active_per_kind` over `kind` where `is_active = 1`; marking a
second one active fails.

On a freshly migrated database **no provider is active**. Activeness is
determined by step-4 detection against a live firewall. Provider selection
policy — how step 4 chooses — is not modelled here.

**Retention:** never purged. **Indexes:** the primary key, the uniqueness
constraint on `(kind, provider_key)`, and the partial unique index above.

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
`NOT EXISTS` over `flow`, `security_event`, `dhcp_lease` and `dns_resolution`,
because removing an identity a surviving flow still points at would leave that
flow unable to name a machine.

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

**East-west and north-south** are carried by `traffic_scope`, which is
`east_west` exactly when both `src_segment_id` and `dst_segment_id` are set,
and `north_south` otherwise. It derives from segment membership and from
nothing else — no address, no CIDR, no name, no assumed addressing plan. The
collector writes it on every insert; the paragraph below says why it is a plain
column rather than a generated one.

**Retention:** purged by `observed_at`. Purging a flow cascades to its
attribution.

**`traffic_scope` is a plain column pinned by a `CHECK`**, not a generated
column. It is `NOT NULL` with no default, so step 4 must write it on every
insert — an insert that omits it fails on the `NOT NULL` constraint before any
other constraint is reached. The `CHECK` is exactly the expression that would have generated it, so
no row can carry a value disagreeing with its segments — the checks demonstrate
that with a failing insert. It is not generated for one reason only: SQLite
never reports an index as covering for a query that reads a generated column,
virtual or stored, and the Overview, Matrix and Segment queries all group by
this value. Making it generated would cost all three their covering plans.

**Indexes:**

| Index | Serves | Covering |
|---|---|---|
| `idx_flow_observed_at` | the Overview and Matrix queries, and any period-wide read | yes, for both |
| `idx_flow_src_segment_observed_at` | the Segment query | yes |
| `idx_flow_src_device_observed_at` | the Device query | no |
| `idx_flow_blocked_observed_at` | the Blocked query; a partial index on the blocking action only, so it stays small | no |

There is exactly **one** index leading on `observed_at` alone, deliberately: a
second one with the same leading column would hide its loss, and the checks
prove the index is load-bearing by dropping it.

#### Column review

Every column of `flow`, and what reads it. A column nothing reads and nothing
explains does not belong here.

<!-- flow-columns:begin -->
```
action
direction
dst_address
dst_device_id
dst_port
dst_segment_id
id
ingested_at
interface_device
interface_lookup_state
ip_version
log_digest
observed_at
packet_bytes
protocol
rid
rule_id
rule_lookup_state
src_address
src_device_id
src_port
src_segment_id
traffic_scope
```
<!-- flow-columns:end -->

| Column | Read by |
|---|---|
| `id` | the Device query; the `domain_attribution` foreign key |
| `log_digest` | nothing, and it stays: it is the **identity** of the row. The endpoint echoes back the record matching the digest a poll supplied, and this uniqueness constraint is what makes that echo harmless instead of a duplicate |
| `observed_at` | all five flow-fed screen queries, the purge, the aggregate refresh |
| `ingested_at` | the **aggregate refresh**: a slot is stale when its `computed_at` is older than the newest `ingested_at` among the flows in its window. That comparison is the staleness rule, and no separate dirty flag exists |
| `interface_device`, `interface_lookup_state` | the Device and Blocked queries |
| `src_segment_id`, `dst_segment_id` | Overview, Matrix, Segment, Blocked; the `traffic_scope` `CHECK` |
| `src_device_id` | Overview, Segment, Device, Blocked; the purge's device guard |
| `dst_device_id` | the **purge**: a device is removed only when no surviving flow names it as source **or destination** |
| `src_address` | the Blocked query |
| `dst_address` | the Segment, Device and Blocked queries; the `geo_asn` join |
| `src_port` | nothing, and it stays: it is the other half of the connection tuple, and it is what lets a flow be matched back to a `pair_volume_observation`, whose `service_port` is Insight's `min(src_port, dst_port)`. Without it that correlation is impossible |
| `dst_port` | the Device and Blocked queries |
| `protocol` | the Device and Blocked queries |
| `ip_version` | nothing, and it stays: it is the address family the filter log reports. An address column alone cannot be classified, and the project forbids inferring an addressing plan, so a v4/v6 split is answerable only from this column. IPv6 seed coverage is recorded against step 4 |
| `action` | Overview, Matrix, Segment, Device; the `blocked_event` view and its partial index |
| `direction` | nothing, and it stays: it is the in/out sense the filter log reports, and it is what tells a reader whether `src_address` on a blocked record is the machine inside or the machine outside. Reading a denial without it is guesswork |
| `packet_bytes` | Overview, Matrix, Segment, Device; the aggregate refresh |
| `rid` | the Blocked query alone, which returns the firewall's own rule identifier next to the resolved description. No other screen query reads it: the segment-pair screen joins through `rule_id` and reports `rule_lookup_state` instead |
| `rule_id`, `rule_lookup_state` | the Matrix and Blocked queries |
| `traffic_scope` | Overview, Matrix, Segment, Device, Blocked; the aggregate refresh |

Four columns — `log_digest`, `src_port`, `ip_version` and `direction` — are
read by no screen query, by no purge statement and by no aggregate refresh.
Each is justified above, and each is kept deliberately.

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

### `geo_asn` — the geo and ASN enrichment of one address

**Source:** the active provider of the `geo_asn` kind — today the MaxMind
GeoLite2 City and ASN databases, the second of the two outbound calls the
project allows. Acquisition, refresh and licence handling are step 4; only the
model is in scope here.

**Identity:** `address`, the primary key. Keyed per address rather than per
prefix: the dataset answers with a prefix, and SQLite has no natural
longest-prefix join.

**A cache miss is a modelled state.** `lookup_state` is `resolved`, `miss` or
`pending`, and the Map query returns a miss with its state rather than dropping
the volume attached to it. `dataset_build_at` records the build date of the
dataset that answered and `provider_id` names the provider that supplied it, so
a stale enrichment is visible instead of silently wrong, and attributable; a
`CHECK` makes both mandatory on a resolved row, and a resolved row missing
either is rejected.

**Two different questions, two different columns.** The `geo_asn` provider's
`source_availability` row says whether the **dataset** can be reached at all —
present, absent, never downloaded. `geo_asn.lookup_state` says what happened to
**one address** against it. A reachable dataset still produces misses, and an
unreachable one produces none of either.

**Retention:** purged by `looked_up_at`. **Indexes:** the primary key and
`idx_geo_asn_looked_up_at`.

### `security_event` — one event from a security-event provider

**Source:** the active provider of the `security_event` kind. Today that is
Suricata, through `/api/ids/service/query_alerts`, data source 2 — `timestamp`,
`src_ip`, `src_port`, `dest_ip`, `dest_port`, `proto`, `in_iface`, `alert_sid`,
`alert_action`, `fileid`, `filepos`, and the `alert` key, which the backend has
overwritten with the signature text. The table describes the kind, not the
product.

**Identity:** `(provider_id, provider_event_key)`, unique. `provider_event_key`
is the key the provider itself guarantees stable, composed by the collector —
for Suricata, from the eve file id and the byte offset inside it, because
paging is offset-from-end-of-file and therefore unstable. Replaying an
ingestion inserts nothing new.

**There is no ingestion-transport column here**, and that is the point of the
restructuring. The file id and byte offset are an ingestion coordinate, not
event data, and they already live in `eve_ingest_cursor`.

**Two guarantees about two different things, and step 4 must not merge them.**

| | `security_event` | `eve_ingest_cursor` |
|---|---|---|
| Unique key | `(provider_id, provider_event_key)` | `(file_id, byte_offset)` |
| Question it answers | have I already stored **this event**? | where did the reader **stop** in this file? |
| Lifetime | purged with the event, by `occurred_at` | never purged; it is a watermark |
| Scope | every provider of the kind | one provider's file format |

Collapsing them would lose one: a watermark says nothing about which events it
covered, and an event key says nothing about where to resume. `eve_ingest_cursor`
additionally models rotation, which no event key can express.

**The rule identity is text**, on this table and on the cache, so a provider
whose rules are named rather than numbered fits without a migration. Step 6's
by-signature aggregation will therefore compare strings rather than integers;
that is noted, not solved here.

**Severity is nullable and is NULL for every Suricata event.** The backend
destroys the nested alert object, so severity and category are not in the
record (survey, gap 3); they are resolved through `provider_rule_info`, exactly
as before. The column exists for a provider that ships severity inside its
event. The vocabulary is above, under *Severity: one ordered vocabulary*.

**Retention:** purged by `occurred_at`. There is no detail table to cascade to.

**Indexes:** `idx_security_event_occurred_at` for the timeline and for the
Alerts query, `idx_security_event_device_occurred_at` for a device's events,
`idx_security_event_rule_occurred_at` for the per-provider by-rule aggregation.

### `provider_rule_info` — the rule-info cache, per provider

**Source:** `/api/ids/settings/get_rule_info/<sid>` for the Suricata provider,
resolved on a cache-miss basis and stored, because the event record cannot
carry it.

**Identity:** `(provider_id, rule_identity)`, the primary key. Keyed per
provider because two providers may name a rule identically and mean different
rules; the identity alone is not a key.

`normalised_severity` is `opnview`'s vocabulary; `provider_severity` keeps the
raw value the provider reported, verbatim.

**A miss is modelled by absence of a row, and rendered explicitly.** The Alerts
query left-joins this table on `(provider_id, rule_identity)` and returns
`severity_state` as `resolved` or `unknown`. An event whose rule identity has
never been resolved, and which carries no inline severity, is returned with
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

### `source_availability` — the health of each registered provider

Exactly one row per `provider` row, created by migration 0002 in the
`unavailable` state with the probe recorded as not yet run, because an absent
row is something a screen could not tell apart from a healthy provider. No
source can be distinguished from silence by an empty result alone (survey, gap
11), so each row carries its state, the instant it was determined and the probe
that determined it. A `CHECK` rejects any state outside `reachable`,
`present_but_disabled` and `unavailable`, and the primary key is a foreign key,
so an availability row for a provider that does not exist cannot be inserted.

There is no `CHECK` enumerating provider names here, and that is the change
this cycle makes: registering a provider is an `INSERT` into two tables, never
a migration.

Per kind, the probe and what an unavailable answer means. In every case the
condition is stored as an **availability state** and rendered as its own
condition; it is never rendered as an absence of data:

| Kind | Probe | What is recorded instead of "no data" |
|---|---|---|
| `firewall_log` | `/api/diagnostics/firewall/log` | an empty or non-array body, 401 or 403 is `unavailable`; a healthy empty array is `reachable` and surfaced as "reachable but silent" |
| `security_event` | `/api/ids/service/status` | 404, 401 or 403 is `unavailable`; a `stopped`, `disabled` or `unknown` status is `present_but_disabled` |
| `flow_volume` | `/api/diagnostics/netflow/is_enabled`, then `get_metadata` | `local == 0` is `present_but_disabled`; a zero `last_sync` is `present_but_disabled` with the detail saying it is not yet aggregating |
| `dhcp_lease` | `/api/kea/service/status`, `/api/dnsmasq/service/status`, `/api/dhcpv4/service/status` | a backend that is not running is `unavailable`; when no backend of the kind runs, devices fall back to being named by address |
| `dns_lookup` | `/api/unbound/overview/is_enabled`, `/api/dnsmasq/settings/get` | reporting or query logging off is `present_but_disabled`; an unreachable resolver is `unavailable`; a window the endpoint did not honour is a collection fault, also `unavailable`, never "this client made no queries" |
| `geo_asn` | the local dataset file | never downloaded, or a download that failed, is `unavailable`; the row describes the **dataset**, while `geo_asn.lookup_state` describes a single address lookup |

Availability is reachability. Which provider `opnview` reads is
`provider.is_active`, and the diagnostic query
`-- diagnostic: Source availability` in `sql/queries/diagnostics.sql` returns
both, one row per registered provider.

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
`flow`, `dns_resolution`, `domain_attribution`, `security_event`, `dhcp_lease`,
`device`, `pair_volume_observation`, `geo_asn` and the four aggregates.
Bounded tables are never purged, so a surviving observation always joins to a
segment, a device, a rule and an interface — and, for a security event, to the
provider that contributed it and to the rule-info entry that gives it a
severity. `provider`, `provider_rule_info` and `source_availability` are
bounded by the installation, not by time, and are never purged.

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
| Alerts | security events joined to provider, device and segment, with severity from the per-provider rule-info cache and an explicit unknown state on a cache miss | `idx_security_event_occurred_at` |
| Map | destinations by country and operator per source segment, read from the 24 h aggregate; also the aggregate-mode query, reading no domain name | `uq_volume_aggregate_24h_slot` |

### Covering indexes

Column count is close to irrelevant to SQLite read speed. What matters is
index selectivity, and whether the index answers the query without fetching the
row. Where a screen query **aggregates a measure over an indexed range**, the
measure belongs in the index, so the plan reads the index alone and `EXPLAIN
QUERY PLAN` prints `USING COVERING INDEX`.

Three of the seven are covered, and the checks assert exactly these three:

<!-- covering-queries:begin -->
```
Overview idx_flow_observed_at
Matrix idx_flow_observed_at
Segment idx_flow_src_segment_observed_at
```
<!-- covering-queries:end -->

| Query | Index | Measures added for coverage |
|---|---|---|
| Overview | `idx_flow_observed_at` | `traffic_scope`, `action`, `packet_bytes`, `src_segment_id`, `dst_segment_id`, `src_device_id` |
| Matrix | `idx_flow_observed_at` | the same, plus `rule_id` and `rule_lookup_state` |
| Segment | `idx_flow_src_segment_observed_at` | `src_device_id`, `packet_bytes`, `action`, `traffic_scope`, `dst_address` |

The other four are **not** covered, and are not claimed to be:

- **Device**, **Blocked** and **Alerts** are detail listings, not aggregations.
  A listing returns most of the row anyway, so a covering index would have to
  hold most of the table and would buy nothing.
- **Map** aggregates over `volume_aggregate_24h`, whose load-bearing index is
  `uq_volume_aggregate_24h_slot` — a **unique** index enforcing slot identity.
  Adding the measures to it would widen the uniqueness key and destroy the
  guarantee it exists for, and a second index leading on `period_start_at`
  would hide the loss of the first. The trade is refused.

Covering indexes are not free: widening an index makes every insert into that
table more expensive, and step 4's collectors write continuously. The scale run
below reports query time, not ingest time, and that gap is deliberate —
measuring ingest needs a collector, which does not exist yet.

### Scale

The 100 000-row baseline is a test size, not a production one. The checks
therefore run one **scale check** at 1 000 000 rows in `flow`, the largest
growing table, re-run the seven queries, record the seven plans verbatim
alongside the baseline ones, and report each query's wall-clock time. A plan
that changes shape at scale — a new `SCAN` of a growing table, or a covered
query that stops being covered — fails.

Every plan is recorded verbatim by the checks. None of the seven scans a
growing table; the scans that do appear are of bounded tables, which is
permitted, and each such table's bound is given in the table above. Dropping a
load-bearing index is shown to turn its query's plan into a scan, so a good
plan is demonstrably the index rather than a coincidence of data size. The
demonstration is run on a throwaway copy of the seeded database, on the
Overview, Alerts and Map queries, dropping `idx_flow_observed_at`,
`idx_security_event_occurred_at` and `uq_volume_aggregate_24h_slot` in turn.

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
