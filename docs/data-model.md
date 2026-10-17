# Data model and SQLite schema

Step 2 of `ROADMAP.md`. What each entity represents, which step-1 finding
shaped it, which OPNsense endpoint and API field feed it, how it is identified,
how retention treats it, and which indexes it carries.

The authority on the sources is `docs/opnsense-api-survey.md`, pinned to
OPNsense 26.7.3. Every endpoint named below is cited there, with its URL; the
citation is not repeated here. The authority on the schema is
`internal/store/schema.sql`, and the authority on the queries is
`sql/queries/screens.sql`.

**There are no migrations, and that is a decision rather than an omission.**
The one deployment is the maintainer's live validation installation, whose
history is re-collectable from the firewall, so numbered files, a `schema_version`
table and a runner would be machinery for a problem that does not exist. The
schema is one file, edited in place, embedded into the binary by
`internal/store` and applied on every start; every statement in it is
idempotent, which is what makes that safe and what replaces the
`schema_version` bookkeeping. `sql/schema-checks.sh` asserts the idempotence in
a stricter form than the old check it replaces: a second apply must change no
row count in any table, so a restart cannot re-insert a default over a setting
somebody changed or reset a probed availability row. **Migrations begin the day
the product runs somewhere with data worth keeping** — that file becomes the
baseline and the first real migration is the one after it. `internal/store`
also holds `purge.sql`, for one reason: Go's embed directive cannot reach
outside its own package directory, and a second copy elsewhere would be a
second truth. This document explains them; it does not restate
them, so the two cannot drift. `docs/architecture.md` names the eight provider
kinds and the seam a provider plugs into; this document is where each entity
is written down.

