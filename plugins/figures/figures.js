// The figures library (ADR 0021, step 1): the arithmetic the widget host did in Go, written once
// for every widget plugin that requires this one. A function here is handed what it needs and
// reaches nothing else: no statement, no request, no host. The areas keep the names the host's
// areas had, so a widget that called host.buckets.width() calls
// host.lib.figures.buckets.width(period) and reads the same answer.

function isWhole(value) { return typeof value === "number" && isFinite(value) && Math.floor(value) === value; }

function whole(value, what) {
  if (!isWhole(value)) throw new TypeError(what + " is " + describe(value) + ", not a whole number");
  return value;
}

function positive(value, what) {
  whole(value, what);
  if (value <= 0) throw new TypeError(what + " is " + value + ", not a positive number");
  return value;
}

function text(value, what) {
  if (typeof value !== "string") throw new TypeError(what + " is " + describe(value) + ", not a string");
  return value;
}

function interval(value, what) {
  if (value === null || typeof value !== "object") throw new TypeError(what + " is " + describe(value) + ", not an interval {from, to}");
  return { from: whole(value.from, what + ".from"), to: whole(value.to, what + ".to") };
}

function describe(value) {
  if (value === undefined) return "undefined";
  if (value === null) return "null";
  return typeof value + " " + String(value);
}

// floorMod is the non-negative remainder of value by width.
function floorMod(value, width) {
  var remainder = value % width;
  if (remainder < 0) remainder += width;
  return remainder;
}

// ---- buckets: the time buckets of a windowed widget's series, as the core cuts them: UTC-aligned
// multiples of their width from the epoch, one minute, a quarter-hour, an hour or a day by period.
var BUCKET_SECONDS = { "1h": 60, "24h": 900, "7d": 3600, "30d": 86400 };

exports.buckets = {
  // width is the bucket width of a period, in seconds; a period that is not one of the four is a day's.
  width: function (period) {
    var width = BUCKET_SECONDS[period];
    return width ? width : BUCKET_SECONDS["24h"];
  },
  // intervals are the buckets of a window, each clipped to it: [{from, to}].
  intervals: function (window, width) {
    var held = interval(window, "the window");
    positive(width, "the bucket width");
    var result = [];
    for (var start = held.from - floorMod(held.from, width); start < held.to; start += width) {
      result.push({ from: Math.max(held.from, start), to: Math.min(held.to, start + width) });
    }
    return result;
  },
  floorMod: function (value, width) {
    positive(width, "the width");
    return floorMod(whole(value, "the value"), width);
  },
  // isGap says whether a bucket lies outside the covered interval (null for none) or meets a gap.
  isGap: function (bucket, covered, gaps) {
    var held = interval(bucket, "the bucket");
    if (covered === null || covered === undefined) return true;
    var within = interval(covered, "the covered interval");
    if (within.from > held.from || within.to < held.to) return true;
    var list = gaps ? gaps : [];
    for (var index = 0; index < list.length; index++) {
      if (list[index].from < held.to && list[index].to >= held.from) return true;
    }
    return false;
  }
};

// ---- periods: the pre-computed periods of the aggregates, finest first, and how each cuts time
// into slots: the hour, the UTC day, the ISO week from Monday, the calendar month.
var PERIODS = ["1h", "24h", "7d", "30d"];
var DAY = 86400;

function periodNamed(name) {
  text(name, "the period");
  if (PERIODS.indexOf(name) < 0) throw new TypeError("\"" + name + "\" is not one of the periods 1h, 24h, 7d and 30d");
  return name;
}

function slotStart(name, instant) {
  var t = whole(instant, "the instant");
  switch (periodNamed(name)) {
    case "1h": return t - floorMod(t, 3600);
    case "24h": return t - floorMod(t, DAY);
    case "7d": {
      var day = t - floorMod(t, DAY);
      // 1 January 1970 was a Thursday, three days after its Monday.
      var sinceMonday = floorMod(Math.floor(day / DAY) + 3, 7);
      return day - sinceMonday * DAY;
    }
    default: {
      var date = new Date(t * 1000);
      return Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), 1) / 1000;
    }
  }
}

exports.periods = {
  names: function () { return PERIODS.slice(); },
  slotStart: slotStart,
  // slotEnd is the end of the slot of a period starting at start.
  slotEnd: function (name, start) {
    var begins = slotStart(name, whole(start, "the slot start"));
    switch (name) {
      case "1h": return begins + 3600;
      case "24h": return begins + DAY;
      case "7d": return begins + 7 * DAY;
      default: {
        var date = new Date(begins * 1000);
        return Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 1) / 1000;
      }
    }
  }
};

// ---- mapfilters: the allowed or the blocked half of summable figures, which the maps and the
// destination tables keep apart. The other half's bytes and connections are 0, and a declared
// measure's parts keep the half's own third of measure_parts.
var SUMMABLE = ["bytes", "allowed_bytes", "blocked_bytes", "unknown_bytes", "allowed_connections",
  "blocked_connections", "unknown_connections"];
var PART_INDEX = { allowed: 0, blocked: 1, unknown: 2 };

function number(value, what) {
  if (value === undefined || value === null) return 0;
  if (typeof value !== "number" || !isFinite(value)) throw new TypeError(what + " is " + describe(value) + ", not a number");
  return value;
}

function half(figures, which) {
  if (figures === null || typeof figures !== "object") throw new TypeError("the figures are " + describe(figures) + ", not summable figures");
  var kept = {};
  for (var index = 0; index < SUMMABLE.length; index++) kept[SUMMABLE[index]] = 0;
  kept.bytes = number(figures[which + "_bytes"], which + "_bytes");
  kept[which + "_bytes"] = kept.bytes;
  kept[which + "_connections"] = number(figures[which + "_connections"], which + "_connections");
  if (typeof figures.measure === "string" && figures.measure !== "") {
    kept.measure = figures.measure;
    var parts = figures.measure_parts;
    if (Array.isArray(parts) && parts.length > 0 && parts.length % 3 === 0) {
      var width = parts.length / 3;
      var own = [];
      for (var at = 0; at < parts.length; at++) {
        own.push(Math.floor(at / width) === PART_INDEX[which] ? parts[at] : 0);
      }
      kept.measure_parts = own;
    }
  }
  return kept;
}

exports.mapfilters = {
  allowedOnly: function (figures) { return half(figures, "allowed"); },
  blockedOnly: function (figures) { return half(figures, "blocked"); }
};

// ---- matrixplacement: the keys of the members that are not an interface, which the interface
// matrix and the Sankey name their outside and this-firewall ends by.
exports.matrixplacement = {
  keys: { outside: "outside", north_south: "north_south", this_firewall: "this_firewall" }
};

// ---- specialpurpose: the IANA special-purpose address registries, so a widget says whether an
// address is public, or lies in a range the Internet never routes to it, by arithmetic on the
// address and never by a guess about a name. The rows are the two registries in their published
// order; globally_reachable is null where the registry gives no value.
var REGISTRY = [
  ["0.0.0.0/8", "This network", "RFC 791", false],
  ["0.0.0.0/32", "This host on this network", "RFC 1122", false],
  ["10.0.0.0/8", "Private-Use", "RFC 1918", false],
  ["100.64.0.0/10", "Shared Address Space", "RFC 6598", false],
  ["127.0.0.0/8", "Loopback", "RFC 1122", false],
  ["169.254.0.0/16", "Link Local", "RFC 3927", false],
  ["172.16.0.0/12", "Private-Use", "RFC 1918", false],
  ["192.0.0.0/24", "IETF Protocol Assignments", "RFC 6890", false],
  ["192.0.0.0/29", "IPv4 Service Continuity Prefix", "RFC 7335", false],
  ["192.0.0.8/32", "IPv4 dummy address", "RFC 7600", false],
  ["192.0.0.9/32", "Port Control Protocol Anycast", "RFC 7723", true],
  ["192.0.0.10/32", "Traversal Using Relays around NAT Anycast", "RFC 8155", true],
  ["192.0.0.170/32", "NAT64/DNS64 Discovery", "RFC 8880", false],
  ["192.0.0.171/32", "NAT64/DNS64 Discovery", "RFC 8880", false],
  ["192.0.2.0/24", "Documentation (TEST-NET-1)", "RFC 5737", false],
  ["192.31.196.0/24", "AS112-v4", "RFC 7535", true],
  ["192.52.193.0/24", "AMT", "RFC 7450", true],
  ["192.88.99.0/24", "Deprecated (6to4 Relay Anycast)", "RFC 7526", null],
  ["192.88.99.2/32", "6a44-relay anycast address", "RFC 6751", false],
  ["192.168.0.0/16", "Private-Use", "RFC 1918", false],
  ["192.175.48.0/24", "Direct Delegation AS112 Service", "RFC 7534", true],
  ["198.18.0.0/15", "Benchmarking", "RFC 2544", false],
  ["198.51.100.0/24", "Documentation (TEST-NET-2)", "RFC 5737", false],
  ["203.0.113.0/24", "Documentation (TEST-NET-3)", "RFC 5737", false],
  ["240.0.0.0/4", "Reserved", "RFC 1112", false],
  ["255.255.255.255/32", "Limited Broadcast", "RFC 8190", false],
  ["::1/128", "Loopback Address", "RFC 4291", false],
  ["::/128", "Unspecified Address", "RFC 4291", false],
  ["::ffff:0:0/96", "IPv4-mapped Address", "RFC 4291", false],
  ["64:ff9b::/96", "IPv4-IPv6 Translat.", "RFC 6052", true],
  ["64:ff9b:1::/48", "IPv4-IPv6 Translat.", "RFC 8215", true],
  ["100::/64", "Discard-Only Address Block", "RFC 6666", true],
  ["100:0:0:1::/64", "Dummy IPv6 Prefix", "RFC 9780", false],
  ["2001::/23", "IETF Protocol Assignments", "RFC 2928", false],
  ["2001::/32", "TEREDO", "RFC 4380", true],
  ["2001:1::1/128", "Port Control Protocol Anycast", "RFC 7723", true],
  ["2001:1::2/128", "Traversal Using Relays around NAT Anycast", "RFC 8155", true],
  ["2001:1::3/128", "DNS-SD Service Registration Protocol Anycast", "RFC 9665", true],
  ["2001:2::/48", "Benchmarking", "RFC 5180", true],
  ["2001:3::/32", "AMT", "RFC 7450", true],
  ["2001:4:112::/48", "AS112-v6", "RFC 7535", true],
  ["2001:10::/28", "Deprecated (previously ORCHID)", "RFC 4843", null],
  ["2001:20::/28", "ORCHIDv2", "RFC 7343", true],
  ["2001:30::/28", "Drone Remote ID Protocol Entity Tags (DETs) Prefix", "RFC 9374", true],
  ["2001:db8::/32", "Documentation", "RFC 3849", false],
  ["2002::/16", "6to4", "RFC 3056", true],
  ["2620:4f:8000::/48", "Direct Delegation AS112 Service", "RFC 7534", true],
  ["3fff::/20", "Documentation", "RFC 9637", false],
  // The registry names this block after the routing it serves (RFC 9602), the unambiguous reference.
  ["5f00::/16", "SRv6 SIDs", "RFC 9602", true],
  ["fc00::/7", "Unique-Local", "RFC 4193", true],
  ["fe80::/10", "Link-Local Unicast", "RFC 4291", false]
];

