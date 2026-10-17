// Package frontend is the surface that serves the front (specs/SPEC-step-7-frontend.md, A3, A4,
// A5 and A8): the shell document at every interface route, the assets of the front's index under
// /front/, the front's registry (GET /api/front), the merged string catalogue of each installed
// language (GET /api/strings/{language}), the appearance (GET and POST /api/appearance) and the
// widget registry (GET /api/widgets).
//
// THE SHELL IS PUBLIC AND CARRIES NO DATA. It is one static document served at every path no other
// route answers, so a bookmark of a view keeps working and the front's router decides what the path
// shows; a reader with no session is shown the session views only, and every read of data goes
// through an authenticated JSON route. An unknown path under /api/ is answered in JSON, never with
// the shell.
//
// THE INDEX IS BUILT ONCE, when the server mounts this surface (internal/front.Build). A part of the
// data directory that cannot be served is refused with its code and path, written to the console
// and listed by GET /api/widgets; the service starts without it. A built-in that breaks a rule is a
// defect of the build, and the surface is refused with that reason.
package frontend
