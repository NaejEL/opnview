#!/usr/bin/env bash
# Run one software-factory cycle headlessly against an approved spec, or run
# the project checks on their own.
# Usage: ci/factory.sh specs/SPEC-example.md [--yolo]
#        ci/factory.sh --checks
#
# Everything is built and tested inside the Debian development container. No Go
# toolchain is installed on the host, so Docker and its compose plugin are a
# hard requirement of both modes.
set -euo pipefail

# The canonical invocation of the four project commands. The same string in
# every shell, no quoting, no host PATH knowledge.
CHECKS_COMMAND='docker compose run --rm checks'

usage() {
  cat >&2 <<'USAGE'
Usage: ci/factory.sh <approved-spec-path> [--yolo]
       ci/factory.sh --checks

  <approved-spec-path>  Markdown file under specs/ containing the line
                        "Status: APPROVED".
  --yolo                Use --dangerously-skip-permissions instead of the
                        tool allowlist.
  --checks              Run the four project commands in the development
                        container and exit, without launching a cycle.
USAGE
  exit 2
}

# Both modes need Docker: the toolchain lives in the container and nowhere else.
require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "Error: the Docker CLI was not found in PATH." >&2
    echo "opnview is built and tested only inside its Debian development" >&2
    echo "container; no Go toolchain is installed on the host. Install Docker" >&2
    echo "Desktop (Windows, macOS) or the docker.io package (Linux), start the" >&2
    echo "engine, and run this script again." >&2
    return 127
  fi
  if ! docker compose version >/dev/null 2>&1; then
    echo "Error: the Docker Compose plugin is unavailable." >&2
    echo "'docker compose version' failed, so '$CHECKS_COMMAND' cannot run." >&2
    echo "Install the compose plugin (package docker-compose-plugin) or update" >&2
    echo "Docker Desktop, then run this script again." >&2
    return 127
  fi
}

SPEC=""
YOLO=0
CHECKS_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --yolo) YOLO=1 ;;
    --checks) CHECKS_ONLY=1 ;;
    -h|--help) usage ;;
    -*) echo "Unknown option: $1" >&2; usage ;;
    *)
      if [ -n "$SPEC" ]; then
        echo "Exactly one spec path is expected (already got: $SPEC)" >&2
        usage
      fi
      SPEC="$1"
      ;;
  esac
  shift
done

if [ "$CHECKS_ONLY" -eq 1 ]; then
  if [ -n "$SPEC" ]; then
    echo "Error: --checks takes no spec path (got: $SPEC)." >&2
    usage
  fi
  if ! require_docker; then
    exit 127
  fi
  echo "Checks: $CHECKS_COMMAND"
  exec docker compose run --rm checks
fi

if [ -z "$SPEC" ]; then
  echo "Error: the path to an approved spec is required." >&2
  usage
fi

if [ ! -f "$SPEC" ]; then
  echo "Error: spec '$SPEC' not found." >&2
  exit 1
fi

if ! grep -q 'Status: APPROVED' "$SPEC"; then
  echo "Error: spec '$SPEC' does not carry the line 'Status: APPROVED'." >&2
  echo "A CI cycle requires an already-approved spec. Open Claude Code and run" >&2
  echo "  /factory-run \"$SPEC\"" >&2
  echo "to get it approved in an interactive session." >&2
  exit 1
fi

if ! require_docker; then
  exit 127
fi

if ! command -v claude >/dev/null 2>&1; then
  echo "Error: command 'claude' not found in PATH." >&2
  exit 127
fi

# The agents get exactly the two checks entry points and nothing wider: not
# 'docker *', not 'docker compose *'. Host 'go', 'gofmt' and 'sqlite3' are
# deliberately absent — the toolchain exists only inside the container.
# This string is kept identical in ci/factory.ps1.
ALLOWED_TOOLS='Read,Glob,Grep,Write,Edit,Bash(git *),Bash(bash *),Bash(shellcheck *),Bash(docker compose run --rm checks),Bash(docker compose run --rm schema-checks)'

LOG_DIR="factory-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG_FILE="$LOG_DIR/factory-$STAMP.json"

set -- \
  -p "/factory-run '$SPEC'" \
  --output-format json \
  --max-turns 100

if [ "$YOLO" -eq 1 ]; then
  set -- "$@" --dangerously-skip-permissions
else
  set -- "$@" --permission-mode acceptEdits --allowedTools "$ALLOWED_TOOLS"
fi

echo "Spec : $SPEC"
echo "Log  : $LOG_FILE"
echo "Mode : $([ "$YOLO" -eq 1 ] && echo 'YOLO (permissions bypassed)' || echo 'allowlist + acceptEdits')"

set +e
claude "$@" | tee "$LOG_FILE"
STATUS=${PIPESTATUS[0]}
set -e

echo "claude exited with code $STATUS"
exit "$STATUS"
