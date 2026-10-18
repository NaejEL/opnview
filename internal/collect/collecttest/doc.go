// Package collecttest is what the collector's tests share, wherever they live: the collector's own
// in internal/collect and each part's beside the part. It holds the stopped clock a test drives,
// the probe harness -- a fake firewall answering 404 everywhere until a test arranges an endpoint,
// a database of the test's own and a collector over both -- and the arrangements of each kind's
// collection. Every address is a documentation address (RFC 5737, RFC 3849) and every identifier a
// synthesised example.
package collecttest
