package sqldb

import "strings"

// The search path of opnview's own connections and of the read role.
//
// THE BRIDGE (ADR 0020). While a table moves from the core schema to a plugin's, the Go that still
// reads or writes it by its unqualified name -- all of it scheduled for deletion by ADR 0017 -- keeps
// working because the plugin's schema is on the search path, after the core's. The bridge is every
// built-in plugin's schema that holds tables or relations, in the built-ins' load order, set by the
// store from their manifests (builtintables.go) so that no plugin is named here; the wave that
// deletes the last Go naming a moved table removes the bridge altogether. The plugin role never has
// it: a plugin's SQL names a moved table by its qualified name, and an unqualified one refuses the
// plugin at load (pluginrole.go).
var bridgeSchemas []string

// SetBridgeSchemas records the schemas on the bridge, in order.
func SetBridgeSchemas(schemas []string) { bridgeSchemas = append([]string(nil), schemas...) }

// BridgeSchemas are the schemas on the bridge, in order.
func BridgeSchemas() []string { return append([]string(nil), bridgeSchemas...) }

// SearchPath is the search path of opnview's own connections and the read role: the core schema, the
// bridge, then public, where the TimescaleDB extension's functions are.
func SearchPath() string {
	return strings.Join(append(append([]string{CoreSchema}, bridgeSchemas...), "public"), ", ")
}

// CoreSearchPath is the search path without the bridge: what a plugin's own SQL and the plan check of a
// plugin's statement resolve names through.
func CoreSearchPath() string { return CoreSchema + ", public" }
