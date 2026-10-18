// alerts_by_signature: one row per rule a security-event provider fired -- its signature, how often,
// when it last fired, how serious it is, its category and ruleset, and the clients and interfaces at
// either end (docs/widget-catalogue.md, "Alerts by signature"). Several providers of the kind may be
// active at once, so a row is a (provider, rule identity) pair and names its provider.
//
// THE SEVERITY IS NEVER GUESSED. It is the event's own when the provider ships one, else the rule-info
// cache's answer; with neither it is unknown, and the row says why: the rule was never fetched, the
// provider does not know it, or the provider knows it and reports no severity. The raw value the
// provider reported is served beside the normalised one.
//
// The answer is the table shape's contract (docs/widget-catalogue.md, Part 1, "Table"): the columns
// by name, each row its cells in that order, and the rows the limit dropped as a counted remainder.

// severityOrder is opnview's severity vocabulary, most severe first (docs/data-model.md, "Severity:
// one ordered vocabulary"); unknown ranks after every severity.
var severityOrder = ["critical", "high", "medium", "low", "informational"];

// severityRank is a severity's place in the order, unknown last.
function severityRank(severity) {
  var index = severity === null ? -1 : severityOrder.indexOf(severity);
  return index < 0 ? severityOrder.length : index;
}

// resolveSeverity resolves one group's severity: the event's own first, then the cache's answer, and
// otherwise unknown with the reason.
function resolveSeverity(row) {
  if (row.event_severity !== null) {
    return { severity: row.event_severity, providerSeverity: null, state: "resolved", reason: null };
  }
  if (row.lookup_state === null) {
    return { severity: null, providerSeverity: null, state: "unknown", reason: "not_fetched" };
  }
  if (row.lookup_state !== "found") {
    return { severity: null, providerSeverity: null, state: "unknown", reason: "rule_not_found" };
  }
  if (row.rule_severity === null) {
    return { severity: null, providerSeverity: row.provider_severity, state: "unknown",
      reason: "not_reported_by_provider" };
  }
  return { severity: row.rule_severity, providerSeverity: row.provider_severity, state: "resolved", reason: null };
}

// observedState is the state of the device an event was observed on: resolved to a discovered
// interface, a device no discovered interface carries (a netmap host-ring device in IPS mode, for
// one), or not reported by the provider.
function observedState(row) {
  if (row.in_interface_device === null) {
    return "not_reported";
  }
  return row.in_interface_id === null ? "not_found" : "resolved";
}

// eventCount is a count of security events, a lower bound like every count of logged material.
function eventCount(value) {
  return { measure: "security_events", lower_bound: true, value: value };
}

// sortedIds is an id set as a sorted list.
function sortedIds(set) {
  return Object.keys(set).map(Number).sort(function (a, b) { return a - b; });
}

// columns are the table's columns, in the order every row's cells are written.
var columns = ["provider_kind", "provider_key", "provider_display_name", "rule_identity", "signature", "events",
  "last_seen_at", "normalised_severity", "provider_severity", "severity_state", "severity_reason", "category",
  "rule_source", "clients", "interfaces", "this_firewall", "observed_on"];

