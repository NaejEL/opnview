// Package aggregates is the part of the store holding the aggregate families: their refresh, slot
// by slot and period by period, with the per-pair volume derivation (aggregate.go and its
// statements), and the record of the service's own downtime (downtime.go). internal/store
// composes it into Store.
package aggregates
