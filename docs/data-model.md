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
interfaces. Traffic between two clients behind one interface never reaches the
router and appears nowhere in this schema. Every volume figure is therefore a
lower bound, and every screen that displays one must say so.

**Site names are inferred.** Every row of `domain_attribution` is an inference
from resolver correlation: a resolver lookup made by a client shortly before a
flow from that client to the address the lookup returned. There is no second
method on OPNsense 26.7. Suricata exposes no `dns` event type, and its `tls`
and `http` events, although they can be written to `eve.json`, cannot be read
back through the API (survey, gaps 1 and 2). Client-side caching, shared
content networks, DNS-over-TLS and DNS-over-HTTPS each break the correlation,
and a client using an external encrypted resolver can only be named by address,
country and operator.

**Provisional columns.** The survey carries `UNVERIFIED:` markers on the ISC
lease contract, on the grammar of the Unbound and Dnsmasq query-log lines, and
on the eve-log volume estimates. `dhcp_lease.backend = 'isc'` and
`dns_resolution.resolver = 'dnsmasq'` rest on those markers and are provisional
until step 4 confirms them against a live firewall.

## Vocabulary

**`opnview` uses the words OPNsense uses.** Where OPNsense already names a
thing, that name is the identifier here, taken from the endpoint that returns
it rather than from memory. Where OPNsense has no word for something `opnview`
needs, that is said out loud below and the term is chosen deliberately. The
rule itself is in `ROADMAP.md`, under *Rules that apply to every step*, and in
`CLAUDE.md`.

| Thing | OPNsense's term | Identifier here | Where that is established |
|---|---|---|---|
| a VLAN, physical link or tunnel the firewall terminates | **interface** | `interface` | `/api/interfaces/overview/interfaces_info`; menu Interfaces > Assignments (survey, *Runtime discovery* (i)) |
| its configuration key | `identifier` | `interface.identifier` | same endpoint, field `identifier` |
| the network device it runs on | `device` | `interface.device`, `interface_map.device`, `flow.interface_device` | same endpoint, field `device`; `/api/diagnostics/interface/get_interface_names` keys its map by it; the filter log reports it in `interface` |
| the description a user typed for it | `description` (`descr` in `config.xml`) | `interface.description` | same endpoint, field `description`, falling back to the upper-cased identifier |
| its raw link type | `link_type` | `interface.link_type` | same endpoint, field `link_type` |
| a VLAN's tag | `vlan_tag` | `interface.vlan_tag` | same endpoint, field `vlan_tag` |
| its link state | `status` | `interface.status` | same endpoint, field `status` |
| its administrative state | `enabled` | `interface.enabled` | same endpoint, field `enabled` |
| a machine on the network, seen in a lease | **client** | `client` | Kea and Dnsmasq lease `client_id`; Unbound `search_queries` takes and returns `client` (survey, data sources 4 and 5) |
| the DHCP option-61 identifier a client sends | `client_id` | `dhcp_lease.dhcp_client_id` | `/api/kea/leases4/search`, `/api/dnsmasq/leases/search` |
| the named list a resolver refused a lookup against | **blocklist** | `blocklist.name`, `dns_resolution.blocklist_id` | `/api/unbound/overview/search_queries` returns a `blocklist` field on every row (survey, data source 5, *Response shape*) |
| a firewall rule | **rule**, with `uuid` and `description` | `rule` | `/api/firewall/filter/search_rule` (survey, *Runtime discovery* (ii)) |
| the pf label the filter log reports as `rid` | `label` / `rid` | `rule.pf_label`, `flow.rid` | filter log `rid`; `search_rule` carries the label in `uuid` for legacy and automatic rules |
| whether a rule writes a line when a packet matches it | `log` | `rule.logs_matches` | `/api/firewall/filter/search_rule`, field `log` (survey, *Runtime discovery* (ii)); the identifier is spelled out because a column called `log` would read as a log rather than as a flag |
| why a filter-log line was written | `reason` | `flow.log_reason` | `/api/diagnostics/firewall/log`, field `reason` (survey, data source 1, *Response shape*); prefixed because `reason` alone would read as the reason for the decision, which is the rule |
| the resolver's validation verdict on a lookup | `dnssec_status` | `dns_resolution.dnssec_status` | `/api/unbound/overview/search_queries`, field `dnssec_status` (survey, data source 5, *Response shape*) |

**Four terms are `opnview`'s own, because OPNsense has no word for them.**
Each is marked as such where it is defined, so nobody later mistakes it for
vocabulary read off an endpoint:

| Term | Why OPNsense has no word | Why this one |
|---|---|---|
| `interface.user_label` | OPNsense carries a single description field, editable only on the firewall | a label the maintainer sets inside `opnview` needs a column a discovery refresh cannot overwrite |
| `interface.link_kind` | OPNsense publishes the raw `link_type` and no normalised class of it | `opnview` needs a closed vocabulary to derive `is_tunnel` from something other than a name |
| `owner` | no product `opnview` reads has any notion of a person; a lease names a host, not a human | the entity answers "whose machine is this", and the word is taken from that question |
| `blocklist.purpose` | the endpoint reports what a list is **called** and nothing about what it is **for**; OPNsense publishes no classification of a subscribed list | the column answers "advertising or threat", which is the maintainer's question, and it is assigned by the user for the same reason `owner` is: `hagezi-pro` and `oisd-small` tell a machine nothing |

**What this replaced.** `segment` was invented before any research existed and
was never reconciled with what OPNsense calls the thing. It is gone: the entity
is `interface`. `device` meant two things at once — the network device on an
interface, and a machine on the network — and now means only the first; the
second is `client`. Nothing is deployed, so both renames were made in the
migrations in place rather than added as a third one.

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

**Identifiers.** No interface identifier, interface description, VLAN name,
address or CIDR
appears as a literal anywhere in the migrations, their defaults, their
constraints, their indexes, the seven screen queries or the seed. There is no
`LIKE`, `GLOB` or `REGEXP` predicate in the DDL at all, so nothing can classify
an interface, a client or a rule by what it is called.

**Zero assumed counts.** Nothing in the schema or in the queries presumes a
number of interfaces, clients, owners, rules or address families. The checks prove
it by re-running every query against a second seed built with different counts.

## Entities

Exactly these entities exist. Blocked events are a view; everything else is a
table.

<!-- entity-list:begin -->
```
blocked_event
blocklist
client
dhcp_lease
dns_resolution
domain_attribution
eve_ingest_cursor
flow
geo_asn
interface
interface_map
owner
owner_volume_aggregate_1h
owner_volume_aggregate_24h
owner_volume_aggregate_30d
owner_volume_aggregate_7d
pair_volume_observation
provider
provider_rule_info
rule
schema_version
security_event
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
client
dhcp_lease
dns_resolution
domain_attribution
flow
geo_asn
owner_volume_aggregate_1h
owner_volume_aggregate_24h
owner_volume_aggregate_30d
owner_volume_aggregate_7d
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
blocklist
eve_ingest_cursor
interface
interface_map
owner
provider
provider_rule_info
rule
schema_version
setting
source_availability
```
<!-- bounded-tables:end -->

Why each bounded table is bounded, and may therefore be scanned:

