package core

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Task is one scheduled loop.
type Task struct {
	// Name identifies the loop in an error report. It is English and it names the
	// source, not the function.
	Name string
	// Interval returns how long the loop waits before its next pass. It is asked
	// again before every wait, which is how a saved interval reaches a running loop;
	// a value that is not positive leaves the previous one in force, and a task
	// whose interval is not positive at start is not run at all. FixedInterval
	// builds one that never changes.
	Interval func() time.Duration
	// Run is one pass. An error is reported and the loop continues.
	Run func(context.Context) error
	// RunAtStart says whether the first pass happens immediately rather than after
	// one interval. Discovery and the probe round do; the purge does not, because
	// there is nothing to purge in the first second of a run.
	RunAtStart bool
	// Wake, when it is not nil, runs a pass as soon as it receives, without waiting
	// for the interval, which then starts again. It is how a save on the settings
	// surface reaches a loop whose interval is a day long.
	Wake <-chan struct{}
	// MinGap, when positive, is the least time between the starts of two passes: a wake that
	// comes sooner runs its pass once the gap has passed, never earlier. It is how a pass a
	// notification triggers is held to its declared lower bound.
	MinGap time.Duration
}

// ErrorReporter is told about every failed pass. It is an interface-free function so
// that this package imposes no logging library on the program.
type ErrorReporter func(taskName string, err error)

// FixedInterval returns an interval function that always answers interval. It is
// for a task whose cadence is not a setting, and for a test that wants a loop at a
// known cadence without building a configuration around it.
//
// The value is captured once; nothing can change it afterwards.
func FixedInterval(interval time.Duration) func() time.Duration {
	return func() time.Duration { return interval }
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
		if task.Run == nil || task.Interval == nil || task.Interval() <= 0 {
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
	// The interval is asked for before every wait, so a saved one takes effect on
	// the next pass. One that is not positive keeps the last good one rather than
	// spinning the loop: Run already refused to start a task with none.
	interval := task.Interval()
	var lastStart time.Time
	run := func() {
		if task.MinGap > 0 && !lastStart.IsZero() {
			if wait := task.MinGap - clock.Now().Sub(lastStart); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-clock.After(wait):
				}
			}
		}
		lastStart = clock.Now()
		if err := task.Run(ctx); err != nil && ctx.Err() == nil {
			report(task.Name, err)
		}
	}
	if task.RunAtStart {
		lastStart = clock.Now()
	}
	for {
		if next := task.Interval(); next > 0 {
			interval = next
		}
		// A nil Wake is a channel nothing ever sends on, so a task without one only
		// ever waits for its interval.
		select {
		case <-ctx.Done():
			return
		case <-task.Wake:
			if ctx.Err() != nil {
				return
			}
			run()
		case <-clock.After(interval):
			if ctx.Err() != nil {
				return
			}
			run()
		}
	}
}
