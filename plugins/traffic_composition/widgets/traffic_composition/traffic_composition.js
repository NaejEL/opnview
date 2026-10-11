// traffic_composition: logged volume as parts of the period's whole, grouped by client, interface,
// protocol, service port -- port and protocol together, as IANA names a service -- or blocking
// reason, the named slices then one Other slice carrying the rest (docs/widget-catalogue.md, "Traffic
// composition, now"). Application is not a grouping: nothing in the model classifies a flow as one
// (G12).
//
// split_by = direction adds one ring per traffic direction beside the whole; window_seconds adds the
// recent part, the same grouping over the trailing seconds in buckets, each bucket after the filter
// log's complete_until a gap and never a zero. The protocol, service-port and blocking-reason parts,
// and the recent part, are the core's (host.flows), read from the flows by their columns.
//
// The current sampled rates ride beside it -- per direction, and per client or interface for those
// groupings -- the second measure, never summed with the first. traffic/top carries no protocol and
// no port, so the other groupings have none by member.

// DIRECTIONS are the rings split_by = direction draws, in the order a reader asks about them.
var DIRECTIONS = ["inbound", "outbound", "inter_interface", "to_this_firewall", "from_this_firewall"];

// ASIDE_KINDS are the refusals a blocking-reason composition counts beside its rings: a refused
// lookup never became a packet and a detection is not a logged record, so neither is a share of
// the logged whole.
var ASIDE_KINDS = [
  { key: "dns_list", provider: "dns_lookup", prefix: "dns_" },
  { key: "security_engine", provider: "security_event", prefix: "security_engine" }
];

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
    decision: fields.decision,
    value: 0,
    volume: null
  };
}

// interfaceSlice is the slice of an inside interface: named when the interface is known.
function interfaceSlice(host, id) {
  var info = host.installation.interface(id);
  return newSlice({ "interface": info === null ? undefined : info });
}

// partMember is the slice key and slice of a part of the flows (host.flows) in a grouping.
function partMember(host, groupBy, part) {
  if (groupBy === "service_port") {
    var key = host.flows.serviceKey(part.protocol, part.port);
    if (part.port === null) {
      return { key: key, slice: newSlice({}) };
    }
    return { key: key, slice: newSlice({ protocol: part.protocol === null ? undefined : part.protocol, port: part.port }) };
  }
  if (groupBy === "blocking_reason") {
    return { key: "decision:" + part.decision, slice: newSlice({ decision: part.decision }) };
  }
  if (groupBy === "protocol") {
    return {
      key: "protocol:" + (part.protocol === null ? "" : part.protocol),
      slice: newSlice({ protocol: part.protocol === null ? undefined : part.protocol })
    };
  }
  if (groupBy === "client") {
    if (part.client_id === null) {
      return null;
    }
    var info = host.installation.client(part.client_id);
    return { key: host.figures.clientKey(part.client_id), slice: newSlice({ client: info === null ? undefined : info }) };
  }
  if (part.interface_id === null) {
    return { key: "no_interface", slice: newSlice({}) };
  }
  return { key: host.figures.interfaceKey(part.interface_id), slice: interfaceSlice(host, part.interface_id) };
}

// ring ranks a set of members by the measure and folds every member past slices into Other.
function ring(host, members, measure, limit) {
  var keys = [];
  var parts = {};
  var sums = {};
  members.forEach(function (member) {
    if (parts[member.key] === undefined) {
      parts[member.key] = member.slice;
      sums[member.key] = host.figures.zero();
      keys.push(member.key);
    }
    sums[member.key] = host.figures.add(sums[member.key], member.figures);
  });
  var ranked = keys.map(function (key) {
    return { key: key, value: host.figures.value(measure, sums[key]) };
  });
  var total = host.figures.zero();
  keys.forEach(function (key) { total = host.figures.add(total, sums[key]); });
  // By value, then by key in byte order, as every ranking of the core.
  ranked.sort(function (a, b) {
    if (a.value !== b.value) {
      return a.value > b.value ? -1 : 1;
    }
    return host.figures.compare(a.key, b.key);
  });
  var slices = [];
  var otherMembers = 0;
  var otherSum = host.figures.zero();
  ranked.forEach(function (member, index) {
    if (index < limit) {
      // A copy, so a slice shared by the whole and by a ring keeps each one's own figures.
      var slice = JSON.parse(JSON.stringify(parts[member.key]));
      slice.key = member.key;
      slice.value = member.value;
      slice.volume = host.figures.volume(sums[member.key]);
      slices.push(slice);
      return;
    }
    otherMembers++;
    otherSum = host.figures.add(otherSum, sums[member.key]);
  });
  return {
    slices: slices,
    // Other is always returned, so the slices and it make the whole; whether it is drawn is
    // show_other's, which is rendering-only (a truncation is never silent, A8).
    other: host.figures.remainder(otherMembers, otherSum, null),
    total: host.figures.volume(total)
  };
}

