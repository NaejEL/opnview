// Package live mounts the live figures that are not a widget's (C3 of the backend-completion
// cycle, docs/widget-api.md "Live figures"). It draws no page and carries no catalogue: it answers
// JSON only, for the step-7 application.
//
//   - GET /api/live/strip draws the rolling strip from the stored samples and records, and renews
//     the live-interest lease: the request is for a live figure.
//   - GET /api/live/settings answers the live-rate settings: the lease, the fine-sample retention
//     and the live cadence of every live pass a loaded plugin declares.
//   - POST /api/live/settings writes the two core live settings, validated as the configuration's
//     load validates them: every key known and every value in range, or nothing is written. A live
//     cadence is a plugin's declared setting, written through POST /api/plugins/{name}/settings.
package live
