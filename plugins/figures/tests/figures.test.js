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

// ---- figures and measure (ADR 0021, step 5): the figures' shapes, their sum and their measure, and the
// choice of the measure a volume answer is given in, against a host written here: a widget's host as the
// library sees it, with one declared measure, a provider and the window intervals each test arranges.

// REPORTED is a declared measure as a plugin's manifest declares one (docs/plugins.md, "Measures"):
// two direction states, one marker, nine figures per part.
var REPORTED = { name: "reported", label: "plugin.example.measure.reported", description: "", provider_kind: "report",
  states: [{ name: "reported", covered: true }, { name: "unreported", covered: false }], complete_state: "reported",
  refused_state: "unreported", markers: ["estimated"],
  classes: { complete: "complete", partial: "partial", unmeasured: "unmeasured", not_known: "not_known" },
  window_states: { covered: "reporting", unrecorded: "reporting_unrecorded", no_provider: "reporter_off",
    not_covering: "reporting_partial", not_served: "reporting_not_served" },
  best_available: true };

// fakeHost is a widget's host as the library reads it. options: requested (volume_measure), providers (ids),
// intervals ([{starts_at, ends_at, state}]), declares (the statement names the widget declares), rows
// (what a statement answers, by name).
function fakeHost(options) {
  var host = { calls: [], states: [], details: {}, overrides: [], snapshots: [] };
  var rows = options.rows || {};
  host.query = {
    rows: function (name, parameters) {
      host.calls.push({ name: name, parameters: parameters });
      if (name === "widget_measure_providers") return (options.providers || []).map(function (id) { return { id: id }; });
      if (name === "widget_measure_intervals") return (options.intervals || []).slice();
      return rows[name] === undefined ? [] : rows[name];
    },
    row: function (name, parameters) { var list = host.query.rows(name, parameters); return list.length === 0 ? null : list[0]; },
    carriesDomain: function () { return false; },
    declares: function (name) { return (options.declares || []).indexOf(name) >= 0; }
  };
  host.request = { type: "example", now: SUNDAY, window: options.window === undefined ? { from: SUNDAY - 3600, to: SUNDAY } : options.window,
    aggregate_mode: "full", no_domains: false, measures: [REPORTED] };
  host.params = { text: function (key) { return key === "volume_measure" ? options.requested : ""; } };
  host.envelope = {
    state: function (code) { if (host.states.indexOf(code) < 0) host.states.push(code); },
    stateDetail: function (code, detail) { host.details[code] = detail; },
    override: function (condition) { host.overrides.push(condition); },
    snapshot: function () { host.snapshots.push({ states: host.states.slice(), details: JSON.parse(JSON.stringify(host.details)) }); return host.snapshots.length - 1; },
    restore: function (token) { var held = host.snapshots[token]; host.states = held.states.slice(); host.details = JSON.parse(JSON.stringify(held.details)); }
  };
  return host;
}

