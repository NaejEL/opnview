// passed_traffic_world_map: where the ALLOWED traffic went, as point marks sized by a measure, with
// the mandatory unplaced counter beside them (docs/widget-catalogue.md, "Passed-traffic world map").
// Placed volume plus unplaced volume plus the points below min_bytes is the window's allowed total
// for the scope; the unplaced counter is present even at zero and is split by reason.

exports.widget = {
  serve: function (host) { return host.lib.figures.measure.serve(host, serveInMeasure); }
};

// serveInMeasure builds the answer under the measure the figures library put in force: it is run
// once per measure tried, and never names one (ADR 0021, step 5).
function serveInMeasure(host) {
  var volume = host.lib.figures.volume.window(host);
  var scope = host.params.text("scope");
  var reasons = host.lib.figures.peers.reasons;
  var bins = host.lib.figures.peers.bins(host, [reasons.miss, reasons.pending, reasons.no_row, reasons.no_coordinates]);
  if (scope === host.lib.figures.volume.scopes.all) {
    bins.add(reasons.east_west, host.lib.figures.figures.zero(), 0);
    bins.add(reasons.this_firewall, host.lib.figures.figures.zero(), 0);
  }
  var grouped = host.lib.figures.peers.outside(host, volume.rows, scope, host.refs.ids("interfaces"));
  // A row of the scope with no outside peer has no point: it is unplaced, as inter-interface
  // traffic or as this firewall's own.
  grouped.others.forEach(function (row) {
    var ownTraffic = row.traffic_scope === host.lib.figures.volume.scopes.this_firewall;
    var reason = ownTraffic ? reasons.this_firewall : reasons.east_west;
    bins.add(reason, host.lib.figures.mapfilters.allowedOnly(row), 0);
  });

  var sizeBy = host.params.text("size_by");
  var minimum = host.params.integer("min_bytes");
  var points = [];
  // The figures summed into the total and the remainder, each summed in one call (host.lib.figures.figures.sum).
  var placed = [];
  var belowList = [];
  var hasRows = false;
  grouped.peers.forEach(function (peer) {
    var allowed = host.lib.figures.mapfilters.allowedOnly(peer.figures);
    if (allowed.allowed_connections === 0 && allowed.bytes === 0) {
      return;
    }
    hasRows = true;
    var geo = host.lib.figures.services.geolocation(host, peer.address);
    var reason = host.lib.figures.peers.placement(geo);
    if (reason !== "") {
      bins.add(reason, allowed, 1);
      return;
    }
    placed.push(allowed);
    if (allowed.bytes < minimum) {
      belowList.push(allowed);
      return;
    }
    points.push({
      address: peer.address,
      engine: "firewall_rule",
      geo: geo,
      size: sizeBy === "connections" ? allowed.allowed_connections : allowed.bytes,
      volume: host.lib.figures.figures.volume(allowed),
      interfaces: peer.interfaces
    });
  });

  var unplaced = bins.total();
  placed.push(unplaced);
  var total = host.lib.figures.figures.sum(placed);
  if (unplaced.bytes > 0 || unplaced.allowed_connections > 0) {
    hasRows = true;
  }
  return {
    data: {
      size_by: sizeBy,
      points: points,
      below_minimum: host.lib.figures.figures.remainder(belowList.length, host.lib.figures.figures.sum(belowList), null),
      unplaced: bins.list(),
      total: host.lib.figures.figures.volume(total)
    },
    has_rows: hasRows
  };
}
