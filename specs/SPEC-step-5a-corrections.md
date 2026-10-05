# SPEC — Step 5A corrections: the remaining defects and the accepted proposals

Status: APPROVED
Cycle kind: standard

The cycle of `specs/SPEC-correlation-classification-aggregation.md` ended
after three verify iterations with one major defect left. On 4 October 2026
the maintainer chose to fix it in a new cycle, together with proposals made by
the builder and the verifiers during that cycle, and with one requirement of
his own: the per-interface on-link ranges (item 6).

The work applies to the **uncommitted working tree** left by that cycle. Every
acceptance criterion of the 5A spec, with AC20 as amended on 4 October 2026,
must still hold at the end of this cycle.

Already done outside this cycle: `CLAUDE.md` and the five agent definitions
now say "three outbound calls", the same as `ROADMAP.md`.

## Scope

### Aggregation correctness

1. **Retention shorter than an hour no longer empties the aggregates.** This is
   the major defect left by the 5A cycle.
   - `RefreshAggregatesAt` skips any hour whose start is before
     `now − retention_seconds`, the current hour included.
   - The days, weeks and months composed from those hours then hold 0, while
     `flow` holds the bytes.
   - Fix: **the current slot of every period is always refreshed**, whatever
     the retention. A slot counts as straddling the horizon only when flows
     inside it can actually have been purged.
2. **An hour that straddles the horizon no longer freezes.** Today a flow
   ingested late into that hour is never counted, and a forced reclassification
   leaves the hour's old classification in place.
   - The hour keeps what it already holds for the flows that were purged.
   - The flows still present are added, or recomputed.
   - The method is the Builder's choice; the result is what AC2 checks.
3. **The current week and month are rolled up.** Their summable figures are
   summed from the day slots instead of being reread from `flow`. A test
   proves the roll-up equals a direct computation over `flow`.
4. **`distinct_peers` is exact in every slot.** Today a composed slot carries
   the largest value of its finer slots, which is only a lower bound.
   - Add the family keyed by peer that this needs. Its key, retention and
     purge follow the other families.
   - Every `distinct_peers` figure, in a composed slot or a rolled-up slot, is
     recounted exactly from it.
   - Remove the "lower bound" statement from `docs/data-model.md`, rule 6.
   - Plan checks cover the new statements at both seed sizes.

### Classification and derivation

5. **Incremental `Reclassify`.** A pass reclassifies only flows with an
   unplaced end, plus a full pass for an address whose membership evidence
   has changed since its last classification. The record of that evidence,
   for example a fingerprint, is stored. Test: with nothing changed, a pass
   touches no row.
