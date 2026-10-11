// attribution_rate_per_client: per client, how many of its outbound flows could be named at all, by
// which method, with what delay, and whether the resolver's cache was read over the window
// (docs/widget-catalogue.md, "Attribution rate per client"). The counts are the core's
// (host.attribution), per client. A rate is UNDEFINED -- null, never 0 -- when no resolver source
// answered for the window or the client has no eligible flow.

// rateKey is a row's rank under worst_first and best_first: an undefined rate ranks as -1, so it
// sorts first under worst_first and last under best_first -- it is the least trustworthy of all.
function rateKey(row) {
  return row.rate === null ? -1 : row.rate;
}

exports.widget = {
  serve: function (host) {
    host.envelope.coverFlows("attribution_beyond_flow_horizon");
    var window = host.attribution.window();
    var interfaces = host.refs.ids("interfaces");
    var clients = host.refs.ids("clients");
    var rows = [];
    host.attribution.clients().forEach(function (id) {
      var row = host.attribution.client(id);
      if (row === null || (clients !== null && clients.indexOf(id) < 0) ||
        (interfaces !== null && (row.client.interface_id === null || interfaces.indexOf(row.client.interface_id) < 0))) {
        return;
      }
      rows.push(row);
    });
    var order = host.params.text("sort");
    // Every order ends on the client id, so no two rows tie.
    rows.sort(function (a, b) {
      if (order === "by_volume") {
        if (a.eligible_bytes.value !== b.eligible_bytes.value) {
          return a.eligible_bytes.value > b.eligible_bytes.value ? -1 : 1;
        }
      } else {
        var ra = rateKey(a);
        var rb = rateKey(b);
        if (ra !== rb) {
          if (order === "best_first") {
            return ra > rb ? -1 : 1;
          }
          return ra < rb ? -1 : 1;
        }
      }
      return a.client.id - b.client.id;
    });
    var limit = host.params.integer("limit");
    var shown = [];
    var members = 0;
    var rest = 0;
    rows.forEach(function (row, index) {
      if (index < limit) {
        shown.push(row);
        return;
      }
      members++;
      rest += row.eligible_flows.value;
    });
    return {
      data: {
        resolver_covered: window.resolver_covered,
        cache_covered: window.cache_covered,
        rows: shown,
        remainder: host.figures.remainder(members, null, host.figures.count(rest))
      },
      has_rows: rows.length > 0
    };
  }
};
