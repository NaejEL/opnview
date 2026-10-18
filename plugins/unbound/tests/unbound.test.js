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

test("a page of the query report is read into records with content keys", function (t) {
  t.answer("search_queries", { fixture: "search_queries_page1.json" });
  var read = t.call("dns_lookup", "lookups", 1);
  if (read.error !== null || !read.lookups || read.lookups.length === 0 || !read.result.separable) {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  read.lookups.forEach(function (record) {
    t.assert(record.deduplication_key.indexOf("content:") === 0,
      "the key " + record.deduplication_key + " was not composed from the row content");
    t.assert(["pass", "block", "drop", "unknown"].indexOf(record.action) >= 0,
      "the action " + record.action + " is outside the closed vocabulary");
  });
  // The window is sent as integers in a JSON body, the one form the controller keeps.
  var sent = t.requests("search_queries");
  t.equal(sent.length, 1, "requests to the query report");
  t.assert(typeof sent[0].json.timeStart === "number" && typeof sent[0].json.timeEnd === "number",
    "the window was not sent as JSON integers: " + JSON.stringify(sent[0]));
});

// The cache.

test("the Unbound cache implementation declares itself", function (t) {
  t.assert(t.registered("resolver_cache"), "the unbound implementation is not registered in the resolver_cache kind");
});

test("the fixture dump is read and its message-cache references are counted, not stored", function (t) {
  t.answer("dump_cache", { fixture: "unbound_dumpcache.json" });
  var read = t.call("resolver_cache", "cache");
  if (read.error !== null || !read.result.separable || read.result.state !== "reachable") {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  t.assert(read.records && read.records.length > 0 && read.response_bytes > 0,
    "the dump holds " + (read.records || []).length + " records of " + read.response_bytes + " bytes");
  read.records.forEach(function (record) {
    t.assert(!(record.owner_name === "" && record.value === ""), "an empty record was handed to the kind");
  });
});

test("an answer of status failed is named and stores nothing", function (t) {
  t.answer("dump_cache", { json: { status: "failed" } });
  var read = t.call("resolver_cache", "cache");
  t.assert(read.error !== null && (read.records || []).length === 0 && read.result.outcome === "status_not_ok",
    "a failed status gave " + JSON.stringify(read));
});

// The local data.

test("the Unbound local data implementation declares itself", function (t) {
  t.assert(t.registered("resolver_local_data"),
    "the unbound implementation is not registered in the resolver_local_data kind");
});

test("the fixture local data is read", function (t) {
  t.answer("list_local_data", { fixture: "unbound_listlocaldata.json" });
  var read = t.call("resolver_local_data", "local_data");
  if (read.error !== null || !read.result.separable || read.result.state !== "reachable") {
    t.fatal("reading the fixture gave " + JSON.stringify(read));
  }
  t.assert(read.records && read.records.length > 0, "the fixture local data produced no record");
  t.assert(!read.not_records || read.not_records.length === 0, "the local data counted " +
    JSON.stringify(read.not_records) + " as not records; the null-ttl rule applies to the cache dump alone");
});
