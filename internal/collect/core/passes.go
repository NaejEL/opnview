package core

import (
	"context"
	"sync"
)

// The collection passes of the core kinds. Each kind's coordinator registers its pass from its own
// package, by init(), and the scheduler runs one loop for every kind whose file declares a poll cadence
// and whose coordinator registered a pass (internal/collect/scheduler.go): no list here or there names a
// kind, so adding a kind with a pass is adding its kind file and its coordinator.

// Pass is a kind's collection pass on a collector.
type Pass func(c *Collector) func(context.Context) error

var passes = struct {
	sync.RWMutex
	byKind map[string]Pass
}{byKind: map[string]Pass{}}

// RegisterPass registers a kind's coordinator's collection pass.
func RegisterPass(kind string, pass Pass) {
	passes.Lock()
	defer passes.Unlock()
	passes.byKind[kind] = pass
}

// PassOf returns a kind's registered collection pass on this collector.
func (c *Collector) PassOf(kind string) (func(context.Context) error, bool) {
	passes.RLock()
	defer passes.RUnlock()
	pass, found := passes.byKind[kind]
	if !found {
		return nil, false
	}
	return pass(c), true
}
