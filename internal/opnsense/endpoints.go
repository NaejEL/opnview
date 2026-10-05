// Package opnsense is the only package in opnview that constructs an HTTP
// request.
//
// Three rules are enforced here rather than remembered:
//
//  1. Every path opnview may call is a registry entry, each carrying the
//     section of docs/opnsense-api-survey.md and the upstream URL that
//     establishes it. A path that is not in the registry cannot be issued, so
//     an invented endpoint cannot compile and pass.
//  2. No mutating command is ever issued. The refusal happens before a request
//     is built, so the transport never sees one.
//  3. The firewall is the only host reached. There is exactly one HTTP client
//     and one base URL, and nothing else in the program builds a request.
package opnsense

import "net/http"

// BodyEncoding says how a request body reaches the firewall.
//
// It is load-bearing on exactly one endpoint and that is why it is modelled.
// OPNsense's Mvc\Request::get() reads $_REQUEST uncast, so a value from a query
// string or a form-encoded body is always a PHP string; a native integer
// reaches the controller only through parseJsonBodyData(), which merges a
// decoded Content-Type: application/json body. /api/unbound/overview/
// search_queries keeps timeStart and timeEnd only if is_int() holds, so any
// other encoding silently degrades to the unwindowed branch with HTTP 200 and
// no diagnostic. See docs/opnsense-api-survey.md, "Parameter types depend on
// the body encoding", and data source 5.
type BodyEncoding int

// The three encodings, and no fourth.
const (
	// NoBody is a GET with positional path arguments and nothing else.
	NoBody BodyEncoding = iota
	// FormBody is an application/x-www-form-urlencoded grid search. Every
	// parameter arrives as a string, which is harmless for a grid search.
	FormBody
	// JSONBody is an application/json body, the only encoding that delivers a
	// native integer.
	JSONBody
)

// ArgumentKind is the kind of value an endpoint's positional path arguments
// carry.
//
// It exists because of a measured trap. /api/diagnostics/traffic/top/<names>
// takes interface NAMES — the configuration keys, `lan`, `opt1` — and a device
// name (`igb0`, `vlan01`) returns an empty array with HTTP 200, which is
// indistinguishable from an absence of traffic. Survey, "Verified against a
// live firewall, 2026-09-27", "Wrong paths". Modelling the kind is what lets
// the sampler refuse the wrong argument instead of recording silence.
type ArgumentKind int

// The argument kinds.
const (
	// NoArgument means the path takes no positional argument.
	NoArgument ArgumentKind = iota
	// InterfaceNameArgument means the path takes interface identifiers, the
	// configuration keys — never network device names.
	InterfaceNameArgument
)

// Endpoint is one path opnview is allowed to call.
type Endpoint struct {
	// Path is the path, with no positional argument appended.
	Path string
	// Method is the HTTP method opnview uses. A POST does not imply a write:
	// OPNsense has no framework-level method routing and its search and get
	// helpers contain no write path at all. Every POST below is a read-only
	// search or query call, tabulated as such in the survey under "GET versus
	// POST, and the read-only guarantee".
	Method string
	// Encoding is how the request body is encoded, if there is one.
	Encoding BodyEncoding
	// Argument is the kind of positional path argument the endpoint takes.
	Argument ArgumentKind
	// SurveySection names the section of docs/opnsense-api-survey.md that
	// establishes this endpoint.
	SurveySection string
	// UpstreamURL is the documentation or source URL the survey cites for it.
	UpstreamURL string
	// Note records what the survey says about this endpoint that a caller has
	// to know and cannot read off the path.
	Note string
}

