package collect

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The live pass (docs/plugins.md, "Live pass"): while somebody looks at a live figure, each provider
// that declared a live pass is run at its live cadence, and the rows its script answers are stored
// into the provider's own tables as fine samples -- rows the script stamps with the cadence they were
// taken at. A live row never replaces a row already held: a baseline pass that read the same capture
// at the same instant keeps its row, and a baseline arriving after promotes the live row by replacing
// it, which is how one capture is stored once (ADR 0020 wave 4; the Go measurement coordinator did
// the same with a conditional upsert before it was deleted).

// SetLiveInterest gives the collector the live-interest lease the live passes run under.
func (c *Collector) SetLiveInterest(interest *live.Interest) {
	c.Live.Mutex.Lock()
	defer c.Live.Mutex.Unlock()
	c.Live.Interest = interest
}

// LivePasses counts the live passes run so far, for the tests.
func (c *Collector) LivePasses() int {
	c.Live.Mutex.Lock()
	defer c.Live.Mutex.Unlock()
	return c.Live.Passes
}

// LiveTask is the live sampling loop: at the finest live cadence declared, or at the live kinds' poll
// interval when no provider declared one, it runs every due live pass while the lease holds.
func (c *Collector) LiveTask(settings *config.Live) core.Task {
	return core.Task{
		Name: "live sampling",
		Interval: func() time.Duration {
			if finest, found := c.finestLiveCadence(context.Background()); found {
				return finest
			}
			for _, kind := range provider.Kinds() {
				if kind.LiveAnswer != "" {
					if interval := settings.Get().Interval(kind.Kind, provider.CadencePoll); interval > 0 {
						return interval
					}
				}
			}
			return time.Minute
		},
		Run:        c.CollectLive,
		RunAtStart: true,
	}
}

// liveSource is one registered provider that declared a live pass.
type liveSource struct {
	registration provider.Registration
	source       provider.LiveImplementation
}

// liveSources are the registered providers declaring a live pass, in kind and key order.
func (c *Collector) liveSources() []liveSource {
	var result []liveSource
	for _, registration := range c.SourceSet.All() {
		if declared, isLive := registration.Implementation.(provider.LiveImplementation); isLive {
			result = append(result, liveSource{registration: registration, source: declared})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].registration.Kind != result[j].registration.Kind {
			return result[i].registration.Kind < result[j].registration.Kind
		}
		return result[i].registration.Key < result[j].registration.Key
	})
	return result
}

// liveCadence is a live pass's cadence in force: its setting, never below its min nor its floor.
func (c *Collector) liveCadence(ctx context.Context, source provider.LiveImplementation) time.Duration {
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

// finestLiveCadence is the shortest live cadence declared, and whether any is.
func (c *Collector) finestLiveCadence(ctx context.Context) (time.Duration, bool) {
	var finest time.Duration
	for _, declared := range c.liveSources() {
		if cadence := c.liveCadence(ctx, declared.source); finest == 0 || cadence < finest {
			finest = cadence
		}
	}
	return finest, finest > 0
}

// CollectLive runs every live pass that is due while the live-interest lease holds.
func (c *Collector) CollectLive(ctx context.Context) error {
	c.Live.Mutex.Lock()
	interest := c.Live.Interest
	c.Live.Mutex.Unlock()
	now := c.Clock.Now()
	if !interest.Held(now) {
		return nil
	}
	sources := c.liveSources()
	if len(sources) == 0 {
		return nil
	}
	var failures []error
	for _, declared := range sources {
		kind, key := declared.registration.Kind, declared.registration.Key
		active, err := c.ActiveSources(ctx, kind)
		if err != nil {
			return err
		}
		var providerID int64
		isActive := false
		for _, source := range active {
			if source.ProviderKey == key {
				providerID, isActive = source.ProviderID, true
			}
		}
		if !isActive {
			continue
		}
		cadence := c.liveCadence(ctx, declared.source)
		if !c.liveDue(providerID, now, cadence) {
			continue
		}
		if err := c.collectLiveFrom(ctx, declared, providerID, cadence); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the live pass was incomplete: %w", core.JoinErrors(failures))
	}
	return nil
}

// liveDue reports whether a provider's live pass is due, a tenth of its cadence early so a loop
// waking a moment short of it runs rather than waits a whole cadence more.
func (c *Collector) liveDue(providerID int64, now time.Time, cadence time.Duration) bool {
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

// collectLiveFrom runs one provider's live pass and stores the rows it answered as fine samples, then
// tells the live figures the newest instant it sampled.
func (c *Collector) collectLiveFrom(ctx context.Context, declared liveSource, providerID int64, cadence time.Duration) error {
	kind, key := declared.registration.Kind, declared.registration.Key
	providerArgument := map[string]any{"kind": kind, "key": key, "id": providerID, "page_size": int64(c.PageSize(kind)),
		"live_cadence_seconds": int64(cadence / time.Second)}
	answer, result, err := declared.source.CallLive(ctx, c.Collector, cadence, providerArgument)
	if err != nil {
		if result.Outcome != "" {
			if writeErr := c.WriteAvailability(ctx, providerID, vocab.StateUnavailable, result.Outcome,
				result.Probe, result.Detail); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if result.State == vocab.StateUnavailable && result.Outcome != "" {
		if err := c.WriteAvailability(ctx, providerID, result.State, result.Outcome, result.Probe,
			result.Detail); err != nil {
			return err
		}
	}
	rows, _ := answer["rows"].(plugintables.PluginRows)
	written := 0
	for _, table := range rows {
		written += len(table)
	}
	if written == 0 {
		return nil
	}
	if err := c.Store.WriteLiveRows(ctx, declared.registration.Plugin, kind, key, rows); err != nil {
		return err
	}
	if latest := provider.Integer(answer, "latest"); latest > 0 {
		return c.Store.NotifyLiveSample(ctx, kind, key, latest)
	}
	return nil
}
