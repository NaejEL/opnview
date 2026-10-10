# SPEC — Resolver-cache follow-ups: one CNAME rule, and three test gaps

Status: APPROVED
Cycle kind: standard

## Context

Agents proposed these points during `specs/SPEC-resolver-cache-attribution.md`
and `specs/SPEC-resolver-cache-closing.md`. The maintainer accepted all four on
10 October 2026 and asked that cycles carry as much as possible. The
uncommitted working tree on top of `8a9e870` holds both cycles; this spec adds
to it.

## Scope

### 1. One rule for a CNAME chain into local data

Today three parts of the code disagree on one case, a cached CNAME chain whose
last name is held by a local-data A/AAAA record:

- `exactCandidates` (`internal/store/attribute.go`) never follows a CNAME to a
  local-data record;
- `answerAddresses` (the contradiction check, `internal/store/records.go`) does
  follow it;
- `live_timing_fallback_other_family_cached` (`sql/queries/diagnostics.sql`)
  does follow it.

**Decision: neither the exact search nor the contradiction check nor the
diagnostic follows a CNAME into local data.**

Why: Unbound answers a local-data name from local data, not by following a
cached CNAME. A lookup is `Local-data` only when the looked-up name itself is a
local-data owner. This keeps decision 6 literal: local-data evidence counts
only for a `Local-data` lookup of that exact name. The Builder checks this
against Unbound's documented local-zone behaviour and reports if it is wrong,
rather than implementing against the source.

All three parts read the same rule. Tests cover the chain case for:

- the exact method: no `resolver_cache_answer` through the chain;
- the contradiction: the chain does not suppress the timing rule;
- the diagnostic: the chain is not counted as evidence.

### 2. The diagnostic's both-families case

Add a test that plants a `lookup_timing` flow whose named domain had evidence
in both families at the flow's instant. It must count 0. Dropping the query's
`same_family = 0` condition must fail it; show this in a scratch copy.

### 3. Stale collector count in a test name

`TestDiscoveryAndAllFiveCollectorsReachTheFirewallAndNothingElse`
(`internal/collect/outbound_test.go`) and the "other four" in the
`scheduler_test.go` preamble state a collector count. Rename and reword them so
that no test name or comment states a collector count.

### 4. Guard: planted inconsistent rows stay out of the live window

The seed plants rows that the rule never produces: flow 2000000001, flow
2000000002 and their records, for RC-AC14. Add a `schema-checks` assertion
that no planted row lies inside the live-validation window
(`WINDOW_START`…NOW). A failing query must fail the check.

### 5. Message-cache references are named for what they are

Found live on 10 October 2026, after deployment. The availability detail of
the cache read said: "skipped 3308 whose ttl is not a whole number of
seconds". That was a third of the A/AAAA/CNAME rows.

A read-only probe of the live dump showed what those rows are:

- **They are not records.** They are the message-cache section of
  `unbound-control dump_cache`. `dump_msg_ref` in Unbound's
  `daemon/cachedump.c` prints `name class type flags`, which carries no TTL
  and no rdata.
- **wrapper.py lets them through.** At 26.7.3 its regex accepts these lines
  with the optional TTL group absent. So `ttl` is `null`, and `value` is the
  flags, `"0"`.
- **The live profile.** Every null-ttl row comes after every integer-ttl row.
  Every null-ttl A/AAAA/CNAME row has `value` `"0"`. Every (host, rrtype) key
  of a null-ttl row also has integer-ttl rows elsewhere in the dump.

No evidence is lost by skipping these rows. The defect is the wording, and
the claim in the code comments and the survey that these are records without
a TTL.

The rule:

- **A cache row whose `ttl` is JSON null** is a message-cache reference. It is
  skipped, and the detail counts it under its own wording, naming the
  message-cache section. It is not counted as a bad TTL.
  - To tell null from the empty string, keep the difference when decoding.
    Do not change `decode.RawString` for the callers that rely on it today.
- **A string of digits** is the seconds left at the dump's instant; `0` is
  valid. This is unchanged.
- **Anything else** stays the bad-TTL anomaly: a non-digit string, a JSON
  number, or a negative value.

What also changes:

- **The fixture.** `internal/collect/testdata/unbound_dumpcache.json` gains
  message-cache reference rows of the live shape, with `ttl` null and
  `value` `"0"`, after its record rows. The fixture's `nottl` row is replaced
  or relabelled accordingly.
- **The documents.** Survey A2 and the comments in `endpoints.go`,
  `resolvercache_unbound.go` and `resourcerecord.go` say what these rows are,
  citing `daemon/cachedump.c` (`dump_msg_ref`) and wrapper.py 26.7.3 lines
  65–68.

## Acceptance criteria

- [ ] AC1 — The three parts agree on the chain case, with a test for each. The
  agreement is shown by a mutation that makes one part follow the chain again
  and fails its test.
- [ ] AC2 — The both-families test exists. Dropping `same_family = 0` in a
  scratch copy fails it.
- [ ] AC3 — No test name or comment in the repository states a collector or
  loop count.
- [ ] AC4 — The guard exists and passes. Moving a planted row into the window
  in a scratch copy fails it.
- [ ] AC4b — A dump holding message-cache references reports them under their
  own wording. None of them is stored, none is counted as a bad TTL, and a
  non-digit or negative ttl is still counted as one. The test fails on the
  current wording.
- [ ] AC5 — `checks` exits 0 under 60 s, and the CPU count is reported.
  `pre-deployment` exits 0, and its wall time is reported.
- [ ] AC6 — Every criterion of both resolver-cache specs, and of the specs they
  carry, still passes.

## Rules

- **Language and vocabulary.** Everything is written in English, using
  OPNsense and project vocabulary.
- **Deletion.** Delete only exact paths. Never use wildcards or globs, and
  never run a destructive command in the background.
- **Git.** No git command that moves or discards work. No commit.
- **The live database.** Never touch the `data` volume or `opnview.db*`; a
  live validation is running on them.
- **No installation data** in any file.
