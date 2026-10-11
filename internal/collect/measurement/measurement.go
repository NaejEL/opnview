// Package measurement is the measurement_sample kind: a numeric reading of a subject at an
// instant.
//
// WHAT THE KIND PRODUCES. store.MeasurementSample rows plus the names of the readings that did NOT
// answer. On OPNsense 26.7 no endpoint answers for a past window: the per-pair data is a live
// snapshot, so the implementation samples and opnview keeps the history. The same pass reads the
// firewall's own gauges, because a gauge and a sampled pair volume are the same five facts -- a
// subject, a measure, a unit, a value and an instant -- and one table carries both. That is why
// the kind exists and why eight of the ten surveyed sources that fit an existing shape fit this
// one.
//
// A READING THAT DID NOT ANSWER IS NEVER WRITTEN AS A ZERO. The difference between "the processor
// is idle" and "this endpoint did not tell us" is the difference the whole product is built on,
// and the second is returned in the missing list so the coordinator records it by name.
//
// The kind admits several concurrently active providers, so the coordinator walks them: a
// firewall reporting UPS volts beside its own gauges is two sources, and every reading carries
// the provider that took it. The one thing this kind is handed that no other is the discovery
// snapshot, per call: the sampler needs the interface identifiers the per-pair endpoint takes.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/measurement/<product>/ holding a
// provider.json whose kind is "measurement_sample", a Go package whose init() calls Register with
// the embedded manifest and a Source, its fixtures and its tests. See docs/extending.md.
package measurement

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store"
)

// Kind is the registry kind this package answers for.
const Kind = "measurement_sample"

// Source is one implementation of the measurement_sample kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Sample returns the readings the firewall reports now, and the readings that did NOT
	// answer -- named, so an absence is recorded rather than written as a zero.
	Sample(ctx context.Context, host provider.Session, snapshot provider.Discovery, providerID, now int64) (
		readings []store.MeasurementSample, missing []string, result provider.Result, err error)
}
