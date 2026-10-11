// The isc plugin's own tests.

test("the ISC implementation declares itself as a dhcp_lease source", function (t) {
  t.assert(t.registered("dhcp_lease"), "the isc implementation is not registered in the dhcp_lease kind");
});

test("an absent plugin is named and never separable", function (t) {
  var probe = t.call("dhcp_lease", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.outcome === "plugin_not_installed",
    "an absent plugin gave " + JSON.stringify(probe));
});

test("the ISC leases are never read and say why", function (t) {
  var read = t.call("dhcp_lease", "leases", 10);
  if (read.unsupported === null) {
    t.fatal("the read returned " + JSON.stringify(read) + ", not the unsupported state");
  }
  t.equal(read.result.outcome, "read_not_supported", "the outcome");
});
