// interface_volume_ranking: interfaces by volume, each bar carrying the bytes, the client
// count behind it, the blocked connections and the east-west and north-south parts
// (docs/widget-catalogue.md, "Interface volume ranking"). A flow counts on the interface of its
// inside end; an interface's own figures add the second legs logged on it, as the rolling window
// gives them.

// zero is summable figures holding nothing: every member left out is 0 (host.figures).
function zero() {
  return {};
}

// add adds summable figures into others, in place. host.figures.add sums every member a figure
// carries -- its measure and, in the netflow measure, its packets and coverage per part included -- so
// the sum is kept whole whatever the measure.
function add(host, into, other) {
  var sum = host.figures.add(into, other);
  Object.keys(sum).forEach(function (member) { into[member] = sum[member]; });
}

// newParts are an interface's figures, whole and in parts.
function newParts() {
  return { own: zero(), eastWest: zero(), northSouth: zero(), thisFirewall: zero(), exitLegs: zero() };
}

exports.widget = {
  serve: function (host) {
    var window = host.volume.window();
    var interfaces = host.installation.interfaces();
    var byId = {};
    var byDevice = {};
    interfaces.forEach(function (info) {
      byId[info.id] = info;
      byDevice[info.device] = info;
    });
    var scopes = host.volume.scopes;
    var scope = host.params.text("scope");
    var figures = {};
    var member = function (id) {
      if (figures[id] === undefined) {
        figures[id] = newParts();
      }
      return figures[id];
    };
    var unattributed = zero();
    var hasRows = false;
    window.rows.forEach(function (row) {
      if (!host.volume.scopeMatches(scope, row)) {
        return;
      }
      hasRows = true;
      if (row.exit_leg_device !== null) {
        var info = byDevice[row.exit_leg_device];
        if (info === undefined) {
          add(host, unattributed, row);
          return;
        }
        var leg = member(info.id);
        add(host, leg.exitLegs, row);
        add(host, leg.own, row);
        return;
      }
      var local = host.volume.localInterface(row);
      if (local === null) {
        add(host, unattributed, row);
        return;
      }
      var bar = member(local);
      add(host, bar.own, row);
      if (row.traffic_scope === scopes.east_west) {
        add(host, bar.eastWest, row);
      } else if (row.traffic_scope === scopes.north_south) {
        add(host, bar.northSouth, row);
      } else {
        add(host, bar.thisFirewall, row);
      }
    });
    var request = host.request.window;
    var clientCounts = {};
    host.interfaces.clients(request.from, request.to).forEach(function (counts) {
      clientCounts[counts.interface_id] = counts;
    });
    var logging = host.interfaces.ruleLogging();

    var measure = host.params.text("measure");
    var includeUnlabelled = host.params.flag("include_unlabelled");
    // A silent member is drawn with an empty bar only where a measurement says zero: an uncovered
    // window measured nothing.
    var covered = window.covered !== null && window.covered.to > window.covered.from;
    var excludedMembers = 0;
    var excludedSum = zero();
    var total = zero();
    var ranked = [];
    var byKey = {};
    interfaces.forEach(function (info) {
      var sums = figures[info.id];
      if (sums === undefined) {
        if (!covered) {
          return;
        }
        sums = newParts();
      }
      add(host, total, sums.own);
      if (!includeUnlabelled && !info.labelled) {
        excludedMembers++;
        add(host, excludedSum, sums.own);
        return;
      }
      var counts = clientCounts[info.id];
      var seen = counts === undefined ? 0 : counts.seen;
      var notLogging = logging.not_logging_by_interface[String(info.id)];
      var bar = {
        interface: info,
        value: measure === "clients" ? seen : host.figures.value(measure, sums.own),
        volume: host.figures.volume(sums.own),
        east_west: host.figures.volume(sums.eastWest),
        north_south: host.figures.volume(sums.northSouth),
        this_firewall: host.figures.volume(sums.thisFirewall),
        exit_legs: host.figures.volume(sums.exitLegs),
        clients_seen: seen,
        clients_known: counts === undefined ? 0 : counts.known,
        rules_not_logging: notLogging === undefined ? 0 : notLogging
      };
      var key = host.figures.interfaceKey(info.id);
      byKey[key] = { bar: bar, own: sums.own };
      ranked.push({ key: key, value: bar.value });
    });
    // Figures of an interface id the interface table no longer holds stay in the total.
    Object.keys(figures).forEach(function (id) {
      if (byId[id] === undefined) {
        add(host, unattributed, figures[id].own);
      }
    });
    add(host, total, unattributed);
    ranked.sort(function (a, b) {
      if (a.value !== b.value) {
        return a.value > b.value ? -1 : 1;
      }
      return host.figures.compare(a.key, b.key);
    });
    var limit = host.params.integer("limit");
    var bars = [];
    var remainderMembers = 0;
    var remainderSum = zero();
    ranked.forEach(function (rankedMember, index) {
      var held = byKey[rankedMember.key];
      if (index < limit) {
        bars.push(held.bar);
        return;
      }
      remainderMembers++;
      add(host, remainderSum, held.own);
    });
    var data = {
      measure: measure,
      bars: bars,
      remainder: { members: remainderMembers, volume: host.figures.volume(remainderSum) }
    };
    if (!includeUnlabelled) {
      data.excluded_unlabelled = { members: excludedMembers, volume: host.figures.volume(excludedSum) };
    }
    data.unattributed = host.figures.volume(unattributed);
    data.total = host.figures.volume(total);
    data.floating_rules_not_logging = logging.floating_not_logging;
    data.unresolved_rules_not_logging = logging.unresolved_not_logging;
    data.rules_logging_not_reported = logging.not_reported;
    return { data: data, has_rows: hasRows };
  }
};
