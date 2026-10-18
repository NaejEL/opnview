#!/usr/bin/env bash
# Run the opnview everyday suite: the four project commands. This script is the
# checks entry point and is meant to run inside the development container, never
# on the host: no Go toolchain is installed on the host.
#
# Canonical invocation, from the repository root, byte-for-byte identical in
# PowerShell and in bash:
#
#     docker compose run --rm checks
#
# Every command is run, even after one fails, and each exit code is reported.
# The script exits 0 only when all four succeeded, gofmt listed nothing, and the
# go test wall time stayed within the test budget below.
#
# THE TEST BUDGET. The everyday suite is what builders and verifiers run on every
# iteration, and its go test wall time must stay under one minute (the
# maintainer's budget of 7 October 2026). go test runs with -count=1 so the time
# measured is the suite's and never a cache hit's. The -race run, the tests moved
# out of this suite and the schema checks with their scale seeds and query plans run before a
# deployment instead:
#
#     docker compose run --rm pre-deployment
#
# The container's CPU count is printed beside the time, so a run on a slower or
# busier machine is identifiable as such.
#
# TWO CI-ONLY SWITCHES, both off by default and set by nothing but what is named here:
#
#   OPNVIEW_TEST_BREAKDOWN=1  records where the suite's time goes -- per package, each
#                             test database's clone, Open's schema apply and read-role
#                             grants, every wait for the database and its retries, the
#                             drop, and the test bodies (internal/testtiming) -- and
#                             prints the table after go test (ci/testtiming):
#
#                                 docker compose run --rm -e OPNVIEW_TEST_BREAKDOWN=1 checks
#
#   OPNVIEW_BUDGET_PROBE_ONLY=1  restricts go test to ci/budgetprobe. ci/budget-self-test.sh
#                             sets it, with the probe's sleep past the budget, so the budget's
#                             own check costs the probe's sleep and not a second whole suite.
set -uo pipefail

# The test budget, in seconds: the one place it is written.
TEST_BUDGET_SECONDS=60

# THE COMMANDS RUN ON A COPY OF THE WORKING TREE IN /tmp, a tmpfs here. The working tree reaches
# the container through a bind mount from the host, and every file read through it crosses the
# host's file sharing: measured on 16 October 2026, the source-policy tests of internal/opnsense
# took 9.6 s reading the tree through the mount and 2.7 s reading a copy, and every compile reads
# the tree the same way. The copy holds everything but .git and .claude, takes a few seconds, is
# timed and printed apart, and is removed by its exact path at the end. Nothing is written back:
# the four commands read the tree, and gofmt -l lists, it does not rewrite.
#
# THE SAME PATH IS THE PRE-DEPLOYMENT COMMAND'S. Go keys a compiled package by its directory as
# well as its content, so a command that compiles the tree from another path finds none of this
# command's results in the shared build cache; ci/pre-deployment.sh copies the tree here too.
#
# The budget self-test's run (OPNVIEW_BUDGET_PROBE_ONLY) makes no copy and runs in place: the
# pre-deployment command starting it already runs on a copy at this path, and the probe package
# is all it compiles.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TREE_COPY=/tmp/opnview-checks-tree

# THE HARD LIMIT, in seconds: the whole run's wall time -- the copy, gofmt, go vet, go build and
# go test together. The test budget above is measured once go test has ended, so on its own it
# cannot stop a run that hangs or crawls; this limit does. The script runs itself again under
# coreutils timeout, which stops the run and every process it started when the limit is reached,
# and the run then fails with a message naming the limit. go test's own -timeout is the test
# budget, so a test binary that hangs is stopped sooner still.
CHECKS_HARD_LIMIT_SECONDS=90

if [ -z "${OPNVIEW_CHECKS_UNDER_LIMIT:-}" ]; then
  OPNVIEW_CHECKS_UNDER_LIMIT=1 timeout --kill-after=5 "$CHECKS_HARD_LIMIT_SECONDS" bash "${BASH_SOURCE[0]}" "$@"
  limited_status=$?
  if [ "$limited_status" -eq 124 ] || [ "$limited_status" -eq 137 ]; then
    # The stopped run's own clean-up may not have run, so the copy is removed by its exact path.
    if [ -z "${OPNVIEW_BUDGET_PROBE_ONLY:-}" ]; then
      rm -rf -- "$TREE_COPY"
    fi
    printf -- 'checks: STOPPED at the hard limit of %d s for the whole run (test budget: %d s).\n' \
      "$CHECKS_HARD_LIMIT_SECONDS" "$TEST_BUDGET_SECONDS" >&2
    printf -- 'checks: FAILED\n' >&2
    exit 1
  fi
  exit "$limited_status"
fi

if [ -n "${OPNVIEW_BUDGET_PROBE_ONLY:-}" ]; then
  cd "$REPO_ROOT" || exit 1
