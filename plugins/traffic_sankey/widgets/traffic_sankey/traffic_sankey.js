// traffic_sankey: who talks to whom at a glance -- interfaces or clients on the left; another
// interface, the destination operator or the country on the right (docs/widget-catalogue.md,
// "Traffic Sankey"). Ribbons past max_nodes or under min_share, and every ribbon whose right-hand name
// could not be resolved, fall into one explicit Other band, so the ribbons and the band make the
// total.

// has reports whether an object holds a key of its own.
function has(object, key) {
  return Object.prototype.hasOwnProperty.call(object, key);
}

// node names one end, its optional members set only when present, in the order they are written.
function node(key, iface, client, asn, operator, countryCode) {
  var named = { key: key };
  if (iface !== null) {
    named.interface = iface;
  }
  if (client !== null) {
    named.client = client;
  }
  if (asn !== null) {
    named.asn = asn;
  }
  if (operator !== null) {
    named.operator = operator;
  }
  if (countryCode !== null) {
    named.country_code = countryCode;
  }
  return named;
}

// scopeOf derives a row's scope from its interfaces and direction, as the schema pins it.
function scopeOf(host, direction, src, dst) {
  var directions = host.volume.directions;
  var scopes = host.volume.scopes;
  if (direction === directions.to_this_firewall || direction === directions.from_this_firewall) {
    return scopes.this_firewall;
  }
  if (src !== null && dst !== null) {
    return scopes.east_west;
  }
  return scopes.north_south;
}

// leftFlows reads the grouped rows of the left side: the rolling window's rows, each on its inside
// interface or outside, or the flows of the window per client.
function leftFlows(host, left, scope, selected) {
  var flows = [];
  if (left === "interface") {
    host.volume.window().rows.forEach(function (row) {
      if (row.exit_leg_device !== null || !host.volume.scopeMatches(scope, row) ||
        !host.peers.keepInterface(selected, row)) {
        return;
      }
      var local = host.volume.localInterface(row);
      flows.push({
        left: local === null ? "outside" : host.figures.interfaceKey(local),
        direction: row.traffic_direction,
        src: row.src_interface_id,
        dst: row.dst_interface_id,
        peer: row.peer_address,
        figures: row,
        leftClient: null,
        leftIface: local
      });
    });
    return flows;
  }
  host.envelope.coverFlows("client_left_beyond_flow_horizon");
  var window = host.request.window;
  host.query.rows("widget_sankey_client_flows", { from: window.from, to: window.to }).forEach(function (row) {
    var key = {
      src_interface_id: row.src_interface_id,
      dst_interface_id: row.dst_interface_id,
      peer_address: null,
      traffic_scope: scopeOf(host, row.traffic_direction, row.src_interface_id, row.dst_interface_id),
      traffic_direction: row.traffic_direction,
      exit_leg_device: null
    };
    if (!host.volume.scopeMatches(scope, key) || !host.peers.keepInterface(selected, key)) {
      return;
    }
    flows.push({
      left: host.figures.clientKey(row.client_id),
      direction: row.traffic_direction,
      src: row.src_interface_id,
      dst: row.dst_interface_id,
      peer: row.peer_address,
      figures: host.figures.withMeasure({
        bytes: row.bytes,
        allowed_bytes: row.bytes - row.blocked_bytes,
        blocked_bytes: row.blocked_bytes,
        allowed_connections: row.connections - row.blocked_connections,
        blocked_connections: row.blocked_connections
      }, row),
      leftClient: row.client_id,
      leftIface: null
    });
  });
  return flows;
}

