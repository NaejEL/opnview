package collect

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// The flow_volume kind, whose material on OPNsense 26.7 has to be sampled.
//
// What is true of any volume source, and is therefore here: writing the readings, and recording
// what did not answer. How the readings are obtained — which endpoint, which argument, which
// field names — is in flowvolume_insight.go.
//
// THE READINGS ARE A SAMPLE AND NOT A CACHE. No endpoint exposes a per-pair aggregate over a
// past window: the per-pair data is a live snapshot, measured on a live firewall. So opnview's
// own history is built from these samples, at the interval internal/config carries, and that
// interval is the RESOLUTION of that history rather than a refresh rate.
//
// A READING THAT DID NOT ANSWER IS NEVER WRITTEN AS A ZERO. The difference between "the
// processor is idle" and "this endpoint did not tell us" is the difference the whole product is
// built on, and the second is recorded in the availability detail by name.

// CollectMeasurement runs one pass of the active implementation of the flow_volume kind.
func (c *Collector) CollectMeasurement(ctx context.Context) error {
	providerKey, providerID, active, err := c.activeSourceKey(ctx, KindFlowVolume)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	source, registered := measurementSources[providerKey]
	if !registered {
		return fmt.Errorf("collect: the active flow_volume provider %q has no implementation",
			providerKey)
	}

	now := c.now()
	snapshot := c.Discovery()

	readings, missing, result, err := source.sample(ctx, c, snapshot, providerID, now)
	if err != nil {
		return err
	}
	for _, reading := range readings {
		if err := c.store.InsertMeasurementSample(ctx, reading); err != nil {
			return err
		}
	}

	// One availability row, carrying both halves. The STATE describes the volume source,
	// because that is what this provider is; the DETAIL names every reading that did not
	// answer, so an absent sensor is a recorded fact rather than a missing row nobody can tell
	// from a quiet one.
	detail := result.detail
	if len(missing) > 0 {
		absent := "these firewall telemetry readings did not answer and were not written as " +
			"zeroes: " + strings.Join(missing, ", ")
		if detail == "" {
			detail = absent
		} else {
			detail += "; " + absent
		}
	}
	state := result.state
	if state == "" {
		state = store.StateReachable
	}
	return c.writeAvailability(ctx, providerID, state, result.probe, detail)
}
