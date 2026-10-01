#!/usr/bin/env bash
# opnview — schema checks.
#
# Applies the schema to a fresh database, seeds it, runs the seven screen
# queries and their query plans, and asserts the criteria the data model and
# the provider-neutral pass established (labelled AC* and PN-AC*). A criterion
# naming an object a later pass renamed is RESTATED against the new name, never
# removed and never relaxed. The same rule has now been applied to the criteria
# that named the MIGRATION MACHINERY: there are no migrations, by the
# maintainer's decision -- nothing is deployed and nobody has data, so numbered
# files, a schema_version table and a runner were machinery for a problem that
# does not exist. Those criteria are restated against what replaces them, which
# is a single file that is idempotent and applied on every start, and the
# restatement is STRICTER than what it replaces: where the old check asserted
# that a second apply added no schema_version row, the new one asserts that a
# second apply changes no row count in any table at all. The VOC-AC* criteria come from no
# spec: they are the maintainer's direct correction of the vocabulary --
# segment became interface, device became client, the owner entity was added --
# and they pin the result so it cannot drift back. This script is the schema
# entry point
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

# The schema and the purge live beside the code that applies them, in
# internal/store, and they are embedded into the binary from there. Go's embed
# directive cannot reach outside its own package directory and a second copy
# under sql/ would be a second truth, so there is one copy and it is where the
# only code that applies it can read it. This harness reads those same two files,
# so what is checked here is what ships.
SCHEMA_FILE="$REPO_ROOT/internal/store/schema.sql"
PURGE_FILE="$REPO_ROOT/internal/store/purge.sql"
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
# Two copies of the seeded database for the checks that have to WRITE: a check
# whose statement is expected to succeed must not leave its rows behind for every
# later count to trip over.
MEAS_DB="$DATA_DIR/schema-checks-measurement.db"
STATE_DB="$DATA_DIR/schema-checks-state.db"
LEASE_DB="$DATA_DIR/schema-checks-lease.db"

# Seed parameters. The default run and the alternative run below differ in
# every count, so nothing may assume an interface, client, owner or rule count.
NOW=1750000000
INTERFACES=6
CLIENTS=40
RULES=12
OWNERS=3
FLOW_ROWS=100000
ALERTS=500
PAIR_ROWS=2000

# Every Nth slot of the seed's address pool is IPv6. It is a seed PARAMETER, and
# the alternative run below uses a different value, so nothing may assume how
# much of a network is v6 -- and because it changes which FAMILY a row carries
# and never how many rows exist, the counts above and their determinism
# comparisons are unaffected.
IPV6_EVERY=11

ALT_INTERFACES=3
ALT_CLIENTS=17
ALT_RULES=5
ALT_OWNERS=2
ALT_FLOW_ROWS=4000
ALT_ALERTS=60
ALT_PAIR_ROWS=300
ALT_IPV6_EVERY=7

# The scale run. The baseline above is a test size; this one is the check that
# the plans hold at a production-ish volume in the largest growing table.
SCALE_FLOW_ROWS=1000000

WINDOW_END=$NOW
WINDOW_START=$((NOW - 86400))
INTERFACE_ID=1
CLIENT_ID=3

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
# The schema applier. There is no runner and nothing to skip: every statement in
# the file is idempotent, which is what makes applying it on every start safe and
# what replaces the schema_version bookkeeping.
# --------------------------------------------------------------------------
apply_schema() {
    local db="$1" errfile="$2"
    : > "$errfile"
    printf -- '.bail on\nPRAGMA foreign_keys = ON;\n.read %s\n' "$SCHEMA_FILE" |
        sqlite3 "$db" 2>>"$errfile" || return 1
    return 0
}

# seed_database <db> <interfaces> <clients> <rules> <flow_rows> <alerts> <pair_rows> <owners> [ipv6_every]
seed_database() {
    local db="$1" ipv6="${9:-$IPV6_EVERY}"
    {
        printf -- '.bail on\n'
        printf -- '.param init\n'
        printf -- '.param set :now %s\n' "$NOW"
        printf -- '.param set :interfaces %s\n' "$2"
        printf -- '.param set :clients %s\n' "$3"
        printf -- '.param set :rules %s\n' "$4"
        printf -- '.param set :flow_rows %s\n' "$5"
        printf -- '.param set :alerts %s\n' "$6"
        printf -- '.param set :pair_rows %s\n' "$7"
        printf -- '.param set :owners %s\n' "$8"
        printf -- '.param set :ipv6_every %s\n' "$ipv6"
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
        printf -- '.param set :interface_id %s\n' "$INTERFACE_ID"
        printf -- '.param set :client_id %s\n' "$CLIENT_ID"
        printf -- '.read %s\n' "$file"
    } | sqlite3 "$db"
}

# run_query_for_client <db> <query file> <client id> — one query with :client_id
# bound to something other than the harness default, so a query can be run against a
# row the seed identified rather than against a fixed id.
run_query_for_client() {
    local db="$1" file="$2" client="$3"
    {
        printf -- '.bail on\n'
        printf -- '.param init\n'
        printf -- '.param set :window_start %s\n' "$WINDOW_START"
        printf -- '.param set :window_end %s\n' "$WINDOW_END"
        printf -- '.param set :interface_id %s\n' "$INTERFACE_ID"
        printf -- '.param set :client_id %s\n' "$client"
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
section 'AC1, AC4 — the schema applies cleanly, and re-applying changes nothing'
# ===========================================================================
sqlite3 "$MAIN_DB" 'PRAGMA journal_mode = WAL;' > /dev/null
if apply_schema "$MAIN_DB" "$WORK/apply1.err"; then
    pass 'AC1 first apply exited 0'
else
    fail 'AC1 first apply exited non-zero'
fi
if [ -s "$WORK/apply1.err" ]; then
    fail "AC1 first apply wrote to stderr: $(cat "$WORK/apply1.err")"
else
    pass 'AC1 first apply wrote nothing to stderr'
fi

OBJECTS_BEFORE="$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master;")"
table_row_counts() {
    local db="$1" t
    for t in $(q "$db" "SELECT name FROM sqlite_master WHERE type = 'table'
                        AND name NOT LIKE 'sqlite_%' ORDER BY name;"); do
        printf -- '%s %s\n' "$t" "$(q "$db" "SELECT count(*) FROM \"$t\";")"
    done
}
table_row_counts "$MAIN_DB" > "$WORK/rows_before_reapply.txt"
if apply_schema "$MAIN_DB" "$WORK/apply2.err"; then
    pass 'AC1 second apply exited 0'
else
    fail 'AC1 second apply exited non-zero'
fi
if [ -s "$WORK/apply2.err" ]; then
    fail "AC1 second apply wrote to stderr: $(cat "$WORK/apply2.err")"
else
    pass 'AC1 second apply wrote nothing to stderr'
fi
check 'AC1 second apply changed no schema object' \
    "$OBJECTS_BEFORE" "$(q "$MAIN_DB" 'SELECT count(*) FROM sqlite_master;')"
# The restatement, and it is stricter than the schema_version check it replaces:
# a second apply must change no row count in ANY table. That is what stops a
# restart from re-inserting a default over a setting somebody changed, or
# resetting a probed availability row to "not yet probed".
table_row_counts "$MAIN_DB" > "$WORK/rows_after_reapply.txt"
if diff -u "$WORK/rows_before_reapply.txt" "$WORK/rows_after_reapply.txt" > "$WORK/reapply.diff"; then
    pass 'AC1 second apply changed no row count in any table'
else
    fail "AC1 second apply changed a row count: $(cat "$WORK/reapply.diff")"
fi

# AC4 restated. It used to assert that schema_version matched the files in
# migrations/; both are gone by decision, so what it asserts now is the decision
# itself, in the only form that can fail: there is no migrations directory, no
# numbered file anywhere, and no schema_version table in the applied schema.
if [ -e "$REPO_ROOT/migrations" ]; then
    fail 'AC4 a migrations directory exists, and there are deliberately no migrations'
else
    pass 'AC4 there is no migrations directory'
fi
NUMBERED="$(find "$REPO_ROOT" -name '0[0-9][0-9][0-9]_*.sql' -not -path '*/.git/*' | head -n 3)"
check 'AC4 no numbered migration file exists anywhere' '' "$NUMBERED"
check 'AC4 the applied schema carries no schema_version table' '' \
    "$(q "$MAIN_DB" "SELECT name FROM sqlite_master WHERE name = 'schema_version';")"
check 'AC4 the schema is exactly one file' '1' \
    "$(find "$REPO_ROOT/internal/store" -maxdepth 1 -name 'schema*.sql' | wc -l | tr -d ' ')"
if [ -f "$SCHEMA_FILE" ] && [ -f "$PURGE_FILE" ]; then
    pass 'AC4 the schema and the purge are where the code that applies them can read them'
else
    fail 'AC4 the schema or the purge is missing from internal/store'
fi

# ===========================================================================
section 'AC7 — deterministic seed, and the baseline row count'
# ===========================================================================
seed_database "$MAIN_DB" "$INTERFACES" "$CLIENTS" "$RULES" "$FLOW_ROWS" "$ALERTS" "$PAIR_ROWS" "$OWNERS"
apply_schema "$REPEAT_DB" "$WORK/apply3.err" || fail 'AC7 repeat database schema apply failed'
seed_database "$REPEAT_DB" "$INTERFACES" "$CLIENTS" "$RULES" "$FLOW_ROWS" "$ALERTS" "$PAIR_ROWS" "$OWNERS"

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
expect_sql_failure 'AC3 a flow whose interface does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO flow (log_digest, observed_at, ingested_at, interface_device,
        interface_lookup_state, src_interface_id, src_address, dst_address, protocol,
        ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope)
     VALUES ('ac3-orphan-flow', $NOW, $NOW, 'ac3-device', 'resolved', 999999999,
             'ac3-src', 'ac3-dst', 'tcp', 4, 'pass', 'in', 100, 'pending',
             'north_south');"
expect_sql_failure 'AC3 a security event whose client does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
        rule_identity, signature, event_action, src_address, dst_address, src_client_id)
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
# The classification is over TABLES: a view holds no rows of its own, so it is
# neither growing nor bounded. The exclusion asks sqlite_master which entities
# are views rather than naming the one that existed when this check was written,
# which is stricter: a second view added without being documented would have
# slipped past a hardcoded name and now cannot.
q "$MAIN_DB" "SELECT name FROM sqlite_master WHERE type = 'table'
              AND name NOT LIKE 'sqlite_%' ORDER BY name;" | sort > "$WORK/db_tables.txt"
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
Interface
Client
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
section 'AC13, AC14, AC15 — interfaces, classification and tunnels'
# ===========================================================================
SCOPES="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/01_Overview.sql" \
    "SELECT group_concat(traffic_scope, ',') FROM (SELECT traffic_scope FROM (" \
    ") ORDER BY traffic_scope);")")"
check 'AC13 the Overview query classifies flows east-west and north-south' 'east_west,north_south' "$SCOPES"
check 'AC13 every stored traffic_scope agrees with interface membership' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM flow WHERE traffic_scope <> (CASE
        WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL
        THEN 'east_west' ELSE 'north_south' END);")"
expect_sql_failure 'AC13 a traffic_scope disagreeing with interface membership is rejected' "$MAIN_DB" \
    "UPDATE flow SET traffic_scope = 'east_west' WHERE traffic_scope = 'north_south';"

if grep -inE '\b(like|glob|regexp)\b' "$SCHEMA_FILE" > "$WORK/namematch.txt"; then
    fail "AC14 the DDL contains a name-matching predicate: $(cat "$WORK/namematch.txt")"
else
    pass 'AC14 the DDL contains no LIKE, GLOB or REGEXP predicate at all'
fi
cp "$MAIN_DB" "$WORK/label.db"
sqlite3 "$WORK/label.db" \
    "UPDATE interface SET user_label = 'a label the maintainer chose' WHERE id = 1;" > /dev/null
check 'AC14 relabelling leaves the discovered description untouched' \
    "$(q "$MAIN_DB" 'SELECT description FROM interface WHERE id = 1;')" \
    "$(q "$WORK/label.db" 'SELECT description FROM interface WHERE id = 1;')"
check 'AC14 a query returns the user label and the discovered description together' \
    'a label the maintainer chose|discovered-description-1' \
    "$(q "$WORK/label.db" "SELECT user_label || '|' || description FROM interface WHERE id = 1;")"

check_ge 'AC15 at least one seeded interface is a tunnel' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM interface WHERE is_tunnel = 1;')"
check_ge 'AC15 at least one seeded interface is a VLAN' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM interface WHERE is_tunnel = 0 AND link_kind = 'vlan';")"
check 'AC15 is_tunnel agrees with the discovered link type on every interface' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM interface
        WHERE is_tunnel <> (CASE WHEN link_type = 'tunnel' THEN 1 ELSE 0 END);")"

