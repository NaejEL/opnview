#!/usr/bin/env bash
# opnview — schema checks.
#
# Applies the migrations to a fresh database, seeds it, runs the seven screen
# queries and their query plans, and asserts the acceptance criteria of
# specs/SPEC-data-model-sqlite-schema.md (labelled AC*) and of
# specs/SPEC-provider-neutral-schema.md (labelled PN-AC*). A criterion of the
# first spec that names an object this cycle renamed is RESTATED against the
# new name, never removed and never relaxed. This script is the schema entry
# point
# and is meant to run inside the development container, never on the host: no
# sqlite3 is installed on the host.
#
# Canonical invocation, from the repository root, byte-for-byte identical in
# PowerShell and in bash:
#
#     docker compose run --rm schema-checks
#
# Every check is run, even after one fails, and the failures are summarised at
# the end. The script exits 0 only when every check passed.
#
# Every database it creates lives under /data, the named volume mounted outside
# the bind-mounted working tree: a SQLite file and its -wal / -shm companions
# must never land in the repository, and WAL on a Windows bind mount locks
# pathologically.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${OPNVIEW_DATA_DIR:-/data}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

MAIN_DB="$DATA_DIR/schema-checks-main.db"
REPEAT_DB="$DATA_DIR/schema-checks-repeat.db"
ALT_DB="$DATA_DIR/schema-checks-alt.db"
PURGE_DB="$DATA_DIR/schema-checks-purge.db"
UNLIMITED_DB="$DATA_DIR/schema-checks-unlimited.db"
INDEX_DB="$DATA_DIR/schema-checks-noindex.db"
FRESH_DB="$DATA_DIR/schema-checks-fresh.db"
REGISTER_DB="$DATA_DIR/schema-checks-register.db"
SCALE_DB="$DATA_DIR/schema-checks-scale.db"

# Seed parameters. The default run and the alternative run below differ in
# every count, so nothing may assume a segment, device or interface count.
NOW=1750000000
SEGMENTS=6
DEVICES=40
RULES=12
FLOW_ROWS=100000
ALERTS=500
PAIR_ROWS=2000

ALT_SEGMENTS=3
ALT_DEVICES=17
ALT_RULES=5
ALT_FLOW_ROWS=4000
ALT_ALERTS=60
ALT_PAIR_ROWS=300

# The scale run. The baseline above is a test size; this one is the check that
# the plans hold at a production-ish volume in the largest growing table.
SCALE_FLOW_ROWS=1000000

WINDOW_END=$NOW
WINDOW_START=$((NOW - 86400))
SEGMENT_ID=1
DEVICE_ID=3

PASS_COUNT=0
FAIL_COUNT=0
FAILURES=""

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf -- '  ok   %s\n' "$1"
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILURES="$FAILURES
  $1"
    printf -- '  FAIL %s\n' "$1" >&2
}

section() {
    printf -- '\n== %s\n' "$1"
}

# check <label> <expected> <actual>
check() {
    if [ "$2" = "$3" ]; then
        pass "$1"
    else
        fail "$1 (expected '$2', got '$3')"
    fi
}

# check_ge <label> <minimum> <actual>
check_ge() {
    if [ -n "$3" ] && [ "$3" -ge "$2" ] 2>/dev/null; then
        pass "$1 ($3 >= $2)"
    else
        fail "$1 (expected at least $2, got '$3')"
    fi
}

# q <db> <sql> — one scalar or one column of results.
q() {
    sqlite3 "$1" "$2"
}

# expect_sql_failure <label> <db> <sql>
#
# A non-zero exit is not enough: a statement naming a column that no longer
# exists also exits non-zero, and an assertion written against a renamed object
# would then report "rejected" on a parse error rather than on the constraint
# it claims to demonstrate. The captured message must therefore say
# "constraint failed" -- the marker SQLite prints for CHECK, NOT NULL, UNIQUE
# and FOREIGN KEY rejections alike. Every assertion in this harness expects one
# of those four; there is no exception.
expect_sql_failure() {
    local label="$1" db="$2" sql="$3" out first
    if out="$(sqlite3 -bail "$db" "PRAGMA foreign_keys = ON; $sql" 2>&1)"; then
        fail "$label (the statement succeeded; it must be rejected)"
        return
    fi
    first="$(printf -- '%s' "$out" | head -n 1)"
    case "$out" in
        *'constraint failed'*)
            pass "$label (rejected: $first)"
            ;;
        *)
            fail "$label (rejected, but not by a constraint: $first)"
            ;;
    esac
}

# expect_sql_success <label> <db> <sql>
expect_sql_success() {
    local label="$1" db="$2" sql="$3" out
    if out="$(sqlite3 -bail "$db" "PRAGMA foreign_keys = ON; $sql" 2>&1)"; then
        pass "$label"
    else
        fail "$label (the statement failed: $(printf -- '%s' "$out" | head -n 1))"
    fi
}