// recentPart reads the recent part of the composition: the logged flows of the trailing
// window_seconds, per bucket, per slice key of the whole -- the keys past the whole's slices folded
// into "other". A bucket that is a gap with no record has no value; one complete with no record
// reads 0.
function recentPart(host, groupBy, measure, scope, interfaces, clients, shownKeys, seconds) {
  var recent = host.flows.recent(seconds, { scope: scope, interfaces: interfaces, clients: clients });
  var shown = {};
  shownKeys.forEach(function (key) { shown[key] = true; });
  var keys = shownKeys.concat(["other"]);
  var sums = {};
  keys.forEach(function (key) { sums[key] = {}; });
  recent.parts.forEach(function (part) {
    if (!directionInScope(host, scope, part.traffic_direction)) {
      return;
    }
    var member = partMember(host, groupBy, part);
    if (member === null) {
      return;
    }
    var key = shown[member.key] === true ? member.key : "other";
    var byStart = sums[key];
    byStart[part.bucket_start] = host.figures.add(byStart[part.bucket_start] || host.figures.zero(), part);
  });
  var width = recent.bucket_seconds;
  return {
    seconds: seconds,
    from: recent.from,
    to: recent.to,
    bucket_seconds: width,
    complete_until: recent.complete_until,
    series: keys.map(function (key) {
      return {
        key: key,
        buckets: recent.buckets.map(function (bucket) {
          var start = bucket.from - host.buckets.floorMod(bucket.from, width);
          var figures = sums[key][start];
          var value = figures === undefined ? (bucket.gap ? null : 0) : host.figures.value(measure, figures);
          return { from: bucket.from, to: bucket.to, gap: bucket.gap, value: value };
        })
      };
    })
  };
}

exports.widget = {
  serve: function (host) {
    var groupBy = host.params.text("group_by");
    var measure = host.params.text("measure");
    var scope = host.params.text("scope");
    var interfaceIds = host.refs.ids("interfaces");
    var clientIds = host.refs.ids("clients");
    var interfaces = idSet(interfaceIds);
    var clients = idSet(clientIds);
    var limit = host.params.integer("slices");
    var effective = host.params.effective();
    var members = [];
    function add(key, slice, figures, direction) {
      members.push({ key: key, slice: slice, figures: figures, direction: direction });
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
        var key = host.figures.clientKey(volume.client_id);
        var added = false;
        Object.keys(volume.by_direction).forEach(function (direction) {
          var figures = volume.by_direction[direction];
          if (!directionInScope(host, scope, direction) || (figures.bytes === 0 &&
            figures.allowed_connections + figures.blocked_connections + figures.unknown_connections === 0)) {
            return;
          }
          added = true;
          add(key, newSlice({ client: info }), figures, direction);
        });
        hasRows = hasRows || added;
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
          add("no_interface", newSlice({}), row, row.traffic_direction);
          return;
        }
        add(host.figures.interfaceKey(local), interfaceSlice(host, local), row, row.traffic_direction);
      });
    } else if (groupBy === "interface") {
      host.envelope.coverFlows("composition_beyond_flow_horizon");
      var window = host.request.window;
      host.query.rows("widget_traffic_composition_interface", {
        from: window.from,
        to: window.to,
        scope: scope,
        interface_ids: host.refs.jsonIds(interfaceIds),
        client_ids: host.refs.jsonIds(clientIds)
      }).forEach(function (row) {
        hasRows = true;
        if (row.member === null) {
          add("no_interface", newSlice({}), row, row.traffic_direction);
          return;
        }
        add(host.figures.interfaceKey(row.member), interfaceSlice(host, row.member), row, row.traffic_direction);
      });
    } else {
      host.envelope.coverFlows("composition_beyond_flow_horizon");
      host.flows.parts({ scope: scope, interfaces: interfaceIds, clients: clientIds }).forEach(function (part) {
        hasRows = true;
        var member = partMember(host, groupBy, part);
        add(member.key, member.slice, part, part.traffic_direction);
      });
    }

    var composed = ring(host, members, measure, limit);
    var data = {
      group_by: groupBy,
      measure: measure,
      slices: composed.slices,
      other: composed.other,
      whole: whole,
      total: composed.total,
      current_rates: host.rates.current(groupBy)
    };
    if (host.params.text("split_by") === "direction") {
      data.rings = DIRECTIONS.map(function (direction) {
        var split = ring(host, members.filter(function (member) { return member.direction === direction; }),
          measure, limit);
        return { direction: direction, slices: split.slices, other: split.other, total: split.total };
      });
    }
    if (groupBy === "blocking_reason") {
      var counts = {};
      host.query.rows("widget_traffic_composition_refusals", { from: host.request.window.from, to: host.request.window.to })
        .forEach(function (row) { counts[row.engine_kind] = row.refusals; });
      data.aside = ASIDE_KINDS.map(function (kind) {
        var value = 0;
        Object.keys(counts).forEach(function (engineKind) {
          if (engineKind.slice(0, kind.prefix.length) === kind.prefix) {
            value += counts[engineKind];
          }
        });
        return {
          key: kind.key,
          condition: host.envelope.kindCondition(kind.provider),
          count: { measure: "refusals", lower_bound: true, value: value }
        };
      });
    }
    if (effective.window_seconds !== null && effective.window_seconds !== undefined) {
      data.recent = recentPart(host, groupBy, measure, scope, interfaceIds, clientIds,
        composed.slices.map(function (slice) { return slice.key; }), host.params.integer("window_seconds"));
    }
    return { data: data, has_rows: hasRows };
  }
};
