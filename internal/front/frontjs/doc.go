// Package frontjs runs the front's own files in the interpreter the server already embeds
// (github.com/dop251/goja), over a minimal document (dom.js), so the front is tested without a
// browser (specs/SPEC-step-7-frontend.md, A9).
//
// IT RUNS THE FILES THE SHELL LOADS, IN THE SHELL'S ORDER, read from the built-in front -- the
// same bytes a browser is served -- so a test exercises the product and not a copy. The server is
// a fake: every request the front makes is answered by the test's handler and recorded, so a test
// asserts both what was drawn and what was asked. Time is a fake clock the test moves; nothing
// sleeps.
//
// The interpreter runs a promise's reactions when the call that queued them returns, so a view's
// load, its answers and its render have all happened by the time a call from Go comes back.
package frontjs
