#!/usr/bin/env bash
# The opnview pre-deployment command: the gate run before a deployment, and not
# on every iteration. Meant to run inside the development container, never on
# the host:
#
#     docker compose run --rm pre-deployment
#
# What it runs, every step even after one fails, each exit code reported:
#
#   1. go vet with the `predeployment`, `reclassifycost` and `brokerbudget` build
#      tags, so the tests moved out of the everyday suite are vetted too;
#   2. go test -race -count=1 over the whole suite, with the `predeployment` tag:
#      every package under the race detector, and every moved test;
#   3. the broker budgets of BR-AC17 (the `brokerbudget` tag, without the race
#      detector), each median printed beside its limit;
#   4. ci/budget-self-test.sh, the test budget's own check;
#   4b. the push decoders' fuzz targets (PS15 of the push amendment): the syslog
#      header, the TCP framing and the NetFlow / IPFIX decoder, each fuzzed for
#      PUSH_FUZZ_SECONDS; their seed corpora already run in the everyday suite;
#   5. sql/schema-checks.sh: the schema, the seeds at 100 000 and 1 000 000 flow
#      rows, the screen queries and their query plans, the widget and data
#      catalogue response budgets, the wall time of ReclassifyEverything on the
#      1 000 000-row seed, printed there and recorded in docs/data-model.md, and
#      the backup round trip (pg_dump, the documented TimescaleDB restore, the row
#      counts compared), all on the PostgreSQL instance db-predeployment.
#
# The command's own wall time has a budget, PRE_DEPLOYMENT_BUDGET_SECONDS below, and
# exceeding it fails the run.
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

# The data directory the schema checks write under is refused before any step runs
# when it is relative or shows Git-Bash path conversion; ci/data-dir-guard.sh says
# why. A refusal here costs nothing, where the same mistake found after the race run
# costs the whole command.
# shellcheck source=ci/data-dir-guard.sh
. "$REPO_ROOT/ci/data-dir-guard.sh"
if ! check_data_dir "${OPNVIEW_DATA_DIR:-/data}"; then
  printf -- 'pre-deployment: FAILED before any step\n' >&2
  exit 1
fi

# THE PRE-DEPLOYMENT BUDGET, in seconds: the one place it is written (Phase E and P-AC20 of
# specs/SPEC-backend-completion-plugins.md). The whole command's wall time is measured,
# printed beside the CPU count, and a run over it fails, as ci/checks.sh does for the
# everyday suite's TEST_BUDGET_SECONDS.
PRE_DEPLOYMENT_BUDGET_SECONDS=180

failed=0
started="$(date +%s%N)"

report() {
  printf -- '-- %s: exit %s\n' "$1" "$2"
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

printf -- '== go vet -tags predeployment,reclassifycost,brokerbudget ./...\n'
go vet -tags predeployment,reclassifycost,brokerbudget ./...
report 'go vet -tags predeployment,reclassifycost,brokerbudget ./...' "$?"

printf -- '== go test -race -count=1 -tags predeployment ./...\n'
go test -race -count=1 -timeout 60m -tags predeployment ./...
report 'go test -race -count=1 -tags predeployment ./...' "$?"

# THE BROKER BUDGETS of BR-AC17, measured without the race detector, which would measure itself:
# each median printed beside its limit.
printf -- '== go test -count=1 -tags brokerbudget -run Budget ./internal/broker/ ./internal/plugin/\n'
go test -count=1 -tags brokerbudget -run Budget -v ./internal/broker/ ./internal/plugin/ | grep -E '^(budget:|--- FAIL|FAIL|ok)'
report 'broker budgets' "${PIPESTATUS[0]}"

# The push decoders' fuzz targets, PUSH_FUZZ_SECONDS each: 30 s of the 45 s the push measures may
# add to this command (PS15 of the push amendment).
PUSH_FUZZ_SECONDS=10
for target in "FuzzHeader ./internal/push/protocols/syslog" "FuzzFraming ./internal/push/protocols/syslog" \
  "FuzzDecoder ./internal/push/protocols/netflow"; do
  name="${target%% *}"
  package="${target#* }"
  printf -- '== go test -fuzz %s -fuzztime %ss %s\n' "$name" "$PUSH_FUZZ_SECONDS" "$package"
  go test -run '^$' -fuzz "^${name}\$" -fuzztime "${PUSH_FUZZ_SECONDS}s" "$package"
  report "fuzz $name" "$?"
done

printf -- '== ci/budget-self-test.sh\n'
bash "$REPO_ROOT/ci/budget-self-test.sh"
report 'ci/budget-self-test.sh' "$?"

printf -- '== sql/schema-checks.sh\n'
bash "$REPO_ROOT/sql/schema-checks.sh"
report 'sql/schema-checks.sh' "$?"

elapsed_ms=$(( ( $(date +%s%N) - started ) / 1000000 ))
cpus="$(nproc)"
printf -- '-- pre-deployment wall time: %d.%03d s on %s CPUs (pre-deployment budget: %d s)\n' \
  $(( elapsed_ms / 1000 )) $(( elapsed_ms % 1000 )) "$cpus" "$PRE_DEPLOYMENT_BUDGET_SECONDS"
if [ "$elapsed_ms" -gt $(( PRE_DEPLOYMENT_BUDGET_SECONDS * 1000 )) ]; then
  printf -- 'pre-deployment: took %d.%03d s on %s CPUs, over the pre-deployment budget of %d s.\n' \
    $(( elapsed_ms / 1000 )) $(( elapsed_ms % 1000 )) "$cpus" "$PRE_DEPLOYMENT_BUDGET_SECONDS" >&2
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  printf -- 'pre-deployment: FAILED\n' >&2
  exit 1
fi
printf -- 'pre-deployment: OK\n'
