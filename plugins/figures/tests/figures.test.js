// The figures library's own tests: each area answers what the widget host's Go area answered.
// 27 September 2026 00:00 UTC, a Sunday, is the harness's instant.
var SUNDAY = 1790467200;

test("buckets: the width of each period, the intervals clipped to the window, the gap rule", function (t) {
  var buckets = t.lib.figures.buckets;
  t.equal(buckets.width("1h"), 60, "a minute for the hour");
  t.equal(buckets.width("7d"), 3600, "an hour for the week");
  t.equal(buckets.width("anything"), 900, "a quarter-hour, the day's, for a period that is not one");
  var intervals = buckets.intervals({ from: 1000, to: 2700 }, 900);
  t.equal(JSON.stringify(intervals), JSON.stringify([{ from: 1000, to: 1800 }, { from: 1800, to: 2700 }]), "clipped to the window");
  t.equal(buckets.floorMod(-1, 900), 899, "a non-negative remainder");
  t.equal(buckets.isGap({ from: 1800, to: 2700 }, null, []), true, "no covered interval is a gap");
  t.equal(buckets.isGap({ from: 1800, to: 2700 }, { from: 1000, to: 2700 }, []), false, "inside the covered interval");
  t.equal(buckets.isGap({ from: 1800, to: 2700 }, { from: 1000, to: 2700 }, [{ from: 2600, to: 2650 }]), true, "a gap met");
  var thrown = false;
  try { buckets.intervals({ from: 1, to: 2 }, 0); } catch (e) { thrown = e instanceof TypeError; }
  t.assert(thrown, "a width that is not positive is a TypeError");
});

test("periods: the slot of each period holding an instant, as the core cuts them", function (t) {
  var periods = t.lib.figures.periods;
  t.equal(JSON.stringify(periods.names()), JSON.stringify(["1h", "24h", "7d", "30d"]), "finest first");
  var instant = SUNDAY + 5 * 3600 + 17 * 60;
  t.equal(periods.slotStart("1h", instant), SUNDAY + 5 * 3600, "the hour");
  t.equal(periods.slotEnd("1h", instant), SUNDAY + 6 * 3600, "the hour's end");
  t.equal(periods.slotStart("24h", instant), SUNDAY, "the UTC day");
  t.equal(periods.slotStart("7d", instant), SUNDAY - 6 * 86400, "the ISO week starts on Monday 21 September");
  t.equal(periods.slotEnd("7d", instant), SUNDAY + 86400, "and ends on Monday 28 September");
  t.equal(periods.slotStart("30d", instant), Date.UTC(2026, 8, 1) / 1000, "the calendar month");
  t.equal(periods.slotEnd("30d", instant), Date.UTC(2026, 9, 1) / 1000, "to the next month's first day");
  var thrown = false;
  try { periods.slotStart("2h", instant); } catch (e) { thrown = e instanceof TypeError; }
  t.assert(thrown, "a period that is not one of the four is a TypeError");
});

test("mapfilters: the allowed and the blocked half of summable figures, with a measure's own part", function (t) {
  var filters = t.lib.figures.mapfilters;
  var figures = { bytes: 30, allowed_bytes: 10, blocked_bytes: 20, unknown_bytes: 0, allowed_connections: 1,
    blocked_connections: 2, unknown_connections: 0, extra: "a row's other column" };
  t.equal(JSON.stringify(filters.allowedOnly(figures)), JSON.stringify({ bytes: 10, allowed_bytes: 10, blocked_bytes: 0,
    unknown_bytes: 0, allowed_connections: 1, blocked_connections: 0, unknown_connections: 0 }), "the allowed half");
  t.equal(JSON.stringify(filters.blockedOnly(figures)), JSON.stringify({ bytes: 20, allowed_bytes: 0, blocked_bytes: 20,
    unknown_bytes: 0, allowed_connections: 0, blocked_connections: 2, unknown_connections: 0 }), "the blocked half");
  var measured = { bytes: 30, allowed_bytes: 10, blocked_bytes: 20, allowed_connections: 1, blocked_connections: 2,
    measure: "netflow", measure_parts: [10, 5, 1, 0, 0, 0, 20, 7, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0] };
  var blocked = filters.blockedOnly(measured);
  t.equal(blocked.measure, "netflow", "the measure is kept");
  t.equal(JSON.stringify(blocked.measure_parts), JSON.stringify([0, 0, 0, 0, 0, 0, 20, 7, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0]),
    "the blocked third of the parts alone");
});

test("matrixplacement: the keys of the ends that are not an interface", function (t) {
  t.equal(JSON.stringify(t.lib.figures.matrixplacement.keys),
    JSON.stringify({ outside: "outside", north_south: "north_south", this_firewall: "this_firewall" }), "the three keys");
});

test("specialpurpose: the registries' most specific block decides, and an address in none is public", function (t) {
  var classify = t.lib.figures.specialpurpose.classify;
  t.equal(JSON.stringify(classify("8.8.8.8")), JSON.stringify({ special_purpose: null, public: true }), "in no block");
  var privateUse = classify("10.1.2.3");
  t.equal(privateUse.special_purpose.name, "Private-Use", "RFC 1918");
  t.equal(privateUse.special_purpose.block, "10.0.0.0/8", "its block");
  t.equal(privateUse.public, false, "not public");
  t.equal(classify("192.0.0.9").public, true, "the /32 inside an unreachable /24 is globally reachable");
  t.equal(classify("192.0.0.9").special_purpose.block, "192.0.0.9/32", "the most specific block");
  t.equal(classify("192.88.99.1").special_purpose.globally_reachable, null, "no value in the registry");
  t.equal(classify("192.88.99.1").public, false, "and not presented as public");
  t.equal(classify("::ffff:10.0.0.1").special_purpose.block, "10.0.0.0/8", "an IPv4-mapped address is its IPv4 address");
  t.equal(classify("2001:db8::1").special_purpose.name, "Documentation", "IPv6 documentation");
  t.equal(classify("64:ff9b::192.0.2.1").public, true, "the translation prefix is reachable");
  t.equal(classify("fe80::1%igb0").special_purpose.name, "Link-Local Unicast", "a zone is set aside");
  t.equal(classify("2a00:1450:4007:80e::200e").public, true, "a public IPv6 address");
  t.equal(JSON.stringify(classify("not an address")), JSON.stringify({ special_purpose: null, public: false }), "not an address");
  t.equal(JSON.stringify(t.lib.figures.specialpurpose.registries),
    JSON.stringify(["iana-ipv4-special-registry", "iana-ipv6-special-registry"]), "the registries cited");
});
