#!/usr/bin/env bash
# Serve opnview on a SCRATCH database loaded with the review fixture of
# specs/SPEC-deferred-interface-bundle.md, for the interface review.
#
# It runs inside the development container, from the repository root:
#
#     docker compose run --rm -p 8081:8081 dev \
#         bash sql/review/serve-deferred-interface-bundle.sh /tmp/opnview-review 0.0.0.0:8081
#
# (In Git Bash on Windows, prefix the command with MSYS_NO_PATHCONV=1 so the
# container paths are passed unchanged.)
#
# WHAT IT REFUSES. A data directory under /data, which is where the `data` volume is
# mounted and where a live service keeps its database, and a data directory that
# already holds a database: the fixture rewrites interfaces, networks, availability
# and gaps, and must never meet real rows. It enters no firewall credential, so the
# service contacts no firewall; the Public Suffix List refresh it runs at start is the
# product's own third outbound call.
#
# HOW IT RUNS. The schema is applied and the fixture loaded BEFORE the service
# starts, so the loop intervals the fixture sets are in force from the first turn;
# the service is started; once its start-up passes have recorded what they found --
# with no firewall, every source unavailable -- the fixture is applied again, which
# puts the review's states back. The service then runs until the container is
# stopped. The one-time setup token it prints is how the reviewer creates the first
# account at /setup.
set -euo pipefail

data_dir="${1:?usage: serve-deferred-interface-bundle.sh <scratch data directory> <listen address>}"
listen="${2:?usage: serve-deferred-interface-bundle.sh <scratch data directory> <listen address>}"
repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
fixture="$repo_root/sql/review/deferred-interface-bundle.sql"
database="$data_dir/opnview.db"

case "$data_dir" in
  /data | /data/*)
    printf -- 'review: %s is the data volume, which a live service uses; refusing\n' "$data_dir" >&2
    exit 2
    ;;
esac
if [ -e "$database" ]; then
  printf -- 'review: %s already holds a database; this script loads a fresh one only\n' "$data_dir" >&2
  exit 2
fi

mkdir -p "$data_dir"
chmod 700 "$data_dir"
sqlite3 -bail "$database" < "$repo_root/internal/store/schema.sql" > /dev/null
sqlite3 -bail "$database" < "$fixture"

binary="$data_dir/opnview-review"
(cd "$repo_root" && go build -o "$binary" ./cmd/opnview)

"$binary" --data-dir "$data_dir" --listen "$listen" &
service=$!
trap 'kill -TERM "$service" 2>/dev/null || true; wait "$service" || true' INT TERM

# Wait for the start-up passes: the MaxMind and Public Suffix List refreshes are the
# last to record a probe, and both do so at start whatever they find.
for _ in $(seq 1 90); do
  pending="$(sqlite3 -cmd '.timeout 5000' "$database" \
    "SELECT count(*) FROM source_availability a JOIN provider p ON p.id = a.provider_id
     WHERE p.kind IN ('geo_asn', 'public_suffix') AND a.probe = 'not_yet_probed'")"
  if [ "$pending" = "0" ]; then
    break
  fi
  sleep 1
done
sleep 3
sqlite3 -bail -cmd '.timeout 5000' "$database" < "$fixture"
printf -- 'review: the fixture is loaded; the interface is at http://%s\n' "$listen"

wait "$service"
