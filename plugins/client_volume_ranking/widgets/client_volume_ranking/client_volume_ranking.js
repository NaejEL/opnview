// client_volume_ranking: clients by logged volume, with their host name, MAC, last address,
// interface, blocked connections and distinct destinations; a randomised MAC is badged unstable
// identity (docs/widget-catalogue.md, "Client volume ranking"). The ranking is per machine, which is
// not the per-person question.

// rankDescending orders members by value, then by key in byte order, as every ranking of the core
// does: equal values keep an order of their own, never the order the members were read in.
function rankDescending(host, members) {
  members.sort(function (a, b) {
    if (a.value !== b.value) {
      return a.value > b.value ? -1 : 1;
    }
    return host.lib.figures.figures.compare(a.key, b.key);
  });
}

exports.widget = {
  serve: function (host) { return host.lib.figures.measure.serve(host, serveInMeasure); }
};

// serveInMeasure builds the answer under the measure the figures library put in force: it is run
// once per measure tried, and never names one (ADR 0021, step 5).
function serveInMeasure(host) {
  var volumes = host.lib.figures.clients.volumes(host).clients;
  var selectedIds = host.refs.ids("interfaces");
  var selected = null;
  if (selectedIds !== null) {
    selected = {};
    selectedIds.forEach(function (id) { selected[id] = true; });
  }
  var identity = host.params.text("identity");
  var measure = host.params.text("measure");
  var includeUnstable = host.params.flag("include_unstable");
  var members = [];
  var bars = {};
  var excludedMembers = 0;
  var excludedSum = host.lib.figures.figures.zero();
  var total = host.lib.figures.figures.zero();
  volumes.forEach(function (volume) {
    var info = host.lib.figures.installation.client(host, volume.client_id);
    if (info === null) {
      return;
    }
    if (selected !== null && (info.interface_id === null || !selected[info.interface_id])) {
      return;
    }
    total = host.lib.figures.figures.add(total, volume.figures);
    if ((identity !== "any" && info.identity_kind !== identity) || (!includeUnstable && info.unstable_identity)) {
      excludedMembers++;
      excludedSum = host.lib.figures.figures.add(excludedSum, volume.figures);
      return;
    }
    var iface = info.interface_id === null ? null : host.lib.figures.installation.interface(host, info.interface_id);
    // The members in the order a bar has always been written in.
    var bar = {
      client: info,
      "interface": iface,
      value: 0,
      volume: host.lib.figures.figures.volume(volume.figures),
      destinations: volume.destinations
    };
    bar.value = measure === "destinations" ? bar.destinations : host.lib.figures.figures.value(measure, volume.figures);
    var key = host.lib.figures.figures.clientKey(volume.client_id);
    bars[key] = bar;
    members.push({ key: key, value: bar.value });
  });
  rankDescending(host, members);
  var limit = host.params.integer("limit");
  var shown = [];
  var remainderMembers = 0;
  var remainderSum = host.lib.figures.figures.zero();
  members.forEach(function (member, index) {
    if (index < limit) {
      shown.push(bars[member.key]);
      return;
    }
    remainderMembers++;
    remainderSum = host.lib.figures.figures.add(remainderSum, bars[member.key].volume);
  });
  return {
    data: {
      measure: measure,
      bars: shown,
      remainder: host.lib.figures.figures.remainder(remainderMembers, remainderSum, null),
      excluded: host.lib.figures.figures.remainder(excludedMembers, excludedSum, null),
      total: host.lib.figures.figures.volume(total)
    },
    has_rows: volumes.length > 0
  };
}
