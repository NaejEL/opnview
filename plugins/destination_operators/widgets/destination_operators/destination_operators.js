// destination_operators: the outside peers of the window grouped by the autonomous system, or the
// operator, the geolocation placed them in, with the logged bytes, the allowed and blocked
// connections, the distinct peers, the contributing interfaces and the build of the dataset that
// answered (docs/widget-catalogue.md, "Destination operators"). An address whose lookup is a miss,
// pending or was never made is carried in an explicit unresolved row. It reads no table holding a
// domain name, so it is served unchanged under aggregate_mode = no_domains.

// has reports whether an object holds a key of its own.
function has(object, key) {
  return Object.prototype.hasOwnProperty.call(object, key);
}

// numeric orders interface ids as numbers.
function numeric(a, b) {
  return a - b;
}

// groupOf is the group key of a resolved address and the members that name it: the AS number and
// its operator, or the operator alone.
function groupOf(group, geo) {
  if (group === "asn") {
    var number = geo.asn === null ? "none" : String(geo.asn);
    return { key: "asn:" + number, asn: geo.asn, operator: geo.operator };
  }
  var name = geo.operator === null ? "none" : geo.operator;
  return { key: "operator:" + name, asn: null, operator: geo.operator };
}

exports.widget = {
  serve: function (host) {
    var group = host.params.text("group");
    var volume = host.volume.window();
    var reasons = host.peers.reasons;
    var peers = host.peers.outside(volume.rows, host.volume.scopes.all, host.refs.ids("interfaces")).peers;
    var bins = host.peers.bins([reasons.miss, reasons.pending, reasons.no_row]);
    var groups = {};
    var groupList = [];
    var total = host.figures.zero();
    peers.forEach(function (peer) {
      total = host.figures.add(total, peer.figures);
      var geo = host.services.geolocation(peer.address);
      if (geo.lookup_state !== "resolved") {
        bins.add(host.peers.placementState(geo), peer.figures, 1);
        return;
      }
      var named = groupOf(group, geo);
      var entry = has(groups, named.key) ? groups[named.key] : null;
      if (entry === null) {
        // The named members are the first peer's of the group: peers come by bytes, then address.
        entry = { named: named, figures: host.figures.zero(), peers: 0, interfaces: [], build: null };
        groups[named.key] = entry;
        groupList.push(entry);
      }
      entry.figures = host.figures.add(entry.figures, peer.figures);
      entry.peers++;
      peer.interfaces.forEach(function (id) {
        if (entry.interfaces.indexOf(id) < 0) {
          entry.interfaces.push(id);
        }
      });
      if (geo.dataset_build_at !== null && (entry.build === null || geo.dataset_build_at > entry.build)) {
        entry.build = geo.dataset_build_at;
      }
    });

    var allowed = host.params.has("series", "allowed");
    var blocked = host.params.has("series", "blocked");
    var rows = groupList.map(function (entry) {
      var row = { key: entry.named.key };
      if (entry.named.asn !== null) {
        row.asn = entry.named.asn;
      }
      if (entry.named.operator !== null) {
        row.operator = entry.named.operator;
      }
      row.bytes = host.figures.byteCount(entry.figures);
      if (allowed) {
        row.allowed = host.figures.volume(host.mapfilters.allowedOnly(entry.figures));
      }
      if (blocked) {
        row.blocked = host.figures.volume(host.mapfilters.blockedOnly(entry.figures));
      }
      row.peers = entry.peers;
      row.interfaces = entry.interfaces.sort(numeric);
      row.dataset_build_at = entry.build;
      return { row: row, figures: entry.figures };
    });
    // The most bytes first; equal bytes by key, in byte order.
    rows.sort(function (a, b) {
      return b.row.bytes.value - a.row.bytes.value || host.figures.compare(a.row.key, b.row.key);
    });

    var limit = host.params.integer("limit");
    var shown = [];
    var dropped = 0;
    var rest = host.figures.zero();
    rows.forEach(function (entry, index) {
      if (index < limit) {
        shown.push(entry.row);
        return;
      }
      dropped++;
      rest = host.figures.add(rest, entry.figures);
    });
    return {
      data: {
        group: group,
        rows: shown,
        remainder: host.figures.remainder(dropped, rest, null),
        unresolved: bins.list(),
        total: host.figures.volume(total)
      },
      has_rows: peers.length > 0
    };
  }
};