| Table | Bound |
|---|---|
| `interface` | one row per discovered interface; a firewall has tens, not millions |
| `owner` | one row per person the user created; a household or an office has tens, and a row is only ever created by hand |
| `blocklist` | one row per distinct list name the resolver has reported; a resolver subscribes to tens of lists, not millions |
| `interface_map` | one row per raw device name the firewall reports |
| `rule` | one row per rule in the running ruleset |
| `provider` | one row per implementation the project knows of; nine today, and a new one is an `INSERT`, not a stream |
| `provider_rule_info` | one row per (provider, rule identity) seen at least once; a rule set holds tens of thousands at most, and the table only ever holds the ones actually observed |
| `source_availability` | exactly one row per `provider` row, for the life of the database |
| `eve_ingest_cursor` | one watermark per rotated `eve.json` file; the firewall keeps the current file plus four archives |
| `setting` | one row per configuration key |
| `schema_version` | one row per applied migration |

`client` is classified as growing, not bounded: an address reissued to another
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

### `interface` — one OPNsense interface, tunnels included

One discovered interface: a VLAN, a physical link or a tunnel the firewall
terminates. The word is OPNsense's own, not a coinage — see *Vocabulary*.
**Source:** `/api/interfaces/overview/interfaces_info` (runtime discovery (i))
— `identifier`, `description`, `device`, `status`, `enabled`, `link_type` and
`vlan_tag`. Every discovered column carries that endpoint's field name
unchanged. The address fields that endpoint also returns — `addr4`, `addr6`,
`ipv4[]`, `ipv6[]` and `gateways[]` — are **not** stored, and that is the open
gap G13 of `docs/widget-catalogue.md`, not an oversight; *API field coverage*
below records it with the rest.

**Identity:** `identifier`, the configuration key, unique. The `device` — the
network device the interface runs on — is unique too and is what the filter log
reports. In this schema `device` means that and only that; a machine on the
network is a `client`.

**Manual labelling is separate from discovery.** `description` holds
what the firewall reports; `user_label` holds the maintainer's own name and is
`NULL` until one is set. Relabelling never overwrites discovery and a refresh
of discovery never overwrites a label; both are returned together by the Matrix
and Alerts queries through `coalesce(user_label, description)`.

**Tunnels carry their own attribute.** `link_type` is the raw value
the API reports. `link_kind` is opnview's normalisation of it — `physical`,
`vlan`, `tunnel` or `other` — and `is_tunnel` is a generated column that is 1
exactly when `link_kind` is `tunnel`. Nothing in that chain reads a name: a
interface called anything at all is a tunnel if and only if its discovered link
type says so. `user_label` and `link_kind` are `opnview`'s own terms, for the
reasons the *Vocabulary* table gives.

**Down, switched off and quiet are three different facts.** `status` and
`enabled` hold the endpoint's fields of those names, verbatim. An interface
with no traffic, an interface whose link is down and an interface the
administrator disabled all render as an empty row, and these two columns are
the only thing that separates them — the same distinction `rule.logs_matches`
carries for a rule that does not log. Both are `TEXT` with no `CHECK` and no
normalisation, because the survey establishes the two fields and **not** their
encoding: a `0`/`1` column or an enumerated vocabulary written here would be
invented rather than discovered. Normalising them is a step-4 decision to take
against a live firewall and to record then.

**Retention:** never purged. An interface that disappears from discovery keeps its
row so historical flows stay joinable.

**Indexes:** the primary key, plus the two uniqueness constraints on
`identifier` and `device`.

### `interface_map` — join key one

The filter log reports a raw device name, not the user description
(survey, data source 1: "raw device name, **not** the user description").
**Source:** `/api/diagnostics/interface/get_interface_names`, runtime discovery
(i).

**Identity:** `device`, the primary key.

**Not found is a state, not a missing row.** A flow whose raw device name is
absent from this table carries `interface_lookup_state = 'not_found'`, and the
Client and Blocked screens return it with that state and a null description.

**Retention:** never purged. **Indexes:** the primary key.

### `rule` — join key two

**Source:** `/api/firewall/filter/search_rule`, runtime discovery (ii) —
`uuid`, `description`, `action`, `direction`, `log`, `is_automatic`. For legacy and
auto-generated rules the `uuid` carries the pf label, which is the same token
the filter log exposes as `rid`; the column is therefore named `pf_label` and
`rid` joins to it for both kinds.

**Identity:** `pf_label`, unique.

**Not found is a state.** A `rid` whose rule no longer exists is normal. The
flow keeps the raw `rid`, sets `rule_id` to `NULL` and
`rule_lookup_state = 'not_found'`, and the Blocked screen renders it as an
unknown rule.

**A rule that does not log is the second observation-point limit.**
`logs_matches` holds the endpoint's `log` field. The first limit is physical —
traffic between two machines behind one interface never reaches the firewall.
This one is configured: a rule that passes traffic without logging it produces
no filter-log record, so that traffic is invisible to `opnview` even though it
crossed the router. Both produce the same empty screen, and only this column
tells them apart; without it, "nothing here" has two causes and no way to say
which. It is nullable, so a rule discovered from a source that did not report
the flag reads as *not reported* rather than as *does not log*.

**Retention:** never purged. **Indexes:** the primary key and the uniqueness
constraint on `pf_label`.

### `client` — one machine

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
| 3 | `address_in_interface` | interface, address and the validity start of that address | neither of the above; the only level available to a machine seen only in flows |

Level 3 carries the validity start deliberately: an address reissued to another
machine after a lease expiry produces a different `identity_key`, so the two
machines stay two rows and never merge into one phantom client.

**Randomised MAC.** `mac_is_randomised` and `unstable_identity` are generated
columns that test the IEEE locally-administered bit: the second hex digit of
the MAC, in `2`, `6`, `a` or `e`. The digits `0`, `4`, `8` and `c` are globally
administered — real burned-in addresses — and are **not** marked unstable. The
roadmap's original wording, "second hex digit even", additionally captured
those four and would have marked genuine clients as unstable identities; the
IEEE rule is what the schema implements. The `mac` column is constrained to
seventeen lowercase characters so the digit test is total. Two randomised
observations with different MACs remain two rows: they are two identity keys.

**Retention:** purged by `last_seen_at`, but only once every observation that
named the client has itself been purged. The purge guards the delete with a
`NOT EXISTS` over `flow`, `security_event`, `dhcp_lease` and `dns_resolution`,
because removing an identity a surviving flow still points at would leave that
flow unable to name a machine.

**Ownership is assigned, never inferred.** `owner_id` is `NULL` until somebody
attributes the machine, and `owner_assigned_at` records when that happened; a
`CHECK` rejects a row where one is set and the other is not. Nothing derives an
owner from a hostname, a MAC prefix, a vendor hint or an address: there is no
`DEFAULT` on the column, no generated expression, no trigger, and no
name-matching predicate anywhere in the DDL. It is the rule `user_label`
already obeys.

**Indexes:** the primary key, `UNIQUE (identity_kind, identity_key)`,
`idx_client_interface` for listing the clients behind one interface, and
`idx_client_owner` for the machines of one person.

### `owner` — a person, assigned by hand

One person the maintainer has told `opnview` about, so that Bob's phone, tablet
and laptop read as one Bob rather than as three unrelated cards.

**Source: none.** This is the only entity in the schema fed by no endpoint. The
firewall does not know who owns what — a lease carries a hostname, a MAC and a
client identifier, and none of them names a human being — so an owner is
created by the user, in `opnview`, and by nothing else. The term is
`opnview`'s own for that reason, and is recorded as such in *Vocabulary*.

**Identity:** `display_name`, unique. Two owners with the same name would be
indistinguishable on screen, so the constraint is on the name rather than on a
surrogate alone.

