package measurement

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
		readings []vocab.MeasurementSample, missing []string, result provider.Result, err error)
}

// LiveSource is an implementation that also declares a LIVE PASS (C3 of the backend-completion
// cycle, docs/plugins.md "Live pass"): a pass reading only its live endpoints, run at its live
// cadence while somebody looks at a live figure (internal/live), whose readings are stored as
// fine samples. It returns what Sample returns, its readings stamped with the instants of the
// answers they were read from.
type LiveSource interface {
	Source
	// LiveCadence names the setting holding the live cadence, in seconds, its default, its lower
	// bound and the highest freshness floor of the endpoints the live pass reads.
	LiveCadence() (key string, fallback, minimum, floor int64)
	// Live runs one live pass whose firewall requests accept an answer at most cadence old.
	Live(ctx context.Context, host provider.Session, snapshot provider.Discovery, providerID, now int64,
		cadence time.Duration) (readings []vocab.MeasurementSample, missing []string, result provider.Result, err error)
}
