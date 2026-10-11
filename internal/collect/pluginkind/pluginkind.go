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

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store"
)

// Pass is what one collection pass produced: the rows of the plugin's own tables, by table name,
// and the durable state to store with them.
type Pass struct {
	Rows  store.PluginRows
	State map[string]string
}

// Source is one implementation of a declared kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Collect reads the source once. state is what the last pass stored.
	Collect(ctx context.Context, host provider.Session, state map[string]string) (Pass, provider.Result, error)
}

// Registration is one implementation of a declared kind.
type Registration struct {
	Kind   string
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

// CadenceSettingKey is the setting a provider's cadence is overridden by, namespaced by its plugin:
// <plugin>_cadence__<kind>__<provider key>. A declared setting key never holds a double underscore,
// so no setting a plugin declares can collide with it. The override is never below the provider's
// declared lower bound.
func CadenceSettingKey(plugin, kind, key string) string {
	return plugin + "_" + CadenceSettingName(kind, key)
}

// CadenceSettingName is the override's key among the plugin's own settings.
func CadenceSettingName(kind, key string) string { return "cadence__" + kind + "__" + key }
