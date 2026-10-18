// connection_tree: where a chosen subject goes (docs/widget-catalogue.md, "Connection tree"). The
// inside side starts at the chosen root -- an interface, a client or a person -- with the client
// below it; the outside side starts at the operator, or the condition the destination is in, with
// the destination below it -- its inferred site name, or its address, labelled with its operator,
// country or lookup state, where no name was inferred, never one unnamed node -- and the service,
// port and protocol together, below that (depth 3). Every flow lands in exactly one node per level
// on each side, so a node's children sum to the node and both roots sum to the same total; a level
// truncated by limit_per_level carries an explicit remainder child. The rule is the store's
// (ReadConnectionTree) and the mockup's (docs/mockups/check-tree-reconciliation.js).
//
// THE LEVELS ARE GROUPED BY THE WIDGET'S OWN STATEMENTS (connection_tree.statements.sql): each level
// arrives ranked within its parent and cut at limit_per_level, its folded nodes in one remainder row,
// so the script reads the nodes it draws rather than every destination of the window. Under
// aggregate_mode = no_domains the statement read reads no site name, so every destination is then in
// address form whether or not a name was ever inferred.

// THIS_FIREWALL is the node key of the firewall itself, on either side: the store's
// TreeNodeThisFirewall.
var THIS_FIREWALL = "this_firewall";

// REMAINDER is the key of the row a statement folds a level's nodes past limit_per_level into.
var REMAINDER = "remainder";

// coverFlows records the coverage of a reading of the flows alone, and the named state when part
// of the window lies past the retention horizon.
function coverFlows(host, state) {
  host.envelope.coverFrom(host.envelope.earliest("widget_earliest_flow"));
  host.envelope.coverFlowsState(state);
}

// newNode is a node holding nothing yet.
function newNode(key) {
  return { key: key, bytes: 0, connections: 0, blocked: 0, newest: 0 };
}

// nodeOf is a node as a statement row carries it.
function nodeOf(row) {
  var node = newNode(row.node);
  node.bytes = row.bytes === null ? 0 : row.bytes;
  node.connections = row.connections;
  node.blocked = row.blocked;
  node.newest = row.newest === null ? 0 : row.newest;
  if (row.node === REMAINDER) {
    node.remainder = row.members;
  }
  return node;
}

// levelKey names the nodes of one level below one parent.
function levelKey(side, level, parentFirst, parentSecond) {
  return side + "\u0000" + level + "\u0000" + parentFirst + "\u0000" + parentSecond;
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

// ordered sorts the nodes a level draws, as the statement ranked them: by the chosen sort, most
// first, equal values by key in byte order; the remainder, when there is one, last.
function ordered(host, nodes) {
  var order = host.params.text("sort");
  var measure = host.params.text("measure");
  var remainder = null;
  var shown = [];
  nodes.forEach(function (node) {
    if (node.key === REMAINDER) {
      remainder = node;
    } else {
      shown.push(node);
    }
  });
  shown.sort(function (a, b) {
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
  return remainder === null ? shown : shown.concat([remainder]);
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

// finishLevel sorts and names one level of a side below one parent, and the levels below it down to
// depth. parents are the keys of the nodes above it, outermost first.
function finishLevel(host, levels, side, parents, depth) {
  var measure = host.params.text("measure");
  var level = parents.length + 1;
  var nodes = levels[levelKey(side, level, parents.length > 0 ? parents[0] : "",
    parents.length > 1 ? parents[1] : "")];
  if (nodes === undefined) {
    return [];
  }
  return ordered(host, nodes).map(function (node) {
    var out = written(measure, node);
    labelNode(host, out);
    if (depth >= 2 && node.key !== REMAINDER) {
      var below = finishLevel(host, levels, side, parents.concat([node.key]), depth - 1);
      if (below.length > 0) {
        out.children = below;
      }
    }
    return out;
  });
}

exports.widget = {
  serve: function (host) {
    coverFlows(host, "tree_beyond_flow_horizon");
    MEASURE = host.figures.measure;
    var root = host.params.text("root");
    var depth = host.params.integer("depth");
    var measure = host.params.text("measure");
    var noDomains = host.request.no_domains;
    var window = host.request.window;
    var rows = host.query.rows(noDomains ? "widget_tree_nodes_no_domains" : "widget_tree_nodes", {
      from: window.from,
      to: window.to,
      root: root,
      depth: depth,
      limit: host.params.integer("limit_per_level"),
      sort: host.params.text("sort"),
      value_by: measure,
      include_blocked: host.params.flag("include_blocked"),
      interface_ids: host.refs.jsonIds(host.refs.ids("interfaces")),
      client_ids: host.refs.jsonIds(host.refs.ids("clients")),
      owner_ids: host.refs.jsonIds(host.refs.ids("owners"))
    });
    var levels = {};
    var total = newNode("total");
    var hasRows = false;
    rows.forEach(function (row) {
      if (row.side === "total") {
        total.bytes = row.bytes;
        total.connections = row.connections;
        total.blocked = row.blocked;
        hasRows = row.members > 0;
        // The core judges every connection the tree counts, as it judges a Volume. A logged answer has
        // nothing to judge: the core records a judgement only while the answer is read in a declared
        // measure.
        if (MEASURE !== "logged" && hasRows) {
          host.figures.cover(row);
        }
        return;
      }
      var key = levelKey(row.side, row.level, row.parent_first, row.parent_second);
      if (levels[key] === undefined) {
        levels[key] = [];
      }
      levels[key].push(nodeOf(row));
    });
    var data = {
      root: root,
      measure: measure,
      depth: depth,
      inside: finishLevel(host, levels, "inside", [], Math.min(depth, 2)),
      outside: finishLevel(host, levels, "outside", [], depth)
    };
    if (noDomains) {
      // The statement read no site name: every destination below the operator is its address.
      host.envelope.withhold("outside[].children[].site_name");
    }
    data.total = written(measure, total);
    return { data: data, has_rows: hasRows };
  }
};
