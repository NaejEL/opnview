#!/usr/bin/env bash
# Run the four opnview project commands. This script is the checks entry point
# and is meant to run inside the development container, never on the host: no
# Go toolchain is installed on the host.
#
# Canonical invocation, from the repository root, byte-for-byte identical in
# PowerShell and in bash:
#
#     docker compose run --rm checks
#
# Every command is run, even after one fails, and each exit code is reported.
# The script exits 0 only when all four succeeded and gofmt listed nothing.
set -uo pipefail

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

printf -- '== go test ./...\n'
go test ./...
report 'go test ./...' "$?"

if [ "$failed" -ne 0 ]; then
  printf -- 'checks: FAILED\n' >&2
  exit 1
fi

printf -- 'checks: OK\n'
