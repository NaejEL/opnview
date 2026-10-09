# SPEC — A one-minute test suite, and two defects found live

Status: APPROVED
Cycle kind: standard

## Context

### The test suite takes almost six minutes

On 7 October 2026 the maintainer set the budget for the test suite:

- the everyday suite, which builders and verifiers run on every iteration,
  finishes in **under one minute**;
- the `-race` run and the scale checks (the 100,000- and 1,000,000-row seeds,
  and the query plans) run **only before a deployment**, as a separate
  command.

Measured on 7 October 2026 on the uncommitted tree (`go test -count=1 -json
./...`, without `-race`, in the dev container), the suite took **341 s of wall
time**.

Package time:

| Package | Time |
|---|---|
| `internal/store` | 338 s |
| `internal/collect` | 216 s |
| `internal/web` | 136 s |
| `internal/maxmind` | 18 s |
| every other package | under 2 s |

The slowest tests:

| Test | Package | Time |
|---|---|---|
| `TestEverySlotOfEveryFamilyEqualsTheSameSumsOverFlow` | store | 44.7 s |
| `TestAttributionFollowsTheRuleExactly` | store | 25.7 s |
| `TestNoUserVisibleStringIsALiteral` | web | 24.8 s |
| `TestNothingIsLoadedFromOffHost` | web | 22.5 s |
| `TestThePassesAndThePurgeRunConcurrentlyAndEverySlotHoldsEveryFlowOnce` | collect | 14.7 s |
| `TestAnOutLegIngestedBeforeItsInLegIsPairedAtTheSecondPass` | store | 13.0 s |
| `TestThereIsNoExplanatoryCopyInTheInterface` | web | 11.8 s |
| `TestTheCurrentSlotsEqualTheSumsOverFlowWhateverTheRetention` | store | 10.9 s |
| `TestEachGatewayAbsenceIsNamedOnItsOwnAndWritesNoReading` | collect | 10.7 s |

They are followed by a long tail between 4 and 9 s.

Under `-race`, `internal/store` alone took about 1,100 s.

### Defect L1 — host-name retry starved

This was left open by `specs/SPEC-step-5a-live-corrections.md`, iteration 3.

- `collectLeases` reports a complete read only when every active backend was
  read.
- A backend that answers `ErrUnsupportedRead`, or that fails on every pass,
  counts as unread.
- So no lookup is ever re-examined, which breaks item 3.3 and AC14 of that
  spec. A verifier's probe reproduced it: dnsmasq plus ISC active, five lease
  passes, the lookup stays `unknown_hostname`.
- `docs/data-model.md` says such lookups "wait for the next complete pass", as
  if one will come.

### Defect L2 — clients minted at this firewall's own addresses from the neighbour tables

This was found live on 7 October 2026, after the deployment of the live
corrections.

- Three `client` rows of identity kind `mac` sit at addresses the firewall
  itself holds: two IPv4 addresses on inside interfaces, and one IPv6.
- No flow, lease or lookup references them.
- They come from the ARP/NDP neighbour tables (`get_arp` / `get_ndp`), which
  list the firewall's own interface entries.
- `specs/SPEC-step-5a-live-corrections.md` says an end at this firewall's
  address is never a client. The neighbour-table path to `client` does not
  apply that rule.

## Scope

### 1. The everyday suite under one minute

1. **Measure before changing anything.** Use the same command as the Context
   measurement. Report the slowest tests and the cause of each: fixture
   construction, repeated sizes, sequential execution, re-rendering, the
   database opened per test, and so on.
2. **Reduce wall time without reducing what is proven.**
   - **Fixtures.** Build them once and share them where a test only reads.
     Prove a sum on the smallest fixture that exercises every family, period,
     IP version and scope.
   - **Parallelism.** Use `t.Parallel` where tests are independent.
   - **Web.** Render the pages and scan the source once per package.
   - **Sizes.** Keep two network sizes only where a property depends on size;
     the rule of "parametrised counts" stays satisfied with two small sizes.
   - **What may not change.** No assertion is removed. A test may only be
     merged, made faster, or moved to the pre-deployment command (item 3) with
     its reason stated.
