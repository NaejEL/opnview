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
// THE SEAM. A preset is a file in this package holding one Preset value, and one row in
// registry.go naming it. Adding a preset's endpoint is adding those two things and
// nothing else: the HTTP routes are built from the registry, the parameter handling
// from the Preset's declaration, and the envelope from its bindings.
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
