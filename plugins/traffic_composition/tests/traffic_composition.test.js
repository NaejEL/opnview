// The traffic_composition widget's own tests, run by `opnview plugin test plugins/traffic_composition`
// against a database of their own. The byte identity of every recorded answer of the widget is the
// Go suite's (internal/widget/widgets, golden_test.go); these say what the widget does, in the
// widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// seed arranges one inside interface and two clients on it, inside the last hour: client 10 opens a
// tcp flow of 3000 bytes to port 443 and a udp one of 500 bytes to port 53; client 11 a tcp flow of
// 700 bytes to port 443.
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
    "(11, 'mac', '00:11:22:33:44:66', 1, '00:11:22:33:44:66', '192.0.2.11', :at, :at)", { at: at });
  var flow = function (id, client, address, protocol, port, bytes) {
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, dst_port, " +
      "protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, :client, :src, '203.0.113.7', :port, " +
      ":protocol, 4, 'pass', 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, client: client, src: address, port: port, protocol: protocol,
        bytes: bytes });
  };
  flow(1, 10, "192.0.2.10", "tcp", 443, 3000);
  flow(2, 10, "192.0.2.10", "udp", 53, 500);
  flow(3, 11, "192.0.2.11", "tcp", 443, 700);
}

test("by protocol, the slices are ranked by bytes and the Other slice carries the rest", function (t) {
  seed(t);
  var answer = t.widget("traffic_composition", { group_by: "protocol", slices: 1 });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.condition, "reachable", "the condition");
  var data = answer.body.data;
  t.equal(data.slices.map(function (slice) { return [slice.key, slice.protocol, slice.value]; }),
    [["protocol:tcp", "tcp", 3700]], "the largest protocol");
  t.equal([data.other.members, data.other.volume.bytes], [1, 500], "udp in Other");
  t.equal([data.whole, data.total.bytes], ["all_traffic", 4200], "the whole");
  t.equal(data.current_rates.by_member, [], "traffic/top carries no protocol");
  t.equal(data.current_rates.by_direction.map(function (entry) { return [entry.key, entry.rate.state]; }),
    [["outbound", "no_current_sample"], ["inbound", "no_current_sample"], ["inter_interface", "no_current_sample"]],
    "no sample is not a rate of 0");
  t.golden("by_protocol.json", answer.raw);
});

test("by client and by service port, each member is a slice of its own", function (t) {
  seed(t);
  var clients = t.widget("traffic_composition", { group_by: "client" }).body.data;
  t.equal(clients.whole, "traffic_with_an_inside_client", "the whole of a client grouping");
  t.equal(clients.slices.map(function (slice) { return [slice.key, slice.client.id, slice.volume.bytes]; }),
    [["client:10", 10, 3500], ["client:11", 11, 700]], "the clients");
  var ports = t.widget("traffic_composition", { group_by: "service_port" }).body.data;
  t.equal(ports.slices.map(function (slice) { return [slice.key, slice.port]; }),
    [["port:443", 443], ["port:53", 53]], "the ports");
  t.assert(!("client" in ports.slices[0]), "a port slice names no client");
});

test("by interface, the volume window's rows are grouped by their inside interface", function (t) {
  seed(t);
  var data = t.widget("traffic_composition", { group_by: "interface" }).body.data;
  t.equal(data.slices.map(function (slice) { return [slice.key, slice["interface"].identifier, slice.value]; }),
    [["interface:1", "opt1", 4200]], "one interface");
});