**Idempotent is not the same as upgradable, and step 5B is where that shows.**
`CREATE TABLE IF NOT EXISTS` leaves a table that already exists as it is, so a
column added in place never reaches a database an earlier build wrote. Step 5B
added `source_availability.outcome`, `rule.destination_net`,
`dns_resolution.client_family` and the tables `opnview_heartbeat` and
`opnview_downtime`; a database from `a1e2707` or earlier therefore does not open
under it, and `store.Open` refuses it with `ErrSchemaOutdated` rather than
leaving SQLite's "no column named outcome" to be decoded. The deployed database
is rebuilt, never migrated, and `ROADMAP.md`, step 5, *Rebuilding the database
after step 5B*, says how.

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
from resolver correlation, by one of two methods, and the method is recorded on the row
(`domain_attribution.method`; specs/SPEC-resolver-cache-attribution.md, which
amends this paragraph). `resolver_cache_answer`: a lookup made by a client, and the
resolver's cache showing the flow's destination among the answers of the name it
looked up over the whole interval from the lookup to the flow. `lookup_timing`, the
5A method and the fallback: a lookup made by a client shortly before a flow from
that client, with no other name looked up in the window. Both remain inferences:
the cache shows the client could have been told the destination when it asked for
the name, not that the flow was for it. Suricata exposes no `dns` event type, and
its `tls` and `http` events, although they can be written to `eve.json`, cannot be
read back through the API (survey, gaps 1 and 2), so the resolver is still the only
source of site names. Client-side caching, shared content networks, DNS-over-TLS and
DNS-over-HTTPS each break the inference, and a client using an external encrypted
resolver can only be named by address, country and operator.

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
| what answered a lookup | `source` (`Recursion`, `Local`, `Local-data`, `Cache`) | `dns_resolution.answer_source` | same endpoint, field `source`, values read from Unbound's logging and the recorded fixtures (survey, data source 5); prefixed because `source` alone would read as the data source |
| an interface's own addresses, and the gateways behind it | `addr4`, `addr6`, `ipv4`, `ipv6`, `gateways` | `interface_address.source_field` holds the field name verbatim | `/api/interfaces/overview/interfaces_info` (survey, *Runtime discovery* (i)) |
| the address range an interface's address and prefix length cover | **network** — the rule editor's "LAN net", the internal alias for "LAN network" | `interface_network` | <https://docs.opnsense.org/manual/aliases.html>, *internal aliases*; the survey's *Runtime discovery* (i) called it the subnet source, and OPNsense's own word is *network* |
| the interface a rule applies to | `interface` | `rule.interface` | `/api/firewall/filter/search_rule`, field `interface`: configuration keys on a model rule, descriptions on a legacy row (survey, *Runtime discovery* (ii)) |
| whether a rule is a legacy row | `legacy` | `rule.legacy` | same endpoint, field `legacy` |
| the domain a site name belongs to, one label below its public suffix | **registrable domain** (the Public Suffix List's own term) | `publicsuffix.List.RegistrableDomain`, computed at read time and never stored | <https://publicsuffix.org/list/>, *formal algorithm* |
| a reading of one local address and one peer on one interface | the `traffic/top` record's `address` and its `details[]` entries | `measurement_sample.subject_kind = 'interface_endpoint_pair'`, key `device local peer` | survey, *What `traffic/top` measures, read from source for step 5*. The kind name is `opnview`'s; the three parts are the endpoint's |
| a gateway's delay, its deviation and its loss | `delay`, `stddev`, `loss` | `delay_milliseconds`, `delay_stddev_milliseconds`, `loss_ratio` under `subject_kind = 'gateway'` | `/api/routes/gateway/status` (survey, *Gateway status, read from source for step 5*); the measure names carry the unit because the endpoint writes it into the text (`1.2 ms`, `0.0 %`) |
| the records a resolver holds and has not expired | **cache** — Unbound's own word: `unbound-control dump_cache`, "The contents of the cache is printed" (unbound-control(8)); OPNsense's action is `dumpcache` | `provider.kind = 'resolver_cache'`, `resource_record_observation.held_in = 'cache'` | `/api/unbound/diagnostics/dumpcache` (survey, *Resolver cache and local data, read from source for the resolver-cache cycle*) |
| the records a resolver serves from its own configuration, host overrides among them | **local data** — unbound.conf(5), `local-data:`; unbound-control(8), `list_local_data` | `provider.kind = 'resolver_local_data'`, `resource_record_observation.held_in = 'local_data'` | `/api/unbound/diagnostics/listlocaldata` (same section) |
| one record of either: its owner name, its type and its data | **resource record**, **owner name**, **RRtype** (RFC 1034, section 3.6; RFC 1035, section 3.2); the endpoints' `host` (dump) or `name` (local data), `rrtype` and `value` | `resource_record_observation` and its `owner_name`, `rrtype`, `value` | same two endpoints; the owner name is lower-cased and loses its trailing dot (RFC 4343), and `rrtype` and `value` keep the endpoint's names |
| the record's class | `type` in both endpoints — the class `IN`, **not** the record type | not stored: every record either endpoint returns is of class IN, and the field is never read as the type | `scripts/unbound/wrapper.py`, whose regular expression captures `(IN)` into `type` |
| the seconds a cached record has left | `ttl` (a string in the dump) | read into `resource_record_observation.covered_until_at` as the poll's instant plus it | same endpoint |
| the firewall's own addresses, as a source or a destination | **This Firewall** — the rule editor's name for pf's `self` | `this_firewall`: `flow.src_is_this_firewall` / `dst_is_this_firewall`, `security_event.src_is_this_firewall`, `dns_resolution.client_is_this_firewall`, the `this_firewall_address` view, `traffic_scope = 'this_firewall'`, the directions `to_this_firewall` / `from_this_firewall`, the tree node `this_firewall`, and `blocked_decision.target_is_this_firewall` before them | <https://docs.opnsense.org/manual/how-tos/caddy.html>: "Create Firewall rules that allow `HTTP` and `HTTPS` to destination `This Firewall` on `WAN`"; `opnsense/core` 26.7.3, `src/opnsense/mvc/app/library/OPNsense/Firewall/FilterRule.php`, where the stored value `(self)` is rendered as `gettext("This Firewall")`, and `Firewall/Rule.php`, which passes `(self)` to pf unchanged; pf.conf(5), FreeBSD 15.1-RELEASE: "self Expands to all addresses assigned to all interfaces." The *manual/firewall.html* page does not carry the phrase |
| the program that wrote a syslog line | **program** -- syslog-ng's filter `program()` and the target form's *Applications* (survey, *Push sources*, S1, S3) | a syslog push entry's `filter.program` | RFC 3164's TAG and RFC 5424's APP-NAME |
| who sends syslog, who receives it | RFC 5424's **originator** and **collector** | the `sender` of a pushed message; `opnview` is the collector | RFC 5424, section 3 |
| who sends NetFlow and its records | RFC 7011's **Exporter**, **Collector**, **Observation Domain**, **Information Element** | `exporterIPv4Address`, `observationDomainId`, the IANA registry's element names | RFC 7011, section 2; RFC 3954 |

**Fifty-seven terms are `opnview`'s own, because OPNsense has no word for them.**
Each is marked as such where it is defined, so nobody later mistakes it for
vocabulary read off an endpoint:

| Term | Why OPNsense has no word | Why this one |
|---|---|---|
| `interface.user_label` | OPNsense carries a single description field, editable only on the firewall | a label the maintainer sets inside `opnview` needs a column a discovery refresh cannot overwrite |
| `interface.link_kind` | OPNsense publishes the raw `link_type` and no normalised class of it | `opnview` needs a closed vocabulary to derive `is_tunnel` from something other than a name |
| `owner` | no product `opnview` reads has any notion of a person; a lease names a host, not a human | the entity answers "whose machine is this", and the word is taken from that question |
| `blocklist.purpose` | the endpoint reports what a list is **called** and nothing about what it is **for**; OPNsense publishes no classification of a subscribed list | the column answers "advertising or threat", which is the maintainer's question, and it is assigned by the user for the same reason `owner` is: `hagezi-pro` and `oisd-small` tell a machine nothing |
| `collection_gap` | no product `opnview` reads has a notion of the data it failed to deliver: every source returns what it has and says nothing about what it did not (survey, gap 11) | the word is taken from the question the row answers — what is missing from this history — and the row exists because a gap is read by a screen rather than by a log reader |
| `measurement_sample` | OPNsense answers for processor, memory, temperature, disk, uptime and per-interface counters and publishes no collective noun for them; nor does it have a word for a per-pair volume `opnview` has to sample because no aggregate of it exists upstream | the two turned out to be the same five facts — a subject, a measure, a unit, a value and an instant — so one term names one table, and `subject_kind`, `measure` and `unit` are vocabularies of `opnview`'s own because the survey establishes those endpoints and **not their field names**. Whether their value sets are open or closed is a property of the schema and is stated there, beside the `CHECK`s that do or do not enumerate them |
| `dns_resolution.lookup_key` | the resolver returns a `uuid` field and it is null on every row, so the endpoint supplies no identity for a lookup at all | the column holds an identity `opnview` composes from the row's own content, so it is named for what it **is** — a key — rather than for the uuid it is not, and not `digest` either, because a provider that does supply a stable identifier stores that here unchanged |
| `reconciled_state`, and with it `state_snapshot`, `state_item` and their `set_key` / `item_key` / `captured_at` | the shape is real and unnamed: ten of the eleven surveyed sources that fit no other kind report **the complete current set of things of one type**, and each of them names only its own contents — decisions, peers, neighbours, routes, circuits. None of them, and no OPNsense endpoint, has a word for the shape itself | the word is taken from what those ten have in common: the set is **reconciled** against the previous one rather than appended to. `captured_at` is named for the claim it makes — the set was complete at that instant — because that claim is the only thing that makes a departure detectable; `set_key` and `item_key` follow the `provider_key` / `subject_key` pattern already in the schema |
| `state_item_departure` | no source reports a departure. Every one of them reports only what it currently holds, and says nothing at all about what it used to hold (the same finding as survey gap 11, arriving from a different direction) | the view is named for the question it answers — what has left this set — and it is a view rather than a column because a departure is a fact about two snapshots, and a stored copy could disagree with them |
| `account` | OPNsense has **users**, and it has **API keys** issued to a user. Neither names a login into a *different* product: an `opnview` account authenticates nobody to the firewall and grants nothing on it | the word is the ordinary one for the thing, and it is deliberately **not** `user`, so that nothing in this schema can be mistaken for the OPNsense user whose API key `opnview` holds. The password columns carry the Argon2id parameters beside the digest because `ROADMAP.md` asks for exactly that; see `account`'s own section |
| `session` | no product `opnview` reads has a notion of a browser signed in to `opnview`. OPNsense's own web session is its own and is never read here | the word is taken from what the row is. Its primary key is a **digest** of the token rather than the token, so the token exists only in the cookie; `expires_at` is named for the absolute cap it holds, and the sliding idle window is deliberately **not** a column — see that section |
| `encrypted_credential` | OPNsense names an **API key** and an **API secret**, and MaxMind a **licence key**, but none of them has a word for *a credential this product holds at rest and has to be able to send again*. `setting` cannot be that word: a ciphertext is not a value | the term names what the row is — a credential, encrypted — and the four columns beside the ciphertext (`algorithm`, `key_id`, `nonce`, `updated_at`) are what make it openable and what let a replaced key be told apart from a tampered ciphertext. `name` holds which credential it is, as data, exactly as `provider_key` does |
| `traffic_direction` (`outbound`, `inbound`, `inter_interface`, `to_this_firewall`, `from_this_firewall`) | pf's `dir` is **per interface**: a packet leaving a client for the Internet is `in` on the client's interface and `out` on the upstream one, so no OPNsense field says which way a flow crossed the firewall as a whole | the first three words are the widget catalogue's scopes. They are derived from interface membership by the `classified_flow` view — the destination outside is outbound, the source outside is inbound, neither is between interfaces — and pf's `dir` enters only to break the tie when both ends are outside. The last two are named after OPNsense's *This Firewall* and say that one end is the firewall itself (decision 6 of the step-5A live corrections). `flow.direction` stays pf's own value |
| `measurement_sample.subject_kind = 'interface_endpoint'` | `traffic/top` reports, per interface, records keyed by a local `address`, each carrying totals over all its peers (`rate_bits_in`, `rate_bits_out`, `cumulative_bytes_in`, `cumulative_bytes_out`) beside a `details[]` list of peers; neither OPNsense nor `iftop` names the record itself | the record is one local address as read on one interface, so the term is the pair subject without its peer, keyed `device local`. It exists because the local address's sending is reported ONLY as that total, never per peer: without it a client talking to several peers would have no outbound figure at all (survey, *What `traffic/top` measures, read from source for step 5*) |
| `interface.is_upstream` | OPNsense applies "upstream" to a **gateway** (*System > Gateways*, "Upstream Gateway") and has no word for the interface a gateway sits behind | the widget catalogue's "upstream interface". It is 1 exactly when `interfaces_info` reported a non-empty `gateways[]` for the interface, and nothing else decides it: no name, description or link type |
| `blocked_decision.engine_kind` | each engine records its own refusal in its own words — pf a `reason`, Unbound a `blocklist`, Suricata an `alert.action` — and nothing names the category of engine that decided | a closed vocabulary, listed under *`blocked_decision`* below, with one value per way a refusal can be told apart, so every refusal has exactly one place and none is guessed into another |
| `interface_network.origin` (`detected`, `operator`) and `removed_at` | OPNsense configures one address and prefix per interface and has no notion of a network another program proposes and an operator confirms, overrides or removes | `detected` is what discovery proposed from the addresses `interfaces_info` reports, `operator` what the operator added or confirmed; `removed_at` keeps a removed network so discovery cannot propose it back. Which of them count is the maintainer's decision D1, under *Classification* |
| `interface.link_local_evidence` | an IPv6 link-local prefix is the same on every interface (RFC 4291, section 2.5.6), and OPNsense has no setting that ties one to an interface for any purpose `opnview` has | named for what it decides: whether a link-local address seen on the interface is **evidence** of membership. The default is no |
| `purged_flow_hour`, the **purged part** of an hour | the firewall keeps no history `opnview` reads, and has no word for what a retention purge removed | the table holds, per hour and per every key an aggregate family reads, the figures of the flows the purge removed from that hour, so an hour holding the horizon keeps them while it still takes in the flows that remain. "Purged" is the purge's own word (*Purge* below) |
| `address_classification` and its `evidence` fingerprint | classification is `opnview`'s own derivation; nothing upstream places an address | the row records the evidence an address was last placed from — the kind of evidence, the interface, the strongest identity — so a pass places in full only an address whose evidence changed. "Fingerprint" is the word the discovery pass already used for the same kind of record |
| `dns_resolution.client_resolution` | the resolver's report silently replaces a querying address by its reverse-lookup name (`opnsense/core` 26.7.3, `scripts/unbound/stats.py`), and says nothing about having done so | the column says what `client` held — an address, or a host name resolved through the leases to one address, to several, or to none, or `localhost`, which names this firewall (`this_firewall_hostname`) — so a lookup that named no single machine is told apart from one that named an address |
| `flow.pair_outcome` (`first_leg`, `second_leg`, `not_paired`) and `flow.paired_flow_id` | pf logs the packet that creates a state on every interface a logging rule creates one on, so one connection is two records; neither pf nor OPNsense has a word for one record being the other's partner, nor for an unpaired record the firewall itself sent | a **leg** is one record of a connection crossing the firewall, as a leg is one stretch of a journey: the first leg is the `in` record on the interface the connection arrived on, the second leg the `out` record on the one it left by. `not_paired` is the only outcome of an unpaired upstream `out` record sourced by this firewall, decision 3 of the step-5A live corrections as amended on 7 October 2026: opnview never claims such a record is the firewall's own traffic; see *Two legs of one connection* |
| `classified_flow.exit_leg_device`, `volume_aggregate_*.exit_leg_device`, `purged_flow_hour.exit_leg_device` | the same: nothing upstream marks the record a connection left the firewall by | the device a second leg was logged on — the interface the connection **exits** by — kept apart in the volume family so a paired connection counts once in every total and still counts on that interface's own figures |
| `dhcp_lease.hostname_label` | a lease reports a `hostname` and nothing more; neither OPNsense nor the DHCP servers name its first label | **label** is DNS's own word for one dot-separated part of a name (RFC 1035, section 2.3.1); the column is the first label, lower-cased, a trailing dot removed, which is what a host name the resolver logged is matched on |
| `dns_resolution.hostname_examined_at` | nothing upstream knows that a lookup's logged host name was resolved again | the instant a lease pass last examined the lookup's host name; `NULL` is the lookup still waiting for one, which is what makes the retry survive a restart and stay bounded |
| `peer_volume_aggregate_*` and its `peer_address` | no OPNsense figure is keyed by the address at the other end of a client's flows | "peer" is the word `client_volume_aggregate_*.distinct_peers` already used for that address; the family exists so that count is exact in every period. For an outbound or an inbound flow the peer is the volume family's `peer_address`, the outside end; between two interfaces it is the other inside address |
| `dhcp_lease.generation_key` | OPNsense's lease endpoints report an expiry, and only Kea reports a validity start. None of them reports an identity for a lease **generation**, which is the thing `opnview` needs one row per | the column is named for what it identifies — one generation of one lease — and its value carries the name of what it rests on (`start:`, `expiry:`, `observed_day:`), so a reader can tell a real start from a substitute without consulting the backend. It exists because `starts_at` was doing this job as well as its own, and was honest only because a comment beside it said so |
| `domain_attribution.method` (`resolver_cache_answer`, `lookup_timing`) | neither OPNsense nor Unbound infers a site name, so neither has a word for how one was inferred | the maintainer's decision 5 of the resolver-cache cycle: `resolver_cache_answer` says the evidence is the resolver cache's **answer** to the name looked up, and `lookup_timing` says the evidence is only the **timing** of the lookup before the flow. The column reverses 5A decision D3, which had refused a column with one possible value |
| `domain_attribution.address_observation_id` | nothing upstream names the record an inference rests on | the A or AAAA observation whose address is the flow's destination: the **address observation** a `resolver_cache_answer` matched, named so the evidence can be checked |
| `dns_resolution.client_resolution = 'local_data_hostname'` | the query report says nothing about having replaced an address by a name, and nothing about where else the name could be resolved | decision 5 again: the host name was resolved through the resolver's **local data**, Unbound's word, after no lease named it; it sits beside `lease_hostname`, which names the leases the same way |
| `dns_resolution.client_resolution = 'dual_stack_hostname'` | none: neither OPNsense nor Unbound names a host held under one address of each family | decision 15 of `specs/SPEC-step-5b-widget-api.md`: *dual stack* is the RFC 4213 term for a host with both an IPv4 and an IPv6 address, which is exactly what the local data says the name is; `_hostname` keeps it beside the other states of a logged host name |
| the **coverage interval**, `resource_record_observation.covered_from_at` and `covered_until_at` | the dump gives what the cache holds at one instant and the seconds each record has left; nothing says since when a record was held | the interval over which the polls allow the resolver to have held the record: from the last successful poll before the first poll that saw it, to the latest poll plus its `ttl` (the latest poll, for local data). It is a derivation of the polls, and "covered" says that rather than claiming the record was held throughout |
| `resource_record_observation.held_in` (`cache`, `local_data`) | the two endpoints return the same shape and nothing says which part of the resolver a record lives in | the part of the resolver the record was held in, in Unbound's own words; it repeats the provider's kind because every search starts from it |
| `resource_record_observation.host_label` | a local-data record carries a name and nothing more | **label** is DNS's word (RFC 1035, section 2.3.1), and the column is the first label of the host name the record names -- the owner of an A or AAAA record, the target of a PTR record -- exactly as `dhcp_lease.hostname_label` is for a lease |
| `resource_record_read` | no endpoint says when it was last read successfully | the last successful **read** of one provider's records, kept so the start of a coverage interval survives a failed poll and a restart |
| **outage**, the count the top bar's indicator shows | OPNsense reports the state of each of its own services and has no notion of a source `opnview` reads failing to answer it; `source_availability` records a state per provider, not whether the operator has to look | an outage is a kind none of whose providers selected for reading is reachable, or a provider the operator turned on that is not reachable; a provider not yet probed is never one (decision 4 of `specs/SPEC-deferred-interface-bundle.md`). It is computed when a page is drawn and never stored |
| `source_availability.outcome` and its codes | OPNsense answers with an HTTP status and a body, and names no outcome of a probe as a whole: a 401 and a 403 are two statuses, "the service reported stopped" and "this resolver offers no cache read" are not statuses at all | **outcome** is the word the client already used for what one call turned into (`opnsense.Outcome`); the column carries it to the whole probe or read, from a closed set of codes listed under *`source_availability`*. Each code is the collector's own distinction made machine-readable, HTTP's where one applies (`not_found`, `denied` for 403, `authentication_failed` for 401), so no reader parses `detail` (SPEC-step-5b-widget-api.md, D1) |
| `opnview_downtime`, an interval during which **opnview was not running** | no product `opnview` reads knows that its reader was absent: the firewall keeps serving, and the next poll is silent about what rotated out meanwhile | named for the fact recorded -- opnview not running -- rather than for any one source, because every source went unread at once; prefixed `opnview_` so it cannot be read as the firewall's own downtime, which no table here records |
| `opnview_heartbeat` | the same: nothing upstream records that `opnview` was alive | **heartbeat** is the ordinary word for a periodic proof of life; the one row holds the last instant the service was known to run and whether it stopped cleanly there |
| the **broker** (`internal/broker`) | OPNsense has an API and no notion of a reader of it that sits between other readers and the firewall | **broker** is the ordinary word for one party that every request goes through and that serves many from one source; it is the only caller of the firewall API (A9 of the broker amendment to `specs/SPEC-backend-completion-plugins.md`) |
| the **mirror**, the `opnsense` schema | the firewall keeps its own state and history, and has no word for a reader's copy of its answers | **mirror**, because it holds what the API answered and nothing derived: the fields some reader read, under the API's own names, learned from the answers (A10) |
| a **datum** (`opnsense.mirror_datum`) | an endpoint answers one request at a time; nothing names "one endpoint called with one set of identifying parameters, on one firewall" | **datum**, the thing the broker refreshes, serves and records reads of; two requests differing only by a page or a window are one datum (A10) |
| the **max age** of a request | OPNsense answers now, every time, and has no notion of an answer young enough | **max age** is HTTP's own word for how old a stored answer may be and still be served (RFC 9111, the request directive `max-age`), and it means that here (A11) |
| the **freshness floor** of an endpoint | nothing upstream says how often an endpoint may usefully be asked | the shortest interval an endpoint is refreshed at -- the largest of its declared floor and its median latency -- below which a request is served at the floor's age (A12) |
| **live interest**, the lease a request for a live figure renews (`live_interest_seconds`) | OPNsense's dashboard polls the firewall itself and has no notion of a reader asking a third party for a live figure | it says what the lease stands for: somebody's interest in a live figure, which lapses when nobody renews it |
| the **live cadence** of a live pass, and the **live pass** itself | OPNsense has one cadence per widget of its own dashboard and no notion of a second pass that runs only while someone looks | **live** is the word the mockup's strip and the spec use; the cadence is the declaring plugin's setting (`docs/plugins.md`, *Live pass*) |
| a **fine sample**, `measurement_sample.live_cadence_seconds IS NOT NULL` | nothing upstream keeps a reading at all, so it has no word for two resolutions of one | **fine** against **baseline**: a sample taken at the live cadence, kept `live_sample_retention_seconds`, beside the samples of the measurement interval |
| a **push source**, and a push declaration's `push` entry | OPNsense names a remote syslog **target** (*System > Settings > Logging*, *Remote*) and a NetFlow **destination**, which are the sending side; it has no word for the receiving side's subscription | **push** is the roadmap's own word for data the firewall sends rather than data `opnview` asks for (`docs/plugins.md`, *Push sources*); the manifest key is `push`, because `stream` and `provider` already mean something else there |
| a **source period**, `provider_source_period` | nothing upstream knows that one provider's records reached `opnview` by two paths: no product names the span during which one delivery path (pull or push) delivers a provider's records | `opnview`'s own term, chosen by the maintainer (11 October 2026): an interval of a provider's history and the one path -- `pull` or `push` -- whose records it keeps. **Period** because it reads "from one instant to another"; it was first called a *source segment*, a word the schema no longer carries since `segment` was retired (below). Periods never overlap, which is what keeps a record delivered by both paths from counting twice |
| a push entry's **liveness** (`liveness_seconds`) | OPNsense sends and knows nothing of whether anybody receives | a push entry is **alive** while messages it accepts arrived within its span; the word is the ordinary one for that test |
| `push_listener` | nothing upstream records that `opnview` was listening | the last instant each push listener was heard alive, so the interval it was not is a gap |
| a **flow record**, `flow_record` | NetFlow v9's and IPFIX's **Flow Record** (RFC 3954, RFC 7011) | `flow_record`, kept although a reader may take it for a `flow` (Q16 (a)): **a `flow` is one filter-log record, a `flow_record` one record an exporter sent**, and the two are different things joined by the NetFlow join |
| the **Observation Point** of a flow record | RFC 7011's **Observation Point**; OPNsense exports none (survey, *Push sources*, S11) | `flow_record.observation_point_identifier` and `_device`: the one interface the capture configuration in force names, when it names exactly one; never inferred from a record |
| the **capture configuration**, `capture_configuration` | OPNsense's *Reporting > NetFlow* settings, the `OPNsense.Netflow` model's `capture` | what an exporter captures on and in which directions, as one read found it; `opnview`'s word for the read, since the settings page has no name of its own beyond *NetFlow* |
| **forward** and **reverse** | RFC 5103's terms for the two directions of a biflow | a record's orientation to the leg it joined: forward is the leg's logged tuple, reverse its swap; and a connection's two directions, as its first leg logged them |
| the **measured volume** of a connection, `connection_measured_volume`, and the **`netflow` measure** | none: no product names the volume of a logged connection as an exporter counted it | **measured**, against **logged**: the bytes and packets the NetFlow export reported for a logged connection, each direction once, at the single Observation Point; served as the measure `netflow`, never summed with `logged` |
| an **unmatched record**, `unmatched_record_aggregate_*` | none: nothing upstream joins flow records to logged connections | a flow record with an Observation Point that joined no leg, with its reason (`no_logged_leg`, `ambiguous`, `beyond_flow_horizon`) |

**What this replaced.** `segment` was invented before any research existed and
was never reconciled with what OPNsense calls the thing. It is gone: the entity
is `interface`. `device` meant two things at once — the network device on an
interface, and a machine on the network — and now means only the first; the
second is `client`. Nothing is deployed, so both renames were made in the
schema in place rather than added as a migration.

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
appears as a literal anywhere in the schema, its defaults, its
constraints, its indexes, the seven screen queries or the seed. There is no
`LIKE`, `GLOB` or `REGEXP` predicate in the DDL at all, so nothing can classify
an interface, a client or a rule by what it is called.

**Zero assumed counts.** Nothing in the schema or in the queries presumes a
number of interfaces, clients, owners, rules or address families. The checks prove
it by re-running every query against a second seed built with different counts.

**Durability: `synchronous_commit = on`.** The store is PostgreSQL, and the
installation's database runs with its default durability: `synchronous_commit = on`,
with `fsync` and `full_page_writes` on, none of them overridden by
`docker-compose.serve.yml` or the service it extends. A commit returns only once its
write-ahead log is on disk, so a committed transaction survives an application crash,
an operating-system crash and a power loss alike
(<https://www.postgresql.org/docs/17/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT>);
what a crash can cost is the work not yet committed. The trade the resolver-cache cycle
approved for SQLite (`specs/SPEC-resolver-cache-attribution.md`, scope G: a commit
synced at the next checkpoint rather than at once) went with that engine, and none is
made here. Only the everyday test instance turns durability off, because the tmpfs
under it loses everything at a stop anyway (`docker-compose.yml`, `db-test`).

## Entities

Exactly these entities exist. `blocked_event`, `blocked_decision`,
`classified_flow`, `state_item_departure` and `this_firewall_address` are views;
everything else is a table.

<!-- entity-list:begin -->
```
account
address_classification
applied_schema
blocked_decision
blocked_event
blocklist
capture_configuration
capture_interface
classified_flow
client
client_volume_aggregate_1h
client_volume_aggregate_24h
client_volume_aggregate_30d
client_volume_aggregate_7d
collection_gap
connection_measured_volume
dhcp_lease
dns_resolution
domain_attribution
domain_volume_aggregate_1h
domain_volume_aggregate_24h
domain_volume_aggregate_30d
domain_volume_aggregate_7d
encrypted_credential
eve_ingest_cursor
flow
flow_record
flow_record_condition
geo_asn
interface
interface_address
interface_map
interface_network
measurement_sample
opnsense.mirror_column
opnsense.mirror_correction
opnsense.mirror_datum
opnsense.mirror_endpoint
opnsense.mirror_gap
opnsense.mirror_parameter
opnsense.mirror_read
opnsense.mirror_schema_version
opnsense.mirror_table
opnview_downtime
opnview_heartbeat
owner
owner_reassigned_hour
owner_volume_aggregate_1h
owner_volume_aggregate_24h
owner_volume_aggregate_30d
owner_volume_aggregate_7d
pair_volume_observation
peer_volume_aggregate_1h
peer_volume_aggregate_24h
peer_volume_aggregate_30d
peer_volume_aggregate_7d
plugin_aside_table
plugin_state
plugin_table
provider
provider_rule_info
provider_source_period
purged_flow_hour
push_listener
resource_record_observation
resource_record_read
retention_purge
rule
rule_volume_aggregate_1h
rule_volume_aggregate_24h
rule_volume_aggregate_30d
rule_volume_aggregate_7d
security_event
session
setting
source_availability
state_item
state_item_departure
state_snapshot
this_firewall_address
unmatched_record_aggregate_1h
unmatched_record_aggregate_24h
unmatched_record_aggregate_30d
unmatched_record_aggregate_7d
volume_aggregate_1h
volume_aggregate_24h
volume_aggregate_30d
volume_aggregate_7d
```
<!-- entity-list:end -->

**There is no TLS or HTTP observation entity**, and there will not be one until
a release of OPNsense makes those records readable. The schema carries nothing
no source can fill.

**`blocked_event`, `blocked_decision`, `classified_flow`,
`state_item_departure` and `this_firewall_address` are views**, and all for the
same reason: each states something derivable from rows that already exist, and a
stored copy could disagree with them. A blocked event is a `flow` with a different
action; a blocked decision is any refusal of any engine, placed once; a classified
flow is a `flow` with its direction, inside end and outside end written once as
expressions; a departure is a fact about two consecutive snapshots of one set; and
this firewall's addresses are `interface_address` under the four fields that
report an address the firewall itself holds, with whether discovery still reports
each one.

### Growing and bounded

A growing table accumulates one row per observation and is unbounded in time. A
bounded table holds one row per object that exists on the firewall, or per
configuration key, and its size is set by the installation rather than by how
long opnview has been running. The distinction is load-bearing: the query-plan
criterion forbids a full scan of any growing table, and permits one of a
bounded table.

<!-- growing-tables:begin -->
```
address_classification
client
client_volume_aggregate_1h
client_volume_aggregate_24h
client_volume_aggregate_30d
client_volume_aggregate_7d
collection_gap
connection_measured_volume
dhcp_lease
dns_resolution
domain_attribution
domain_volume_aggregate_1h
domain_volume_aggregate_24h
domain_volume_aggregate_30d
domain_volume_aggregate_7d
flow
flow_record
flow_record_condition
geo_asn
interface_address
measurement_sample
opnsense.mirror_gap
opnview_downtime
owner_volume_aggregate_1h
owner_volume_aggregate_24h
owner_volume_aggregate_30d
owner_volume_aggregate_7d
pair_volume_observation
peer_volume_aggregate_1h
peer_volume_aggregate_24h
peer_volume_aggregate_30d
peer_volume_aggregate_7d
provider_source_period
purged_flow_hour
resource_record_observation
rule_volume_aggregate_1h
rule_volume_aggregate_24h
rule_volume_aggregate_30d
rule_volume_aggregate_7d
security_event
state_item
state_snapshot
unmatched_record_aggregate_1h
unmatched_record_aggregate_24h
unmatched_record_aggregate_30d
unmatched_record_aggregate_7d
volume_aggregate_1h
volume_aggregate_24h
volume_aggregate_30d
volume_aggregate_7d
```
<!-- growing-tables:end -->

<!-- bounded-tables:begin -->
```
account
applied_schema
blocklist
capture_configuration
capture_interface
encrypted_credential
eve_ingest_cursor
interface
interface_map
interface_network
opnsense.mirror_column
opnsense.mirror_correction
opnsense.mirror_datum
opnsense.mirror_endpoint
opnsense.mirror_parameter
opnsense.mirror_read
opnsense.mirror_schema_version
opnsense.mirror_table
opnview_heartbeat
owner
owner_reassigned_hour
plugin_aside_table
plugin_state
plugin_table
provider
provider_rule_info
push_listener
resource_record_read
retention_purge
rule
session
setting
source_availability
```
<!-- bounded-tables:end -->

Why each bounded table is bounded, and may therefore be scanned:

| Table | Bound |
|---|---|
| `interface` | one row per discovered interface; a firewall has tens, not millions |
| `owner` | one row per person the user created; a household or an office has tens, and a row is only ever created by hand |
| `owner_reassigned_hour` | one row per hour slot a change of ownership invalidated, from the change to the next refresh of the aggregates, which deletes the rows it read; seconds at most, and a change is made by hand (*Refresh contract*, rule 3) |
| `blocklist` | one row per distinct list name the resolver has reported; a resolver subscribes to tens of lists, not millions |
| `interface_map` | one row per raw device name the firewall reports |
| `retention_purge` | exactly one row, the furthest horizon the purge has applied |
| `opnview_heartbeat` | exactly one row, the last instant the service was known to run |
| `interface_network` | one row per network an interface has carried, detected by discovery or set by the operator; a firewall has a few per interface |
| `rule` | one row per rule in the running ruleset |
| `provider` | one row per implementation the project knows of; fifteen today, and a new one is an `INSERT`, not a stream |
| `provider_rule_info` | one row per (provider, rule identity) seen at least once; a rule set holds tens of thousands at most, and the table only ever holds the ones actually observed |
| `source_availability` | exactly one row per `provider` row, for the life of the database |
| `eve_ingest_cursor` | one watermark per rotated `eve.json` file; the firewall keeps the current file plus four archives |
| `setting` | one row per configuration key |
| `account` | one row per opnview login, created by hand in the setup surface |
| `session` | one row per session issued and not yet expired and collected; a browser holds one |
| `encrypted_credential` | one row per named credential opnview has to send somewhere; two exist |
| `resource_record_read` | one row per provider of the two resolver-record kinds that has been read successfully; four are registered |
| `plugin_state` | a plugin's durable values -- a watermark, a cursor, the last thing it read; a handful of rows per plugin |
| `plugin_table` | one row per table a plugin declares |
| `plugin_aside_table` | one row per plugin table set aside by a schema change; one with a time column is purged by its recorded retention and dropped, with its row, when empty |
| `capture_configuration`, `capture_interface` | one row per run of reads that found the same NetFlow capture configuration, and its captured interfaces: a new row only when the configuration changes, a handful a year; purged by its last read, the newest kept |
| `push_listener` | one row per push protocol and transport, a handful in all |
| `applied_schema` | exactly one row, the digest of the `schema.sql` last applied in full, which lets a start skip an apply that would change nothing |
| `opnsense.mirror_endpoint`, `opnsense.mirror_parameter` | one row per firewall endpoint a reader has called, and per request parameter of one; a firewall's API has hundreds of endpoints, and only those some plugin reads are ever called |
| `opnsense.mirror_datum`, `opnsense.mirror_read`, `opnsense.mirror_correction` | one row per endpoint and set of identifying parameters a reader asked for, per path a reader read of one, per reader correcting one: set by what the installed plugins read, not by time |
| `opnsense.mirror_table`, `opnsense.mirror_column` | one row per table the mirror learned, one per endpoint and row set, and per field some reader read of it |
| `opnsense.mirror_schema_version` | one row per change of an endpoint's answer shape, which a firewall upgrade brings: a handful a year |

`provider_source_period` is classified as growing, not bounded: a period is opened at every
handover between polling and a push path (`internal/store/push/push.go`, `OpenSourcePeriod`), and
every start of `opnview` whose last period was a push one opens a pull period
(`internal/collect/kinds/filterlog/pushpaths.go`, `loadPushPath`) -- one row a restart and two a
push source's coming and going, without bound. It is purged at the flows' horizon: a closed period
that ended before it goes, and the open one, the period in force, is kept however old
(`purge.sql`). `opnsense.mirror_gap` is growing for the same reason -- one row a failed call or a
cut stream -- and the mirror purges it by its end (`internal/broker/mirror`).

`client` is classified as growing, not bounded: an address reissued to another
machine creates a new identity rather than updating an existing one, so the
table accumulates over time even on a network with a fixed number of machines.
It is purged accordingly, under the guard described in its own section, and no
screen query is allowed to scan it.

## Storage fit

The store is PostgreSQL with TimescaleDB (P4 and P5 of
`specs/SPEC-backend-completion-plugins.md`). A table becomes a hypertable only where TimescaleDB's
constraint rule allows it -- **every unique key of a hypertable includes its time column**, and no
foreign key may reference a hypertable -- and an aggregate becomes a continuous aggregate only where
it reproduces exactly what the refresh contract asserts. **No core table is a hypertable but `flow_record`, and
no family is a continuous aggregate, in this cycle**, for the reasons below; every retention is the
purge's own `DELETE`, inside the purge's transaction and ordered with derivation, on opnview's
clock -- no TimescaleDB background job runs on a path the refresh contract orders. The plugins'
tables are hypertables (`docs/plugins.md`, *Tables*); a push output table is one chunked by day on its
`instant` column, a `timestamptz`, written by batches of at most a thousand rows, one transaction each.

**The growing tables.** Every one of them carries a surrogate `id BIGINT PRIMARY KEY` (or a
natural key) that omits its time column, which a hypertable refuses; most are also referenced by a
foreign key, which a hypertable cannot be.

| Table | Hypertable | Why not | Retention | Test that proves the purge |
|---|---|---|---|---|
| `flow` | no | `id` and `log_digest` are unique without `observed_at`; referenced by `domain_attribution` | row `DELETE` before the horizon, its purged part kept (`purge.sql`) | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer`, `TestAPurgeUnderAShortRetentionKeepsEveryFamilyWhole` |
| `dns_resolution` | no | `id` and `lookup_key` are unique without `looked_up_at`; referenced by `domain_attribution` | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `domain_attribution` | no | keyed on `flow_id`; follows its flow | row `DELETE` with its flow | `TestThePurgeLeavesTheForeignKeysConsistent` |
| `security_event` | no | `id` and `(provider_id, provider_event_key)` are unique without `occurred_at` | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `measurement_sample` | no | `id` is unique without `sampled_at` | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `resource_record_observation` | no | keyed on the record's identity, not on time | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `pair_volume_observation` | no | keyed on the pair and the day | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `purged_flow_hour` | no | keyed on the hour and the identity; written by the purge itself | row `DELETE` of whole periods | `TestThePurgeRecordsHowFarBackItHasPurged` |
| `dhcp_lease` | no | `id` and its generation key are unique without a time column | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `client` | no | referenced by every flow, lookup and lease; mutable | row `DELETE` under the assigned-client guard | `TestThePurgeLeavesTheBoundedTablesAndTheUserInputAlone` |
| `geo_asn`, `address_classification`, `interface_address` | no | keyed on the address, mutable | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `collection_gap`, `opnview_downtime` | no | `id` is unique without the interval | row `DELETE` | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `state_snapshot`, `state_item` | no | `state_item` references its snapshot | row `DELETE` of whole snapshots | `TestThePurgeRemovesRowsOlderThanTheHorizonAndNothingNewer` |
| `flow_record` | **yes**, chunked by day on `start_at` | -- its one unique key, `(identity, start_at)`, holds the time column, and nothing references it | `drop_chunks` of the whole chunks past its own horizon (`retention_flow_record_seconds`, 0 following `retention_seconds`), then row `DELETE` to the exact second | `TestFlowRecordsArePurgedByChunkThenByRowAtTheirOwnRetention` |
| `connection_measured_volume` | no | keyed on the connection's first leg and the direction, not on time; rewritten in place as a figure grows | row `DELETE` with its connection, at the flows' horizon | `TestTheMeasuresAreCountedOnceAndNeverSummed` |
| `flow_record_condition` | no | `id` is unique without `observed_at` | row `DELETE` at the flow records' horizon | `TestFlowRecordsArePurgedByChunkThenByRowAtTheirOwnRetention` |
| `provider_source_period` | no | keyed on the provider and the period's start, a period spanning any length of time | row `DELETE` of closed periods ended before the flows' horizon; the open period kept | `sql/schema-checks.sh`, E21 and E22 |
| `unmatched_record_aggregate_*` | no | keyed on the slot and the record's Observation Point, direction, protocol, version and reason; accumulated by the join, not recomputed | row `DELETE` of slots ended before the flows' horizon | `TestAnUnmatchedRecordIsCountedInNoFigure` |
| a plugin's table with a time column | **yes**, chunked by day | its declaration may not hold a unique key without the time column (`unique_key_without_time`) | `drop_chunks` of the whole chunks past the horizon, then row `DELETE` to the exact second | `TestAPluginHypertableIsPurgedByChunkToTheExactSecond` |

**The aggregate families.** Every family -- `volume`, `owner`, `client`, `peer`, `domain`,
`rule` -- at every period -- `1h`, `24h`, `7d`, `30d` -- is **hand-refreshed**, as before the move:

| Family | Periods | Mechanism | Why not a continuous aggregate | Test that proves equivalence |
|---|---|---|---|---|
| `volume` | 1h, 24h, 7d, 30d | hand-refreshed table | the second leg of a paired connection counts under its exit leg only; reclassification moves a flow between interfaces; the ISO week and the calendar month are rolled up from days | `TestEverySlotOfEveryFamilyEqualsTheSameSumsOverFlow`, `TestEverySlotEqualsTheFlowsAcrossTheCalendarAndItsCorrections`, `TestAPairedConnectionCountsOnceInTheClientPeerAndOwnerFamilies` |
| `owner` | 1h, 24h, 7d, 30d | hand-refreshed table | keyed on `client.owner_id`, a joined mutable attribute whose change invalidation does not track (refresh contract, rule 3) | the same, and `TestAPurgeUnderAShortRetentionKeepsEveryFamilyWhole` |
| `client` | 1h, 24h, 7d, 30d | hand-refreshed table | identity re-pointing moves a flow between clients; `client_count` is an exact distinct count | the same, and `TestALeaseNamingAnAddressSeenOnlyInFlowsRepointsItsRows` |
| `peer` | 1h, 24h, 7d, 30d | hand-refreshed table | `distinct_peers` is exact in a composed and a rolled-up slot, which a summed continuous aggregate cannot be | the same, and `TestDistinctPeersIsExactInAComposedAndARolledUpSlot` |
| `domain` | 1h, 24h, 7d, 30d | hand-refreshed table | the attribution of a flow changes after it is stored | the same |
| `rule` | 1h, 24h, 7d, 30d | hand-refreshed table | the second leg counts under its exit leg; a purged hour keeps its purged part | the same, and `TestARetentionShorterThanAnHourStillFillsTheCurrentSlots` |

**Continuous aggregates: the verdict for every candidate** (Phase E, 16 October 2026). A
continuous aggregate replaces a hand-built table only where it is exactly equivalent: every slot equal
to the hand-built one across late ingestion, reclassification, purge and the existing fixtures. **None
qualifies**, for the reasons below, each resting on TimescaleDB 2.30.2 as its source at that tag
says; so **no continuous aggregate exists, and the installation stays on the Apache-2.0 edition**
(`timescaledb.license=apache`). The licence decision of 12 October 2026 -- enable the Timescale
License and use continuous aggregates wherever they are exactly equivalent -- is superseded: with
nothing equivalent to use them for, enabling it would be a placeholder.

Two facts every verdict below shares:

- **A continuous aggregate is a Timescale License feature.** Every file of
  `tsl/src/continuous_aggs/` is "licensed under the Timescale License"
  ([create.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/create.c#L1-L5)),
  and under the Apache edition creating or refreshing one fails, "functionality not supported under
  the current "apache" license"
  ([cross_module_fn.c](https://github.com/timescale/timescaledb/blob/2.30.2/src/cross_module_fn.c#L124-L188)).
- **A refresh recomputes a bucket from the raw rows that remain, and deletes a bucket left with
  none.** Its materialisation deletes the window and re-inserts it from the defining query
  ([materialize.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/materialize.c#L520-L534),
  and L625-L652 for the merge path), and a `drop_chunks` or a row `DELETE` of raw data invalidates
  its range so that the next refresh does exactly that
  ([chunk.c](https://github.com/timescale/timescaledb/blob/2.30.2/src/chunk.c#L4055-L4073);
  [insert.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/insert.c#L32-L49)).
  An aggregate that must keep what its raw rows no longer hold cannot be one.

| Candidate | Verdict | Reason, verified at 2.30.2 |
|---|---|---|
| The 24 family tables (`volume`, `owner`, `client`, `peer`, `domain`, `rule` × `1h`, `24h`, `7d`, `30d`) | no | A continuous aggregate must query a hypertable: a plain table is refused, "table ... is not a hypertable" ([common.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/common.c#L1209-L1214)); and `flow` cannot become one, its `id` and `log_digest` being unique without `observed_at` while a hypertable's every unique key must hold its time column ([indexing.c](https://github.com/timescale/timescaledb/blob/2.30.2/src/indexing.c#L84-L108)). The reasons of the table above stand besides: the families are keyed on joined, mutable attributes, and the week and month are rolled up from days. |
| `unmatched_record_aggregate_*` | no | Its source, `flow_record`, is a hypertable, but the family is **accumulated from the join's decisions**, which are no column a query could re-read, and it **outlives the purge of its records** (`flow_record` purged at `retention_flow_record_seconds`, the family at the flows' horizon): the first refresh over a purged range would recompute its slots from the records left, or delete them. |
| The broker's `#minute` and `#hour` stream aggregates | no | Five reasons, each enough: (1) `last` is the value of the event received last, in insertion order, at one-second resolution (`internal/broker/recording.go`), while TimescaleDB's `last(value, time)` keeps the row with the greatest time and resolves a tie by whichever row the scan fed first -- no insertion order exists ([agg_bookend.c](https://github.com/timescale/timescaledb/blob/2.30.2/src/agg_bookend.c#L366-L370)); (2) columns are added at run time, one per numeric field a reader starts reading, and a continuous aggregate takes only an added aggregate `GENERATED ALWAYS AS (...) STORED` column, left NULL until a forced refresh recomputes it from the raw rows ([add_column.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/add_column.c#L76-L92), [CHANGELOG](https://github.com/timescale/timescaledb/blob/2.30.2/CHANGELOG.md#L233)), which no longer exist past the detail's retention; (3) the aggregates are kept for `retention_seconds`, the detail for `mirror_stream_detail_retention_seconds`, and a refresh over the dropped detail loses them (above); (4) each event is visible in the aggregates at once, where a continuous aggregate is materialised only by a refresh, real-time aggregation being off by default ([create_materialized_view_with_clause.c](https://github.com/timescale/timescaledb/blob/2.30.2/src/with_clause/create_materialized_view_with_clause.c#L32-L36), [CHANGELOG 2.13.0](https://github.com/timescale/timescaledb/blob/2.30.2/CHANGELOG.md#L1837)); (5) a refresh cannot run inside a transaction block, `PreventInTransactionBlock(nonatomic, "refresh_continuous_aggregate()")` ([refresh.c](https://github.com/timescale/timescaledb/blob/2.30.2/tsl/src/continuous_aggs/refresh.c#L1137-L1144)), so the aggregate could not move with the event that changes it. |

Since nothing qualified, no equivalence test was written: a continuous aggregate that does not exist
has nothing to be equivalent to. `sql/schema-checks.sh` asserts the edition the database runs with
(E15).

**The mirror.** The `opnsense` schema holds the broker's mirror of the firewall API (A10 of the
broker amendment): its own bookkeeping tables (`mirror_endpoint`, `mirror_parameter`,
`mirror_datum`, `mirror_table`, `mirror_column`, `mirror_schema_version`, `mirror_read`,
`mirror_correction`, `mirror_gap`), declared in `schema.sql`, and the tables it learns from the
answers, created at run time, one per endpoint and row set, with the fields some reader read. None
of them is a hypertable: a learned table is keyed by the datum and the row's key, never by time.
Each is purged to the exact second by the mirror's own `DELETE` -- a row no answer held since the
horizon, a record past it on its cursor, a history entry written before it
(`TestTheMirrorIsPurgedToTheExactSecond` in `internal/broker/mirror`). **A recorded stream** is
the exception: its detail table is a hypertable on its reception instant, chunked by day, purged by
`drop_chunks` and then a row `DELETE` to the exact second, and its per-minute and per-hour
aggregates are tables opnview maintains as each event arrives -- not continuous aggregates, since
only the Apache-2.0 edition of TimescaleDB is used -- whose equality with the detail is
`TestAStreamIsRecordedAggregatedAndPurgedOnItsOwnClock`. A minute with no event has no row: a gap,
never a zero (`TestAStreamCutIsAGapNeverAZero`).

Late ingestion into a closed slot is `TestAnHourHoldingTheHorizonTakesALateFlowAndAReclassification`;
a gap is never a zero, the data catalogue's `TestGapsAreGapsAndBucketsAlignOnTheAggregatePeriods`.
Columnstore compression is not used: `ReclassifyEverything` rewrites rows across the whole
retention.

## Providers

`docs/architecture.md` holds the seam; this section holds the data. A
**kind** is a contract `opnview` implements, a **provider** is one
implementation of it, and the registry holds one row per implementation, not
one per kind.

Registering a provider is an `INSERT` into `provider` plus an `INSERT` into
`source_availability`. Nothing in the DDL enumerates provider names: the only
`CHECK` on the registry constrains `kind`, because a kind is code `opnview`
ships and not data a deployment supplies.

The fifteen rows the schema registers, with the survey section that established
each one. **Each is one IMPLEMENTATION of a kind, and the kind is the seam:**
`dhcp_lease` has three and `dns_lookup`, `resolver_cache` and
`resolver_local_data` have two each, and on the firewall the survey
probed Dnsmasq serves DHCP with Kea disabled while Unbound and Dnsmasq are both
running as resolvers. `internal/collect` carries one interface per kind and one
file per implementation, each registering itself against the `provider_key`
below, so adding a backend is one file plus one row here.

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
| `geo_asn` | `maxmind_geolite2` | the GeoLite2 City and ASN databases | the second of the three outbound calls the project allows |
| `measurement_sample` | `insight` | `/api/diagnostics/traffic/top/<interface names>`, the system-gauge endpoints, since step 5A `/api/routes/gateway/status`, and since the resolver-cache cycle `/api/diagnostics/cpu_usage/stream` for the processor | *The telemetry the data model calls gaps G9 and G10 exists*, *The per-pair data is a live snapshot, not history*, *Gateway status, read from source for step 5* and *Processor stream, read from source for the resolver-cache cycle* |
| `resolver_cache` | `unbound` | `/api/unbound/diagnostics/dumpcache` | *Resolver cache and local data, read from source for the resolver-cache cycle* |
| `resolver_cache` | `dnsmasq` | none: no cache read through the API, reported as that named state | same section |
| `resolver_local_data` | `unbound` | `/api/unbound/diagnostics/listlocaldata` | same section |
| `resolver_local_data` | `dnsmasq` | none: no local-data read through the API, reported as that named state | same section |
| `public_suffix` | `public_suffix_list` | <https://publicsuffix.org/list/public_suffix_list.dat>, conditional GET | the third of the three outbound calls, added by step 5A; MPL 2.0 (`internal/publicsuffix`) |

**Two kinds have no implementation, each for its own recorded reason, and
neither absence is an oversight.**

`flow_volume` keeps its registry row and is collected by nothing:
`pair_volume_observation` is **derived from `flow`** by step 5 — the maintainer's
ruling — so there is no pass to run and nothing to probe. The netflow
`is_enabled` probe that used to answer for it did not disappear; it moved with
the sampler to the `measurement_sample` row, which is the row whose material it
actually governs.

`reconciled_state` has **no registry row at all**. A row is a claim that an
implementation exists and can be probed; a row with none would read "not yet
probed" for ever, and a screen would show a source nobody is looking at. The
kind exists so the ten surveyed state-shaped sources have a destination, and
writing a connector for one of them is a later cycle's work.

**Four kinds have several providers**, and that is what makes the registry real
rather than notional: `dns_lookup`, `resolver_cache` and `resolver_local_data`
have Unbound and Dnsmasq, `dhcp_lease` has Kea, Dnsmasq and ISC dhcpd. Step-1 detection already tells them apart, and
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
`(kind, provider_key)`. The core kinds are the twelve `docs/architecture.md`
names: the nine before the resolver-cache cycle, `resolver_cache` and
`resolver_local_data`, which it added, and `flow_record`, which the NetFlow addendum
added; a plugin may bring kinds of its own, and `kind` is constrained to a snake_case
name.

**`is_active` is not reachability.** It says which implementation `opnview`
actually reads for that kind; `source_availability.state` says which ones can
be reached. A machine may have two reachable implementations of one kind.

**Exclusivity is per kind only where two providers would be
indistinguishable.** One active provider per kind was the wrong universal rule:
people run several detection engines side by side, and two DHCP servers on two
VLANs is an ordinary estate rather than a misconfiguration. The rule that
replaced it — *a kind admits several concurrently active providers exactly when
the identity of its destination rows includes the provider* — is stated, kind by
kind, in `docs/architecture.md` under *Activeness, and what is still exclusive*,
and it is enforced by the partial unique index
`uq_provider_active_per_exclusive_kind` over `kind` where `is_active = 1` and the
kind is not one of the six the rule exempts — the four it exempted before, and the
two resolver-record kinds, whose rows name the provider that read them. Marking a second provider of an
**exclusive** kind active fails; marking a second provider of a concurrent kind
active succeeds, and both are read.

On a freshly applied schema **no provider is active**. Activeness is determined
by detection against a live firewall. For an exclusive kind, two candidates the
firewall's own configuration does not separate activate **neither**; for a
concurrent kind, every candidate is activated.

**The operator decides first, and the probe round only where the operator left
the decision to it.** *(Rewritten after the loss of 2 October 2026; the
behaviour described is that of the compiled build of that evening.)* Each
registry row has a selection, stored as a `setting` row keyed
`source_selection_<kind>_<provider_key>` (see `setting` below): `off` is never
read, `on` is always read whatever the probe concluded, and `auto` — what a
missing row means — leaves the decision to the probe round, so an installation
nobody configured behaves as it did before. Turning one implementation of an
exclusive kind `on` is what breaks the tie that would otherwise activate
neither. The selection is a decision, not a claim about the firewall:
`source_availability` keeps recording what the probe found, so a source kept
`on` while it reports unavailable is a legitimate state that the availability
and `collection_gap` rows then explain. A selection row that cannot be parsed is
reported on the console by the probe round; in the compiled build that round
then decides as it does for `auto`, while the collection surface answers with
an internal error until the row is corrected.

**The operator sets the selection on the collection surface**, one choice per
implementation in its kind's card, saved with the card. The save writes the
rows and applies them at once: the collector keeps what the last probe round
found for each kind and decides again from that and the new selections, without
contacting the firewall and without writing an availability row, since nothing
was checked. Before the first probe round of a start there is nothing to decide
from, and that round applies the selection as every round does. A selection is a `setting` row
rather than a column here because `setting` is where configuration lives and a
column would need a migration on a database that holds real data.

**Retention:** never purged. **Indexes:** the primary key, the uniqueness
constraint on `(kind, provider_key)`, and the partial unique index above.

### `interface` — one OPNsense interface, tunnels included

One discovered interface: a VLAN, a physical link or a tunnel the firewall
terminates. The word is OPNsense's own, not a coinage — see *Vocabulary*.
**Source:** `/api/interfaces/overview/interfaces_info` (runtime discovery (i))
— `identifier`, `description`, `device`, `status`, `enabled`, `link_type` and
`vlan_tag`. Every discovered column carries that endpoint's field name
unchanged. Since step 5A, `addr4`, `addr6`, `ipv4[]`, `ipv6[]` and `gateways[]`
are stored in `interface_address` below, and `gateways[]` also decides
`is_upstream`; the networks the addresses cover are proposed in
`interface_network`. That closes gap G13 of `docs/widget-catalogue.md`. *API
field coverage* below records it with the rest.

**The link-local rule is the operator's.** `link_local_evidence` says whether an
IPv6 link-local address seen on the interface is evidence that the address belongs
to it. Such an address is the same on every interface, so the default is no, and
setting it on one interface makes it evidence there only (*Classification* below).
Discovery never writes the column.

**Upstream is read, never guessed.** `is_upstream` is 1 exactly when the last
`interfaces_info` response reported a non-empty `gateways[]` for the interface,
and 0 otherwise — no name, description or link type enters. An address reached
through an upstream interface is **outside**: on-link evidence seen there is not
evidence of membership (*Classification* below). The term is the widget
catalogue's; see *Vocabulary*. A tunnel that carries a gateway is upstream by
the same rule, which the step-5 validation checks on the live firewall.

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

### `interface_address` — the addresses an interface held, and the gateways behind it

**Source:** `/api/interfaces/overview/interfaces_info` (runtime discovery (i)),
fields `addr4` and `addr6` — one `address/prefix length` string each, or empty —
`ipv4[]` and `ipv6[]` — every address the interface holds, each entry an object
whose `ipaddr` is `address/prefix length`, with a `vhid` beside it for a CARP
address — and `gateways[]`, a list of gateway **addresses**, at most one per family
(read at `opnsense/core` 26.7.3, `Interfaces/Api/OverviewController.php`).
`source_field` holds the field name verbatim, so nothing is invented to tell a
primary address from a secondary one or an interface address from a gateway; a
gateway row carries no prefix length, and a `CHECK` holds that. The primary
address normally appears under `addr4` and again in `ipv4[]`, and is stored under
both. A link-local entry of `ipv6[]` may carry its zone, which is dropped: the
address is the same on every interface whatever the zone.

**A change is a new row, and the history is the table.** A row is **one holding**:
an interface, a field, an address and its prefix length, from the first discovery
that read it to the last one that read it without a break, keyed on those and
`first_seen_at`. A refresh that reads the same value, having read it at the previous
discovery, moves `last_seen_at`; one that reads a different value inserts a row and
leaves the predecessor where its last sighting put it; and one that reads a value
again after a discovery that did not read it — released, then re-acquired — starts a
new row, so the interval between the two holdings is held by neither. The previous
discovery's instant is read once, before a discovery pass writes anything
(`store.LatestInterfaceDiscoveryAt`), so the outcome does not depend on the order the
firewall lists its interfaces in. "Held at an instant", for this firewall, reads one
row as one holding (*Classification*). *When did the public address last
change* is therefore the `first_seen_at` of the current row when an earlier row
exists, and it can only ever come from `opnview` having looked before: no
endpoint keeps that history.

**What reads it.** `blocked_decision` and `store.IsFirewallAddress` read every
address field to say a packet was aimed at the firewall itself;
`store.ReadPublicAddresses` returns each upstream interface's history, each row
naming its field. Classification reads it **for one purpose**, since the step-5A
live corrections: the `this_firewall_address` view — every row under `addr4`,
`addr6`, `ipv4` or `ipv6`, with whether the latest discovery of its interface read
it — is how an end is recognised as **this firewall**, as of its own instant
(*Classification*). Membership of every other address comes from the networks in
`interface_network`, below, and never from this table.

### `interface_network` — an interface's networks, detected or set by the operator

One address range of one interface: the range classification takes to sit behind
that interface. **The term is OPNsense's**: the range an interface's address and
prefix length cover is that interface's *network* — "LAN net" in the rule editor,
the internal alias for "LAN network" (<https://docs.opnsense.org/manual/aliases.html>).

**Detected or set by the operator.** `origin = 'detected'` is a network discovery
proposed: each address of `addr4`, `addr6`, `ipv4[]` and `ipv6[]` masked to its
prefix length. An IPv6 link-local prefix is never proposed — it is the same on
every interface and places nothing; the link-local rule on `interface` decides
whether such an address is evidence. `origin = 'operator'` is a network the operator
added, or a detected one the operator confirmed. `removed_at` is set when the
operator removes one, and the row stays: discovery moves `last_detected_at` and
never touches `origin` or `removed_at`, so a removed network is not proposed back.

**Which networks count — decision D1** (`specs/SPEC-step-5a-corrections.md`, 4
October 2026): a detected network counts as membership evidence until the operator
overrides it; an operator network always wins over a detected one; a removed one no
longer counts. A detected network counts only while discovery still reports it —
its `last_detected_at` is its interface's latest detection — and a network on an
upstream interface never counts.

**The write path** is `store.AddInterfaceNetwork`, `store.RemoveInterfaceNetwork`,
`store.ConfirmInterfaceNetwork` and `store.SetLinkLocalEvidence`. A range that does
not parse as `address/prefix length`, that has bits set beyond its prefix length,
that is an IPv4-mapped IPv6 range, or that overlaps the IPv6 link-local prefix is
refused; so is a network on an upstream interface, or on no interface. A network
that overlaps another interface's counting network is **reported**, not refused:
the operator may be correcting a detection, and the operator's network wins. Every
edit changes `store.OnLinkFingerprint`, which the collector compares before every
derivation, so the next derivation places every stored address again, places in
full each one whose evidence moved, and recomputes every slot its flows touch.

**Where they are set: the Interfaces surface** (`/interfaces`, a link on the top
bar). One card per interface; an upstream interface's card carries no control. On
every other card each stored network is listed with its origin, whether it is
removed and whether it counts now; a detected network carries a confirm control
(`ConfirmInterfaceNetwork`), any network in place a remove control, a removed one a
restore control (`ConfirmInterfaceNetwork` again), and a field adds one
(`AddInterfaceNetwork`), each refusal named against it. A network that does not count
says why (removed, or no longer detected), and every counting network overlapping a
counting network of another interface is listed on both cards for as long as the
overlap exists.
**The link-local rule is set on the same card** (`SetLinkLocalEvidence`),
default no. The surface says an edit is saved for the next derivation, never that
it has been applied.

**Nothing is a default.** No range appears in code or in the schema: every row is
read from the firewall or typed by the operator.

**Retention:** bounded — one row per network an interface has carried — and never
purged, because an operator row is user input. **Indexes:** the uniqueness of
`(interface_id, network_address, prefix_length)`.

**Retention:** growing, slowly — one row per address an interface ever held.
Purged by `last_seen_at`, which never drops a current row because discovery
moves it. **Indexes:** the value's uniqueness index, and indexes on
`(interface_id, last_seen_at)`, `(address, source_field)` and `last_seen_at`.

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
`uuid`, `description`, `action`, `direction`, `interface`, `legacy`, `log`,
`is_automatic`. For legacy and
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

**The interface a rule applies to is stored verbatim, since step 5A.**
`interface` holds the endpoint's field unchanged: one configuration key, several
joined by commas, or the empty string of a floating rule on a model rule — and,
on a **legacy** row, a **description** rather than a key (survey, *Runtime
discovery* (ii)). `legacy` holds the endpoint's flag, so a reader knows which of
the two it is reading. Nothing resolves the field to an `interface` row at
ingest: a description is not an identity, and `store.ReadRuleLogging` counts a
rule whose `interface` names nothing it can resolve as **unresolved** rather
than placing it under a guess.

**What a rule was written against is stored verbatim, since step 5B, and never
evaluated.** `destination_net` holds the endpoint's field unchanged — an address, a
network, an alias name or `any` — or `NULL` when the row does not report it (B8a of
`specs/SPEC-step-5b-widget-api.md`, decision 12: the reason is the rule table, which
shows it beside the rule). `opnview` matches no flow against it, resolves no alias in
it and derives nothing from it: which rule a record matched is the `rid` the filter log
names, never a criterion `opnview` re-evaluates.

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
`NOT EXISTS` over `flow`, `security_event`, `dhcp_lease`, `dns_resolution` **and
`purged_flow_hour`**, because removing an identity a surviving flow still points at
would leave that flow unable to name a machine, and removing one the purged part of
a kept hour names would take its bytes out of the client, peer, owner and domain
families. So a client older than the horizon is kept **when, and only when,** one of
those still names it after the rest of the purge has run — the purged part's own
expired hours are removed first. **A client somebody assigned to a person is never
purged** (C1 of `specs/SPEC-backend-completion-plugins.md`): the assignment is user
input, so the machine outlives the retention as the person does, and is still there to
be listed, reassigned or unassigned when it next appears. Unassigned, it is purged as
before.

**This firewall is never a client, and the neighbour tables list it.**
`/api/diagnostics/interface/get_arp` and `get_ndp`, read by every lease pass, carry
this firewall's own interface entries beside its neighbours'. An entry at an address
the firewall holds at that instant — "held at an instant" as `this_firewall_address`
defines it, or a loopback address — creates and updates no client (defect L2 of
`specs/SPEC-test-budget-and-two-live-defects.md`): before that rule each became a
`mac`-level client at the firewall's own address. Such clients, from a database an
earlier build wrote, are removed by the next full placement (`ReclassifyAll`) once
nothing names them and nobody assigned them to an owner, by the same guard as the
address-level and upstream clients. The statement,
`purge_unreferenced_this_firewall_clients_at_address`, runs once per address the
firewall ever held, each found by an index seek (`next_this_firewall_address`), so
neither `interface_address` nor `client` is scanned.

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

**A lease names the DHCP server that issued it.** `provider_id` is `NOT NULL` and
joins to `provider.display_name`, so clicking a machine can tell you *which*
server gave it its address. That is a fact a reader is entitled to rather than an
implementation detail of de-duplication, and it became a question worth asking
when this kind became concurrent: a client can hold leases from two servers at
once. The read is written down and executed — the diagnostic *Leases per client
and issuing server* in `sql/queries/diagnostics.sql` — and it takes the name off
the join rather than reconstructing it from a key or inferring it from a backend
token.

`backend` is kept beside it and is a different fact: it is the normalised
vocabulary of **response shapes**, which says which field-name normalisation
produced the row. It is not an identity for a server.

**Identity:** `(address, generation_key, provider_id)`, unique — one row per lease
generation per server, so a reissue is a second row rather than an overwrite, and
the same address legitimately leased by two servers on two scopes is two leases
rather than one contested row.

**This kind admits several concurrently active providers**, and the deployment is
the ordinary one: one server on one VLAN, another on a second. The reasoning, and
why the client identity cascade makes a machine leased by both *one* client with
*two* leases, is in `docs/architecture.md` under *Activeness, and what is still
exclusive*. The identity names the provider rather than the backend because the
rule is stated in terms of the provider and `backend` is a vocabulary two
providers could share.

**The validity start is nullable, and the generation key is a column of its
own.** The two used to be one column meaning two things. Kea reports
`valid_lifetime`, so `expire` minus it is a real start; Dnsmasq reports no start
at all, and a standing reservation reports neither a start nor an expiry. The
earlier schema stored the expiry — or the day of observation — in a `NOT NULL`
`starts_at`, and was honest only because a comment beside it said so. Now a
backend that cannot know stores `NULL`, and what keeps a second poll of one lease
from inserting a second row is `generation_key`.

`generation_key` is composed by the collector, in
`store.GenerationKeyOf`, from the most specific instant the backend supplied, and
it carries the name of what it rests on:

| Value | What the backend reported |
|---|---|
| `start:<epoch>` | a validity start — Kea, through `valid_lifetime` |
| `expiry:<epoch>` | only an expiry. The expiry still discriminates a generation, and a renewal is still a new row; the column that says so no longer pretends to be a start |
| `observed_day:<epoch>` | neither, which is what a standing reservation looks like. One row per day rather than one per poll, because a reservation is configuration rather than a generation |

Nothing parses it or matches on it: it is opaque to every query, and it is the
term recorded as `opnview`'s own in *Vocabulary*. **Keying on `starts_at` would
now be a defect rather than a style choice**: SQLite treats nulls in a uniqueness
constraint as distinct from each other, so every lease of a backend that reports
no start would insert a fresh row on every pass.

**Retention:** purged by `observed_at`.

**`hostname_label`**, a generated column, is the host name's first label, lower-cased, a trailing dot removed: what a host name the resolver logged as a lookup's client is matched on, by first label on both sides (`dns_resolution`, and the step-5A live corrections, causes H1 and H2). `idx_dhcp_lease_hostname_label` serves that match; it replaced an index on the whole host name.

**Indexes:** `idx_dhcp_lease_observed_at` for the purge and for recency,
`idx_dhcp_lease_client` for a client's lease history — keyed on `observed_at`
rather than on `starts_at`, because an index leading on a column that is null for
a whole backend would order that backend's leases arbitrarily. It is what makes
"this machine's leases, per issuing server" an index search rather than a scan of
a growing table, which matters more now that a machine can hold leases from two
servers at once.

### `flow` — one filter-log record

**Source:** `/api/diagnostics/firewall/log`, data source 1 — `interface`,
`action`, `rid`, `label`, `src`, `dst`, `srcport`, `dstport`, `protoname`,
`protonum`, `length`, `ipversion`, `dir`, `reason`, `id`, `seq` and
`__timestamp__`, deduplicated on `__digest__`, which is stored as `log_digest`. Every other
field the endpoint returns is accounted for in *API field coverage* below.

**Why it was logged is not what was done to it.** `log_reason` holds the
endpoint's `reason` field verbatim. A record whose reason is not a rule match
was not denied by a rule at all, and presenting it as a rule denial would
attribute a decision to a rule that made none. The value is stored unparsed and
unnormalised, and no `CHECK` enumerates it.

**Matching on it is allowed on the ESTABLISHED values only**, which step 5A read
from the source (survey, *The `reason` field's values, read for step 5*): the
filterlog plugin writes pf's reason table — `match`, `bad-offset`, `fragment`,
`short`, `normalize`, `memory`, `bad-timestamp`, `congestion`, `ip-option`,
`proto-cksum`, `state-mismatch`, `state-insert`, `state-limit`, `src-limit`,
`synproxy` — and `unknown(n)` for anything outside it. `store.LogReasons` holds
the set, the `blocked_decision` view is the one place that matches on it, and a
test fails if any code compares `log_reason` with a value outside it. `match`
is a rule's decision; every other established value is a drop no rule expresses;
an absent or unestablished value is a refusal whose reason was not recorded.

**Logged bytes are not conversation bytes.** pf logs the packet that
**establishes** a state, not the rest of the connection (`pf.conf(5)`, FreeBSD
15.1, `log`; survey, *Which packets a logging rule writes, read for step 5*), so
`packet_bytes` is the length of that one packet, and every sum of it — every
aggregate below — is **logged volume**. No figure computed from `flow` may be
presented as the volume of a conversation.

**A connection logged on two interfaces is two records, counted once.** pf writes
the packet that creates a state on every interface a logging rule creates one on,
so a client's connection to the Internet is logged `in` on the client's interface
and `out` on the upstream one. The two are paired (*Two legs of one connection*
below), and the pair is counted **once, on the client's `in` record**: the `out`
record, `pair_outcome` `second_leg`, stays in `flow`, counts on the figures of the
interface it was logged on and in the rule family, and in no other volume figure.
This is decision 2 of the step-5A live corrections, and it **amends 5A Scope A2
and its open question OQ4**, which counted every record.

**Identity:** `log_digest`, unique. The endpoint echoes back the record
matching the digest a poll supplied, so the uniqueness constraint is what makes
the echo harmless.

**East-west, north-south and this firewall** are carried by `traffic_scope`, which
is `this_firewall` when an end is this firewall (`src_is_this_firewall` or
`dst_is_this_firewall`), `east_west` exactly when both `src_interface_id` and
`dst_interface_id` are set otherwise, and `north_south` otherwise. It derives from
interface membership and this firewall's own addresses and from nothing else — no
address literal, no CIDR, no name, no assumed addressing plan. The third value is
decision 6 of the step-5A live corrections. The
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
| `idx_flow_refused_observed_at` | the Blocked query; a partial index on the refusing actions only, `block` and `reject`, so it stays small | no |
| `idx_flow_src_address`, `idx_flow_dst_address` | classification, which rewrites every flow touching one address and reads that address's sightings (step 5A) | no |
| `idx_flow_ingested_at` | the aggregate refresh, which finds the slots holding a flow ingested since the last refresh (step 5A) | no |
| `idx_flow_dst_client_observed_at` | re-pointing a client identity, and the purge's client guard, which look a client up as a destination (step 5A) | no |

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
dst_interface_id
dst_is_this_firewall
dst_port
id
ingested_at
interface_device
interface_lookup_state
ip_id
ip_version
log_digest
log_reason
observed_at
packet_bytes
paired_flow_id
pair_outcome
protocol
rid
rule_id
rule_lookup_state
src_address
src_client_id
src_interface_id
src_is_this_firewall
src_port
tcp_seq
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
| `dst_client_id` | the **purge**: a client is removed only when no surviving flow names it as source **or destination**; `classified_flow`, for an inbound flow's inside client |
| `src_address` | the Blocked query; classification and attribution (step 5A), and `classified_flow` |
| `dst_address` | the Interface, Client and Blocked queries; the `geo_asn` join |
| `src_port` | nothing, and it stays: it is the other half of the connection tuple, and the only record of which end of a connection chose its port. Since step 5A `pair_volume_observation.service_port` is derived from `dst_port`, not from this column |
| `dst_port` | the Client and Blocked queries |
| `protocol` | the Client and Blocked queries |
| `ip_version` | nothing, and it stays: it is the address family the filter log reports. An address column alone cannot be classified, and the project forbids inferring an addressing plan, so a v4/v6 split is answerable only from this column. IPv6 seed coverage is recorded against step 4 |
| `action` | Overview, Matrix, Interface, Client; the `blocked_event` view and its partial index |
| `direction` | no screen query, and it stays: it is pf's per-interface `dir`, and it is what tells a reader whether `src_address` on a blocked record is the machine inside or the machine outside. Since step 5A `classified_flow` reads it for one case only — both ends outside — to break the tie membership cannot |
| `packet_bytes` | Overview, Matrix, Interface, Client; the aggregate refresh |
| `rid` | the Blocked query alone, which returns the firewall's own rule identifier next to the resolved description. No other screen query reads it: the interface-pair screen joins through `rule_id` and reports `rule_lookup_state` instead |
| `rule_id`, `rule_lookup_state` | the Matrix and Blocked queries |
| `traffic_scope` | Overview, Matrix, Interface, Client, Blocked; the aggregate refresh |
| `log_reason` | no screen query; the `blocked_decision` view, which places it in `engine_kind`. It stays: it is the filter log's own answer to *why was this line written*, and it is the only thing that separates a packet a rule denied from a packet the firewall dropped for a reason no rule expresses. Without it a malformed-packet drop and a policy denial are the same row, and the denial screens would attribute the first to a rule that made no decision. It is projected by the `blocked_event` view, where that distinction is read |
| `src_is_this_firewall`, `dst_is_this_firewall` | `classified_flow`, which gives a flow with such an end the directions `to_this_firewall` and `from_this_firewall` and takes it out of the inside and outside ends; the `traffic_scope` `CHECK`; attribution, which never attributes a flow to this firewall; the pairing, which recognises a NATed second leg by its source. Classification writes them (step-5A live corrections) |
| `ip_id`, `tcp_seq` | the pairing alone (`internal/store/pairchunk.statements.sql`, `pairing_*_candidates`): the filter log's `id` and `seq`, the two fields verified to cross the firewall unchanged. Nothing else reads them, and they stay for that one purpose |
| `pair_outcome`, `paired_flow_id` | `classified_flow.exit_leg_device`, which keeps a second leg out of every total but its own interface's; attribution; the per-pair volume. `paired_flow_id` names the other record of the pair and carries no foreign key, because the retention purge can remove one record of a pair and keep the other |

Three columns — `log_digest`, `src_port` and `ip_version` — are read by no
screen query, by no purge statement, by no view and by no aggregate refresh.
Two more, `direction` and `log_reason`, are read by no screen query and by the
step-5A views only. Each is justified above, and each is kept deliberately.

### `blocked_event` — a view, not a table

Every blocked record is the same filter-log line as an allowed one with a
different `action`. Making it a second table would mean a second ingestion path
and a second copy of the data. It is therefore a view over `flow`, paired with
the partial index `idx_flow_refused_observed_at` so that reading it is an index
search rather than a scan of every flow ever recorded.

**A refusal is a `block` or a `reject`**, the rule the aggregate families follow:
a reject is a refusal that answers. The view projects `action` so the two can be
told apart, and its predicate and the index's are written identically, which is
what lets the planner use the index. A record whose action could not be named
(`unknown`) is not in it, and no screen counts one as allowed either. The
step-5A corrections made this change; until then the view and the index covered
`block` alone, and the old index is dropped by name.

Code that expects a table will not find one. This is deliberate.

### `classified_flow` — a flow, with its direction and its two ends written once

A view over `flow` alone, so the planner flattens it and every statement that
reads it keeps the flow indexes. It adds three things every derivation needs,
each written **once**, as an expression over the stored columns:

- **`traffic_direction`** — `from_this_firewall` when the source is this firewall,
  `to_this_firewall` when the destination is and the source is not (decision 6 of
  the step-5A live corrections); otherwise `outbound` when the destination is
  outside (it has no interface), `inbound` when the source is outside,
  `inter_interface` when neither is. When **both** ends are outside — a packet
  between two outside addresses the firewall forwards — membership cannot decide,
  and pf's `dir` on the interface the record was logged on does: `out` is
  outbound, anything else inbound. That tie-break is the only place `dir` enters,
  so the five directions **partition** the flows exactly. A LAN-to-Internet flow
  logged `in` on the LAN interface is **outbound**. Before the step-5A live
  corrections the unsolicited packet to the firewall's own upstream address was
  the both-outside case; it is `to_this_firewall` now.
- **`local_interface_id`, `local_client_id`** — the **inside** end: the source when
  it has an interface and is not this firewall, else the destination on the same
  terms, else nothing.
- **`peer_address`** — the **outside** end: the destination of an outbound flow,
  the source of an inbound one, and `NULL` between two interfaces. For a flow
  with an end that is this firewall it is the other end when that end is outside,
  and `NULL` when it is inside: this firewall is never a peer.
- **`exit_leg_device`** — the device a second leg was logged on, `NULL` for every
  other record. The volume family keys on it, so a second leg counts on that
  interface's own figures and in no total; every other family but the rule family
  leaves it out.

### `blocked_decision` — every refusal of every engine, placed once

A view, a `UNION ALL` over the three places a refusal is recorded:
`flow.action IN ('block', 'reject')`, `dns_resolution.action IN ('block', 'drop')` and
`security_event.event_action = 'blocked'`. Each refusal appears **exactly once**,
keyed by `(source_table, source_id)`, with `occurred_at`, `engine_kind`,
`engine_reference` (the pf label, the list's name or the rule identity),
`client_id`, `interface_id`, `target` and `target_kind` (an address or a domain),
and `target_is_this_firewall`, which is 1 when the refused address is one the
firewall itself holds (`interface_address`, `addr4` or `addr6`).

**`engine_kind` is a closed vocabulary of `opnview`'s own**, and every refusal has
a place in it:

| `engine_kind` | The refusal |
|---|---|
| `firewall_rule` | a filter-log record whose `reason` is `match`: a rule decided |
| `firewall_no_rule` | one whose `reason` is another established pf reason: dropped for a reason no rule expresses |
| `firewall_reason_not_recorded` | one whose `reason` is absent or outside the established set, so neither can be claimed |
| `dns_advertising_list`, `dns_tracking_list`, `dns_threat_list`, `dns_parental_list`, `dns_other_list` | a lookup refused by a list carrying that **user-assigned** purpose |
| `dns_unassigned_list` | refused by a list nobody has classified |
| `dns_list_not_recorded` | refused with no list named by the resolver |
| `security_engine` | a security event the engine blocked |

The purpose is the user's, from `blocklist.purpose`, and is never derived from a
list's name. The established `reason` values are listed under `flow` above.

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

**`first_seen_at` and `last_seen_at` are the lookups' instants, and never move
backwards** (decision 14 of `specs/SPEC-step-5b-widget-api.md`). `first_seen_at` is
the earliest and `last_seen_at` the latest `looked_up_at` of a lookup naming the
list. The resolver's ring buffer offers the same lookups again on every pass, and a
pass may meet an older lookup after a newer one, so `store.UpsertBlocklist` only
ever widens the two instants (`min` and `max`). Until step 5B both were the
ingestion instant and the last was overwritten unconditionally, which made it the
time `opnview` read the buffer rather than the time the list last refused anything.

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

**`client` is not always an address.** The resolver's report replaces the
querying address by the name a reverse lookup of it returned, whenever there was
one: `scripts/unbound/logger.py` fills a `client` table through
`socket.gethostbyaddr`, and the `details` query of `scripts/unbound/stats.py`
returns that name in place of the address (`opnsense/core` 26.7.3). A host name
is resolved back to an address through the DHCP leases **valid at the lookup's
instant** — a lease whose host name has the **same first label**, compared without
regard to case and with a trailing dot removed on **both** sides
(`dhcp_lease.hostname_label`), because a reverse lookup may answer with a fully
qualified name, a trailing dot or a bare label and a lease may carry any of the
three; started, if its start is known; not expired, if its expiry is known. 5A
compared the logged name and its label with the lease's whole name, so a bare
logged label never matched a lease carrying `host.domain` (cause H1 of the step-5A
live corrections) and a trailing dot on either side defeated the match (H2). The
name `localhost`, which a reverse lookup of a loopback address returns, names
**this firewall** whatever the leases hold (H4; decision 11).
**A host name no lease names is resolved through the resolver's local data
(scope D of `specs/SPEC-resolver-cache-attribution.md`).** The A and AAAA records
of `resource_record_observation` held in `local_data` whose owner name, and the
PTR records whose target, has the logged name's first label — the same rule as the
leases, through `host_label` — and that the local data held at the lookup's instant
(`store.ResolveLoggedHostname`). Exactly one address answering gives
`local_data_hostname`; two addresses of one family, or any three, give
`ambiguous_hostname`. Two leases naming the host are ambiguous whatever the local
data holds: they already say two machines carried the name.
**The dual-stack case** (group C of `specs/SPEC-step-5b-widget-api.md`, decision
15). A host override with an A and an AAAA record names one machine, and used to
make every lookup logged under its name `ambiguous_hostname`. When the local data
held **exactly one IPv4 and one IPv6 address under one name** at the lookup's
instant — the owner of an A or AAAA record, or the target of a PTR record, compared
without regard to case and with a trailing dot removed — the lookup's **querying
family** decides: `client_family`, 4 or 6, read from the `family` field the query
report carries on every row (`docs/opnsense-api-survey.md`, *The querying family
behind a logged host name, read from source for step 5B*). With it, the lookup
resolves to the address of that family as `local_data_hostname`. Without it — a row
that reported no family, or `unix` — the lookup is **`dual_stack_hostname`**, its
own state rather than a merge into `ambiguous_hostname`, because it is a different
fact: one machine, two addresses, and no evidence which one asked. Nothing is
guessed. The evidence is recorded **per lookup**, in `client_family`, so the retry
below decides a waiting lookup from the same evidence ingest had. Two addresses of
one family stay ambiguous, two addresses that share a first label but no name are two
hosts and stay ambiguous, and a lease still wins.
`client_resolution` records the outcome: `logged_address` (`client` was an
address), `lease_hostname` (exactly one address answered through the leases;
`client_address` is it and `client_hostname` the logged name), `local_data_hostname`
(no lease named it and exactly one address answered through the local data, or one
of each family did and the querying family chose; the same two columns),
`ambiguous_hostname` (several did), `dual_stack_hostname` (one of each family did,
under one name, and the querying family was not reported), `unknown_hostname`
(neither a lease nor the local data named it) or `this_firewall_hostname`
(`localhost`). In the last four cases `client_address` keeps the logged name
verbatim, and the attribution reads no lookup that is not `logged_address`,
`lease_hostname` or `local_data_hostname`: a name that does not resolve to one
address does not say which machine asked.
**`client_family`** is the querying address's family, 4 or 6, as the query report's
`family` field gives it (`ip4`, `ip6`), on every lookup whose row carries one, the
address-logged ones included; NULL for a Dnsmasq lookup, a `unix` socket or an
absent field. A `CHECK` holds it to 4 and 6. It is not part of `lookup_key`, so a
lookup stored before the field was read keeps its key.

**`client_is_this_firewall`** is 1 when the querier is this firewall — its address
was held by the firewall at the lookup's instant, or it is a loopback address or
`localhost` (*Classification*). Such a lookup has no client; it carries the
interface the address belonged to, or none for the loopback, in which case its
`interface_lookup_state` is `not_found`.

**The retry, and the restart (cause H6).** A lookup can be read before the lease
that names its host — every loop runs at start — so **every lease pass resolves
again the `ambiguous_hostname`, `dual_stack_hostname` and `unknown_hostname` lookups
no lease pass has examined since they were ingested**, each as of its own instant
and with its own stored querying family
(`store.ResolveLoggedHostnames`), and stamps `hostname_examined_at`. The stamp is
stored, so the first lease pass after a restart examines what the run before it
left: 5A kept the point to resume from in memory, starting at the collector's
creation, and a lookup stored before a restart was never examined again. The
statement reads `idx_dns_resolution_hostname_unexamined`, a partial index holding
the lookups still waiting and nothing else, so the work of a pass is bounded by
what changed, it never rescans every unresolved lookup, and it does not grow with
those that never resolve — a host whose name no lease carries. **Only a complete
lease pass examines anything**, and it examines only the lookups ingested before its
lease read began: a lookup ingested while the leases were being read may postdate
them, so it waits for the next complete pass. A lookup is examined once by a lease
pass after it was ingested and not again; a lease the firewall reports only after
that pass would not resolve it.

**With the local data in use, the examination waits for both sources.** Since the
resolver-cache cycle a lookup's single examination resolves through the leases
and then the local data, so when a `resolver_local_data` provider is active it
waits until both have been read after the lookup was ingested: a complete lease
pass examines only the lookups ingested before the earlier of its own read and the
last local-data read, and a local-data pass examines, the same way, once a
complete lease pass has run in this process (or at once when no lease backend is
active). A provider's first local-data poll ever covers no instant before its own,
so it does not count as such a read. A local-data pass that failed does not hold the
lookups back; its failure is in its own availability row. The instants are kept in
memory, so after a restart the examination waits until both sources have been read
once — at most the slower interval — and loses nothing, because the mark is stored.

**What makes a lease pass complete (defect L1 of
`specs/SPEC-test-budget-and-two-live-defects.md`).** A pass is complete when it read
at least one backend's lease table and every active backend it did not read is one of
two:

- **a backend that cannot be read by design** — one whose implementation answers
  `ErrUnsupportedRead`, the ISC plugin today. It contributes no lease at this pass or
  any later one, so it never makes a pass incomplete. Before this rule an ISC plugin
  active beside Dnsmasq kept every unresolved lookup waiting for a complete pass that
  could not come;
- **a backend that has failed on every one of the last
  `lease_backend_failure_pass_limit` passes** (default 3, see `setting`). Each failure
  is reported in its `source_availability` row as before, and its detail adds how many
  lease passes in a row it has failed and the limit, and, once the limit is reached,
  that unresolved host names are re-examined without it. Until the limit, a failing
  backend holds the examination back, because one or two failed passes are what a
  restart costs and examining the lookups then would spend each one's single
  examination on a partial table; from the limit on, the other backends' leases are
  the table there is. A backend read again ends its count. The count is kept in
  memory, so a restart begins it again: the examination is delayed by at most the
  limit, never forgone. The diagnostic `Unresolved host
names by cause` in `sql/queries/diagnostics.sql` sorts every `unknown_hostname`
lookup into the cause that left it so, and every `local_data_hostname` lookup into
its own bin, *resolved by local data*; its last bin is now *neither a lease nor
local data names the host*. Since group C of `specs/SPEC-step-5b-widget-api.md` every
`dual_stack_hostname` lookup has a bin of its own too, `dual_stack`: the local data
named one machine under one address of each family and the lookup reported no
querying family. A `local_data_hostname` lookup the querying family resolved stays in
the `local_data` bin, since it is resolved.

**Identity: `lookup_key`, unique — and `opnview` composes it, because the
endpoint's own identifier does not exist.** `search_queries` returns a `uuid`
field and it is **null on every row**, measured against a live firewall on
2026-09-27 (survey, *Verified against a live firewall*). The column was called
`lookup_uuid`; it is renamed because a name that says uuid while holding a hash
is worse than a comment that says it — the comment is next to the column, the
name travels into every query somebody writes.

For the Unbound provider the value is `content:` followed by the SHA-256 of the
resolver name, the lookup instant, and the row's own `client`, `domain`,
`action`, `source`, `rcode`, `dnssec_status` and `blocklist`, joined by a unit
separator. It has to be content-addressed because the endpoint is a **ring
buffer with no cursor and no working window**: every pass re-reads rows already
stored, and without this each pass would insert them again and multiply every
per-client lookup count by the number of passes that saw it.

**The trade, which is a real loss and is chosen deliberately.** Two genuinely
distinct lookups by the same client, for the same domain, with the same verdict,
in the *same second* collapse into one row, and such a pair is under-counted by
one. The endpoint returns nothing that could tell them apart — no identifier, no
sub-second instant, no sequence — so the only alternative is a key that is not
stable across passes, which would over-count *every* row on *every* poll instead
of under-counting a rare coincidence. An under-count of identical lookups is
also the direction that cannot invent traffic. A provider that does supply a
stable row identifier stores it here unchanged, which is why the column says
`key` and not `digest`.

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

**What answered it.** `answer_source` holds the endpoint's `source` verbatim.
Unbound writes `Recursion`, `Local`, `Local-data` and `Cache`, and `action` is
read from `Pass`, `Block` and `Drop` (survey, data source 5, step-5 value sets).
The attribution rule below admits `Recursion`, `Cache` and `Local-data` — the
last being a host override — and `store.AttributionAnswerSources` holds that set.
The endpoint's `client` may be a **hostname** rather than an address when the
resolver resolved it, and such a row matches no flow by address.

**The interface behind it, since step 5A (closes G6).** `interface_id` and
`interface_lookup_state` are resolved from the **same membership evidence** as a
flow's ends (*Classification* below): `resolved` with an interface, `not_found`
when no evidence places the address, and `pending` — the column default — until
the classification pass has looked. A `CHECK` ties `resolved` to a non-null
interface. Re-pointing a client identity re-points its lookups too.

**Retention:** purged by `looked_up_at`, **except a lookup an attribution of a
surviving flow still names**: the lookup was made up to the attribution delay before
its flow, so it can lie just before the horizon while the flow lies just after it,
and purging it would take the flow's site name with it. A lookup older than the
horizon is therefore kept when, and only when, such an attribution names it; the
flows are purged first, taking their own attributions with them. Purging a lookup
cascades to every attribution inferred from it, because an attribution with no
lookup behind it could not be judged. It does **not** touch the `blocklist` row it pointed at.

**Indexes:** `idx_dns_resolution_looked_up_at` for the purge,
`idx_dns_resolution_client` for the per-client window the collector asks for,
and `idx_dns_resolution_blocklist` so "which list refused what, over this
period" is an index search rather than a scan of a growing table. That index's
leading column is NULL-bearing, and SQLite keeps the unattributed blocked
lookups at its head rather than omitting them, so it serves the *list not
recorded* rows too. `idx_dns_resolution_domain_name`, on
`(lower(rtrim(domain, '.')), looked_up_at)`, is the resolver-cache attribution's
search from the names an address was held under to the lookups of those names; it
indexes the comparison itself, so no second copy of the domain is stored.
`idx_dns_resolution_name_client_id`, on `(lower(rtrim(domain, '.')), client_id,
looked_up_at)`, and `idx_dns_resolution_name_client_address`, on
`(lower(rtrim(domain, '.')), client_address, looked_up_at)`, bound that search by the
lookup's client as well (B2 of `specs/SPEC-step-5b-widget-api.md`): the statement
`attribution_name_lookups` is two disjoint branches joined by `UNION ALL`, one per
side of its client predicate — by `client_id`, and by the address when either side
has no `client_id` — and each is an index search on name and client, so a popular
name looked up by many clients is no longer read whole before the client filter.
The address branch has an index of its own rather than a rewrite onto
`idx_dns_resolution_client`, because that index is bounded by client and not by name
and would read every lookup the client made in the window. `sql/schema-checks.sh`
asserts both branches' plans at both seed sizes. **Write cost:** `dns_resolution` is
a growing table, and each of the two indexes adds one entry per stored lookup —
about the size of the domain name plus two integers — and one B-tree insertion per
ingest, on top of the six indexes the table already carried.

### `domain_attribution` — the site name a flow was given

**Identity:** `flow_id`, the primary key. One flow carries at most one site
name.

**The method is recorded on every attribution: `method`, constrained by a `CHECK`
to `resolver_cache_answer` or `lookup_timing`.** This **reverses decision D3** of
`specs/SPEC-correlation-classification-aggregation.md`, which refused a method
column because every attribution then came from one method and a field with one
possible value states nothing. The resolver-cache cycle
(`specs/SPEC-resolver-cache-attribution.md`, C3) added the second method, so the
checks now assert the column, its `CHECK` and both values in the seeds, where they
used to assert its absence. `address_observation_id` names the A or AAAA record a
`resolver_cache_answer` matched, and a `CHECK` requires it exactly for that method.
What the model carried before it still carries: the lookup itself, as a mandatory
foreign key — an attribution cannot exist without the resolver lookup it came from,
and inserting one fails — and `correlation_delay_seconds`, the delay between that
lookup and the flow, stored and queryable, so a wide delay can be treated as a weak
attribution.

**A flow with no attribution is a first-class case**, not an omission: the
Client query returns it with its address, country and operator and a null site
name.

**The rule, written once in `internal/store/attribute.go` and
`internal/store/attribute.statements.sql`.** The query report carries **no answer address** —
`search_queries` reports who asked and for what, never what the answer was (survey,
data source 5, *Response shape*) — and the resolver's **cache** does: it holds the
A, AAAA and CNAME records of the names it resolved, with the seconds each has left
(`resource_record_observation`). So the rule takes **exact evidence first** and
falls back on timing:

- **exact evidence, `resolver_cache_answer`.** An eligible lookup of `D` by the
  flow's client, made at most `attribution_max_cache_answer_delay_seconds` (default
  3600) before the flow, is an **exact candidate** when the flow's destination is
  among `D`'s answer addresses **over the whole interval from the lookup to the
  flow**: the cache's CNAME observations from `D`, every link's coverage interval
  holding both instants, lead to an A or AAAA observation of the destination whose
  coverage holds both too. A CNAME chain is followed through at most 11 links,
  Unbound's own `max-query-restarts` default, and a loop yields nothing. For a
  lookup answered from `Local-data`, a local-data A or AAAA record of `D` holding
  the destination counts as well (decision 6). The flow is attributed
  `resolver_cache_answer` **exactly when its exact candidates name exactly one
  distinct domain**; two or more — a shared content-network address the client
  reached under two names it looked up — give no row. The window is the evidence's
  own validity, and the setting only caps it (decision 2);

  **No CNAME is followed into the local data** (`specs/SPEC-resolver-cache-follow-ups.md`,
  scope 1). A local-data A or AAAA record is evidence for one lookup only: a lookup
  answered from `Local-data`, of the record's **own** owner name. A cached CNAME chain
  whose last name is a local-data owner gives no local-data evidence for the name the
  chain starts from. **The reason is how Unbound answers**: local data answers a query
  that matches it for the query's own name (`unbound.conf(5)`, local-zone types
  `static`, `transparent` and `typetransparent`), and the one CNAME local data may
  carry, at the apex of a `redirect` zone, proceeds with upstream resolution "that
  does not include the lookup in local zones" (`unbound.conf(5)`, local-zone type
  `redirect`); a CNAME the iterator follows is resolved by recursion, not from local
  data, so treating its target's local data as the answer would name a flow from
  evidence Unbound never used. **Three places read the rule**, and all three stop at the
  looked-up name: `exactCandidates` (`internal/store/attribute.go`), which builds the
  exact candidates above; `answerAddresses` (`internal/store/records.go`), which counts
  the local data of the name itself and never of a name a CNAME reached; and the
  diagnostic `live_timing_fallback_other_family_cached`
  (`sql/queries/diagnostics.sql`). The code states it once, beside `MaxCNAMELinks` in
  `internal/store/records.go`;
- **timing, `lookup_timing`, the fallback.** With no exact candidate, the 5A rule
  unchanged: a flow gets an attribution **exactly when** the eligible lookups by the
  same client in `[observed_at − max delay, observed_at]` name **exactly one
  distinct domain**; two lookups of that same domain still attribute, two distinct
  domains attribute nothing. **It is suppressed by contradicting evidence**
  (decision 3): when the cache held answers of the destination's address family for
  that domain at the referenced lookup's instant, and the destination was not among
  them, no row is written. Answers of the other family say nothing about this
  destination and suppress nothing;
- **eligible flows** have an outside destination (`dst_interface_id IS NULL`), are
  not to this firewall (`dst_is_this_firewall = 0`), and are not a second leg
  (`pair_outcome` is not `second_leg`, decision 2); an east-west flow is never
  attributed, nor a flow to this firewall, and a flow attributed before it became
  ineligible — its destination placed inside or recognised as this firewall since,
  or paired as a second leg — loses its attribution at the next pass over its
  window (`attribution_ineligible`);
- **eligible lookups** passed (`action = 'pass'`), were answered by
  `Recursion`, `Cache` or `Local-data`, and named one address
  (`client_resolution` is `logged_address`, `lease_hostname` or
  `local_data_hostname`);
- **the same client** means the same `client_id` where both rows carry one, and
  the same address otherwise;
- the **max delay** is the `attribution_max_delay_seconds` row of `setting`,
  default 5, a positive whole number;
- `site_name` is the domain the client looked up, **verbatim** — never a CNAME
  target, and a domain is never invented — the referenced lookup is the latest of
  the matching ones, the lookup key breaking a tie so no choice depends on the
  order rows were stored in, and `correlation_delay_seconds` is never negative;
- **late arrival** is decided again: a pass that stores lookups re-derives the
  flows up to the larger of the two delays after them, and a pass that stores
  resolver records re-derives the flows inside the interval over which what the
  resolver held changed, so the result is independent of the order flows, lookups
  and records arrive in;
- **re-pointing and ineligibility apply to both methods**, and a second derivation
  over the same rows writes nothing.

**`aggregate_mode = 'no_domains'` does not change what is written** (decision 7).
Attributions, `domain_volume_aggregate_*` and the resolver's records in
`resource_record_observation` are stored whatever the mode; the mode withholds
names at the API, which is step 5B's. A user who turns the mode off again
therefore finds the history intact.

**Attribution rate.** One definition, held twice: `store.ReadAttributionRate`
and the query `-- diagnostic: Attribution rate per client` in
`sql/queries/diagnostics.sql` both count, as eligible, the flows with an
**outside** destination, a source client, no end that is this firewall — a flow
from this firewall is outside the per-client rate — and that are not a second leg,
and as named those carrying an attribution — **per method as well**
(`NamedByCacheAnswer`, `NamedByLookupTiming`; the diagnostic's
`named_by_resolver_cache_answer` and `named_by_lookup_timing`), the two summing to
the named total. The store function also says whether the rate is **defined** at
all: it is **undefined** when opnview holds no evidence that a resolver source
answered for the window -- no lookup stored for an instant inside it and no
reachable `dns_lookup` probe inside it -- because the availability row holds only
the latest probe, and a resolver reachable now says nothing about last week. With
coverage and nothing named it is 0. That meaning is 5A's and is unchanged; beside
it, `CacheCovered` says whether the cache was read for the window — a cache record
whose coverage interval meets it — and so whether the exact method could have
named anything. It is the figure the UI must expose per client.

**Retention:** removed by the cascade of either parent, and by its own
`attributed_at`. The address observation it names is kept by the purge for as long
as it is (`resource_record_observation`).

**Indexes:** the primary key, `idx_domain_attribution_resolution` so the cascade
from a purged lookup is an index search, and
`idx_domain_attribution_address_observation` so the purge's question "does an
attribution name this record" is one too.

### `resource_record_observation` — one record the resolver held, over the polls that saw it

**Source:** the active providers of two kinds — `resolver_cache`, reading
`/api/unbound/diagnostics/dumpcache`, and `resolver_local_data`, reading
`/api/unbound/diagnostics/listlocaldata` (survey, *Resolver cache and local data,
read from source for the resolver-cache cycle*). The dump's fields are `host`,
`ttl`, `type`, `rrtype` and `value`, the local data's `name`, `ttl`, `type`,
`rrtype` and `value`; `type` is the record's **class**, `IN`, and is never read as
its type. The cache's **A, AAAA and CNAME** records are stored, and the local
data's **A, AAAA and PTR** records; every other type is counted in the read's
availability detail, with the response's size and the time the read took, and not
stored. A record whose `ttl` is not a whole number of seconds (cache) or whose
`value` is not valid for its type — an A record that is not an IPv4 address, a PTR
record whose owner encodes no address — is skipped and counted.

**Columns:** `held_in` (`cache` or `local_data`); `owner_name`, lower-cased with
the trailing dot removed (RFC 4343); `rrtype` verbatim; `value`, the address in
canonical text form (RFC 5952) or the target name normalised like the owner;
`address`, the address the record maps a name to — an A or AAAA record's value, the
address a PTR record's owner encodes (RFC 1035, section 3.5; RFC 3596, section 2.5),
NULL for a CNAME; `host_label`, for local data only, the first label of the host
name the record names; `first_seen_at` and `last_seen_at`, the first and latest
polls that saw it; and the **coverage interval**.

**The coverage interval** (`covered_from_at`, `covered_until_at`) is `opnview`'s
own derivation (*Vocabulary*). It runs from the **last successful poll before the
first poll that saw the record** — read from `resource_record_read`, so a failed
poll or a restart leaves it at that earlier successful poll, and with none it is the
first poll's own instant — to the **latest poll's instant plus the `ttl` it
reported**, the seconds the resolver will serve it no longer than. Local data has no
remaining TTL — its `ttl` is the configured one — so its interval ends at the latest
poll that saw it. A record evicted before its TTL makes the upper bound an
over-statement, recorded under *Limitations* in the README; a record inserted and
expired between two polls is never seen.

**Bounded growth.** A record seen by two consecutive successful polls of its
provider is **one** observation, extended: `last_seen_at` moves to the newer poll
and `covered_until_at` to the later of the two bounds. A record that reappears after
a poll that did not hold it starts a new observation. The table therefore grows
with the distinct records the resolver held and their comings and goings, not with
the number of polls — the diagnostic `live_resolver_records_per_hour` shows it.

**Identity:** `(provider_id, owner_name, rrtype, value, first_seen_at)`. It
includes the provider, so both kinds admit several concurrently active providers.

**Why the local data is not a `state_snapshot`.** Scope D asked for
`state_snapshot` and `state_item` to be reused where they fit, and local data is a
complete set replaced on every poll — the reconciled shape. They do not fit, for two
reasons the attribute rule and the growth rule state: a host name is **matched** on
(its first label, against the name a lookup logged), and `state_item.attributes` is
tier 2 of the attribute rule — displayed, never filtered on or joined; and a snapshot
per poll grows with the number of polls, which is exactly what this table's bounded
growth exists to avoid. "What the local data held at instant T" is the coverage
interval's question, answered by the same columns as the cache's.

**Retention:** purged by `covered_until_at` — a record whose coverage ended before
the horizon can no longer have been held at any instant the history keeps —
**except one an attribution of a surviving flow names**; the flows go first, taking
their attributions with them, and the foreign key from `domain_attribution` makes
forgetting the exception an error rather than a dangling id.

**Indexes:** `idx_resource_record_observation_address` (`address`,
`covered_until_at`), where the exact attribution starts from a flow's destination;
`idx_resource_record_observation_cname_target`, partial on CNAME records, the climb
from an answer's name back to the names that pointed at it;
`idx_resource_record_observation_owner`, the descent from a looked-up name to its
addresses; `idx_resource_record_observation_host_label`, partial on local data, the
resolution of a logged host name; and `idx_resource_record_observation_covered_until`
for the purge and the coverage read. No statement scans the table, at either seed
size.

### `resource_record_read` — the last successful poll of each resolver-record provider

One row per provider of `resolver_cache` and `resolver_local_data` that has been
read successfully: `read_at`, the instant of its last successful poll. It is what
makes the coverage interval's lower bound survive a failed poll and a restart. A
poll that was refused, not found, undecodable or answered a failed status does not
move it. **Bounded**, never purged.

### `geo_asn` — the geo and ASN enrichment of one address

**Source:** the active provider of the `geo_asn` kind — today the MaxMind
GeoLite2 City and ASN databases, the second of the three outbound calls the
project allows, acquired and read by `internal/maxmind` (step 4C; see
`specs/SPEC-maxmind-geolite2.md`). The databases are downloaded with the
account ID and the licence key, checked once a day and replaced only by a newer
build that opens, and kept in `geoip/` in the data directory. A lookup reads
them and contacts nothing. Every public address the stored flows carry, as
source or destination, is looked up; a private, loopback, link-local or
otherwise non-public address is never looked up and has no row. An address
looked up against an older build than the one in use is looked up again.

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

**`src_is_this_firewall`** is 1 when the event's source is this firewall, as
classification decides it for a flow's end (*Classification*): such a source
carries the interface its address belonged to and no client.

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

Exactly one row per `provider` row, created by the schema in the
`unavailable` state with the probe recorded as not yet run, because an absent
row is something a screen could not tell apart from a healthy provider. No
source can be distinguished from silence by an empty result alone (survey, gap
11), so each row carries its state, the instant it was determined and the probe
that determined it. A `CHECK` rejects any state outside `reachable`,
`present_but_disabled` and `unavailable`, and the primary key is a foreign key,
so an availability row for a provider that does not exist cannot be inserted.

There is no `CHECK` enumerating provider names here: registering a provider is
an `INSERT` into two tables, never a schema change.

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
| `geo_asn` | the local dataset files, after each refresh | `reachable` exactly when both databases are on disk and open; never downloaded, or no download that produced one, is `unavailable`. The probe records what the last refresh found — current, no licence key, no account ID, refused, limit reached, failed — so a failed refresh over databases still in use is `reachable` with the failure as its probe. The row describes the **dataset**, while `geo_asn.lookup_state` describes a single address lookup |

Availability is reachability. Which provider `opnview` reads is
`provider.is_active`, and the diagnostic query
`-- diagnostic: Source availability` in `sql/queries/diagnostics.sql` returns
both, one row per registered provider. **The Sources surface** (`/sources`) draws
that query row for row (`store.SourceAvailability`), beside the operator's selection
of each provider and whether it counts towards an outage, with the conditions as the
*Source availability* preset of
`docs/widget-catalogue.md` names them and `detail` verbatim as data; below it, every
`collection_gap` detected in the last 24 hours (`store.RecentCollectionGaps`, through
`idx_collection_gap_detected_at`). The top bar of every signed-in page counts the
current **outages** (see *Vocabulary*) and links there.

**The outcome code** (`SPEC-step-5b-widget-api.md`, D1). Beside the state, each
row carries `outcome`: what the last probe or read turned into, from a closed set
a `CHECK` holds (`store.AvailabilityOutcomes` is the same set, and a test compares
the two). **Every collector writes the code with the detail, and no code reads
`detail` to decide anything** — a test walks every Go and SQL file and fails on a
comparison or match against it. `detail` stays, verbatim, as diagnostic text; the
code is what an interface translates and what the widget API carries. The state
and the code are independent facts: a MaxMind refresh that failed over databases
still in use is `reachable` with `download_failed`. A probe round that finds a
source healthy keeps the code of the last read or sampling pass it restates in the
detail, so a row's code never contradicts its words.

| Code | Meaning |
|---|---|
| `not_yet_probed` | the row the schema creates; nothing has asked yet |
| `healthy` | the probe or read answered and was read |
| `healthy_empty` | the read answered and returned no record: reachable and silent, which is not an absence of activity |
| `partial_readings` | a sampling pass answered and some of its readings did not; the detail names them |
| `present_but_disabled` | the component runs and the setting this source needs is off — a configuration state the user can change |
| `service_not_running` | the service's status endpoint reported something other than running |
| `authentication_failed` | HTTP 401: the API key and secret were refused; no privilege cures it |
| `denied` | HTTP 403: the key's user lacks the privilege for the path |
| `not_found` | HTTP 404: the firewall does not answer the path |
| `server_error` | any other unsuccessful HTTP status |
| `empty_body` | HTTP 200 with an empty body, a backend failure |
| `result_failed` | HTTP 200 with OPNsense's `{"result": "failed"}` envelope |
| `undecodable` | an answer whose body is not the shape the endpoint returns, or lacks the field it exists to carry |
| `status_not_ok` | an envelope whose `status` is not `ok` — the Unbound diagnostics controller's `failed` when Unbound is not running |
| `transport_failure` | no answer at all: the firewall was not reached |
| `certificate_refused` | the firewall answered with a certificate `opnview` does not accept |
| `request_not_made` | the probe or read ended before any request reached the firewall: no firewall URL configured, or a request `opnview`'s own guards refused |
| `plugin_not_installed` | the end-of-life ISC DHCP plugin answered 404 |
| `read_not_supported` | an implementation that answers and whose material `opnview` does not read, its field contract unverified (the ISC plugin) |
| `no_cache_read` | a resolver that offers no cache read through the API (Dnsmasq) |
| `no_local_data_read` | a resolver that offers no local-data read through the API (Dnsmasq) |
| `log_grammar_not_recorded` | a resolver whose lookups are exposed only as free text under a grammar nobody has recorded (Dnsmasq) |
| `no_licence_key` | the MaxMind refresh with no licence key stored |
| `licence_key_undecryptable` | a stored licence key the key file beside the database cannot open |
| `no_account_id` | the MaxMind refresh with no account ID stored |
| `download_refused` | a download the server refused, its credentials not accepted |
| `download_limit_reached` | a download refused because the account's daily limit is spent |
| `download_failed` | a download that could not be completed |
| `download_malformed` | a download that fetched something that is not the Public Suffix List |
| `store_failure` | the source answered and `opnview` could not store what it returned: the fault is `opnview`'s database, not the source (the lease pass, `SPEC-step-5b-widget-api.md`, B8g, and since the 5B correction round the DNS-lookup pass and the resolver-cache and local-data record passes) |
| `listener_not_configured` | a provider its push entries feed, while no listener of their protocol on a transport they accept is open: nothing can arrive (`docs/plugins.md`, *Push sources*) |
| `no_message_received` | the same provider while its listener is open and nothing it accepts arrived within its liveness span |

**Retention:** never purged. **Indexes:** the primary key.

### `pair_volume_observation` — daily per-pair volume, derived from `flow`

**IT IS DERIVED, NOT COLLECTED, AND THAT IS THE MAINTAINER'S RULING.** No
collector writes this table and none will. The only per-pair endpoint the survey
found carries **neither a port nor a protocol**, and `flow` carries both exactly,
from the filter log — so these rows are **step 5's to compute from `flow`**, and
nobody should go looking for the collector that fills them. Since step 5A the
derivation exists: `insert_pair_volume` in `internal/store/pairvolume.statements.sql`, run with
the rest of a day's slot. A test fails if any non-test code other than that
statement and the retention purge writes the table.

The **sampled** per-pair volume is a different thing and lives elsewhere: it is a
live rate snapshot rather than a period total, and it is in `measurement_sample`.
`flow_volume` is therefore a kind with a registry row and no implementation; see
*Providers*.

**Origin of the shape:**
`/api/diagnostics/networkinsight/top/FlowSourceAddrDetails/...`
with a field list containing both `src_addr` and `dst_addr`, data source 3. The
key is kept exactly as surveyed, because a per-pair figure summed over a day is
direction-free whether it came from the firewall's own aggregate or from
`opnview`'s own records.

**The de-duplication key is explicit, and this is the point of the entity.** The
unique key is direction-free and interface-free:

```
(day_start_at, endpoint_low, endpoint_high, service_port, protocol)
```

where `endpoint_low` and `endpoint_high` are the two addresses in lexicographic
order, enforced by a `CHECK`. Both directions of a pair collapse into one row.
The interface and pf `dir` of the day's first flow of the pair are kept for
provenance only and are not part of the key.

**`service_port` is the flow's real `dst_port`**, since step 5A, and **0** for a
protocol with no port. It used to be documented as Insight's
`min(src_port, dst_port)`, a heuristic; the filter log carries the exact port, and
the derivation reads it. `octets` is the sum of `packet_bytes` — logged bytes, see
`flow` — and `packets` the number of logged records; both stay `REAL` because the
key and the columns are Insight's shape.

**Retention:** purged by `day_start_at`. **Indexes:** the uniqueness
constraint, plus `idx_pair_volume_day`.

### The aggregate families — six keys, four calendar periods each

Twenty-four materialised tables in six families, all computed from `opnview`'s own
`flow` by `internal/store/aggregate.statements.sql` and refreshed **together, slot by slot**, on
the one contract below:

| Family | Key, within a period slot | Closes |
|---|---|---|
| `volume_aggregate_*` | source interface, destination interface, outside peer, `traffic_direction`, `exit_leg_device` | G3 |
| `owner_volume_aggregate_*` | owner of the inside client (NULL = unassigned), `traffic_scope` | G11 |
| `client_volume_aggregate_*` | inside client, `traffic_direction` | G4 |
| `domain_volume_aggregate_*` | site name, source client | G5 |
| `rule_volume_aggregate_*` | rule (NULL = no rule resolved), source interface, destination interface | the rule table's history beyond the flow horizon |
| `peer_volume_aggregate_*` | inside client, `traffic_direction`, the address at the other end | exact `distinct_peers` in every period |

**They are `opnview`'s primary store, not a cache over Insight**, and the reason
is survey data source 3: per-address-pair volume exists on the firewall only at
**daily** resolution and only for **62 days**, and per-source totals at 300 s
resolution are kept **one hour**. They are tables rather than views because a view
over a growing base table cannot satisfy the query-plan criterion: every read of
it would rescan `flow`.

**The four periods are calendar slots, aligned to UTC.** `_1h` is the hour, `_24h`
the day, `_7d` the **ISO week** starting Monday 00:00 UTC, and `_30d` the
**calendar month**, of 28, 29, 30 or 31 days. The suffixes are the periods a
widget asks for, and are kept; `period_end_at` says exactly where each slot ends,
so nothing multiplies a day by thirty. `store.Period` holds the slot arithmetic in
one place.

**Every family carries the allowed / blocked / unknown split**, of bytes and of
connections, and `bytes` is the sum of the three, held by a `CHECK`. "Allowed"
is `flow.action = 'pass'`; "blocked" is `'block'` or `'reject'` -- a reject is a
refusal that answers; "unknown" is a record whose action the collector could not
name, and it is counted as **neither**: a decision nobody read is not an
allowance. The step-2 screen queries and the `blocked_event` view follow the
same rule since the step-5A corrections: `blocked_event` holds every `block` and
every `reject`, and no screen counts an `unknown` decision as allowed. The
figures are **logged bytes** — see `flow` — and never conversation volume.

**A flow belongs to its inside end.** The interface family keys on both
interfaces and on `classified_flow.peer_address`; the owner and client families
on `classified_flow.local_client_id`, so a client's inbound traffic is its own as
well as its outbound; the domain family on the flow's source client, the machine
that made the lookup; the rule family on the rule the `rid` resolved to. A flow
with no inside client is in no owner or client row, and is still in the interface
and rule families, which therefore sum to every flow.

**This firewall, and the second legs** (decisions 2 and 6 of the step-5A live
corrections). A flow with an end that is this firewall is its own key in the
interface family — the directions `to_this_firewall` and `from_this_firewall`, the
scope `this_firewall` — and is never part of the outside peers; it contributes
nothing to the client, peer and owner families, since this firewall is never a
client and never a peer. A **second leg** — the `out` record of a connection whose
`in` record is counted — is in the interface family only under its own
`exit_leg_device`, the device it was logged on: every total leaves those rows out,
and they are that interface's own figure for the connections it carried. It is in
no client, peer, owner or domain row and in no per-pair volume, and it is in the
rule family, which counts every record because each is a match of its own rule. The
interface family therefore still sums to every flow, and its totals — every row
whose `exit_leg_device` is `NULL` — count every connection once.

**Identity:** one unique index per table over the period start and the key, with
every NULL-bearing column wrapped in `ifnull`, because SQLite treats NULLs in a
unique index as distinct. In the interface family `src_interface_id` is nullable —
an inbound flow's source is outside — and `CHECK`s pin `traffic_scope`,
`traffic_direction` and `peer_address` to the interfaces the row carries.

**Distinct figures are never summed across slots.** `owner_volume_aggregate_*.client_count`
and `client_volume_aggregate_*.distinct_peers` count distinct things in **one**
slot; the same machine active in two hours would be counted twice by a sum. Within
a slot they are counted from the family whose key carries the thing counted: a
client's `distinct_peers` is the number of its rows in `peer_volume_aggregate_*`
for the same slot, and an owner's `client_count` the number of distinct clients in
`client_volume_aggregate_*` for the same slot — in an hour, a composed day and a
rolled-up week or month alike. Across slots, distinct clients are counted as
distinct `client_id` over the client or domain family's rows, which is exact
because the client is in the key.
`store.ReadOwnerTotals` and `store.ReadSiteTotals` do exactly that, and a test
asserts that a client active in three hours counts once over the three, not three
times.

**The owner family's unassigned bucket is not optional.** `owner_id IS NULL` is
every inside client nobody attributed; ownership is assigned by hand, so a
per-person aggregate that dropped the unowned clients would under-report the
network while looking complete. The checks assert every period holds an
unassigned slot and that the unowned bytes land in it.

**Refresh contract** — `store.RefreshAggregatesAt(ctx, since, watermark, now, forced)`,
and `store.RefreshAggregates(ctx, since, now, forced)` for a caller with nothing
ingesting concurrently, which passes `now` as the watermark:

1. A slot is rewritten when it holds a flow whose `ingested_at` is at or after
   `since`, the instant of the previous refresh, or an hour whose purged part a
   purge wrote at or after `since` (`purged_flow_hour.purged_at`).
   `idx_flow_ingested_at` and `idx_purged_flow_hour_purged_at` find them; nothing
   scans `flow` or the purged part.
2. The **current** slot of each period — the one containing the present instant —
   is rewritten on every pass, because it is still filling.
3. A slot holding one of the **forced** instants is rewritten. A pass that
   re-points a client identity, or that changes an attribution, forces the
   instants of every flow it touched, so a slot a flow moved **out of** is
   recomputed as well as the slot it moved into. Reassigning a client to another
   owner invalidates every slot holding one of its flows, because ownership is an
   attribute of the person and not of the flow, and no flow's `ingested_at` moves,
   so rule 4 cannot see it. **The write that changes ownership records those slots
   itself** (`internal/store/owners.go`): assigning, reassigning or unassigning a
   client records, in the same transaction, every kept 1 h slot of the client family
   holding the client's traffic -- beyond the flow horizon as well as inside it --
   in `owner_reassigned_hour`, and deleting a person records every slot of the
   owner family that person stood in and removes those rows. The next refresh
   reads the recorded hours as forced instants, rewriting the hour and the day,
   week and month holding it, and deletes exactly the rows it read once it has
   rewritten them, so a change made while it runs waits for the next one. Until
   then the widget API's `freshness` counts each such hour in `stale_slots`. It is
   the one recorded invalidation of the contract: the change it records leaves no
   trace in the flows for the recognition rule to find.
4. `computed_at` is the refresh's **watermark**, not its clock: the instant
   before which every flow stamped with an `ingested_at` is known to have been
   committed when the slot was read. A filter-log pass stamps every row with the
   instant it began and commits them one by one, while other passes' refreshes run
   in their own goroutines; so the collector records each pass's stamp as in
   flight until its rows are written, and a refresh's watermark is the older of its
   own instant and the oldest stamp in flight. A slot that is neither current nor
   forced is rewritten only when it is **stale**: its `computed_at` is **not
   strictly later** than the newest `ingested_at` among the flows inside its window,
   or than the newest `purged_at` of its purged part -- equal one-second stamps
   cannot be ordered, so equal is stale -- or it holds rows while neither a flow nor
   a purged part remains inside it. The refresh selects slots from the
   `ingested_at` and the `purged_at` of the previous refresh's watermark onward.
   That comparison is the recognition rule; no dirty flag exists for anything the
   flows record, and a change of ownership, which they do not, is recorded as rule 3
   says. Two tests
   reproduce the race it closes: a refresh running before a pass's row commits, and
   one running between two rows of one pass while the slot already holds the first.

**The refresh and the retention purge are ordered.** The purge never runs between
a pass storing its rows and the end of that pass's derivation — its
classification, its attribution and this refresh — and no pass stores or derives
while a purge runs (*Purge*, below). Without that, a purge committing in the
window would move rows no refresh had read into the purged part, unplaced, and,
since rule 1 used to read `flow` alone and a forced instant comes only from a flow
that still exists, no refresh would ever select their hour again: the hour, and
the day, week and month composed from it, would lose them. **And the refresh does
not rest on the ordering alone:** rules 1 and 4 read the purged part's `purged_at`
beside the flows' `ingested_at`, so the hours a purge writes are rewritten by the
next refresh whatever ran before it, and the slot invariant would hold even were
the ordering ever broken. A test turns the ordering off to prove it.
5. ~~The 7 d and 30 d slots may lean on the firewall's daily per-pair aggregate
   for history predating opnview's own.~~ **Struck by step 5A.** The only
   per-pair endpoint is a live `traffic/top` snapshot carrying neither a port nor
   a protocol, and no endpoint returns per-pair history `opnview` could seed a
   slot from (survey, *The per-pair data is a live snapshot, not history*, and
   *What `traffic/top` measures, read from source for step 5*). Every slot of
   every period is computed from `opnview`'s own flows, and a period predating
   them shows its true coverage rather than a zero.
6. **No slot is ever emptied or frozen by the retention.** Restated by the
   step-5A corrections, whose cycle found that a retention shorter than an hour
   emptied the current slots. How each period is computed:
   - an **hour** is computed from the flows still inside it **plus its purged
     part**: the `purged_flow_hour` rows the purge wrote, before deleting them, for
     the flows it removed from that hour. So an hour holding the horizon — the
     current hour under a retention shorter than an hour — keeps what it held for
     the purged flows, takes in a flow ingested late into it, and follows a
     reclassification of the flows still present. A slot has a purged part only
     when flows inside it have actually been purged;
   - a **day** is composed from its 24 hours;
   - a **week** and a **month** are rolled up from their days, which tile them.
   The purge keeps an hour or a day slot, and an hour's purged part, for as long as
   the ISO week and the calendar month holding it, so every composition has all
   of its finer slots. Every summable figure is the sum of the finer slots'; the
   client family of every slot is regrouped from the peer family of the same slot,
   and the owner family from the client family, so `distinct_peers` and
   `client_count` are exact in every period. **The current slot of every period is
   rewritten on every pass, whatever the retention.** A slot the purge would remove
   outright is not rewritten unless it is current. The day's
   `pair_volume_observation` rows are derived from `flow` whenever the day is
   rewritten; they have no purged part, because the purge removes a day's rows once
   the day starts before the horizon.

All six families of one slot, and that day's `pair_volume_observation` rows,
are rewritten in **one transaction**, so a reader never sees one family of a slot
ahead of another.

**Aggregate mode.** The roadmap's aggregate mode — volumes, interfaces, countries
and operators without domain names — is the Map query,
`-- screen: Map` in `sql/queries/screens.sql`. It reads
`volume_aggregate_24h`, `geo_asn` and `interface`, and no table that holds a
domain name. `aggregate_mode = 'no_domains'` does **not** stop the domain family
being written: the mode withholds names at the API (step 5B), and what is stored
is unchanged.

**Observation-point limit:** traffic between two clients behind one interface
never reaches the firewall and is in none of these figures. Every figure is a
lower bound.

**Freshness and coverage** are read by `-- diagnostic: Aggregate coverage per
period` and `-- diagnostic: Owner aggregate coverage per period` in
`sql/queries/diagnostics.sql`.

**Retention:** purged by `period_end_at`, so a slot survives as long as its
window lies inside the horizon; an hour or a day slot survives, in addition, as
long as the ISO week and the calendar month holding it, because a week or a month
is composed from them (rule 6). The client, peer and domain families' `client_id`
cascades on delete, after the refresh has already re-keyed every slot a removed
client's flows moved out of. The `owner` and `rule` rows they point at are never
purged.

**Indexes:** the unique slot index per table, whose leading column is
`period_start_at`, which is also what makes a period read an index search; plus
`(client_id, period_start_at)` on the client and peer families and `(rule_id,
period_start_at)` on the rule family, for one client's or one rule's history.

### `purged_flow_hour` — the purged part of an hour

What the retention purge removed from `flow`, per UTC hour and per every key an
aggregate family reads: both ends' interfaces, the source client, the inside
client, the traffic direction, the peer (the address at the other end from the
inside end, or `classified_flow.peer_address` for a flow with no inside end), the
rule, the attributed site name and the decision, with the summed bytes and the
number of records. Since the step-5A live corrections it also keeps
`exit_leg_device`, so a purged second leg still counts on its interface's figures
only, and the peer of a flow with an end that is this firewall is
`classified_flow.peer_address`, `NULL` when the other end is not outside: the peer,
client and owner families read none of those rows. **The purge writes it**, as its first statement and in its own
transaction, for exactly the flows it is about to delete, so the attribution of
each is still there. Every hour slot is computed from the flows still present plus
these rows (*Refresh contract*, rule 6).

It keeps the classification the flows had when they were purged, with the one
exception the flows themselves obey: when the strongest identity at an address — a
lease, a MAC — replaces the address-level client its flows named, the purged part
naming that client is re-pointed to it too (`repoint_purged_local_client` and
`repoint_purged_source_client` in `classify.statements.sql`), and the hours it lies in are
recomputed with the flows. So a lease that names an address after its flows were
purged moves their bytes and their peers to the better identity rather than out of
the client, peer and owner families. An interface that changes after the flows
are gone does not reach them.

**A client the purged part names is not purged**, by the retention purge or by
the removal of unreferenced clients, so the client, peer, owner and domain
families keep its bytes for as long as the hour is kept; the foreign key's
`ON DELETE SET NULL` is only its backstop.

**A flow is added to it once, because it is stored once.** There is no unique
key: each purge appends the rows of the flows it removes. The filter-log page
keeps offering records the purge has removed — under a retention shorter than the
span of one page, every pass offers them again — so the purge records the
furthest horizon it has applied in `retention_purge`, and **the insert itself**
refuses a flow or a lookup observed before it: `store.InsertFlow` and
`store.InsertDNSResolution` read the watermark inside the `INSERT` statement. The
purge runs in its own loop and is ordered against a pass only from the pass's
first stored row to the end of its derivation (*Purge*, below), and SQLite runs
one write at a time, so a purge that commits at any earlier moment of a collection
pass — after the page was read, after a digest was checked — is seen by every
insert that follows it, and a row inserted before it is purged once. A record that would
otherwise be stored again would be counted twice, in `flow` and in the purged
part. Lengthening the retention later does not move that watermark back.

**Retention:** growing; purged as the hour slots are, once the hour, its ISO week
and its calendar month have all ended before the horizon. **Indexes:**
`hour_start_at`; `(purged_at, hour_start_at)`, by which the refresh finds the
hours a purge wrote since the previous refresh (*Refresh contract*, rule 1); and
one on each client column.

### `retention_purge` — how far back the purge has purged

One row: `purged_before`, the furthest horizon any retention purge has applied,
and `purged_at`, when the last one ran. Every flow and lookup observed before
`purged_before` may be gone, its figures moved into the purged part, so the flow
and lookup inserts refuse such a record, inside the insert statement (see
`purged_flow_hour`). It only moves forward. The purge writes it; it is absent until
the first purge with a finite retention, and `store.PurgedBefore` reads it.
**Retention:** bounded, one row, never purged.

### `owner_reassigned_hour` — an hour slot a change of ownership invalidated

One row per hour slot a change of ownership invalidated and no refresh has rewritten
yet: `hour_start_at`, the hour, and `recorded_at`, when the change was made. A change
of ownership moves no flow's `ingested_at`, so the recognition rule cannot see it; the
write that makes it records the slots here, the next refresh reads them as forced
instants and deletes exactly the rows it read, and the widget API counts them stale
until then (*Refresh contract*, rule 3). The term is `opnview`'s own: no product it
reads has owners. **Retention:** bounded, emptied by the refresh, never purged.

### Classification — which interface an address sits behind

`flow` is stored with neither end placed, and **classification** places both, in
`internal/store/classify.go` over the statements of `classify.statements.sql`.

**This firewall comes first** (step-5A live corrections, item 1). Before any
on-link evidence is read, an end of a `flow`, the source of a `security_event` or
the querier of a `dns_resolution` is **this firewall** — OPNsense's *This
Firewall*, pf's `self`, see *Vocabulary* — when either its address was held by the
firewall at the row's instant, or it is a loopback address (the IPv4 loopback
network or the IPv6 loopback address) or the name `localhost`, which are protocol
constants and not configuration (decision 11). **Held at an instant** is defined by
the `this_firewall_address` view: an `interface_address` row under `addr4`, `addr6`,
`ipv4` or `ipv6`, on any interface, with
`first_seen_at − margin ≤ t` and either `t ≤ last_seen_at + margin` or the row is
**current** — read by the latest discovery of its interface — where the margin is
the discovery interval (`refresh_interval_discovery_seconds`, 300 s by default),
because discovery reads the addresses once per interval and an address can have been
taken or released at any instant between two reads. An end that is this firewall
records the interface its address belonged to (the most recently seen holding, then
the lowest interface id; none for a loopback address) and **no client**, sets
`*_is_this_firewall`, and is never outside, never a client, never an outside peer
and never an operator node. The decision is taken row by row, as of each row's own
instant, inside the statements that place the rows. The 5A behaviour it replaces
made the firewall's upstream addresses anonymous outside ends and minted a phantom
address-level client at each of its inside addresses (findings F1).

