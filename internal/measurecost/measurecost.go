package measurecost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// Requester is the name the measurement reads under, as the broker's records show it.
const Requester = "measure_cost"

// DefaultThreshold is the spec's bound: the finest cadence adding at most 2 processor percentage
// points over the idle baseline is the endpoint's freshness floor (C3.0 and LV-C).
const DefaultThreshold = 2.0

// Clock is the measurement's clock: when a poll starts and ends, and how long the next waits.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the wall clock.
type SystemClock struct{}

// Now is the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits, or returns early with the context's error.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Options say what to measure.
type Options struct {
	// Broker is the one path to the firewall: every poll and the processor stream go through it,
	// under its read-only rule.
	Broker *broker.Broker
	// Endpoint is the endpoint polled, and Request what each poll sends.
	Endpoint opnsense.Endpoint
	Request  opnsense.RequestOptions
	// Idle is how long the processor is sampled with no extra poll, first; Duration how long each
	// candidate cadence is polled.
	Idle, Duration time.Duration
	// Cadences are the candidate intervals, polled finest first.
	Cadences []time.Duration
	// Threshold is the most processor percentage points a cadence may add and still be a floor;
	// zero takes DefaultThreshold.
	Threshold float64
	// Clock is the measurement's clock; nil is the wall clock.
	Clock Clock
	// StreamPoll is how often the processor stream's events are collected; zero is one second.
	StreamPoll time.Duration
	// Progress, when not nil, is told each phase as it starts.
	Progress io.Writer
}

// Phase is what one phase measured.
type Phase struct {
	// CadenceSeconds is the polling interval, zero for the idle baseline.
	CadenceSeconds float64 `json:"cadence_seconds"`
	// ProcessorSamples is how many processor readings fell in the phase, and ProcessorPercent their
	// mean, in percent of the processor; null with none.
	ProcessorSamples int      `json:"processor_samples"`
	ProcessorPercent *float64 `json:"processor_percent"`
	// AddedPoints is the mean less the idle baseline's, in percentage points; null for the baseline
	// or when either has no reading.
	AddedPoints *float64 `json:"added_points"`
	// Polls, Failures and the latency and size figures describe the polls of the phase.
	Polls               int      `json:"polls"`
	Failures            int      `json:"failures"`
	LatencyMedianMS     *float64 `json:"latency_median_ms"`
	LatencyP95MS        *float64 `json:"latency_p95_ms"`
	ResponseBytesMedian *float64 `json:"response_bytes_median"`
	// WithinThreshold says the cadence added at most the threshold.
	WithinThreshold bool `json:"within_threshold"`
}

// Report is the measurement: proportions and timings only, nothing the firewall answered.
type Report struct {
	Endpoint  string  `json:"endpoint"`
	Method    string  `json:"method"`
	Threshold float64 `json:"threshold_points"`
	Idle      Phase   `json:"idle"`
	Cadences  []Phase `json:"cadences"`
	// FloorSeconds is the finest cadence adding at most the threshold, null when none did: the
	// endpoint's freshness floor.
	FloorSeconds *float64 `json:"floor_seconds"`
}

// ErrNoCadence is a measurement asked for no candidate cadence.
var ErrNoCadence = errors.New("measurecost: no candidate cadence to measure")

// Measure measures the endpoint's cost: the idle baseline, then each candidate cadence, finest
// first, each phase sampling the processor stream throughout.
func Measure(ctx context.Context, options Options) (Report, error) {
	if len(options.Cadences) == 0 {
		return Report{}, ErrNoCadence
	}
	if options.Clock == nil {
		options.Clock = SystemClock{}
	}
	if options.Threshold <= 0 {
		options.Threshold = DefaultThreshold
	}
	if options.StreamPoll <= 0 {
		options.StreamPoll = time.Second
	}
	cadences := append([]time.Duration(nil), options.Cadences...)
	sort.Slice(cadences, func(i, j int) bool { return cadences[i] < cadences[j] })
	report := Report{Endpoint: options.Endpoint.Path, Method: options.Endpoint.Method, Threshold: options.Threshold}
	sampler := newProcessorSampler(options)
	defer sampler.stop()

	idle, err := runPhase(ctx, options, sampler, 0, options.Idle)
	if err != nil {
		return report, err
	}
	report.Idle = idle
	for _, cadence := range cadences {
		phase, err := runPhase(ctx, options, sampler, cadence, options.Duration)
		if err != nil {
			return report, err
		}
		report.Cadences = append(report.Cadences, phase)
	}
	Summarise(&report)
	return report, nil
}

// Summarise computes each cadence's added points and whether it is within the threshold, and the
// floor: the finest cadence within it.
func Summarise(report *Report) {
	report.FloorSeconds = nil
	for index := range report.Cadences {
		phase := &report.Cadences[index]
		phase.AddedPoints, phase.WithinThreshold = nil, false
		if phase.ProcessorPercent == nil || report.Idle.ProcessorPercent == nil {
			continue
		}
		added := *phase.ProcessorPercent - *report.Idle.ProcessorPercent
		phase.AddedPoints = &added
		phase.WithinThreshold = added <= report.Threshold
	}
	for index := range report.Cadences {
		phase := report.Cadences[index]
		if phase.WithinThreshold && phase.Failures == 0 &&
			(report.FloorSeconds == nil || phase.CadenceSeconds < *report.FloorSeconds) {
			seconds := phase.CadenceSeconds
			report.FloorSeconds = &seconds
		}
	}
}

