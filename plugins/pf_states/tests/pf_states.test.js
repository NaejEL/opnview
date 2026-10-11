// The pf_states plugin's own tests, run by `opnview plugin test plugins/pf_states` against a fake
// firewall: the state table read twice, a minute apart, as two passes read it.

// AT is the instant of the first read: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// row is one state of a later read.
function row(id, iface, proto, direction, dstPort, age, bytes) {
  return { id: id, iface: iface, proto: proto, direction: direction, src_addr: "192.0.2.10", src_port: "50000",
    dst_addr: "203.0.113.7", dst_port: dstPort, state: "ESTABLISHED:ESTABLISHED", age: age, expires: "24:00:00",
    label: "", pkts: [1, 1], bytes: bytes };
}

// secondRead is the table a minute after the fixture's: the two TCP states grew by 7500 bytes forward
// and 30000 reverse, the UDP state is gone, a TCP state to port 80 was created 30 seconds ago, an
// ICMP state 10 seconds ago, and an old state the first read did not hold is left out.
function secondRead() {
  var rows = [
    row("00000000000000a1/00000001", "exdev0", "tcp", "in", "443", "00:06:00", [17500, 80000]),
    row("00000000000000a2/00000001", "exdevup", "tcp", "out", "443", "00:06:00", [17500, 80000]),
    row("00000000000000a4/00000001", "exdev0", "tcp", "in", "80", "00:00:30", [600, 1500]),
    row("00000000000000a5/00000001", "exdev0", "icmp", "in", "0", "00:00:10", [84, 84]),
    row("00000000000000a6/00000001", "exdev0", "tcp", "in", "22", "02:00:00", [999999, 999999])
  ];
  return { json: { rows: rows, rowCount: rows.length, total: rows.length, current: 1 } };
}

// firstPass runs a pass on the fixture's read.
function firstPass(t) {
  t.clock(AT);
  t.answer("query_states", { fixture: "query_states.json" });
  var first = t.call("pf_state_rate", "collect", {});
  t.equal(first.error, null, "the first pass");
  return first;
}

// secondPass runs the next pass a minute later, on an arranged answer.
function secondPass(t, first, answer) {
  t.clock(AT + 60);
  t.answer("query_states", answer);
  return t.call("pf_state_rate", "collect", first.state);
}

test("the state table is probed reachable through a POST to a read action", function (t) {
  t.clock(AT);
  t.answer("query_states", { fixture: "query_states.json" });
  var probe = t.call("pf_state_rate", "probe");
  t.equal([probe.result.state, probe.result.outcome], ["reachable", "healthy"], "the probe");
  var requests = t.requests("query_states");
  t.equal(requests.length, 1, "one request");
  t.equal(requests[0].method, "POST", "the method");
});

test("a firewall without the action is unavailable, not an error", function (t) {
  t.clock(AT);
  var probe = t.call("pf_state_rate", "probe");
  t.equal([probe.result.state, probe.result.outcome, probe.error], ["unavailable", "not_found", null], "the probe");
});

test("the first pass stores no rate: one read is not a rate", function (t) {
  var first = firstPass(t);
  t.equal(first.rows.rate, undefined, "no rate row");
  t.assert(typeof first.state.snapshot === "string", "the read is kept for the next pass");
});

test("the second pass stores the rate by interface, direction, protocol and port", function (t) {
  var second = secondPass(t, firstPass(t), secondRead());
  t.equal(second.error, null, "the second pass");
  var rows = second.rows.rate.map(function (rate) {
    return [rate.interface_device, rate.direction, rate.protocol, rate.port, rate.forward_bits_per_second,
      rate.reverse_bits_per_second, rate.bits_per_second, rate.states, rate.span_seconds, rate.sampled_at];
  });
  t.equal(rows, [
    ["exdev0", "in", "icmp", null, 84 * 8 / 60, 84 * 8 / 60, 168 * 8 / 60, 1, 60, AT + 60],
    ["exdev0", "in", "tcp", 443, 7500 * 8 / 60, 30000 * 8 / 60, 37500 * 8 / 60, 1, 60, AT + 60],
    ["exdev0", "in", "tcp", 80, 600 * 8 / 60, 1500 * 8 / 60, 2100 * 8 / 60, 1, 60, AT + 60],
    ["exdevup", "out", "tcp", 443, 7500 * 8 / 60, 30000 * 8 / 60, 37500 * 8 / 60, 1, 60, AT + 60]
  ], "two reads' difference over the seconds between them; a new state counts whole; ICMP has no port");
  t.equal(second.result.outcome, "healthy", "the outcome");
  t.assert(second.result.detail.indexOf("1 left out") >= 0,
    "the old state the first read lacked is left out: " + second.result.detail);
});

test("a counter that went down is left out, never a negative rate", function (t) {
  var rows = [row("00000000000000a1/00000001", "exdev0", "tcp", "in", "443", "00:06:00", [5, 5])];
  var second = secondPass(t, firstPass(t), { json: { rows: rows, rowCount: 1, total: 1, current: 1 } });
  t.equal(second.rows.rate, undefined, "no row");
});

test("a read truncated by max_states says so", function (t) {
  var answer = secondRead();
  answer.json.total = 90000;
  var second = secondPass(t, firstPass(t), answer);
  t.equal(second.result.outcome, "partial_readings", "the outcome");
  t.assert(second.result.detail.indexOf("5 of 90000 states read") >= 0, second.result.detail);
});

test("max_states is the page size asked for", function (t) {
  t.setting("max_states", 2000);
  firstPass(t);
  var requests = t.requests("query_states");
  t.equal(requests[requests.length - 1].form.rowCount, "2000", "the page size");
});

test("pfctl's age and a port are read as pfctl and list_states.py write them", function (t) {
  t.equal([t.helper("ageSeconds", "00:01:05"), t.helper("ageSeconds", "27:00:00"), t.helper("ageSeconds", "x")],
    [65, 97200, null], "ages");
  t.equal([t.helper("portOf", "tcp", "443"), t.helper("portOf", "icmp", "0"), t.helper("portOf", "udp", "0")],
    [443, null, null], "ports");
});