// parseIPv4 reads dotted decimal into four bytes, or null.
function parseIPv4(value) {
  var fields = value.split(".");
  if (fields.length !== 4) return null;
  var bytes = [];
  for (var index = 0; index < 4; index++) {
    if (!/^(0|[1-9][0-9]{0,2})$/.test(fields[index])) return null;
    var octet = parseInt(fields[index], 10);
    if (octet > 255) return null;
    bytes.push(octet);
  }
  return bytes;
}

// parseIPv6 reads the text forms RFC 4291 allows, with an embedded IPv4 at the end, into sixteen
// bytes, or null.
function parseIPv6(value) {
  var zone = value.indexOf("%");
  if (zone >= 0) value = value.slice(0, zone);
  var halves = value.split("::");
  if (halves.length > 2) return null;
  var head = halves[0] === "" ? [] : halves[0].split(":");
  var tail = halves.length === 2 ? (halves[1] === "" ? [] : halves[1].split(":")) : [];
  var groups = [];
  function readGroups(fields, into, last) {
    for (var index = 0; index < fields.length; index++) {
      var field = fields[index];
      if (index === fields.length - 1 && last && field.indexOf(".") >= 0) {
        var embedded = parseIPv4(field);
        if (embedded === null) return false;
        into.push((embedded[0] << 8) | embedded[1], (embedded[2] << 8) | embedded[3]);
        continue;
      }
      if (!/^[0-9a-fA-F]{1,4}$/.test(field)) return false;
      into.push(parseInt(field, 16));
    }
    return true;
  }
  var headGroups = [], tailGroups = [];
  if (!readGroups(head, headGroups, halves.length === 1) || !readGroups(tail, tailGroups, true)) return null;
  if (halves.length === 1) {
    if (headGroups.length !== 8) return null;
    groups = headGroups;
  } else {
    if (headGroups.length + tailGroups.length > 7) return null;
    groups = headGroups;
    for (var filler = headGroups.length + tailGroups.length; filler < 8; filler++) groups.push(0);
    groups = groups.concat(tailGroups);
  }
  var bytes = [];
  for (var at = 0; at < 8; at++) bytes.push(groups[at] >> 8, groups[at] & 255);
  return bytes;
}

// parseAddress reads an address as the core does: an IPv4-mapped IPv6 address is its IPv4 address.
function parseAddress(value) {
  if (typeof value !== "string") return null;
  var v4 = value.indexOf(":") < 0 ? parseIPv4(value) : null;
  if (v4 !== null) return { v4: true, bytes: v4 };
  if (value.indexOf(":") < 0) return null;
  var v6 = parseIPv6(value);
  if (v6 === null) return null;
  var mapped = true;
  for (var index = 0; index < 10; index++) if (v6[index] !== 0) mapped = false;
  if (mapped && v6[10] === 255 && v6[11] === 255) return { v4: true, bytes: v6.slice(12) };
  return { v4: false, bytes: v6 };
}

function parsePrefix(value) {
  var slash = value.indexOf("/");
  var address = parseAddress(value.slice(0, slash));
  return { address: address, bits: parseInt(value.slice(slash + 1), 10) };
}

var BLOCKS = REGISTRY.map(function (row) {
  var prefix = parsePrefix(row[0]);
  return { block: row[0], name: row[1], reference: row[2], globally_reachable: row[3], address: prefix.address, bits: prefix.bits };
});

function contains(block, address) {
  if (block.address.v4 !== address.v4) return false;
  var bits = block.bits;
  for (var index = 0; index < block.address.bytes.length && bits > 0; index++, bits -= 8) {
    var mask = bits >= 8 ? 255 : (255 << (8 - bits)) & 255;
    if ((block.address.bytes[index] & mask) !== (address.bytes[index] & mask)) return false;
  }
  return true;
}

// lookup is the most specific registry block holding an address, or null. The most specific block
// decides reachability: 192.0.0.9/32 is globally reachable although the /24 holding it is not.
function lookup(address) {
  var best = null;
  for (var index = 0; index < BLOCKS.length; index++) {
    var candidate = BLOCKS[index];
    if (!contains(candidate, address)) continue;
    if (best === null || candidate.bits > best.bits) best = candidate;
  }
  return best;
}

exports.specialpurpose = {
  // classify answers {special_purpose, public}: the most specific block holding the address, or
  // null, and whether the Internet can reach it -- outside every block, or inside a block the
  // registry marks globally reachable. A text that is not an address is in no block and not public.
  classify: function (value) {
    text(value, "the address");
    var address = parseAddress(value);
    if (address === null) return { special_purpose: null, public: false };
    var block = lookup(address);
    if (block === null) return { special_purpose: null, public: true };
    return {
      special_purpose: { block: block.block, name: block.name, reference: block.reference, globally_reachable: block.globally_reachable },
      public: block.globally_reachable === true
    };
  },
  registries: ["iana-ipv4-special-registry", "iana-ipv6-special-registry"]
};

// ---- installation: what a response names its members by -- this installation's interfaces,
// clients, owners and rules -- read once per request through the widget's own statements over the
// installation plugin's views (plugins/installation/installation.relations.sql), which the widget
// declares under these names:
//
//   -- statement: widget_installation_interfaces   SELECT * FROM plugin_installation.interface_label ORDER BY id;
//   -- statement: widget_installation_client       SELECT * FROM plugin_installation.client_name WHERE id = :client_id;
//   -- statement: widget_installation_owners       SELECT * FROM plugin_installation.owner_name ORDER BY id;
//   -- statement: widget_installation_rule         SELECT * FROM plugin_installation.rule_label WHERE id = :rule_id;
//
// Each function takes the host and reads through host.query; a library is evaluated once per
// request, so what it read is kept for the request and read once.
var INSTALLATION = { interfaces: null, byId: null, clients: {}, owners: null, rules: {} };

function hostOf(host) {
  if (host === null || typeof host !== "object" || !host.query) throw new TypeError("the host is " + describe(host) + ", not the widget's host");
  return host;
}

function readInterfaces(host) {
  if (INSTALLATION.interfaces === null) {
    INSTALLATION.interfaces = hostOf(host).query.rows("widget_installation_interfaces", {});
    INSTALLATION.byId = {};
    INSTALLATION.interfaces.forEach(function (info) { INSTALLATION.byId[info.id] = info; });
  }
  return INSTALLATION.interfaces;
}

exports.installation = {
  // interfaces are every interface: [{id, identifier, device, description, user_label, label,
  // labelled, link_kind, vlan_tag, upstream}].
  interfaces: function (host) { return readInterfaces(host); },
  // interface is one interface by id, or null.
  interface: function (host, id) {
    readInterfaces(host);
    whole(id, "the interface id");
    var info = INSTALLATION.byId[id];
    return info === undefined ? null : info;
  },
  // client is one client, {id, identity_kind, identity_key, interface_id, mac, hostname,
  // last_address, owner_id, owner_assigned_at, unstable_identity, last_seen_at}, or null.
  client: function (host, id) {
    whole(id, "the client id");
    if (!Object.prototype.hasOwnProperty.call(INSTALLATION.clients, id)) {
      INSTALLATION.clients[id] = hostOf(host).query.row("widget_installation_client", { client_id: id });
    }
    return INSTALLATION.clients[id];
  },
  // owners are every owner: [{id, display_name}].
  owners: function (host) {
    if (INSTALLATION.owners === null) INSTALLATION.owners = hostOf(host).query.rows("widget_installation_owners", {});
    return INSTALLATION.owners;
  },
  // rule is one rule, {id, pf_label, description, action, automatic}, or null.
  rule: function (host, id) {
    whole(id, "the rule id");
    if (!Object.prototype.hasOwnProperty.call(INSTALLATION.rules, id)) {
      INSTALLATION.rules[id] = hostOf(host).query.row("widget_installation_rule", { rule_id: id });
    }
    return INSTALLATION.rules[id];
  }
};

// ---- volume: the rolling window of volume every volume widget reads, assembled as the core assembled
// it (ADR 0021, step 4): the whole hours inside the window from the 1 h slots of the rollups plugin,
// the partial hours at either edge from the classified flows, in the measure the request is answered
// under -- the host runs a statement's measured twin when the answer is in a declared measure. Reading
// it records the window's coverage and the freshness of the slots read. The widget declares:
//
//   -- statement: widget_volume_slots / widget_volume_slots_measured    the 1 h slots of [:from, :to)
//   -- statement: widget_volume_flows / widget_volume_flows_measured    the flows of [:from, :to)
//   -- statement: widget_volume_earliest_slot                            SELECT min(period_start_at) AS earliest
//
// each row keyed by src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction
// and exit_leg_device, with its summable figures (host.figures) under their names.
var VOLUME_KEYS = ["src_interface_id", "dst_interface_id", "peer_address", "traffic_scope", "traffic_direction", "exit_leg_device"];
var FIGURE_KEYS = ["bytes", "allowed_bytes", "blocked_bytes", "unknown_bytes", "allowed_connections", "blocked_connections",
  "unknown_connections", "measure", "measure_parts"];

function hourEdges(window) {
  var first = window.from - floorMod(window.from, 3600);
  if (first < window.from) first += 3600;
  return { first: first, last: window.to - floorMod(window.to, 3600) };
}

function windowOf(host, what) {
  var window = hostOf(host).request.window;
  if (window === null || window === undefined) throw new TypeError(what + " reads a rolling window, and the widget " + host.request.type + " is not windowed");
  return window;
}

// readParts runs a statement over the edges and the whole hours of a window: the flows statement for
// [from, first hour) and [last hour, to), the slots statement between, as the core read them.
function readParts(host, window, flows, slots) {
  var edges = hourEdges(window);
  var parts = [];
  function read(name, from, to) {
    if (to <= from) return;
    host.query.rows(name, { from: from, to: to }).forEach(function (row) { parts.push(row); });
  }
  if (edges.first < edges.last) {
    read(flows, window.from, edges.first);
    read(slots, edges.first, edges.last);
    read(flows, edges.last, window.to);
  } else {
    read(flows, window.from, window.to);
  }
  return parts;
}

function figuresOf(row) {
  var figures = {};
  FIGURE_KEYS.forEach(function (key) { if (row[key] !== undefined && row[key] !== null) figures[key] = row[key]; });
  return figures;
}

