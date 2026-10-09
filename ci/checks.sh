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
# maintainer's budget of 7 October 2026,
# specs/SPEC-test-budget-and-two-live-defects.md). go test runs with -count=1 so
# the time measured is the suite's and never a cache hit's. The -race run, the
# tests moved out of this suite and the schema checks with their scale seeds and
# query plans run before a deployment instead:
#
#     docker compose run --rm pre-deployment
#
# The container's CPU count is printed beside the time, so a run on a slower or
# busier machine is identifiable as such.
set -uo pipefail

# The test budget, in seconds: the one place it is written.
TEST_BUDGET_SECONDS=60

failed=0

report() {
  printf -- '-- %s: exit %s\n' "$1" "$2"
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

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

printf -- '== go test -count=1 ./...\n'
test_started="$(date +%s%N)"
go test -count=1 ./...
test_status=$?
test_elapsed_ms=$(( ( $(date +%s%N) - test_started ) / 1000000 ))
report 'go test -count=1 ./...' "$test_status"

cpus="$(nproc)"
printf -- '-- go test wall time: %d.%03d s on %s CPUs (test budget: %d s)\n' \
  $(( test_elapsed_ms / 1000 )) $(( test_elapsed_ms % 1000 )) "$cpus" "$TEST_BUDGET_SECONDS"
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
