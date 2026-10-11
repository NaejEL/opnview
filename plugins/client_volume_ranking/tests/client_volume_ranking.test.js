// The client_volume_ranking widget's own tests, run by
// `opnview plugin test plugins/client_volume_ranking` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// seed arranges one inside interface and three clients on it, all inside the last hour: client 10
// (a stable MAC) opens two outbound flows, 3000 and 1000 bytes, to two destinations; client 11 (a
// randomised MAC) one of 700 bytes; client 12 one blocked flow of 200 bytes.
function seed(t) {
  t.clock(AT);
  reachable(t);
  var at = AT - 600;
  t.sql("INSERT INTO interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :at, :at)",
    { at: at });
  t.sql("INSERT INTO client (id, identity_kind, identity_key, interface_id, mac, last_address, " +
    "first_seen_at, last_seen_at) VALUES " +
    "(10, 'mac', '00:11:22:33:44:55', 1, '00:11:22:33:44:55', '192.0.2.10', :at, :at), " +
    "(11, 'mac', '02:11:22:33:44:66', 1, '02:11:22:33:44:66', '192.0.2.11', :at, :at), " +
    "(12, 'mac', '00:11:22:33:44:77', 1, '00:11:22:33:44:77', '192.0.2.12', :at, :at)", { at: at });
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
  flow(2, 10, "192.0.2.10", "203.0.113.8", "pass", 1000);
  flow(3, 11, "192.0.2.11", "203.0.113.7", "pass", 700);
  flow(4, 12, "192.0.2.12", "203.0.113.9", "block", 200);
}

test("clients are ranked by their logged bytes, with their interface and destinations", function (t) {
  seed(t);
  var answer = t.widget("client_volume_ranking");
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.bars.map(function (bar) { return bar.client.id; }), [10, 11, 12], "the most bytes first");
  t.equal([data.bars[0].value, data.bars[0].destinations], [4000, 2], "client 10's bytes and destinations");
  t.equal(data.bars[0]["interface"].identifier, "opt1", "the interface the client is on");
  t.equal(data.total.bytes, 4900, "the total");
  t.equal(data.remainder, { members: 0, volume: data.remainder.volume }, "nothing dropped");
  t.equal(data.remainder.volume.bytes, 0, "the remainder holds no byte");
  t.golden("three_clients.json", answer.raw);
});

test("limit drops the rest into the remainder, and the unstable identities can be left out", function (t) {
  seed(t);
  var limited = t.widget("client_volume_ranking", { limit: 1 }).body.data;
  t.equal(limited.bars.length, 1, "one bar");
  t.equal([limited.remainder.members, limited.remainder.volume.bytes], [2, 900], "clients 11 and 12 dropped");
  var stable = t.widget("client_volume_ranking", { include_unstable: false }).body.data;
  t.equal(stable.bars.map(function (bar) { return bar.client.id; }), [10, 12], "the randomised MAC left out");
  t.equal([stable.excluded.members, stable.excluded.volume.bytes], [1, 700], "and counted as excluded");
  t.equal(stable.total.bytes, 4900, "the total still holds it");
});

test("measure = destinations ranks by distinct far ends, measure = connections by connections", function (t) {
  seed(t);
  var destinations = t.widget("client_volume_ranking", { measure: "destinations" }).body.data;
  t.equal(destinations.bars.map(function (bar) { return [bar.client.id, bar.value]; }),
    [[10, 2], [11, 1], [12, 1]], "a tie ranked by key");
  var connections = t.widget("client_volume_ranking", { measure: "connections" }).body.data;
  t.equal(connections.bars.map(function (bar) { return bar.value; }), [2, 1, 1], "connections");
});

test("an installation with no flow ranks nothing", function (t) {
  t.clock(AT);
  reachable(t);
  var answer = t.widget("client_volume_ranking");
  t.equal(answer.condition, "reachable_no_rows", "the condition");
  t.equal(answer.body.data.bars, [], "no bar");
});