# ===========================================================================
section 'AC16, AC17 — client identity and randomised MACs'
# ===========================================================================
check 'AC16 the lease and the flows of one client resolve to a single client row' '1' \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT d.id) FROM client d
        JOIN dhcp_lease l ON l.client_id = d.id
        JOIN flow f ON f.src_client_id = d.id
        WHERE d.identity_kind = 'dhcp_client_id' AND d.id = 1;")"
check_ge 'AC16 a client seen only in flows, with no MAC, is representable and queryable' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM client d
        WHERE d.mac IS NULL AND d.identity_kind = 'address_in_interface'
          AND EXISTS (SELECT 1 FROM flow f WHERE f.src_client_id = d.id)
          AND NOT EXISTS (SELECT 1 FROM dhcp_lease l WHERE l.client_id = d.id);")"
REUSED_ADDRESS="$(q "$MAIN_DB" "SELECT last_address FROM client WHERE id = $((CLIENTS + 1));")"
check 'AC16 an address reissued after a lease expiry stays two clients' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM client WHERE last_address = '$REUSED_ADDRESS';")"
check 'AC16 the two reissued identities differ' '2' \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT identity_key) FROM client WHERE last_address = '$REUSED_ADDRESS';")"

check 'AC17 every MAC whose second hex digit is 2, 6, a or e is an unstable identity' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM client
        WHERE mac IS NOT NULL AND instr('26ae', substr(mac, 2, 1)) > 0
          AND unstable_identity <> 1;")"
check 'AC17 no MAC whose second hex digit is 0, 4, 8 or c is an unstable identity' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM client
        WHERE mac IS NOT NULL AND instr('048c', substr(mac, 2, 1)) > 0
          AND unstable_identity <> 0;")"
check_ge 'AC17 the seed contains globally administered MACs to test the negative direction' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM client WHERE mac IS NOT NULL AND instr('048c', substr(mac, 2, 1)) > 0;")"
check_ge 'AC17 two randomised observations with different MACs remain two clients' 2 \
    "$(q "$MAIN_DB" 'SELECT count(DISTINCT id) FROM client WHERE unstable_identity = 1;')"
check 'AC17 every randomised client has its own identity_key' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) - count(DISTINCT identity_key) FROM client WHERE unstable_identity = 1;")"

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

CLIENT_ROWS_NO_SITE="$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/04_Client.sql" \
    'SELECT count(*) FROM (' \
    ') WHERE site_name IS NULL AND dst_address IS NOT NULL
        AND country_code IS NOT NULL AND operator IS NOT NULL;')")"
check_ge 'AC19 the Client query returns an unattributed flow with its address, country and operator' \
    1 "$CLIENT_ROWS_NO_SITE"

split_queries "$REPO_ROOT/sql/queries/diagnostics.sql" 'diagnostic' "$WORK/diag"
DIAG_ATTR="$WORK/diag/01_Attribution_rate_per_client.sql"
ATTR_ROWS="$(run_query "$MAIN_DB" "$DIAG_ATTR" | wc -l | tr -d ' ')"
check_ge 'AC19 the documented attribution-rate query runs and returns rows' 1 "$ATTR_ROWS"
printf -- '  attribution rate, first five clients:\n'
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
        AND occurred_at IS NOT NULL AND client_id IS NOT NULL AND interface_id IS NOT NULL;")")"
check_ge 'AC20 a security event joins to a client and an interface and carries signature, severity, endpoints and time' \
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
section 'GAP-AC1 .. GAP-AC5 — the collection-gap table'
# ===========================================================================
# A gap is a row because a gap is read by a SCREEN. A byte total over a window
# that contains one is not a lower bound for the usual physical reason; it is a
# lower bound because opnview was not looking, and those are two different
# sentences to put next to a figure.
check 'GAP-AC1 the collection_gap table exists' 'table' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'collection_gap';")"
check 'GAP-AC1 it records the source, the interval, the reason and when it was detected' '5' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('collection_gap')
        WHERE name IN ('provider_id', 'interval_start_at', 'interval_end_at', 'reason',
                       'detected_at');")"
check 'GAP-AC1 the source is a foreign key to the registry, not a name' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_foreign_key_list('collection_gap')
        WHERE \"table\" = 'provider' AND \"from\" = 'provider_id';")"
expect_sql_failure 'GAP-AC1 a gap attributed to a provider that does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at, reason, detected_at)
     VALUES (999999999, $NOW - 60, $NOW, 'digest_outside_returned_window', $NOW);"

# GAP-AC2: the three reasons are the three MEASURED failure modes, and the
# vocabulary is closed because these are opnview's own detections rather than
# values any endpoint reports -- so enumerating them invents nothing.
check 'GAP-AC2 all three measured failure modes are represented in the seed' \
    'digest_outside_returned_window|eve_rotation_lost|resolver_window_not_honoured' \
    "$(q "$MAIN_DB" "SELECT group_concat(reason, '|') FROM
        (SELECT DISTINCT reason FROM collection_gap ORDER BY reason);")"
expect_sql_failure 'GAP-AC2 a reason outside the vocabulary is rejected' "$MAIN_DB" \
    "UPDATE collection_gap SET reason = 'something-went-wrong'
     WHERE id = (SELECT min(id) FROM collection_gap);"
expect_sql_failure 'GAP-AC2 an interval that ends before it starts is rejected' "$MAIN_DB" \
    "UPDATE collection_gap SET interval_end_at = interval_start_at - 1
     WHERE id = (SELECT min(id) FROM collection_gap);"

# GAP-AC3: a gap carries an interval a screen can put next to a figure, and it
# says why in words.
check 'GAP-AC3 every seeded gap carries a bounded interval' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM collection_gap
        WHERE interval_end_at < interval_start_at;')"
check_ge 'GAP-AC3 every seeded gap says why, in words' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM collection_gap WHERE detail IS NOT NULL;')"
check 'GAP-AC3 a gap is attributed to a provider that exists' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM collection_gap g
        WHERE NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = g.provider_id);')"

# GAP-AC4: no other table was added for this. A capability the product does not
# have gets no table.
#
# RESTATED, NOT RELAXED, BY THE SIGN-IN CYCLE. The original wording said that "the
# accounts and secrets the sign-in decision implies are not here either -- they
# arrive with the cycle that has a sign-in". That cycle has now happened: it creates
# the first account, it signs people in, and it is where the firewall URL and the API
# key and secret enter the product. So the three tables it adds are named and
# REQUIRED to exist, one by one, and every other name in the original vocabulary is
# still forbidden.
#
# The restatement is STRICTER than what it replaces, in two ways. The old check could
# only fail on a table appearing; this one also fails on one of the three
# DISAPPEARING. And the forbidden list keeps every capability the product still does
# not have -- dashboards, canvases, widgets and themes, which are step 7 -- plus
# `user`, which here is not a synonym for `account`: it is OPNsense's own word for the
# firewall user whose API key opnview holds, and it must never become a table.
GAP_AC4_EXPECTED='account
encrypted_credential
session'
check 'GAP-AC4 the three tables the sign-in cycle adds all exist' "$GAP_AC4_EXPECTED" \
    "$(q "$MAIN_DB" "SELECT name FROM sqlite_master
        WHERE type = 'table'
          AND name IN ('account', 'session', 'encrypted_credential')
        ORDER BY name;")"
check 'GAP-AC4 no table exists for a capability this cycle does not have' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM sqlite_master
        WHERE type = 'table'
          AND name NOT IN ('account', 'session', 'encrypted_credential')
          AND (lower(name) GLOB '*account*' OR lower(name) GLOB '*user*'
               OR lower(name) GLOB '*secret*' OR lower(name) GLOB '*credential*'
               OR lower(name) GLOB '*session*' OR lower(name) GLOB '*password*'
               OR lower(name) GLOB '*dashboard*' OR lower(name) GLOB '*canvas*'
               OR lower(name) GLOB '*widget*' OR lower(name) GLOB '*theme*');")"

# GAP-AC4b: the three carry what the security design requires of them, and the
# SCHEMA is what enforces it rather than the code remembering to.
#
# The password parameters are COLUMNS, which is the whole of what ROADMAP.md means by
# storing them beside the hash so they can be raised later. The session's primary key
# is a DIGEST of the token rather than the token, so a copied database hands over no
# usable session. And a ciphertext carries the nonce, the algorithm label and the key
# identifier without which it cannot be opened, and without which a REPLACED key
# cannot be told apart from a TAMPERED ciphertext.
check 'GAP-AC4b the account carries the Argon2id parameters beside the hash' '6' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('account')
        WHERE name IN ('password_algorithm', 'password_memory_kib', 'password_iterations',
                       'password_parallelism', 'password_salt', 'password_digest');")"
check 'GAP-AC4b the account has no column that could hold a password' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM pragma_table_info('account')
        WHERE name = 'password' OR name GLOB '*plaintext*' OR name GLOB '*cleartext*';")"
check 'GAP-AC4b the session is keyed by a digest of its token, never the token' 'token_digest' \
    "$(q "$MAIN_DB" "SELECT name FROM pragma_table_info('session') WHERE pk = 1;")"
check 'GAP-AC4b the session carries both bounds and its own form token' '4' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('session')
        WHERE name IN ('csrf_token', 'created_at', 'last_seen_at', 'expires_at');")"
check 'GAP-AC4b the session belongs to an account by foreign key' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_foreign_key_list('session')
        WHERE \"table\" = 'account' AND \"from\" = 'account_id';")"
check 'GAP-AC4b a ciphertext carries its nonce, its algorithm and its key identifier' '4' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('encrypted_credential')
        WHERE name IN ('algorithm', 'key_id', 'nonce', 'ciphertext');")"
check 'GAP-AC4b no encryption key is a column anywhere in the schema' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(m.name || '.' || i.name, ',')
        FROM sqlite_master m JOIN pragma_table_info(m.name) i
        WHERE m.type = 'table'
          AND (i.name GLOB '*encryption_key*' OR i.name GLOB '*private_key*'
               OR i.name GLOB '*key_material*');")"

# GAP-AC4c: nothing seeds them. The schema has been applied and the seed has run by
# this point, and all three are still empty -- because an account nobody created, a
# session nobody signed in to and a credential nobody typed would each be worse than
# an empty table. The credentials enter through the settings surface and through no
# other means, which is the whole point of the cycle that added them.
for t in account session encrypted_credential; do
    check "GAP-AC4c neither the schema nor the seed puts a row into $t" '0' \
        "$(q "$MAIN_DB" "SELECT count(*) FROM \"$t\";")"
done

# GAP-AC5: the two tables this cycle added are classified growing and are purged,
# because each accumulates one row per pass and would otherwise grow without
# bound from the first day.
for t in collection_gap measurement_sample; do
    if grep -qx "$t" "$WORK/growing.txt"; then
        pass "GAP-AC5 $t is classified growing in the document"
    else
        fail "GAP-AC5 $t is not classified growing, and it accumulates one row per pass"
    fi
    if grep -q "DELETE FROM $t" "$PURGE_FILE"; then
        pass "GAP-AC5 the purge removes old rows from $t"
    else
        fail "GAP-AC5 the purge does not touch $t, which therefore grows without bound"
    fi
done

# ===========================================================================
section 'MEAS-AC1 .. MEAS-AC5 — the sampled-measurement table'
# ===========================================================================
# One table for a subject, a measure, a unit, a value and an instant. It carries
# both the firewall's own gauges and the per-pair volume, because the second has
# to be SAMPLED: no endpoint exposes a flow aggregate over a past window, so
# nothing upstream can answer "what did these two talk about last Tuesday".
check 'MEAS-AC1 the measurement_sample table exists' 'table' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'measurement_sample';")"
check 'MEAS-AC1 it carries a subject, a measure, a unit, a value and an instant' '6' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('measurement_sample')
        WHERE name IN ('subject_kind', 'subject_key', 'measure', 'unit', 'value', 'sampled_at');")"
check 'MEAS-AC1 exactly one table was added for the sampled measurement' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master WHERE type = 'table'
        AND (lower(name) GLOB '*measurement*' OR lower(name) GLOB '*sample*'
             OR lower(name) GLOB '*gauge*' OR lower(name) GLOB '*metric*'
             OR lower(name) GLOB '*telemetry*');")"

# MEAS-AC2: both kinds of reading round-trip, which is the whole claim that one
# table serves both.
check_ge 'MEAS-AC2 a firewall gauge round-trips' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'firewall' AND measure = 'uptime_seconds' AND unit = 'second';")"
check_ge 'MEAS-AC2 a per-sensor gauge round-trips under its own subject key' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'firewall' AND subject_key <> ''
          AND measure = 'temperature_celsius' AND unit = 'celsius';")"
check_ge 'MEAS-AC2 a per-interface counter round-trips' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'interface' AND measure = 'bytes_in' AND unit = 'byte';")"
check_ge 'MEAS-AC2 a sampled per-pair volume round-trips' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'endpoint_pair' AND measure = 'cumulative_bytes_in'
          AND unit = 'byte';")"

