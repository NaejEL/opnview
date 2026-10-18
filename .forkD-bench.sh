#!/bin/sh
# Fork D's throwaway bench: builds widgetbudget from HEAD overlaid with fork D's files, so another
# fork's half-done edit elsewhere does not stop the build. Removed before hand-back.
set -e
rm -rf /tmp/ws && mkdir -p /tmp/ws
tar -xf /workspace/.forkD-head.tar -C /tmp/ws
for path in ci/widgetbudget internal/widget/script plugins/connection_tree $OVERLAY; do
  rm -rf "/tmp/ws/$path"; mkdir -p "$(dirname /tmp/ws/$path)"; cp -r "/workspace/$path" "/tmp/ws/$path"
done
[ -n "$SCHEMA" ] && cp /workspace/.forkD-schema.sql /tmp/ws/internal/store/schema.sql
[ -n "$VARIANT" ] && cp "/workspace/$VARIANT" /tmp/ws/plugins/connection_tree/widgets/connection_tree/connection_tree.js
cd /tmp/ws
[ -n "$TESTS" ] && { go test -count=1 $TESTS; exit $?; }
go build -o /tmp/wb ./ci/widgetbudget
DB=${DB:-schema_checks_main_seed}
/tmp/wb -database "host=db-schema-checks port=5432 user=postgres dbname=$DB sslmode=disable" -now 1750000000 -label "$DB" -budget-24h ${B24:-250ms} -budget-30d ${B30:-500ms} -cases "$TYPES" $EXTRA || true
