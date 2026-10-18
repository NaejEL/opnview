package collecttest

import (
	"fmt"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
)

// The OPNsense endpoints the built-in plugins read are read from their manifests and from nowhere
// else: the compiled registry holds only what discovery and the clock read (internal/opnsense,
// endpoints.go), and a test names a plugin's endpoint as the manifest does, Declared("<plugin>",
// "<endpoint>").

// Declared is the endpoint a built-in plugin's manifest declares under a name. A name no built-in
// declares is a mistake in a test, and stops it at once.
func Declared(pluginName, name string) opnsense.Endpoint {
	loaded, found := plugin.BuiltinsLoaded().Plugin(pluginName)
	if !found {
		panic(fmt.Sprintf("collecttest: no built-in plugin is named %q", pluginName))
	}
	endpoint, declared := loaded.Endpoints[name]
	if !declared {
		panic(fmt.Sprintf("collecttest: the plugin %s declares no endpoint %q", pluginName, name))
	}
	return endpoint
}