// The upstream citations. Each is a URL the survey's own tables or References
// section carries; they are grouped so one correction changes one place.
const (
	docsDiagnostics = "https://docs.opnsense.org/development/api/core/diagnostics.html"
	docsFirewall    = "https://docs.opnsense.org/development/api/core/firewall.html"
	docsInterfaces  = "https://docs.opnsense.org/development/api/core/interfaces.html"
	docsIDS         = "https://docs.opnsense.org/development/api/core/ids.html"
	docsUnbound     = "https://docs.opnsense.org/development/api/core/unbound.html"
	docsDHCP        = "https://docs.opnsense.org/manual/dhcp.html"
	docsKea         = "https://docs.opnsense.org/manual/kea.html"
	docsDnsmasq     = "https://docs.opnsense.org/manual/dnsmasq.html"

	srcFirewallLog  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/FirewallController.php"
	srcFilterRules  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php"
	srcInterfaces   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php"
	srcIfaceNames   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php"
	srcIDSService   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php"
	srcIDSSettings  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/SettingsController.php"
	srcAlertLogs    = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/suricata/listAlertLogs.py"
	srcNetflow      = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php"
	srcKeaLeases    = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Leases4Controller.php"
	srcKeaService   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/ServiceController.php"
	srcKeaDHCPv4    = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Dhcpv4Controller.php"
	srcDnsmasqLease = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/LeasesController.php"
	srcDnsmasqSvc   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php"
	srcDnsmasqModel = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml"
	srcUnboundOver  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/OverviewController.php"
	srcUnboundSvc   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/ServiceController.php"
	srcUnboundModel = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Unbound.xml"
	srcGateways     = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Routes/Api/GatewayController.php"
	docsRoutes      = "https://docs.opnsense.org/development/api/core/routes.html"
	srcSystemSwap   = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php"
	srcSwapInfo     = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/system/swapinfo.py"
)

// The survey sections. "Verified" is the section measured against a live
// OPNsense 26.7.3_11 on 2026-09-27, which overrides the inferred text above it
// wherever the two disagree — it says so itself.
const (
	sectionSource1   = "Data source 1 — Filter logs"
	sectionSource2   = "Data source 2 — Suricata `eve.json`"
	sectionSource3   = "Data source 3 — NetFlow / Insight"
	sectionSource4   = "Data source 4 — DHCP leases"
	sectionSource5   = "Data source 5 — Resolver DNS lookups"
	sectionDiscovery = "Runtime discovery"
	sectionVerified  = "Verified against a live firewall, 2026-09-27"
	sectionGateway   = "Gateway status, read from source for step 5"
	sectionSwap      = "Swap, read from source for step 5"
)