# MEAS-AC3, RESTATED. The three vocabularies were closed CHECKs and are not any
# more, because the property changed rather than stopped mattering: this table is
# now a KIND, and the eight surveyed sources that fit its shape share no
# vocabulary at all -- a UPS reports volts, SMART reports reallocated sectors,
# HAProxy's subject is a backend. A closed CHECK would have made every one of them
# a schema change, which is the plugin-hostile design the promotion removes.
#
# What is asserted instead is the SHAPE of a term rather than its membership of a
# list: a term that is empty, upper-cased or spaced would let two spellings of one
# measure coexist, and a screen grouping by measure would then show one reading
# twice under two names. The mandatory unit is unchanged, verbatim. The
# extensibility this replaces closedness with is MEAS-AC6 below, which proves a
# provider's own term is accepted AND that accepting it changed no DDL.
expect_sql_failure 'MEAS-AC3 a subject kind that is empty is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET subject_kind = ''
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a subject kind that is not a lower-case token is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET subject_kind = 'Something Else'
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a measure that is empty is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET measure = ''
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a measure that is not a lower-case token is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET measure = 'Something Else'
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a unit that is empty is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET unit = ''
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a unit that is not a lower-case token is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET unit = 'Furlongs Per Fortnight'
     WHERE id = (SELECT min(id) FROM measurement_sample);"
expect_sql_failure 'MEAS-AC3 a reading with no unit is rejected' "$MAIN_DB" \
    "UPDATE measurement_sample SET unit = NULL
     WHERE id = (SELECT min(id) FROM measurement_sample);"
# And the vocabularies opnview's OWN sampler uses are still exactly the shipped
# ones: extension is additive, and a drift in what the firewall's own readings are
# stored under would show up here rather than in a screen.
check 'MEAS-AC3 the firewall readings still use only the shipped subject terms' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(DISTINCT subject_kind) FROM measurement_sample
        WHERE subject_kind NOT IN ('firewall', 'interface', 'endpoint_pair');")"
check 'MEAS-AC3 the firewall readings still use only the shipped units' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(DISTINCT unit) FROM measurement_sample
        WHERE unit NOT IN ('ratio', 'celsius', 'second', 'packet', 'byte',
                           'bit_per_second', 'dimensionless');")"

# MEAS-AC4: re-reading the same instant is a no-op, which is what a sampler
# restarting inside one interval needs.
expect_sql_failure 'MEAS-AC4 a duplicate reading of one subject at one instant is rejected' "$MAIN_DB" \
    "INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
         unit, value, sampled_at)
     SELECT provider_id, subject_kind, subject_key, measure, unit, value, sampled_at
     FROM measurement_sample LIMIT 1;"
check 'MEAS-AC4 the pair subject is canonically ordered, so one pair is one subject' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'endpoint_pair'
          AND substr(subject_key, 1, instr(subject_key, ' ') - 1) >
              substr(subject_key, instr(subject_key, ' ') + 1);")"

# MEAS-AC5: the firewall's own telemetry names no provider, because it implements
# no external contract. Attributing it to the volume provider would say the
# volume source measured the temperature.
check 'MEAS-AC5 no firewall gauge is attributed to a provider' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'firewall' AND provider_id IS NOT NULL;")"
check 'MEAS-AC5 every sampled pair volume names the provider whose material it is' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample
        WHERE subject_kind = 'endpoint_pair' AND provider_id IS NULL;")"
check 'MEAS-AC5 no measurement names a provider that does not exist' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM measurement_sample m
        WHERE m.provider_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = m.provider_id);')"

# MEAS-AC6: measurement_sample IS A KIND. It used to be a table with no kind: no
# provider_key, no availability row, no place in the provider.kind CHECK, so none
# of the eight surveyed sources that fit its shape had anywhere to announce itself.
check 'MEAS-AC6 the measurement_sample kind has a registry row' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE kind = 'measurement_sample';")"
check 'MEAS-AC6 that registry row has an availability row' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM source_availability a
        JOIN provider p ON p.id = a.provider_id
        WHERE p.kind = 'measurement_sample';")"
check 'MEAS-AC6 the sampled pair volume is attributed to the measurement provider' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM measurement_sample m
        WHERE m.subject_kind = 'endpoint_pair'
          AND m.provider_id NOT IN (SELECT id FROM provider WHERE kind = 'measurement_sample');")"

# MEAS-AC6, the extensibility that replaced closedness: a provider introduces a
# subject, a measure and a unit the schema did not ship with, WITHOUT a schema
# change, and reads it back. The DDL is compared before and after, because "no
# schema change" is the half of the claim that a successful insert alone would not
# prove.
cp "$MAIN_DB" "$MEAS_DB"
MEAS_DDL_BEFORE="$(q "$MEAS_DB" "SELECT group_concat(type || ' ' || name || ' ' || ifnull(sql, ''), '
') FROM sqlite_master ORDER BY type, name;")"
expect_sql_success 'MEAS-AC6 a provider may introduce a subject, a measure and a unit' "$MEAS_DB" \
    "INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
         unit, value, sampled_at)
     SELECT id, 'example-power-supply', 'example-unit-one', 'example_input_volts',
            'example_volt', 231.5, $NOW
     FROM provider WHERE kind = 'measurement_sample' LIMIT 1;"
check 'MEAS-AC6 the introduced reading reads back under its own terms' '231.5' \
    "$(q "$MEAS_DB" "SELECT value FROM measurement_sample
        WHERE subject_kind = 'example-power-supply' AND measure = 'example_input_volts'
          AND unit = 'example_volt';")"
check 'MEAS-AC6 introducing a term changed no DDL at all' "$MEAS_DDL_BEFORE" \
    "$(q "$MEAS_DB" "SELECT group_concat(type || ' ' || name || ' ' || ifnull(sql, ''), '
') FROM sqlite_master ORDER BY type, name;")"

# MEAS-AC7: the provider is part of a reading's identity, because this kind admits
# several concurrently active providers -- and the firewall's own gauges carry a
# NULL provider, which SQLite treats as distinct from every other NULL. So the
# index wraps the column in ifnull, and both halves are asserted: two providers
# reading one subject at one instant are two readings, and a provider-less gauge
# offered twice is still one row.
expect_sql_success 'MEAS-AC7 a second provider may report the same subject at the same instant' "$MEAS_DB" \
    "INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
       VALUES ('measurement_sample', 'meas-ac7-second', 'MEAS-AC7 second sampler', 1, $NOW);
     INSERT INTO source_availability (provider_id, state, probe, detail, checked_at)
       SELECT id, 'reachable', 'meas-ac7-probe', NULL, $NOW
       FROM provider WHERE provider_key = 'meas-ac7-second';
     INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
         unit, value, sampled_at)
     SELECT (SELECT id FROM provider WHERE provider_key = 'meas-ac7-second'),
            m.subject_kind, m.subject_key, m.measure, m.unit, m.value, m.sampled_at
     FROM measurement_sample m
     WHERE m.subject_kind = 'endpoint_pair' LIMIT 1;"
expect_sql_failure 'MEAS-AC7 one provider cannot report the same reading twice' "$MEAS_DB" \
    "INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
         unit, value, sampled_at)
     SELECT provider_id, subject_kind, subject_key, measure, unit, value, sampled_at
     FROM measurement_sample WHERE provider_id IS NOT NULL LIMIT 1;"
expect_sql_failure 'MEAS-AC7 a provider-less gauge cannot be stored twice either' "$MEAS_DB" \
    "INSERT INTO measurement_sample (provider_id, subject_kind, subject_key, measure,
         unit, value, sampled_at)
     SELECT provider_id, subject_kind, subject_key, measure, unit, value, sampled_at
     FROM measurement_sample WHERE provider_id IS NULL LIMIT 1;"

# ===========================================================================
section 'V6-AC1 .. V6-AC4 — both address families, in every screen query'
# ===========================================================================
# The seed produces both families from a counter, with no address literal and no
# addressing-plan meaning. flow.ip_version is the only column from which a
# v4-against-v6 split is answerable: an address column alone cannot be
# classified, and the project forbids inferring an addressing plan.
check 'V6-AC1 flow carries both address families' '4|6' \
    "$(q "$MAIN_DB" "SELECT group_concat(ip_version, '|') FROM
        (SELECT DISTINCT ip_version FROM flow ORDER BY ip_version);")"
for pair in 'dns_resolution:client_address' 'security_event:src_address' \
            'client:last_address' 'pair_volume_observation:endpoint_low'; do
    tbl="${pair%%:*}"
    col="${pair##*:}"
    check_ge "V6-AC1 $tbl carries an IPv6 address" 1 \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $tbl WHERE $col LIKE '%:%';")"
    check_ge "V6-AC1 $tbl carries an IPv4 address" 1 \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $tbl WHERE $col NOT LIKE '%:%';")"
done
check 'V6-AC1 every flow of one family carries both endpoints of that family' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM flow
        WHERE (src_address LIKE '%:%') <> (dst_address LIKE '%:%');")"
check 'V6-AC1 the family a flow reports agrees with the addresses it carries' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM flow
        WHERE (ip_version = 6) <> (src_address LIKE '%:%');")"

# V6-AC2: the seed still contains no address literal. The IPv6 form is eight
# hexadecimal groups derived from a counter, deliberately not a documentation
# prefix, because a recognisable prefix would be a literal about somebody's
# addressing.
if grep -nE '[0-9a-f]{1,4}:[0-9a-f]{1,4}:[0-9a-f]{1,4}:' "$REPO_ROOT/sql/seed.sql" \
        > "$WORK/v6_literals.txt"; then
    fail "V6-AC2 an IPv6 literal was found in the seed: $(cat "$WORK/v6_literals.txt")"
else
    pass 'V6-AC2 no IPv6 literal appears in the seed'
fi
check 'V6-AC2 the family split is a bound parameter rather than a constant' '1' \
    "$(grep -c ':ipv6_every' "$REPO_ROOT/sql/seed.sql" > /dev/null && echo 1 || echo 0)"

# V6-AC3: every screen query answers with both families present.
#
# Five of the seven project an address, and for those the assertion is direct: a
# row derived from each family. Overview and Matrix aggregate and project no
# address at all, so for those the assertion is COVERAGE -- the query's own
# totals equal the totals over the flows in its window, computed with both
# families present, which is what proves neither family was dropped. Map
# aggregates too, and its coverage is asserted the same way against the rows the
# geo join actually reaches.
family_both() {
    local name="$1" file="$2" column="$3" v6 v4
    v6="$(run_query "$MAIN_DB" "$(wrap_query "$file" 'SELECT count(*) FROM (' \
        ") WHERE $column LIKE '%:%';")")"
    v4="$(run_query "$MAIN_DB" "$(wrap_query "$file" 'SELECT count(*) FROM (' \
        ") WHERE $column NOT LIKE '%:%';")")"
    check_ge "V6-AC3 the $name query returns a row derived from IPv6" 1 "$v6"
    check_ge "V6-AC3 the $name query returns a row derived from IPv4" 1 "$v4"
}
family_both 'Interface' "$WORK/screens/03_Interface.sql" 'last_address'
family_both 'Client' "$WORK/screens/04_Client.sql" 'dst_address'
family_both 'Blocked' "$WORK/screens/05_Blocked.sql" 'src_address'
family_both 'Alerts' "$WORK/screens/06_Alerts.sql" 'src_address'

WINDOW_FLOWS="$(q "$MAIN_DB" "SELECT count(*) FROM flow
    WHERE observed_at >= $WINDOW_START AND observed_at < $WINDOW_END;")"
WINDOW_FLOWS_V6="$(q "$MAIN_DB" "SELECT count(*) FROM flow
    WHERE observed_at >= $WINDOW_START AND observed_at < $WINDOW_END AND ip_version = 6;")"
check_ge 'V6-AC3 the Overview window holds flows of both families to count' 1 "$WINDOW_FLOWS_V6"
check 'V6-AC3 the Overview query counts every flow in the window, both families included' \
    "$WINDOW_FLOWS" \
    "$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/01_Overview.sql" \
        'SELECT sum(flow_count) FROM (' ');')")"

WINDOW_BYTES="$(q "$MAIN_DB" "SELECT sum(packet_bytes) FROM flow
    WHERE observed_at >= $WINDOW_START AND observed_at < $WINDOW_END
      AND src_interface_id IS NOT NULL;")"
WINDOW_BYTES_V6="$(q "$MAIN_DB" "SELECT sum(packet_bytes) FROM flow
    WHERE observed_at >= $WINDOW_START AND observed_at < $WINDOW_END
      AND src_interface_id IS NOT NULL AND ip_version = 6;")"
check_ge 'V6-AC3 the Matrix window holds IPv6 bytes to sum' 1 "$WINDOW_BYTES_V6"
check 'V6-AC3 the Matrix query sums every byte in the window, both families included' \
    "$WINDOW_BYTES" \
    "$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/02_Matrix.sql" \
        'SELECT sum(observed_bytes) FROM (' ');')")"

