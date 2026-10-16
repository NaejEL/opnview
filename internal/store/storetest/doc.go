// Package storetest is what the store's tests share, wherever they live: the store's own in
// internal/store and each part's beside the part. It gives a test a database of its own -- a clone
// of the schema's template on the test instance (store.NewTestDatabase), opened, and dropped when the
// test ends (storetest.go) -- and the test network every derivation test runs against: interfaces,
// clients of both address families and the records they log, the two legs of a connection, and the
// plan of a statement as PostgreSQL chooses it (network.go).
package storetest