**Ownership is never inferred, and the schema is what enforces it.** The link
lives on `client.owner_id`, with no `DEFAULT`, no generated expression and no
trigger, so the only thing that can fill it is a statement somebody wrote. This
is the same rule the roadmap already applies to interface labels, and for the
same reason: a name is not evidence, and guessing one wrong is worse than
leaving it blank.

**A client with no owner is normal and stays visible.** Most machines on a
network belong to nobody in particular. A per-person view must therefore carry
an explicit **unassigned** bucket rather than quietly dropping every client
nobody has claimed, which would under-report the network while looking
complete. The diagnostic query `-- diagnostic: Clients per owner` in
`sql/queries/diagnostics.sql` is that guarantee written down: it drives from
`client` with a `LEFT JOIN` to `owner`, returns the unassigned bucket as a row
of its own, and the checks assert that its client counts add up to every row of
`client`.

**A per-person question beyond the `flow` horizon is answerable.** It was not:
the four `volume_aggregate_*` tables carry interfaces and peer addresses and no
owner dimension at all, so a 7-day or 30-day per-person question had nothing to
read. The `owner_volume_aggregate_*` family below is keyed on the person and
closes G11 of `docs/widget-catalogue.md`. What remains open there is the widget
side of that gap, which is that document's to close.

**Retention:** never purged. An owner is user input, not an observation, and
purging a client leaves its owner alone.

**Indexes:** the primary key and the uniqueness constraint on `display_name`.

### `dhcp_lease` — one observed lease generation

**Source:** data source 4, the three lease endpoints. The MAC field name is the
only normalisation needed: `hwaddr` on Kea and Dnsmasq, `mac` on the legacy ISC
plugin.

**Identity:** `(address, starts_at, backend)`, unique — one row per lease
generation, so a reissue is a second row rather than an overwrite.

**Retention:** purged by `observed_at`.

**Indexes:** `idx_dhcp_lease_observed_at` for the purge and for recency,
`idx_dhcp_lease_client` for a client's lease history.

### `flow` — one filter-log record

**Source:** `/api/diagnostics/firewall/log`, data source 1 — `interface`,
`action`, `rid`, `label`, `src`, `dst`, `srcport`, `dstport`, `protoname`,
`protonum`, `length`, `ipversion`, `dir`, `reason` and `__timestamp__`,
deduplicated on `__digest__`, which is stored as `log_digest`. Every other
field the endpoint returns is accounted for in *API field coverage* below.

**Why it was logged is not what was done to it.** `log_reason` holds the
endpoint's `reason` field verbatim. A record whose reason is not a rule match
was not denied by a rule at all, and presenting it as a rule denial would
attribute a decision to a rule that made none. The value is never parsed,
normalised or matched on, and no `CHECK` enumerates its values: the survey
establishes the field and not its value set, so a vocabulary written here would
be invented rather than discovered.

**Identity:** `log_digest`, unique. The endpoint echoes back the record
matching the digest a poll supplied, so the uniqueness constraint is what makes
the echo harmless.

**East-west and north-south** are carried by `traffic_scope`, which is
`east_west` exactly when both `src_interface_id` and `dst_interface_id` are set,
and `north_south` otherwise. It derives from interface membership and from
nothing else — no address, no CIDR, no name, no assumed addressing plan. The
collector writes it on every insert; the paragraph below says why it is a plain
column rather than a generated one.

**Retention:** purged by `observed_at`. Purging a flow cascades to its
attribution.

**`traffic_scope` is a plain column pinned by a `CHECK`**, not a generated
column. It is `NOT NULL` with no default, so step 4 must write it on every
insert — an insert that omits it fails on the `NOT NULL` constraint before any
other constraint is reached. The `CHECK` is exactly the expression that would have generated it, so
no row can carry a value disagreeing with its interfaces — the checks demonstrate
that with a failing insert. It is not generated for one reason only: SQLite
never reports an index as covering for a query that reads a generated column,
virtual or stored, and the Overview, Matrix and Interface queries all group by
this value. Making it generated would cost all three their covering plans.

**Indexes:**

