package reconciledstate

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Kind is the registry kind this package answers for.
const Kind = "reconciled_state"

// Set is one complete set as an implementation read it: its key and its members. The provider
// and the instant are the kind's to add.
type Set struct {
	SetKey string
	Items  []vocab.StateItem
}

// Source is one implementation of the reconciled_state kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Snapshot returns every set the implementation reports, each complete.
	Snapshot(ctx context.Context, host provider.Session) ([]Set, provider.Result, error)
}

// Registration is one implementation and the cadence it declared.
type Registration struct {
	Key    string
	Plugin string
	// Cadence is the declared interval between passes, and MinCadence the lowest an override may set,
	// and the least time between two passes however often a trigger fires.
	Cadence    time.Duration
	MinCadence time.Duration
	// Triggers are the core notifications that run a pass besides the cadence
	// (store.Channels), each "channel" or "channel:subject".
	Triggers []string
	Source   Source
}
