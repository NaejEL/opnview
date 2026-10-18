// OPNsense's NetFlow export: the netflow plugin's flow_record kind, its capture configuration, and the join of
// its records to the logged connections, counted as the measure netflow.
//
// WHAT IT READS, AND WHERE IT IS ESTABLISHED (docs/opnsense-api-survey.md, "Push sources", S5, S5c, S6,
// S6a, S6b, S6c, S9, S10, S11, S12).
//   - The flow records themselves are not read here: the firewall SENDS them, NetFlow v9 over UDP, and the
//     push entry of plugin.json maps each record's IANA Information Elements to the columns of the table
//     flow_record with no script at all, beside the delivery the record came in.
//   - The capture configuration is read here, through the broker, from two read-only actions of
//     opnsense/core 26.7.3's Diagnostics/Api/NetflowController.php
//     (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php):
//       GET /api/diagnostics/netflow/getconfig  getconfigAction answers, on a GET,
//           {netflow: <the OPNsense.Netflow model's getNodes()>}. Of it this script reads
//           capture.interfaces and capture.egress_only -- InterfaceFields with Multiple, each an
//           object of {value, selected} keyed by interface identifier, `selected` 1 on the chosen
//           ones (BaseListField::getNodeOptions) -- capture.version, an OptionField of the same
//           shape (v5, v9), and activeTimeout and inactiveTimeout, IntegerFields answered as text.
//           The model is https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Diagnostics/Netflow.xml.
//       GET /api/diagnostics/netflow/is_enabled  isEnabledAction answers {netflow, local}: netflow is
//           1 exactly when capture.targets and capture.interfaces are both non-empty.
//   - Which captured interface is actually exported, which interfaces' packets another one's node exports
//     again, and what interface each kernel index the exporter writes names, are read from one read-only
//     action of opnsense/core 26.7.3's Interfaces/Api/OverviewController.php
//     (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php):
//       GET /api/interfaces/overview/interfaces_info/1  interfacesInfoAction answers the standard
//           search envelope; each row is a device's ifconfig details as legacy_interfaces_details
//           parses them (https://github.com/opnsense/core/blob/26.7.3/src/etc/inc/interfaces.lib.inc),
//           with its `identifier` (empty for a device no assignment names). Of a row this script reads
//           `identifier`, `device`, `macaddr` -- the address of ifconfig's `ether` line, all zeros
//           when there is none -- `vlan.parent`, the "parent interface" of ifconfig's vlan line,
//           `statistics.index`, the kernel interface index ng_netflow exports as ingressInterface and
//           egressInterface (the details argument adds `statistics`, ifinfo's output per device:
//           parseIfInfo, legacy_interface_stats), and `flags`, the interface flags ifconfig prints,
//           whose `loopback` (IFF_LOOPBACK) names the loopback interface.
//     None of the three writes anything. setconfig, reconfigure and reset, beside them, are never called.
//
// HOW THE CAPTURE IS MEANT. /etc/rc.d/netflow (opnsense/core 26.7.3, src/etc/rc.d/netflow) creates
// one ng_netflow node per interface of capture.interfaces, and none for an interface listed only in
// capture.egress_only; a node is configured `conf=11` (ingress and egress, accounted once per node) or
// `conf=10` (egress only) when the interface is also in egress_only. So an interface of
// capture.interfaces is captured on egress always, and on ingress unless it is in egress_only.
//
// EVERY INTERFACE MAY BE CAPTURED (decision 15d of specs/SPEC-backend-completion-plugins.md). Each record is
// joined from its own two interface indexes (NJ4, the analysis's "Rule B"); no Observation Point is assigned,
// OPNsense exports none (S11). What this plugin establishes per captured interface (S5c and S6c):
//   - exported: the node is hooked to the interface's ng_ether node, which exists only for an
//     interface ifconfig shows an `ether` line for (ng_ether attaches to IFT_ETHER, IFT_L2VLAN and
//     IFT_BRIDGE interfaces, the three types ifconfig's match_ether prints `ether` for). A tunnel such
//     as WireGuard has none, gets no node and exports nothing: "no", for the reason no_export_point.
//     An interface interfaces_info does not list, or a read that failed, is "unknown".
//   - parent_of: the node of an interface captured on ingress receives, on ng_ether's lower hook, the
//     frames of every VLAN whose parent it is, and exports them with itself as the receiving interface
//     (observed 16 October 2026; survey S5). Those VLANs are its parent_of.
// This script reports what the firewall says, and never how many interfaces it should capture on.
//
// WHAT THE JOIN READS, through the core datasets its manifest declares: the logged legs (`leg`), the firewall's
// interfaces and addresses as discovery holds them (`interface`, `this_firewall_address`), pf's state timeouts
// the active firewall_log provider exports (`provider_value`: pf.h's PFTM_*_VAL, survey S12), how far the filter
// log has been read (`read_through`), the gaps every provider recorded (`collection_gap`) and how far back the
// retention purge removed legs (`retention_purge`). Every rule reads those and the records' own fields, and names
// no device, interface, VLAN or address.

var d = opnview.decode;

// The reason an interface that cannot be exported is reported with: its node has no hook to attach to.
var NO_EXPORT_POINT = "no_export_point";

// The roles of a record's mapped index (NJ3).
var ROLE_INTERFACE = "interface";
var ROLE_NO_RECEIVING = "no_receiving_interface";
var ROLE_LOCAL = "local_delivery";
var ROLE_UNMAPPED = "unmapped";

// The join states (NJ2); a record the derivation could not read -- an address that is no address, a version its
// addresses contradict, a last packet before its first -- is `unreadable`, and counted nowhere.
var JOIN_PENDING = "pending";
var JOIN_JOINED = "joined";
var JOIN_COPY = "copy";
var JOIN_UNMATCHED = "unmatched";
var JOIN_UNREADABLE = "unreadable";

var COPY_TRANSLATED = "translated_copy";
var COPY_PARENT = "parent_copy";

// The unmatched reasons, a closed list (NJ4 rule 9, and N2 of the live validation of 17 October 2026: a leg the
// filter log's own recorded gap may hold is never claimed absent).
var UNMATCHED_NO_LOGGED_LEG = "no_logged_leg";
var UNMATCHED_AMBIGUOUS = "ambiguous";
var UNMATCHED_BEYOND_HORIZON = "beyond_flow_horizon";
var UNMATCHED_INDEX_UNMAPPED = "interface_index_unmapped";
var UNMATCHED_REVERSE_UNCONFIRMED = "reverse_unconfirmed";
var UNMATCHED_FILTER_LOG_GAP = "filter_log_gap";

var FORWARD = "forward";
var REVERSE = "reverse";

// The per-direction states (NJ5, N3 and D5 of the live validation of 17 October 2026), as the measure declares
// them in plugin.json.
var VOLUME_MEASURED = "measured";
var VOLUME_IN_PROGRESS = "in_progress";
var VOLUME_AWAITING_EXPORT = "awaiting_export";
var VOLUME_MISSING = "missing";
var VOLUME_EXPORT_GAP = "export_gap";
var VOLUME_NOT_EXPORTED_WHEN_REFUSED = "not_exported_when_refused";
var VOLUME_FORWARDING_UNSETTLED = "forwarding_export_unsettled";
var VOLUME_INGRESS_NOT_CAPTURED = "ingress_not_captured";
var VOLUME_PARENT_COPIES_ONLY = "parent_copies_only";
var VOLUME_INTERFACE_NOT_EXPORTED = "interface_not_exported";
var VOLUME_INTERFACE_NOT_CAPTURED = "interface_not_captured";
var VOLUME_CAPTURE_UNKNOWN = "capture_configuration_unknown";
var VOLUME_AMBIGUOUS_RECORDS = "ambiguous_records";
var VOLUME_NOT_EXPECTED_WHEN_REFUSED = "not_expected_when_refused";

// The window states the measure's intervals record.
var CAPTURE_CONFIGURED = "capture_configured";
var CAPTURE_NOT_CONFIGURED = "netflow_not_configured";

// The IANA protocol numbers the join treats on their own: TCP and UDP carry ports, ICMP and ICMPv6 do not.
var PROTOCOL_ICMP = 1;
var PROTOCOL_TCP = 6;
var PROTOCOL_UDP = 17;
var PROTOCOL_ICMPV6 = 58;

// The TCP flags the continuity rule reads (RFC 9293, section 3.1).
var TCP_FIN = 0x01;
var TCP_SYN = 0x02;
var TCP_RST = 0x04;
var TCP_ACK = 0x10;

// How long past a direction's span its state waits before it is final: the gaps the receiver records are merged
// over ten seconds and a sequence jump is confirmed within a minute (docs/plugins.md, "Push sources").
var FINALITY_MARGIN_SECONDS = 120;

// How old a read of interfaces_info the index map may use, in seconds: runtime discovery's default interval
// (docs/data-model.md), so the map is never older than discovery's view of the interfaces. After the exporter
// restarted the map is read afresh, a reboot being free to renumber cloned interfaces (NJ3).
var INDEX_MAP_MAX_AGE_SECONDS = 300;

// The decoder's condition for an exporter whose uptime went backwards (docs/plugins.md, "Push sources").
var EXPORTER_RESTARTED = "netflow_exporter_restarted";

// The most keys or ids one set read carries: the data catalogue reads at most 10 000 of either.
var READ_CHUNK = 4000;

// pf's state timeouts, as the active firewall_log provider exports them (the firewall_log contract's values).
var STATE_TIMEOUTS = {
  tcpFirst: "state_timeout_tcp_first_seconds",
  tcpEstablished: "state_timeout_tcp_established_seconds",
  tcpClosing: "state_timeout_tcp_closing_seconds",
  tcpClosed: "state_timeout_tcp_closed_seconds",
  udpMultiple: "state_timeout_udp_multiple_seconds",
  icmpFirst: "state_timeout_icmp_first_seconds",
  otherMultiple: "state_timeout_other_multiple_seconds"
};

// ---------------------------------------------------------------------------------------------------------------
// The capture configuration (PS18, kept by NJ6): what getconfig, is_enabled and interfaces_info say.
// ---------------------------------------------------------------------------------------------------------------

// selectedOptions returns the keys of an option list whose `selected` is set, sorted.
function selectedOptions(list) {
  if (list === null || typeof list !== "object" || Array.isArray(list)) {
    return null;
  }
  var keys = Object.keys(list).filter(function (key) {
    var option = list[key];
    return option !== null && typeof option === "object" && (option.selected === 1 || option.selected === "1" ||
      option.selected === true);
  });
  keys.sort();
  return keys;
}

// timeout reads a timeout field, answered as text by the model's IntegerField; null when absent or not
// a positive whole number.
function timeout(value) {
  if (value === null || value === undefined) {
    return null;
  }
  var number = Number(value);
  if (!isFinite(number) || number <= 0 || Math.floor(number) !== number) {
    return null;
  }
  return number;
}

// hasEthernetAddress says whether a row carries the address of an `ether` line: a MAC address that is
// not all zeros, which legacy_interfaces_details writes when the line is absent.
function hasEthernetAddress(row) {
  var mac = row.macaddr;
  if (typeof mac !== "string" || !/^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$/.test(mac)) {
    return false;
  }
  return !/^(00:){5}00$/.test(mac);
}

// interfacesFrom reads interfaces_info's rows into {byIdentifier, children}: the row of each
// assigned identifier, and, per parent device, the identifiers of the VLANs whose parent it is. null
// when the answer is not the search envelope.
function interfacesFrom(body) {
  if (body === null || typeof body !== "object" || !Array.isArray(body.rows)) {
    return null;
  }
  var byIdentifier = {};
  var children = {};
  body.rows.forEach(function (row) {
    if (row === null || typeof row !== "object") {
      return;
    }
    var identifier = typeof row.identifier === "string" ? row.identifier : "";
    if (identifier !== "") {
      byIdentifier[identifier] = row;
    }
    var vlan = row.vlan;
    if (identifier !== "" && vlan !== null && typeof vlan === "object" && typeof vlan.parent === "string" &&
        vlan.parent !== "") {
      (children[vlan.parent] = children[vlan.parent] || []).push(identifier);
    }
  });
  Object.keys(children).forEach(function (parent) { children[parent].sort(); });
  return { byIdentifier: byIdentifier, children: children };
}

// describe adds exported, not_exported_reason and parent_of to a captured interface, from
// interfaces_info's rows; interfaces null is a read that failed, which establishes nothing.
function describe(captured, interfaces) {
  if (interfaces === null) {
    captured.exported = "unknown";
    return captured;
  }
  var row = interfaces.byIdentifier[captured.identifier];
  if (row === undefined || typeof row.device !== "string" || row.device === "") {
    captured.exported = "unknown";
    return captured;
  }
  if (!hasEthernetAddress(row)) {
    captured.exported = "no";
    captured.not_exported_reason = NO_EXPORT_POINT;
    return captured;
  }
  captured.exported = "yes";
  var children = interfaces.children[row.device] || [];
  if (captured.ingress && children.length > 0) {
    captured.parent_of = children.filter(function (identifier) { return identifier !== captured.identifier; });
  }
  return captured;
}

