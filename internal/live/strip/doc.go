// Package strip draws the rolling strip (C3 of the backend-completion cycle, coverage row 8 of
// docs/widget-api.md): a trailing series at the live cadence of the throughput by traffic
// direction, read from the stored samples, and of the decisions per engine -- allowed, refused by a
// firewall rule, refused with no rule, refused for a reason not recorded, refused by a resolver's
// list -- counted from the filter log's and the resolvers' own timestamps. Each decision series
// carries complete_until, and a bucket past it is a gap, never a zero. It reads the store and
// renews the live-interest lease; it never calls the firewall.
package strip
