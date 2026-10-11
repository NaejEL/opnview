// The connection_tree widget's own tests, run by `opnview plugin test plugins/connection_tree` against
// a database of their own. The byte identity of every recorded answer of the widget is the Go
// suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// seed arranges one inside interface, two clients on it, and three outbound records of the last
// hour: client 10 passes 3000 bytes to a geolocated address and 500 to one never looked up; client
// 11 is blocked sending 200 bytes to the geolocated one.
function seed(t) {
  t.clock(AT);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', :at, :at), " +
    "(11, 'mac', '00:11:22:33:44:66', 1, '00:11:22:33:44:66', '192.0.2.11', :at, :at)", { at: at });
  t.sql("INSERT INTO geo_asn (address, lookup_state, asn, operator, looked_up_at) " +
    "VALUES ('203.0.113.7', 'miss', NULL, NULL, :at)", { at: at });
  var flow = function (id, client, address, destination, action, bytes) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, protocol, " +
      "ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, :client, :src, :dst, 'tcp', 4, " +
      ":action, 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, client: client, src: address, dst: destination,
        action: action, bytes: bytes });
  };
  flow(1, 10, "192.0.2.10", "203.0.113.7", "pass", 3000);
  flow(2, 10, "192.0.2.10", "203.0.113.8", "pass", 500);
  flow(3, 11, "192.0.2.11", "203.0.113.7", "block", 200);
}

// sum adds the bytes of a level.
function sum(nodes) {
  return nodes.reduce(function (total, node) { return total + node.bytes.value; }, 0);
}

test("both sides sum to the same total, and each node's children sum to the node", function (t) {
  seed(t);
  var answer = t.widget("connection_tree", { period: "1h" });
  t.equal(answer.status, 200, "the status");
  var data = answer.body.data;
  t.equal(data.total.bytes.value, 3700, "the total");
  t.equal([sum(data.inside), sum(data.outside)], [3700, 3700], "both roots");
  t.equal(data.inside.map(function (node) { return node.key; }), ["interface:1"], "the interface root");
  t.equal(data.inside[0]["interface"].identifier, "opt1", "named by the interface");
  t.equal(data.inside[0].children.map(function (node) { return [node.key, node.bytes.value]; }),
    [["client:10", 3500], ["client:11", 200]], "its clients, the most bytes first");
  t.equal(data.outside.map(function (node) { return [node.key, node.bytes.value]; }),
    [["unplaced:miss", 3200], ["unplaced:no_row", 500]], "the destinations' conditions");
  data.outside.forEach(function (node) {
    t.equal(sum(node.children), node.bytes.value, node.key + " sums to its children");
  });
  t.golden("three_flows.json", answer.raw);
});

test("include_blocked = false leaves blocked connections out whole, and depth = 1 keeps one level",
  function (t) {
    seed(t);
    var passed = t.widget("connection_tree", { period: "1h", include_blocked: false }).body.data;
    t.equal([passed.total.bytes.value, passed.total.blocked_connections.value], [3500, 0], "no blocked bytes");
    var shallow = t.widget("connection_tree", { period: "1h", depth: 1, root: "client" }).body.data;
    t.equal(shallow.inside.map(function (node) { return node.key; }), ["client:10", "client:11"], "the clients");
    t.assert(shallow.inside.every(function (node) { return !("children" in node); }), "no second level");
  });

test("limit_per_level folds the rest into a remainder node that keeps the level whole", function (t) {
  seed(t);
  var data = t.widget("connection_tree", { period: "1h", limit_per_level: 1 }).body.data;
  var clients = data.inside[0].children;
  t.equal(clients.map(function (node) { return node.key; }), ["client:10", "remainder"], "one client and the rest");
  t.equal([clients[1].remainder_members, clients[1].bytes.value], [1, 200], "the remainder");
});

test("with no source probed and nothing stored, the answer is no data, never an empty tree", function (t) {
  t.clock(AT);
  var answer = t.widget("connection_tree");
  t.equal([answer.condition, answer.body.data], ["not_yet_probed", null], "the condition, and no data");
});