Every other end reads **on-link evidence only**, in this order, and the first that
answers decides:

1. a **lease** naming the address;
2. a **client identity** — DHCP client identifier, then MAC — last seen at the
   address;
3. an **interface network** that contains the address (`interface_network`):
   an operator network wins over a detected one, decision D1, and the longest
   prefix wins within each;
4. the most recent **sighting** of the address in the filter log: a record
   logged `in` names the source's interface, one logged `out` the destination's —

each on a **non-upstream** interface only (`interface.is_upstream = 0`). An
address no evidence places is **outside**: it has no interface and gets no client
row. Neither a name nor an assumed addressing plan enters.

**An IPv6 link-local address** is the same on every interface, so evidence for
one counts only on an interface whose `link_local_evidence` rule is set: with the
rule at its default nowhere, such an address is outside, and setting the rule on
one interface makes it evidence there only. No network ever places one.

**A pass is incremental.** The evidence each address was placed from — the kind,
the interface, the strongest client identity — is recorded as a fingerprint in
`address_classification`. An address whose fingerprint is unchanged has only its
rows with an **unplaced end** placed: an end the collector stored, which has no
interface yet. An address whose evidence changed, or that was never placed, is
placed in full: every row naming it is rewritten, the clients nothing names any
more are removed, and the new fingerprint recorded. Every statement writes a row
only when a value changes, so a pass with nothing new writes no row. A change of
which interfaces are upstream, of a link-local rule or of the networks that count
is compared before every derivation (`store.OnLinkFingerprint`), and every stored
address is then placed again — each in full only when its own evidence moved. **This
firewall's addresses are part of both fingerprints**: an address it starts holding,
an address on another interface, or a new discovery interval places every stored
address again, and an address it ever held has its holdings in its own fingerprint,
so the phantom clients 5A minted at the firewall's addresses are removed by the
first such pass once nothing refers to them, and the slots they fed are recomputed.
`last_seen_at` is left out of both, since it moves at every discovery.