// parts is the measure_parts of one allowed connection: bytes, packets, the four classes, the two states
// and the marker, then the blocked and the unknown parts holding nothing.
function parts(bytes, packets, complete, partial, unmeasured, notKnown, reported, unreported, estimated) {
  return [bytes, packets, complete, partial, unmeasured, notKnown, reported, unreported, estimated,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
}

function reportedRow(bytes) {
  return { bytes: bytes, allowed_bytes: bytes, blocked_bytes: 0, unknown_bytes: 0, allowed_connections: 1, blocked_connections: 0,
    unknown_connections: 0, measure: "reported", measure_parts: parts(bytes, 10, 1, 0, 0, 0, 2, 0, 1) };
}

test("figures: a logged Volume, a Count and a remainder carry the logged measure and the lower bound", function (t) {
  var figures = t.lib.figures.figures;
  var volume = figures.volume({ bytes: 30, allowed_bytes: 10, blocked_bytes: 20, allowed_connections: 1, blocked_connections: 2 });
  t.equal(JSON.stringify(volume), JSON.stringify({ measure: "logged", lower_bound: true, bytes: 30, allowed_bytes: 10, blocked_bytes: 20,
    unknown_bytes: 0, connections: 3, allowed_connections: 1, blocked_connections: 2, unknown_connections: 0 }), "the Volume");
  t.equal(JSON.stringify(figures.count(4)), JSON.stringify({ measure: "logged", lower_bound: true, value: 4 }), "a count");
  t.equal(JSON.stringify(figures.loggedCount(4)), JSON.stringify({ measure: "logged", lower_bound: true, value: 4 }), "a logged count");
  t.equal(JSON.stringify(figures.byteCount({ bytes: 7 })), JSON.stringify({ measure: "logged", lower_bound: true, value: 7 }), "a byte count");
  t.equal(JSON.stringify(figures.remainder(2, { bytes: 5, allowed_bytes: 5, allowed_connections: 1 }, 3)),
    JSON.stringify({ members: 2, volume: { measure: "logged", lower_bound: true, bytes: 5, allowed_bytes: 5, blocked_bytes: 0, unknown_bytes: 0,
      connections: 1, allowed_connections: 1, blocked_connections: 0, unknown_connections: 0 }, count: { measure: "logged", lower_bound: true, value: 3 } }),
    "a remainder with its volume and a count read as a logged count");
  t.equal(JSON.stringify(figures.remainder(1, null, { measure: "logged", lower_bound: true, value: 9 })),
    JSON.stringify({ members: 1, count: { measure: "logged", lower_bound: true, value: 9 } }), "a remainder with a Count handed back");
  t.equal(JSON.stringify(figures.zero()), JSON.stringify({ bytes: 0, allowed_bytes: 0, blocked_bytes: 0, unknown_bytes: 0, allowed_connections: 0,
    blocked_connections: 0, unknown_connections: 0 }), "zero under the logged measure carries no measure");
  t.equal(JSON.stringify(figures.add({ bytes: 1, allowed_bytes: 1, allowed_connections: 1 }, { bytes: 2, blocked_bytes: 2, blocked_connections: 1 })),
    JSON.stringify({ bytes: 3, allowed_bytes: 1, blocked_bytes: 2, unknown_bytes: 0, allowed_connections: 1, blocked_connections: 1, unknown_connections: 0 }), "a sum");
  t.equal(figures.sum([{ bytes: 1 }, { bytes: 2 }, {}]).bytes, 3, "the sum of a list");
  t.equal(figures.value("connections", { allowed_connections: 2, blocked_connections: 1 }), 3, "the connections figure");
  t.equal(figures.value("bytes", { bytes: 12 }), 12, "the bytes figure");
  t.equal(figures.value("packets", { bytes: 12 }), 0, "logged figures carry no packets");
  t.equal(figures.measure, "logged", "the measure in force");
  t.equal(JSON.stringify(figures.measures), JSON.stringify({ logged: "logged", sampled: "sampled", interface_counter: "interface_counter" }),
    "the measures before any host is handed");
  t.equal(figures.interfaceKey(3) + " " + figures.clientKey(12), "interface:3 client:12", "the member keys");
  var thrown = false;
  try { figures.volume(null); } catch (e) { thrown = e instanceof TypeError; }
  t.assert(thrown, "no figures is a TypeError");
});

test("figures: compare ranks strings in byte order, and nameFreeCompare by the FNV-64a hash of the name", function (t) {
  var figures = t.lib.figures.figures;
  t.equal(figures.compare("a", "b"), -1, "a before b");
  t.equal(figures.compare("b", "a"), 1, "b after a");
  t.equal(figures.compare("a", "a"), 0, "equal");
  t.equal(figures.compare("ab", "a"), 1, "a prefix comes first");
  t.equal(figures.compare("￿", "😀"), -1, "U+FFFF before U+1F600 in byte order, where JavaScript's < ranks them the other way");
  t.equal("￿" < "😀", false, "and JavaScript's own order differs");
  // FNV-64a("a") = 0xaf63dc4c8601ec8c and FNV-64a("foobar") = 0x85944171f73967e8 (the FNV test vectors): the
  // hash order is the reverse of the text order.
  t.equal(figures.nameFreeCompare("a", "foobar"), 1, "a after foobar by hash");
  t.equal(figures.nameFreeCompare("foobar", "a"), -1, "foobar before a by hash");
  t.equal(figures.nameFreeCompare("x", "x"), 0, "the same name");
  // FNV-64a("hello") = 0xa430d84680aabd0b, FNV-64a("a") = 0xaf63dc4c8601ec8c: decided by the high word.
  t.equal(figures.nameFreeCompare("hello", "a"), -1, "hello before a by hash");
});

test("measure: volume_measure = logged runs the answer once, under the logged measure", function (t) {
  var host = fakeHost({ requested: "logged", providers: [1], declares: ["widget_total", "widget_total_measured"],
    rows: { widget_total: [{ bytes: 5, allowed_bytes: 5, allowed_connections: 1 }], widget_total_measured: [reportedRow(100)] } });
  var runs = 0;
  var answer = t.lib.figures.measure.serve(host, function (h) {
    runs++;
    return { data: { total: t.lib.figures.figures.volume(h.query.row("widget_total", {})), measure: t.lib.figures.figures.measure }, has_rows: true };
  });
  t.equal(runs, 1, "one run");
  t.equal(answer.data.total.measure, "logged", "the figures' measure");
  t.equal(answer.data.total.bytes, 5, "read through the statement itself, not its twin");
  t.equal(answer.data.measure, "logged", "the measure in force");
  t.equal(JSON.stringify(host.states), "[]", "no state");
  t.equal(JSON.stringify(t.lib.figures.figures.declared), JSON.stringify(["reported"]), "the declared measures, read from the host");
  t.equal(t.lib.figures.figures.measures.reported, "reported", "and listed among the measures");
});

test("measure: with no active provider of the measure's kind, best_available answers logged with the measure's state", function (t) {
  var host = fakeHost({ requested: "best_available", providers: [] });
  var runs = 0;
  var answer = t.lib.figures.measure.serve(host, function (h) {
    runs++;
    return { data: { zero: t.lib.figures.figures.zero() }, has_rows: false };
  });
  t.equal(runs, 1, "one run: the measure was never tried");
  t.equal(JSON.stringify(answer.data.zero.measure), undefined, "logged figures");
  t.equal(JSON.stringify(host.states), JSON.stringify(["reporter_off"]), "the no-provider state");
  t.equal(JSON.stringify(host.details), "{}", "which carries no detail");
  t.equal(host.calls.filter(function (call) { return call.name === "widget_measure_providers"; }).length, 1, "the providers read once");
});

test("measure: a covered window answers the measure, with the twins run in the statements' place", function (t) {
  var host = fakeHost({ requested: "best_available", providers: [1],
    intervals: [{ starts_at: SUNDAY - 86400, ends_at: null, state: "reporting" }],
    declares: ["widget_total", "widget_total_measured", "widget_names"],
    rows: { widget_total_measured: [reportedRow(100)], widget_total: [{ bytes: 999 }], widget_names: [{ name: "a" }] } });
  var figures = t.lib.figures.figures;
  var runs = 0;
  var answer = t.lib.figures.measure.serve(host, function (h) {
    runs++;
    var zero = figures.zero();
    var total = figures.add(zero, h.query.row("widget_total", { from: 1, to: 2 }));
    var names = h.query.rows("widget_names", {});
    h.envelope.state("built");
    return { data: { zero: zero, total: figures.volume(total), bytes: figures.connectionBytes(total), count: figures.count(3),
      names: names.length, measure: figures.measure, value: figures.value("packets", total) }, has_rows: true };
  });
  t.equal(runs, 1, "answered at the first run");
  t.equal(answer.data.measure, "reported", "the measure in force during the run");
  t.equal(figures.measure, "logged", "and logged again after it");
  t.equal(JSON.stringify(answer.data.zero), JSON.stringify({ bytes: 0, allowed_bytes: 0, blocked_bytes: 0, unknown_bytes: 0, allowed_connections: 0,
    blocked_connections: 0, unknown_connections: 0, measure: "reported", measure_parts: parts(0, 0, 0, 0, 0, 0, 0, 0, 0) }), "zero carries the measure and empty parts");
  var twin = host.calls.filter(function (call) { return call.name === "widget_total_measured"; });
  t.equal(twin.length, 1, "the twin was run");
  t.equal(JSON.stringify(twin[0].parameters), JSON.stringify({ from: 1, to: 2, measure: "reported" }), "with the statement's parameters and the measure");
  t.equal(host.calls.filter(function (call) { return call.name === "widget_total"; }).length, 0, "and the statement itself was not");
  t.equal(answer.data.names, 1, "a statement with no twin runs as it is");
  t.equal(JSON.stringify(answer.data.total), JSON.stringify({ measure: "reported", lower_bound: true, bytes: 100, allowed_bytes: 100, blocked_bytes: 0,
    unknown_bytes: 0, connections: 1, allowed_connections: 1, blocked_connections: 0, unknown_connections: 0, packets: 10, allowed_packets: 10,
    blocked_packets: 0, unknown_packets: 0,
    coverage: { complete: 1, partial: 0, unmeasured: 0, not_known: 0, direction_states: { reported: 2, unreported: 0 }, estimated: 1 },
    allowed_coverage: { complete: 1, partial: 0, unmeasured: 0, not_known: 0, direction_states: { reported: 2, unreported: 0 }, estimated: 1 },
    blocked_coverage: { complete: 0, partial: 0, unmeasured: 0, not_known: 0, direction_states: { reported: 0, unreported: 0 }, estimated: 0 },
    unknown_coverage: { complete: 0, partial: 0, unmeasured: 0, not_known: 0, direction_states: { reported: 0, unreported: 0 }, estimated: 0 } }),
    "the measured Volume, with its packets and its coverage whole and per part");
  t.equal(JSON.stringify(answer.data.bytes), JSON.stringify({ measure: "reported", lower_bound: false, value: 100 }),
    "one connection complete in every direction: the only exact figure");
  t.equal(JSON.stringify(answer.data.count), JSON.stringify({ measure: "reported", lower_bound: true, value: 3 }), "a count in the measure");
  t.equal(answer.data.value, 10, "the packets figure");
  t.equal(JSON.stringify(host.states), JSON.stringify(["built"]), "the run's own state is kept, and no measure state is added");
  t.equal(t.lib.figures.measure.windowState(host, "reported"), null, "the window state of a measure that can answer");
});

test("measure: a Volume handed back is read as its figures, with its parts from its packets and coverage", function (t) {
  var host = fakeHost({ requested: "reported", providers: [1], intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }] });
  var figures = t.lib.figures.figures;
  var answer = t.lib.figures.measure.serve(host, function () {
    var volume = figures.volume(reportedRow(40));
    var again = figures.add(figures.zero(), volume);
    var moved = figures.withMeasure({}, reportedRow(40));
    return { data: { again: again, moved: moved, same: JSON.stringify(figures.volume(again)) === JSON.stringify(volume) }, has_rows: true };
  });
  t.equal(JSON.stringify(answer.data.again.measure_parts), JSON.stringify(parts(40, 10, 1, 0, 0, 0, 2, 0, 1)), "the parts read back from the Volume");
  t.equal(answer.data.same, true, "and the Volume built again is the same");
  t.equal(JSON.stringify(answer.data.moved.measure_parts), JSON.stringify(parts(0, 10, 1, 0, 0, 0, 2, 0, 1)), "withMeasure takes the row's parts, the bytes its own");
  t.equal(JSON.stringify(host.states), "[]", "every connection covered: no state");
});

