// blocked_dns_lookups: the names the resolver refused, ranked by count, with the clients that
// asked, the interfaces they sit in, and the list that refused them; the purpose of a list is
// somebody's decision, never inferred from its name, and a refusal attributed to no list is its
// own state (docs/widget-catalogue.md, "Blocked DNS lookups"). A blocked lookup's domain is a
// domain name, withheld under aggregate_mode = no_domains (decision 6); its counts are not.

// lookupCount is a count of resolver lookups, a lower bound like every count opnview reads.
function lookupCount(value) {
  return { measure: "dns_lookups", lower_bound: true, value: value };
}

// idSet is a resolved reference list as a set, or null for no restriction.
function idSet(ids) {
  if (ids === null) {
    return null;
  }
  var set = {};
  ids.forEach(function (id) { set[id] = true; });
  return set;
}

// sortedIds is an id set as a sorted list.
function sortedIds(set) {
  return Object.keys(set).map(Number).sort(function (a, b) { return a - b; });
}

exports.widget = {
  serve: function (host) {
    host.envelope.coverFrom(host.envelope.earliest("widget_earliest_lookup"));
    var interfaces = idSet(host.refs.ids("interfaces"));
    var clients = idSet(host.refs.ids("clients"));
    var lists = host.params.set("blocklists") || [];
    var window = host.request.window;
    var rows = {};
    var total = 0;
    host.query.rows("widget_blocked_lookups", { from: window.from, to: window.to }).forEach(function (row) {
      var listed = row.blocklist_id !== null;
      var state = "not_recorded";
      if (listed && row.purpose !== null) {
        state = row.purpose;
      } else if (listed) {
        state = "unassigned";
      }
      if (!host.params.has("purposes", state) ||
        (lists.length > 0 && (row.list_name === null || lists.indexOf(row.list_name) < 0)) ||
        (clients !== null && (row.client_id === null || !clients[row.client_id])) ||
        (interfaces !== null && (row.interface_id === null || !interfaces[row.interface_id]))) {
        return;
      }
      var key = row.domain + "\x1f" + (listed ? row.blocklist_id : 0) + (listed ? "true" : "false");
      var entry = rows[key];
      if (entry === undefined) {
        entry = {
          domain: row.domain,
          list: row.list_name,
          purpose: state,
          lookups: 0,
          lastSeenAt: 0,
          clients: {},
          addresses: {},
          interfaces: {}
        };
        rows[key] = entry;
      }
      entry.lookups += row.lookups;
      entry.lastSeenAt = Math.max(entry.lastSeenAt, row.newest);
      if (row.client_id !== null) {
        entry.clients[row.client_id] = true;
      } else {
        entry.addresses[row.client_address] = true;
      }
      if (row.interface_id !== null) {
        entry.interfaces[row.interface_id] = true;
      }
      total += row.lookups;
    });

    var noDomains = host.request.no_domains;
    var list = Object.keys(rows).sort(host.figures.compare).map(function (key, index) {
      var entry = rows[key];
      if (noDomains) {
        host.envelope.withhold("rows[].domain");
      }
      return {
        key: key,
        index: index,
        lookups: entry.lookups,
        row: {
          domain: noDomains ? null : entry.domain,
          list: entry.list,
          purpose: entry.purpose,
          lookups: lookupCount(entry.lookups),
          clients: sortedIds(entry.clients),
          unmatched_client_addresses: Object.keys(entry.addresses).sort(host.figures.compare),
          interfaces: sortedIds(entry.interfaces),
          last_seen_at: entry.lastSeenAt
        }
      };
    });
    // Rows tied on their count keep the key order, which is the domain's alphabetical rank; with
    // the domain withheld, a hash of the key orders them instead.
    list.sort(function (a, b) {
      if (a.lookups !== b.lookups) {
        return b.lookups - a.lookups;
      }
      if (noDomains) {
        var byHash = host.figures.nameFreeCompare(a.key, b.key);
        if (byHash !== 0) {
          return byHash;
        }
      }
      return a.index - b.index;
    });

    var limit = host.params.integer("limit");
    var minimum = host.params.integer("min_count");
    var shown = [];
    var belowMembers = 0;
    var below = 0;
    var restMembers = 0;
    var rest = 0;
    list.forEach(function (item) {
      if (item.lookups < minimum) {
        belowMembers++;
        below += item.lookups;
      } else if (shown.length < limit) {
        shown.push(item.row);
      } else {
        restMembers++;
        rest += item.lookups;
      }
    });
    return {
      data: {
        rows: shown,
        remainder: { members: restMembers, count: lookupCount(rest) },
        below_min_count: { members: belowMembers, count: lookupCount(below) },
        total: lookupCount(total)
      },
      has_rows: total > 0
    };
  }
};
