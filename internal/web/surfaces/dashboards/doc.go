// Package dashboards is the surface of the stored dashboards (specs/SPEC-step-7-frontend.md, A7):
// listing them, reading one, creating, saving with a revision check, deleting with an answer that
// undoes the deletion, restoring and ordering. Every route is authenticated and JSON; every write
// is a JSON POST under the CSRF rule of the middleware.
//
// A DOCUMENT IS STORED AND ANSWERED AS THE TEXT IT WAS SENT, byte for byte, member order and unknown
// keys included: it travels as the raw bytes of the body's `document` member and is spliced into an
// answer rather than re-encoded, because an export without repair must give back the imported file's
// members unchanged (docs/dashboard-format.md). Its structure is validated fail-closed by
// internal/dashboard before anything is written; its references are never checked on a save.
package dashboards