test("measure: a run that builds a logged figure is not served in the measure; best_available falls back to the logged run", function (t) {
  var host = fakeHost({ requested: "best_available", providers: [1], intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }] });
  var figures = t.lib.figures.figures;
  var measures = [];
  var answer = t.lib.figures.measure.serve(host, function (h) {
    measures.push(figures.measure);
    h.envelope.state("seen_" + figures.measure);
    return { data: { total: figures.volume({ bytes: 5, allowed_bytes: 5, allowed_connections: 1 }), records: figures.loggedCount(0) }, has_rows: true };
  });
  t.equal(JSON.stringify(measures), JSON.stringify(["reported", "logged"]), "the measured run, then the logged one");
  t.equal(answer.data.total.measure, "logged", "the logged answer");
  t.equal(JSON.stringify(host.states), JSON.stringify(["seen_logged", "reporting_not_served"]),
    "the measured run's state was restored away; the not-served state names why");
  t.equal(JSON.stringify(host.details), "{}", "the not-served state carries no detail");
});

test("measure: a figure the answer wrote as logged itself, or an answer that throws, is not served in the measure either", function (t) {
  var figures = t.lib.figures.figures;
  var host = fakeHost({ requested: "best_available", providers: [1], intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }] });
  var answer = t.lib.figures.measure.serve(host, function () {
    return { data: { rows: [{ rate: { measure: "logged", value: 1 } }] }, has_rows: true };
  });
  t.equal(JSON.stringify(host.states), JSON.stringify(["reporting_not_served"]), "a logged member found anywhere in the data");
  t.equal(answer.data.rows[0].rate.value, 1, "the logged run's data");
  var thrower = fakeHost({ requested: "best_available", providers: [1], intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }] });
  var runs = 0;
  var served = t.lib.figures.measure.serve(thrower, function () {
    runs++;
    if (figures.measure === "reported") throw new TypeError("cannot build in the measure");
    return { data: { total: figures.volume(figures.zero()) }, has_rows: false };
  });
  t.equal(runs, 2, "the throw ended the measured run, and the logged one answered");
  t.equal(served.data.total.measure, "logged", "the logged answer");
  t.equal(JSON.stringify(thrower.states), JSON.stringify(["reporting_not_served"]), "the not-served state");
  t.equal(figures.measure, "logged", "the logged measure is back in force after the throw");
});

