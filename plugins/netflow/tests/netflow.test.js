// The netflow plugin's own tests: its probe and its capture read, through the plugin loader and the adapter the
// collector runs it through; its push entry, through opnview's real NetFlow decoder and router; and the rules of
// its join -- the index map, the tuple, the capture history and the decision of a group (NJ3 to NJ5 of the
// amendment "the NetFlow join with every interface captured", and N2 of the live validation of 17 October 2026) --
// through the helpers the script exports for them. The join over NJ12's fixtures F1 to F17, through the real
// filter-log parser and the core's pairing and aggregates, is run in opnview's core (internal/collect,
// netflowrecording_test.go), and compared there with the Go join's recording (testdata/go-join).

// sameInterfaces compares captured interfaces field by field: an answer's keys come in no set order.
function sameInterfaces(t, got, want) {
  t.equal((got || []).length, want.length, "captured interfaces");
  want.forEach(function (expected, index) {
    var actual = (got || [])[index] || {};
    ["identifier", "ingress", "egress"].forEach(function (field) {
      t.equal(actual[field], expected[field], "interface " + index + " " + field);
    });
  });
}

// collected runs the provider's pass at an instant from a state, and answers its rows, state and result.
function collected(t, at, state) {
  t.clock(at);
  var pass = t.call("flow_record", "collect", state || {});
  if (pass.error !== null) {
    t.fatal("the capture pass failed: " + pass.error);
  }
  return pass;
}

test("the netflow implementation declares itself as the flow_record kind", function (t) {
  t.assert(t.registered("flow_record"), "the netflow implementation is not registered in the flow_record kind");
});

test("an export that sends records is reachable and separable", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  var probe = t.call("flow_record", "probe");
  if (probe.error !== null) {
    t.fatal("the probe returned " + probe.error);
  }
  t.equal(probe.result.state, "reachable", "the state");
  t.equal(probe.result.outcome, "healthy", "the outcome");
  t.assert(probe.result.separable, "the export is not separable");
});

test("an export that captures nothing stays this implementation's, present but disabled", function (t) {
  t.answer("is_enabled", { json: { netflow: 0, local: 0 } });
  var probe = t.call("flow_record", "probe");
  t.equal(probe.result.state, "present_but_disabled", "the state");
  t.assert(probe.result.separable, "a disabled export is not separable, so its configuration would go unread");
});

test("an unanswered export is unavailable and never separable", function (t) {
  var probe = t.call("flow_record", "probe");
  t.equal(probe.result.state, "unavailable", "the state");
  t.equal(probe.result.outcome, "not_found", "the outcome");
  t.assert(!probe.result.separable, "an absent export is separable");
});

test("one captured interface is recorded with both its directions and the run's timeouts", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_one_interface.json" });
  var pass = collected(t, 1000);
  var run = pass.rows.capture_configuration[0];
  t.equal(run.readable, true, "readable");
  t.equal(run.enabled, true, "enabled");
  t.equal(run.export_version, "v9", "the export version");
  t.equal(run.active_timeout_seconds, 1800, "the active timeout");
  t.equal(run.inactive_timeout_seconds, 15, "the inactive timeout");
  t.equal(run.first_read_at, 1000, "the run's first read");
  sameInterfaces(t, pass.rows.capture_interface, [{ identifier: "example_if_a", ingress: true, egress: true }]);
});

test("an egress-only interface captures egress alone, and one listed only as egress-only is not captured", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 1 } });
  t.answer("getconfig", { fixture: "getconfig_egress_only.json" });
  var pass = collected(t, 1000);
  sameInterfaces(t, pass.rows.capture_interface, [
    { identifier: "example_if_a", ingress: true, egress: true },
    { identifier: "example_if_u", ingress: false, egress: true }
  ]);
  t.equal(pass.rows.capture_configuration[0].export_version, "v5", "the export version");
  t.equal(pass.rows.capture_configuration[0].active_timeout_seconds, null, "an absent timeout is absent, never a default");
});

test("a refused or unreadable configuration is recorded unreadable, naming what the firewall answered", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { status: 403, json: { message: "denied" } });
  var refused = collected(t, 1000);
  t.equal(refused.result.outcome, "denied", "the outcome of a 403");
  t.equal(refused.rows.capture_configuration[0].readable, false, "a refused read is readable");
  t.equal(refused.rows.capture_configuration[0].outcome, "denied", "the run's outcome");
  t.equal((refused.rows.capture_interface || []).length, 0, "an unreadable run holds interfaces");
  t.answer("getconfig", { json: { netflow: "not the model" } });
  var unreadable = collected(t, 1100, refused.state);
  t.equal(unreadable.result.outcome, "undecodable", "the outcome of an unparseable body");
  t.equal(unreadable.rows.capture_configuration[0].first_read_at, 1100, "a failure of another kind opens a run");
});

