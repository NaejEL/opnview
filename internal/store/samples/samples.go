package samples

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/accounts"
)

// The read functions over measurement_sample: current rates, bytes seen in
// samples, and per-interface throughput.
//
// WHAT /api/diagnostics/traffic/top MEASURES, read at opnsense/core 26.7.3
// (scripts/interfaces/traffic_top.py): it runs iftop on each interface with
// `-s 2`, one text output after two seconds, and reports per local address and
// per peer a rate_bits figure -- iftop's average over the last two seconds -- and
// a cumulative_bytes figure, the bytes of that same two-second capture. So:
//
//   - a CURRENT RATE is a rate read in a recent sample. A sample is current for
//     twice the configured measurement interval; beyond that there is no current
//     sample, which is a state and never a rate of 0;
//   - SAMPLED BYTES are the bytes seen in samples, two seconds per sample, carried
//     with the seconds sampled as their coverage and NEVER extrapolated to a
//     period. A period with no sample is "not sampled", never 0.
//
// It is a second measure, distinct from logged bytes, and neither is the volume of
// a conversation.

// MeasurementIntervalKey is the setting row holding the sampled-measurement
// interval, in seconds, and DefaultMeasurementIntervalSeconds the interval in force
// when the row is absent. internal/config reads the same row and derives its
// default from this constant, so the interval the sampler runs at and the bound a
// current rate is read within cannot drift apart.
const (
	MeasurementIntervalKey            = "poll_interval_measurement_seconds"
	DefaultMeasurementIntervalSeconds = 300
)

// MeasurementIntervalSeconds reads the configured baseline sampling interval: the
// cadence every baseline sample was taken at.
func (s *SampleStore) MeasurementIntervalSeconds(ctx context.Context) (int64, error) {
	interval := int64(DefaultMeasurementIntervalSeconds)
	value, present, err := accounts.On(s.Handle).Setting(ctx, MeasurementIntervalKey)
	if err != nil {
		return 0, err
	}
	if present {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("store: the setting %s is %q, not a positive whole number of seconds",
				MeasurementIntervalKey, value)
		}
		interval = parsed
	}
	return interval, nil
}

// CurrentSampleBound is how far back a sample still counts as current: twice the
// configured measurement interval, read from the setting row. One missed sampling
// pass therefore does not turn a rate into "no current sample"; two do.
func (s *SampleStore) CurrentSampleBound(ctx context.Context) (int64, error) {
	interval, err := s.MeasurementIntervalSeconds(ctx)
	if err != nil {
		return 0, err
	}
	return 2 * interval, nil
}
