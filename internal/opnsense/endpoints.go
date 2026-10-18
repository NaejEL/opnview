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
	// Stream marks an endpoint that answers with a server-sent event stream which
	// never ends of its own accord. Such an endpoint is read only through
	// Client.Stream, which stops after a bounded number of events or a bounded time;
	// Client.Call refuses it, because Call reads a body to its end and this body has
	// none. Survey, "Processor stream, read from source for the resolver-cache cycle".
	Stream bool
}

// The upstream citations. Each is a URL the survey's own tables or References
// section carries; they are grouped so one correction changes one place.
const (
	docsFirewall   = "https://docs.opnsense.org/development/api/core/firewall.html"
	docsInterfaces = "https://docs.opnsense.org/development/api/core/interfaces.html"

	srcFirewallLog = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/FirewallController.php"
	srcFilterRules = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php"
	srcInterfaces  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php"
	srcIfaceNames  = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php"
	srcSystem      = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php"
	// The processor stream: the controller, the configd action, its script and the ACL
	// page that admits the path.
	srcCPUUsage      = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/CpuUsageController.php"
	srcSystemActions = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/service/conf/actions.d/actions_system.conf"
	srcCPUScript     = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/system/cpu.py"
	srcCoreACL       = "https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Core/ACL/ACL.xml"
)

// The survey sections. "Verified" is the section measured against a live
// OPNsense 26.7.3_11 on 2026-09-27, which overrides the inferred text above it
// wherever the two disagree — it says so itself.
const (
	sectionSource1   = "Data source 1 — Filter logs"
	sectionDiscovery = "Runtime discovery"
	sectionVerified  = "Verified against a live firewall, 2026-09-27"
	sectionVersions  = "Firewall versions, read from source for A19 (broker amendment)"
	sectionCPU       = "Processor stream, read from source for the resolver-cache cycle"
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

	// SystemTime carries the uptime and the load averages.
	SystemTime = Endpoint{
		Path: "/api/diagnostics/system/systemTime", Method: http.MethodGet,
		SurveySection: sectionVerified, UpstreamURL: srcSystem,
		Note: "Uptime and load average. Field names not established by the survey.",
	}

	// SystemInformation carries the firewall's OPNsense version, read at every discovery pass so
	// a plugin's version ranges (A19 of the broker amendment) follow an upgrade without a restart.
	SystemInformation = Endpoint{
		Path: "/api/diagnostics/system/systemInformation", Method: http.MethodGet,
		SurveySection: sectionVersions, UpstreamURL: srcSystem,
		Note: "The first versions entry is '<product name> <version>-<arch>'.",
	}

	// CPUUsageStream is the processor figure, as a server-sent event stream that never
	// ends. streamAction (GET, no write) streams the configd action `system cpu stream`
	// with an interval of 1, which is scripts/system/cpu.py running `iostat -w 1 cpu` and
	// printing, per line, "event: message" and "data: {total, user, nice, sys, intr,
	// idle}" -- integer percentages, total being their sum less idle. iostat's first line
	// is the average since boot (iostat(8)), so the first event is not a current reading.
	// Admitted by the ACL page "Lobby: Dashboard" (page-system-login-logout, pattern
	// api/diagnostics/cpu_usage/*).
	CPUUsageStream = Endpoint{
		Path: "/api/diagnostics/cpu_usage/stream", Method: http.MethodGet, Stream: true,
		SurveySection: sectionCPU, UpstreamURL: srcCPUUsage,
		Note: "text/event-stream, one event about every second and no end; read only with a bound. " +
			"configd: " + srcSystemActions + " [cpu.stream]; script: " + srcCPUScript + "; ACL: " +
			srcCoreACL,
	}
)

// Registry is every endpoint opnview may call, and nothing else. Client refuses
// any Endpoint that is not in it, so a path invented in a collector cannot
// reach the transport.
func Registry() []Endpoint {
	return []Endpoint{
		InterfacesInfo, InterfaceNames, SearchRule, FirewallLog, SystemTime,
		SystemInformation, CPUUsageStream,
	}
}

// registered reports whether ep is a registry entry, or an endpoint a plugin declared and
// that was admitted at load (plugin_endpoints.go), compared on the fields that decide what
// reaches the firewall.
func registered(ep Endpoint) bool {
	if admitted(ep) {
		return true
	}
	for _, known := range Registry() {
		if known.Path == ep.Path && known.Method == ep.Method &&
			known.Encoding == ep.Encoding && known.Argument == ep.Argument &&
			known.Stream == ep.Stream {
			return true
		}
	}
	return false
}
