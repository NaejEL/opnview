package opnsense

import (
	"strings"
	"testing"
)

// The audit of every controller the endpoint registry reaches (B1 of
// specs/SPEC-step-5b-widget-api.md).
//
// ONE TEST PER CONTROLLER, each listing that controller's public actions at
// opnsense/core 26.7.3, read from the controller's source at the URL in its comment,
// together with the actions the controller inherits from the base class it extends.
// The two inherited sets are read once, here:
//
//   - ApiMutableServiceControllerBase.php declares startAction, stopAction,
//     restartAction, reconfigureAction and statusAction; the first four change the
//     service and the last reads it.
//     https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableServiceControllerBase.php
//   - ApiMutableModelControllerBase.php declares getAction, which reads the model,
//     and setAction, which writes it.
//     https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableModelControllerBase.php
//
// Each action is written as the URL command the router accepts: the action name
// without its "Action" suffix, in both its camelCase spelling and its snake_case one,
// since the OPNsense router maps either to the same method. Every mutating action
// must be refused in both spellings, with and without a positional parameter after
// it; every read-only one must not be; and every action the registry calls must be
// among the read-only ones.

// controllerAudit is one controller's actions at 26.7.3.
type controllerAudit struct {
	// module and controller are the two path elements after /api/.
	module, controller string
	// mutating and readOnly are the actions, camelCase, without "Action".
	mutating []string
	readOnly []string
}

var serviceBaseMutating = []string{"start", "stop", "restart", "reconfigure"}
var serviceBaseReadOnly = []string{"status"}

// toSnake turns an action's camelCase name into the snake_case spelling the router
// also accepts.
func toSnake(name string) string {
	var out strings.Builder
	for index, character := range name {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(character + ('a' - 'A'))
			continue
		}
		out.WriteRune(character)
	}
	return out.String()
}

// spellings returns both spellings of an action, without repeating one that has
// a single spelling.
func spellings(action string) []string {
	snake := toSnake(action)
	if snake == action {
		return []string{action}
	}
	return []string{action, snake}
}

// checkControllerAudit asserts one controller's audit against the guard and the
// registry.
func checkControllerAudit(t *testing.T, audit controllerAudit) {
	t.Helper()
	base := "/api/" + audit.module + "/" + audit.controller + "/"
	for _, action := range audit.mutating {
		for _, command := range spellings(action) {
			for _, path := range []string{base + command, base + command + "/argument"} {
				if MutatingCommand(path) == "" {
					t.Errorf("%s is a mutating action at 26.7.3 and was not refused", path)
				}
			}
		}
	}
	readOnly := map[string]bool{}
	for _, action := range audit.readOnly {
		for _, command := range spellings(action) {
			readOnly[command] = true
			if refused := MutatingCommand(base + command); refused != "" {
				t.Errorf("%s reads and was wrongly refused as %q", base+command, refused)
			}
		}
	}
	called := 0
	for _, endpoint := range Registry() {
		elements := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
		if len(elements) < 4 || elements[1] != audit.module || elements[2] != audit.controller {
			continue
		}
		called++
		if !readOnly[elements[3]] {
			t.Errorf("the registry calls %s, which is not among the read-only actions of %s/%s",
				endpoint.Path, audit.module, audit.controller)
		}
	}
	if called == 0 {
		t.Errorf("no registry endpoint reaches %s/%s, so this audit tests nothing the registry uses",
			audit.module, audit.controller)
	}
}

// TestEveryRegistryControllerIsAudited fails when the registry reaches a controller
// no test below lists, so a new endpoint cannot arrive without its controller read.
func TestEveryRegistryControllerIsAudited(t *testing.T) {
	audited := map[string]bool{}
	for _, audit := range controllerAudits() {
		audited[audit.module+"/"+audit.controller] = true
	}
	for _, endpoint := range Registry() {
		elements := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
		if len(elements) < 4 {
			continue
		}
		if key := elements[1] + "/" + elements[2]; !audited[key] {
			t.Errorf("the registry reaches %s (%s), whose actions no audit lists", key, endpoint.Path)
		}
	}
}

