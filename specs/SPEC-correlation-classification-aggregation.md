# SPEC — Step 5A: correlation, classification, aggregation (the data behind the canvas)

Status: APPROVED
Cycle kind: standard

Every decision below was taken by the maintainer on 3 October 2026; A9 and
AC4 were amended on 4 October 2026 to admit the swap endpoint, and AC20 to
state the watermark and equal-is-stale rule. The cycle's remaining defects
and the accepted proposals are carried by
`specs/SPEC-step-5a-corrections.md`. Step 5 is
split: this spec is **5A** (data). **5B** (the widget HTTP API) is a later
cycle, and its share is listed under *Deferred to 5B*.

## Context

**What step 4 left in place:**

- **Collection.** `internal/collect` writes:
  - `flow`, with `traffic_scope` pinned by a `CHECK`;
  - `client`, through the identity cascade in `internal/collect/identity.go`;
  - `dhcp_lease`, `dns_resolution`, `blocklist`, `security_event`,
    `collection_gap` and `source_availability`;
  - `measurement_sample`, holding the firewall gauges, per-interface counters,
    and per-pair `rate_bits_*` / `cumulative_bytes_*` readings from
    `traffic/top`.
- **Geolocation.** `internal/maxmind` fills `geo_asn` (4C).
- **Derived tables nobody writes yet:**
  - `volume_aggregate_*`, `owner_volume_aggregate_*`,
    `pair_volume_observation`, `domain_attribution`;
  - `TestNoCodePathWritesThePairVolumeTable` in
    `internal/store/write_test.go` forbids writing `pair_volume_observation`
    until step 5 does so deliberately.

**What 5A covers.** Correlation, classification and derivation, the
collection additions the step-3 mockup needs, and the store-level read
functions 5B will consume. 5A produces no route, HTML, CSS, JavaScript or
template, which is why the cycle is `standard`.

**Findings from reading the code and the documents.** Each one shapes the
scope below.

1. **The resolver log carries no answer address.** `search_queries` returns
   `client`, `domain`, `time`, `action`, `source`, `rcode`, `dnssec_status`,
   `blocklist` and `uuid` (survey, *Data source 5*). An attribution is
   therefore a lookup and a flow by the same client, close in time.
2. **Logged volume may not be conversation volume.** `flow.packet_bytes` is
   the length of one logged packet. pf normally logs only the packet that
   creates a state (`pf.conf(5)`, `log`). This must be verified before it is
   built on.
3. **The per-pair samples lose two facts.** `samplePairVolume`
   (`internal/collect/flowvolume_insight.go`) builds a canonical pair key and
   drops both the interface the record was read on and which address was the
   local one. The same pair read on two interfaces at one instant then
   collides on the sample's identity, and one reading is lost.
4. **Classification depends on ingestion order.** `buildFlow` and
   `clientForAddress` give every remote address a `client` row whose interface
   is whatever record named it first.
5. **pf's `dir` is per interface, not per network.** A LAN-to-Internet packet
   is `in` on the LAN interface.
6. **G6's recorded fix depends on interface addressing**, which step 4 drops
   (G13).

## Data the mockup displays

Source: `docs/mockups/canvas-mockup.html`, the step-3 deliverable. This is
what the product will look like. The maintainer's instruction is to have **as
much data as possible** behind it.

The following were read in full:

- the invented-installation tables and `computeAgg`;
- every `RENDER.*` renderer and every detail and popup builder;
- the People page and the no-code selector.

The four `docs/mockups/check-*.js` harnesses introduce no data item. The
tree's cross-side rule is carried as AC24.

**Status** gives the outcome after the maintainer's decisions. **Cannot**
items are listed with their reason and are not built.

