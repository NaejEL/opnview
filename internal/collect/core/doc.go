// Package core is the collector's shared state and what every part of the collector uses: the
// Collector every part embeds (collector.go), its clock, configuration and discovery snapshot
// (core.go), the state of the push paths, the live sampling, the NetFlow join and the services
// (state.go), the reads through the broker (brokered.go), the resolution of a kind's active
// sources (sources.go), the scheduler's tasks (tasks.go), and what the hostname sources last read
// (hostnamesources.go). internal/collect composes the parts on it.
package core
