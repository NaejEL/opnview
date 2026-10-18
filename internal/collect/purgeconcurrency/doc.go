// Package purgeconcurrency holds the retention purge's tests against the collector: the purge of each
// table and the concurrency of the passes and the purge. They are a package of their own so that the
// longest of them, the four loops running together, starts as soon as the suite does and runs beside the
// collector's other tests, rather than at whatever moment a slot frees in internal/collect (carried item 1
// of Lot 4 of the plugin-API amendment).
package purgeconcurrency
