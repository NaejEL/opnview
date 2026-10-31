// Package values holds the script host's areas that work on values a script already holds:
// envelopes and conditions, parameters and references, the request, JSON, services and settings.
// Each area is a file that registers itself from its own init() (script.RegisterHostArea);
// internal/widget links the package. The figures, their sums and their measure are the figures
// library's since ADR 0021 (plugins/figures/figures.js).
package values
