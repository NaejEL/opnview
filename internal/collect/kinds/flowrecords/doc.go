// Package flowrecords is the coordinator of the flow_record kind: the exporter's capture
// configuration read at a cadence, the records it pushes, their stream conditions, and its probe.
// The join itself is a derivation (internal/collect/derive). internal/collect composes it into the
// Collector.
package flowrecords
