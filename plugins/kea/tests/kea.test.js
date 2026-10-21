// The kea plugin's own tests: its probe and its reading of its own recorded answers, through the
// plugin loader and the same adapter the collector runs it through.

test("the Kea implementation declares itself as a dhcp_lease source", function (t) {
  t.assert(t.registered("dhcp_lease"), "the kea implementation is not registered in the dhcp_lease kind");
});

test("Kea is separable only when the daemon runs and DHCPv4 is configured", function (t) {
  t.answer("status", { fixture: "kea_status_disabled.json" });
  var disabled = t.call("dhcp_lease", "probe");
  t.assert(disabled.error === null && !disabled.result.separable,
    "a disabled daemon gave " + JSON.stringify(disabled));
});

test("Kea is separable when the daemon runs and DHCPv4 is configured", function (t) {
  t.answer("status", { fixture: "kea_status_running.json" });
  t.answer("dhcpv4", { fixture: "kea_dhcpv4_get.json" });
  var serving = t.call("dhcp_lease", "probe");
  t.assert(serving.error === null && serving.result.separable && serving.result.state === "reachable",
    "a serving daemon gave " + JSON.stringify(serving));
});

test("Kea leases are read from the fixture into the leases plugin's table, and never start after they expire",
  function (t) {
    t.answer("leases", { fixture: "kea_leases.json" });
    var read = t.call("dhcp_lease", "collect", {});
    var rows = read.rows && read.rows["leases.dhcp_lease"];
    if (read.error !== null || !rows || rows.length === 0 || !read.result.separable) {
      t.fatal("reading the fixture gave " + JSON.stringify(read));
    }
    rows.forEach(function (lease) {
      t.assert(lease.address, "an observation carries no address");
      if (lease.starts_at !== null && lease.expires_at !== null) {
        t.assert(lease.starts_at <= lease.expires_at,
          "a lease starts (" + lease.starts_at + ") after it expires (" + lease.expires_at + ")");
      }
    });
  });

// Kea's own fields are decoded here, in the plugin, rather than by a helper of the host that decoded
// every backend's union (PA11 of the plugin-API amendment): each field of the fixture's first row lands
// in its column of plugin_leases.dhcp_lease, the start is the expiry less the valid lifetime, and the
// states map onto the closed vocabulary. The row names the provider it was handed and the pass's instant.
test("Kea's lease fields are decoded into the leases table's row", function (t) {
  t.answer("leases", { fixture: "kea_leases.json" });
  var read = t.call("dhcp_lease", "collect", {});
  var rows = read.rows["leases.dhcp_lease"];
  t.equal(rows.length, 2, "the leases read");
  var first = rows[0];
  t.equal(first.address, "198.51.100.13", "the address");
  t.equal(first.mac, "0a:11:22:33:44:88", "the hardware address, as Kea spells it");
  t.equal(first.hostname, "example-host-four", "the host name");
  t.equal(first.dhcp_client_id, "example-client-identifier-four", "the client identifier");
  t.equal(first.duid, "example-duid", "the DUID");
  t.equal(first.iaid, "1", "the IAID");
  t.equal(first.vendor_hint, "example vendor string", "the vendor string");
  t.equal(first.interface_identifier, "example_if_a", "the interface's configuration key");
  t.equal(first.expires_at, 1750003600, "the expiry");
  t.equal(first.starts_at, 1750000000, "the start, the expiry less the valid lifetime");
  t.equal(first.lease_state, "active", "the state of an active lease");
  t.equal(first.backend, "kea", "the provider key, as the backend");
  t.equal(typeof first.provider_id, "number", "the provider's registry id");
  t.equal(first.observed_at, t.now(), "the pass's instant");
  t.equal(rows[1].lease_state, "expired", "the state of an expired lease");
});
