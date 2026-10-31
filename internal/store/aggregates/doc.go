// Package aggregates is the part of the store holding the record of the service's own downtime
// (downtime.go). The aggregate families and their refresh it was named for are the rollups plugin's
// tables and derivation since ADR 0021 (plugins/rollups). internal/store composes it into Store.
package aggregates
