// Package netflow mounts the NetFlow join's settings (PS17 and PS19 rule 3 of the NetFlow join
// amendment to specs/SPEC-backend-completion-plugins.md; docs/widget-api.md, "Settings API"). It draws no
// page and carries no catalogue: it answers JSON only.
//
//   - GET /api/netflow/settings answers the two settings as they stand: netflow_join_tolerance_seconds,
//     how far after a leg was logged a flow record may start and still join it, and
//     retention_flow_record_seconds, how long a flow record is kept, 0 following retention_seconds.
//   - POST /api/netflow/settings writes them, validated as the configuration's load validates them:
//     every key known and every value in range, or nothing is written.
package netflow
