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
    var list = [];
    var totalFlows = 0;
    byName.forEach(function (entry, name) {
      var row = host.sites.accumulate(name, entry.rows);
      if (groups !== null) {
        row = withSiteNames(row, Array.from(entry.members.keys()).sort(compare));
      }
      totalFlows += row.flows.value;
      list.push(row);
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
      var row = host.sites.accumulateAddress(address, rows);
      totalFlows += row.flows.value;
      list.push(row);
    });
    // A site row and an address row are told apart by kind; within a kind, names and addresses are
    // distinct, so no two rows tie.
    list.sort(function (a, b) {
      if (a.flows.value !== b.flows.value) {
        return a.flows.value > b.flows.value ? -1 : 1;
      }
      if (a.kind !== b.kind) {
        return a.kind === "site" ? -1 : 1;
      }
      return a.kind === "site" ? compare(a.name, b.name) : compare(a.address, b.address);
    });
    var minimum = host.params.integer("min_flows");
    var limit = host.params.integer("limit");
    var shown = [];
    var belowMembers = 0;
    var below = 0;
    var restMembers = 0;
    var rest = 0;
    list.forEach(function (row) {
      if (row.flows.value < minimum) {
        belowMembers++;
        below += row.flows.value;
      } else if (shown.length < limit) {
        shown.push(row);
      } else {
        restMembers++;
        rest += row.flows.value;
      }
    });
    var window = host.attribution.window();
    // The members in the order the table has always been written in.
    return {
      data: {
        grouping: grouping,
        rows: shown,
        remainder: host.figures.remainder(restMembers, null, rest),
        below_min_flows: host.figures.remainder(belowMembers, null, below),
        total: host.figures.remainder(list.length, null, totalFlows),
        attribution: window.attribution
      },
      has_rows: byName.size > 0 || byAddress.size > 0
    };
  }
};