# --------------------------------------------------------------------------
# Migration runner. A migration whose filename is already recorded in
# schema_version is skipped, which is what makes re-applying a no-op.
# --------------------------------------------------------------------------
apply_migrations() {
    local db="$1" errfile="$2" f name applied
    : > "$errfile"
    for f in "$REPO_ROOT"/migrations/*.sql; do
        name="$(basename "$f")"
        applied="$(sqlite3 "$db" "SELECT count(*) FROM schema_version WHERE filename = '$name';" 2>/dev/null)"
        if [ "$applied" = "1" ]; then
            continue
        fi
        printf -- '.bail on\nPRAGMA foreign_keys = ON;\n.read %s\n' "$f" |
            sqlite3 "$db" 2>>"$errfile" || return 1
    done
    return 0
}

# seed_database <db> <segments> <devices> <rules> <flow_rows> <alerts> <pair_rows>
seed_database() {
    local db="$1"
    {
        printf -- '.bail on\n'
        printf -- '.param init\n'
        printf -- '.param set :now %s\n' "$NOW"
        printf -- '.param set :segments %s\n' "$2"
        printf -- '.param set :devices %s\n' "$3"
        printf -- '.param set :rules %s\n' "$4"
        printf -- '.param set :flow_rows %s\n' "$5"
        printf -- '.param set :alerts %s\n' "$6"
        printf -- '.param set :pair_rows %s\n' "$7"
        printf -- '.read %s\n' "$REPO_ROOT/sql/seed.sql"
    } | sqlite3 "$db"
}

# Split a query file on its "-- <prefix>: <Name>" markers into one file per
# query, and write the ordered list of names to <outdir>/names.txt.
split_queries() {
    local src="$1" prefix="$2" outdir="$3"
    mkdir -p "$outdir"
    : > "$outdir/names.txt"
    awk -v outdir="$outdir" -v prefix="$prefix" '
        index($0, "-- " prefix ": ") == 1 {
            n++
            name = substr($0, length("-- " prefix ": ") + 1)
            slug = name
            gsub(/[^A-Za-z0-9]/, "_", slug)
            file = sprintf("%s/%02d_%s.sql", outdir, n, slug)
            print name >> (outdir "/names.txt")
        }
        n > 0 { print >> file }
    ' "$src"
}

# run_query <db> <query file> — bind the parameters and execute.
run_query() {
    local db="$1" file="$2"
    {
        printf -- '.bail on\n'
        printf -- '.param init\n'
        printf -- '.param set :window_start %s\n' "$WINDOW_START"
        printf -- '.param set :window_end %s\n' "$WINDOW_END"
        printf -- '.param set :segment_id %s\n' "$SEGMENT_ID"
        printf -- '.param set :device_id %s\n' "$DEVICE_ID"
        printf -- '.read %s\n' "$file"
    } | sqlite3 "$db"
}

# explain_query <db> <query file>
explain_query() {
    local db="$1" file="$2" tmp="$WORK/explain.sql"
    {
        printf -- 'EXPLAIN QUERY PLAN\n'
        cat "$file"
    } > "$tmp"
    run_query "$db" "$tmp"
}

# wrap_query <query file> <outer prefix> <outer suffix> — turn a screen query
# into a subquery so an assertion can be expressed over its result set without
# restating it. The authoritative text stays the one in sql/queries.
wrap_query() {
    local file="$1" prefix="$2" suffix="$3" tmp="$WORK/wrapped.sql"
    {
        printf -- '%s\n' "$prefix"
        sed 's/;[[:space:]]*$//' "$file"
        printf -- '%s\n' "$suffix"
    } > "$tmp"
    printf -- '%s' "$tmp"
}

printf -- 'opnview schema checks\n'
printf -- 'repository root : %s\n' "$REPO_ROOT"
printf -- 'data directory  : %s\n' "$DATA_DIR"
printf -- 'sqlite3         : %s\n' "$(sqlite3 --version)"

mkdir -p "$DATA_DIR"
rm -f "$DATA_DIR"/schema-checks-*.db "$DATA_DIR"/schema-checks-*.db-wal "$DATA_DIR"/schema-checks-*.db-shm

# ===========================================================================
section 'AC1, AC4 — migrations apply cleanly, and re-applying is a no-op'
# ===========================================================================
sqlite3 "$MAIN_DB" 'PRAGMA journal_mode = WAL;' > /dev/null
if apply_migrations "$MAIN_DB" "$WORK/migrate1.err"; then
    pass 'AC1 first apply exited 0'
else
    fail 'AC1 first apply exited non-zero'
fi
if [ -s "$WORK/migrate1.err" ]; then
    fail "AC1 first apply wrote to stderr: $(cat "$WORK/migrate1.err")"
else
    pass 'AC1 first apply wrote nothing to stderr'
fi

OBJECTS_BEFORE="$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master;")"
VERSIONS_BEFORE="$(q "$MAIN_DB" 'SELECT count(*) FROM schema_version;')"
if apply_migrations "$MAIN_DB" "$WORK/migrate2.err"; then
    pass 'AC1 second apply exited 0'
else
    fail 'AC1 second apply exited non-zero'
fi
if [ -s "$WORK/migrate2.err" ]; then
    fail "AC1 second apply wrote to stderr: $(cat "$WORK/migrate2.err")"
else
    pass 'AC1 second apply wrote nothing to stderr'
fi
check 'AC1 second apply changed no schema object' \
    "$OBJECTS_BEFORE" "$(q "$MAIN_DB" 'SELECT count(*) FROM sqlite_master;')"
check 'AC1 second apply added no schema_version row' \
    "$VERSIONS_BEFORE" "$(q "$MAIN_DB" 'SELECT count(*) FROM schema_version;')"

q "$MAIN_DB" 'SELECT filename FROM schema_version ORDER BY filename;' > "$WORK/versions.txt"
find "$REPO_ROOT/migrations" -maxdepth 1 -name '*.sql' -printf '%f\n' | sort > "$WORK/migration_files.txt"
if diff -u "$WORK/migration_files.txt" "$WORK/versions.txt" > "$WORK/versions.diff"; then
    pass 'AC4 schema_version matches the files in migrations/'
else
    fail "AC4 schema_version does not match migrations/: $(cat "$WORK/versions.diff")"
fi

# ===========================================================================
section 'AC7 — deterministic seed, and the baseline row count'
# ===========================================================================
seed_database "$MAIN_DB" "$SEGMENTS" "$DEVICES" "$RULES" "$FLOW_ROWS" "$ALERTS" "$PAIR_ROWS"
apply_migrations "$REPEAT_DB" "$WORK/migrate3.err" || fail 'AC7 repeat database migration failed'
seed_database "$REPEAT_DB" "$SEGMENTS" "$DEVICES" "$RULES" "$FLOW_ROWS" "$ALERTS" "$PAIR_ROWS"

table_counts() {
    local db="$1" t
    for t in $(q "$db" "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name;"); do
        printf -- '%s %s\n' "$t" "$(q "$db" "SELECT count(*) FROM \"$t\";")"
    done
}
table_counts "$MAIN_DB" > "$WORK/counts_main.txt"
table_counts "$REPEAT_DB" > "$WORK/counts_repeat.txt"
if diff -u "$WORK/counts_main.txt" "$WORK/counts_repeat.txt" > "$WORK/counts.diff"; then
    pass 'AC7 two seed runs produce identical row counts per table'
else
    fail "AC7 seed is not deterministic: $(cat "$WORK/counts.diff")"
fi
printf -- '  seeded row counts:\n'
sed 's/^/    /' "$WORK/counts_main.txt"

LARGEST_TABLE="$(sort -k2 -n -r "$WORK/counts_main.txt" | head -n 1 | awk '{print $1}')"
LARGEST_COUNT="$(sort -k2 -n -r "$WORK/counts_main.txt" | head -n 1 | awk '{print $2}')"
check 'AC7 the largest growing table is flow' 'flow' "$LARGEST_TABLE"
check_ge 'AC7 the largest growing table holds at least 100000 rows' 100000 "$LARGEST_COUNT"

# ===========================================================================
section 'AC2, AC3 — integrity and foreign keys'
# ===========================================================================
check 'AC2 PRAGMA integrity_check' 'ok' "$(q "$MAIN_DB" 'PRAGMA integrity_check;')"
check 'AC2 PRAGMA foreign_key_check returns no rows' '' "$(q "$MAIN_DB" 'PRAGMA foreign_key_check;')"
expect_sql_failure 'AC3 a flow whose segment does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device,
        interface_lookup_state, src_segment_id, src_address, dst_address, protocol,
        ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope)
     VALUES ('ac3-orphan-flow', $NOW, $NOW, 'ac3-device', 'resolved', 999999999,
             'ac3-src', 'ac3-dst', 'tcp', 4, 'pass', 'in', 100, 'pending',
             'north_south');"
expect_sql_failure 'AC3 a security event whose device does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
        rule_identity, signature, event_action, src_address, dst_address, src_device_id)
     SELECT id, 'ac3-key', $NOW, $NOW, '1', 'ac3', 'allowed', 'a', 'b', 999999999
     FROM provider WHERE kind = 'security_event' LIMIT 1;"
expect_sql_failure 'AC3 a security event whose provider does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
        rule_identity, signature, event_action, src_address, dst_address)
     VALUES (999999999, 'ac3-key-2', $NOW, $NOW, '1', 'ac3', 'allowed', 'a', 'b');"

# ===========================================================================
section 'AC5, AC6 — the entity list, and the absence of a TLS/HTTP entity'
# ===========================================================================
DOC="$REPO_ROOT/docs/data-model.md"
extract_block() {
    awk -v begin="$2" -v end="$3" '
        index($0, begin) == 1 { inside = 1; next }
        index($0, end) == 1 { inside = 0 }
        inside && NF && index($0, "```") != 1 { print $1 }
    ' "$1"
}
extract_block "$DOC" '<!-- entity-list:begin -->' '<!-- entity-list:end -->' | sort > "$WORK/doc_entities.txt"
q "$MAIN_DB" "SELECT name FROM sqlite_master WHERE type IN ('table', 'view')
              AND name NOT LIKE 'sqlite_%' ORDER BY name;" | sort > "$WORK/db_entities.txt"
if diff -u "$WORK/doc_entities.txt" "$WORK/db_entities.txt" > "$WORK/entities.diff"; then
    pass "AC5 the documented entity list matches sqlite_master ($(wc -l < "$WORK/db_entities.txt" | tr -d ' ') entities)"
else
    fail "AC5 the entity lists differ: $(cat "$WORK/entities.diff")"
fi
check 'AC5 blocked_event is a view' 'view' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'blocked_event';")"
check 'AC6 no entity is a TLS or HTTP observation record' '' \
    "$(q "$MAIN_DB" "SELECT name FROM sqlite_master
        WHERE type IN ('table', 'view')
          AND (lower(name) GLOB '*tls*' OR lower(name) GLOB '*http*');")"

extract_block "$DOC" '<!-- growing-tables:begin -->' '<!-- growing-tables:end -->' | sort > "$WORK/growing.txt"
extract_block "$DOC" '<!-- bounded-tables:begin -->' '<!-- bounded-tables:end -->' | sort > "$WORK/bounded.txt"
sort -u "$WORK/growing.txt" "$WORK/bounded.txt" > "$WORK/classified.txt"
grep -v '^blocked_event$' "$WORK/db_entities.txt" | sort > "$WORK/db_tables.txt"
if diff -u "$WORK/db_tables.txt" "$WORK/classified.txt" > "$WORK/classified.diff"; then
    pass 'AC10 every table is classified growing or bounded in the document'
else
    fail "AC10 the growing/bounded classification is incomplete: $(cat "$WORK/classified.diff")"
fi

# ===========================================================================
section 'AC8, AC9, AC10 — the seven screen queries and their plans'
# ===========================================================================
split_queries "$REPO_ROOT/sql/queries/screens.sql" 'screen' "$WORK/screens"
SCREEN_COUNT="$(wc -l < "$WORK/screens/names.txt" | tr -d ' ')"
check 'AC8 screens.sql holds exactly seven queries' '7' "$SCREEN_COUNT"
EXPECTED_SCREENS='Overview
Matrix
Segment
Device
Blocked
Alerts
Map'
check 'AC8 the seven screen names are the roadmap screens' "$EXPECTED_SCREENS" \
    "$(cat "$WORK/screens/names.txt")"

# A plan line may name a table by its alias. Resolve aliases back to their
# table so a scan can never hide behind a short name.
resolve_scans() {
    local qfile="$1" planfile="$2" token table
    grep -oE '(FROM|JOIN)[[:space:]]+[a-z_]+[[:space:]]+AS[[:space:]]+[a-z_]+' "$qfile" |
        awk '{print $4 " " $2}' | sort -u > "$WORK/aliases.txt"
    grep -oE 'SCAN [A-Za-z_][A-Za-z0-9_]*' "$planfile" | awk '{print $2}' | sort -u |
    while read -r token; do
        [ -n "$token" ] || continue
        table="$(awk -v a="$token" '$1 == a { print $2 }' "$WORK/aliases.txt")"
        if [ -z "$table" ]; then
            table="$token"
        fi
        printf -- '%s\n' "$table"
    done
}

for f in "$WORK"/screens/[0-9]*.sql; do
    name="$(head -n 1 "$f" | sed 's/^-- screen: //')"
    rows="$(run_query "$MAIN_DB" "$f" | wc -l | tr -d ' ')"
    if [ "$rows" -ge 1 ]; then
        pass "AC9 the $name query returned $rows rows"
    else
        fail "AC9 the $name query returned no rows"
    fi
    explain_query "$MAIN_DB" "$f" > "$WORK/plan_$name.txt"
    printf -- '  EXPLAIN QUERY PLAN — %s\n' "$name"
    sed 's/^/    /' "$WORK/plan_$name.txt"
    scanned_growing=''
    while read -r table; do
        [ -n "$table" ] || continue
        if grep -qx "$table" "$WORK/growing.txt"; then
            scanned_growing="$scanned_growing $table"
        fi
    done < <(resolve_scans "$f" "$WORK/plan_$name.txt")
    if [ -z "$scanned_growing" ]; then
        pass "AC10 the $name plan scans no growing table"
    else
        fail "AC10 the $name plan scans a growing table:$scanned_growing"
    fi
done

# ===========================================================================
section 'AC11 — the good plans are the indexes, not the data size'
# ===========================================================================
cp "$MAIN_DB" "$INDEX_DB"

# demo_index_drop <screen file> <screen name> <index> <table> <alias>
demo_index_drop() {
    local file="$1" name="$2" index="$3" table="$4" alias="$5" plan="$WORK/plan_${2}_noindex.txt"
    sqlite3 -bail "$INDEX_DB" "DROP INDEX $index;" > /dev/null
    explain_query "$INDEX_DB" "$file" > "$plan"
    printf -- '  %s plan without %s:\n' "$name" "$index"
    sed 's/^/    /' "$plan"
    if grep -qE "SCAN ($table|$alias)\b" "$plan"; then
        pass "AC11 dropping $index turns the $name plan into a scan of $table"
    else
        fail "AC11 the $name plan did not degrade to a scan of $table; $index is not load-bearing"
    fi
}

demo_index_drop "$WORK/screens/01_Overview.sql" 'Overview' 'idx_flow_observed_at' 'flow' 'f'
demo_index_drop "$WORK/screens/06_Alerts.sql" 'Alerts' 'idx_security_event_occurred_at' \
    'security_event' 'se'
demo_index_drop "$WORK/screens/07_Map.sql" 'Map' 'uq_volume_aggregate_24h_slot' \
    'volume_aggregate_24h' 'v'

# ===========================================================================
section 'AC12 — the matrix cell carries all four figures'
# ===========================================================================
MATRIX_COMPLETE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/02_Matrix.sql" \
    'SELECT count(*) FROM (' \
    ') WHERE observed_bytes IS NOT NULL AND allowed_connections IS NOT NULL
        AND blocked_connections IS NOT NULL AND matching_rules IS NOT NULL
        AND blocked_connections > 0;')")"
check_ge 'AC12 a matrix cell carries bytes, allowed, blocked and the matching rules' 1 "$MATRIX_COMPLETE"

# ===========================================================================
section 'AC13, AC14, AC15 — segments, classification and tunnels'
# ===========================================================================
SCOPES="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/01_Overview.sql" \
    "SELECT group_concat(traffic_scope, ',') FROM (SELECT traffic_scope FROM (" \
    ") ORDER BY traffic_scope);")")"
check 'AC13 the Overview query classifies flows east-west and north-south' 'east_west,north_south' "$SCOPES"
check 'AC13 every stored traffic_scope agrees with segment membership' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM flow WHERE traffic_scope <> (CASE
        WHEN src_segment_id IS NOT NULL AND dst_segment_id IS NOT NULL
        THEN 'east_west' ELSE 'north_south' END);")"
expect_sql_failure 'AC13 a traffic_scope disagreeing with segment membership is rejected' "$MAIN_DB" \
    "UPDATE flow SET traffic_scope = 'east_west' WHERE traffic_scope = 'north_south';"

if grep -inE '\b(like|glob|regexp)\b' "$REPO_ROOT"/migrations/*.sql > "$WORK/namematch.txt"; then
    fail "AC14 the DDL contains a name-matching predicate: $(cat "$WORK/namematch.txt")"
else
    pass 'AC14 the DDL contains no LIKE, GLOB or REGEXP predicate at all'
fi
cp "$MAIN_DB" "$WORK/label.db"
sqlite3 "$WORK/label.db" \
    "UPDATE segment SET user_label = 'a label the maintainer chose' WHERE id = 1;" > /dev/null
check 'AC14 relabelling leaves the discovered description untouched' \
    "$(q "$MAIN_DB" 'SELECT discovered_description FROM segment WHERE id = 1;')" \
    "$(q "$WORK/label.db" 'SELECT discovered_description FROM segment WHERE id = 1;')"
check 'AC14 a query returns the user label and the discovered description together' \
    'a label the maintainer chose|discovered-description-1' \
    "$(q "$WORK/label.db" "SELECT user_label || '|' || discovered_description FROM segment WHERE id = 1;")"

check_ge 'AC15 at least one seeded segment is a tunnel' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM segment WHERE is_tunnel = 1;')"
check_ge 'AC15 at least one seeded segment is a VLAN' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM segment WHERE is_tunnel = 0 AND link_kind = 'vlan';")"
check 'AC15 is_tunnel agrees with the discovered link type on every segment' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM segment
        WHERE is_tunnel <> (CASE WHEN discovered_link_type = 'tunnel' THEN 1 ELSE 0 END);")"

# ===========================================================================
section 'AC16, AC17 — device identity and randomised MACs'
# ===========================================================================
check 'AC16 the lease and the flows of one device resolve to a single device row' '1' \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT d.id) FROM device d
        JOIN dhcp_lease l ON l.device_id = d.id
        JOIN flow f ON f.src_device_id = d.id
        WHERE d.identity_kind = 'dhcp_client_id' AND d.id = 1;")"
check_ge 'AC16 a device seen only in flows, with no MAC, is representable and queryable' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM device d
        WHERE d.mac IS NULL AND d.identity_kind = 'address_in_segment'
          AND EXISTS (SELECT 1 FROM flow f WHERE f.src_device_id = d.id)
          AND NOT EXISTS (SELECT 1 FROM dhcp_lease l WHERE l.device_id = d.id);")"
REUSED_ADDRESS="$(q "$MAIN_DB" "SELECT last_address FROM device WHERE id = $((DEVICES + 1));")"
check 'AC16 an address reissued after a lease expiry stays two devices' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM device WHERE last_address = '$REUSED_ADDRESS';")"
check 'AC16 the two reissued identities differ' '2' \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT identity_key) FROM device WHERE last_address = '$REUSED_ADDRESS';")"

check 'AC17 every MAC whose second hex digit is 2, 6, a or e is an unstable identity' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM device
        WHERE mac IS NOT NULL AND instr('26ae', substr(mac, 2, 1)) > 0
          AND unstable_identity <> 1;")"
check 'AC17 no MAC whose second hex digit is 0, 4, 8 or c is an unstable identity' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM device
        WHERE mac IS NOT NULL AND instr('048c', substr(mac, 2, 1)) > 0
          AND unstable_identity <> 0;")"
check_ge 'AC17 the seed contains globally administered MACs to test the negative direction' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM device WHERE mac IS NOT NULL AND instr('048c', substr(mac, 2, 1)) > 0;")"
check_ge 'AC17 two randomised observations with different MACs remain two devices' 2 \
    "$(q "$MAIN_DB" 'SELECT count(DISTINCT id) FROM device WHERE unstable_identity = 1;')"
check 'AC17 every randomised device has its own identity_key' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) - count(DISTINCT identity_key) FROM device WHERE unstable_identity = 1;")"

# ===========================================================================
section 'AC18, AC19 — site-name attribution'
# ===========================================================================
expect_sql_failure 'AC18 an attribution without its resolver lookup is rejected' "$MAIN_DB" \
    "INSERT INTO domain_attribution (flow_id, dns_resolution_id, site_name,
        correlation_delay_seconds, attributed_at)
     VALUES (1, 999999999, 'ac18', 1, $NOW);"
check_ge 'AC18 the delay between the lookup and the flow is stored and queryable' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM domain_attribution WHERE correlation_delay_seconds > 0;')"
check 'AC18 domain_attribution carries no provenance or method column' '' \
    "$(q "$MAIN_DB" "SELECT name FROM pragma_table_info('domain_attribution')
        WHERE lower(name) GLOB '*provenance*' OR lower(name) GLOB '*method*';")"

DEVICE_ROWS_NO_SITE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/04_Device.sql" \
    'SELECT count(*) FROM (' \
    ') WHERE site_name IS NULL AND dst_address IS NOT NULL
        AND country_code IS NOT NULL AND operator IS NOT NULL;')")"
check_ge 'AC19 the Device query returns an unattributed flow with its address, country and operator' \
    1 "$DEVICE_ROWS_NO_SITE"

split_queries "$REPO_ROOT/sql/queries/diagnostics.sql" 'diagnostic' "$WORK/diag"
DIAG_ATTR="$WORK/diag/01_Attribution_rate_per_device.sql"
ATTR_ROWS="$(run_query "$MAIN_DB" "$DIAG_ATTR" | wc -l | tr -d ' ')"
check_ge 'AC19 the documented attribution-rate query runs and returns rows' 1 "$ATTR_ROWS"
printf -- '  attribution rate, first five devices:\n'
run_query "$MAIN_DB" "$DIAG_ATTR" | head -n 5 | sed 's/^/    /'
PARTIAL_ATTR="$(run_query "$MAIN_DB" "$(wrap_query "$DIAG_ATTR" \
    'SELECT count(*) FROM (' \
    ') WHERE attribution_rate_percent > 0 AND attribution_rate_percent < 100;')")"
check_ge 'AC19 the attribution rate is a real rate, strictly between 0 and 100 per cent' 1 "$PARTIAL_ATTR"

# ===========================================================================
section 'AC20, AC21, AC22 — alerts, cache misses and unknown joins'
# ===========================================================================
ALERT_COMPLETE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE signature IS NOT NULL AND severity IS NOT NULL AND severity_state = 'resolved'
        AND src_address IS NOT NULL AND dst_address IS NOT NULL
        AND occurred_at IS NOT NULL AND device_id IS NOT NULL AND segment_id IS NOT NULL;")")"
check_ge 'AC20 a security event joins to a device and a segment and carries signature, severity, endpoints and time' \
    1 "$ALERT_COMPLETE"
# AC20 restated for the provider-neutral core: the assertion is no longer "the
# table has no severity column at all" but the behaviour that assertion
# protected -- no Suricata-attributed row carries an inline severity value, so
# its severity still comes from the rule-info cache and from nowhere else.
SURICATA_EVENTS="$(q "$MAIN_DB" "SELECT count(*) FROM security_event e
    JOIN provider p ON p.id = e.provider_id WHERE p.provider_key = 'suricata';")"
check_ge 'AC20 the seed actually contains Suricata-attributed events to check' 1 "$SURICATA_EVENTS"
check 'AC20 no Suricata-attributed row carries an inline severity value' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM security_event e
        JOIN provider p ON p.id = e.provider_id
        WHERE p.provider_key = 'suricata' AND e.normalised_severity IS NOT NULL;")"
# Every severity the Alerts query resolved equals the cache entry for that
# (provider, rule identity), so it demonstrably came from the cache.
ALERT_SEVERITY_FROM_CACHE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT count(*) FROM (' \
    ") a WHERE a.severity_state = 'resolved' AND a.severity IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM provider_rule_info ri
                        JOIN provider p ON p.id = ri.provider_id
                        WHERE p.provider_key = a.provider_key
                          AND ri.rule_identity = a.rule_identity
                          AND ri.normalised_severity = a.severity);")")"
check 'AC20 every resolved severity came from the rule-info cache' '0' "$ALERT_SEVERITY_FROM_CACHE"
ALERT_UNKNOWN="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE severity_state = 'unknown' AND severity IS NULL;")")"
check_ge 'AC21 an event whose rule identity is not cached is returned with an explicit unknown severity' \
    1 "$ALERT_UNKNOWN"

BLOCKED_UNKNOWN_RULE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/05_Blocked.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE rule_lookup_state = 'not_found' AND rule_description IS NULL AND rid IS NOT NULL;")")"
check_ge 'AC22 a blocked event whose rid matches no rule is returned with an unknown-rule state' \
    1 "$BLOCKED_UNKNOWN_RULE"
BLOCKED_UNKNOWN_IFACE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/05_Blocked.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE interface_lookup_state = 'not_found' AND interface_description IS NULL
        AND interface_device IS NOT NULL;")")"
check_ge 'AC22 a flow whose raw interface name is unmapped is returned with an unknown-interface state' \
    1 "$BLOCKED_UNKNOWN_IFACE"

# ===========================================================================
section 'AC23, AC24 — source availability and the eve.json cursor'
# ===========================================================================
# AC23 restated. It no longer pins a count -- it cannot, once the provider set
# is data -- and is stronger in the other direction: EVERY registered provider
# holds exactly one state, drawn from the three, with a timestamp and a probe,
# and every provider that exists today is registered.
PROVIDER_COUNT="$(q "$MAIN_DB" 'SELECT count(*) FROM provider;')"
check_ge 'AC23 the registry holds every provider surveyed today' 9 "$PROVIDER_COUNT"
check 'AC23 every registered provider holds exactly one availability row' "$PROVIDER_COUNT" \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM source_availability;')"
check 'AC23 no availability row exists for a provider that does not' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM source_availability a
        WHERE NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = a.provider_id);')"
check 'AC23 every state is one of the three modelled values' "$PROVIDER_COUNT" \
    "$(q "$MAIN_DB" "SELECT count(*) FROM source_availability
        WHERE state IN ('reachable', 'present_but_disabled', 'unavailable');")"
check 'AC23 every provider carries a timestamp and the probe that determined it' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM source_availability WHERE probe IS NULL OR checked_at IS NULL;')"
expect_sql_failure 'AC23 an invalid availability state is rejected' "$MAIN_DB" \
    "UPDATE source_availability SET state = 'probably-fine'
     WHERE provider_id = (SELECT min(id) FROM provider);"
check 'AC23 one query returns the current state of every registered provider' "$PROVIDER_COUNT" \
    "$(run_query "$MAIN_DB" "$WORK/diag/03_Source_availability.sql" | wc -l | tr -d ' ')"

expect_sql_failure 'AC24 a duplicate (file_id, byte_offset) watermark is rejected' "$MAIN_DB" \
    "INSERT INTO eve_ingest_cursor (file_id, byte_offset, file_sequence, rotation_state, observed_at)
     SELECT file_id, byte_offset, file_sequence, rotation_state, observed_at
     FROM eve_ingest_cursor LIMIT 1;"
ALERTS_BEFORE="$(q "$MAIN_DB" 'SELECT count(*) FROM security_event;')"
sqlite3 -bail "$MAIN_DB" "INSERT OR IGNORE INTO security_event (provider_id, provider_event_key,
        occurred_at, ingested_at, rule_identity, signature, event_action,
        normalised_severity, src_address, dst_address)
    SELECT provider_id, provider_event_key, occurred_at, ingested_at, rule_identity,
           signature, event_action, normalised_severity, src_address, dst_address
    FROM security_event;" > /dev/null
check 'AC24 replaying the same ingestion leaves the security-event count unchanged' \
    "$ALERTS_BEFORE" "$(q "$MAIN_DB" 'SELECT count(*) FROM security_event;')"
check 'AC24 rotation is representable and queryable' 'current|lost|rotated' \
    "$(q "$MAIN_DB" "SELECT group_concat(rotation_state, '|') FROM
        (SELECT DISTINCT rotation_state FROM eve_ingest_cursor ORDER BY rotation_state);")"

# ===========================================================================
section 'AC25 — NetFlow direction doubling collapses to one volume'
# ===========================================================================
check 'AC25 a direction-doubled pair yields one logical volume, not two' "$PAIR_ROWS" \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM pair_volume_observation;')"
check 'AC25 the de-duplication key is unique and direction-free' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM (SELECT day_start_at, endpoint_low, endpoint_high,
        service_port, protocol FROM pair_volume_observation
        GROUP BY 1, 2, 3, 4, 5 HAVING count(*) > 1);')"
check 'AC25 every stored pair is canonically ordered' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM pair_volume_observation WHERE endpoint_low > endpoint_high;')"

# ===========================================================================
section 'AC26 — timestamps are integer UTC epochs'
# ===========================================================================
TEXT_INSTANTS="$(q "$MAIN_DB" "
    SELECT m.name || '.' || i.name || ' ' || i.type
    FROM sqlite_master m
    JOIN pragma_table_info(m.name) i
    WHERE m.type = 'table' AND i.name GLOB '*_at' AND upper(i.type) <> 'INTEGER';")"
check 'AC26 no instant-carrying column is declared TEXT' '' "$TEXT_INSTANTS"
INSTANT_COLUMNS="$(q "$MAIN_DB" "
    SELECT count(*) FROM sqlite_master m JOIN pragma_table_info(m.name) i
    WHERE m.type = 'table' AND i.name GLOB '*_at';")"
check_ge 'AC26 the schema actually has instant columns to check' 20 "$INSTANT_COLUMNS"
# Restated against the registry: the availability row is keyed by provider_id
# since this cycle replaced the closed `source` enum with a foreign key.
expect_sql_failure 'AC26 a negative instant is rejected by a constraint' "$MAIN_DB" \
    "UPDATE source_availability SET checked_at = -1
     WHERE provider_id = (SELECT min(id) FROM provider);"
expect_sql_failure 'AC26 a millisecond value stored as seconds is rejected by a constraint' "$MAIN_DB" \
    "UPDATE source_availability SET checked_at = 1750000000000
     WHERE provider_id = (SELECT min(id) FROM provider);"

# ===========================================================================
section 'AC27, AC28, AC29 — aggregates, freshness and aggregate mode'
# ===========================================================================
run_query "$MAIN_DB" "$WORK/diag/02_Aggregate_coverage_per_period.sql" > "$WORK/coverage.txt"
printf -- '  aggregate coverage:\n'
sed 's/^/    /' "$WORK/coverage.txt"
check 'AC27 the aggregate coverage query returns one row per period' '4' \
    "$(wc -l < "$WORK/coverage.txt" | tr -d ' ')"
EMPTY_PERIODS="$(awk -F'|' '$2 + 0 == 0 { print $1 }' "$WORK/coverage.txt")"
check 'AC27 every one of the four periods is non-empty' '' "$EMPTY_PERIODS"
for t in volume_aggregate_1h volume_aggregate_24h volume_aggregate_7d volume_aggregate_30d; do
    check "AC28 every $t row carries a freshness timestamp" '0' \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $t WHERE computed_at IS NULL;")"
done
if grep -q 'Refresh contract' "$DOC"; then
    pass 'AC28 the document states the refresh contract step 4 must honour'
else
    fail 'AC28 the document does not state a refresh contract'
fi
if grep -qE 'dns_resolution|domain_attribution' "$WORK/screens/07_Map.sql"; then
    fail 'AC29 the aggregate-mode query reads a table holding domain names'
else
    pass 'AC29 the aggregate-mode query reads no table holding a domain name'
fi
check_ge 'AC29 the aggregate-mode query returns rows' 1 \
    "$(run_query "$MAIN_DB" "$WORK/screens/07_Map.sql" | wc -l | tr -d ' ')"

# ===========================================================================
section 'AC30, AC31, AC32 — retention and purge'
# ===========================================================================
check 'AC30 the retention horizon is stored in the database and defaults to 90 days in seconds' \
    '7776000' "$(q "$MAIN_DB" "SELECT value FROM setting WHERE key = 'retention_seconds';")"
if grep -nE '7776000|\b90[[:space:]]*days?\b' \
        "$REPO_ROOT/sql/queries/screens.sql" "$REPO_ROOT/sql/queries/diagnostics.sql" \
        "$REPO_ROOT/sql/purge.sql" > "$WORK/retention_literals.txt"; then
    fail "AC30 a retention duration appears as a literal: $(cat "$WORK/retention_literals.txt")"
else
    pass 'AC30 no retention duration appears as a literal in any query or in the purge'
fi

run_purge() {
    local db="$1"
    {
        printf -- '.bail on\n'
        printf -- '.param init\n'
        printf -- '.param set :now %s\n' "$NOW"
        printf -- '.read %s\n' "$REPO_ROOT/sql/purge.sql"
    } | sqlite3 "$db"
}

cp "$MAIN_DB" "$PURGE_DB"
HORIZON_HOURS=$((6 * 3600))
sqlite3 "$PURGE_DB" "UPDATE setting SET value = '$HORIZON_HOURS' WHERE key = 'retention_seconds';" > /dev/null
check 'AC30 the horizon is settable to a value of hours' "$HORIZON_HOURS" \
    "$(q "$PURGE_DB" "SELECT value FROM setting WHERE key = 'retention_seconds';")"
sqlite3 "$PURGE_DB" "UPDATE setting SET value = '7776000' WHERE key = 'retention_seconds';" > /dev/null

CUTOFF=$((NOW - 7776000))
PURGEABLE='flow:observed_at dns_resolution:looked_up_at security_event:occurred_at
dhcp_lease:observed_at device:last_seen_at pair_volume_observation:day_start_at
geo_asn:looked_up_at domain_attribution:attributed_at
volume_aggregate_1h:period_end_at volume_aggregate_24h:period_end_at
volume_aggregate_7d:period_end_at volume_aggregate_30d:period_end_at'
for t in $PURGEABLE; do
    tbl="${t%%:*}"
    col="${t##*:}"
    before_old="$(q "$PURGE_DB" "SELECT count(*) FROM $tbl WHERE $col < $CUTOFF;")"
    if [ "$before_old" -eq 0 ]; then
        fail "AC31 the seed has nothing older than the horizon in $tbl, so the purge proves nothing"
    else
        pass "AC31 $tbl holds $before_old rows older than the horizon before the purge"
    fi
done

BEFORE_NEW_FLOW="$(q "$PURGE_DB" "SELECT count(*) FROM flow WHERE observed_at >= $CUTOFF;")"
BEFORE_NEW_ALERT="$(q "$PURGE_DB" "SELECT count(*) FROM security_event WHERE occurred_at >= $CUTOFF;")"
run_purge "$PURGE_DB"
for t in $PURGEABLE; do
    tbl="${t%%:*}"
    col="${t##*:}"
    check "AC31 the purge removed every $tbl row older than the horizon" '0' \
        "$(q "$PURGE_DB" "SELECT count(*) FROM $tbl WHERE $col < $CUTOFF;")"
done
check 'AC31 the purge removed no flow newer than the horizon' "$BEFORE_NEW_FLOW" \
    "$(q "$PURGE_DB" "SELECT count(*) FROM flow WHERE observed_at >= $CUTOFF;")"
check 'AC31 the purge removed no security event newer than the horizon' "$BEFORE_NEW_ALERT" \
    "$(q "$PURGE_DB" "SELECT count(*) FROM security_event WHERE occurred_at >= $CUTOFF;")"
check 'AC31 the purge left the registry and the rule-info cache untouched' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM provider;')|$(q "$MAIN_DB" 'SELECT count(*) FROM provider_rule_info;')" \
    "$(q "$PURGE_DB" 'SELECT count(*) FROM provider;')|$(q "$PURGE_DB" 'SELECT count(*) FROM provider_rule_info;')"
check 'AC32 the purge leaves the foreign keys consistent' '' \
    "$(q "$PURGE_DB" 'PRAGMA foreign_key_check;')"
check 'AC32 the purge leaves no attribution without its lookup or its flow' '0' \
    "$(q "$PURGE_DB" 'SELECT count(*) FROM domain_attribution a
        WHERE NOT EXISTS (SELECT 1 FROM flow f WHERE f.id = a.flow_id)
           OR NOT EXISTS (SELECT 1 FROM dns_resolution r WHERE r.id = a.dns_resolution_id);')"
run_query "$PURGE_DB" "$WORK/diag/02_Aggregate_coverage_per_period.sql" > "$WORK/coverage_after.txt"
printf -- '  aggregate coverage after the purge:\n'
sed 's/^/    /' "$WORK/coverage_after.txt"
EMPTY_AFTER="$(awk -F'|' '$2 + 0 == 0 { print $1 }' "$WORK/coverage_after.txt")"
check 'AC32 every period the document says survives a purge is still queryable' '' "$EMPTY_AFTER"

cp "$MAIN_DB" "$UNLIMITED_DB"
sqlite3 "$UNLIMITED_DB" "UPDATE setting SET value = '0' WHERE key = 'retention_seconds';" > /dev/null
table_counts "$UNLIMITED_DB" > "$WORK/counts_unlimited_before.txt"
run_purge "$UNLIMITED_DB"
table_counts "$UNLIMITED_DB" > "$WORK/counts_unlimited_after.txt"
if diff -u "$WORK/counts_unlimited_before.txt" "$WORK/counts_unlimited_after.txt" > "$WORK/unlimited.diff"; then
    pass 'AC31 with an unlimited horizon the purge removes nothing'
else
    fail "AC31 an unlimited horizon still deleted rows: $(cat "$WORK/unlimited.diff")"
fi

# ===========================================================================
section 'AC33 — geo and ASN enrichment'
# ===========================================================================
check 'AC33 geo/ASN is keyed per address' 'address' \
    "$(q "$MAIN_DB" "SELECT name FROM pragma_table_info('geo_asn') WHERE pk = 1;")"
check 'AC33 a resolved lookup carries the dataset build date' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn WHERE lookup_state = 'resolved' AND dataset_build_at IS NULL;")"
check_ge 'AC33 a cache miss is a modelled row, not an absent one' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn WHERE lookup_state = 'miss';")"
MAP_MISS="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/07_Map.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE geo_lookup_state = 'miss';")")"
check_ge 'AC33 the Map query returns the cache miss rather than dropping its volume' 1 "$MAP_MISS"

# ===========================================================================
section 'AC34 — no address, CIDR or discovered name as a literal'
# ===========================================================================
CYCLE_FILES="$REPO_ROOT/migrations/0001_core.sql
$REPO_ROOT/migrations/0002_aggregates_and_defaults.sql
$REPO_ROOT/sql/queries/screens.sql
$REPO_ROOT/sql/queries/diagnostics.sql
$REPO_ROOT/sql/seed.sql
$REPO_ROOT/sql/purge.sql
$REPO_ROOT/sql/schema-checks.sh
$REPO_ROOT/docs/data-model.md
$REPO_ROOT/docs/architecture.md"
DOTTED_QUAD='[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(/[0-9]{1,2})?'
: > "$WORK/literals.txt"
while IFS= read -r f; do
    [ -n "$f" ] || continue
    grep -nE "$DOTTED_QUAD" "$f" >> "$WORK/literals.txt" 2>/dev/null
done <<< "$CYCLE_FILES"
if [ -s "$WORK/literals.txt" ]; then
    fail "AC34 an address or CIDR literal was found: $(cat "$WORK/literals.txt")"
else
    pass 'AC34 no dotted-quad or CIDR literal in any file this cycle adds'
fi
check 'AC34 every seeded address was synthesised from a counter at generation time' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM flow WHERE src_address IS NULL OR dst_address IS NULL;")"

# ===========================================================================
section 'AC35 — no assumed segment, device or interface count'
# ===========================================================================
apply_migrations "$ALT_DB" "$WORK/migrate_alt.err" || fail 'AC35 the alternative database failed to migrate'
seed_database "$ALT_DB" "$ALT_SEGMENTS" "$ALT_DEVICES" "$ALT_RULES" "$ALT_FLOW_ROWS" \
    "$ALT_ALERTS" "$ALT_PAIR_ROWS"
check 'AC35 the alternative seed has a different segment count' "$ALT_SEGMENTS" \
    "$(q "$ALT_DB" 'SELECT count(*) FROM segment;')"
check 'AC35 the alternative database is consistent' '' "$(q "$ALT_DB" 'PRAGMA foreign_key_check;')"
ALT_SEGMENT_ID=$SEGMENT_ID
ALT_DEVICE_ID=$DEVICE_ID
for f in "$WORK"/screens/[0-9]*.sql; do
    name="$(head -n 1 "$f" | sed 's/^-- screen: //')"
    rows="$(run_query "$ALT_DB" "$f" | wc -l | tr -d ' ')"
    if [ "$rows" -ge 1 ]; then
        pass "AC35 the $name query returned $rows rows against the alternative seed"
    else
        fail "AC35 the $name query returned no rows against the alternative seed"
    fi
done
printf -- '  alternative seed used segments=%s devices=%s rules=%s flows=%s (segment_id=%s device_id=%s)\n' \
    "$ALT_SEGMENTS" "$ALT_DEVICES" "$ALT_RULES" "$ALT_FLOW_ROWS" "$ALT_SEGMENT_ID" "$ALT_DEVICE_ID"

# ===========================================================================
section 'AC36, AC37, AC42 — the model document'
# ===========================================================================
for needle in 'opnsense-api-survey.md' 'interfaces_info' 'get_interface_names' 'search_rule' \
              'query_alerts' 'get_rule_info' 'FlowSourceAddrDetails' 'search_queries' \
              'leases4/search' 'firewall/log'; do
    if grep -qF "$needle" "$DOC"; then
        pass "AC36 the document cites $needle"
    else
        fail "AC36 the document does not cite $needle"
    fi
done
for needle in 'Observation-point limit' 'inferred' 'resolver correlation' 'availability state'; do
    if grep -qF "$needle" "$DOC"; then
        pass "AC37 the document states: $needle"
    else
        fail "AC37 the document does not state: $needle"
    fi
done
# The line defining the pattern is skipped in every file, because it holds the
# French words themselves and would otherwise match this script.
FRENCH='\b(le|la|les|des|une|est|sont|pour|avec|cette|dans|nous|vous|mais|donc|ainsi|aucun|chaque|toujours|jamais|fichier|requête|données|réseau|serveur|adresse)\b'
: > "$WORK/french.txt"
while IFS= read -r f; do
    [ -n "$f" ] || continue
    grep -v '^FRENCH=' "$f" | grep -nEi "$FRENCH" >> "$WORK/french.txt" 2>/dev/null
done <<< "$CYCLE_FILES"
if [ -s "$WORK/french.txt" ]; then
    fail "AC42 a French word was found: $(head -n 5 "$WORK/french.txt")"
else
    pass 'AC42 no French word in any file this cycle touches'
fi

# ===========================================================================
section 'AC39, AC41, AC45 — allowlist, line endings and stray databases'
# ===========================================================================
SH_ALLOWLIST="$(sed -n "s/^ALLOWED_TOOLS='\(.*\)'$/\1/p" "$REPO_ROOT/ci/factory.sh")"
PS_ALLOWLIST="$(sed -n "s/^\\\$allowedTools = '\(.*\)'$/\1/p" "$REPO_ROOT/ci/factory.ps1")"
check 'AC39 the two allowlist strings are identical' "$SH_ALLOWLIST" "$PS_ALLOWLIST"
check 'AC39 the allowlist grants exactly the two documented invocations' \
    'Bash(docker compose run --rm checks)
Bash(docker compose run --rm schema-checks)' \
    "$(printf -- '%s' "$SH_ALLOWLIST" | tr ',' '\n' | grep '^Bash(docker' | sort)"
if printf -- '%s' "$SH_ALLOWLIST" | grep -qE 'Bash\(docker compose \*|Bash\(docker \*'; then
    fail 'AC39 the allowlist grants a docker wildcard'
else
    pass 'AC39 the allowlist grants no docker wildcard'
fi

: > "$WORK/crlf.txt"
while IFS= read -r f; do
    [ -n "$f" ] || continue
    case "$f" in
        *.sh|*.sql)
            if grep -q $'\r' "$f"; then
                printf -- '%s\n' "$f" >> "$WORK/crlf.txt"
            fi
            ;;
    esac
done <<< "$CYCLE_FILES"
if [ -s "$WORK/crlf.txt" ]; then
    fail "AC41 a file is not LF-terminated: $(cat "$WORK/crlf.txt")"
else
    pass 'AC41 every .sh and .sql file this cycle adds is LF-terminated'
fi
if bash -n "$REPO_ROOT/sql/schema-checks.sh"; then
    pass 'AC41 bash -n on the harness exits 0'
else
    fail 'AC41 bash -n on the harness failed'
fi

STRAY="$(find "$REPO_ROOT" -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' | head -n 5)"
check 'AC45 the bind-mounted working tree holds no database file' '' "$STRAY"
DB_LOCATION="$(find "$DATA_DIR" -maxdepth 1 -name 'schema-checks-*.db' | wc -l | tr -d ' ')"
check_ge 'AC45 every database a check created lives under the data directory' 1 "$DB_LOCATION"

# ===========================================================================
# The criteria of specs/SPEC-provider-neutral-schema.md follow, labelled
# PN-AC*. The criteria above are those of specs/SPEC-data-model-sqlite-schema.md,
# restated against the renamed objects where this cycle renamed one.
# ===========================================================================

ARCH_DOC="$REPO_ROOT/docs/architecture.md"

# ===========================================================================
section 'PN-AC7, PN-AC20, PN-AC26 — no provider name is an identifier'
# ===========================================================================
# PN-AC7: registering a provider is an INSERT, not a migration, so no CHECK
# anywhere enumerates the source names the closed enum used to hold.
q "$MAIN_DB" "SELECT ifnull(sql, '') FROM sqlite_master;" > "$WORK/schema_sql.txt"
SOURCE_ENUM_HITS=''
for needle in filter_log suricata_eve netflow_insight dhcp_leases resolver_dns; do
    if grep -qF "'$needle'" "$WORK/schema_sql.txt"; then
        SOURCE_ENUM_HITS="$SOURCE_ENUM_HITS $needle"
    fi
done
check 'PN-AC7 no CHECK in the live schema enumerates a source name' '' "$SOURCE_ENUM_HITS"

# PN-AC20: no provider name in any table name, index name or column name.
PROVIDER_NAME_IDENTIFIERS="$(q "$MAIN_DB" "
    SELECT m.name || '.' || ifnull(i.name, '(object)')
    FROM sqlite_master m
    LEFT JOIN pragma_table_info(m.name) i
    WHERE lower(m.name) GLOB '*maxmind*' OR lower(m.name) GLOB '*crowdsec*'
       OR lower(m.name) GLOB '*zenarmor*' OR lower(m.name) GLOB '*suricata*'
       OR lower(i.name) GLOB '*maxmind*' OR lower(i.name) GLOB '*crowdsec*'
       OR lower(i.name) GLOB '*zenarmor*' OR lower(i.name) GLOB '*suricata*';")"
check 'PN-AC20 no provider name appears in any table, index or column name' '' \
    "$PROVIDER_NAME_IDENTIFIERS"
check_ge 'PN-AC20 a provider name does appear as a value, in the registry' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE provider_key GLOB '*maxmind*'
        OR provider_key = 'suricata';")"

# PN-AC26: nothing anywhere is named for a provider that is not installed and
# surveyed today. The list is every security or visibility product the roadmap
# discussion raised and the survey did not cover.
#
# Scope: the migrations, the SQL, and the two documents that describe the model
# -- docs/data-model.md and docs/architecture.md. It deliberately does NOT scan
# docs/ as a directory, and deliberately does not scan specs/ at all.
#
# The reason is that the property being asserted is a property of the *model*:
# no table, column, index, CHECK value, registry row or provider key exists for
# a product nobody has surveyed. Research documents and specifications
# legitimately *name* surveyed products -- docs/ui-references.md studies ntopng
# and Zenarmor as prior art, and the spec that commissioned it requires exactly
# that -- and naming a product one is studying is the opposite of building an
# abstraction for it. Widening the scope back to docs/ or to specs/ would make
# the check assert a model property by grepping prose, which is what made it
# fail on a document that violated nothing.
: > "$WORK/speculative.txt"
# This script is excluded from its own grep: it is the file that has to name
# those strings in order to forbid them, and matching itself would make the
# check unfalsifiable rather than strict.
grep -rinE --exclude='schema-checks.sh' 'crowdsec|zenarmor|sensei|snort|wazuh|ntopng' \
    "$REPO_ROOT/migrations" "$REPO_ROOT/sql" \
    "$REPO_ROOT/docs/data-model.md" "$REPO_ROOT/docs/architecture.md" \
    >> "$WORK/speculative.txt" 2>/dev/null
if [ -s "$WORK/speculative.txt" ]; then
    fail "PN-AC26 a provider nobody surveyed is named: $(head -n 3 "$WORK/speculative.txt")"
else
    pass 'PN-AC26 no unsurveyed provider is named in migrations, sql or docs'
fi

# ===========================================================================
section 'PN-AC9, PN-AC10, PN-AC12 — a freshly migrated, unseeded database'
# ===========================================================================
apply_migrations "$FRESH_DB" "$WORK/migrate_fresh.err" || fail 'PN-AC10 the fresh database failed to migrate'
FRESH_PROVIDERS="$(q "$FRESH_DB" 'SELECT count(*) FROM provider;')"
check_ge 'PN-AC10 the fresh database registers every provider surveyed today' 9 "$FRESH_PROVIDERS"
check 'PN-AC10 every registry row has exactly one availability row' "$FRESH_PROVIDERS" \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM source_availability;')"
check 'PN-AC10 no availability row references a registry row that does not exist' '0' \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM source_availability a
        WHERE NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = a.provider_id);')"
check 'PN-AC10 every availability row is in the not-yet-probed unavailable state' "$FRESH_PROVIDERS" \
    "$(q "$FRESH_DB" "SELECT count(*) FROM source_availability
        WHERE state = 'unavailable' AND probe = 'not_yet_probed';")"
expect_sql_failure 'PN-AC10 an availability row for a provider that does not exist is rejected' "$FRESH_DB" \
    "INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
     VALUES (999999999, 'reachable', 'pn-ac10', NULL, $NOW);"

check 'PN-AC12 no provider is active on a freshly migrated database' '0' \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM provider WHERE is_active = 1;')"

expect_sql_failure 'PN-AC9 a registry row whose kind is outside the six is rejected' "$FRESH_DB" \
    "INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
     VALUES ('telepathy', 'pn-ac9', 'PN-AC9', 0, $NOW);"
extract_block "$ARCH_DOC" '<!-- provider-kinds:begin -->' '<!-- provider-kinds:end -->' |
    sort > "$WORK/doc_kinds.txt"
check 'PN-AC9 the document lists exactly six kinds' '6' \
    "$(wc -l < "$WORK/doc_kinds.txt" | tr -d ' ')"
q "$MAIN_DB" 'SELECT DISTINCT kind FROM provider ORDER BY kind;' | sort > "$WORK/db_kinds.txt"
if diff -u "$WORK/doc_kinds.txt" "$WORK/db_kinds.txt" > "$WORK/kinds.diff"; then
    pass 'PN-AC9 the documented kinds and the live kinds are identical'
else
    fail "PN-AC9 the kind lists differ: $(cat "$WORK/kinds.diff")"
fi

# ===========================================================================
section 'PN-AC11, PN-AC12, PN-AC13 — several providers per kind, one active'
# ===========================================================================
for kind in dns_lookup dhcp_lease; do
    check_ge "PN-AC11 the $kind kind has several providers" 2 \
        "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE kind = '$kind';")"
done
printf -- '  the registry:\n'
q "$MAIN_DB" 'SELECT kind, provider_key, is_active FROM provider ORDER BY kind, provider_key;' |
    sed 's/^/    /'

KIND_COUNT="$(q "$MAIN_DB" 'SELECT count(DISTINCT kind) FROM provider;')"
check 'PN-AC12 the seed makes exactly one provider active per kind' "$KIND_COUNT" \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM provider WHERE is_active = 1;')"
check 'PN-AC12 no kind has two active providers' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM (SELECT kind FROM provider WHERE is_active = 1
        GROUP BY kind HAVING count(*) > 1);')"
expect_sql_failure 'PN-AC12 marking a second provider of the same kind active is rejected' "$MAIN_DB" \
    "UPDATE provider SET is_active = 1
     WHERE id = (SELECT p.id FROM provider p
                 JOIN provider q ON q.kind = p.kind AND q.is_active = 1
                 WHERE p.is_active = 0 LIMIT 1);"

DIAG_AVAIL="$WORK/diag/03_Source_availability.sql"
run_query "$MAIN_DB" "$DIAG_AVAIL" > "$WORK/availability.txt"
printf -- '  provider availability diagnostic:\n'
sed 's/^/    /' "$WORK/availability.txt"
check 'PN-AC13 the diagnostic returns one row per registry row' "$PROVIDER_COUNT" \
    "$(wc -l < "$WORK/availability.txt" | tr -d ' ')"
check 'PN-AC13 every diagnostic row carries a kind, a key, a state, a probe and an active flag' '0' \
    "$(awk -F'|' 'NF < 8 || $1 == "" || $2 == "" || $4 == "" || $5 == "" || $8 == "" { n++ }
                  END { print n + 0 }' "$WORK/availability.txt")"
check 'PN-AC13 the diagnostic names the active provider of every kind' "$KIND_COUNT" \
    "$(awk -F'|' '$8 == 1 { n++ } END { print n + 0 }' "$WORK/availability.txt")"

# ===========================================================================
section 'PN-AC8, PN-AC18 — registering a second provider of an existing kind'
# ===========================================================================
# No DDL: an INSERT into the registry, an INSERT into availability, and events.
cp "$MAIN_DB" "$REGISTER_DB"
expect_sql_success 'PN-AC8 registering a second security-event provider needs no DDL' "$REGISTER_DB" \
    "INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
       VALUES ('security_event', 'second-event-provider', 'Second event provider', 0, $NOW);
     INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
       SELECT id, 'reachable', 'pn-ac8-probe', NULL, $NOW
       FROM provider WHERE provider_key = 'second-event-provider';
     INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
         rule_identity, signature, event_action, normalised_severity,
         src_address, src_port, dst_address, dst_port, protocol,
         src_device_id, src_segment_id)
       SELECT p.id, 'pn-ac8-event-' || k.n, $WINDOW_END - 60, $WINDOW_END - 30,
              'named-rule-identity-alpha', 'second provider signature', 'blocked',
              CASE WHEN k.n = 1 THEN 'critical' END,
              e.src_address, e.src_port, e.dst_address, e.dst_port,
              e.protocol, e.src_device_id, e.src_segment_id
       FROM provider p, security_event e, (SELECT 1 AS n UNION ALL SELECT 2) k
       WHERE p.provider_key = 'second-event-provider' AND e.id = 1;
     INSERT INTO provider_rule_info (provider_id, rule_identity, normalised_severity,
         provider_severity, category, rule_source, fetched_at)
       SELECT id, 'named-rule-identity-alpha', 'informational', 'minor',
              'pn-ac8-category', 'pn-ac8-source', $NOW
       FROM provider WHERE provider_key = 'second-event-provider';"
check 'PN-AC8 the second provider is registered with no schema change' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM sqlite_master;')" \
    "$(q "$REGISTER_DB" 'SELECT count(*) FROM sqlite_master;')"
ALERT_PROVIDERS="$(run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT count(DISTINCT provider_key) FROM (' \
    ');')")"
check 'PN-AC8 the Alerts query returns rows attributed to both providers' '2' "$ALERT_PROVIDERS"
printf -- '  Alerts rows per provider after registering a second one:\n'
run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT provider_key, count(*) FROM (' \
    ') GROUP BY provider_key ORDER BY provider_key;')" | sed 's/^/    /'

check 'PN-AC18 the same rule identity under two providers is two cache rows' '2' \
    "$(q "$REGISTER_DB" "SELECT count(*) FROM provider_rule_info
        WHERE rule_identity = 'named-rule-identity-alpha';")"
check 'PN-AC18 both cache rows are retrievable and differ by provider' '2' \
    "$(q "$REGISTER_DB" "SELECT count(DISTINCT provider_id) FROM provider_rule_info
        WHERE rule_identity = 'named-rule-identity-alpha';")"
# One event of the second provider ships its own severity inline ('critical');
# the other has none and must resolve through the SECOND provider's cache entry
# ('informational'), while the Suricata events on the very same rule identity
# resolve through Suricata's own entry. Distinct answers from one rule identity
# are what "keyed per provider" means.
SEVERITIES_BY_PROVIDER="$(run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    "SELECT group_concat(provider_key || '=' || severity, ',') FROM (SELECT DISTINCT provider_key, severity FROM (" \
    ") WHERE rule_identity = 'named-rule-identity-alpha' ORDER BY provider_key, severity);")")"
check 'PN-AC18 each event resolves its severity through its own provider entry' \
    'second-event-provider=critical,second-event-provider=informational,suricata=low' \
    "$SEVERITIES_BY_PROVIDER"

# ===========================================================================
section 'PN-AC14, PN-AC15, PN-AC16, PN-AC17, PN-AC19 — the security-event core'
# ===========================================================================
check 'PN-AC14 security_event carries no ingestion-transport column' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM pragma_table_info('security_event')
        WHERE lower(name) GLOB '*file*' OR lower(name) GLOB '*pos*' OR lower(name) GLOB '*offset*';")"
check 'PN-AC14 no provider-named side table exists anywhere' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM sqlite_master
        WHERE lower(name) GLOB '*suricata*' OR lower(name) GLOB '*maxmind*'
           OR lower(name) GLOB '*unbound*' OR lower(name) GLOB '*dnsmasq*'
           OR lower(name) GLOB '*kea*' OR lower(name) GLOB '*insight*';")"
check 'PN-AC14 the ingestion coordinate still lives in the cursor table' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('eve_ingest_cursor')
        WHERE name IN ('file_id', 'byte_offset');")"

check 'PN-AC15 the security-event uniqueness constraint has exactly two columns' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_index_list('security_event') il
        JOIN pragma_index_info(il.name) ii WHERE il.origin = 'u';")"
check 'PN-AC15 they are the provider and the provider event key' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_index_list('security_event') il
        JOIN pragma_index_info(il.name) ii WHERE il.origin = 'u'
          AND ii.name IN ('provider_id', 'provider_event_key');")"
expect_sql_failure 'PN-AC15 a duplicate (provider_id, provider_event_key) is rejected' "$MAIN_DB" \
    "INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
        rule_identity, signature, event_action, src_address, dst_address)
     SELECT provider_id, provider_event_key, occurred_at, ingested_at, rule_identity,
            signature, event_action, src_address, dst_address
     FROM security_event LIMIT 1;"

check 'PN-AC16 the rule identity is TEXT on the security-event core' 'TEXT' \
    "$(q "$MAIN_DB" "SELECT type FROM pragma_table_info('security_event') WHERE name = 'rule_identity';")"
check 'PN-AC16 the rule identity is TEXT on the rule-info cache' 'TEXT' \
    "$(q "$MAIN_DB" "SELECT type FROM pragma_table_info('provider_rule_info') WHERE name = 'rule_identity';")"
check_ge 'PN-AC16 the seed contains an event whose rule identity is non-numeric' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM security_event
        WHERE CAST(rule_identity AS INTEGER) = 0 AND rule_identity <> '0';")"
NON_NUMERIC_RESOLVED="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
    'SELECT count(*) FROM (' \
    ") WHERE CAST(rule_identity AS INTEGER) = 0 AND rule_identity <> '0'
        AND severity_state = 'resolved' AND severity IS NOT NULL;")")"
check_ge 'PN-AC16 the Alerts query returns it with its severity resolved from the cache' \
    1 "$NON_NUMERIC_RESOLVED"

check 'PN-AC17 every seeded Suricata-attributed event carries a NULL normalised severity' \
    "$SURICATA_EVENTS" \
    "$(q "$MAIN_DB" "SELECT count(*) FROM security_event e JOIN provider p ON p.id = e.provider_id
        WHERE p.provider_key = 'suricata' AND e.normalised_severity IS NULL;")"
check_ge 'PN-AC17 the Alerts query returns resolved severities from the cache' 1 "$ALERT_COMPLETE"
check_ge 'PN-AC17 the Alerts query returns unknown severities for uncached identities' 1 "$ALERT_UNKNOWN"

for t in security_event provider_rule_info; do
    expect_sql_failure "PN-AC19 a severity outside the vocabulary is rejected on $t" "$MAIN_DB" \
        "UPDATE $t SET normalised_severity = 'catastrophic';"
done
check 'PN-AC19 every stored normalised severity is in the vocabulary' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(DISTINCT normalised_severity) FROM provider_rule_info
        WHERE normalised_severity NOT IN ('critical', 'high', 'medium', 'low', 'informational');")"
for needle in 'critical' 'high' 'medium' 'low' 'informational'; do
    if grep -qF "$needle" "$DOC"; then
        pass "PN-AC19 the document states the severity level $needle"
    else
        fail "PN-AC19 the document does not state the severity level $needle"
    fi
done
if grep -qF 'Suricata numeric scale' "$DOC"; then
    pass 'PN-AC19 the document gives the mapping from the Suricata numeric scale'
else
    fail 'PN-AC19 the document does not give the mapping from the Suricata numeric scale'
fi

# ===========================================================================
section 'PN-AC21 — stale-dataset visibility survives the rename'
# ===========================================================================
check 'PN-AC21 every resolved geo row carries a dataset build date' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn
        WHERE lookup_state = 'resolved' AND dataset_build_at IS NULL;")"
check 'PN-AC21 every resolved geo row names the provider that supplied it' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn
        WHERE lookup_state = 'resolved' AND provider_id IS NULL;")"
expect_sql_failure 'PN-AC21 a resolved geo row with no dataset build date is rejected' "$MAIN_DB" \
    "INSERT INTO geo_asn (address, provider_id, lookup_state, country_code, looked_up_at)
     SELECT 'pn-ac21-a', id, 'resolved', 'ZZ', $NOW FROM provider WHERE kind = 'geo_asn' LIMIT 1;"
expect_sql_failure 'PN-AC21 a resolved geo row naming no provider is rejected' "$MAIN_DB" \
    "INSERT INTO geo_asn (address, provider_id, lookup_state, country_code,
         dataset_build_at, looked_up_at)
     VALUES ('pn-ac21-b', NULL, 'resolved', 'ZZ', $NOW, $NOW);"
check_ge 'PN-AC21 a seeded cache miss still exists' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn WHERE lookup_state = 'miss';")"
check_ge 'PN-AC21 the Map query still returns the cache miss' 1 "$MAP_MISS"

# ===========================================================================
section 'PN-AC22 — migration discipline'
# ===========================================================================
check 'PN-AC22 migrations/ holds exactly two .sql files' '2' \
    "$(find "$REPO_ROOT/migrations" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')"
check 'PN-AC22 no 0003 migration exists' '' \
    "$(find "$REPO_ROOT/migrations" -maxdepth 1 -name '0003*' | head -n 1)"
check 'PN-AC22 schema_version holds exactly two rows after a fresh apply' '2' \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM schema_version;')"

# ===========================================================================
section 'PN-AC24 — the architecture document'
# ===========================================================================
if [ -f "$ARCH_DOC" ]; then
    pass 'PN-AC24 docs/architecture.md exists'
else
    fail 'PN-AC24 docs/architecture.md does not exist'
fi
while read -r kind; do
    [ -n "$kind" ] || continue
    if grep -qF "$kind" "$ARCH_DOC"; then
        pass "PN-AC24 the architecture document names the $kind kind"
    else
        fail "PN-AC24 the architecture document does not name the $kind kind"
    fi
done < "$WORK/db_kinds.txt"
for needle in 'declares its own availability' \
              'modelled state, not an absence of rows' \
              'at most one provider per kind is active' \
              'surveyed exactly as the OPNsense API was surveyed'; do
    if grep -qiF "$needle" "$ARCH_DOC"; then
        pass "PN-AC24 the architecture document states: $needle"
    else
        fail "PN-AC24 the architecture document does not state: $needle"
    fi
done
if grep -qF 'no plugin system' "$ARCH_DOC"; then
    pass 'PN-AC24 the architecture document describes no plugin mechanism'
else
    fail 'PN-AC24 the architecture document does not rule a plugin mechanism out'
fi
: > "$WORK/plugin.txt"
grep -inE 'dynamic(ally)? load|shared object|\.so\b|manifest file|external process' \
    "$ARCH_DOC" | grep -viE 'no |never |not ' >> "$WORK/plugin.txt" 2>/dev/null
if [ -s "$WORK/plugin.txt" ]; then
    fail "PN-AC24 the architecture document specifies a loading mechanism: $(head -n 2 "$WORK/plugin.txt")"
else
    pass 'PN-AC24 the architecture document specifies no dynamic-loading mechanism'
fi

# ===========================================================================
section 'PN-AC25 — the restructured tables cite their endpoints'
# ===========================================================================
for needle in 'query_alerts' 'get_rule_info' 'leases4/search' 'search_queries' \
              'firewall/log' 'networkinsight'; do
    if grep -qF "$needle" "$REPO_ROOT/migrations/0001_core.sql"; then
        pass "PN-AC25 the DDL cites $needle where it is consumed"
    else
        fail "PN-AC25 the DDL does not cite $needle"
    fi
done
for needle in 'availability state' 'never rendered as an absence of data' 'GeoLite2'; do
    if grep -qF "$needle" "$DOC"; then
        pass "PN-AC25 the model document states: $needle"
    else
        fail "PN-AC25 the model document does not state: $needle"
    fi
done

# ===========================================================================
section 'PN-AC29 — covering indexes'
# ===========================================================================
awk '
    index($0, "<!-- covering-queries:begin -->") == 1 { inside = 1; next }
    index($0, "<!-- covering-queries:end -->") == 1 { inside = 0 }
    inside && NF && index($0, "```") != 1 { print $1, $2 }
' "$DOC" > "$WORK/covering.txt"
check_ge 'PN-AC29 the document claims at least one covered query' 1 \
    "$(wc -l < "$WORK/covering.txt" | tr -d ' ')"
while read -r cname cindex; do
    [ -n "$cname" ] || continue
    if grep -qF "USING COVERING INDEX $cindex" "$WORK/plan_$cname.txt"; then
        pass "PN-AC29 the $cname plan reads $cindex as a covering index"
    else
        fail "PN-AC29 the $cname plan does not say USING COVERING INDEX $cindex"
    fi
done < "$WORK/covering.txt"

# ===========================================================================
section 'PN-AC31, PN-AC32 — no entity-attribute-value, and the flow columns'
# ===========================================================================
# An EAV table is one whose columns are an entity reference, an attribute name
# and a value. Nothing here has a column named for an attribute name.
check 'PN-AC31 no table carries an attribute-name column' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(m.name || '.' || i.name, ',')
        FROM sqlite_master m JOIN pragma_table_info(m.name) i
        WHERE m.type = 'table' AND m.name <> 'setting'
          AND (lower(i.name) IN ('attribute', 'attribute_name', 'attr', 'attr_name',
                                 'property', 'property_name', 'field_name'));")"
if grep -qF 'entity-attribute-value' "$DOC"; then
    pass 'PN-AC31 the document states the attribute rule'
else
    fail 'PN-AC31 the document does not state the attribute rule'
fi

extract_block "$DOC" '<!-- flow-columns:begin -->' '<!-- flow-columns:end -->' | sort > "$WORK/doc_flow_columns.txt"
q "$MAIN_DB" "SELECT name FROM pragma_table_info('flow') ORDER BY name;" | sort > "$WORK/db_flow_columns.txt"
if diff -u "$WORK/doc_flow_columns.txt" "$WORK/db_flow_columns.txt" > "$WORK/flow_columns.diff"; then
    pass "PN-AC32 every flow column is reviewed in the document ($(wc -l < "$WORK/db_flow_columns.txt" | tr -d ' ') columns)"
else
    fail "PN-AC32 the reviewed flow columns and the live ones differ: $(cat "$WORK/flow_columns.diff")"
fi
# The diff above proves the reviewed column list is complete; it says nothing
# about the "Read by" claims next to each column, which drifted unnoticed once.
# Each row of that table names zero or more screen queries, and the claim is
# checked against the query text itself, in both directions:
#   - every screen query a row names reads at least one of that row's columns;
#   - every column of a row that names a screen query is read by at least one
#     of them.
# A row naming no screen query (read only by the purge or the aggregate
# refresh, or deliberately read by nothing) is left to its prose justification,
# which the four assertions below pin down.
awk '
    index($0, "<!-- flow-columns:end -->") == 1 { inside = 1; next }
    inside && index($0, "### ") == 1 { inside = 0 }
    inside && index($0, "| `") == 1 {
        split($0, cell, "|")
        cols = ""
        rest = cell[2]
        while (match(rest, /`[a-z_]+`/)) {
            cols = cols (cols == "" ? "" : ",") substr(rest, RSTART + 1, RLENGTH - 2)
            rest = substr(rest, RSTART + RLENGTH)
        }
        screens = ""
        n = split("Overview Matrix Segment Device Blocked Alerts Map", names, " ")
        for (i = 1; i <= n; i++) {
            if (match(cell[3], "[^A-Za-z]" names[i] "[^A-Za-z]")) {
                screens = screens (screens == "" ? "" : ",") names[i]
            }
        }
        if (cols != "" && screens != "") { print cols "|" screens }
    }
' "$DOC" > "$WORK/flow_read_by.txt"
check_ge 'PN-AC32 the column review makes read-by claims to verify' 5 \
    "$(wc -l < "$WORK/flow_read_by.txt" | tr -d ' ')"
UNREAD_CLAIMS=''
UNCLAIMED_COLUMNS=''
while IFS='|' read -r cols screens; do
    [ -n "$cols" ] || continue
    for screen in $(printf -- '%s' "$screens" | tr ',' ' '); do
        qfile="$(find "$WORK/screens" -maxdepth 1 -name "[0-9]*_${screen}.sql")"
        hit=0
        for col in $(printf -- '%s' "$cols" | tr ',' ' '); do
            if grep -qE "(^|[^A-Za-z0-9_])${col}([^A-Za-z0-9_]|\$)" "$qfile"; then
                hit=1
            fi
        done
        [ "$hit" = 1 ] || UNREAD_CLAIMS="$UNREAD_CLAIMS $screen($cols)"
    done
    for col in $(printf -- '%s' "$cols" | tr ',' ' '); do
        hit=0
        for screen in $(printf -- '%s' "$screens" | tr ',' ' '); do
            qfile="$(find "$WORK/screens" -maxdepth 1 -name "[0-9]*_${screen}.sql")"
            if grep -qE "(^|[^A-Za-z0-9_])${col}([^A-Za-z0-9_]|\$)" "$qfile"; then
                hit=1
            fi
        done
        [ "$hit" = 1 ] || UNCLAIMED_COLUMNS="$UNCLAIMED_COLUMNS $col"
    done
done < "$WORK/flow_read_by.txt"
check 'PN-AC32 every screen query the column review names reads a column of its row' \
    '' "$UNREAD_CLAIMS"
check 'PN-AC32 every column claimed read by a screen query is read by one of them' \
    '' "$UNCLAIMED_COLUMNS"

for needle in 'log_digest' 'src_port' 'ip_version' 'direction'; do
    if grep -qE "^\| \`$needle\`.*and it stays" "$DOC"; then
        pass "PN-AC32 the document justifies keeping $needle, which no query reads"
    else
        fail "PN-AC32 the document does not justify keeping $needle"
    fi
done

# ===========================================================================
section 'PN-AC30 — the scale run: 1 000 000 rows in the largest growing table'
# ===========================================================================
apply_migrations "$SCALE_DB" "$WORK/migrate_scale.err" || fail 'PN-AC30 the scale database failed to migrate'
printf -- '  seeding %s flow rows, this takes a while...\n' "$SCALE_FLOW_ROWS"
seed_database "$SCALE_DB" "$SEGMENTS" "$DEVICES" "$RULES" "$SCALE_FLOW_ROWS" "$ALERTS" "$PAIR_ROWS"
SCALE_COUNT="$(q "$SCALE_DB" 'SELECT count(*) FROM flow;')"
check_ge 'PN-AC30 the scale database holds 1 000 000 rows in flow' 1000000 "$SCALE_COUNT"
check 'PN-AC30 the scale database is consistent' '' "$(q "$SCALE_DB" 'PRAGMA foreign_key_check;')"

for f in "$WORK"/screens/[0-9]*.sql; do
    name="$(head -n 1 "$f" | sed 's/^-- screen: //')"
    started="$(date +%s%N)"
    rows="$(run_query "$SCALE_DB" "$f" | wc -l | tr -d ' ')"
    elapsed_ms=$(( ( $(date +%s%N) - started ) / 1000000 ))
    printf -- '  %s at scale: %s rows in %s ms\n' "$name" "$rows" "$elapsed_ms"
    if [ "$rows" -ge 1 ]; then
        pass "PN-AC30 the $name query returned $rows rows at scale in ${elapsed_ms} ms"
    else
        fail "PN-AC30 the $name query returned no rows at scale"
    fi
    explain_query "$SCALE_DB" "$f" > "$WORK/scale_plan_$name.txt"
    printf -- '  EXPLAIN QUERY PLAN at scale — %s\n' "$name"
    sed 's/^/    /' "$WORK/scale_plan_$name.txt"
    scanned_growing=''
    while read -r table; do
        [ -n "$table" ] || continue
        if grep -qx "$table" "$WORK/growing.txt"; then
            scanned_growing="$scanned_growing $table"
        fi
    done < <(resolve_scans "$f" "$WORK/scale_plan_$name.txt")
    if [ -z "$scanned_growing" ]; then
        pass "PN-AC30 the $name plan scans no growing table at scale"
    else
        fail "PN-AC30 the $name plan scans a growing table at scale:$scanned_growing"
    fi
    # The plan must keep the same shape: the same index, used the same way.
    if diff -q "$WORK/plan_$name.txt" "$WORK/scale_plan_$name.txt" > /dev/null; then
        pass "PN-AC30 the $name plan is identical at 100 000 and at 1 000 000 rows"
    else
        fail "PN-AC30 the $name plan changed shape at scale: $(diff "$WORK/plan_$name.txt" "$WORK/scale_plan_$name.txt" | tr '\n' ' ')"
    fi
done
while read -r cname cindex; do
    [ -n "$cname" ] || continue
    if grep -qF "USING COVERING INDEX $cindex" "$WORK/scale_plan_$cname.txt"; then
        pass "PN-AC29 the $cname plan is still covered at 1 000 000 rows"
    else
        fail "PN-AC29 the $cname plan stopped being covered at 1 000 000 rows"
    fi
done < "$WORK/covering.txt"

# ===========================================================================
section 'PN-AC28 — shellcheck'
# ===========================================================================
# The static analyser is not part of the development image, which carries the
# Go toolchain and sqlite3 and nothing else. When it is present it is run here;
# otherwise it is run on the host. A missing tool is never counted as a pass.
if command -v shellcheck > /dev/null 2>&1; then
    if shellcheck -S error "$REPO_ROOT/sql/schema-checks.sh"; then
        pass 'PN-AC28 shellcheck reports no error-level finding on the harness'
    else
        fail 'PN-AC28 shellcheck reported an error-level finding on the harness'
    fi
else
    printf -- '  shellcheck is absent from this image; run it on the host:\n'
    printf -- '    shellcheck sql/schema-checks.sh\n'
fi

# ===========================================================================
printf -- '\n==========================================================\n'
printf -- 'checks passed : %s\n' "$PASS_COUNT"
printf -- 'checks failed : %s\n' "$FAIL_COUNT"
if [ "$FAIL_COUNT" -ne 0 ]; then
    printf -- 'failed checks:%s\n' "$FAILURES" >&2
    printf -- 'schema-checks: FAILED\n' >&2
    exit 1
fi
printf -- 'schema-checks: OK\n'