function volumeKey(row) {
  function part(value) { return value === null || value === undefined ? "-" : String(value); }
  return [part(row.src_interface_id), part(row.dst_interface_id),
    row.peer_address === null || row.peer_address === undefined ? "-" : "=" + row.peer_address,
    row.traffic_scope, row.traffic_direction,
    row.exit_leg_device === null || row.exit_leg_device === undefined ? "-" : "=" + row.exit_leg_device].join("\u001f");
}

function compareText(a, b) { return a < b ? -1 : (a > b ? 1 : 0); }

// coverAssembled records the coverage of a window assembled from the slots and the flows: from the
// earliest flow or slot kept, whichever is older, to now; none when neither holds a record.
function coverAssembled(host, earliestSlotStatement) {
  var earliestFlow = host.envelope.earliest("widget_earliest_flow");
  var slot = host.query.row(earliestSlotStatement, {});
  var earliestSlot = slot === null ? null : slot.earliest;
  var earliest = null;
  if (earliestFlow !== null && earliestSlot !== null) earliest = Math.min(earliestFlow, earliestSlot);
  else if (earliestFlow !== null) earliest = earliestFlow;
  else if (earliestSlot !== null) earliest = earliestSlot;
  host.envelope.coverFrom(earliest);
  if (earliest === null) return null;
  var window = host.request.window;
  var covered = { from: Math.max(window.from, earliest), to: Math.min(window.to, host.request.now) };
  return covered.to > covered.from ? covered : null;
}

exports.volume = {
  directions: { outbound: "outbound", inbound: "inbound", inter_interface: "inter_interface",
    to_this_firewall: "to_this_firewall", from_this_firewall: "from_this_firewall" },
  scopes: { all: "all", east_west: "east_west", north_south: "north_south", this_firewall: "this_firewall" },
  // window answers {requested, covered, rows}: covered is null when the store holds none of the window;
  // the rows are by bytes then key.
  window: function (host) {
    var window = windowOf(host, "volume.window");
    var sums = {};
    var order = [];
    readParts(host, window, "widget_volume_flows", "widget_volume_slots").forEach(function (row) {
      var key = volumeKey(row);
      if (sums[key] === undefined) {
        var kept = {};
        VOLUME_KEYS.forEach(function (name) { kept[name] = row[name] === undefined ? null : row[name]; });
        sums[key] = { row: kept, figures: figuresOf(row) };
        order.push(key);
      } else {
        sums[key].figures = host.figures.add(sums[key].figures, figuresOf(row));
      }
    });
    var rows = order.map(function (key) {
      var kept = sums[key].row;
      FIGURE_KEYS.forEach(function (name) { if (sums[key].figures[name] !== undefined) kept[name] = sums[key].figures[name]; });
      return { key: key, row: kept };
    });
    rows.sort(function (a, b) { return a.row.bytes !== b.row.bytes ? (a.row.bytes > b.row.bytes ? -1 : 1) : compareText(a.key, b.key); });
    // The left edge is read from the flows: when its flows are purged while the slot of its hour survives,
    // the edge is not covered.
    var earliestFlow = host.envelope.earliest("widget_earliest_flow");
    var slot = host.query.row("widget_volume_earliest_slot", {});
    var earliestSlot = slot === null ? null : slot.earliest;
    var covered = null;
    if (earliestFlow !== null || earliestSlot !== null) {
      var dataStart = earliestFlow;
      if (earliestFlow === null || (earliestSlot !== null && earliestSlot < dataStart)) dataStart = earliestSlot;
      covered = { from: Math.max(window.from, dataStart), to: Math.min(window.to, host.request.now) };
      var edges = hourEdges(window);
      if ((earliestFlow === null || window.from < earliestFlow) && earliestSlot !== null &&
          earliestSlot <= window.from - floorMod(window.from, 3600) && edges.first < edges.last) {
        covered.from = Math.max(covered.from, edges.first);
      }
      if (covered.to <= covered.from) covered = null;
    }
    host.envelope.cover(covered);
    host.envelope.hourFreshness(window.from, window.to);
    // The outside peers of the rows are placed: their geolocations are read together at the first one asked for.
    exports.services.place(rows.map(function (entry) { return entry.row.peer_address; }));
    return { requested: { from: window.from, to: window.to }, covered: covered, rows: rows.map(function (entry) { return entry.row; }) };
  },
  // total is the summable figures of every row that counts a connection: every row but the second
  // legs, so a connection logged on two interfaces is counted once.
  total: function (host, rows) {
    var counted = [];
    rows.forEach(function (row) { if (row.exit_leg_device === null || row.exit_leg_device === undefined) counted.push(figuresOf(row)); });
    return hostOf(host).figures.sum(counted);
  },
  // localInterface is the inside interface a row belongs to -- the source of an outbound,
  // inter-interface or to-this-firewall row, the destination of an inbound or from-this-firewall row --
  // or null when neither end is inside.
  localInterface: function (row) {
    var local = (row.traffic_direction === "inbound" || row.traffic_direction === "from_this_firewall") ? row.dst_interface_id : row.src_interface_id;
    return local === undefined ? null : local;
  },
  // scopeMatches says whether a row is in a traffic scope; "all" holds every row.
  scopeMatches: function (scope, row) {
    text(scope, "the scope");
    return scope === "all" || scope === "" || row.traffic_scope === scope;
  }
};

// ---- clients: every client's logged volume over the request's window, assembled as the interface
// family's window is -- whole hours from the client and peer families' 1 h slots, the partial hours at
// either edge from the flows. A client's flows are those whose inside end it is; this firewall is never
// a client, and a second leg is its first leg's connection. The widget declares:
//
//   -- statement: widget_client_volume_slots / _measured   client_id, traffic_direction and the figures
//   -- statement: widget_client_volume_flows / _measured   the same from the flows
//   -- statement: widget_client_peer_slots                 DISTINCT client_id, peer
//   -- statement: widget_client_peer_flows                 the same from the flows
//   -- statement: widget_client_earliest_slot              SELECT min(period_start_at) AS earliest
exports.clients = {
  // volumes answers {requested, covered, clients}, one entry per client with traffic in the window, by
  // ascending id: {client_id, figures, by_direction, destinations}.
  volumes: function (host) {
    var window = windowOf(host, "clients.volumes");
    var members = {};
    var ids = [];
    function member(id) {
      if (members[id] === undefined) { members[id] = { figures: host.figures.zero(), byDirection: {}, peers: {} }; ids.push(id); }
      return members[id];
    }
    readParts(host, window, "widget_client_volume_flows", "widget_client_volume_slots").forEach(function (row) {
      var entry = member(row.client_id);
      var figures = figuresOf(row);
      entry.figures = host.figures.add(entry.figures, figures);
      entry.byDirection[row.traffic_direction] = host.figures.add(
        entry.byDirection[row.traffic_direction] === undefined ? host.figures.zero() : entry.byDirection[row.traffic_direction], figures);
    });
    readParts(host, window, "widget_client_peer_flows", "widget_client_peer_slots").forEach(function (row) {
      member(row.client_id).peers[row.peer] = true;
    });
    var covered = coverAssembled(host, "widget_client_earliest_slot");
    host.envelope.hourFreshness(window.from, window.to);
    ids.sort(function (a, b) { return a - b; });
    var clients = ids.map(function (id) {
      var entry = members[id];
      return { client_id: id, figures: entry.figures, by_direction: host.json.map(entry.byDirection),
        destinations: Object.keys(entry.peers).length };
    });
    return { requested: { from: window.from, to: window.to }, covered: covered, clients: clients };
  }
};

// ---- peers: the outside peers of the rolling window and where the geolocation placed them, for the
// map, country, operator and Sankey widgets. A peer the geolocation could not place is never dropped:
// it lands in a named unplaced bin, so placed plus unplaced is the total. The rows are the volume
// area's; a geolocation is what host.services.geolocation answers; figures are summable figures.
var UNPLACED = { miss: "miss", pending: "pending", no_row: "no_row", no_coordinates: "no_coordinates",
  east_west: "east_west", this_firewall: "this_firewall_without_outside_end", other_endpoint: "other_endpoint" };

function interfaceFilterOf(value) {
  if (value === null || value === undefined) return null;
  if (!Array.isArray(value)) throw new TypeError("the interface filter is " + describe(value) + ", not an array of whole numbers");
  var selected = {};
  value.forEach(function (id) { selected[whole(id, "the interface filter's member")] = true; });
  return selected;
}

function keepInterface(selected, row) {
  if (selected === null) return true;
  var local = exports.volume.localInterface(row);
  return local !== null && selected[local] === true;
}

function sortedPeers(peers) {
  var list = Object.keys(peers).map(function (address) { return peers[address]; });
  list.sort(function (a, b) { return a.figures.bytes !== b.figures.bytes ? (a.figures.bytes > b.figures.bytes ? -1 : 1) : compareText(a.address, b.address); });
  return list.map(function (peer) {
    var ids = Object.keys(peer.interfaces).map(Number);
    ids.sort(function (a, b) { return a - b; });
    return { address: peer.address, figures: peer.figures, interfaces: ids };
  });
}

function geoOf(geo) {
  if (geo === null || typeof geo !== "object") throw new TypeError("the geolocation is " + describe(geo) + ", not what host.services.geolocation answers");
  return geo;
}

