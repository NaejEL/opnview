// Package web is opnview's HTTP core: the server, the route table, the session and CSRF
// middleware, the string catalogue and the seam the interface's surfaces plug into.
//
// WHAT THE SEAM IS. The server owns routing, sessions and the catalogue; a surface owns its
// handlers and its catalogue keys, lives in a directory of its own under internal/web/surfaces,
// and registers itself (surface.go). The routes are a TABLE with a declared access level per row
// rather than a sequence of mux calls: a route added later carries a decision about who may
// reach it, and a test enumerates the table and fails on a route that carries none.
//
// THE CORE SERVES NO ROUTE ITSELF, AND NO SURFACE DRAWS A PAGE. Every surface answers JSON; the
// interface is the front (specs/SPEC-step-7-frontend.md), whose shell, assets and string
// catalogue the frontend surface serves.
//
// NO CONTROL CAN CHANGE A FIREWALL SETTING, and none suggests that it can. The only call the
// server makes to the firewall is the credential verification of the settings surface, which is
// a read of one registry endpoint.
//
// NOTHING IS LOADED FROM OFF-HOST. Every byte the interface needs is embedded in the binary and
// served by it. No font, no script, no icon set, no favicon, no analytics, no version check.
package web
