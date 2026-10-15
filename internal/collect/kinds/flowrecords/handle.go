package flowrecords

import "github.com/NaejEL/opnview/internal/collect/core"

// FlowRecordCoordinator is the part of the collector this package holds: the coordinator of the flow_record kind: its capture reads, its pushed records and its probe.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type FlowRecordCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *FlowRecordCoordinator {
	return &FlowRecordCoordinator{Collector: state}
}
