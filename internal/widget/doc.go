// Package widget is the widget HTTP API of step 5B (specs/SPEC-step-5b-widget-api.md):
// one read-only endpoint per served preset of docs/widget-catalogue.md, answering
// JSON and nothing else.
//
// WHAT A REQUEST CARRIES. The widget's `parameters` object, exactly as
// docs/dashboard-format.md defines it -- scalars, arrays of scalars, reference objects
// and the ordered array of objects of `series` -- as JSON in one query parameter,
// `parameters`. There is no second encoding of the parameters.
//
// WHAT A RESPONSE CARRIES. One envelope for every type (envelope.go): the effective
// parameters after defaults, the requested and the covered window, the gaps, the
// availability of every provider the preset binds with the condition each maps to, the
// freshness of the aggregates read, what could not be resolved or parsed, what the
// aggregate mode withheld, and the data. docs/widget-api.md documents it once.
//
// THE SEAM. A widget is a DIRECTORY holding its manifest.json, its query files and the logic a
// shape cannot express. A widget a plugin carries -- plugins/<name>/widgets/<type>/ in the
// repository, <data-dir>/plugins/<name>/widgets/<type>/ for a third party -- writes that logic in
// JavaScript, run by the script host (script.go, host*.go; docs/plugins.md, "Widgets"). Every
// built-in widget is such a plugin; an operator widget under <data-dir>/widgets is a manifest and
// a query. The HTTP routes are built from the loaded types, the parameter handling
// from the manifest, the envelope from its bindings, and what is common to a shape is written
// once in this package (engine.go, shape files). docs/widget-authoring.md is the reference.
//
// WHAT IT NEVER DOES. It never calls the firewall, MaxMind or the Public Suffix List --
// the three outbound calls stay three, and none is reachable from here -- and it never
// writes: every statement runs on the store's read-only connections. No response
// carries a credential, a ciphertext, a session token or a CSRF token, and no response
// sets a CORS header. No user-facing sentence is a literal here: the API emits
// condition codes and field names, which are English identifiers, and the interface
// translates them through its string catalogue.
//
// THE OBSERVATION-POINT LIMIT. opnview sees only what crosses the firewall, so every
// byte and connection figure is a lower bound: each one says so in the payload
// (`lower_bound: true`, beside the measure it is), and docs/widget-api.md states the
// limit. Nothing here prints it as a sentence (CLAUDE.md).
package widget
