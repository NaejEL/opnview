// Package purge is the part of the store that applies the retention purge: purge.sql, run in one
// transaction with the plugin tables' and the NetFlow tables' own purges, and the reads of what
// it has purged. The ordering against the passes is the collector's (internal/collect, Purge).
// internal/store composes it into Store.
package purge
