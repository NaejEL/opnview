// The leases plugin's own tests: the interface a lease was given on is named from the installation
// model after the pass, by configuration key first and by device otherwise, and the retention purge
// removes what the installation no longer keeps.
var NOW = 1790000400;

function installation(t) {
  t.sql("INSERT INTO plugin_installation.interface (identifier, device, description, link_type, link_kind, " +
    "is_upstream, first_seen_at, last_seen_at) VALUES " +
    "('lan', 'igb1', 'LAN', 'ethernet', 'physical', false, :then, :now), " +
    "('guest', 'igb1_vlan20', 'Guest', 'ethernet', 'vlan', false, :then, :now)", { then: NOW - 86400, now: NOW });
  return {
    lan: t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'lan'")[0].id,
    guest: t.query("SELECT id FROM plugin_installation.interface WHERE identifier = 'guest'")[0].id
  };
}

function lease(t, address, fields) {
  var row = { address: address, identifier: null, device: null, at: NOW - 60, expires: NOW + 3600 };
  Object.keys(fields || {}).forEach(function (key) { row[key] = fields[key]; });
  t.sql("INSERT INTO plugin_leases.dhcp_lease (provider_id, backend, address, lease_state, interface_identifier, " +
    "interface_device, expires_at, observed_at) VALUES (1, 'kea', :address, 'active', :identifier, :device, " +
    "CAST(:expires AS BIGINT), CAST(:at AS BIGINT))", row);
}

test("a lease is given the interface its server named, by configuration key first and by device otherwise",
  function (t) {
    t.clock(NOW);
    var ids = installation(t);
    lease(t, "10.1.0.10", { identifier: "lan" });
    lease(t, "10.1.0.11", { device: "igb1_vlan20" });
    lease(t, "10.1.0.12", { identifier: "lan", device: "igb1_vlan20" });
    lease(t, "10.1.0.13", {});
    var run = t.derive("resolve");
    t.equal(run.error, null, "the run");
    var rows = t.query("SELECT address, interface_id FROM plugin_leases.dhcp_lease ORDER BY address");
    t.equal(rows.map(function (row) { return row.interface_id; }), [ids.lan, ids.guest, ids.lan, null],
      "the key wins over the device, and a lease named on neither keeps none");
  });

test("a lease generation is one row: the unique key is the address, the generation and the server",
  function (t) {
    t.clock(NOW);
    installation(t);
    lease(t, "10.1.0.10", { expires: NOW + 3600 });
    lease(t, "10.1.0.10", { expires: NOW + 7200 });
    var rows = t.query("SELECT generation_key FROM plugin_leases.dhcp_lease ORDER BY generation_key");
    t.equal(rows.map(function (row) { return row.generation_key; }), ["expiry:" + (NOW + 3600), "expiry:" + (NOW + 7200)],
      "two generations of one address");
    t.equal(t.query("SELECT hostname_label FROM plugin_leases.dhcp_lease LIMIT 1")[0].hostname_label, null,
      "no host name, no label");
  });

test("the retention purge removes the leases observed longer ago than the installation keeps", function (t) {
  t.clock(NOW);
  installation(t);
  lease(t, "10.1.0.10", { at: NOW - 100 * 86400 });
  lease(t, "10.1.0.11", { at: NOW - 60 });
  var run = t.derive("resolve");
  t.equal(run.error, null, "the run");
  t.equal(t.query("SELECT address FROM plugin_leases.dhcp_lease").map(function (row) { return row.address; }),
    ["10.1.0.11"], "the old lease is gone, at the default retention of ninety days");
});
