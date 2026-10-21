// The dnsmasq plugin's own tests, for each of the four kinds it implements.

// The DHCP lease backend.

test("the Dnsmasq lease implementation declares itself", function (t) {
  t.assert(t.registered("dhcp_lease"), "the dnsmasq implementation is not registered in the dhcp_lease kind");
});

test("Dnsmasq is not separable without DHCP ranges", function (t) {
  t.answer("status", { fixture: "dnsmasq_status_running.json" });
  t.answer("settings", { fixture: "dnsmasq_settings_no_ranges.json" });
  var probe = t.call("dhcp_lease", "probe");
  t.assert(probe.error === null && !probe.result.separable, "a resolver with no ranges gave " + JSON.stringify(probe));
});

test("Dnsmasq is separable when it serves DHCP ranges", function (t) {
  t.answer("status", { fixture: "dnsmasq_status_running.json" });
  t.answer("settings", { fixture: "dnsmasq_settings.json" });
  var probe = t.call("dhcp_lease", "probe");
  t.assert(probe.error === null && probe.result.separable, "a serving backend gave " + JSON.stringify(probe));
});

test("Dnsmasq leases are read from the fixture into the leases plugin's table, with no validity start", function (t) {
  t.answer("leases", { fixture: "dnsmasq_leases.json" });
  var read = t.call("dhcp_lease", "collect", {});
  var rows = read.rows && read.rows["leases.dhcp_lease"];
  if (read.error !== null || !rows || rows.length === 0) {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  rows.forEach(function (lease) {
    t.assert(lease.starts_at === null, "a Dnsmasq lease reports the start " + lease.starts_at +
      "; only Kea reports one");
  });
});

// Dnsmasq's own fields are decoded here, in the plugin (PA11 of the plugin-API amendment): it reports
// the interface under its device as well, a reservation through is_reserved, and no lease state
// beside it, so a lease that is not a reservation is `unknown` rather than a guess.
test("Dnsmasq's lease fields are decoded into the leases table's row", function (t) {
  t.answer("leases", { fixture: "dnsmasq_leases.json" });
  var read = t.call("dhcp_lease", "collect", {});
  var rows = read.rows["leases.dhcp_lease"];
  t.equal(rows.length, 3, "the leases read");
  var first = rows[0];
  t.equal(first.address, "198.51.100.10", "the address");
  t.equal(first.mac, "0a:11:22:33:44:55", "the hardware address, as Dnsmasq spells it");
  t.equal(first.dhcp_client_id, "example-client-identifier-one", "the client identifier");
  t.equal(first.interface_identifier, "example_if_a", "the interface's configuration key");
  t.equal(first.interface_device, "exdev0", "the interface's device");
  t.equal(first.expires_at, 1750003600, "the expiry");
  t.equal(first.lease_state, "unknown", "a lease that is not a reservation");
  t.equal(rows[2].lease_state, "reserved", "a reservation");
  t.assert(rows[2].expires_at === null, "a reservation reports an expiry");
});

// The resolver: lookups.

test("the Dnsmasq resolver is registered, probed and never read", function (t) {
  t.assert(t.registered("dns_lookup"), "the dnsmasq implementation is not registered in the dns_lookup kind");
  t.equal(t.value("dns_lookup", "page_bound"), 1, "the page bound");
  t.equal(t.value("dns_lookup", "requested_span_seconds"), 0, "the requested span");
  var read = t.call("dns_lookup", "lookups", 1);
  t.assert(read.unsupported !== null, "the read returned " + JSON.stringify(read) + ", not the unsupported state");
  t.equal(read.result.outcome, "log_grammar_not_recorded", "the outcome");
});

test("a running Dnsmasq resolver is never separable whatever its logging", function (t) {
  t.answer("status", { fixture: "dnsmasq_status_running.json" });
  t.answer("settings", { fixture: "dnsmasq_settings.json" });
  var probe = t.call("dns_lookup", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.outcome === "log_grammar_not_recorded",
    "a running resolver gave " + JSON.stringify(probe));
});

// The resolver: cache.

test("the Dnsmasq cache is registered, probed and never read", function (t) {
  t.assert(t.registered("resolver_cache"), "the dnsmasq implementation is not registered in the resolver_cache kind");
  var read = t.call("resolver_cache", "cache");
  t.assert(read.unsupported !== null && read.result.outcome === "no_cache_read",
    "the read gave " + JSON.stringify(read));
});

test("a running Dnsmasq names the absence of a cache read and is never separable", function (t) {
  t.answer("status", { fixture: "dnsmasq_status_running.json" });
  var probe = t.call("resolver_cache", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.outcome === "no_cache_read",
    "a running resolver gave " + JSON.stringify(probe));
});

// The resolver: local data.

test("the Dnsmasq local data is registered, probed and never read", function (t) {
  t.assert(t.registered("resolver_local_data"),
    "the dnsmasq implementation is not registered in the resolver_local_data kind");
  var read = t.call("resolver_local_data", "local_data");
  t.assert(read.unsupported !== null && read.result.outcome === "no_local_data_read",
    "the read gave " + JSON.stringify(read));
});

test("a running Dnsmasq names the local-data absence and is never separable", function (t) {
  t.answer("status", { fixture: "dnsmasq_status_running.json" });
  var probe = t.call("resolver_local_data", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.outcome === "no_local_data_read",
    "a running resolver gave " + JSON.stringify(probe));
});
