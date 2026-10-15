// Package plugins mounts the plugin listing and the plugin settings write endpoint (A5 and A6 of
// the backend-completion cycle, docs/widget-api.md, "Plugins"). It draws no page and carries no
// catalogue: it answers JSON only, for the step-7 application.
//
//   - GET /api/plugins lists every plugin discovered, built-ins included: what each provides,
//     reads, contacts, owns and lets the operator set, or why it was refused. A secret setting
//     reports only whether it is set.
//   - POST /api/plugins/{name}/settings writes settings a plugin declares, validated against the
//     declaration: every key is declared and every value within its bounds, or nothing is written.
//     It is a JSON write, so the middleware asks it for the session's form token in a header.
package plugins