// captureFrom builds the capture configuration from getconfig's answer, is_enabled's, and
// interfaces_info's rows (null when that read failed); null when getconfig's answer does not have the
// model's shape.
function captureFrom(config, enabled, interfaces) {
  if (config === null || typeof config !== "object" || config.netflow === null ||
      typeof config.netflow !== "object") {
    return null;
  }
  var netflow = config.netflow;
  var capture = netflow.capture;
  if (capture === null || typeof capture !== "object") {
    return null;
  }
  var chosen = selectedOptions(capture.interfaces);
  var egressOnly = selectedOptions(capture.egress_only);
  if (chosen === null || egressOnly === null) {
    return null;
  }
  var versions = selectedOptions(capture.version);
  var result = {
    enabled: enabled,
    interfaces: chosen.map(function (identifier) {
      return describe({ identifier: identifier, ingress: egressOnly.indexOf(identifier) < 0, egress: true },
        interfaces === undefined ? null : interfaces);
    })
  };
  if (versions !== null && versions.length === 1) {
    result.export_version = versions[0];
  }
  var active = timeout(netflow.activeTimeout);
  if (active !== null) {
    result.active_timeout_seconds = active;
  }
  var inactive = timeout(netflow.inactiveTimeout);
  if (inactive !== null) {
    result.inactive_timeout_seconds = inactive;
  }
  return result;
}

// readEnabled reads is_enabled: { enabled, result, error }.
function readEnabled(host) {
  var result = { probe: "is_enabled", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("is_enabled");
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail;
    return { result: result, failed: true };
  }
  var body = response.decodable ? response.value : null;
  if (body === null || typeof body !== "object" || body.netflow === undefined) {
    result.outcome = "undecodable";
    result.detail = "HTTP 200 with a body that carries no netflow field";
    return { result: result, failed: true };
  }
  return { result: result, enabled: d.int(body, "netflow") === 1 };
}

// readInterfaces reads interfaces_info's rows; null when the read failed or the answer is not the
// search envelope, in which case every interface's export is unknown, never guessed. A failure is
// named in detail, so the availability says what the broker answered.
function readInterfaces(host) {
  var response = host.call("interfaces_info", { arguments: ["1"] });
  if (response.error || !response.ok) {
    return { interfaces: null, detail: "interfaces_info answered " + (response.error || response.outcome) +
      ": whether each captured interface is exported is unknown" };
  }
  var interfaces = interfacesFrom(response.decodable ? response.value : null);
  if (interfaces === null) {
    return { interfaces: null, detail: "interfaces_info answered a body that is not the search envelope: " +
      "whether each captured interface is exported is unknown" };
  }
  return { interfaces: interfaces };
}

// readCapture reads the capture configuration: {result, capture} when it was read, {result, error} when a read
// of getconfig or is_enabled failed -- a status, a body without the model's shape -- with what the firewall
// answered. A read of interfaces_info that fails leaves the configuration readable, with every interface's export
// unknown.
function readCapture(host) {
  var enabled = readEnabled(host);
  if (enabled.error || enabled.failed) {
    return { result: enabled.result, error: enabled.error || ("the NetFlow export's state could not be read: " +
      enabled.result.outcome) };
  }
  var result = { probe: "getconfig", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("getconfig");
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    result.detail = response.detail;
    return { result: result, error: "the capture configuration could not be read: " + response.outcome };
  }
  var interfaces = readInterfaces(host);
  var capture = captureFrom(response.decodable ? response.value : null, enabled.enabled, interfaces.interfaces);
  if (capture === null) {
    result.outcome = "undecodable";
    result.detail = "HTTP 200 with a body that is not the NetFlow model's settings";
    return { result: result, error: "the capture configuration is not the model's shape" };
  }
  result.state = "reachable";
  result.outcome = "healthy";
  result.separable = true;
  if (interfaces.detail) {
    result.detail = interfaces.detail;
  }
  return { result: result, capture: capture };
}

// checkedCapture refuses what no reading of the model can give: an interface named twice or with no
// identifier, a not_exported_reason other than exactly when exported is no, an interface its own parent. It
// answers the reason, or "".
function checkedCapture(capture) {
  var seen = {};
  for (var index = 0; index < capture.interfaces.length; index++) {
    var captured = capture.interfaces[index];
    if (!captured.identifier || seen[captured.identifier]) {
      return "interface " + index + " of the capture names an empty or repeated identifier";
    }
    seen[captured.identifier] = true;
    if (!captured.exported) {
      captured.exported = "unknown";
    }
    if ((captured.exported === "no") !== !!captured.not_exported_reason) {
      return "interface " + index + " of the capture names a not_exported_reason exactly when exported is not \"no\"";
    }
    var parents = captured.parent_of || [];
    for (var at = 0; at < parents.length; at++) {
      if (!parents[at] || parents[at] === captured.identifier) {
        return "interface " + index + " of the capture lists an empty identifier, or itself, in parent_of";
      }
    }
  }
  return "";
}

// fingerprint is a readable configuration as one canonical text: two reads are the same configuration exactly
// when their fingerprints are equal.
function fingerprint(capture) {
  var optional = function (value) { return value === null || value === undefined ? "-" : String(value); };
  var text = "enabled=" + (capture.enabled ? "true" : "false") + ";version=" + (capture.export_version || "") +
    ";active=" + optional(capture.active_timeout_seconds) + ";inactive=" + optional(capture.inactive_timeout_seconds);
  var interfaces = capture.interfaces.slice().sort(function (a, b) { return compareText(a.identifier, b.identifier); });
  interfaces.forEach(function (captured) {
    var parents = (captured.parent_of || []).slice().sort(compareText);
    text += ";" + captured.identifier + ":" + (captured.ingress ? "true" : "false") + ":" +
      (captured.egress ? "true" : "false") + ":" + captured.exported + ":" + (captured.not_exported_reason || "") + ":" +
      parents.join("+");
  });
  return text;
}

function compareText(a, b) {
  return a < b ? -1 : a > b ? 1 : 0;
}

// captureRows records one read of the capture configuration at readAt (PS18): a read equal to the newest run --
// the same fingerprint, or the same failure -- extends it; any other opens a run. A read older than the newest
// one recorded changes nothing: the history is written forward. It answers the rows to write and whether a run
// opened.
function captureRows(state, readAt, read) {
  var newest = state.newest_first_read_at ? Number(state.newest_first_read_at) : null;
  var newestLast = state.newest_last_read_at ? Number(state.newest_last_read_at) : null;
  if (newest !== null && readAt < newestLast) {
    return { rows: [], interfaces: [], opened: false, state: state };
  }
  var print = read.readable ? fingerprint(read.capture) : "";
  var same = newest !== null && ((read.readable && state.newest_fingerprint === print) ||
    (!read.readable && state.newest_fingerprint === "" && state.newest_outcome === read.outcome));
  var row = { first_read_at: same ? newest : readAt, last_read_at: readAt, readable: read.readable,
    outcome: read.readable ? null : read.outcome, detail: read.detail || null, enabled: null, export_version: null,
    active_timeout_seconds: null, inactive_timeout_seconds: null, fingerprint: read.readable ? print : null };
  if (read.readable) {
    row.enabled = read.capture.enabled === true;
    row.export_version = read.capture.export_version || null;
    row.active_timeout_seconds = read.capture.active_timeout_seconds || null;
    row.inactive_timeout_seconds = read.capture.inactive_timeout_seconds || null;
  }
  var interfaces = [];
  if (!same && read.readable) {
    read.capture.interfaces.forEach(function (captured) {
      interfaces.push({ first_read_at: readAt, identifier: captured.identifier, ingress: captured.ingress === true,
        egress: captured.egress === true, exported: captured.exported || "unknown",
        not_exported_reason: captured.exported === "no" ? (captured.not_exported_reason || null) : null,
        parent_of: (captured.parent_of || []).slice().sort(compareText).join(",") });
    });
  }
  var next = { newest_first_read_at: String(row.first_read_at), newest_last_read_at: String(readAt),
    newest_fingerprint: print, newest_outcome: read.readable ? "" : read.outcome };
  if (same && read.readable) {
    // The run's interfaces are the ones it opened with: an extension carries the configuration's own fields.
    row.enabled = state.newest_enabled === "true";
    next.newest_enabled = state.newest_enabled;
  } else {
    next.newest_enabled = row.enabled === true ? "true" : "false";
  }
  return { rows: [row], interfaces: interfaces, opened: !same, state: next, counters: read.readable ?
    sequenceCounters(read.capture) : 0 };
}

// sequenceCounters is how many sequence counters the configuration lets the exporter run under one observation
// domain: the interfaces it reports as exporting (NJ7). 0 when that is not known -- an interface whose export is
// unknown -- or when the export is off, and no sequence gap is then asserted.
function sequenceCounters(capture) {
  if (!capture.enabled) {
    return 0;
  }
  var counters = 0;
  for (var index = 0; index < capture.interfaces.length; index++) {
    var captured = capture.interfaces[index];
    if (!captured.ingress && !captured.egress) {
      continue;
    }
    if (captured.exported === "unknown" || !captured.exported) {
      return 0;
    }
    if (captured.exported === "yes") {
      counters++;
    }
  }
  return counters;
}

// ---------------------------------------------------------------------------------------------------------------
// The capture history, as the derivation reads it back.
// ---------------------------------------------------------------------------------------------------------------

// readHistory reads the capture configurations, oldest first, each with its interfaces.
function readHistory(host) {
  var rows = host.query("SELECT first_read_at, last_read_at, readable, enabled, active_timeout_seconds, fingerprint " +
    "FROM plugin_netflow.capture_configuration ORDER BY first_read_at", {});
  var interfaces = host.query("SELECT first_read_at, identifier, ingress, egress, exported, parent_of " +
    "FROM plugin_netflow.capture_interface ORDER BY first_read_at, identifier", {});
  var byRead = {};
  var history = rows.map(function (row) {
    var configuration = { firstReadAt: row.first_read_at, lastReadAt: row.last_read_at, readable: row.readable === true,
      enabled: row.enabled === true, activeTimeout: row.active_timeout_seconds, interfaces: [], byIdentifier: {} };
    byRead[row.first_read_at] = configuration;
    return configuration;
  });
  interfaces.forEach(function (row) {
    var configuration = byRead[row.first_read_at];
    if (configuration === undefined) {
      return;
    }
    var captured = { identifier: row.identifier, ingress: row.ingress === true, egress: row.egress === true,
      exported: row.exported, parentOf: row.parent_of === "" ? [] : row.parent_of.split(",") };
    configuration.interfaces.push(captured);
    configuration.byIdentifier[row.identifier] = captured;
  });
  return history;
}

// configurationAt is the configuration in force at an instant (PS18): the run whose reads hold the instant;
// after the newest read, the newest run; null before the first read, between the last read showing one
// configuration and the first showing another, and on a run whose read failed -- every instant whose
// configuration is unknown.
function configurationAt(history, at) {
  if (history.length === 0 || at < history[0].firstReadAt) {
    return null;
  }
  for (var index = 0; index < history.length; index++) {
    var row = history[index];
    if (at >= row.firstReadAt && at <= row.lastReadAt) {
      return row.readable ? row : null;
    }
    if (index + 1 < history.length && at > row.lastReadAt && at < history[index + 1].firstReadAt) {
      return null;
    }
  }
  var newest = history[history.length - 1];
  return newest.readable ? newest : null;
}

// exports reports whether a captured interface's exporter exports a direction: captured, and exported.
function exportsDirection(captured, ingress) {
  if (captured.exported !== "yes") {
    return false;
  }
  return ingress ? captured.ingress : captured.egress;
}

// captured are a configuration's interfaces with at least one direction captured.
function capturedOf(configuration) {
  return configuration.interfaces.filter(function (captured) { return captured.ingress || captured.egress; });
}

// interfaceOf is the captured interface of an identifier, or null when the configuration names none.
function interfaceOf(configuration, identifier) {
  if (!identifier) {
    return null;
  }
  return configuration.byIdentifier[identifier] || null;
}

// lossDetectable says whether a loss would have been recorded as a gap under the configuration in force: its
// sequence counters are bounded (NJ7).
function lossDetectable(configuration) {
  if (configuration === null || !configuration.readable || !configuration.enabled) {
    return false;
  }
  var counters = 0;
  var captured = capturedOf(configuration);
  for (var index = 0; index < captured.length; index++) {
    if (captured[index].exported === "unknown") {
      return false;
    }
    if (captured[index].exported === "yes") {
      counters++;
    }
  }
  return counters > 0;
}