else
  copy_started="$(date +%s%N)"
  rm -rf -- "$TREE_COPY"
  mkdir -p "$TREE_COPY"
  tar -C "$REPO_ROOT" --exclude=./.git --exclude=./.claude -cf - . | tar -C "$TREE_COPY" -xf -
  copy_status=$?
  copy_elapsed_ms=$(( ( $(date +%s%N) - copy_started ) / 1000000 ))
  printf -- '-- copying the working tree to %s: exit %s, %d.%03d s\n' "$TREE_COPY" "$copy_status" \
    $(( copy_elapsed_ms / 1000 )) $(( copy_elapsed_ms % 1000 ))
  if [ "$copy_status" -ne 0 ]; then
    printf -- 'checks: FAILED, the working tree could not be copied\n' >&2
    exit 1
  fi
  cd "$TREE_COPY" || exit 1
  trap 'cd / && rm -rf -- "$TREE_COPY"' EXIT
fi

failed=0

report() {
  printf -- '-- %s: exit %s\n' "$1" "$2"
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

# The budget self-test's run (OPNVIEW_BUDGET_PROBE_ONLY) checks the budget's enforcement and
# nothing else: the pre-deployment command starting it vets the whole tree in a step of its own,
# and go test compiles what it runs.
if [ -z "${OPNVIEW_BUDGET_PROBE_ONLY:-}" ]; then
  # gofmt -l exits 0 even when it lists unformatted files, so the output is what
  # decides here, not the exit code.
  printf -- '== gofmt -l .\n'
  gofmt_output="$(gofmt -l . 2>&1)"
  gofmt_status=$?
  if [ -n "$gofmt_output" ]; then
    printf -- '%s\n' "$gofmt_output"
    printf -- 'The files listed above are not gofmt-formatted.\n'
    if [ "$gofmt_status" -eq 0 ]; then
      gofmt_status=1
    fi
  fi
  report 'gofmt -l .' "$gofmt_status"

  printf -- '== go vet ./...\n'
  go vet ./...
  report 'go vet ./...' "$?"

  printf -- '== go build ./...\n'
  go build ./...
  report 'go build ./...' "$?"
fi

test_packages=(./...)
if [ -n "${OPNVIEW_BUDGET_PROBE_ONLY:-}" ]; then
  test_packages=(./ci/budgetprobe)
fi
breakdown_dir=''
if [ -n "${OPNVIEW_TEST_BREAKDOWN:-}" ]; then
  breakdown_dir="$(mktemp -d)"
  export OPNVIEW_TEST_TIMING_DIR="$breakdown_dir"
fi

# AS MANY TEST BINARIES AT ONCE AS THE MACHINE HAS PHYSICAL CORES, not logical CPUs. go test's -p
# defaults to the logical CPU count, and every test binary runs its own parallel tests against the
# test database, so on a machine whose logical CPUs are hyperthreads the suite ran twice as many
# database-bound binaries as there are cores to run them. Measured on 18 October 2026 on 8 cores and
# 16 hyperthreads, the suite bound by database CPU: 64.5 s at -p 16 (twice), 58.5 s and 58.9 s at
# -p 8, 57.8 s at -p 4, 61.3 s at -p 6, 75.3 s at -p 2. A topology lscpu cannot read keeps the
# default.
test_parallelism=()
physical_cores="$(lscpu -p=CORE,SOCKET 2>/dev/null | grep -v '^#' | sort -u | wc -l)"
if [ "${physical_cores:-0}" -gt 0 ]; then
  test_parallelism=(-p "$physical_cores")
fi

test_started="$(date +%s%N)"
printf -- '== go test -count=1 -timeout %ds %s %s\n' "$TEST_BUDGET_SECONDS" "${test_parallelism[*]}" \
  "${test_packages[*]}"
go test -count=1 -timeout "${TEST_BUDGET_SECONDS}s" "${test_parallelism[@]}" "${test_packages[@]}"
report "go test -count=1 -timeout ${TEST_BUDGET_SECONDS}s ${test_parallelism[*]} ${test_packages[*]}" "$?"
test_elapsed_ms=$(( ( $(date +%s%N) - test_started ) / 1000000 ))

if [ -n "$breakdown_dir" ]; then
  unset OPNVIEW_TEST_TIMING_DIR
  printf -- '== the breakdown of the go test time, per package (seconds summed over parallel tests)\n'
  go run ./ci/testtiming "$breakdown_dir"
  report 'ci/testtiming' "$?"
  rm -rf -- "$breakdown_dir"
fi

cpus="$(nproc)"
printf -- '-- go test wall time: %d.%03d s on %s CPUs, %s physical cores (test budget: %d s)\n' \
  $(( test_elapsed_ms / 1000 )) $(( test_elapsed_ms % 1000 )) "$cpus" "${physical_cores:-unknown}" \
  "$TEST_BUDGET_SECONDS"
if [ "$test_elapsed_ms" -gt $(( TEST_BUDGET_SECONDS * 1000 )) ]; then
  printf -- 'checks: go test took %d.%03d s on %s CPUs, over the test budget of %d s.\n' \
    $(( test_elapsed_ms / 1000 )) $(( test_elapsed_ms % 1000 )) "$cpus" "$TEST_BUDGET_SECONDS" >&2
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  printf -- 'checks: FAILED\n' >&2
  exit 1
fi

printf -- 'checks: OK\n'