exports.widget = {
  serve: function (host) {
    var left = host.params.text("left");
    var right = host.params.text("right");
    var measure = host.params.text("measure");
    var directions = host.volume.directions;
    var thisFirewall = host.matrixplacement.keys.this_firewall;
    var reasons = host.peers.reasons;
    var flows = leftFlows(host, left, host.params.text("scope"), host.refs.ids("interfaces"));

    // Each unnamed bin lists its addresses with their figures, which sum to the bin: a destination the
    // right-hand side cannot name is still shown by its address (C2).
    var unnamed = host.peers.bins([reasons.miss, reasons.pending, reasons.no_row], true);
    var pairs = {};
    var pairList = [];
    var nodes = {};
    var total = host.figures.zero();
    var other = host.figures.zero();
    flows.forEach(function (flow) {
      total = host.figures.add(total, flow.figures);
      var rightKey;
      if (flow.direction === directions.to_this_firewall ||
        flow.direction === directions.from_this_firewall) {
        rightKey = thisFirewall;
      } else if (flow.peer === null) {
        var far = flow.dst;
        if (flow.leftIface !== null && flow.dst !== null && flow.leftIface === flow.dst) {
          far = flow.src;
        }
        if (right === "interface" && far !== null) {
          rightKey = host.figures.interfaceKey(far);
          var known = host.installation.interface(far);
          if (known !== null) {
            nodes[rightKey] = node(rightKey, known, null, null, null, null);
          }
        } else {
          rightKey = "inter_interface";
        }
      } else if (right === "interface") {
        rightKey = "outside";
      } else {
        var geo = host.services.geolocation(flow.peer);
        if (geo.lookup_state !== "resolved") {
          unnamed.addAddress(host.peers.placementState(geo), flow.peer, flow.figures);
          other = host.figures.add(other, flow.figures);
          return;
        }
        if (right === "country") {
          rightKey = "country:" + (geo.country_code === null ? "none" : geo.country_code);
          nodes[rightKey] = node(rightKey, null, null, null, null, geo.country_code);
        } else {
          rightKey = "operator:" + (geo.asn === null ? "none" : String(geo.asn));
          nodes[rightKey] = node(rightKey, null, null, geo.asn, geo.operator, null);
        }
      }
      var pairKey = flow.left + "\u0000" + rightKey;
      var entry = has(pairs, pairKey) ? pairs[pairKey] : null;
      if (entry === null) {
        entry = { left: flow.left, right: rightKey, figures: host.figures.zero() };
        pairs[pairKey] = entry;
        pairList.push(entry);
      }
      entry.figures = host.figures.add(entry.figures, flow.figures);
      if (!has(nodes, flow.left)) {
        var iface = null;
        if (flow.leftIface !== null && left === "interface") {
          iface = host.installation.interface(flow.leftIface);
        }
        var client = null;
        if (flow.leftClient !== null) {
          client = host.installation.client(flow.leftClient);
        }
        nodes[flow.left] = node(flow.left, iface, client, null, null, null);
      }
    });

    var showBlocked = host.params.flag("show_blocked");
    var ribbons = pairList.map(function (entry) {
      var volume = host.figures.volume(entry.figures);
      var ribbon = {
        left: entry.left,
        right: entry.right,
        value: host.figures.value(measure, entry.figures),
        bytes: host.figures.byteCount(entry.figures),
        connections: host.figures.count(volume.connections)
      };
      if (showBlocked && volume.connections > 0) {
        ribbon.blocked_share = volume.blocked_connections / volume.connections;
      }
      return { ribbon: ribbon, figures: entry.figures };
    });
    // The largest first; equal values by their left end, then their right, in byte order.
    ribbons.sort(function (a, b) {
      return b.ribbon.value - a.ribbon.value || host.figures.compare(a.ribbon.left, b.ribbon.left) ||
        host.figures.compare(a.ribbon.right, b.ribbon.right);
    });

    var whole = host.figures.value(measure, total);
    var maxNodes = host.params.integer("max_nodes");
    var minShare = host.params.number("min_share");
    var drawnRibbons = [];
    var otherRibbons = 0;
    var drawn = {};
    ribbons.forEach(function (entry) {
      var share = whole > 0 ? entry.ribbon.value / whole : 0;
      if (drawnRibbons.length >= maxNodes || share < minShare) {
        otherRibbons++;
        other = host.figures.add(other, entry.figures);
        return;
      }
      drawnRibbons.push(entry.ribbon);
      drawn[entry.ribbon.left] = true;
      drawn[entry.ribbon.right] = true;
    });
    var drawnNodes = [];
    Object.keys(nodes).sort(host.figures.compare).forEach(function (key) {
      if (drawn[key] === true) {
        drawnNodes.push(nodes[key]);
      }
    });
    ["outside", "inter_interface", thisFirewall].forEach(function (key) {
      if (drawn[key] === true) {
        drawnNodes.push({ key: key });
      }
    });
    var otherVolume = host.figures.volume(other);
    return {
      data: {
        left: left,
        right: right,
        measure: measure,
        nodes: drawnNodes,
        ribbons: drawnRibbons,
        other: {
          ribbons: otherRibbons,
          bytes: host.figures.byteCount(other),
          connections: host.figures.count(otherVolume.connections),
          unnamed: unnamed.list()
        },
        total: host.figures.volume(total)
      },
      has_rows: flows.length > 0
    };
  }
};