// intervalsOf are the measure's window intervals from the configurations (PA5): each in force from its first read
// until the instant after its last read when another follows, the newest for as long as no later read says
// otherwise; a run whose read failed is capture_configuration_unknown, a readable one capture_configured when its
// export is on and it captures an interface, and netflow_not_configured otherwise.
function intervalsOf(history) {
  return history.map(function (configuration, index) {
    var state = VOLUME_CAPTURE_UNKNOWN;
    if (configuration.readable) {
      state = configuration.enabled && capturedOf(configuration).length > 0 ? CAPTURE_CONFIGURED : CAPTURE_NOT_CONFIGURED;
    }
    return { starts_at: configuration.firstReadAt,
      ends_at: index + 1 < history.length ? configuration.lastReadAt + 1 : null, state: state };
  });
}

// ---------------------------------------------------------------------------------------------------------------
// The index map in force at reception (NJ3).
// ---------------------------------------------------------------------------------------------------------------

// interfaceIndex reads `index`, which ifinfo prints and legacy_interface_stats keeps as text.
function interfaceIndex(value) {
  if (typeof value === "string") {
    if (!/^[0-9]+$/.test(value)) {
      return null;
    }
    var parsed = Number(value);
    return parsed > 0 && parsed <= 4294967295 ? parsed : null;
  }
  if (typeof value === "number" && value > 0 && Math.floor(value) === value) {
    return value;
  }
  return null;
}

// indexMapFrom reads the index map from interfaces_info's rows: each row's `statistics.index`, the kernel
// index ng_netflow exports, mapped to its device and identifier; the loopback's index is the index of the one
// row whose flags hold `loopback` (S6b), read from that flag and never from a device name, and with no such row,
// or two, it is not established. A row carrying no index maps nothing: an index is never matched by position,
// name or count.
function indexMapFrom(body) {
  var map = { byIndex: {}, loopback: 0 };
  if (body === null || typeof body !== "object" || !Array.isArray(body.rows)) {
    return map;
  }
  var loopbacks = 0;
  body.rows.forEach(function (row) {
    if (row === null || typeof row !== "object" || typeof row.device !== "string" || row.statistics === null ||
        typeof row.statistics !== "object" || Array.isArray(row.statistics)) {
      return;
    }
    var index = interfaceIndex(row.statistics.index);
    if (index === null) {
      return;
    }
    map.byIndex[index] = { identifier: typeof row.identifier === "string" ? row.identifier : "", device: row.device };
    if (Array.isArray(row.flags) && row.flags.indexOf("loopback") >= 0) {
      map.loopback = index;
      loopbacks++;
    }
  });
  if (loopbacks !== 1) {
    map.loopback = 0;
  }
  return map;
}

// readIndexMap reads the index map through the broker, at discovery's interval or afresh. A map that cannot be
// read maps nothing: every index other than 0 is then unmapped, never guessed.
function readIndexMap(host, fresh) {
  var response = host.call("interfaces_info", { arguments: ["1"], max_age_seconds: fresh ? 0 : INDEX_MAP_MAX_AGE_SECONDS });
  if (response.error || !response.ok || !response.decodable) {
    return { byIndex: {}, loopback: 0, readAt: null };
  }
  var map = indexMapFrom(response.value);
  map.readAt = response.fetched_at;
  return map;
}

// mapIndex is what an index names (NJ3): index 0 has no receiving interface, the loopback's index is local
// delivery, an index the map holds is that interface, and any other -- or none -- is unmapped.
function mapIndex(map, index) {
  if (index === null || index === undefined || index < 0) {
    return { role: ROLE_UNMAPPED, identifier: null, device: null };
  }
  if (index === 0) {
    return { role: ROLE_NO_RECEIVING, identifier: null, device: null };
  }
  if (map.loopback !== 0 && index === map.loopback) {
    return { role: ROLE_LOCAL, identifier: null, device: null };
  }
  var mapped = map.byIndex[index];
  if (mapped === undefined || mapped.device === "") {
    return { role: ROLE_UNMAPPED, identifier: null, device: null };
  }
  return { role: ROLE_INTERFACE, identifier: mapped.identifier === "" ? null : mapped.identifier, device: mapped.device };
}

// ---------------------------------------------------------------------------------------------------------------
// Addresses, tuples and the time rules.
// ---------------------------------------------------------------------------------------------------------------

// addressCache keeps what opnview.ip read of an address, for the run.
var addressCache = {};

// ip is an address read as a value: {address, version, key, loopback}, or null.
function ip(text) {
  if (text === null || text === undefined) {
    return null;
  }
  var cached = addressCache[text];
  if (cached === undefined) {
    cached = opnview.ip(text);
    addressCache[text] = cached;
  }
  return cached;
}

// portless reports whether a record joins on its two addresses alone: ICMP, ICMPv6, and any record with no port or
// ports 0 -- ICMP and non-first fragments carry ports 0 (survey, Push sources, S10).
function portless(protocol, source, destination) {
  return protocol === PROTOCOL_ICMP || protocol === PROTOCOL_ICMPV6 || source === null || destination === null ||
    (source === 0 && destination === 0);
}

// tupleKey is a record's two ends, unordered, with its version and protocol: the join groups records by it.
function tupleKey(version, protocol, source, destination, sourcePort, destinationPort) {
  var noPorts = portless(protocol, sourcePort, destinationPort);
  var end = function (address, port) { return noPorts ? address.address : address.address + "#" + port; };
  var low = end(source, sourcePort);
  var high = end(destination, destinationPort);
  if (destination.key < source.key || (destination.key === source.key && !noPorts && destinationPort < sourcePort)) {
    var swap = low;
    low = high;
    high = swap;
  }
  return version + "|" + protocol + "|" + low + "|" + high;
}

// idle is pf's timeout bounding the idle time of a connection of a protocol, given the TCP flags its records
// carried so far (survey, S12).
function idle(timeouts, protocol, flags) {
  switch (protocol) {
    case PROTOCOL_TCP:
      if ((flags & TCP_RST) !== 0) {
        return timeouts.tcpClosed;
      }
      if ((flags & TCP_FIN) !== 0) {
        return timeouts.tcpClosing;
      }
      if ((flags & TCP_ACK) === 0) {
        // No packet acknowledged anything: the state never left its first packet.
        return timeouts.tcpFirst;
      }
      return timeouts.tcpEstablished;
    case PROTOCOL_UDP:
      return timeouts.udpMultiple;
    case PROTOCOL_ICMP:
    case PROTOCOL_ICMPV6:
      return timeouts.icmpFirst;
  }
  return timeouts.otherMultiple;
}

// longest is the longest a connection of a protocol can stay idle and keep its pf state: an established TCP
// connection's, for TCP.
function longest(timeouts, protocol) {
  if (protocol === PROTOCOL_TCP) {
    return timeouts.tcpEstablished;
  }
  return idle(timeouts, protocol, TCP_ACK);
}

// readTimeouts reads pf's seven state timeouts the active providers export; null when any is missing or not a
// positive whole number, and then nothing is decided: no default stands in for a fact a source did not state.
function readTimeouts(host) {
  var values = {};
  host.datasets.rows("provider_value", { columns: ["name", "integer_value"] }).rows.forEach(function (row) {
    values[row.name] = row.integer_value;
  });
  var timeouts = {};
  var names = Object.keys(STATE_TIMEOUTS);
  for (var index = 0; index < names.length; index++) {
    var value = values[STATE_TIMEOUTS[names[index]]];
    if (typeof value !== "number" || value <= 0) {
      return null;
    }
    timeouts[names[index]] = value;
  }
  return timeouts;
}

// ---------------------------------------------------------------------------------------------------------------
// Reading the records.
// ---------------------------------------------------------------------------------------------------------------

// RECORD_COLUMNS reads a whole row of flow_record, its instants as whole milliseconds since the epoch.
var RECORD_COLUMNS = "delivery, delivery_position, CAST(extract(epoch FROM flow_start_at) * 1000 AS BIGINT) AS start_ms, " +
  "CAST(extract(epoch FROM flow_end_at) * 1000 AS BIGINT) AS end_ms, " +
  "CAST(extract(epoch FROM collected_at) * 1000 AS BIGINT) AS received_ms, exporter_address, observation_domain, " +
  "ingress_interface, egress_interface, ip_version, protocol, src_address, dst_address, src_port, dst_port, tcp_flags, " +
  "octets, packets, identity, start_at, start_millisecond, end_at, end_millisecond, received_at, ingress_role, " +
  "ingress_identifier, ingress_device, egress_role, egress_identifier, egress_device, loss_detectable, tuple_key, " +
  "join_state, joined_flow_id, copy_kind, copy_flow_id, orientation, counted_copy, unmatched_reason, decided_at";

// isoOf is an instant of whole milliseconds as the RFC 3339 text an instant column takes.
function isoOf(milliseconds) {
  return new Date(milliseconds).toISOString();
}

// rowOf is a record as a row of flow_record to write: every column, the raw ones as they were received.
function rowOf(record) {
  return {
    delivery: record.delivery, delivery_position: record.delivery_position, flow_start_at: isoOf(record.start_ms),
    flow_end_at: isoOf(record.end_ms), collected_at: isoOf(record.received_ms), exporter_address: record.exporter_address,
    observation_domain: record.observation_domain, ingress_interface: record.ingress_interface,
    egress_interface: record.egress_interface, ip_version: record.ip_version, protocol: record.protocol,
    src_address: record.src_address, dst_address: record.dst_address, src_port: record.src_port,
    dst_port: record.dst_port, tcp_flags: record.tcp_flags, octets: record.octets, packets: record.packets,
    identity: record.identity, start_at: record.start_at, start_millisecond: record.start_millisecond,
    end_at: record.end_at, end_millisecond: record.end_millisecond, received_at: record.received_at,
    ingress_role: record.ingress_role, ingress_identifier: record.ingress_identifier,
    ingress_device: record.ingress_device, egress_role: record.egress_role, egress_identifier: record.egress_identifier,
    egress_device: record.egress_device, loss_detectable: record.loss_detectable, tuple_key: record.tuple_key,
    join_state: record.join_state, joined_flow_id: record.joined_flow_id, copy_kind: record.copy_kind,
    copy_flow_id: record.copy_flow_id, orientation: record.orientation, counted_copy: record.counted_copy,
    unmatched_reason: record.unmatched_reason, decided_at: record.decided_at
  };
}

// chunks splits a list into lists of at most size.
function chunks(list, size) {
  var out = [];
  for (var start = 0; start < list.length; start += size) {
    out.push(list.slice(start, start + size));
  }
  return out;
}

// THE RUN'S OWN WRITES ARE READ BACK AS IF STORED. What a run returns is written together at its end, so a record
// it mapped is not pending in the table yet when it joins, and a record it decided is not joined there yet when it
// measures. overlay holds every record the run wrote, by its key, and every read of records reads through it.
var overlay = {};

// recordKey is a record's primary key, as one text.
function recordKey(record) {
  return record.delivery + "|" + record.delivery_position + "|" + record.start_ms;
}

// remember writes a record's row and keeps the record as the run now holds it.
function remember(rows, record) {
  rows.flow_record.push(rowOf(record));
  overlay[recordKey(record)] = record;
}

// throughOverlay reads records as the run holds them: each stored one the run wrote replaced by what it wrote, kept
// only while it still meets the read's condition, and every record the run wrote that meets it and was not read
// added; oldest first, as the table orders them.
function throughOverlay(stored, keep) {
  var seen = {};
  var read = [];
  stored.forEach(function (record) {
    var key = recordKey(record);
    seen[key] = true;
    var held = overlay[key] || record;
    if (keep(held)) {
      read.push(held);
    }
  });
  Object.keys(overlay).forEach(function (key) {
    if (!seen[key] && keep(overlay[key])) {
      read.push(overlay[key]);
    }
  });
  read.sort(function (a, b) {
    return a.start_at - b.start_at || a.start_millisecond - b.start_millisecond || compareText(a.identity, b.identity);
  });
  return read;
}

// recordsWhere reads whole records whose column is one of some numbers or texts, in chunks, oldest first, through
// the run's own writes.
function recordsWhere(host, column, values, cast) {
  var read = [];
  var wanted = {};
  values.forEach(function (value) { wanted[value] = true; });
  chunks(values, READ_CHUNK).forEach(function (chunk) {
    read = read.concat(host.query("SELECT " + RECORD_COLUMNS + " FROM plugin_netflow.flow_record " +
      "WHERE " + column + " IN (SELECT CAST(j.value AS " + cast + ") FROM jsonb_array_elements_text(CAST(:values AS jsonb)) AS j(value)) " +
      "ORDER BY start_at, start_millisecond, identity", { values: JSON.stringify(chunk) }));
  });
  return throughOverlay(read, function (record) {
    return record[column] !== null && record[column] !== undefined && wanted[record[column]] === true;
  });
}