// The registry. Every entry is reachable only through these variables, so a
// caller cannot name a path that is not here.
var (
	// InterfacesInfo is runtime discovery (i): the interfaces, with the
	// user-given description, the network device, the link state, the
	// administrative state, the raw link type and the VLAN tag.
	InterfacesInfo = Endpoint{
		Path: "/api/interfaces/overview/interfaces_info", Method: http.MethodGet,
		SurveySection: sectionDiscovery, UpstreamURL: docsInterfaces,
		Note: "Returns the standard search envelope. `description` falls back to the upper-cased identifier. See also " + srcInterfaces,
	}
	// InterfaceNames is the first of the two first-class join keys: the raw
	// device name the filter log reports, to the user-given description.
	InterfaceNames = Endpoint{
		Path: "/api/diagnostics/interface/get_interface_names", Method: http.MethodGet,
		SurveySection: sectionDiscovery, UpstreamURL: srcIfaceNames,
		Note: "A flat map from device name to description.",
	}
	// SearchRule is runtime discovery (ii): the second join key. For legacy and
	// automatic rules `uuid` carries the pf label, the token the filter log
	// reports as `rid`, and the %-prefixed twins carry the raw values machine
	// logic must read because the human-facing fields are localised.
	SearchRule = Endpoint{
		Path: "/api/firewall/filter/search_rule", Method: http.MethodPost, Encoding: FormBody,
		SurveySection: sectionDiscovery, UpstreamURL: docsFirewall,
		Note: "Read-only grid search; rowCount -1 retrieves everything. See also " + srcFilterRules,
	}

	// FirewallLog is data source 1. It returns a JSON array, newest first, not
	// a paginated envelope, and `digest` is NOT a server-side cursor: passing it
	// returned byte-identical output twice on a live firewall.
	FirewallLog = Endpoint{
		Path: "/api/diagnostics/firewall/log", Method: http.MethodGet,
		SurveySection: sectionSource1, UpstreamURL: srcFirewallLog,
		Note: "limit defaults to 1000; limit=0 is coerced back to 1000. An empty array does not distinguish disabled logging from no matching traffic.",
	}

	// QueryAlerts is data source 2, the only path that opens
	// /var/log/suricata/eve.json. It returns a record only if it carries a
	// top-level `alert` key, and the nested alert object is overwritten with the
	// signature text before opnview sees it.
	QueryAlerts = Endpoint{
		Path: "/api/ids/service/query_alerts", Method: http.MethodPost, Encoding: FormBody,
		SurveySection: sectionSource2, UpstreamURL: srcIDSService,
		Note: "Read-only. Paging is offset-from-end-of-file and therefore unstable; the durable cursor is (fileid, filepos).",
	}
	// AlertLogs enumerates the rotated eve.json files. A watermark whose file no
	// longer appears here is a permanent loss.
	AlertLogs = Endpoint{
		Path: "/api/ids/service/get_alert_logs", Method: http.MethodGet,
		SurveySection: sectionSource2, UpstreamURL: srcAlertLogs,
		Note: "One entry per rotated file with filename, size, modified and sequence.",
	}
	// IDSStatus separates Suricata absent from installed-but-stopped from
	// running. 404, 401 or 403 means absent or not permitted.
	IDSStatus = Endpoint{
		Path: "/api/ids/service/status", Method: http.MethodGet,
		SurveySection: sectionSource2, UpstreamURL: docsIDS,
		Note: "status of running / stopped / disabled / unknown.",
	}
	// IDSSettings carries the configured state and the covered interfaces.
	IDSSettings = Endpoint{
		Path: "/api/ids/settings/get", Method: http.MethodGet,
		SurveySection: sectionSource2, UpstreamURL: srcIDSSettings,
		Note: "ids.general.enabled is the configured state, independent of the running state.",
	}

	// NetflowIsEnabled is data source 3's first degradation probe. Insight data
	// exists only when `local` is 1: a firewall exporting to an external
	// collector reports netflow 1, local 0 and has no Insight data at all.
	NetflowIsEnabled = Endpoint{
		Path: "/api/diagnostics/netflow/is_enabled", Method: http.MethodGet,
		SurveySection: sectionSource3, UpstreamURL: srcNetflow,
		Note: "Returns {netflow: 0|1, local: 0|1}.",
	}
	// NetflowStatus is the companion probe. The survey lists the endpoint and
	// gives no response shape, so nothing is read from its body beyond whether
	// the call succeeded.
	NetflowStatus = Endpoint{
		Path: "/api/diagnostics/netflow/status", Method: http.MethodGet,
		SurveySection: sectionSource3, UpstreamURL: srcNetflow,
		Note: "The survey establishes the endpoint and not its response shape.",
	}
	// TrafficTop is the measured replacement for the per-pair aggregate the
	// survey's inferred text expected. /api/diagnostics/netflow/top,
	// netflow/get_metadata and netflow/aggregate all answered 404 on a live
	// 26.7.3_11; the per-pair data is here, and it is a LIVE RATE SNAPSHOT
	// rather than history.
	//
	// IT TAKES INTERFACE NAMES. A device name returns an empty array with
	// HTTP 200, which is indistinguishable from an absence of traffic — hence
	// the InterfaceNameArgument kind declared here, and the guard that acts on
	// it, which is guardInterfaceNames in internal/collect/flowvolume_insight.go:
	// it compares the names about to be sent against the device set discovery
	// read from the firewall, and refuses rather than sampling.
	TrafficTop = Endpoint{
		Path: "/api/diagnostics/traffic/top", Method: http.MethodGet, Argument: InterfaceNameArgument,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Per interface, a records[] of local addresses with rate_bits_in/out, cumulative_bytes_in/out and a details[] of peers. No port and no protocol.",
	}
	// TrafficInterface carries the per-interface packet and byte counters,
	// errors, link state and line rate.
	TrafficInterface = Endpoint{
		Path: "/api/diagnostics/traffic/interface", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Per-interface counters. The survey establishes the endpoint and not its field names.",
	}

	// KeaLeases is one of the two current lease backends.
	KeaLeases = Endpoint{
		Path: "/api/kea/leases4/search", Method: http.MethodPost, Encoding: FormBody,
		SurveySection: sectionSource4, UpstreamURL: srcKeaLeases,
		Note: "Read-only grid search against the running Kea daemon through its control agent. If that agent is off the list is empty although DHCP works.",
	}
	// KeaStatus probes the Kea service.
	KeaStatus = Endpoint{
		Path: "/api/kea/service/status", Method: http.MethodGet,
		SurveySection: sectionSource4, UpstreamURL: srcKeaService,
		Note: "status field.",
	}
	// KeaDHCPv4 confirms the configured state behind the service status.
	KeaDHCPv4 = Endpoint{
		Path: "/api/kea/dhcpv4/get", Method: http.MethodGet,
		SurveySection: sectionSource4, UpstreamURL: srcKeaDHCPv4,
		Note: "dhcpv4.general.enabled.",
	}
	// DnsmasqLeases is the other current lease backend, and it returns
	// structured JSON. The survey's inferred text claimed Dnsmasq needed a
	// free-text parser; that is wrong for leases, measured on a live firewall.
	DnsmasqLeases = Endpoint{
		Path: "/api/dnsmasq/leases/search", Method: http.MethodPost, Encoding: FormBody,
		SurveySection: sectionVerified, UpstreamURL: srcDnsmasqLease,
		Note: "Rows carry hwaddr, address, hostname, client_id, expire, is_reserved, mac_info and the interface under all three of its names. No parser is needed.",
	}
	// DnsmasqStatus probes the Dnsmasq service, which serves both DHCP and DNS.
	DnsmasqStatus = Endpoint{
		Path: "/api/dnsmasq/service/status", Method: http.MethodGet,
		SurveySection: sectionSource4, UpstreamURL: srcDnsmasqSvc,
		Note: "status field.",
	}
	// DnsmasqSettings carries the enable flag, the DHCP ranges and the
	// query-logging flag. opnview reads them and never writes them.
	DnsmasqSettings = Endpoint{
		Path: "/api/dnsmasq/settings/get", Method: http.MethodGet,
		SurveySection: sectionSource4, UpstreamURL: srcDnsmasqModel,
		Note: "dnsmasq.enable (not `enabled`), dnsmasq.dhcp_ranges, dnsmasq.log_queries. See also " + docsDnsmasq,
	}
	// ISCStatus is the presence probe for the end-of-life ISC plugin. A 404 is
	// the expected answer on a 26.7 installation and means the plugin is absent,
	// which is a state and not a fault.
	ISCStatus = Endpoint{
		Path: "/api/dhcpv4/service/status", Method: http.MethodGet,
		SurveySection: sectionSource4, UpstreamURL: docsDHCP,
		Note: "404 means the end-of-life plugin is not installed. /api/dhcpv4/leases/searchLease answered 404 on a live 26.7.3_11, so no lease read is attempted. See also " + docsKea,
	}
	// ARPTable is a client-identity source the survey's inferred text did not
	// name. On a firewall whose DHCP is unreadable it is the only identity there
	// is.
	ARPTable = Endpoint{
		Path: "/api/diagnostics/interface/get_arp", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: srcIfaceNames,
		Note: "One row per neighbour with mac, ip, intf, intf_description, manufacturer, expired, permanent.",
	}
	// NDPTable is the IPv6 equivalent of ARPTable.
	NDPTable = Endpoint{
		Path: "/api/diagnostics/interface/get_ndp", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: srcIfaceNames,
		Note: "The IPv6 neighbour table, same shape as get_arp.",
	}

	// SearchQueries is data source 5, and the one endpoint whose CALL FORM is
	// load-bearing: timeStart and timeEnd are kept only if is_int() holds, which
	// they can be only in a JSON body.
	//
	// And the window is not honoured at all. Measured: a 5-minute and a 24-hour
	// request both returned the same ~410-second span, `total` is 1000 whatever
	// is asked, and pages walk further back. It is a ring buffer of the last
	// 1000 lookups. `uuid` is null on every row.
	SearchQueries = Endpoint{
		Path: "/api/unbound/overview/search_queries", Method: http.MethodPost, Encoding: JSONBody,
		SurveySection: sectionVerified, UpstreamURL: srcUnboundOver,
		Note: "Read-only. JSON body with integer timeStart/timeEnd is the correct form and is confirmed; the window is nevertheless ignored, so what comes back is never coverage of the window asked for.",
	}
	// UnboundIsEnabled says whether query reporting is on, which is what
	// separates "reporting is switched off" from "the resolver is down".
	UnboundIsEnabled = Endpoint{
		Path: "/api/unbound/overview/is_enabled", Method: http.MethodGet,
		SurveySection: sectionSource5, UpstreamURL: docsUnbound,
		Note: "enabled field.",
	}
	// UnboundStatus probes the Unbound service.
	UnboundStatus = Endpoint{
		Path: "/api/unbound/service/status", Method: http.MethodGet,
		SurveySection: sectionSource5, UpstreamURL: srcUnboundSvc,
		Note: "status field.",
	}
	// UnboundSettings confirms the configured state behind the service status.
	UnboundSettings = Endpoint{
		Path: "/api/unbound/settings/get", Method: http.MethodGet,
		SurveySection: sectionSource5, UpstreamURL: srcUnboundModel,
		Note: "unbound.general.enabled.",
	}

	// SystemResources, SystemTemperature, SystemTime, SystemDisk and Activity
	// are the telemetry the data model recorded as gaps G9 and G10. The survey
	// establishes that all five answer on a live firewall and does NOT establish
	// their field names, which is why internal/collect extracts from them
	// tolerantly and marks the key names UNVERIFIED.
	SystemResources = Endpoint{
		Path: "/api/diagnostics/system/systemResources", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Memory figures. Field names not established by the survey.",
	}
	// SystemTemperature carries the per-sensor temperatures.
	SystemTemperature = Endpoint{
		Path: "/api/diagnostics/system/systemTemperature", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Per-sensor temperatures. Absent on hardware with no sensor, which is a state and not a zero.",
	}
	// SystemTime carries the uptime and the load averages.
	SystemTime = Endpoint{
		Path: "/api/diagnostics/system/systemTime", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Uptime and load average. Field names not established by the survey.",
	}
	// SystemDisk carries the filesystem usage.
	SystemDisk = Endpoint{
		Path: "/api/diagnostics/system/systemDisk", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Filesystem usage. Field names not established by the survey.",
	}
	// Activity is the process activity view, which is where a CPU figure comes
	// from.
	Activity = Endpoint{
		Path: "/api/diagnostics/activity/getActivity", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: docsDiagnostics,
		Note: "Process activity. Field names not established by the survey.",
	}

	// GatewayStatus is the first of the two firewall endpoints step 5 adds: each gateway's
	// round-trip time and packet loss, as dpinger measures them. Read from the
	// 26.7.3 source and not yet probed against a live firewall.
	GatewayStatus = Endpoint{
		Path: "/api/routes/gateway/status", Method: http.MethodGet,
		SurveySection: sectionGateway, UpstreamURL: srcGateways,
		Note: "Returns {items: [...], status: ok | failed}; each item carries name, address, status, " +
			"loss (\"0.0 %\"), delay and stddev (\"1.2 ms\") and monitor, or \"~\" where dpinger has no " +
			"figure. Listed in " + docsRoutes,
	}

	// SystemSwap is the second firewall endpoint step 5 adds, by the amendment of
	// 4 October 2026: each swap device's size and use. systemSwapAction returns the
	// configd action `system show swapinfo` unchanged, which runs swapinfo.py over
	// `swapinfo -k`. Read from the 26.7.3 source and not yet probed against a live
	// firewall.
	SystemSwap = Endpoint{
		Path: "/api/diagnostics/system/systemSwap", Method: http.MethodGet,
		SurveySection: sectionSwap, UpstreamURL: srcSystemSwap,
		Note: "Returns {swap: [{device, total, used}]}, total and used as strings in KiB, one " +
			"entry per /dev/ line of swapinfo -k and no Total line; {swap: []} with no swap " +
			"device. Script: " + srcSwapInfo + ". Listed in " + docsDiagnostics,
	}
)

// Registry is every endpoint opnview may call, and nothing else. Client refuses
// any Endpoint that is not in it, so a path invented in a collector cannot
// reach the transport.
func Registry() []Endpoint {
	return []Endpoint{
		InterfacesInfo, InterfaceNames, SearchRule,
		FirewallLog,
		QueryAlerts, AlertLogs, IDSStatus, IDSSettings,
		NetflowIsEnabled, NetflowStatus, TrafficTop, TrafficInterface,
		KeaLeases, KeaStatus, KeaDHCPv4,
		DnsmasqLeases, DnsmasqStatus, DnsmasqSettings,
		ISCStatus, ARPTable, NDPTable,
		SearchQueries, UnboundIsEnabled, UnboundStatus, UnboundSettings,
		SystemResources, SystemTemperature, SystemTime, SystemDisk, Activity,
		GatewayStatus, SystemSwap,
	}
}

// registered reports whether ep is a registry entry, compared on the fields
// that decide what reaches the firewall.
func registered(ep Endpoint) bool {
	for _, known := range Registry() {
		if known.Path == ep.Path && known.Method == ep.Method &&
			known.Encoding == ep.Encoding && known.Argument == ep.Argument {
			return true
		}
	}
	return false
}
