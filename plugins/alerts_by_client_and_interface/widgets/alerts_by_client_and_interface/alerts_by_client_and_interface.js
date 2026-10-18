// alerts_by_client_and_interface: which machine, in which interface, the detection engine is
// complaining about (docs/widget-catalogue.md, "Alerts by client and interface"). One row per client
// at EITHER end of the window's security events (decision D2 of specs/SPEC-step-6-alerts.md): many
// detections fire on the server-to-client packet, where the client is the destination, so a row
// splits its count into the events it was the source of and the events it was the destination of.
//
// THREE KINDS OF ROW. A client; this firewall, when it is an end, as a row of its own; and an event
// with no client and no end at this firewall, as a row labelled by its source address, shown whole
// and kept while include_unmatched is set -- dropping it would under-report.
//
// THE ROWS NEED NOT SUM TO THE TOTAL. An event between two clients counts under both, so the
// distinct-event total is served apart from the rows, and is what the window holds.
//
// The answer is the table shape's contract (docs/widget-catalogue.md, Part 1, "Table"): the columns
// by name, each row its cells in that order, and the rows the limit dropped as a counted remainder.

// severityOrder is opnview's severity vocabulary, most severe first (docs/data-model.md, "Severity:
// one ordered vocabulary"); unknown ranks after every severity.
var severityOrder = ["critical", "high", "medium", "low", "informational"];

// topSignatures is how many signatures a row names.
var topSignatures = 3;

// severityRank is a severity's place in the order, unknown last.
function severityRank(severity) {
  var index = severityOrder.indexOf(severity);
  return index < 0 ? severityOrder.length : index;
}

// severityOf is an event group's severity: its own, else the cache's answer, else unknown.
function severityOf(row) {
  if (row.event_severity !== null) {
    return row.event_severity;
  }
  if (row.lookup_state === "found" && row.rule_severity !== null) {
    return row.rule_severity;
  }
  return "unknown";
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
var columns = ["subject_kind", "client_id", "address", "events", "as_source", "as_destination", "worst_severity",
  "interfaces", "top_signatures", "last_seen_at"];

exports.widget = {
  serve: function (host) {
    host.envelope.coverFrom(host.envelope.earliest("widget_earliest_security_event"));
    var window = host.request.window;
    var wanted = {};
    (host.params.set("severities") || []).forEach(function (severity) { wanted[severity] = true; });
    var includeUnmatched = host.params.flag("include_unmatched");

    var subjects = {};
    var distinct = 0;
    // attribute counts one event group under one subject, at one end.
    var attribute = function (key, kind, clientId, address, end, interfaceId, row, severity) {
      var subject = subjects[key];
      if (subject === undefined) {
        subject = { kind: kind, clientId: clientId, address: address, source: 0, destination: 0, worst: "unknown",
          interfaces: {}, signatures: {}, lastSeenAt: 0 };
        subjects[key] = subject;
      }
      if (end === "source") {
        subject.source += row.events;
      } else {
        subject.destination += row.events;
      }
      if (severityRank(severity) < severityRank(subject.worst)) {
        subject.worst = severity;
      }
      if (interfaceId !== null) {
        subject.interfaces[interfaceId] = true;
      }
      var signatureKey = row.provider_id + "\u0000" + row.rule_identity;
      var signature = subject.signatures[signatureKey];
      if (signature === undefined) {
        signature = { key: signatureKey, provider_kind: row.provider_kind, provider_key: row.provider_key,
          rule_identity: row.rule_identity, signature: row.signature, at: -1, events: 0 };
        subject.signatures[signatureKey] = signature;
      }
      signature.events += row.events;
      if (row.newest > signature.at) {
        signature.signature = row.signature;
        signature.at = row.newest;
      }
      subject.lastSeenAt = Math.max(subject.lastSeenAt, row.newest);
    };

    host.query.rows("widget_alerts_by_client_rows", {
      from: window.from,
      to: window.to,
      interface_ids: host.refs.ids("interfaces")
    }).forEach(function (row) {
      var severity = severityOf(row);
      if (!wanted[severity]) {
        return;
      }
      distinct += row.events;
      if (row.unmatched_address !== null) {
        var interfaceId = row.src_interface_id !== null ? row.src_interface_id : row.dst_interface_id;
        attribute("unmatched:" + row.unmatched_address, "unmatched", null, row.unmatched_address, "source",
          interfaceId, row, severity);
        if (row.src_interface_id !== null && row.dst_interface_id !== null) {
          subjects["unmatched:" + row.unmatched_address].interfaces[row.dst_interface_id] = true;
        }
        return;
      }
      if (row.src_client_id !== null) {
        attribute("client:" + row.src_client_id, "client", row.src_client_id, null, "source", row.src_interface_id,
          row, severity);
      } else if (row.src_is_this_firewall === 1) {
        attribute("this_firewall", "this_firewall", null, null, "source", row.src_interface_id, row, severity);
      }
      if (row.dst_client_id !== null) {
        attribute("client:" + row.dst_client_id, "client", row.dst_client_id, null, "destination", row.dst_interface_id,
          row, severity);
      } else if (row.dst_is_this_firewall === 1) {
        attribute("this_firewall", "this_firewall", null, null, "destination", row.dst_interface_id, row, severity);
      }
    });

    var entries = [];
    var excludedMembers = 0;
    var excludedEvents = 0;
    Object.keys(subjects).sort(host.figures.compare).forEach(function (key) {
      var subject = subjects[key];
      if (subject.kind === "unmatched" && !includeUnmatched) {
        excludedMembers++;
        excludedEvents += subject.source + subject.destination;
        return;
      }
      entries.push(subject);
    });

    // A stable sort on the requested order: entries tied on it keep their key order.
    var order = host.params.text("sort");
    entries = entries.map(function (entry, index) { return { entry: entry, index: index }; });
    entries.sort(function (a, b) {
      var difference = 0;
      var countA = a.entry.source + a.entry.destination;
      var countB = b.entry.source + b.entry.destination;
      if (order === "worst_severity") {
        difference = severityRank(a.entry.worst) - severityRank(b.entry.worst);
      }
      if (difference === 0) {
        difference = countB - countA;
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
      var count = entry.source + entry.destination;
      if (index >= limit) {
        restMembers++;
        restEvents += count;
        return;
      }
      var interfaces = sortedIds(entry.interfaces);
      interfaces.forEach(function (id) { interfaceIds[id] = true; });
      if (entry.clientId !== null) {
        clientIds[entry.clientId] = true;
      }
      var signatures = Object.keys(entry.signatures).map(function (key) { return entry.signatures[key]; });
      signatures.sort(function (a, b) {
        return b.events !== a.events ? b.events - a.events : host.figures.compare(a.key, b.key);
      });
      rows.push([
        entry.kind,
        entry.clientId,
        entry.address,
        eventCount(count),
        eventCount(entry.source),
        eventCount(entry.destination),
        entry.worst,
        interfaces,
        signatures.slice(0, topSignatures).map(function (signature) {
          return { provider_kind: signature.provider_kind, provider_key: signature.provider_key,
            rule_identity: signature.rule_identity, signature: signature.signature,
            events: eventCount(signature.events) };
        }),
        entry.lastSeenAt
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
        excluded_unmatched: { members: excludedMembers, events: eventCount(excludedEvents) },
        distinct_events: eventCount(distinct),
        clients: named.clients,
        interfaces: named.interfaces
      },
      has_rows: distinct > 0
    };
  }
};
