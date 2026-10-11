// top_sites: inferred site names ranked by attributed flows, with the distinct clients that reached
// them, the logged bytes, the country and operator behind them, and the named flows per method
// (docs/widget-catalogue.md, "Top sites"). Withheld whole under aggregate_mode = no_domains
// (decision 6).

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
    // Names are distinct, so no two rows tie.
    list.sort(function (a, b) {
      if (a.flows.value !== b.flows.value) {
        return a.flows.value > b.flows.value ? -1 : 1;
      }
      return compare(a.name, b.name);
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
        total: host.figures.remainder(byName.size, null, totalFlows),
        attribution: window.attribution
      },
      has_rows: list.length > 0
    };
  }
};
