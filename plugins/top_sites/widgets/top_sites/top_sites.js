// top_sites: inferred site names ranked by attributed flows, with the distinct clients that reached
// them, the logged bytes, the country and operator behind them, and the named flows per method
// (docs/widget-catalogue.md, "Top sites"); beside them, ranked with them, every destination address
// no name was inferred for, with its operator and country or the state of its lookup, so the rows,
// the remainder and the names below min_flows make the window's eligible flows. Withheld whole under
// aggregate_mode = no_domains (decision 6).

// withSiteNames is a grouped row: the names it groups written right after its own name, where the
// table's rows have always carried them.
function withSiteNames(row, names) {
  var grouped = { name: row.name, site_names: names };
  Object.keys(row).forEach(function (key) {
    if (key !== "name") {
      grouped[key] = row[key];
    }
  });
  return grouped;
}

// flowsOf is the flows a list of site or address rows sums to: the flows of the row they make.
function flowsOf(rows) {
  var flows = 0;
  rows.forEach(function (row) {
    flows += row.flows;
  });
  return flows;
}

// firstRanked is the first `limit` rows of a list in the order `rank` gives them, a total order: what
// sorting the list and keeping its head gives, with each row compared with the rows kept so far rather
// than the whole list sorted.
function firstRanked(rows, limit, rank) {
  var kept = [];
  if (limit <= 0) {
    return kept;
  }
  rows.forEach(function (row) {
    if (kept.length === limit && rank(row, kept[limit - 1]) >= 0) {
      return;
    }
    var low = 0;
    var high = kept.length;
    while (low < high) {
      var middle = (low + high) >> 1;
      if (rank(row, kept[middle]) < 0) {
        high = middle;
      } else {
        low = middle + 1;
      }
    }
    kept.splice(low, 0, row);
    if (kept.length > limit) {
      kept.pop();
    }
  });
  return kept;
}

exports.widget = {
  serve: function (host) {
    var compare = host.figures.compare;
    var rows = host.sites.rows();
    var grouping = "site_name";
    var groups = null;
    if (host.params.flag("group_by_registrable_domain")) {
      var distinct = new Map();
      rows.forEach(function (row) {
        distinct.set(row.site, true);
      });
      var names = Array.from(distinct.keys()).sort(compare);
      var answer = host.services.registrable(names);
      if (answer.available) {
        groups = answer.groups;
        grouping = "registrable_domain";
      } else {
        host.envelope.state("registrable_domain_grouping_unavailable");
      }
    }
    var nameOf = function (site) {
      return groups === null ? site : groups[site];
    };
    // The site rows of each name the table ranks, and the site names it groups, in the order read.
    var byName = new Map();
    rows.forEach(function (row) {
      var name = nameOf(row.site);
      if (!byName.has(name)) {
        byName.set(name, { rows: [], members: new Map() });
      }
      var entry = byName.get(name);
      entry.rows.push(row);
      entry.members.set(row.site, true);
    });
    // Each name and address is ranked by its flows -- the sum of its rows' -- before any is summed into
    // the row it is written as: only the rows shown are (host.sites.accumulate).
    var list = [];
    var totalFlows = 0;
    byName.forEach(function (entry, name) {
      var flows = flowsOf(entry.rows);
      totalFlows += flows;
      list.push({ kind: "site", name: name, members: entry.members, rows: entry.rows, flows: { value: flows } });
    });
    // A destination no name was inferred for is a row of its own, by its address with its operator
    // and country, ranked with the names: never one unnamed bucket (C2).
    var byAddress = new Map();
    host.sites.addresses().forEach(function (row) {
      if (!byAddress.has(row.address)) {
        byAddress.set(row.address, []);
      }
      byAddress.get(row.address).push(row);
    });
    byAddress.forEach(function (rows, address) {
      var flows = flowsOf(rows);
      totalFlows += flows;
      list.push({ kind: "address", address: address, rows: rows, flows: { value: flows } });
    });
    // A site row and an address row are told apart by kind; within a kind, names and addresses are
    // distinct, so no two rows tie.
    var rank = function (a, b) {
      if (a.flows.value !== b.flows.value) {
        return a.flows.value > b.flows.value ? -1 : 1;
      }
      if (a.kind !== b.kind) {
        return a.kind === "site" ? -1 : 1;
      }
      return a.kind === "site" ? compare(a.name, b.name) : compare(a.address, b.address);
    };
    var minimum = host.params.integer("min_flows");
    var limit = host.params.integer("limit");
    var belowMembers = 0;
    var below = 0;
    var eligible = [];
    var eligibleFlows = 0;
    list.forEach(function (row) {
      if (row.flows.value < minimum) {
        belowMembers++;
        below += row.flows.value;
      } else {
        eligible.push(row);
        eligibleFlows += row.flows.value;
      }
    });
    // The rows shown are the first `limit` of the eligible ones in rank order: only they are ranked
    // among themselves, so the window's thousands of names are never sorted whole.
    var first = firstRanked(eligible, limit, rank);
    var isShown = new Map();
    first.forEach(function (entry) {
      isShown.set(entry, true);
    });
    var shown = first.map(function (entry) {
      if (entry.kind === "address") {
        return host.sites.accumulateAddress(entry.address, entry.rows);
      }
      var row = host.sites.accumulate(entry.name, entry.rows);
      return groups === null ? row : withSiteNames(row, Array.from(entry.members.keys()).sort(compare));
    });
    // The rows of every name and address not shown are judged as the rows shown are
    // (host.figures.cover): their flows are counted in the remainders. A logged answer has nothing to
    // judge.
    if (host.figures.measure !== host.figures.measures.logged) {
      list.forEach(function (entry) {
        if (!isShown.has(entry)) {
          host.figures.cover(entry.rows);
        }
      });
    }
    var restMembers = eligible.length - shown.length;
    var rest = eligibleFlows;
    shown.forEach(function (row) {
      rest -= row.flows.value;
    });
    var window = host.attribution.window();
    // The members in the order the table has always been written in.
    return {
      data: {
        grouping: grouping,
        rows: shown,
        remainder: host.figures.remainder(restMembers, null, host.figures.count(rest)),
        below_min_flows: host.figures.remainder(belowMembers, null, host.figures.count(below)),
        total: host.figures.remainder(list.length, null, host.figures.count(totalFlows)),
        attribution: window.attribution
      },
      has_rows: byName.size > 0 || byAddress.size > 0
    };
  }
};
