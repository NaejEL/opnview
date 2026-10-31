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
