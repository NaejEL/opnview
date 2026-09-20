#!/usr/bin/env bash
# Run one software-factory cycle headlessly against an approved spec.
# Usage: ci/factory.sh specs/SPEC-example.md [--yolo]
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: ci/factory.sh <approved-spec-path> [--yolo]

  <approved-spec-path>  Markdown file under specs/ containing the line
                        "Status: APPROVED".
  --yolo                Use --dangerously-skip-permissions instead of the
                        tool allowlist.
USAGE
  exit 2
}

SPEC=""
YOLO=0

while [ $# -gt 0 ]; do
  case "$1" in
    --yolo) YOLO=1 ;;
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

if ! command -v claude >/dev/null 2>&1; then
  echo "Error: command 'claude' not found in PATH." >&2
  exit 127
fi

ALLOWED_TOOLS='Read,Glob,Grep,Write,Edit,Bash(git *),Bash(go *),Bash(gofmt *),Bash(bash *),Bash(shellcheck *)'

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