| # | Widget (mockup) | Item shown | Status | Where / why |
|---|---|---|---|---|
| 1 | header | canvas period 1 h / 24 h / 7 d / 30 d | 5A derives | Rolling-window assembly (C2) |
| 2 | composition | current throughput in bit/s, split inbound / outbound / between interfaces | 5A derives | Latest per-pair `rate_bits_*` per interface (A4, A5); direction (C7). Its resolution is the sampling interval, not 2 s |
| 3 | composition | the same throughput grouped by interface or by client | 5A derives | As #2, with the local address mapped to its client and interface |
| 4 | composition | throughput grouped by protocol or by service port | **cannot** | `traffic/top` carries neither (`internal/opnsense/endpoints.go`, TrafficTop note; survey) |
| 5 | composition | packets by blocking reason (allowed / blocked by a rule / dropped with no rule) per direction | 5A derives | `flow.action` and the established `log_reason` vocabulary (A7); direction (C7) |
| 6 | composition | refused lookups beside the rings | already stored | `dns_resolution.action` |
| 7 | composition, feed, signatures | detection-engine figures | state stored; figures in step 6 | `source_availability`; `security_event` |
| 8 | composition | rolling strip at 2-second marks | **cannot at 2 s** | Rates exist only at the sampling interval. Decisions per bucket are derivable from `flow.observed_at` (C5) |
| 9 | composition | "Port not recorded" | 5A derives | `flow.dst_port IS NULL` |
| 10 | over time by scope | totals for outbound, inbound and between interfaces | 5A derives | Aggregates plus direction (C7) |
| 11 | over time by scope | 24 buckets per period | 5A derives | Bucketed series (C5) |
| 12 | interface ranking | label, VLAN tag, identifier, device | already stored | `interface` |
| 13 | interface ranking | bytes east-west / north-south, share, connections, blocked | 5A derives | Aggregates |
| 14 | interface ranking | clients seen out of clients known | 5A derives | Distinct `src_client_id` in the window / `client` rows of the interface with `last_seen_at` in the window |
| 15 | interface ranking | "N rules do not log" | 5A derives | `search_rule`'s `interface` is now stored (B5) |
| 16 | client ranking | hostname, MAC, address, unstable badge, interface, bytes, blocked, distinct destinations | 5A derives | `client`; G4 aggregates |
| 17 | public address | IPv4, IPv6, carrying interface, gateway, last changed | 5A derives | `interface_address` and the upstream fact (B4). "Last changed" comes from opnview's own history |
| 18 | per-person | per owner: clients, blocked, current rate, bytes, connections, interfaces | 5A derives | Owner aggregates; latest samples |
| 19 | per-person | "Reaching": site name, else operator, else address | 5A derives | `domain_attribution`, `geo_asn` |
| 20 | per-person | per-client chip: address, MAC, rate or "no data in the period" | 5A derives | `client`; latest samples |
| 21 | passed map | per placed peer: allowed bytes and connections, interfaces, coordinates, operator, country, AS | 5A derives | Allowed/blocked byte split (A6); `geo_asn` |
| 22 | passed map | unplaced volume (`miss`, `pending`, no row, east-west) | 5A derives | `LEFT JOIN` to `geo_asn` (`docs/widget-catalogue.md`, Part 4, item 2) |
| 23 | blocked map | outbound blocked by destination, unplaced, between interfaces, inbound by source | 5A derives | Direction (C7) |
| 24 | client detail | current rate, and the share per destination | 5A derives | Per-pair rate samples for the client's address |
| 25 | client detail | `local ⇄ peer:port`, connections, bytes | already stored | `flow` |
| 26 | connection tree | inside levels and outside levels; children summing to their node | 5A derives | `flow`, `client`, `owner`, `geo_asn`, `domain_attribution` |
| 27 | top sites | attribution rate, named / outbound flows, per site: flows, clients, bytes, dominant operator, clients | 5A derives | `domain_attribution`; G5 aggregates |
| 28 | top sites | collapse to the registrable domain | 5A derives | Public Suffix List provider, refreshed daily (A8) |
| 29 | sites by client | top sites per client | 5A derives | As #27 |
| 30 | attribution rate | outbound flows, named, rate, mean and maximum delay | 5A derives | `correlation_delay_seconds` |
| 31 | operators | AS, operator, bytes, allowed, blocked, peers, interfaces, "no geolocation answer" | 5A derives | Aggregates and `geo_asn` |
| 32 | Sankey | interface → interface / operator / "no geolocation answer", Other band | 5A derives | Aggregates and `geo_asn` |
| 33 | matrix | cells: bytes, allowed, blocked, rules; totals; outside column | 5A derives | Aggregates; rules beyond the horizon from `rule_volume_aggregate_*` (C9) |
| 34 | blocked feed | instant, engine, target, decided-by, run count, "→ this firewall" | 5A derives | G2 view (C6); `interface_address` (B4) |
| 35 | blocked feed | totals per engine, firewall rules, DNS lists, no rule matched | 5A derives | G2 view; A7 |
| 36 | rule table | description, pf label, deleted rule with its `rid`, interface it fired on, interfaces and clients reached, blocked, recent sparkline | 5A derives | `flow.rid`, `rule`, `flow.interface_device`; history from C9 |
| 37 | blocked DNS lookups | domain, list, purpose, clients and their interfaces, refused count | 5A derives | G6 places a lookup with no client row |
| 38 | source availability | kind, provider, availability, active, checked, detail | already stored | `provider`, `source_availability` |
| 39 | feed / availability | "detection engine stopped at 06:12" | **cannot** | The status endpoint reports a state, not when it began; `source_availability` keeps only the latest probe |
| 40 | health | uptime, CPU, memory, disk, temperature, history | already stored | `measurement_sample` |
| 41 | health | swap; gateway latency and loss | 5A collects | A9 |
| 42 | interface throughput | bit/s, bytes, packets | 5A derives | Counter deltas with reset detection |
| 43 | interface throughput | errors | 5A collects | A9 |
| 44 | custom chart | throughput per bucket, volume per bucket, CPU | 5A derives / already stored | Throughput from sampled rates, never logged volume divided by seconds |
| 45 | People page | owners, client counts, identity level, unstable badge, assignment date | already stored | `owner`, `client` |

