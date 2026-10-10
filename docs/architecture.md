# Architecture — the provider seam

`opnview` reads material from a firewall and stores it in one SQLite database.
This document names the seam between "the material" and "who supplied it", and
stops there. It is a seam document, not a plugin specification.

The authority on the schema is `internal/store/schema.sql`; on the sources,
`docs/opnsense-api-survey.md`; on the entities, `docs/data-model.md`.

## Eleven kinds

A **kind** is a contract `opnview` implements: a shape of material, with a
normalised destination in the schema. Exactly eleven exist, and the `provider`
table constrains its `kind` column to them. The resolver-cache cycle added the last
two (`specs/SPEC-resolver-cache-attribution.md`, decision 1).

<!-- provider-kinds:begin -->
```
dhcp_lease
dns_lookup
firewall_log
flow_volume
geo_asn
measurement_sample
public_suffix
reconciled_state
resolver_cache
resolver_local_data
security_event
```
<!-- provider-kinds:end -->

| Kind | What it supplies | Normalised into |
|---|---|---|
| `firewall_log` | one record per logged packet decision | `flow`, and `blocked_event` over it |
| `security_event` | one record per detection | `security_event`, with `provider_rule_info` for severity |
| `flow_volume` | per-pair byte and packet counters | `pair_volume_observation`, **derived from `flow` and collected from nowhere** |
| `dhcp_lease` | one record per lease generation | `dhcp_lease`, naming the server that issued each one, feeding `client` identity |
| `dns_lookup` | one record per resolver lookup | `dns_resolution`, feeding `domain_attribution` |
| `geo_asn` | country, coordinates, ASN and operator per address | `geo_asn` |
| `measurement_sample` | one numeric reading of one subject at one instant | `measurement_sample` |
| `reconciled_state` | the complete set of things of one type, as of one instant | `state_snapshot` and `state_item`, with `state_item_departure` over them |
| `public_suffix` | the rule set a registrable domain is computed from | **no row**: one list file on disk, read by `internal/publicsuffix` to group site names at read time |
| `resolver_cache` | the records a resolver holds in its cache — the answer addresses of the names it resolved, which the query report of `dns_lookup` does not carry | `resource_record_observation` (held in `cache`), feeding `domain_attribution`'s `resolver_cache_answer` method |
| `resolver_local_data` | the records a resolver serves from its own local data, host overrides among them | `resource_record_observation` (held in `local_data`), feeding host-name resolution (`dns_resolution.client_resolution = 'local_data_hostname'`) and the exact evidence of a lookup answered from Local-data |

**`measurement_sample` and `reconciled_state` are the survey's finding rather than a design instinct.** Of the
~34 data-producing sources the plugin ecosystem exposes, **eight of the ten that
fit an existing shape fit `measurement_sample`** — per-peer transfer counters,
frontend and backend counters, per-interface volume, UPS, SMART and sensor
readings — and it was not a kind: a table with no `provider_key`, no availability
row and no place in `provider.kind`. **Ten of the eleven that fit nothing at all
are one single shape**, the reconciled set. Counting only the log-shaped kinds,
two sources in the whole ecosystem add to them. See
`docs/opnsense-api-survey.md`, *What the plugin ecosystem actually exposes*.

`flow_volume` keeps its registry row and has no implementation, deliberately:
its destination is **computed from `flow`** by step 5, because the only per-pair
endpoint carries neither a port nor a protocol and the filter log carries both
exactly. `reconciled_state` has **no registry row at all**: a row is a claim that
an implementation exists, and no connector for a state-shaped source is written
yet.

`public_suffix`, added by step 5A, supplies **no row of observed data**. It is the
Public Suffix List, downloaded from publicsuffix.org — the third and last of the
outbound calls the project allows — and it is the rule set the registrable domain
of a site name is computed from, at read time, by `internal/publicsuffix`. It is a
kind rather than a helper because it has everything else a kind has: a provider,
an availability row that says whether a list is held, and failures to record (a
download that failed is a `collection_gap` with the reason `download_failed`,
while the list held since the last refresh stays in use). It is exclusive, because
two lists would be two answers to one question.

**`resolver_cache` and `resolver_local_data` are two kinds and not one more
method of `dns_lookup`**, because each has its own material, its own cadence and its
own availability: a resolver whose cache cannot be read still reports its lookups,
and the reverse, and a screen has to be able to say which of the two is missing.
"Cache" and "local data" are Unbound's own words (unbound-control(8), `dump_cache`
and `list_local_data`; unbound.conf(5), `local-data:`). Unbound implements both;
Dnsmasq is registered for both and reports the named state "this resolver offers no
cache read through the API" (or "no local-data read"), because OPNsense 26.7.3
exposes neither for it. Adding a resolver is one implementation file and one
registry row per kind.

A **provider** is one implementation of one kind. It is a row in the `provider`
registry, identified by `(kind, provider_key)`. Several providers of one kind
exist today: Unbound and Dnsmasq both supply `dns_lookup`, `resolver_cache` and
`resolver_local_data`; Kea, Dnsmasq and ISC dhcpd all supply `dhcp_lease`.

**No provider name appears in the schema as an identifier.** Not as a table
name, not as a column name. A provider name is a value — a registry row, or a
string in a row that references one. The single exception is
`eve_ingest_cursor`, which names a file format rather than a product, and which
is rewritten when a second ingesting provider is surveyed.

## A provider declares its own availability

