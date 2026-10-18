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
#   go test -race    the selected packages, with the `predeployment` tag, against the
#                    everyday test instance (OPNVIEW_RACE_TEST_DATABASE, see below);
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
#                    plan, the widget and data catalogue budgets, the backup round
#                    trip (ReclassifyEverything's wall time only on request:
#                    OPNVIEW_RECLASSIFY_COST=1, see sql/schema-checks.sh);
#   answer drift     ci/answerdrift, once the schema checks have ended: every served widget
#                    type answered again on the 100 000-row seed they keep, its member paths
#                    compared with the front's recorded answers
#                    (internal/front/frontjs/testdata/widget_answers.json.gz), failing on any
#                    difference (A9 of specs/SPEC-step-7-frontend.md). With
#                    OPNVIEW_RECORD_SEED_ANSWERS=1 it records the seed's answers into that file
#                    of the working tree instead of comparing.
#
# THE STEPS THAT DO NOT DEPEND ON ONE ANOTHER RUN SIDE BY SIDE: go vet, the fuzz
# targets, the budget self-test (most of which is its probe's sleep) and the schema
# checks start in the background, and so does the non-race go test run, while the
# -race run and then the broker budgets run in the foreground; each background step's
# output is held and printed, in one block, when it ends. When the hard limit stops the
# run, every step that had not ended is printed with how long it ran and, for a
# background step, the output it had written so far (STEPS_DIR below).
#
# The command's own wall time has a budget, PRE_DEPLOYMENT_BUDGET_SECONDS below, and
# exceeding it fails the run. The wall time of every step is printed at the end,
# beside the total and the CPU count.
#
# The everyday suite is `docker compose run --rm checks`. The tests compiled only
# here are listed in ROADMAP.md, "Development and test environment".
#
# Unlike checks, this command runs with /tmp on the container's overlay
# filesystem. The non-race go test runs against db-predeployment, a disk-backed
# PostgreSQL instance with production durability, so the suite meets a real disk once
# before a deployment, and the schema checks against db-schema-checks, configured the
# same way and holding the kept seeds, which docker-compose.yml hands this command as
# OPNVIEW_SCHEMA_CHECKS_DATABASE (Phase E decision 10, 17 October 2026): beside the Go
# suite on one instance, every DROP DATABASE of the schema checks waited for a
# checkpoint writing the Go suite's buffers too. The -race step runs
# against db-test, the everyday instance on its tmpfs, which docker-compose.yml
# hands this command as OPNVIEW_RACE_TEST_DATABASE (Phase E decision 6, 16 October
# 2026): on the disk-backed instance four of the selected packages reached the
# budget under the race detector alone, and nothing the race detector proves
# depends on a write reaching a disk.
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
# ci/checks.sh does for the everyday suite's TEST_BUDGET_SECONDS. Every go test below runs
# with this budget as its -timeout.
PRE_DEPLOYMENT_BUDGET_SECONDS=180

# THE HARD LIMIT, in seconds: the budget above is measured once every step has ended, so on its
# own it cannot stop a run that hangs or crawls; this limit does. The script runs itself again
# under coreutils timeout, which stops the run and every process it started when the limit is
# reached, and the run then fails with a message naming the limit.
PRE_DEPLOYMENT_HARD_LIMIT_SECONDS=240

# THE STEPS' LOGS, in one fixed directory that the limited run writes and this wrapper reads (Phase E
# decision 8, 17 October 2026). Every step started registers its id in steps.txt, its name in
# <id>.name and its start in <id>.begun; a background step writes its output to <id>.log as it
# comes, and every step writes <id>.status when it ends. When the hard limit stops the run, a step
# with no status is one the limit cut short, and the wrapper prints its name, how long it ran and,
# for a background step, the output it had written so far: without that, a step whose output is
# held until it ends leaves nothing to say where its time went. The directory is created and
# removed by this exact path, by the wrapper alone.
STEPS_DIR=/tmp/opnview-pre-deployment-steps
# The copy of the working tree the limited run makes (see below), named here so the wrapper can
# remove it by its exact path when the limit stopped the run before its own clean-up.
TREE_COPY=/tmp/opnview-checks-tree