6. **Per-interface on-link ranges, set by the operator** (the maintainer's
   requirement). This cycle builds the backend; the editing screen is a later
   interface cycle.
   - **What is stored per interface:**
     - its on-link address ranges, each one either **detected** or **set by
       the operator**;
     - a rule saying whether link-local addresses (`fe80::/10`) count as
       membership evidence on that interface. The default is **no**.
   - **Detected ranges.** They come from `addr4`/`addr6` and their prefixes,
     plus `ipv4[]`/`ipv6[]` (item 11), as read from `interfaces_info`.
     Link-local prefixes are never proposed as detected ranges, because they
     are the same on every interface.
   - **Which ranges count** as membership evidence in classification (B2 of
     the 5A spec) follows the decision recorded under *Decisions*.
   - **Operator edits.** The store gets a write path to add, remove and
     confirm ranges and to set the link-local rule, validated so that a
     malformed range is refused and a range already owned by another
     interface is reported. Every edit forces the affected flows to be
     reclassified and their slots recomputed.
   - **No interface name or address appears as a literal**, and no range is a
     default in code.
   - **Vocabulary.** Look up OPNsense's term for an interface's network (for
     example the interface's subnet or network, on `docs.opnsense.org`) and
     use it. If OPNsense has none, choose a term and record it in
     *Vocabulary* with the reason.
7. **A pass that fails part-way still places what it stored.** When a
   firewall-log pass, or any pass that hands addresses to `derive`, returns an
   error after storing rows, `derive` still runs on the addresses stored so
   far, and the error is still returned.
8. **A hostname logged as the client.** When Unbound logs a hostname in a
   lookup's `client` field, it is resolved to an address through the DHCP
   leases valid at the lookup's instant before attribution.
   - A hostname that maps to more than one address at that instant, or to
     none, stays unresolved and attributes nothing.
   - The resolution is recorded, so that it can be told apart from a lookup
     that logged an address.
9. **Step-2 queries use the three-way split.** `sql/queries/screens.sql` and
   the `blocked_event` view, with its partial index, adopt the 5A rule:
   - allowed is `pass`;
   - blocked is `block` or `reject`;
   - unknown is counted as neither.

### Checks and documents

10. **Purge checks cover every family.** The AC31 purge assertions in
    `sql/schema-checks.sh` cover the client, domain, rule and peer families,
    not only volume and owner.
11. **G13 closed.** `ipv4[]` and `ipv6[]` are stored in `interface_address`
    under their own `source_field` values. If nothing else in G13 remains open,
    G13 moves to *Gaps closed* in `docs/widget-catalogue.md`.
12. **Live-validation checklist.** `ROADMAP.md`, step 5 *Validation*, lists
    what this environment cannot prove and must be probed against the live
    firewall:
    - the gateway-status field names and the `~` placeholder;
    - the `systemSwap` response;
    - the time span a `traffic/top` sample covers;
    - whether a tunnel carrying `gateways[]` is classified as upstream;
    - whether `iftop` reports an inter-interface client on both interfaces,
      counting its rate twice;
    - the FreeBSD release wording against the `releng` link cited in the
      survey;
    - how often Unbound logs a hostname as the client.

## Decisions

- **D1 — which on-link ranges count before the operator acts** (decided on 4
  October 2026): detected ranges count as membership evidence until the
  operator overrides them. An operator-set range always wins over a detected
  one, and a detected range the operator removed no longer counts.

## Acceptance criteria

All criteria run through `docker compose run --rm checks`,
`docker compose run --rm schema-checks` and
`docker compose run --rm dev go test -race -count=1 ./...`, and
`shellcheck -S error sql/schema-checks.sh` passes on the host. Each new test is
shown to fail on the code as it stood before its fix, and the report says so.

- [ ] AC1 — Retention below one hour:
  - with `retention_seconds` at 600 and `now` 800 s into its hour, a 77-byte
    flow at `now − 100` appears in the current 1h, 24h, 7d and 30d slots of
    every family;
  - with retention set to 0, 1, 599, 3599 and 3600, the current slots equal
    the sums over `flow`.
- [ ] AC2 — Straddling hour:
  - a flow ingested late into the hour that holds the horizon is counted,
    and the slot equals its purged part plus the flows still present;
  - a forced reclassification of a flow still present in that hour changes
    the hour's figures accordingly.
- [ ] AC3 — The rolled-up current week and month equal a direct computation
  over `flow` in every family, including the allowed, blocked and unknown
  split.
- [ ] AC4 — `distinct_peers` equals a direct distinct count:
  - in a composed slot and in a rolled-up slot;
  - with one peer active in two child slots, counted once;
  - with no lower-bound wording left in `docs/data-model.md`.
- [ ] AC5 — A second `Reclassify` pass with no evidence change writes no row.
  A lease that newly names one address reclassifies that address's flows and
  no other address's. AC10 and AC11 of the 5A spec still hold.
- [ ] AC6 — On-link ranges:
  - detected ranges are proposed from `addr4`/`addr6` and `ipv4[]`/`ipv6[]`,
    and never include `fe80::/10`;
  - classification follows D1;
  - an operator range added, removed or confirmed through the store's write
    path changes classification at the next derivation, and the affected
    slots equal a recomputation;
  - with the link-local rule at its default, a `fe80::` address is not
    membership evidence on any interface, and setting the rule on one
    interface makes it evidence on that interface only;
  - a malformed range is refused, and a range overlapping another interface's
    range is reported;
  - the literal checks pass over every new file.
- [ ] AC7 — A firewall-log pass that fails after storing rows leaves those
  rows classified (placed ends, correct `traffic_scope`), and still returns its
  error.
- [ ] AC8 — Hostname as client:
  - a lookup whose client is a hostname with exactly one valid lease
    attributes like an address lookup;
  - an ambiguous or unknown hostname attributes nothing;
  - the resolution is distinguishable in storage.
- [ ] AC9 — The step-2 screen queries and `blocked_event`:
  - count `reject` as blocked;
  - count no `unknown` as allowed;
  - their plans scan no growing table at either seed size.
- [ ] AC10 — The schema-checks purge assertions cover the client, domain,
  rule and peer families.
- [ ] AC11 — `ipv4[]` and `ipv6[]` are stored with their own `source_field`.
  G13's status in the catalogue matches what remains, and the
  `MISSING FROM MODEL:` marker count equals the number of open rows.
- [ ] AC12 — `ROADMAP.md`, step 5, carries the live-validation checklist of
  item 12.
- [ ] AC13 — Every acceptance criterion of
  `specs/SPEC-correlation-classification-aggregation.md`, with AC20 as
  amended, still passes. No test is weakened to make a criterion pass.

## Out of scope

- The screen for editing on-link ranges, the link-local rule, the two 5A
  settings, and the Public Suffix List state: these belong to a later
  `interface` cycle.
- The widget HTTP API (5B).