The pass walks the distinct addresses of `flow`, `dns_resolution` and
`security_event` in sorted order, in chunks of one transaction each, so its
outcome does not depend on the order in which records arrived: ingesting one
fixture in two orders yields the same rows. When better evidence arrives — a
lease that later names an address seen only in flows — the earlier `flow` and
`dns_resolution` rows are **re-pointed** to the better identity, the address-level
client left with no reference is removed, attribution is re-run for the flows
touched, and their slots are forced into the next refresh. A first discovery, or a
discovery whose interfaces or addresses changed, reclassifies everything.

### Two legs of one connection — the pairing

pf writes the packet that creates a state, and a connection crossing the firewall
creates one on the interface it arrives on and one on the interface it leaves by
(survey, *Two legs of one connection, read for the step-5A live corrections*). So a
client's connection to the Internet is logged `in` on the client's interface and
`out` on the upstream one; over IPv6, and between two interfaces, it is logged once
per interface with no translation. 5A counted every record (Scope A2, OQ4), and on
the live firewall most of its outbound volume was counted twice.

**The rule** (`internal/store/pair.go` over the `pairing_*` statements of
`pairchunk.statements.sql`). An `out` record on an interface is the **second leg** of an `in`
record on another interface when:

- both carry the same protocol, IP version, destination address and destination
  port;