# report_unfinished — every step the hard limit stopped, with its elapsed time and partial output.
report_unfinished() {
  local id name begun elapsed_ms
  [ -f "$STEPS_DIR/steps.txt" ] || return 0
  while IFS= read -r id; do
    [ -f "$STEPS_DIR/$id.status" ] && continue
    name="$(cat "$STEPS_DIR/$id.name" 2> /dev/null)"
    begun="$(cat "$STEPS_DIR/$id.begun" 2> /dev/null)"
    elapsed_ms=$(( ( $(date +%s%N) - ${begun:-0} ) / 1000000 ))
    printf -- '== %s: UNFINISHED at the hard limit, after %d.%03d s\n' "$name" \
      $(( elapsed_ms / 1000 )) $(( elapsed_ms % 1000 ))
    if [ -f "$STEPS_DIR/$id.log" ]; then
      printf -- '   its output so far:\n'
      cat "$STEPS_DIR/$id.log"
      printf -- '== end of the partial output of %s\n' "$name"
    else
      printf -- '   (a foreground step: its output so far is printed above)\n'
    fi
  done < "$STEPS_DIR/steps.txt"
}

if [ -z "${OPNVIEW_PRE_DEPLOYMENT_UNDER_LIMIT:-}" ]; then
  rm -rf -- "$STEPS_DIR"
  mkdir -p "$STEPS_DIR"
  OPNVIEW_PRE_DEPLOYMENT_UNDER_LIMIT=1 timeout --kill-after=5 "$PRE_DEPLOYMENT_HARD_LIMIT_SECONDS" \
    bash "${BASH_SOURCE[0]}" "$@"
  limited_status=$?
  if [ "$limited_status" -eq 124 ] || [ "$limited_status" -eq 137 ]; then
    report_unfinished
    rm -rf -- "$STEPS_DIR" "$TREE_COPY"
    printf -- 'pre-deployment: STOPPED at the hard limit of %d s for the whole run (pre-deployment budget: %d s).\n' \
      "$PRE_DEPLOYMENT_HARD_LIMIT_SECONDS" "$PRE_DEPLOYMENT_BUDGET_SECONDS" >&2
    printf -- 'pre-deployment: FAILED\n' >&2
    exit 1
  fi
  rm -rf -- "$STEPS_DIR"
  exit "$limited_status"
fi
if [ ! -d "$STEPS_DIR" ]; then
  printf -- 'pre-deployment: %s does not exist; run the command, never its limited run alone\n' "$STEPS_DIR" >&2
  exit 1
fi

# THE COMMAND RUNS ON A COPY OF THE WORKING TREE, at the path ci/checks.sh copies it to. Go keys
# a compiled package by its directory as well as its content: compiled from /workspace, no
# package found the everyday suite's results in the shared build cache (go-build-cache, the
# same named volume), and go vet alone took 413 s on 16 October 2026. Compiled from the same
# path, every package the everyday suite compiled is a cache hit here. The copy is made only
# after the data-directory guard above, which the everyday suite runs from inside its own copy
# at this path, and it is removed by its exact path at the end.
copy_started="$(date +%s%N)"
rm -rf -- "$TREE_COPY"
mkdir -p "$TREE_COPY"
tar -C "$REPO_ROOT" --exclude=./.git --exclude=./.claude -cf - . | tar -C "$TREE_COPY" -xf -
copy_status=$?
copy_elapsed_ms=$(( ( $(date +%s%N) - copy_started ) / 1000000 ))
printf -- '-- copying the working tree to %s: exit %s, %d.%03d s\n' "$TREE_COPY" "$copy_status" \
  $(( copy_elapsed_ms / 1000 )) $(( copy_elapsed_ms % 1000 ))
if [ "$copy_status" -ne 0 ]; then
  printf -- 'pre-deployment: FAILED, the working tree could not be copied\n' >&2
  exit 1
fi
# The working tree itself, before the copy: the one place a recording is written back to.
SOURCE_ROOT="$REPO_ROOT"
REPO_ROOT="$TREE_COPY"
cd "$REPO_ROOT" || exit 1

# The push decoders' fuzz targets run this long each, side by side.
PUSH_FUZZ_SECONDS=10

failed=0
started="$(date +%s%N)"
trap 'cd / && rm -rf -- "$TREE_COPY"' EXIT

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

# register_step <id> <name> — a step in the steps directory, for the wrapper above to report should
# the hard limit stop it.
register_step() {
  printf -- '%s\n' "$2" > "$STEPS_DIR/$1.name"
  date +%s%N > "$STEPS_DIR/$1.begun"
  printf -- '%s\n' "$1" >> "$STEPS_DIR/steps.txt"
}

