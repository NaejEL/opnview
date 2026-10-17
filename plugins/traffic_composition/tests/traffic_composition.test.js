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
  t.equal(data.current_rates.by_member, [], "traffic/top carries no protocol, and this world has no pf_states");
  t.equal([data.current_rates.by_member_source.dataset, data.current_rates.by_member_source.state,
    data.current_rates.by_member_source.at], ["pf_states.rate", "unknown_dataset", null],
    "the named absence: the dataset the rate by protocol is read from is not installed");
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
  t.equal(ports.slices.map(function (slice) { return [slice.key, slice.port, slice.protocol]; }),
    [["port:443/tcp", 443, "tcp"], ["port:53/udp", 53, "udp"]], "the services, each a port and a protocol");
  t.assert(!("client" in ports.slices[0]), "a port slice names no client");
});

test("53/udp and 53/tcp are two services, and a record with no port is port:not_recorded", function (t) {
  seed(t);
  t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
    "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, dst_port, " +
    "protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
    "(4, 'd4', :at, :at, 'vlan0.10', 'resolved', 1, 11, '192.0.2.11', '203.0.113.7', 53, 'tcp', 4, " +
    "'pass', 'in', 90, 'pending', 'north_south'), " +
    "(5, 'd5', :at, :at, 'vlan0.10', 'resolved', 1, 11, '192.0.2.11', '203.0.113.7', NULL, 'icmp', 4, " +
    "'pass', 'in', 60, 'pending', 'north_south')", { at: AT - 300 });
  var data = t.widget("traffic_composition", { group_by: "service_port", slices: 10 }).body.data;
  t.equal(data.slices.map(function (slice) { return [slice.key, slice.volume.bytes]; }),
    [["port:443/tcp", 3700], ["port:53/udp", 500], ["port:53/tcp", 90], ["port:not_recorded", 60]],
    "the services");
  t.assert(!("port" in data.slices[3]) && !("protocol" in data.slices[3]), "port:not_recorded names neither");
});

test("by blocking reason, every logged record by its decision, the refusals no packet carries beside",
  function (t) {
    seed(t);
    t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, dst_port, " +
      "protocol, ip_version, action, direction, log_reason, packet_bytes, rule_lookup_state, traffic_scope) " +
      "VALUES (4, 'd4', :at, :at, 'vlan0.10', 'resolved', 1, 11, '192.0.2.11', '203.0.113.7', 22, 'tcp', 4, " +
      "'block', 'in', 'match', 40, 'pending', 'north_south'), " +
      "(5, 'd5', :at, :at, 'vlan0.10', 'resolved', 1, 11, '192.0.2.11', '203.0.113.7', 22, 'tcp', 4, " +
      "'block', 'in', 'state-mismatch', 20, 'pending', 'north_south')", { at: AT - 300 });
    var data = t.widget("traffic_composition", { group_by: "blocking_reason", measure: "connections" }).body.data;
    t.equal(data.slices.map(function (slice) { return [slice.key, slice.decision, slice.value]; }),
      [["decision:allowed", "allowed", 3], ["decision:firewall_no_rule", "firewall_no_rule", 1],
        ["decision:firewall_rule", "firewall_rule", 1]], "the decisions");
    t.equal(data.aside.map(function (entry) { return [entry.key, entry.count.value]; }),
      [["dns_list", 0], ["security_engine", 0]], "the refusals beside the rings");
  });

test("split_by = direction draws one ring per direction beside the whole", function (t) {
  seed(t);
  var data = t.widget("traffic_composition", { group_by: "protocol", split_by: "direction" }).body.data;
  t.equal(data.rings.map(function (ring) { return [ring.direction, ring.total.bytes]; }),
    [["inbound", 0], ["outbound", 4200], ["inter_interface", 0], ["to_this_firewall", 0],
      ["from_this_firewall", 0]], "the rings");
  t.equal(data.rings[1].slices.map(function (slice) { return slice.key; }), ["protocol:tcp", "protocol:udp"],
    "the outbound ring's slices");
  var whole = t.widget("traffic_composition", { group_by: "protocol" }).body.data;
  t.assert(!("rings" in whole), "no ring unless asked");
});

test("window_seconds adds the recent part, a gap after complete_until and never a zero", function (t) {
  seed(t);
  var none = t.widget("traffic_composition", { group_by: "protocol" }).body.data;
  t.assert(!("recent" in none), "no recent part unless asked");
  t.sql("INSERT INTO flow (id, log_digest, observed_at, ingested_at, interface_device, " +
    "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, dst_port, " +
    "protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
    "(4, 'd4', :at, :at, 'vlan0.10', 'resolved', 1, 11, '192.0.2.11', '203.0.113.7', 443, 'tcp', 4, " +
    "'pass', 'in', 80, 'pending', 'north_south')", { at: AT - 50 });
  // The filter log's last pass ended healthy 30 s before now: the last 30 s are not complete.
  t.sql("UPDATE provider SET is_active = true WHERE kind = 'firewall_log'", {});
  t.sql("UPDATE source_availability SET outcome = 'healthy', probe = 'records_read', checked_at = :at " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')", { at: AT - 30 });
  var recent = t.widget("traffic_composition", { group_by: "protocol", window_seconds: 120 }).body.data.recent;
  t.equal([recent.seconds, recent.bucket_seconds, recent.complete_until], [120, 2, AT - 30], "the span");
  var tcp = recent.series[0];
  t.equal(tcp.key, "protocol:tcp", "the first series is the first slice");
  var withRecord = tcp.buckets.filter(function (bucket) { return bucket.value !== null && bucket.value > 0; });
  t.equal(withRecord.map(function (bucket) { return [bucket.from, bucket.value]; }), [[AT - 50, 80]],
    "the record in its bucket");
  var after = tcp.buckets.filter(function (bucket) { return bucket.to > AT - 30; });
  t.assert(after.length > 0 && after.every(function (bucket) { return bucket.gap && bucket.value === null; }),
    "every bucket after complete_until is a gap, never a zero");
  var before = tcp.buckets.filter(function (bucket) { return bucket.to <= AT - 30 && bucket.from !== AT - 50; });
  t.assert(before.every(function (bucket) { return !bucket.gap && bucket.value === 0; }),
    "a complete bucket with no record reads 0");
});

test("by interface, the volume window's rows are grouped by their inside interface", function (t) {
  seed(t);
  var data = t.widget("traffic_composition", { group_by: "interface" }).body.data;
  t.equal(data.slices.map(function (slice) { return [slice.key, slice["interface"].identifier, slice.value]; }),
    [["interface:1", "opt1", 4200]], "one interface");
});

test("Other names the members it folds, each the slice it would have been, and they make its volume",
  function (t) {
    seed(t);
    var data = t.widget("traffic_composition", { group_by: "client", slices: 1, split_by: "direction" }).body.data;
    t.equal(data.slices.map(function (slice) { return slice.key; }), ["client:10"], "one slice shown");
    t.equal(data.other.slices.map(function (slice) { return [slice.key, slice.client.id, slice.value]; }),
      [["client:11", 11, 700]], "the folded client, as a slice");
    t.equal([data.other.members, data.other.volume.bytes], [1, data.other.slices[0].volume.bytes],
      "the folded slices make Other");
    data.rings.forEach(function (ring) {
      t.equal(ring.other.slices.length, ring.other.members, "the " + ring.direction + " ring's Other names its members");
    });
    var all = t.widget("traffic_composition", { group_by: "client" }).body.data;
    t.equal([all.other.members, all.other.slices], [0, []], "nothing folded, nothing named");
  });