exports.peers = {
  reasons: UNPLACED,
  // outside groups the window's rows by outside peer, in a scope and for an interface filter (an id
  // list, or null for none): {peers, others}. peers is [{address, figures, interfaces}], by bytes then
  // address; others is every row of the scope and filter that has no outside peer, in order. A second
  // leg is in neither.
  outside: function (host, rows, scope, interfaces) {
    hostOf(host);
    text(scope, "the scope");
    var selected = interfaceFilterOf(interfaces);
    var peers = {};
    var others = [];
    rows.forEach(function (row) {
      if ((row.exit_leg_device !== null && row.exit_leg_device !== undefined) || !exports.volume.scopeMatches(scope, row) ||
          !keepInterface(selected, row)) return;
      if (row.peer_address === null || row.peer_address === undefined) { others.push(row); return; }
      var peer = peers[row.peer_address];
      if (peer === undefined) { peer = { address: row.peer_address, figures: host.figures.zero(), interfaces: {} }; peers[row.peer_address] = peer; }
      peer.figures = host.figures.add(peer.figures, figuresOf(row));
      var local = exports.volume.localInterface(row);
      if (local !== null) peer.interfaces[local] = true;
    });
    return { peers: sortedPeers(peers), others: others };
  },
  // sorted is peers [{address, figures, interfaces}] in the same order, each interface list sorted.
  sorted: function (host, listed) {
    hostOf(host);
    var peers = {};
    listed.forEach(function (peer) {
      if (peers[peer.address] !== undefined) throw new TypeError("peers.sorted is handed the address " + peer.address + " twice");
      var interfaces = {};
      (peer.interfaces || []).forEach(function (id) { interfaces[id] = true; });
      peers[peer.address] = { address: peer.address, figures: host.figures.add(host.figures.zero(), figuresOf(peer.figures)), interfaces: interfaces };
    });
    return sortedPeers(peers);
  },
  // keepInterface says whether a row's inside interface passes an interface filter.
  keepInterface: function (interfaces, row) { return keepInterface(interfaceFilterOf(interfaces), row); },
  // placement is "" for an address placed with coordinates, otherwise its unplaced bin.
  placement: function (geo) {
    geoOf(geo);
    switch (geo.lookup_state) {
      case "resolved": return (geo.latitude === null || geo.latitude === undefined || geo.longitude === null || geo.longitude === undefined) ? UNPLACED.no_coordinates : "";
      case "miss": return UNPLACED.miss;
      case "pending": return UNPLACED.pending;
      default: return UNPLACED.no_row;
    }
  },
  // placementState is the bin of an address the geolocation did not resolve.
  placementState: function (geo) {
    geoOf(geo);
    switch (geo.lookup_state) {
      case "miss": return UNPLACED.miss;
      case "pending": return UNPLACED.pending;
      default: return UNPLACED.no_row;
    }
  },
  // bins are the unplaced bins, each reason present even at zero: {add(reason, figures, addresses),
  // addAddress(reason, address, figures), list(), total()}; list() is [{reason, addresses, volume}] in
  // the order the reasons were first named, with members [{address, volume}] when listed.
  bins: function (host, reasons, listed) {
    hostOf(host);
    var order = reasons.slice();
    var figures = {};
    var members = {};
    var addresses = {};
    order.forEach(function (reason) { figures[reason] = host.figures.zero(); members[reason] = 0; });
    function add(reason, added, count) {
      text(reason, "the reason");
      if (figures[reason] === undefined) { figures[reason] = host.figures.zero(); members[reason] = 0; order.push(reason); }
      figures[reason] = host.figures.add(figures[reason], added);
      members[reason] += count;
    }
    return {
      add: function (reason, added, count) { add(reason, figuresOf(added), whole(count, "the addresses")); },
      addAddress: function (reason, address, added) {
        text(address, "the address");
        if (addresses[reason] === undefined) addresses[reason] = {};
        var known = addresses[reason][address];
        if (known === undefined) { addresses[reason][address] = host.figures.zero(); add(reason, host.figures.zero(), 1); }
        addresses[reason][address] = host.figures.add(addresses[reason][address], figuresOf(added));
        figures[reason] = host.figures.add(figures[reason], figuresOf(added));
      },
      list: function () {
        return order.map(function (reason) {
          var bin = { reason: reason, addresses: members[reason], volume: host.figures.volume(figures[reason]) };
          if (listed === true) {
            var held = addresses[reason] || {};
            var list = Object.keys(held).map(function (address) { return { address: address, volume: host.figures.volume(held[address]) }; });
            list.sort(function (a, b) { return a.volume.bytes !== b.volume.bytes ? (a.volume.bytes > b.volume.bytes ? -1 : 1) : compareText(a.address, b.address); });
            bin.members = list;
          }
          return bin;
        });
      },
      total: function () { return host.figures.sum(order.map(function (reason) { return figures[reason]; })); }
    };
  }
};

// ---- sites: the site names of the request's window and the table row a set of them sums to -- the
// shared figures of every widget that ranks inferred site names. Inside the flow horizon they are read
// from the attributions, which carry each name's method; for the whole hours the retention purge has
// removed, from the domain family's 1 h slots, which keep the totals and not the method. The widget
// declares, in a sites.statements.sql:
//
//   -- statement: widget_site_flows / _measured       the attributed flows of [:from, :to), per site,
//                                                     client, interface, method and destination operator
//   -- statement: widget_site_slots / _measured       the domain family's 1 h slots of [:from, :to)
//   -- statement: widget_address_flows / _measured    the eligible flows no name was inferred for
//   -- statement: widget_sites_purged_before          SELECT purged_before FROM retention_purge WHERE id = 1
//   -- statement: widget_site_client_interface        SELECT interface_id FROM plugin_installation.client WHERE id = :client_id
var METHOD_NOT_RECORDED = "not_recorded_beyond_flow_horizon";
var SITE_CLIENT_INTERFACES = {};

function idSet(list) {
  if (list === null || list === undefined) return null;
  var set = {};
  list.forEach(function (id) { set[id] = true; });
  return set;
}

function keptBy(clients, interfaces, clientId, interfaceId) {
  if (clients !== null && (clientId === null || clientId === undefined || clients[clientId] !== true)) return false;
  if (interfaces !== null && (interfaceId === null || interfaceId === undefined || interfaces[interfaceId] !== true)) return false;
  return true;
}

function orNull(value) { return value === undefined ? null : value; }

function withMeasure(into, row) {
  if (row.measure !== undefined && row.measure !== null) { into.measure = row.measure; if (row.measure_parts !== undefined && row.measure_parts !== null) into.measure_parts = row.measure_parts; }
  return into;
}

// measuredFigures are a row's bytes in the measure it was read in.
function measuredFigures(row) { return withMeasure({ bytes: row.bytes }, row); }

function clientInterface(host, id) {
  if (!Object.prototype.hasOwnProperty.call(SITE_CLIENT_INTERFACES, id)) {
    var found = host.query.row("widget_site_client_interface", { client_id: id });
    SITE_CLIENT_INTERFACES[id] = found === null ? null : orNull(found.interface_id);
  }
  return SITE_CLIENT_INTERFACES[id];
}

function methodCounts(rows) {
  var counts = { resolver_cache_answer: 0, lookup_timing: 0, not_recorded_beyond_flow_horizon: 0 };
  rows.forEach(function (row) {
    if (row.method === "resolver_cache_answer") counts.resolver_cache_answer += row.flows;
    else if (row.method === "lookup_timing") counts.lookup_timing += row.flows;
    else counts.not_recorded_beyond_flow_horizon += row.flows;
  });
  return counts;
}

function sortedIds(set) {
  var ids = Object.keys(set).map(Number);
  ids.sort(function (a, b) { return a - b; });
  return ids;
}

exports.sites = {
  // rows are the window's site figures, filtered by the request's interfaces and clients references:
  // [{site, client_id, interface_id, method, country_code, asn, operator, flows, bytes, delay_sum,
  // delay_max}]. Reading them records the window's coverage, and the named state
  // site_methods_not_recorded_beyond_flow_horizon when slots were read.
  rows: function (host) {
    var window = windowOf(host, "sites.rows");
    var interfaces = idSet(host.refs.ids("interfaces"));
    var clients = idSet(host.refs.ids("clients"));
    var rows = [];
    var flowsFrom = window.from;
    var purgedRow = host.query.row("widget_sites_purged_before", {});
    var purged = purgedRow === null ? null : orNull(purgedRow.purged_before);
    var earliest = host.envelope.earliest("widget_earliest_flow");
    var coveredFrom = window.to;
    if (earliest !== null) coveredFrom = Math.max(window.from, earliest);
    if (purged !== null && purged > window.from) {
      var boundary = purged - floorMod(purged, 3600);
      if (boundary < purged) boundary += 3600;
      var firstHour = hourEdges(window).first;
      boundary = Math.min(boundary, window.to - floorMod(window.to, 3600));
      if (firstHour < boundary) {
        var read = false;
        host.query.rows("widget_site_slots", { from: firstHour, to: boundary }).forEach(function (slot) {
          var clientId = orNull(slot.client_id);
          var item = { site: slot.site, client_id: clientId, interface_id: clientId === null ? null : clientInterface(host, clientId),
            method: METHOD_NOT_RECORDED, country_code: null, asn: null, operator: null, flows: slot.flows, bytes: slot.bytes,
            delay_sum: null, delay_max: null };
          withMeasure(item, slot);
          read = true;
          if (keptBy(clients, interfaces, item.client_id, item.interface_id)) rows.push(item);
        });
        flowsFrom = boundary;
        if (read) {
          coveredFrom = Math.min(coveredFrom, firstHour);
          host.envelope.state("site_methods_not_recorded_beyond_flow_horizon");
        }
      }
    }
    host.query.rows("widget_site_flows", { from: flowsFrom, to: window.to }).forEach(function (flow) {
      var item = { site: flow.site, client_id: orNull(flow.client_id), interface_id: orNull(flow.interface_id), method: flow.method,
        country_code: orNull(flow.country_code), asn: orNull(flow.asn), operator: orNull(flow.operator), flows: flow.flows,
        bytes: flow.bytes, delay_sum: orNull(flow.delay_sum), delay_max: orNull(flow.delay_max) };
      withMeasure(item, flow);
      if (keptBy(clients, interfaces, item.client_id, item.interface_id)) rows.push(item);
    });
    var until = Math.min(window.to, host.request.now);
    host.envelope.cover(coveredFrom < until ? { from: coveredFrom, to: until } : null);
    return rows;
  },
  // accumulate is the table row a list of site rows sums to, named name: {kind: "site", name, flows,
  // bytes, clients, client_ids, methods, mean_delay_seconds, max_delay_seconds, country_code, asn,
  // operator}, the country and operator being those that carried most of its bytes.
  accumulate: function (host, name, rows) {
    hostOf(host);
    text(name, "the row's name");
    if (!Array.isArray(rows)) throw new TypeError("the site rows are " + describe(rows) + ", not an array");
    var flows = 0, figures = {}, clients = {}, delaySum = 0, timed = 0, maxDelay = null;
    var geoBytes = {}, geo = {};
    rows.forEach(function (item) {
      if (item.delay_sum !== null && item.delay_sum !== undefined && (item.delay_max === null || item.delay_max === undefined)) {
        throw new TypeError("a site row carries delay_sum without delay_max");
      }
      flows += item.flows;
      figures = host.figures.add(figures, measuredFigures(item));
      if (item.client_id !== null && item.client_id !== undefined) clients[item.client_id] = true;
      if (item.delay_sum !== null && item.delay_sum !== undefined) {
        delaySum += item.delay_sum;
        timed += item.flows;
        if (maxDelay === null || item.delay_max > maxDelay) maxDelay = item.delay_max;
      }
      var hasCountry = item.country_code !== null && item.country_code !== undefined;
      var hasAsn = item.asn !== null && item.asn !== undefined;
      if (hasCountry || hasAsn) {
        var key = (hasCountry ? item.country_code : "") + "|" + (hasAsn ? String(item.asn) : "");
        geoBytes[key] = (geoBytes[key] || 0) + item.bytes;
        geo[key] = [orNull(item.country_code), orNull(item.asn), orNull(item.operator)];
      }
    });
    var row = { kind: "site", name: name, flows: host.figures.count(flows), bytes: host.figures.byteCount(figures),
      clients: 0, client_ids: sortedIds(clients), methods: methodCounts(rows),
      mean_delay_seconds: timed > 0 ? delaySum / timed : null, max_delay_seconds: maxDelay,
      country_code: null, asn: null, operator: null };
    row.clients = row.client_ids.length;
    var best = "", bestBytes = -1;
    Object.keys(geoBytes).sort().forEach(function (key) { if (geoBytes[key] > bestBytes) { best = key; bestBytes = geoBytes[key]; } });
    if (geo[best] !== undefined) { row.country_code = geo[best][0]; row.asn = geo[best][1]; row.operator = geo[best][2]; }
    return row;
  },
  // addresses are the window's eligible flows no site name was inferred for, per destination address,
  // filtered as rows is: [{address, client_id, interface_id, lookup_state, country_code, asn,
  // operator, flows, bytes}]. A window reaching past the flow horizon records the named state
  // unnamed_destinations_beyond_flow_horizon.
  addresses: function (host) {
    var window = windowOf(host, "sites.addresses");
    var interfaces = idSet(host.refs.ids("interfaces"));
    var clients = idSet(host.refs.ids("clients"));
    var rows = [];
    host.query.rows("widget_address_flows", { from: window.from, to: window.to }).forEach(function (flow) {
      var item = { address: flow.address, client_id: orNull(flow.client_id), interface_id: orNull(flow.interface_id),
        lookup_state: flow.lookup_state, country_code: orNull(flow.country_code), asn: orNull(flow.asn), operator: orNull(flow.operator),
        flows: flow.flows, bytes: flow.bytes };
      withMeasure(item, flow);
      if (keptBy(clients, interfaces, item.client_id, item.interface_id)) rows.push(item);
    });
    host.envelope.coverFlowsState("unnamed_destinations_beyond_flow_horizon");
    return rows;
  },
  // accumulateAddress is the table row of one address: {kind: "address", address, flows, bytes,
  // clients, client_ids, lookup_state, country_code, asn, operator}.
  accumulateAddress: function (host, address, rows) {
    hostOf(host);
    text(address, "the address");
    if (!Array.isArray(rows)) throw new TypeError("the address rows are " + describe(rows) + ", not an array");
    var flows = 0, figures = {}, clients = {};
    var row = { kind: "address", address: address, flows: null, bytes: null, clients: 0, client_ids: [], lookup_state: "no_row",
      country_code: null, asn: null, operator: null };
    rows.forEach(function (item) {
      if (item.address !== address) throw new TypeError("accumulateAddress(" + address + ") is handed a row of " + item.address);
      flows += item.flows;
      figures = host.figures.add(figures, measuredFigures(item));
      if (item.client_id !== null && item.client_id !== undefined) clients[item.client_id] = true;
      // One address has one geolocation, so every row of it carries the same.
      row.lookup_state = item.lookup_state; row.country_code = orNull(item.country_code); row.asn = orNull(item.asn); row.operator = orNull(item.operator);
    });
    row.flows = host.figures.count(flows);
    row.bytes = host.figures.byteCount(figures);
    row.client_ids = sortedIds(clients);
    row.clients = row.client_ids.length;
    return row;
  }
};