test("measure: a connection with a direction not covered: best_available falls back, the measure asked for answers its figures", function (t) {
  var uncovered = reportedRow(60);
  uncovered.measure_parts = parts(60, 6, 0, 1, 0, 0, 1, 1, 0);
  var arranged = { declares: ["widget_total", "widget_total_measured"], providers: [1],
    intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }],
    rows: { widget_total: [{ bytes: 9, allowed_bytes: 9, allowed_connections: 1 }], widget_total_measured: [uncovered] } };
  var figures = t.lib.figures.figures;
  var host = fakeHost(Object.assign({ requested: "best_available" }, arranged));
  var runs = 0;
  var answer = t.lib.figures.measure.serve(host, function (h) {
    runs++;
    return { data: { total: figures.volume(h.query.row("widget_total", {})) }, has_rows: true };
  });
  t.equal(runs, 2, "two runs");
  t.equal(answer.data.total.measure + " " + answer.data.total.bytes, "logged 9", "the logged answer, read through the statement itself");
  t.equal(JSON.stringify(host.states), JSON.stringify(["reporting_partial"]), "the not-covering state");
  t.equal(JSON.stringify(host.details), JSON.stringify({ reporting_partial: { interval_states: ["reporting"] } }),
    "its detail: the window was covered throughout, the judgment is the run's");
  var explicit = fakeHost(Object.assign({ requested: "reported" }, arranged));
  runs = 0;
  var served = t.lib.figures.measure.serve(explicit, function (h) {
    runs++;
    var row = h.query.row("widget_total", {});
    figures.cover([row]);
    return { data: { total: figures.volume(row) }, has_rows: true };
  });
  t.equal(runs, 1, "one run");
  t.equal(served.data.total.measure, "reported", "the measure's figures, each with its coverage");
  t.equal(served.data.total.coverage.direction_states.unreported, 1, "the direction not covered is counted");
  t.equal(JSON.stringify(explicit.states), JSON.stringify(["reporting_partial"]), "and the state that says not every connection was covered");
});