test("a read equal to the newest run extends it, and any other opens one", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_one_interface.json" });
  var first = collected(t, 1000);
  var same = collected(t, 1060, first.state);
  t.equal(same.rows.capture_configuration[0].first_read_at, 1000, "the same configuration extends its run");
  t.equal(same.rows.capture_configuration[0].last_read_at, 1060, "the run's last read");
  t.equal((same.rows.capture_interface || []).length, 0, "an extension rewrites the run's interfaces");
  t.answer("getconfig", { fixture: "getconfig_egress_only.json" });
  var other = collected(t, 1120, same.state);
  t.equal(other.rows.capture_configuration[0].first_read_at, 1120, "another configuration opens a run");
  t.equal(other.rows.capture_interface.length, 2, "the new run's interfaces");
  var older = collected(t, 1090, other.state);
  t.equal((older.rows.capture_configuration || []).length, 0, "a read older than the newest one recorded changes nothing");
});

test("the capture read calls only the three read-only actions", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_one_interface.json" });
  t.answer("interfaces_info", { fixture: "interfaces_info_details.json" });
  t.call("flow_record", "probe");
  collected(t, 1000);
  t.equal(t.requests("getconfig").length, 1, "requests to getconfig");
  t.equal(t.requests("is_enabled").length, 2, "requests to is_enabled");
  t.equal(t.requests("interfaces_info").length, 1, "requests to interfaces_info");
  t.requests("getconfig").concat(t.requests("is_enabled"), t.requests("interfaces_info")).forEach(function (request) {
    t.equal(request.method, "GET", "a capture read's method");
  });
});

test("every captured interface says whether it is exported, and a parent its VLANs", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 1 } });
  t.answer("getconfig", { fixture: "getconfig_every_interface.json" });
  t.answer("interfaces_info", { fixture: "interfaces_info_details.json" });
  var pass = collected(t, 1000);
  var byIdentifier = {};
  pass.rows.capture_interface.forEach(function (captured) { byIdentifier[captured.identifier] = captured; });
  t.equal(pass.rows.capture_interface.length, 5, "the captured interfaces");
  t.equal(byIdentifier.example_if_a.exported, "yes", "example_if_a exported");
  t.equal(byIdentifier.example_if_p.parent_of, "example_if_n,example_if_v", "example_if_p parent_of");
  t.equal(byIdentifier.example_if_v.parent_of, "", "example_if_v parent_of");
  t.equal(byIdentifier.example_if_w.exported, "no", "example_if_w exported");
  t.equal(byIdentifier.example_if_w.not_exported_reason, "no_export_point", "example_if_w not_exported_reason");
  t.equal(byIdentifier.example_if_u.exported, "yes", "example_if_u exported");
});

test("an unreadable interfaces_info leaves every export unknown and says so", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_every_interface.json" });
  t.answer("interfaces_info", { status: 403, json: { message: "denied" } });
  var pass = collected(t, 1000);
  t.equal(pass.rows.capture_configuration[0].readable, true, "an unreadable interfaces_info made the run unreadable");
  pass.rows.capture_interface.forEach(function (captured) {
    t.equal(captured.exported, "unknown", captured.identifier + " exported");
    t.equal(captured.parent_of, "", captured.identifier + " parent_of");
  });
  t.assert(pass.result.detail.indexOf("interfaces_info answered forbidden") >= 0, "the detail names what interfaces_info answered: " +
    pass.result.detail);
});

test("a v9 record is stored from its Information Elements with no script, beside its delivery", function (t) {
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  var records = t.pushed("flow_records");
  t.equal(records.length, 1, "records");
  var record = records[0];
  t.equal(record.exporter_address, "192.0.2.1", "the exporter");
  t.equal(record.observation_domain, 0, "the observation domain");
  t.equal(record.ingress_interface, 13, "the raw ingress index");
  t.equal(record.egress_interface, 14, "the raw egress index");
  t.equal(record.ip_version, null, "the version the record does not carry, which the join reads from the addresses");
  t.equal(record.protocol, 6, "the protocol number");
  t.equal(record.src_address, "198.51.100.10", "the source");
  t.equal(record.dst_address, "203.0.113.20", "the destination");
  t.equal(record.src_port, 44321, "the source port");
  t.equal(record.dst_port, 443, "the destination port");
  t.equal(record.tcp_flags, 27, "the cumulative flags");
  t.equal(record.octets, 14200, "the octets");
  t.equal(record.packets, 10, "the packets");
  t.equal(record.flow_start_at, 1790467140, "uptime 40 s, exported at uptime 100 s");
  t.equal(record.flow_end_at, 1790467195, "uptime 95 s");
  t.equal(record.delivery.length, 64, "the delivery's digest");
  t.equal(record.join_state, null, "a record is decided by the derivation, never at reception");
  t.equal(t.pushCounters("flow_records").script_calls, undefined, "a script call for a mapped record");
});

