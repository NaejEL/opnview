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

// firstRanked is the first `limit` rows of a list in the order `rank` gives them, a total order: what
// sorting the list and keeping its head gives, with each row compared with the rows kept so far rather
// than the whole list sorted.
function firstRanked(rows, limit, rank) {
  var kept = [];
  if (limit <= 0) {
    return kept;
  }
  rows.forEach(function (row) {
    if (kept.length === limit && rank(row, kept[limit - 1]) >= 0) {
      return;
    }
    var low = 0;
    var high = kept.length;
    while (low < high) {
      var middle = (low + high) >> 1;
      if (rank(row, kept[middle]) < 0) {
        high = middle;
      } else {
        low = middle + 1;
      }
    }
    kept.splice(low, 0, row);
    if (kept.length > limit) {
      kept.pop();
    }
  });
  return kept;
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
    // Every peer's figures, summed in one call: host.figures.sum is add folded over a list.
    var total = host.figures.sum(peers.map(function (peer) { return peer.figures; }));
    peers.forEach(function (peer) {
      var geo = host.services.geolocation(peer.address);
      if (geo.lookup_state !== "resolved") {
        bins.add(host.peers.placementState(geo), peer.figures, 1);
        return;
      }
      var named = groupOf(group, geo);
      var entry = has(groups, named.key) ? groups[named.key] : null;
      if (entry === null) {
        // The named members are the first peer's of the group: peers come by bytes, then address.
        entry = { named: named, members: [], figures: null, peers: 0, interfaces: [], build: null };
        groups[named.key] = entry;
        groupList.push(entry);
      }
      entry.members.push(peer.figures);
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
    // Every group's figures, and its bytes, which rank it; only the groups shown are written as rows.
    groupList.forEach(function (entry) {
      entry.figures = host.figures.sum(entry.members);
    });
    // The most bytes first; equal bytes by key, in byte order. The rows shown are the first `limit` in
    // that order, and only they are ranked among themselves: the window's groups are never sorted whole.
    var rank = function (a, b) {
      return b.figures.bytes - a.figures.bytes || host.figures.compare(a.named.key, b.named.key);
    };
    var limit = host.params.integer("limit");
    var first = firstRanked(groupList, limit, rank);
    var isShown = new Map();
    first.forEach(function (entry) {
      isShown.set(entry, true);
    });
    var shown = first.map(function (entry) {
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
      return row;
    });
    var dropped = [];
    groupList.forEach(function (entry) {
      if (!isShown.has(entry)) {
        dropped.push(entry.figures);
      }
    });
    var rest = host.figures.sum(dropped);
    return {
      data: {
        group: group,
        rows: shown,
        remainder: host.figures.remainder(dropped.length, rest, null),
        unresolved: bins.list(),
        total: host.figures.volume(total)
      },
      has_rows: peers.length > 0
    };
  }
};
