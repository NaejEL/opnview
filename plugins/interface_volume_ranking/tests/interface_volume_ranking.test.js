// The interface_volume_ranking widget's own tests, run by
// `opnview plugin test plugins/interface_volume_ranking` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges two inside interfaces -- the first labelled by the user, the second not -- a
// client on each, a rule naming the first whose logging is off, and outbound records of the last
// hour: 3000 bytes from the first interface's client, 500 from the second's.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, user_label, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(1, 'opt1', 'vlan0.10', 'First', 'Workshop', 'ethernet', 'vlan', :at, :at), " +
    "(2, 'opt2', 'vlan0.20', 'Second', NULL, 'ethernet', 'vlan', :at, :at)", { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', :at, :at), " +
    "(20, 'mac', '00:11:22:33:44:66', 2, '00:11:22:33:44:66', '198.51.100.20', :at, :at)", { at: at });
  t.sql("INSERT INTO rule (id, pf_label, description, action, interface, legacy, logs_matches, discovered_at) " +
    "VALUES (5, 'label-five', 'Quiet pass', 'pass', 'opt1', 0, 0, :at)", { at: at });
  var flow = function (id, iface, device, client, address, bytes) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, :device, 'resolved', :iface, :client, :src, '203.0.113.7', 'tcp', 4, " +
      "'pass', 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, device: device, iface: iface, client: client, src: address,
        bytes: bytes });
  };
  flow(1, 1, "vlan0.10", 10, "192.0.2.10", 3000);
  flow(2, 2, "vlan0.20", 20, "198.51.100.20", 500);
}

test("interfaces are ranked by their logged bytes, each with its clients and its rules", function (t) {
  seed(t);
  var answer = t.widget("interface_volume_ranking", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.bars.map(function (bar) { return [bar["interface"].identifier, bar.value]; }),
    [["opt1", 3000], ["opt2", 500]], "the most bytes first");
  t.equal([data.bars[0].north_south.bytes, data.bars[0].east_west.bytes], [3000, 0], "the parts of the bar");
  t.equal([data.bars[0].clients_seen, data.bars[0].rules_not_logging], [1, 1], "its client and its quiet rule");
  t.equal(data.total.bytes, 3500, "the total");
  t.golden("two_interfaces.json", answer.raw);
});

test("limit and include_unlabelled say what they left out", function (t) {
  seed(t);
  var limited = t.widget("interface_volume_ranking", { period: "1h", limit: 1 }).body.data;
  t.equal([limited.bars.length, limited.remainder.members, limited.remainder.volume.bytes], [1, 1, 500],
    "one bar and the rest");
  var labelled = t.widget("interface_volume_ranking", { period: "1h", include_unlabelled: false }).body.data;
  t.equal(labelled.bars.map(function (bar) { return bar["interface"].identifier; }), ["opt1"], "the labelled one");
  t.equal([labelled.excluded_unlabelled.members, labelled.excluded_unlabelled.volume.bytes], [1, 500],
    "the unlabelled one, counted");
  var all = t.widget("interface_volume_ranking", { period: "1h" }).body.data;
  t.assert(!("excluded_unlabelled" in all), "nothing excluded, nothing said");
});

test("measure = clients ranks by the clients seen", function (t) {
  seed(t);
  var data = t.widget("interface_volume_ranking", { period: "1h", measure: "clients" }).body.data;
  t.equal(data.bars.map(function (bar) { return bar.value; }), [1, 1], "one client each, ranked by key");
});

test("with no source probed and nothing stored, the answer is no data, never an empty bar", function (t) {
  t.clock(AT);
  var answer = t.widget("interface_volume_ranking");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
