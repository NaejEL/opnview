package collect

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
)

// The scheduler.
//
// ONE LOOP PER COLLECTOR AT THE SURVEYED CADENCES, plus runtime discovery and the
// retention purge. Each loop runs in its own goroutine and each failure is isolated:
// a collector that returns an error is reported and its loop continues, and the
// others are untouched. One unavailable source never stalls the others, which
// is the thing that would otherwise happen first — a firewall with no Suricata would
// stop the filter log.
//
// THE PURGE IS ONE OF THE LOOPS, from the first day. sql/purge.sql and
// setting.retention_seconds both exist, and without a loop running them the database
// grows without bound from the first poll.
//
// AN INTERVAL IS READ BEFORE EVERY WAIT, NOT ONCE AT START. Each task carries a
// function that returns its interval, and the loop asks it again before each wait,
// so an interval saved on the collection surface changes the next scheduled pass of
// a running service without a restart. A pass already waiting finishes its wait
// first: the change takes effect one pass later, never by cutting a wait short.
// The intervals come from config.Live, which the collection surface writes after
// it has stored and reloaded the configuration.
//
// THE CLOCK IS INJECTED, so a test drives every loop deterministically rather than
// sleeping. Nothing here reads time.Now directly.

// Tasks returns the loops, wired to one collector and one live configuration: the
// collectors, the two reads of the resolver's records, runtime discovery, the probe
// round and the retention purge.
//
// The collector cadences are the intervals the core kinds' files declare, each
// with the survey section that justifies it beside its default. They are not
// restated here, so the justification cannot drift from the number. Each task
// reads its own field of settings before every wait, through config.Live, so a
// saved interval reaches the loop without a restart.
//
// The retention purge is this collector's own Purge, so it is ordered against the
// passes whoever builds the loops (purge.go).
func (c *Collector) Tasks(settings *config.Live) []core.Task {
	tasks := append(append(c.coreTasks(settings), c.DeclaredTasks()...), c.DerivationTasks()...)
	tasks = append(tasks, c.LiveTask(settings))
	return append(tasks, c.listenTask()...)
}

// coreTasks are the loops of discovery, the probe round, every core kind with a poll cadence and a
// registered pass, the broker and the purge.
func (c *Collector) coreTasks(settings *config.Live) []core.Task {
	tasks := []core.Task{
		{
			Name:       "runtime discovery",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.DiscoveryInterval }),
			Run:        c.RefreshDiscovery,
			RunAtStart: true,
		},
		{
			// The probe round shares discovery's cadence: which implementation of a
			// kind opnview reads can change when the firewall's configuration does,
			// and that is the same kind of fact discovery refreshes.
			Name:       "availability probes",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.DiscoveryInterval }),
			Run:        c.ProbeAll,
			RunAtStart: true,
		},
	}
	// ONE LOOP PER CORE KIND, FROM ITS DECLARATION: every kind whose file declares a poll cadence and
	// whose coordinator registered a pass (core.RegisterPass), named after the kind, at the interval its
	// file names -- the setting, or the file's default -- read before every wait.
	for _, cadence := range provider.Cadences() {
		if cadence.Role != provider.CadencePoll {
			continue
		}
		pass, registered := c.PassOf(cadence.Kind)
		if !registered {
			continue
		}
		kind := cadence.Kind
		tasks = append(tasks, core.Task{
			Name:       kind,
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.Interval(kind, provider.CadencePoll) }),
			Run:        c.afterPass(kind, pass),
			RunAtStart: true,
		})
	}
	return append(tasks,
		core.Task{
			// The broker: its scheduler and its stream subscriptions. Its pass returns only when
			// the run stops.
			Name:       "firewall broker",
			Interval:   core.FixedInterval(time.Minute),
			Run:        func(ctx context.Context) error { c.APIBroker.Run(ctx); return nil },
			RunAtStart: true,
		},
		core.Task{
			Name:       "retention purge",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.PurgeInterval }),
			Run:        c.Purge,
			RunAtStart: false,
		})
}
