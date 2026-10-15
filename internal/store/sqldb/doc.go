// Package sqldb is what every part of the store runs on: the Handle -- the read-write pool, the
// read connections, the instance lock and the plugin tables ensured (handle.go, readers.go) --
// the driver that rewrites ? and :name parameters for pgx (driver.go), the statement registry
// every owner of a statements file registers into (statements.go), the query check
// (querycheck.go), LISTEN and NOTIFY (notify.go), and the helpers statements share (exec.go).
package sqldb
