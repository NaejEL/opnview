// The installation model: its tables are declared, its views are relations, and what the Go that
// still writes them relies on holds -- the generated columns, the holdings view, the uniqueness.
var NOW = 1790000400;

test("an interface's tunnel flag is generated from its link kind, and its identifier is unique", function (t) {
  t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES ('opt1', 'vlan01', 'IoT', 'vlan', 'vlan', :now, :now), " +
    "('wg0', 'wg0', 'Road warriors', 'wireguard', 'tunnel', :now, :now)", { now: NOW });
  var rows = t.query("SELECT identifier, is_tunnel, is_upstream, link_local_evidence " +
    "FROM plugin_installation.interface ORDER BY identifier");
  t.equal(rows, [
    { identifier: "opt1", is_tunnel: 0, is_upstream: false, link_local_evidence: false },
    { identifier: "wg0", is_tunnel: 1, is_upstream: false, link_local_evidence: false }
  ], "the generated flag and the defaults");
});

test("this_firewall_address holds an interface's own addresses and never its gateways, and says which are current",
  function (t) {
    t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
      "first_seen_at, last_seen_at) VALUES ('wan', 'igb0', 'WAN', 'ethernet', 'physical', :then, :now)",
      { then: NOW - 7200, now: NOW });
    var id = t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'wan'")[0].id;
    t.sql("INSERT INTO plugin_installation.interface_address (interface_id, source_field, address, prefix_length, " +
      "address_family, first_seen_at, last_seen_at) VALUES " +
      "(:id, 'addr4', '203.0.113.7', 24, 4, :then, :now), " +
      "(:id, 'addr4', '203.0.113.6', 24, 4, :then, :earlier), " +
      "(:id, 'gateways', '203.0.113.1', NULL, 4, :then, :now)",
      { id: id, then: NOW - 7200, earlier: NOW - 3600, now: NOW });
    var held = t.query("SELECT address, is_current FROM plugin_installation.this_firewall_address ORDER BY address");
    t.equal(held, [{ address: "203.0.113.6", is_current: 0 }, { address: "203.0.113.7", is_current: 1 }],
      "the two addresses, the gateway absent, the released one no longer current");
  });

test("a client's randomised MAC is generated, and a client is owned only with the instant of its assignment",
  function (t) {
    t.sql("INSERT INTO plugin_installation.client (identity_kind, identity_key, mac, first_seen_at, last_seen_at) " +
      "VALUES ('mac', 'a2:00:00:00:00:01', 'a2:00:00:00:00:01', :now, :now), " +
      "('mac', 'a0:00:00:00:00:02', 'a0:00:00:00:00:02', :now, :now)", { now: NOW });
    var rows = t.query("SELECT identity_key, mac_is_randomised, unstable_identity FROM plugin_installation.client " +
      "ORDER BY identity_key");
    t.equal(rows, [{ identity_key: "a0:00:00:00:00:02", mac_is_randomised: 0, unstable_identity: 0 },
      { identity_key: "a2:00:00:00:00:01", mac_is_randomised: 1, unstable_identity: 1 }], "the generated flags");
    var refused = false;
    try {
      t.sql("UPDATE plugin_installation.client SET owner_id = 1 WHERE identity_key = 'a0:00:00:00:00:02'");
    } catch (error) { refused = true; }
    t.assert(refused, "an owner without its assignment instant is refused by the check");
  });