MAP_BYTES="$(q "$MAIN_DB" "SELECT sum(v.bytes) FROM volume_aggregate_24h AS v
    JOIN geo_asn AS g ON g.address = v.peer_address
    WHERE v.period_start_at >= $WINDOW_START AND v.period_start_at < $WINDOW_END;")"
MAP_BYTES_V6="$(q "$MAIN_DB" "SELECT sum(v.bytes) FROM volume_aggregate_24h AS v
    JOIN geo_asn AS g ON g.address = v.peer_address
    WHERE v.period_start_at >= $WINDOW_START AND v.period_start_at < $WINDOW_END
      AND v.peer_address LIKE '%:%';")"
check_ge 'V6-AC3 the Map window reaches IPv6 destinations' 1 "$MAP_BYTES_V6"
check 'V6-AC3 the Map query sums every enriched destination, both families included' \
    "$MAP_BYTES" \
    "$(run_query "$MAIN_DB" "$(wrap_query "$WORK/screens/07_Map.sql" \
        'SELECT sum(observed_bytes) FROM (' ');')")"

# V6-AC4: an IPv6 destination is enriched rather than being a permanent cache
# miss, which is what would happen if the seed had been extended in one family
# only -- and it would look like a dataset problem rather than a seed problem.
check_ge 'V6-AC4 an IPv6 destination carries a resolved geo enrichment' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn
        WHERE address LIKE '%:%' AND lookup_state = 'resolved';")"
check_ge 'V6-AC4 an IPv6 destination also exercises the modelled cache miss' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM geo_asn
        WHERE address LIKE '%:%' AND lookup_state = 'miss';")"

# ===========================================================================
section 'VOC-AC1, VOC-AC2 — the vocabulary comes from OPNsense, not from us'
# ===========================================================================
# VOC-AC1: nothing in the live schema is called a "segment". OPNsense names the
# thing an INTERFACE -- /api/interfaces/overview/interfaces_info, menu
# Interfaces > Assignments -- and the schema now says so.
check 'VOC-AC1 no table, view, index or column is named for a segment' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(m.name || '.' || ifnull(i.name, '(object)'), ',')
        FROM sqlite_master m LEFT JOIN pragma_table_info(m.name) i
        WHERE lower(m.name) GLOB '*segment*' OR lower(i.name) GLOB '*segment*';")"
if grep -rniE '\bsegments?\b' "$SCHEMA_FILE" "$PURGE_FILE" "$REPO_ROOT/sql/queries" \
        "$REPO_ROOT/sql/seed.sql" > "$WORK/segment_word.txt"; then
    fail "VOC-AC1 the word segment survives in the schema or its queries: $(head -n 3 "$WORK/segment_word.txt")"
else
    pass 'VOC-AC1 the word segment appears nowhere in the schema, the queries, the seed or the purge'
fi
check 'VOC-AC1 the interface table carries the endpoint field names verbatim' '5' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('interface')
        WHERE name IN ('identifier', 'device', 'description', 'link_type', 'vlan_tag');")"

# VOC-AC2: "device" now has exactly one meaning, the network device OPNsense
# reports. The machine on the network is a client. No table is named device,
# and every column called device belongs to an interface-shaped table.
check 'VOC-AC2 no table or view is named device' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM sqlite_master
        WHERE type IN ('table', 'view') AND lower(name) = 'device';")"
check 'VOC-AC2 every column named device belongs to an interface, never to a machine' \
    'interface,interface_map' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM (
        SELECT m.name AS name FROM sqlite_master m JOIN pragma_table_info(m.name) i
        WHERE m.type = 'table' AND i.name = 'device' ORDER BY m.name);")"
check 'VOC-AC2 the machine on the network is the client table' 'table' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'client';")"
check 'VOC-AC2 the DHCP client identifier no longer collides with the client foreign key' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('dhcp_lease')
        WHERE name = 'dhcp_client_id';")"
check 'VOC-AC2 dhcp_lease.client_id is the foreign key to client' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_foreign_key_list('dhcp_lease')
        WHERE \"table\" = 'client' AND \"from\" = 'client_id';")"
for f in "$REPO_ROOT/ROADMAP.md" "$REPO_ROOT/CLAUDE.md"; do
    if grep -qF 'never invents a term for something those products already name' "$f"; then
        pass "VOC-AC2 $(basename "$f") states the vocabulary rule"
    else
        fail "VOC-AC2 $(basename "$f") does not state the vocabulary rule"
    fi
done

# ===========================================================================
section 'VOC-AC3, VOC-AC4, VOC-AC5 — the owner entity'
# ===========================================================================
# VOC-AC3: one person, several machines, one row per person.
check 'VOC-AC3 the owner table exists' 'table' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'owner';")"
check 'VOC-AC3 the seed created exactly the owners it was asked for' "$OWNERS" \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM owner;')"
check_ge 'VOC-AC3 at least one owner holds three machines, so a person aggregates' 3 \
    "$(q "$MAIN_DB" 'SELECT max(n) FROM (SELECT count(*) AS n FROM client
        WHERE owner_id IS NOT NULL GROUP BY owner_id);')"
expect_sql_failure 'VOC-AC3 two owners with the same display name are rejected' "$MAIN_DB" \
    "INSERT INTO owner (id, display_name, created_at, updated_at)
     SELECT NULL, display_name, $NOW, $NOW FROM owner LIMIT 1;"
expect_sql_failure 'VOC-AC3 a client owned by an owner that does not exist is rejected' "$MAIN_DB" \
    "UPDATE client SET owner_id = 999999999, owner_assigned_at = $NOW WHERE id = 1;"
expect_sql_failure 'VOC-AC3 an owner assigned with no assignment instant is rejected' "$MAIN_DB" \
    "UPDATE client SET owner_id = (SELECT min(id) FROM owner), owner_assigned_at = NULL
     WHERE id = 1;"

# VOC-AC4: ownership is assigned by the user and never inferred. The enforceable
# form: no DEFAULT, no generated expression and no trigger can put a value in
# owner_id, so only a statement somebody wrote can.
check 'VOC-AC4 owner_id carries no DEFAULT, so nothing fills it on its own' '' \
    "$(q "$MAIN_DB" "SELECT ifnull(dflt_value, '') FROM pragma_table_info('client')
        WHERE name = 'owner_id' AND dflt_value IS NOT NULL;")"
check 'VOC-AC4 no trigger exists anywhere that could derive an owner' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM sqlite_master WHERE type = 'trigger';")"
check 'VOC-AC4 owner_id is a plain column, not a generated one' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_xinfo('client')
        WHERE name = 'owner_id' AND hidden IN (2, 3);")"

# VOC-AC5: a client with no owner is normal and stays visible.
UNOWNED="$(q "$MAIN_DB" 'SELECT count(*) FROM client WHERE owner_id IS NULL;')"
check_ge 'VOC-AC5 unowned clients exist, which is the normal state of a network' 1 "$UNOWNED"
DIAG_OWNER="$WORK/diag/04_Clients_per_owner.sql"
run_query "$MAIN_DB" "$DIAG_OWNER" > "$WORK/owners.txt"
printf -- '  clients per owner:\n'
sed 's/^/    /' "$WORK/owners.txt"
check_ge 'VOC-AC5 the per-owner diagnostic returns rows' 1 \
    "$(wc -l < "$WORK/owners.txt" | tr -d ' ')"
check 'VOC-AC5 the unassigned bucket is a row of its own, not an omission' "$UNOWNED" \
    "$(awk -F'|' '$3 == "unassigned" { print $4 }' "$WORK/owners.txt")"
check 'VOC-AC5 every client is counted by the per-owner view, owned or not' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM client;')" \
    "$(awk -F'|' '{ n += $4 } END { print n + 0 }' "$WORK/owners.txt")"
check 'VOC-AC5 the per-owner view reports both an assigned and an unassigned bucket' \
    'assigned,unassigned' \
    "$(awk -F'|' '{ print $3 }' "$WORK/owners.txt" | sort -u | paste -sd, -)"

# ===========================================================================
section 'G1-AC1 .. G1-AC5 — the blocklist, observed by name and assigned by purpose'
# ===========================================================================
# G1 was the maintainer's first named gap: /api/unbound/overview/search_queries
# returns a `blocklist` field (docs/opnsense-api-survey.md, data source 5,
# "Response shape") and the schema discarded it, so "blocked by which list" was
# unanswerable. The criteria below are the correction, and they pin BOTH halves
# of it: the name is observed data, the purpose is user input, and nothing may
# derive the second from the first.
check 'G1-AC1 the blocklist table exists' 'table' \
    "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = 'blocklist';")"
check 'G1-AC1 a lookup carries the list that refused it' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('dns_resolution')
        WHERE name = 'blocklist_id';")"
expect_sql_failure 'G1-AC1 two lists with the same observed name are rejected' "$MAIN_DB" \
    "INSERT INTO blocklist (id, name, first_seen_at, last_seen_at)
     SELECT NULL, name, $NOW, $NOW FROM blocklist LIMIT 1;"
expect_sql_failure 'G1-AC1 a lookup refused by a list that does not exist is rejected' "$MAIN_DB" \
    "UPDATE dns_resolution SET blocklist_id = 999999999
     WHERE id = (SELECT min(id) FROM dns_resolution);"

# G1-AC2: the purpose is assigned, never inferred. Same enforceable form as
# VOC-AC4 for owner_id -- no DEFAULT, no generated column, no trigger.
check 'G1-AC2 purpose carries no DEFAULT, so nothing fills it on its own' '' \
    "$(q "$MAIN_DB" "SELECT dflt_value FROM pragma_table_info('blocklist')
        WHERE name = 'purpose' AND dflt_value IS NOT NULL;")"
check 'G1-AC2 purpose is a plain column, not a generated one' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_xinfo('blocklist')
        WHERE name = 'purpose' AND hidden IN (2, 3);")"
check 'G1-AC2 no trigger exists anywhere that could derive a purpose' '' \
    "$(q "$MAIN_DB" "SELECT name FROM sqlite_master WHERE type = 'trigger';")"
expect_sql_failure 'G1-AC2 a purpose outside the vocabulary is rejected' "$MAIN_DB" \
    "UPDATE blocklist SET purpose = 'whatever-the-name-suggests', purpose_assigned_at = $NOW
     WHERE id = (SELECT min(id) FROM blocklist);"
expect_sql_failure 'G1-AC2 a purpose assigned with no assignment instant is rejected' "$MAIN_DB" \
    "UPDATE blocklist SET purpose = 'threat', purpose_assigned_at = NULL
     WHERE id = (SELECT min(id) FROM blocklist);"

# G1-AC3: a list nobody has classified is normal and stays visible, and a
# blocked lookup the resolver did not attribute to any list is its own state.
check_ge 'G1-AC3 a list with no assigned purpose exists, which is the normal state' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM blocklist WHERE purpose IS NULL;')"
check_ge 'G1-AC3 a blocked lookup naming no list exists, and is a modelled state' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dns_resolution
        WHERE action IN ('block', 'drop') AND blocklist_id IS NULL;")"
check_ge 'G1-AC3 a blocked lookup naming a list exists' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dns_resolution
        WHERE action IN ('block', 'drop') AND blocklist_id IS NOT NULL;")"
check 'G1-AC3 a blocked lookup has no attribution, because it produced no flow' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM domain_attribution a
        JOIN dns_resolution r ON r.id = a.dns_resolution_id
        WHERE r.action IN ('block', 'drop');")"

# G1-AC4: the diagnostic that answers "blocked by which list".
DIAG_BLOCKLIST="$WORK/diag/05_Blocked_lookups_by_list.sql"
run_query "$MAIN_DB" "$DIAG_BLOCKLIST" > "$WORK/blocklists.txt"
printf -- '  blocked lookups by list:\n'
sed 's/^/    /' "$WORK/blocklists.txt"
check_ge 'G1-AC4 the blocked-lookups-by-list diagnostic returns rows' 1 \
    "$(wc -l < "$WORK/blocklists.txt" | tr -d ' ')"
check 'G1-AC4 every blocked lookup in the window is counted, attributed or not' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dns_resolution
        WHERE action IN ('block', 'drop')
          AND looked_up_at >= $WINDOW_START AND looked_up_at < $WINDOW_END;")" \
    "$(awk -F'|' '{ n += $5 } END { print n + 0 }' "$WORK/blocklists.txt")"
check_ge 'G1-AC4 the diagnostic reports the list-not-recorded state as a row of its own' 1 \
    "$(awk -F'|' '$3 == "list not recorded" { c += 1 } END { print c + 0 }' "$WORK/blocklists.txt")"
check_ge 'G1-AC4 the diagnostic reports an unassigned purpose as a state, not a guess' 1 \
    "$(awk -F'|' '$4 == "unassigned" { c += 1 } END { print c + 0 }' "$WORK/blocklists.txt")"

