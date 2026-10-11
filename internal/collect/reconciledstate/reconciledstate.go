// Package reconciledstate is the reconciled_state kind: a source of complete sets of things of one
// type as of one instant -- an intrusion-prevention engine's ban list, the peers of a tunnel, LLDP
// neighbours.
//
// WHAT THE KIND PRODUCES. One or more Sets per read, each asserted COMPLETE at the instant it was
// read, so a thing present in one snapshot and absent from the next has left. What is true of any
// such source is written once in internal/collect (reconciledstate.go): the snapshot is written in
// one transaction (store.InsertStateSnapshot), and the departure is a view over two snapshots.
//
// IT HAS NO SURVEYED CADENCE. The core kinds run at intervals internal/config justifies from the
// survey; nothing surveyed a source of this kind, so each implementation states its own cadence,
// in its plugin manifest, with a lower bound (docs/plugins.md), and the collector runs one loop
// per implementation at that cadence.
//
// WHAT AN IMPLEMENTATION IS. A plugin (docs/plugins.md) whose manifest declares a provider of the
// kind reconciled_state and whose script exports reconciled_state.probe and
// reconciled_state.snapshot. The plugin host adds it to a source set (internal/collect/sources), never
// to a list.
package reconciledstate

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store"
)

// Kind is the registry kind this package answers for.
const Kind = "reconciled_state"

// Set is one complete set as an implementation read it: its key and its members. The provider
// and the instant are the kind's to add.
type Set struct {
	SetKey string
	Items  []store.StateItem
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