// ---------------------------------------------------------------------------------------------------------------
// Mapping what arrived (NJ3): each record's two indexes, with the map in force at reception, and what the core
// derived of a pushed record -- its identity, its canonical addresses, its version, its instants.
// ---------------------------------------------------------------------------------------------------------------

// mapReceived maps every record not mapped yet. A record is mapped once and never again.
function mapReceived(host, run, history, rows) {
  var received = host.query("SELECT " + RECORD_COLUMNS + " FROM plugin_netflow.flow_record WHERE join_state IS NULL " +
    "ORDER BY collected_at, delivery, delivery_position", {});
  if (received.length === 0) {
    return [];
  }
  var restarted = run.changes.some(function (change) {
    return change.trigger === "condition" && change.reason === EXPORTER_RESTARTED;
  });
  var map = readIndexMap(host, restarted);
  var mapRows = Object.keys(map.byIndex).map(function (index) {
    var mapped = map.byIndex[index];
    return { interface_index: Number(index), device: mapped.device, identifier: mapped.identifier === "" ? null :
      mapped.identifier, loopback: Number(index) === map.loopback, in_map: true, read_at: Math.floor(map.readAt || run.now) };
  });
  host.query("SELECT interface_index, device, identifier, loopback, in_map, read_at FROM plugin_netflow.interface_index", {})
    .forEach(function (held) {
      if (map.byIndex[held.interface_index] === undefined && held.in_map) {
        held.in_map = false;
        mapRows.push(held);
      }
    });
  rows.interface_index = mapRows;
  var mapped = [];
  received.forEach(function (record) {
    var source = ip(record.src_address);
    var destination = ip(record.dst_address);
    var exporter = ip(record.exporter_address);
    var family = function (address) { return address.version; };
    var unreadable = source === null || destination === null || exporter === null || record.end_ms < record.start_ms;
    var version = null;
    if (!unreadable) {
      version = record.ip_version === null ? family(source) : record.ip_version;
      unreadable = version !== family(source) || family(destination) !== version;
    }
    record.identity = opnview.sha256Hex((exporter === null ? record.exporter_address : exporter.address) + "|" +
      record.observation_domain + "|" + record.delivery + "|" + record.delivery_position);
    record.start_at = Math.floor(record.start_ms / 1000);
    record.start_millisecond = record.start_ms - record.start_at * 1000;
    record.end_at = Math.floor(record.end_ms / 1000);
    record.end_millisecond = record.end_ms - record.end_at * 1000;
    record.received_at = Math.floor(record.received_ms / 1000);
    if (unreadable) {
      record.join_state = JOIN_UNREADABLE;
      record.counted_copy = false;
      record.loss_detectable = false;
      remember(rows, record);
      return;
    }
    record.exporter_address = exporter.address;
    record.src_address = source.address;
    record.dst_address = destination.address;
    record.ip_version = version;
    var ingress = mapIndex(map, record.ingress_interface);
    var egress = mapIndex(map, record.egress_interface);
    record.ingress_role = ingress.role;
    record.ingress_identifier = ingress.identifier;
    record.ingress_device = ingress.device;
    record.egress_role = egress.role;
    record.egress_identifier = egress.identifier;
    record.egress_device = egress.device;
    record.loss_detectable = lossDetectable(configurationAt(history, record.start_at));
    record.tuple_key = tupleKey(version, record.protocol, source, destination, record.src_port, record.dst_port);
    record.join_state = JOIN_PENDING;
    record.counted_copy = false;
    remember(rows, record);
    mapped.push(record);
  });
  return mapped;
}

// ---------------------------------------------------------------------------------------------------------------
// The join of NJ4 (the analysis's "Rule B"), decided for one group of records -- one unordered tuple -- from
// stored rows only. Definitions: dev(i) is the interface a record's mapped index i names; L is the loopback's
// index (the local_delivery role); a leg is a logged connection leg; a second leg is a leg whose pairing outcome is
// second_leg, and is never a candidate.
//
//  1. ingress_interface = 0 (firewall-originated): forward to the `out` leg on dev(egress), or in reverse to the
//     `in` leg on dev(egress).
//  2. ingress_interface != 0: (a) forward to the `in` leg on dev(ingress); (b) in reverse to the `in` leg on
//     dev(egress); (c) if egress_interface = L, in reverse to the `out` leg on dev(ingress).
//  3. The candidates of every case are pooled and PS19's time, ambiguity, SYN and continuity rules choose.
//  4. A reverse join under rule 1 or 2(b) needs a forward record joined to the same leg whose egress_interface is
//     this record's ingress_interface, 0 and L counted equal; until one exists the record stays pending, up to the
//     leg plus the active timeout plus the tolerance.
//  5. A record joining nothing, with ingress_interface != 0 and egress_interface != L, whose tuple is a second
//     leg's logged tuple on dev(egress) or dev(ingress), or one of whose ends is the firewall's, is a translated
//     copy.
//  6. A record whose dev(ingress) declares a parent_of is a parent copy when it would join under rule 2 with
//     dev(ingress) replaced by one of them, or when rule 4 rejected it and the accepted forward record's egress
//     interface is one of them.
// ---------------------------------------------------------------------------------------------------------------

// firewallHeld reports whether an address was the firewall's at an instant, or is a loopback address.
function firewallHeld(input, address, at) {
  if (address.loopback) {
    return true;
  }
  var held = input.firewall[address.address];
  if (held === undefined) {
    return false;
  }
  for (var index = 0; index < held.length; index++) {
    if (at >= held[index].from && (at <= held[index].until || held[index].current)) {
      return true;
    }
  }
  return false;
}

// parentOf is the parent_of of an interface in the configuration in force at an instant.
function parentOf(input, identifier, at) {
  if (!identifier) {
    return [];
  }
  var configuration = configurationAt(input.history, at);
  if (configuration === null) {
    return [];
  }
  var captured = interfaceOf(configuration, identifier);
  return captured === null ? [] : captured.parentOf;
}

// childrenOf are the devices of the interfaces a record's ingress interface is the parent of.
function childrenOf(input, record) {
  var devices = [];
  parentOf(input, record.ingress_identifier, record.start_at).forEach(function (child) {
    if (input.devices[child] !== undefined) {
      devices.push(input.devices[child]);
    }
  });
  return devices;
}

// exportBoundAt is how long after a leg its records may still be exported: the active timeout in force at the
// leg plus the tolerance, or pf's established TCP timeout when no timeout is known.
function exportBoundAt(input, at) {
  var configuration = configurationAt(input.history, at);
  if (configuration !== null && configuration.activeTimeout !== null) {
    return configuration.activeTimeout + input.tolerance;
  }
  return input.timeouts.tcpEstablished + input.tolerance;
}

// sameTuple reports whether a record's tuple is a leg's in an orientation: forward is the leg's logged tuple,
// reverse its swap (RFC 5103). Addresses are compared as values; a record joining on its addresses alone ignores
// the ports.
function sameTuple(record, leg, orientation) {
  var legSource = leg.source, legDestination = leg.destination, legSourcePort = leg.src_port,
    legDestinationPort = leg.dst_port;
  if (orientation === REVERSE) {
    legSource = leg.destination;
    legDestination = leg.source;
    legSourcePort = leg.dst_port;
    legDestinationPort = leg.src_port;
  }
  if (record.source.key !== legSource.key || record.destination.key !== legDestination.key) {
    return false;
  }
  if (portless(record.protocol, record.src_port, record.dst_port)) {
    return true;
  }
  return samePort(record.src_port, legSourcePort) && samePort(record.dst_port, legDestinationPort);
}

function samePort(a, b) {
  return a !== null && b !== null && a === b;
}

// candidatesOf pools the candidates of rules 1 and 2 (NJ4 rule 3): legs that are not second legs, of the record's
// protocol, logged at or before latestAllowed, on the device and with the pf direction a case names, whose tuple is
// the record's in that case's orientation.
function candidatesOf(record, ingress, egress, legs, latestAllowed) {
  var candidates = [];
  var add = function (device, legDirection, orientation, confirm) {
    if (device === null) {
      return;
    }
    legs.forEach(function (leg) {
      if (leg.device !== device || leg.direction !== legDirection || leg.pair_outcome === "second_leg" ||
          leg.protocolNumber === null || leg.protocolNumber !== record.protocol || leg.observed_at > latestAllowed) {
        return;
      }
      if (sameTuple(record, leg, orientation)) {
        candidates.push({ leg: leg, orientation: orientation, confirm: confirm });
      }
    });
  };
  switch (ingress.role) {
    case ROLE_NO_RECEIVING:
      if (egress.role === ROLE_INTERFACE) {
        add(egress.device, "out", FORWARD, false);
        add(egress.device, "in", REVERSE, true);
      }
      break;
    case ROLE_INTERFACE:
      add(ingress.device, "in", FORWARD, false);
      if (egress.role === ROLE_INTERFACE) {
        add(egress.device, "in", REVERSE, true);
      } else if (egress.role === ROLE_LOCAL) {
        add(ingress.device, "out", REVERSE, false);
      }
      break;
  }
  return candidates;
}

// ingressOf and egressOf are a record's two mapped indexes.
function ingressOf(record) {
  return { role: record.ingress_role, identifier: record.ingress_identifier, device: record.ingress_device };
}

function egressOf(record) {
  return { role: record.egress_role, identifier: record.egress_identifier, device: record.egress_device };
}

// joinOne decides one record: the candidate it joins, why it joins none, or that no candidate exists. reason is
// empty when no candidate existed at all, which the copy rules then examine; ambiguous lists the legs tied.
function joinOne(record, legs, chains, input) {
  if (!record.parsed) {
    return { chosen: null, reason: UNMATCHED_NO_LOGGED_LEG };
  }
  if (input.purged && record.start_at < input.purgedBefore) {
    // Started before the filter log's horizon: its leg may be gone.
    return { chosen: null, reason: UNMATCHED_BEYOND_HORIZON };
  }
  var candidates = candidatesOf(record, ingressOf(record), egressOf(record), legs, record.start_at + input.tolerance);
  if (candidates.length === 0) {
    return { chosen: null, reason: "" };
  }
  // The latest leg wins; two at that instant are never told apart by guessing.
  var latest = candidates[0];
  var tie = false;
  for (var index = 1; index < candidates.length; index++) {
    var other = candidates[index];
    if (other.leg.observed_at > latest.leg.observed_at) {
      latest = other;
      tie = false;
    } else if (other.leg.observed_at === latest.leg.observed_at) {
      tie = true;
    }
  }
  if (tie) {
    return { chosen: null, reason: UNMATCHED_AMBIGUOUS };
  }
  // A TCP record opening a connection joins only a leg logged with its first packet.
  if (record.protocol === PROTOCOL_TCP && record.tcp_flags !== null && (record.tcp_flags & TCP_SYN) !== 0 &&
      latest.leg.observed_at < record.start_at - input.tolerance) {
    return { chosen: null, reason: UNMATCHED_NO_LOGGED_LEG };
  }
  // Continuity: the connection's pf state must still be able to exist.
  var base = latest.leg.observed_at;
  var flags = 0;
  var chain = chains[latest.leg.flow_id];
  if (chain !== undefined) {
    var end = chain.lastEnd[latest.orientation];
    if (end !== undefined && end > base) {
      base = end;
    }
    flags = chain.flags;
  }
  if (record.start_at - base > idle(input.timeouts, record.protocol, flags)) {
    return { chosen: null, reason: UNMATCHED_NO_LOGGED_LEG };
  }
  return { chosen: latest, reason: "" };
}

// sameIndex reports whether two raw indexes are one interface, or both no interface at all: index 0 and the
// loopback's index are counted equal.
function sameIndex(a, aRole, b, bRole) {
  if (a === null || b === null) {
    return false;
  }
  if (a === b) {
    return true;
  }
  var local = function (index, role) { return index === 0 || role === ROLE_LOCAL; };
  return local(a, aRole) && local(b, bRole);
}

// confirmedBy reports whether a forward record joined to the same leg confirms a reverse record (rule 4): its
// egress_interface is the reverse record's ingress_interface, 0 and L counted as equal.
function confirmedBy(reverse, forwards) {
  for (var index = 0; index < forwards.length; index++) {
    var forward = forwards[index];
    if (sameIndex(forward.egress_interface, forward.egress_role, reverse.ingress_interface, reverse.ingress_role)) {
      return true;
    }
  }
  return false;
}

