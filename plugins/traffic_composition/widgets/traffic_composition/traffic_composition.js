// traffic_composition: logged volume as parts of the period's whole, grouped by client, interface,
// protocol or service port, the named slices then one Other slice carrying the rest
// (docs/widget-catalogue.md, "Traffic composition, now"). Application is not a grouping: nothing in
// the model classifies a flow as one (G12).
//
// The current sampled rates ride beside it -- per direction, and per client or interface for those
// groupings -- the second measure, never summed with the first. traffic/top carries no protocol and
// no port, so the other two groupings have none by member.

// STATEMENTS names the statement each grouping read from the flows answers by.
var STATEMENTS = {
  protocol: "widget_traffic_composition_protocol",
  service_port: "widget_traffic_composition_port",
  "interface": "widget_traffic_composition_interface"
};

// directionInScope reports whether a client-family direction is in a traffic scope.
function directionInScope(host, scope, direction) {
  var directions = host.volume.directions;
  var scopes = host.volume.scopes;
  if (scope === scopes.east_west) {
    return direction === directions.inter_interface;
  }
  if (scope === scopes.north_south) {
    return direction === directions.outbound || direction === directions.inbound;
  }
  return true;
}

// idSet is a reference list's ids as a set, null for no restriction.
function idSet(ids) {
  if (ids === null) {
    return null;
  }
  var set = {};
  ids.forEach(function (id) { set[id] = true; });
  return set;
}

// newSlice is a slice in the order its members have always been written in; a member left
// undefined is left out.
function newSlice(fields) {
  return {
    key: "",
    "interface": fields["interface"],
    client: fields.client,
    protocol: fields.protocol,
    port: fields.port,
    value: 0,
    volume: null
  };
}

// interfaceSlice is the slice of an inside interface: named when the interface is known.
function interfaceSlice(host, id) {
  var info = host.installation.interface(id);
  return newSlice({ "interface": info === null ? undefined : info });
}

exports.widget = {
  serve: function (host) {
    var groupBy = host.params.text("group_by");
    var measure = host.params.text("measure");
    var scope = host.params.text("scope");
    var interfaces = idSet(host.refs.ids("interfaces"));
    var clients = idSet(host.refs.ids("clients"));
    var parts = {};
    var sums = {};
    var keys = [];
    function add(key, slice, figures) {
      if (parts[key] === undefined) {
        slice.key = key;
        parts[key] = slice;
        sums[key] = host.figures.zero();
        keys.push(key);
      }
      sums[key] = host.figures.add(sums[key], figures);
    }
    var whole = "all_traffic";
    var hasRows = false;

    if (groupBy === "client") {
      whole = "traffic_with_an_inside_client";
      host.clients.volumes().clients.forEach(function (volume) {
        var info = host.installation.client(volume.client_id);
        if (info === null || (clients !== null && !clients[volume.client_id]) ||
          (interfaces !== null && (info.interface_id === null || !interfaces[info.interface_id]))) {
          return;
        }
        var figures = host.figures.zero();
        Object.keys(volume.by_direction).forEach(function (direction) {
          if (directionInScope(host, scope, direction)) {
            figures = host.figures.add(figures, volume.by_direction[direction]);
          }
        });
        if (figures.bytes === 0 && figures.allowed_connections + figures.blocked_connections +
          figures.unknown_connections === 0) {
          return;
        }
        hasRows = true;
        add(host.figures.clientKey(volume.client_id), newSlice({ client: info }), figures);
      });
    } else if (groupBy === "interface" && clients === null) {
      host.volume.window().rows.forEach(function (row) {
        if (row.exit_leg_device !== null || !host.volume.scopeMatches(scope, row)) {
          return;
        }
        var local = host.volume.localInterface(row);
        if (interfaces !== null && (local === null || !interfaces[local])) {
          return;
        }
        hasRows = true;
        if (local === null) {
          add("no_interface", newSlice({}), row);
          return;
        }
        add(host.figures.interfaceKey(local), interfaceSlice(host, local), row);
      });
    } else {
      host.envelope.coverFlows("composition_beyond_flow_horizon");
      var window = host.request.window;
      var parameters = {
        from: window.from,
        to: window.to,
        scope: scope,
        interface_ids: host.refs.jsonIds(host.refs.ids("interfaces")),
        client_ids: host.refs.jsonIds(host.refs.ids("clients"))
      };
      host.query.rows(STATEMENTS[groupBy], parameters).forEach(function (row) {
        hasRows = true;
        var member = row.member;
        if (groupBy === "interface") {
          if (member === null) {
            add("no_interface", newSlice({}), row);
            return;
          }
          add(host.figures.interfaceKey(member), interfaceSlice(host, member), row);
          return;
        }
        if (groupBy === "service_port") {
          if (member === null) {
            add("port:not_recorded", newSlice({}), row);
            return;
          }
          add("port:" + member, newSlice({ port: member }), row);
          return;
        }
        add("protocol:" + (member === null ? "" : member), newSlice({ protocol: member === null ? undefined : member }), row);
      });
    }

    var members = keys.map(function (key) {
      return { key: key, value: host.figures.value(measure, sums[key]) };
    });
    var total = host.figures.zero();
    keys.forEach(function (key) { total = host.figures.add(total, sums[key]); });
    // By value, then by key in byte order, as every ranking of the core.
    members.sort(function (a, b) {
      if (a.value !== b.value) {
        return a.value > b.value ? -1 : 1;
      }
      return host.figures.compare(a.key, b.key);
    });
    var limit = host.params.integer("slices");
    var slices = [];
    var otherMembers = 0;
    var otherSum = host.figures.zero();
    members.forEach(function (member, index) {
      if (index < limit) {
        var slice = parts[member.key];
        slice.value = member.value;
        slice.volume = host.figures.logged(sums[member.key]);
        slices.push(slice);
        return;
      }
      otherMembers++;
      otherSum = host.figures.add(otherSum, sums[member.key]);
    });
    return {
      data: {
        group_by: groupBy,
        measure: measure,
        slices: slices,
        // Other is always returned, so the slices and it make the whole; whether it is drawn is
        // show_other's, which is rendering-only (a truncation is never silent, A8).
        other: host.figures.remainder(otherMembers, otherSum, null),
        whole: whole,
        total: host.figures.logged(total),
        current_rates: host.rates.current(groupBy)
      },
      has_rows: hasRows
    };
  }
};
