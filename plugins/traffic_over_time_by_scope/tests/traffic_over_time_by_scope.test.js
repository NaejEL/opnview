// The traffic_over_time_by_scope widget's own tests, run by
// `opnview plugin test plugins/traffic_over_time_by_scope` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// seed arranges one inside interface and one client on it: the client opens two outbound flows of
// 3000 and 1000 bytes, and one blocked flow of 200 bytes, ten minutes before AT.
function seed(t) {
  t.clock(AT);
  reachable(t);
  var at = AT - 600;
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO plugin_installation.client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', :at, :at)", { at: at });
  var flow = function (id, destination, action, bytes) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, 10, '192.0.2.10', :dst, 'tcp', 4, " +
      ":action, 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, dst: destination, action: action, bytes: bytes });
  };
  flow(1, "203.0.113.7", "pass", 3000);
  flow(2, "203.0.113.8", "pass", 1000);
  flow(3, "203.0.113.9", "block", 200);
}

test("one series per scope, the outbound one holding the flows and its totals", function (t) {
  seed(t);
  var answer = t.widget("traffic_over_time_by_scope", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.bucket_seconds, 60, "one-minute buckets for one hour");
  t.equal(data.series.map(function (line) { return line.key; }), ["outbound", "inbound", "inter_interface"],
    "the scopes, in the order selected");
  var outbound = data.series[0];
  t.equal([outbound.condition, outbound.total, outbound.total_volume.connections], ["reachable", 4200, 3],
    "the outbound series holds every byte and connection");
  t.equal([data.series[1].condition, data.series[1].total], ["reachable_no_rows", 0], "no inbound flow");
  t.golden("one_hour.json", answer.raw);
});

test("measure = connections counts connections, and scopes selects the series", function (t) {
  seed(t);
  var data = t.widget("traffic_over_time_by_scope",
    { period: "1h", measure: "connections", scopes: ["outbound"] }).body.data;
  t.equal(data.series.length, 1, "one series");
  t.equal([data.series[0].unit, data.series[0].total], ["connections", 3], "three connections");
});

test("an installation with no flow draws every bucket as a gap, never a zero", function (t) {
  t.clock(AT);
  reachable(t);
  var answer = t.widget("traffic_over_time_by_scope", { period: "1h" });
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  var buckets = answer.body.data.series[0].buckets;
  t.assert(buckets.every(function (bucket) { return bucket.gap && bucket.value === null; }), "every bucket a gap");
});