// wouldJoinAsChild reports whether a record would join under rule 2 if its dev(ingress) were replaced by an
// interface of its parent_of (rule 6(i)): a leg logged `in` there with its tuple forward, or, when its
// egress_interface is L, a leg logged `out` there with its tuple reversed.
function wouldJoinAsChild(record, legs, input) {
  if (!record.parsed || record.ingress_role !== ROLE_INTERFACE) {
    return false;
  }
  var latestAllowed = record.start_at + input.tolerance;
  var children = childrenOf(input, record);
  for (var index = 0; index < children.length; index++) {
    var child = children[index];
    var replaced = { role: ROLE_INTERFACE, identifier: null, device: child };
    var found = candidatesOf(record, replaced, egressOf(record), legs, latestAllowed);
    for (var at = 0; at < found.length; at++) {
      if (found[at].leg.device === child) {
        return true;
      }
    }
  }
  return false;
}

// translatedOf is the second leg whose logged tuple a record's is, in either orientation, on dev(egress) or
// dev(ingress) (rule 5's first signal): the latest such leg at or before the record's start plus the tolerance.
// Two at that instant name neither.
function translatedOf(record, legs, input) {
  if (!record.parsed) {
    return null;
  }
  var found = null;
  var orientation = "";
  var tie = false;
  legs.forEach(function (leg) {
    if (leg.pair_outcome !== "second_leg" || leg.protocolNumber === null || leg.protocolNumber !== record.protocol ||
        leg.observed_at > record.start_at + input.tolerance ||
        (leg.device !== record.ingress_device && leg.device !== record.egress_device)) {
      return;
    }
    [FORWARD, REVERSE].forEach(function (candidate) {
      if (!sameTuple(record, leg, candidate)) {
        return;
      }
      if (found === null || leg.observed_at > found.observed_at) {
        found = leg;
        orientation = candidate;
        tie = false;
      } else if (leg.observed_at === found.observed_at && leg.flow_id !== found.flow_id) {
        tie = true;
      }
    });
  });
  if (tie || found === null) {
    return null;
  }
  return { leg: found, orientation: orientation };
}

// inFilterLogGap reports whether a leg the record would join may lie in a gap the filter log recorded: a gap
// meeting [start - pf's longest timeout of its protocol, start + tolerance], the span its leg was logged in if it
// was (N2 of the live validation of 17 October 2026).
function inFilterLogGap(record, input) {
  var from = record.start_at - longest(input.timeouts, record.protocol);
  var to = record.start_at + input.tolerance;
  for (var index = 0; index < input.filterLogGaps.length; index++) {
    var gap = input.filterLogGaps[index];
    if (gap.interval_start_at <= to && gap.interval_end_at >= from) {
      return true;
    }
  }
  return false;
}

// decision is a record's decision, its previous one kept to compare.
function decision(record, joinState) {
  return { record: record, join_state: joinState, joined_flow_id: null, copy_kind: null, copy_flow_id: null,
    orientation: null, unmatched_reason: null };
}

function unmatched(made, reason) {
  made.join_state = JOIN_UNMATCHED;
  made.unmatched_reason = reason;
  return made;
}

function copyOf(made, kind) {
  made.join_state = JOIN_COPY;
  made.copy_kind = kind;
  return made;
}

// unconfirmedDecision decides a record whose reverse join rule 4 did not confirm: a parent copy by rule 6(ii) when
// the accepted forward record left by an interface its ingress interface is the parent of; pending until the leg's
// export bound; past it a parent copy by rule 6(i), or else reverse_unconfirmed.
function unconfirmedDecision(record, chosen, forwards, legs, input) {
  var made = decision(record, JOIN_PENDING);
  var parents = parentOf(input, record.ingress_identifier, record.start_at);
  for (var index = 0; index < forwards.length; index++) {
    if (forwards[index].egress_identifier !== null && parents.indexOf(forwards[index].egress_identifier) >= 0) {
      return copyOf(made, COPY_PARENT);
    }
  }
  if (input.readThrough <= chosen.leg.observed_at + exportBoundAt(input, chosen.leg.observed_at)) {
    return made;
  }
  if (wouldJoinAsChild(record, legs, input)) {
    return copyOf(made, COPY_PARENT);
  }
  return unmatched(made, UNMATCHED_REVERSE_UNCONFIRMED);
}

// unjoinedDecision decides a record no candidate was found for: a parent copy (rule 6(i)), a translated copy (rule
// 5), or unmatched.
function unjoinedDecision(record, legs, input) {
  var made = decision(record, JOIN_PENDING);
  if (wouldJoinAsChild(record, legs, input)) {
    return copyOf(made, COPY_PARENT);
  }
  if (record.ingress_role === ROLE_INTERFACE && record.egress_role !== ROLE_LOCAL) {
    var translated = translatedOf(record, legs, input);
    if (translated !== null) {
      copyOf(made, COPY_TRANSLATED);
      made.copy_flow_id = translated.leg.flow_id;
      made.orientation = translated.orientation;
      return made;
    }
    if (firewallHeld(input, record.source, record.start_at) || firewallHeld(input, record.destination, record.start_at)) {
      return copyOf(made, COPY_TRANSLATED);
    }
  }
  if (record.ingress_role === ROLE_UNMAPPED ||
      (record.ingress_role !== ROLE_LOCAL && record.egress_role === ROLE_UNMAPPED)) {
    // The index this record needed names no interface the map holds.
    return unmatched(made, UNMATCHED_INDEX_UNMAPPED);
  }
  if (input.purged && record.start_at - longest(input.timeouts, record.protocol) < input.purgedBefore) {
    // Its leg may have been logged before the horizon and purged: never claimed absent.
    return unmatched(made, UNMATCHED_BEYOND_HORIZON);
  }
  if (inFilterLogGap(record, input)) {
    // The filter log was not read where its leg would be: never claimed absent either.
    return unmatched(made, UNMATCHED_FILTER_LOG_GAP);
  }
  return unmatched(made, UNMATCHED_NO_LOGGED_LEG);
}

// decidePass decides the group once, rule 4 applied to the records already rejected; it answers the decisions and
// every record whose reverse join found no confirming forward record in this pass.
function decidePass(group, legs, input, rejected) {
  var chains = {};
  var chainOf = function (id) {
    if (chains[id] === undefined) {
      chains[id] = { lastEnd: {}, flags: 0 };
    }
    return chains[id];
  };
  var join = function (record, id, orientation) {
    var chain = chainOf(id);
    var held = chain.lastEnd[orientation];
    chain.lastEnd[orientation] = held === undefined ? Math.max(0, record.end_at) : Math.max(held, record.end_at);
    if (record.tcp_flags !== null) {
      chain.flags |= record.tcp_flags;
    }
  };
  var decided = [];
  var forwards = {};
  for (var index = 0; index < group.length; index++) {
    var record = group[index];
    if (record.start_at + input.tolerance >= input.readThrough) {
      break;
    }
    if (input.purged && record.start_at < input.purgedBefore && record.join_state !== JOIN_PENDING) {
      // Decided before the purge passed it: never decided again, and still the chain's history.
      if (record.join_state === JOIN_JOINED && record.joined_flow_id !== null && record.orientation !== null) {
        join(record, record.joined_flow_id, record.orientation);
        if (record.orientation === FORWARD) {
          (forwards[record.joined_flow_id] = forwards[record.joined_flow_id] || []).push(record);
        }
      }
      continue;
    }
    var outcome = joinOne(record, legs, chains, input);
    var chosen = outcome.chosen;
    if (chosen !== null && !(chosen.confirm && rejected[record.identity])) {
      join(record, chosen.leg.flow_id, chosen.orientation);
      if (chosen.orientation === FORWARD) {
        (forwards[chosen.leg.flow_id] = forwards[chosen.leg.flow_id] || []).push(record);
      }
    }
    decided.push({ record: record, outcome: outcome });
  }
  var unconfirmed = {};
  var decisions = decided.map(function (item) {
    var made;
    var picked = item.outcome.chosen;
    if (picked !== null && picked.confirm && !confirmedBy(item.record, forwards[picked.leg.flow_id] || [])) {
      unconfirmed[item.record.identity] = true;
      made = unconfirmedDecision(item.record, picked, forwards[picked.leg.flow_id] || [], legs, input);
    } else if (picked !== null) {
      made = decision(item.record, JOIN_JOINED);
      made.joined_flow_id = picked.leg.flow_id;
      made.orientation = picked.orientation;
    } else if (item.outcome.reason !== "") {
      made = unmatched(decision(item.record, JOIN_PENDING), item.outcome.reason);
    } else {
      made = unjoinedDecision(item.record, legs, input);
    }
    return made;
  });
  return { decisions: decisions, unconfirmed: unconfirmed };
}

// decideGroup decides every record of one group, oldest first: a record starting while the filter log has not been
// read past its start plus the tolerance stays pending, and so does every later one. Rule 4's rejections are found
// by passes over the group until none is added.
function decideGroup(group, legs, input) {
  var rejected = {};
  for (;;) {
    var pass = decidePass(group, legs, input, rejected);
    var grown = false;
    Object.keys(pass.unconfirmed).forEach(function (identity) {
      if (!rejected[identity]) {
        rejected[identity] = true;
        grown = true;
      }
    });
    if (!grown) {
      return pass.decisions;
    }
  }
}

// sameDecision reports whether a decision is what is stored.
function sameDecision(made) {
  var r = made.record;
  return r.join_state === made.join_state && r.joined_flow_id === made.joined_flow_id && r.copy_kind === made.copy_kind &&
    r.copy_flow_id === made.copy_flow_id && r.orientation === made.orientation &&
    r.unmatched_reason === made.unmatched_reason;
}

// prepareLeg reads a leg's addresses as values and its protocol as its IANA number; null for a leg whose tuple
// holds no addresses.
function prepareLeg(leg) {
  leg.source = ip(leg.src_address);
  leg.destination = ip(leg.dst_address);
  if (leg.source === null || leg.destination === null) {
    return null;
  }
  leg.protocolNumber = opnview.protocolNumber(leg.protocol);
  return leg;
}

// LEG_COLUMNS are the columns of the leg dataset the join and the measured volume read.
var LEG_COLUMNS = ["flow_id", "observed_at", "device", "direction", "action", "pair_outcome", "paired_flow_id",
  "src_address", "dst_address", "canonical_src_address", "canonical_dst_address", "src_port", "dst_port", "protocol",
  "ip_version", "dst_is_this_firewall", "ingested_at"];

// legsByKeys reads the legs of some (device, address, address) keys logged at or before an instant, in chunks:
// each read is one statement whatever the number of keys.
function legsByKeys(host, keys, latest) {
  var legs = [];
  chunks(keys, READ_CHUNK).forEach(function (chunk) {
    var read = host.datasets.rows("leg", { keys: { columns: ["device", "canonical_src_address", "canonical_dst_address"],
      values: chunk }, to: latest + 1, columns: LEG_COLUMNS });
    if (read.truncated) {
      throw new Error("the legs of " + chunk.length + " keys are more than one read holds");
    }
    legs = legs.concat(read.rows);
  });
  return legs;
}

// legsByIds reads the legs of some flow ids.
function legsByIds(host, ids, now) {
  var legs = [];
  chunks(ids, READ_CHUNK).forEach(function (chunk) {
    var read = host.datasets.rows("leg", { keys: { columns: ["flow_id"], values: chunk }, to: now + 1,
      columns: LEG_COLUMNS });
    legs = legs.concat(read.rows);
  });
  return legs;
}

// legPages reads every leg a filter keeps logged in [from, to], however many: the dataset answers at most one
// read's rows, ordered by its time column first, so a read that was truncated is followed by one from the last
// instant it held -- its legs at that instant read again and kept once. More legs at one instant than one read
// holds cannot be paged, and fail the run rather than lose any.
function legPages(host, filters, from, to, columns) {
  var legs = [];
  var seen = {};
  for (;;) {
    var read = host.datasets.rows("leg", { filters: filters, from: from, to: to + 1, columns: columns });
    var last = read.rows.length > 0 ? read.rows[read.rows.length - 1].observed_at : null;
    read.rows.forEach(function (leg) {
      if (!seen[leg.flow_id]) {
        seen[leg.flow_id] = true;
        legs.push(leg);
      }
    });
    if (!read.truncated || read.rows.length === 0) {
      return legs;
    }
    if (last === from) {
      throw new Error("more legs were logged at " + from + " than one read of the dataset leg holds");
    }
    from = last;
  }
}

// legsBetween reads every leg logged in [from, to] that a filter keeps.
function legsBetween(host, from, to, filters) {
  return legPages(host, filters, from, to, LEG_COLUMNS);
}