// ---- attribution: how much of the window's outbound traffic could be named at all, as a whole and
// per client. A rate is UNDEFINED -- null, never 0 -- when no resolver source answered for the window
// or there is no eligible flow to divide by; rate_state says which. The widget declares, in an
// attribution.statements.sql:
//
//   -- statement: widget_attribution_window              the window's counts: eligible_flows, named_flows,
//                                                        mean_delay, max_delay, by_cache_answer, by_lookup_timing
//   -- statement: widget_attribution_lookup_coverage     covered > 0 when a resolver source answered for the window
//   -- statement: widget_attribution_cache_coverage      covered > 0 when the resolver's cache was read over it
//   -- statement: widget_attribution_rate_by_client / _measured   the same counts per client_id, with the eligible bytes
//
// and widget_installation_client (the installation area) for the client a row names.
var ATTRIBUTION = { whole: null, counts: null };
var EMPTY_CLIENT = { id: 0, identity_kind: "", identity_key: "", interface_id: null, mac: null, hostname: null, last_address: null,
  owner_id: null, owner_assigned_at: null, unstable_identity: false, last_seen_at: 0 };

function attributionWhole(host) {
  if (ATTRIBUTION.whole === null) {
    var window = windowOf(host, "attribution.window");
    var parameters = { from: window.from, to: window.to };
    var lookups = host.query.row("widget_attribution_lookup_coverage", parameters);
    var cache = host.query.row("widget_attribution_cache_coverage", parameters);
    var counts = host.query.row("widget_attribution_window", parameters);
    ATTRIBUTION.whole = { resolver_covered: lookups !== null && lookups.covered > 0, cache_covered: cache !== null && cache.covered > 0,
      eligible: counts.eligible_flows, named: counts.named_flows, mean: orNull(counts.mean_delay), max: orNull(counts.max_delay),
      cache: counts.by_cache_answer, timing: counts.by_lookup_timing };
  }
  return ATTRIBUTION.whole;
}

function attributionCounts(host) {
  if (ATTRIBUTION.counts === null) {
    var window = windowOf(host, "attribution.clients");
    var counts = {};
    host.query.rows("widget_attribution_rate_by_client", { from: window.from, to: window.to }).forEach(function (row) {
      counts[row.client_id] = { eligible: row.eligible_flows, named: row.named_flows, mean: orNull(row.mean_delay), max: orNull(row.max_delay),
        cache: row.by_cache_answer, timing: row.by_lookup_timing, figures: withMeasure({ bytes: row.eligible_bytes }, row) };
    });
    ATTRIBUTION.counts = counts;
  }
  return ATTRIBUTION.counts;
}

function rateOf(count, resolverCovered) {
  if (!resolverCovered) return { rate: null, state: "undefined_no_resolver_evidence" };
  if (count.eligible === 0) return { rate: null, state: "undefined_no_eligible_flow" };
  return { rate: count.named / count.eligible, state: "defined" };
}

exports.attribution = {
  // window answers {resolver_covered, cache_covered, attribution}: whether a resolver source answered
  // for the window, whether its cache was read over it, and the window's attribution row as a whole
  // (its client empty, no eligible_bytes).
  window: function (host) {
    var whole = attributionWhole(host);
    var rate = rateOf(whole, whole.resolver_covered);
    return { resolver_covered: whole.resolver_covered, cache_covered: whole.cache_covered,
      attribution: { client: EMPTY_CLIENT, eligible_flows: host.figures.count(whole.eligible), named_flows: host.figures.count(whole.named),
        methods: { resolver_cache_answer: whole.cache, lookup_timing: whole.timing, not_recorded_beyond_flow_horizon: 0 },
        rate: rate.rate, rate_state: rate.state, mean_delay_seconds: whole.mean, max_delay_seconds: whole.max } };
  },
  // clients are the ids of the clients with an eligible flow in the window, ascending.
  clients: function (host) {
    attributionWhole(host);
    return sortedIds(attributionCounts(host));
  },
  // client is one client's attribution row: {client, eligible_flows, eligible_bytes, named_flows,
  // methods, rate, rate_state, mean_delay_seconds, max_delay_seconds}, its counts 0 when it has no
  // eligible flow; null for no such client.
  client: function (host, id) {
    whole(id, "the client id");
    var all = attributionWhole(host);
    var counts = attributionCounts(host);
    var info = exports.installation.client(host, id);
    if (info === null) return null;
    var count = counts[id] || { eligible: 0, named: 0, mean: null, max: null, cache: 0, timing: 0, figures: {} };
    var rate = rateOf(count, all.resolver_covered);
    return { client: info, eligible_flows: host.figures.count(count.eligible), eligible_bytes: host.figures.byteCount(count.figures),
      named_flows: host.figures.count(count.named),
      methods: { resolver_cache_answer: count.cache, lookup_timing: count.timing, not_recorded_beyond_flow_horizon: 0 },
      rate: rate.rate, rate_state: rate.state, mean_delay_seconds: count.mean, max_delay_seconds: count.max };
  }
};

// ---- flows: the window's flows by their columns -- whichever source wrote them -- for the
// compositions by protocol, service port and blocking reason, and their recent part. A filter is
// {scope, interfaces, clients}: a traffic scope ("all", or absent, for every one) and id lists, null
// for no restriction. The widget declares, in a flows.statements.sql:
//
//   -- statement: widget_flow_parts / _measured          the window's flows per direction, protocol,
//                                                        port and decision, with their summable figures
//   -- statement: widget_flow_recent_parts / _measured   the same per bucket of :width seconds, inside
//                                                        interface and client as well
var DECISIONS = { allowed: "allowed", firewall_rule: "firewall_rule", firewall_no_rule: "firewall_no_rule",
  firewall_reason_not_recorded: "firewall_reason_not_recorded", unknown: "unknown" };

function flowFilterOf(filter) {
  if (filter === null || filter === undefined) return { scope: "all", interface_ids: null, client_ids: null };
  if (typeof filter !== "object") throw new TypeError("the flow filter is " + describe(filter) + ", not an object");
  var scope = filter.scope === undefined || filter.scope === null || filter.scope === "" ? "all" : text(filter.scope, "the filter's scope");
  return { scope: scope, interface_ids: filter.interfaces === undefined ? null : filter.interfaces,
    client_ids: filter.clients === undefined ? null : filter.clients };
}

// completeUntil is the instant up to which a provider kind's records are complete: when the last pass
// of its active provider ended healthy, never later than now; null when none did.
function completeUntil(host, kind) {
  var until = null;
  host.envelope.availability().forEach(function (provider) {
    if (provider.kind !== kind || !provider.active || provider.outcome === null || provider.outcome === undefined ||
        provider.checked_at === null || provider.checked_at === undefined) return;
    if (provider.outcome !== "healthy" && provider.outcome !== "healthy_empty") return;
    var at = Math.min(provider.checked_at, host.request.now);
    if (until === null || at > until) until = at;
  });
  return until;
}

