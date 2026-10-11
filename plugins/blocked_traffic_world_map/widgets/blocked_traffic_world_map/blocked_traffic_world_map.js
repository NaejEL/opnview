// blocked_traffic_world_map: where the traffic that was STOPPED was heading, or coming from, by the
// engine that stopped it -- the firewall, whose blocked records carry a real remote address, and the
// security engine, whose events carry a peer address (docs/widget-catalogue.md, "Blocked-traffic
// world map"). The DNS engines contribute nothing to a map, because a refused lookup resolves to no
// address: their refusals are a counter of their own beside it, so the map's total reconciles with
// the unified blocked feed's.

// has reports whether an object holds a key of its own.
function has(object, key) {
  return Object.prototype.hasOwnProperty.call(object, key);
}

// numeric orders interface ids as numbers.
function numeric(a, b) {
  return a - b;
}

exports.widget = {
  serve: function (host) {
    var volume = host.volume.window();
    var window = host.request.window;
    var endpoint = host.params.text("endpoint");
    var sizeBy = host.params.text("size_by");
    var minimum = host.params.integer("min_count");
    var selected = host.refs.ids("interfaces");
    var reasons = host.peers.reasons;
    var directions = host.volume.directions;
    var firewall = host.params.has("engines", "firewall_rule");
    var points = [];
    var belowMembers = 0;
    var bins = host.peers.bins([reasons.miss, reasons.pending, reasons.no_row, reasons.no_coordinates,
      reasons.east_west, reasons.other_endpoint]);
    var total = host.figures.zero();
    var below = host.figures.zero();
    var hasRows = false;
    // The directions whose outside end is the endpoint the map shows.
    var wanted = {};
    if (endpoint === "destination") {
      wanted[directions.outbound] = true;
      wanted[directions.from_this_firewall] = true;
    } else {
      wanted[directions.inbound] = true;
      wanted[directions.to_this_firewall] = true;
    }

    if (firewall) {
      var peers = {};
      var order = [];
      volume.rows.forEach(function (row) {
        if (row.exit_leg_device !== null || row.blocked_connections === 0 ||
          !host.peers.keepInterface(selected, row)) {
          return;
        }
        var blocked = host.mapfilters.blockedOnly(row);
        hasRows = true;
        if (row.peer_address === null) {
          bins.add(reasons.east_west, blocked, 0);
          return;
        }
        if (wanted[row.traffic_direction] !== true) {
          bins.add(reasons.other_endpoint, blocked, 0);
          return;
        }
        var peer = has(peers, row.peer_address) ? peers[row.peer_address] : null;
        if (peer === null) {
          peer = { address: row.peer_address, figures: host.figures.zero(), interfaces: [] };
          peers[row.peer_address] = peer;
          order.push(peer);
        }
        peer.figures = host.figures.add(peer.figures, blocked);
        var local = host.volume.localInterface(row);
        if (local !== null && peer.interfaces.indexOf(local) < 0) {
          peer.interfaces.push(local);
        }
      });
      host.peers.sorted(order).forEach(function (peer) {
        var geo = host.services.geolocation(peer.address);
        var reason = host.peers.placement(geo);
        if (reason !== "") {
          bins.add(reason, peer.figures, 1);
          return;
        }
        total = host.figures.add(total, peer.figures);
        if (peer.figures.blocked_connections < minimum) {
          belowMembers++;
          below = host.figures.add(below, peer.figures);
          return;
        }
        points.push({
          address: peer.address,
          engine: "firewall_rule",
          geo: geo,
          size: sizeBy === "bytes" ? peer.figures.blocked_bytes : peer.figures.blocked_connections,
          volume: host.figures.logged(peer.figures),
          interfaces: peer.interfaces
        });
      });
    }
    // What the firewall's half placed, left below the minimum and could not place: the security
    // engine's half below adds members below the minimum, and no volume.
    var belowVolume = host.figures.logged(below);
    var unplaced = bins.list();
    total = host.figures.logged(host.figures.add(total, bins.total()));

    var securityBins = host.peers.bins([reasons.miss, reasons.pending, reasons.no_row,
      reasons.no_coordinates]);
    var securityEvents = host.figures.loggedCount(0);
    if (host.params.has("engines", "security_engine")) {
      var events = {};
      var interfaces = {};
      var addresses = [];
      var bounds = { from: window.from, to: window.to, endpoint: endpoint };
      host.query.rows("widget_security_blocked_peers", bounds).forEach(function (row) {
        if (selected !== null && (row.interface_id === null || selected.indexOf(row.interface_id) < 0)) {
          return;
        }
        if (!has(events, row.peer_address)) {
          events[row.peer_address] = 0;
          interfaces[row.peer_address] = [];
          addresses.push(row.peer_address);
        }
        events[row.peer_address] += row.events;
        if (row.interface_id !== null && interfaces[row.peer_address].indexOf(row.interface_id) < 0) {
          interfaces[row.peer_address].push(row.interface_id);
        }
      });
      // Most events first; equal counts in the byte order of their addresses.
      addresses.sort(function (a, b) {
        return events[b] - events[a] || host.figures.compare(a, b);
      });
      addresses.forEach(function (address) {
        hasRows = true;
        var count = events[address];
        securityEvents.value += count;
        var geo = host.services.geolocation(address);
        var reason = host.peers.placement(geo);
        if (reason !== "") {
          securityBins.add(reason, { blocked_connections: count }, 1);
          return;
        }
        if (count < minimum) {
          belowMembers++;
          return;
        }
        points.push({
          address: address,
          engine: "security_engine",
          geo: geo,
          size: count,
          events: { measure: "security_events", lower_bound: true, value: count },
          interfaces: interfaces[address].sort(numeric)
        });
      });
    }

    var dns = 0;
    var counts = host.query.rows("widget_blocked_decision_counts", { from: window.from, to: window.to });
    counts.forEach(function (row) {
      if (row.engine_kind.length > 4 && row.engine_kind.slice(0, 4) === "dns_") {
        dns += row.decisions;
      }
    });
    var refusals = dns + securityEvents.value;
    if (firewall) {
      refusals += total.blocked_connections;
    }
    return {
      data: {
        size_by: sizeBy,
        points: points,
        below_minimum: { members: belowMembers, volume: belowVolume },
        unplaced: unplaced,
        total: total,
        security_events: securityEvents,
        security_unplaced: securityBins.list(),
        dns_blocked_lookups: { measure: "dns_lookups", lower_bound: true, value: dns },
        total_refusals: { measure: "refusals", lower_bound: true, value: refusals }
      },
      has_rows: hasRows
    };
  }
};
