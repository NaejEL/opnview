// The unbound plugin's own tests, for each of the three kinds it implements.

// The window the query report is asked for, and how far one pass walks it: the values the plugin
// states, which these tests pin.
var dnsMaxPages = 5;
var dnsRequestedSpan = 3600;

// The lookup key depends on the row and not on when it was polled. A test running every pass at one
// fixed instant could not catch a key that had quietly included the POLL time; this one separates
// the two instants that matter: the lookup's own, part of its identity, and the instant opnview
// read it, which is not.
test("the lookup key depends on the row and not on when it was polled", function (t) {
  var row = {
    client: "example-client-address",
    domain: "example-name.invalid",
    action: "block",
    source: "local-data",
    rcode: "NXDOMAIN",
    dnssec_status: "example-dnssec-verdict",
    blocklist: "example-blocklist-name",
    time: 1749990000,
    uuid: null
  };
  var lookedUpAt = 1749990000;
  var base = t.helper("lookup_key", row, lookedUpAt);
  if (!base) {
    t.fatal("the key is empty");
  }
  if (base.indexOf("content:") !== 0) {
    t.fatal("the key is " + base + ", which does not say that it was composed from the row content");
  }
  t.equal(t.helper("lookup_key", row, lookedUpAt), base, "composing the same row twice");
  ["client", "domain", "action", "source", "rcode", "dnssec_status", "blocklist"].forEach(function (field) {
    var altered = JSON.parse(JSON.stringify(row));
    altered[field] = "a-different-value";
    t.assert(t.helper("lookup_key", altered, lookedUpAt) !== base, "changing " + field + " left the key " +
      "unchanged, so two lookups differing only in that field would collapse into one row");
  });
  t.assert(t.helper("lookup_key", row, lookedUpAt + 1) !== base, "a lookup one second later has the same " +
    "key, so a repeated query would never be counted twice");
  var duplicate = JSON.parse(JSON.stringify(row));
  t.equal(t.helper("lookup_key", duplicate, lookedUpAt), base, "two rows identical in every field including " +
    "the instant (the deliberate under-count)");
});

test("the query report family is read as the querying family", function (t) {
  var families = { "ip4": 4, "ip6": 6, "unix": null, "": null, "IPv4": null, "4": null };
  Object.keys(families).forEach(function (value) {
    t.equal(t.helper("query_family", value), families[value], "the family of " + JSON.stringify(value));
  });
});

// The verdict is mapped onto the dns_lookup contract's closed vocabulary here, in the plugin that
// reads the query report, and anything the report says that the vocabulary does not name is
// `unknown`: a lookup whose verdict cannot be named is never counted as allowed.
test("the query report action is mapped onto the closed action vocabulary", function (t) {
  var actions = { "pass": "pass", "Passed": "pass", "allowed": "pass", "BLOCK": "block", "blocked": "block",
    "drop": "drop", "dropped": "drop", "": "unknown", "refused": "unknown", "redirect": "unknown" };
  Object.keys(actions).forEach(function (raw) {
    t.equal(t.helper("normalise_action", raw), actions[raw], "the action " + JSON.stringify(raw));
  });
});

test("the Unbound lookup implementation declares itself and states its window", function (t) {
  t.assert(t.registered("dns_lookup"), "the unbound implementation is not registered in the dns_lookup kind");
  t.equal(t.value("dns_lookup", "page_bound"), dnsMaxPages, "the page bound");
  t.equal(t.value("dns_lookup", "requested_span_seconds"), dnsRequestedSpan, "the requested span");
});

test("Unbound is not separable when query reporting is off", function (t) {
  t.answer("status", { fixture: "unbound_status_running.json" });
  t.answer("settings", { fixture: "unbound_settings.json" });
  t.answer("is_enabled", { fixture: "unbound_is_enabled_off.json" });
  var probe = t.call("dns_lookup", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.state === "present_but_disabled",
    "reporting off gave " + JSON.stringify(probe));
});

test("Unbound is separable when query reporting is on", function (t) {
  t.answer("status", { fixture: "unbound_status_running.json" });
  t.answer("settings", { fixture: "unbound_settings.json" });
  t.answer("is_enabled", { fixture: "unbound_is_enabled_on.json" });
  var probe = t.call("dns_lookup", "probe");
  t.assert(probe.error === null && probe.result.separable, "reporting on gave " + JSON.stringify(probe));
});