// joinPending decides the records of the groups holding one still pending that started before the filter log's
// read-through minus the tolerance, and answers the decisions that changed.
function joinPending(host, input) {
  var before = input.readThrough - input.tolerance;
  var pendingGroups = {};
  host.query("SELECT DISTINCT tuple_key FROM plugin_netflow.flow_record WHERE join_state = 'pending' " +
    "AND start_at < :before", { before: before }).forEach(function (row) {
      pendingGroups[row.tuple_key] = true;
    });
  Object.keys(overlay).forEach(function (key) {
    var record = overlay[key];
    if (record.join_state === JOIN_PENDING && record.start_at < before && record.tuple_key !== null) {
      pendingGroups[record.tuple_key] = true;
    }
  });
  var groups = Object.keys(pendingGroups).sort(compareText);
  if (groups.length === 0) {
    return [];
  }
  var byGroup = {};
  recordsWhere(host, "tuple_key", groups, "TEXT").forEach(function (record) {
    record.source = ip(record.src_address);
    record.destination = ip(record.dst_address);
    record.parsed = record.source !== null && record.destination !== null;
    (byGroup[record.tuple_key] = byGroup[record.tuple_key] || []).push(record);
  });
  // The legs every group may join or match, in one read per chunk of keys: the legs logged on the devices its
  // records name, of its version, whose two addresses are the records' in either orientation, logged at or before
  // the latest instant one may join.
  var keys = [];
  var seenKey = {};
  var latestOf = {};
  var latestAll = 0;
  groups.forEach(function (group) {
    var records = byGroup[group] || [];
    if (records.length === 0) {
      return;
    }
    var devices = {};
    var latest = 0;
    records.forEach(function (record) {
      latest = Math.max(latest, record.start_at);
      [record.ingress_device, record.egress_device].forEach(function (device) {
        if (device !== null) {
          devices[device] = true;
        }
      });
      childrenOf(input, record).forEach(function (child) { devices[child] = true; });
    });
    latestOf[group] = latest + input.tolerance;
    latestAll = Math.max(latestAll, latestOf[group]);
    var first = records[0];
    if (first.source === null || first.destination === null) {
      return;
    }
    Object.keys(devices).forEach(function (device) {
      [[first.source.address, first.destination.address], [first.destination.address, first.source.address]]
        .forEach(function (pair) {
          var key = device + "\u0000" + pair[0] + "\u0000" + pair[1];
          if (!seenKey[key]) {
            seenKey[key] = true;
            keys.push([device, pair[0], pair[1]]);
          }
        });
    });
    records.devices = devices;
  });
  var legsByPair = {};
  legsByKeys(host, keys, latestAll).forEach(function (leg) {
    if (prepareLeg(leg) === null) {
      return;
    }
    var pair = [leg.source.key, leg.destination.key].sort().join(" ");
    (legsByPair[pair] = legsByPair[pair] || []).push(leg);
  });
  var changed = [];
  groups.forEach(function (group) {
    var records = byGroup[group] || [];
    if (records.length === 0) {
      return;
    }
    var first = records[0];
    var legs = [];
    if (first.source !== null && first.destination !== null) {
      var family = Number(group.split("|")[0]);
      legs = (legsByPair[[first.source.key, first.destination.key].sort().join(" ")] || []).filter(function (leg) {
        return records.devices[leg.device] === true && leg.ip_version === family && leg.observed_at <= latestOf[group];
      });
      legs.sort(function (a, b) { return a.observed_at - b.observed_at || a.flow_id - b.flow_id; });
    }
    decideGroup(records, legs, input).forEach(function (made) {
      if (!sameDecision(made)) {
        changed.push(made);
      }
    });
  });
  return changed;
}

// ---------------------------------------------------------------------------------------------------------------
// The unmatched family (NJ8, PS21): per slot of each period, the records unmatched, by the interfaces their indexes
// name -- the identifier, or the role -- their protocol, version and reason. A record is added to its reason's
// slots, and taken out of its previous reason's when its decision changes. A copy is in no slot.
// ---------------------------------------------------------------------------------------------------------------

var PERIODS = [
  { table: "unmatched_record", start: function (at) { return Math.floor(at / 3600) * 3600; },
    end: function (start) { return start + 3600; } },
  { table: "unmatched_record_24h", start: function (at) { return Math.floor(at / 86400) * 86400; },
    end: function (start) { return start + 86400; } },
  { table: "unmatched_record_7d", start: function (at) {
      // The ISO week, Monday first.
      var day = Math.floor(at / 86400) * 86400;
      var weekday = new Date(day * 1000).getUTCDay();
      return day - ((weekday + 6) % 7) * 86400;
    }, end: function (start) { return start + 7 * 86400; } },
  { table: "unmatched_record_30d", start: function (at) {
      var instant = new Date(at * 1000);
      return Date.UTC(instant.getUTCFullYear(), instant.getUTCMonth(), 1) / 1000;
    }, end: function (start) {
      var instant = new Date(start * 1000);
      return Date.UTC(instant.getUTCFullYear(), instant.getUTCMonth() + 1, 1) / 1000;
    } }
];

// nameOf is how the unmatched family keys an index: the interface's identifier, or its role.
function nameOf(role, identifier) {
  if (role === ROLE_INTERFACE && identifier !== null && identifier !== "") {
    return identifier;
  }
  return role;
}

// countUnmatched adds a record's figures to, or takes them out of (sign -1), the four slots holding its start.
function countUnmatched(rows, record, reason, sign) {
  PERIODS.forEach(function (period) {
    var start = period.start(record.start_at);
    rows[period.table].push({ period_start_at: start, period_end_at: period.end(start),
      ingress_interface: nameOf(record.ingress_role, record.ingress_identifier),
      egress_interface: nameOf(record.egress_role, record.egress_identifier), protocol: record.protocol,
      ip_version: record.ip_version, reason: reason, bytes: sign * record.octets, packets: sign * record.packets,
      records: sign });
  });
}

// applyDecisions writes every changed decision onto its record's row, moves the record between the unmatched
// slots, and answers the legs whose connections gained or lost a record.
function applyDecisions(changed, rows, input) {
  var touched = {};
  changed.forEach(function (made) {
    var record = made.record;
    [record.joined_flow_id, record.copy_flow_id, made.joined_flow_id, made.copy_flow_id].forEach(function (leg) {
      if (leg !== null) {
        touched[leg] = true;
      }
    });
    if (record.join_state === JOIN_UNMATCHED && record.unmatched_reason !== null) {
      countUnmatched(rows, record, record.unmatched_reason, -1);
    }
    if (made.join_state === JOIN_UNMATCHED && made.unmatched_reason !== null) {
      countUnmatched(rows, record, made.unmatched_reason, 1);
    }
    // A translated copy keeps counted_copy while it stays the copy of the same leg in the same orientation; every
    // other change clears it, for the measured volume to set again.
    if (record.copy_flow_id !== made.copy_flow_id || record.orientation !== made.orientation) {
      record.counted_copy = false;
    }
    record.join_state = made.join_state;
    record.joined_flow_id = made.joined_flow_id;
    record.copy_kind = made.copy_kind;
    record.copy_flow_id = made.copy_flow_id;
    record.orientation = made.orientation;
    record.unmatched_reason = made.unmatched_reason;
    record.decided_at = made.join_state === JOIN_PENDING ? null : input.now;
    remember(rows, record);
  });
  return Object.keys(touched).map(Number);
}

// ---------------------------------------------------------------------------------------------------------------
// A logged connection's measured volume: each direction counted once (NJ5).
//
// A connection is a first leg with its second leg, or a single leg. Each direction has an entry (the interface its
// packets arrive by, or the firewall itself) and an exit (the interface they leave by, or the firewall itself), from
// the configuration in force at the connection's start and from its legs. The exporters that can see a direction
// are the entry's ingress and the exit's egress wherever they are exported; on a translated connection an exporter
// on the second leg's interface sees only the translated form, and is excluded. What remains are the counted
// exporters:
//   - exactly one: the direction's figure is the sum of the records joined to the first leg in that orientation, in
//     one of the approved states;
//   - none, and the second leg's interface exports the translated form: the translated copies are counted, and
//     marked counted_copy (the sole-copy rule);
//   - two: forwarding_export_unsettled, with no bytes -- unless the direction is the reverse of a connection pf
//     refused, whose replies nothing expects: not_expected_when_refused (D5);
//   - none otherwise: the first cause that applies, the entry side looked at before the exit side.
// Every choice is made from the configuration alone, never from the records, so a direction is never counted twice.
// A connection that started under no readable, enabled configuration holds no row.
// ---------------------------------------------------------------------------------------------------------------

var SIDE_INTERFACE = "interface";
var SIDE_LOCAL = "local";
var SIDE_UNKNOWN = "unknown";

// connectionsOf builds the connections of first (or single) legs, each with its second leg when the pairing found
// one.
function connectionsOf(host, firsts, now) {
  var seconds = {};
  var secondIDs = [];
  firsts.forEach(function (leg) {
    if (leg.pair_outcome === "first_leg" && leg.paired_flow_id !== null) {
      secondIDs.push(leg.paired_flow_id);
    }
  });
  legsByIds(host, secondIDs, now).forEach(function (leg) { seconds[leg.flow_id] = leg; });
  return firsts.map(function (leg) {
    var second = leg.pair_outcome === "first_leg" && leg.paired_flow_id !== null ? seconds[leg.paired_flow_id] : undefined;
    return { firstID: leg.flow_id, startAt: leg.observed_at, first: leg, secondID: second === undefined ? null :
      second.flow_id, second: second === undefined ? null : second, firstToFirewall: leg.dst_is_this_firewall === 1 };
  });
}

// connectionsOfLegs are the connections some legs belong to: a first or single leg's own, and a second leg's first
// leg's.
function connectionsOfLegs(host, ids, now) {
  if (ids.length === 0) {
    return [];
  }
  var firstIDs = {};
  legsByIds(host, ids, now).forEach(function (leg) {
    firstIDs[leg.pair_outcome === "second_leg" ? leg.paired_flow_id : leg.flow_id] = true;
  });
  var firsts = legsByIds(host, Object.keys(firstIDs).map(Number), now).filter(function (leg) {
    return leg.pair_outcome !== "second_leg";
  });
  return connectionsOf(host, firsts, now);
}

// sameLoggedTuple reports whether two legs logged one tuple: the same addresses, compared as values, and the same
// ports.
function sameLoggedTuple(a, b) {
  var address = function (text) {
    var read = ip(text);
    return read === null ? "" : read.key;
  };
  var port = function (value) { return value === null || value === undefined ? -1 : value; };
  return address(a.src_address) === address(b.src_address) && address(a.dst_address) === address(b.dst_address) &&
    port(a.src_port) === port(b.src_port) && port(a.dst_port) === port(b.dst_port);
}

// planConnection reads the entry and exit of a connection's forward direction from its legs (NJ5's table): a first
// leg `in` on A leaves by its second leg's interface, by the firewall itself when its destination is the firewall's,
// and by an unknown interface otherwise; a single `out` leg on A is firewall-originated, entering from the firewall
// and leaving by A.
function planConnection(connection, identifiers) {
  var interfaceSide = function (device) {
    return { kind: SIDE_INTERFACE, device: device, identifier: identifiers[device] || "" };
  };
  var refused = function (action) { return action === "block" || action === "reject"; };
  var first = connection.first;
  var plan = { forwardEntry: null, forwardExit: null, translated: false, second: null, forwardRefused: false,
    refusedOut: false };
  if (first.direction === "out" && connection.secondID === null) {
    plan.forwardEntry = { kind: SIDE_LOCAL };
    plan.forwardExit = interfaceSide(first.device);
    plan.forwardRefused = refused(first.action);
    plan.refusedOut = refused(first.action);
    return plan;
  }
  plan.forwardEntry = interfaceSide(first.device);
  if (connection.secondID !== null) {
    var second = interfaceSide(connection.second.device);
    plan.forwardExit = second;
    plan.second = second;
    plan.translated = !sameLoggedTuple(first, connection.second);
    plan.refusedOut = refused(connection.second.action);
  } else if (connection.firstToFirewall) {
    plan.forwardExit = { kind: SIDE_LOCAL };
  } else {
    plan.forwardExit = { kind: SIDE_UNKNOWN };
  }
  plan.forwardRefused = plan.refusedOut || refused(first.action);
  return plan;
}

// anyExports reports whether an interface other than a direction's known side exports one side of it: what an
// unknown entry or exit could be counted by.
function anyExports(configuration, ingress, known) {
  var captured = capturedOf(configuration);
  for (var index = 0; index < captured.length; index++) {
    if (known.kind === SIDE_INTERFACE && captured[index].identifier === known.identifier) {
      continue;
    }
    if (exportsDirection(captured[index], ingress)) {
      return true;
    }
  }
  return false;
}

// isParentOf reports whether an exported interface lists an interface in its parent_of: that interface's received
// packets are exported only as parent copies, which are never counted.
function isParentOf(configuration, identifier) {
  if (!identifier) {
    return false;
  }
  var captured = capturedOf(configuration);
  for (var index = 0; index < captured.length; index++) {
    if (exportsDirection(captured[index], true) && captured[index].parentOf.indexOf(identifier) >= 0) {
      return true;
    }
  }
  return false;
}