test("a datagram delivered twice names one delivery", function (t) {
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  var records = t.pushed("flow_records");
  t.equal(records.length, 2, "records handed on: the table keeps one row per delivery and position");
  t.equal(records[0].delivery, records[1].delivery, "the deliveries of one datagram delivered twice");
  t.equal(records[0].delivery_position, records[1].delivery_position, "the positions");
});

// The index map of interfaces_info_details.json: example_if_a on index 21 ... the loopback on 27.
var interfacesInfo = null;

function interfaces(t) {
  if (interfacesInfo === null) {
    interfacesInfo = t.fixture("interfaces_info_details.json");
  }
  return interfacesInfo;
}

test("an index names its interface, index 0 no receiving interface, the loopback's local delivery (NJ3)", function (t) {
  var mapped = t.helper("map_index", interfaces(t), 21);
  t.equal(mapped.role, "interface", "index 21");
  t.equal(mapped.identifier, "example_if_a", "index 21's identifier");
  t.equal(mapped.device, "exnic0", "index 21's device");
  t.equal(t.helper("map_index", interfaces(t), 0).role, "no_receiving_interface", "index 0");
  t.equal(t.helper("map_index", interfaces(t), 27).role, "local_delivery", "the loopback's index, read from its flag");
  t.equal(t.helper("map_index", interfaces(t), 99).role, "unmapped", "an index the map does not hold");
  t.equal(t.helper("map_index", interfaces(t), null).role, "unmapped", "no index");
});

test("a row with no index maps nothing, and two loopbacks establish none", function (t) {
  var body = { rows: [
    { identifier: "example_if_a", device: "exnic0", statistics: {} },
    { identifier: "", device: "exlo0", flags: ["loopback"], statistics: { index: "27" } },
    { identifier: "", device: "exlo1", flags: ["loopback"], statistics: { index: "28" } }
  ] };
  var map = t.helper("index_map", body);
  t.equal(Object.keys(map.byIndex).length, 2, "the indexes held");
  t.equal(map.loopback, 0, "two rows carrying the loopback flag");
});

test("a tuple is keyed unordered, its addresses by value, and ICMP on its addresses alone", function (t) {
  var forward = t.helper("tuple_key", 4, 6, "198.51.100.10", "192.0.2.80", 50000, 443);
  var reverse = t.helper("tuple_key", 4, 6, "192.0.2.80", "198.51.100.10", 443, 50000);
  t.equal(forward, reverse, "the two directions of one connection");
  t.equal(forward, "4|6|192.0.2.80#443|198.51.100.10#50000", "the key");
  t.equal(t.helper("tuple_key", 6, 58, "2001:db8:0:0::10", "2001:db8::1", 0, 0), "6|58|2001:db8::1|2001:db8::10",
    "ICMPv6, an address written two ways");
});

test("a configuration is in force over its run, and unknown between two that differ (PS18)", function (t) {
  var runs = [
    { firstReadAt: 100, lastReadAt: 200, readable: true, enabled: true, interfaces: [{ ingress: true, egress: true }] },
    { firstReadAt: 300, lastReadAt: 400, readable: false, enabled: false, interfaces: [] },
    { firstReadAt: 500, lastReadAt: 600, readable: true, enabled: true, interfaces: [] }
  ];
  t.equal(t.helper("configuration_at", runs, 50), null, "before the first read");
  t.equal(t.helper("configuration_at", runs, 150), 100, "inside a run");
  t.equal(t.helper("configuration_at", runs, 250), null, "between two runs");
  t.equal(t.helper("configuration_at", runs, 350), null, "a run whose read failed");
  t.equal(t.helper("configuration_at", runs, 900), 500, "after the newest read");
  var intervals = t.helper("intervals", runs);
  t.equal(JSON.stringify(intervals.map(function (interval) { return interval.state; })),
    JSON.stringify(["capture_configured", "capture_configuration_unknown", "netflow_not_configured"]),
    "the window states");
  t.equal(intervals[0].ends_at, 201, "a run ends the instant after its last read when another follows");
  t.equal(intervals[2].ends_at, null, "the newest run is in force while no read says otherwise");
});

