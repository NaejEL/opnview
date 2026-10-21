// The client_traffic_detail widget's own tests, run by
// `opnview plugin test plugins/client_traffic_detail` against a database of their own. The byte
// identity of every recorded answer of the widget is the Go suite's (internal/widget/widgets,
// golden_test.go); these say what the widget does, in the widget's own words.

// AT is the instant the tests answer at: 2026-09-21 13:20:00 UTC.
var AT = 1790000400;

// reachable marks the filter log's providers reachable, as a probe round would.
function reachable(t) {
  t.sql("UPDATE source_availability SET state = 'reachable' " +
    "WHERE provider_id IN (SELECT id FROM provider WHERE kind = 'firewall_log')");
}

// CLIENT is the reference to the client the tests read.
var CLIENT = { kind: "client", by: "mac", value: "00:11:22:33:44:55" };

// seed arranges one inside interface and one client on it, inside the last hour: three flows, the
// first passed (1000 bytes) and named example.org by lookup timing two seconds before, the second
// passed (400 bytes) and unnamed, the third blocked (300 bytes).
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
  t.sql("INSERT INTO dns_resolution (id, lookup_key, client_address, domain, resolver, action, " +
    "looked_up_at, ingested_at) VALUES (1, 'k1', '192.0.2.10', 'example.org', 'unbound', 'pass', :at, :at)",
    { at: at });
  var flow = function (id, action, port, bytes) {
    t.sql("INSERT INTO plugin_pf.flow (id, log_digest, observed_at, ingested_at, interface_device, " +
      "interface_lookup_state, src_interface_id, src_client_id, src_address, dst_address, dst_port, " +
      "protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, traffic_scope) VALUES " +
      "(:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, 10, '192.0.2.10', '203.0.113.7', :port, " +
      "'tcp', 4, :action, 'in', :bytes, 'pending', 'north_south')",
      { id: id, digest: "d" + id, at: at + id, port: port, action: action, bytes: bytes });
  };
  flow(1, "pass", 443, 1000);
  flow(2, "pass", 80, 400);
  flow(3, "block", 22, 300);
  t.sql("INSERT INTO domain_attribution (flow_id, dns_resolution_id, site_name, correlation_delay_seconds, " +
    "attributed_at, method) VALUES (1, 1, 'example.org', 2, :at, 'lookup_timing')", { at: at + 60 });
}

test("the client's records come newest first, a named one with its site, its method and its delay",
  function (t) {
    seed(t);
    var answer = t.widget("client_traffic_detail", { client: CLIENT });
    t.equal(answer.status, 200, "the status");
    t.equal(answer.condition, "reachable", "the condition");
    var data = answer.body.data;
    t.equal(data.client.id, 10, "the client");
    t.equal(data.records.map(function (record) { return record.flow_id; }), [3, 2, 1], "newest first");
    var named = data.records[2];
    t.equal([named.site_name, named.method, named.correlation_delay_seconds], ["example.org", "lookup_timing", 2],
      "the named record");
    t.equal([data.records[1].site_name, data.records[1].geo.lookup_state], [null, "no_row"],
      "an unnamed record is a row of its own, its address never looked up");
    t.equal(data.current_rate.state, "no_current_sample", "no sample is not a rate of 0");
    t.equal(data.leases, [], "no lease");
    t.equal([data.total.members, data.total.count.value], [3, 1700], "the whole");
    t.equal([data.remainder.members, data.remainder.count.value], [0, 0], "nothing dropped");
    t.golden("three_records.json", answer.raw);
  });

test("action and limit filter the records, and the remainder carries what limit dropped", function (t) {
  seed(t);
  var allowed = t.widget("client_traffic_detail", { client: CLIENT, action: "allowed" }).body.data;
  t.equal(allowed.records.map(function (record) { return record.flow_id; }), [2, 1], "the passed records");
  var limited = t.widget("client_traffic_detail", { client: CLIENT, limit: 1 }).body.data;
  t.equal(limited.records.length, 1, "one record");
  t.equal([limited.remainder.members, limited.remainder.count.value], [2, 1400], "the two older records");
});

// sample stores one baseline reading of the measurement_sample kind, as the insight plugin does: its
// subject key is the device and the addresses joined by spaces, local end first, and the parts the
// core reads -- the device, the local address and the peer -- are reported beside it.
function sample(t, id, kind, key, measure, value, at) {
  var parts = key.split(" ");
  t.sql("INSERT INTO measurement_sample (id, subject_kind, subject_key, interface_device, local_address, " +
    "peer_address, measure, unit, value, sampled_at) " +
    "VALUES (:id, :kind, :key, :device, :local, :peer, :measure, 'bit_per_second', :value, :at)",
    { id: id, kind: kind, key: key, device: parts[0], local: parts[1], peer: parts.length > 2 ? parts[2] : null,
      measure: measure, value: value, at: at });
}

test("the client's current rate per peer, keyed by the address its records name", function (t) {
  seed(t);
  t.sql("INSERT INTO plugin_installation.interface_network (id, interface_id, network_address, prefix_length, address_family, " +
    "origin, updated_at) VALUES (1, 1, '192.0.2.0', 24, 4, 'operator', :at)", { at: AT - 600 });
  var at = AT - 30;
  // The client was read with two peers: their per-pair inbound figures, and no outbound one --
  // traffic/top reports a local address's sending only as its total over every peer.
  sample(t, 1, "interface_endpoint", "vlan0.10 192.0.2.10", "rate_bits_in", 5000, at);
  sample(t, 2, "interface_endpoint", "vlan0.10 192.0.2.10", "rate_bits_out", 800, at);
  sample(t, 3, "interface_endpoint_pair", "vlan0.10 192.0.2.10 203.0.113.7", "rate_bits_in", 4000, at);
  sample(t, 4, "interface_endpoint_pair", "vlan0.10 192.0.2.10 198.51.100.20", "rate_bits_in", 1000, at);
  var data = t.widget("client_traffic_detail", { client: CLIENT }).body.data;
  t.equal(data.current_rate_by_peer.map(function (entry) {
    return [entry.peer, entry.rate.state, entry.rate.in_bits_per_second, entry.rate.out_bits_per_second,
      entry.rate.out_measured];
  }), [["198.51.100.20", "current", 1000, null, false], ["203.0.113.7", "current", 4000, null, false]],
  "each peer's rate, its sending not split per peer");
  t.equal(data.records[0].destination, "203.0.113.7", "a record names the peer its rate is keyed by");
  t.equal(data.current_rate.in_bits_per_second, 5000, "the client's own rate is unchanged");
});

test("a client with no current per-pair reading has no peer rate, not a rate of 0", function (t) {
  seed(t);
  t.equal(t.widget("client_traffic_detail", { client: CLIENT }).body.data.current_rate_by_peer, [], "no peer");
});
