package core

import (
	"context"
	"sync"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// The core's side of every push entry delivering a kind's records (provider.PushReceiver). The
// collector takes them through one receiver whatever the kind, and hands them to the coordinator of the
// kind, which registers what it does with them from its own package, by init(): no list here names a
// kind. A kind no coordinator registered a rule for is refused, and asserts every loss its stream shows.

// PushHandler is what one kind's coordinator does with what its push entries deliver.
type PushHandler struct {
	// Receive stores a provider's pushed records.
	Receive func(ctx context.Context, c *Collector, key string, records []provider.Answer) error
	// Observe is told a condition of the stream; nil ignores it.
	Observe func(ctx context.Context, c *Collector, key string, condition provider.PushCondition) error
	// Liveness is told when a push entry replacing one of the provider's polled endpoints becomes alive or
	// stops being alive; nil ignores it.
	Liveness func(ctx context.Context, c *Collector, key string, alive bool, at int64) error
}

var pushHandlers = struct {
	sync.RWMutex
	byKind map[string]PushHandler
}{byKind: map[string]PushHandler{}}

// RegisterPushHandler registers a kind's coordinator as the receiver of its pushed records.
func RegisterPushHandler(kind string, handler PushHandler) {
	pushHandlers.Lock()
	defer pushHandlers.Unlock()
	pushHandlers.byKind[kind] = handler
}

func pushHandlerOf(kind string) (PushHandler, bool) {
	pushHandlers.RLock()
	defer pushHandlers.RUnlock()
	handler, found := pushHandlers.byKind[kind]
	return handler, found
}

// ReceivePushed hands a provider's pushed records to its kind's coordinator.
func (c *Collector) ReceivePushed(ctx context.Context, kind, key string, records []provider.Answer) error {
	handler, found := pushHandlerOf(kind)
	if !found || handler.Receive == nil {
		return &unhandledPush{kind: kind}
	}
	return handler.Receive(ctx, c, key, records)
}

// ObservePushCondition tells the kind's coordinator a condition of a stream.
func (c *Collector) ObservePushCondition(ctx context.Context, kind, key string, condition provider.PushCondition) error {
	handler, found := pushHandlerOf(kind)
	if !found || handler.Observe == nil {
		return nil
	}
	return handler.Observe(ctx, c, key, condition)
}

// PushLiveness tells the kind's coordinator a replacing push entry's liveness changed.
func (c *Collector) PushLiveness(ctx context.Context, kind, key string, alive bool, at int64) error {
	handler, found := pushHandlerOf(kind)
	if !found || handler.Liveness == nil {
		return nil
	}
	return handler.Liveness(ctx, c, key, alive, at)
}

// unhandledPush is a pushed record of a kind no coordinator receives.
type unhandledPush struct{ kind string }

func (e *unhandledPush) Error() string {
	return "collect: no coordinator receives pushed records of the " + e.kind + " kind"
}

// SequenceBoundObserver is told every recorded read of a sequence bound (Collector.RecordSequenceBound):
// the provider, the value's name, whether the sender's configuration changed, and how many counters it now
// runs. The push service bounds the decoders of every entry that takes its bound from that value, and has
// them follow the sender's sequence numbers afresh when the configuration changed.
type SequenceBoundObserver func(kind, key, name string, changed bool, counters int)

// SetSequenceBoundObserver gives the collector what it tells after every read of a sequence bound.
func (c *Collector) SetSequenceBoundObserver(observe SequenceBoundObserver) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.SequenceBoundObserver = observe
}

// RecordSequenceBound records one read of a provider's sequence bound under its value name (schema.sql,
// sequence_bound), then tells the observer. A push entry declaring the value judges every loss its sender's
// sequence numbers show by it.
func (c *Collector) RecordSequenceBound(ctx context.Context, kind, key, name string, at int64, counters int,
	changed bool) error {
	providerID, err := c.Store.ProviderID(ctx, kind, key)
	if err != nil {
		return err
	}
	if err := c.Store.RecordSequenceBound(ctx, providerID, name, at, int64(counters), changed); err != nil {
		return err
	}
	c.Mutex.RLock()
	observe := c.SequenceBoundObserver
	c.Mutex.RUnlock()
	if observe != nil {
		observe(kind, key, name, changed, counters)
	}
	return nil
}
