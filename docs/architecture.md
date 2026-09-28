# Architecture — the provider seam

`opnview` reads material from a firewall and stores it in one SQLite database.
This document names the seam between "the material" and "who supplied it", and
stops there. It is a seam document, not a plugin specification.

The authority on the schema is `internal/store/schema.sql`; on the sources,
`docs/opnsense-api-survey.md`; on the entities, `docs/data-model.md`.

## Six kinds

A **kind** is a contract `opnview` implements: a shape of material, with a
normalised destination in the schema. Exactly six exist, and the `provider`
table constrains its `kind` column to them.

<!-- provider-kinds:begin -->
```
dhcp_lease
dns_lookup
firewall_log
flow_volume
geo_asn
security_event
```
<!-- provider-kinds:end -->

| Kind | What it supplies | Normalised into |
|---|---|---|
| `firewall_log` | one record per logged packet decision | `flow`, and `blocked_event` over it |
| `security_event` | one record per detection | `security_event`, with `provider_rule_info` for severity |
| `flow_volume` | per-pair byte and packet counters | `pair_volume_observation` |
| `dhcp_lease` | one record per lease generation | `dhcp_lease`, feeding `client` identity |
| `dns_lookup` | one record per resolver lookup | `dns_resolution`, feeding `domain_attribution` |
| `geo_asn` | country, coordinates, ASN and operator per address | `geo_asn` |

A **provider** is one implementation of one kind. It is a row in the `provider`
registry, identified by `(kind, provider_key)`. Several providers of one kind
exist today: Unbound and Dnsmasq both supply `dns_lookup`; Kea, Dnsmasq and ISC
dhcpd all supply `dhcp_lease`.

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

## At most one provider per kind is active

Reachability and use are two different facts. A machine may have two
implementations of a kind installed and reachable at once, and the model must
say which one `opnview` actually read. That is `provider.is_active`, and **at
most one provider per kind is active**, enforced by a partial unique index over
`kind` where `is_active = 1`.

On a freshly migrated database **no provider is active**. Activeness is
determined by runtime detection at step 4, never by the schema: a schema file
has no way to know what is installed and must not pretend to.

How step 4 chooses which provider to activate — the selection policy — is not
decided here. This cycle models the marker.

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