// runPhase polls at a cadence -- none for the baseline -- for a duration, and returns what the phase
// measured.
func runPhase(ctx context.Context, options Options, sampler *processorSampler, cadence, duration time.Duration) (
	Phase, error) {
	if options.Progress != nil {
		if cadence == 0 {
			fmt.Fprintf(options.Progress, "idle baseline for %s\n", duration)
		} else {
			fmt.Fprintf(options.Progress, "polling %s every %s for %s\n", options.Endpoint.Path, cadence, duration)
		}
	}
	phase := Phase{CadenceSeconds: cadence.Seconds()}
	start := options.Clock.Now()
	sampler.mark()
	var latencies, sizes []float64
	for {
		now := options.Clock.Now()
		if now.Sub(start) >= duration {
			break
		}
		if cadence == 0 {
			if err := options.Clock.Sleep(ctx, duration-now.Sub(start)); err != nil {
				return phase, err
			}
			continue
		}
		began := options.Clock.Now()
		served, err := options.Broker.Call(ctx, broker.Request{Requester: broker.Requester{Name: Requester},
			Endpoint: options.Endpoint, Options: options.Request, Fresh: true, Whole: true})
		ended := options.Clock.Now()
		phase.Polls++
		if err != nil || served == nil || !served.Response.OK() {
			phase.Failures++
		} else {
			latencies = append(latencies, float64(ended.Sub(began))/float64(time.Millisecond))
			sizes = append(sizes, float64(len(served.Response.Body)))
		}
		if ctx.Err() != nil {
			return phase, ctx.Err()
		}
		if err := options.Clock.Sleep(ctx, cadence-ended.Sub(began)); err != nil {
			return phase, err
		}
	}
	readings := sampler.since()
	phase.ProcessorSamples = len(readings)
	if len(readings) > 0 {
		mean := 0.0
		for _, reading := range readings {
			mean += reading
		}
		mean /= float64(len(readings))
		phase.ProcessorPercent = &mean
	}
	phase.LatencyMedianMS = percentile(latencies, 0.5)
	phase.LatencyP95MS = percentile(latencies, 0.95)
	phase.ResponseBytesMedian = percentile(sizes, 0.5)
	return phase, nil
}

// percentile is the nearest-rank percentile of the values, null with none.
func percentile(values []float64, rank float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(rank*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	value := sorted[index]
	return &value
}

// processorSampler collects the processor stream's readings through the broker's shared
// subscription, for the whole measurement.
//
// The stream is scripts/system/cpu.py, `iostat -w 1 cpu`, one event a second of {total, user,
// nice, sys, intr, idle} (docs/opnsense-api-survey.md, "Processor stream, read from source for the
// resolver-cache cycle"); a reading is total / (total + idle) in percent, as the insight plugin
// reads it, and the stream's first event -- iostat's average since boot -- is discarded.
type processorSampler struct {
	mutex    sync.Mutex
	readings []float64
	marked   int
	cancel   context.CancelFunc
	done     chan struct{}
}

func newProcessorSampler(options Options) *processorSampler {
	ctx, cancel := context.WithCancel(context.Background())
	sampler := &processorSampler{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(sampler.done)
		requester := broker.Requester{Name: Requester}
		cursor := int64(0)
		first := true
		ticker := time.NewTicker(options.StreamPoll)
		defer ticker.Stop()
		for {
			events, _ := options.Broker.EventsAfter(requester, opnsense.CPUUsageStream, opnsense.RequestOptions{}, cursor)
			for _, event := range events {
				cursor = event.Sequence
				if first {
					first = false
					continue
				}
				if reading, ok := processorPercent(event.Data); ok {
					sampler.mutex.Lock()
					sampler.readings = append(sampler.readings, reading)
					sampler.mutex.Unlock()
				}
			}
			select {
			case <-ctx.Done():
				options.Broker.Unsubscribe(Requester, opnsense.CPUUsageStream, opnsense.RequestOptions{})
				return
			case <-ticker.C:
			}
		}
	}()
	return sampler
}

// mark starts a phase: the readings from here on are the phase's.
func (s *processorSampler) mark() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.marked = len(s.readings)
}

// since returns the readings since the last mark.
func (s *processorSampler) since() []float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]float64(nil), s.readings[s.marked:]...)
}

func (s *processorSampler) stop() {
	s.cancel()
	<-s.done
}

// processorPercent reads one event as a processor use in percent.
func processorPercent(data []byte) (float64, bool) {
	var event struct {
		Total *float64 `json:"total"`
		Idle  *float64 `json:"idle"`
	}
	if err := json.Unmarshal(data, &event); err != nil || event.Total == nil || event.Idle == nil ||
		*event.Total < 0 || *event.Idle < 0 || *event.Total+*event.Idle <= 0 {
		return 0, false
	}
	return 100 * *event.Total / (*event.Total + *event.Idle), true
}