exports.flows = {
  decisions: DECISIONS,
  // parts are the window's flows per direction, protocol, destination port and decision, each
  // connection once: [{traffic_direction, protocol, port, decision} and their summable figures].
  parts: function (host, filter) {
    var window = windowOf(host, "flows.parts");
    var bound = flowFilterOf(filter);
    return host.query.rows("widget_flow_parts", { from: window.from, to: window.to, scope: bound.scope,
      interface_ids: bound.interface_ids, client_ids: bound.client_ids });
  },
  // recent is the logged flows of the trailing seconds before now, in buckets: {from, to,
  // bucket_seconds, complete_until, buckets, parts}; a bucket after complete_until, before the
  // earliest flow kept or meeting a collection gap is a gap.
  recent: function (host, seconds, filter) {
    hostOf(host);
    whole(seconds, "the seconds");
    if (seconds < 1) seconds = 1;
    var width = Math.floor((seconds + 59) / 60);
    var now = host.request.now;
    var window = { from: now - seconds, to: now };
    var bound = flowFilterOf(filter);
    var parts = host.query.rows("widget_flow_recent_parts", { from: window.from, to: window.to, scope: bound.scope,
      interface_ids: bound.interface_ids, client_ids: bound.client_ids, width: width });
    var until = completeUntil(host, "firewall_log");
    var earliest = host.envelope.earliest("widget_earliest_flow");
    var covered = null;
    if (earliest !== null && until !== null) {
      var interval = { from: Math.max(window.from, earliest), to: Math.min(window.to, until) };
      if (interval.to > interval.from) covered = interval;
    }
    var gaps = host.envelope.readGaps(["firewall_log"], window);
    var buckets = exports.buckets.intervals(window, width).map(function (bucket) {
      return { from: bucket.from, to: bucket.to, gap: exports.buckets.isGap(bucket, covered, gaps) };
    });
    return { from: window.from, to: window.to, bucket_seconds: width, complete_until: until, buckets: buckets, parts: parts };
  },
  // serviceKey is the key of a service, its port and protocol together: "port:53/udp", or
  // "port:not_recorded" when no port was logged.
  serviceKey: function (protocol, port) {
    if (port === null || port === undefined) return "port:not_recorded";
    var name = protocol === null || protocol === undefined || protocol === "" ? "not_recorded" : text(protocol, "the protocol");
    return "port:" + whole(port, "the port") + "/" + name;
  }
};

// ---- series: the traffic of the window per bucket, for a chart of series. The widget declares, in
// a series.statements.sql:
//
//   -- statement: widget_traffic_buckets / _measured      the flows per bucket start and direction
//   -- statement: widget_traffic_hour_slots / _measured   the interface family's 1 h slots of [:from, :to)
exports.series = {
  // traffic answers {buckets, covered}: buckets maps a bucket start to {direction: summable figures};
  // covered is the interval read, null for none. filter is {interfaces, client_id, rule_id, asn,
  // country, site}, each null or left out for no restriction, and measure: the name of a declared
  // measure to read whatever the request is answered under. Whole-hour buckets the flows no longer
  // hold are read from the 1 h slots where only interfaces restrict the series, and say so by a
  // named state.
  traffic: function (host, filter, width) {
    var window = windowOf(host, "series.traffic");
    positive(width, "the bucket width");
    var scope = filter === null || filter === undefined ? {} : filter;
    var measure = scope.measure === null || scope.measure === undefined || scope.measure === "" ? "" : text(scope.measure, "the filter's measure");
    if (measure !== "" && host.figures.declared.indexOf(measure) < 0) throw new TypeError("no plugin declares the measure \"" + measure + "\"");
    var optional = function (value) { return value === undefined ? null : value; };
    var interfaces = optional(scope.interfaces);
    var buckets = {};
    function add(start, direction, figures) {
      if (buckets[start] === undefined) buckets[start] = {};
      buckets[start][direction] = host.figures.add(buckets[start][direction] === undefined ? {} : buckets[start][direction], figures);
    }
    var parameters = { from: window.from, to: window.to, width: width, interface_ids: interfaces, client_id: optional(scope.client_id),
      rule_id: optional(scope.rule_id), asn: optional(scope.asn), country: optional(scope.country), site: optional(scope.site) };
    var bucketsStatement = "widget_traffic_buckets", slotsStatement = "widget_traffic_hour_slots";
    if (measure !== "") { bucketsStatement += "_measured"; slotsStatement += "_measured"; parameters.measure = measure; }
    host.query.rows(bucketsStatement, parameters).forEach(function (row) { add(row.bucket_start, row.traffic_direction, figuresOf(row)); });
    var earliest = host.envelope.earliest("widget_earliest_flow");
    var coveredFrom = window.to;
    if (earliest !== null) coveredFrom = Math.max(window.from, earliest);
    var unscoped = optional(scope.client_id) === null && optional(scope.rule_id) === null && optional(scope.country) === null &&
      optional(scope.asn) === null && optional(scope.site) === null;
    if (width % 3600 === 0 && unscoped && (earliest === null || earliest > window.from)) {
      var boundary = earliest === null ? window.to : earliest - floorMod(earliest, 3600);
      var firstHour = hourEdges(window).first;
      if (firstHour < boundary) {
        var selected = null;
        if (interfaces !== null && interfaces.length > 0) { selected = {}; interfaces.forEach(function (id) { selected[id] = true; }); }
        var slotsFrom = -1;
        var slotParameters = { from: firstHour, to: boundary };
        if (measure !== "") slotParameters.measure = measure;
        host.query.rows(slotsStatement, slotParameters).forEach(function (row) {
          var local = exports.volume.localInterface({ traffic_direction: row.traffic_direction, src_interface_id: row.src_interface_id,
            dst_interface_id: row.dst_interface_id });
          if (selected !== null && (local === null || selected[local] !== true)) return;
          if (slotsFrom < 0 || row.hour < slotsFrom) slotsFrom = row.hour;
          add(row.hour - floorMod(row.hour, width), row.traffic_direction, figuresOf(row));
        });
        if (slotsFrom >= 0) {
          coveredFrom = Math.min(coveredFrom, slotsFrom);
          host.envelope.state("series_read_from_hour_slots_beyond_flow_horizon");
        }
      }
    }
    Object.keys(buckets).forEach(function (start) { buckets[start] = host.json.map(buckets[start]); });
    var until = Math.min(window.to, host.request.now);
    return { buckets: host.json.map(buckets), covered: coveredFrom >= until ? null : { from: coveredFrom, to: until } };
  }
};

// ---- samples: the firewall's sampled readings -- its own gauges and its interface counters, as the
// insight plugin's measurement_sample holds them. A sample is a value at an instant: a bucket of a
// longer period wants an aggregation, never a count, and a bucket with no sample is a gap, never a 0.
// Which subject kind and which measures serve a health metric is what the providers' plugins declare
// (host.samples.metrics). The widget declares, in a samples.statements.sql:
//
//   -- statement: widget_measurement_subjects   DISTINCT kind, key, measure, unit of [:from, :to] for :subject_kinds
//   -- statement: widget_measurement_series     at, value of one subject over [:from, :to)
//   -- statement: widget_interface_counter      at, value of one interface counter over [:from, :to)
var THROUGHPUT_MEASURES = {
  bits_per_second: { in: "bytes_in", out: "bytes_out", per_second: true, factor: 8 },
  bytes: { in: "bytes_in", out: "bytes_out", per_second: false, factor: 1 },
  packets: { in: "packets_in", out: "packets_out", per_second: false, factor: 1 },
  errors: { in: "errors_in", out: "errors_out", per_second: false, factor: 1 }
};

function aggregatePoints(points, aggregation) {
  points.sort(function (a, b) { return a.at - b.at; });
  var result = points[0].value;
  var sum = 0;
  points.forEach(function (point) {
    sum += point.value;
    if (aggregation === "min") result = Math.min(result, point.value);
    else if (aggregation === "max") result = Math.max(result, point.value);
  });
  if (aggregation === "sum") return sum;
  if (aggregation === "last") return points[points.length - 1].value;
  if (aggregation === "mean") return sum / points.length;
  return result;
}

exports.samples = {
  // subjects are the subjects that reported one of a metric's measures in the window: [{kind, key,
  // measure, unit}], by measure then key.
  subjects: function (host, metric) {
    var window = windowOf(host, "samples.subjects");
    text(metric, "the metric");
    var mapping = host.samples.metrics[metric];
    if (mapping === undefined) throw new TypeError("\"" + metric + "\" is not a health metric");
    var result = [];
    host.query.rows("widget_measurement_subjects", { from: window.from, to: window.to, subject_kinds: [mapping.kind] }).forEach(function (row) {
      if (mapping.measures.indexOf(row.measure) >= 0) result.push({ kind: row.kind, key: row.key, measure: row.measure, unit: row.unit });
    });
    result.sort(function (a, b) { return a.measure !== b.measure ? compareText(a.measure, b.measure) : compareText(a.key, b.key); });
    return result;
  },
  // points are one subject's samples over the window, oldest first: [{at, value}].
  points: function (host, subject) {
    var window = windowOf(host, "samples.points");
    if (subject === null || typeof subject !== "object") throw new TypeError("the subject is " + describe(subject) + ", not a subject");
    return host.query.rows("widget_measurement_series", { subject_kind: subject.kind, subject_key: subject.key, measure: subject.measure,
      from: window.from, to: window.to }).map(function (row) { return { at: row.at, value: row.value }; });
  },
  // throughput is one interface counter as points: {points, resets}. A point is the per-second rate
  // (times 8 for bits) or the increase between two samples, stamped at the later one; a counter that
  // went down is a reset, counted and never a point.
  throughput: function (host, device, measure, direction) {
    var window = windowOf(host, "samples.throughput");
    text(device, "the device");
    text(measure, "the throughput measure");
    text(direction, "the direction");
    var mapping = THROUGHPUT_MEASURES[measure];
    if (mapping === undefined) throw new TypeError("\"" + measure + "\" is not a throughput measure");
    if (direction !== "in" && direction !== "out") throw new TypeError("the direction is \"" + direction + "\", not in or out");
    var counter = direction === "in" ? mapping.in : mapping.out;
    var points = [], resets = 0, previous = 0, previousAt = 0, have = false;
    host.query.rows("widget_interface_counter", { device: device, measure: counter, from: window.from, to: window.to }).forEach(function (row) {
      if (have && row.at > previousAt) {
        if (row.value < previous) resets++;
        else {
          var perSecond = (row.value - previous) / (row.at - previousAt);
          points.push({ at: row.at, value: mapping.per_second ? perSecond * mapping.factor : perSecond * (row.at - previousAt) * mapping.factor });
        }
      }
      previous = row.value; previousAt = row.at; have = true;
    });
    return { points: points, resets: resets };
  },
  // aggregate cuts points into the window's buckets, each combined by mean, min, max, sum or last:
  // [{from, to, gap, value}], a bucket no point falls in being a gap with no value.
  aggregate: function (window, width, points, aggregation) {
    var held = interval(window, "the window");
    positive(width, "the bucket width");
    text(aggregation, "the aggregation");
    var grouped = {};
    (points || []).forEach(function (point) {
      var start = point.at - floorMod(point.at, width);
      if (grouped[start] === undefined) grouped[start] = [];
      grouped[start].push({ at: point.at, value: point.value });
    });
    var buckets = exports.buckets.intervals(held, width).map(function (slot) {
      var start = slot.from - floorMod(slot.from, width);
      var bucket = { from: slot.from, to: slot.to, gap: true, value: null };
      if (grouped[start] !== undefined && grouped[start].length > 0) { bucket.gap = false; bucket.value = aggregatePoints(grouped[start], aggregation); }
      return bucket;
    });
    return buckets.length === 0 ? null : buckets;
  }
};