3. **Two commands, both documented in `ROADMAP.md`, *Development and test
   environment*, and in `CLAUDE.md`'s command line:**
   - **`docker compose run --rm checks`, the everyday suite:**
     - `gofmt`, `go vet`, `go build`, and `go test` without `-race`;
     - it runs every test except those moved to the pre-deployment command.
   - **A pre-deployment command** (a new compose service, named in the docs),
     which runs:
     - `go test -race -count=1` over the whole suite;
     - the tests moved out of the everyday suite;
     - `schema-checks`, the seeds and the plans.
   - **How a test is moved out.** By a build tag or an environment switch read
     by the test, never by deleting it. Each moved test states why in a
     comment.
4. **The budget is enforced.**
   - `checks` measures its own `go test` wall time and fails above 60 s, naming
     the budget.
   - The limit is one constant in the script, not scattered across files.
   - The dev container's CPU count is reported beside the time, so a slower
     machine is identifiable.
5. **What the factory runs.** In `.claude/agents/factory-builder.md`,
   `factory-verifier.md` and `factory-ui-builder.md`:
   - the per-iteration command is `checks`;
   - the pre-deployment command is named as the gate run before a deployment;
   - the change is one paragraph per file, nothing more.

   The maintainer authorises these agent-file edits in approving this spec.

### 2. Defect L1

- A backend that cannot be read by design (`ErrUnsupportedRead`) contributes
  no leases. It does not make a pass incomplete.
- A backend that fails is reported in `source_availability`, as today.
- A pass that read every readable backend counts as complete.
- A backend that fails on every pass must not starve the retry forever. When
  a backend has failed on every pass for a bounded number of passes, a
  setting row whose default is the Builder's to choose and justify, a pass
  that read every other backend counts as complete. The limit is documented
  in `docs/data-model.md` and in the availability detail.

### 3. Defect L2

- A neighbour-table (ARP/NDP) entry whose address this firewall holds at that
  instant, under the recognition rule of the live-corrections spec, creates or
  updates no `client`.
- The clients already minted that way are purged when nothing references
  them, by the existing purge path.

## Acceptance criteria

- [ ] AC1 — **Everyday suite.** `docker compose run --rm checks` exits 0, and
  its `go test` wall time is under 60 s on the dev container. The report shows
  the time three runs in a row, and the CPU count.
- [ ] AC2 — **Budget enforced.** Raising the measured time over the limit (for
  example a test-only sleep, behind an environment switch used only by the
  budget's own test) makes `checks` fail with a message that names the
  budget.
- [ ] AC3 — **Pre-deployment command.** It exits 0 and runs `-race` over every
  package, every moved test, and `schema-checks`. Its wall time is reported.
- [ ] AC4 — **Nothing lost.**
  - The set of test names is the same as before, or every missing name is
    shown merged into a named test that keeps its assertions.
  - Every test moved out of `checks` is listed with its reason.
  - `grep` shows no `t.Skip` added outside the move mechanism.
- [ ] AC5 — **L1.**
  - A test with one readable backend and one `ErrUnsupportedRead` backend
    resolves a pending host-name lookup on the first pass.
  - A test with one backend failing on every pass resolves it after the
    bounded number of passes, and not before.
  - Both tests fail on the current code.
- [ ] AC6 — **L2.**
  - A neighbour entry at an address this firewall holds creates no client.
  - A database holding such clients, with nothing referencing them, has them
    purged at the next full placement.
  - Both tests fail on the current code.
- [ ] AC7 — **Documents.** `ROADMAP.md`, `CLAUDE.md` and the three agent files
  name the two commands consistently. `docs/data-model.md` states the L1
  limit.
- [ ] AC8 — **The live-corrections spec still holds.** Every criterion of
  `specs/SPEC-step-5a-live-corrections.md` still passes, and so does every
  criterion of the three 5A specs it carries.

## Live validation

After deployment, read-only queries on the live database show:

- 0 `client` rows at a firewall address;
- the `unknown_hostname` count no higher than before.

Nothing is committed before the maintainer accepts it.

## Rules

- **Language and vocabulary.** Everything is written in English, using
  OPNsense and project vocabulary.
- **Deletion.** Never delete with a wildcard or glob. Delete only an exact
  path, after showing it. Never run a destructive command in the background.
- **Git.** No git command that moves or discards work.
- **Committing.** No commit before live validation.
- **The live database.** Never touch the `data` volume's `opnview.db*` files;
  a live service runs on them.
