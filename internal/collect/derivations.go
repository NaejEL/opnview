package collect

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The plugins' derivations (PA4 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md;
// internal/plugin, derivation.go): one loop each, so two triggers arriving together run it one after the
// other and never at once. A collection pass of a kind it names -- or of one provider of that kind -- is
// noted from the notification the pass published; a batch its plugin's push entry stored is noted by the
// entry's sink; its cadence is its loop's interval. After a run that changed measure rows, the aggregate
// slots of those connections are refreshed.

// derivationIdle is the interval of a derivation with no cadence: its loop only wakes on its triggers, and a
// pass at this interval with nothing noted runs no script.
const derivationIdle = time.Hour

// DerivationTasks are the loops of the derivations the source set's plugins declare.
func (c *Collector) DerivationTasks() []core.Task {
	var tasks []core.Task
	for _, derivation := range c.SourceSet.Derivations() {
		for _, pass := range derivation.AfterPasses() {
			kind, key, scoped := strings.Cut(pass, "/")
			c.Triggers.Observe([]string{sqldb.ChannelCollectionPass + ":" + kind}, func(notification sqldb.Notification) {
				passed, _ := notification.Payload["provider_key"].(string)
				if scoped && passed != key {
					return
				}
				derivation.Note(provider.DerivationChange{Trigger: provider.TriggerPass, Kind: kind, Provider: passed,
					At: c.Now()})
			})
		}
		interval := derivation.Cadence()
		if interval <= 0 {
			interval = derivationIdle
		}
		tasks = append(tasks, core.Task{
			Name:     "derivation " + derivation.Name(),
			Interval: core.FixedInterval(interval),
			Run:      func(ctx context.Context) error { return c.RunDerivation(ctx, derivation) },
			Wake:     derivation.Wake(),
			// At start-up too: what arrived or was stored while the service was down is derived at once.
			RunAtStart: true,
		})
	}
	return tasks
}

// RunDerivation runs one derivation over what was noted for it. A failed run wrote nothing, and its closed
// outcome is recorded in the availability of its plugin's provider; a run that changed measure rows has their
// connections' aggregate slots refreshed.
func (c *Collector) RunDerivation(ctx context.Context, derivation provider.Derivation) error {
	outcome, err := derivation.Run(ctx, c.Collector, c.Now())
	var failure *provider.DerivationFailure
	if errors.As(err, &failure) && failure.ProviderKind == "" {
		// A plugin of derivations alone has no provider to record the failure in: the loop's report logs it.
		return failure
	}
	if errors.As(err, &failure) {
		providerID, idErr := c.Store.ProviderID(ctx, failure.ProviderKind, failure.ProviderKey)
		if idErr != nil {
			return errors.Join(failure, idErr)
		}
		if writeErr := c.WriteAvailability(ctx, providerID, vocab.StateUnavailable, failure.Outcome, failure.Probe,
			failure.Detail); writeErr != nil {
			return errors.Join(failure, writeErr)
		}
		return failure
	}
	if err != nil {
		return err
	}
	if outcome.Ran {
		// The derivations that follow this one (ADR 0018, after_derivations) are woken, in this process: every
		// derivation of the installation runs here.
		ran := strings.Replace(derivation.Name(), ".", "/", 1)
		for _, follower := range c.SourceSet.Derivations() {
			for _, after := range follower.AfterDerivations() {
				if after == ran {
					follower.Note(provider.DerivationChange{Trigger: provider.TriggerDerivation, Entry: after, At: c.Now()})
				}
			}
		}
	}
	// The rollups plugin's refresh follows on its own triggers (ADR 0021): the flows a run changed
	// (changed_at) and the connections it wrote measure rows for (connection_measure.computed_at)
	// select their slots at its next run.
	return nil
}

// DerivationParameters supplies the core's values to the plugins' SQL-only derivations (ADR 0020, waves 3
// and 4): the discovery interval a held address is read with, the leg-pairing window, the installation's
// retention in seconds, zero for an unlimited one, and the two attribution windows.
func (c *Collector) DerivationParameters(ctx context.Context) (map[string]any, error) {
	held, err := c.Store.HeldMargin(ctx)
	if err != nil {
		return nil, err
	}
	c.Mutex.RLock()
	window := c.PairingWindowSeconds
	c.Mutex.RUnlock()
	retention := c.SettingSeconds(ctx, config.KeyRetentionSeconds, config.DefaultRetentionSeconds)
	c.Mutex.RLock()
	timing, cache := c.MaxDelaySeconds, c.CacheAnswerDelaySeconds
	// The ingest watermark (ADR 0021, step 3): the oldest instant a pass still in flight stamped its rows
	// with, or nothing when none is -- what a slot's computed_at may be stamped with so that a row committed
	// after the run read its slot, stamped before, is read by the next run rather than lost.
	var watermark any
	for _, stamped := range c.Ingesting {
		if watermark == nil || stamped < watermark.(int64) {
			watermark = stamped
		}
	}
	c.Mutex.RUnlock()
	return map[string]any{"held_margin": held, "leg_pairing_window": window, "retention": retention,
		"attribution_max_delay": timing, "cache_answer_delay": cache, "ingest_watermark": watermark}, nil
}
