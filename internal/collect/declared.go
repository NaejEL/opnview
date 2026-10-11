package collect

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/collect/pluginkind"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/reconciledstate"
	"github.com/NaejEL/opnview/internal/store"
)

// The kinds whose implementations state their own cadence: reconciled_state, and every kind a
// plugin declares for itself.
//
// WHAT IS THE SAME AS FOR EVERY OTHER KIND. Each implementation is probed in the probe round, its
// availability recorded and its activation decided by resolveKind, the operator's selection
// included; a pass reads only an active implementation; a failure is recorded in its availability
// row and stops nothing else.
//
// WHAT DIFFERS. No surveyed cadence exists for these sources, so each implementation's manifest
// states one (docs/plugins.md), and Tasks runs one loop per implementation at it. And what a pass
// stores is the kind's own: a complete set per set key for reconciled_state
// (store.InsertStateSnapshot), the rows of the plugin's own tables and its durable state, in one
// transaction, for a declared kind (store.WritePluginPass).

// probeReconciledState probes every implementation of the reconciled_state kind.
func (c *Collector) probeReconciledState(ctx context.Context) error {
	registered := c.sources.ReconciledState.Sources()
	probeables := make([]probeable, 0, len(registered))
	for key, registration := range registered {
		probeables = append(probeables, probeable{providerKey: key, run: registration.Source.Probe})
	}
	if len(probeables) == 0 {
		return nil
	}
	return c.resolveKind(ctx, reconciledstate.Kind, sortedProbeables(probeables))
}

// probeDeclaredKinds probes every implementation of every kind a plugin declared.
func (c *Collector) probeDeclaredKinds(ctx context.Context) error {
	byKind := map[string][]probeable{}
	for _, registration := range c.sources.PluginKindRegistrations() {
		byKind[registration.Kind] = append(byKind[registration.Kind],
			probeable{providerKey: registration.Key, run: registration.Source.Probe})
	}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	var failures []error
	for _, kind := range kinds {
		if err := c.resolveKind(ctx, kind, sortedProbeables(byKind[kind])); err != nil {
			failures = append(failures, err)
		}
	}
	return joinErrors(failures)
}

// declaredTasks are the loops of the implementations that state their own cadence: one per
// implementation, named for it.
func (c *Collector) declaredTasks() []Task {
	var tasks []Task
	for _, key := range sortedKeys(c.sources.ReconciledState.Sources()) {
		registration, _ := c.sources.ReconciledState.Lookup(key)
		tasks = append(tasks, Task{
			Name: fmt.Sprintf("%s %s", reconciledstate.Kind, key),
			Interval: c.overriddenCadence(pluginkind.CadenceSettingKey(registration.Plugin, reconciledstate.Kind, key),
				registration.Cadence, registration.MinCadence),
			Run:        func(ctx context.Context) error { return c.CollectReconciledState(ctx, key) },
			RunAtStart: true,
			Wake:       c.triggers.wakeOn(registration.Triggers),
			MinGap:     registration.MinCadence,
		})
	}
	for _, registration := range c.sources.PluginKindRegistrations() {
		kind, key := registration.Kind, registration.Key
		tasks = append(tasks, Task{
			Name: fmt.Sprintf("%s %s", kind, key),
			Interval: c.overriddenCadence(pluginkind.CadenceSettingKey(registration.Plugin, kind, key),
				registration.Cadence, registration.MinCadence),
			Run:        func(ctx context.Context) error { return c.CollectDeclaredKind(ctx, kind, key) },
			RunAtStart: true,
			Wake:       c.triggers.wakeOn(registration.Triggers),
			MinGap:     registration.MinCadence,
		})
	}
	return tasks
}

// cadenceOrDefault is a declared cadence, or five minutes -- the measurement sampler's own
// interval -- for a registration that declared none, which the plugin host never registers.
func cadenceOrDefault(cadence time.Duration) time.Duration {
	if cadence <= 0 {
		return 5 * time.Minute
	}
	return cadence
}

// overriddenCadence is a provider's interval, asked before every wait: the operator's override
// (pluginkind.CadenceSettingKey), never below the declared lower bound, or the declared cadence. A
// value that cannot be read leaves the declared cadence in force.
func (c *Collector) overriddenCadence(settingKey string, declared, minimum time.Duration) func() time.Duration {
	declared = cadenceOrDefault(declared)
	return func() time.Duration {
		ctx, cancel := context.WithTimeout(context.Background(), cadenceReadBound)
		defer cancel()
		text, present, err := c.store.Setting(ctx, settingKey)
		if err != nil || !present {
			return declared
		}
		seconds, err := strconv.ParseInt(text, 10, 64)
		if err != nil || seconds <= 0 {
			return declared
		}
		override := time.Duration(seconds) * time.Second
		if override < minimum {
			return minimum
		}
		return override
	}
}

// cadenceReadBound bounds the read of a cadence override before a wait.
const cadenceReadBound = 5 * time.Second

