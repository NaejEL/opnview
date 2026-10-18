package collect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/config"
)

// N5 of the live validation of 17 October 2026: at start every loop ran at once, and the first
// measurement pass ran before discovery had published an interface, so it sampled nothing and the
// interface counters of the start were lost. A measurement pass waits for the run's first discovery.

// taskNamed returns a collector's loop by name.
func taskNamed(t *testing.T, collector *collect.Collector, settings config.Config, name string) core.Task {
	t.Helper()
	for _, task := range collector.Tasks(config.NewLive(settings)) {
		if task.Name == name {
			return task
		}
	}
	t.Fatalf("there is no %q loop", name)
	return core.Task{}
}

// freshCollector is a collector just started over an arranged measurement firewall: the same
// fake and store, discovery not yet run.
func freshCollector(t *testing.T) (*collecttest.ProbeHarness, *collect.Collector) {
	t.Helper()
	harness := arrangeMeasurementCollection(t)
	return harness, collecttest.NewTestCollector(t, providertest.NewClient(t, harness.Fake), harness.Store,
		harness.Clock)
}

// TestTheFirstMeasurementPassWaitsForTheFirstDiscovery is N5.
func TestTheFirstMeasurementPassWaitsForTheFirstDiscovery(t *testing.T) {
	t.Parallel()
	harness, fresh := freshCollector(t)
	task := taskNamed(t, fresh, config.Defaults(), "measurement_sample")
	detail := func() string {
		return harness.DetailOf(t, "measurement_sample", collecttest.ProviderInsight)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- task.Run(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("the measurement pass ran before the first discovery (%v): %q", err, detail())
	case <-time.After(200 * time.Millisecond):
	}

	if err := fresh.RefreshDiscovery(ctx); err != nil {
		t.Fatalf("the first discovery: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the measurement pass after the first discovery: %v", err)
	}
	if got := detail(); strings.Contains(got, "no interface has been discovered") {
		t.Errorf("the pass after the first discovery sampled no interface: %q", got)
	}

	// Once the run's first discovery has ended, a pass waits for nothing.
	if err := task.Run(ctx); err != nil {
		t.Fatalf("a later measurement pass: %v", err)
	}
}

// TestAMeasurementPassWithNoDiscoveryLoopDoesNotWait: a run whose discovery loop is off has no
// first discovery to wait for, and its measurement passes are not held for ever.
func TestAMeasurementPassWithNoDiscoveryLoopDoesNotWait(t *testing.T) {
	t.Parallel()
	_, fresh := freshCollector(t)
	settings := config.Defaults()
	settings.DiscoveryInterval = 0
	task := taskNamed(t, fresh, settings, "measurement_sample")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := task.Run(ctx); err != nil {
		t.Fatalf("a measurement pass with discovery off: %v", err)
	}
}
