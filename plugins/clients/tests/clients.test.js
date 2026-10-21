// The classification and the pairing, as the derivation runs them: the required plugins give it the
// installation model and the flow table. The parity with the Go that preceded it is a test of the
// repository (internal/plugin/clientsparity_test.go); these state the rules on small cases.
var NOW = 1790000400;

function installation(t) {
  t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
    "is_upstream, first_seen_at, last_seen_at) VALUES " +
    "('lan', 'igb1', 'LAN', 'ethernet', 'physical', false, :then, :now), " +
    "('wan', 'igb0', 'WAN', 'ethernet', 'physical', true, :then, :now)", { then: NOW - 86400, now: NOW });
  var lan = t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'lan'")[0].id;
  var wan = t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'wan'")[0].id;
  t.sql("INSERT INTO plugin_installation.interface_address (interface_id, source_field, address, prefix_length, " +
    "address_family, first_seen_at, last_seen_at) VALUES " +
    "(:lan, 'addr4', '10.1.0.1', 24, 4, :then, :now), (:wan, 'addr4', '203.0.113.2', 24, 4, :then, :now), " +
    "(:wan, 'gateways', '203.0.113.1', NULL, 4, :then, :now)", { lan: lan, wan: wan, then: NOW - 86400, now: NOW });
  t.sql("INSERT INTO plugin_installation.interface_network (interface_id, network_address, prefix_length, " +
    "address_family, origin, first_detected_at, last_detected_at, updated_at) VALUES " +
    "(:lan, '10.1.0.0', 24, 4, 'detected', :then, :now, :now), (:wan, '203.0.113.0', 24, 4, 'detected', :then, :now, :now)",
    { lan: lan, wan: wan, then: NOW - 86400, now: NOW });
  return { lan: lan, wan: wan };
}

function flow(t, digest, device, direction, src, dst, extra) {
  var fields = { digest: digest, device: device, direction: direction, src: src, dst: dst, at: NOW - 60,
    src_port: null, dst_port: 443, ip_id: null, tcp_seq: null, action: "pass" };
  Object.keys(extra || {}).forEach(function (key) { fields[key] = extra[key]; });
  t.sql("INSERT INTO plugin_pf.flow (log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_address, dst_address, src_port, dst_port, protocol, ip_version, action, direction, packet_bytes, " +
    "rule_lookup_state, ip_id, tcp_seq, traffic_scope) VALUES (:digest, :at, :at, :device, 'resolved', :src, :dst, " +
    "CAST(:src_port AS BIGINT), CAST(:dst_port AS BIGINT), 'tcp', 4, :action, :direction, 60, 'not_found', " +
    "CAST(:ip_id AS BIGINT), CAST(:tcp_seq AS BIGINT), 'north_south')", fields);
}

test("an address inside a detected network is placed on its interface and given an address-level client; an outside one is not",
  function (t) {
    t.clock(NOW);
    var ids = installation(t);
    flow(t, "f1", "igb1", "in", "10.1.0.42", "198.51.100.9");
    var run = t.derive("place");
    t.equal(run.error, null, "the run");
    var rows = t.query("SELECT f.src_interface_id, f.dst_interface_id, f.traffic_scope, c.identity_kind, c.identity_key " +
      "FROM plugin_pf.flow f LEFT JOIN plugin_installation.client c ON c.id = f.src_client_id WHERE f.log_digest = 'f1'");
    t.equal(rows[0].src_interface_id, ids.lan, "the source is on the LAN");
    t.equal(rows[0].dst_interface_id, null, "the destination is outside");
    t.equal(rows[0].traffic_scope, "north_south", "the scope");
    t.equal(rows[0].identity_kind, "address_in_interface", "an address-level client was minted");
    t.equal(rows[0].identity_key, ids.lan + "|10.1.0.42|" + ((Math.floor(NOW / 86400)) * 86400), "its key");
  });

test("the firewall's own address is this firewall, never a client, and a client's connection is paired with its NATed second leg",
  function (t) {
    t.clock(NOW);
    installation(t);
    flow(t, "first", "igb1", "in", "10.1.0.42", "198.51.100.9", { src_port: 41000, ip_id: 4001, tcp_seq: 11 });
    flow(t, "second", "igb0", "out", "203.0.113.2", "198.51.100.9", { src_port: 52000, ip_id: 4001, tcp_seq: 11 });
    flow(t, "alone", "igb0", "out", "203.0.113.2", "198.51.100.10", { src_port: 52001, ip_id: 4002, tcp_seq: 12 });
    t.derive("place");
    var rows = t.query("SELECT log_digest, src_is_this_firewall, traffic_scope, pair_outcome, " +
      "(SELECT p.log_digest FROM plugin_pf.flow p WHERE p.id = f.paired_flow_id) AS partner, src_client_id " +
      "FROM plugin_pf.flow f ORDER BY log_digest");
    t.equal(rows, [
      { log_digest: "alone", src_is_this_firewall: true, traffic_scope: "this_firewall", pair_outcome: "not_paired", partner: null, src_client_id: null },
      { log_digest: "first", src_is_this_firewall: false, traffic_scope: "north_south", pair_outcome: "first_leg", partner: "second", src_client_id: rows[1].src_client_id },
      { log_digest: "second", src_is_this_firewall: true, traffic_scope: "this_firewall", pair_outcome: "second_leg", partner: "first", src_client_id: null }
    ], "the pair, and the unpaired NATed record");
    t.assert(rows[1].src_client_id !== null, "the first leg has its client");
  });

test("a run over unchanged evidence changes nothing", function (t) {
  t.clock(NOW);
  installation(t);
  flow(t, "f1", "igb1", "in", "10.1.0.42", "198.51.100.9");
  t.derive("place");
  t.clock(NOW + 1);
  t.derive("place");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_pf.flow WHERE changed_at = :at", { at: NOW + 1 })[0].n, 0,
    "no flow changed on the second run");
});
