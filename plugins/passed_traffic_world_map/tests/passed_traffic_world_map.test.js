// The passed_traffic_world_map widget's own tests, run by
// `opnview plugin test plugins/passed_traffic_world_map` against a database of their own. The byte
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

test("an allowed peer placed by the geolocation is a point, and a miss is counted unplaced", function (t) {
  seed(t);
  var answer = t.widget("passed_traffic_world_map");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.points.map(function (point) { return [point.address, point.size, point.interfaces]; }),
    [["203.0.113.7", 3000, [1, 2]]], "one point, sized by its allowed bytes, reached from both interfaces");
  t.equal(data.points[0].geo.country_code, "FR", "where it was placed");
  var miss = data.unplaced.filter(function (bin) { return bin.reason === "miss"; })[0];
  t.equal([miss.addresses, miss.volume.bytes], [1, 1000], "the miss, counted");
  t.equal(data.unplaced.map(function (bin) { return bin.reason; }),
    ["miss", "pending", "no_row", "no_coordinates"], "every bin present, even at zero");
  t.equal(data.total.bytes, 4000, "placed plus unplaced is the allowed total of the scope");
  t.golden("placed_and_missed.json", answer.raw);
});

test("a point under min_bytes is left off the map and counted below the minimum", function (t) {
  seed(t);
  var data = t.widget("passed_traffic_world_map", { min_bytes: 5000 }).body.data;
  t.equal(data.points, [], "no point");
  t.equal([data.below_minimum.members, data.below_minimum.volume.bytes], [1, 3000], "below the minimum");
  t.equal(data.total.bytes, 4000, "the total still holds it");
});

test("scope = all counts inter-interface traffic unplaced, as east-west", function (t) {
  seed(t);
  var data = t.widget("passed_traffic_world_map", { scope: "all" }).body.data;
  var eastWest = data.unplaced.filter(function (bin) { return bin.reason === "east_west"; })[0];
  t.equal([eastWest.addresses, eastWest.volume.bytes], [0, 500], "the inter-interface bytes");
  t.equal(data.total.bytes, 4500, "the total of every scope");
});