// ---- rates: the current rates, from the latest samples of the insight plugin: a second measure,
// sampled, never summed with the logged one. A subject with no current sample reads its named absence,
// no_current_sample, and one older than twice its cadence stale_sample; never a rate of 0. Reading a
// current rate renews the live-interest lease (host.live.renew). The widget declares, in a
// rates.statements.sql:
//
//   -- statement: widget_rate_rows                  the readings of both sampled subjects in [:since, :now]
//   -- statement: widget_rate_inside                whether an address is inside, by the clients plugin's membership
//   -- statement: widget_rate_client_at_address     the strongest identity at an address
//   -- statement: widget_rate_address_level_client  the most recent address-level client at an address
//
// and widget_installation_interfaces (the installation area) for a rate per interface.
var SAMPLE_SECONDS = 2;
var RATES = { current: null, inside: {}, clients: {} };

function measurementInterval(host) {
  var value = host.settings.get("poll_interval_measurement_seconds");
  if (value === null) return 300;
  var parsed = parseInt(String(value).trim(), 10);
  if (isNaN(parsed) || parsed <= 0 || String(parsed) !== String(value).trim()) {
    throw new TypeError("the setting poll_interval_measurement_seconds is \"" + value + "\", not a positive whole number of seconds");
  }
  return parsed;
}

function newRate() { return { in: 0, out: 0, outMeasured: false, oldestSampledAt: 0, sampledAt: 0, cadence: 0, outSeen: 0, outMissing: 0 }; }

function stampRate(rate, sampledAt, cadence) {
  if (rate.sampledAt === 0 || sampledAt > rate.sampledAt) rate.sampledAt = sampledAt;
  if (rate.oldestSampledAt === 0 || sampledAt < rate.oldestSampledAt) rate.oldestSampledAt = sampledAt;
  if (cadence > rate.cadence) rate.cadence = cadence;
}

function addIn(rate, value, sampledAt, cadence) { stampRate(rate, sampledAt, cadence); rate.in += value; }

function addOut(rate, value, sampledAt, cadence) {
  stampRate(rate, sampledAt, cadence);
  if (value === null) rate.outMissing++;
  else { rate.out += value; rate.outSeen++; }
  rate.outMeasured = rate.outSeen > 0 && rate.outMissing === 0;
}

function addRate(rates, key, value, out, sampledAt, cadence) {
  if (rates[key] === undefined) rates[key] = newRate();
  addIn(rates[key], value, sampledAt, cadence);
  addOut(rates[key], out, sampledAt, cadence);
}

// addPairRate adds one per-pair reading under a peer: its inbound figure, and its outbound one or
// the record that it carried none. A pair with neither figure adds nothing.
function addPairRate(rates, peer, pair) {
  if (pair.in === null && pair.out === null) return;
  addRate(rates, peer, pair.in === null ? 0 : pair.in, pair.out, pair.sampledAt, pair.cadence);
}

// latestReadings keeps each subject's latest reading among the rows: the totals of each local address
// on a device, and the per-pair figures.
function latestReadings(rows) {
  var endpoints = {}, pairs = {};
  rows.forEach(function (row) {
    if (row.subject_kind === "interface_endpoint") {
      var key = row.interface_device + "\u001f" + row.local_address;
      var reading = endpoints[key];
      if (reading === undefined || row.sampled_at > reading.sampledAt) {
        reading = { device: row.interface_device, local: row.local_address, in: null, out: null, sampledAt: row.sampled_at, cadence: row.cadence };
        endpoints[key] = reading;
      } else if (row.sampled_at < reading.sampledAt) return;
      if (row.measure === "rate_bits_in") reading.in = row.value;
      else if (row.measure === "rate_bits_out") reading.out = row.value;
    } else if (row.subject_kind === "interface_endpoint_pair") {
      var pairKey = row.interface_device + "\u001f" + row.local_address + "\u001f" + row.peer;
      var pair = pairs[pairKey];
      if (pair === undefined || row.sampled_at > pair.sampledAt) {
        pair = { device: row.interface_device, local: row.local_address, peer: row.peer, in: null, out: null, sampledAt: row.sampled_at, cadence: row.cadence };
        pairs[pairKey] = pair;
      } else if (row.sampled_at < pair.sampledAt) return;
      if (row.measure === "rate_bits_in") pair.in = row.value;
      else if (row.measure === "rate_bits_out") pair.out = row.value;
    }
  });
  return { endpoints: endpoints, pairs: pairs };
}

function isInside(host, address) {
  if (!Object.prototype.hasOwnProperty.call(RATES.inside, address)) {
    var row = host.query.row("widget_rate_inside", { address: address });
    RATES.inside[address] = row !== null && row.inside === true;
  }
  return RATES.inside[address];
}

function clientAt(host, address) {
  if (!Object.prototype.hasOwnProperty.call(RATES.clients, address)) {
    var row = host.query.row("widget_rate_client_at_address", { address: address });
    if (row === null) row = host.query.row("widget_rate_address_level_client", { address: address });
    RATES.clients[address] = row === null ? null : row.id;
  }
  return RATES.clients[address];
}

// split turns one set of readings into rates per device and per direction, and, with clients, per
// client and per peer. The totals of each local address are what a device and a client sum; a
// client and a direction count only the local addresses that are inside. Per direction, the inbound
// figures are per peer and placed by the peer; the outbound total of a local address goes to outbound
// when every peer it was read with is outside, to inter_interface when every one is inside, and to
// outbound_unsplit otherwise.
function splitReadings(host, readings, withClients) {
  var result = { byClient: {}, byDevice: {}, byDirection: {}, byPeer: {}, byClientPeer: {}, outboundUnsplit: newRate() };
  var peersOf = {};
  Object.keys(readings.pairs).forEach(function (key) {
    var pair = readings.pairs[key];
    var endpointKey = pair.device + "\u001f" + pair.local;
    var endpoint = readings.endpoints[endpointKey];
    if (endpoint !== undefined && endpoint.sampledAt === pair.sampledAt) {
      if (peersOf[endpointKey] === undefined) peersOf[endpointKey] = [];
      peersOf[endpointKey].push(pair);
    }
  });
  var keys = Object.keys(readings.endpoints);
  keys.sort(function (a, b) {
    var ea = readings.endpoints[a], eb = readings.endpoints[b];
    return ea.device !== eb.device ? compareText(ea.device, eb.device) : compareText(ea.local, eb.local);
  });
  keys.forEach(function (key) {
    var endpoint = readings.endpoints[key];
    var inbound = endpoint.in === null ? 0 : endpoint.in;
    addRate(result.byDevice, endpoint.device, inbound, endpoint.out, endpoint.sampledAt, endpoint.cadence);
    if (!isInside(host, endpoint.local)) return;
    var peers = (peersOf[key] || []).slice();
    peers.sort(function (a, b) { return compareText(a.peer, b.peer); });
    var outside = 0, between = 0;
    peers.forEach(function (pair) {
      var direction = "inbound";
      if (isInside(host, pair.peer)) { direction = "inter_interface"; between++; } else outside++;
      if (pair.in !== null) {
        if (result.byDirection[direction] === undefined) result.byDirection[direction] = newRate();
        addIn(result.byDirection[direction], pair.in, pair.sampledAt, pair.cadence);
      }
    });
    var outDirection = "";
    if (outside > 0 && between === 0) outDirection = "outbound";
    else if (between > 0 && outside === 0) outDirection = "inter_interface";
    if (outDirection === "") addOut(result.outboundUnsplit, endpoint.out, endpoint.sampledAt, endpoint.cadence);
    else {
      if (result.byDirection[outDirection] === undefined) result.byDirection[outDirection] = newRate();
      addOut(result.byDirection[outDirection], endpoint.out, endpoint.sampledAt, endpoint.cadence);
    }
    if (!withClients) return;
    peers.forEach(function (pair) { addPairRate(result.byPeer, pair.peer, pair); });
    var clientId = clientAt(host, endpoint.local);
    if (clientId === null) return;
    addRate(result.byClient, clientId, inbound, endpoint.out, endpoint.sampledAt, endpoint.cadence);
    if (peers.length > 0 && result.byClientPeer[clientId] === undefined) result.byClientPeer[clientId] = {};
    peers.forEach(function (pair) { addPairRate(result.byClientPeer[clientId], pair.peer, pair); });
  });
  return result;
}

function rateRows(host, since, until) {
  var interval = measurementInterval(host);
  return { interval: interval, rows: host.query.rows("widget_rate_rows", { since: since, now: until }).map(function (row) {
    return { subject_kind: row.subject_kind, interface_device: row.interface_device, local_address: row.local_address, peer: row.peer,
      measure: row.measure, value: row.value, sampled_at: row.sampled_at,
      cadence: row.live_cadence_seconds === null || row.live_cadence_seconds === undefined ? interval : row.live_cadence_seconds };
  }) };
}

// currentRates reads the rates sampled within twice the measurement interval of now and keeps each
// subject's latest reading, fine or baseline, renewing the live-interest lease.
function currentRates(host) {
  host.live.renew();
  if (RATES.current === null) {
    var now = host.request.now;
    var read = rateRows(host, now - 2 * measurementInterval(host), now);
    RATES.current = splitReadings(host, latestReadings(read.rows), true);
  }
  return RATES.current;
}

// sampledRate renders one current rate at an instant, or its named absence.
function sampledRate(rate, present, now) {
  if (!present) {
    return { state: "no_current_sample", measure: "sampled", in_bits_per_second: null, out_bits_per_second: null, out_measured: false,
      sampled_at: null, span_seconds: null, cadence_seconds: null, lower_bound: true };
  }
  var stale = rate.cadence > 0 && now - rate.oldestSampledAt > 2 * rate.cadence;
  return { state: stale ? "stale_sample" : "current", measure: "sampled", in_bits_per_second: rate.in,
    out_bits_per_second: rate.outMeasured ? rate.out : null, out_measured: rate.outMeasured, sampled_at: rate.sampledAt,
    span_seconds: SAMPLE_SECONDS, cadence_seconds: rate.cadence > 0 ? rate.cadence : null, lower_bound: true };
}

function peerRates(host, rates) {
  var result = {};
  Object.keys(rates).forEach(function (peer) { result[peer] = sampledRate(rates[peer], true, host.request.now); });
  return host.json.map(result);
}

