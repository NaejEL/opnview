// connection_tree: where a chosen subject goes (docs/widget-catalogue.md, "Connection tree"). The
// inside side starts at the chosen root -- an interface, a client or a person -- with the client
// below it; the outside side starts at the operator, or the condition the destination is in, with
// the destination below it -- its inferred site name, or its address, labelled with its operator,
// country or lookup state, where no name was inferred, never one unnamed node -- and the service,
// port and protocol together, below that (depth 3). Every flow lands in exactly one node per level
// on each side, so a node's children sum to the node and both roots sum to the same total; a level
// truncated by limit_per_level carries an explicit remainder child. The rule is the store's
// (ReadConnectionTree) and the mockup's (docs/mockups/check-tree-reconciliation.js). The rows are
// the core's (host.flows.destinations), which reads no site name under aggregate_mode = no_domains,
// so every destination is then in address form whether or not a name was ever inferred.

// THIS_FIREWALL is the node key of the firewall itself, on either side: the store's
// TreeNodeThisFirewall.
var THIS_FIREWALL = "this_firewall";

// coverFlows records the coverage of a reading of the flows alone, and the named state when part
// of the window lies past the retention horizon.
function coverFlows(host, state) {
  host.envelope.coverFrom(host.envelope.earliest("widget_earliest_flow"));
  host.envelope.coverFlowsState(state);
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

// treeKey renders a nullable id as a node key.
function treeKey(prefix, value, fallback) {
  return prefix + ":" + (value === null ? fallback : value);
}

// newNode is a node holding nothing yet.
function newNode(key) {
  return { key: key, bytes: 0, connections: 0, blocked: 0, newest: 0 };
}

// accumulate adds one row's figures to a node.
function accumulate(node, bytes, connections, blocked, newest) {
  node.bytes += bytes;
  node.connections += connections;
  node.blocked += blocked;
  node.newest = Math.max(node.newest, newest);
}

// newLevels is one level of a side, each node holding the level below it.
function newLevels() {
  return { nodes: {}, order: [], children: {} };
}

// addToLevels lands one row in a node of each level of a side, from the first key down; an empty
// key ends the path there. The keys are arguments rather than an array: the tree lands every row of
// the window twice, and building an array of keys for each was a measurable share of its time.
function addToLevels(side, first, second, third, bytes, connections, blocked, newest) {
  if (first === "") {
    return;
  }
  var level = addToLevel(side, first, bytes, connections, blocked, newest);
  if (second === "") {
    return;
  }
  level = addToLevel(level, second, bytes, connections, blocked, newest);
  if (third === "") {
    return;
  }
  addToLevel(level, third, bytes, connections, blocked, newest);
}

// addToLevel lands one row in a node of one level and returns the level below that node.
function addToLevel(level, key, bytes, connections, blocked, newest) {
  var node = level.nodes[key];
  if (node === undefined) {
    node = newNode(key);
    level.nodes[key] = node;
    level.order.push(key);
    level.children[key] = newLevels();
  }
  accumulate(node, bytes, connections, blocked, newest);
  return level.children[key];
}

// nodeValue is the figure a node is drawn by.
function nodeValue(measure, node) {
  return measure === "connections" ? node.connections : node.bytes;
}

// MEASURE is the measure the answer is given in (host.figures.measure), set when it is served.
var MEASURE = "";

// figure is a count of records or bytes, in the measure the answer is given in.
function figure(value) {
  return { measure: MEASURE, lower_bound: true, value: value };
}

// written is a node as the response carries it, its members in the order they have always been
// written in; the labels and the children are set afterwards.
function written(measure, node) {
  var out = {
    key: node.key,
    value: nodeValue(measure, node),
    bytes: figure(node.bytes),
    connections: figure(node.connections),
    blocked_connections: figure(node.blocked)
  };
  if (node.newest > 0) {
    out.last_seen_at = node.newest;
  }
  if (node.remainder !== undefined) {
    out.remainder_members = node.remainder;
  }
  return out;
}

// truncate sorts a level and folds every member past limit into one remainder node, so the level
// still sums to its parent.
function truncate(host, nodes, limit) {
  var order = host.params.text("sort");
  var measure = host.params.text("measure");
  nodes.sort(function (a, b) {
    var difference = 0;
    switch (order) {
      case "connections":
        difference = b.connections - a.connections;
        break;
      case "name":
        return host.figures.compare(a.key, b.key);
      case "recency":
        difference = b.newest - a.newest;
        break;
      default:
        difference = nodeValue(measure, b) - nodeValue(measure, a);
    }
    return difference !== 0 ? (difference > 0 ? 1 : -1) : host.figures.compare(a.key, b.key);
  });
  if (nodes.length <= limit) {
    return nodes;
  }
  var remainder = newNode("remainder");
  remainder.remainder = nodes.length - limit;
  nodes.slice(limit).forEach(function (node) {
    accumulate(remainder, node.bytes, node.connections, node.blocked, node.newest);
  });
  return nodes.slice(0, limit).concat([remainder]);
}

// labelNode attaches what a node's key names.
function labelNode(host, node) {
  var colon = node.key.indexOf(":");
  if (colon < 0) {
    return;
  }
  var prefix = node.key.slice(0, colon);
  var value = node.key.slice(colon + 1);
  var numeric = /^[+-]?[0-9]+$/.test(value);
  var id = numeric ? Number(value) : 0;
  if (prefix === "interface" && numeric) {
    var info = host.installation.interface(id);
    if (info !== null) {
      node.interface = info;
    }
  } else if (prefix === "client" && numeric) {
    var client = host.installation.client(id);
    if (client !== null) {
      node.client = client;
    }
  } else if (prefix === "owner" && numeric) {
    host.installation.owners().forEach(function (owner) {
      if (owner.id === id) {
        node.owner = owner;
      }
    });
  } else if (prefix === "operator" && numeric) {
    var row = host.query.row("widget_operator_name", { asn: id });
    if (row !== null) {
      node.operator = row.operator;
    }
  } else if (prefix === "site") {
    node.site_name = value;
  } else if (prefix === "address") {
    var geo = host.services.geolocation(value);
    node.address = value;
    node.lookup_state = geo.lookup_state;
    node.country_code = geo.country_code;
    node.asn = geo.asn;
    node.operator = geo.operator;
  } else if (prefix === "port" && value !== "not_recorded") {
    var slash = value.indexOf("/");
    node.port = Number(value.slice(0, slash));
    node.protocol = value.slice(slash + 1);
  }
}

// finishLevel sorts, truncates and names one level of a side, and the levels below it down to depth.
function finishLevel(host, level, limit, depth) {
  var measure = host.params.text("measure");
  var members = level.order.map(function (key) { return level.nodes[key]; });
  return truncate(host, members, limit).map(function (node) {
    var out = written(measure, node);
    labelNode(host, out);
    var below = level.children[node.key];
    if (depth >= 2 && below !== undefined && below.order.length > 0) {
      out.children = finishLevel(host, below, limit, depth - 1);
    }
    return out;
  });
}

exports.widget = {
  serve: function (host) {
    coverFlows(host, "tree_beyond_flow_horizon");
    MEASURE = host.figures.measure;
    var measured = MEASURE !== "logged";
    var services = host.params.integer("depth") >= 3;
    var directions = host.volume.directions;
    var root = host.params.text("root");
    var interfaces = idSet(host.refs.ids("interfaces"));
    var clients = idSet(host.refs.ids("clients"));
    var owners = idSet(host.refs.ids("owners"));
    var includeBlocked = host.params.flag("include_blocked");
    var inside = newLevels();
    var outside = newLevels();
    var total = newNode("total");
    var hasRows = false;
    host.flows.destinations().forEach(function (row) {
      if ((interfaces !== null && (row.interface_id === null || !interfaces[row.interface_id])) ||
        (clients !== null && (row.client_id === null || !clients[row.client_id])) ||
        (owners !== null && (row.owner_id === null || !owners[row.owner_id]))) {
        return;
      }
      var connections = row.connections;
      var bytes = row.bytes === null ? 0 : row.bytes;
      var blocked = row.blocked === null ? 0 : row.blocked;
      var blockedBytes = row.blocked_bytes === null ? 0 : row.blocked_bytes;
      if (!includeBlocked) {
        connections -= blocked;
        bytes -= blockedBytes;
        blocked = 0;
        if (connections === 0) {
          return;
        }
      }
      hasRows = true;
      // The core judges every row the tree counts, as it judges a Volume. A logged answer has nothing
      // to judge: the core records a judgement only while the answer is read in a declared measure.
      if (measured) {
        host.figures.cover(row);
      }
      var direction = row.traffic_direction;
      var firewall = direction === directions.to_this_firewall || direction === directions.from_this_firewall;
      var looked = row.lookup_state !== null;
      var firewallInside = firewall && (looked || row.interface_id === null);
      var firewallOutside = firewall && !looked;
      var clientNode = treeKey("client", row.client_id, "none");
      var first = "";
      var second = "";
      if (firewallInside) {
        first = THIS_FIREWALL;
        if (root !== "client") {
          second = THIS_FIREWALL;
        }
      } else if (root === "interface") {
        first = treeKey("interface", row.interface_id, "none");
        second = clientNode;
      } else if (root === "client") {
        first = clientNode;
      } else {
        first = treeKey("owner", row.owner_id, "unassigned");
        if (row.client_id === null) {
          first = "owner:none";
        }
        second = clientNode;
      }
      addToLevels(inside, first, second, "", bytes, connections, blocked, row.newest);
      var destination;
      if (firewallOutside) {
        destination = THIS_FIREWALL;
      } else if (direction === directions.inter_interface) {
        destination = directions.inter_interface;
      } else if (looked && row.lookup_state === "resolved") {
        destination = treeKey("operator", row.asn, "none");
      } else if (looked) {
        destination = "unplaced:" + row.lookup_state;
      } else {
        destination = "unplaced:no_row";
      }
      // Below an outside destination: its site name where one was inferred and its address
      // otherwise; then its service, port and protocol together. A destination with no outside
      // address -- this firewall, another interface -- has no level below it.
      var named = "";
      var service = "";
      if (row.peer_address !== null && !firewallOutside && direction !== directions.inter_interface) {
        named = row.site_name !== null ? "site:" + row.site_name : "address:" + row.peer_address;
        // The service is the third level, which only a tree of depth 3 draws.
        if (services) {
          service = host.flows.serviceKey(row.protocol, row.port);
        }
      }
      addToLevels(outside, destination, named, service, bytes, connections, blocked, row.newest);
      total.bytes += bytes;
      total.connections += connections;
      total.blocked += blocked;
    });
    var limit = host.params.integer("limit_per_level");
    var depth = host.params.integer("depth");
    var measure = host.params.text("measure");
    var noDomains = host.request.no_domains;
    var data = {
      root: root,
      measure: measure,
      depth: depth,
      inside: finishLevel(host, inside, limit, Math.min(depth, 2)),
      outside: finishLevel(host, outside, limit, depth)
    };
    if (noDomains) {
      // The core read no site name: every destination below the operator is its address.
      host.envelope.withhold("outside[].children[].site_name");
    }
    data.total = written(measure, total);
    return { data: data, has_rows: hasRows };
  }
};
