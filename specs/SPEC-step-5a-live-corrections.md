# SPEC — Step 5A live corrections: this firewall as an end, the two-leg duplicate, host names, gateway samples

Status: APPROVED
Cycle kind: standard

All decisions below were taken by the maintainer on 5 October 2026.

## Context

Step 5A was committed at `dd224a5`. Its specs are:

- `specs/SPEC-correlation-classification-aggregation.md`;
- `specs/SPEC-step-5a-corrections.md`;
- `specs/SPEC-purge-serialisation.md`.

It was verified only on synthesised fixtures. The first live run on the
maintainer's OPNsense 26.7, on 5 October 2026, about 25 minutes after logging
was turned on for the internal pass rules, measured the defects below. Each one
is matched here to the code that causes it.

Figures from the live run are given here as orders of magnitude, to motivate
the work. They never enter a committed file other than this spec (rule *On
scale, without describing anyone's network* of the survey).

### F1 — The firewall's own addresses are not recognised as an end

- **The data already exists.** `interface_address` stores every address the
  firewall holds, under the source fields `addr4`, `addr6`, `ipv4` and `ipv6`.
  `store.IsFirewallAddress` exists, and the `blocked_decision` view already says
  "→ this firewall" (`target_is_this_firewall`). Classification ignores all of
  this.
- **Upstream addresses become anonymous.** An address on an upstream interface
  is *outside*. A record logged `out` on the upstream interface whose source is
  the firewall's upstream address therefore has neither end placed, and counts
  as anonymous `north_south` / outbound. This was about 70% of live flows,
  mostly Unbound recursion (udp/53), then 443, ICMPv6, NTP and STUN.
- **Internal addresses become a phantom client.** The firewall's address on a
  non-upstream interface falls inside that interface's network, so
  `classifyAddress` mints an `address_in_interface` client for it. Clients'
  flows to the firewall (DNS, NTP, the GUI) then count as east-west, with the
  firewall as a client.
- **Knock-on effects:**
  - flows to the firewall become attribution candidates, because
    `attribution_candidates` only needs `dst_interface_id IS NULL`;
  - the firewall's global IPv6 upstream addresses appear as an outside peer
    under an operator in `ReadConnectionTree`.

### F2 — One connection, two records

- pf writes the packet that creates a state. A client's connection to the
  Internet is therefore logged `in` on the client's interface and `out` on the
  upstream interface.
- Under outbound NAT, the second record's source is already the firewall's
  upstream address (*to verify*: translation happens before filtering).
- IPv6 and inter-interface connections are also logged once per interface,
  with no translation.
- 5A counts every row (5A Scope A2, OQ4), and nothing pairs the two records.
- The filter log returns fields that could pair them — `ttl`, `id`, `seq`,
  `ack`, `datalen`, `length`, `flow`, `hoplimit` (survey, *Data source 1*,
  *Response shape*) — but `flow` stores none of them except `length`.

### F4 — Some lookups stay `unknown_hostname`

About 5% of live lookups stay `unknown_hostname`, although leases are being
collected. The candidate causes the code admits are:

- **H1** — the label is taken from the logged side only. A bare `host` never
  matches a lease carrying `host.domain`.
- **H2** — a trailing dot on either side.
- **H3** — the lease's validity window excludes the lookup, for example
  `expire` 0 for an infinite lease.
- **H4** — the querier is the firewall itself.
- **H5** — no lease exists for the host (static hosts).
- **H6** — a lookup left unresolved before a restart is never retried, because
  `hostnamesSince` restarts at `Now` and is not persisted.

### F5 — No gateway sample

`sampleGateways` runs every measurement pass, and swap samples, which are read
just after it, do arrive. Gateway samples never do. `readObject` folds
401/403/404/5xx and a non-JSON body into a single "did not answer", so the
cause cannot be told from the code.

### F6 — Late lookups

- **Re-attribution when the lookup arrives later appears to work in the code.**
  The lookup pass widens the derivation window over each lookup it stores, and
  `Attribute` re-runs over the flows in that window. No test pins this.
- **A defect sits next to it.** `CollectDNSLookup` skips, without inserting,
  every row with `LookedUpAt <= newestStored`. A different lookup in the same
  second as the newest stored one is therefore lost.

### F7 — The roadmap

The step table and the step-5 checklist are out of date.

### Already established, and the next cycle

`/api/unbound/diagnostics/dumpcache` exists on `opnsense/core` `master`
(`Unbound/Api/DiagnosticsController.php`, `dumpcacheAction`, configd
`unbound dumpcache`). So does `listlocaldata`. Attribution from the resolver
cache (exact evidence first, the timing rule as fallback, method recorded per
attribution), and host-name resolution through local data, are the **next
cycle**, by the maintainer's decision. This cycle does not touch either.

## Decisions (5 October 2026)

1. **Term.** Use OPNsense's own term for the firewall's own addresses. The lead
   is *This Firewall* in the rule editor, with pf's `self`: verify it on
   `docs.opnsense.org` and in pf.conf(5) and cite it. Keep it consistent with
   `target_is_this_firewall`. If the verification finds a different OPNsense
   word, stop and report it.
2. **Counting a paired connection.**
   - Count it **once**, on the client's `in` record.
   - The exit leg stays in `flow`. It still counts on the upstream interface's
     own per-interface figures, and in no other volume family.
   - The rule family keeps counting every record, since each record is a match
     of its own rule.
   - `pair_volume_observation` excludes exit legs.
   - This amends 5A Scope A2 and its OQ4.
3. **Unpaired upstream `out` records sourced by this firewall** are *not
   paired*, always. The record keeps its this-firewall end, but opnview never
   claims it is the firewall's own traffic rather than a client's NATed
   connection.
   - **Amended on 7 October 2026 by the maintainer.** Two verify iterations
     each found a configuration where the inference from rule logging was
     wrong: non-logging automatic port-forward rules, and `rdr pass`, which no
     ruleset reading can see.
   - The `pass_rule_logging` history, and every rule read made for this
     inference alone, are removed.
4. **Pairing time bound.** A validated `setting` row, default 1 s.
5. **Untranslated two-leg records** (IPv6, inter-interface) are paired by the
   same rule and counted per decision 2.
6. **How this firewall appears in the derived columns.**
   - Direction: new values, to and from this firewall, named per decision 1.
   - Scope: a third `traffic_scope` value for a flow with a this-firewall end.
     The `CHECK`, the seed, the schema checks and the catalogue are amended
     accordingly.
7. **Catalogue.** Amend `docs/widget-catalogue.md` now: this firewall gets its
   own row and column in the matrix binding, and the *Public address* card
   records the double-NAT limit.
8. **Resolver cache.** Moved to the next cycle (see above).
9. **Reasons for non-attribution.** Moot.
10. **Hosts with no lease (H5).** Moved to the next cycle (`listlocaldata`).
11. **Loopback queriers.** `127.0.0.1`, `::1` and the name `localhost` are
    recognised as this firewall. These are protocol constants, not
    configuration.
12. **Schema change.** Edit `schema.sql` in place and rebuild the live
    database.
    - The orchestrator takes a dump first and shows the exact paths before
      removing anything.
    - Migrations begin at step 8.
13. **Roadmap figures.** `ROADMAP.md` records proportions only, never an
    installation's names, addresses or counts.

## Scope

Everything here applies to the working tree on top of `dd224a5`.

### Process rules for the whole cycle

- **Language and vocabulary.** Everything is written in English. Every term
  comes from OPNsense, FreeBSD pf or the product concerned, researched and
  cited. Where none exists, the term is opnview's own and is recorded in
  `docs/data-model.md` *Vocabulary* with the reason.
- **Deletion.** Nothing is deleted with a wildcard or glob. A file is deleted
  only by its exact path, after that path has been shown to the maintainer. No
  destructive command runs in the background.
- **Git.** No git command moves or discards work.
- **Committing.** Nothing is committed until the live validation below passes
  and the maintainer accepts it.
- **No installation data in the repository.** No device name, address or count
  from the maintainer's installation enters a committed file. Fixtures are
  synthesised and say so in their provenance block.
- **Sources.** Every behaviour that rests on an OPNsense or pf fact cites that
  fact, at `opnsense/core` 26.7.3 or at the FreeBSD release pf.conf(5) page the
  survey already cites. The citation goes in the code comment and in
  `docs/opnsense-api-survey.md`.

### 1. This firewall as an end

1. **The term.** Establish it per decision 1 and record it in *Vocabulary*.
2. **Recognition.**
   - **What is recognised.** An end of a `flow`, the source of a
     `security_event`, or the querier of a `dns_resolution` is this firewall
     when either:
     - its address was held by the firewall at the row's instant, meaning
       `interface_address` under `addr4`, `addr6`, `ipv4` or `ipv6`, on any
       interface; or
     - it is a loopback address or name (decision 11).
   - **"Held at the instant"** is defined from `first_seen_at`/`last_seen_at`
     and the discovery interval, and documented.
   - **Order.** Recognition comes before every on-link rule.
   - **What a this-firewall end is.** It is never a client, never outside,
     never an outside peer and never an operator node. It records the
     interface the address belongs to.
3. **Counting and presentation in the store.**
   - `classified_flow` carries the new direction and scope values (decision
     6).
   - This firewall is its own key in the volume family, and therefore not part
     of the matrix's outside column.
   - `ReadConnectionTree` has a this-firewall node on each side, and every level
     still sums to the level above.
   - The client, peer and owner families get no contribution from this
     firewall.
4. **Attribution.**
   - A flow whose destination is this firewall is not a candidate.
   - A flow whose source is this firewall is outside the per-client rate.
5. **Re-placement.**
   - `ReclassifyEverything`, and the incremental pass when the firewall's
     addresses change, apply recognition to every stored row.
   - The phantom clients minted at firewall addresses are purged once nothing
     refers to them.
   - The affected slots are recomputed.
   - A second run writes nothing.

### 2. Two legs of one connection

1. **Verify first, and record it in the survey.** In a new subsection of *Data
   source 1*, citing pf.conf(5) and `docs.opnsense.org` `manual/nat.html` /
   `manual/firewall_settings.html`, record:
   - **(a)** whether translation happens before filtering;
   - **(b)** which logged fields translation and forwarding rewrite, and which
     they keep;
   - **(c)** OPNsense 26.7's defaults for *static port*, `modulate state` and
     scrub `random-id`;
   - **(d)** OPNsense's terms for all of this.

   A claim that cannot be confirmed is marked `UNVERIFIED:` and is never used
   for pairing.
2. **Store only the logged fields verified invariant across both legs.**
   - Update *API field coverage*, reversing those fields' "not stored" verdicts
     with the reason.
   - The schema is edited in place (decision 12).
3. **Pairing rule.** An `out` record on an interface is the second leg of an
   `in` record on another interface when:
   - both have the same protocol, IP version, destination address and
     destination port;
   - every verified invariant field is equal;
   - their `observed_at` values lie within the setting of decision 4;
   - the `in` record passed;
   - the pair is one-to-one: exactly one candidate on each side.

   **Translation.** Under outbound NAT the source differs; the second leg's
   source is this firewall. Without NAT the source is equal (decision 5).

   **How the pairing is decided:**
   - independently of the order records arrived in;
   - again when either leg arrives later, through the derivation window;
   - idempotently;
   - recorded per record, naming its partner.
4. **Outcomes.** Every upstream `out` record sourced by this firewall has
   exactly one outcome:
   - *second leg of a client record*;
   - *not paired* (decision 3, as amended).

   Outcome names are researched first; failing that they are opnview's own
   terms, recorded in *Vocabulary*. No unpaired record is assigned to a
   client.
5. **Counting.**
   - Applied per decision 2.
   - `docs/data-model.md` states the policy next to the logged-bytes measure,
     and names the 5A clause it amends.

### 3. The unresolved host names

1. **Diagnostic query.** A named query in `sql/queries/diagnostics.sql` sorts
   every `unknown_hostname` lookup into one cause bin:
   - exact match;
   - first label of the logged name;
   - first label on both sides;
   - equal once a trailing dot is removed;
   - a lease matches by name but not at the lookup's instant;
   - no lease names the host;
   - the querier is this firewall.
2. **Fix H1, H2, H3, H4 and H6** wherever the code admits them. Each fix has a
   synthesised fixture that fails on `dd224a5` and passes afterwards.
3. **H6.**
   - An unresolved lookup is re-examined by at least one lease pass after it
     was ingested, including across a restart.
   - The work of a pass stays bounded by what changed.
   - No pass rescans every unresolved lookup.
4. **Matching** stays case-insensitive. No name or pattern is hardcoded.

### 4. The missing gateway samples

1. **Keep the HTTP outcome.** `readObject`, or its caller in this path, keeps
   the HTTP outcome, so each absence can be named.
2. **Name each absence separately** in the availability detail:
   - denied (401/403), with the privilege as OPNsense names it (to verify);
   - not found (404);
   - `"status": "failed"`;
   - no gateway listed;
   - gateways listed with no figure (`~`), stating what `~` means in
     `dpinger.inc` (to verify).
3. **Find the cause.** The orchestrator runs live query L10 and one read-only
   call of `/api/routes/gateway/status` with opnview's key. The output is
   inspected, never committed, and handed to the Builder.
4. **Fix the cause found.**
   - If the response shape differs from the survey, correct both the survey and
     the code.
   - Add a fixture with the live *shape* and synthesised values, whose
     provenance block says so.

### 5. Late lookups

1. **Pin the behaviour with a test.**
   - A flow is stored and derived with no lookup.
   - A later lookup pass stores a matching lookup, and the attribution then
     exists with no further flow pass.
   - A still later lookup of a second domain in the window removes it.
2. **Fix the skip in `CollectDNSLookup`.**
   - A row is inserted, and the insert decides, whatever its instant compared
     with the newest stored one.
   - The early page stop and the gap test are kept.
   - Code and comment agree.

### 6. `ROADMAP.md` and the documents

1. **Step table, row 5.** It says 5A is implemented and **not yet validated
   live**, gives the first live run (5 October 2026), and points to this spec
   and to the next cycle (resolver cache). 5B stays to do.
2. **Step-5 checklist.** Each item is marked answered or open, in proportions
   only:
   - **upstream detection:** answered;
   - **share of host-name queriers:** answered;
   - **double NAT / carrier-grade NAT:** a new item. The firewall does not see
     its public IPv4 address, and nothing opnview reads can supply it;
   - **gateway status:** answered or still open, per item 4.

   The double-NAT limit is also stated in the `ReadPublicAddresses` contract.
3. **`docs/data-model.md`.**
   - *Vocabulary*, with the count sentence updated;
   - *Classification*;
   - the `flow`, `dns_resolution` and `domain_attribution` sections;
   - *API field coverage*;
   - the 5B contracts that change.
4. **`docs/widget-catalogue.md`.** Amended per decision 7.
5. **`docs/opnsense-api-survey.md`.** Items 2.1 and 4.

## Acceptance criteria

All criteria run through `docker compose run --rm checks`,
`docker compose run --rm schema-checks` and
`docker compose run --rm dev go test -race -count=1 -timeout 30m ./...`, and
`shellcheck -S error sql/schema-checks.sh` passes on the host. Fixtures take
their interface and client counts as parameters, carry both IP versions, and
name no real installation. Each new test is shown to fail on the code before
its fix, and the report says so.

- [ ] AC0 — Every criterion of the three 5A specs still passes, except where
  this spec amends one by name (5A Scope A2 and OQ4, by decision 2). No test is
  weakened. The plan checks cover every new statement at both seed sizes.
- [ ] AC1 — *Vocabulary* carries the term, with its `docs.opnsense.org`
  citation and pf.conf(5) if `self` is cited. Code identifiers use the term.
- [ ] AC2 — Recognition, in a fixture where the firewall holds addresses on one
  upstream interface and N non-upstream interfaces, in both families:
  - every flow end, event source and lookup querier at such an address, or at a
    loopback, is recorded as this firewall, with its interface;
  - after `ReclassifyEverything`, no `client` row sits at a firewall address;
  - no `flow`, `security_event` or `dns_resolution` row carries a client on
    such an end.
- [ ] AC3 — Recognition is decided as of the row's instant, with a test inside
  and a test outside the "held" interval.
- [ ] AC4 — Counting:
  - this firewall's volume key equals a direct sum over `flow`;
  - the outside column contains none of those flows;
  - `ReadConnectionTree` places them under this-firewall nodes, never under
    `operator:*`, `unplaced:*`, `interface:none` or `client:none`, and every
    level sums to its parent;
  - the third `traffic_scope` value passes the `CHECK`, and the seed fills it.
- [ ] AC5 — Attribution:
  - a flow to this firewall gets no attribution and is not a candidate;
  - `ReadAttributionRate` excludes flows from this firewall.
- [ ] AC6 — A database holding 5A-era phantom clients at firewall addresses is
  corrected by `ReclassifyEverything`:
  - those clients are purged;
  - the slots equal a recomputation;
  - a second run writes nothing.
- [ ] AC7 — The survey subsection of item 2.1 exists, each claim quoted with its
  URL. Unconfirmed claims are marked `UNVERIFIED:`, and a test fails if pairing
  reads a field whose invariance is unverified.
- [ ] AC8 — The stored invariant fields appear in *API field coverage* as
  stored, with the reason.
- [ ] AC9 — Pairing:
  - a NATed pair is paired one-to-one;
  - an untranslated IPv6 pair is paired;
  - two clients reaching the same destination and port within the bound, with
    distinct invariants, are each paired to their own leg;
  - indistinguishable candidates leave both legs unpaired;
  - an `out` leg ingested a pass before its `in` leg is paired at the second
    pass;
  - two ingestion orders give identical rows;
  - a second derivation writes nothing.
- [ ] AC10 — Every upstream `out` record sourced by this firewall has exactly
  one outcome of item 2.4. No record is ever labelled the firewall's own
  traffic, and no unpaired record carries a client. No code or schema object
  remains for the removed rule-logging inference (amended 7 October 2026).
- [ ] AC11 — Decision 2 holds:
  - each volume family equals a direct computation that counts a paired
    connection once;
  - the upstream interface's own figures count its legs;
  - the rule family counts every record;
  - `pair_volume_observation` excludes exit legs;
  - there is one test per family.
- [ ] AC12 — The pairing bound is a validated setting row: a malformed value is
  refused, and the default is 1.
- [ ] AC13 — The diagnostic query of item 3.1 is in `diagnostics.sql`, and
  `schema-checks` executes it on the seed.
- [ ] AC14 — Each of H1, H2, H3, H4 and H6 that the code admits has a fixture
  that fails before its fix and passes after. A lookup stored as
  `unknown_hostname` before a restart is resolved by the first lease pass after
  it, once a lease matches. The retry statement scans no growing table.
- [ ] AC15 — Gateways:
  - with a fixture of the live shape, `measurement_sample` receives the
    gateway measures for each gateway that has figures;
  - each of the five absences of item 4.2 yields its own wording and no row,
    with one test each;
  - `~` never yields 0.
- [ ] AC16 — The test of item 5.1 passes. A lookup whose instant equals the
  newest stored one is inserted when its key is new, and not duplicated when
  its key exists.
- [ ] AC17 — Documents:
  - row 5 says "not yet validated live" and gives 5 October 2026;
  - the checklist items are marked;
  - the double-NAT limit is in the checklist, in the `ReadPublicAddresses`
    contract and in the catalogue;
  - a grep of every changed document for the installation's device names and
    upstream addresses returns nothing;
  - every new opnview-own term has its *Vocabulary* row, and the count sentence
    matches.

## Live validation — before anything is committed

The orchestrator runs these, read-only, on the live database. Each query is run
once on the current deployment (the baseline), and again after the new build
has run for at least 25 minutes with the same logging settings, over a window
of the same length.

| # | What | Expected |
|---|---|---|
| L1 | Flows with a firewall-address end (source / destination), as a share | Same order as the baseline, an input measure |
| L2 | Named query `live_this_firewall_unmarked` | 0 |
| L3 | Named query `live_anonymous_north_south` | About 0 |
| L4 | `client` rows at a firewall address | 0 |
| L5 | Attribution rate on eligible flows | About unchanged (the next cycle raises it) |
| L7 | Eligible flows with exactly one candidate domain in the window and no attribution | 0 |
| L8 | Attributions whose lookup was ingested after the flow | Above 0 |
| L9 | `client_resolution` counts for host-name queriers, then the diagnostic of item 3.1 | `unknown_hostname` down; the remainder in "no lease" |
| L10 | Gateway availability detail and gateway samples | Samples per monitored gateway, or a named cause |
| L11 | Named query `live_upstream_firewall_source_outcomes` | Every paired leg's partner exists |
| L12 | Named query `live_paired_counted_twice` | 0 |
| L13 | Collection gaps by reason | Informational |

The validation passes when:

- L2, L4, L7 and L12 are 0;
- L3 is about 0;
- L10 shows samples or a named cause;
- the maintainer accepts L5 and L9.

## Out of scope

- Interface work.
- 5B.
- Reprocessing the step-4 dumps.
- The resolver cache and local data, which are the next cycle.
- Suricata and step 6.
- Any change on the firewall.
- The filter-log page size and the `digest_outside_returned_window` gap.
