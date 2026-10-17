package store

import "time"

// The everyday suite's breakdown of where its time goes (internal/testtiming, ci/testtiming) records
// how long each step of opening a store and of a test database's life took. The store does not link
// that package: a test binary hands it in through Timing (internal/store/storetest does, from its
// init), and the product leaves it nil, so nothing is recorded and no test code is in the binary.

// Step names of the breakdown.
const (
	TimingClone    = "clone"
	TimingSchema   = "schema"
	TimingRole     = "role"
	TimingWait     = "wait"
	TimingRetry    = "retry"
	TimingDrop     = "drop"
	TimingLifetime = "lifetime"
)

// Timer records the steps of the breakdown.
type Timer interface {
	// Record records a step of the test package calling it.
	Record(step string, took time.Duration)
	// RecordFor records a step of a named test package.
	RecordFor(pkg, step string, took time.Duration)
	// Package names the test package calling it.
	Package() string
}

// Timing is the breakdown's recorder, nil outside a test binary that sets it.
var Timing Timer

func recordTiming(step string, took time.Duration) {
	if Timing != nil {
		Timing.Record(step, took)
	}
}

func recordTimingFor(pkg, step string, took time.Duration) {
	if Timing != nil {
		Timing.RecordFor(pkg, step, took)
	}
}

func timingPackage() string {
	if Timing != nil {
		return Timing.Package()
	}
	return ""
}