# G1-AC5: no predicate anywhere reads a list's name. Classifying a list by what
# it is called is the same defect as classifying an interface by its
# description, and the enforceable form is that no SQL this cycle ships
# compares blocklist.name to anything but an equality on a supplied value.
if grep -nE 'blocklist[._]?name[[:space:]]*(LIKE|GLOB|REGEXP)|b\.name[[:space:]]*(LIKE|GLOB|REGEXP)' \
        "$SCHEMA_FILE" \
        "$REPO_ROOT/sql/queries/screens.sql" \
        "$REPO_ROOT/sql/queries/diagnostics.sql" > "$WORK/name_predicates.txt"; then
    fail "G1-AC5 a predicate matches on a blocklist name: $(cat "$WORK/name_predicates.txt")"
else
    pass 'G1-AC5 no predicate anywhere matches on what a blocklist is called'
fi

# ===========================================================================
section 'G11-AC1 .. G11-AC5 — the per-owner aggregates'
# ===========================================================================
# G11: the owner entity existed and no aggregate was keyed on it, so a
# per-person question could only be answered inside the flow horizon. The four
# tables below are the correction, and the unassigned bucket is part of it:
# ownership is assigned by hand, so a per-person aggregate that dropped the
# unowned clients would under-report the network while looking complete.
OWNER_AGGREGATES='owner_volume_aggregate_1h owner_volume_aggregate_24h
owner_volume_aggregate_7d owner_volume_aggregate_30d'
for t in $OWNER_AGGREGATES; do
    check "G11-AC1 $t exists" 'table' \
        "$(q "$MAIN_DB" "SELECT type FROM sqlite_master WHERE name = '$t';")"
    check "G11-AC2 every $t row carries a freshness timestamp" '0' \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $t WHERE computed_at IS NULL;")"
    check_ge "G11-AC3 $t holds an unassigned slot" 1 \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $t WHERE owner_id IS NULL;")"
    check_ge "G11-AC3 $t holds an assigned slot" 1 \
        "$(q "$MAIN_DB" "SELECT count(*) FROM $t WHERE owner_id IS NOT NULL;")"
done
# G11-AC4: the slot is unique, and the unassigned slot participates in that
# uniqueness rather than escaping it through a NULL.
expect_sql_failure 'G11-AC4 a duplicate assigned slot is rejected' "$MAIN_DB" \
    "INSERT INTO owner_volume_aggregate_24h (period_start_at, period_end_at, owner_id,
        traffic_scope, bytes, allowed_connections, blocked_connections, client_count, computed_at)
     SELECT period_start_at, period_end_at, owner_id, traffic_scope, 0, 0, 0, 0, $NOW
     FROM owner_volume_aggregate_24h WHERE owner_id IS NOT NULL LIMIT 1;"
expect_sql_failure 'G11-AC4 a duplicate unassigned slot is rejected too' "$MAIN_DB" \
    "INSERT INTO owner_volume_aggregate_24h (period_start_at, period_end_at, owner_id,
        traffic_scope, bytes, allowed_connections, blocked_connections, client_count, computed_at)
     SELECT period_start_at, period_end_at, NULL, traffic_scope, 0, 0, 0, 0, $NOW
     FROM owner_volume_aggregate_24h WHERE owner_id IS NULL LIMIT 1;"
expect_sql_failure 'G11-AC4 a slot attributed to an owner that does not exist is rejected' "$MAIN_DB" \
    "UPDATE owner_volume_aggregate_24h SET owner_id = 999999999
     WHERE id = (SELECT min(id) FROM owner_volume_aggregate_24h);"
expect_sql_failure 'G11-AC4 a negative byte count in a per-owner slot is rejected' "$MAIN_DB" \
    "UPDATE owner_volume_aggregate_24h SET bytes = -1
     WHERE id = (SELECT min(id) FROM owner_volume_aggregate_24h);"

# G11-AC5: the aggregate agrees with the flows it was computed from, so a
# per-person card reading it does not contradict a per-person card reading
# flow. The 24 h family is checked; the arithmetic is the same for all four.
DIAG_OWNER_AGG="$WORK/diag/06_Owner_aggregate_coverage_per_period.sql"
run_query "$MAIN_DB" "$DIAG_OWNER_AGG" > "$WORK/owner_coverage.txt"
printf -- '  per-owner aggregate coverage:\n'
sed 's/^/    /' "$WORK/owner_coverage.txt"
check 'G11-AC5 the per-owner coverage diagnostic returns one row per period' '4' \
    "$(wc -l < "$WORK/owner_coverage.txt" | tr -d ' ')"
check 'G11-AC5 every one of the four per-owner periods is non-empty' '' \
    "$(awk -F'|' '$2 + 0 == 0 { print $1 }' "$WORK/owner_coverage.txt")"
check 'G11-AC5 every one of the four per-owner periods carries an unassigned slot' '' \
    "$(awk -F'|' '$7 + 0 == 0 { print $1 }' "$WORK/owner_coverage.txt")"
check 'G11-AC5 the per-owner aggregate totals agree with the flows behind them' \
    "$(q "$MAIN_DB" 'SELECT sum(f.packet_bytes) FROM flow f
        JOIN client c ON c.id = f.src_client_id;')" \
    "$(q "$MAIN_DB" 'SELECT sum(bytes) FROM owner_volume_aggregate_24h;')"
check 'G11-AC5 an unowned client lands in the unassigned slot rather than being dropped' \
    "$(q "$MAIN_DB" 'SELECT sum(f.packet_bytes) FROM flow f
        JOIN client c ON c.id = f.src_client_id WHERE c.owner_id IS NULL;')" \
    "$(q "$MAIN_DB" 'SELECT sum(bytes) FROM owner_volume_aggregate_24h WHERE owner_id IS NULL;')"

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
        "$PURGE_FILE" > "$WORK/retention_literals.txt"; then
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
        printf -- '.read %s\n' "$PURGE_FILE"
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
dhcp_lease:observed_at client:last_seen_at pair_volume_observation:day_start_at
geo_asn:looked_up_at domain_attribution:attributed_at
collection_gap:detected_at measurement_sample:sampled_at
state_snapshot:captured_at
volume_aggregate_1h:period_end_at volume_aggregate_24h:period_end_at
volume_aggregate_7d:period_end_at volume_aggregate_30d:period_end_at
owner_volume_aggregate_1h:period_end_at owner_volume_aggregate_24h:period_end_at
owner_volume_aggregate_7d:period_end_at owner_volume_aggregate_30d:period_end_at'
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
check 'VOC-AC3 the purge left the owner table untouched' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM owner;')" \
    "$(q "$PURGE_DB" 'SELECT count(*) FROM owner;')"
# G1-AC6: a blocklist purpose is user input, exactly like an owner. Purging the
# lookups that named a list must not discard the classification behind it.
check 'G1-AC6 the purge left the blocklist table untouched' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM blocklist;')" \
    "$(q "$PURGE_DB" 'SELECT count(*) FROM blocklist;')"
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
CYCLE_FILES="$SCHEMA_FILE
$PURGE_FILE
$REPO_ROOT/sql/queries/screens.sql
$REPO_ROOT/sql/queries/diagnostics.sql
$REPO_ROOT/sql/seed.sql
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
section 'AC35 — no assumed interface, client or interface count'
# ===========================================================================
apply_schema "$ALT_DB" "$WORK/apply_alt.err" || fail 'AC35 the alternative database failed to take the schema'
seed_database "$ALT_DB" "$ALT_INTERFACES" "$ALT_CLIENTS" "$ALT_RULES" "$ALT_FLOW_ROWS" \
    "$ALT_ALERTS" "$ALT_PAIR_ROWS" "$ALT_OWNERS" "$ALT_IPV6_EVERY"
check 'AC35 the alternative seed has a different interface count' "$ALT_INTERFACES" \
    "$(q "$ALT_DB" 'SELECT count(*) FROM interface;')"
check 'AC35 the alternative seed has a different owner count' "$ALT_OWNERS" \
    "$(q "$ALT_DB" 'SELECT count(*) FROM owner;')"
check 'AC35 the alternative database is consistent' '' "$(q "$ALT_DB" 'PRAGMA foreign_key_check;')"
ALT_INTERFACE_ID=$INTERFACE_ID
ALT_CLIENT_ID=$CLIENT_ID
for f in "$WORK"/screens/[0-9]*.sql; do
    name="$(head -n 1 "$f" | sed 's/^-- screen: //')"
    rows="$(run_query "$ALT_DB" "$f" | wc -l | tr -d ' ')"
    if [ "$rows" -ge 1 ]; then
        pass "AC35 the $name query returned $rows rows against the alternative seed"
    else
        fail "AC35 the $name query returned no rows against the alternative seed"
    fi
done
printf -- '  alternative seed used interfaces=%s clients=%s rules=%s owners=%s flows=%s (interface_id=%s client_id=%s)\n' \
    "$ALT_INTERFACES" "$ALT_CLIENTS" "$ALT_RULES" "$ALT_OWNERS" "$ALT_FLOW_ROWS" \
    "$ALT_INTERFACE_ID" "$ALT_CLIENT_ID"

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
# The provider-neutral criteria follow, labelled PN-AC*. Those above are the
# data model's own, restated against the renamed objects where a later pass
# renamed one.
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
    "$SCHEMA_FILE" "$PURGE_FILE" "$REPO_ROOT/sql" \
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
apply_schema "$FRESH_DB" "$WORK/apply_fresh.err" || fail 'PN-AC10 the fresh database failed to take the schema'
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

check 'VOC-AC4 a freshly migrated database attributes nothing to anybody' '0' \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM owner;')"
check 'PN-AC12 no provider is active on a freshly migrated database' '0' \
    "$(q "$FRESH_DB" 'SELECT count(*) FROM provider WHERE is_active = 1;')"

expect_sql_failure 'PN-AC9 a registry row whose kind is outside the eight is rejected' "$FRESH_DB" \
    "INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
     VALUES ('telepathy', 'pn-ac9', 'PN-AC9', 0, $NOW);"
extract_block "$ARCH_DOC" '<!-- provider-kinds:begin -->' '<!-- provider-kinds:end -->' |
    sort > "$WORK/doc_kinds.txt"
check 'PN-AC9 the document lists exactly eight kinds' '8' \
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
# PN-AC12, RESTATED AGAINST THE RULE THAT REPLACED UNIVERSAL EXCLUSIVITY. These
# assertions named a property the model deliberately changed, not one that stopped
# mattering, so they are restated and not removed. The rule:
#
#   A KIND ADMITS SEVERAL CONCURRENTLY ACTIVE PROVIDERS EXACTLY WHEN THE IDENTITY
#   OF ITS DESTINATION ROWS INCLUDES THE PROVIDER.
#
# Four kinds qualify -- security_event, dhcp_lease, measurement_sample and
# reconciled_state -- and the rest are exclusive. What changed is which kinds are
# exempt, and what the seed's active set looks like now that one of them is a kind
# this installation really runs two of: the seed used to activate exactly one
# provider per kind and now activates two dhcp_lease providers, so the active set
# is asserted by name below rather than as a count.
EXCLUSIVE_PREDICATE="p.kind NOT IN ('security_event', 'dhcp_lease', 'measurement_sample',
                                    'reconciled_state')"
check 'PN-AC12 the registry holds a provider of every one of the eight kinds' '8' "$KIND_COUNT"
# The active set is asserted as a SET rather than as a count derived from the kind
# count, and that is the restatement: the seed used to activate exactly one provider
# per kind, and it now models a two-DHCP-server estate, so a count alone would no
# longer say which shape it is in. Written out, it fails loudly whichever way it
# drifts -- a server that stopped being read, or one that started.
EXPECTED_ACTIVE='dhcp_lease/dnsmasq
dhcp_lease/kea
dns_lookup/unbound
firewall_log/pf
geo_asn/maxmind_geolite2
measurement_sample/insight
reconciled_state/example-state-source
security_event/suricata'
check 'PN-AC12 the seed activates exactly the providers it names' "$EXPECTED_ACTIVE"     "$(q "$MAIN_DB" "SELECT kind || '/' || provider_key FROM provider
        WHERE is_active = 1 ORDER BY kind, provider_key;")"
ACTIVE_PROVIDER_COUNT="$(printf -- '%s
' "$EXPECTED_ACTIVE" | wc -l | tr -d ' ')"
# flow_volume is not activated: nothing collects it, because its destination is
# derived from flow.
check 'PN-AC12 the kind nothing collects has no active provider' '0'     "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE kind = 'flow_volume' AND is_active = 1;")"
check 'PN-AC12 no EXCLUSIVE kind has two active providers' '0'     "$(q "$MAIN_DB" "SELECT count(*) FROM (SELECT p.kind FROM provider AS p
        WHERE p.is_active = 1 AND $EXCLUSIVE_PREDICATE
        GROUP BY p.kind HAVING count(*) > 1);")"
