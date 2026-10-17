// OPNsense's NetFlow export: one implementation of the flow_record kind.
//
// WHAT IT READS, AND WHERE IT IS ESTABLISHED (docs/opnsense-api-survey.md, "Push sources", S5, S5c, S6,
// S6c, S9, S10, S11).
//   - The flow records themselves are not read here: the firewall SENDS them, NetFlow v9 over UDP,
//     and the push entry of plugin.json maps each record's IANA Information Elements to the
//     contract's fields with no script at all.
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
//   - Which captured interface is actually exported, and which interfaces' packets another one's node
//     exports again, are read from one read-only action of opnsense/core 26.7.3's
//     Interfaces/Api/OverviewController.php
//     (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php):
//       GET /api/interfaces/overview/interfaces_info/1  interfacesInfoAction answers the standard
//           search envelope; each row is a device's ifconfig details as legacy_interfaces_details
//           parses them (https://github.com/opnsense/core/blob/26.7.3/src/etc/inc/interfaces.lib.inc),
//           with its `identifier` (empty for a device no assignment names). Of a row this script reads
//           `identifier`, `device`, `macaddr` -- the address of ifconfig's `ether` line, all zeros
//           when there is none -- and `vlan.parent`, the "parent interface" of ifconfig's vlan line.
//     None of the three writes anything. setconfig, reconfigure and reset, beside them, are never called.
//
// HOW THE CAPTURE IS MEANT. /etc/rc.d/netflow (opnsense/core 26.7.3, src/etc/rc.d/netflow) creates
// one ng_netflow node per interface of capture.interfaces, and none for an interface listed only in
// capture.egress_only; a node is configured `conf=11` (ingress and egress, accounted once per node) or
// `conf=10` (egress only) when the interface is also in egress_only. So an interface of
// capture.interfaces is captured on egress always, and on ingress unless it is in egress_only.
//
// EVERY INTERFACE MAY BE CAPTURED. The core joins each record from its own interface indexes and counts
// each direction of a connection once, whatever is captured; what it needs from here is, per captured
// interface (S5c and S6c):
//   - exported: the node is hooked to the interface's ng_ether node, which exists only for an
//     interface ifconfig shows an `ether` line for (ng_ether attaches to IFT_ETHER, IFT_L2VLAN and
//     IFT_BRIDGE interfaces, the three types ifconfig's match_ether prints `ether` for). A tunnel such
//     as WireGuard has none, gets no node and exports nothing: "no", for the reason no_export_point.
//     An interface interfaces_info does not list, or a read that failed, is "unknown".
//   - parent_of: the node of an interface captured on ingress receives, on ng_ether's lower hook, the
//     frames of every VLAN whose parent it is, and exports them with itself as the receiving interface
//     (observed 16 October 2026; survey S5). Those VLANs are its parent_of.
// This script reports what the firewall says, and never how many interfaces it should capture on.

var d = opnview.decode;

// The reason an interface that cannot be exported is reported with: its node has no hook to attach to.
var NO_EXPORT_POINT = "no_export_point";

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

exports.flow_record = {
  // probe asks whether the export can be read. A firewall whose export is off is still this
  // implementation's: its capture configuration then says so, and the core records
  // netflow_not_configured rather than nothing.
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

  // capture reads the capture configuration. A read of getconfig or is_enabled that fails -- a status,
  // a body without the model's shape -- is an error with what the firewall answered, which the core
  // records as an unreadable configuration: the interval is capture_configuration_unknown, never an
  // empty one. A read of interfaces_info that fails leaves the configuration readable, with every
  // interface's export unknown.
  capture: function (host) {
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
};

exports.helpers = {
  capture_from: function (config, enabled, interfacesInfo) {
    return captureFrom(config, enabled, interfacesInfo === undefined ? null : interfacesFrom(interfacesInfo));
  }
};
