package measurements

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
)

// MeasurementCoordinator is the part of the collector this package holds: the coordinator of the measurement_sample kind, its baseline pass and its live pass.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type MeasurementCoordinator struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *MeasurementCoordinator {
	return &MeasurementCoordinator{Collector: state}
}

func init() {
	core.RegisterPass(Kind, func(c *core.Collector) func(context.Context) error { return On(c).CollectMeasurement })
}