// sideCause is why one side of a direction counts nothing: the entry's ingress (ingress set) or the exit's egress;
// empty when that side is the firewall itself.
function sideCause(configuration, side, ingress) {
  if (side.kind === SIDE_LOCAL) {
    return "";
  }
  if (side.kind === SIDE_UNKNOWN) {
    return VOLUME_INTERFACE_NOT_CAPTURED;
  }
  var captured = interfaceOf(configuration, side.identifier);
  if (captured === null && ingress && isParentOf(configuration, side.identifier)) {
    return VOLUME_PARENT_COPIES_ONLY;
  }
  if (captured === null) {
    return VOLUME_INTERFACE_NOT_CAPTURED;
  }
  if (ingress && !captured.ingress && captured.egress) {
    return VOLUME_INGRESS_NOT_CAPTURED;
  }
  if (!ingress && !captured.egress) {
    return VOLUME_INTERFACE_NOT_CAPTURED;
  }
  if (captured.exported === "no") {
    return VOLUME_INTERFACE_NOT_EXPORTED;
  }
  if (captured.exported === "unknown") {
    return VOLUME_CAPTURE_UNKNOWN;
  }
  return "";
}

// causeOf is the first cause of NJ5's table that applies to a direction no exporter counts, each row looked at on the
// entry side, then on the exit side.
function causeOf(configuration, entry, exit, refusedOut) {
  if (refusedOut) {
    return VOLUME_NOT_EXPORTED_WHEN_REFUSED;
  }
  var entryCause = sideCause(configuration, entry, true);
  var exitCause = sideCause(configuration, exit, false);
  var order = [VOLUME_INGRESS_NOT_CAPTURED, VOLUME_PARENT_COPIES_ONLY, VOLUME_INTERFACE_NOT_EXPORTED,
    VOLUME_INTERFACE_NOT_CAPTURED, VOLUME_CAPTURE_UNKNOWN];
  for (var index = 0; index < order.length; index++) {
    if (entryCause === order[index] || exitCause === order[index]) {
      return order[index];
    }
  }
  return VOLUME_INTERFACE_NOT_CAPTURED;
}

// measured is a direction one exporter counts, from its records, in one of the approved states: measured,
// in_progress, awaiting_export, missing -- or ambiguous_records when the only records of the direction are ones the
// join could not tell between two legs (N3) -- or export_gap.
function measured(volume, configuration, legAt, records, input, ambiguous) {
  var lastEnd = 0, flags = 0, protocol = 0;
  records.forEach(function (record) {
    volume.bytes += record.octets;
    volume.packets += record.packets;
    volume.records++;
    lastEnd = Math.max(lastEnd, record.end_at);
    protocol = record.protocol;
    if (record.tcp_flags !== null) {
      flags |= record.tcp_flags;
    }
  });
  var bound = input.timeouts.tcpEstablished + input.tolerance;
  if (configuration.activeTimeout !== null) {
    bound = configuration.activeTimeout + input.tolerance;
  }
  var spanEnd = legAt + bound;
  if (volume.records > 0) {
    spanEnd = Math.max(lastEnd, legAt);
  }
  // Each state but a final one says when it may next change by time alone: the read-through instant it falls due at.
  var settle = function (state, due) {
    volume.state = state;
    if (input.readThrough >= due && (state === VOLUME_EXPORT_GAP || state === VOLUME_MEASURED ||
        state === VOLUME_MISSING || state === VOLUME_AMBIGUOUS_RECORDS)) {
      volume.due_at = null;
      return volume;
    }
    volume.due_at = due;
    return volume;
  };
  for (var index = 0; index < input.exportGaps.length; index++) {
    var gap = input.exportGaps[index];
    if (gap.interval_start_at <= spanEnd && gap.interval_end_at >= legAt) {
      return settle(VOLUME_EXPORT_GAP, spanEnd + FINALITY_MARGIN_SECONDS + 1);
    }
  }
  if (volume.records > 0) {
    var limit = idle(input.timeouts, protocol, flags);
    if (input.readThrough - lastEnd > limit) {
      return settle(VOLUME_MEASURED, lastEnd + limit + FINALITY_MARGIN_SECONDS + 1);
    }
    return settle(VOLUME_IN_PROGRESS, lastEnd + limit + 1);
  }
  if (input.readThrough <= legAt + bound) {
    return settle(VOLUME_AWAITING_EXPORT, legAt + bound + 1);
  }
  return settle(ambiguous ? VOLUME_AMBIGUOUS_RECORDS : VOLUME_MISSING, spanEnd + FINALITY_MARGIN_SECONDS + 1);
}

// direction is one direction of one connection (NJ5).
function directionOf(connection, configuration, plan, name, input) {
  var volume = { connection: connection.firstID, connection_start_at: connection.startAt, direction: name, state: "",
    bytes: 0, packets: 0, records: 0, markers: [], due_at: null };
  var final = function (state) {
    volume.state = state;
    return volume;
  };
  var entry = plan.forwardEntry, exit = plan.forwardExit, refused = plan.forwardRefused;
  if (name === REVERSE) {
    entry = plan.forwardExit;
    exit = plan.forwardEntry;
    refused = false;
  }
  var onSecond = function (side) {
    return plan.translated && plan.second !== null && side.kind === SIDE_INTERFACE && side.device === plan.second.device;
  };
  var counted = 0;
  // The entry's ingress.
  if (entry.kind === SIDE_INTERFACE) {
    var entryCaptured = interfaceOf(configuration, entry.identifier);
    if (entryCaptured !== null && exportsDirection(entryCaptured, true) && !onSecond(entry)) {
      counted++;
    }
  } else if (entry.kind === SIDE_UNKNOWN && anyExports(configuration, true, exit)) {
    counted++;
  }
  // The exit's egress: nothing leaves when pf refused the forward packets.
  if (!refused) {
    if (exit.kind === SIDE_INTERFACE) {
      var exitCaptured = interfaceOf(configuration, exit.identifier);
      if (exitCaptured !== null && exportsDirection(exitCaptured, false) && !onSecond(exit)) {
        counted++;
      }
    } else if (exit.kind === SIDE_UNKNOWN && anyExports(configuration, false, entry)) {
      counted++;
    }
  }
  if (counted >= 2) {
    if (name === REVERSE && plan.forwardRefused) {
      // pf refused the connection: no reply is expected, and none is a coverage failure (D5).
      return final(VOLUME_NOT_EXPECTED_WHEN_REFUSED);
    }
    // Untranslated forwarding between two exported interfaces, or an unknown side another interface exports: each
    // packet may be exported once or twice, which no record says (decided question 1).
    return final(VOLUME_FORWARDING_UNSETTLED);
  }
  if (counted === 1) {
    return measured(volume, configuration, connection.startAt,
      recordsIn(input.joined[connection.firstID] || [], name), input,
      ambiguousFor(connection, name, input));
  }
  // The sole-copy rule: no exporter sees the first leg's form, and the second leg's interface exports the translated
  // form of this direction.
  if (plan.translated && plan.second !== null && !refused) {
    var secondCaptured = interfaceOf(configuration, plan.second.identifier);
    if (secondCaptured !== null && exportsDirection(secondCaptured, name === REVERSE)) {
      volume.markers = ["counted_copy"];
      return measured(volume, configuration, connection.startAt,
        recordsIn(input.copies[connection.secondID] || [], name), input, false);
    }
  }
  return final(causeOf(configuration, entry, exit, refused && plan.refusedOut));
}

// recordsIn keeps the records of one orientation.
function recordsIn(records, name) {
  return records.filter(function (record) { return record.orientation === name; });
}

// ambiguousFor reports whether the join found records of a direction it could not tell between two legs (N3): an
// ambiguous record whose tuple is the first leg's in the direction's orientation, started within the leg's export
// bound. A direction with no joined record and such records is not settled missing.
function ambiguousFor(connection, name, input) {
  var candidates = input.ambiguous[connection.firstID];
  if (candidates === undefined) {
    return false;
  }
  return candidates.some(function (candidate) { return candidate.orientation === name; });
}

// ---------------------------------------------------------------------------------------------------------------
// The derivation.
// ---------------------------------------------------------------------------------------------------------------

// progressOf reads the derivation's own progress: what it read up to, by name.
function progressOf(host) {
  var progress = {};
  host.query("SELECT name, number, text FROM plugin_netflow.join_progress", {}).forEach(function (row) {
    progress[row.name] = row;
  });
  return progress;
}

// firewallAddresses reads the addresses discovery holds, or held, for the firewall, by canonical address.
function firewallAddresses(host) {
  var held = {};
  host.datasets.rows("this_firewall_address", { columns: ["address", "first_seen_at", "last_seen_at", "is_current"] })
    .rows.forEach(function (row) {
      var address = ip(row.address);
      if (address === null) {
        return;
      }
      (held[address.address] = held[address.address] || []).push({ from: row.first_seen_at, until: row.last_seen_at,
        current: row.is_current === 1 });
    });
  return held;
}

// gapsOf reads the gaps a kind's providers recorded meeting [from, to].
function gapsOf(host, kind, from, to) {
  return host.datasets.rows("collection_gap", { filters: [{ column: "kind", op: "eq", value: kind },
    { column: "interval_start_at", op: "le", value: to }, { column: "interval_end_at", op: "ge", value: from }],
    columns: ["interval_start_at", "interval_end_at", "reason"] }).rows;
}

// newLegs reads the legs stored since the derivation's last run: the span of their instants is the pairing window a
// filter-log pass asks for (the connections whose first leg lies in it are computed again), and their flow ids the
// legs it has seen at the newest storing instant, so a pass sharing that instant is not read twice.
function newLegs(host, progress, now) {
  var through = progress.legs_ingested_through ? progress.legs_ingested_through.number : -1;
  var seenAt = progress.legs_ingested_through && progress.legs_ingested_through.text ?
    progress.legs_ingested_through.text.split(",").map(Number) : [];
  var seen = {};
  seenAt.forEach(function (id) { seen[id] = true; });
  var legs = legPages(host, [{ column: "ingested_at", op: "ge", value: through }], 0, now,
    ["flow_id", "observed_at", "ingested_at"]).filter(function (leg) {
      return !(leg.ingested_at === through && seen[leg.flow_id]);
    });
  var newest = through;
  legs.forEach(function (leg) { newest = Math.max(newest, leg.ingested_at); });
  var atNewest = newest === through ? seenAt.slice() : [];
  legs.forEach(function (leg) {
    if (leg.ingested_at === newest) {
      atNewest.push(leg.flow_id);
    }
  });
  atNewest.sort(function (a, b) { return a - b; });
  return { legs: legs, progress: { name: "legs_ingested_through", number: newest, text: atNewest.join(",") } };
}

// captureWindow is the window of connections a newly opened capture run asks to compute again: from the last read
// of the run before the first new one, to the newest run's first read (Go's MarkDirty).
function captureWindow(history, progress) {
  var known = progress.capture_newest_first_read_at ? progress.capture_newest_first_read_at.number : null;
  if (history.length === 0) {
    return null;
  }
  var newest = history[history.length - 1];
  if (known !== null && newest.firstReadAt === known) {
    return null;
  }
  var firstNew = history.length - 1;
  while (firstNew > 0 && (known === null || history[firstNew - 1].firstReadAt > known)) {
    firstNew--;
  }
  var from = firstNew > 0 ? history[firstNew - 1].lastReadAt : history[0].firstReadAt;
  return { from: from, to: newest.firstReadAt };
}

