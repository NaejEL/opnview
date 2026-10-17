// Package references is the surface of the reference candidates and their resolution
// (specs/SPEC-step-7-frontend.md, A8): GET /api/references/{kind} lists what a reference of one
// kind can name on this installation, every accepted `by` key with each candidate, and
// GET /api/references/resolve answers each reference object of a list resolved, or the reason it
// is not, by the widget API's own resolution (internal/widget/params, ResolveReference). Both are
// authenticated reads that answer codes and data, never a sentence.
package references
