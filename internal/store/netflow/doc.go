// Package netflow is the part of the store holding NetFlow: the records as the exporter sent them,
// the capture configuration read beside them, their join to the logged connections, and the
// measured volume of each connection -- a second measure, never summed with the logged one.
// internal/store composes it into Store.
package netflow
