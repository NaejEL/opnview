// The destination_operators widget's own tests, run by
// `opnview plugin test plugins/destination_operators` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// seed arranges two inside interfaces, a rule, and four flows ten minutes before AT: interface 1
// sends 3000 bytes to 203.0.113.7 (placed in FR, AS 64500) and 1000 bytes to 203.0.113.8 (a
// geolocation miss), both allowed by rule 1; interface 2 is refused 200 bytes to 203.0.113.7 by a
// label no rule carries; interface 1 sends 500 bytes to interface 2.
function seed(t) {
  t.clock(AT);
  reachable(t);
  var at = AT - 600;
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, first_seen_at, " +
    "last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'First inside', 'ethernet', 'vlan', :at, :at), " +
    "(2, 'opt2', 'vlan0.20', 'Second inside', 'ethernet', 'vlan', :at, :at)", { at: at });
  t.sql("INSERT INTO plugin_installation.rule (id, pf_label, description, action, discovered_at) " +
    "VALUES (1, 'example-pass', 'Example pass rule', 'pass', :at)", { at: at });
  // The dataset that placed the addresses is the geolocation provider the installation registered.
  var provider = "(SELECT id FROM provider WHERE kind = 'ip_geolocation' ORDER BY id LIMIT 1)";
  t.sql("INSERT INTO geo_asn (address, provider_id, lookup_state, country_code, country_name, latitude, " +
    "longitude, asn, operator, dataset_build_at, looked_up_at) VALUES " +
    "('203.0.113.7', " + provider + ", 'resolved', 'FR', 'France', 48.85, 2.35, 64500, " +
    "'Example Operator', :build, :at), " +
    "('203.0.113.8', " + provider + ", 'miss', NULL, NULL, NULL, NULL, NULL, NULL, :build, :at)",
    { build: AT - 86400, at: at });
  var flow = function (id, src, dst, destination, action, bytes, rid, rule) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, dst_interface_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, log_reason, packet_bytes, rid, rule_id, rule_lookup_state, " +
      "traffic_scope) VALUES (:id, :digest, :at, :at, 'vlan0.10', 'resolved', :src, :dst, '192.0.2.10', " +
      ":destination, 'tcp', 4, :action, 'in', 'match', :bytes, :rid, :rule, :state, :scope)",
      { id: id, digest: "d" + id, at: at + id, src: src, dst: dst, destination: destination, action: action,
        bytes: bytes, rid: rid, rule: rule, state: rule === null ? "not_found" : "resolved",
        scope: dst === null ? "north_south" : "east_west" });
  };
  flow(1, 1, null, "203.0.113.7", "pass", 3000, "example-pass", 1);
  flow(2, 1, null, "203.0.113.8", "pass", 1000, "example-pass", 1);
  flow(3, 2, null, "203.0.113.7", "block", 200, "example-unknown", null);
  flow(4, 1, 2, "192.0.2.20", "pass", 500, "example-pass", 1);
}

test("the outside peers are grouped by AS number, with the operator beside it", function (t) {
  seed(t);
  var answer = t.widget("destination_operators");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.group, "asn", "the grouping");
  t.equal(data.rows.map(function (row) { return [row.key, row.asn, row.operator, row.bytes.value]; }),
    [["asn:64500", 64500, "Example Operator", 3200]], "one autonomous system");
  t.equal(data.unresolved[0].addresses, 1, "the miss, counted");
  t.golden("one_operator.json", answer.raw);
});

test("group = operator keys the rows by the operator's name and carries no AS number", function (t) {
  seed(t);
  var row = t.widget("destination_operators", { group: "operator" }).body.data.rows[0];
  t.equal(row.key, "operator:Example Operator", "the key");
  t.assert(!("asn" in row), "no AS number");
});