// pf's default state timeouts, pf.h's PFTM_*_VAL as the pf plugin exports them (survey, S12).
var timeouts = { tcpFirst: 120, tcpEstablished: 86400, tcpClosing: 900, tcpClosed: 90, udpMultiple: 60,
  icmpFirst: 20, otherMultiple: 60 };

// copyShape is a translated copy's shape: a record received inside with the translated source and port the
// second leg logged upstream, and the two legs of its connection.
function copyShape() {
  var record = { identity: "a", start_at: 1000, start_millisecond: 0, end_at: 1010, src_address: "203.0.113.2",
    dst_address: "192.0.2.80", src_port: 61000, dst_port: 443, protocol: 6, ip_version: 4, tcp_flags: 0x1b,
    octets: 1000, packets: 10, ingress_interface: 23, egress_interface: 25, ingress_role: "interface",
    ingress_identifier: "example_if_a", ingress_device: "exdev0", egress_role: "interface",
    egress_identifier: "example_if_d", egress_device: "exdev3", join_state: "pending", joined_flow_id: null,
    copy_kind: null, copy_flow_id: null, orientation: null, unmatched_reason: null };
  var legs = [
    { flow_id: 1, observed_at: 1000, device: "exdev0", direction: "in", action: "pass", pair_outcome: "first_leg",
      src_address: "198.51.100.10", dst_address: "192.0.2.80", src_port: 50000, dst_port: 443, protocol: "tcp",
      ip_version: 4 },
    { flow_id: 2, observed_at: 1000, device: "exdev3", direction: "out", action: "pass", pair_outcome: "second_leg",
      src_address: "203.0.113.2", dst_address: "192.0.2.80", src_port: 61000, dst_port: 443, protocol: "tcp",
      ip_version: 4 }
  ];
  return { record: record, legs: legs };
}

function decideOne(t, spec) {
  var decisions = t.helper("decide", spec);
  t.equal(decisions.length, 1, "decisions");
  return decisions[0] || {};
}

test("a translated copy is classified by each of its two signals alone (NJ-AC7)", function (t) {
  var shape = copyShape();
  var byLeg = decideOne(t, { records: [shape.record], legs: shape.legs, readThrough: 5000, tolerance: 3,
    timeouts: timeouts });
  t.equal(byLeg.join_state, "copy", "by the second leg's tuple alone");
  t.equal(byLeg.copy_kind, "translated_copy", "the copy kind");
  t.equal(byLeg.copy_flow_id, 2, "the second leg it copies");
  t.equal(byLeg.orientation, "forward", "the orientation");
  t.equal(byLeg.joined_flow_id, null, "a copy joined the second leg");
  shape = copyShape();
  var byAddress = decideOne(t, { records: [shape.record], legs: shape.legs.slice(0, 1), readThrough: 5000,
    tolerance: 3, timeouts: timeouts, firewall: [{ address: "203.0.113.2", from: 0, until: 9999 }] });
  t.equal(byAddress.copy_kind, "translated_copy", "by the firewall's address alone");
  t.equal(byAddress.copy_flow_id, null, "a copy by address copies no leg");
  shape = copyShape();
  var neither = decideOne(t, { records: [shape.record], legs: shape.legs.slice(0, 1), readThrough: 5000,
    tolerance: 3, timeouts: timeouts });
  t.equal(neither.join_state, "unmatched", "with neither signal");
  t.equal(neither.unmatched_reason, "no_logged_leg", "the reason");
});

test("a record no leg explains, whose leg would lie in a filter-log gap, is filter_log_gap (N2)", function (t) {
  var shape = copyShape();
  var inGap = decideOne(t, { records: [shape.record], legs: shape.legs.slice(0, 1), readThrough: 5000, tolerance: 3,
    timeouts: timeouts, gaps: [{ interval_start_at: 990, interval_end_at: 1001 }] });
  t.equal(inGap.unmatched_reason, "filter_log_gap", "a gap meeting the span its leg was logged in");
  shape = copyShape();
  var before = decideOne(t, { records: [shape.record], legs: shape.legs.slice(0, 1), readThrough: 5000, tolerance: 3,
    timeouts: timeouts, gaps: [{ interval_start_at: 1004, interval_end_at: 1100 }] });
  t.equal(before.unmatched_reason, "no_logged_leg", "a gap after the record's start plus the tolerance");
});

test("a record starting while the filter log has not been read past it plus the tolerance stays pending", function (t) {
  var shape = copyShape();
  var decisions = t.helper("decide", { records: [shape.record], legs: shape.legs, readThrough: 1003, tolerance: 3,
    timeouts: timeouts });
  t.equal(decisions.length, 0, "a record decided before the filter log was read past it");
});