exports.widget = {
  serve: function (host) {
    host.envelope.coverFrom(host.envelope.earliest("widget_earliest_security_event"));
    var window = host.request.window;
    var wanted = {};
    (host.params.set("severities") || []).forEach(function (severity) { wanted[severity] = true; });

    var groups = {};
    var keys = [];
    var total = 0;
    host.query.rows("widget_alerts_by_signature_rows", {
      from: window.from,
      to: window.to,
      provider_ids: host.refs.ids("providers"),
      client_ids: host.refs.ids("clients"),
      interface_ids: host.refs.ids("interfaces")
    }).forEach(function (row) {
      var severity = resolveSeverity(row);
      if (!wanted[severity.state === "resolved" ? severity.severity : "unknown"]) {
        return;
      }
      var key = row.provider_id + "\u0000" + row.rule_identity;
      var group = groups[key];
      if (group === undefined) {
        group = {
          providerId: row.provider_id,
          providerKind: row.provider_kind,
          providerKey: row.provider_key,
          displayName: row.display_name,
          ruleIdentity: row.rule_identity,
          signature: row.signature,
          signatureAt: -1,
          severity: severity,
          category: row.lookup_state === "found" ? row.category : null,
          ruleSource: row.lookup_state === "found" ? row.rule_source : null,
          events: 0,
          lastSeenAt: 0,
          clients: {},
          interfaces: {},
          thisFirewall: false,
          observed: {}
        };
        groups[key] = group;
        keys.push(key);
      }
      // The signature text of the newest event: a provider may rewrite a rule's text between two
      // rulesets, and the newest is what the rule says now. Ties keep the first in byte order.
      if (row.newest > group.signatureAt ||
          (row.newest === group.signatureAt && host.figures.compare(row.signature, group.signature) < 0)) {
        group.signature = row.signature;
        group.signatureAt = row.newest;
      }
      // An event's own severity outranks the cache's within one rule, and a resolved one an unknown.
      if (severity.state === "resolved" && (group.severity.state !== "resolved" ||
          (row.event_severity !== null && severityRank(severity.severity) < severityRank(group.severity.severity)))) {
        group.severity = severity;
      }
      group.events += row.events;
      group.lastSeenAt = Math.max(group.lastSeenAt, row.newest);
      [row.src_client_id, row.dst_client_id].forEach(function (id) {
        if (id !== null) {
          group.clients[id] = true;
        }
      });
      [row.src_interface_id, row.dst_interface_id].forEach(function (id) {
        if (id !== null) {
          group.interfaces[id] = true;
        }
      });
      group.thisFirewall = group.thisFirewall || row.src_is_this_firewall === 1 || row.dst_is_this_firewall === 1;
      var device = row.in_interface_device === null ? "" : row.in_interface_device;
      group.observed[device] = { device: row.in_interface_device, interface_id: row.in_interface_id,
        state: observedState(row) };
      total += row.events;
    });

    var entries = keys.sort(host.figures.compare).map(function (key) { return groups[key]; });
    // A stable sort on the requested order: entries tied on it keep their key order.
    var order = host.params.text("sort");
    entries = entries.map(function (entry, index) { return { entry: entry, index: index }; });
    entries.sort(function (a, b) {
      var difference = 0;
      switch (order) {
        case "severity":
          difference = severityRank(a.entry.severity.severity) - severityRank(b.entry.severity.severity);
          if (difference === 0) {
            difference = b.entry.events - a.entry.events;
          }
          break;
        case "recency":
          difference = b.entry.lastSeenAt - a.entry.lastSeenAt;
          break;
        default:
          difference = b.entry.events - a.entry.events;
      }
      return difference !== 0 ? difference : a.index - b.index;
    });
    entries = entries.map(function (item) { return item.entry; });

    var limit = host.params.integer("limit");
    var rows = [];
    var restMembers = 0;
    var restEvents = 0;
    var clientIds = {};
    var interfaceIds = {};
    entries.forEach(function (entry, index) {
      if (index >= limit) {
        restMembers++;
        restEvents += entry.events;
        return;
      }
      var clients = sortedIds(entry.clients);
      var interfaces = sortedIds(entry.interfaces);
      clients.forEach(function (id) { clientIds[id] = true; });
      interfaces.forEach(function (id) { interfaceIds[id] = true; });
      Object.keys(entry.observed).forEach(function (device) {
        var interfaceId = entry.observed[device].interface_id;
        if (interfaceId !== null) {
          interfaceIds[interfaceId] = true;
        }
      });
      rows.push([
        entry.providerKind,
        entry.providerKey,
        entry.displayName,
        entry.ruleIdentity,
        entry.signature,
        eventCount(entry.events),
        entry.lastSeenAt,
        entry.severity.severity,
        entry.severity.providerSeverity,
        entry.severity.state,
        entry.severity.reason,
        entry.category,
        entry.ruleSource,
        clients,
        interfaces,
        entry.thisFirewall,
        Object.keys(entry.observed).sort(host.figures.compare).map(function (device) { return entry.observed[device]; })
      ]);
    });

    // The clients and interfaces the shown rows name, as the installation knows them, so a renderer
    // names them by the data and never by an id.
    var named = { clients: [], interfaces: [] };
    sortedIds(clientIds).forEach(function (id) {
      var info = host.installation.client(id);
      if (info !== null) {
        named.clients.push(info);
      }
    });
    sortedIds(interfaceIds).forEach(function (id) {
      var info = host.installation.interface(id);
      if (info !== null) {
        named.interfaces.push(info);
      }
    });

    return {
      data: {
        columns: columns,
        rows: rows,
        remainder: { members: restMembers, events: eventCount(restEvents) },
        total: eventCount(total),
        clients: named.clients,
        interfaces: named.interfaces
      },
      has_rows: total > 0
    };
  }
};