check 'PN-AC12 the seed models a two-server DHCP estate, which is why the kind is concurrent' '2'     "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE kind = 'dhcp_lease' AND is_active = 1;")"
expect_sql_failure 'PN-AC12 marking a second provider of an EXCLUSIVE kind active is rejected' "$MAIN_DB" \
    "UPDATE provider SET is_active = 1
     WHERE id = (SELECT p.id FROM provider p
                 JOIN provider q ON q.kind = p.kind AND q.is_active = 1
                 WHERE p.is_active = 0 AND $EXCLUSIVE_PREDICATE LIMIT 1);"
# And the index that enforces it says which kinds it exempts, in its own
# definition, so the rule cannot be documented one way and enforced another.
check 'PN-AC12 the exclusivity index exists under its new name' '1' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master
        WHERE type = 'index' AND name = 'uq_provider_active_per_exclusive_kind';")"
check 'PN-AC12 the index that admitted one provider per kind is gone' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master
        WHERE type = 'index' AND name = 'uq_provider_active_per_kind';")"
for exempt in security_event dhcp_lease measurement_sample reconciled_state; do
    if q "$MAIN_DB" "SELECT sql FROM sqlite_master
            WHERE name = 'uq_provider_active_per_exclusive_kind';" |
            grep -qF "'$exempt'"; then
        pass "PN-AC12 the index exempts the $exempt kind, whose rows carry their provider"
    else
        fail "PN-AC12 the index does not exempt the $exempt kind, so its second active provider would be rejected"
    fi
done

DIAG_AVAIL="$WORK/diag/03_Source_availability.sql"
run_query "$MAIN_DB" "$DIAG_AVAIL" > "$WORK/availability.txt"
printf -- '  provider availability diagnostic:\n'
sed 's/^/    /' "$WORK/availability.txt"
check 'PN-AC13 the diagnostic returns one row per registry row' "$PROVIDER_COUNT" \
    "$(wc -l < "$WORK/availability.txt" | tr -d ' ')"
check 'PN-AC13 every diagnostic row carries a kind, a key, a state, a probe and an active flag' '0' \
    "$(awk -F'|' 'NF < 8 || $1 == "" || $2 == "" || $4 == "" || $5 == "" || $8 == "" { n++ }
                  END { print n + 0 }' "$WORK/availability.txt")"
check 'PN-AC13 the diagnostic names every active provider, two DHCP servers included' \
    "$ACTIVE_PROVIDER_COUNT" \
    "$(awk -F'|' '$8 == 1 { n++ } END { print n + 0 }' "$WORK/availability.txt")"
# And each of the two DHCP servers carries ITS OWN probe: one service-status
# endpoint informing two registry rows would be the smear the seam exists to
# prevent, and with two servers active it would now be invisible in a total.
check 'PN-AC13 the two active DHCP servers carry two different probes' '2' \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT a.probe) FROM source_availability a
        JOIN provider p ON p.id = a.provider_id
        WHERE p.kind = 'dhcp_lease' AND p.is_active = 1;")"

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
         src_client_id, src_interface_id)
       SELECT p.id, 'pn-ac8-event-' || k.n, $WINDOW_END - 60, $WINDOW_END - 30,
              'named-rule-identity-alpha', 'second provider signature', 'blocked',
              CASE WHEN k.n = 1 THEN 'critical' END,
              e.src_address, e.src_port, e.dst_address, e.dst_port,
              e.protocol, e.src_client_id, e.src_interface_id
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
section 'EX-AC1 .. EX-AC4 — two active providers of one kind, which is the normal case'
# ===========================================================================
# The registry now holds two implementations of the security_event kind, so this
# is where the rule that replaced universal exclusivity is exercised rather than
# merely read out of an index definition.
#
# THE CASE: people run several detection engines side by side, and the how-to
# corpus is people stacking them (survey, "Four findings that bear on the
# collectors already written"). Event identity is (provider_id,
# provider_event_key), so two engines reporting one intrusion are two attributed
# rows and no figure is doubled -- which is exactly the condition under which the
# rule permits two active providers.
expect_sql_success 'EX-AC1 two providers of the security_event kind can both be active' "$REGISTER_DB" \
    "UPDATE provider SET is_active = 1 WHERE kind = 'security_event';"
check 'EX-AC1 and both stay active' '2' \
    "$(q "$REGISTER_DB" "SELECT count(*) FROM provider
        WHERE kind = 'security_event' AND is_active = 1;")"
check 'EX-AC1 the two active providers are distinguishable by key' 'second-event-provider|suricata' \
    "$(q "$REGISTER_DB" "SELECT group_concat(provider_key, '|') FROM
        (SELECT provider_key FROM provider WHERE kind = 'security_event' AND is_active = 1
         ORDER BY provider_key);")"

# EX-AC2: the same event key under two active providers is two rows, because the
# identity carries the provider. This is the property that makes concurrency safe,
# and it is asserted rather than assumed.
expect_sql_success 'EX-AC2 one event key under two active providers is two rows' "$REGISTER_DB" \
    "INSERT INTO security_event (provider_id, provider_event_key, occurred_at, ingested_at,
         rule_identity, signature, event_action, src_address, dst_address)
     SELECT id, 'ex-ac2-shared-event-key', $WINDOW_END - 120, $WINDOW_END - 60,
            'named-rule-identity-alpha', 'ex-ac2 signature', 'blocked',
            'ex-ac2-source', 'ex-ac2-destination'
     FROM provider WHERE kind = 'security_event' AND is_active = 1;"
check 'EX-AC2 both rows are stored and attributed' '2' \
    "$(q "$REGISTER_DB" "SELECT count(*) FROM security_event
        WHERE provider_event_key = 'ex-ac2-shared-event-key';")"
check 'EX-AC2 each names a different provider' '2' \
    "$(q "$REGISTER_DB" "SELECT count(DISTINCT provider_id) FROM security_event
        WHERE provider_event_key = 'ex-ac2-shared-event-key';")"
check 'EX-AC2 the Alerts screen returns both events of the shared key' '2' \
    "$(run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
        'SELECT count(*) FROM (' \
        ") WHERE signature = 'ex-ac2 signature';")")"
check 'EX-AC2 and attributes them to the two different providers' '2' \
    "$(run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
        'SELECT count(DISTINCT provider_key) FROM (' \
        ") WHERE signature = 'ex-ac2 signature';")")"
# Each resolves its severity through its OWN provider's cache entry, which is what
# keeps two concurrent engines from borrowing each other's classification.
check 'EX-AC2 each of the two resolves its severity through its own provider entry' \
    'informational,low' \
    "$(run_query "$REGISTER_DB" "$(wrap_query "$WORK/screens/06_Alerts.sql" \
        "SELECT group_concat(severity, ',') FROM (SELECT DISTINCT severity FROM (" \
        ") WHERE signature = 'ex-ac2 signature' ORDER BY severity);")")"

# EX-AC3, RESTATED. This assertion named dhcp_lease as the exclusive kind, and the
# maintainer moved that kind into the concurrent set: one server issuing on one VLAN
# and another on a second is an ordinary deployment, not a misconfiguration. So it is
# restated in BOTH directions rather than deleted -- the kind that moved is asserted
# to permit two, and the kinds that did not move still refuse them, on the same
# database, which keeps the rule a discrimination rather than a blanket permission.
expect_sql_success 'EX-AC3 two providers of the dhcp_lease kind CAN both be active' "$REGISTER_DB" \
    "UPDATE provider SET is_active = 1 WHERE kind = 'dhcp_lease';"
check 'EX-AC3 and all of them stay active' '3' \
    "$(q "$REGISTER_DB" "SELECT count(*) FROM provider
        WHERE kind = 'dhcp_lease' AND is_active = 1;")"
expect_sql_failure 'EX-AC3 two providers of the dns_lookup kind cannot both be active' "$REGISTER_DB" \
    "UPDATE provider SET is_active = 1 WHERE kind = 'dns_lookup';"

# EX-AC4: the concurrency is still bounded by the registry. A kind that admits
# several active providers does not admit an unregistered one.
expect_sql_failure 'EX-AC4 an active provider of a kind outside the eight is still rejected' "$REGISTER_DB" \
    "INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
     VALUES ('telepathy', 'ex-ac4', 'EX-AC4', 1, $NOW);"

# ===========================================================================
section 'ST-AC1 .. ST-AC6 — the reconciled-state kind, and the departure it exists for'
# ===========================================================================
# THE SHAPE: here is the complete set of things of this type, as of now. Ten of
# the eleven surveyed sources that fit no other kind produce it (survey, "Shapes
# the model has no room for"), and the question it answers is what has LEFT.
check 'ST-AC1 the reconciled-state kind has a snapshot table and a member table' '2' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM sqlite_master
        WHERE type = 'table' AND name IN ('state_snapshot', 'state_item');")"
check 'ST-AC1 a snapshot carries a provider, a set and the instant it was complete' '3' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('state_snapshot')
        WHERE name IN ('provider_id', 'set_key', 'captured_at');")"
check 'ST-AC1 a member carries an identity, its attributes and an optional validity end' '3' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('state_item')
        WHERE name IN ('item_key', 'attributes', 'valid_until_at');")"
# ST-AC1, the negative half, and the one the risk section of the spec asks for:
# the kind carries NOTHING the examined sources do not need. No status, no
# category, no severity, no enabled flag, no display name.
check 'ST-AC1 the member table carries no invented status, category or severity' '' \
    "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM pragma_table_info('state_item')
        WHERE name IN ('status', 'state', 'category', 'severity', 'enabled',
                       'display_name', 'kind', 'type', 'origin', 'reason');")"
check 'ST-AC1 the member table is exactly five columns wide' '5' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('state_item');")"
check 'ST-AC1 the snapshot table is exactly four columns wide' '4' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_info('state_snapshot');")"

# ST-AC2: the seed holds several complete captures of two sets, each capture
# holding one fewer thing than its predecessor, so a departure exists to find.
check_ge 'ST-AC2 the seed holds several complete captures' 2 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_snapshot;')"
check_ge 'ST-AC2 one provider reports more than one set' 2 \
    "$(q "$MAIN_DB" 'SELECT count(DISTINCT set_key) FROM state_snapshot;')"
check 'ST-AC2 every capture names a provider that exists' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_snapshot s
        WHERE NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = s.provider_id);')"
check 'ST-AC2 every member belongs to a capture that exists' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item i
        WHERE NOT EXISTS (SELECT 1 FROM state_snapshot s WHERE s.id = i.snapshot_id);')"

# ST-AC3: A DEPARTURE IS DETECTABLE. This is the criterion the whole kind exists
# for: a thing in the previous complete capture and not in the latest one has LEFT,
# and the model says so rather than silently keeping it or silently dropping it.
check_ge 'ST-AC3 a departure is reported for every set whose membership shrank' 2 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item_departure;')"
check 'ST-AC3 a departure is dated: last present, and absent since' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item_departure
        WHERE last_present_at IS NULL OR absent_since_at IS NULL
           OR absent_since_at <= last_present_at;')"
check 'ST-AC3 nothing still in the latest capture is reported as having left' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item_departure d
        JOIN state_snapshot s ON s.provider_id = d.provider_id AND s.set_key = d.set_key
                             AND s.captured_at = d.absent_since_at
        JOIN state_item i ON i.snapshot_id = s.id AND i.item_key = d.item_key;')"
check 'ST-AC3 nothing is silently dropped: the departed thing is still readable where it was' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item_departure d
        WHERE NOT EXISTS (
            SELECT 1 FROM state_snapshot s
            JOIN state_item i ON i.snapshot_id = s.id
            WHERE s.provider_id = d.provider_id AND s.set_key = d.set_key
              AND s.captured_at = d.last_present_at AND i.item_key = d.item_key);')"

# ST-AC4: the constraints that make a set a set rather than a bag of rows.
cp "$MAIN_DB" "$STATE_DB"
expect_sql_failure 'ST-AC4 a member with no identity is rejected' "$STATE_DB" \
    "INSERT INTO state_item (snapshot_id, item_key) SELECT id, '' FROM state_snapshot LIMIT 1;"
expect_sql_failure 'ST-AC4 one capture cannot report one thing twice' "$STATE_DB" \
    "INSERT INTO state_item (snapshot_id, item_key, attributes, valid_until_at)
     SELECT snapshot_id, item_key, attributes, valid_until_at FROM state_item LIMIT 1;"
expect_sql_failure 'ST-AC4 attributes that are not a JSON object are rejected' "$STATE_DB" \
    "INSERT INTO state_item (snapshot_id, item_key, attributes)
     SELECT id, 'st-ac4-item', '\"a bare string\"' FROM state_snapshot LIMIT 1;"
expect_sql_failure 'ST-AC4 a capture with no set name is rejected' "$STATE_DB" \
    "INSERT INTO state_snapshot (provider_id, set_key, captured_at)
     SELECT id, '', $NOW FROM provider WHERE kind = 'reconciled_state' LIMIT 1;"
