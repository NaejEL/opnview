package collecttest

import (
	"fmt"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
)

// The OPNsense endpoints the built-in plugins read, as their manifests declare them, for the tests
// that answer them from a fake firewall. They are read from the manifests and from nowhere else: the
// compiled registry holds only what discovery and the clock read (internal/opnsense, endpoints.go).
var (
	QueryAlerts          = Declared("suricata", "query_alerts")
	AlertLogs            = Declared("suricata", "alert_logs")
	IDSStatus            = Declared("suricata", "status")
	NetflowIsEnabled     = Declared("insight", "netflow_is_enabled")
	TrafficTop           = Declared("insight", "traffic_top")
	TrafficInterface     = Declared("insight", "traffic_interface")
	SystemResources      = Declared("insight", "system_resources")
	SystemTemperature    = Declared("insight", "system_temperature")
	SystemDisk           = Declared("insight", "system_disk")
	SystemSwap           = Declared("insight", "system_swap")
	GatewayStatus        = Declared("insight", "gateway_status")
	KeaLeases            = Declared("kea", "leases")
	KeaStatus            = Declared("kea", "status")
	KeaDHCPv4            = Declared("kea", "dhcpv4")
	DnsmasqLeases        = Declared("dnsmasq", "leases")
	DnsmasqStatus        = Declared("dnsmasq", "status")
	DnsmasqSettings      = Declared("dnsmasq", "settings")
	ISCStatus            = Declared("isc", "status")
	SearchQueries        = Declared("unbound", "search_queries")
	UnboundIsEnabled     = Declared("unbound", "is_enabled")
	UnboundStatus        = Declared("unbound", "status")
	UnboundSettings      = Declared("unbound", "settings")
	UnboundDumpCache     = Declared("unbound", "dump_cache")
	UnboundListLocalData = Declared("unbound", "list_local_data")
)

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
