#!/usr/bin/env bash
# The one test command of opnview, run inside the development container:
#
#     docker compose run --rm checks
#
# gofmt, go vet, go build, go test. The plugins' JavaScript tests run inside
# go test (internal/plugin/harness). Every step runs even after one fails, and
# the script exits 0 only when all of them succeeded.
#
# go test runs with -count=1, so what is measured is the suite and never a
# cache hit, and with a hard timeout: a suite that does not end within the
# budget is a defect to fix, never a budget to raise (docs/adr/0012-one-test-command.md).
set -uo pipefail

TEST_TIMEOUT=60s

# The commands run on a copy of the working tree in /tmp (a tmpfs here): every
# file read through the bind mount crosses the host's file sharing, which costs
# a few seconds on every compile. The copy excludes .git and .claude and is
# removed by its exact path at the end. Nothing is written back.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TREE_COPY=/tmp/opnview-checks-tree

rm -rf -- "$TREE_COPY"
mkdir -p "$TREE_COPY"
if ! tar -C "$REPO_ROOT" --exclude=./.git --exclude=./.claude -cf - . | tar -C "$TREE_COPY" -xf -; then
  printf -- 'checks: FAILED, the working tree could not be copied\n' >&2
  exit 1
fi
cd "$TREE_COPY" || exit 1
trap 'cd / && rm -rf -- "$TREE_COPY"' EXIT

failed=0
report() {
  printf -- '-- %s: exit %s\n' "$1" "$2"
  if [ "$2" -ne 0 ]; then
    failed=1
  fi
}

printf -- '== gofmt -l .\n'
gofmt_output="$(gofmt -l . 2>&1)"
gofmt_status=$?
if [ -n "$gofmt_output" ]; then
  printf -- '%s\n' "$gofmt_output"
  printf -- 'The files listed above are not gofmt-formatted.\n'
  gofmt_status=1
fi
report 'gofmt -l .' "$gofmt_status"

printf -- '== go vet ./...\n'
go vet ./...
report 'go vet ./...' "$?"

printf -- '== go build ./...\n'
go build ./...
report 'go build ./...' "$?"

started="$(date +%s)"
printf -- '== go test -count=1 -timeout %s ./...\n' "$TEST_TIMEOUT"
go test -count=1 -timeout "$TEST_TIMEOUT" ./...
report "go test -count=1 -timeout $TEST_TIMEOUT ./..." "$?"
printf -- '-- go test wall time: %d s on %s CPUs\n' "$(( $(date +%s) - started ))" "$(nproc)"

if [ "$failed" -ne 0 ]; then
  printf -- 'checks: FAILED\n' >&2
  exit 1
fi
printf -- 'checks: OK\n'
