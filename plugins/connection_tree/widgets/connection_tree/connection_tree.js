// connection_tree: where a chosen subject goes (docs/widget-catalogue.md, "Connection tree"). The
// inside side starts at the chosen root -- an interface, a client or a person -- with the client
// below it; the outside side starts at the operator, or the condition the destination is in, with
// the site name below it. Every flow lands in exactly one node per level on each side, so a node's
// children sum to the node and both roots sum to the same total; a level truncated by
// limit_per_level carries an explicit remainder child. The rule is the store's
// (ReadConnectionTree) and the mockup's (docs/mockups/check-tree-reconciliation.js).

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

// newLevels is a two-level side.
function newLevels() {
  return { nodes: {}, order: [], children: {} };
}

// addToLevels lands one row in a node of each level of a side.
function addToLevels(side, first, second, bytes, connections, blocked, newest) {
  var node = side.nodes[first];
  if (node === undefined) {
    node = newNode(first);
    side.nodes[first] = node;
    side.order.push(first);
    side.children[first] = { nodes: {}, order: [] };
  }
  accumulate(node, bytes, connections, blocked, newest);
  if (second === "") {
    return;
  }
  var children = side.children[first];
  var child = children.nodes[second];
  if (child === undefined) {
    child = newNode(second);
    children.nodes[second] = child;
    children.order.push(second);
  }
  accumulate(child, bytes, connections, blocked, newest);
}

// nodeValue is the figure a node is drawn by.
function nodeValue(measure, node) {
  return measure === "connections" ? node.connections : node.bytes;
}

// logged is a count of logged records or bytes.
function logged(value) {
  return { measure: "logged", lower_bound: true, value: value };
}

// written is a node as the response carries it, its members in the order they have always been
// written in; the labels and the children are set afterwards.
function written(measure, node) {
  var out = {
    key: node.key,
    value: nodeValue(measure, node),
    bytes: logged(node.bytes),
    connections: logged(node.connections),
    blocked_connections: logged(node.blocked)
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
        difference = nodeValue(host.params.text("measure"), b) - nodeValue(host.params.text("measure"), a);
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
  } else if (prefix === "site" && value !== "none") {
    node.site_name = value;
  }
}

// finishLevel sorts, truncates and names one side. A withheld second level keeps its figures in
// the first and drops the names.
function finishLevel(host, side, limit, depth, withholdChildren) {
  var measure = host.params.text("measure");
  var top = side.order.map(function (key) { return side.nodes[key]; });
  var nodes = truncate(host, top, limit).map(function (node) {
    var out = written(measure, node);
    labelNode(host, out);
    if (depth >= 2 && !withholdChildren && side.children[node.key] !== undefined) {
      var level = side.children[node.key];
      var children = level.order.map(function (key) { return level.nodes[key]; });
      if (children.length > 0) {
        out.children = truncate(host, children, limit).map(function (child) {
          var label = written(measure, child);
          labelNode(host, label);
          return label;
        });
      }
    }
    return out;
  });
  return nodes;
}

exports.widget = {
  serve: function (host) {
    coverFlows(host, "tree_beyond_flow_horizon");
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
    var window = host.request.window;
    host.query.rows("widget_connection_tree", { from: window.from, to: window.to }).forEach(function (row) {
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
      addToLevels(inside, first, second, bytes, connections, blocked, row.newest);
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
      var siteNode = row.site_name === null ? "site:none" : "site:" + row.site_name;
      addToLevels(outside, destination, siteNode, bytes, connections, blocked, row.newest);
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
      inside: finishLevel(host, inside, limit, depth, false),
      outside: finishLevel(host, outside, limit, depth, noDomains)
    };
    if (noDomains) {
      host.envelope.withhold("outside[].children");
    }
    data.total = written(measure, total);
    return { data: data, has_rows: hasRows };
  }
};
