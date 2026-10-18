test("a running server is probed reachable", function (t) {
  t.answer("status", { json: { status: "running" } });
  var probe = t.call("dhcp_lease", "probe");
  t.equal(probe.result.state, "reachable", "the state");
});

test("each row is a lease", function (t) {
  t.answer("leases", { json: { total: 1, rowCount: 1, current: 1, rows: [
    { address: "192.0.2.10", hwaddr: "AA:BB:CC:00:11:22", hostname: "example-host", expire: 1790000000 }
  ] } });
  var read = t.call("dhcp_lease", "leases", 50);
  t.equal(read.leases.length, 1, "the leases read");
  t.equal(read.leases[0].mac, "aa:bb:cc:00:11:22", "the hardware address, lower-cased");
  t.equal(read.leases[0].expires_at, 1790000000, "the expiry");
  t.equal(read.leases[0].lease_state, "active", "the state");
});