# run_step <name> <command...> — a step in the foreground, its output as it comes.
foreground_steps=0
run_step() {
  local name="$1" id begun status
  shift
  foreground_steps=$(( foreground_steps + 1 ))
  id="foreground_$foreground_steps"
  register_step "$id" "$name"
  printf -- '== %s\n' "$name"
  begun="$(date +%s%N)"
  "$@"
  status=$?
  echo "$status" > "$STEPS_DIR/$id.status"
  record "$name" "$status" $(( ( $(date +%s%N) - begun ) / 1000000 ))
}

# start_step <id> <name> <command...> — a step in the background, its output held until it ends.
# Its wall time runs from its start to its own end, written when it ends, not to the moment it is
# waited for.
declare -A step_pid=() step_name=() step_begun=()
start_step() {
  local id="$1" name="$2"
  shift 2
  step_name[$id]="$name"
  step_begun[$id]="$(date +%s%N)"
  register_step "$id" "$name"
  ( "$@" > "$STEPS_DIR/$id.log" 2>&1; status=$?; date +%s%N > "$STEPS_DIR/$id.ended"
    echo "$status" > "$STEPS_DIR/$id.status" ) &
  step_pid[$id]=$!
  printf -- '.. %s started in the background\n' "$name"
}

# wait_step <id> — waits for a background step and prints its block.
wait_step() {
  local id="$1" status ended
  wait "${step_pid[$id]}"
  status="$(cat "$STEPS_DIR/$id.status" 2> /dev/null || echo 1)"
  ended="$(cat "$STEPS_DIR/$id.ended" 2> /dev/null || date +%s%N)"
  printf -- '== %s\n' "${step_name[$id]}"
  cat "$STEPS_DIR/$id.log"
  record "${step_name[$id]}" "$status" $(( ( ended - ${step_begun[$id]} ) / 1000000 ))
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
# The schema checks run on their own instance, db-schema-checks, which holds the kept seeds (Phase E
# decision 10): docker-compose.yml hands its connection string as OPNVIEW_SCHEMA_CHECKS_DATABASE.
schema_test_database="${OPNVIEW_SCHEMA_CHECKS_DATABASE:-}"
if [ -z "$schema_test_database" ]; then
  printf -- 'OPNVIEW_SCHEMA_CHECKS_DATABASE is not set: docker-compose.yml names the schema checks their instance.\n' >&2
  record 'OPNVIEW_SCHEMA_CHECKS_DATABASE' 1 0
else
  start_step schema 'sql/schema-checks.sh (on the schema checks'"'"' instance)' \
    env OPNVIEW_TEST_DATABASE="$schema_test_database" bash "$REPO_ROOT/sql/schema-checks.sh"
fi
start_step selftest 'ci/budget-self-test.sh' bash "$REPO_ROOT/ci/budget-self-test.sh"
# The answer drift's program is built now, beside the other steps, so that what runs after the schema
# checks is the program alone.
start_step answerdrift_build 'go build ./ci/answerdrift' go build -o "$STEPS_DIR/answerdrift" ./ci/answerdrift
fuzz_ids=()
for target in "FuzzHeader ./internal/push/protocols/syslog" "FuzzFraming ./internal/push/protocols/syslog" \
  "FuzzDecoder ./internal/push/protocols/netflow"; do
  fuzz_name="${target%% *}"
  fuzz_package="${target#* }"
  start_step "fuzz_$fuzz_name" "go test -fuzz $fuzz_name -fuzztime ${PUSH_FUZZ_SECONDS}s $fuzz_package" \
    go test -timeout "${PRE_DEPLOYMENT_BUDGET_SECONDS}s" -run '^$' -fuzz "^${fuzz_name}\$" -fuzztime "${PUSH_FUZZ_SECONDS}s" "$fuzz_package"
  fuzz_ids+=("fuzz_$fuzz_name")
done

# THE TWO GO TEST RUNS SIDE BY SIDE, then the broker budgets. The -race step's test databases are
# created on the everyday test instance, with that instance's clone strategy (the default,
# FILE_COPY; internal/store/testdatabase.go); the non-race run keeps db-predeployment. The two runs
# share no database instance, so the non-race run starts in the background as the -race run starts
# in the foreground: measured on 16 October 2026 on 16 CPUs, one after the other they took 95.2 s
# and 103.1 s, and the run reached the hard limit before the schema checks ended.
#
# AS MANY TEST BINARIES AT ONCE AS THE MACHINE HAS PHYSICAL CORES, the rule ci/checks.sh states and
# measured: go test's -p defaults to the logical CPU count, and every test binary runs its parallel
# tests against a test database, so on hyperthreads twice as many database-bound binaries ran as there
# are cores to run them. Both go test runs below take it; a topology lscpu cannot read keeps the
# default.
test_parallelism=()
physical_cores="$(lscpu -p=CORE,SOCKET 2>/dev/null | grep -v '^#' | sort -u | wc -l)"
if [ "${physical_cores:-0}" -gt 0 ]; then
  test_parallelism=(-p "$physical_cores")
fi
if [ "${#plain_packages[@]}" -gt 0 ]; then
  start_step plain "go test -count=1 ${test_parallelism[*]} -tags predeployment (${#plain_packages[@]} excluded packages)" \
    go test -count=1 -timeout "${PRE_DEPLOYMENT_BUDGET_SECONDS}s" "${test_parallelism[@]}" -tags predeployment \
    "${plain_packages[@]}"
fi
race_test_database="${OPNVIEW_RACE_TEST_DATABASE:-}"
if [ -z "$race_test_database" ]; then
  printf -- 'OPNVIEW_RACE_TEST_DATABASE is not set: docker-compose.yml names the -race step its test instance.\n' >&2
  record 'OPNVIEW_RACE_TEST_DATABASE' 1 0
elif [ "${#race_packages[@]}" -gt 0 ]; then
  run_step "go test -race -count=1 ${test_parallelism[*]} -tags predeployment (${#race_packages[@]} selected packages, on the everyday test instance)" \
    env -u OPNVIEW_TEST_CLONE_STRATEGY OPNVIEW_TEST_DATABASE="$race_test_database" \
    go test -race -count=1 -timeout "${PRE_DEPLOYMENT_BUDGET_SECONDS}s" "${test_parallelism[@]}" -tags predeployment "${race_packages[@]}"
fi
if [ "${#plain_packages[@]}" -gt 0 ]; then
  wait_step plain
fi
# The broker budgets measure medians, so they start once neither go test run competes for the CPU.
broker_budgets() {
  go test -count=1 -timeout "${PRE_DEPLOYMENT_BUDGET_SECONDS}s" -tags brokerbudget -run Budget -v ./internal/broker/ ./internal/plugin/scripthost/ |
    grep -E '^(budget:|--- FAIL|FAIL|ok)'
  return "${PIPESTATUS[0]}"
}
run_step 'broker budgets (go test -tags brokerbudget -run Budget)' broker_budgets

for id in vet "${fuzz_ids[@]}" selftest answerdrift_build schema; do
  [ -n "${step_pid[$id]:-}" ] || continue
  wait_step "$id"
done

# THE ANSWER DRIFT (A9 of specs/SPEC-step-7-frontend.md), on the 100 000-row seed the schema checks keep
# (schema_checks_main_seed, read in place, as the widget budgets read the 1 000 000-row one: the store
# only reads it). The seed's name and instant are the schema checks' own, read from where they are
# written, never written twice.
answer_drift() {
  local seed_db seed_now answers mode
  seed_db="$(sed -n 's/^MAIN_SEED_DB=//p' "$REPO_ROOT/sql/schema-checks.sh")"
  seed_now="$(sed -n 's/^NOW=//p' "$REPO_ROOT/sql/schema-checks.sh")"
  answers="$REPO_ROOT/internal/front/frontjs/testdata/widget_answers.json.gz"
  mode=()
  if [ "${OPNVIEW_RECORD_SEED_ANSWERS:-}" = 1 ]; then
    answers="$SOURCE_ROOT/internal/front/frontjs/testdata/widget_answers.json.gz"
    mode=(-record)
  fi
  if [ -z "$seed_db" ] || [ -z "$seed_now" ] || [ -z "$schema_test_database" ]; then
    printf -- 'the seed of the answer drift is not named: MAIN_SEED_DB and NOW of sql/schema-checks.sh, and its instance\n' >&2
    return 1
  fi
  "$STEPS_DIR/answerdrift" -database "$schema_test_database dbname=$seed_db" -now "$seed_now" -answers "$answers" "${mode[@]}"
}
run_step 'answer drift (ci/answerdrift on the 100 000-row seed)' answer_drift

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
