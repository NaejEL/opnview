#!/usr/bin/env bash
# The opnview pre-deployment command: the gate run before a deployment, and not
# on every iteration. Meant to run inside the development container, never on
# the host:
#
#     docker compose run --rm pre-deployment
#
# What it runs, every step even after one fails, each exit code reported:
#
#   1. go vet with the `predeployment` build tag, so the tests moved out of the
#      everyday suite are vetted too;
#   2. go test -race -count=1 over the whole suite, with the same tag: every
#      package under the race detector, and every moved test;
#   3. ci/budget-self-test.sh, the test budget's own check;
#   4. sql/schema-checks.sh: the schema, the seeds at 100 000 and 1 000 000 flow
#      rows, the screen queries and their query plans;
#   5. the wall time of ReclassifyEverything at the 1 000 000-row seed, under the
#      `reclassifycost` build tag, printed and recorded in docs/data-model.md.
#
# The everyday suite is `docker compose run --rm checks`. A test moved out of it
# carries the `predeployment` build tag and says why in a comment beside it; the
# list is in ROADMAP.md, "Development and test environment".
#
# Unlike checks, this command runs with /tmp on the container's overlay
# filesystem, so the whole suite still meets a real disk once before a
# deployment.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT" || exit 1

failed=0
started="$(date +%s)"

report() {
  printf -- '-- %s: exit %s\n' "$1" "$2"
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

printf -- '== go vet -tags predeployment ./...\n'
go vet -tags predeployment ./...
report 'go vet -tags predeployment ./...' "$?"

printf -- '== go test -race -count=1 -tags predeployment ./...\n'
go test -race -count=1 -timeout 60m -tags predeployment ./...
report 'go test -race -count=1 -tags predeployment ./...' "$?"

printf -- '== ci/budget-self-test.sh\n'
bash "$REPO_ROOT/ci/budget-self-test.sh"
report 'ci/budget-self-test.sh' "$?"

printf -- '== sql/schema-checks.sh\n'
bash "$REPO_ROOT/sql/schema-checks.sh"
report 'sql/schema-checks.sh' "$?"

# The cost of ReclassifyEverything at the million-row seed (B8h of
# specs/SPEC-step-5b-widget-api.md): measured and printed, with no budget (decision
# 17). Its own build tag rather than `predeployment`, so it runs without -race: a
# race-instrumented run would measure the race detector.
printf -- '== go test -tags reclassifycost (ReclassifyEverything at 1 000 000 rows)\n'
go test -count=1 -timeout 60m -tags reclassifycost -v \
    -run '^TestReclassifyEverythingWallTimeAtTheMillionRowSeed$' ./internal/collect/ |
    grep -E '^(reclassify-cost:|--- |ok|FAIL)'
report 'go test -tags reclassifycost' "${PIPESTATUS[0]}"

elapsed=$(( $(date +%s) - started ))
printf -- '-- pre-deployment wall time: %d s on %s CPUs\n' "$elapsed" "$(nproc)"

if [ "$failed" -ne 0 ]; then
  printf -- 'pre-deployment: FAILED\n' >&2
  exit 1
fi
printf -- 'pre-deployment: OK\n'