// isActive reports whether opnview reads one implementation of a kind, and its registry id.
func (c *Collector) isActive(ctx context.Context, kind, key string) (int64, bool, error) {
	sources, err := c.activeSources(ctx, kind)
	if err != nil {
		return 0, false, err
	}
	for _, source := range sources {
		if source.providerKey == key {
			return source.providerID, true, nil
		}
	}
	return 0, false, nil
}

// CollectReconciledState runs one pass of one reconciled_state implementation: every set it
// reports, each written whole, at one instant.
func (c *Collector) CollectReconciledState(ctx context.Context, key string) error {
	providerID, active, err := c.isActive(ctx, reconciledstate.Kind, key)
	if err != nil || !active {
		return err
	}
	registration, found := c.sources.ReconciledState.Lookup(key)
	if !found {
		return fmt.Errorf("collect: the active %s provider %q has no implementation", reconciledstate.Kind, key)
	}
	sets, result, readErr := registration.Source.Snapshot(ctx, c)
	if readErr != nil {
		if errors.Is(readErr, provider.ErrUnsupportedRead) {
			return c.writeAvailability(ctx, providerID, result.State, result.Outcome, result.Probe, readErr.Error())
		}
		if writeErr := c.writeAvailability(ctx, providerID, orUnavailable(result.State),
			orOutcome(result.Outcome), result.Probe, result.Detail); writeErr != nil {
			return writeErr
		}
		return readErr
	}
	now := c.now()
	items := 0
	for _, set := range sets {
		if err := c.store.InsertStateSnapshot(ctx, store.StateSnapshot{ProviderID: providerID, SetKey: set.SetKey,
			CapturedAt: now, Items: set.Items}); err != nil {
			if writeErr := c.writeAvailability(ctx, providerID, store.StateReachable, store.OutcomeStoreFailure,
				result.Probe, "the sets were read and could not be stored"); writeErr != nil {
				return writeErr
			}
			return err
		}
		items += len(set.Items)
	}
	if err := c.store.NotifyCollectionPass(ctx, reconciledstate.Kind, key); err != nil {
		return err
	}
	detail := result.Detail
	if detail == "" {
		detail = fmt.Sprintf("read %d sets holding %d items", len(sets), items)
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = store.OutcomeHealthy
	}
	if outcome == store.OutcomeHealthy && len(sets) == 0 {
		outcome = store.OutcomeHealthyEmpty
	}
	return c.writeAvailability(ctx, providerID, orReachable(result.State), outcome, result.Probe, detail)
}

// CollectDeclaredKind runs one pass of one implementation of a kind a plugin declared: it is
// handed the state it stored last, and its rows and new state are stored together.
func (c *Collector) CollectDeclaredKind(ctx context.Context, kind, key string) error {
	providerID, active, err := c.isActive(ctx, kind, key)
	if err != nil || !active {
		return err
	}
	registration, found := c.sources.PluginKind(kind, key)
	if !found {
		return fmt.Errorf("collect: the active %s provider %q has no implementation", kind, key)
	}
	state, err := c.store.PluginState(ctx, registration.Plugin)
	if err != nil {
		return err
	}
	pass, result, readErr := registration.Source.Collect(ctx, c, state)
	if readErr != nil {
		if errors.Is(readErr, provider.ErrUnsupportedRead) {
			return c.writeAvailability(ctx, providerID, result.State, result.Outcome, result.Probe, readErr.Error())
		}
		if writeErr := c.writeAvailability(ctx, providerID, orUnavailable(result.State),
			orOutcome(result.Outcome), result.Probe, result.Detail); writeErr != nil {
			return writeErr
		}
		return readErr
	}
	rows := 0
	for _, table := range pass.Rows {
		rows += len(table)
	}
	if err := c.store.WritePluginPass(ctx, registration.Plugin, key, pass.Rows, pass.State, c.now()); err != nil {
		if writeErr := c.writeAvailability(ctx, providerID, store.StateReachable, store.OutcomeStoreFailure,
			result.Probe, "the rows were read and could not be stored"); writeErr != nil {
			return writeErr
		}
		return err
	}
	detail := result.Detail
	if detail == "" {
		detail = fmt.Sprintf("stored %d rows", rows)
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = store.OutcomeHealthy
	}
	if outcome == store.OutcomeHealthy && rows == 0 {
		outcome = store.OutcomeHealthyEmpty
	}
	return c.writeAvailability(ctx, providerID, orReachable(result.State), outcome, result.Probe, detail)
}

// orUnavailable is a state a failed read left empty, as unavailable.
func orUnavailable(state store.AvailabilityState) store.AvailabilityState {
	if state == "" {
		return store.StateUnavailable
	}
	return state
}

// orReachable is a state a successful read left empty, as reachable.
func orReachable(state store.AvailabilityState) store.AvailabilityState {
	if state == "" {
		return store.StateReachable
	}
	return state
}

// orOutcome is an outcome left empty, as a request that was not made.
func orOutcome(outcome store.AvailabilityOutcome) store.AvailabilityOutcome {
	if outcome == "" {
		return store.OutcomeRequestNotMade
	}
	return outcome
}

// sortedKeys returns a map's keys, sorted.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
