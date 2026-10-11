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
    return host.figures.compare(a.key, b.key);
  });
}

exports.widget = {
  serve: function (host) {
    var volumes = host.clients.volumes().clients;
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
    var excludedSum = host.figures.zero();
    var total = host.figures.zero();
    volumes.forEach(function (volume) {
      var info = host.installation.client(volume.client_id);
      if (info === null) {
        return;
      }
      if (selected !== null && (info.interface_id === null || !selected[info.interface_id])) {
        return;
      }
      total = host.figures.add(total, volume.figures);
      if ((identity !== "any" && info.identity_kind !== identity) || (!includeUnstable && info.unstable_identity)) {
        excludedMembers++;
        excludedSum = host.figures.add(excludedSum, volume.figures);
        return;
      }
      var iface = info.interface_id === null ? null : host.installation.interface(info.interface_id);
      // The members in the order a bar has always been written in.
      var bar = {
        client: info,
        "interface": iface,
        value: 0,
        volume: host.figures.volume(volume.figures),
        destinations: volume.destinations
      };
      bar.value = measure === "destinations" ? bar.destinations : host.figures.value(measure, volume.figures);
      var key = host.figures.clientKey(volume.client_id);
      bars[key] = bar;
      members.push({ key: key, value: bar.value });
    });
    rankDescending(host, members);
    var limit = host.params.integer("limit");
    var shown = [];
    var remainderMembers = 0;
    var remainderSum = host.figures.zero();
    members.forEach(function (member, index) {
      if (index < limit) {
        shown.push(bars[member.key]);
        return;
      }
      remainderMembers++;
      remainderSum = host.figures.add(remainderSum, bars[member.key].volume);
    });
    return {
      data: {
        measure: measure,
        bars: shown,
        remainder: host.figures.remainder(remainderMembers, remainderSum, null),
        excluded: host.figures.remainder(excludedMembers, excludedSum, null),
        total: host.figures.volume(total)
      },
      has_rows: volumes.length > 0
    };
  }
};
