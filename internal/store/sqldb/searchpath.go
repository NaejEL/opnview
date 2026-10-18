package sqldb

import "strings"

// The search path of opnview's own connections and of the read role.
//
// THE BRIDGE (ADR 0020). While a table moves from the core schema to a plugin's, the Go that still
// reads or writes it by its unqualified name -- all of it scheduled for deletion by ADR 0017 -- keeps
// working because the plugin's schema is on the search path, after the core's. The list below is the
// bridge: one schema per wave of the migration, and the wave that deletes the last Go naming a moved
// table removes its schema from here. The plugin role never has it: a plugin's SQL names a moved
// table by its qualified name, and an unqualified one refuses the plugin at load (pluginrole.go).
var BridgeSchemas = []string{
	// Wave 1: the installation model (interface, interface_address, interface_map, interface_network,
	// rule, client, owner, and the views this_firewall_address and blocked_decision).
	"plugin_installation",
}

// SearchPath is the search path of opnview's own connections and the read role: the core schema, the
// bridge, then public, where the TimescaleDB extension's functions are.
func SearchPath() string {
	return strings.Join(append(append([]string{CoreSchema}, BridgeSchemas...), "public"), ", ")
}

// CoreSearchPath is the search path without the bridge: what a plugin's own SQL and the plan check of a
// plugin's statement resolve names through.
func CoreSearchPath() string { return CoreSchema + ", public" }
