// Package dashboard is the structural validator of a dashboard document: docs/dashboard-format.md,
// format version 1, as amended by decision D7 of specs/SPEC-step-7-frontend.md (the optional
// canvas `period` and `scope`).
//
// STRUCTURAL VALIDITY IS FAIL-CLOSED, AND IT IS THE ONLY VALIDITY CHECKED HERE. A document that
// cannot be parsed, that lacks its format version or its canvases, whose canvas or widget lacks an
// identity, or whose placement is not four whole numbers is refused whole, naming the canvas index,
// the widget index, the field and the path of the first defect in document order. Referential
// validity -- an unknown widget type, a reference matching nothing, an unknown parameter key, a
// value outside a vocabulary -- is never checked: the format makes it fail-open, reported by the
// widget API's own envelopes and never a reason to refuse a save.
//
// THIS VALIDATOR IS THE AUTHORITATIVE ONE (decision D13). The front's document module,
// front/core/document.js, mirrors it exactly for the editor; the shared conformance fixtures of
// testdata/dashboards/conformance hold the two together, and a test fails when they disagree on any
// fixture.
package dashboard
