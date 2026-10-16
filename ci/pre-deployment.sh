#!/usr/bin/env bash
# The opnview pre-deployment command: the gate run before a deployment, and not
# on every iteration. Meant to run inside the development container, never on
# the host:
#
#     docker compose run --rm pre-deployment
#
# What it runs, every step even after one fails, each step's output in a block of
# its own with its exit code and its wall time:
#
#   race selection   ci/raceselect decides, by a stated rule and never a hand list,
#                    which packages the race detector runs on (ROADMAP.md,
#                    "Development and test environment"); every package of ./... is
#                    printed, selected or excluded, with the clause that decided it,
#                    and the two sets together must be exactly go list ./...;
#   go vet           with the `predeployment`, `reclassifycost` and `brokerbudget`
#                    build tags, so the tests out of the everyday suite are vetted too;
#   go test -race    the selected packages, with the `predeployment` tag;
#   go test          the excluded packages, with the `predeployment` tag and no race
#                    detector: every package of ./... runs exactly once;
#   broker budgets   BR-AC17's budgets (the `brokerbudget` tag, without the race
#                    detector, which would measure itself), each median beside its limit;
#   fuzz             the push decoders' three fuzz targets, PUSH_FUZZ_SECONDS each,
#                    side by side; their seed corpora already run in the everyday suite;
#   budget self-test ci/budget-self-test.sh, the test budget's own check, which runs
#                    checks.sh with its go test restricted to the probe package;
#   schema checks    sql/schema-checks.sh: the schema, the seeds at 100 000 and
#                    1 000 000 flow rows, the screen queries and every statement's
#                    plan, the widget and data catalogue budgets, the wall time of
#                    ReclassifyEverything, the backup round trip.
#
# THE STEPS THAT DO NOT DEPEND ON ONE ANOTHER RUN SIDE BY SIDE: go vet, the fuzz
# targets, the budget self-test (most of which is its probe's sleep) and the schema
# checks start in the background while the two go test runs and the broker budgets
# run in the foreground; each background step's output is held and printed, in one
# block, when it ends.
#
# The command's own wall time has a budget, PRE_DEPLOYMENT_BUDGET_SECONDS below, and
# exceeding it fails the run. The wall time of every step is printed at the end,
# beside the total and the CPU count.
#
# The everyday suite is `docker compose run --rm checks`. The tests compiled only
# here are listed in ROADMAP.md, "Development and test environment".
#
# Unlike checks, this command runs with /tmp on the container's overlay
# filesystem, against db-predeployment, a disk-backed PostgreSQL instance with
# production durability, so the whole suite meets a real disk once before a
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

# THE PRE-DEPLOYMENT BUDGET, in seconds: the one place it is written. The whole command's
# wall time is measured, printed beside the CPU count, and a run over it fails, as
# ci/checks.sh does for the everyday suite's TEST_BUDGET_SECONDS.
PRE_DEPLOYMENT_BUDGET_SECONDS=180

# The push decoders' fuzz targets run this long each, side by side.
PUSH_FUZZ_SECONDS=10

failed=0
started="$(date +%s%N)"
STEPS_DIR="$(mktemp -d)"
trap 'rm -rf -- "$STEPS_DIR"' EXIT

# Every step's name, exit code and wall time, in the order they ended, for the table at the end.
step_names=()
step_codes=()
step_milliseconds=()

