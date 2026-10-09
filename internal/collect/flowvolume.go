package collect

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// The measurement_sample kind, whose material on OPNsense 26.7 has to be sampled.
//
// THE KIND IT ANSWERS FOR CHANGED, AND THE COLLECTOR DID NOT. This pass was registered under
// flow_volume because measurement_sample was a table with no kind: no provider_key, no
// availability row, no place in provider.kind. It is a kind now — the one eight of the ten
// surveyed sources that fit an existing shape fit — so the pass answers for it, and flow_volume
// is left with no implementation because its own destination is DERIVED from flow by step 5.
//
// It admits several concurrently active providers, so the pass walks them: a firewall reporting
// UPS volts beside its own gauges is two sources, and every reading carries the provider that
// took it.
//
// What is true of any measurement source, and is therefore here: writing the readings, and
// recording what did not answer. How the readings are obtained — which endpoint, which argument,
// which field names — is in flowvolume_insight.go.
//
// THE READINGS ARE A SAMPLE AND NOT A CACHE. No endpoint exposes a per-pair aggregate over a
// past window: the per-pair data is a live snapshot, measured on a live firewall. So opnview's
// own history is built from these samples, at the interval internal/config carries, and that
// interval is the RESOLUTION of that history rather than a refresh rate.
//
// A READING THAT DID NOT ANSWER IS NEVER WRITTEN AS A ZERO. The difference between "the
// processor is idle" and "this endpoint did not tell us" is the difference the whole product is
// built on, and the second is recorded in the availability detail by name.

// CollectMeasurement runs one pass of every active implementation of the
// measurement_sample kind.
//
// One failing source does not stop the others: they are separate providers reading separate
// endpoints, and letting the first failure end the pass would turn one unreadable source into
// several.
func (c *Collector) CollectMeasurement(ctx context.Context) error {
	sources, err := c.activeSources(ctx, KindMeasurementSample)
	if err != nil {
		return err
	}
	var failures []error
	for _, active := range sources {
		if err := c.collectMeasurementFrom(ctx, active); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the measurement pass was incomplete: %w", joinErrors(failures))
	}
	return nil
}

// collectMeasurementFrom runs one pass of one implementation.
func (c *Collector) collectMeasurementFrom(ctx context.Context, active activeSource) error {
	providerKey, providerID := active.providerKey, active.providerID
	source, registered := measurementSources[providerKey]
	if !registered {
		return fmt.Errorf("collect: the active measurement_sample provider %q has no implementation",
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
	absent := ""
	if len(missing) > 0 {
		absent = "these firewall telemetry readings did not answer and were not written as " +
			"zeroes: " + strings.Join(missing, ", ")
		if detail == "" {
			detail = absent
		} else {
			detail += "; " + absent
		}
	}
	// The probe round rewrites this availability row from its own probe, which says
	// nothing about the readings; what the last pass could not read is kept so the
	// probe round restates it rather than erasing it.
	c.setSampleAbsence(providerID, absent)
	state := result.state
	if state == "" {
		state = store.StateReachable
	}
	return c.writeAvailability(ctx, providerID, state, result.probe, detail)
}
