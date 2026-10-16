#!/usr/bin/env bash
# The test budget's own check: it proves that ci/checks.sh fails, with a message
# naming the budget, when its go test wall time exceeds the limit.
#
# It reads the limit from checks.sh itself, where it is the one constant, and
# runs checks.sh with OPNVIEW_BUDGET_PROBE_SECONDS one second past it. That
# switch is read by ci/budgetprobe and by nothing else: the probe test sleeps that
# long, so the measured time is over the budget by construction, whatever the
# rest of the suite costs. OPNVIEW_BUDGET_PROBE_ONLY restricts checks.sh's go test
# to that probe package (Q3 of the Phase E amendment to
# specs/SPEC-backend-completion-plugins.md): the check is of the budget's
# enforcement, which the probe alone exercises, and the whole suite already ran
# once in the same command. In that mode checks.sh makes no copy of the tree and
# skips gofmt, go vet and go build -- the pre-deployment command vets and compiles
# the whole tree itself, and gofmt is the everyday suite's -- so
# the run fits checks.sh's hard limit beside the probe's sleep. Run inside the
# development container, as part of
#
#     docker compose run --rm pre-deployment
#
# and never on the host.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKS="$REPO_ROOT/ci/checks.sh"

budget="$(sed -n 's/^TEST_BUDGET_SECONDS=\([0-9][0-9]*\)$/\1/p' "$CHECKS")"
if [ -z "$budget" ]; then
  printf -- 'budget self-test: ci/checks.sh declares no TEST_BUDGET_SECONDS constant\n' >&2
  exit 1
fi
probe=$(( budget + 1 ))
printf -- '== checks.sh with the budget probe sleeping %d s (test budget: %d s)\n' "$probe" "$budget"

output="$(cd "$REPO_ROOT" && OPNVIEW_BUDGET_PROBE_SECONDS="$probe" OPNVIEW_BUDGET_PROBE_ONLY=1 bash "$CHECKS" 2>&1)"
status=$?
printf -- '%s\n' "$output" | tail -n 5

failed=0
if [ "$status" -eq 0 ]; then
  printf -- 'budget self-test: checks.sh exited 0 with the probe past the budget\n' >&2
  failed=1
fi
if ! printf -- '%s\n' "$output" | grep -q "over the test budget of $budget s"; then
  printf -- 'budget self-test: checks.sh did not name the test budget when it was exceeded\n' >&2
  failed=1
fi
if [ "$failed" -ne 0 ]; then
  printf -- 'budget self-test: FAILED\n' >&2
  exit 1
fi
printf -- 'budget self-test: OK\n'
