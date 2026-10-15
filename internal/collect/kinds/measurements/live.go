package measurements

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/measurement"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Live sampling (C3 of the backend-completion cycle, docs/widget-api.md "Live figures").
//
// TWO MODES, ONE SAMPLER. The baseline pass of every measurement_sample provider runs at the
// measurement interval whatever happens, and is persisted (flowvolume.go). A provider declaring a
// live pass (internal/plugin, live.go) is ALSO run at its live cadence -- and only while the
// live-interest lease holds, that is while somebody has asked for a live figure within
// live_interest_seconds. Its readings are stored as fine samples, marked with the cadence they were
// taken at, and purged after live_sample_retention_seconds.
//
// ONE ROUND OF CALLS SERVES BOTH. The live pass's requests carry the live cadence as their max
// age, so the broker classes them real-time at or below its threshold and coalesces every reader
// of one datum into one call; when the baseline pass falls due during live mode, its requests --
// whose max age is the measurement interval -- are served from the mirror by the live pass's last
// answer, and its readings, stamped with that answer's instant, promote the fine rows already
// stored rather than adding a second copy (store.InsertMeasurementSample). No extra call is made
// and the baseline series has no hole.
//
// THE LOOP TICKS AT THE FINEST LIVE CADENCE, and a tick with no lease does nothing at all: no
// firewall call, no row. Each provider runs once its own cadence has elapsed since its last live
// pass.

// SetLiveInterest gives the collector the lease its live passes run under. Without one, no live
// pass ever runs; the baseline is unchanged.
func (c *MeasurementCoordinator) SetLiveInterest(interest *live.Interest) {
	c.Live.Mutex.Lock()
	defer c.Live.Mutex.Unlock()
	c.Live.Interest = interest
}

// LivePasses is how many live passes this collector has run.
func (c *MeasurementCoordinator) LivePasses() int {
	c.Live.Mutex.Lock()
	defer c.Live.Mutex.Unlock()
	return c.Live.Passes
}

// liveTask is the live sampling loop.
func (c *MeasurementCoordinator) LiveTask(settings *config.Live) core.Task {
	return core.Task{
		Name: "live sampling",
		Interval: func() time.Duration {
			if finest, found := c.finestLiveCadence(context.Background()); found {
				return finest
			}
			// No provider declares a live pass: the loop idles at the baseline's cadence.
			return settings.Get().MeasurementInterval
		},
		Run: c.CollectLive,
		// At start no lease holds, so the first tick does nothing; it runs at start like every loop
		// but the purge.
		RunAtStart: true,
	}
}

// liveSource is one provider's live pass.
type liveSource struct {
	providerKey string
	source      measurement.LiveSource
}

// liveSources are the implementations declaring a live pass, by provider key.
func (c *MeasurementCoordinator) liveSources() []liveSource {
	var result []liveSource
	for key, source := range c.SourceSet.Measurement.Sources() {
		if declared, isLive := source.(measurement.LiveSource); isLive {
			result = append(result, liveSource{providerKey: key, source: declared})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].providerKey < result[j].providerKey })
	return result
}

// liveCadence is one live source's cadence in force: its setting, never below its lower bound nor
// its endpoints' freshness floor.
func (c *MeasurementCoordinator) liveCadence(ctx context.Context, source measurement.LiveSource) time.Duration {
	key, fallback, minimum, floor := source.LiveCadence()
	seconds := c.SettingSeconds(ctx, key, fallback)
	if seconds < minimum {
		seconds = minimum
	}
	if seconds < floor {
		seconds = floor
	}
	return time.Duration(seconds) * time.Second
}

// finestLiveCadence is the shortest live cadence of the implementations declaring a live pass.
func (c *MeasurementCoordinator) finestLiveCadence(ctx context.Context) (time.Duration, bool) {
	var finest time.Duration
	for _, declared := range c.liveSources() {
		if cadence := c.liveCadence(ctx, declared.source); finest == 0 || cadence < finest {
			finest = cadence
		}
	}
	return finest, finest > 0
}

// CollectLive runs, while the live-interest lease holds, the live pass of every active
// measurement_sample provider declaring one whose cadence has elapsed since its last. With no
// lease it returns at once, having called nothing.
func (c *MeasurementCoordinator) CollectLive(ctx context.Context) error {
	c.Live.Mutex.Lock()
	interest := c.Live.Interest
	c.Live.Mutex.Unlock()
	now := c.Clock.Now()
	if !interest.Held(now) {
		return nil
	}
	declared := map[string]measurement.LiveSource{}
	for _, entry := range c.liveSources() {
		declared[entry.providerKey] = entry.source
	}
	if len(declared) == 0 {
		return nil
	}
	active, err := c.ActiveSources(ctx, core.KindMeasurementSample)
	if err != nil {
		return err
	}
	var failures []error
	for _, source := range active {
		liveSource, isLive := declared[source.ProviderKey]
		if !isLive {
			continue
		}
		cadence := c.liveCadence(ctx, liveSource)
		if !c.liveDue(source.ProviderID, now, cadence) {
			continue
		}
		if err := c.collectLiveFrom(ctx, source, liveSource, cadence); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the live pass was incomplete: %w", core.JoinErrors(failures))
	}
	return nil
}

// liveDue says whether a provider's live cadence has elapsed since its last live pass, and if so
// records this one as begun. A tick a fraction of a second early -- the clock's own jitter -- is
// still due, so a pass at the loop's own cadence is never skipped to the next tick.
func (c *MeasurementCoordinator) liveDue(providerID int64, now time.Time, cadence time.Duration) bool {
	c.Live.Mutex.Lock()
	defer c.Live.Mutex.Unlock()
	if c.Live.Last == nil {
		c.Live.Last = map[int64]time.Time{}
	}
	if last, ran := c.Live.Last[providerID]; ran && now.Sub(last) < cadence-cadence/10 {
		return false
	}
	c.Live.Last[providerID] = now
	c.Live.Passes++
	return true
}

// collectLiveFrom runs one live pass of one provider and stores its readings as fine samples.
//
// A healthy live pass leaves the availability row alone: the baseline pass writes it, with the
// readings of the gauges a live pass does not read. A failed one records its outcome, so a live
// figure that stopped answering is never mistaken for a quiet network.
func (c *MeasurementCoordinator) collectLiveFrom(ctx context.Context, active core.ActiveSource, source measurement.LiveSource,
	cadence time.Duration) error {
	readings, _, result, err := source.Live(ctx, c, c.Discovery(), active.ProviderID, c.Now(), cadence)
	if err != nil {
		if result.Outcome != "" {
			if writeErr := c.WriteAvailability(ctx, active.ProviderID, vocab.StateUnavailable, result.Outcome,
				result.Probe, result.Detail); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if result.State == vocab.StateUnavailable && result.Outcome != "" {
		if err := c.WriteAvailability(ctx, active.ProviderID, result.State, result.Outcome, result.Probe,
			result.Detail); err != nil {
			return err
		}
	}
	seconds := int64(cadence / time.Second)
	for _, reading := range readings {
		reading.LiveCadenceSeconds = &seconds
		if err := c.Store.InsertMeasurementSample(ctx, reading); err != nil {
			return err
		}
	}
	return nil
}
