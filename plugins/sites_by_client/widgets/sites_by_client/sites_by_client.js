// sites_by_client: clients down the side, their top inferred site names across, each cell carrying
// the flow count, the logged bytes and the named flows per method; a client with no attributed flow
// keeps its row and says no site name could be inferred, with its attribution rate beside it
// (docs/widget-catalogue.md, "Sites by client"). A destination no name was inferred for is a cell of
// its own, by its address with its operator and country, ranked with the names and truncated by the
// same sites_per_client and min_flows. Withheld whole under aggregate_mode = no_domains
// (decision 6).

// bySiteRank orders a client's sites and addresses by flows, most first, then the site rows before
// the address rows, each by its name or address in byte order.
function bySiteRank(host) {
  return function (a, b) {
    if (a.flows.value !== b.flows.value) {
      return a.flows.value > b.flows.value ? -1 : 1;
    }
    if (a.kind !== b.kind) {
      return a.kind === "site" ? -1 : 1;
    }
    return a.kind === "site" ? host.figures.compare(a.name, b.name) : host.figures.compare(a.address, b.address);
  };
}

// totalFlows is a client row's eligible flows: those shown, those past the limit and those below
// min_flows, named or by address.
function totalFlows(row) {
  var total = row.remainder.count.value + row.below_min_flows.count.value;
  row.sites.forEach(function (site) {
    total += site.flows.value;
  });
  return total;
}

exports.widget = {
  serve: function (host) {
    var rows = host.sites.rows();
    var interfaces = host.refs.ids("interfaces");
    var clients = host.refs.ids("clients");
    // The site rows of each client, by site name, in the order they were read. A Map, so a site
    // name is never mistaken for a member of an object.
    var perClient = new Map();
    rows.forEach(function (row) {
      if (row.client_id === null) {
        return;
      }
      if (!perClient.has(row.client_id)) {
        perClient.set(row.client_id, new Map());
      }
      var sites = perClient.get(row.client_id);
      if (!sites.has(row.site)) {
        sites.set(row.site, []);
      }
      sites.get(row.site).push(row);
    });
    // The destinations of each client no name was inferred for, by address: each a cell of its own,
    // ranked with the names, never one unnamed bucket (C2).
    var addressesPerClient = new Map();
    var addresses = host.sites.addresses();
    addresses.forEach(function (row) {
      if (row.client_id === null) {
        return;
      }
      if (!addressesPerClient.has(row.client_id)) {
        addressesPerClient.set(row.client_id, new Map());
      }
      var byAddress = addressesPerClient.get(row.client_id);
      if (!byAddress.has(row.address)) {
        byAddress.set(row.address, []);
      }
      byAddress.get(row.address).push(row);
      if (!perClient.has(row.client_id)) {
        perClient.set(row.client_id, new Map());
      }
    });
    // A client with eligible flows and no named one keeps its row.
    host.attribution.clients().forEach(function (id) {
      if (!perClient.has(id)) {
        perClient.set(id, new Map());
      }
    });
    var ids = Array.from(perClient.keys());
    var perRow = host.params.integer("sites_per_client");
    var minimum = host.params.integer("min_flows");
    var showRate = host.params.flag("show_attribution_rate");
    var answer = [];
    ids.forEach(function (id) {
      var info = host.installation.client(id);
      if (info === null || (clients !== null && clients.indexOf(id) < 0) ||
        (interfaces !== null && (info.interface_id === null || interfaces.indexOf(info.interface_id) < 0))) {
        return;
      }
      var sites = [];
      var named = perClient.get(id).size;
      perClient.get(id).forEach(function (siteRows, name) {
        sites.push(host.sites.accumulate(name, siteRows));
      });
      (addressesPerClient.get(id) || new Map()).forEach(function (addressRows, address) {
        sites.push(host.sites.accumulateAddress(address, addressRows));
      });
      sites.sort(bySiteRank(host));
      var shown = [];
      var belowMembers = 0;
      var below = 0;
      var restMembers = 0;
      var rest = 0;
      sites.forEach(function (site) {
        if (site.flows.value < minimum) {
          belowMembers++;
          below += site.flows.value;
        } else if (shown.length < perRow) {
          shown.push(site);
        } else {
          restMembers++;
          rest += site.flows.value;
        }
      });
      // The members in the order the matrix's rows have always been written in.
      var entry = {
        client: info,
        state: named === 0 ? "no_site_name_inferred" : "named",
        sites: shown,
        remainder: host.figures.remainder(restMembers, null, host.figures.count(rest)),
        below_min_flows: host.figures.remainder(belowMembers, null, host.figures.count(below))
      };
      if (showRate) {
        entry.attribution = host.attribution.client(id);
      }
      answer.push(entry);
    });
    // Every order ends on the client id, so no two rows tie.
    answer.sort(function (a, b) {
      var ta = totalFlows(a);
      var tb = totalFlows(b);
      if (ta !== tb) {
        return ta > tb ? -1 : 1;
      }
      return a.client.id - b.client.id;
    });
    return { data: { rows: answer }, has_rows: rows.length > 0 || addresses.length > 0 };
  }
};
