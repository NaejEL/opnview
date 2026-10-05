# SPEC — Serialise the retention purge with the derivation

Status: APPROVED
Cycle kind: standard

The cycle of `specs/SPEC-step-5a-corrections.md` ended after three verify
iterations with one major defect left, and the maintainer chose this
mini-cycle on 4 October 2026 to fix its cause.

## The defect

The retention purge runs in its own scheduler loop, with nothing that
orders it against the derivation (`internal/collect/derive.go`, `deriveMutex`).
Each corrective iteration closed one interleaving of the two, and the next
verifier found another.

**The one left open.** A purge that commits between a pass storing its rows
and that pass's aggregate refresh moves those rows into `purged_flow_hour`.
After that, no refresh ever selects their hour, because `dirty_hours` reads
only `flow` and forced instants come only from flows that still exist. The
hour, and the current day, week and month built from it, lose those bytes for
good.

Verifier's probe: retention 600 s, a 77-byte flow at `now − 1000`, insert,
purge, refresh. Result: purged part 77, hour slot 0, current day slot 0.

**A related case.** A purge that runs before the first derivation places a row
freezes that row in its purged part as unplaced and north-south.

## Scope

1. **Serialise the purge with the derivation.** The retention purge, its
   writes to `purged_flow_hour` and `retention_purge` included, never runs
   while a pass is between storing its rows and the end of its derivation, and
   a pass's store-and-derive never runs during a purge.
   - The mechanism is the Builder's choice: the existing `deriveMutex` held by
     the purge, or an equivalent ordering.
   - It must not deadlock with `beginIngest`/`endIngest`, nor with the
     lease, resolver and security-event passes.
2. **Belt and braces.** The hours a purge writes to `purged_flow_hour` are
   rewritten by the next refresh, so the slot invariant holds even if the
   ordering is ever broken.
3. **Documents.** `docs/data-model.md`: the purge section and the refresh
   contract state the ordering.

## Acceptance criteria

All criteria run through `docker compose run --rm checks`,
`docker compose run --rm schema-checks` and
`docker compose run --rm dev go test -race -count=1 -timeout 30m ./...`, and
`shellcheck -S error sql/schema-checks.sh` passes on the host. Each new test
is shown to fail on the code as it stands now, and the report says so.

- [ ] AC1 — A deterministic test runs a purge that is requested between a
  filter-log pass's store and its refresh. Every hour, day, week and month
  slot of every family then equals its purged part plus the flows present,
  which is the verifier's probe at store and at collector level.
- [ ] AC2 — A purge requested before the first derivation does not leave any
  purged row unplaced when its address has membership evidence.
- [ ] AC3 — Under `-race`, a filter-log loop, a lease loop, a resolver loop
  and a purge loop run concurrently for at least 30 iterations each at a
  retention below one hour. Afterwards every slot of every family equals the
  sums over every flow ever ingested, there is no deadlock (the test finishes
  within a bound), and nothing is counted twice.
- [ ] AC4 — With the ordering disabled in a test hook, item 2 alone still
  makes AC1's slots correct at the next refresh.
- [ ] AC5 — Every acceptance criterion of
  `specs/SPEC-correlation-classification-aggregation.md` and of
  `specs/SPEC-step-5a-corrections.md` still passes. No test is weakened.

## Out of scope

Everything else, including the proposals still awaiting the maintainer's
decision: gap detection after a purge, the race time budget, and the note on
the retention watermark.