test("measure: a window the intervals do not cover throughout: the measure asked for answers no figure, best_available logged", function (t) {
  var intervals = [{ starts_at: SUNDAY - 86400, ends_at: SUNDAY - 1800, state: "reporting" }];
  var figures = t.lib.figures.figures;
  var explicit = fakeHost({ requested: "reported", providers: [1], intervals: intervals });
  var runs = 0;
  var answer = t.lib.figures.measure.serve(explicit, function () {
    runs++;
    return { data: { total: figures.volume(figures.zero()) }, has_rows: true };
  });
  t.equal(runs, 1, "the logged run alone: the measure was never tried");
  t.equal(answer.data, null, "an explicit measure that cannot answer carries no figure");
  t.equal(answer.has_rows, true, "and keeps has_rows");
  t.equal(JSON.stringify(explicit.states), JSON.stringify(["reporting_partial"]), "the window mixes two states");
  t.equal(JSON.stringify(explicit.details), JSON.stringify({ reporting_partial: { interval_states: ["reporting", "reporting_unrecorded"] } }),
    "the interval states met, in name order");
  var best = fakeHost({ requested: "best_available", providers: [1], intervals: intervals });
  var served = t.lib.figures.measure.serve(best, function () { return { data: { total: figures.volume(figures.zero()) }, has_rows: false }; });
  t.equal(served.data.total.measure, "logged", "best_available answers logged");
  t.equal(JSON.stringify(best.states), JSON.stringify(["reporting_partial"]), "with the same state");
  t.equal(t.lib.figures.measure.windowState(explicit, "reported"), "reporting_partial", "windowState names the state");
  var thrown = false;
  try { t.lib.figures.measure.windowState(explicit, "nobody"); } catch (e) { thrown = e instanceof TypeError; }
  t.assert(thrown, "a measure nobody declares is a TypeError");
});