| Index | Serves | Covering |
|---|---|---|
| `idx_flow_observed_at` | the Overview and Matrix queries, and any period-wide read | yes, for both |
| `idx_flow_src_interface_observed_at` | the Interface query | yes |
| `idx_flow_src_client_observed_at` | the Client query | no |
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
dst_client_id
dst_port
dst_interface_id
id
ingested_at
interface_device
interface_lookup_state
ip_version
log_digest
log_reason
observed_at
packet_bytes
protocol
rid
rule_id
rule_lookup_state
src_address
src_client_id
src_port
src_interface_id
traffic_scope
```
<!-- flow-columns:end -->

| Column | Read by |
|---|---|
| `id` | the Client query; the `domain_attribution` foreign key |
| `log_digest` | nothing, and it stays: it is the **identity** of the row. The endpoint echoes back the record matching the digest a poll supplied, and this uniqueness constraint is what makes that echo harmless instead of a duplicate |
| `observed_at` | all five flow-fed screen queries, the purge, the aggregate refresh |
| `ingested_at` | the **aggregate refresh**: a slot is stale when its `computed_at` is older than the newest `ingested_at` among the flows in its window. That comparison is the staleness rule, and no separate dirty flag exists |
| `interface_device`, `interface_lookup_state` | the Client and Blocked queries |
| `src_interface_id`, `dst_interface_id` | Overview, Matrix, Interface, Blocked; the `traffic_scope` `CHECK` |
| `src_client_id` | Overview, Interface, Client, Blocked; the purge's client guard |
| `dst_client_id` | the **purge**: a client is removed only when no surviving flow names it as source **or destination** |
| `src_address` | the Blocked query |
| `dst_address` | the Interface, Client and Blocked queries; the `geo_asn` join |
| `src_port` | nothing, and it stays: it is the other half of the connection tuple, and it is what lets a flow be matched back to a `pair_volume_observation`, whose `service_port` is Insight's `min(src_port, dst_port)`. Without it that correlation is impossible |
| `dst_port` | the Client and Blocked queries |
| `protocol` | the Client and Blocked queries |
| `ip_version` | nothing, and it stays: it is the address family the filter log reports. An address column alone cannot be classified, and the project forbids inferring an addressing plan, so a v4/v6 split is answerable only from this column. IPv6 seed coverage is recorded against step 4 |
| `action` | Overview, Matrix, Interface, Client; the `blocked_event` view and its partial index |
| `direction` | nothing, and it stays: it is the in/out sense the filter log reports, and it is what tells a reader whether `src_address` on a blocked record is the machine inside or the machine outside. Reading a denial without it is guesswork |
| `packet_bytes` | Overview, Matrix, Interface, Client; the aggregate refresh |
| `rid` | the Blocked query alone, which returns the firewall's own rule identifier next to the resolved description. No other screen query reads it: the interface-pair screen joins through `rule_id` and reports `rule_lookup_state` instead |
| `rule_id`, `rule_lookup_state` | the Matrix and Blocked queries |
| `traffic_scope` | Overview, Matrix, Interface, Client, Blocked; the aggregate refresh |
| `log_reason` | no screen query yet, and it stays: it is the filter log's own answer to *why was this line written*, and it is the only thing that separates a packet a rule denied from a packet the firewall dropped for a reason no rule expresses. Without it a malformed-packet drop and a policy denial are the same row, and the denial screens would attribute the first to a rule that made no decision. It is projected by the `blocked_event` view, where that distinction is read |

Five columns — `log_digest`, `src_port`, `ip_version`, `direction` and
`log_reason` — are read by no screen query, by no purge statement and by no
aggregate refresh. Each is justified above, and each is kept deliberately.

### `blocked_event` — a view, not a table

Every blocked record is the same filter-log line as an allowed one with a
different `action`. Making it a second table would mean a second ingestion path
and a second copy of the data. It is therefore a view over `flow`, paired with
the partial index above so that reading it is an index search rather than a
scan of every flow ever recorded.

Code that expects a table will not find one. This is deliberate and is the only
entity in the list that is not a table.

### `blocklist` — a list the resolver refused a lookup against

One named list, as the resolver reported it, and the purpose a user assigned to
it. This closes G1 of `docs/widget-catalogue.md`: "blocked by which list" was
unanswerable because the schema had nowhere to put a value the API was already
handing it.

**Source of the name:** `/api/unbound/overview/search_queries`, data source 5.
The survey's *Response shape* for that endpoint lists `blocklist` among the
per-row fields, beside `client`, `domain`, `time`, `action`, `source`, `rcode`,
`dnssec_status` and `uuid`. The name is stored **verbatim**, exactly as the
endpoint spelled it, and is never parsed, normalised, trimmed or matched on.

**Source of the purpose: none — it is user input.** The endpoint reports what a
list is *called* and says nothing about what it is *for*. That is the whole
finding: the survey establishes the field and establishes nothing beyond it,
and the strings a resolver actually reports — `hagezi-pro`, `oisd-small`,
whatever a given installation subscribed to — carry their meaning in a human
reader's head and nowhere a machine can reach. So `purpose` is assigned inside
`opnview`, by the person who knows, and by nothing else.

**Inferring a purpose from a name is forbidden, and the schema is what enforces
it.** There is no `DEFAULT` on `purpose`, no generated expression, no trigger,
and no `LIKE`, `GLOB` or `REGEXP` predicate on `name` anywhere in the DDL or in
the queries. It is exactly the rule `interface.user_label` and `client.owner_id`
already obey, for exactly the same reason: a name is not evidence. A tool that
told a user a list was a threat feed because its name contained a suggestive
substring would be guessing, and presenting a guess as a fact is the one thing
this project refuses.

**The vocabulary** is `advertising`, `tracking`, `threat`, `parental`, `other`,
constrained by a `CHECK`. It is `opnview`'s own — recorded as such in
*Vocabulary* — because OPNsense publishes no classification to borrow.

**A list with no purpose is normal and stays visible.** `purpose IS NULL` means
nobody has classified it yet, which is the state every list is in on the day it
first appears. A screen renders that as **purpose not assigned**, a state of its
own, never as `other` and never as a hidden row. `purpose_assigned_at` records
when somebody decided, and a `CHECK` keeps the two columns from disagreeing.

**Identity:** `name`, unique. Two rows with the same observed name would be the
same list.

**The query that answers the question** is
`-- diagnostic: Blocked lookups by list` in `sql/queries/diagnostics.sql`. It
drives from `dns_resolution` with a `LEFT JOIN` to `blocklist`, so an
unattributed block is a row of its own, and it projects the unassigned purpose
as a state rather than folding it into a named one. The checks execute it and
assert that its counts add up to every blocked lookup in the window.

**Retention:** never purged. A purpose is user input, not an observation, and
purging the lookups that named a list must not discard the classification work
behind it — the same rule `owner` obeys.

**Indexes:** the primary key and the uniqueness constraint on `name`.

### `dns_resolution` — one resolver lookup

**Source:** `/api/unbound/overview/search_queries`, data source 5 — `client`,
`domain`, `time`, `action`, `source`, `rcode`, `dnssec_status`, `blocklist` and `uuid`. The call must be a
JSON-body read-only POST with integer `timeStart` and `timeEnd`: any other call
form silently returns the 1000 most recent records with HTTP 200 and no
diagnostic. For a Dnsmasq resolver there is no structured endpoint at all and
the rows come from parsing the free-text `line` of the generic log endpoint,
whose grammar the survey marks `UNVERIFIED:`.

**Identity:** `lookup_uuid`, unique — the row `uuid` the endpoint returns, which
is what deduplicates a re-requested window.

**The list that refused it.** `blocklist_id` is a nullable foreign key to
`blocklist`, resolved at ingest from the row's `blocklist` field. It is NULL for
every passed lookup, and also for a **blocked** lookup the resolver did not
attribute to any list — two different facts, told apart by `action`. The second
is rendered as *"blocked, list not recorded"*: a lookup that was refused by
something nobody can name is not the same as a lookup that was allowed, and it
is not a row to drop.

**The validation verdict.** `dnssec_status` holds the endpoint's field of that
name verbatim — the resolver's own conclusion about whether the answer
validated. It is stored and not dropped for the same reason `rcode` is: the
resolver reached a verdict, and discarding it would leave `opnview` unable to
say anything about a class of failure the resolver already diagnosed. It is
never parsed or matched on and no `CHECK` enumerates its values, because the
survey establishes the field and not its value set. It is `NULL` when the
resolver reported no verdict — every lookup read from the free-text dnsmasq
query log, which has no such field — and that is *not reported*, not
*unvalidated*.

**Retention:** purged by `looked_up_at`. Purging a lookup cascades to every
attribution inferred from it, because an attribution with no lookup behind it
could not be judged. It does **not** touch the `blocklist` row it pointed at.

**Indexes:** `idx_dns_resolution_looked_up_at` for the purge,
`idx_dns_resolution_client` for the per-client window the collector asks for,
and `idx_dns_resolution_blocklist` so "which list refused what, over this
period" is an index search rather than a scan of a growing table. That index's
leading column is NULL-bearing, and SQLite keeps the unattributed blocked
lookups at its head rather than omitting them, so it serves the *list not
recorded* rows too.

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
Client query returns it with its address, country and operator and a null site
name.

**Attribution rate.** The per-client rate is the query
`-- diagnostic: Attribution rate per client` in `sql/queries/diagnostics.sql`.
It is executed by the checks, and it is the figure the UI must expose per
client.

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
Alerts query, `idx_security_event_client_occurred_at` for a client's events,
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
| `dhcp_lease` | `/api/kea/service/status`, `/api/dnsmasq/service/status`, `/api/dhcpv4/service/status` | a backend that is not running is `unavailable`; when no backend of the kind runs, clients fall back to being named by address |
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
(source interface, peer) pair.

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
`(period_start_at, src_interface_id, ifnull(dst_interface_id, -1), ifnull(peer_address, ''))`.
The `ifnull` wrappers are necessary because SQLite treats NULLs in a unique
index as distinct, and both columns are NULL-bearing by design: a north-south
slot has no destination interface, and an east-west slot has no peer address.

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

**Aggregate mode.** The roadmap's aggregate mode — volumes, interfaces, countries
and operators without domain names — is the Map query,
`-- screen: Map` in `sql/queries/screens.sql`. It reads
`volume_aggregate_24h`, `geo_asn` and `interface`, and no table that holds a
domain name. The checks assert that by inspecting the tables it references.

**Retention:** purged by `period_end_at`, so a slot survives as long as its
window lies inside the horizon. Every period remains queryable after a purge;
only slots entirely older than the horizon are removed.

**Indexes:** the unique slot index per table, whose leading column is
`period_start_at`, which is also what makes a period read an index search.

### `owner_volume_aggregate_1h`, `_24h`, `_7d`, `_30d` — the per-person volume store

The same four periods, keyed on the person rather than on the interface pair.
They exist because the four tables above have no owner dimension, so "how much
did one person's machines move last month" could only be answered by scanning
`flow` — which is bounded by the `retention_seconds` row of `setting` and so
cannot answer a question older than the horizon at all. That was G11 of
`docs/widget-catalogue.md`.

**Source: the same flows, joined to `client.owner_id`.** Nothing here is read
from an endpoint and nothing here infers an owner. The firewall has no notion of
a person, so the only thing that can put a flow under a name is a link somebody
wrote by hand, and these tables are downstream of that link and of nothing else.

**Identity:** the slot, enforced by a unique index over
`(period_start_at, ifnull(owner_id, -1), traffic_scope)`. The `ifnull` wrapper
is the same device the four tables above use, and for the same reason: SQLite
treats NULLs in a unique index as distinct, and `owner_id` is NULL-bearing by
design. One difference from the interface family is deliberate and is not a
drift: `traffic_scope` is part of the key here, where above it is derivable from
whether `dst_interface_id` is set. A per-person slot has no destination
interface to derive it from, so the scope is keyed explicitly.

**`owner_id IS NULL` is the unassigned bucket, and it is not optional.**
Ownership is assigned by hand and most machines on a network belong to nobody in
particular, so a per-person aggregate that dropped the unowned clients would
under-report the network while looking complete. It is the same guarantee the
`-- diagnostic: Clients per owner` query already carries, applied to the
pre-computed periods: the checks assert that every one of the four periods holds
an unassigned slot, and that the unowned bytes land in it rather than vanishing.

**`client_count` is per slot and must not be summed across slots.** It is the
number of distinct clients that contributed to that one slot; the same machine
active in two hours would be counted twice by a sum. `bytes`,
`allowed_connections` and `blocked_connections` are summable across slots;
`client_count` is not, and no query in this repository sums it.

**Refresh contract:** the five rules above apply unchanged, with one addition
specific to this family. Reassigning a client to another owner, or clearing its
owner, invalidates every slot whose window holds a flow from that client —
because ownership is an attribute of the person and not of the flow, so a row
that was correct when it was computed stops being correct the moment somebody
edits the link. There is no dirty flag for that either; the reassignment is what
triggers the recomputation, and `computed_at` is what shows whether it ran.

**Observation-point limit** applies exactly as above: every figure is a lower
bound, and a per-person screen must say so.

**Freshness and coverage** are read by
`-- diagnostic: Owner aggregate coverage per period` in
`sql/queries/diagnostics.sql`, which returns one row per period with its slot
count, its freshness and — the honest column — how many of its slots are the
unassigned bucket.

**Retention:** purged by `period_end_at`, like the four above. The `owner` rows
they point at are never purged.

**Indexes:** the unique slot index per table, leading on `period_start_at`.

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

## API field coverage

Every field `docs/opnsense-api-survey.md` says an endpoint returns, and what
the schema does with it. The table exists because the same defect was found
twice by accident: `dns_resolution` had no `blocklist` column although
`/api/unbound/overview/search_queries` returns one, and while that was being
closed `dnssec_status` turned out to be missing from the same response for the
same reason. Two accidents mean the class was never checked, so it is checked
here, field by field, and the result is written down rather than remembered.

**Three verdicts, and no fourth.**

- **stored** — a column holds it. The verdict names that column, and
  `sql/schema-checks.sh` fails if the column does not exist.
- **not stored** — a decision was taken not to hold it, and the reason is on
  the row. A field `opnview` has no use for is a legitimate *not stored*; what
  is not legitimate is the decision being invisible.
- **dropped** — the API returns it, the schema has nowhere to put it, and
  nobody decided that. Every row carrying this verdict is a defect. The ones
  closed in this pass are marked *stored* below; the ones left open say which
  recorded gap tracks them and why closing them is not a column.

**What this table cannot do.** It cannot detect a newly dropped field on its
own. The survey is prose, and no machine can tell that a response gained a key
nobody wrote down. What the harness does enforce is that the table stays
honest about the schema: a *stored* row naming a column that does not exist
fails the checks. A field added to the survey and forgotten here is caught by a
human reading both, which is why this table is maintained in the same pass as
any survey change.

**Where the survey is thin, the row says so** rather than guessing a type.
A field whose shape is not established is marked as such and stored verbatim
or not at all — never given an invented vocabulary, a `CHECK` or a normalised
encoding that the OPNsense documentation does not support.

<!-- api-field-coverage:begin -->

### Data source 1 — filter logs

`/api/diagnostics/firewall/log`, survey *Data source 1*, *Response shape*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `rulenr` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — pf's ordinal rule number, renumbered on every ruleset reload; `rid` is the stable identity and is stored |
| `subrulenr` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the ordinal inside an anchor, with the same instability as `rulenr` |
| `anchorname` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — names the pf anchor a rule sits in; rule identity is carried by `rid` and no screen groups by anchor |
| `rid` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.rid`, joined to `rule.pf_label` |
| `interface` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.interface_device`, resolved through `interface_map.device` |
| `reason` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.log_reason`. Dropped until this pass; closed here, because a record logged for a reason that is not a rule match is not a rule denial and must not be rendered as one. Value set not established by the survey, so it is stored verbatim with no `CHECK` |
| `action` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.action` |
| `dir` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.direction` |
| `ipversion` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.ip_version` |
| `__digest__` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.log_digest`, the identity that makes the echoed record harmless |
| `__timestamp__` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.observed_at`, normalised to UTC epoch seconds at the boundary |
| `__host__` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the syslog host that wrote the line. `opnview` reads one firewall, so the value is constant across every record it will ever see |
| `__spec__` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the positional field specification the backend parser used; an artefact of the line format, not a property of the packet |
| `label` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** on the record — the rule description resolved at the firewall. The same text is discovered authoritatively into `rule.description` and reached through `flow.rule_id`, with `flow.rule_lookup_state` carrying the removed-rule case; copying it onto every log line would duplicate it a million times over |
| `protoname` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.protocol` |
| `protonum` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the numeric twin of `protoname`, which is stored verbatim |
| `length` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.packet_bytes`, the figure every volume column sums |
| `src` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.src_address` |
| `dst` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.dst_address` |
| `srcport` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.src_port` (TCP and UDP records only) |
| `dstport` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.dst_port` (TCP and UDP records only) |
| `tos` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 type-of-service marking. No entry in `docs/widget-catalogue.md` asks about traffic marking, and a per-packet marking does not aggregate into any figure the product shows |
| `ecn` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — congestion-notification bits; per-packet transport detail with no question behind it |
| `ttl` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the per-packet hop budget; it varies inside one conversation and sums to nothing |
| `id` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 fragment identifier |
| `offset` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 fragment offset |
| `ipflags` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 fragmentation flags |
| `class` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 traffic class, the twin of `tos` |
| `flow` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 flow label; it labels a conversation for the routers, and `opnview` identifies one by its endpoints |
| `hoplimit` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 twin of `ttl` |
| `datalen` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the payload length. `length` is the figure the volume columns sum, and holding both invites a screen summing the wrong one |
| `tcpflags` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — judged and left out, recorded so the judgement is visible. No entry in `docs/widget-catalogue.md` reads them, and the one use that would justify the column — telling a scan from a conversation — needs flags aggregated across records the log only emits where a rule logs, so it would answer the question for the logging subset and stay silent elsewhere. A partial answer to a security question is worse than none |
| `seq` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP sequencing internals |
| `ack` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP sequencing internals |
| `urp` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP urgent pointer |
| `tcpopts` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP options |
| `action`, `interface_name`, `dir` | `/api/diagnostics/firewall/log_filters` | Data source 1, Response shape | **not stored** — the filter vocabulary the firewall's own UI offers, read from its configuration rather than from the log. `opnview` filters in SQL over the columns it stores |

### Data source 2 — security events

`/api/ids/service/query_alerts` and its companions, survey *Data source 2*,
*Response shape* and *Degradation*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `timestamp` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.occurred_at`, normalised from the ISO string |
| `src_ip` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.src_address` |
| `src_port` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.src_port` |
| `dest_ip` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.dst_address` |
| `dest_port` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.dst_port` |
| `proto` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.protocol` |
| `in_iface` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.in_interface_device` |
| `alert` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.signature`. The backend has overwritten the nested alert object with the signature text before `opnview` sees it (survey, gap 3), so this is the whole of what the key carries |
| `alert_sid` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.rule_identity`, and the key of `provider_rule_info` |
| `alert_action` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `security_event.event_action` |
| `fileid` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `eve_ingest_cursor.file_id`, and half of the composed `security_event.provider_event_key` |
| `filepos` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **stored** — `eve_ingest_cursor.byte_offset`, the other half |
| `event_type` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **not stored** — constant on this feed: the backend returns a record only if it carries a top-level `alert` key, so every row is of one type |
| `flow_id` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **not stored** — the engine's own per-conversation identifier. No entry in `docs/widget-catalogue.md` groups events by it, and each event already carries its endpoints. `security_event.flow_ref` is **not** this field: it is an unconstrained `INTEGER` that nothing writes and nothing reads, raised and withdrawn as a candidate in that catalogue, and it is recorded here so the two are never confused |
| `total_rows` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **not stored** — an envelope counter of how many records the backend has scanned so far; a property of the call, not of any event |
| `origin` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **not stored** — the basename of the file the rows came from; `fileid` identifies it and the cursor is keyed on that |
| `filters` | `/api/ids/service/query_alerts` | Data source 2, Response shape | **not stored** — the envelope's echo of the filter set the backend applied |
| `sequence` | `/api/ids/service/get_alert_logs` | Data source 2, Response shape | **stored** — `eve_ingest_cursor.file_sequence`, which is how rotation is detected |
| `filename` | `/api/ids/service/get_alert_logs` | Data source 2, Response shape | **not stored** — a path on the firewall; the cursor is keyed on `file_id`, and a path would tie the watermark to a filesystem layout |
| `size` | `/api/ids/service/get_alert_logs` | Data source 2, Response shape | **not stored** — the file's size at probe time; rotation is detected from `sequence`, and a size is a fact about a file `opnview` never opens |
| `modified` | `/api/ids/service/get_alert_logs` | Data source 2, Response shape | **not stored** — same: rotation is detected from `sequence` |
| `status` | `/api/ids/service/status` | Data source 2, Degradation | **stored** — `source_availability.state`, normalised to the three modelled values, with the raw string in `source_availability.detail` and the endpoint in `source_availability.probe` |
| `ids.general.enabled` | `/api/ids/settings/get` | Runtime discovery (v) | **stored** — `source_availability.state`, as the `present_but_disabled` value that separates *switched off* from *unreachable* |
| `ids.general.interfaces` | `/api/ids/settings/get` | Runtime discovery (v) | **dropped** — which interfaces the engine actually covers is not modelled, so a screen cannot say per interface whether it is watched. Tracked as **G8** in `docs/widget-catalogue.md`, whose fix is a `provider_interface_coverage` table rather than a column, and therefore not closed in this pass |
| `ids.general.eveLog.tls.enable`, `ids.general.eveLog.http.enable` | `/api/ids/settings/get` | Runtime discovery (v) | **not stored** — the events those toggles produce cannot be read back through any endpoint at all (survey, gap 2), so the flag would describe a source `opnview` has no path to and would invite a screen to promise records that can never arrive |
| response shape | `/api/ids/service/get_alert_info/<alertId>` | Data source 2, Endpoints | **not stored** — *the survey establishes the endpoint and its parameter and gives no response shape.* Nothing is read from it and nothing can be stored from it until it is surveyed; inventing columns for a shape nobody has read would be guessing |
| response shape | `/api/ids/settings/get_rule_info/<sid>` | Data source 2, Endpoints; gap 3 | **stored** — what is read from it lands in `provider_rule_info.provider_severity`, `provider_rule_info.category` and `provider_rule_info.rule_source`, normalised into `provider_rule_info.normalised_severity`. *The survey establishes the endpoint and not its field names*, so those three columns are `opnview`'s own shape and the mapping onto the real keys is a step-4 decision to record then |

