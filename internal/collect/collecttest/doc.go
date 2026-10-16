// Package collecttest is what the collector's tests share, wherever they live: the collector's own
// in internal/collect and each part's beside the part. It holds the stopped clock a test drives,
// the probe harness -- a fake firewall answering 404 everywhere until a test arranges an endpoint,
// a database of the test's own and a collector over both -- the arrangements of each kind's
// collection, and the NetFlow join's test world: a fake firewall whose discovery maps two
// interfaces to kernel indexes, a filter log answered row by row, and NetFlow v9 datagrams built
// field by field from RFC 3954 in the template ng_netflow emits (survey, Push sources, S10),
// delivered through the core's real decoder and router to the flow_record provider's push entry.
// Every address is a documentation address (RFC 5737, RFC 3849) and every identifier a synthesised
// example.
package collecttest
