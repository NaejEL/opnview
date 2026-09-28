package collect

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// The scheduler.
//
// FIVE COLLECTOR LOOPS AT THE SURVEYED CADENCES, plus runtime discovery and the
// retention purge. Each loop runs in its own goroutine and each failure is isolated:
// a collector that returns an error is reported and its loop continues, and the
// other four are untouched. One unavailable source never stalls the others, which
// is the thing that would otherwise happen first — a firewall with no Suricata would
// stop the filter log.
//
// THE PURGE IS ONE OF THE LOOPS, from the first day. sql/purge.sql and
// setting.retention_seconds both exist, and without a loop running them the database
// grows without bound from the first poll.
//
// THE CLOCK IS INJECTED, so a test drives every loop deterministically rather than
// sleeping. Nothing here reads time.Now directly.

// Task is one scheduled loop.
type Task struct {
	// Name identifies the loop in an error report. It is English and it names the
	// source, not the function.
	Name string
	// Interval is how long the loop waits between passes.
	Interval time.Duration
	// Run is one pass. An error is reported and the loop continues.
	Run func(context.Context) error
	// RunAtStart says whether the first pass happens immediately rather than after
	// one interval. Discovery and the probe round do; the purge does not, because
	// there is nothing to purge in the first second of a run.
	RunAtStart bool
}

// ErrorReporter is told about every failed pass. It is an interface-free function so
// that this package imposes no logging library on the program.
type ErrorReporter func(taskName string, err error)

// Tasks returns the seven loops, wired to one collector and one configuration.
//
// The five collector cadences are the intervals internal/config carries, each of
// which names the survey section that justifies it beside its constant. They are
// not restated here, so the justification cannot drift from the number.
func (c *Collector) Tasks(settings config.Config, purge func(context.Context) error) []Task {
	return []Task{
		{
			Name:       "runtime discovery",
			Interval:   settings.DiscoveryInterval,
			Run:        c.RefreshDiscovery,
			RunAtStart: true,
		},
		{
			// The probe round shares discovery's cadence: which implementation of a
			// kind opnview reads can change when the firewall's configuration does,
			// and that is the same kind of fact discovery refreshes.
			Name:       "availability probes",
			Interval:   settings.DiscoveryInterval,
			Run:        c.ProbeAll,
			RunAtStart: true,
		},
		{
			Name:       "filter log",
			Interval:   settings.FirewallLogInterval,
			Run:        c.CollectFirewallLog,
			RunAtStart: true,
		},
		{
			Name:       "security events",
			Interval:   settings.SecurityEventInterval,
			Run:        c.CollectSecurityEvent,
			RunAtStart: true,
		},
		{
			Name:       "sampled measurement",
			Interval:   settings.MeasurementInterval,
			Run:        c.CollectMeasurement,
			RunAtStart: true,
		},
		{
			Name:       "DHCP leases",
			Interval:   settings.DHCPLeaseInterval,
			Run:        c.CollectDHCPLease,
			RunAtStart: true,
		},
		{
			Name:       "resolver lookups",
			Interval:   settings.DNSLookupInterval,
			Run:        c.CollectDNSLookup,
			RunAtStart: true,
		},
		{
			Name:       "retention purge",
			Interval:   settings.PurgeInterval,
			Run:        purge,
			RunAtStart: false,
		},
	}
}

// Run runs every task until the context is cancelled, then returns.
//
// It returns nil on a clean cancellation. A cancelled context is how the program
// stops, not a failure, and returning an error for it would make a normal SIGTERM
// look like a crash in whatever reads the exit code.
func Run(ctx context.Context, clock Clock, tasks []Task, report ErrorReporter) error {
	if clock == nil {
		clock = SystemClock{}
	}
	if report == nil {
		report = func(string, error) {}
	}

	var waiting sync.WaitGroup
	for _, task := range tasks {
		if task.Run == nil || task.Interval <= 0 {
			// A loop with no work or no cadence is a configuration mistake rather than
			// something to run every zero seconds.
			continue
		}
		waiting.Add(1)
		go func(task Task) {
			defer waiting.Done()
			runLoop(ctx, clock, task, report)
		}(task)
	}
	waiting.Wait()

	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// runLoop is one task's loop. The failure isolation is here: a pass that returns an
// error is reported and the loop waits for the next tick exactly as a successful one
// does.
func runLoop(ctx context.Context, clock Clock, task Task, report ErrorReporter) {
	if task.RunAtStart {
		if err := task.Run(ctx); err != nil && ctx.Err() == nil {
			report(task.Name, err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-clock.After(task.Interval):
			if ctx.Err() != nil {
				return
			}
			if err := task.Run(ctx); err != nil && ctx.Err() == nil {
				report(task.Name, err)
			}
		}
	}
}