### Data source 3 — volume

`/api/diagnostics/networkinsight/...` and the NetFlow probes, survey
*Data source 3*, *Response shape* and *Degradation*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `if` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.observed_interface_device`, kept for provenance and deliberately outside the de-duplication key |
| `direction` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.observed_direction`, also outside the key: the aggregate writes each flow once per direction with the endpoints swapped, so a direction-bearing key would double-count |
| `src_addr` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.endpoint_low` or `endpoint_high`, whichever the canonical ordering puts it in |
| `dst_addr` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — the other of `pair_volume_observation.endpoint_low` and `endpoint_high` |
| `service_port` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.service_port`, which is the aggregate's own `min(src_port, dst_port)` heuristic and is documented as a hint rather than a destination port |
| `protocol` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.protocol` |
| `total` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.octets` or `pair_volume_observation.packets`, according to the `measure` the call asked for; `top` returns no separate keys |
| `last_seen` | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **stored** — `pair_volume_observation.last_seen_at` |
| remainder record | `/api/diagnostics/networkinsight/top/...` | Data source 3, Response shape | **not stored** — the single row rows beyond `max_hits` collapse into, whose key fields are empty. It names no pair, so it cannot be a pair observation; a row with blank endpoints would read as a pair between two unknown machines |
| `mtime` | the aggregate tables behind `top` | Data source 3, Response shape | **not stored** — a column of the firewall's own aggregate database, not a field of the HTTP response |
| `key`, `values`, `interface`, `direction` | `/api/diagnostics/networkinsight/timeserie/...` | Data source 3, Response shape | **dropped** — the time series has no destination table, while the polling plan polls it every 300 s. That is a contradiction between the plan and the model, and it is reported rather than closed here: whether the series is ingested at all is a step-4 decision, and if it is, it needs a sampled-series table of the shape G9 describes, not a column. The `volume_aggregate_*` family is computed from `flow` and is unaffected either way |
| `netflow`, `local` | `/api/diagnostics/netflow/is_enabled` | Data source 3, Degradation | **stored** — `source_availability.state`: `local` of 0 is the `present_but_disabled` value, which is what separates a firewall exporting to an external collector from one that is broken |
| `last_sync`, aggregator map | `/api/diagnostics/networkinsight/get_metadata` | Data source 3, Degradation | **not stored** — consumed at detection time and folded into `source_availability.state`, `source_availability.checked_at` and `source_availability.detail`. *The survey is thin on the aggregator map's shape*, and no column is invented for it |
| response shape | `/api/diagnostics/networkinsight/get_interfaces` | Data source 3, Endpoints | **not stored** — the aggregator's own view of the interfaces. Interfaces are discovered authoritatively by runtime discovery (i) into `interface`, and a second list would be a second truth |
| response shape | `/api/diagnostics/netflow/status` | Data source 3, Endpoints | **not stored** — *the survey lists the endpoint and gives no response shape.* Availability is determined from `is_enabled` and `get_metadata`, which are surveyed |