Every registered provider carries exactly one row in `source_availability`,
holding `reachable`, `present_but_disabled` or `unavailable`, the instant that
was determined, and the probe that determined it.

**Availability is a modelled state, not an absence of rows.** No source on this
firewall can be told apart from silence by an empty result alone (survey, gap
11). A provider that is not installed, not running, or running with reporting
switched off is therefore recorded as being in that state and rendered as that
condition — never as "no traffic", "no alerts", "no clients" or "no names". A
provider with no availability row would be indistinguishable from a healthy
one, so the row exists from the first apply of the schema onwards, in the not-yet-probed
`unavailable` state.

## Activeness, and what is still exclusive

Reachability and use are two different facts. A machine may have two
implementations of a kind installed and reachable at once, and the model must
say which one `opnview` actually read. That is `provider.is_active`.

**One active provider per kind was the wrong universal rule.** People run
several detection engines side by side, and the how-to corpus is people stacking
them (survey, *Four findings that bear on the collectors already written*), so
three concurrent providers of the detection kind is the normal installation.
The partial unique index over `kind WHERE is_active = 1` is gone. What replaces
it is derived from the schema rather than chosen kind by kind:

> **A kind admits several concurrently active providers exactly when the
> identity of its destination rows includes the provider.**

Where the identity includes it, two active providers can neither collide nor
double-count: every row says who reported it, so one fact reported twice is two
attributed rows and a screen can show them per provider or side by side. Where
it does not, the second provider's rows are indistinguishable from the first's
by origin, so it would either collide with them or silently double a figure
nobody could decompose.

| Kind | Destination identity | Several active? |
|---|---|---|
| `firewall_log` | `flow.log_digest` | no |
| `security_event` | `(provider_id, provider_event_key)` | **yes** |
| `flow_volume` | `(day_start_at, endpoints, port, protocol)` | no |
| `dhcp_lease` | `(address, generation_key, provider_id)` | **yes** |
| `dns_lookup` | `dns_resolution.lookup_key` | no |
| `geo_asn` | `geo_asn.address` | no |
| `measurement_sample` | `(subject, measure, sampled_at, provider)` | **yes** |
| `reconciled_state` | `(provider_id, set_key, captured_at)` | **yes** |
| `public_suffix` | one list file on disk, no row | no |
| `resolver_cache` | `(provider_id, owner_name, rrtype, value, first_seen_at)` of `resource_record_observation` | **yes** |
| `resolver_local_data` | the same, of the same table | **yes** |

**`dhcp_lease` is concurrent, and the deployment is the ordinary one:** one
server issuing on one VLAN and another on a second, two scopes with no overlap.
The objection to it was that two active lease providers would put one machine on
the screen twice under two names, and the **client identity cascade** is what
answers it — the cascade keys on the DHCP client identifier first and on the MAC
second, and *neither is scoped to an interface*, so a machine leased on two VLANs
by two servers resolves to **one client holding two leases**, which is the truth.
The duplication the objection feared needs two servers issuing on the *same*
scope, and that is a misconfiguration of the firewall rather than a shape this
model should contort itself to absorb.

Its identity names the **provider** and not the backend, and the distinction is
load-bearing rather than pedantic: `backend` is a normalised vocabulary of
response *shapes*, and two providers could report the same one — a second
implementation reading a Kea running elsewhere would — so keying on it satisfied
the letter of the rule above and not its substance. The rule is stated in terms of
the provider, and so is the key.

`geo_asn` is exclusive because its row is one cache entry per address, and a
second provider would overwrite the first's answer rather than add to it.
`dns_lookup` is exclusive because `dns_resolution.lookup_key` carries no provider:
a lookup that transited two resolvers would be two records nothing could tell
apart from two lookups.

It is enforced by a partial unique index over `kind` where `is_active = 1` **and
the kind is not one of the six marked yes above**, and mirrored in `internal/store/kinds.go` so
the probe round can activate every qualifying provider of a concurrent kind
instead of discovering the constraint by failing. The database is the authority,
and a test reads the index's own definition back and asserts the two agree.

On a freshly applied schema **no provider is active**. Activeness is determined
by runtime detection, never by the schema: a schema file has no way to know what
is installed and must not pretend to. For an exclusive kind, two candidates the
firewall's own configuration does not separate activate **neither**, and the
ambiguity is recorded on both rows; for a concurrent kind there is nothing to
decide, so every candidate is activated.

## A future provider's event shape will be surveyed, not guessed

`security_event` carries what one surveyed provider actually supplies, plus the
two things any provider must have: an identity of its own
(`provider_id`, `provider_event_key`) and a rule identity as text, so a provider
whose rules are named rather than numbered fits without a schema change. It carries
no column invented for a provider nobody has installed.

When a second provider of any kind is actually installed, **its event shape
will be surveyed exactly as the OPNsense API was surveyed at step 1**, and the
schema will be extended against observed data. An abstraction designed before
seeing the data is wrong; step 1 already produced one, for Suricata `tls` and
`dns` events that turn out not to exist.

The rule for provider-specific data that does not fit the core is in
`docs/data-model.md` under *The attribute rule*: a typed, indexed column for
anything a screen aggregates or filters on; a JSON column, displayed only, for
anything merely shown; never an entity-attribute-value table.

## What this is not

`opnview` is one Go binary. There is **no plugin system**: no dynamic loading,
no shared object, no manifest file, no external process, no registry of code.
A kind is implemented by code compiled into the binary. The registry described
above is a table of rows saying which implementations exist and which one is
read — it is data about providers, not a mechanism for loading them.