## Scope

### A. Measures and collection

1. **pf logging, verified first.** Verify which packets a logging rule writes,
   against `pf.conf(5)` for the FreeBSD release OPNsense 26.7 ships and
   against `docs.opnsense.org`. Record the cited finding in
   `docs/opnsense-api-survey.md` (*Data source 1*) and on `flow.packet_bytes`
   in `docs/data-model.md`.
2. **Logged bytes** = `sum(flow.packet_bytes)`, named for what it is. **Every
   `flow` row counts.** A connection logged `in` on one interface and `out` on
   another counts twice, and this is documented next to the measure.
3. **Sampled bytes, a second and distinct measure.** First verify, against the
   `opnsense/core` source at 26.7.3, what time span `traffic/top`'s
   `cumulative_bytes_*` and `rate_bits_*` cover, and record the finding in the
   survey. Sampled bytes is then defined from that finding and nothing else.
   - If the counters cover only a sampling window, the measure is *bytes seen
     in samples*, carries its sampled seconds as coverage, and is **never
     extrapolated**.
   - A period with no samples is "not sampled", never 0.
4. **The sampler keeps what it currently drops.** Each per-pair reading
   records the interface it was read on and which address was the local end.
   Two readings of one pair on two interfaces at one instant both persist. This
   is the same endpoint and the same call; only what is stored changes. The
   subject vocabulary term is the Builder's to choose and record in
   *Vocabulary*.
5. **Current rate** per client, interface, owner and direction is read from
   the latest per-pair samples and stamped with their `sampled_at`.
   - A sample is current for **2 × the configured measurement interval**.
   - Beyond that, the answer is a "no current sample" state, never 0.
6. **Allowed and blocked bytes.** Every volume aggregate family carries the
   allowed/blocked byte split, as a schema edit in place.
