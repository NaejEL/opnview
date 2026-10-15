// Package discovery is runtime discovery: the firewall's interfaces, their addresses, its rules,
// its version and its clock, read through the broker and written as the snapshot every
// coordinator joins against. Nothing is classified by its name. internal/collect composes it into
// the Collector.
package discovery
