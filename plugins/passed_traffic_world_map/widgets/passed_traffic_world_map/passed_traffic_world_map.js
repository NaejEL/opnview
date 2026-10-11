// passed_traffic_world_map: where the ALLOWED traffic went, as point marks sized by a measure, with
// the mandatory unplaced counter beside them (docs/widget-catalogue.md, "Passed-traffic world map").
// Placed volume plus unplaced volume plus the points below min_bytes is the window's allowed total
// for the scope; the unplaced counter is present even at zero and is split by reason.

exports.widget = {
  serve: function (host) {
    var volume = host.volume.window();
    var scope = host.params.text("scope");
    var reasons = host.peers.reasons;
    var bins = host.peers.bins([reasons.miss, reasons.pending, reasons.no_row, reasons.no_coordinates]);
    if (scope === host.volume.scopes.all) {
      bins.add(reasons.east_west, host.figures.zero(), 0);
      bins.add(reasons.this_firewall, host.figures.zero(), 0);
    }
    var grouped = host.peers.outside(volume.rows, scope, host.refs.ids("interfaces"));
    // A row of the scope with no outside peer has no point: it is unplaced, as inter-interface
    // traffic or as this firewall's own.
    grouped.others.forEach(function (row) {
      var ownTraffic = row.traffic_scope === host.volume.scopes.this_firewall;
      var reason = ownTraffic ? reasons.this_firewall : reasons.east_west;
      bins.add(reason, host.mapfilters.allowedOnly(row), 0);
    });

    var sizeBy = host.params.text("size_by");
    var minimum = host.params.integer("min_bytes");
    var points = [];
    var belowMembers = 0;
    var total = host.figures.zero();
    var below = host.figures.zero();
    var hasRows = false;
    grouped.peers.forEach(function (peer) {
      var allowed = host.mapfilters.allowedOnly(peer.figures);
      if (allowed.allowed_connections === 0 && allowed.bytes === 0) {
        return;
      }
      hasRows = true;
      var geo = host.services.geolocation(peer.address);
      var reason = host.peers.placement(geo);
      if (reason !== "") {
        bins.add(reason, allowed, 1);
        return;
      }
      total = host.figures.add(total, allowed);
      if (allowed.bytes < minimum) {
        belowMembers++;
        below = host.figures.add(below, allowed);
        return;
      }
      points.push({
        address: peer.address,
        engine: "firewall_rule",
        geo: geo,
        size: sizeBy === "connections" ? allowed.allowed_connections : allowed.bytes,
        volume: host.figures.logged(allowed),
        interfaces: peer.interfaces
      });
    });

    var unplaced = bins.total();
    total = host.figures.add(total, unplaced);
    if (unplaced.bytes > 0 || unplaced.allowed_connections > 0) {
      hasRows = true;
    }
    return {
      data: {
        size_by: sizeBy,
        points: points,
        below_minimum: host.figures.remainder(belowMembers, below, null),
        unplaced: bins.list(),
        total: host.figures.logged(total)
      },
      has_rows: hasRows
    };
  }
};
