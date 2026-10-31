// Package samples is the part of the store holding the measurement interval the sampled readings are
// taken at. The current rates once read here -- per client, interface, direction and peer -- are the
// figures library's rates area since ADR 0021 (plugins/figures), over each widget's own statements.
// internal/store composes it into Store.
package samples