- every logged field verified to cross the firewall unchanged is equal: `ip_id`,
  the IPv4 identification, and `tcp_seq`, the TCP sequence number
  (`store.PairingFields` holds the verdict on every candidate field, and a test
  fails if the pairing reads one whose invariance is unverified);
- their `observed_at` lie within `leg_pairing_window_seconds` of each other
  (default 1, decision 4);
- the `in` record passed;
- the pair is one-to-one: each record is the other's only candidate.

**Translation.** Under outbound NAT the sources differ: the second leg's source is
this firewall's upstream address. pf translates before it filters (pf.conf(5));
that the logged `out` record carries the translated source is what the live run of
5 October 2026 showed, and the survey marks it so, since no documentation says which
form of the packet `log` writes. The rule reads nothing of the second leg's source
under NAT but that it is this firewall. Without NAT — IPv6, and between two
interfaces — the sources are equal, with their ports, and the same rule pairs them
(decision 5).

**How it is decided.** From the stored records alone, so the result does not depend
on the order the records arrived in; again whenever either leg arrives later, because
every pass that stores or places a record asks for the pairing of its window; and
idempotently — a pass that changes nothing writes nothing. Each record names its
partner: `pair_outcome` `first_leg` or `second_leg` and `paired_flow_id`. A partner
the retention purge removed leaves the other record as it was, which is why
`paired_flow_id` carries no foreign key.

