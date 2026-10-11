// The unresolved_joins widget's own tests, run by `opnview plugin test plugins/unresolved_joins`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface, a client known only by its address on it, and three records
// of the last hour: one fully resolved, one logged on a device no interface carries, and one whose
// rid matches no rule.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, last_address, " +
    "first_seen_at, last_seen_at) VALUES (10, 'address_in_interface', '1/192.0.2.10', 1, '192.0.2.10', :at, :at)",
    { at: at });
  var flow = function (id, device, deviceState, iface, client, rid, ruleState) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rid, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, :device, :device_state, :iface, :client, '192.0.2.10', '203.0.113.7', " +
      "'tcp', 4, 'pass', 'in', 100, :rid, :rule_state, 'north_south')",
      { id: id, digest: "d" + id, at: at + id, device: device, device_state: deviceState, iface: iface,
        client: client, rid: rid, rule_state: ruleState });
  };
  flow(1, "vlan0.10", "resolved", 1, 10, null, "pending");
  flow(2, "vlan0.99", "not_found", null, null, null, "pending");
  flow(3, "vlan0.10", "resolved", 1, 10, "label-gone", "not_found");
}

test("each counter says how many failed their join, out of how many", function (t) {
  seed(t);
  var answer = t.widget("unresolved_joins", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var counters = answer.body.data.counters;
  t.equal(counters.map(function (counter) { return [counter.counter, counter.unresolved.value, counter.of.value]; }),
    [["interface", 1, 3], ["rule", 1, 3], ["client_identity", 1, 1], ["geo", 2, 2]], "the four counters");
  t.assert(counters.every(function (counter) { return !("records" in counter); }), "no record unless asked");
  t.golden("three_flows.json", answer.raw);
});

test("show_records lists the records behind each counter", function (t) {
  seed(t);
  var counters = t.widget("unresolved_joins", { period: "1h", show_records: true }).body.data.counters;
  t.equal(counters[0].records, [{ flow_id: 2, observed_at: AT - 598, interface_device: "vlan0.99", state: "not_found" }],
    "the record on an unknown device");
  t.equal(counters[1].records[0].rid, "label-gone", "the rid no rule carries");
  t.equal(counters[2].records, [{ state: "address_in_interface", client_id: 10, address: "192.0.2.10" }],
    "the client known by its address");
  t.equal(counters[3].records, [{ state: "no_row", address: "192.0.2.10" }, { state: "no_row", address: "203.0.113.7" }],
    "the addresses never looked up: both ends of the record on an unknown device are outside");
});

test("a counter with no record behind it lists none, and the counters keep the order asked", function (t) {
  seed(t);
  var counters = t.widget("unresolved_joins", { period: "1h", counters: ["rule", "interface"], show_records: true,
    interfaces: [{ kind: "interface", by: "identifier", value: "opt1" }] }).body.data.counters;
  t.equal(counters.map(function (counter) { return [counter.counter, counter.unresolved.value]; }),
    [["rule", 1], ["interface", 0]], "the flows of the interface alone, in the order asked");
  t.assert(!("records" in counters[1]), "an empty list is left out");
});

test("with no source probed and nothing stored, the answer is no data, never a zero", function (t) {
  t.clock(AT);
  var answer = t.widget("unresolved_joins");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