expect_sql_failure 'ST-AC4 one set cannot be captured twice at one instant' "$STATE_DB" \
    "INSERT INTO state_snapshot (provider_id, set_key, captured_at)
     SELECT provider_id, set_key, captured_at FROM state_snapshot LIMIT 1;"
expect_sql_failure 'ST-AC4 a capture attributed to no provider is rejected' "$STATE_DB" \
    "INSERT INTO state_snapshot (provider_id, set_key, captured_at)
     VALUES (999999999, 'st-ac4-set', $NOW);"

# ST-AC5: an optional validity end, because the surveyed ban list carries a TTL
# and no timestamp at all -- and it is NOT how a departure is detected.
check_ge 'ST-AC5 some members carry a validity end and some do not' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item WHERE valid_until_at IS NOT NULL;')"
check_ge 'ST-AC5 a member with no validity end is a normal row' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item WHERE valid_until_at IS NULL;')"
check_ge 'ST-AC5 some members carry attributes and some carry none' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM state_item WHERE attributes IS NOT NULL;')"
check 'ST-AC5 every stored attribute set is a JSON object' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM state_item
        WHERE attributes IS NOT NULL AND json_type(attributes) <> 'object';")"

# ST-AC6: removing a capture removes its members, because a member with no capture
# would be a set member with no set and no instant -- the one thing this kind
# exists to prevent.
STATE_MEMBERS_BEFORE="$(q "$STATE_DB" 'SELECT count(*) FROM state_item;')"
sqlite3 "$STATE_DB" "PRAGMA foreign_keys = ON;
    DELETE FROM state_snapshot WHERE id = (SELECT min(id) FROM state_snapshot);" > /dev/null
check 'ST-AC6 removing a capture removed its members with it' '0' \
    "$(q "$STATE_DB" 'SELECT count(*) FROM state_item i
        WHERE NOT EXISTS (SELECT 1 FROM state_snapshot s WHERE s.id = i.snapshot_id);')"
if [ "$(q "$STATE_DB" 'SELECT count(*) FROM state_item;')" -lt "$STATE_MEMBERS_BEFORE" ]; then
    pass 'ST-AC6 the cascade actually removed something, so the check is not vacuous'
else
    fail 'ST-AC6 removing a capture removed no member, so nothing was cascaded'
fi

# ===========================================================================
section 'DL-AC1 .. DL-AC4 — the lease start stops meaning two things'
# ===========================================================================
# The finding is unchanged: Kea reports valid_lifetime, so `expire` minus it is a
# REAL validity start. What changed is what the backends that report none do.
# starts_at is nullable now, so such a backend says so instead of storing its
# expiry in a column named for a start, and generation_key is what keeps a re-poll
# idempotent.
check 'DL-AC1 the lease validity start is nullable' '0' \
    "$(q "$MAIN_DB" "SELECT \"notnull\" FROM pragma_table_info('dhcp_lease')
        WHERE name = 'starts_at';")"
check 'DL-AC1 the generation key is not' '1' \
    "$(q "$MAIN_DB" "SELECT \"notnull\" FROM pragma_table_info('dhcp_lease')
        WHERE name = 'generation_key';")"
check_ge 'DL-AC2 a backend that reports a real start stores it' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dhcp_lease
        WHERE backend = 'kea' AND starts_at IS NOT NULL
          AND generation_key = 'start:' || starts_at;")"
check_ge 'DL-AC2 a backend that reports none stores null' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dhcp_lease
        WHERE backend = 'dnsmasq' AND starts_at IS NULL;")"
check 'DL-AC2 no lease stores its expiry as its validity start' '0' \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM dhcp_lease
        WHERE starts_at IS NOT NULL AND starts_at = expires_at;')"
check_ge 'DL-AC3 a lease with no start is keyed on its expiry' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dhcp_lease
        WHERE starts_at IS NULL AND expires_at IS NOT NULL
          AND generation_key = 'expiry:' || expires_at;")"
check_ge 'DL-AC3 a standing reservation is keyed on the day it was observed' 1 \
    "$(q "$MAIN_DB" "SELECT count(*) FROM dhcp_lease
        WHERE starts_at IS NULL AND expires_at IS NULL
          AND generation_key = 'observed_day:' || (observed_at / 86400 * 86400);")"
# DL-AC4: the generation key keeps a re-poll idempotent, INCLUDING for the leases
# whose start is null -- which a key on starts_at could not have done, because a
# null in a uniqueness constraint is distinct from every other null.
expect_sql_failure 'DL-AC4 re-polling a lease with a real start inserts nothing' "$MAIN_DB" \
    "INSERT INTO dhcp_lease (client_id, backend, address, mac, hostname, lease_state,
         interface_id, starts_at, generation_key, expires_at, observed_at)
     SELECT client_id, backend, address, mac, hostname, lease_state, interface_id,
            starts_at, generation_key, expires_at, observed_at + 60
     FROM dhcp_lease WHERE starts_at IS NOT NULL LIMIT 1;"
expect_sql_failure 'DL-AC4 re-polling a lease with NO start inserts nothing either' "$MAIN_DB" \
    "INSERT INTO dhcp_lease (client_id, backend, address, mac, hostname, lease_state,
         interface_id, starts_at, generation_key, expires_at, observed_at)
     SELECT client_id, backend, address, mac, hostname, lease_state, interface_id,
            starts_at, generation_key, expires_at, observed_at + 60
     FROM dhcp_lease WHERE starts_at IS NULL LIMIT 1;"
# DL-AC4, RESTATED: the identity names the PROVIDER and not the backend. `backend`
# is a normalised vocabulary of response shapes, and two providers could report the
# same one -- a second implementation reading a Kea running somewhere else would --
# so keying on it satisfied the letter of the concurrency rule and not its substance.
# The rule is stated in terms of the provider, and now so is the key.
check 'DL-AC4 the lease identity is (address, generation_key, provider_id)' '3' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_index_list('dhcp_lease') il
        JOIN pragma_index_info(il.name) ii WHERE il.origin = 'u'
          AND ii.name IN ('address', 'generation_key', 'provider_id');")"
check 'DL-AC4 the identity is exactly those three columns' '3' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_index_list('dhcp_lease') il
        JOIN pragma_index_info(il.name) ii WHERE il.origin = 'u';")"
check 'DL-AC4 and it names neither the validity start nor the backend' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_index_list('dhcp_lease') il
        JOIN pragma_index_info(il.name) ii WHERE il.origin = 'u'
          AND ii.name IN ('starts_at', 'backend');")"
check 'DL-AC4 no lease is unattributed: the issuing server is mandatory' '1' \
    "$(q "$MAIN_DB" "SELECT \"notnull\" FROM pragma_table_info('dhcp_lease')
        WHERE name = 'provider_id';")"
expect_sql_failure 'DL-AC4 a lease attributed to a server that does not exist is rejected' "$MAIN_DB" \
    "INSERT INTO dhcp_lease (provider_id, backend, address, lease_state, generation_key,
         observed_at)
     VALUES (999999999, 'kea', 'dl-ac4-address', 'active', 'start:1', $NOW);"

# ===========================================================================
section 'DL-AC5 — one machine, two servers, two VLANs: one client, two leases'
# ===========================================================================
# THIS IS THE CLAIM THE CONCURRENCY DECISION RESTS ON, so it is asserted directly
# rather than left to follow from the key.
#
# The objection to making dhcp_lease concurrent was that two active lease providers
# would put one machine on the screen twice under two names. The client identity
# cascade is what answers it: it keys on the DHCP client identifier first and on the
# MAC second, and NEITHER is scoped to an interface, so one machine leased on two
# VLANs by two servers resolves to one client holding two leases. The duplication the
# objection feared needs two servers issuing on the SAME scope, which is a
# misconfiguration of the firewall rather than a shape this model absorbs.
#
# The seed models the deployment explicitly: one client, a lease from each server, on
# two different interfaces. The cascade itself is exercised in Go, in
# internal/collect, where the two backends' reports of one MAC actually meet.
cp "$MAIN_DB" "$LEASE_DB"
check_ge 'DL-AC5 a machine holds leases from two different servers' 1 \
    "$(q "$LEASE_DB" "SELECT count(*) FROM (
        SELECT l.client_id FROM dhcp_lease AS l
        WHERE l.client_id IS NOT NULL
        GROUP BY l.client_id
        HAVING count(DISTINCT l.provider_id) >= 2);")"
check_ge 'DL-AC5 and it is ONE client row, not two' 1 \
    "$(q "$LEASE_DB" "SELECT count(*) FROM client AS c
        WHERE (SELECT count(DISTINCT l.provider_id) FROM dhcp_lease AS l
               WHERE l.client_id = c.id) >= 2;")"
check_ge 'DL-AC5 one machine holds leases on two different interfaces' 1 \
    "$(q "$LEASE_DB" "SELECT count(*) FROM (
        SELECT l.client_id FROM dhcp_lease AS l
        WHERE l.client_id IS NOT NULL AND l.interface_id IS NOT NULL
        GROUP BY l.client_id
        HAVING count(DISTINCT l.provider_id) >= 2
           AND count(DISTINCT l.interface_id) >= 2);")"
check 'DL-AC5 every lease names a server that exists' '0' \
    "$(q "$LEASE_DB" "SELECT count(*) FROM dhcp_lease AS l
        WHERE NOT EXISTS (SELECT 1 FROM provider p WHERE p.id = l.provider_id);")"
# The SAME address leased by two different servers is two leases, not one contested
# row -- the direct consequence of the provider joining the identity, and a case a key
# on the backend allowed only by accident, for as long as one backend meant one server.
expect_sql_success 'DL-AC5 one address leased by two servers is two leases' "$LEASE_DB" \
    "INSERT INTO dhcp_lease (provider_id, backend, address, lease_state, generation_key,
         observed_at)
     SELECT id, 'kea', 'dl-ac5-two-servers', 'active', 'start:1', $NOW
     FROM provider WHERE kind = 'dhcp_lease' AND is_active = 1;"
check 'DL-AC5 both of them are stored' '2' \
    "$(q "$LEASE_DB" "SELECT count(*) FROM dhcp_lease WHERE address = 'dl-ac5-two-servers';")"
expect_sql_failure 'DL-AC5 and one server still cannot report it twice' "$LEASE_DB" \
    "INSERT INTO dhcp_lease (provider_id, backend, address, lease_state, generation_key,
         observed_at)
     SELECT provider_id, backend, address, lease_state, generation_key, observed_at + 60
     FROM dhcp_lease WHERE address = 'dl-ac5-two-servers' LIMIT 1;"

# The addendum: the server is READABLE per lease, not merely a key component. The
# diagnostic is that read, and it names the server rather than reconstructing it.
DIAG_LEASES="$WORK/diag/07_Leases_per_client_and_issuing_server.sql"
TWO_SERVER_CLIENT="$(q "$MAIN_DB" "SELECT l.client_id FROM dhcp_lease AS l
    WHERE l.client_id IS NOT NULL
    GROUP BY l.client_id
    HAVING count(DISTINCT l.provider_id) >= 2
    ORDER BY l.client_id LIMIT 1;")"
LEASES_OF_ONE="$(run_query_for_client "$MAIN_DB" "$DIAG_LEASES" "$TWO_SERVER_CLIENT")"
printf -- '  the leases of one machine, per issuing server:\n'
printf -- '%s\n' "$LEASES_OF_ONE" | sed 's/^/    /'
check_ge 'DL-AC5 the diagnostic returns both leases of that machine' 2 \
    "$(printf -- '%s\n' "$LEASES_OF_ONE" | grep -c .)"
check 'DL-AC5 the diagnostic names two different issuing servers' '2' \
    "$(printf -- '%s\n' "$LEASES_OF_ONE" | cut -d'|' -f3 | sort -u | grep -c .)"
check 'DL-AC5 every returned lease names its server in words' '0' \
    "$(printf -- '%s\n' "$LEASES_OF_ONE" | awk -F'|' 'NF > 1 && $3 == "" { n++ } END { print n + 0 }')"
if grep -q 'p.display_name  *AS issuing_server' "$REPO_ROOT/sql/queries/diagnostics.sql"; then
    pass 'DL-AC5 the diagnostic reads the server name rather than reconstructing it'
else
    fail 'DL-AC5 the diagnostic does not read provider.display_name, so a screen would have to guess'
fi