test("the query report is read into the plugin's table with content keys, page by page, until nothing is new", function (t) {
  t.clock(1750000000);
  t.answer("search_queries", { fixture: "search_queries_page1.json" });
  var read = t.call("dns_lookup", "collect", {});
  var rows = read.rows && read.rows.dns_resolution;
  if (read.error !== null || !rows || rows.length === 0 || !read.result.separable) {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  rows.forEach(function (record) {
    t.assert(record.lookup_key.indexOf("content:") === 0,
      "the key " + record.lookup_key + " was not composed from the row content");
    t.assert(["pass", "block", "drop", "unknown"].indexOf(record.action) >= 0,
      "the action " + record.action + " is outside the closed vocabulary");
    t.equal(record.resolver, "unbound", "the resolver");
    t.equal(record.ingested_at, 1750000000, "the pass's instant");
  });
  var blocked = rows.filter(function (record) { return record.action === "block"; });
  t.assert(blocked.length > 0 && blocked[0].blocklist_name !== "", "a refused lookup names its list");
  t.equal(read.gaps, [], "no gap on a first read");
  // The same page read again brings nothing new, and the walk stops at the first page.
  rows.forEach(function (record) {
    t.sql("INSERT INTO plugin_unbound.dns_resolution (lookup_key, client_address, domain, resolver, action, " +
      "blocklist_name, looked_up_at, ingested_at) VALUES (:key, :client, :domain, 'unbound', :action, :list, :at, :at)",
      { key: record.lookup_key, client: record.client_address, domain: record.domain, action: record.action,
        list: record.blocklist_name, at: record.looked_up_at });
  });
  var again = t.call("dns_lookup", "collect", {});
  t.equal((again.rows && again.rows.dns_resolution) || [], [], "nothing new");
  t.equal(t.requests("search_queries").length, 3, "two pages for the first walk, which stops at the page bringing nothing new, and one for the second");
  // The window is sent as integers in a JSON body, the one form the controller keeps.
  var sent = t.requests("search_queries");
  t.assert(typeof sent[0].json.timeStart === "number" && typeof sent[0].json.timeEnd === "number",
    "the window was not sent as JSON integers: " + JSON.stringify(sent[0]));
});

// The cache.

test("the Unbound cache implementation declares itself", function (t) {
  t.assert(t.registered("resolver_cache"), "the unbound implementation is not registered in the resolver_cache kind");
});

test("the fixture dump is read into observations, its message-cache references counted and not stored", function (t) {
  t.clock(1750000000);
  t.answer("dump_cache", { fixture: "unbound_dumpcache.json" });
  var read = t.call("resolver_cache", "collect", {});
  if (read.error !== null || !read.result.separable || read.result.state !== "reachable") {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  var rows = read.rows.resource_record_observation;
  t.assert(rows.length > 0, "the dump produced no observation");
  rows.forEach(function (row) {
    t.equal(row.held_in, "cache", "where the record is held");
    t.assert(["A", "AAAA", "CNAME"].indexOf(row.rrtype) >= 0, "a type the cache does not keep: " + row.rrtype);
    t.equal(row.owner_name, row.owner_name.toLowerCase(), "the owner name is lower-cased");
    t.assert(row.first_seen_at === 1750000000 && row.covered_until_at >= 1750000000, "the coverage of " + row.owner_name);
    t.equal(row.address === null, row.rrtype === "CNAME", "an address unless a CNAME");
  });
  t.equal(read.rows.resource_record_read, [{ provider_id: rows[0].provider_id, read_at: 1750000000 }], "the read is recorded");
  t.assert(read.result.detail.indexOf("skipped") >= 0 && read.result.detail.indexOf("message-cache") >= 0,
    "the message-cache references are counted in the detail: " + read.result.detail);
});

test("a record seen again at the next poll extends its observation; one seen after a gap starts another", function (t) {
  t.clock(1750000000);
  t.answer("dump_cache", { fixture: "unbound_dumpcache.json" });
  var first = t.call("resolver_cache", "collect", {});
  var provider = first.rows.resource_record_read[0].provider_id;
  first.rows.resource_record_observation.forEach(function (row) {
    t.sql("INSERT INTO plugin_unbound.resource_record_observation (provider_id, held_in, owner_name, rrtype, value, " +
      "address, first_seen_at, last_seen_at, covered_from_at, covered_until_at) VALUES (:p, 'cache', :owner, :type, " +
      ":value, :address, :first, :last, :from, :until)", { p: provider, owner: row.owner_name, type: row.rrtype,
      value: row.value, address: row.address, first: row.first_seen_at, last: row.last_seen_at,
      from: row.covered_from_at, until: row.covered_until_at });
  });
  t.sql("INSERT INTO plugin_unbound.resource_record_read (provider_id, read_at) VALUES (:p, 1750000000)", { p: provider });
  t.clock(1750000060);
  var second = t.call("resolver_cache", "collect", {});
  second.rows.resource_record_observation.forEach(function (row) {
    t.equal(row.first_seen_at, 1750000000, "the observation of " + row.owner_name + " is extended, not restarted");
    t.equal(row.last_seen_at, 1750000060, "its last sighting");
    t.assert(row.covered_until_at >= 1750000060, "its coverage reaches the poll");
  });
  t.assert(second.result.detail.indexOf("0 observations started, " + second.rows.resource_record_observation.length +
    " extended") >= 0, "the detail counts the extensions: " + second.result.detail);
});

test("an answer of status failed is named and stores nothing", function (t) {
  t.answer("dump_cache", { json: { status: "failed" } });
  var read = t.call("resolver_cache", "collect", {});
  t.assert(read.error !== null && Object.keys(read.rows || {}).length === 0 && read.result.outcome === "status_not_ok",
    "a failed status gave " + JSON.stringify(read));
});

// The local data.

test("the Unbound local data implementation declares itself", function (t) {
  t.assert(t.registered("resolver_local_data"),
    "the unbound implementation is not registered in the resolver_local_data kind");
});

test("the fixture local data is read into observations with their host label", function (t) {
  t.clock(1750000000);
  t.answer("list_local_data", { fixture: "unbound_listlocaldata.json" });
  var read = t.call("resolver_local_data", "collect", {});
  if (read.error !== null || !read.result.separable || read.result.state !== "reachable") {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  var rows = read.rows.resource_record_observation;
  t.assert(rows.length > 0, "the fixture local data produced no observation");
  rows.forEach(function (row) {
    t.equal(row.held_in, "local_data", "where the record is held");
    t.assert(["A", "AAAA", "PTR"].indexOf(row.rrtype) >= 0, "a type the local data does not keep: " + row.rrtype);
    t.assert(typeof row.host_label === "string" && row.host_label !== "", "the host label of " + row.owner_name);
    t.assert(typeof row.address === "string", "the address of " + row.owner_name);
    t.equal(row.covered_until_at, 1750000000, "local data covers the poll alone");
  });
  t.assert(read.result.detail.indexOf("message-cache") < 0, "the null-ttl rule applies to the cache dump alone");
});

test("the records are normalised: names lower-cased, addresses canonical, a PTR owner decoded", function (t) {
  t.equal(t.helper("canonical_address", "192.0.2.010"), null, "a leading zero is no address");
  t.equal(t.helper("canonical_address", "2001:DB8::1"), "2001:db8::1", "lower-cased");
  t.equal(t.helper("canonical_address", "fe80::1%em0"), null, "a zone is refused");
  t.equal(t.helper("reverse_name_address", "10.2.0.192.in-addr.arpa"), "192.0.2.10", "an IPv4 PTR owner");
  t.equal(t.helper("reverse_name_address", "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa"),
    "2001:db8::1", "an IPv6 PTR owner");
  var normalised = t.helper("normalise_records", [
    { owner_name: "Example.Invalid.", ttl: "30", ttl_shape: "string", rrtype: "A", value: "192.0.2.1" },
    { owner_name: "example.invalid", ttl: "30", ttl_shape: "string", rrtype: "A", value: "2001:db8::1" },
    { owner_name: "example.invalid", ttl: "x", ttl_shape: "string", rrtype: "AAAA", value: "2001:db8::1" },
    { owner_name: "example.invalid", ttl: "30", ttl_shape: "string", rrtype: "MX", value: "mail.example.invalid" }
  ], "cache");
  t.equal(normalised.records.map(function (r) { return [r.owner_name, r.rrtype, r.value, r.ttl]; }),
    [["example.invalid", "A", "192.0.2.1", 30]], "the one record kept");
  t.equal([normalised.counts.total, normalised.counts.badValue, normalised.counts.badTTL], [4, 1, 1], "the counts");
});

// The derivation resolve: the lists the refused lookups name, and the machine behind a logged host name.

function lookupRow(t, key, client, fields) {
  var row = { key: key, client: client, hostname: null, resolution: "logged_address", family: null, list: "", at: 1750000000 };
  Object.keys(fields || {}).forEach(function (name) { row[name] = fields[name]; });
  t.sql("INSERT INTO plugin_unbound.dns_resolution (lookup_key, client_address, client_hostname, client_resolution, " +
    "client_family, domain, resolver, action, blocklist_name, looked_up_at, ingested_at) VALUES (:key, :client, " +
    ":hostname, :resolution, CAST(:family AS BIGINT), 'example.invalid', 'unbound', :action, :list, :at, :at)",
    Object.assign({ action: row.list === "" ? "pass" : "block" }, row));
}

test("a refused lookup is linked to its list, created once and spanning the lookups naming it", function (t) {
  t.clock(1750000100);
  lookupRow(t, "k1", "192.0.2.10", { list: "list-one", at: 1750000000 });
  lookupRow(t, "k2", "192.0.2.10", { list: "list-one", at: 1750000050 });
  lookupRow(t, "k3", "192.0.2.10", { list: "list-two", at: 1750000020 });
  var run = t.derive("resolve");
  t.equal(run.error, null, "the run");
  t.equal(t.query("SELECT name, first_seen_at, last_seen_at FROM plugin_unbound.blocklist ORDER BY name"),
    [{ name: "list-one", first_seen_at: 1750000000, last_seen_at: 1750000050 },
      { name: "list-two", first_seen_at: 1750000020, last_seen_at: 1750000020 }], "the lists");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_unbound.dns_resolution r JOIN plugin_unbound.blocklist b " +
    "ON b.id = r.blocklist_id")[0].n, 3, "every refused lookup is linked");
});

test("a logged host name is resolved through the leases, then the local data, once", function (t) {
  t.clock(1750000100);
  t.sql("INSERT INTO plugin_leases.dhcp_lease (provider_id, backend, address, hostname, lease_state, observed_at) " +
    "VALUES (1, 'kea', '192.0.2.20', 'printer.lan', 'active', 1750000000)");
  t.sql("INSERT INTO plugin_leases.dhcp_lease (provider_id, backend, address, hostname, lease_state, observed_at) " +
    "VALUES (1, 'kea', '192.0.2.21', 'twins', 'active', 1750000000), (1, 'kea', '192.0.2.22', 'twins', 'active', 1750000000)");
  t.sql("INSERT INTO plugin_unbound.resource_record_observation (provider_id, held_in, owner_name, rrtype, value, address, " +
    "host_label, first_seen_at, last_seen_at, covered_from_at, covered_until_at) VALUES " +
    "(1, 'local_data', 'nas.lan', 'A', '192.0.2.30', '192.0.2.30', 'nas', 1749990000, 1750000000, 1749990000, 1750000100), " +
    "(1, 'local_data', 'nas.lan', 'AAAA', '2001:db8::30', '2001:db8::30', 'nas', 1749990000, 1750000000, 1749990000, 1750000100)");
  lookupRow(t, "k1", "Printer", { hostname: "Printer", resolution: "unknown_hostname" });
  lookupRow(t, "k2", "twins", { hostname: "twins", resolution: "unknown_hostname" });
  lookupRow(t, "k3", "nas.lan", { hostname: "nas.lan", resolution: "unknown_hostname", family: 6 });
  lookupRow(t, "k4", "nas", { hostname: "nas", resolution: "unknown_hostname" });
  lookupRow(t, "k5", "nobody", { hostname: "nobody", resolution: "unknown_hostname" });
  lookupRow(t, "k6", "localhost", { hostname: "localhost", resolution: "unknown_hostname" });
  var run = t.derive("resolve");
  t.equal(run.error, null, "the run");
  var rows = t.query("SELECT lookup_key, client_address, client_resolution, interface_lookup_state, hostname_examined_at " +
    "FROM plugin_unbound.dns_resolution ORDER BY lookup_key");
  t.equal(rows.map(function (r) { return [r.lookup_key, r.client_address, r.client_resolution]; }), [
    ["k1", "192.0.2.20", "lease_hostname"],
    ["k2", "twins", "ambiguous_hostname"],
    ["k3", "2001:db8::30", "local_data_hostname"],
    ["k4", "nas", "dual_stack_hostname"],
    ["k5", "nobody", "unknown_hostname"],
    ["k6", "localhost", "this_firewall_hostname"]
  ], "the resolutions");
  t.equal(rows.map(function (r) { return r.hostname_examined_at; }), [1750000100, 1750000100, 1750000100, 1750000100,
    1750000100, 1750000100], "each examined once");
  t.equal(rows[0].interface_lookup_state, "pending", "a resolved lookup is placed again");
});
