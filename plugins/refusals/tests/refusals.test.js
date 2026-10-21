// blocked_decision projects every refusal once, under one vocabulary; the required plugins, loaded
// first, give it the flow table and the installation model it joins.
var NOW = 1790000400;

test("a blocked flow is one firewall refusal, with its reason named and its target placed", function (t) {
  t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES ('wan', 'igb0', 'WAN', 'ethernet', 'physical', :now, :now)", { now: NOW });
  var id = t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'wan'")[0].id;
  t.sql("INSERT INTO plugin_installation.interface_address (interface_id, source_field, address, prefix_length, " +
    "address_family, first_seen_at, last_seen_at) VALUES (:id, 'addr4', '203.0.113.7', 24, 4, :now, :now)",
    { id: id, now: NOW });
  t.sql("INSERT INTO plugin_pf.flow (log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_address, dst_address, protocol, ip_version, action, direction, log_reason, packet_bytes, rid, " +
    "rule_lookup_state, traffic_scope) VALUES " +
    "('d1', :now, :now, 'igb0', 'resolved', '198.51.100.9', '203.0.113.7', 'tcp', 4, 'block', 'in', 'match', 60, '42', " +
    "'not_found', 'north_south'), " +
    "('d2', :now, :now, 'igb0', 'resolved', '198.51.100.9', '203.0.113.8', 'tcp', 4, 'block', 'in', 'short', 60, NULL, " +
    "'not_found', 'north_south'), " +
    "('d3', :now, :now, 'igb0', 'resolved', '198.51.100.9', '203.0.113.9', 'tcp', 4, 'pass', 'in', 'match', 60, '7', " +
    "'not_found', 'north_south')", { now: NOW });
  var decisions = t.query("SELECT source_table, engine_kind, engine_reference, target, target_is_this_firewall " +
    "FROM plugin_refusals.blocked_decision ORDER BY target");
  t.equal(decisions, [
    { source_table: "flow", engine_kind: "firewall_rule", engine_reference: "42", target: "203.0.113.7", target_is_this_firewall: 1 },
    { source_table: "flow", engine_kind: "firewall_no_rule", engine_reference: null, target: "203.0.113.8", target_is_this_firewall: 0 }
  ], "two refusals, the passed record absent, the firewall's own address recognised");
});
