// blocked_by_firewall_rule: rules ranked by blocked connections, each naming its description, its
// pf label, the interfaces it fired on and the interfaces and clients it blocked
// (docs/widget-catalogue.md, "Blocked by firewall rule"). A rid that matches no known rule is a
// first-class row, rule_not_found: a rule can be removed after it logged.

// coverFlows records the coverage of a reading of the flows alone -- from the earliest flow kept to
// now -- and the named state when part of the window lies past the retention horizon; it answers
// the covered interval it recorded, null for none.
function coverFlows(host, state) {
  var earliest = host.envelope.earliest("widget_earliest_flow");
  host.envelope.coverFrom(earliest);
  host.envelope.coverFlowsState(state);
  if (earliest === null) {
    return null;
  }
  var window = host.request.window;
  var covered = { from: Math.max(window.from, earliest), to: Math.min(window.to, host.request.now) };
  return covered.to <= covered.from ? null : covered;
}

// ruleKey is the key a row is accumulated under: the rule's id, or the rid no rule carries.
function ruleKey(ruleId, rid) {
  if (ruleId !== null) {
    return "rule:" + ruleId;
  }
  return "rid:" + (rid === null ? "" : rid);
}

// sortedKeys are an object's keys in byte order.
function sortedKeys(host, object) {
  return Object.keys(object).sort(host.figures.compare);
}

// sortedIds is an id set as a sorted list.
function sortedIds(set) {
  return Object.keys(set).map(Number).sort(function (a, b) { return a - b; });
}

// loggedCount is a count of logged records.
function loggedCount(value) {
  return { measure: "logged", lower_bound: true, value: value };
}

exports.widget = {
  serve: function (host) {
    var covered = coverFlows(host, "rules_beyond_flow_horizon");
    var window = host.request.window;
    var parameters = {
      from: window.from,
      to: window.to,
      client_ids: host.refs.ids("clients"),
      interface_ids: host.refs.ids("interfaces")
    };
    var rules = {};
    var total = 0;
    host.query.rows("widget_blocked_by_rule", parameters).forEach(function (row) {
      var key = ruleKey(row.rule_id, row.rid);
      var entry = rules[key];
      if (entry === undefined) {
        entry = {
          row: { state: "rule_not_found", rule: null, rid: row.rid },
          blocked: 0,
          lastSeenAt: 0,
          automatic: false,
          devices: {},
          interfaces: {},
          clients: {}
        };
        if (row.rule_id !== null) {
          var automatic = row.is_automatic === 1 || row.is_automatic === true;
          entry.row.state = "resolved";
          entry.row.rule = {
            id: row.rule_id,
            pf_label: row.pf_label === null ? "" : row.pf_label,
            description: row.description === null ? "" : row.description,
            action: "",
            automatic: automatic
          };
          entry.automatic = automatic;
        }
        rules[key] = entry;
      }
      entry.blocked += row.records;
      entry.lastSeenAt = Math.max(entry.lastSeenAt, row.newest);
      entry.devices[row.interface_device] = {
        device: row.interface_device,
        description: row.map_description,
        interface_id: row.map_interface_id
      };
      if (row.src_interface_id !== null) {
        entry.interfaces[row.src_interface_id] = true;
      }
      if (row.src_client_id !== null) {
        entry.clients[row.src_client_id] = true;
      }
      total += row.records;
    });

    var includeAutomatic = host.params.flag("include_automatic");
    var excludedMembers = 0;
    var excludedCount = 0;
    var rows = [];
    sortedKeys(host, rules).forEach(function (key) {
      var entry = rules[key];
      if (!includeAutomatic && entry.automatic) {
        excludedMembers++;
        excludedCount += entry.blocked;
        return;
      }
      var firedOn = sortedKeys(host, entry.devices).map(function (device) { return entry.devices[device]; });
      rows.push({
        key: key,
        lastSeenAt: entry.lastSeenAt,
        blocked: entry.blocked,
        clientCount: Object.keys(entry.clients).length,
        row: {
          state: entry.row.state,
          rule: entry.row.rule,
          rid: entry.row.rid,
          fired_on: firedOn.length > 0 ? firedOn : null,
          interfaces: sortedIds(entry.interfaces),
          clients: sortedIds(entry.clients),
          blocked_connections: loggedCount(entry.blocked),
          last_seen_at: entry.lastSeenAt
        }
      });
    });

    // A stable sort on the requested order: rows tied on it keep their key order.
    var order = host.params.text("sort");
    rows = rows.map(function (row, index) { return { row: row, index: index }; });
    rows.sort(function (a, b) {
      var difference = 0;
      switch (order) {
        case "clients":
          difference = b.row.clientCount - a.row.clientCount;
          break;
        case "recency":
          difference = b.row.lastSeenAt - a.row.lastSeenAt;
          break;
        default:
          difference = b.row.blocked - a.row.blocked;
      }
      return difference !== 0 ? difference : a.index - b.index;
    });
    rows = rows.map(function (item) { return item.row; });

    var limit = host.params.integer("limit");
    var shown = [];
    var restMembers = 0;
    var restCount = 0;
    rows.forEach(function (row, index) {
      if (index < limit) {
        shown.push(row);
        return;
      }
      restMembers++;
      restCount += row.blocked;
    });

    // Each shown rule's inline timeline: its blocked records per bucket of the window. A bucket
    // with none reads 0 where the window is covered and no gap meets it, and is a gap otherwise.
    var width = host.buckets.width();
    var counts = {};
    host.query.rows("widget_blocked_rule_buckets", {
      from: parameters.from,
      to: parameters.to,
      client_ids: parameters.client_ids,
      interface_ids: parameters.interface_ids,
      width: width
    }).forEach(function (row) {
      var key = ruleKey(row.rule_id, row.rid);
      if (counts[key] === undefined) {
        counts[key] = {};
      }
      counts[key][row.bucket_start] = (counts[key][row.bucket_start] || 0) + row.records;
    });
    var gaps = host.envelope.gaps();
    var intervals = host.buckets.intervals(window, width);
    shown.forEach(function (row) {
      var byStart = counts[row.key] || {};
      row.row.timeline = intervals.map(function (interval) {
        var start = interval.from - host.buckets.floorMod(interval.from, width);
        var present = Object.prototype.hasOwnProperty.call(byStart, String(start));
        var gap = host.buckets.isGap(interval, covered, gaps);
        return { from: interval.from, to: interval.to, gap: gap, value: present || !gap ? (present ? byStart[start] : 0) : null };
      });
    });

    return {
      data: {
        rows: shown.map(function (row) { return row.row; }),
        remainder: { members: restMembers, count: loggedCount(restCount) },
        excluded_automatic: { members: excludedMembers, count: loggedCount(excludedCount) },
        total: loggedCount(total)
      },
      has_rows: total > 0
    };
  }
};
