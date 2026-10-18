package request

import (
	"context"
	"fmt"
)

// THE LOGGED ANSWER, STARTED BESIDE THE MEASURED ONE. Under volume_measure = best_available a type is
// answered in a declared measure when a run in it covers every connection it counts, and in the logged
// measure otherwise (ServeMeasured) -- which, over a window the measure covers only in part, is two
// runs one after the other: the run in the measure, judged and set aside, then the logged one. The
// logged run depends on nothing the measured run computes: it starts from what the request held before
// the measured run, which the measured run's judgement restores. So it is started at the same moment,
// on a fork of the request, and the answer served is the one the sequence would serve: the measured
// run's when it is served, the logged run's -- waited for, and its state taken into the request --
// otherwise. A response takes the longer of the two runs rather than their sum.
//
// THE FORK SHARES NOTHING A RUN WRITES. It holds a copy of the response and of every map a run fills
// (the peers placed, the measure windows read), and reads the installation afresh; what a run only
// reads -- the parameters, the references, the store -- it shares, and so are the reads a measured
// attempt makes for every run of the request (SharedRead). Its script's log
// lines are held, and written only when its answer is the one served. It always runs to its end,
// whichever answer is served, so the statements a request runs do not depend on which run ended first;
// only a request that fails stops it.

// heldLine is a line a script logged in a speculative run, written when that run's answer is served.
type heldLine struct {
	plugin, message string
	write           func(plugin, message string)
}

// ScriptLog writes a line a widget script logged: at once, or, in a speculative run, once the run's
// answer is the one served.
func (r *Request) ScriptLog(plugin, message string, write func(plugin, message string)) {
	if r.held != nil {
		*r.held = append(*r.held, heldLine{plugin: plugin, message: message, write: write})
		return
	}
	write(plugin, message)
}

// speculativeRun is the logged run started beside a measured one.
type speculativeRun struct {
	fork    *Request
	cancel  context.CancelFunc
	done    chan struct{}
	data    any
	hasRows bool
	err     error
	// panicked is what the run panicked with, raised again where its answer is taken.
	panicked any
}

// speculate starts a serve on a fork of the request.
func (r *Request) speculate(serve ServeFunc) *speculativeRun {
	fork := r.fork()
	ctx, cancel := context.WithCancel(r.Ctx)
	fork.Ctx = ctx
	run := &speculativeRun{fork: fork, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		defer func() {
			if recovered := recover(); recovered != nil {
				run.panicked = recovered
				run.err = fmt.Errorf("widget: the logged run panicked: %v", recovered)
			}
		}()
		run.data, run.hasRows, run.err = serve(fork)
	}()
	return run
}

// fork is a copy of the request a run can write without touching the request: its own response, its
// own maps, no installation read yet, no measure.
func (r *Request) fork() *Request {
	if r.shared == nil {
		// Made before the fork, so the fork shares it.
		r.shared = &sharedReads{}
	}
	fork := *r
	snapshot := r.snapshot()
	response := snapshot.response
	if snapshot.window != nil {
		window := *snapshot.window
		response.Window = &window
	}
	fork.Response = &response
	fork.Override = snapshot.override
	fork.Measure = measureRun{}
	fork.installation = nil
	fork.placed = copyMap(r.placed)
	fork.measureWindows = copyMap(r.measureWindows)
	fork.held = &[]heldLine{}
	return &fork
}

// copyMap copies a map, keeping a missing one missing.
func copyMap[V any](held map[string]V) map[string]V {
	if held == nil {
		return nil
	}
	copied := make(map[string]V, len(held))
	for key, value := range held {
		copied[key] = value
	}
	return copied
}

// wait waits for the run to end; stop says to stop it first, for a request that failed.
func (run *speculativeRun) wait(stop bool) {
	if stop {
		run.cancel()
	}
	<-run.done
	run.cancel()
}

// adopt waits for the run and takes its answer and its state into the request, as if the request had
// run it.
func (run *speculativeRun) adopt(r *Request) (any, bool, error) {
	run.wait(false)
	if run.panicked != nil {
		panic(run.panicked)
	}
	fork := run.fork
	*r.Response = *fork.Response
	r.Override = fork.Override
	r.Measure = fork.Measure
	r.placed = fork.placed
	for _, line := range *fork.held {
		line.write(line.plugin, line.message)
	}
	return run.data, run.hasRows, run.err
}