// The window's reading of a measure is made once per request, so each window below is a request of its own.
test("measure: a window in one state that is not covered answers that state, with no detail", function (t) {
  var single = fakeHost({ requested: "reported", providers: [1], intervals: [{ starts_at: 0, ends_at: SUNDAY - 7200, state: "reporting" }] });
  var answer = t.lib.figures.measure.serve(single, function () { return { data: { n: 1 }, has_rows: true }; });
  t.equal(answer.data, null, "no figure");
  t.equal(JSON.stringify(single.states), JSON.stringify(["reporting_unrecorded"]), "the unrecorded state: the interval ended before the window");
  t.equal(JSON.stringify(single.details), "{}", "which carries no detail");
});

test("measure: two active providers of an exclusive kind cannot answer the window", function (t) {
  var two = fakeHost({ requested: "best_available", providers: [1, 2], intervals: [{ starts_at: 0, ends_at: null, state: "reporting" }] });
  var answer = t.lib.figures.measure.serve(two, function () { return { data: { n: t.lib.figures.figures.count(1) }, has_rows: true }; });
  t.equal(answer.data.n.measure, "logged", "the logged answer");
  t.equal(JSON.stringify(two.states), JSON.stringify(["reporting_partial"]), "the not-covering state");
  t.equal(JSON.stringify(two.details), JSON.stringify({ reporting_partial: { interval_states: ["reporting"] } }), "with the interval states met");
});

test("measure: a widget that is not windowed is answered at its instant", function (t) {
  var instant = fakeHost({ requested: "reported", providers: [1], window: null, intervals: [{ starts_at: SUNDAY - 1, ends_at: null, state: "reporting" }] });
  var now = t.lib.figures.measure.serve(instant, function () { return { data: { n: t.lib.figures.figures.count(1) }, has_rows: true }; });
  t.equal(now.data.n.measure, "reported", "the measure covers the instant");
});

test("measure: an interval starting after the instant leaves it unrecorded", function (t) {
  var before = fakeHost({ requested: "reported", providers: [1], window: null, intervals: [{ starts_at: SUNDAY + 1, ends_at: null, state: "reporting" }] });
  var later = t.lib.figures.measure.serve(before, function () { return { data: { n: t.lib.figures.figures.count(1) }, has_rows: true }; });
  t.equal(later.data, null, "an interval starting after the instant does not");
  t.equal(JSON.stringify(before.states), JSON.stringify(["reporting_unrecorded"]), "the instant is unrecorded");
});
