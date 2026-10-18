# 0012 — One test command, under a minute, and no other gate

Status: Accepted
Decided by: the maintainer, 7 October 2026 (the one-minute budget), 16 and 18 October 2026 ("it is not normal to retest everything every time a line changes"), and the reset of 18 October 2026

## Context

By 18 October 2026 the repository had three PostgreSQL instances for tests, a
409-line pre-deployment script, schema checks with 100 000- and 1 000 000-row
seeds rebuilt cold in 25 minutes, widget budgets, broker budgets, an answer-drift
check, a race selection tool, a test-template cleaner, a timing breakdown tool
and a self-test of the budget itself: fourteen CI tools, 69 000 lines of Go
tests. Agents re-ran all of it after every change. Three days went into the test
machinery and not the product. The maintainer had asked for the opposite dozens
of times.

## Decision

- **There is exactly one test command**: `docker compose run --rm checks`. It
  runs gofmt, go vet, go build and `go test ./...` with a hard 60 s timeout, on
  one test database. The plugins' JavaScript tests run inside it.
- **There is no other gate.** No pre-deployment suite, no schema checks, no
  seeds, no budgets, no drift check, no race run. Adding one requires a
  maintainer decision recorded as an ADR.
- **While working, test only what you changed**: the package, plugin or widget
  touched, through `go test` on that package or `opnview plugin test
  plugins/<name>`. For a performance change, measure only the thing being
  optimised.
- **Run the one command exactly once, before the commit.**
- A suite that exceeds the timeout is a defect to fix by removing or
  simplifying tests, never a budget to raise or a reason to add tooling.
- **A test proves a behaviour a plugin author or an operator depends on.** A
  test of test infrastructure, of a budget, of a line count or of a document's
  wording is not written.
- Tests of code destined to be rewritten (0017) were deleted on 18 October 2026,
  not kept "until the rewrite": a test of code that will not survive is a cost
  with no return.

## Consequences

- `ci/` holds `checks.sh`, the `credentialscan` test (no published database
  port, no password in a tracked file: a hard rule of 0008) and the
  `go generate` tool `discover`. Nothing else.
- The development image keeps gcc only because `go test -race` may be run by
  hand in the dev container when a race is suspected; it is nobody's gate.
- Validation of a change is on the maintainer's firewall (0013), not in a
  bigger suite.
