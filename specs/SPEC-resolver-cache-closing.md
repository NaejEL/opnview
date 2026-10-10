# SPEC — Close the resolver-cache cycle: decision 6's test and four clean-ups

Status: APPROVED
Cycle kind: standard

## Context

`specs/SPEC-resolver-cache-attribution.md` was built and verified in three
iterations. It did not reach approval. The only blocking finding of the third
verifier:

- Decision 6 (scope C1) has no test. The rule: local-data A/AAAA records are
  exact evidence only for lookups whose `answer_source` is `Local-data`.
- Three mutations of `internal/store/attribute.go` left every test passing,
  under both the everyday tags and `-tags predeployment`:
  - never using local-data evidence in `exactCandidates`;
  - admitting local-data evidence for lookups answered by recursion or from
    the cache;
  - inverting the `withLocalData` flag passed to `answerAddresses` in the
    contradiction check of `decideAttribution`.

The maintainer chose to close the cycle with this mini-cycle, and to include
four clean-ups proposed by the agents. The uncommitted working tree on top of
`8a9e870` holds the resolver-cache implementation. This spec adds to it and
amends it; it does not restart it.

## Scope

### 1. Decision 6 is tested

A store test, in `internal/store/resolvercache_test.go`, with the existing
`cacheScenario` / `pollLocalData` helpers:

- **Local-data lookup.** A lookup answered from `Local-data`, whose name has
  only a local-data A/AAAA record covering the destination, gives
  `resolver_cache_answer` naming that local-data observation.
- **Recursion lookup.** The same lookup answered by `Recursion` (and, as a
  separate case, from the cache) does not use that record. It gives
  `lookup_timing` when the timing rule applies, and no row otherwise.
- **Contradiction.** Local-data answers that exclude the destination:
  - suppress the timing rule for a `Local-data` lookup;
  - do not suppress it for a `Recursion` lookup.

Each of the three mutations above must make at least one of these tests fail.

### 2. The unread `Activity` registry entry is removed

- `opnsense.Activity` is no longer read for any figure. Remove it from the
  registry and its source constant, and remove `activity.json` with the tests
  that serve it, only where nothing else reads it.
- **What still checks that the processor figure is not read from it.** The
  test asserting that `getActivity` is never requested is replaced by an
  assertion that the registry holds no `/api/diagnostics/activity/` path.
- **Amended by name:**
  - the registry-count test, from 35 to 34;
  - `SPEC-resolver-cache-attribution.md` AC3: the diff against `8a9e870` adds
    the three named endpoints and removes `getActivity`.
- **Docs.** The survey keeps its record of what `getActivity` answers, as
  research. It states that `opnview` no longer registers the endpoint. The
  data-model row, and the `widget-catalogue.md` mention, say the same.

### 3. The stale loop-count test name

`TestTheSevenLoopsRunAtTheConfiguredCadences` checks ten loops. Rename it to a
name that carries no count, and fix its comment.

### 4. RC-AC14's keep branch in the shell harness

In `sql/seed.sql` (main, alt and scale), seed one surviving attribution whose
referenced resolver record has a coverage that ended before the purge horizon.
RC-AC14 must then exercise both branches:

- the referenced record is kept;
- an unreferenced record older than the horizon is purged.

Show the keep branch failing when the purge's reference exclusion is removed,
in a scratch copy.

### 5. A live diagnostic for timing fallbacks that only the other family explains

Add `live_timing_fallback_other_family_cached` to
`sql/queries/diagnostics.sql`, with parameters `:window_start` and
`:window_end`. It counts the `lookup_timing` attributions whose named domain
had, at the flow's instant, resolver evidence in the other address family
only. Add a seeded test that shows it counting a planted case.

## Acceptance criteria

- [ ] AC1 — The decision-6 tests exist and pass. Each of the three mutations
  above, applied in a scratch copy, fails at least one of them. The output is
  shown.
- [ ] AC2 — No `/api/diagnostics/activity/` path is in the registry. The count
  test reads 34. Nothing else references `opnsense.Activity`. The docs and the
  resolver-cache spec AC3 are amended by name.
- [ ] AC3 — No test name in the repository states a loop count.
- [ ] AC4 — RC-AC14 checks both the keep branch and the purge branch on every
  seed. The keep branch fails without the reference exclusion; this is shown.
- [ ] AC5 — The new diagnostic exists, is documented beside the other `live_*`
  queries, and counts a planted case in a test.
- [ ] AC6 — `checks` exits 0, under 60 s, and the CPU count is reported.
  `pre-deployment` exits 0, and its wall time is reported.
- [ ] AC7 — Every criterion of `SPEC-resolver-cache-attribution.md` (AC4 as
  corrected on 10 October 2026), and of the specs it carries, still passes.

## Live validation

Run after deployment, as `SPEC-resolver-cache-attribution.md` V1–V10 against
the baseline taken on 9 October 2026, plus the count from the new diagnostic.
Nothing is committed before the maintainer accepts it.

## Rules

- **Language and vocabulary.** Everything is written in English, using
  OPNsense and project vocabulary.
- **Deletion.** Never delete with a wildcard or glob. Delete only an exact
  path. Never run a destructive command in the background.
- **Git.** No git command that moves or discards work. No commit.
- **The live database.** Never touch the `data` volume's `opnview.db*` files.
- **No installation data** in any file.
