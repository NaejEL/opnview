// Package pluginkind is every kind a plugin declares for itself: a source whose records fit none
// of the core kinds, written into tables the plugin owns.
//
// THE KIND IS THE PLUGIN'S, AND SO IS ITS CONTRACT. A core kind publishes a record contract
// (internal/collect/provider, contract.go); a declared kind's contract is the tables its plugin
// declares -- columns, keys, indexes, retention -- which opnview creates at load
// (store.EnsurePluginTable). What is true of every declared kind is written once in
// internal/collect (pluginkind.go): the probe round decides whether opnview reads it, exactly as for
// a core kind; a pass hands the implementation the durable state it stored last time, and stores
// the rows it returns together with its new state in ONE transaction (store.WritePluginPass), so a
// restart neither loses nor repeats a record; the retention purge removes its old rows.
//
// WHAT AN IMPLEMENTATION IS. A plugin (docs/plugins.md, "A kind of your own") whose manifest
// declares a provider of a kind no core contract names, with the tables that kind writes, and
// whose script exports <kind>.probe and <kind>.collect. The plugin host adds it to a source set
// (internal/collect/sources), never to a list.
package pluginkind