record() {
  step_names+=("$1")
  step_codes+=("$2")
  step_milliseconds+=("$3")
  printf -- '-- %s: exit %s, %d.%03d s\n' "$1" "$2" $(( $3 / 1000 )) $(( $3 % 1000 ))
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

# run_step <name> <command...> — a step in the foreground, its output as it comes.
run_step() {
  local name="$1" begun status
  shift
  printf -- '== %s\n' "$name"
  begun="$(date +%s%N)"
  "$@"
  status=$?
  record "$name" "$status" $(( ( $(date +%s%N) - begun ) / 1000000 ))
}

# start_step <id> <name> <command...> — a step in the background, its output held until it ends.
declare -A step_pid=() step_name=() step_begun=()
start_step() {
  local id="$1" name="$2"
  shift 2
  step_name[$id]="$name"
  step_begun[$id]="$(date +%s%N)"
  ( "$@" > "$STEPS_DIR/$id.log" 2>&1; echo $? > "$STEPS_DIR/$id.status" ) &
  step_pid[$id]=$!
  printf -- '.. %s started in the background\n' "$name"
}

# wait_step <id> — waits for a background step and prints its block.
wait_step() {
  local id="$1" status
  wait "${step_pid[$id]}"
  status="$(cat "$STEPS_DIR/$id.status" 2> /dev/null || echo 1)"
  printf -- '== %s\n' "${step_name[$id]}"
  cat "$STEPS_DIR/$id.log"
  record "${step_name[$id]}" "$status" $(( ( $(date +%s%N) - ${step_begun[$id]} ) / 1000000 ))
}

# THE RACE SELECTION, first, because the two go test runs follow from it.
printf -- '== race selection (ci/raceselect)\n'
selection_begun="$(date +%s%N)"
selection_status=0
go run ./ci/raceselect > "$STEPS_DIR/selection.txt" || selection_status=$?
cat "$STEPS_DIR/selection.txt"
mapfile -t race_packages < <(awk '$1 == "selected" { print $3 }' "$STEPS_DIR/selection.txt")
mapfile -t plain_packages < <(awk '$1 == "excluded" { print $3 }' "$STEPS_DIR/selection.txt")
# Every package of ./... exactly once: the two sets together are go list's.
go list ./... | sort > "$STEPS_DIR/listed.txt"
printf -- '%s\n' "${race_packages[@]}" "${plain_packages[@]}" | sed '/^$/d' | sort > "$STEPS_DIR/decided.txt"
if ! diff -u "$STEPS_DIR/listed.txt" "$STEPS_DIR/decided.txt" > "$STEPS_DIR/selection.diff"; then
  printf -- 'the race selection does not cover go list ./... exactly once:\n' >&2
  cat "$STEPS_DIR/selection.diff" >&2
  selection_status=1
fi
printf -- '  %d packages selected for -race, %d excluded, %d in go list ./...\n' "${#race_packages[@]}" \
  "${#plain_packages[@]}" "$(wc -l < "$STEPS_DIR/listed.txt")"
record 'race selection' "$selection_status" $(( ( $(date +%s%N) - selection_begun ) / 1000000 ))

# The steps that need nothing from the others, in the background.
start_step vet 'go vet -tags predeployment,reclassifycost,brokerbudget ./...' \
  go vet -tags predeployment,reclassifycost,brokerbudget ./...
start_step schema 'sql/schema-checks.sh' bash "$REPO_ROOT/sql/schema-checks.sh"
start_step selftest 'ci/budget-self-test.sh' bash "$REPO_ROOT/ci/budget-self-test.sh"
fuzz_ids=()
for target in "FuzzHeader ./internal/push/protocols/syslog" "FuzzFraming ./internal/push/protocols/syslog" \
  "FuzzDecoder ./internal/push/protocols/netflow"; do
  fuzz_name="${target%% *}"
  fuzz_package="${target#* }"
  start_step "fuzz_$fuzz_name" "go test -fuzz $fuzz_name -fuzztime ${PUSH_FUZZ_SECONDS}s $fuzz_package" \
    go test -run '^$' -fuzz "^${fuzz_name}\$" -fuzztime "${PUSH_FUZZ_SECONDS}s" "$fuzz_package"
  fuzz_ids+=("fuzz_$fuzz_name")
done

# The two go test runs and the broker budgets, in the foreground.
if [ "${#race_packages[@]}" -gt 0 ]; then
  run_step "go test -race -count=1 -tags predeployment (${#race_packages[@]} selected packages)" \
    go test -race -count=1 -timeout 60m -tags predeployment "${race_packages[@]}"
fi
if [ "${#plain_packages[@]}" -gt 0 ]; then
  run_step "go test -count=1 -tags predeployment (${#plain_packages[@]} excluded packages)" \
    go test -count=1 -timeout 60m -tags predeployment "${plain_packages[@]}"
fi
broker_budgets() {
  go test -count=1 -tags brokerbudget -run Budget -v ./internal/broker/ ./internal/plugin/scripthost/ |
    grep -E '^(budget:|--- FAIL|FAIL|ok)'
  return "${PIPESTATUS[0]}"
}
run_step 'broker budgets (go test -tags brokerbudget -run Budget)' broker_budgets

for id in vet "${fuzz_ids[@]}" selftest schema; do
  wait_step "$id"
done

elapsed_ms=$(( ( $(date +%s%N) - started ) / 1000000 ))
cpus="$(nproc)"
printf -- '-- the steps, by wall time (the background ones overlap the foreground):\n'
for index in "${!step_names[@]}"; do
  printf -- '   %8d.%03d s  exit %s  %s\n' $(( step_milliseconds[index] / 1000 )) $(( step_milliseconds[index] % 1000 )) \
    "${step_codes[index]}" "${step_names[index]}"
done
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