### Data source 4 — DHCP leases

The three backend lease endpoints, survey *Data source 4*, *Response shape*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `hostname` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.hostname`, and carried onto `client.hostname` |
| `hwaddr` / `mac` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.mac`, lower-cased and normalised across the two spellings; carried onto `client.mac` |
| `address` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.address`, and carried onto `client.last_address` |
| `state` / `lease_type` and `is_reserved` / `status` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.lease_state`, normalised across the three backends into one vocabulary |
| `expire` / `ends` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.expires_at` |
| `starts` | `/api/dhcpv4/leases/searchLease` | Data source 4, Response shape | **stored** — `dhcp_lease.starts_at` |
| `client_id` | the two current lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.dhcp_client_id`, the most stable level of the client identity cascade |
| `duid` | `/api/kea/leases4/search` | Data source 4, Response shape | **stored** — `dhcp_lease.duid` |
| `iaid` | the two current lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.iaid` |
| `mac_info` / `man` | the three lease endpoints | Data source 4, Response shape | **stored** — `dhcp_lease.vendor_hint`, and carried onto `client.vendor_hint`. It is context and is never a basis for classifying a machine |
| `if_name` / `if` | the three lease endpoints | Data source 4, Response shape | **stored** — resolved into `dhcp_lease.interface_id` |
| `if_descr` | the three lease endpoints | Data source 4, Response shape | **not stored** — the interface description, discovered authoritatively by runtime discovery (i) into `interface.description`. Storing the lease endpoint's copy would give one interface two descriptions that can disagree |
| `valid_lifetime` | `/api/kea/leases4/search` | Data source 4, Response shape | **not stored** — consumed at ingest: `dhcp_lease.starts_at` is `expire` minus this value, so the pair of instants the model keeps carries the same fact in the form every other table uses |
| `prefix_len` | `/api/kea/leases4/search` | Data source 4, Response shape | **not stored** — the prefix length beside the leased address. A machine is keyed by its address, and an interface's addressing is runtime discovery (i)'s business, where **G13** already tracks that it is not stored |
| `stats` | `/api/kea/leases4/search` | Data source 4, Response shape | **not stored** — the envelope's active / inactive / total counters, which count the rows the same response carried and are a `count(*)` over `dhcp_lease` |
| `interfaces` | the two current lease endpoints | Data source 4, Response shape | **not stored** — the envelope's identifier-to-description map, which is `interface_map` obtained from a less authoritative endpoint |
| `status` | `/api/kea/service/status`, `/api/dnsmasq/service/status`, `/api/dhcpv4/service/status` | Data source 4, Degradation; Runtime discovery (iv) | **stored** — `source_availability.state`, with the raw string in `source_availability.detail` and the endpoint in `source_availability.probe`. A 404 from the third is the `unavailable` state, meaning the end-of-life plugin is absent |
| `dhcpv4.general.enabled` | `/api/kea/dhcpv4/get` | Runtime discovery (iv) | **stored** — `source_availability.state`, as the `present_but_disabled` value |
| `dnsmasq.dhcp_ranges` | `/api/dnsmasq/settings/get` | Runtime discovery (iv) | **not stored** — only its emptiness is read, and it decides `source_availability.state`. The ranges themselves are the firewall's addressing configuration, which `opnview` neither stores nor interprets |

### Data source 5 — resolver lookups

`/api/unbound/overview/search_queries` and the generic log endpoint, survey
*Data source 5*, *Response shape* and *Degradation*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `client` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.client_address`, resolved where possible into `dns_resolution.client_id` |
| `domain` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.domain` |
| `time` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.looked_up_at` |
| `action` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.action` |
| `source` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.answer_source` |
| `rcode` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.rcode` |
| `dnssec_status` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.dnssec_status`. Dropped until this pass; closed here. It is the resolver's own validation verdict, reached and then discarded, and it is stored verbatim with no `CHECK` because the survey establishes the field and not its value set |
| `blocklist` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `blocklist.name`, referenced by `dns_resolution.blocklist_id`. This is the gap **G1** closed in an earlier pass, and the accident that started this sweep |
| `uuid` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **stored** — `dns_resolution.lookup_uuid`, which deduplicates a re-requested window |
| `status` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **not stored** — a value the backend derives from `action`, which is stored. A derived twin of a stored column can only ever disagree with it |
| category metadata | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **not stored** — *the survey names it only as "category metadata" and gives neither a field name nor a shape.* It is marked here rather than closed: a column for a field nobody has read would be an invented name holding an invented type. Re-surveying this response is the fix, and it is a survey task, not a migration |
| `enabled` | `/api/unbound/overview/is_enabled` | Data source 5, Degradation | **stored** — `source_availability.state`, as the `present_but_disabled` value that tells *reporting is switched off* apart from *the resolver is down* |
| `status` | `/api/unbound/service/status`, `/api/dnsmasq/service/status` | Data source 5, Degradation; Runtime discovery (iii) | **stored** — `source_availability.state`, with the raw string in `source_availability.detail` |
| `unbound.general.enabled`, `dnsmasq.enable` | `/api/unbound/settings/get`, `/api/dnsmasq/settings/get` | Runtime discovery (iii) | **stored** — `source_availability.state` and `provider.is_active`: the first says whether the resolver is configured on, the second which implementation `opnview` reads |
| `dnsmasq.log_queries` | `/api/dnsmasq/settings/get` | Data source 5, Degradation | **stored** — `source_availability.state`, as the `present_but_disabled` value. `opnview` reads this flag and never writes it |
| `timestamp` | `/api/diagnostics/log/core/resolver`, `/api/diagnostics/log/core/dnsmasq` | Data source 5, Response shape | **stored** — `dns_resolution.looked_up_at`, parsed from the row |
| `line` | `/api/diagnostics/log/core/resolver`, `/api/diagnostics/log/core/dnsmasq` | Data source 5, Response shape | **not stored** as text — the fields parsed out of it are, and the raw line is not: it would copy the firewall's log into `opnview`'s database row for row. *Its grammar is marked `UNVERIFIED:` in the survey*, so the parse is best-effort and its failures are a collection fault rather than a silent zero |
| `severity` | `/api/diagnostics/log/core/resolver`, `/api/diagnostics/log/core/dnsmasq` | Data source 5, Response shape | **not stored** — the syslog severity of the resolver process, a property of the log line and not of the lookup it describes |
| `process_name` | `/api/diagnostics/log/core/resolver`, `/api/diagnostics/log/core/dnsmasq` | Data source 5, Response shape | **not stored** — it names the daemon that wrote the line, which `dns_resolution.resolver` already records per row |
| response shape | `/api/unbound/overview/totals/<maximum>` | Data source 5, Endpoints | **not stored** — *the survey lists the endpoint and gives no response shape.* It is not called, and nothing can be stored from a shape nobody has read |

