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
	// SequenceAsserted judges a loss the sender's sequence numbers show over [from, to]; nil asserts
	// every one.
	SequenceAsserted func(ctx context.Context, c *Collector, key string, from, to int64) bool
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

// PushSequenceAsserted asks the kind's coordinator whether a loss is asserted.
func (c *Collector) PushSequenceAsserted(ctx context.Context, kind, key string, from, to int64) bool {
	handler, found := pushHandlerOf(kind)
	if !found || handler.SequenceAsserted == nil {
		return true
	}
	return handler.SequenceAsserted(ctx, c, key, from, to)
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
