// The netflow plugin's own tests: its probe and its capture read, through the plugin loader and the
// adapter the collector runs it through, and its push entry, through opnview's real NetFlow decoder
// and router. What the core makes of the records -- their Observation Point, the join, the measured
// volume -- is tested in opnview's core (internal/collect).

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

test("one captured interface is read with both its directions", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_one_interface.json" });
  var read = t.call("flow_record", "capture");
  if (read.error !== null) {
    t.fatal("reading the capture configuration: " + read.error);
  }
  t.equal(read.capture.enabled, true, "enabled");
  t.equal(read.capture.export_version, "v9", "the export version");
  t.equal(read.capture.active_timeout_seconds, 1800, "the active timeout");
  t.equal(read.capture.inactive_timeout_seconds, 15, "the inactive timeout");
  sameInterfaces(t, read.capture.interfaces, [{ identifier: "lan", ingress: true, egress: true }]);
});

test("an egress-only interface captures egress alone, and one listed only as egress-only is not captured", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 1 } });
  t.answer("getconfig", { fixture: "getconfig_egress_only.json" });
  var read = t.call("flow_record", "capture");
  if (read.error !== null) {
    t.fatal("reading the capture configuration: " + read.error);
  }
  sameInterfaces(t, read.capture.interfaces, [
    { identifier: "lan", ingress: true, egress: true },
    { identifier: "wan", ingress: false, egress: true }
  ]);
  t.equal(read.capture.export_version, "v5", "the export version");
  t.equal(read.capture.active_timeout_seconds, undefined, "an absent timeout is absent, never a default");
});

test("a refused or unreadable configuration is an error naming what the firewall answered", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { status: 403, json: { message: "denied" } });
  var refused = t.call("flow_record", "capture");
  t.assert(refused.error !== null, "a refused read is not an error");
  t.equal(refused.result.outcome, "denied", "the outcome of a 403");
  t.answer("getconfig", { json: { netflow: "not the model" } });
  var unreadable = t.call("flow_record", "capture");
  t.assert(unreadable.error !== null, "a body that is not the model is not an error");
  t.equal(unreadable.result.outcome, "undecodable", "the outcome of an unparseable body");
});

test("the capture read calls only the two read-only actions", function (t) {
  t.answer("is_enabled", { json: { netflow: 1, local: 0 } });
  t.answer("getconfig", { fixture: "getconfig_one_interface.json" });
  t.call("flow_record", "probe");
  t.call("flow_record", "capture");
  t.equal(t.requests("getconfig").length, 1, "requests to getconfig");
  t.equal(t.requests("is_enabled").length, 2, "requests to is_enabled");
  t.requests("getconfig").concat(t.requests("is_enabled")).forEach(function (request) {
    t.equal(request.method, "GET", "a capture read's method");
  });
});

test("a v9 record is mapped from its Information Elements with no script", function (t) {
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  var records = t.pushed("flow_records");
  t.equal(records.length, 1, "records");
  var record = records[0];
  t.equal(record.exporter_address, "192.0.2.1", "the exporter");
  t.equal(record.observation_domain, 0, "the observation domain");
  t.equal(record.ingress_interface, 3, "the raw ingress index");
  t.equal(record.egress_interface, 1, "the raw egress index");
  t.equal(record.ip_version, 4, "the version, read from the addresses");
  t.equal(record.protocol, 6, "the protocol number");
  t.equal(record.src_address, "198.51.100.10", "the source");
  t.equal(record.dst_address, "203.0.113.20", "the destination");
  t.equal(record.src_port, 44321, "the source port");
  t.equal(record.dst_port, 443, "the destination port");
  t.equal(record.tcp_flags, 27, "the cumulative flags");
  t.equal(record.octets, 14200, "the octets");
  t.equal(record.packets, 10, "the packets");
  t.equal(record.start_at, 1790467140, "uptime 40 s, exported at uptime 100 s");
  t.equal(record.end_at, 1790467195, "uptime 95 s");
  t.equal(record.identity.length, 64, "the core's identity");
  t.equal(t.pushCounters("flow_records").script_calls, undefined, "a script call for a mapped record");
});

test("a datagram delivered twice gives the same identities", function (t) {
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  t.deliverNetflow(t.fixture("netflow_v9.json"));
  var records = t.pushed("flow_records");
  t.equal(records.length, 2, "records handed on: the store keeps one row per identity");
  t.equal(records[0].identity, records[1].identity, "the identities of one datagram delivered twice");
});