**The outcomes of an upstream `out` record sourced by this firewall.** Every such
record has exactly one:

- `second_leg` — the second record of a client connection;
- `not_paired` — no first leg was found. The record keeps its this-firewall end, but
  `opnview` **never claims it is the firewall's own traffic** (decision 3, as amended
  on 7 October 2026): a client connection whose first record opnview cannot see — a
  rule that does not log, a non-logging automatic port-forward rule, an `rdr pass`,
  which no ruleset reading can see, a rewritten identification or sequence number, a
  first leg further than the window — looks exactly the same. An inference from the
  rules' logging was tried and removed for that reason.

No unpaired record is given a client: its source is this firewall, which never is
one.

**Counting** is decision 2, under `flow` and *The aggregate families*: a paired
connection counts once, on its first leg.

**A long span is paired in chunks, with the same result** (B8b of
`specs/SPEC-step-5b-widget-api.md`). A reclassification of everything can move records
across the whole retained history, and the pairing they call for used to read every
`out` record of that span into memory inside one transaction, holding the single
connection — and every collector's write — for as long as the history is long.
`store.Pair` now cuts a span longer than `store.PairingChunkSeconds` (one hour, the
finest aggregate slot) into chunks that tile it without overlap, decides every chunk's
records first into one decision for the whole span, then writes each chunk against
that decision in a transaction of its own. The decision for one record reads logged
fields only, never the pairing columns, and the one step that needs more than a record
— clearing a partner a record no longer has, unless another record now pairs with it —
reads the whole span's decision, so the outcome is exactly that of one transaction over
the span; a test asserts it on a fixture spanning several chunks with stale partners
handed across them. A span within one chunk, the collectors' everyday case, is decided
and written in one transaction as before.

**The cost of a reclassification of everything — measured, with no budget** (B8h,
decision 17). `sql/schema-checks.sh`, when `OPNVIEW_RECLASSIFY_COST=1` is set (out of the pre-deployment
command since 16 October 2026: the figures below do not fit its 180 s budget), times
`ReclassifyEverything` twice on the copy of its million-row seed it has just timed the widget
response budgets on, rather than building a second million rows in Go (which took 4 min
39 s): the second run moves nothing and measures the floor. Re-measured on 10 October 2026
in the development container on 16 CPUs, over 1 000 202 flows, in the two pre-deployment
runs of the 5B correction round: **2 min 4 s and 1 min 32 s** for the first run, which
re-places every address and re-pairs and refreshes what moved, and **9.5 s and 5.6 s** for
the second, which moves nothing. The spread between two runs of the same command on the
same seed is that wide, so these are figures of an order, not a budget. The first
measurement, 1 min 43 s and 7.4 s, was taken on a seed built in Go from the same file with
the same parameters. Of the first run, the
part a pass holds the single connection for is now one chunk of pairing or one slot of
refresh at a time, not the whole.

### What step 5B reads — the store's contracts

Step 5B serves widgets over HTTP and computes nothing the store already answers.
Each function below is in `internal/store` unless named otherwise, takes UTC epoch
seconds, and treats a window as half-open, `[from, to)`.

| Function | Contract |
|---|---|
| `ReadVolumeWindow(ctx, from, to, now)` | the volume of a rolling window: whole hours from the 1 h slots, the partial hours at either edge from `flow`. Inside the flow horizon it equals a computation over `flow` alone; beyond it the slots answer, and `Covered` says which interval they cover. `ByDirection` splits it by `traffic_direction`, this firewall's two directions included, so this firewall is its own key and never part of the outside column. **A paired connection counts once** (decision 2): `Total` and `ByDirection` leave out the rows whose `ExitLegDevice` is set, and `ExitLegs` sums those per device — what an interface's own figures add for the connections whose first leg was logged on another interface |
| `ReadVolumeSeries(ctx, from, to, bucket, now)` | the same window in buckets aligned to one period's slots. The buckets sum to the window total. A bucket inside a covered, gap-free interval with no rows is 0; one outside coverage, or overlapping a `firewall_log` collection gap, is marked `Gap` and carries no figure |
| `ReadConnectionTree(ctx, from, to, root)` | both sides of the connection tree. Every flow lands in exactly one node per level on each side, with an explicit `none`, `unassigned`, `unplaced:miss`, `unplaced:pending` or `unplaced:no_row` node rather than a dropped flow, so a level always sums to the level above. **This firewall has a node on each side**, `this_firewall`: an end that is this firewall lands there and never under an operator, an unplaced condition, `interface:none` or `client:none` — inside when the other end is outside, with `this_firewall` again below it for the roots with a second level, and outside when the other end is an inside client. A second leg is left out, its connection being its first leg's |
| `ReadAttributionRate(ctx, from, to, clientID)` | the attribution rate of one client, or of all, over the flows with an outside destination, a source client, no end that is this firewall, and that are not a second leg. **Undefined** (nil) when opnview holds no evidence that a resolver source answered for the window -- no lookup stored inside it, no reachable `dns_lookup` probe inside it -- or when there is no eligible flow; **0** only when one answered and named nothing. Since the resolver-cache cycle it also returns the named flows **per method**, `NamedByCacheAnswer` and `NamedByLookupTiming`, which sum to `NamedFlows`, and `CacheCovered`, whether a cache record's coverage interval meets the window; the per-client diagnostic counts the same figures. The method of each attribution is a column of `domain_attribution`, so a site-name preset carries it per record |
| `AnswerAddresses(ctx, name, at)` | the addresses the resolver's cache held for a name at an instant, through CNAME observations each covering it, at most 11 links; a loop yields nothing beyond the names before it |
| `ReadSiteTotals(ctx, period, from, to)` | the domain family summed over slots, with distinct clients counted, never summed |
| `publicsuffix.Refresher.Group(sites)` | site totals collapsed into registrable domains; `GroupingUnavailable`, and nothing grouped, before the first successful list download |
| `ReadOwnerTotals(ctx, period, from, to)` | the owner family summed over slots, the unassigned bucket included, distinct clients counted from the client family |
| `ReadInterfaceClients(ctx, from, to)` | per interface, the clients seen in the window out of those known |
| `ReadBlockedDecisionCounts(ctx, from, to)` | the refusals of the window per `engine_kind`; each equals a direct count of its source rows |
| `ReadRuleLogging(ctx)` | per interface, the rules that do not log; a rule whose `interface` resolves to nothing is counted as unresolved |
| `ReadPublicAddresses(ctx)` | every upstream interface's address history and its gateways. **The double-NAT limit**: it returns the address the firewall holds and only that. Behind an upstream NAT — an Internet box in router mode, or a carrier-grade NAT at the provider — the firewall does not see its public IPv4 address at all, and nothing opnview reads can supply it: no OPNsense endpoint reports it, and asking a third party would be an outbound call the project does not allow. A screen built on it says so rather than presenting the interface's address as public |
| `Pair(ctx, from, to, window)` | the pairing of the two records of one connection over a window: see *Two legs of one connection*. Its outcome is on `flow.pair_outcome` and `flow.paired_flow_id` |
| `ReadCurrentRates(ctx, now)` | the latest sampled rates within `CurrentSampleBound` -- twice the `poll_interval_measurement_seconds` row, read by the store -- per device, client, owner and direction. A device, client and owner sum the **totals of each local address** (`interface_endpoint`), because a local address's outbound figure exists only as that total; `OutMeasured` is false where a summed reading carried none, so an unmeasured outbound is never a 0. Inbound is placed per peer; a local address's outbound total goes to outbound when all its peers are outside, to inter_interface when all are inside, and otherwise to `OutboundUnsplit`, whole. Only local addresses that are inside count for a client, owner or direction, so one conversation read on two interfaces is not counted twice |
| `ReadClientCurrentRate(ctx, clientID, now)` | one client's current rate, or `RateNoCurrentSample` — never a 0 that means nobody looked |
| `ReadSampledBytes(ctx, from, to)` | the bytes seen in samples, per device, from the totals of each local address, or `NotSampled`. A second measure, distinct from logged bytes; neither is conversation volume |
| `ReadInterfaceThroughput(ctx, device, measure, from, to)` | throughput between consecutive counter samples; a counter that went down is a `Reset` with no rate |
| `IsFirewallAddress(ctx, address)` | whether an address is one the firewall holds, under any of `addr4`, `addr6`, `ipv4` and `ipv6` |
| `InterfaceNetworks(ctx)` | every interface network, detected or the operator's, removed or not, and whether it counts now under decision D1 — what the screen that edits them reads. Its write path is `AddInterfaceNetwork`, `RemoveInterfaceNetwork`, `ConfirmInterfaceNetwork` and `SetLinkLocalEvidence`, each validated as *`interface_network`* says |
| `WidgetRows(ctx, name, period, parameters, scan)` and `WidgetRow` | the widget API's reads (step 5B, `docs/widget-api.md`): one named statement, registered by the widget core or the widget directory that owns it -- only a `widget_*` statement -- run on the read-only connections with the parameters it names, each row handed to the caller. Every statement is planned by `sql/schema-checks.sh` at both seed sizes like every other, and none scans a growing table. The shapes they serve are the API's; their contracts are the statements' comments and `docs/widget-api.md` |
| `ReadPreparedStatementCount()` | how many statements the read connections have prepared: every read function's statement is prepared once and reused (B8d), which a test observes |
| `ReadNotRunning(ctx, from, to)` | the intervals `opnview` was not running that meet a window (Group D): every widget reads them as gaps of every provider it binds. Its statement is `read_not_running` of `downtime.statements.sql`, planned by `schema-checks` like every other; `ReadStatementsOutsideTheFiles` lists any statement the read connections ran that no statement file holds, and a test serving every widget type asserts the list is empty |

**Where the read functions run (decision 10 and B8d of `specs/SPEC-step-5b-widget-api.md`).**
Every read function above, and every widget statement, runs on a small pool of read-only
connections (`internal/store/readpool.go`): four connections opened with
`PRAGMA query_only`, after the schema is applied, beside the single read-write connection
every write and derivation uses. Under WAL a reader sees the last committed transaction and
neither side blocks the other, so a widget read no longer queues behind
`ReclassifyEverything` and a long read no longer delays collection. Each statement text is
prepared once on the pool and reused across calls; `database/sql` re-prepares it on each
pool connection the first time that connection runs it, which is what makes one cache
compatible with several connections. `Close` closes the pool first, so the writer's
checkpoint is the last connection to close and the `-wal` and `-shm` companions are removed.

**Three indexes the widget API's references need** (A12: no statement may scan a growing
table): `idx_client_hostname` on `client (hostname)` for a `client` reference by host name,
and `idx_geo_asn_country_code` and `idx_geo_asn_asn` on `geo_asn` for a `country` or an
`operator` reference. Each costs one index entry per row of a table that grows with the
machines and the addresses seen, not with the traffic.

### `setting` — configuration

**Identity:** `key`, the primary key.

**Retention is configuration, not a constant.** `retention_seconds` holds the
purge horizon in seconds, so it is as easy to set to a few hours as to months.
It defaults to `7776000` — 90 days — and `0` means unlimited, which purges
nothing. No retention duration appears as a literal in any query, in any index,
or in `internal/store/purge.sql`: every statement reads the horizon from this row through
a scalar subquery that yields `NULL` when the horizon is unlimited, so every
comparison is `NULL` and nothing is deleted.

`aggregate_mode` is `full` or `no_domains`.

**Each paged read is governed by a pair of rows.** *(Rewritten after the loss of
2 October 2026.)* `poll_interval_<kind>_seconds` overrides how often a kind is
polled, and `page_size_firewall_log`, `page_size_security_event` and
`page_size_dhcp_lease` override how many records one request asks for. An
interval alone does not size a paged read: a poll every ten seconds asking for
500 records keeps up with a source producing fewer than fifty a second and
drops the rest. A missing row means the default `internal/config` documents
beside each key. The per-pair sampler and the resolver take no page size, the
first because its endpoint is not paged and the second because the firewall
fixes the size of its answer. An interval is a positive whole number of seconds
no larger than a Go duration holds; a page size is a positive whole number, and
one above what the firewall serves is stored and reported as such on the
collection surface rather than refused. The collection surface writes both rows
of a pair together, and the running service reads them again without a restart.

**`maxmind_account_id` is the MaxMind account ID**, which MaxMind requires beside
the licence key to download a database. It is a setting row rather than an
`encrypted_credential` because it grants nothing without the key, like the
firewall URL beside the API secret; it is a positive whole number.
`refresh_interval_geoip_seconds` (default 86 400) is how often the databases are
checked for a newer build, and `poll_interval_geo_lookup_seconds` (default 300)
how often stored addresses are looked up in them.

**`attribution_max_delay_seconds` is the attribution window** (default 5): how
long before a flow a lookup by the same client may be and still name it. It is a
positive whole number, validated by `internal/config`, and it governs every
attribution pass made after it is set: attributions already stored stay as they
were computed. The rule it governs is under `domain_attribution`.
**`attribution_max_cache_answer_delay_seconds` caps the exact window** (default
3600, decision 2 of `specs/SPEC-resolver-cache-attribution.md`): how long before a
flow, at most, a lookup may have been made and still name it through the
resolver's cache. The evidence's own coverage is the window, and this setting only
bounds it. It is a positive whole number, validated by `internal/config`, and the
schema writes its default row. **`poll_interval_resolver_cache_seconds`** (default
60) and **`poll_interval_resolver_local_data_seconds`** (default 300) are how often
the resolver's cache and its local data are read (decision 4); like every interval
they are positive whole numbers of seconds validated by `internal/config`.
`refresh_interval_public_suffix_list_seconds` (default 86 400) is how often the
Public Suffix List is checked for a newer copy.

**`leg_pairing_window_seconds` is the pairing window** (default 1, decision 4 of the
step-5A live corrections): how far apart, in seconds, the two records of one
connection may have been logged and still be paired. It is a whole number of
seconds, 0 or more and **at most 60** (decision 13 of
`specs/SPEC-step-5b-widget-api.md`), validated by `internal/config`, which refuses a
larger value at load with a message naming the setting; the schema writes its default
row. The bound exists because the two records of one connection are one packet logged
on two interfaces within the same instant, and a window of minutes would pair records
of different connections that merely share their ends and port, dropping a real
connection from every total. **`lease_backend_failure_pass_limit` is how long a failing DHCP backend holds the
host-name retry back** (default 3, defect L1 of
`specs/SPEC-test-budget-and-two-live-defects.md`): how many lease passes in a row one
backend may fail before a pass that read every other backend counts as complete, so the
unresolved host-name lookups are examined without it (`dns_resolution`, *The retry,
and the restart*). Three passes are a quarter of an hour at the default lease interval
of five minutes: longer than the one or two failed passes a restart of the backend's
service or of the firewall costs, and short enough that a backend that is gone for good
holds nothing back for long. It is a whole number of passes, 1 or more, validated by
`internal/config`; the schema writes its default row.
**`heartbeat_interval_seconds`** (default 60) **and `downtime_threshold_seconds`**
(default 300) govern `opnview_downtime`: how often the running service records
that it is alive, and how long it must have been gone for the interval to be
recorded. After a crash the last beat is the only bound held, so a minute keeps the
recorded start within a minute of the real one for one single-row write a minute;
five minutes is five beats, so a delayed beat never reads as downtime, and is the
default lease and local-data interval, so a restart for an update records nothing
while an absence long enough to leave a source unread for a whole poll is recorded.
Both are positive whole numbers of seconds, the threshold longer than the interval,
validated by `internal/config`; the schema writes their default rows.
**`refresh_interval_discovery_seconds`** is read by the store as well as by the
scheduler: it is the margin "held at an instant" allows either side of a sighting of
this firewall's address, so the two cannot disagree.

**`source_selection_<kind>_<provider_key>` is the operator's selection** of one
registry row — `auto`, `on` or `off`, exactly as written — described under
`provider` above. No row is `auto`.

**Where each setting is set.** Every one is validated by `internal/config`'s `Load`
itself before it is stored, and reaches the running service without a restart; none
is retroactive.

| Setting | Surface | Card |
|---|---|---|
| `poll_interval_<kind>_seconds`, `page_size_<kind>`, `source_selection_<kind>_<provider_key>` of the four sized kinds | collection (`/collection`) | the kind's own card |
| `poll_interval_resolver_cache_seconds`, `poll_interval_resolver_local_data_seconds`, and the selections of those two kinds | collection | the kind's own card, after the resolver lookups' card; drawn only when the `provider` table holds the kind |
| `attribution_max_delay_seconds`, `attribution_max_cache_answer_delay_seconds` | collection | *Attribution*, after the resolver cards |
| `leg_pairing_window_seconds` | collection | *Connection legs*, after the filter log's card |
| `lease_backend_failure_pass_limit` | collection | *DHCP backends*, after the DHCP leases' card |
| `retention_seconds`, `aggregate_mode` | collection | *Storage*, first on the page; a shorter retention horizon is stored only once confirmed, for the horizon the confirmation named |
| `refresh_interval_public_suffix_list_seconds` | settings (`/settings`) | *Public Suffix List*, beside *Geolocation* |
| the firewall URL, the API key and secret, the certificate fingerprint, the MaxMind account ID and licence key, the theme | settings | *OPNsense*, *Geolocation*, *Appearance* |

The intervals of discovery, the purge, the sampled measurements and the MaxMind
refresh and lookup have no surface yet; their rows are read as described above.

**The firewall URL, the OPNsense API key and the theme are rows here.** They are
configuration, `internal/config` already looks here for configuration, and a
second location would be a second truth. The two ciphertexts are **not** rows
here, for the reason `encrypted_credential` gives: `value` is a single
`TEXT NOT NULL` and a ciphertext needs four columns beside it.

**Retention:** never purged. **Indexes:** the primary key.

### `account` — one opnview login

**Source: none.** The term is `opnview`'s own and is recorded as such in
*Vocabulary*: OPNsense has users and API keys, and neither names a login into a
different product. Nothing in this table is read from an endpoint, and nothing
in it authenticates anybody to the firewall.

**Identity:** `id`; `login` is unique.

**The algorithm parameters are columns, and that is the whole design.**
`ROADMAP.md`, *Rules that apply to every step*, asks for Argon2id, a per-user
salt, and the parameters stored beside the hash so they can be raised later. So
`password_algorithm`, `password_memory_kib`, `password_iterations` and
`password_parallelism` sit beside `password_salt` and `password_digest`, and
verification reads them **from the record**. Raising the product's defaults
therefore changes what a *new* record is written with and leaves every record
already written verifiable. That is parameter agility, and it is a column layout
rather than a registry: one algorithm is mandated, so there is no kind with two
implementations here and nothing to select at runtime.

`password_algorithm` is not constrained to a value list, for the same reason no
provider name is: an implementation label is data, and a `CHECK` enumerating
today's one label would have to be edited the day there are two.

**The password is never stored and never read back.** `password_digest` is the
Argon2id output over the password, the salt and the parameters. No read path in
`opnview` returns a digest, a salt or a parameter set.

**A forgotten password is recoverable by possession of the key file**, and by
nothing else. Whoever holds that file already holds the decryptable secrets, so
requiring it grants an attacker nothing they did not have, and it returns the
installation without deleting the database.

