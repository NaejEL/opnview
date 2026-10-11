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

test("Kea leases are read from the fixture and never start after they expire", function (t) {
  t.answer("leases", { fixture: "kea_leases.json" });
  var read = t.call("dhcp_lease", "leases", 100);
  if (read.error !== null || !read.leases || read.leases.length === 0 || !read.result.separable) {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  read.leases.forEach(function (lease) {
    t.assert(lease.address, "an observation carries no address");
    if (lease.starts_at !== undefined && lease.expires_at !== undefined) {
      t.assert(lease.starts_at <= lease.expires_at,
        "a lease starts (" + lease.starts_at + ") after it expires (" + lease.expires_at + ")");
    }
  });
});