exports.rates = {
  // current is the rates a "now" figure shows: {by_direction, outbound_unsplit, by_member}, by_direction
  // one {key, rate} per traffic direction, by_member one per client ("client") or interface
  // ("interface") holding a current sample, sorted by key, and empty for any other grouping.
  current: function (host, groupBy) {
    text(groupBy, "the grouping");
    var rates = currentRates(host);
    var now = host.request.now;
    var result = { by_direction: [], outbound_unsplit: sampledRate(rates.outboundUnsplit, rates.outboundUnsplit.sampledAt !== 0, now), by_member: [] };
    ["outbound", "inbound", "inter_interface"].forEach(function (direction) {
      var present = rates.byDirection[direction] !== undefined;
      result.by_direction.push({ key: direction, rate: sampledRate(present ? rates.byDirection[direction] : newRate(), present, now) });
    });
    if (groupBy === "client") {
      Object.keys(rates.byClient).forEach(function (id) { result.by_member.push({ key: "client:" + id, rate: sampledRate(rates.byClient[id], true, now) }); });
    } else if (groupBy === "interface") {
      var byDevice = {};
      exports.installation.interfaces(host).forEach(function (info) { byDevice[info.device] = info; });
      Object.keys(rates.byDevice).forEach(function (device) {
        var key = byDevice[device] !== undefined ? "interface:" + byDevice[device].id : "device:" + device;
        result.by_member.push({ key: key, rate: sampledRate(rates.byDevice[device], true, now) });
      });
    }
    result.by_member.sort(function (a, b) { return compareText(a.key, b.key); });
    return result;
  },
  // byClient is every client holding a current sample: a map from its id, as text, to its rate.
  byClient: function (host) {
    var rates = currentRates(host);
    var result = {};
    Object.keys(rates.byClient).forEach(function (id) { result[id] = sampledRate(rates.byClient[id], true, host.request.now); });
    return host.json.map(result);
  },
  // client is one client's current rate, or its named absence.
  client: function (host, id) {
    whole(id, "the client id");
    var rates = currentRates(host);
    var present = rates.byClient[id] !== undefined;
    return sampledRate(present ? rates.byClient[id] : newRate(), present, host.request.now);
  },
  // byPeer is every peer address an inside local address was read with in a current sample: a map
  // from the address to its rate.
  byPeer: function (host) { return peerRates(host, currentRates(host).byPeer); },
  // clientPeers is the same for one client: a map from each peer address it was read with to its
  // rate, empty when it has none.
  clientPeers: function (host, id) {
    whole(id, "the client id");
    var rates = currentRates(host);
    return peerRates(host, rates.byClientPeer[id] === undefined ? {} : rates.byClientPeer[id]);
  },
  // series is every sampling instant of [from, to), fine and baseline, oldest first, each split per
  // direction as the current rates are: {span_seconds, instants: [{at, by_direction, outbound_unsplit}]}.
  // It renews no lease: it reads history.
  series: function (host, window) {
    var held = interval(window, "the window");
    if (held.to <= held.from) return { span_seconds: SAMPLE_SECONDS, instants: [] };
    var read = rateRows(host, held.from, held.to - 1);
    var byInstant = {};
    read.rows.forEach(function (row) { if (byInstant[row.sampled_at] === undefined) byInstant[row.sampled_at] = []; byInstant[row.sampled_at].push(row); });
    var instants = Object.keys(byInstant).map(Number);
    instants.sort(function (a, b) { return a - b; });
    var figures = function (rate) { return { in_bits_per_second: rate.in, out_bits_per_second: rate.out }; };
    return { span_seconds: SAMPLE_SECONDS, instants: instants.map(function (at) {
      var rates = splitReadings(host, latestReadings(byInstant[at]), false);
      var byDirection = {};
      Object.keys(rates.byDirection).forEach(function (direction) { byDirection[direction] = figures(rates.byDirection[direction]); });
      return { at: at, by_direction: host.json.map(byDirection), outbound_unsplit: figures(rates.outboundUnsplit) };
    }) };
  },
  // none is the named absence of a rate: what a subject with no current sample reads.
  none: function (host) { return sampledRate(newRate(), false, hostOf(host).request.now); }
};

// ---- interfaces: what the store counts per interface beyond its volume -- the clients a window
// showed on each interface out of those it is known to have, and the rules that do not log, per
// interface. The widget declares, in an interfaces.statements.sql:
//
//   -- statement: widget_interface_seen_clients   interface_id, seen over [:from, :to)
//   -- statement: widget_interface_known_clients  known of :interface_id seen at or after :from
//   -- statement: widget_rules_logging            id, interface, legacy, logs_matches of every rule
//
// and widget_installation_interfaces (the installation area).
exports.interfaces = {
  // clients are, per interface, [{interface_id, seen, known}]: seen is the distinct inside clients of
  // the flows of [from, to) on it, known the client rows of the interface seen at or after from.
  clients: function (host, from, to) {
    whole(from, "from");
    whole(to, "to");
    var seen = {};
    host.query.rows("widget_interface_seen_clients", { from: from, to: to }).forEach(function (row) {
      if (row.interface_id !== null && row.interface_id !== undefined) seen[row.interface_id] = row.seen;
    });
    return exports.installation.interfaces(host).map(function (info) {
      var known = host.query.row("widget_interface_known_clients", { interface_id: info.id, from: from });
      return { interface_id: info.id, seen: seen[info.id] === undefined ? 0 : seen[info.id], known: known === null ? 0 : known.known };
    });
  },
  // ruleLogging is {not_logging_by_interface, floating_not_logging, unresolved_not_logging,
  // not_reported}: the first maps an interface id, as a string, to the rules naming it whose logging
  // is off; the others count the rules naming no interface, the rules whose interfaces did not
  // resolve, and the rules whose logging flag was not reported at all.
  ruleLogging: function (host) {
    var byKey = {};
    exports.installation.interfaces(host).forEach(function (info) { byKey[info.identifier] = info.id; });
    var result = { not_logging_by_interface: {}, floating_not_logging: 0, unresolved_not_logging: 0, not_reported: 0 };
    host.query.rows("widget_rules_logging", {}).forEach(function (rule) {
      if (rule.logs_matches === null || rule.logs_matches === undefined) { result.not_reported++; return; }
      if (rule.logs_matches === true) return;
      var list = rule.interface === null || rule.interface === undefined ? null : rule.interface;
      var keys = list === null ? [] : list.split(",").map(function (part) { return part.trim(); }).filter(function (part) { return part !== ""; });
      if (list === null) { result.unresolved_not_logging++; return; }
      if (keys.length === 0) { result.floating_not_logging++; return; }
      if (rule.legacy === true) { result.unresolved_not_logging++; return; }
      var resolved = {}, unresolved = false;
      keys.forEach(function (key) { if (byKey[key] === undefined) unresolved = true; else resolved[byKey[key]] = true; });
      Object.keys(resolved).forEach(function (id) { result.not_logging_by_interface[id] = (result.not_logging_by_interface[id] || 0) + 1; });
      if (unresolved && Object.keys(resolved).length === 0) result.unresolved_not_logging++;
    });
    result.not_logging_by_interface = host.json.map(result.not_logging_by_interface);
    return result;
  }
};

// ---- addresses: the addresses this installation's upstream interfaces -- those carrying a gateway,
// never one called something -- have held, as opnview saw them. The address an interface holds is
// what the firewall sees, and only that: behind an upstream NAT the public address belongs to a
// device opnview does not read. The widget declares, in an addresses.statements.sql:
//
//   -- statement: widget_interface_addresses   the addresses and gateways of :interface_id, newest first
//
// and widget_installation_interfaces (the installation area).
exports.addresses = {
  sourceFields: { addr4: "addr4", addr6: "addr6", ipv4: "ipv4", ipv6: "ipv6", gateways: "gateways" },
  // upstreams are every upstream interface: [{interface_id, history}], history being every address and
  // gateway it held, newest first: [{source_field, address, prefix_length, address_family,
  // first_seen_at, last_seen_at}].
  upstreams: function (host) {
    var result = [];
    exports.installation.interfaces(host).forEach(function (info) {
      if (!info.upstream) return;
      var history = host.query.rows("widget_interface_addresses", { interface_id: info.id }).map(function (row) {
        return { source_field: row.source_field, address: row.address, prefix_length: orNull(row.prefix_length), address_family: row.address_family,
          first_seen_at: row.first_seen_at, last_seen_at: row.last_seen_at };
      });
      result.push({ interface_id: info.id, history: history });
    });
    return result;
  }
};

// ---- services: the stored geolocation of an address, as the maxmind plugin holds it. A widget never
// downloads, looks up or refreshes anything: it reads what the core holds. The widget declares, in a
// geolocation.statements.sql:
//
//   -- statement: widget_geolocation   the rows of plugin_maxmind.geo_asn for :addresses, a JSON list
//
// The outside peers the volume area handed are read together at the first address asked for, in one
// statement rather than one statement a peer; geolocations(host, addresses) reads a list of its own.
var GEOLOCATION = { read: {}, placed: {} };
var GEOLOCATION_BATCH = 20000;

function readGeolocations(host, addresses) {
  addresses.sort(compareText);
  for (var start = 0; start < addresses.length; start += GEOLOCATION_BATCH) {
    var batch = addresses.slice(start, start + GEOLOCATION_BATCH);
    host.query.rows("widget_geolocation", { addresses: batch }).forEach(function (row) {
      GEOLOCATION.read[row.address] = { lookup_state: row.lookup_state, country_code: orNull(row.country_code), country_name: orNull(row.country_name),
        latitude: orNull(row.latitude), longitude: orNull(row.longitude), asn: orNull(row.asn), operator: orNull(row.operator),
        dataset_build_at: orNull(row.dataset_build_at) };
    });
    batch.forEach(function (address) {
      if (GEOLOCATION.read[address] === undefined) {
        GEOLOCATION.read[address] = { lookup_state: "no_row", country_code: null, country_name: null, latitude: null, longitude: null,
          asn: null, operator: null, dataset_build_at: null };
      }
    });
  }
}

exports.services = {
  // place records outside peers the request is placing, so their geolocations are read together.
  place: function (addresses) {
    (addresses || []).forEach(function (address) { if (typeof address === "string" && address !== "") GEOLOCATION.placed[address] = true; });
  },
  // geolocations reads a list of addresses at once; each is then answered from memory.
  geolocations: function (host, addresses) {
    hostOf(host);
    var pending = [];
    (addresses || []).forEach(function (address) { if (typeof address === "string" && GEOLOCATION.read[address] === undefined && pending.indexOf(address) < 0) pending.push(address); });
    if (pending.length > 0) readGeolocations(host, pending);
  },
  // geolocation is the stored geolocation of one address: {lookup_state, country_code, country_name,
  // latitude, longitude, asn, operator, dataset_build_at}; lookup_state is resolved, miss, pending, or
  // no_row for an address never looked up.
  geolocation: function (host, address) {
    hostOf(host);
    text(address, "the address");
    if (GEOLOCATION.read[address] === undefined) {
      var pending = [address];
      Object.keys(GEOLOCATION.placed).forEach(function (placed) { if (GEOLOCATION.read[placed] === undefined && placed !== address) pending.push(placed); });
      GEOLOCATION.placed = {};
      readGeolocations(host, pending);
    }
    return GEOLOCATION.read[address];
  }
};