// controllerAudits is every audit below, for the coverage test.
func controllerAudits() []controllerAudit {
	return []controllerAudit{
		auditDiagnosticsCPUUsage, auditDiagnosticsFirewall, auditDiagnosticsInterface,
		auditDiagnosticsNetflow, auditDiagnosticsSystem, auditDiagnosticsTraffic,
		auditDnsmasqLeases, auditDnsmasqService, auditDnsmasqSettings, auditFirewallFilter,
		auditIDSService, auditIDSSettings, auditInterfacesOverview, auditKeaService,
		auditKeaLeases4, auditKeaDhcpv4, auditISCService, auditRoutesGateway, auditUnboundDiagnostics, auditUnboundOverview, auditUnboundService,
		auditUnboundSettings,
	}
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/CpuUsageController.php
var auditDiagnosticsCPUUsage = controllerAudit{
	module: "diagnostics", controller: "cpu_usage",
	readOnly: []string{"getCPUType", "stream"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/FirewallController.php
var auditDiagnosticsFirewall = controllerAudit{
	module: "diagnostics", controller: "firewall",
	mutating: []string{"delState", "killStates", "flushStates", "flushSources"},
	readOnly: []string{"log", "streamLog", "logFilters", "stats", "queryStates", "queryPfTop",
		"listRuleIds", "pfStatistics", "pfStates"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php
// carpStatusAction($status) runs `interface carp_set_status` on a POST, which changes
// the CARP status; the first reading of this controller missed it.
var auditDiagnosticsInterface = controllerAudit{
	module: "diagnostics", controller: "interface",
	mutating: []string{"flushArp", "delRoute", "carpStatus"},
	readOnly: []string{"getInterfaceNames", "getArp", "searchArp", "getNdp", "searchNdp",
		"getRoutes", "getProtocolStatistics", "getInterfaceStatistics", "getInterfaceConfig",
		"getVipStatus", "getPfsyncNodes", "getSocketStatistics", "getMemoryStatistics",
		"getBpfStatistics", "getNetisrStatistics"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php
var auditDiagnosticsNetflow = controllerAudit{
	module: "diagnostics", controller: "netflow",
	mutating: []string{"setconfig", "reconfigure", "reset"},
	readOnly: []string{"isEnabled", "getconfig", "status", "cacheStats"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php
var auditDiagnosticsSystem = controllerAudit{
	module: "diagnostics", controller: "system",
	readOnly: []string{"memory", "systemInformation", "systemTime", "systemResources",
		"systemDisk", "systemMbuf", "systemSwap", "systemTemperature"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/TrafficController.php
// The source spells two actions with a capital, InterfaceAction and TopAction; the
// registry calls them in lower case, which the router also maps to them.
var auditDiagnosticsTraffic = controllerAudit{
	module: "diagnostics", controller: "traffic",
	readOnly: []string{"interface", "stream", "top"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/LeasesController.php
var auditDnsmasqLeases = controllerAudit{
	module: "dnsmasq", controller: "leases",
	readOnly: []string{"search"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php
// declares no action of its own: it extends ApiMutableServiceControllerBase.
var auditDnsmasqService = controllerAudit{
	module: "dnsmasq", controller: "service",
	mutating: serviceBaseMutating, readOnly: serviceBaseReadOnly,
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/SettingsController.php
// extends ApiMutableModelControllerBase; its getAction override only adds read data
// to the answer.
var auditDnsmasqSettings = controllerAudit{
	module: "dnsmasq", controller: "settings",
	mutating: []string{"set", "setHost", "addHost", "delHost", "uploadHosts",
		"setDomain", "addDomain", "delDomain", "setTag", "addTag", "delTag",
		"setRange", "addRange", "delRange", "setOption", "addOption", "delOption",
		"setBoot", "addBoot", "delBoot"},
	readOnly: []string{"get", "searchHost", "getHost", "downloadHosts", "searchDomain",
		"getDomain", "searchTag", "getTag", "searchRange", "getRange", "searchOption",
		"getOption", "searchBoot", "getBoot", "getTagList"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php
// extends FilterBaseController, which adds listCategories, listNetworkSelectOptions,
// listPortSelectOptions and apply (a filter reload):
// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterBaseController.php
var auditFirewallFilter = controllerAudit{
	module: "firewall", controller: "filter",
	mutating: []string{"setRule", "addRule", "delRule", "toggleRule", "toggleRuleLog",
		"moveRuleBefore", "uploadRules", "flushInspectCache", "apply", "set"},
	readOnly: []string{"searchRule", "getRule", "getInterfaceList", "downloadRules",
		"listCategories", "listNetworkSelectOptions", "listPortSelectOptions", "get"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php
var auditIDSService = controllerAudit{
	module: "ids", controller: "service",
	mutating: append([]string{"updateRules", "reloadRules", "dropAlertLog"}, serviceBaseMutating...),
	readOnly: append([]string{"queryAlerts", "getAlertInfo", "getAlertLogs"}, serviceBaseReadOnly...),
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/SettingsController.php
var auditIDSSettings = controllerAudit{
	module: "ids", controller: "settings",
	mutating: []string{"set", "setRulesetproperties", "setRuleset", "toggleRuleset",
		"toggleRule", "setRule", "setUserRule", "addUserRule", "delUserRule",
		"toggleUserRule", "setPolicy", "addPolicy", "delPolicy", "togglePolicy",
		"setPolicyRule", "addPolicyRule", "delPolicyRule", "togglePolicyRule"},
	readOnly: []string{"get", "searchInstalledRules", "getRuleInfo", "listRuleMetadata",
		"getRulesetproperties", "listRulesets", "getRuleset", "searchUserRule", "getUserRule",
		"searchPolicy", "getPolicy", "searchPolicyRule", "getPolicyRule", "checkPolicyRule"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php
var auditInterfacesOverview = controllerAudit{
	module: "interfaces", controller: "overview",
	mutating: []string{"reloadInterface"},
	readOnly: []string{"interfacesInfo", "getInterface", "export"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/ServiceController.php
// declares no action of its own: it extends ApiMutableServiceControllerBase.
var auditKeaService = controllerAudit{
	module: "kea", controller: "service",
	mutating: serviceBaseMutating, readOnly: serviceBaseReadOnly,
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Leases4Controller.php
// declares no action of its own: it extends LeasesController, which declares
// searchAction and delLeaseAction (`kea delete lease`):
// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/LeasesController.php
var auditKeaLeases4 = controllerAudit{
	module: "kea", controller: "leases4",
	mutating: []string{"delLease"},
	readOnly: []string{"search"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Dhcpv4Controller.php
var auditKeaDhcpv4 = controllerAudit{
	module: "kea", controller: "dhcpv4",
	mutating: []string{"set", "setSubnet", "addSubnet", "delSubnet", "setReservation",
		"addReservation", "delReservation", "uploadReservations", "setOption", "addOption",
		"delOption", "setPeer", "addPeer", "delPeer"},
	readOnly: []string{"get", "searchSubnet", "getSubnet", "searchReservation",
		"getReservation", "downloadReservations", "searchOption", "getOption", "searchPeer",
		"getPeer"},
}

// The end-of-life ISC DHCP probe, /api/dhcpv4/service/status. opnsense/core 26.7.3
// HAS NO DHCPv4 CONTROLLER DIRECTORY
// (https://github.com/opnsense/core/tree/26.7.3/src/opnsense/mvc/app/controllers/OPNsense):
// the controller left core with ISC, and the registry row exists so that its 404
// is recorded as the plugin's absence. UNVERIFIED: that the plugin's controller
// extends ApiMutableServiceControllerBase. Nothing is built on that: the audit
// below asserts only that the service base's verbs are refused and that `status`,
// the one command the registry sends, is not.
var auditISCService = controllerAudit{
	module: "dhcpv4", controller: "service",
	mutating: serviceBaseMutating, readOnly: serviceBaseReadOnly,
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Routes/Api/GatewayController.php
var auditRoutesGateway = controllerAudit{
	module: "routes", controller: "gateway",
	readOnly: []string{"status"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/DiagnosticsController.php
var auditUnboundDiagnostics = controllerAudit{
	module: "unbound", controller: "diagnostics",
	readOnly: []string{"stats", "dumpcache", "dumpinfra", "listlocaldata", "listlocalzones",
		"listinsecure", "testBlocklist"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/OverviewController.php
// resetAction runs `unbound qstats reset`, which clears the statistics the query
// report reads.
var auditUnboundOverview = controllerAudit{
	module: "unbound", controller: "overview",
	mutating: []string{"reset"},
	readOnly: []string{"isEnabled", "isBlockListEnabled", "rolling", "totals",
		"searchQueries", "getPolicies"},
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/ServiceController.php
var auditUnboundService = controllerAudit{
	module: "unbound", controller: "service",
	mutating: append([]string{"dnsbl", "reconfigureGeneral"}, serviceBaseMutating...),
	readOnly: serviceBaseReadOnly,
}

// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/SettingsController.php
var auditUnboundSettings = controllerAudit{
	module: "unbound", controller: "settings",
	mutating: []string{"set", "updateBlocklist", "addForward", "delForward", "setForward",
		"toggleForward", "addHostOverride", "delHostOverride", "setHostOverride",
		"toggleHostOverride", "addHostAlias", "delHostAlias", "setHostAlias", "toggleHostAlias",
		"addAcl", "delAcl", "setAcl", "toggleAcl", "addDnsbl", "delDnsbl", "setDnsbl",
		"toggleDnsbl"},
	readOnly: []string{"get", "getNameservers", "searchForward", "getForward",
		"searchHostOverride", "getHostOverride", "searchHostAlias", "getHostAlias",
		"searchAcl", "getAcl", "searchDnsbl", "getDnsbl"},
}

func TestMutatingCommandAuditDiagnosticsCPUUsage(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsCPUUsage)
}

func TestMutatingCommandAuditDiagnosticsFirewall(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsFirewall)
}

func TestMutatingCommandAuditDiagnosticsInterface(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsInterface)
}

func TestMutatingCommandAuditDiagnosticsNetflow(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsNetflow)
}

func TestMutatingCommandAuditDiagnosticsSystem(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsSystem)
}

func TestMutatingCommandAuditDiagnosticsTraffic(t *testing.T) {
	checkControllerAudit(t, auditDiagnosticsTraffic)
}

func TestMutatingCommandAuditDnsmasqLeases(t *testing.T) {
	checkControllerAudit(t, auditDnsmasqLeases)
}

func TestMutatingCommandAuditDnsmasqService(t *testing.T) {
	checkControllerAudit(t, auditDnsmasqService)
}

func TestMutatingCommandAuditDnsmasqSettings(t *testing.T) {
	checkControllerAudit(t, auditDnsmasqSettings)
}

func TestMutatingCommandAuditFirewallFilter(t *testing.T) {
	checkControllerAudit(t, auditFirewallFilter)
}

func TestMutatingCommandAuditIDSService(t *testing.T) {
	checkControllerAudit(t, auditIDSService)
}

func TestMutatingCommandAuditIDSSettings(t *testing.T) {
	checkControllerAudit(t, auditIDSSettings)
}

func TestMutatingCommandAuditInterfacesOverview(t *testing.T) {
	checkControllerAudit(t, auditInterfacesOverview)
}

func TestMutatingCommandAuditKeaService(t *testing.T) {
	checkControllerAudit(t, auditKeaService)
}

func TestMutatingCommandAuditKeaLeases4(t *testing.T) {
	checkControllerAudit(t, auditKeaLeases4)
}

func TestMutatingCommandAuditKeaDhcpv4(t *testing.T) {
	checkControllerAudit(t, auditKeaDhcpv4)
}

func TestMutatingCommandAuditISCService(t *testing.T) {
	checkControllerAudit(t, auditISCService)
}

func TestMutatingCommandAuditRoutesGateway(t *testing.T) {
	checkControllerAudit(t, auditRoutesGateway)
}

func TestMutatingCommandAuditUnboundDiagnostics(t *testing.T) {
	checkControllerAudit(t, auditUnboundDiagnostics)
}

func TestMutatingCommandAuditUnboundOverview(t *testing.T) {
	checkControllerAudit(t, auditUnboundOverview)
}

func TestMutatingCommandAuditUnboundService(t *testing.T) {
	checkControllerAudit(t, auditUnboundService)
}

func TestMutatingCommandAuditUnboundSettings(t *testing.T) {
	checkControllerAudit(t, auditUnboundSettings)
}

// TestMutatingCommandRefusesByVerbPrefix is AC21's named list: every verb, prefixed
// in both spellings, and `reset` exactly and prefixed, each also with a positional
// parameter.
func TestMutatingCommandRefusesByVerbPrefix(t *testing.T) {
	for _, command := range []string{"add_x", "addX", "set_x", "setX", "del_x", "delX",
		"toggle_x", "toggleX", "update_x", "updateX", "reset", "reset_x", "resetX"} {
		for _, path := range []string{"/api/example/controller/" + command,
			"/api/example/controller/" + command + "/argument"} {
			if MutatingCommand(path) == "" {
				t.Errorf("%s was not refused", path)
			}
		}
	}
}

// TestMutatingCommandSparesReadOnlyNamesSharingLetters is B1's list of read-only
// names that merely begin with a mutating verb's letters.
func TestMutatingCommandSparesReadOnlyNamesSharingLetters(t *testing.T) {
	for _, command := range []string{"settings", "address", "addresses", "delay",
		"delay_seconds", "search", "search_rule", "get", "get_arp", "status", "dumpcache",
		"listlocaldata", "searchLease", "get_c_p_u_type", "getCPUType", "setting",
		"toggled", "updated", "resetting", "settle"} {
		if refused := MutatingCommand("/api/example/controller/" + command); refused != "" {
			t.Errorf("%s reads and was wrongly refused as %q", command, refused)
		}
	}
}
