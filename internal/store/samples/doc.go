// Package samples is the part of the store holding the measurement samples and the current rates
// read from them -- per client, interface, direction, owner and peer -- a second measure,
// sampled, never summed with the logged one. internal/store composes it into Store.
package samples
