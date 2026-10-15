// Package web is opnview's HTTP core: the server, the route table, the session and CSRF
// middleware, the layout, the themes, the embedded stylesheet, the string catalogue and the
// seam the interface's surfaces plug into.
//
// WHAT THE SEAM IS. The server owns routing, sessions and the static-asset story; a surface
// owns its handlers, its templates and its catalogue keys, lives in a directory of its own
// under internal/web/surfaces, and registers itself (surface.go). The routes are a TABLE with
// a declared access level per row rather than a sequence of mux calls: a route added later
// carries a decision about who may reach it, and a test enumerates the table and fails on a
// route that carries none.
//
// WHAT THE CORE SERVES ITSELF, AND NOTHING MORE. The root, which sends a reader to whichever
// surface applies, and the one stylesheet. Every page, the sign-in and setup surfaces
// included, is a surface. There is no canvas, no dashboard format and no language picker
// here: those are step 7.
//
// NO CONTROL ON ANY PAGE CAN CHANGE A FIREWALL SETTING, and none suggests that it can. The
// only call the interface makes to the firewall is the credential verification of the
// settings surface, which is a read of one registry endpoint.
//
// NOTHING IS LOADED FROM OFF-HOST. Every byte a page needs is embedded in this package or in
// a surface's package and served by this binary. No font, no script, no icon set, no
// favicon, no analytics, no version check.
package web