// measureConnections computes the measured volume of some connections and answers the measure's rows, the counted
// copies to mark, and the due instants to keep.
function measureConnections(host, connections, history, identifiers, input, rows, measureRows, held) {
  var firstIDs = [], secondIDs = [];
  var low = -1, high = 0;
  connections.forEach(function (connection) {
    firstIDs.push(connection.firstID);
    if (connection.secondID !== null) {
      secondIDs.push(connection.secondID);
    }
    if (low < 0 || connection.startAt < low) {
      low = connection.startAt;
    }
    high = Math.max(high, connection.startAt);
  });
  input.joined = {};
  recordsWhere(host, "joined_flow_id", firstIDs, "BIGINT").forEach(function (record) {
    (input.joined[record.joined_flow_id] = input.joined[record.joined_flow_id] || []).push(record);
  });
  input.copies = {};
  var copyRecords = recordsWhere(host, "copy_flow_id", secondIDs, "BIGINT");
  copyRecords.forEach(function (record) {
    (input.copies[record.copy_flow_id] = input.copies[record.copy_flow_id] || []).push(record);
  });
  // A direction's span reaches at most its last record, or the export bound after its leg.
  var reach = high + input.timeouts.tcpEstablished;
  [input.joined, input.copies].forEach(function (byLeg) {
    Object.keys(byLeg).forEach(function (leg) {
      byLeg[leg].forEach(function (record) { reach = Math.max(reach, record.end_at); });
    });
  });
  input.exportGaps = gapsOf(host, "flow_record", low, reach);
  input.ambiguous = ambiguousCandidates(host, connections, input);
  var marks = {};
  connections.forEach(function (connection) {
    var configuration = configurationAt(history, connection.startAt);
    if (configuration === null || !configuration.enabled || capturedOf(configuration).length === 0) {
      // Started under no readable, enabled configuration: no row (capture_not_known).
      if (connection.secondID !== null) {
        marks[connection.secondID] = { forward: false, reverse: false };
      }
      if (held[connection.firstID] !== undefined && held[connection.firstID].held) {
        [FORWARD, REVERSE].forEach(function (name) {
          measureRows.push({ connection: connection.firstID, direction: name, remove: true });
        });
      }
      rows.measure_due.push({ connection: connection.firstID, connection_start_at: connection.startAt, due_at: null,
        held: false });
      return;
    }
    var plan = planConnection(connection, identifiers);
    var mark = { forward: false, reverse: false };
    var due = null;
    [FORWARD, REVERSE].forEach(function (name) {
      var volume = directionOf(connection, configuration, plan, name, input);
      measureRows.push(volume);
      if (volume.markers.length > 0) {
        mark[name] = true;
      }
      if (volume.due_at !== null) {
        due = due === null ? volume.due_at : Math.min(due, volume.due_at);
      }
    });
    if (connection.secondID !== null) {
      marks[connection.secondID] = mark;
    }
    rows.measure_due.push({ connection: connection.firstID, connection_start_at: connection.startAt, due_at: due,
      held: true });
  });
  // The translated copies of the second legs carry counted_copy exactly where the sole-copy rule counts them.
  copyRecords.forEach(function (record) {
    var mark = marks[record.copy_flow_id];
    if (mark === undefined) {
      return;
    }
    var counted = (record.orientation === FORWARD && mark.forward) || (record.orientation === REVERSE && mark.reverse);
    if (record.counted_copy !== counted) {
      record.counted_copy = counted;
      remember(rows, record);
    }
  });
}

// ambiguousCandidates finds, for each connection, the ambiguous records whose tuple is its first leg's in either
// orientation and that started within its export bound (N3), by orientation.
function ambiguousCandidates(host, connections, input) {
  var byTuple = {};
  var keys = [];
  connections.forEach(function (connection) {
    var first = connection.first;
    var source = ip(first.src_address), destination = ip(first.dst_address);
    var protocol = opnview.protocolNumber(first.protocol);
    if (source === null || destination === null || protocol === null) {
      return;
    }
    var key = tupleKey(first.ip_version, protocol, source, destination, first.src_port, first.dst_port);
    (byTuple[key] = byTuple[key] || []).push({ connection: connection, source: source });
    keys.push(key);
  });
  var found = {};
  if (keys.length === 0) {
    return found;
  }
  var unique = Object.keys(byTuple);
  recordsWhere(host, "tuple_key", unique, "TEXT").filter(function (record) {
    return record.join_state === JOIN_UNMATCHED && record.unmatched_reason === UNMATCHED_AMBIGUOUS;
  }).forEach(function (record) {
      (byTuple[record.tuple_key] || []).forEach(function (candidate) {
        var at = candidate.connection.startAt;
        if (record.start_at < at - input.tolerance || record.start_at > at + exportBoundAt(input, at)) {
          return;
        }
        var source = ip(record.src_address);
        var orientation = source !== null && source.key === candidate.source.key ? FORWARD : REVERSE;
        (found[candidate.connection.firstID] = found[candidate.connection.firstID] || []).push({ orientation: orientation });
      });
    });
  return found;
}

// measureHeld reads, of some connections, which hold measure rows and when each falls due.
function measureHeld(host, ids) {
  var held = {};
  chunks(ids, READ_CHUNK).forEach(function (chunk) {
    host.query("SELECT connection, due_at, held FROM plugin_netflow.measure_due WHERE connection IN (SELECT " +
      "CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:ids AS jsonb)) AS j(value))",
      { ids: JSON.stringify(chunk) }).forEach(function (row) {
        held[row.connection] = row;
      });
  });
  return held;
}

// join is the derivation: it maps what arrived, joins the records the filter log has been read past, measures the
// connections whose figures may have changed, and records the measure's window intervals.
function join(host, run) {
  addressCache = {};
  overlay = {};
  var rows = { flow_record: [], interface_index: [], unmatched_record: [], unmatched_record_24h: [],
    unmatched_record_7d: [], unmatched_record_30d: [], measure_due: [], join_progress: [] };
  var measureRows = [];
  var history = readHistory(host);
  var answer = { rows: rows, measures: { netflow: measureRows } };
  if (history.length > 0) {
    answer.intervals = { netflow: intervalsOf(history) };
  }
  mapReceived(host, run, history, rows);

  var progress = progressOf(host);
  var readThrough = 0;
  host.datasets.rows("read_through", { filters: [{ column: "kind", op: "eq", value: "firewall_log" }],
    columns: ["read_through"] }).rows.forEach(function (row) {
      readThrough = Math.max(readThrough, row.read_through);
    });
  var timeouts = readTimeouts(host);
  if (timeouts === null) {
    // No state timeouts, no decision and no figure: nothing is decided, and nothing seen is forgotten, until a pass
    // reads them.
    return answer;
  }
  var purge = host.datasets.rows("retention_purge", { columns: ["purged_before"] }).rows;
  var devices = {}, identifiers = {};
  host.datasets.rows("interface", { columns: ["identifier", "device"] }).rows.forEach(function (row) {
    devices[row.identifier] = row.device;
    identifiers[row.device] = row.identifier;
  });
  var input = { readThrough: readThrough, tolerance: host.settings.join_tolerance_seconds, now: run.now,
    purged: purge.length > 0, purgedBefore: purge.length > 0 ? purge[0].purged_before : 0, history: history,
    firewall: firewallAddresses(host), devices: devices, timeouts: timeouts, filterLogGaps: [] };

  var touched = [];
  if (readThrough > 0) {
    input.filterLogGaps = gapsOf(host, "firewall_log", 0, readThrough);
    var changed = joinPending(host, input);
    touched = applyDecisions(changed, rows, input);
  }

  // The connections whose figure may have changed: those of the legs stored since the last run (the pairing window
  // they span), those whose joined or copied records changed, those of a window a new capture run covers, and those
  // whose figure falls due by the read-through instant.
  var stored = newLegs(host, progress, run.now);
  var window = captureWindow(history, progress);
  rows.join_progress.push(stored.progress);
  if (history.length > 0) {
    rows.join_progress.push({ name: "capture_newest_first_read_at", number: history[history.length - 1].firstReadAt,
      text: null });
  }
  if (history.length === 0) {
    // No configuration was ever read: no connection started under a known capture.
    return answer;
  }
  var byID = {};
  var addFirsts = function (connections) {
    connections.forEach(function (connection) { byID[connection.firstID] = connection; });
  };
  var windows = [];
  if (stored.legs.length > 0) {
    var low = stored.legs[0].observed_at, high = stored.legs[0].observed_at;
    stored.legs.forEach(function (leg) {
      low = Math.min(low, leg.observed_at);
      high = Math.max(high, leg.observed_at);
    });
    windows.push({ from: low, to: high });
  }
  if (window !== null) {
    windows.push(window);
  }
  if (windows.length > 0) {
    var from = windows[0].from, to = windows[0].to;
    windows.forEach(function (one) {
      from = Math.min(from, one.from);
      to = Math.max(to, one.to);
    });
    addFirsts(connectionsOf(host, legsBetween(host, from, to, []).filter(function (leg) {
      return leg.pair_outcome !== "second_leg";
    }), run.now));
  }
  var due = host.query("SELECT connection FROM plugin_netflow.measure_due WHERE due_at <= :due ORDER BY connection",
    { due: readThrough }).map(function (row) { return row.connection; });
  addFirsts(connectionsOfLegs(host, touched.concat(due), run.now));
  var connections = Object.keys(byID).map(function (id) { return byID[id]; });
  if (connections.length === 0) {
    return answer;
  }
  connections.sort(function (a, b) { return a.firstID - b.firstID; });
  var held = measureHeld(host, connections.map(function (connection) { return connection.firstID; }));
  measureConnections(host, connections, history, identifiers, input, rows, measureRows, held);
  return answer;
}

exports.flow_record = {
  // probe asks whether the export can be read. A firewall whose export is off is still this implementation's: its
  // capture configuration then says so, and the measure records netflow_not_configured rather than nothing.
  probe: function (host) {
    var read = readEnabled(host);
    if (read.error) {
      return { result: read.result, error: read.error };
    }
    if (read.failed) {
      return { result: read.result };
    }
    var result = read.result;
    result.separable = true;
    if (read.enabled) {
      result.state = "reachable";
      result.outcome = "healthy";
    } else {
      result.state = "present_but_disabled";
      result.outcome = "present_but_disabled";
      result.detail = "the NetFlow export captures no interface or sends to no destination";
    }
    return { result: result };
  },

  // collect reads the capture configuration and records it as runs of reads (PS18). A read that fails -- a
  // status, a body without the model's shape -- is recorded as an unreadable configuration with what the firewall
  // answered: the interval is capture_configuration_unknown, never an empty one. It also records how many sequence
  // counters the configuration lets the exporter run (NJ7), the bound of the push entry's sequence_bound.
  collect: function (host, state) {
    var readAt = Math.floor(host.now());
    var read = readCapture(host);
    var outcome = { readable: !read.error, capture: read.capture, outcome: read.result.outcome || "result_failed",
      detail: read.result.detail || "" };
    if (!read.error) {
      var invalid = checkedCapture(read.capture);
      if (invalid !== "") {
        read.result = { probe: read.result.probe, state: "unavailable", outcome: "records_invalid",
          detail: "the flow_record provider netflow: " + invalid };
        outcome = { readable: false, outcome: "records_invalid", detail: read.result.detail };
      }
    } else if (outcome.detail === "") {
      outcome.detail = read.error;
    }
    var recorded = captureRows(state || {}, readAt, outcome);
    var answer = { result: read.result, rows: { capture_configuration: recorded.rows,
      capture_interface: recorded.interfaces }, state: recorded.state };
    if (recorded.rows.length > 0) {
      answer.bounds = { sequence_counters: { counters: recorded.counters, changed: recorded.opened } };
    }
    return answer;
  }
};

exports.derivations = {
  join: join
};

exports.helpers = {
  capture_from: function (config, enabled, interfacesInfo) {
    return captureFrom(config, enabled, interfacesInfo === undefined ? null : interfacesFrom(interfacesInfo));
  },
  index_map: function (interfacesInfo) {
    return indexMapFrom(interfacesInfo);
  },
  map_index: function (interfacesInfo, index) {
    return mapIndex(indexMapFrom(interfacesInfo), index);
  },
  tuple_key: function (version, protocol, source, destination, sourcePort, destinationPort) {
    return tupleKey(version, protocol, ip(source), ip(destination), sourcePort, destinationPort);
  },
  fingerprint: function (capture) {
    return fingerprint(capture);
  },
  intervals: function (configurations) {
    return intervalsOf(configurations);
  },
  configuration_at: function (configurations, at) {
    var found = configurationAt(configurations, at);
    return found === null ? null : found.firstReadAt;
  },
  // decide decides one group of records against some legs, as the join does, from plain objects: the records as
  // the table holds them, the legs as the dataset leg serves them, and the join's inputs -- the read-through
  // instant, the tolerance, pf's timeouts, the firewall's addresses, the interfaces' devices by identifier and
  // the filter log's gaps. It answers each record's decision.
  decide: function (spec) {
    addressCache = {};
    var input = { readThrough: spec.readThrough, tolerance: spec.tolerance, now: spec.readThrough, purged: false,
      purgedBefore: 0, history: [], timeouts: spec.timeouts, devices: spec.devices || {},
      filterLogGaps: spec.gaps || [], firewall: {} };
    (spec.firewall || []).forEach(function (held) {
      var address = ip(held.address);
      (input.firewall[address.address] = input.firewall[address.address] || []).push({ from: held.from,
        until: held.until, current: held.current === true });
    });
    var records = spec.records.map(function (record) {
      record.source = ip(record.src_address);
      record.destination = ip(record.dst_address);
      record.parsed = record.source !== null && record.destination !== null;
      return record;
    });
    var legs = spec.legs.map(prepareLeg).filter(function (leg) { return leg !== null; });
    return decideGroup(records, legs, input).map(function (made) {
      return { join_state: made.join_state, joined_flow_id: made.joined_flow_id, copy_kind: made.copy_kind,
        copy_flow_id: made.copy_flow_id, orientation: made.orientation, unmatched_reason: made.unmatched_reason };
    });
  }
};
