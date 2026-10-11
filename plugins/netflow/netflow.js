// OPNsense's NetFlow export: one implementation of the flow_record kind.
//
// WHAT IT READS, AND WHERE IT IS ESTABLISHED (docs/opnsense-api-survey.md, "Push sources", S5, S6,
// S9, S10, S11).
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
//     Neither writes anything. setconfig, reconfigure and reset, beside them, are never called.
//
// HOW THE CAPTURE IS MEANT. /etc/rc.d/netflow (opnsense/core 26.7.3, src/etc/rc.d/netflow) creates
// one ng_netflow node per interface of capture.interfaces, and none for an interface listed only in
// capture.egress_only; a node is configured `conf=11` (ingress and egress, accounted once per node) or
// `conf=10` (egress only) when the interface is also in egress_only. So an interface of
// capture.interfaces is captured on egress always, and on ingress unless it is in egress_only.
//
// WHAT IS NOT HERE. Whether the records of one interface can be told from another's, and what a record
// says of its direction: neither is exported (S11), and the core reads an Observation Point only from
// the configuration this script reports. This script reports the configuration as it is, and never
// how many interfaces it should capture on.

var d = opnview.decode;

// options returns the keys of an option list whose `selected` is set, sorted.
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

// captureFrom builds the capture configuration from getconfig's answer and is_enabled's; null when
// the answer does not have the model's shape.
function captureFrom(config, enabled) {
  if (config === null || typeof config !== "object" || config.netflow === null ||
      typeof config.netflow !== "object") {
    return null;
  }
  var netflow = config.netflow;
  var capture = netflow.capture;
  if (capture === null || typeof capture !== "object") {
    return null;
  }
  var interfaces = selectedOptions(capture.interfaces);
  var egressOnly = selectedOptions(capture.egress_only);
  if (interfaces === null || egressOnly === null) {
    return null;
  }
  var versions = selectedOptions(capture.version);
  var result = {
    enabled: enabled,
    interfaces: interfaces.map(function (identifier) {
      return { identifier: identifier, ingress: egressOnly.indexOf(identifier) < 0, egress: true };
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

  // capture reads the capture configuration. A read that fails -- a status, a body without the
  // model's shape -- is an error with what the firewall answered, which the core records as an
  // unreadable configuration: the interval is capture_configuration_unknown, never an empty one.
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
    var capture = captureFrom(response.decodable ? response.value : null, enabled.enabled);
    if (capture === null) {
      result.outcome = "undecodable";
      result.detail = "HTTP 200 with a body that is not the NetFlow model's settings";
      return { result: result, error: "the capture configuration is not the model's shape" };
    }
    result.state = "reachable";
    result.outcome = "healthy";
    result.separable = true;
    return { result: result, capture: capture };
  }
};

exports.helpers = {
  capture_from: function (config, enabled) { return captureFrom(config, enabled); }
};