### Runtime discovery

Survey *Runtime discovery* (i) to (v).

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `identifier` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.identifier`, the configuration key and the identity of the row |
| `description` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.description`, never overwritten by `interface.user_label` and never overwriting it |
| `device` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.device` |
| `status` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.status`. Dropped until this pass; closed here, because an interface that is quiet and an interface whose link is down render identically without it. Stored verbatim: *the survey establishes the field and not its encoding* |
| `enabled` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.enabled`. Dropped until this pass; closed here for the same reason and stored verbatim for the same reason |
| `link_type` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.link_type`, normalised into `interface.link_kind`, from which `interface.is_tunnel` derives. Nothing in that chain reads a name |
| `vlan_tag` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface.vlan_tag` |
| `addr4`, `addr6` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **dropped** — the primary address of an interface has nowhere to live, so the installation's own address cannot be shown and no address history exists. Tracked as **G13** in `docs/widget-catalogue.md`, whose fix is an `interface_address` table with a first-seen and last-seen pair, not a column, and therefore not closed in this pass |
| `ipv4[]`, `ipv6[]` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **dropped** — the secondary addresses, with the same destination and the same gap **G13**. One interface can carry several, which is precisely why the fix is a table |
| `gateways[]` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **dropped** — which gateway sits behind an address is named in **G13**'s fix, and nothing holds it today |
| `routes[]` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **not stored** — the firewall's routing table. `opnview` reports what crossed the router and never how it decided to route it; no entry in `docs/widget-catalogue.md` shows a route |
| `macaddr` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **not stored** — the firewall's own adapter address. It identifies the router, not a machine on the network; `client.mac` is the column that names machines |
| device-to-description map | `/api/diagnostics/interface/get_interface_names` | Runtime discovery (i) | **stored** — `interface_map.device` and `interface_map.description`, the first of the two first-class join keys |
| `uuid` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.pf_label`. For legacy and automatic rules it carries the pf label, which is the same token the filter log reports as `rid`, so one column joins both kinds |
| `description` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.description` |
| `action` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.action`, read from the `%`-prefixed raw twin rather than from the localised field |
| `direction` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.direction`, read from the raw twin for the same reason |
| `log` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.logs_matches`. Dropped until this pass; closed here, because a rule that does not log makes its traffic invisible to `opnview` even though it crossed the router — the second observation-point limit, and otherwise indistinguishable from silence |
| `is_automatic` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.is_automatic` |
| `%action`, `%direction` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — these raw twins are what `rule.action` and `rule.direction` are written from; the human-facing fields are localised and are never read by machine logic |
| `enabled` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — it describes the ruleset as it stands now, while every row that joins to `rule` is a record of a packet that matched when it crossed. The two disagree on every historical row, and `rule` exists to give a `rid` a name rather than to mirror the current ruleset |
| `interface` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — which rules bear on a pair of interfaces is derived from the flows actually observed, not from a rule's configured interface list. Deriving it from configuration would name rules that never matched anything |
| `protocol`, `ipprotocol`, `%protocol`, `%ipprotocol` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — a rule's protocol criterion. `flow.protocol` carries the protocol actually observed, and `opnview` never evaluates a criterion |
| `source_net`, `source_port`, `destination_net`, `destination_port` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — the rule's match criteria, for the same reason: `opnview` joins an observed record to a rule identity and evaluates nothing |
| `categories` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — no entry in `docs/widget-catalogue.md` groups rules by category, and a category is a name, which nothing in this schema is allowed to classify by |
| `sort_order` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — the ordinal position of the rule in the ruleset, which changes on any edit and means nothing to a record written before the edit |
| `legacy` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — it says where the rule is defined. `rule.pf_label` joins both kinds, which is the only thing that matters here |

<!-- api-field-coverage:end -->

**What the sweep found.** Beyond the two instances that prompted it —
`blocklist`, closed earlier as G1, and `dnssec_status` — it found three more
dropped fields with an obvious use, all closed in the same pass and all of the
same family: they are what separates *nothing happened* from *we could not
see*. `reason` on the filter log, `log` on a rule, and `status` and `enabled`
on an interface. It also confirmed four drops that were already recorded as
gaps rather than forgotten (`ids.general.interfaces` as G8; `addr4`, `addr6`,
`ipv4[]`, `ipv6[]` and `gateways[]` as G13), and it turned up one contradiction
that is not a column at all: the polling plan polls the Insight time series
every 300 s and no table holds it.

## Purge

`sql/purge.sql` is the documented purge. It takes `:now`, reads the horizon
from `setting`, and removes rows older than it from every growing table:
`flow`, `dns_resolution`, `domain_attribution`, `security_event`, `dhcp_lease`,
`client`, `pair_volume_observation`, `geo_asn` and the eight aggregates — the
four keyed on interfaces and the four keyed on owners.
Bounded tables are never purged, so a surviving observation always joins to an
interface, a client, a rule and an interface-map entry — and, for a security
event, to the provider that contributed it and to the rule-info entry that
gives it a severity. `provider`, `provider_rule_info`, `source_availability`,
`owner` and `blocklist` are bounded by the installation, not by time, and are
never purged: purging a client removes the machine, never the person it was
attributed to, and purging a lookup removes the lookup, never the purpose
somebody assigned to the list that refused it.

Afterwards, `PRAGMA foreign_key_check` returns no rows: `domain_attribution`
is removed by the `ON DELETE CASCADE` of both its parents, and nothing else
points at a purgeable row.

With `retention_seconds = 0` the purge removes nothing at all. Both directions
are asserted by row counts before and after.

## The seven screen queries

All seven live in `sql/queries/screens.sql`, each labelled with its screen
name, each executable as written, each taking `:window_start` and `:window_end`
and — for two of them — `:interface_id` or `:client_id`. None of them contains
an address, a CIDR, an interface identifier, a device name, a VLAN name or an
interface description, and none assumes a count of anything.

| Screen | What it answers | Load-bearing index |
|---|---|---|
| Overview | totals for the period and the east-west versus north-south split, classified from interface membership alone | `idx_flow_observed_at` |
| Matrix | source interface by destination interface: volume, allowed connections, blocked connections, the matching rules, and how many connections matched no known rule | `idx_flow_observed_at` |
| Interface | the clients of one interface, their volume, their denials, their distinct destinations | `idx_flow_src_interface_observed_at` |
| Client | one client's flows with destination, country, operator and the inferred site name, returning unattributed flows rather than hiding them | `idx_flow_src_client_observed_at` |
| Blocked | the blocked timeline with the rule and the interface, each carrying an explicit unknown state when the join key resolves to nothing | `idx_flow_blocked_observed_at`, the partial index |
| Alerts | security events joined to provider, client and interface, with severity from the per-provider rule-info cache and an explicit unknown state on a cache miss | `idx_security_event_occurred_at` |
| Map | destinations by country and operator per source interface, read from the 24 h aggregate; also the aggregate-mode query, reading no domain name | `uq_volume_aggregate_24h_slot` |

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
Interface idx_flow_src_interface_observed_at
```
<!-- covering-queries:end -->

| Query | Index | Measures added for coverage |
|---|---|---|
| Overview | `idx_flow_observed_at` | `traffic_scope`, `action`, `packet_bytes`, `src_interface_id`, `dst_interface_id`, `src_client_id` |
| Matrix | `idx_flow_observed_at` | the same, plus `rule_id` and `rule_lookup_state` |
| Interface | `idx_flow_src_interface_observed_at` | `src_client_id`, `packet_bytes`, `action`, `traffic_scope`, `dst_address` |

The other four are **not** covered, and are not claimed to be:

- **Client**, **Blocked** and **Alerts** are detail listings, not aggregations.
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
scan: its `src_interface_id IS NOT NULL` clause lets SQLite fall back to a full
range of `idx_flow_src_interface_observed_at`, which is a whole-table read
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