7. **The `log_reason` vocabulary.** Establish pf's reason values from the pf
   source shipped with 26.7 and from the fixtures recorded off the live
   firewall, and record them, cited, in the survey and `docs/data-model.md`.
   Amend the data-model rule that forbids matching on `log_reason`, so that it
   allows matching on the **established** values only. "Dropped, no rule
   matched" is then derived from them.
8. **Public Suffix List, a third outbound call** (the maintainer's decision,
   which amends `ROADMAP.md`).
   - **Source.** The list is fetched from its canonical publicsuffix.org
     location. Verify the URL and the licence (expected to be MPL 2.0) from
     the project's own pages, and record both in the docs.
   - **Package.** The download lives in its own package, built on the 4C
     pattern (`specs/SPEC-maxmind-geolite2.md`):
     - a provider row and its `source_availability`;
     - a `collection_gap` on failure;
     - a daily refresh, conditional where the server supports it;
     - the previous good copy is kept when a download fails or is malformed;
     - no domain grouping is offered before the first successful download, and
       that state is explicit.
   - **Function.** It provides a registrable-domain function over
     `site_name`, using the list's own term, *registrable domain*. The
     stored `site_name` is never altered.
9. **Telemetry the mockup shows and nothing samples.**
   - **Interface errors.** Read from a response already fetched. The field
     names are `UNVERIFIED:` today and must be verified against the
     `opnsense/core` 26.7.3 source and the recorded fixtures before they are
     sampled.
   - **Swap.** `systemResources` carries no swap figure. Swap is read from
     `/api/diagnostics/system/systemSwap`, a **second new firewall endpoint**
     (amendment of 4 October 2026, the maintainer's decision), held to the
     same rules as the gateway-status endpoint below.
   - **Gateway latency and loss.** Read from `/api/routes/gateway/status`, a
     **new firewall endpoint**. It is:
     - cited against `docs.opnsense.org` or the `opnsense/core` source;
     - added to `internal/opnsense/endpoints.go`;
     - recorded in `docs/opnsense-api-survey.md`, marked for live probing at
       the step's validation;
     - recorded in `source_availability` when it is unavailable, never worked
       around.

   All three are sampled into `measurement_sample` at the existing
   measurement interval.

### B. Classification

1. **Upstream interface.** An interface is upstream exactly when
   `/api/interfaces/overview/interfaces_info` reports a non-empty `gateways[]`
   for it. That response is already read; the fact is now stored instead of
   dropped. The term borrows OPNsense's *upstream gateway* (verify the wording
   on `docs.opnsense.org`) and the catalogue's *upstream interface*.
2. **Outside.** An address reached through an upstream interface is
   *outside* and has no interface membership. Membership comes only from
   on-link evidence:
   - a lease;
   - an ARP or NDP neighbour entry;
   - an address inside a prefix stored in `interface_address` (B4);
   - an earlier sighting inside a non-upstream interface.
3. **No more phantom clients.**
   - No `client` row is created for an outside address.
   - The rows step 4 created for such addresses are purged once every
     reference to them is cleared: `flow.src_client_id` / `dst_client_id`,
     `security_event.src_client_id` and `dns_resolution.client_id`.
   - Stored flows are reclassified, and every slot they touch is recomputed.
   - The whole operation is idempotent.
4. **Interface addresses (G13 in part).** `addr4`, `addr6` (with their
   prefixes) and `gateways[]` from the same `interfaces_info` response are
   stored in an `interface_address` table with first-seen and last-seen
   instants. This lets the feed say "this firewall" and gives the *Public
   address* card its data. Update G13 in the catalogue to state what remains
   open.
5. **Rule interface.** `search_rule`'s `interface` field is stored, reversing
   the recorded *not stored* verdict in `docs/data-model.md`, with the reason
   recorded: the mockup's "N rules do not log" per interface.

### C. Aggregation

