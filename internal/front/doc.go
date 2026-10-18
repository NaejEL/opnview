// Package front is the front's index: what the browser is served, built once at start from the
// built-in front (the repository's front/ tree, embedded by the root package) and from the parts a
// third party drops into the data directory (specs/SPEC-step-7-frontend.md, A1 to A4;
// docs/front-plugins.md).
//
// A THEME IS A FILE, A LANGUAGE IS A FILE, A WIDGET'S RENDERER IS A FILE OF ITS DIRECTORY.
// <data-dir>/themes/<id>.json and <data-dir>/languages/<code>.json are discovered here; a widget's
// renderer and a front-only widget are discovered by the widget loader (internal/widget), which
// checks the file and keeps its bytes, and indexed here; an operator or front-only widget's
// strings are read from its directory's locale/. Nothing is listed anywhere: adding a part is
// adding its file and restarting, with no file of the repository changed and no Go rebuild.
//
// A PART THAT CANNOT BE SERVED IS REFUSED, NEVER FATAL. Each refusal carries a closed code and
// the path of the file, is listed by GET /api/widgets, and the service starts without the part. A
// built-in that breaks a rule is a defect of the build: Build returns an error naming it and the
// test suite fails. The built-in wins every collision.
//
// ONLY WHAT THE INDEX DECLARES IS SERVED. The assets are an in-memory map from a published path to
// bytes read at start: the core's scripts and stylesheet, the generated palette stylesheet, each
// theme file, each renderer entry. A widget's server-side script, a statement file, a manifest or
// any other file of a plugin or of the data directory is never in it, and a path that is not a key
// of the map -- `..`, an encoded traversal, a name nobody declared -- is a 404 by construction.
//
// NOTHING IS LOADED FROM ANOTHER ORIGIN. Every asset is served by this binary under the policy of
// headers.go; there is no CDN, no web font and no remote icon.
package front