**Several rows are permitted; one exists.** Nothing in `ROADMAP.md`, in this
document or in the survey mentions a second login, a role, a permission or an
invitation, so there are none — and no constraint restricts the table to one
row, because such a constraint would be honest today and a migration tomorrow,
and there are no migrations.

**Retention:** never purged. **Indexes:** the primary key and the unique
`login`.

### `session` — one signed-in browser

**Source: none.** `opnview`'s own term, recorded in *Vocabulary*.

**Identity:** `token_digest`, the primary key — **a digest of the session token,
not the token.** The token exists in the cookie and nowhere else, so a database
that is copied, backed up or sent by mistake — the case the key file's own
limitation is written against — hands over no usable session. A lookup hashes
what the cookie carried and compares digests.

**Two expiries, both enforced.** `expires_at` is the **absolute cap**, written
once when the session is issued and never moved. The **sliding idle window** is
`last_seen_at` plus a duration `internal/auth` carries, evaluated on every
request; it is deliberately not a column, because a stored copy of it would be a
second truth that a change to the window could not reach. An expired session is
refused and never renewed, on either bound.

`csrf_token` is the session's own, so a submission carrying another session's
token fails the comparison rather than being accepted.

**Retention:** not purged by `retention_seconds`, which is the observation
horizon and says nothing about a sign-in. Rows are deleted when the session is
signed out, when it is found expired, and when the account is removed — the
foreign key cascades.

**Indexes:** the primary key, `idx_session_account` for the cascade and for
signing every session of one account out, and `idx_session_expires_at` so
collecting expired rows is an index range rather than a scan.

### `encrypted_credential` — one credential opnview has to send to somebody else

**Source: the user, through the settings surface, and nowhere else.** There is
no environment variable, no configuration file and no `configure` subcommand.

**Identity:** `name`, the primary key. Two names exist: the OPNsense API secret
and the MaxMind licence key — the exact two `ROADMAP.md` names.

**Why encryption and not hashing.** `ROADMAP.md`, *Rules that apply to every
step*: these are not passwords. They are sent to the firewall and to MaxMind on
every call, so hashing them would make the product unable to work. They are
encrypted at rest with authenticated encryption and decrypted to be used.

**Why this is a table and not a `setting` row.** `setting.value` is a single
`TEXT NOT NULL`, and a ciphertext is not a value: it needs the `nonce` it was
sealed with, the `algorithm` label of the scheme that sealed it, and the
`key_id` of the key that did it — or it cannot be opened, and **a key that has
been replaced cannot be told apart from a ciphertext that has been tampered
with.** Those are four columns.

**The OPNsense API key is not here.** It is the Basic-auth username half of the
pair, `ROADMAP.md` does not list it among the secrets, and putting it here would
make a third ciphertext where the settled design names two. It is a `setting`
row.

**The key is a file beside the database**, created on first start with
permissions denying group and other, and never a row in the database. The limit
of that protection is stated in `README.md`'s limitations section in
`ROADMAP.md`'s own words, and it is **not** restated on any screen.

**A lost or replaced key file is a named state.** With the database intact and
the key file gone or different, opening a ciphertext fails, and that failure is
reported as its own state — *the stored credentials cannot be decrypted* — which
is not the same state as *no firewall is configured*. The two are
distinguishable, and re-entry is offered.

**Retention:** never purged. Overwriting a credential replaces the row, so no
prior ciphertext survives beside the current one. **Indexes:** the primary key.

### `collection_gap` — an interval opnview knows it did not cover

**Source: none.** This is the second entity in the schema fed by no endpoint,
and the reason is the finding the whole product rests on: every source returns
what it has and says nothing about what it did not return (survey, gap 11). The
term is `opnview`'s own, taken from the question the row answers — what is
missing from this history — and recorded as such in *Vocabulary*.

**Why it is a row rather than a log line.** A gap is read by a screen. A byte
total over a window that contains one is not a lower bound for the usual
physical reason; it is a lower bound because `opnview` was not looking, and
those are two different sentences to put next to a figure.

**The reasons are measured failure modes, not suppositions.**

| `reason` | What was measured |
|---|---|
| `digest_outside_returned_window` | the filter log's `digest` is a de-duplication key and **not** a server-side cursor: passing it returned byte-identical output twice on a live firewall. So a poll whose oldest returned line is newer than the newest line stored has missed everything between them, and nothing can recover it |
| `eve_rotation_lost` | rotation discarded the `eve.json` file a watermark pointed at. Detectable because `get_alert_logs` no longer lists it; permanent, and recorded as `rotation_state = 'lost'` as well |
| `resolver_window_not_honoured` | `search_queries` ignores `timeStart` and `timeEnd` outright — a 5-minute and a 24-hour request returned the same span — so it is a ring buffer of the most recent lookups and a pass whose oldest row is newer than the newest stored one lost what was between them |
| `push_buffer_overflow` | a push source's message dropped because `opnview`'s own queue was full (`docs/plugins.md`, *Push sources*): the interval spans the reception instants of what was lost |
| `push_store_failed` | a batch of pushed records that could not be committed after its retries |
| `push_listener_down` | the interval during which a push listener was not listening, `opnview`'s own downtime included (`push_listener`) |
| `push_script_failed` | a push entry's parse function that threw or ran past its bound, over its messages' reception instants |
| `push_not_alive` | a push entry that received nothing for longer than its liveness span; re-recorded, and extended, while the silence lasts |
| `netflow_sequence_gap` | a jump in a NetFlow exporter's sequence numbers, confirmed by the next packet, between the export times on either side |
| `netflow_template_missing` | NetFlow data whose template never arrived within the bound |
| `download_failed` | a Public Suffix List download failed or returned something that is not a list (step 5A). The interval runs from the last successful refresh to the failure, the list held since then stays in use, and `detail` names the failure |

The vocabulary is closed, and it may be because these are `opnview`'s own
detections rather than values any endpoint reports: enumerating them invents
nothing. `detail` carries the free text, and the collector writes into it what
the reader should do about it — for the filter log, that the page size has to be
sized for this installation.

**Retention:** purged by `detected_at`. A gap describing a window whose
observations would themselves have gone says nothing a screen can use, and
keeping it would make the oldest edge of the history read as permanently broken.

**Indexes:** the primary key, `idx_collection_gap_detected_at` for the purge,
and `idx_collection_gap_interval` so "was this window covered" is an index
search per provider.

### `opnview_downtime` — an interval during which opnview was not running

**Source: none**, like `collection_gap`, and for a stronger reason: while
`opnview` is stopped every source goes unread at once, the firewall keeps
serving, and the next poll returns what the firewall still holds and is silent
about what rotated out meanwhile (`SPEC-step-5b-widget-api.md`, D2). The terms are
`opnview`'s own; see *Vocabulary*.

**How it is recorded.** `opnview_heartbeat` is one row: the last instant the
service was known to run, and whether it stopped cleanly at that instant. The
service writes it on start, every `heartbeat_interval_seconds` while it runs, and
once more at a clean stop with `clean_stop` set. On start, before anything
collects, it compares that row with the clock: a gap longer than
`downtime_threshold_seconds` is written here as **one row**, from the last beat to
the restart, with `clean_stop` saying whether the start of the interval is an exact
stop instant or the last periodic beat of a process that ended without one — a
crash, a kill, a power loss — after which the service may have run for up to one
more heartbeat interval. A restart within the threshold writes nothing; a first
start ever writes nothing, because no instant before it is known. A beat that
finds its predecessor older than the threshold — a suspended host, a stopped and
continued process — records that interval too, since every source experienced it
as a stop and a start.

**Its meaning.** Every provider went unread over the interval, so a widget reads
it as a gap of every provider it binds, beside that provider's own
`collection_gap` rows; `store.ReadNotRunning` returns the intervals overlapping a
window. A figure over a window containing one is a lower bound because `opnview`
was not running, which is a third sentence beside the observation-point limit and
a source's own gap.

**Why a table of its own, and not a `collection_gap` reason.** `collection_gap` is
per provider: `provider_id` is `NOT NULL` and is the foreign key every "was this
provider's window covered" search rests on. Written there, one interval would be
one row per provider, or a `NULL` provider every reader of that table would have
to learn to skip. Here it is one row per interval.

**Retention:** purged by `detected_at`, as `collection_gap` is. **Indexes:** the
primary key, `idx_opnview_downtime_detected_at` for the purge, and
`idx_opnview_downtime_interval` for the overlap search. `opnview_heartbeat` is
bounded at one row by a `CHECK (id = 1)`.

### `push_listener` — when each push listener was last heard alive

One row per protocol and transport (`syslog`/`udp`, `syslog`/`tcp`, `netflow`/`udp`): the instant
the listener last recorded that it was alive, every 30 seconds while it listens and when it stops.
When it opens again, the interval since that instant -- `opnview`'s own downtime between runs included
-- is a `push_listener_down` gap of every push entry it feeds. The row holds no address and no port,
which are the installation's start parameters (`docs/plugins.md`, *Push sources*).

**Retention:** never purged; a handful of rows. **Indexes:** the primary key.

### `provider_source_period` — which path delivered a provider's records at each instant

A provider of a core kind may deliver its records by polling **and** through a push entry that
replaces the polled endpoint (PS9 of the push amendment to `specs/SPEC-backend-completion-plugins.md`).
The two paths' identities cannot meet -- a pushed record's `flow.log_digest` begins `push:` -- so what
keeps a record both delivered from counting twice is that every instant belongs to one path: a
period is `(provider_id, path, from_at, to_at)`, `path` `pull` or `push`, `to_at` NULL for the open one;
periods never overlap, each boundary closes one at a second and opens the next one second later; a
record is stored only when its instant lies in a period of the path that delivered it, and is
otherwise **superseded** -- counted, not stored. `internal/collect` (`pushpaths.go`) decides the
boundaries: from polling to push once both paths delivered an overlap, at the last second polling read
whole; from push to polling at the last second the push path delivered. A gap polling detects is clipped
by the push period before it.

**Retention:** a growing table -- one row per handover, and a pull period opened at every start of
`opnview` whose last period was a push one (`loadPushPath`) -- purged at the flows' horizon
(`purge.sql`): a closed period that ended before the horizon is deleted, the open one is kept however
old, and one straddling the horizon is kept whole. An instant older than every remaining period reads
as polled, which only a record older than the horizon could meet, and such a record is refused before
it is stored. **Indexes:** the primary key, and a unique partial index holding at most one open period
per provider.

### NetFlow volumes — `flow_record`, the capture configuration, the join and the measured volume

The NetFlow join amendment to `specs/SPEC-backend-completion-plugins.md` (PS17–PS23, decision 15b): the
flow records an exporter sends are stored as they came, and **when the capture configuration in force
names exactly one captured interface**, each record is joined to the filter-log leg logged on that
interface, and every logged connection gets its **measured volume** beside its logged one. With any
other configuration -- none, two interfaces or more, unreadable -- the records are stored and nothing
is joined. **A flow record is not a `flow`**: a `flow` is one filter-log record, a `flow_record` one
record an exporter sent (*Vocabulary*).

**`capture_configuration`, `capture_interface`** -- the configurations a `flow_record` provider read,
as runs of reads: a read equal to the newest run's extends it (`last_read_at`), any other opens a run.
A configuration is in force over its run; **between two runs that differ, before the first, and over
an unreadable run (`fingerprint` NULL, `outcome` the broker's) it is `capture_configuration_unknown`**;
after the newest read the newest configuration is taken as in force until a read says otherwise. The
captured interfaces are those with a captured direction, each `ingress` and `egress`.

**`flow_record_condition`** -- a condition the decoder detected on the exporter's stream:
`netflow_sequence_interleaved`, sequence numbers going backwards, which shows several counters behind
one observation domain. Under a one-interface configuration the stream contradicts the configuration:
the undecided records starting from `observed_at` lose their Observation Point, and the join is
suspended until the next read (`resolved_at`), which also has the receiver follow the sequence afresh.
It never says which interface a record came from.

**`flow_record`** -- one record as its exporter sent it, a hypertable on `start_at` (*Storage fit*):
the exporter and observation domain, the raw `ingress_interface` and `egress_interface`, version, IANA
protocol, addresses and ports, cumulative `tcp_flags`, `octets`, `packets`, start and end at the
precision exported, reception. Its `identity` is the core's, from the delivery: a datagram delivered
twice is stored once, two identical records of one datagram are two rows. At reception the core writes
the state of the configuration in force at the start (`capture_state`), the captured-interface count,
the **Observation Point** -- the one captured interface, mapped to its kernel index through
`interfaces_info` -- and the **direction** at it (egress for an egress-only interface; ingress when
`ingress_interface` is the point's index; egress otherwise, a firewall-originated packet's index being
0, survey S6a); `loss_detectable` is 1 where the configuration made the exporter's sequence one
counter's. A record is never given a point later, and never loses one, but by the contradiction above.

**The join** (`internal/collect`, `flowrecord.go`), decided from stored rows only, a group -- one
Observation Point and one unordered tuple -- at a time, oldest record first, so the order of arrival
changes nothing and a second derivation writes nothing:

1. A record is decided only once the filter log has been read past its start plus
   `netflow_join_tolerance_seconds` (3 by default: the filter log's, NetFlow's and the normaliser's
   one-second resolutions); until then it is `pending`.
2. Its candidates are the legs logged on the point's device, of its version and protocol (by IANA
   number; the filter log's name is read through IANA's keywords, survey S4a), whose addresses -- as
   values -- and ports are the record's (**forward**) or swapped (**reverse**); ICMP, ICMPv6 and a
   record with ports 0 compare the addresses alone. The record's direction, its orientation and the
   leg's pf `dir` must agree: ingress forward or egress reverse with an `in` leg, egress forward or
   ingress reverse with an `out` leg.
3. The leg logged latest at or before the start plus the tolerance wins; two at that instant are
   `ambiguous`. A TCP record carrying SYN joins only a leg logged within the tolerance of its start.
4. The idle time from the leg, or from its last joined record of that orientation, to the start never
   exceeds pf's default state timeout (S12): TCP established 86 400 s, 900 s after a FIN, 90 s after a
   RST, 120 s before any acknowledgement; UDP 60 s; ICMP 20 s; any other 60 s.
5. Every action joins: a refused `in` leg's forward packets were captured before pf.
6. A record joining nothing is **unmatched**: `no_logged_leg`, `ambiguous`, or `beyond_flow_horizon`
   when it started before `retention_purge.purged_before`, or its leg can have been purged.

**`connection_measured_volume`** -- per connection (named by its first or single leg) and direction
(`forward`, `reverse`, as the first leg logged them): the bytes, packets and records joined to **the leg
logged on the Observation Point** -- the first leg when the point carries it, else the second
(`measured_at_leg`) -- with the point it was measured at. A row exists only for a connection that
started under one Observation Point. The state is `measured` (its continuity ended), `in_progress`,
`awaiting_export` (no record yet, within the active timeout plus the tolerance), `missing` (past it),
`not_captured` (no leg on the point, or the point does not capture that direction), `export_gap` (a
gap of the provider meets its span) or `not_exported_when_refused` (a refused `out` leg's forward
direction: pf refused it before the egress capture). A connection under any other configuration is in
no row: its state is the interval's. `final` is 1 once nothing can change the row; until then `due_at` is
the read-through instant at which its state may change by time alone, and the derivation computes an
open row again when it falls due, or when a record, a leg or the pairing it reads changes.

**`connection_netflow`** -- the one place a logged flow's `netflow` figures are read from
`connection_measured_volume`: a SQL function of `schema.sql` taking the flow's id, pair outcome,
paired flow and action, and answering its `bytes` (its connection's, both directions, on a first or
single leg; on a second leg only the directions measured at that leg) and its `parts`. The aggregate
refresh, the purge and every `_netflow` twin join it (`LEFT JOIN LATERAL connection_netflow(...) AS n ON
true`), and none reads the table itself; PostgreSQL inlines it, so the plan is the one the statement
would have spelled out. `TestNoStatementCopiesThePerLegCounting` fails on a copy.