1. **Calendar slots, aligned to UTC:**
   - `_1h` = the hour;
   - `_24h` = the day;
   - `_7d` = the ISO week, starting Monday 00:00 UTC;
   - `_30d` = the calendar month.

   The naming mismatch (`_30d` holds months) is documented.
2. **Rolling windows** are assembled at query time from finer slots plus
   `flow`, by a store-level function 5B will consume.
3. **Refresh contract.** The refresh follows rules 1–4 of `docs/data-model.md`;
   rule 5 is struck, with the survey cited (the Insight aggregate endpoints
   answer 404 on 26.7). The refresh runs **after every `firewall_log` pass**.
4. **Nullable source interface.** `volume_aggregate_*.src_interface_id`
   becomes nullable, with an `ifnull` slot key. `schema.sql`, `sql/seed.sql`
   and the schema checks are edited in place.
5. **Bucketed series.** A store function returns a series whose buckets are
   aligned to slot boundaries.
   - A bucket inside a covered, gap-free interval with no rows is 0.
   - A bucket outside coverage, or inside a `collection_gap`, is a gap.
6. **G2 — the `blocked_decision` view.** **Extend** the recorded
   `engine_kind` set so that it has a place for:
   - each `blocklist.purpose` value, including unassigned;
   - a DNS block with no list recorded;
   - a firewall drop with no rule.

   Record the set in `docs/data-model.md`.
