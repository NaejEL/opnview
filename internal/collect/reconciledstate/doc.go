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