# ===========================================================================
section 'PV-AC1, PV-AC2 — the per-pair volume is derived, not collected'
# ===========================================================================
# The maintainer's ruling: the only per-pair endpoint carries neither a port nor a
# protocol, and `flow` carries both exactly, so these rows are step 5's to compute
# from `flow`. The de-duplication above (AC25) still holds, because the derivation
# has to respect it; what is asserted here is that nothing COLLECTS them.
#
# The test sources are excluded, and deliberately: the test that asserts the
# derivation's de-duplication contract has to write the table in order to assert
# it, and matching itself would make this check unfalsifiable rather than strict.
# What is forbidden is a COLLECTOR, which is non-test code.
#
# The scan NORMALISES EACH FILE WHOLE before matching, and that is not cosmetic: a
# long SQL statement in Go is ordinarily wrapped, either as a raw literal spanning
# lines or as quoted fragments joined with +, so a collector that put the table
# name on the line after INSERT INTO would have passed a line-by-line grep while
# writing the table. Newlines, tabs, quotes, backticks, brackets and + all become
# spaces, the schema qualifier is dropped, and runs of spaces collapse, so every
# wrapping reads as the one statement it is -- and none of those characters can
# occur inside an SQL identifier, so the normalisation cannot invent a match.
#
# EVERY WRITING VERB IS ENUMERATED BELOW, NOT ONLY INSERT, and the reason is worth
# recording because an earlier revision of this check got it wrong: it enumerated
# the six INSERT conflict clauses and claimed that covered the statement, which is
# true of conflict clauses and false of the statement. REPLACE INTO is SQLite's own
# alias for INSERT OR REPLACE and is the most natural way to re-derive a day's slot
# -- precisely what step 5 will be doing -- so a check blind to it was blind to the
# likeliest write there is. UPDATE and DELETE are writes too.
: > "$WORK/pair_writers.txt"
while IFS= read -r source_file; do
    normalised="$(tr '\n\t"\140+[]' '        ' < "$source_file" | tr -s ' ' |
        tr '[:upper:]' '[:lower:]' | sed 's/main\.//g')"
    case "$normalised" in
        *'insert into pair_volume_observation'* | \
        *'insert or ignore into pair_volume_observation'* | \
        *'insert or replace into pair_volume_observation'* | \
        *'insert or abort into pair_volume_observation'* | \
        *'insert or fail into pair_volume_observation'* | \
        *'insert or rollback into pair_volume_observation'* | \
        *'replace into pair_volume_observation'* | \
        *'delete from pair_volume_observation'* | \
        *'update pair_volume_observation set'*)
            printf -- '%s\n' "$source_file" >> "$WORK/pair_writers.txt"
            ;;
    esac
done <<EOF
$(find "$REPO_ROOT/internal" "$REPO_ROOT/cmd" -name '*.go' ! -name '*_test.go' | sort)
EOF
if [ -s "$WORK/pair_writers.txt" ]; then
    fail "PV-AC1 a code path writes the derived per-pair table: $(head -n 3 "$WORK/pair_writers.txt")"
else
    pass 'PV-AC1 no Go code path writes the derived per-pair table'
fi
check 'PV-AC2 the kind whose destination is derived has no active provider' '0' \
    "$(q "$MAIN_DB" "SELECT count(*) FROM provider WHERE kind = 'flow_volume' AND is_active = 1;")"
for needle in 'DERIVED, NOT COLLECTED' "step 5's to COMPUTE from"; do
    if grep -qF "$needle" "$SCHEMA_FILE"; then
        pass "PV-AC2 the schema records the ruling: $needle"
    else
        fail "PV-AC2 the schema does not record the ruling: $needle"
    fi
done

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
section 'PN-AC22 — schema discipline'
# ===========================================================================
# PN-AC22 restated. It counted migration files and schema_version rows, and both
# are gone by decision. What it was protecting was that the schema does not
# sprawl, and the decision makes that stronger rather than weaker: there is one
# file, it is idempotent, and it is the file the binary embeds. Migrations begin
# the day the product runs somewhere with data worth keeping, and that file
# becomes the baseline.
check 'PN-AC22 the schema is one file and it is the one the code embeds' '1' \
    "$(grep -c 'go:embed schema.sql purge.sql' "$REPO_ROOT/internal/store/store.go")"
if grep -qF 'THERE ARE NO MIGRATIONS' "$SCHEMA_FILE"; then
    pass 'PN-AC22 the schema states that there are no migrations, and why'
else
    fail 'PN-AC22 the schema does not state the no-migrations decision'
fi
if grep -qF 'Migrations begin the day' "$SCHEMA_FILE"; then
    pass 'PN-AC22 the schema states when migrations begin'
else
    fail 'PN-AC22 the schema does not say when migrations begin'
fi
check 'PN-AC22 a fresh apply creates no version-tracking table' '' \
    "$(q "$FRESH_DB" "SELECT group_concat(name, ',') FROM sqlite_master
        WHERE lower(name) GLOB '*schema_version*' OR lower(name) GLOB '*migration*';")"

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
              'admits several concurrently active providers exactly when' \
              'still exclusive' \
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
    if grep -qF "$needle" "$SCHEMA_FILE"; then
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
        n = split("Overview Matrix Interface Client Blocked Alerts Map", names, " ")
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
apply_schema "$SCALE_DB" "$WORK/apply_scale.err" || fail 'PN-AC30 the scale database failed to take the schema'
printf -- '  seeding %s flow rows, this takes a while...\n' "$SCALE_FLOW_ROWS"
seed_database "$SCALE_DB" "$INTERFACES" "$CLIENTS" "$RULES" "$SCALE_FLOW_ROWS" "$ALERTS" "$PAIR_ROWS" "$OWNERS"
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
section 'FC-AC1 to FC-AC5 — the API field coverage table stays honest'
# ===========================================================================
# The "API field coverage" table of docs/data-model.md sweeps every field the
# survey says an endpoint returns and gives each of them one of three verdicts:
# stored, not stored, or dropped. It exists because the same defect -- a field
# the API returns and the schema silently discards -- was found twice by
# accident.
#
# WHAT THIS HARNESS CAN AND CANNOT DO, stated plainly rather than implied. It
# CANNOT detect a newly dropped field: the survey is prose, and no machine can
# tell that a documented response gained a key nobody wrote down. What it CAN
# do, and does below, is stop the table drifting away from the schema -- a row
# claiming a field is stored must name a column that actually exists -- and
# stop a row being written with no verdict at all, which is how a field goes
# back to being dropped by accident.
parse_coverage() {
    awk '
        index($0, "<!-- api-field-coverage:begin -->") == 1 { inside = 1; next }
        index($0, "<!-- api-field-coverage:end -->") == 1 { inside = 0 }
        inside && substr($0, 1, 1) == "|" {
            n = split($0, cell, "|")
            if (n < 6) next
            if (cell[2] ~ /^ *:?-+:? *$/) next
            if (cell[2] ~ /^ *API field *$/) next
            verdict = "none"
            if (cell[5] ~ /^ \*\*not stored\*\*/) { verdict = "not_stored" }
            else if (cell[5] ~ /^ \*\*stored\*\*/) { verdict = "stored" }
            else if (cell[5] ~ /^ \*\*dropped\*\*/) { verdict = "dropped" }
            cols = ""
            rest = cell[5]
            while (match(rest, /`[^`]*`/)) {
                token = substr(rest, RSTART + 1, RLENGTH - 2)
                if (token ~ /^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$/) {
                    cols = cols (cols == "" ? "" : ",") token
                }
                rest = substr(rest, RSTART + RLENGTH)
            }
            field = cell[2]
            gsub(/^ +| +$/, "", field)
            survey = cell[4]
            gsub(/^ +| +$/, "", survey)
            print verdict "\t" cols "\t" field "\t" survey
        }
    ' "$1"
}
parse_coverage "$DOC" > "$WORK/coverage_rows.txt"
COVERAGE_ROWS="$(wc -l < "$WORK/coverage_rows.txt" | tr -d ' ')"
check_ge 'FC-AC1 the coverage table sweeps every surveyed response, not a sample' \
    100 "$COVERAGE_ROWS"
for source_name in 'Data source 1' 'Data source 2' 'Data source 3' \
                   'Data source 4' 'Data source 5' 'Runtime discovery'; do
    check_ge "FC-AC1 the coverage table reaches $source_name" 1 \
        "$(awk -F'\t' -v s="$source_name" 'index($4, s) > 0' \
           "$WORK/coverage_rows.txt" | wc -l | tr -d ' ')"
done

# FC-AC2: every row carries one of the three verdicts. A row with none is a
# field nobody decided about, which is exactly the state the table exists to
# make impossible.
check 'FC-AC2 every coverage row carries one of the three verdicts' '' \
    "$(awk -F'\t' '$1 == "none" { print $3 }' "$WORK/coverage_rows.txt" | tr '\n' ' ')"
check_ge 'FC-AC2 the table records fields that are deliberately not stored' 20 \
    "$(awk -F'\t' '$1 == "not_stored"' "$WORK/coverage_rows.txt" | wc -l | tr -d ' ')"
check_ge 'FC-AC2 the table records the fields that are stored' 50 \
    "$(awk -F'\t' '$1 == "stored"' "$WORK/coverage_rows.txt" | wc -l | tr -d ' ')"

# FC-AC3: a stored row must name a column that exists. This is the assertion
# that keeps the table and the schema from drifting apart.
column_missing() {
    local token="$1" tname="${1%%.*}" cname="${1#*.}"
    # table_xinfo rather than table_info: the latter omits generated columns,
    # and interface.is_tunnel is one. A column review that could not see a
    # generated column would report it missing and be wrong.
    [ "$(q "$MAIN_DB" "SELECT count(*) FROM pragma_table_xinfo('$tname')
         WHERE name = '$cname';")" = '0' ]
}
MISSING_COLUMNS=''
CLAIMED_COLUMNS=0
while IFS="$(printf '\t')" read -r verdict cols field survey; do
    [ "$verdict" = 'stored' ] || continue
    [ -n "$cols" ] || continue
    for token in $(printf -- '%s' "$cols" | tr ',' ' '); do
        CLAIMED_COLUMNS=$((CLAIMED_COLUMNS + 1))
        if column_missing "$token"; then
            MISSING_COLUMNS="$MISSING_COLUMNS $field->$token"
        fi
    done
done < "$WORK/coverage_rows.txt"
check_ge 'FC-AC3 the stored rows name columns to verify' 50 "$CLAIMED_COLUMNS"
check 'FC-AC3 every column a stored row names exists in the live schema' '' \
    "$MISSING_COLUMNS"

# FC-AC4: the assertion above has teeth. The same resolver is run against a
# column that deliberately does not exist, and must report it; a checker that
# passes on anything would pass on a table that had drifted.
if column_missing 'flow.this_column_does_not_exist'; then
    pass 'FC-AC4 the column resolver reports a column that does not exist'
else
    fail 'FC-AC4 the column resolver accepted a column that does not exist'
fi
if column_missing 'flow.log_reason'; then
    fail 'FC-AC4 the column resolver rejected a column that does exist'
else
    pass 'FC-AC4 the column resolver accepts a column that does exist'
fi

# FC-AC5: the fields this sweep closed are stored, and the columns are there.
# Each separates "nothing happened" from "we could not see", which is the
# distinction the whole project is built on.
for closed in 'dns_resolution.dnssec_status' 'flow.log_reason' \
              'rule.logs_matches' 'interface.status' 'interface.enabled'; do
    if column_missing "$closed"; then
        fail "FC-AC5 the closed field $closed has no column"
    else
        pass "FC-AC5 the closed field is stored in $closed"
    fi
done
check_ge 'FC-AC5 the resolver validation verdict is queryable and is reported for most lookups' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM dns_resolution WHERE dnssec_status IS NOT NULL;')"
check_ge 'FC-AC5 a lookup whose verdict was not reported stays distinguishable' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM dns_resolution WHERE dnssec_status IS NULL;')"
check_ge 'FC-AC5 two distinct log reasons are representable, so a denial and a drop differ' 2 \
    "$(q "$MAIN_DB" 'SELECT count(DISTINCT log_reason) FROM flow;')"
check_ge 'FC-AC5 the blocked projection carries the reason the record was logged' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM blocked_event WHERE log_reason IS NOT NULL;')"
check_ge 'FC-AC5 a rule that does not log is representable' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM rule WHERE logs_matches = 0;')"
check_ge 'FC-AC5 a rule whose logging flag was not reported stays distinguishable' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM rule WHERE logs_matches IS NULL;')"
expect_sql_failure 'FC-AC5 a logging flag outside the two states is rejected' "$MAIN_DB" \
    'UPDATE rule SET logs_matches = 2 WHERE id = 1;'
check_ge 'FC-AC5 an interface whose link state differs from the common one is representable' 1 \
    "$(q "$MAIN_DB" "SELECT count(DISTINCT status) FROM interface WHERE status IS NOT NULL;")"
check_ge 'FC-AC5 an interface discovered without a state reads as not reported' 1 \
    "$(q "$MAIN_DB" 'SELECT count(*) FROM interface WHERE status IS NULL AND enabled IS NULL;')"
# The two interface state columns and the two verbatim text columns carry no
# CHECK, deliberately: the survey establishes those fields and not their value
# sets, and a vocabulary written here would be invented rather than discovered.
check 'FC-AC5 no invented vocabulary constrains a field whose value set the survey does not establish' \
    '' "$(q "$MAIN_DB" "SELECT group_concat(name, ',') FROM pragma_table_info('interface')
         WHERE name IN ('status', 'enabled') AND type <> 'TEXT';")"

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
