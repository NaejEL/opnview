#!/usr/bin/env bash
# The data-directory guard, sourced by ci/pre-deployment.sh and sql/schema-checks.sh
# (B3 and decision 16 of specs/SPEC-step-5b-widget-api.md). It defines one function
# and runs nothing when sourced.
#
# WHY IT EXISTS. sql/schema-checks.sh writes every database it creates under
# OPNVIEW_DATA_DIR, /data by default: the `data` volume, mounted outside the
# bind-mounted working tree, so no SQLite file, -wal or -shm companion ever lands in
# the repository and WAL never meets a Windows bind mount. The value is a path INSIDE
# THE CONTAINER. Two mistakes turn it into something else without any error:
#
#   - a relative value, `data` or `./data`, resolves against the working directory,
#     which is /workspace -- the bind-mounted repository;
#   - Git Bash rewrites an argument or variable that looks like a POSIX path into a
#     Windows one before docker sees it (MSYS path conversion), so `/data` arrives as
#     `C:/Program Files/Git/data`, which is again a relative-looking path inside the
#     container, or a value with a drive letter, a `:` or a `\`.
#
# Each is refused before any step runs, with a message naming the variable and the
# fix, rather than discovered later as database files in the working tree.

# check_data_dir <value> -- exits the function with 0 when <value> is a usable
# container path, and prints the reason and the fix and returns 1 otherwise.
check_data_dir() {
  local value="$1"
  local reason=''
  case "$value" in
    '')
      reason='it is empty' ;;
    *:*)
      reason='it carries a drive letter or a colon, which no container path has' ;;
    *\\*)
      reason='it carries a backslash, which no container path has' ;;
    /*)
      case "$value" in
        */Program\ Files/*)
          reason='it shows Git-Bash path conversion (a Windows install directory)' ;;
      esac ;;
    *)
      reason='it is not absolute, so it would resolve inside the bind-mounted working tree' ;;
  esac
  if [ -n "$reason" ]; then
    printf -- 'OPNVIEW_DATA_DIR=%s is refused: %s.\n' "$value" "$reason" >&2
    printf -- 'Fix: set OPNVIEW_DATA_DIR to an absolute path inside the container, such as /data (the default), or leave it unset; from Git Bash, run docker with MSYS_NO_PATHCONV=1 so the path is not rewritten into a Windows one.\n' >&2
    return 1
  fi
  return 0
}