**`netflow_parts`** -- a composite type of thirty-six `BIGINT` fields: for each of the `allowed`,
`blocked` and `unknown` parts, `<part>_packets`; the four coverage classes, which sum to the part's
connections -- `<part>_measured_connections` (both directions `measured`),
`<part>_partial_connections` (some measured bytes, not both directions measured),
`<part>_unmeasured_connections` (one Observation Point, no measured byte) and
`<part>_not_joinable_connections` (no single Observation Point at the connection's start); and, per
direction state above, `<part>_direction_<state>_connections`, the connections with at least one
direction in that state (a connection whose two directions are in two states counts under both).
`netflow_sum` adds values field by field; `(netflow_sum(x)).*` reads one back as its thirty-six named
columns. A connection is **covered** when it is joinable and none of its directions is
`not_captured`, `export_gap` or `not_exported_when_refused` (PS22); the widget measure judges it from
these counts.

**In the aggregate families.** Every family and period, and `purged_flow_hour`, carries `netflow_bytes`
split allowed / blocked / unknown by the counting leg's action, and `netflow_parts` -- **beside** the
logged columns, which are unchanged; no statement sums the two measures. A connection's figure counts
in its first leg's slot and rows, in the part of that row's action; measured at the second leg it also
counts under that leg's `exit_leg_device` and in the rule family, as the second leg's logged bytes do.
A changed figure forces its slot. The purge keeps a purged flow's share in the purged part.

**`unmatched_record_aggregate_*`** -- the unmatched records, per slot of their start, Observation
Point, direction, protocol, version and reason: bytes, packets, records. Accumulated by the join in the
transaction of its decision, so a slot keeps them after the records are purged; in no other family and
no total.

**Retention:** `flow_record` at `retention_flow_record_seconds` (0 follows `retention_seconds`), by
chunk then row; the conditions and every configuration run but the newest with it; the measured
volume with its connection; the unmatched family's slots at the flows' horizon. **Indexes:** the
pending records by provider, the records of a group, the records joined to a leg; on `flow`,
`idx_flow_join` (device, the two canonical addresses, `observed_at`) for the candidates.

### `measurement_sample` — one numeric reading of one subject at one instant

**Sources:** `/api/diagnostics/traffic/top/<interface names>` for the per-pair
volume, and `/api/diagnostics/system/systemResources`,
`/api/diagnostics/system/systemTemperature`,
`/api/diagnostics/system/systemTime`, `/api/diagnostics/system/systemDisk`,
`/api/diagnostics/cpu_usage/stream` and
`/api/diagnostics/traffic/interface` for the firewall's own gauges. Survey,
*Verified against a live firewall, 2026-09-27*.

**Fine and baseline samples (C3 of the backend-completion cycle).** A baseline sample
is taken by the `sample` pass at `poll_interval_measurement_seconds`, whether anyone
looks or not, and follows `retention_seconds`. A **fine sample** is taken by a
provider's live pass (`docs/plugins.md`, *Live pass*) at its live cadence, only while
the live-interest lease holds, and carries that cadence in `live_cadence_seconds`;
the purge removes it after `live_sample_retention_seconds` (an hour by default), to
the exact second. Both are written at the instant the firewall's answer was fetched.
When the baseline pass falls due during live mode, the broker serves it the live
pass's answer from the mirror, so it writes the same readings at the same instant,
and the conflict **promotes** the fine row to a baseline one (`live_cadence_seconds`
set to null) instead of storing it twice: one round of calls serves both, the
baseline series has no hole, and no reading is counted twice. Nothing summarises the
fine samples before they are purged (the maintainer's answer (a) to open question 1).
A current rate is read from the newest reading of either kind and judged stale past
twice its own cadence (`docs/widget-api.md`, *Live figures*).

**The processor, corrected by the resolver-cache cycle.** The processor figure was
read, until then, from `/api/diagnostics/activity/getActivity` under invented keys
(`cpu`, `cpu_usage`, `used`, `total`) that no field of that endpoint carries — its
body is top's header lines as text and the process list — so it never produced a
value. `opnview` no longer registers `getActivity`
(`specs/SPEC-resolver-cache-closing.md`, scope 2); the survey keeps what it answers,
as research. The figure is now read from **`/api/diagnostics/cpu_usage/stream`** (survey,
*Processor stream, read from source for the resolver-cache cycle*): a server-sent
event stream of `iostat -w 1 cpu`, one event a second, each
`{"total", "user", "nice", "sys", "intr", "idle"}` in whole percentages, `total`
being their sum less `idle`. The stream never ends, so it is read with a bound — two
events within five seconds, then closed — and **the derivation is
`total / (total + idle)` of the second event**, stored as `cpu_use_ratio`: the first
event is iostat's average since boot (iostat(8)), not a current reading, and the
ratio is taken against `total + idle` rather than 100 because iostat rounds each
column on its own. Each absence is named on its own in the measurement provider's
availability detail and writes no reading: denied, naming the privilege "Lobby:
Dashboard"; not found; no event within the bound; only the since-boot event; an
event that cannot be read.

**IT IS A PROVIDER KIND, and it is the kind most of the ecosystem fits.** Of the
~34 data-producing sources surveyed in the plugin ecosystem, **eight of the ten
that fit a shape this model already had fit this one**: per-peer transfer
counters, frontend and backend counters, per-interface volume, and the UPS, SMART
and sensor readings of the monitoring plugins (survey, *What does produce data,
and in what shape*). It was a table with **no kind** — no `provider_key`, no
availability row, no place in the `provider.kind` CHECK — so none of them had
anywhere to announce itself, and the connector protocol could not have been
designed around it. It has all three now.

**It exists because two different needs turned out to have one shape.** The
telemetry the earlier passes recorded as gaps **G9** and **G10** is answerable:
those six endpoints exist, so the gap was never in the API and was always in
the schema, which had nowhere to put a gauge. Separately, `traffic/top` is a
**live rate snapshot** and no endpoint exposes a flow aggregate over a past
window — `netflow/aggregate` answered 404 — so per-pair volume has to be
sampled too. A gauge and a sampled pair volume are the same five facts: a
subject, a measure, a unit, a value and an instant. One table carries both.

**This is not an entity-attribute-value table**, and the distinction matters
because this document forbids one. An EAV table stores attributes of
heterogeneous entities as untyped name/value pairs, losing every type and every
constraint. This stores **one** kind of thing — a numeric reading over time —
with a typed `REAL` value, a mandatory unit, a closed subject vocabulary and a
closed measure vocabulary, and the screens that read it filter on
`(subject_kind, subject_key, measure)` over a range of `sampled_at`, which is
exactly what its index serves. It is the shape every time-series store uses,
including the aggregates OPNsense itself keeps under `/var/netflow`.

**The subject.** `subject_kind` is `firewall`, `interface`,
`interface_endpoint_pair`, `interface_endpoint` or `gateway`. An
`interface_endpoint` is one local address on one device, over all its peers,
keyed `device local`. For an interface the key is the network
**device** name, the token `interface_map` keys by, so a reading joins to an
interface without a foreign key a discovery refresh could break. For a pair it is
the device the reading was taken on, the local address and the peer address,
joined by spaces in that order: `traffic/top` reports each local address with its
peers on one interface, and a peer's figures are **inbound** to the local address
(survey, *What `traffic/top` measures, read from source for step 5*), so the order
carries the direction and is never canonicalised. The step-4 form, the two
addresses in lexicographic order, discarded exactly that direction and is
replaced. For a gateway it is the gateway's name as *System > Gateways* reports
it. For the firewall it names the part measured when there is one, a temperature
sensor or a mounted filesystem, and is empty for a reading of the whole machine.

**`subject_kind`, `measure` and `unit` are `opnview`'s own vocabularies, and they
are EXTENSIBLE BY A PROVIDER without a schema change.** They are `opnview`'s own
because the survey establishes those six endpoints and **not their field names**,
so a measure named after a response key nobody has read would be an assertion
about a shape nobody has seen. Which response key each reading is taken from is a
candidate list marked `UNVERIFIED:` in `internal/collect`, and a reading none of
the candidates matches is recorded as **absent** rather than written as a zero —
the difference between "the processor is idle" and "this endpoint did not tell
us".

**What step 5A read from the source**, and samples on that ground (survey,
*Telemetry field names, read from source for step 5*, *What `traffic/top`
measures, read from source for step 5*, *Gateway status, read from source
for step 5* and *Swap, read from source for step 5*):

- **Interface errors.** `traffic/interface` reports `input errors` and
  `output errors` per interface, as text; they are sampled as `errors_in` and
  `errors_out`, in packets, and a reading that does not answer is absent.
- **Per-pair readings.** A `traffic/top` sample covers about **two seconds** —
  the endpoint runs `iftop` with `-s 2` — so its `rate_bits` are a rate over
  that sample and its `cumulative_bytes` are bytes seen **in** it, not since the
  conversation began. Each `details[]` entry is one peer of the record's local
  address, and its figures are **inbound** to that address; they are sampled as
  `rate_bits_in` and `cumulative_bytes_in`. The record's own `rate_bits_out` and
  `cumulative_bytes_out` are its total over all its peers, so they become one
  pair's outbound figures **only when the record has a single peer**. The
  record's four totals are therefore ALSO sampled, under
  `subject_kind = 'interface_endpoint'` keyed `device local`: they are the only
  outbound figure a local address talking to several peers has, and a client's,
  owner's and device's current rate and the sampled bytes are read from them, so
  a client with several peers is never given an outbound of 0. Sampled bytes are
  a second measure, distinct from logged bytes, and neither is conversation
  volume; they are carried with the seconds sampled as their coverage, never
  extrapolated, and a window holding no sample is **not sampled**, never 0 bytes.
- **Gateway latency and loss.** `/api/routes/gateway/status` reports each
  gateway's `delay` and `stddev` as text in milliseconds (`1.2 ms`) and its
  `loss` as a percentage (`0.0 %`), with `~` where dpinger has no figure. They
  are sampled as `delay_milliseconds`, `delay_stddev_milliseconds` and
  `loss_ratio`; a `~` is no sample, and an endpoint that does not answer is
  recorded in the provider's availability rather than as a zero.
- **Swap.** `systemResources` carries no swap figure. Swap is read from
  `/api/diagnostics/system/systemSwap`, the second firewall endpoint step 5
  adds (amendment of 4 October 2026): `swapinfo -k`'s `total` and `used` per
  device, strings in KiB, sampled in bytes as `swap_total_bytes` and
  `swap_used_bytes` under the firewall subject keyed by the device. A firewall
  with no swap device, or an endpoint that does not answer, is recorded in the
  availability detail, never as a zero.

They were three closed `CHECK`s, and that was correct for exactly one provider.
The eight sources this kind absorbs share no vocabulary at all: a UPS reports
volts, SMART reports reallocated sectors, and HAProxy's subject is a backend
rather than an interface or a client. A closed `CHECK` would have made every one of
them a schema change, which is the plugin-hostile design the promotion exists to
remove. What the columns still enforce is the **shape** of a term rather than its
membership of a list — non-empty, lower-case, no space — so a typo, a
free-text sentence, or two spellings of one measure are still rejected. The terms
`opnview`'s own sampler uses are the named constants in
`internal/store/rows.go`; a provider's term is an ordinary value beside them, and
a test adds one and reads it back while asserting that no DDL moved.

This is still **not** an entity-attribute-value table. The tier-1 rule of *The
attribute rule* is about a provider's arbitrary fields; a declared measure with a
unit and a typed `REAL` value is one kind of thing recorded over time, and the
screens filter on `(subject_kind, subject_key, measure)` over a range of
`sampled_at`, which is what the index serves.

**`provider_id` is nullable and is `NULL` on every firewall gauge.** The
firewall's own telemetry implements **no external contract** — it is the machine
reporting on itself — and attributing it to a source would say that source
measured the temperature. That is recorded here rather than papered over with a
foreign key that means something else. The per-pair readings **do** name the
`measurement_sample` provider, because that volume is its material.

**Identity:** `(subject_kind, subject_key, measure, sampled_at, provider)`.
Re-reading the same instant is a no-op, which is what a sampler restarting inside
one interval needs. The provider is part of it because this kind **admits several
concurrently active providers** — see *Providers* — so two of them reading one
subject at one instant are two readings and not a collision.

It is a unique **index** over `ifnull(provider_id, -1)` rather than a table
constraint, and that detail is load-bearing: SQLite treats nulls in a `UNIQUE`
constraint as distinct from one another, so a bare `provider_id` in the key would
have made **every firewall gauge non-idempotent** — the exact opposite of what
the identity is for. It is the same `ifnull` wrapper the volume aggregates
already use, for the same reason.

**Retention:** purged by `sampled_at`.

**Indexes:** `idx_measurement_sample_subject`, which is what makes "this
subject's readings of this measure over this period" an index search, and
`idx_measurement_sample_sampled_at` for the purge.

### `state_snapshot`, `state_item` — a complete set of things, as of one instant

**Source: none yet, and that is the point.** These two tables are the
destination of the `reconciled_state` kind, and no connector for it is written.
The shape is the survey's finding rather than a design instinct: **ten of the
eleven sources that fit no other kind produce one single shape** — the current
things of type X, each with attributes and sometimes a TTL, replaced wholesale on
each poll rather than appended to (survey, *Shapes the model has no room for*).

**Why it is not another append-only kind.** Every other kind here records events:
a row arrives, it is stored, it is never contradicted. These sources record the
opposite. The surveyed ban-list endpoint runs its own tool with no limit and no
`since`, so **every poll re-dumps the whole set**, and a decision carries a TTL
and no timestamp at all. Storing such a dump as events would either insert the
whole set again on every poll, or — with de-duplication on content — keep a thing
that has gone for ever.

**A DEPARTURE IS THE WHOLE POINT, AND IT IS WHAT THE INSTANT IS FOR.** Because a
snapshot asserts that the set was **complete** at `captured_at`, a key present in
one snapshot and absent from the next has **left**: the ban expired, the peer was
removed, the neighbour went away. The model can therefore say so, rather than
silently keeping the item or silently dropping it — which are the two failures an
append-only table and an overwrite-in-place table respectively produce.

`state_item_departure` is that statement, as a view: what was in the previous
complete snapshot of a set and is not in the latest one, with the instant it was
last present, the instant it was first absent, and the attributes it carried while
it was there. It is the **latest pair** and not every pair, deliberately: "what
has just left" is the question a screen asks, and a view over every consecutive
pair would grow with the history for no reader. A set observed once yields no
rows, which is the honest answer — nothing can be said to have left a set seen
once.

**What it deliberately does not carry.** No status, no category, no severity, no
enabled flag, no display name. This is the one part of the model designed from
research rather than from a working collector, and this project has twice been
caught building against a document, so it holds exactly what the examined sources
need: a set, an instant, an identity within the set, the attributes, and an
optional validity end. A field a screen turns out to want is added when a real
provider's shape has been surveyed, in the same way `security_event` will be
extended.

| Column | What it is |
|---|---|
| `state_snapshot.provider_id` | the provider that reported the set; mandatory, because an unattributed set could not be compared with anything |
| `state_snapshot.set_key` | **which** set, because one provider commonly reports several — one surveyed engine reports decisions and alerts, and a tunnel reports its peers. Without it, a poll of one would read as a departure of everything in the other |
| `state_snapshot.captured_at` | the instant at which the set was complete. The load-bearing column: without it a set is a bag of rows and a departure is undetectable |
| `state_item.item_key` | the identity within the set, supplied by the provider and stored verbatim — the banned address, the peer's public key, the neighbour's chassis id. `TEXT`, because a provider whose things are named rather than numbered has to fit |
| `state_item.attributes` | the provider's own fields, as a JSON object. Tier 2 of *The attribute rule*: displayed on a detail screen, never aggregated, never filtered on, never joined. `NULL` is a thing with no attributes, which is normal |
| `state_item.valid_until_at` | the end of the thing's own validity when the provider states one, which the surveyed ban decision does and nothing else does. It is **not** how a departure is detected: an item is gone when the next complete snapshot omits it, which is the only signal every one of these sources gives |

**Identity:** `(provider_id, set_key, captured_at)` for a snapshot, and
`(snapshot_id, item_key)` for a member. Re-reading one poll is a no-op, and one
poll cannot report one thing twice.

**A snapshot is written whole or not at all.** A half-written snapshot is not a
slightly incomplete row: because the snapshot asserts completeness, every member
missing from it reads as a departure. `store.InsertStateSnapshot` is therefore one
transaction, and a test proves that a set whose second member is rejected leaves
no snapshot behind.

**An empty set is a legitimate snapshot and is written as one.** A ban list that
is now empty has not stopped reporting; it has reported that every ban is gone.
Skipping the write would leave the previous set reading as current for ever.

**Retention:** purged by `captured_at`, and the members go with their snapshot
through `ON DELETE CASCADE`. **Indexes:** `idx_state_snapshot_captured_at` for the
purge, `idx_state_snapshot_set` so "the latest snapshots of this set" is an index
search, and the two uniqueness constraints.

### `interface` and `dhcp_lease` are the same shape, modelled twice

Both are reconciled state: the firewall reports the complete current set of
interfaces, and each DHCP server reports the complete current lease table of its
own scope, replaced wholesale on every poll. They are bespoke tables rather than rows of the
`reconciled_state` kind, and **they were deliberately left that way**.

The reason is not that the duplication is invisible — it is written here so that
it is not mistaken for an oversight — but that they work, they are verified by the
assertions in `sql/schema-checks.sh`, and the point of adding the kind was to let
**other** sources supply that shape rather than to re-plumb the two that already
do. Migrating them would trade a working, typed, indexed, screen-queried model for
a generic one, and it would do it in the cycle whose whole purpose is to make the
generic one exist. They also earn their typed columns under tier 1 of *The
attribute rule*: every screen filters and aggregates on an interface's
`link_kind`, on a lease's `lease_state`, on a client's identity cascade — none of
which a JSON attribute bag could serve.

If a later cycle does migrate them, the thing to keep is what the bespoke tables
have that the generic one does not: `interface.user_label` and
`client.owner_id` are **user input**, and a reconciled set replaced on every poll
must never be able to overwrite them.

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
| `reason` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.log_reason`, verbatim with no `CHECK`. A record logged for a reason that is not a rule match is not a rule denial and must not be rendered as one. Since step 5A the value set is established from the source (survey, *The `reason` field's values, read for step 5*), and the `blocked_decision` view matches on the established values only |
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
| `ttl` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the per-packet hop budget; it varies inside one conversation and sums to nothing. Not a pairing field either: no cited source says what a forwarded packet's logged `ttl` is on each leg (survey, *Two legs of one connection*, marked `UNVERIFIED:`) |
| `id` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.ip_id`, since the step-5A live corrections, which reversed the earlier *not stored*: the IPv4 identification is one of the two logged fields verified to cross the firewall unchanged under OPNsense's defaults — pf rewrites it only under scrub `random-id`, which OPNsense adds only when its IP Random id option is on, off by default (survey, *Two legs of one connection*) — so it is what tells two connections to one destination apart when their two legs are paired. Nothing else reads it |
| `offset` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 fragment offset |
| `ipflags` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv4 fragmentation flags |
| `class` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 traffic class, the twin of `tos` |
| `flow` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 flow label; it labels a conversation for the routers, and `opnview` identifies one by its endpoints. Not a pairing field: whether pf rewrites it is `UNVERIFIED:` (survey, *Two legs of one connection*) |
| `hoplimit` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the IPv6 twin of `ttl`, and not a pairing field for the same reason |
| `datalen` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — the payload length. `length` is the figure the volume columns sum, and holding both invites a screen summing the wrong one. Not a pairing field: that a leg logs the same length after scrub's fragment reassembly is `UNVERIFIED:` (survey, *Two legs of one connection*) |
| `tcpflags` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — judged and left out, recorded so the judgement is visible. No entry in `docs/widget-catalogue.md` reads them, and the one use that would justify the column — telling a scan from a conversation — needs flags aggregated across records the log only emits where a rule logs, so it would answer the question for the logging subset and stay silent elsewhere. A partial answer to a security question is worse than none |
| `seq` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **stored** — `flow.tcp_seq`, since the step-5A live corrections, which reversed the earlier *not stored*: the TCP sequence number is the second logged field verified to cross the firewall unchanged — pf rewrites it only under `modulate state` or `synproxy state`, and an OPNsense rule keeps state by default (survey, *Two legs of one connection*) — and the core parser names it at the position `filterlog` writes it (survey, *Two legs of one connection*, (b)). It pairs the two legs of one connection, and nothing else reads it |
| `ack` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP sequencing internals |
| `urp` | `/api/diagnostics/firewall/log` | Data source 1, Response shape | **not stored** — per-packet TCP urgent pointer. On 26.7.3 the parser files `filterlog`'s window under this name, one position early, so the key does not hold what it says (survey, *Two legs of one connection*, (b)) |
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
| `uuid` | `/api/unbound/overview/search_queries` | Data source 5, Response shape | **not stored** — *and it cannot be: it is null on every row a live firewall returns, measured 2026-09-27.* The identity the model needs is composed instead, into `dns_resolution.lookup_key`, from the row's own content; the composition and the under-count it accepts are recorded on that column and in its entity section. This row is **not** a `dropped` field: there is nothing there to store |
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

### The resolver's cache and local data, and the processor stream

`/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata` and
`/api/diagnostics/cpu_usage/stream`, survey *Resolver cache and local data, read
from source for the resolver-cache cycle* and *Processor stream, read from source
for the resolver-cache cycle*.

| API field | Endpoint | Survey section | Verdict |
|---|---|---|---|
| `status` | `/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — `source_availability.state` and `source_availability.detail`: anything but `ok` is the named absence "the resolver answered status …", and nothing else of the answer is stored |
| `host` | `/api/unbound/diagnostics/dumpcache` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — `resource_record_observation.owner_name`, lower-cased with its trailing dot removed |
| `name` | `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — `resource_record_observation.owner_name`, normalised as `host` is, and its first label in `resource_record_observation.host_label` for an A or AAAA record |
| `ttl` | `/api/unbound/diagnostics/dumpcache` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — as `resource_record_observation.covered_until_at`, the poll's instant plus the seconds the record has left; a record whose `ttl` is not a whole number of seconds is skipped and counted |
| `ttl` | `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **not stored** — the TTL the local data is configured with, not a time remaining; a local-data record is covered while consecutive polls see it |
| `type` | `/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **not stored** — the record's **class**, `IN` on every record either endpoint returns, and never read as the record's type |
| `rrtype` | `/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — `resource_record_observation.rrtype` for A, AAAA and CNAME (cache) and A, AAAA and PTR (local data); every other type is counted in `source_availability.detail` and not stored |
| `value` | `/api/unbound/diagnostics/dumpcache`, `/api/unbound/diagnostics/listlocaldata` | Resolver cache and local data, read from source for the resolver-cache cycle | **stored** — `resource_record_observation.value`, and `resource_record_observation.address` for an address record; a value not valid for its type is skipped and counted |
| `total`, `idle` | `/api/diagnostics/cpu_usage/stream` | Processor stream, read from source for the resolver-cache cycle | **stored** — `measurement_sample.value` under `cpu_use_ratio`, as `total / (total + idle)` of the second event |
| `user`, `nice`, `sys`, `intr` | `/api/diagnostics/cpu_usage/stream` | Processor stream, read from source for the resolver-cache cycle | **not stored** — their sum is `total`, which is; no screen splits the processor's use by mode |
| `headers`, `details` | `/api/diagnostics/activity/getActivity` | Processor stream, read from source for the resolver-cache cycle | **not stored** — top's header lines as text and the process list; neither carries a processor figure as a number, and `opnview` no longer registers the endpoint (`specs/SPEC-resolver-cache-closing.md`, scope 2); the row is kept as research |

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
| `addr4`, `addr6` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface_address.address` and `interface_address.prefix_length`, with `interface_address.source_field` holding the field name, since step 5A. A change is a new row, so the table is the address history no endpoint keeps |
| `ipv4[]`, `ipv6[]` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — each entry's `ipaddr` as `interface_address.address` and `interface_address.prefix_length`, under `interface_address.source_field` `ipv4` or `ipv6`, since the step-5A corrections; the networks they cover are proposed as `interface_network.network_address`, a link-local one excepted. That closes **G13** in `docs/widget-catalogue.md`. An entry's `vhid` and the CARP fields beside it are not stored: no widget shows CARP state |
| `gateways[]` | `/api/interfaces/overview/interfaces_info` | Runtime discovery (i) | **stored** — `interface_address.address` under `source_field` `gateways`, one row per gateway address, and `interface.is_upstream`, which is 1 exactly when the list is non-empty, since step 5A |
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
| `enabled` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.enabled`, since the step-5A live corrections, which reversed the earlier *not stored*: as coverage of the field, so a screen listing rules can say one is switched off. Nothing in the derivation reads it, and `rule` still names the `rid` of historical records whatever the rule's state now |
| `interface` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.interface`, verbatim, since step 5A: configuration keys on a model rule, a description on a legacy row, empty on a floating rule. Which rules bear on a pair of interfaces is still derived from the flows actually observed; this column answers which rules do not log, per interface |
| `protocol`, `ipprotocol`, `%protocol`, `%ipprotocol` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — a rule's protocol criterion. `flow.protocol` carries the protocol actually observed, and `opnview` never evaluates a criterion |
| `destination_net` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.destination_net`, verbatim, since step 5B, which reversed the earlier *not stored* (B8a of `specs/SPEC-step-5b-widget-api.md`, decision 12). The reason is the rule table: a reader looking at which rules fire is entitled to see what each rule was written against, and the field is the one criterion that says where a rule's traffic was going. It is **stored and never evaluated**: `opnview` matches no flow against it and derives nothing from it, for the reason below. A rule that does not report it stores `NULL`, not reported |
| `source_net`, `source_port`, `destination_port` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — the rule's match criteria, for the same reason: `opnview` joins an observed record to a rule identity and evaluates nothing |
| `categories` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — no entry in `docs/widget-catalogue.md` groups rules by category, and a category is a name, which nothing in this schema is allowed to classify by |
| `sort_order` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **not stored** — the ordinal position of the rule in the ruleset, which changes on any edit and means nothing to a record written before the edit |
| `legacy` | `/api/firewall/filter/search_rule` | Runtime discovery (ii) | **stored** — `rule.legacy`, since step 5A, because it says whether `rule.interface` holds keys or descriptions |
| `name` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **stored** — `measurement_sample.subject_key` of every gateway reading, under `subject_kind` `gateway` |
| `delay`, `stddev` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **stored** — `measurement_sample.value`, parsed from the `1.2 ms` text into milliseconds as `delay_milliseconds` and `delay_stddev_milliseconds`; a `~` is no reading |
| `loss` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **stored** — `measurement_sample.value`, parsed from the `0.0 %` text into a ratio as `loss_ratio`; a `~` is no reading |
| `address` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **not stored** — the gateway's address is already held from `interfaces_info` `gateways[]`, which ties it to its interface; this endpoint's copy would be a second truth |
| `status`, `status_translated` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **not stored** — dpinger's verdict on its own thresholds; the delay and loss it is computed from are stored, so a screen states the figures rather than a threshold nobody configured in `opnview` |
| `monitor` | `/api/routes/gateway/status` | Gateway status, read from source for step 5 | **not stored** — the address dpinger pings, configuration rather than a measurement |

<!-- api-field-coverage:end -->

**What the sweep found.** Beyond the two instances that prompted it —
`blocklist`, closed earlier as G1, and `dnssec_status` — it found three more
dropped fields with an obvious use, all closed in the same pass and all of the
same family: they are what separates *nothing happened* from *we could not
see*. `reason` on the filter log, `log` on a rule, and `status` and `enabled`
on an interface. It also confirmed four drops that were already recorded as
gaps rather than forgotten (`ids.general.interfaces` as G8; `addr4`, `addr6`,
`ipv4[]`, `ipv6[]` and `gateways[]` as G13, since closed), and it turned up one contradiction
that is not a column at all: the polling plan polls the Insight time series
every 300 s and no table holds it.

## Record rate

*Rewritten after the loss of 2 October 2026, from the compiled build of that
evening.*

The record rate is what the collection surface sizes polling from: for one kind,
how many records the kind's table received over a window, and how they were
spread. `store.MeasureRecordRate` computes it from the rows already stored; it
reads nothing else and writes nothing. The same query, for one bucket width, is
the *Record rate per kind* section of `sql/queries/diagnostics.sql`.

| Kind | Table | Source clock | Ingested clock |
|---|---|---|---|
| `firewall_log` | `flow` | `observed_at` | `ingested_at` |
| `security_event` | `security_event` | `occurred_at` | `ingested_at` |
| `dhcp_lease` | `dhcp_lease` | `observed_at` | none |
| `dns_lookup` | `dns_resolution` | `looked_up_at` | `ingested_at` |

**The window** is the last 24 hours on the source clock, or the retention horizon
if that is shorter, since the purge has removed whatever lies beyond it. Its start
is included and its end excluded, so consecutive windows tile.

**The figures** are the record count; the span the records cover, first to last;
the mean rate over that span on the source clock and, where the table has one, on
the ingested clock; the peak rate, read in 60-second buckets; and the collection
gaps detected in the window, per reason, with the seconds they cover. A table with
no ingested clock reports that rate as not measured rather than copying the source
rate over, and a window holding no record reports no span and no rate rather than
a rate of nought.

**The rate is a lower bound.** It counts the records `opnview` stored, which is not
the same as the records the firewall produced: a record that fell in a collection
gap — lines a filter-log page no longer reached, an `eve.json` file rotation
discarded, lookups the resolver's ring buffer dropped — is in no row and is counted
nowhere. So every rate on the collection surface is at most the source's real rate,
and a suggestion derived from it can fall short in the same direction; the
headroom factor of 4 is `opnview`'s own figure and is not a correction for this.
The caveat is documented here and never printed on a screen, under the same rule
as the observation-point limit.

## Purge

`internal/store/purge.sql` is the documented purge. It takes `:now`, reads the
horizon from `setting`, and removes rows older than it from every growing table:
`flow`, `dns_resolution`, `domain_attribution`, `security_event`, `dhcp_lease`,
`client`, `pair_volume_observation`, `geo_asn`, `collection_gap`,
`measurement_sample`, `state_snapshot`, `interface_address`,
`address_classification`, `purged_flow_hour`, `resource_record_observation` — by
`covered_until_at`, the end of its coverage interval — and the twenty-four
aggregates — six families of four periods each. **Its first statement records the purged part**:
before any flow is deleted, the figures of the flows it is about to delete are
added to `purged_flow_hour`, per hour and per every key an aggregate family reads,
so an hour slot computed afterwards still counts them. An hour or a day slot, and
an hour's purged part, is kept beyond its own end for as long as the ISO week and
the calendar month holding it, because a week or a month is composed from them. A
client the purged part still names is not removed, so no family loses its bytes, a
client assigned to a person is not removed either (*`client`*, *Retention*),
and a lookup that an attribution of a surviving flow names is kept, so no flow
loses its site name while it is kept: the lookup can lie up to the attribution
delay before the horizon while its flow lies after it. A resolver record an
attribution of a surviving flow names is kept for the same reason, and the foreign
key from `domain_attribution.address_observation_id` makes forgetting that an error.
The purge also records the
furthest horizon it has applied in `retention_purge`, which the flow and lookup
inserts read inside the insert statement, so a record the purge already removed is
never stored again, whenever the purge commits.

**The purge is ordered against the passes.** A running service purges through
`collect.Collector.Purge`, the retention-purge loop's only work, and never through
`store.Purge` directly. Every pass that stores rows — the filter log, the resolver,
the resolver's cache and local data, the leases and neighbours, the security events
— holds the collector's purge lock
for reading from before it stores its first row until its derivation has ended; the
purge holds it for writing, and the derivation lock as well, so it never runs while
a pass is between storing its rows and the end of its derivation, never runs during
a derivation with no pass behind it, and no pass stores or derives while it runs.
The passes still run side by side. The locks are always taken in one order — the
purge lock, then the derivation lock — and never while a database transaction is
open, so the purge cannot deadlock with a pass. Inside that section, before it
deletes anything, the purge runs a derivation of every unplaced end of a flow it is
about to remove (`purge_due_addresses` in `classify.statements.sql`): a row stored by a pass
whose derivation failed, or by an earlier run that stopped before deriving, enters
the purged part where the evidence held puts it, rather than frozen unplaced and
north-south. When the purge runs before any derivation of a run, that derivation is
the run's first, which places every address. Should the ordering ever break, the
refresh still rewrites every hour a purge wrote (*Refresh contract*, rules 1 and 4).

`state_item` needs no statement of
its own: its rows go with their snapshot through `ON DELETE CASCADE`, and a
member without its snapshot would be a set member with no set and no instant.
Bounded tables are never purged, so a surviving observation always joins to an
interface, a client, a rule and an interface-map entry — and, for a security
event, to the provider that contributed it and to the rule-info entry that
gives it a severity. `provider`, `provider_rule_info`, `source_availability`,
`resource_record_read`, `owner`, `blocklist` and `interface_network` are bounded by
the installation, not
by time, and are never purged: purging a client removes the machine, never the person it was
attributed to, and purging a lookup removes the lookup, never the purpose
somebody assigned to the list that refused it.

Afterwards, `PRAGMA foreign_key_check` returns no rows: `domain_attribution`
is removed by the `ON DELETE CASCADE` of both its parents, the resolver records it
names are kept while it is, and nothing else points at a purgeable row.

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
| Blocked | the blocked timeline with the rule and the interface, each carrying an explicit unknown state when the join key resolves to nothing; a block and a reject both, with their action | `idx_flow_refused_observed_at`, the partial index |
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
| Overview | `idx_flow_observed_at` | `traffic_scope`, `action`, `packet_bytes`, `src_interface_id`, `dst_interface_id`, `src_client_id`, `pair_outcome`, `interface_device` |
| Matrix | `idx_flow_observed_at` | the same, plus `rule_id` and `rule_lookup_state` |
| Interface | `idx_flow_src_interface_observed_at` | `src_client_id`, `packet_bytes`, `action`, `traffic_scope`, `dst_address`, `pair_outcome`, `interface_device` |

**A paired connection counts once in the screen queries, since step 5B** (B8c of
`specs/SPEC-step-5b-widget-api.md`). Overview, Matrix and Interface read
`classified_flow` and keep the rows whose `exit_leg_device` is `NULL`, and the Map
keeps the 24 h aggregate's rows whose `exit_leg_device` is `NULL`, so the second record
of a connection logged on two interfaces is left out, as `store.ReadVolumeWindow`'s
`Total` leaves it out. `exit_leg_device` is computed from `pair_outcome` and
`interface_device`, which is why both were added to the two covering indexes: without
them each covered plan would fetch every row to tell a second leg from a first. The
Matrix no longer drops the flows with no source interface, so what came from outside
or from this firewall has its row and the Matrix sums to the window's total;
`sql/schema-checks.sh` asserts that the Overview and Matrix totals equal the
volume-window total on a seed holding paired connections.

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

`sql/schema-checks.sh` applies `internal/store/schema.sql` to a fresh database
under `/data` — twice, because applying it again must change nothing — seeds it,
runs every query and every plan, and asserts each acceptance assertion it
carries. It exits non-zero on any failure.

`sql/seed.sql` is deterministic, takes its row counts as parameters, and
produces at least 100 000 rows in `flow`. It contains no address literal: every
address is synthesised from a counter at generation time and carries no
addressing-plan meaning, because nothing in the schema or in the queries
interprets an address.

**It produces both address families**, and how much of the network is IPv6 is
itself a bound parameter, so nothing may assume a proportion any more than it
may assume a count. `flow.ip_version` takes 4 and 6, and `client`,
`dns_resolution`, `security_event` and `pair_volume_observation` each carry
addresses of both. Both endpoints of a flow are always of one family, as a real
record is. The IPv6 form is eight hexadecimal groups derived from the counter
and deliberately **not** a documentation prefix: a recognisable prefix would be
a literal about somebody's addressing. Each of the seven screen queries is
asserted to answer with both families present — directly, for the five that
project an address, and by coverage for the two that only aggregate, where the
query's own totals are compared with the totals over its window.

Databases live in the `data` named volume mounted at `/data`, outside the
bind-mounted working tree. SQLite in WAL mode on a Windows bind mount locks
pathologically, and a `.db`, `-wal` or `-shm` file must never land in the
repository.