7. **G3 — direction.** Direction is derived from membership:
   - **inbound** = the source is outside;
   - **outbound** = the destination is outside;
   - **between interfaces** = neither end is outside.

   `flow.direction` stays pf's own per-interface value. The three terms are
   the catalogue's, and are recorded in *Vocabulary* as opnview's own, with
   the reason (pf's `dir` is per interface).
8. **G4 and G5.** Add a `client_volume_aggregate_*` family and a
   `domain_volume_aggregate_*` family. `distinct_*` counts are never summed
   across slots.
9. **Rule history beyond the horizon.** Add a `rule_volume_aggregate_*`
   family keyed on `rule_id`, carrying allowed and blocked counts and bytes.
   The matrix cells and the rule table's "Recent" sparkline read it beyond the
   `flow` horizon.
10. **G6.** `dns_resolution.interface_id` plus an `interface_lookup_state`,
    resolved from the same membership evidence as B2.
11. **`pair_volume_observation`** is derived from `flow`:
    - `service_port` = the real `dst_port`, with the column documentation
      amended;
    - a protocol with no port gets 0;
    - the guard test is replaced by one that admits the derivation and nothing
      else.
12. Gaps G2–G6 move to *Gaps closed* in `docs/widget-catalogue.md`. G13 is
    updated as partly closed.

### D. Site-name attribution

1. **The rule.** A flow gets an attribution exactly when the eligible lookups
   by the same client in `[observed_at − max_delay, observed_at]` name **exactly
   one distinct domain**. Two lookups of that same domain still attribute.
   Otherwise the flow gets no row.
   - **The same client** means the same `client_id` where both rows carry one,
     and the same address otherwise.
   - **Eligible flows** have an **outside** destination. East-west flows are
     not attributed.
   - **Eligible lookups** have `action = pass`, answered by recursion, from
     cache, or from local data (host overrides).
   - The exact `source` values are established from the survey and the
     recorded fixtures, and recorded.
2. **The maximum delay** is a `setting` row, default 5 s, validated as a
   positive whole number.
3. **What is stored.**
   - `site_name` is the lookup's `domain`, verbatim. A domain is never
     invented.
   - `correlation_delay_seconds` is never negative.
   - No provenance column is added: the lookup foreign key and the delay are
     the stored provenance. The API-level constant method field is 5B's.
   - *Reversed on 9 October 2026 by `specs/SPEC-resolver-cache-attribution.md`,
     C3:* the resolver's cache added a second method, so `domain_attribution`
     now records the method on each attribution (`resolver_cache_answer` or
     `lookup_timing`) in a column constrained by a `CHECK`, beside the lookup
     foreign key and the delay, which stay.
4. **`aggregate_mode = no_domains`** does **not** change what is written.
   Attributions and domain aggregates are stored, and 5B withholds them at the
   API. `docs/data-model.md` states this.
5. **Attribution rate per client.** It is **undefined** when no `dns_lookup`
   provider was reachable over the window, and 0 only when one was.
6. **Re-pointing.** When a client identity improves:
   - earlier `flow` and `dns_resolution` rows are re-pointed to it;
   - attribution is re-run for the affected flows;
   - every affected slot is recomputed.

### E. Documents

- **`ROADMAP.md`:**
  - the *Two outbound calls* rule becomes three, naming the Public Suffix List
    download, with the reason;
  - the Insight sentence of step 5 is struck, citing the survey;
  - the step table is updated.
- **`docs/data-model.md`:**
  - the entity list, the growing-table list, *Vocabulary* and *API field
    coverage*;
  - the correlation rule, stating that the resolver gives no answer address;
  - refresh rule 5 struck;
  - the pf, `traffic/top` and `log_reason` findings;
  - the `engine_kind` set;
  - the contracts of the store functions 5B consumes.
- **`docs/opnsense-api-survey.md`:** the gateway-status endpoint, and the swap
  and interface-error fields.
- **`docs/widget-catalogue.md`:** G2–G6 closed and G13 updated.
- **`README.md`:** the outbound destinations, if it lists them.

## Acceptance criteria

All criteria run through `docker compose run --rm checks` and
`docker compose run --rm schema-checks`. Fixtures take their interface and
client counts as parameters and carry both IP versions.

### Verification and outbound calls

- [ ] AC1 — The pf logging behaviour is recorded in the survey and on
  `flow.packet_bytes`, each claim citing `pf.conf(5)` or `docs.opnsense.org`.
- [ ] AC2 — The time coverage of `traffic/top`'s counters is recorded, citing
  `opnsense/core` 26.7.3, and the definition of sampled bytes refers to it. A
  period with no samples yields "not sampled", not 0.
- [ ] AC3 — The source-policy test is updated to exactly **three** packages
  that build HTTP requests: the firewall, MaxMind and the Public Suffix List.
  Its comment and `ROADMAP.md` say three. A fourth package building a request
  fails the test.
- [ ] AC4 — The only new firewall endpoints are `/api/routes/gateway/status`
  and `/api/diagnostics/system/systemSwap` (amended 4 October 2026). Each is a
  read-only GET, cited, recorded in the survey, and degrades to
  `source_availability` when unavailable. A test covers that state for each.
  *Amended on 9 October 2026 by `specs/SPEC-resolver-cache-attribution.md`,
  AC3:* `/api/unbound/diagnostics/dumpcache`,
  `/api/unbound/diagnostics/listlocaldata` and
  `/api/diagnostics/cpu_usage/stream` are admitted as well, on the same terms,
  each verified at `opnsense/core` 26.7.3.
- [ ] AC5 — The `log_reason` values are recorded with citations. No code
  matches on a value outside the recorded set, and a test fails if one does.

### Public Suffix List

- [ ] AC6 — On a fake server:
  - a successful download is stored and used;
  - a failed or malformed download keeps the previous copy and records a
    `collection_gap` and the availability state;
  - with no copy at all, registrable-domain grouping reports an explicit
    unavailable state;
  - an unchanged list does not re-download where the server signals it.
- [ ] AC7 — The registrable-domain function is correct on wildcard, exception
  and multi-label rules, using the list's own test vectors, and leaves
  `site_name` untouched.

### Classification

- [ ] AC8 — An interface is upstream if and only if the fixture's
  `interfaces_info` gives it a non-empty `gateways[]`.
  - Moving `gateways[]` to another interface moves the property.
  - Rewriting every description, `user_label` and identifier changes nothing.
- [ ] AC9 — An outside address seen on the upstream interface and on a
  non-upstream one:
  - gets no `client` row;
  - makes every flow touching it `north_south`;
  - leaves no interface on its end of those flows.
- [ ] AC10 — Ingesting one fixture in two orders into two databases yields
  identical `src_interface_id`, `dst_interface_id`, `traffic_scope`,
  `src_client_id` and `dst_client_id` per `log_digest`.
- [ ] AC11 — Reclassification of a step-4-shaped database holding
  remote-address clients:
  - the clients are gone;
  - `PRAGMA foreign_key_check` is clean;
  - the `CHECK` holds;
  - affected slots equal a recomputation;
  - a second run changes nothing.
- [ ] AC12 — `interface_address` keeps first-seen and last-seen, and records a
  change of public address as a new row whose predecessor's last-seen is kept.
- [ ] AC13 — `rule.interface` is stored from `search_rule`. The count of
  non-logging rules per interface equals a direct count over the fixture.

### Measures, samples and telemetry

- [ ] AC14 — Two readings of one pair on two interfaces at one instant
  persist as two rows. Each names its interface and its local address.
- [ ] AC15 — A client's current rate equals the sum of its latest per-pair
  `rate_bits` readings within 2 × the measurement interval, and carries their
  `sampled_at`. Beyond that bound it is "no current sample".
- [ ] AC16 — Per-interface throughput comes from consecutive counter samples.
  A decreasing counter is a reset, never a negative rate.
- [ ] AC17 — Swap, interface errors, and gateway latency and loss are sampled
  from fixtures whose field names cite their verification. A missing field
  yields no sample, never 0.

### Aggregation

- [ ] AC18 — Slot boundaries are correct for:
  - months of 28, 29, 30 and 31 days;
  - an ISO week spanning a year boundary;
  - an hour crossing midnight UTC.
- [ ] AC19 — For every family (interface, owner, client, domain, rule) and
  every period, each slot's summable figures equal the same sums over `flow`.
  This includes the `ifnull` source-interface slot (no remainder) and the
  allowed/blocked byte split.
- [ ] AC20 — Refresh contract:
  - a flow in a closed slot rewrites that slot only;
  - a pass with nothing new rewrites only the current slots;
  - a slot whose `computed_at` is strictly later than its newest
    `ingested_at` is not rewritten; equality counts as stale, because two
    one-second stamps cannot be ordered, and `computed_at` is the refresh's
    watermark, not its clock (amended 4 October 2026);
  - the refresh runs after each `firewall_log` pass.
- [ ] AC21 — `client_count` and the `distinct_*` figures are never summed
  across slots: a client active in two slots counts once. Every owner period
  holds its unassigned slot.
- [ ] AC22 — Inside the horizon, the rolling-window function equals a direct
  computation over `flow` for randomly drawn windows. Beyond it, the function
  reads slots and returns the interval it actually covers.
- [ ] AC23 — Bucketed series sum to the window total. Buckets inside a gap or
  outside coverage are gaps, not zeros.
- [ ] AC24 — Every flow in a window lands in exactly one node per level for
  each inside root (interface, client, owner) and on the outside side
  (operator, unplaced, site name, "no site name inferred"). Children sum to
  their parent, which is the rule of
  `docs/mockups/check-tree-reconciliation.js`.

### Gaps

- [ ] AC25 — Every blocked flow, every blocked or dropped lookup and every
  blocked security event appears in `blocked_decision` exactly once, under the
  extended `engine_kind` set. Per-kind counts equal direct counts.
- [ ] AC26 — Direction from membership: inbound + outbound + between
  interfaces = all, for bytes and connections. A LAN-to-Internet flow logged
  `in` on the LAN is **outbound**.
- [ ] AC27 — `dns_resolution.interface_id` resolves from membership
  evidence. An address with no evidence is `not_found`, and one not yet
  resolved is `pending`.
- [ ] AC28 — G2–G6 sit under *Gaps closed* and G13 is updated. The count of
  `MISSING FROM MODEL:` markers equals the number of open rows.

### Attribution

- [ ] AC29 — Fixture outcomes:
  - one eligible domain in the window → a row with that domain verbatim and
    the exact delay;
  - two lookups of the same domain → a row;
  - two distinct domains → no row;
  - a blocked lookup → no row;
  - an east-west flow → no row;
  - a host-override lookup → eligible;
  - a lookup 6 s before → no row at the default, and a row with the setting at
    6.
- [ ] AC30 — The attribution rate is undefined with no reachable resolver
  source, and 0 with a reachable one that yielded nothing.
- [ ] AC31 — When a lease later names an address seen only in flows:
  - rows re-point to the better identity;
  - attributions are recomputed;
  - slots match a recomputation.
- [ ] AC32 — With `no_domains` set, attributions and domain aggregates are
  still written.
- [ ] AC33 — Purging a lookup or a flow removes its attributions, and
  `PRAGMA foreign_key_check` is clean.

### Derivation, schema and documents

- [ ] AC34 — `pair_volume_observation`:
  - both directions collapse to one row keyed on the real `dst_port`;
  - a portless protocol gets 0;
  - the replacement guard fails on any other non-test writer.
- [ ] AC35 — The schema:
  - still applies idempotently;
  - the seed fills every new column and table in both IP versions;
  - no refresh or read query introduced here plans a `SCAN` of a growing
    table, at either seed size.
- [ ] AC36 — The literal checks (no interface name, device, VLAN name, address
  or CIDR literal) pass over every new file. No new setting is hardcoded in
  place of a `setting` row.
- [ ] AC37 — The document edits under *Scope E* are present, and the entity
  and growing-table lists match the schema.

## Deferred to 5B

- One authenticated route per widget type, with:
  - parameter parsing against the catalogue;
  - reference resolution with an unresolved state;
  - unknown-key reporting;
  - availability, coverage, gaps and freshness carried in each response;
  - explicit remainders on truncation;
  - map, tree and Sankey reconciliation at response level.
- `no_domains` withheld at the API.
- ~~A constant attribution-method field (`resolver_lookup`) in responses.~~
  Replaced on 9 October 2026 by `specs/SPEC-resolver-cache-attribution.md`:
  the method is stored per attribution (`domain_attribution.method`,
  `resolver_cache_answer` or `lookup_timing`), and 5B returns that stored
  per-record method rather than a constant.
- The questions that go with these:
  - which presets get an endpoint;
  - GET or a read-only POST, and CSRF for the latter;
  - defaults for absent parameters;
  - bare-`shape` endpoints;
  - a lower-bound flag on volume figures;
  - `custom_chart` bucket alignment.

## Out of scope

- Every route, template and renderer (5B, step 7).
- Alert aggregation and severity resolution (step 6), and G7/G8.
- G12, which is never to be implemented.
- Owner-assignment surfaces (step 7).
- **Cannot** items #4, #8 at 2 s, and #39.

## Risks

1. **The sampled measure may cover only seconds per sample.** Logged bytes
   would then be the only period total. Neither measure may be presented as
   conversation volume.
2. **Reclassification rewrites history since step 4.** AC11 keeps it total
   and idempotent.
3. **`gateways[]` may be set on a tunnel**, which then becomes upstream. Check
   this on the live firewall.
4. **The strict attribution rule will give low rates on busy clients.** That
   is the honest outcome.
5. **Scope.** This cycle is large: collection additions, a third outbound
   call, one new firewall endpoint, five aggregate families, and three gaps
   beyond G2–G6. The Builder reports any part it could not finish, rather than
   thinning it.
6. **Refresh cost at 1 M `flow` rows.** AC35 guards the query plans.
7. **The gateway-status endpoint and the telemetry field names are only
   verified from source in this cycle.** They are probed live at the step's
   validation.
